# Full Review — October 2026 (branch `review/full-audit-2026-10`)

Continuation of the 2026 audit (`README_FULL_REVIEW_2026.md`, fixes B1–B27).
New fixes are tagged **B28+** in code comments. This file is the working
ledger; each item has a status: `TODO`, `WIP`, `DONE`.

Scope requested: bug/race/deadlock hunt, perf optimisation, complexity
reduction, deep frontend review (templates + console backend coherence),
apply fixes, bump Go toolchain + deps, logging coherence, add/verify tests,
commit on this branch.

Baseline before work: `go build ./...`, `go vet ./...` clean;
`go test ./... -count=1 -race` PASS (httpmuxer ~1s, sshmuxer ~9s, utils ~2.7s).

Note: tokensave index currently serves branch `main`, not this branch — grep
override used for symbol lookups during the audit. Re-sync the index after merge.

---

## Critical / correctness

| Tag | File | Issue | Status |
|-----|------|-------|--------|
| B28 | httpmuxer/proxy.go | `ResponseModifier` runs for 101 Switching Protocols: `io.ReadAll(resp.Body)` on the hijacked upgrade conn hangs the websocket and the `NopCloser` swap yields "101 response with non-writable body". Skip body capture for 101 + `text/event-stream`; still broadcast metadata. | DONE |
| B29 | httpmuxer/proxy.go | `proxySocket` set only inside the console gate → with only `log-to-client`/forwarders-log enabled, the access-log fan-out broadcasts to every backend on the route instead of the one that served the request. Set it above the gate. | DONE |
| B30 | utils/utils.go | `loadKeys` WalkDir callback calls `d.IsDir()` when `d == nil` (missing dir) → nil-pointer panic, also at runtime via the key watcher. Return the walk error when `d == nil`. | DONE |
| B31 | utils/utils.go | `loadKeys`/`loadPrivateKeys` log `err` instead of `e` in "Can't read file" messages → wrong/empty error. | DONE |
| B32 | utils/utils.go | `GetRandomPortInRange` `Intn(hi-lo)` excludes `hi`, panics when `lo==hi`/empty range, unbounded recursion when all ports busy. | DONE |
| B58 | sshmuxer/tcphandler.go | Follow-up to B32: `GetRandomPortInRange` now returns 0 on an exhausted/malformed range (instead of the old infinite recursion). `GetOpenPort` then yields port 0 and `handleTCPListener` bound `":0"`, letting the kernel pick an ephemeral port **outside the configured `port-bind-range`** — a silent policy bypass. Refuse the forward when a range is configured and the resolved port is 0. Port 0 stays the legitimate kernel-chosen sentinel only when no range is set. | DONE |
| B33 | utils/state.go | `TCPHolder.Handle` proxy-protocol header decided from the FIRST ssh conn in `Range` (always `return false`) instead of the selected backend → wrong source with load balancing. Resolve the owning conn once. | DONE |
| B34 | utils/state.go | `TCPHolder.Handle` accept loop `break`s on any error (e.g. EMFILE) → forward dies / goes dirty. Treat transient errors without killing the forward. | DONE |
| B35 | utils/census.go | On census-url transient failure the URL-sourced IDs vanish and the strict enforcer disconnects all URL-censed clients. Keep last-good URL entries on error, record `LastError`. | DONE |
| B36 | utils/census.go | Concurrent refreshes (ticker, manual, editcensus save) can interleave; an older slow refresh can overwrite newer data. Serialise with a refresh mutex. | DONE |
| B37 | utils/census.go | `downloadCensusBody` `io.ReadAll` unbounded. Apply a generous size limit + log when exceeded. | DONE |
| B38 | utils/headers_settings.go | Subdomain overrides were resolved in isolation, so a partial override (`enabled:true` with no value, or an `always`-only override) was dropped by `toResolvedHeaderSetting` (`ok=false`) instead of merging against the matching default. Fixed the merge so each override field (value / enabled / always) inherits from the default when the override omits it, per README_HEADERS.md; `enabled:true` without a value and with no default value supplying one is still skipped. Tests cover inherit-value, enabled:false removal, and always-only (`always:false`) override cases. | DONE |
| B39 | sshmuxer/requests.go | `onCleanup` closures mutate shared holders (RemoveServer/empty-check/state-map delete/listener close/RemoveRoute) without the bind-allocation lock → race with concurrent allocation reusing the holder. Take `utils.LockBindAllocation()` in cleanup. | DONE |

## Races / sync / robustness

| Tag | File | Issue | Status |
|-----|------|-------|--------|
| B40 | utils/conn.go | `closeBoth` called by both copy goroutines → second `Close` of the ssh channel returns `io.EOF`, logged "Error closing reader: EOF" every teardown. Wrap in `sync.Once`. | DONE |
| B41 | utils/conn.go | `rateLimitedReader.waitForTokens` uses `context.Background()` → throttled copies can't be cancelled on teardown. Cancel via a ctx closed by `closeBoth`. | DONE |
| B42 | utils/conn.go | `SetBandwidthProfile` copies counters Store-before-swap (lost bytes). Swap then Add. | DONE |
| B43 | utils/listen.go | multilistener: Accept-after-Close returns `multilistener.ErrClosed` (not `net.ErrClosed`) so `errors.Is(err, net.ErrClosed)` checks fail; partial failure leaks listeners; late conns leak. Replace with a small in-tree multi-listener + `ListenerPort` helper; drop the dependency. | DEFERRED (see notes) |
| B44 | utils/sshmux_listener.go | dispatch `continue`s on non-ErrClosed accept errors without backoff (busy loop on EMFILE). | DONE |
| B45 | utils/forwarder_logs.go | `cleanupForwardersLogWriters` takes exclusive Lock on every write before the interval check → serialises logging. Atomic last-cleanup check before locking. | DONE |
| B57 | httpmuxer/https.go, utils/state.go, utils/sshmux_listener.go, sshmuxer/sshmuxer.go, sshmuxer/requests.go | Accept loops handled transient errors inconsistently: the HTTPS SNI loop returned on **any** Accept error → `close(ready)` → `ServeTLS` returns → `log.Fatal` **killed the whole process** on a single EMFILE; the two SSH loops busy-looped (`continue` with no backoff); the TCP-forward loop had a 100-failure give-up cap that could drop a healthy forward. Unify on a shared `utils.AcceptBackoff` (net/http-style 5ms→1s exponential) + `utils.IsListenerClosedErr`: exit only on a closed listener, otherwise back off and retry, never giving up on the listener. Verified the closed-error propagates through the go-proxyproto v0.16 wrapper (its `Accept` returns the underlying listener's error verbatim; our `ConnPolicy` never returns an Accept-time error, and REJECT surfaces on Read in the per-conn goroutine, so the loop never backs off on a per-connection policy error). Covered by `AcceptBackoff` progression tests and `IsListenerClosedErr` sentinel + live `ListenerHolder` Accept-after-Close tests. | DONE |

## HTTP hardening

| Tag | File | Issue | Status |
|-----|------|-------|--------|
| B46 | httpmuxer/httpmuxer.go | logger `param.Keys["originalURI"].(string)` unchecked assertion. | DONE |
| B47 | httpmuxer/httpmuxer.go | token redaction uses `strings.Replace n=1` and only for admin/global service tokens; auto-generated per-route `x-authorization=` query values logged in plaintext. Redact via regex. | DONE |
| B48 | httpmuxer/httpmuxer.go | log-to-client blocking `SendMessage` in request path → `TrySendMessage`. | DONE |
| B49 | httpmuxer/httpmuxer.go | tunnel basic-auth compared with `==` → `crypto/subtle`. | DONE |
| B50 | httpmuxer/httpmuxer.go + https.go | `http.Server` without `ReadHeaderTimeout`/`IdleTimeout` (slowloris). Add both; never Read/Write timeout (kills ws/SSE/uploads). | DONE |

## Frontend / console backend

| Tag | File | Issue | Status |
|-----|------|-------|--------|
| B51 | utils/console.go | `HandleLogsFile` clamped `lines` only AFTER `tailFileLines`, so the JSON `lines` field misreported the value used. (tailFileLines already clamps internally, so not an overflow.) Clamp at the handler for consistency/defense-in-depth. | DONE |
| B52 | utils/console.go | History CSV filename `time.Now().Format("20060201")` → YYYYDDMM. Should be `"20060102"`. Audit census/audit CSV for the same. | DONE |
| B53 | templates/* | Deep frontend review (jQuery + Knockout + Bootstrap). XSS: no raw `innerHTML`/`document.write`; the routes info modal, audit forwarders + reason modal and census source badges escape untrusted data via a proper `escapeHtml`/`$('<span>').text().html()`; jQuery is 3.7.1 (patched for CVE-2020-11022/23). AJAX: all `/_sish/...` endpoints referenced in templates map to registered handlers; dispatcher in HandleRequest enforces auth + feature flags, specific routes before general. Path traversal on edit/logs files is guarded by the `resolve*File` helpers. Save/validate payload contracts ({file,content}/{content}) match the handlers. **Superseded in part:** a workflow-assisted second pass (B59–B64) found one `.html()` sink this pass missed (internal.tmpl ping-fail modal) plus a Knockout binding bug and two un-gated GET routes. | DONE |

## Frontend / console backend — round 2 (workflow-assisted re-audit)

| Tag | File | Issue | Status |
|-----|------|-------|--------|
| B59 | templates/internal.tmpl | `showPingFailWindows` built the fail-window modal body by concatenating backend fields (`closeReason`/`closeInitiator` are server-set free text from `SetCloseInfo`, can echo client-influenced input; plus from/to/recoveredAt/closedAt) and injected it via `.html()` with no escaping → DOM-based XSS in the admin console. Added an `escapeHtml` helper and escaped every interpolated value. This was the only unescaped `.html()` sink in internal.tmpl (B53 had not covered this template). | DONE |
| B60 | templates/routes.tmpl | Census indicator bound `css: { 'cid-green': isCensused, ... }` — `isCensused` is a `ko.observable`, so uncalled it is the (always-truthy) function object and the green dot showed for every client regardless of census status. Call `isCensused()`. | DONE |
| B64 | utils/console.go | `/_sish/api/clients` and the generic `/_sish/api/internal` routes lacked the `MethodGet` guard that the sibling read endpoints (audit, ping-status, metrics) enforce. Added the 405 gate to both (defense-in-depth; templates already use GET). | DONE |

## Logging / config coherence

| Tag | File | Issue | Status |
|-----|------|-------|--------|
| B61 | cmd/sish.go | viper `OnConfigChange` raised the logrus level to Debug when `debug=true` but had no else branch, so once a hot reload enabled debug the level stayed at Debug for the process lifetime even after a later reload set `debug=false`. Added the symmetric `logrus.SetLevel(logrus.InfoLevel)` reset. | DONE |

## Behaviour / performance

| Tag | File | Issue | Status |
|-----|------|-------|--------|
| B62 | sshmuxer/sshmuxer.go | **Behaviour change.** Per-connection strip-path was only set in `handleSession`; a client that never opens a session channel (`ssh -N -R`) kept the zero value `false`. In httpmuxer the per-conn value is compared against the global default and `false != true` forced stripping OFF for that forward, so `-N` clients silently diverged from session clients and from the documented default (`strip-http-path` defaults to true). Seed `SetStripPath(viper.GetBool("strip-http-path"))` at `holderConn` construction. `-N` clients now honour the default; operators wanting no stripping set `--strip-http-path=false` (now applied uniformly). | DONE |
| B63 | sshmuxer/requests.go, sshmuxer/channels.go, utils/conn.go | `handleRemoteForward` (processed sequentially in the request loop) and `handleAlias` each waited up to 1s for the client's exec request before proceeding, restarting the timeout per forward/channel. `sshConn.Exec` is closed once (broadcast), so exec-mode clients only ever wait once; but a non-exec client (`-N` with several forwards, or a visitor opening many alias channels) paid a fresh 1s every time. Replaced the per-call `time.After(1s)` with a per-connection deadline set lazily on the first wait (`ExecWaitDeadline()` = now+1s, once) — the first forward keeps the full historical 1s window (anchored at processing time, not handshake, so high-RTT exec detection is unchanged) while later forwards share the deadline and stop paying a fresh 1s. Unit-tested for stability. | DONE |
| B65 | sshmuxer/requests.go | The per-forward banner `SendMessage(mainRequestMessages, true)` was sent inline, before the forward's accept goroutine started. `SendMessage(block=true)` blocks until the client drains its message queue (or the connection closes); the queue is buffered (`messageQueueSize = 64`) and a `-N` client with no session drains nothing for the first ~2s (until `checkSession`'s fallback drainer), so once that buffer fills the inline send stalls the forward's accept loop. The client already has the successful `Reply(true)`, so the banner is now sent on its own goroutine and the accept loop starts immediately. Minor behavioural note: banners from concurrently-started forwards may now arrive out of order. | DONE |

## Go / deps / tooling

| Tag | Item | Status |
|-----|------|--------|
| B54 | Bump Go toolchain to latest 1.27.x (go.mod, Dockerfile, workflows, CLAUDE.md). | DONE |
| B55 | `go get -u ./... && go mod tidy`; `govulncheck` if present. Own commit. | DONE |
| B56 | utils/state.go `LogWriter.Write` returned `fmt.Fprintf`'s count (prefix included), violating `io.Writer` (n must be ≤ len(p)). Return `len(p)`. | DONE |

## Notes on items intentionally NOT changed / deferred

- **B43 (replace the multilistener dependency): deferred.** The main correctness
  risk it carried — Accept-after-Close returning `multilistener.ErrClosed` rather
  than `net.ErrClosed` so close detection failed — is now neutralised by the
  `utils.IsListenerClosedErr` helper used in the TCP accept loop (B34) and the SSH
  mux dispatcher (B44). Replacing the dependency outright (ordered addresses,
  close-on-partial-failure, late-conn handling, dropping the type assertion at
  `requests.go:505`) is a larger change with its own regression surface and is
  left as a follow-up. The existing `tH.Listener.Addr().(*multilistener.MultiListener)`
  assertion in requests.go is unchanged.
- Dirty-forward 2+ cycle threshold: intentional, kept.
- audit.go O(n) eviction at 10k under lock: acceptable, kept.
- TeeConn TLS-peek buffer: NOT shrunk below ~16KB+5 (PQ ClientHellos are large).
- headers_settings.go per-response resolution: left as-is (O(configured headers),
  small); precomputing at load time was judged not worth the added complexity/risk.
- **Unused JSON response fields, kept.** The round-2 audit noted a few emitted-but-
  ignored keys: `rejectReasonsText` (console.go `HandleAudit`; the template builds
  its own summary from `rejectReasons`), and `status`/`lines` in the logs
  endpoints. They are harmless; removing them is a response-contract change with no
  functional benefit (and `status:true` is a convention across the console APIs),
  so they are left in place.
- **logrus imported but not used for application logging, kept.** `logrus` is
  configured (output + level) but the code logs via the stdlib `log` package.
  `logrus.SetOutput(multiWriter)` still routes any dependency that logs through
  logrus into the same sinks, so the configuration is not dead. Converting the
  codebase to logrus (or removing it) is an invasive change out of scope for this
  audit; only the level-reset bug (B61) was fixed.
- **Watcher boilerplate dedupe (WatchCerts/Keys/AuthUsers/headers), deferred.**
  The four watchers share a similar poll/reload shape, but there is no watcher
  test coverage and each has subtle per-target reload semantics; factoring them
  into one helper carries real regression risk for little gain. The simplification
  actually delivered this round was the shared `utils.AcceptBackoff` /
  `utils.IsListenerClosedErr` across the five accept loops (B57).
