package httpmuxer

import (
	"bytes"
	"compress/gzip"
	"crypto/tls"
	"encoding/base64"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/antoniomika/sish/utils"
	"github.com/gin-gonic/gin"
	"github.com/spf13/viper"
)

// reverseProxyBufferSize is the per-stream copy buffer size used by the HTTP
// reverse proxy. httputil.ReverseProxy defaults to a fresh 32KB buffer per
// streamed response; pooling a larger one cuts allocations and syscalls on
// bulk HTTP transfers.
const reverseProxyBufferSize = 64 * 1024

type reverseProxyBufferPool struct {
	pool sync.Pool
}

func (b *reverseProxyBufferPool) Get() []byte  { return *(b.pool.Get().(*[]byte)) }
func (b *reverseProxyBufferPool) Put(p []byte) { b.pool.Put(&p) }

var sharedReverseProxyBufferPool = &reverseProxyBufferPool{
	pool: sync.Pool{New: func() any {
		b := make([]byte, reverseProxyBufferSize)
		return &b
	}},
}

// ReverseProxyBufferPool returns the shared buffer pool for oxy's underlying
// httputil.ReverseProxy (O4).
func ReverseProxyBufferPool() httputil.BufferPool { return sharedReverseProxyBufferPool }

// RoundTripper returns the specific handler for unix connections. This
// will allow us to use our created sockets cleanly.
func RoundTripper() *http.Transport {
	dialer := func(network, addr string) (net.Conn, error) {
		realAddr, err := base64.StdEncoding.DecodeString(strings.Split(addr, ":")[0])
		if err != nil {
			log.Println("Unable to parse socket:", err)
		}

		return net.Dial("unix", string(realAddr))
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: !viper.GetBool("verify-ssl"),
	}

	// Tune for keep-alive reuse to the unix-socket backends and larger socket
	// buffers (O3). Defaults are MaxIdleConnsPerHost=2 and 4KB read/write
	// buffers, which throttle busy backends and add syscalls under load.
	return &http.Transport{
		Dial:                dialer,
		TLSClientConfig:     tlsConfig,
		MaxIdleConns:        256,
		MaxIdleConnsPerHost: 64,
		IdleConnTimeout:     90 * time.Second,
		WriteBufferSize:     reverseProxyBufferSize,
		ReadBufferSize:      reverseProxyBufferSize,
	}
}

// respModKey is the request-context key under which the per-request response
// modifier is stored. The shared per-host *httputil.ReverseProxy reads it in
// its (static) ModifyResponse so the modifier is no longer mutated on the
// shared forwarder per request (which was a data race under concurrent load).
type respModKey struct{}

// forwardedServerHostname is the local hostname advertised in X-Forwarded-Server,
// matching oxy's HeaderRewriter default (resolved once at startup).
var forwardedServerHostname = func() string {
	h, err := os.Hostname()
	if err != nil {
		return "localhost"
	}
	return h
}()

// NewReverseProxy builds the shared per-host reverse proxy. It replaces oxy's
// forward.Forwarder while preserving its behavior:
//   - streaming responses (FlushInterval, matching oxy's Stream(true)),
//   - PassHostHeader(true): the client Host header is forwarded untouched,
//   - the unix-socket RoundTripper used for both HTTP and websocket upgrades
//     (Go's httputil.ReverseProxy proxies websocket upgrades through Transport,
//     which is why the custom oxy fork is no longer needed),
//   - oxy's HeaderRewriter X-Forwarded-* headers (see applyForwardedHeaders),
//   - the pooled copy buffer (O4) and the per-request ResponseModifier.
func NewReverseProxy() *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport:     RoundTripper(),
		FlushInterval: 100 * time.Millisecond,
		BufferPool:    ReverseProxyBufferPool(),
		ErrorLog:      log.Default(),
		Director: func(out *http.Request) {
			// The round-robin balancer rewrites out.URL to the chosen backend
			// (scheme + base64-encoded unix socket as host) but drops the path
			// and query, so recover them from RequestURI like oxy did, then
			// clear RequestURI (required for an outbound client request).
			if out.RequestURI != "" {
				if parsed, err := url.ParseRequestURI(out.RequestURI); err == nil {
					out.URL.Path = parsed.Path
					out.URL.RawPath = parsed.RawPath
					out.URL.RawQuery = parsed.RawQuery
				}
			}
			out.RequestURI = ""

			out.Proto = "HTTP/1.1"
			out.ProtoMajor = 1
			out.ProtoMinor = 1

			// PassHostHeader(true): leave out.Host as the client's Host header.
			applyForwardedHeaders(out)
		},
		ModifyResponse: func(resp *http.Response) error {
			if resp.Request != nil {
				if fn, ok := resp.Request.Context().Value(respModKey{}).(func(*http.Response) error); ok && fn != nil {
					return fn(resp)
				}
			}
			return nil
		},
	}
}

// applyForwardedHeaders mirrors oxy's HeaderRewriter (TrustForwardHeader=true):
// it sets the X-Forwarded-* and X-Real-Ip headers only when absent, never
// stripping client-provided values. X-Forwarded-For is intentionally NOT set
// here: httputil.ReverseProxy appends it automatically in Director mode for
// both plain HTTP and websocket upgrades, so setting it here would duplicate it.
func applyForwardedHeaders(req *http.Request) {
	if clientIP, _, err := net.SplitHostPort(req.RemoteAddr); err == nil {
		// clean up an ipv6 zone suffix, e.g. "fe80::1%eth0".
		clientIP = strings.Split(clientIP, "%")[0]
		if req.Header.Get("X-Real-Ip") == "" {
			req.Header.Set("X-Real-Ip", clientIP)
		}
	}

	if req.Header.Get("X-Forwarded-Proto") == "" {
		if req.TLS != nil {
			req.Header.Set("X-Forwarded-Proto", "https")
		} else {
			req.Header.Set("X-Forwarded-Proto", "http")
		}
	}

	if isWebsocketUpgrade(req) {
		switch req.Header.Get("X-Forwarded-Proto") {
		case "https":
			req.Header.Set("X-Forwarded-Proto", "wss")
		case "http":
			req.Header.Set("X-Forwarded-Proto", "ws")
		}
	}

	if req.Header.Get("X-Forwarded-Port") == "" {
		req.Header.Set("X-Forwarded-Port", forwardedPort(req))
	}

	if req.Header.Get("X-Forwarded-Host") == "" && req.Host != "" {
		req.Header.Set("X-Forwarded-Host", req.Host)
	}

	if forwardedServerHostname != "" {
		req.Header.Set("X-Forwarded-Server", forwardedServerHostname)
	}
}

// isWebsocketUpgrade reports whether req is a websocket handshake, matching
// oxy's IsWebsocketRequest. Safe to call inside Director: hop-by-hop headers
// are stripped by ReverseProxy only after Director returns.
func isWebsocketUpgrade(req *http.Request) bool {
	contains := func(name, value string) bool {
		for _, item := range strings.Split(req.Header.Get(name), ",") {
			if value == strings.ToLower(strings.TrimSpace(item)) {
				return true
			}
		}
		return false
	}
	return contains("Connection", "upgrade") && contains("Upgrade", "websocket")
}

// forwardedPort derives the X-Forwarded-Port value, matching oxy's logic.
func forwardedPort(req *http.Request) string {
	if _, port, err := net.SplitHostPort(req.Host); err == nil && port != "" {
		return port
	}

	if proto := req.Header.Get("X-Forwarded-Proto"); proto == "https" || proto == "wss" {
		return "443"
	}

	if req.TLS != nil {
		return "443"
	}

	return "80"
}

// ResponseModifier implements a response modifier for the specified request.
// We don't actually modify any requests, but we do want to record the request
// so we can send it to the web console.
func ResponseModifier(state *utils.State, hostname string, reqBody []byte, c *gin.Context, currentListener *utils.HTTPHolder) func(*http.Response) error {
	return func(response *http.Response) error {
		utils.ApplyForwarderHeaders(response.Header, hostname, response.StatusCode)

		if viper.GetBool("admin-console") || viper.GetBool("service-console") {
			var err error
			var resBody []byte

			if viper.GetInt64("service-console-max-content-length") == -1 || (viper.GetInt64("service-console-max-content-length") > -1 && response.ContentLength > -1 && response.ContentLength < viper.GetInt64("service-console-max-content-length")) {
				resBody, err = io.ReadAll(response.Body)
				if err != nil {
					log.Println("Error reading response body:", err)
				}
			}

			if resBody != nil {
				response.Body = io.NopCloser(bytes.NewBuffer(resBody))

				if response.Header.Get("Content-Encoding") == "gzip" {
					gzData := bytes.NewBuffer(resBody)
					gzReader, err := gzip.NewReader(gzData)
					if err != nil {
						log.Println("Error reading gzip data:", err)
					}

					resBody, err = io.ReadAll(gzReader)
					if err != nil {
						log.Println("Error reading gzip data:", err)
					}
				}
			} else {
				resBody = []byte("{\"_sish_status\": false, \"_sish_message\": \"response body size exceeds limit for service console\"}")
			}

			startTime := c.GetTime("startTime")

			requestHeaders := c.Request.Header.Clone()
			requestHeaders.Add("Host", hostname)

			data := map[string]any{
				"startTime":          startTime,
				"startTimePretty":    startTime.Format(viper.GetString("time-format")),
				"requestIP":          c.ClientIP(),
				"requestMethod":      c.Request.Method,
				"requestUrl":         c.Request.URL,
				"originalRequestURI": c.GetString("originalURI"),
				"requestHeaders":     requestHeaders,
				"requestBody":        base64.StdEncoding.EncodeToString(reqBody),
				"responseHeaders":    response.Header,
				"responseBody":       base64.StdEncoding.EncodeToString(resBody),
			}

			if response.Request != nil {
				hostLocation, err := base64.StdEncoding.DecodeString(response.Request.URL.Host)
				if err != nil {
					log.Println("Error loading proxy info from request", err)
				}

				c.Set("proxySocket", string(hostLocation))
			}

			c.Set("broadcastRoute", currentListener.HTTPUrl.String())
			c.Set("broadcastData", data)
		}

		return nil
	}
}
