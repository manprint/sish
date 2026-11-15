# Upstream sync — antoniomika/sish v4.1.0 (selective)

Backport of the only functional commit between upstream `v4.0.0` (`6227b1b`)
and `v4.1.0` (`9e527c5`): `69962b8` *fix(security): harden tunnel binding,
routing and consoles*. The other four commits in the range are dependency
bumps already superseded in the fork (brotli 1.2.8, ipfilter 1.5.13,
go-proxyproto 0.16.0, oxy/v2 2.3.0, x/net 0.62.0).

Upstream tags `v3.0.3`–`v4.1.0` are not the fork's tags (different commits);
keep `git config remote.upstream.tagOpt --no-tags`.

## Why only part of the commit

The rest of `69962b8` hardens features this deployment does not enable
(`sni-proxy-https`, `bind-wildcards`, `proxy-protocol-listener`,
`tcp-aliases-allowed-users`, `https-ondemand-certificate`, auth HTTP requests).
Those guards, plus the console route-ID and slog changes, are deliberately not
ported.

## Ported

| Upstream | What | Where | Tests |
|---|---|---|---|
| `69962b8` (subset) | `--http-trusted-proxies`: comma separated IPs/CIDRs of reverse proxies. Gin now takes `ClientIP` only from those addresses, and forwarding headers (`Forwarded`, `X-Forwarded-*`, `X-Real-IP`) sent by anyone else are removed before the request reaches a tunnel. Empty trusts no proxy, so logs and apps behind tunnels no longer see visitor-supplied addresses. | `cmd/sish.go`, `utils/utils.go` (`ParsePrefix`), `httpmuxer/httpmuxer.go` (`setTrustedProxies`, `dropForwardHeaders`), `config.example.yml`, `docs/posts/cli.md` | `httpmuxer/trust_test.go` |

The IP filter still checks the connecting address as before; with trusted
proxies configured, `ClientIP` in logs and the service console is the visitor
address from the proxy instead of a visitor-controlled header.

## Behaviour change

Deployments behind a reverse proxy must set `--http-trusted-proxies` (or
`http-trusted-proxies` in the config file) to the proxy addresses, or that
proxy's forwarding headers are stripped. Without the flag the default is
"trust nobody", which is safe but loses the visitor address.

## Not ported

- `SNINameTaken`, `HTTPHostBound`, `hostsOverlap`, `validBindHost`: only
  reachable with `--sni-proxy-https`/`--bind-wildcards`, both off.
- `selectAliasBackend`: only reachable with `--tcp-aliases-allowed-users`.
- `--proxy-protocol-trusted-upstreams`: only reachable with
  `--proxy-protocol-listener`.
- `--https-ondemand-certificate-wildcards`: on-demand issuance is off.
- `onDemandAllowed`, auth-request redirect/limit hardening, `RedactRoute`/route
  IDs, `localhost-as-all` loopback matching, `RemoveTCPHolder`/`ListenerPort`:
  fork has equivalents or the behaviour is not reachable.
- Dependency bumps (fork already newer) and CI changes.

## Tests

`go build ./...`, `go vet ./...`, `go test ./... -count=1 -race`.
