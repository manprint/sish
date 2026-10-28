package httpmuxer

import (
	"bufio"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/spf13/viper"
)

// Visitors that stall on headers or sit idle on a keep-alive connection get
// disconnected instead of holding the connection forever.
func TestNewServerClosesStalledConnections(t *testing.T) {
	for _, key := range []string{"http-read-header-timeout", "http-idle-timeout"} {
		prev := viper.Get(key)
		viper.Set(key, 200*time.Millisecond)
		t.Cleanup(func() { viper.Set(key, prev) })
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	srv := newServer(l.Addr().String(), http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })

	closedWithin := func(t *testing.T, r io.Reader) {
		t.Helper()

		done := make(chan struct{})
		go func() {
			_, _ = io.Copy(io.Discard, r)
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Fatal("server kept the connection open")
		}
	}

	t.Run("partial headers", func(t *testing.T) {
		conn, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()

		if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: example.com\r\n"); err != nil {
			t.Fatal(err)
		}
		closedWithin(t, conn)
	})

	t.Run("idle keep-alive", func(t *testing.T) {
		conn, err := net.Dial("tcp", l.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()

		if _, err := io.WriteString(conn, "GET / HTTP/1.1\r\nHost: example.com\r\n\r\n"); err != nil {
			t.Fatal(err)
		}

		br := bufio.NewReader(conn)
		resp, err := http.ReadResponse(br, nil)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.Close {
			t.Fatal("server closed the connection instead of keeping it alive")
		}

		closedWithin(t, br)
	})
}

// Idle connections to a tunnel expire before the tunnel side's own idle
// timeout closes them, so a reused connection is never already dead.
func TestTunnelIdleConnTimeout(t *testing.T) {
	for _, key := range []string{"idle-connection", "idle-connection-timeout"} {
		prev := viper.Get(key)
		t.Cleanup(func() { viper.Set(key, prev) })
	}

	viper.Set("idle-connection", false)
	viper.Set("idle-connection-timeout", 10*time.Second)
	if got := tunnelIdleConnTimeout(); got != 90*time.Second {
		t.Errorf("idle-connection off: got %v, want 90s", got)
	}

	viper.Set("idle-connection", true)
	if got := tunnelIdleConnTimeout(); got != 5*time.Second {
		t.Errorf("idle-connection on: got %v, want 5s", got)
	}

	transport, ok := RoundTripper().(*tunnelTransport)
	if !ok {
		t.Fatal("RoundTripper is not a tunnelTransport")
	}
	if got := transport.base.IdleConnTimeout; got != 5*time.Second {
		t.Errorf("transport IdleConnTimeout = %v, want 5s", got)
	}
}
