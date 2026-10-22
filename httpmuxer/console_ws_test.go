package httpmuxer

import (
	"encoding/base64"
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/antoniomika/sish/utils"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
)

// newConsoleFront serves the tunnel like Start does, with a real console:
// requests not recorded by websocket inspection are broadcast once the
// handlers return, as the access logger does. It returns the front and a
// console page's queue, which receives what a connected page would.
func newConsoleFront(t *testing.T, sock string) (*httptest.Server, chan []byte, *utils.WebConsole) {
	t.Helper()

	console := utils.NewWebConsole()
	state := &utils.State{Console: console}

	fwd := NewReverseProxy()
	listener := &utils.HTTPHolder{
		HTTPUrl:  &url.URL{Scheme: "http", Host: "sub.example.com"},
		Forward:  fwd,
		Balancer: newTunnelBalancer(t, fwd, sock),
	}

	route := listener.HTTPUrl.String()
	console.AddRoute(route, "token")

	page := make(chan []byte, 64)
	console.AddClient(route, &utils.WebClient{Console: console, Send: page, Done: make(chan struct{}), Route: route})

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Next()

		if data, ok := c.Get("broadcastData"); ok {
			recordRequestBody(c.Keys, data.(map[string]any))
			console.BroadcastRoute(c.GetString("broadcastRoute"), data.(map[string]any))
		}
	}, gin.Recovery(), recoverAbort, func(c *gin.Context) {
		hostname := c.Request.Host

		reqBody, err := readRequestBody(c)
		if err != nil {
			c.AbortWithStatus(400)
			return
		}

		c.Request = withPublicHost(c.Request, hostname)
		c.Request = withResponseModifier(c.Request, ResponseModifier(state, hostname, reqBody, c, listener))

		gin.WrapH(listener.Balancer)(c)
	})

	front := httptest.NewServer(reraiseAbort(router))
	t.Cleanup(front.Close)

	return front, page, console
}

// nextConsoleMessage returns the next message a console page receives.
func nextConsoleMessage(t *testing.T, page <-chan []byte) map[string]any {
	t.Helper()

	select {
	case raw := <-page:
		var message map[string]any
		if err := json.Unmarshal(raw, &message); err != nil {
			t.Fatalf("console received invalid JSON %q: %s", raw, err)
		}

		return message
	case <-time.After(5 * time.Second):
		t.Fatal("expected a console message")
	}

	return nil
}

// An inspected websocket shows up in the console once, as it opens, followed
// by its messages and its close.
func TestConsoleInspectsWebSocket(t *testing.T) {
	enableConsole(t)
	viper.Set("service-console-history", 25)
	viper.Set("service-console-websocket-messages", 200)

	sock := startTunnelBackend(t)
	front, page, _ := newConsoleFront(t, sock)

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(front.URL, "http")+"/socket", nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %s", err)
	}

	request := nextConsoleMessage(t, page)
	if request["websocket"] != true || request["responseCode"].(float64) != 101 {
		t.Fatalf("expected the upgrade to be recorded as it opens, got %v", request)
	}

	id := request["id"].(float64)

	if err := conn.WriteMessage(websocket.TextMessage, []byte("hi")); err != nil {
		t.Fatalf("unable to write websocket message: %s", err)
	}

	if _, _, err := conn.ReadMessage(); err != nil {
		t.Fatalf("unable to read websocket message: %s", err)
	}

	_ = conn.Close()

	// The two directions are copied by separate goroutines, so their events
	// can arrive in either order.
	seen := map[string]string{}
	for range 2 {
		event := nextConsoleMessage(t, page)

		if event["type"] != "ws-message" || event["requestId"].(float64) != id {
			t.Fatalf("expected a ws-message of request %v, got %v", id, event)
		}

		payload, _ := base64.StdEncoding.DecodeString(event["payload"].(string))
		seen[event["direction"].(string)] = string(payload)
	}

	if seen["client"] != "hi" || seen["server"] != "echo:hi" {
		t.Errorf("expected client %q and server %q messages, got %v", "hi", "echo:hi", seen)
	}

	// The backend closes after one echo; whatever close frames pass, the
	// last event is ws-close, and the request is not recorded a second time.
	for {
		message := nextConsoleMessage(t, page)

		if _, isRequest := message["id"]; isRequest {
			t.Fatalf("expected the websocket to be recorded once, got another request %v", message)
		}

		if message["type"] == "ws-close" {
			break
		}
	}

	front.Close()

	select {
	case raw := <-page:
		var message map[string]any
		_ = json.Unmarshal(raw, &message)
		if _, isRequest := message["id"]; isRequest {
			t.Fatalf("expected the websocket to be recorded once, got another request %s", raw)
		}
	default:
	}
}

// With inspection off, a websocket is still recorded once, when it ends.
func TestConsoleWebSocketWithoutInspection(t *testing.T) {
	enableConsole(t)
	viper.Set("service-console-websocket-messages", 0)

	sock := startTunnelBackend(t)
	front, page, _ := newConsoleFront(t, sock)

	dialer := websocket.Dialer{HandshakeTimeout: 5 * time.Second}

	conn, _, err := dialer.Dial("ws"+strings.TrimPrefix(front.URL, "http")+"/socket", nil)
	if err != nil {
		t.Fatalf("websocket dial failed: %s", err)
	}

	_ = conn.Close()

	request := nextConsoleMessage(t, page)
	if _, ok := request["id"]; !ok || request["websocket"] == true {
		t.Fatalf("expected an uninspected request entry, got %v", request)
	}

	front.Close()

	select {
	case raw := <-page:
		t.Fatalf("expected a single console message, got %s", raw)
	default:
	}
}

// A plain request reaches a connected page live, and a page that connects
// later gets it from the history.
func TestConsoleHistoryReplaysRequests(t *testing.T) {
	enableConsole(t)
	viper.Set("service-console-history", 25)

	sock := startTunnelBackend(t)
	front, page, console := newConsoleFront(t, sock)

	resp, err := getThroughFront(t.Context(), front, "/path?query=1")
	if err != nil {
		t.Fatalf("request failed: %s", err)
	}
	_ = resp.Body.Close()

	live := nextConsoleMessage(t, page)
	if u, _ := live["requestUrl"].(map[string]any); u["Path"] != "/path" || u["RawQuery"] != "query=1" {
		t.Fatalf("expected the live request for /path?query=1, got %v", live)
	}

	front.Close()

	history := console.RequestHistory("http://sub.example.com")
	if len(history) != 1 {
		t.Fatalf("expected one request in history, got %d", len(history))
	}

	var replayed map[string]any
	if err := json.Unmarshal(history[0], &replayed); err != nil {
		t.Fatal(err)
	}

	if replayed["id"] != live["id"] {
		t.Fatalf("expected history to replay request %v, got %v", live["id"], replayed["id"])
	}
}
