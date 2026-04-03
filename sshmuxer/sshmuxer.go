// Package sshmuxer handles the underlying SSH server
// and multiplexing forwarding sessions.
package sshmuxer

import (
	"errors"
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

	handleSSHConn := func(conn net.Conn, ingress string) {
		go func() {
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
				SSHConn:                sshConn,
				ConnectionID:           fmt.Sprintf("rand-%s", strings.ToLower(utils.RandStringBytesMaskImprSrc(8))),
				ConnectedAt:            time.Now(),
				BandwidthProfileLock:   &sync.RWMutex{},
				Listeners:              syncmap.New[string, net.Listener](),
				ForwardCleanups:        syncmap.New[string, func()](),
				Closed:                 &sync.Once{},
				Close:                  make(chan bool),
				Exec:                   make(chan bool),
				Messages:               make(chan string),
				Session:                make(chan bool),
				SetupLock:              &sync.Mutex{},
				TCPAliasesAllowedUsers: []string{pubKeyFingerprint},
				Ingress:                ingress,
				IngressPort:            ingressPort,
				VisitorForwarders:      syncmap.New[string, bool](),
			}
			holderConn.SetBandwidthProfile(userBandwidthProfile)

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

				for {
					select {
					case <-ticker.C:
						runTime++

						if holderConn.Deadline != nil && time.Now().After(*holderConn.Deadline) {
							holderConn.SendMessage("Connection deadline reached. Closing connection.", true)
							time.Sleep(1 * time.Millisecond)
							holderConn.CleanUp(state)
							return
						}

						if ((viper.GetBool("cleanup-unbound") && runTime > viper.GetDuration("cleanup-unbound-timeout").Seconds()) || holderConn.AutoClose) && holderConn.ListenerCount() == 0 {
							holderConn.SendMessage("No forwarding requests sent. Closing connection.", true)
							time.Sleep(1 * time.Millisecond)
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
					ticker := time.NewTicker(tickDuration)

					for {
						deadline := time.Now().Add(tickDuration).Add(viper.GetDuration("ping-client-timeout"))
						err := conn.SetDeadline(deadline)
						if err != nil {
							log.Println("Unable to set deadline")
						}
						holderConn.PingDeadlineNs.Store(deadline.UnixNano())

						select {
						case <-ticker.C:
							holderConn.PingSentTotal.Add(1)
							holderConn.LastPingAtNs.Store(time.Now().UnixNano())
							_, _, err := sshConn.SendRequest("keepalive@sish", true, nil)
							if err != nil {
								holderConn.PingFailTotal.Add(1)
								log.Println("Error retrieving keepalive response:", err)
								return
							}
							holderConn.LastPingOkAtNs.Store(time.Now().UnixNano())
						case <-holderConn.Close:
							return
						}
					}
				}()
			}
		}()
	}

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
			for {
				conn, err := sshAdditionalListener.Accept()
				if err != nil {
					if errors.Is(err, net.ErrClosed) {
						return
					}
					log.Println(err)
					continue
				}

				handleSSHConn(conn, "https")
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

	for {
		conn, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			log.Println(err)
			continue
		}

		handleSSHConn(conn, "ssh")
	}
}
