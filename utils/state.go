package utils

import (
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/jpillora/ipfilter"
	"github.com/pires/go-proxyproto"
	"github.com/spf13/viper"
	"github.com/vulcand/oxy/forward"
	"github.com/vulcand/oxy/roundrobin"
)

// ListenerType represents any listener sish supports.
type ListenerType int

const (
	// AliasListener represents a tcp alias.
	AliasListener ListenerType = iota

	// HTTPListener represents a HTTP proxy.
	HTTPListener

	// TCPListener represents a generic tcp listener.
	TCPListener

	// ProcessListener represents a process specific listener.
	ProcessListener
)

// LogWriter represents a writer that is used for writing logs in multiple locations.
type LogWriter struct {
	TimeFmt     string
	MultiWriter io.Writer
}

// Write implements the write function for the LogWriter. It will add a time in a
// specific format to logs.
func (w LogWriter) Write(bytes []byte) (int, error) {
	return fmt.Fprintf(w.MultiWriter, "%v | %s", time.Now().Format(w.TimeFmt), string(bytes))
}

// ListenerHolder represents a generic listener.
type ListenerHolder struct {
	net.Listener
	ListenAddr   string
	Type         ListenerType
	SSHConn      *SSHConnection
	OriginalAddr string
	OriginalPort uint32
}

// HTTPHolder holds proxy and connection info.
type HTTPHolder struct {
	HTTPUrl        *url.URL
	SSHConnections *syncmap.Map[string, *SSHConnection]
	Forward        *forward.Forwarder
	Balancer       *roundrobin.RoundRobin
}

// AliasHolder holds alias and connection info.
type AliasHolder struct {
	AliasHost      string
	SSHConnections *syncmap.Map[string, *SSHConnection]
	Balancer       *roundrobin.RoundRobin
}

// TCPHolder holds proxy and connection info.
type TCPHolder struct {
	TCPHost        string
	Listener       net.Listener
	SSHConnections *syncmap.Map[string, *SSHConnection]
	SNIProxy       bool
	Balancers      *syncmap.Map[string, *roundrobin.RoundRobin]
	NoHandle       bool
}

// Handle will copy connections from one handler to a roundrobin server.
func (tH *TCPHolder) Handle(state *State) {
	for {
		cl, err := tH.Listener.Accept()
		if err != nil {
			break
		}

		go func() {
			clientRemote, _, err := net.SplitHostPort(cl.RemoteAddr().String())

			if err != nil || state.IPFilter.Blocked(clientRemote) {
				err := cl.Close()
				if err != nil {
					log.Printf("Unable to close connection: %s", err)
				}

				if viper.GetBool("debug") {
					log.Printf("Blocked connection from %s to %s", cl.RemoteAddr().String(), cl.LocalAddr().String())
				}

				return
			}

			var bufBytes []byte

			balancerName := ""
			if tH.SNIProxy {
				tlsHello, teeConn, err := PeekTLSHello(cl)
				if tlsHello == nil {
					log.Printf("Unable to read TLS hello: %s", err)

					err := cl.Close()
					if err != nil {
						log.Printf("Unable to close connection: %s", err)
					}

					return
				}

				bufBytes = make([]byte, teeConn.Buffer.Buffered())

				_, err = io.ReadFull(teeConn, bufBytes)
				if err != nil {
					log.Printf("Unable to read buffered data: %s", err)

					err := cl.Close()
					if err != nil {
						log.Printf("Unable to close connection: %s", err)
					}

					return
				}

				balancerName = tlsHello.ServerName
			}

			pB, ok := tH.Balancers.Load(balancerName)
			if !ok {
				tH.Balancers.Range(func(n string, b *roundrobin.RoundRobin) bool {
					if MatchesWildcardHost(balancerName, n) {
						pB = b
						return false
					}
					return true
				})

				if pB == nil {
					log.Printf("Unable to load connection location: %s not found on TCP listener %s", balancerName, tH.TCPHost)

					err := cl.Close()
					if err != nil {
						log.Printf("Unable to close connection: %s", err)
					}

					return
				}
			}

			balancer := pB

			connectionLocation, err := balancer.NextServer()
			if err != nil {
				log.Println("Unable to load connection location:", err)

				err := cl.Close()
				if err != nil {
					log.Printf("Unable to close connection: %s", err)
				}

				return
			}

			host, err := base64.StdEncoding.DecodeString(connectionLocation.Host)
			if err != nil {
				log.Println("Unable to decode connection location:", err)

				err := cl.Close()
				if err != nil {
					log.Printf("Unable to close connection: %s", err)
				}

				return
			}

			hostAddr := string(host)

			logLine := fmt.Sprintf("Accepted connection from %s -> %s", cl.RemoteAddr().String(), cl.LocalAddr().String())
			log.Println(logLine)

			listenPort := 0
			if localTCPAddr, ok := cl.LocalAddr().(*net.TCPAddr); ok {
				listenPort = localTCPAddr.Port
			}

			if viper.GetBool("log-to-client") {
				tH.SSHConnections.Range(func(key string, sshConn *SSHConnection) bool {
					sshConn.Listeners.Range(func(listenerAddr string, val net.Listener) bool {
						if listenerAddr == hostAddr {
							sshConn.SendMessage(logLine, true)
							if listenPort > 0 {
								WriteForwardersLogLine(BuildTCPForwardersLogKey(sshConn.ConnectionID, listenPort), logLine)
							}

							return false
						}

						return true
					})

					return true
				})
			} else {
				tH.SSHConnections.Range(func(_ string, sshConn *SSHConnection) bool {
					sshConn.Listeners.Range(func(listenerAddr string, _ net.Listener) bool {
						if listenerAddr == hostAddr && listenPort > 0 {
							WriteForwardersLogLine(BuildTCPForwardersLogKey(sshConn.ConnectionID, listenPort), logLine)
							return false
						}

						return true
					})
					return true
				})
			}

			conn, err := net.Dial("unix", hostAddr)
			if err != nil {
				log.Println("Error connecting to tcp balancer:", err)

				err := cl.Close()
				if err != nil {
					log.Printf("Unable to close connection: %s", err)
				}

				return
			}

			var proxyProtoHeader *proxyproto.Header

			tH.SSHConnections.Range(func(_ string, sshConn *SSHConnection) bool {
				if sshConn.ProxyProto != 0 {
					var sourceInfo *net.TCPAddr
					var destInfo *net.TCPAddr
					if _, ok := cl.RemoteAddr().(*net.TCPAddr); !ok {
						sourceInfo = sshConn.SSHConn.RemoteAddr().(*net.TCPAddr)
						destInfo = sshConn.SSHConn.LocalAddr().(*net.TCPAddr)
					} else {
						sourceInfo = cl.RemoteAddr().(*net.TCPAddr)
						destInfo = cl.LocalAddr().(*net.TCPAddr)
					}

					addressFamily := proxyproto.TCPv4
					if sourceInfo.IP.To4() == nil {
						addressFamily = proxyproto.TCPv6
					}

					proxyProtoHeader = &proxyproto.Header{
						Version:           sshConn.ProxyProto,
						Command:           proxyproto.PROXY,
						TransportProtocol: addressFamily,
						SourceAddr:        sourceInfo,
						DestinationAddr:   destInfo,
					}
				}

				return false
			})

			if proxyProtoHeader != nil {
				_, err := proxyProtoHeader.WriteTo(conn)
				if err != nil && viper.GetBool("debug") {
					log.Println("Error writing to channel:", err)
				}
			}

			if bufBytes != nil {
				_, err := conn.Write(bufBytes)
				if err != nil {
					log.Println("Unable to write to conn:", err)

					err := cl.Close()
					if err != nil {
						log.Printf("Unable to close connection: %s", err)
					}

					return
				}
			}

			CopyBoth(conn, cl)
		}()
	}
}

type Ports struct {
	// HTTPPort is used as a string override for the used HTTP port.
	HTTPPort int

	// HTTPSPort is used as a string override for the used HTTPS port.
	HTTPSPort int

	// SSHPort is used as a string override for the used SSH port.
	SSHPort int
}

// LifecycleMetrics contains forward lifecycle counters for observability.
type LifecycleMetrics struct {
	ForwardCreateTotal                     atomic.Uint64
	ForwardCleanupTotal                    atomic.Uint64
	ForwardCleanupErrorsTotal              atomic.Uint64
	ForwardCleanupListenerCloseErrorsTotal atomic.Uint64
	ForwardCleanupSocketRemoveErrorsTotal  atomic.Uint64
	ForwardCleanupUnknownErrorsTotal       atomic.Uint64
	DirtyForwardsStableTotal               atomic.Uint64
	DirtyForwardsListenerTotal             atomic.Uint64
	DirtyForwardsHTTPTotal                 atomic.Uint64
	DirtyForwardsTCPTotal                  atomic.Uint64
	DirtyForwardsAliasTotal                atomic.Uint64
	ForceConnectTakeoversTotal             atomic.Uint64
	DebugBindConflictTotal                 atomic.Uint64
	DebugBindConflictHTTPTotal             atomic.Uint64
	DebugBindConflictAliasTotal            atomic.Uint64
	DebugBindConflictSNITotal              atomic.Uint64
	DebugBindConflictTCPTotal              atomic.Uint64
	DebugStaleHolderPurgedTotal            atomic.Uint64
	DebugStaleHolderPurgedHTTPTotal        atomic.Uint64
	DebugStaleHolderPurgedAliasTotal       atomic.Uint64
	DebugStaleHolderPurgedSNITotal         atomic.Uint64
	DebugStaleHolderPurgedTCPTotal         atomic.Uint64
	DebugForceDisconnectNoopTotal          atomic.Uint64
	DebugTargetReleaseTimeoutTotal         atomic.Uint64
	VisitorAliasConnectionsTotal           atomic.Uint64
}

// State handles overall state. It retains mutexed maps for various
// datastructures and shared objects.
type State struct {
	Console        *WebConsole
	SSHConnections *syncmap.Map[string, *SSHConnection]
	Listeners      *syncmap.Map[string, net.Listener]
	HTTPListeners  *syncmap.Map[string, *HTTPHolder]
	AliasListeners *syncmap.Map[string, *AliasHolder]
	TCPListeners   *syncmap.Map[string, *TCPHolder]
	IPFilter       *ipfilter.IPFilter
	LogWriter      io.Writer
	Ports          *Ports
	Lifecycle      *LifecycleMetrics
}

// NewState returns a new State struct.
func NewState() *State {
	return &State{
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Listeners:      syncmap.New[string, net.Listener](),
		HTTPListeners:  syncmap.New[string, *HTTPHolder](),
		AliasListeners: syncmap.New[string, *AliasHolder](),
		TCPListeners:   syncmap.New[string, *TCPHolder](),
		IPFilter:       Filter,
		Console:        NewWebConsole(),
		LogWriter:      multiWriter,
		Ports:          &Ports{},
		Lifecycle:      &LifecycleMetrics{},
	}
}

func (s *State) IncrementForwardCreate() {
	if s == nil || s.Lifecycle == nil {
		return
	}
	s.Lifecycle.ForwardCreateTotal.Add(1)
}

func (s *State) IncrementForwardCleanup() {
	if s == nil || s.Lifecycle == nil {
		return
	}
	s.Lifecycle.ForwardCleanupTotal.Add(1)
}

func (s *State) IncrementForwardCleanupError() {
	s.IncrementForwardCleanupErrorCause("unknown")
}

func (s *State) IncrementForwardCleanupErrorCause(cause string) {
	if s == nil || s.Lifecycle == nil {
		return
	}
	s.Lifecycle.ForwardCleanupErrorsTotal.Add(1)

	switch strings.TrimSpace(strings.ToLower(cause)) {
	case "listener_close":
		s.Lifecycle.ForwardCleanupListenerCloseErrorsTotal.Add(1)
	case "socket_remove":
		s.Lifecycle.ForwardCleanupSocketRemoveErrorsTotal.Add(1)
	default:
		s.Lifecycle.ForwardCleanupUnknownErrorsTotal.Add(1)
	}
}

func (s *State) IncrementForceConnectTakeovers(n int) {
	if s == nil || s.Lifecycle == nil || n <= 0 {
		return
	}
	s.Lifecycle.ForceConnectTakeoversTotal.Add(uint64(n))
}

func (s *State) RecordStableDirtyForwardTypes(rows []internalForwardIssue) {
	if s == nil || s.Lifecycle == nil || len(rows) == 0 {
		return
	}

	for _, row := range rows {
		s.Lifecycle.DirtyForwardsStableTotal.Add(1)
		switch row.Type {
		case "listener":
			s.Lifecycle.DirtyForwardsListenerTotal.Add(1)
		case "http":
			s.Lifecycle.DirtyForwardsHTTPTotal.Add(1)
		case "tcp":
			s.Lifecycle.DirtyForwardsTCPTotal.Add(1)
		case "alias":
			s.Lifecycle.DirtyForwardsAliasTotal.Add(1)
		}
	}
}

func (s *State) IncrementDebugBindConflict(cause string) {
	if s == nil || s.Lifecycle == nil {
		return
	}
	s.Lifecycle.DebugBindConflictTotal.Add(1)
	switch strings.TrimSpace(strings.ToLower(cause)) {
	case "http":
		s.Lifecycle.DebugBindConflictHTTPTotal.Add(1)
	case "alias":
		s.Lifecycle.DebugBindConflictAliasTotal.Add(1)
	case "sni":
		s.Lifecycle.DebugBindConflictSNITotal.Add(1)
	case "tcp":
		s.Lifecycle.DebugBindConflictTCPTotal.Add(1)
	}
}

func (s *State) IncrementDebugStaleHolderPurged(cause string) {
	if s == nil || s.Lifecycle == nil {
		return
	}
	s.Lifecycle.DebugStaleHolderPurgedTotal.Add(1)
	switch strings.TrimSpace(strings.ToLower(cause)) {
	case "http":
		s.Lifecycle.DebugStaleHolderPurgedHTTPTotal.Add(1)
	case "alias":
		s.Lifecycle.DebugStaleHolderPurgedAliasTotal.Add(1)
	case "sni":
		s.Lifecycle.DebugStaleHolderPurgedSNITotal.Add(1)
	case "tcp":
		s.Lifecycle.DebugStaleHolderPurgedTCPTotal.Add(1)
	}
}

func (s *State) IncrementDebugForceDisconnectNoop() {
	if s == nil || s.Lifecycle == nil {
		return
	}
	s.Lifecycle.DebugForceDisconnectNoopTotal.Add(1)
}

func (s *State) IncrementDebugTargetReleaseTimeout() {
	if s == nil || s.Lifecycle == nil {
		return
	}
	s.Lifecycle.DebugTargetReleaseTimeoutTotal.Add(1)
}

func (s *State) IncrementVisitorAliasConnection() {
	if s == nil || s.Lifecycle == nil {
		return
	}
	s.Lifecycle.VisitorAliasConnectionsTotal.Add(1)
}
