// Package sshmuxer handles the underlying SSH server
// and multiplexing forwarding sessions.
package sshmuxer

import (
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/antoniomika/sish/httpmuxer"
	"github.com/antoniomika/sish/utils"
	"github.com/antoniomika/syncmap"
	"github.com/pires/go-proxyproto"
	"github.com/spf13/viper"
	"golang.org/x/crypto/ssh"
)

// messageQueueSize buffers console messages queued for a client. A buffer lets
// advisory notices be delivered with a non-blocking send (TrySendMessage) from
// paths that must not block, such as the bind-allocation critical section.
const messageQueueSize = 64

// Start initializes the ssh muxer service. It will start necessary components
// and begin listening for SSH connections.
func Start() {
	var (
		httpPort  int
		httpsPort int
		sshPort   int
	)

	_, httpPortString, err := utils.ParseAddress(viper.GetString("http-address"))
	if err != nil {
		log.Fatalln("Error parsing address:", err)
	}

	_, httpsPortString, err := utils.ParseAddress(viper.GetString("https-address"))
	if err != nil {
		log.Fatalln("Error parsing address:", err)
	}

	_, sshPortString, err := utils.ParseAddress(viper.GetString("ssh-address"))
	if err != nil {
		log.Fatalln("Error parsing address:", err)
	}

	httpPort, err = strconv.Atoi(httpPortString)
	if err != nil {
		log.Fatalln("Error parsing address:", err)
	}

	httpsPort, err = strconv.Atoi(httpsPortString)
	if err != nil {
		log.Fatalln("Error parsing address:", err)
	}

	sshPort, err = strconv.Atoi(sshPortString)
	if err != nil {
		log.Fatalln("Error parsing address:", err)
	}

	if viper.GetInt("http-port-override") != 0 {
		httpPort = viper.GetInt("http-port-override")
	}

	if viper.GetInt("https-port-override") != 0 {
		httpsPort = viper.GetInt("https-port-override")
	}

	utils.WatchKeys()
	utils.WatchAuthUsers()
	utils.WatchHeadersSettings()
	if (viper.GetBool("strict-id-censed") || viper.GetBool("strict-id-censed-url") || viper.GetBool("strict-id-censed-files")) && !viper.GetBool("census-enabled") {
		log.Println("strict-id-censed is enabled but census-enabled is false; strict ID enforcement is disabled")
	}
	// Legacy: strict-id-censed enables both url and files modes.
	if viper.GetBool("strict-id-censed") {
		if !viper.GetBool("strict-id-censed-url") && strings.TrimSpace(viper.GetString("census-url")) != "" {
			viper.Set("strict-id-censed-url", true)
		}
		if !viper.GetBool("strict-id-censed-files") && strings.TrimSpace(viper.GetString("census-directory")) != "" {
			viper.Set("strict-id-censed-files", true)
		}
	}
	utils.StartCensusRefresher()

	state := utils.NewState()
	state.Ports.HTTPPort = httpPort
	state.Ports.HTTPSPort = httpsPort
	state.Ports.SSHPort = sshPort
	startStrictIDCensedConnectionEnforcer(state)
	utils.StartBandwidthHotReload(state)

	state.Console.State = state

	sshConfig := utils.GetSSHConfig()

	var httpsServerListener net.Listener
	sshAdditionalListener := net.Listener(nil)

	if viper.GetBool("https") && viper.GetBool("ssh-over-https") {
		httpsRawListener, err := utils.Listen(viper.GetString("https-address"))
		if err != nil {
			log.Fatal(err)
		}

		httpsBaseListener := httpsRawListener
		if viper.GetBool("proxy-protocol-listener") {
			hListener := &proxyproto.Listener{
				Listener: httpsRawListener,
			}

			utils.LoadProxyProtoConfig(hListener)
			httpsBaseListener = hListener
		}

		sshOnHTTPS, nonSSHOnHTTPS := utils.NewSSHMuxListeners(httpsBaseListener)
		sshAdditionalListener = sshOnHTTPS
		httpsServerListener = nonSSHOnHTTPS
	}

	sshIngressLog := []string{sshPortString}
	if sshAdditionalListener != nil {
		if sshPortString == httpsPortString {
			sshIngressLog = []string{fmt.Sprintf("%s (multiplexed)", sshPortString)}
		} else {
			sshIngressLog = append(sshIngressLog, fmt.Sprintf("%s (multiplexed)", httpsPortString))
		}
	}
	log.Printf("SSH ingress enabled on: %s", strings.Join(sshIngressLog, ", "))

	go httpmuxer.Start(state, httpsServerListener)

	debugInterval := viper.GetDuration("debug-interval")

	if viper.GetBool("debug") && debugInterval > 0 {
		go func() {
			for {
				log.Println("=======Start=========")
				log.Println("===Goroutines=====")
				log.Println(runtime.NumGoroutine())
				log.Println("===Listeners======")
				state.Listeners.Range(func(key string, value net.Listener) bool {
					log.Println(key)
					return true
				})
				log.Println("===Clients========")
				state.SSHConnections.Range(func(key string, value *utils.SSHConnection) bool {
					listeners := []string{}
					value.Listeners.Range(func(name string, listener net.Listener) bool {
						listeners = append(listeners, name)
						return true
					})

					log.Println(key, value.SSHConn.User(), listeners)
					return true
				})
				log.Println("===HTTP Listeners===")
				state.HTTPListeners.Range(func(key string, value *utils.HTTPHolder) bool {
					clients := []string{}
					value.SSHConnections.Range(func(name string, conn *utils.SSHConnection) bool {
						clients = append(clients, conn.SSHConn.RemoteAddr().String())
						return true
					})

					log.Println(key, clients)
					return true
				})
				log.Println("===TCP Aliases====")
				state.AliasListeners.Range(func(key string, value *utils.AliasHolder) bool {
					clients := []string{}
					value.SSHConnections.Range(func(name string, conn *utils.SSHConnection) bool {
						clients = append(clients, conn.SSHConn.RemoteAddr().String())
						return true
					})

					log.Println(key, clients)
					return true
				})
				log.Println("===TCP Listeners====")
				state.TCPListeners.Range(func(key string, value *utils.TCPHolder) bool {
					clients := []string{}
					value.SSHConnections.Range(func(name string, conn *utils.SSHConnection) bool {
						clients = append(clients, conn.SSHConn.RemoteAddr().String())
						return true
					})

					log.Println(key, clients)
					return true
				})
				log.Println("===Web Console Routes====")
				state.Console.Clients.Range(func(key string, value []*utils.WebClient) bool {
					newData := []string{}
					for _, cl := range value {
						newData = append(newData, cl.Conn.RemoteAddr().String())
					}

					log.Println(key, newData)
					return true
				})
				log.Println("===Web Console Tokens====")
				state.Console.RouteTokens.Range(func(key, value string) bool {
					log.Println(key, value)
					return true
				})
				log.Print("========End==========\n")

				time.Sleep(debugInterval)
			}
		}()
	}

	log.Println("Starting SSH service on address:", viper.GetString("ssh-address"))

	var listener net.Listener

	if sshAdditionalListener != nil && viper.GetString("ssh-address") == viper.GetString("https-address") {
		listener = sshAdditionalListener
		sshAdditionalListener = nil
	} else {
		l, err := utils.Listen(viper.GetString("ssh-address"))
		if err != nil {
			log.Fatal(err)
		}

		if viper.GetBool("proxy-protocol-listener") {
			hListener := &proxyproto.Listener{
				Listener: l,
			}

			utils.LoadProxyProtoConfig(hListener)
			listener = hListener
		} else {
			listener = l
		}
	}

	state.Listeners.Store(viper.GetString("ssh-address"), listener)

	defer func() {
		err := listener.Close()
		if err != nil {
			log.Println("Error closing listener:", err)
		}

		state.Listeners.Delete(viper.GetString("ssh-address"))
	}()

	if sshAdditionalListener != nil {
		state.Listeners.Store(viper.GetString("https-address")+" (ssh-multiplex)", sshAdditionalListener)
		log.Println("SSH over HTTPS listener enabled on address:", viper.GetString("https-address"))
		defer func() {
			err := sshAdditionalListener.Close()
			if err != nil {
				log.Println("Error closing multiplexed ssh listener:", err)
			}
			state.Listeners.Delete(viper.GetString("https-address") + " (ssh-multiplex)")
		}()

		go func() {
			var backoff utils.AcceptBackoff
			for {
				conn, err := sshAdditionalListener.Accept()
				if err != nil {
					// B57: exit only on a closed listener; back off on transient
					// errors (EMFILE) instead of busy-looping.
					if utils.IsListenerClosedErr(err) {
						return
					}
					d := backoff.Pause()
					log.Printf("Transient accept error on multiplexed ssh listener, retrying in %s: %v", d, err)
					time.Sleep(d)
					continue
				}
				backoff.Reset()

				go handleConn(conn, "https", state, sshConfig)
			}
		}()
	}

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for range c {
			os.Exit(0)
		}
	}()

	var backoff utils.AcceptBackoff
	for {
		conn, err := listener.Accept()
		if err != nil {
			// B57: exit only on a closed listener; back off on transient errors.
			if utils.IsListenerClosedErr(err) {
				return
			}
			d := backoff.Pause()
			log.Printf("Transient accept error on ssh listener, retrying in %s: %v", d, err)
			time.Sleep(d)
			continue
		}
		backoff.Reset()

		go handleConn(conn, "ssh", state, sshConfig)
	}
}

// handleConn handles a single accepted SSH connection: it runs the handshake,
// wires up request and channel handling, and starts the connection lifecycle
// goroutines. It is a package level function so that lifecycle can be driven
// directly from tests without binding real service listeners.
func handleConn(conn net.Conn, ingress string, state *utils.State, sshConfig *ssh.ServerConfig) {
	ingressPort := ""
	if _, port, splitErr := net.SplitHostPort(conn.LocalAddr().String()); splitErr == nil {
		ingressPort = port
	}

	utils.RecordOriginIPAttempt(conn.RemoteAddr().String(), ingress, ingressPort)

	clientRemote, _, err := net.SplitHostPort(conn.RemoteAddr().String())

	if err != nil || state.IPFilter.Blocked(clientRemote) {
		reason := "IP blocked by filter"
		if err != nil {
			reason = "invalid remote address"
		}
		utils.RecordOriginIPReject(conn.RemoteAddr().String(), reason)

		err := conn.Close()
		if err != nil {
			log.Println("Error closing connection:", err)
		}

		if viper.GetBool("debug") {
			log.Printf("Blocked connection from %s to %s", conn.RemoteAddr().String(), conn.LocalAddr().String())
		}

		return
	}

	clientLoggedInMutex := &sync.Mutex{}

	clientLoggedInMutex.Lock()
	clientLoggedIn := false
	clientLoggedInMutex.Unlock()

	if viper.GetBool("cleanup-unauthed") {
		go func() {
			<-time.After(viper.GetDuration("cleanup-unauthed-timeout"))
			clientLoggedInMutex.Lock()
			if !clientLoggedIn {
				err := conn.Close()
				if err != nil {
					log.Println("Error closing connection:", err)
				}
			}
			clientLoggedInMutex.Unlock()
		}()
	}

	log.Printf("Accepted SSH connection for: %s (ingress: %s)", conn.RemoteAddr(), ingress)

	sshConn, chans, reqs, err := ssh.NewServerConn(conn, sshConfig)
	clientLoggedInMutex.Lock()
	clientLoggedIn = true
	clientLoggedInMutex.Unlock()
	if err != nil {
		utils.RecordOriginIPReject(conn.RemoteAddr().String(), err.Error())

		err := conn.Close()
		if err != nil {
			log.Println("Error closing connection:", err)
		}

		log.Printf("SSH connection could not be established (ingress: %s): %v", ingress, err)
		return
	}

	utils.RecordOriginIPSuccess(conn.RemoteAddr().String())

	pubKeyFingerprint := ""

	if sshConn.Permissions != nil {
		if _, ok := sshConn.Permissions.Extensions["pubKey"]; ok {
			pubKeyFingerprint = sshConn.Permissions.Extensions["pubKeyFingerprint"]
		}
	}

	userBandwidthProfile := utils.UserBandwidthProfileFromPermissions(sshConn.Permissions)
	if userBandwidthProfile == nil {
		userBandwidthProfile = utils.NewConnectionStatsProfile()
	}

	holderConn := &utils.SSHConnection{
		SSHConn:              sshConn,
		ConnectionID:         fmt.Sprintf("rand-%s", strings.ToLower(utils.RandStringBytesMaskImprSrc(8))),
		ConnectedAt:          time.Now(),
		BandwidthProfileLock: &sync.RWMutex{},
		Listeners:            syncmap.New[string, net.Listener](),
		ForwardCleanups:      syncmap.New[string, func()](),
		Closed:               &sync.Once{},
		Close:                make(chan bool),
		Exec:                 make(chan bool),
		Messages:             make(chan string, messageQueueSize),
		Session:              make(chan bool),
		SetupLock:            &sync.Mutex{},
		Ingress:              ingress,
		IngressPort:          ingressPort,
		VisitorForwarders:    syncmap.New[string, bool](),
	}
	holderConn.SetTCPAliasesAllowedUsers([]string{pubKeyFingerprint})
	holderConn.SetBandwidthProfile(userBandwidthProfile)
	// B62: seed strip-path from the configured default at construction. Only
	// handleSession set it (channels.go), so a client that never opens a session
	// channel (e.g. `ssh -N -R`) kept the zero value false. httpmuxer then saw
	// GetStripPath() != the global default and forced strip-path off for that
	// forward, so -N clients silently diverged from session clients and from the
	// documented default (strip-http-path defaults to true). Initialise it here so
	// every connection starts consistent; handleSession/exec overrides still apply.
	holderConn.SetStripPath(viper.GetBool("strip-http-path"))

	state.SSHConnections.Store(sshConn.RemoteAddr().String(), holderConn)

	go func() {
		err := sshConn.Wait()
		if err != nil && viper.GetBool("debug") {
			log.Println("Closing SSH connection:", err)
		}

		select {
		case <-holderConn.Close:
			break
		default:
			initiator := "client"
			reason := "disconnected"
			if err != nil {
				errStr := err.Error()
				if strings.Contains(errStr, "i/o timeout") {
					initiator = "server"
					reason = "ping timeout"
				} else {
					reason = errStr
				}
			}
			holderConn.SetCloseInfo(initiator, reason)
			holderConn.CleanUp(state)
		}
	}()

	go handleRequests(reqs, holderConn, state)
	go handleChannels(chans, holderConn, state)

	go func() {
		select {
		case <-holderConn.Exec:
		case <-time.After(1 * time.Second):
			break
		}

		runTime := 0.0
		ticker := time.NewTicker(1 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				runTime++

				if deadline := holderConn.GetDeadline(); deadline != nil && time.Now().After(*deadline) {
					holderConn.SendMessage("Connection deadline reached. Closing connection.", true)
					time.Sleep(1 * time.Millisecond)
					holderConn.SetCloseInfo("server", "deadline reached")
					holderConn.CleanUp(state)
					return
				}

				if ((viper.GetBool("cleanup-unbound") && runTime > viper.GetDuration("cleanup-unbound-timeout").Seconds()) || holderConn.GetAutoClose()) && holderConn.ListenerCount() == 0 {
					holderConn.SendMessage("No forwarding requests sent. Closing connection.", true)
					time.Sleep(1 * time.Millisecond)
					holderConn.SetCloseInfo("server", "no forwarding requests")
					holderConn.CleanUp(state)
				}
			case <-holderConn.Close:
				return
			}
		}
	}()

	if viper.GetBool("ping-client") {
		go func() {
			tickDuration := viper.GetDuration("ping-client-interval")
			pingTimeout := viper.GetDuration("ping-client-timeout")
			ticker := time.NewTicker(tickDuration)
			defer ticker.Stop()

			// The connection must stay readable until the reply to the next
			// ping is due: one interval until it is sent, plus the timeout to
			// answer it. A deadline of only pingTimeout expired exactly when
			// the first ping went out with the default 5s/5s settings, so every
			// tunnel was closed after ~5s ("Error retrieving keepalive
			// response: i/o timeout"). Upstream uses interval+timeout as well.
			nextDeadline := func(from time.Time) time.Time {
				return from.Add(tickDuration).Add(pingTimeout)
			}

			deadline := nextDeadline(time.Now())
			if err := conn.SetDeadline(deadline); err != nil {
				log.Println("Unable to set initial deadline")
			}
			holderConn.PingDeadlineNs.Store(deadline.UnixNano())

			var pendingReply chan error // non-nil when a SendRequest is in-flight

			for {
				select {
				case <-ticker.C:
					now := time.Now()

					if pendingReply != nil {
						// A previous SendRequest is still in-flight.
						// Check non-blocking if a reply arrived.
						select {
						case err := <-pendingReply:
							pendingReply = nil
							if err != nil {
								// SSH-level error: connection is dead.
								holderConn.PingFailTotal.Add(1)
								holderConn.LastPingFailAtNs.Store(now.UnixNano())
								holderConn.LastPingAtNs.Store(now.UnixNano())
								holderConn.RecordPingFail(now.UnixNano())
								log.Println("Error retrieving keepalive response:", err)
								return
							}
							// Late reply arrived — success.
							holderConn.LastPingOkAtNs.Store(now.UnixNano())
							holderConn.RecordPingRecovery(now.UnixNano())
							deadline = nextDeadline(now)
							if err := conn.SetDeadline(deadline); err != nil {
								log.Println("Unable to set deadline")
							}
							holderConn.PingDeadlineNs.Store(deadline.UnixNano())
						default:
							// Still no reply. Count as another failed ping cycle.
							holderConn.PingSentTotal.Add(1)
							holderConn.LastPingAtNs.Store(now.UnixNano())
							holderConn.PingFailTotal.Add(1)
							holderConn.LastPingFailAtNs.Store(now.UnixNano())
							holderConn.RecordPingFail(now.UnixNano())
							continue
						}
					}

					// Send a new ping.
					holderConn.PingSentTotal.Add(1)
					holderConn.LastPingAtNs.Store(now.UnixNano())

					replyCh := make(chan error, 1)
					go func() {
						_, _, err := sshConn.SendRequest("keepalive@sish", true, nil)
						replyCh <- err
					}()

					// Wait for reply up to one tick interval.
					select {
					case err := <-replyCh:
						if err != nil {
							failNs := time.Now().UnixNano()
							holderConn.PingFailTotal.Add(1)
							holderConn.LastPingFailAtNs.Store(failNs)
							holderConn.RecordPingFail(failNs)
							log.Println("Error retrieving keepalive response:", err)
							return
						}
						okNs := time.Now().UnixNano()
						holderConn.LastPingOkAtNs.Store(okNs)
						holderConn.RecordPingRecovery(okNs)
						deadline = nextDeadline(time.Now())
						if err := conn.SetDeadline(deadline); err != nil {
							log.Println("Unable to set deadline")
						}
						holderConn.PingDeadlineNs.Store(deadline.UnixNano())
					case <-time.After(tickDuration):
						// No reply within one interval — count as fail.
						failNs := time.Now().UnixNano()
						holderConn.PingFailTotal.Add(1)
						holderConn.LastPingFailAtNs.Store(failNs)
						holderConn.RecordPingFail(failNs)
						pendingReply = replyCh
					case <-holderConn.Close:
						return
					}
				case <-holderConn.Close:
					return
				}
			}
		}()
	}
}
