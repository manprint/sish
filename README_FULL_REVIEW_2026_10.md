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
| B33 | utils/state.go | `TCPHolder.Handle` proxy-protocol header decided from the FIRST ssh conn in `Range` (always `return false`) instead of the selected backend → wrong source with load balancing. Resolve the owning conn once. | DONE |
| B34 | utils/state.go | `TCPHolder.Handle` accept loop `break`s on any error (e.g. EMFILE) → forward dies / goes dirty. Treat transient errors without killing the forward. | DONE |
| B35 | utils/census.go | On census-url transient failure the URL-sourced IDs vanish and the strict enforcer disconnects all URL-censed clients. Keep last-good URL entries on error, record `LastError`. | DONE |
| B36 | utils/census.go | Concurrent refreshes (ticker, manual, editcensus save) can interleave; an older slow refresh can overwrite newer data. Serialise with a refresh mutex. | DONE |
| B37 | utils/census.go | `downloadCensusBody` `io.ReadAll` unbounded. Apply a generous size limit + log when exceeded. | DONE |
| B38 | utils/headers_settings.go | Subdomain override `enabled:true` without value should inherit the default value AND apply the override `always`, but `toResolvedHeaderSetting` returns `ok=false` → override skipped. Merge semantics fixed per README_HEADERS.md. | DONE |
| B39 | sshmuxer/requests.go | `onCleanup` closures mutate shared holders (RemoveServer/empty-check/state-map delete/listener close/RemoveRoute) without the bind-allocation lock → race with concurrent allocation reusing the holder. Take `utils.LockBindAllocation()` in cleanup. | DONE |

## Races / sync / robustness

| Tag | File | Issue | Status |
|-----|------|-------|--------|
| B40 | utils/conn.go | `closeBoth` called by both copy goroutines → second `Close` of the ssh channel returns `io.EOF`, logged "Error closing reader: EOF" every teardown. Wrap in `sync.Once`. | DONE |
| B41 | utils/conn.go | `rateLimitedReader.waitForTokens` uses `context.Background()` → throttled copies can't be cancelled on teardown. Cancel via a ctx closed by `closeBoth`. | DONE |
| B42 | utils/conn.go | `SetBandwidthProfile` copies counters Store-before-swap (lost bytes). Swap then Add. | DONE |
| B43 | utils/listen.go | multilistener: Accept-after-Close returns `multilistener.ErrClosed` (not `net.ErrClosed`) so `errors.Is(err, net.ErrClosed)` checks fail; partial failure leaks listeners; late conns leak. Replace with a small in-tree multi-listener + `ListenerPort` helper; drop the dependency. | TODO |
| B44 | utils/sshmux_listener.go | dispatch `continue`s on non-ErrClosed accept errors without backoff (busy loop on EMFILE). | DONE |
| B45 | utils/forwarder_logs.go | `cleanupForwardersLogWriters` takes exclusive Lock on every write before the interval check → serialises logging. Atomic last-cleanup check before locking. | DONE |

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
| B53 | templates/* | Deep review: every `fetch()` path/method/JSON field vs handler; `AbortWithError` returns non-JSON (JS `.json()` throws); `innerHTML`/template-literal sinks with attacker-controlled data (usernames, subdomains, IDs, census notes, URIs, reject reasons) must be escaped. | TODO |

## Go / deps / tooling

| Tag | Item | Status |
|-----|------|--------|
| B54 | Bump Go toolchain to latest 1.27.x (go.mod, Dockerfile, workflows, CLAUDE.md). | DONE |
| B55 | `go get -u ./... && go mod tidy`; `govulncheck` if present. Own commit. | DONE |
| B56 | utils/state.go `LogWriter.Write` returned `fmt.Fprintf`'s count (prefix included), violating `io.Writer` (n must be ≤ len(p)). Return `len(p)`. | DONE |

## Notes on items intentionally NOT changed

- Dirty-forward 2+ cycle threshold: intentional, kept.
- audit.go O(n) eviction at 10k under lock: acceptable, kept.
- TeeConn TLS-peek buffer: NOT shrunk below ~16KB+5 (PQ ClientHellos are large).
