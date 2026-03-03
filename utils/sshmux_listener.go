package utils

import (
	"errors"
	"net"
	"sync"
)

// muxConnListener implements net.Listener by receiving accepted connections from a channel.
type muxConnListener struct {
	addr   net.Addr
	conns  chan net.Conn
	closed chan struct{}
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

func (m *muxConnListener) Close() error {
	select {
	case <-m.closed:
		return nil
	default:
		close(m.closed)
		close(m.conns)
		return nil
	}
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
			if errors.Is(err, net.ErrClosed) {
				s.close.Do(func() {
					_ = s.ssh.Close()
					_ = s.other.Close()
				})
				return
			}

			continue
		}

		teeConn := NewTeeConn(conn)
		header, err := teeConn.Buffer.Peek(4)
		if err != nil {
			_ = teeConn.Close()
			continue
		}

		teeConn.Unbuffer = true

		target := s.other.conns
		if string(header) == "SSH-" {
			target = s.ssh.conns
		}

		select {
		case target <- teeConn:
		case <-s.ssh.closed:
			_ = teeConn.Close()
			return
		case <-s.other.closed:
			_ = teeConn.Close()
			return
		}
	}
}