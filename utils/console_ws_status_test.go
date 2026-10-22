package utils

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/spf13/viper"
)

// sish registers no routes, so gin starts every request at 404. The console's
// websocket must still be logged as the upgrade it is (upstream f411b59).
func TestHandleWebSocketLogsSwitchingProtocols(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.Set("service-console", true)

	console := NewWebConsole()
	console.AddRoute("http://sub.example.com", "token")

	status := make(chan int, 1)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.Next()
		status <- c.Writer.Status()
	}, func(c *gin.Context) {
		console.HandleRequest("http://sub.example.com", false, c)
	})

	server := httptest.NewServer(router)
	t.Cleanup(server.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/_sish/console/ws?x-authorization=token", nil)
	if err != nil {
		t.Fatalf("unable to open the console websocket: %s", err)
	}

	// The handler, and with it the logged status, may only finish once the
	// websocket closes.
	_ = conn.Close()

	select {
	case got := <-status:
		if got != http.StatusSwitchingProtocols {
			t.Errorf("expected status 101, got %d", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("expected the console websocket request to finish")
	}
}
