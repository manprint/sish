package httpmuxer

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/antoniomika/sish/utils"
	"github.com/antoniomika/sish/wsinspect"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// wsInspectDefaultLimit caps the payload kept per websocket message when
// service-console-max-content-length is -1, since inspected messages are
// retained in the console history rather than only streamed.
const wsInspectDefaultLimit = 64 * 1024

// wsInspectionEnabled reports whether the console inspects websocket messages.
func wsInspectionEnabled() bool {
	return consoleEnabled() && viper.GetInt("service-console-websocket-messages") > 0
}

// inspectWebSocket shows an upgraded websocket connection in the console as
// soon as it opens, then reports each message and the close as they pass
// through the tunnel (upstream 86d6b30). It returns false, leaving the
// response untouched, if the route has no console or the upgrade can't be
// inspected; the request is then recorded when it ends, as before.
//
// Everything the callbacks need is captured here: they run on the proxy's
// copy goroutines, possibly after gin has recycled c.
func inspectWebSocket(state *utils.State, hostname string, reqBody []byte, c *gin.Context, currentListener *utils.HTTPHolder, response *http.Response) bool {
	if state == nil || state.Console == nil {
		return false
	}

	// httputil.ReverseProxy hijacks the upgrade through this assertion, so the
	// wrapper below must keep the body an io.ReadWriteCloser.
	backend, ok := response.Body.(io.ReadWriteCloser)
	if !ok {
		return false
	}

	console := state.Console
	route := currentListener.HTTPUrl.String()
	opened := time.Now()

	data := requestData(c, hostname, reqBody)
	data["websocket"] = true
	data["currentTime"] = opened.Format(viper.GetString("time-format"))
	data["responseHeaders"] = response.Header
	data["responseBody"] = ""
	data["responseCode"] = response.StatusCode
	data["responseStatus"] = fmt.Sprintf("%d %s", response.StatusCode, http.StatusText(response.StatusCode))
	data["requestTime"] = opened.Sub(c.GetTime("startTime")).Round(10 * time.Microsecond).String()

	id := console.BroadcastRoute(route, data)
	if id == 0 {
		return false
	}

	var seq atomic.Uint64
	event := func(kind string, fields map[string]any) {
		fields["type"] = kind
		fields["seq"] = seq.Add(1)
		fields["time"] = time.Now()
		console.BroadcastEvent(route, id, fields)
	}

	limit := viper.GetInt("service-console-max-content-length")
	if limit < 0 {
		limit = wsInspectDefaultLimit
	}

	response.Body = wsinspect.NewConn(backend, response.Header, limit, wsinspect.Handler{
		Message: func(dir wsinspect.Direction, m wsinspect.Message) {
			fields := map[string]any{
				"direction":  dir,
				"opcode":     wsinspect.OpcodeName(m.Opcode),
				"size":       m.Size,
				"payload":    base64.StdEncoding.EncodeToString(m.Payload),
				"truncated":  m.Truncated,
				"compressed": m.Compressed,
				"undecoded":  m.Undecoded,
			}

			if m.Opcode == wsinspect.OpClose {
				fields["closeCode"] = m.CloseCode
				fields["closeReason"] = m.CloseReason
			}

			event("ws-message", fields)
		},
		Error: func(dir wsinspect.Direction, err error) {
			event("ws-error", map[string]any{"direction": dir, "error": err.Error()})
		},
		Close: func() {
			event("ws-close", map[string]any{"duration": time.Since(opened).Round(time.Millisecond).String()})
		},
	})

	return true
}

// isWebsocketResponse reports whether response completes a websocket upgrade.
func isWebsocketResponse(response *http.Response) bool {
	return response.StatusCode == http.StatusSwitchingProtocols && strings.EqualFold(response.Header.Get("Upgrade"), "websocket")
}
