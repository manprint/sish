# Upstream sync — antoniomika/sish after v3.0.2 (up to v4.0.0)

Selective port of the 10 upstream commits between upstream `v3.0.2`
(`1361c51`, the end of [README_UPSTREAM_SYNC_3.0.2.md](README_UPSTREAM_SYNC_3.0.2.md))
and upstream `main` / `v4.0.0` (`6227b1b`, 2026-10-27). The fork keeps its own
code paths (lifecycle manager, reverse proxy, console), so each commit was
checked against them and ported by hand when a cherry-pick conflicted.
The release that ships this sync is `v3.1.0`. Upstream tags `v3.0.3`–`v4.0.0`
are not the fork's tags (different commits); keep
`git config remote.upstream.tagOpt --no-tags`.

Go: already on the latest release (`1.27.3`, checked against go.dev). Direct and
indirect dependencies were bumped with `go get -u ./... && go mod tidy`
(brotli 1.2.7, compress 1.20.3, ipfilter 1.5.12, vulcand/oxy/v2 2.3.0,
x/net 0.62.0, sonic 1.15.5).

## Ported

| Upstream | What | Where | Tests |
|---|---|---|---|
| `06ed6ac` | A second `session` channel on one SSH connection is rejected (`ssh.Prohibited`). The fork already made the double `close()` idempotent (B8); `CloseSessionChannel()` now returns whether this call opened the session | `utils/conn.go`, `sshmuxer/handle.go` | `TestSessionChannels/second_session_rejected`, `/shell_then_exec` |
| `554f344` | Console request filter: space-separated terms (all must match), `"quoted phrase"`, `-term` to exclude | `templates/console.tmpl` (`textFilter`) | `templates/console_filter_test.go` (runs the real function under `node`, skipped when `node` is absent) |
| `68e6850` | TXT lookup for `_sish.<host>` uses a fully qualified name (trailing dot) so the resolver does not retry under its search domains. Kept the fork's lookup timeout | `utils/utils.go` (`dnsTXTName`) | `TestDNSTXTName` |
| `a4f07a2` | `forwarded-tcpip` originator is always an IP (RFC 4254). A hostname bind used to put the hostname in `OriginAddr`, which strict clients (`x/crypto/ssh`) reject | `sshmuxer/requests.go` | `TestForwardedOriginAddrIsAlwaysAnIP` (helper `forwardedOriginAddr`; an unspecified bind also falls back to loopback). The upstream end-to-end test was verified to fail without the fix but not kept: its tunnel goroutines read viper after it ends and race the next test under `-race` |
| `86b3d45` | `--http-read-header-timeout` (30s) and `--http-idle-timeout` (2m) replace the fixed B50 constants (`newServer`); idle tunnel connections expire at half of `--idle-connection-timeout` when `--idle-connection` is on, so a reused connection is never already closed by the tunnel side; session requests that want a reply get one (`pty-req` accepted, others refused) instead of hanging the client; `--service-console-max-content-length` defaults to 1 MiB (was -1, unlimited; -1 still means everything) | `cmd/sish.go`, `httpmuxer/httpmuxer.go`, `httpmuxer/proxy.go`, `sshmuxer/channels.go` | `TestNewServerClosesStalledConnections`, `TestTunnelIdleConnTimeout`, `TestSessionChannels/requests_replied` |
| `54f04cc` (subset) | `--log-format=text\|json` for the server log. JSON writes one `{"time","level","msg"}` record per line; default text output is unchanged | `utils/state.go` (`LogWriter`), `cmd/sish.go` | `TestLogWriterJSON`, `TestLogWriterTextUnchanged` |
| `ba752d0` | Dependency bump | `go.mod`, `go.sum` | full suite |

Already in the fork: the request body is only buffered when a console is on
(O5), `MaxIdleConnsPerHost` 64 (O3), request-body reading for the console.

## Fork-specific adaptations

- The fork's `env` session request is accepted (it carries `SISH_*` options);
  upstream refuses `env`. `TestSessionChannels/requests_replied` therefore asserts `env`
  is answered, not refused.
- The fork's test server serialises connections under `viperConfigLock`, so
  the session tests use one SSH connection at a time on one shared server
  (`TestSessionChannels`). Session goroutines read viper after a test ends, so
  `sshmuxer/session_test.go` must stay the last test file of the package
  (a later `viper.Set` would race them under `-race`).
- **Behaviour change:** the service console records at most 1 MiB per body by
  default. Set `service-console-max-content-length: -1` for the old behaviour.

## Not ported

- `ea291dc`, `8cbebb5`, `54f04cc` (rest), `6227b1b` — the slog migration.
  It rewrites ~100 log call sites in `sshmuxer/`, `httpmuxer/` and `utils/`,
  drops `--time-format`, `LogWriter` and logrus, changes the default log line
  to slog text (`time=… level=… source=… msg=…`), and adds the client-side
  `log-format=json` option and quieting of expected disconnect errors that
  depend on it. The fork's forwarder logs, audit, log-to-client and many tests
  match the current log lines, so it would break log consumers and tests for
  no functional gain. Only the server-side `--log-format=json` was taken, in a
  form that leaves the default output as it is. Revisit as its own project.
- `4861628` (routes view): the fork's routes dashboard covers it (see the
  3.0.2 sync).
- CI changes (`ca9ab14`, `0ef6032`, dependabot, digest pinning).
- Embedded templates and assets (`52b1114`): the fork ships `templates/`.

## Tests

New or ported tests are listed per row above. Regression: `go build ./...`,
`go vet ./...`, `go test ./... -count=1 -race`.
