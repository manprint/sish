package utils

import (
	"log"
	"net"
	"sync"
	"time"

	"github.com/spf13/viper"
)

// muxHandshakeTimeout bounds how long the dispatcher waits for a newly accepted
// connection to reveal whether it speaks SSH. Without it, a peer that connects
// and never writes blocked the single dispatch goroutine forever and stalled
// every other connection on the multiplexed listener (B11).
const muxHandshakeTimeout = 10 * time.Second

// muxHandshakeConcurrency bounds how many peeks run at once so a flood of idle
// connections cannot spawn unbounded goroutines.
const muxHandshakeConcurrency = 256

// muxAcceptBackoff is the pause after a transient Accept error before retrying so
// a persistent failure (e.g. EMFILE under fd pressure) does not spin the
// dispatcher at 100% CPU (B44).
const muxAcceptBackoff = 10 * time.Millisecond

// muxConnListener implements net.Listener by receiving accepted connections from a channel.
type muxConnListener struct {
	addr   net.Addr
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func (m *muxConnListener) Accept() (net.Conn, error) {
	select {
	case <-m.closed:
		return nil, net.ErrClosed
	case conn, ok := <-m.conns:
		if !ok {
			return nil, net.ErrClosed
		}

		if conn == nil {
			return nil, net.ErrClosed
		}

		return conn, nil
	}
}

// Close marks the listener closed. It deliberately does NOT close the conns
// channel: the dispatcher may be mid-send on it, and closing it raced into a
// "send on closed channel" panic that took the process down (B12). Accept and
// the dispatcher both select on closed, so signalling it is sufficient.
func (m *muxConnListener) Close() error {
	m.once.Do(func() {
		close(m.closed)
	})

	return nil
}

func (m *muxConnListener) Addr() net.Addr {
	return m.addr
}

// sshMuxListener splits a single listener into two listeners:
// one for SSH traffic (prefix "SSH-") and one for all other traffic.
type sshMuxListener struct {
	listener net.Listener
	ssh      *muxConnListener
	other    *muxConnListener
	close    sync.Once
	slots    chan struct{}
}

// NewSSHMuxListeners returns two listeners sourced from a shared base listener:
// - sshListener receives SSH connections
// - otherListener receives non-SSH connections
func NewSSHMuxListeners(listener net.Listener) (sshListener net.Listener, otherListener net.Listener) {
	mux := &sshMuxListener{
		listener: listener,
		ssh: &muxConnListener{
			addr:   listener.Addr(),
			conns:  make(chan net.Conn),
			closed: make(chan struct{}),
		},
		other: &muxConnListener{
			addr:   listener.Addr(),
			conns:  make(chan net.Conn),
			closed: make(chan struct{}),
		},
		slots: make(chan struct{}, muxHandshakeConcurrency),
	}

	go mux.dispatch()

	return &sharedMuxConnListener{mux: mux, listener: mux.ssh}, &sharedMuxConnListener{mux: mux, listener: mux.other}
}

type sharedMuxConnListener struct {
	mux      *sshMuxListener
	listener *muxConnListener
}

func (s *sharedMuxConnListener) Accept() (net.Conn, error) {
	return s.listener.Accept()
}

func (s *sharedMuxConnListener) Close() error {
	s.mux.close.Do(func() {
		_ = s.mux.listener.Close()
		_ = s.mux.ssh.Close()
		_ = s.mux.other.Close()
	})

	return nil
}

func (s *sharedMuxConnListener) Addr() net.Addr {
	return s.listener.Addr()
}

func (s *sshMuxListener) dispatch() {
	for {
		conn, err := s.listener.Accept()
		if err != nil {
			if IsListenerClosedErr(err) {
				s.close.Do(func() {
					_ = s.ssh.Close()
					_ = s.other.Close()
				})
				return
			}

			// B44: a transient error must not spin this single dispatch
			// goroutine. Back off briefly and retry; the mux listener is the
			// whole SSH service, so we never give up on it.
			log.Println("Transient accept error on mux listener:", err)
			time.Sleep(muxAcceptBackoff)
			continue
		}

		// Classify off the accept path. The peek below is a blocking read, so
		// doing it inline let a single silent client wedge the listener (B11).
		select {
		case s.slots <- struct{}{}:
		case <-s.ssh.closed:
			_ = conn.Close()
			return
		case <-s.other.closed:
			_ = conn.Close()
			return
		}

		go func(conn net.Conn) {
			defer func() { <-s.slots }()
			s.classify(conn)
		}(conn)
	}
}

// classify peeks the connection prefix under a deadline and hands the
// connection to the matching sub-listener.
func (s *sshMuxListener) classify(conn net.Conn) {
	teeConn := NewTeeConn(conn)

	if err := conn.SetReadDeadline(time.Now().Add(muxHandshakeTimeout)); err != nil && viper.GetBool("debug") {
		log.Println("Unable to set mux handshake deadline:", err)
	}

	header, err := teeConn.Buffer.Peek(4)
	if err != nil {
		_ = teeConn.Close()
		return
	}

	// Clear the handshake deadline; from here the connection is handled by the
	// SSH server or the TLS stack, which manage their own timeouts.
	if err := conn.SetReadDeadline(time.Time{}); err != nil && viper.GetBool("debug") {
		log.Println("Unable to clear mux handshake deadline:", err)
	}

	teeConn.Unbuffer = true

	target := s.other
	if string(header) == "SSH-" {
		target = s.ssh
	}

	select {
	case target.conns <- teeConn:
	case <-s.ssh.closed:
		_ = teeConn.Close()
	case <-s.other.closed:
		_ = teeConn.Close()
	}
}
