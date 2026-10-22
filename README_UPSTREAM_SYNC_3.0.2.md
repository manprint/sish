# Upstream sync — antoniomika/sish up to v3.0.2 (branch `dev`)

Selective port of the 29 upstream commits between the fork point (`4289daa`)
and upstream `v3.0.2` (`1361c51`). The fork replaced oxy's forwarder with its
own `httputil.ReverseProxy` and has a heavily customized console, so the
changes were ported by hand instead of merged. The fork release that ships
this sync is numbered `v3.0.2` to match upstream; it is not upstream's tag
(different commits), so upstream tags must not be fetched over it
(`git config remote.upstream.tagOpt --no-tags`).

## Ported

| Upstream | What | Where |
|---|---|---|
| `17ad1f3` | Round-robin balancer on `vulcand/oxy/v2` (drops the unmaintained oxy v1) | all `roundrobin` imports |
| `738f8a2`, `a3b9c0b` | A response cut off by a dropped tunnel closes the client connection instead of ending as complete (`recoverAbort`/`reraiseAbort`); a body buffered for the console that comes back short yields a 502; one log line naming the public host | `httpmuxer/httpmuxer.go`, `httpmuxer/proxy.go` |
| `f4de59a` | HTTPS tunnels get the request host as TLS server name. Before, the base64 socket address was used, so with `--verify-ssl` (default true) every HTTPS tunnel failed verification | `httpmuxer/proxy.go` (`tunnelTransport`) |
| `ba35c1c`, `f411b59` | Websocket upgrades (proxied and console) logged as 101 instead of 404 — also fixes forwarder logs, history and log-to-client | `httpmuxer/proxy.go`, `utils/console.go` |
| `1361c51` | Console tokens redacted at every occurrence, per-route token included; access log names the host the client asked for, not a rewritten Host header | `httpmuxer/httpmuxer.go` (`redactURI`) |
| `0f087a9`, `3c97622` | Console captures bodies of unknown length as they stream; decodes br, zstd, deflate besides gzip; empty encoded bodies (304) not decoded | `httpmuxer/proxy.go` |
| `52b1114` (part) | Service console request history (`--service-console-history`, default 25) | `utils/console_requests.go` |
| `86d6b30` | Websocket message inspection (`--service-console-websocket-messages`, default 200; 0 disables) | `wsinspect/`, `httpmuxer/websocket.go` |
| `52b1114`, `86d6b30` (UI) | Service console page rebuilt on the fork's look: filters, pause/clear, JSON/hex body views, copy as curl/websocat, replay, websocket message view, reconnect | `templates/console.tmpl` |

Already in the fork before this sync: `0a33aa6` (proxy-proto IPv6), `7c10d2c`
(our B30), `0c20ea6`/`8407b16` (v2.25.0), dependency bumps.

## Fork-specific adaptations

- The fork keeps its own reverse proxy (Director, X-Forwarded-* parity, O3/O4
  tuning, B28 streaming guard, B29 proxySocket); upstream's fixes were applied
  to it rather than switching to oxy/v2's `forward`.
- The request body is still only buffered when a console is enabled (O5).
- Retained data is bounded: request history at 8 MiB per route; websocket
  payloads at `service-console-max-content-length`, or 64 KiB when it is -1.
- A slow console page now gets a `{"type":"dropped"}` notice instead of being
  disconnected (queue 512; the 10s write deadline still closes stuck pages).
- **Privacy:** a console opened later now sees earlier requests of its route,
  headers (`Authorization`, `Cookie`) and bodies included. Set
  `--service-console-history=0` to keep the previous live-only behaviour.

## Not ported

- Embedded templates/assets and the new CSS/docs assets (`52b1114`): packaging
  change; the fork ships `templates/` and keeps `load-templates-directory`.
- Upstream admin routes view (`4861628`): the fork's routes dashboard covers it.
- `requirePost` for the disconnect APIs (`52b1114`): path-based GET may have
  script callers; the admin token already gates it.
- CI: actions/base image pinned to digests, dependabot, docker build cache
  (`ca9ab14`, `0ef6032`).

## Tests

Upstream tests ported and adapted to `NewReverseProxy`: `pipeline_test.go`
(dropped tunnels, logged status, chunked capture, client abort),
`transport_test.go`, `redact_test.go`, `proxy_console_test.go`,
`wsinspect/parser_test.go`, console websocket status. New: console history
bounds/removal (`utils/console_requests_test.go`) and end-to-end console
websocket inspection (`httpmuxer/console_ws_test.go`). The UI was checked in
headless Chromium against a live sish with XSS payloads in path, headers,
bodies and websocket messages.
