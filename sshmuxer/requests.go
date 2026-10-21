package sshmuxer

import (
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antoniomika/multilistener"
	"github.com/antoniomika/sish/utils"
	"github.com/logrusorgru/aurora"
	"github.com/spf13/viper"
	"github.com/vulcand/oxy/roundrobin"
	"golang.org/x/crypto/ssh"
)

var (
	// forceConnectTargetLocksMu guards the registry map. Serializing the
	// refCount increment and the delete decision under one mutex closes the
	// classic sync.Map ref-count race where a delete could drop an entry while
	// another goroutine was about to reuse it, yielding two live locks (and
	// thus two concurrent critical sections) for the same target (B6).
	forceConnectTargetLocksMu sync.Mutex
	forceConnectTargetLocks   = map[string]*forceConnectTargetLock{}
)

type forceConnectTargetLock struct {
	mu       sync.Mutex
	refCount int
}

func forceConnectTargetKey(listenerType utils.ListenerType, addr string, port uint32) string {
	return fmt.Sprintf("%d|%s|%d", listenerType, strings.ToLower(strings.TrimSpace(addr)), port)
}

// acquireForceConnectTargetLock serializes force-connect takeovers for a single
// target and returns a release function. The returned release must be called
// exactly once (it is idempotent). Callers hold the lock across the whole
// disconnect → wait → re-register sequence so a takeover is atomic per target.
func acquireForceConnectTargetLock(listenerType utils.ListenerType, addr string, port uint32) func() {
	key := forceConnectTargetKey(listenerType, addr, port)

	forceConnectTargetLocksMu.Lock()
	lock, ok := forceConnectTargetLocks[key]
	if !ok {
		lock = &forceConnectTargetLock{}
		forceConnectTargetLocks[key] = lock
	}
	lock.refCount++
	forceConnectTargetLocksMu.Unlock()

	lock.mu.Lock()

	var once sync.Once
	return func() {
		once.Do(func() {
			lock.mu.Unlock()

			forceConnectTargetLocksMu.Lock()
			lock.refCount--
			if lock.refCount == 0 {
				delete(forceConnectTargetLocks, key)
			}
			forceConnectTargetLocksMu.Unlock()
		})
	}
}

func withForceConnectTargetLock(listenerType utils.ListenerType, addr string, port uint32, fn func()) {
	release := acquireForceConnectTargetLock(listenerType, addr, port)
	defer release()
	fn()
}

// channelForwardMsg is the message sent by SSH
// to init a forwarded connection.
type channelForwardMsg struct {
	Addr  string
	Rport uint32
}

// channelForwardReply defines the reply to inform the client what port was
// actually assigned https://tools.ietf.org/html/rfc4254#section-7.1
type channelForwardReply struct {
	Rport uint32
}

// forwardedTCPPayload is the payload sent by SSH
// to init a forwarded connection.
type forwardedTCPPayload struct {
	Addr       string
	Port       uint32
	OriginAddr string
	OriginPort uint32
}

// handleCancelRemoteForward will handle a remote forward cancellation
// request and remove the relevant listeners.
func handleCancelRemoteForward(newRequest *ssh.Request, sshConn *utils.SSHConnection, _ *utils.State) {
	check := &channelForwardMsg{}

	err := ssh.Unmarshal(newRequest.Payload, check)
	if err != nil {
		log.Println("Error unmarshaling remote forward payload:", err)
		err = newRequest.Reply(false, nil)
		if err != nil {
			log.Println("Error replying to request:", err)
		}
		return
	}

	closed := false

	sshConn.Listeners.Range(func(remoteAddr string, listener net.Listener) bool {
		holder, ok := listener.(*utils.ListenerHolder)
		if !ok {
			// Skip entries that are not forward holders; returning false here
			// aborted the whole scan and left the tunnel open (B10).
			return true
		}

		if holder.OriginalAddr == check.Addr && holder.OriginalPort == check.Rport {
			closed = true
			if !sshConn.RunForwardCleanup(remoteAddr) {
				err := holder.Close()
				if err != nil {
					log.Println("Error closing listener:", err)
				}
			}

			return false
		}

		return true
	})

	if !closed {
		log.Println("Unable to close tunnel")

		err = newRequest.Reply(false, nil)
		if err != nil {
			log.Println("Error replying to request:", err)
		}

		return
	}

	err = newRequest.Reply(true, nil)
	if err != nil {
		log.Println("Error replying to request:", err)
	}
}

// handleRemoteForward will handle a remote forward request
// and stand up the relevant listeners.
func handleRemoteForward(newRequest *ssh.Request, sshConn *utils.SSHConnection, state *utils.State) {
	select {
	case <-sshConn.Exec:
	case <-sshConn.Close:
	case <-time.After(1 * time.Second):
	}

	cleanupOnce := &sync.Once{}

	// If the connection was torn down while we waited above (for example, the
	// client dropped right after requesting the forward), don't stand up any
	// forwarding for it: doing so would reserve the address for a dead session.
	select {
	case <-sshConn.Close:
		if err := newRequest.Reply(false, nil); err != nil {
			log.Println("Error replying to socket request:", err)
		}
		return
	default:
	}

	check := &channelForwardMsg{}

	err := ssh.Unmarshal(newRequest.Payload, check)
	if err != nil {
		log.Println("Error unmarshaling remote forward payload:", err)

		err = newRequest.Reply(false, nil)
		if err != nil {
			log.Println("Error replying to socket request:", err)
		}
		return
	}

	if utils.IsStrictIDCensedEnabled() {
		if !sshConn.IsConnectionIDProvided() || strings.TrimSpace(sshConn.GetConnectionID()) == "" {
			sshConn.SendMessage("Id is enforced server side.", true)
			err = newRequest.Reply(false, nil)
			if err != nil {
				log.Println("Error replying to socket request:", err)
			}
			time.Sleep(500 * time.Millisecond)
			sshConn.SetCloseInfo("server", "ID not provided (strict census)")
			sshConn.CleanUp(state)
			return
		}

		if !utils.IsIDCensed(sshConn.GetConnectionID()) {
			sshConn.SendMessage("Forwarded id is not censed.", true)
			err = newRequest.Reply(false, nil)
			if err != nil {
				log.Println("Error replying to socket request:", err)
			}
			time.Sleep(500 * time.Millisecond)
			sshConn.SetCloseInfo("server", "ID not censed")
			sshConn.CleanUp(state)
			return
		}
	}

	if viper.GetBool("strict-unique-ip") && sshConn.IsConnectionIDProvided() && strings.TrimSpace(sshConn.GetConnectionID()) != "" {
		var duplicateFound bool
		state.SSHConnections.Range(func(_ string, existing *utils.SSHConnection) bool {
			if existing == nil || existing == sshConn {
				return true
			}
			if existing.IsConnectionIDProvided() && existing.GetConnectionID() == sshConn.GetConnectionID() && existing.ListenerCount() > 0 {
				duplicateFound = true
				return false
			}
			return true
		})

		if duplicateFound {
			reason := fmt.Sprintf("Connection ID '%s' is already in use.", sshConn.GetConnectionID())
			sshConn.SendMessage(reason, true)
			err = newRequest.Reply(false, nil)
			if err != nil {
				log.Println("Error replying to socket request:", err)
			}
			time.Sleep(500 * time.Millisecond)
			sshConn.SetCloseInfo("server", "connection ID already in use")
			sshConn.CleanUp(state)
			return
		}
	}

	originalCheck := &channelForwardMsg{
		Addr:  check.Addr,
		Rport: check.Rport,
	}

	originalAddress := check.Addr
	check.Addr = strings.ToLower(check.Addr)

	bindPort := check.Rport
	stringPort := strconv.FormatUint(uint64(bindPort), 10)

	listenerType := utils.HTTPListener

	comparePortHTTP := viper.GetUint32("http-port-override")
	comparePortHTTPS := viper.GetUint32("https-port-override")

	httpRequestPortOverride := viper.GetUint32("http-request-port-override")
	httpsRequestPortOverride := viper.GetUint32("https-request-port-override")

	if httpRequestPortOverride != 0 {
		comparePortHTTP = httpRequestPortOverride
	}

	if httpsRequestPortOverride != 0 {
		comparePortHTTPS = httpsRequestPortOverride
	}

	if comparePortHTTP == 0 {
		comparePortHTTP = 80
	}

	if comparePortHTTPS == 0 {
		comparePortHTTPS = 443
	}

	tcpAliasForced := viper.GetBool("tcp-aliases") && sshConn.GetTCPAlias()
	sniProxyForced := viper.GetBool("sni-proxy") && sshConn.GetSNIProxy()
	forceConnectEnabled := viper.GetBool("enable-force-connect")
	forceConnectRequested := forceConnectEnabled && sshConn.GetForceConnect()

	if tcpAliasForced {
		listenerType = utils.AliasListener
	} else if sniProxyForced {
		listenerType = utils.TCPListener
	} else if bindPort != comparePortHTTP && bindPort != comparePortHTTPS {
		testAddr := net.ParseIP(check.Addr)
		if check.Addr != "localhost" && testAddr == nil {
			listenerType = utils.AliasListener
		} else if check.Addr == "localhost" || testAddr != nil {
			listenerType = utils.TCPListener
		}
	}

	if allowed, reason := utils.IsAuthUserForwardAllowed(sshConn.SSHConn.User(), listenerType, check.Addr, bindPort); !allowed {
		switch listenerType {
		case utils.HTTPListener:
			utils.WriteForwardersLogLine(utils.BuildHTTPForwardersLogKey(sshConn.GetConnectionID(), check.Addr), "Forward denied: "+reason)
		case utils.TCPListener:
			utils.WriteForwardersLogLine(utils.BuildTCPForwardersLogKey(sshConn.GetConnectionID(), int(bindPort)), "Forward denied: "+reason)
		case utils.AliasListener:
			utils.WriteForwardersLogLine(utils.BuildAliasForwardersLogKey(sshConn.GetConnectionID(), check.Addr, bindPort), "Forward denied: "+reason)
		}

		sshConn.SendMessage(reason, true)
		err = newRequest.Reply(false, nil)
		if err != nil {
			log.Println("Error replying to socket request:", err)
		}
		// Give the client a brief window to receive the denial reason before closing.
		time.Sleep(500 * time.Millisecond)
		sshConn.SetCloseInfo("server", "forward denied: "+reason)
		sshConn.CleanUp(state)
		return
	}

	forcedDisconnected := 0
	if sshConn.GetForceConnect() && !forceConnectEnabled {
		sshConn.SendMessage("Force connect requested, but server-side enable-force-connect is disabled. Continuing with standard allocation.", true)
	}

	if forceConnectRequested {
		// Hold the per-target lock across the entire takeover: disconnect the
		// existing holders, wait for the target to free up, AND register this
		// forward's backend (the listenerType switch below). Releasing after
		// only the disconnect+wait left a window where a second concurrent
		// force-connect for the same target could also pass waitForTargetRelease
		// and both register as backends (B6). defer releases at function return,
		// which is just after the accept goroutine is spawned.
		release := acquireForceConnectTargetLock(listenerType, originalCheck.Addr, originalCheck.Rport)
		defer release()

		forcedDisconnected = forceDisconnectTargetConnections(listenerType, originalCheck, sshConn, state)
		waitForTargetRelease(listenerType, originalCheck, sshConn, state)
		state.IncrementForceConnectTakeovers(forcedDisconnected)
	}

	tmpfile, err := os.CreateTemp("", strings.ReplaceAll(sshConn.SSHConn.RemoteAddr().String()+":"+stringPort, ":", "_"))
	if err != nil {
		log.Println("Error creating temporary file:", err)

		err = newRequest.Reply(false, nil)
		if err != nil {
			log.Println("Error replying to socket request:", err)
		}
		return
	}

	err = tmpfile.Close()
	if err != nil {
		log.Println("Error closing temporary file:", err)
	}

	err = os.Remove(tmpfile.Name())
	if err != nil {
		log.Println("Error removing temporary file:", err)
	}

	listenAddr := tmpfile.Name()

	chanListener, err := net.Listen("unix", listenAddr)
	if err != nil {
		log.Println("Error listening on unix socket:", err)

		err = newRequest.Reply(false, nil)
		if err != nil {
			log.Println("Error replying to socket request:", err)
		}
		return
	}

	listenerHolder := &utils.ListenerHolder{
		ListenAddr:   listenAddr,
		Listener:     chanListener,
		Type:         listenerType,
		SSHConn:      sshConn,
		OriginalAddr: originalCheck.Addr,
		OriginalPort: originalCheck.Rport,
	}

	state.Listeners.Store(listenAddr, listenerHolder)
	sshConn.Listeners.Store(listenAddr, listenerHolder)
	state.IncrementForwardCreate()

	lifecycle := newForwardLifecycle(state, sshConn, listenAddr, listenerHolder, cleanupOnce)

	connType := "tcp"
	if sniProxyForced {
		connType = "tls"
	} else if !tcpAliasForced && stringPort == strconv.FormatUint(uint64(comparePortHTTP), 10) {
		connType = "http"
	} else if !tcpAliasForced && stringPort == strconv.FormatUint(uint64(comparePortHTTPS), 10) {
		connType = "https"
	}

	portChannelForwardReplyPayload := channelForwardReply{bindPort}

	forcedPrefix := ""
	if forceConnectRequested {
		forcedPrefix = "(forced) "
	}

	mainRequestMessages := fmt.Sprintf("Starting SSH Forwarding service %sfor %s. Forwarded connections can be accessed via the following methods:\r\n", forcedPrefix, aurora.Sprintf(aurora.Green("%s:%s"), connType, stringPort))

	if forceConnectRequested {
		mainRequestMessages += fmt.Sprintf("Forced takeover enabled. Disconnected %d existing connection(s) for target %s:%d\r\n", forcedDisconnected, originalCheck.Addr, originalCheck.Rport)
	}

	// Allocating a target and registering the holder for it must be atomic;
	// see utils.LockBindAllocation (B20). The lock is released as soon as the
	// listener-specific handler has registered its holder, well before any
	// blocking work (SendMessage, the accept loop) below.
	releaseBind := utils.LockBindAllocation()

	switch listenerType {
	case utils.HTTPListener:
		pH, serverURL, requestMessages, err := handleHTTPListener(check, stringPort, mainRequestMessages, listenerHolder, state, sshConn, connType)
		if err != nil {
			log.Println("Error setting up HTTPListener:", err)

			err = newRequest.Reply(false, nil)
			if err != nil {
				log.Println("Error replying to socket request:", err)
			}

			releaseBind()
			cleanupOnce.Do(lifecycle.cleanup)

			return
		}

		mainRequestMessages = requestMessages

		lifecycle.setForwarding(strings.ToUpper(connType), pH.HTTPUrl.String())

		lifecycle.setOnCleanup(func() {
			// B39: hold the bind-allocation lock across this teardown. These
			// mutations (RemoveServer, the empty-check, the state-map delete and
			// RemoveRoute) race with a concurrent forward reusing the same holder
			// between the empty-check and the delete, which would leave the
			// reused forward dirty. The allocation side holds the same lock.
			releaseBind := utils.LockBindAllocation()
			defer releaseBind()

			err := pH.Balancer.RemoveServer(serverURL)
			if err != nil {
				log.Println("Unable to remove server from balancer:", err)
			}

			pH.SSHConnections.Delete(listenerHolder.Addr().String())

			if len(pH.Balancer.Servers()) == 0 {
				state.HTTPListeners.Delete(pH.HTTPUrl.String())

				if viper.GetBool("admin-console") || viper.GetBool("service-console") {
					state.Console.RemoveRoute(pH.HTTPUrl.String())
				}
			}
		})
	case utils.AliasListener:
		aH, serverURL, validAlias, requestMessages, err := handleAliasListener(check, stringPort, mainRequestMessages, listenerHolder, state, sshConn)
		if err != nil {
			log.Println("Error setting up AliasListener:", err)

			err = newRequest.Reply(false, nil)
			if err != nil {
				log.Println("Error replying to socket request:", err)
			}

			releaseBind()
			cleanupOnce.Do(lifecycle.cleanup)

			return
		}

		mainRequestMessages = requestMessages

		lifecycle.setForwarding("TCP Alias", validAlias)

		lifecycle.setOnCleanup(func() {
			// B39: serialise holder teardown against bind allocation (see the
			// HTTP case above).
			releaseBind := utils.LockBindAllocation()
			defer releaseBind()

			err := aH.Balancer.RemoveServer(serverURL)
			if err != nil {
				log.Println("Unable to remove server from balancer:", err)
			}

			aH.SSHConnections.Delete(listenerHolder.Addr().String())

			if len(aH.Balancer.Servers()) == 0 {
				state.AliasListeners.Delete(validAlias)
			}
		})
	case utils.TCPListener:
		tH, balancer, balancerName, serverURL, tcpAddr, requestMessages, err := handleTCPListener(check, bindPort, mainRequestMessages, listenerHolder, state, sshConn, sniProxyForced)
		if err != nil {
			log.Println("Error setting up TCPListener:", err)

			err = newRequest.Reply(false, nil)
			if err != nil {
				log.Println("Error replying to socket request:", err)
			}

			releaseBind()
			cleanupOnce.Do(lifecycle.cleanup)

			return
		}

		portChannelForwardReplyPayload.Rport = uint32(tH.Listener.Addr().(*multilistener.MultiListener).Addresses()[0].(*net.TCPAddr).Port)

		mainRequestMessages = requestMessages

		lifecycle.setForwarding(strings.ToUpper(connType), tcpAddr)

		if !tH.NoHandle {
			go tH.Handle(state)
		}

		lifecycle.setOnCleanup(func() {
			// B39: serialise holder teardown against bind allocation (see the
			// HTTP case above).
			releaseBind := utils.LockBindAllocation()
			defer releaseBind()

			err := balancer.RemoveServer(serverURL)
			if err != nil {
				log.Println("Unable to remove server from balancer:", err)
			}

			tH.SSHConnections.Delete(listenerHolder.Addr().String())

			if len(balancer.Servers()) == 0 {
				tH.Balancers.Delete(balancerName)

				balancers := 0
				tH.Balancers.Range(func(n string, b *roundrobin.RoundRobin) bool {
					balancers += 1
					return false
				})

				if balancers == 0 {
					err := tH.Listener.Close()
					if err != nil {
						log.Println("Error closing TCPListener:", err)
					}

					state.Listeners.Delete(tcpAddr)
					state.TCPListeners.Delete(tcpAddr)
				}
			}
		})
	}

	releaseBind()

	// Only now that the listener specific cleanup has been assigned by the
	// switch above do we register the forward and start watching for connection
	// teardown. Wiring these up earlier races with that assignment and can run
	// cleanup against the no-op handler, leaving the route registered. If the
	// connection already closed during setup, the watcher fires immediately and
	// tears everything back down.
	lifecycle.register()

	// Watch for connection teardown. Selecting on the lifecycle's done channel
	// too means this goroutine exits as soon as the forward is cleaned up (for
	// example by cancel-tcpip-forward); previously it stayed parked on
	// sshConn.Close for the whole session, so a client cycling forwards on one
	// long-lived connection accumulated goroutines indefinitely (B26).
	go func() {
		select {
		case <-sshConn.Close:
			cleanupOnce.Do(lifecycle.cleanup)
		case <-lifecycle.Done():
		}
	}()

	if check.Rport != 0 {
		portChannelForwardReplyPayload.Rport = check.Rport
	}

	err = newRequest.Reply(true, ssh.Marshal(portChannelForwardReplyPayload))
	if err != nil {
		log.Println("Error replying to port forwarding request:", err)
		cleanupOnce.Do(lifecycle.cleanup)
		return
	}

	sshConn.SendMessage(mainRequestMessages, true)

	go func() {
		defer cleanupOnce.Do(lifecycle.cleanup)
		var backoff utils.AcceptBackoff
		for {
			cl, err := listenerHolder.Accept()
			if err != nil {
				// B57: a closed listener means this forward is being torn down —
				// stop and let the deferred cleanup run. A transient error
				// (EMFILE) must not kill an otherwise healthy forward, so back
				// off and retry; teardown still happens when the SSH connection
				// closes and closes this listener.
				if utils.IsListenerClosedErr(err) {
					break
				}
				d := backoff.Pause()
				log.Printf("Transient accept error on forward %s, retrying in %s: %v", listenAddr, d, err)
				time.Sleep(d)
				continue
			}
			backoff.Reset()

			go func() {
				resp := &forwardedTCPPayload{
					Addr:       originalAddress,
					Port:       portChannelForwardReplyPayload.Rport,
					OriginAddr: originalAddress,
					OriginPort: portChannelForwardReplyPayload.Rport,
				}

				newChan, newReqs, err := sshConn.SSHConn.OpenChannel("forwarded-tcpip", ssh.Marshal(resp))
				if err != nil {
					sshConn.SendMessage(err.Error(), true)

					err := cl.Close()
					if err != nil {
						log.Println("Error closing client connection:", err)
					}
					return
				}

				go ssh.DiscardRequests(newReqs)
				utils.CopyBothWithBandwidthProfileGetter(cl, newChan, sshConn.GetBandwidthProfileFast)
			}()
		}
	}()
}

func forceDisconnectTargetConnections(listenerType utils.ListenerType, target *channelForwardMsg, currentConn *utils.SSHConnection, state *utils.State) int {
	targetAddr := strings.ToLower(strings.TrimSpace(target.Addr))
	connections := map[string]*utils.SSHConnection{}

	state.Listeners.Range(func(name string, listener net.Listener) bool {
		holder, ok := listener.(*utils.ListenerHolder)
		if !ok {
			return true
		}

		if holder.Type != listenerType {
			return true
		}

		holderAddr := strings.ToLower(strings.TrimSpace(holder.OriginalAddr))
		if holderAddr != targetAddr || holder.OriginalPort != target.Rport {
			return true
		}

		if holder.SSHConn == nil || holder.SSHConn == currentConn || holder.SSHConn.SSHConn == nil {
			return true
		}

		connections[holder.SSHConn.SSHConn.RemoteAddr().String()] = holder.SSHConn
		return true
	})

	for _, conn := range connections {
		conn.SetCloseInfo("server", "force-connect takeover")
		conn.CleanUp(state)
	}

	if len(connections) == 0 && state != nil {
		state.IncrementDebugForceDisconnectNoop()
	}

	return len(connections)
}

func waitForTargetRelease(listenerType utils.ListenerType, target *channelForwardMsg, currentConn *utils.SSHConnection, state *utils.State) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !targetInUse(listenerType, target, currentConn, state) {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}
	if state != nil {
		state.IncrementDebugTargetReleaseTimeout()
	}
}

func targetInUse(listenerType utils.ListenerType, target *channelForwardMsg, currentConn *utils.SSHConnection, state *utils.State) bool {
	targetAddr := strings.ToLower(strings.TrimSpace(target.Addr))
	inUse := false

	state.Listeners.Range(func(name string, listener net.Listener) bool {
		holder, ok := listener.(*utils.ListenerHolder)
		if !ok {
			return true
		}

		if holder.Type != listenerType {
			return true
		}

		holderAddr := strings.ToLower(strings.TrimSpace(holder.OriginalAddr))
		if holderAddr == targetAddr && holder.OriginalPort == target.Rport {
			if holder.SSHConn != nil && holder.SSHConn != currentConn {
				inUse = true
				return false
			}
		}

		return true
	})

	return inUse
}
