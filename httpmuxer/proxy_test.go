package httpmuxer

import (
	"context"
	"encoding/base64"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/antoniomika/sish/utils"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
	"github.com/vulcand/oxy/roundrobin"
)

// newUnixBackend starts an HTTP server on a unix socket and returns the socket
// path. The handler reflects request details back so tests can assert what the
// backend received after passing through NewReverseProxy + roundrobin, exactly
// as sish wires it (server URL host = base64(unix socket path)).
func newUnixBackend(t *testing.T) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "sishproxy")
	if err != nil {
		t.Fatalf("mkdtemp: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })

	sock := filepath.Join(dir, "b.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen unix: %v", err)
	}

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

	mux := http.NewServeMux()
	mux.HandleFunc("/echo", func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Echo-Path", r.URL.Path)
		h.Set("X-Echo-Query", r.URL.RawQuery)
		h.Set("X-Echo-Host", r.Host)
		h.Set("X-Echo-Method", r.Method)
		h.Set("X-Echo-Xff", r.Header.Get("X-Forwarded-For"))
		h.Set("X-Echo-Xfproto", r.Header.Get("X-Forwarded-Proto"))
		h.Set("X-Echo-Xfhost", r.Header.Get("X-Forwarded-Host"))
		h.Set("X-Echo-Xfport", r.Header.Get("X-Forwarded-Port"))
		h.Set("X-Echo-Xfserver", r.Header.Get("X-Forwarded-Server"))
		h.Set("X-Echo-Xrealip", r.Header.Get("X-Real-Ip"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		proto := r.Header.Get("X-Forwarded-Proto")
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		// First frame reports the X-Forwarded-Proto the backend observed, then
		// echo subsequent frames.
		if err := conn.WriteMessage(websocket.TextMessage, []byte("proto="+proto)); err != nil {
			return
		}
		for {
			mt, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if err := conn.WriteMessage(mt, msg); err != nil {
				return
			}
		}
	})

	srv := &http.Server{Handler: mux}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})

	return sock
}

// newProxyFront wires NewReverseProxy behind a roundrobin balancer pointing at
// the unix-socket backend and fronts it with an httptest server, mirroring how
// handleHTTPListener builds the holder. mod (optional) is attached to the
// request context like httpmuxer.go does so the proxy's ModifyResponse runs it.
func newProxyFront(t *testing.T, sock string, mod func(*http.Response) error) *httptest.Server {
	t.Helper()

	lb, err := roundrobin.New(NewReverseProxy())
	if err != nil {
		t.Fatalf("roundrobin.New: %v", err)
	}

	serverURL := &url.URL{
		Scheme: "http",
		Host:   base64.StdEncoding.EncodeToString([]byte(sock)),
	}
	if err := lb.UpsertServer(serverURL); err != nil {
		t.Fatalf("UpsertServer: %v", err)
	}

	front := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if mod != nil {
			r = r.WithContext(context.WithValue(r.Context(), respModKey{}, mod))
		}
		lb.ServeHTTP(w, r)
	}))
	t.Cleanup(front.Close)
	return front
}

// TestReverseProxyHTTPParity verifies that the stdlib-based reverse proxy
// preserves the path/query, the client Host header (PassHostHeader), and the
// X-Forwarded-* / X-Real-Ip headers oxy's HeaderRewriter used to set — and that
// X-Forwarded-For is appended exactly once (not duplicated).
func TestReverseProxyHTTPParity(t *testing.T) {
	sock := newUnixBackend(t)
	front := newProxyFront(t, sock, nil)

	req, err := http.NewRequest(http.MethodGet, front.URL+"/echo?x=1&y=2", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Host = "myhost.example"
	req.Header.Set("X-Forwarded-For", "1.2.3.4")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	checks := map[string]string{
		"X-Echo-Path":    "/echo",
		"X-Echo-Query":   "x=1&y=2",
		"X-Echo-Host":    "myhost.example",
		"X-Echo-Xff":     "1.2.3.4, 127.0.0.1",
		"X-Echo-Xfproto": "http",
		"X-Echo-Xfhost":  "myhost.example",
		"X-Echo-Xfport":  "80",
		"X-Echo-Xrealip": "127.0.0.1",
	}
	for header, want := range checks {
		if got := resp.Header.Get(header); got != want {
			t.Errorf("backend %s = %q, want %q", header, got, want)
		}
	}
	if resp.Header.Get("X-Echo-Xfserver") == "" {
		t.Errorf("backend X-Forwarded-Server is empty, want the local hostname")
	}
}

// TestReverseProxyModifyResponse verifies the per-request response modifier is
// invoked via the request context (replacing oxy's per-request mutation of the
// shared forwarder).
func TestReverseProxyModifyResponse(t *testing.T) {
	sock := newUnixBackend(t)

	called := false
	mod := func(resp *http.Response) error {
		called = true
		resp.Header.Set("X-Test-Modified", "yes")
		return nil
	}
	front := newProxyFront(t, sock, mod)

	resp, err := http.Get(front.URL + "/echo")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if !called {
		t.Errorf("response modifier was not invoked")
	}
	if got := resp.Header.Get("X-Test-Modified"); got != "yes" {
		t.Errorf("X-Test-Modified = %q, want %q", got, "yes")
	}
}

// TestReverseProxyWebsocket is the critical regression guard: it proves the
// stdlib reverse proxy forwards a websocket upgrade through the unix-socket
// Transport (the sole reason the custom oxy fork existed). It also checks that
// the backend observes X-Forwarded-Proto rewritten to "ws".
func TestReverseProxyWebsocket(t *testing.T) {
	sock := newUnixBackend(t)
	front := newProxyFront(t, sock, nil)

	wsURL := "ws://" + strings.TrimPrefix(front.URL, "http://") + "/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial through proxy failed: %v", err)
	}
	defer conn.Close()

	_, first, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read first frame: %v", err)
	}
	if got := string(first); got != "proto=ws" {
		t.Errorf("backend X-Forwarded-Proto frame = %q, want %q", got, "proto=ws")
	}

	want := "hello-websocket"
	if err := conn.WriteMessage(websocket.TextMessage, []byte(want)); err != nil {
		t.Fatalf("write: %v", err)
	}
	_, echo, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read echo: %v", err)
	}
	if string(echo) != want {
		t.Errorf("echo = %q, want %q", string(echo), want)
	}
}

// TestReverseProxyWebsocketConsoleModifier is the B28 regression guard. With a
// console enabled, the real ResponseModifier used to io.ReadAll the body of
// every response — including the 101 Switching Protocols upgrade, whose body is
// the hijacked tunnel — which hung the websocket handshake forever and left a
// non-writable body. The dial must complete (and echo) well within the timeout.
func TestReverseProxyWebsocketConsoleModifier(t *testing.T) {
	gin.SetMode(gin.TestMode)

	prevAdmin := viper.Get("admin-console")
	prevMax := viper.Get("service-console-max-content-length")
	prevTF := viper.Get("time-format")
	viper.Set("admin-console", true)
	viper.Set("service-console-max-content-length", int64(-1))
	viper.Set("time-format", time.RFC3339)
	t.Cleanup(func() {
		viper.Set("admin-console", prevAdmin)
		viper.Set("service-console-max-content-length", prevMax)
		viper.Set("time-format", prevTF)
	})

	sock := newUnixBackend(t)

	hu, err := url.Parse("http://myhost.example")
	if err != nil {
		t.Fatalf("parse url: %v", err)
	}
	holder := &utils.HTTPHolder{HTTPUrl: hu}

	rec := httptest.NewRecorder()
	gctx, _ := gin.CreateTestContext(rec)
	gctx.Request = httptest.NewRequest(http.MethodGet, "http://myhost.example/ws", nil)

	mod := ResponseModifier(nil, "myhost.example", nil, gctx, holder)
	front := newProxyFront(t, sock, mod)

	wsURL := "ws://" + strings.TrimPrefix(front.URL, "http://") + "/ws"

	type dialResult struct {
		conn *websocket.Conn
		err  error
	}
	done := make(chan dialResult, 1)
	go func() {
		conn, _, derr := websocket.DefaultDialer.Dial(wsURL, nil)
		done <- dialResult{conn, derr}
	}()

	select {
	case res := <-done:
		if res.err != nil {
			t.Fatalf("ws dial through proxy with console modifier failed: %v", res.err)
		}
		defer res.conn.Close()

		want := "hello-console-ws"
		if err := res.conn.WriteMessage(websocket.TextMessage, []byte(want)); err != nil {
			t.Fatalf("write: %v", err)
		}
		// First frame is the backend's proto report; the second is the echo.
		if _, _, err := res.conn.ReadMessage(); err != nil {
			t.Fatalf("read first frame: %v", err)
		}
		_, echo, err := res.conn.ReadMessage()
		if err != nil {
			t.Fatalf("read echo: %v", err)
		}
		if string(echo) != want {
			t.Errorf("echo = %q, want %q", string(echo), want)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("websocket dial hung: console ResponseModifier read the 101 upgrade body (B28 regression)")
	}
}
