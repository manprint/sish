# sish

An open source serveo/ngrok alternative.

[Read the docs.](https://docs.ssi.sh)

## Recent updates

- SSH over HTTPS multiplexing on port `443` (`--ssh-over-https`)
- Forced takeover for in-use targets (`force-connect=true`, guarded by `--enable-force-connect`)
- Unified command passing from both SSH exec args and `SISH_*` environment variables
- Connection metadata support: `id`, `note`, `note64`
- Admin dashboard improvements: ID column, live duration, notes modal, compact session/fingerprint cells
- Dedicated admin history page at `/_sish/history` (in-memory, CSV export, search, pagination, gated by `--history-enabled`)
- Admin audit page at `/_sish/audit` (origin IP statistics + bandwidth snapshot)
- Admin forwarder logs page at `/_sish/logs` (tail/search/download, gated by `--forwarders-log=enable`)
- Forwarder logs readability hardening (ANSI color escape sequences stripped for web console)
- Admin headers editor page at `/_sish/editheaders` (edit YAML headers config from web UI)
- Admin census editor page at `/_sish/editcensus` (edit local census YAML files from web UI)
- Census dual-source support: remote URL (`--census-url`) + local directory (`--census-directory`) with merge
- Census source control: `--strict-id-censed-url` and `--strict-id-censed-files` for fine-grained enforcement
- Census per-ID optional notes and per-ID source tracking (URL vs files)
- Census dashboard: forward column, notes column in Proxy Censed/Uncensed sections
- Internal runtime status page at `/_sish/internal` (memory, goroutines, active forwards, dirty listeners, startup flags)
- Info modal ingress section showing connection type (SSH/Multiplexer) and port
- History page ingress column showing how each connection was established
- Strict unique connection ID enforcement (`--strict-unique-ip`): reject forwards when ID is already in use
- Disconnect confirmation modal on sish page
- Per-user bandwidth hot-reload without restart (`--bandwidth-hot-reload-enabled`, `--bandwidth-hot-reload-time`)
- Dockerfile migrated from scratch to Alpine 3.23 (with nano, wget, tzdata)

## New server flags

- `--ssh-over-https=true` enables SSH ingress on the HTTPS listener (requires `--https=true`)
- `--enable-force-connect=true` allows clients to request takeover via `force-connect=true`
- `--history-enabled=true|false` controls history page/API visibility in admin frontend
- `--show-internal-state=true|false` enables internal runtime status page/API
- `--bandwidth-hot-reload-enabled=true|false` enables periodic hot-reload of per-user bandwidth limits
- `--bandwidth-hot-reload-time=20s` configures hot-reload interval
- `--strict-id-censed-url=true|false` controls census enforcement from remote URL
- `--strict-id-censed-files=true|false` controls census enforcement from local files + editcensus page
- `--strict-unique-ip=true|false` rejects forwards when the connection ID is already in use

- Frontend: Forwarder column added to both the history page and the clients (sish) page; shows subdomain, TCP port (":8080") or TCP alias ("alias:7777"). CSV export and search include this field.
- Frontend: SSH "Session ID" and "SSH Pubkey Fingerprint" were removed from the clients table and moved into the Info modal under a dedicated "SSH" section. Values are truncated in the modal with a "Show and Copy" action that opens the value modal for full view and copy. Value modal z-index adjusted so it appears above the Info modal.
- Frontend: Audit page (Origin IP Stats) now shows "Show" buttons for Last Reject Reason and Reject Reasons Summary when data exists; otherwise displays "None". The buttons open a modal with a clean, ordered list of reasons. by another active connection

Example:

```bash
go run main.go \
	--ssh-address=:2222 \
	--http-address=:80 \
	--https=true \
	--https-address=:443 \
	--ssh-over-https=true \
	--enable-force-connect=true \
	--admin-console=true \
	--admin-console-token='change-me'
```

## Command passing (new)

Tunnel options can be passed in two ways:

- Remote command args (traditional): `ssh ... 'force-https=true note=hello'`
- Environment mode (recommended for autossh): `SISH_*` + `-o SendEnv=...`

Common examples:

- `SISH_FORCE_CONNECT=true`
- `SISH_ID=nginx-001`
- `SISH_NOTE='maintenance tunnel'`
- `SISH_NOTE64=<base64>`

## Admin console routes

- Clients page: `/_sish/console?x-authorization=<admin-token>`
- History page: `/_sish/history?x-authorization=<admin-token>`
- Audit page: `/_sish/audit?x-authorization=<admin-token>`
- Logs page: `/_sish/logs?x-authorization=<admin-token>`
- Census page: `/_sish/census?x-authorization=<admin-token>`
- Internal page: `/_sish/internal?x-authorization=<admin-token>`
- Edit Keys: `/_sish/editkeys` (Basic Auth)
- Edit Users: `/_sish/editusers` (Basic Auth)
- Edit Headers: `/_sish/editheaders` (Basic Auth)
- Edit Census: `/_sish/editcensus` (Basic Auth)

## Project README index

- `README_COMMANDS.md` - full command/env mapping and autossh-safe examples
- `README_FORCE_CONNECT.md` - forced takeover behavior and operational notes
- `README_SSH_SSL.md` - SSH over HTTPS setup and multiplexing behavior
- `README_CONSOLLE.md` - admin dashboard: clients, notes, stats, history, census, internal pages
- `README_NGINX_REAL_IP.md` - nginx stream/proxy-protocol setup for real client IP in sish
- `README_FWLOGS.md` - dedicated per-forwarder logs, console logs page, retention/rotation knobs
- `SSH_SISH_CLIENT.md` - docker SSH client (non-root, env-driven tunnels, autorestart, log rotation)
- `docker-client/README.md` - complete docker-client runbook with full env reference and usage cases
- `README_USERS.md` - SSH authentication with per-user YAML passwords and live reload
- `README_HEADERS.md` - managed response headers by YAML with defaults/overrides
- `README_CENSUS.md` - census page, APIs, dual-source (URL + files), strict enforcement, per-ID notes and source tracking
- `README_USER_BANDWIDTH_LIMIT.md` - per-user upload/download limits from auth-users YAML, with optional hot-reload
- `README_SPECS_01_02.md` - development documentation for Spec 01 (editheaders) and Spec 02 (census directory + editcensus)
- `README_SPECS_03.md` - development documentation for Spec 03 (census source control, strict-id-censed-url/files split)
- `PLAN_BANDW_DEV.md` - bandwidth hot-reload development plan (Spec 14, implemented)
- `README_ANALISYS.md` - production-grade analysis: architecture, robustness, memory, concurrency
- `README_SYNC_UPSTREAM.md` - guide to sync fork with upstream repository

## dev

Clone the `sish` repo:

```bash
git clone git@github.com:antoniomika/sish.git
cd sish
```

Add your SSH public key:

```bash
cp ~/.ssh/id_ed25519.pub ./deploy/pubkeys
```

Run the binary:

```bash
go run main.go --http-address localhost:3000 --domain testing.ssi.sh
```

We have an alias `make dev` for running the binary.

SSH to your host to communicate with sish:

```bash
ssh -p 2222 -R 80:localhost:8080 testing.ssi.sh
```
> The `testing.ssi.sh` DNS record points to `localhost` so anyone can use it for
> development
