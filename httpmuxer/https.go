package httpmuxer

import (
	"encoding/base64"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/antoniomika/sish/utils"
	"github.com/spf13/viper"
	"github.com/vulcand/oxy/roundrobin"
)

// sniHandshakeTimeout bounds how long we wait for a newly accepted connection
// to send its TLS ClientHello before giving up on it.
const sniHandshakeTimeout = 10 * time.Second

// sniHandshakeConcurrency bounds the number of in-flight ClientHello peeks so a
// flood of idle connections cannot spawn unbounded goroutines.
const sniHandshakeConcurrency = 256

type proxyListener struct {
	Listener net.Listener
	Holder   *utils.TCPHolder
	State    *utils.State

	// ready carries connections that must be served by the local HTTPS server
	// (no SNI match). accepted/slots are lazily initialised on first Accept.
	ready chan net.Conn
	slots chan struct{}
	start sync.Once
}

// Accept returns the next connection destined for the local HTTPS server.
//
// SNI classification happens on per-connection goroutines rather than inline:
// peeking the ClientHello is a blocking read, so doing it on the accept path
// let a single client that connected without sending anything stall the entire
// HTTPS listener (B13).
func (pL *proxyListener) Accept() (net.Conn, error) {
	pL.ensureStarted()

	conn, ok := <-pL.ready
	if !ok || conn == nil {
		return nil, net.ErrClosed
	}

	return conn, nil
}

func (pL *proxyListener) ensureStarted() {
	pL.start.Do(func() {
		pL.ready = make(chan net.Conn)
		pL.slots = make(chan struct{}, sniHandshakeConcurrency)

		go pL.acceptLoop()
	})
}

func (pL *proxyListener) acceptLoop() {
	// inFlight tracks connections still being classified. ready must not be
	// closed while one of them may still need to be handed to Accept, or those
	// connections would be dropped at shutdown.
	var inFlight sync.WaitGroup

	defer func() {
		inFlight.Wait()
		close(pL.ready)
	}()

	var backoff utils.AcceptBackoff
	for {
		cl, err := pL.Listener.Accept()
		if err != nil {
			// B57: only a closed listener ends the loop. Previously ANY Accept
			// error returned, which closed pL.ready; proxyListener.Accept then
			// returned net.ErrClosed, ServeTLS returned it and the log.Fatal
			// around ServeTLS killed the whole process. A single EMFILE was
			// enough. Back off and retry transient errors instead.
			if utils.IsListenerClosedErr(err) {
				return
			}

			d := backoff.Pause()
			log.Printf("Transient accept error on HTTPS SNI listener, retrying in %s: %v", d, err)
			time.Sleep(d)
			continue
		}
		backoff.Reset()

		clientRemote, _, splitErr := net.SplitHostPort(cl.RemoteAddr().String())
		if splitErr != nil || pL.State.IPFilter.Blocked(clientRemote) {
			if closeErr := cl.Close(); closeErr != nil {
				log.Println("Error closing connection:", closeErr)
			}

			if viper.GetBool("debug") {
				log.Printf("Blocked connection from %s to %s", cl.RemoteAddr().String(), cl.LocalAddr().String())
			}

			continue
		}

		pL.slots <- struct{}{}
		inFlight.Add(1)

		go func(cl net.Conn) {
			defer inFlight.Done()
			defer func() { <-pL.slots }()
			pL.route(cl)
		}(cl)
	}
}

// route peeks the TLS ClientHello under a deadline and either proxies the
// connection to a matching SNI backend or hands it to the local HTTPS server.
func (pL *proxyListener) route(cl net.Conn) {
	if err := cl.SetReadDeadline(time.Now().Add(sniHandshakeTimeout)); err != nil && viper.GetBool("debug") {
		log.Println("Unable to set SNI handshake deadline:", err)
	}

	tlsHello, teeConn, _ := utils.PeekTLSHello(cl)

	if err := cl.SetReadDeadline(time.Time{}); err != nil && viper.GetBool("debug") {
		log.Println("Unable to clear SNI handshake deadline:", err)
	}

	if tlsHello == nil || tlsHello.ServerName == "" {
		pL.deliverLocal(teeConn)
		return
	}

	balancerName := tlsHello.ServerName

	balancer, ok := pL.Holder.Balancers.Load(balancerName)
	if !ok {
		pL.Holder.Balancers.Range(func(n string, b *roundrobin.RoundRobin) bool {
			if utils.MatchesWildcardHost(balancerName, n) {
				balancer = b
				return false
			}
			return true
		})

		if balancer == nil {
			pL.deliverLocal(teeConn)
			return
		}
	}

	connectionLocation, err := balancer.NextServer()
	if err != nil {
		log.Println("Unable to load connection location:", err)

		if closeErr := teeConn.Close(); closeErr != nil {
			log.Println("Error closing teeConn:", closeErr)
		}

		return
	}

	host, err := base64.StdEncoding.DecodeString(connectionLocation.Host)
	if err != nil {
		log.Println("Unable to decode connection location:", err)

		if closeErr := teeConn.Close(); closeErr != nil {
			log.Println("Error closing teeConn:", closeErr)
		}

		return
	}

	hostAddr := string(host)

	// The root HTTPS balancer entry is a sentinel for "serve this locally"
	// rather than a real unix socket backend.
	if hostAddr == "_sish_https_root" {
		pL.deliverLocal(teeConn)
		return
	}

	logLine := fmt.Sprintf("Accepted connection from %s -> %s", teeConn.RemoteAddr().String(), teeConn.LocalAddr().String())
	log.Println(logLine)

	if viper.GetBool("log-to-client") {
		pL.Holder.SSHConnections.Range(func(key string, sshConn *utils.SSHConnection) bool {
			sshConn.Listeners.Range(func(listenerAddr string, val net.Listener) bool {
				if listenerAddr == hostAddr {
					// B48: this runs on a bounded SNI-classification goroutine
					// that holds a concurrency slot; never block it on a slow
					// client reading its log stream.
					sshConn.TrySendMessage(logLine)

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

		if closeErr := teeConn.Close(); closeErr != nil {
			log.Println("Error closing teeConn:", closeErr)
		}

		return
	}

	go utils.CopyBoth(conn, teeConn)
}

// deliverLocal hands a connection to the local HTTPS server via Accept.
func (pL *proxyListener) deliverLocal(conn net.Conn) {
	if conn == nil {
		return
	}

	defer func() {
		// The ready channel is closed once the underlying listener stops; a
		// send racing with that close would panic, so recover and drop the
		// connection instead.
		if r := recover(); r != nil {
			_ = conn.Close()
		}
	}()

	pL.ready <- conn
}

func (pL *proxyListener) Close() error {
	return pL.Listener.Close()
}

// Addr returns the listener's network address.
func (pL *proxyListener) Addr() net.Addr {
	return pL.Listener.Addr()
}
