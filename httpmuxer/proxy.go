package httpmuxer

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
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

	"github.com/andybalholm/brotli"
	"github.com/antoniomika/sish/utils"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/spf13/viper"
	oxyutils "github.com/vulcand/oxy/v2/utils"
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

// maxTLSTransports bounds how many server names one forwarder keeps
// transports for, since a wildcard listener can see any number of hosts.
const maxTLSTransports = 256

// RoundTripper returns the specific handler for unix connections. This
// will allow us to use our created sockets cleanly.
func RoundTripper() http.RoundTripper {
	dialer := func(ctx context.Context, network, addr string) (net.Conn, error) {
		realAddr, err := base64.StdEncoding.DecodeString(strings.Split(addr, ":")[0])
		if err != nil {
			// Dialling an empty/garbage path produced a confusing connection
			// error far from the cause; surface the decode failure instead.
			log.Println("Unable to parse socket:", err)
			return nil, fmt.Errorf("unable to parse backend socket address: %w", err)
		}

		var d net.Dialer

		return d.DialContext(ctx, "unix", string(realAddr))
	}

	tlsConfig := &tls.Config{
		InsecureSkipVerify: !viper.GetBool("verify-ssl"),
	}

	// Tune for keep-alive reuse to the unix-socket backends and larger socket
	// buffers (O3). Defaults are MaxIdleConnsPerHost=2 and 4KB read/write
	// buffers, which throttle busy backends and add syscalls under load.
	return &tunnelTransport{
		base: &http.Transport{
			DialContext:         dialer,
			TLSClientConfig:     tlsConfig,
			MaxIdleConns:        256,
			MaxIdleConnsPerHost: 64,
			IdleConnTimeout:     90 * time.Second,
			WriteBufferSize:     reverseProxyBufferSize,
			ReadBufferSize:      reverseProxyBufferSize,
		},
		byName: map[string]*http.Transport{},
	}
}

// tunnelTransport sends HTTPS requests to a tunnel with the request's host as
// the TLS server name (upstream f4de59a). The URL host is the base64-encoded
// socket address, which the transport would otherwise use as the server name:
// with verify-ssl on (the default) every HTTPS tunnel failed certificate
// verification, and with it off the backend got no usable SNI. Each name gets
// its own transport so a connection made for one host is never reused for
// another.
type tunnelTransport struct {
	base *http.Transport

	mu     sync.Mutex
	byName map[string]*http.Transport
}

func (t *tunnelTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.Host == "" {
		return t.base.RoundTrip(req)
	}

	return t.forName(tlsServerName(req.Host)).RoundTrip(req)
}

func (t *tunnelTransport) forName(name string) *http.Transport {
	t.mu.Lock()
	defer t.mu.Unlock()

	if transport, ok := t.byName[name]; ok {
		return transport
	}

	if len(t.byName) >= maxTLSTransports {
		for evicted, transport := range t.byName {
			transport.CloseIdleConnections()
			delete(t.byName, evicted)
			break
		}
	}

	// Clone keeps the O3 tuning and deep-copies TLSClientConfig.
	transport := t.base.Clone()
	transport.TLSClientConfig.ServerName = name
	t.byName[name] = transport

	return transport
}

// tlsServerName returns host without its port or IPv6 brackets.
func tlsServerName(host string) string {
	if name, _, err := net.SplitHostPort(host); err == nil {
		return name
	}

	return strings.TrimSuffix(strings.TrimPrefix(host, "["), "]")
}

// respModKey is the request-context key under which the per-request response
// modifier is stored. The shared per-host *httputil.ReverseProxy reads it in
// its (static) ModifyResponse so the modifier is no longer mutated on the
// shared forwarder per request (which was a data race under concurrent load).
type respModKey struct{}

// publicHostKey is the request context key for the host set by withPublicHost.
type publicHostKey struct{}

// withPublicHost returns a copy of req that carries the host the client asked
// for, so the proxy's logs name it after a host-header rewrite changed
// req.Host.
func withPublicHost(req *http.Request, host string) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), publicHostKey{}, host))
}

// publicHost returns the host set by withPublicHost, or the Host header.
func publicHost(req *http.Request) string {
	if host, ok := req.Context().Value(publicHostKey{}).(string); ok {
		return host
	}

	return req.Host
}

// loggingBody logs a failed read of an upstream response body, which is how a
// tunnel that drops mid-response shows up. Both readers of the body stop at
// the first error, so it logs at most once.
type loggingBody struct {
	io.ReadCloser

	host string
	ctx  context.Context
}

func (b *loggingBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)

	// A canceled context means the client went away, not the tunnel.
	if err != nil && err != io.EOF && b.ctx.Err() == nil {
		log.Printf("Error reading response body from %s: %s", b.host, err)
	}

	return n, err
}

// proxyErrorHandler answers a request the proxy could not complete.
func proxyErrorHandler(w http.ResponseWriter, req *http.Request, err error) {
	// Once an upgrade has hijacked the connection there is no status left to
	// send.
	if gw, ok := w.(gin.ResponseWriter); ok && gw.Written() {
		log.Printf("Error switching protocols for %s: %s", publicHost(req), err)
		return
	}

	// The client went away; there is nobody to answer and nothing to report.
	if req.Context().Err() != nil {
		w.WriteHeader(oxyutils.StatusClientClosedRequest)
		return
	}

	// The tunnel closed before the response was complete (a body buffered for
	// the console came back short). Send a 502 instead of the partial body.
	if errors.Is(err, io.ErrUnexpectedEOF) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(http.StatusText(http.StatusBadGateway)))
		return
	}

	log.Printf("Error proxying request to %s: %s", publicHost(req), err)
	oxyutils.DefaultHandler.ServeHTTP(w, req, err)
}

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
//
// A response cut off by a dropped tunnel is logged once by loggingBody (the
// proxy's own copy-error log is discarded) and the proxy then panics with
// http.ErrAbortHandler, which recoverAbort/reraiseAbort turn into a closed
// client connection instead of a response that looks complete.
func NewReverseProxy() *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Transport:     RoundTripper(),
		FlushInterval: 100 * time.Millisecond,
		BufferPool:    ReverseProxyBufferPool(),
		ErrorLog:      log.New(io.Discard, "", 0),
		ErrorHandler:  proxyErrorHandler,
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
			if resp.Request == nil {
				return nil
			}

			if resp.Body != nil && resp.StatusCode != http.StatusSwitchingProtocols {
				resp.Body = &loggingBody{
					ReadCloser: resp.Body,
					host:       publicHost(resp.Request),
					ctx:        resp.Request.Context(),
				}
			}

			if fn, ok := resp.Request.Context().Value(respModKey{}).(func(*http.Response) error); ok && fn != nil {
				return fn(resp)
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

		// B29: record the backend that served this request unconditionally, not
		// only when a console is enabled. The access-log fan-out (log-to-client
		// and forwarders log in httpmuxer.go) uses proxySocket to target the
		// owning SSH connection; without it those features broadcast every log
		// line to all backends on the route. Only set it on a successful decode
		// so the existing "unknown socket" fallback still applies otherwise.
		if response.Request != nil {
			if hostLocation, decErr := base64.StdEncoding.DecodeString(response.Request.URL.Host); decErr == nil {
				c.Set("proxySocket", string(hostLocation))
			} else {
				log.Println("Error loading proxy info from request", decErr)
			}
		}

		// The proxy writes a 101 to the hijacked connection, past gin's writer,
		// so the access log, forwarder logs and history reported every
		// successful websocket upgrade as the 404 gin presets for unrouted
		// requests (upstream ba35c1c). Nothing has been written to the client
		// yet, so setting the status here only records it.
		if response.StatusCode == http.StatusSwitchingProtocols {
			c.Status(http.StatusSwitchingProtocols)
		}

		if !consoleEnabled() {
			return nil
		}

		// An inspected websocket is recorded as it opens, so the logger must
		// not record it again when it closes.
		if isWebsocketResponse(response) && wsInspectionEnabled() && inspectWebSocket(state, hostname, reqBody, c, currentListener, response) {
			return nil
		}

		limit := viper.GetInt64("service-console-max-content-length")
		encoding := response.Header.Get("Content-Encoding")

		data := requestData(c, hostname, reqBody)
		data["responseHeaders"] = response.Header
		data["responseBody"] = base64.StdEncoding.EncodeToString(notCapturedResponse)

		c.Set("broadcastRoute", currentListener.HTTPUrl.String())
		c.Set("broadcastData", data)

		// B28: never read the body of a 101 Switching Protocols response or
		// a streaming (text/event-stream) response. httputil.ReverseProxy
		// calls ModifyResponse BEFORE handleUpgradeResponse, so the 101
		// Body is the raw hijacked tunnel: io.ReadAll would hang the
		// websocket and the NopCloser swap would trip "101 switching
		// protocols response with non-writable body". We still broadcast
		// the request metadata, just with a placeholder body.
		contentType := strings.ToLower(response.Header.Get("Content-Type"))
		if response.StatusCode == http.StatusSwitchingProtocols || strings.HasPrefix(contentType, "text/event-stream") {
			data["responseBody"] = base64.StdEncoding.EncodeToString(notCapturedStream)
			return nil
		}

		switch {
		case limit == -1 || (limit > -1 && response.ContentLength > -1 && response.ContentLength < limit):
			resBody, readErr := io.ReadAll(response.Body)
			response.Body = io.NopCloser(bytes.NewBuffer(resBody))

			data["responseBody"] = base64.StdEncoding.EncodeToString(decodeConsoleBody(resBody, encoding, limit))

			// A cut-off body is still recorded, and the error makes the proxy
			// send a 502 in place of the partial response (upstream 738f8a2).
			return readErr
		case limit > -1 && response.ContentLength == -1:
			// Waiting for the whole body would hold up streamed responses, so
			// it is recorded as it passes through to the client (upstream
			// 0f087a9).
			response.Body = &consoleCapture{
				ReadCloser:  response.Body,
				encoding:    encoding,
				limit:       limit,
				notCaptured: notCapturedResponse,
				onClose: func(body []byte) {
					data["responseBody"] = base64.StdEncoding.EncodeToString(body)
				},
			}
		}

		return nil
	}
}

// notCapturedRequest, notCapturedResponse and notCapturedStream stand in for
// bodies the console didn't record.
var (
	notCapturedRequest  = []byte("{\"_sish_status\": false, \"_sish_message\": \"request body size exceeds limit for service console\"}")
	notCapturedResponse = []byte("{\"_sish_status\": false, \"_sish_message\": \"response body size exceeds limit for service console\"}")
	notCapturedStream   = []byte("{\"_sish_status\": false, \"_sish_message\": \"streaming response not captured for service console\"}")
)

// readRequestBody prepares the request body of c for the console. It returns
// the body when it can be read before forwarding. A body of unknown length is
// recorded as the tunnel reads it instead, and recordRequestBody adds it to
// the console data once the request is done.
func readRequestBody(c *gin.Context) ([]byte, error) {
	limit := viper.GetInt64("service-console-max-content-length")

	switch {
	case limit == -1 || (limit > -1 && c.Request.ContentLength > -1 && c.Request.ContentLength < limit):
		reqBody, err := io.ReadAll(c.Request.Body)
		if err != nil {
			return nil, err
		}

		c.Request.Body = io.NopCloser(bytes.NewBuffer(reqBody))

		return reqBody, nil
	case limit > -1 && c.Request.ContentLength == -1:
		capture := &consoleCapture{ReadCloser: c.Request.Body, limit: limit, notCaptured: notCapturedRequest}
		c.Request.Body = capture
		c.Set("requestCapture", capture)

		return nil, nil
	}

	return notCapturedRequest, nil
}

// recordRequestBody adds a request body recorded by readRequestBody to data.
// The tunnel can still be reading the body after the response arrives, so
// this waits until the request is logged.
func recordRequestBody(keys map[any]any, data map[string]any) {
	if capture, ok := keys["requestCapture"].(*consoleCapture); ok {
		data["requestBody"] = base64.StdEncoding.EncodeToString(capture.recorded())
	}
}

// consoleCapture records up to limit bytes of a body for the console as it is
// read, so a body of unknown length doesn't have to be held back first.
type consoleCapture struct {
	io.ReadCloser

	encoding    string
	limit       int64
	notCaptured []byte

	// onClose, if set, is passed the recorded body when the body is closed.
	onClose func(body []byte)

	mu   sync.Mutex
	buf  bytes.Buffer
	over bool
	once sync.Once
}

func (b *consoleCapture) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)

	b.mu.Lock()
	defer b.mu.Unlock()

	if !b.over {
		if int64(b.buf.Len()+n) >= b.limit {
			b.over = true
			b.buf = bytes.Buffer{}
		} else {
			b.buf.Write(p[:n])
		}
	}

	return n, err
}

func (b *consoleCapture) Close() error {
	if b.onClose != nil {
		b.once.Do(func() { b.onClose(b.recorded()) })
	}

	return b.ReadCloser.Close()
}

// recorded returns the body read so far without its Content-Encoding, or
// notCaptured if it reached the limit.
func (b *consoleCapture) recorded() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.over {
		return b.notCaptured
	}

	return decodeConsoleBody(b.buf.Bytes(), b.encoding, b.limit)
}

// decodeConsoleBody returns body with its Content-Encoding removed so the
// console can show it. A body that can't be decoded is returned as-is, and
// one that decodes past limit is replaced by notCapturedResponse. A backend
// may advertise an encoding and send something else; that used to dereference
// a nil *gzip.Reader and panic the request (B18).
func decodeConsoleBody(body []byte, encoding string, limit int64) []byte {
	// A 304 can carry a Content-Encoding with no body (upstream 3c97622).
	if len(body) == 0 {
		return body
	}

	var reader io.Reader
	var err error

	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "gzip", "x-gzip":
		var gzReader *gzip.Reader
		gzReader, err = gzip.NewReader(bytes.NewReader(body))
		if err == nil {
			defer func() { _ = gzReader.Close() }()
			reader = gzReader
		}
	case "deflate":
		// Deflate should be zlib wrapped, but some servers send it raw.
		reader, err = zlib.NewReader(bytes.NewReader(body))
		if err != nil {
			reader, err = flate.NewReader(bytes.NewReader(body)), nil
		}
	case "br":
		reader = brotli.NewReader(bytes.NewReader(body))
	case "zstd":
		var decoder *zstd.Decoder
		decoder, err = zstd.NewReader(bytes.NewReader(body), zstd.WithDecoderConcurrency(1))
		if err == nil {
			defer decoder.Close()
			reader = decoder
		}
	default:
		return body
	}

	if err != nil {
		log.Printf("Error decoding %s console body: %s", encoding, err)
		return body
	}

	// limit+1 so a body that decodes to exactly limit bytes is detected as
	// over the limit below without reading an unbounded decompression bomb.
	if limit > -1 {
		reader = io.LimitReader(reader, limit+1)
	}

	decoded, err := io.ReadAll(reader)
	if err != nil {
		log.Printf("Error decoding %s console body: %s", encoding, err)
		return body
	}

	if limit > -1 && int64(len(decoded)) >= limit {
		return notCapturedResponse
	}

	return decoded
}

func consoleEnabled() bool {
	return viper.GetBool("admin-console") || viper.GetBool("service-console")
}

// requestData returns the request fields the console shows for c.
func requestData(c *gin.Context, hostname string, reqBody []byte) map[string]any {
	startTime := c.GetTime("startTime")

	requestHeaders := c.Request.Header.Clone()
	requestHeaders.Add("Host", hostname)

	return map[string]any{
		"startTime":          startTime,
		"startTimePretty":    startTime.Format(viper.GetString("time-format")),
		"requestIP":          c.ClientIP(),
		"requestMethod":      c.Request.Method,
		"requestUrl":         c.Request.URL,
		"originalRequestURI": c.GetString("originalURI"),
		"requestHeaders":     requestHeaders,
		"requestBody":        base64.StdEncoding.EncodeToString(reqBody),
	}
}
