package utils

import (
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

func newMuxTestListeners(t *testing.T) (base net.Listener, sshListener net.Listener, otherListener net.Listener) {
	t.Helper()

	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("unable to listen: %v", err)
	}

	sshListener, otherListener = NewSSHMuxListeners(base)
	t.Cleanup(func() {
		_ = sshListener.Close()
		_ = otherListener.Close()
	})

	return base, sshListener, otherListener
}

func dialMux(t *testing.T, base net.Listener, payload string) net.Conn {
	t.Helper()

	conn, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatalf("unable to dial mux listener: %v", err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	if payload != "" {
		if _, err := conn.Write([]byte(payload)); err != nil {
			t.Fatalf("unable to write payload: %v", err)
		}
	}

	return conn
}

type acceptResult struct {
	conn net.Conn
	err  error
}

func acceptAsync(listener net.Listener) <-chan acceptResult {
	results := make(chan acceptResult, 1)

	go func() {
		conn, err := listener.Accept()
		results <- acceptResult{conn: conn, err: err}
	}()

	return results
}

// The dispatcher classifies connections by peeking their first bytes. That peek
// is a blocking read, so a peer that connects and never writes used to wedge the
// single dispatch goroutine and stall every other connection on the port (B11).
func TestMuxListenerSilentConnectionDoesNotStallDispatch(t *testing.T) {
	base, sshListener, _ := newMuxTestListeners(t)

	// Connect without ever writing: this connection can never be classified.
	dialMux(t, base, "")

	// A well-behaved SSH client arriving afterwards must still be served.
	dialMux(t, base, "SSH-2.0-testclient\r\n")

	select {
	case res := <-acceptAsync(sshListener):
		if res.err != nil {
			t.Fatalf("ssh Accept returned error: %v", res.err)
		}
		_ = res.conn.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("ssh Accept blocked behind a silent connection")
	}
}

func TestMuxListenerRoutesByPrefix(t *testing.T) {
	base, sshListener, otherListener := newMuxTestListeners(t)

	sshResults := acceptAsync(sshListener)
	otherResults := acceptAsync(otherListener)

	dialMux(t, base, "SSH-2.0-testclient\r\n")
	dialMux(t, base, "GET / HTTP/1.1\r\n\r\n")

	select {
	case res := <-sshResults:
		if res.err != nil {
			t.Fatalf("ssh Accept returned error: %v", res.err)
		}
		defer res.conn.Close()

		// The peeked bytes must still be readable by the SSH server.
		buf := make([]byte, 4)
		if err := res.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
			t.Fatalf("unable to set read deadline: %v", err)
		}
		if _, err := io.ReadFull(res.conn, buf); err != nil {
			t.Fatalf("unable to read replayed prefix: %v", err)
		}
		if string(buf) != "SSH-" {
			t.Fatalf("prefix=%q, want %q", buf, "SSH-")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("ssh Accept timed out")
	}

	select {
	case res := <-otherResults:
		if res.err != nil {
			t.Fatalf("other Accept returned error: %v", res.err)
		}
		_ = res.conn.Close()
	case <-time.After(5 * time.Second):
		t.Fatal("other Accept timed out")
	}
}

// Closing a sub-listener used to close the channel the dispatcher was sending
// on, which raced into a "send on closed channel" panic (B12). Closing while
// connections are in flight must be silent, and Accept must then report the
// listener as closed.
func TestMuxListenerCloseDuringInFlightConnections(t *testing.T) {
	base, sshListener, otherListener := newMuxTestListeners(t)

	for i := 0; i < 16; i++ {
		dialMux(t, base, "SSH-2.0-testclient\r\n")
		dialMux(t, base, "GET / HTTP/1.1\r\n\r\n")
	}

	if err := sshListener.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	results := acceptAsync(otherListener)

	select {
	case res := <-results:
		if res.err == nil {
			_ = res.conn.Close()
			// A connection classified before the close may still be delivered;
			// the next Accept must report closure.
			res = <-acceptAsync(otherListener)
		}
		if !errors.Is(res.err, net.ErrClosed) {
			t.Fatalf("Accept error=%v, want net.ErrClosed", res.err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Accept did not return after Close")
	}

	// Give the dispatcher a moment to observe the close with connections still
	// pending; a regression panics here rather than failing an assertion.
	time.Sleep(100 * time.Millisecond)
}

func TestMuxListenerCloseIsIdempotent(t *testing.T) {
	_, sshListener, otherListener := newMuxTestListeners(t)

	for i := 0; i < 4; i++ {
		if err := sshListener.Close(); err != nil {
			t.Fatalf("ssh Close returned error: %v", err)
		}
		if err := otherListener.Close(); err != nil {
			t.Fatalf("other Close returned error: %v", err)
		}
	}
}
