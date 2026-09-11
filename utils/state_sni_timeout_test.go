package utils

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/antoniomika/syncmap"
	"github.com/jpillora/ipfilter"
	"github.com/vulcand/oxy/roundrobin"
)

// An SNI-proxied TCP forward peeks the TLS ClientHello on a per-connection
// goroutine. Without a deadline, a peer that connects and never writes pinned
// that goroutine and its file descriptor for the lifetime of the process (B27).
func TestTCPHolderSNIPeekTimesOut(t *testing.T) {
	previous := tcpSNIHandshakeTimeout
	tcpSNIHandshakeTimeout = 250 * time.Millisecond
	t.Cleanup(func() { tcpSNIHandshakeTimeout = previous })

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unable to listen: %v", err)
	}
	defer listener.Close()

	balancer, err := roundrobin.New(nil)
	if err != nil {
		t.Fatalf("roundrobin.New: %v", err)
	}

	balancers := syncmap.New[string, *roundrobin.RoundRobin]()
	balancers.Store("", balancer)

	holder := &TCPHolder{
		TCPHost:        listener.Addr().String(),
		Listener:       listener,
		SNIProxy:       true,
		SSHConnections: syncmap.New[string, *SSHConnection](),
		Balancers:      balancers,
	}

	state := NewState()
	state.IPFilter = ipfilter.New(ipfilter.Options{})
	go holder.Handle(state)

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("unable to dial: %v", err)
	}
	defer conn.Close()

	// Never send a ClientHello. The server must give up and close the
	// connection rather than block on the read forever.
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("unable to set read deadline: %v", err)
	}

	buf := make([]byte, 1)
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("expected the server to close the silent connection")
	} else if !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			t.Fatal("the server never closed the silent connection")
		}
		// Any other error (e.g. connection reset) also means the server hung up.
	}
}
