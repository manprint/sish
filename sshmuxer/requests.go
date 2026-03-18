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
			return false
		}

		if holder.OriginalAddr == check.Addr && holder.OriginalPort == check.Rport {
			closed = true

			err := holder.Close()
			if err != nil {
				log.Println("Error closing listener:", err)
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
	case <-time.After(1 * time.Second):
		break
	}

	cleanupOnce := &sync.Once{}
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
		if !sshConn.ConnectionIDProvided || strings.TrimSpace(sshConn.ConnectionID) == "" {
			sshConn.SendMessage("Id is enforced server side.", true)
			err = newRequest.Reply(false, nil)
			if err != nil {
				log.Println("Error replying to socket request:", err)
			}
			time.Sleep(500 * time.Millisecond)
			sshConn.CleanUp(state)
			return
		}

		if !utils.IsIDCensed(sshConn.ConnectionID) {
			sshConn.SendMessage("Forwarded id is not censed.", true)
			err = newRequest.Reply(false, nil)
			if err != nil {
				log.Println("Error replying to socket request:", err)
			}
			time.Sleep(500 * time.Millisecond)
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

	tcpAliasForced := viper.GetBool("tcp-aliases") && sshConn.TCPAlias
	sniProxyForced := viper.GetBool("sni-proxy") && sshConn.SNIProxy
	forceConnectEnabled := viper.GetBool("enable-force-connect")
	forceConnectRequested := forceConnectEnabled && sshConn.ForceConnect

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
			utils.WriteForwardersLogLine(utils.BuildHTTPForwardersLogKey(sshConn.ConnectionID, check.Addr), "Forward denied: "+reason)
		case utils.TCPListener:
			utils.WriteForwardersLogLine(utils.BuildTCPForwardersLogKey(sshConn.ConnectionID, int(bindPort)), "Forward denied: "+reason)
		case utils.AliasListener:
			utils.WriteForwardersLogLine(utils.BuildAliasForwardersLogKey(sshConn.ConnectionID, check.Addr, bindPort), "Forward denied: "+reason)
		}

		sshConn.SendMessage(reason, true)
		err = newRequest.Reply(false, nil)
		if err != nil {
			log.Println("Error replying to socket request:", err)
		}
		// Give the client a brief window to receive the denial reason before closing.
		time.Sleep(500 * time.Millisecond)
		sshConn.CleanUp(state)
		return
	}

	forcedDisconnected := 0
	if sshConn.ForceConnect && !forceConnectEnabled {
		sshConn.SendMessage("Force connect requested, but server-side enable-force-connect is disabled. Continuing with standard allocation.", true)
	}

	if forceConnectRequested {
		forcedDisconnected = forceDisconnectTargetConnections(listenerType, originalCheck, sshConn, state)
		waitForTargetRelease(listenerType, originalCheck, sshConn, state)
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

	deferHandler := func() {}

	cleanupChanListener := func() {
		err := listenerHolder.Close()
		if err != nil {
			log.Println("Error closing listener:", err)
		}

		state.Listeners.Delete(listenAddr)
		sshConn.Listeners.Delete(listenAddr)

		err = os.Remove(listenAddr)
		if err != nil {
			log.Println("Error removing unix socket:", err)
		}

		deferHandler()
	}

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

	switch listenerType {
	case utils.HTTPListener:
		pH, serverURL, requestMessages, err := handleHTTPListener(check, stringPort, mainRequestMessages, listenerHolder, state, sshConn, connType)
		if err != nil {
			log.Println("Error setting up HTTPListener:", err)

			err = newRequest.Reply(false, nil)
			if err != nil {
				log.Println("Error replying to socket request:", err)
			}

			cleanupOnce.Do(cleanupChanListener)

			return
		}

		mainRequestMessages = requestMessages

		deferHandler = func() {
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
		}
	case utils.AliasListener:
		aH, serverURL, validAlias, requestMessages, err := handleAliasListener(check, stringPort, mainRequestMessages, listenerHolder, state, sshConn)
		if err != nil {
			log.Println("Error setting up AliasListener:", err)

			err = newRequest.Reply(false, nil)
			if err != nil {
				log.Println("Error replying to socket request:", err)
			}

			cleanupOnce.Do(cleanupChanListener)

			return
		}

		mainRequestMessages = requestMessages

		deferHandler = func() {
			err := aH.Balancer.RemoveServer(serverURL)
			if err != nil {
				log.Println("Unable to remove server from balancer:", err)
			}

			aH.SSHConnections.Delete(listenerHolder.Addr().String())

			if len(aH.Balancer.Servers()) == 0 {
				state.AliasListeners.Delete(validAlias)
			}
		}
	case utils.TCPListener:
		tH, balancer, balancerName, serverURL, tcpAddr, requestMessages, err := handleTCPListener(check, bindPort, mainRequestMessages, listenerHolder, state, sshConn, sniProxyForced)
		if err != nil {
			log.Println("Error setting up TCPListener:", err)

			err = newRequest.Reply(false, nil)
			if err != nil {
				log.Println("Error replying to socket request:", err)
			}

			cleanupOnce.Do(cleanupChanListener)

			return
		}

		portChannelForwardReplyPayload.Rport = uint32(tH.Listener.Addr().(*multilistener.MultiListener).Addresses()[0].(*net.TCPAddr).Port)

		mainRequestMessages = requestMessages

		if !tH.NoHandle {
			go tH.Handle(state)
		}

		deferHandler = func() {
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
		}
	}

	go func() {
		<-sshConn.Close
		cleanupOnce.Do(cleanupChanListener)
	}()

	if check.Rport != 0 {
		portChannelForwardReplyPayload.Rport = check.Rport
	}

	err = newRequest.Reply(true, ssh.Marshal(portChannelForwardReplyPayload))
	if err != nil {
		log.Println("Error replying to port forwarding request:", err)
		cleanupOnce.Do(cleanupChanListener)
		return
	}

	sshConn.SendMessage(mainRequestMessages, true)

	go func() {
		defer cleanupOnce.Do(cleanupChanListener)
		for {
			cl, err := listenerHolder.Accept()
			if err != nil {
				break
			}

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
				utils.CopyBothWithBandwidthProfileGetter(cl, newChan, sshConn.GetBandwidthProfile)
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
		conn.CleanUp(state)
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
