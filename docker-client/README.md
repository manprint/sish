# docker-client - SSH client container for sish

This folder provides a production-oriented SSH client container to open and keep tunnels towards a sish server.

It is fully environment-driven, supports non-root execution, supports password and key authentication, supports `-R`/`-L`/`-D`, supports internal auto-restart, and writes both forwarder output logs and session stats logs with rotation policies.

## Contents

- `Dockerfile`
- `entrypoint.sh`
- `scripts/build-ssh-command.sh`
- `scripts/run-client.sh`
- `scripts/log-rotate.sh`
- `compose.yml`

## Design goals

- Non-root runtime by default (`1000:1000`) with runtime override.
- Dynamic SSH command generation from env vars.
- Full support for tunnel types:
  - reverse: `-R`
  - local: `-L`
  - dynamic socks: `-D`
- Auth modes:
  - key
  - password (non interactive via `sshpass`)
  - auto fallback
- Internal reconnect loop independent from Docker restart policy.
- Dual logs:
  - forwarder output log
  - connection lifecycle stats log
- Rotation/retention by max size and max age.

## Runtime flow

1. `entrypoint.sh`
- Ensures user/group exists.
- Ensures paths exist (`/app/log`, `/app/ssh`, home `.ssh`).
- Drops privileges to configured UID/GID using `su-exec`.
- Starts `scripts/run-client.sh`.

2. `scripts/run-client.sh`
- Prepares log files.
- Rotates logs before and after each session.
- Builds command via `scripts/build-ssh-command.sh`.
- Starts SSH.
- Captures SSH output to forwarder log.
- Writes connect/disconnect/restart stats.
- Applies internal restart policy.

3. `scripts/build-ssh-command.sh`
- Translates env vars into final SSH argv.
- Adds `-R`, `-L`, `-D` from semicolon-separated lists.
- Adds `-o` options and `SendEnv` mappings.
- Supports SSH behavior tuning entirely via environment variables.

4. `scripts/log-rotate.sh`
- Rotates if current file exceeds size threshold.
- Rotates if file age exceeds hour threshold.
- Keeps only newest `N` rotated files.

## Complete environment variables reference

### A) Runtime identity and user model

- `SISH_CLIENT_UID`
  - default: `1000`
  - used by: `entrypoint.sh`
- `SISH_CLIENT_GID`
  - default: `1000`
  - used by: `entrypoint.sh`
- `SISH_CLIENT_USER`
  - default: `sishclient`
- `SISH_CLIENT_GROUP`
  - default: `sishclient`
- `SISH_CLIENT_HOME`
  - default: `/home/${SISH_CLIENT_USER}`

### B) SSH target and session

- `SSH_HOST`
  - default: `tuns.0912345.xyz`
- `SSH_PORT`
  - default: `2222`
- `SSH_USER`
  - default: empty
- `SSH_TARGET`
  - default: computed
  - behavior:
    - if set, it has priority
    - else if `SSH_USER` set -> `${SSH_USER}@${SSH_HOST}`
    - else -> `${SSH_HOST}`

### C) Authentication

- `SSH_AUTH_MODE`
  - default: `auto`
  - values:
    - `key`: always use key mode
    - `password`: always use password mode
    - `auto`: use key if key file exists, otherwise password if provided
- `SSH_PRIVATE_KEY_PATH`
  - default: `/app/ssh/id_ed25519`
- `SSH_IDENTITY_ONLY`
  - default: `yes`
- `SSH_PASSWORD`
  - default: empty
  - required when `SSH_AUTH_MODE=password`

### D) Tunnel configuration

- `SSH_REMOTE_FORWARDS`
  - default: empty
  - format: semicolon separated list
  - each item becomes: `-R <item>`
  - examples:
    - `mysub:80:localhost:8080`
    - `9001:localhost:9001`
    - `myalias:9111:localhost:9111`
- `SSH_LOCAL_FORWARDS`
  - default: empty
  - each item becomes: `-L <item>`
- `SSH_DYNAMIC_FORWARDS`
  - default: empty
  - each item becomes: `-D <item>`

### E) SSH behavior flags/options

The following options are always passable via env vars and map directly to SSH `-o` flags:

- `SSH_STRICT_HOST_KEY_CHECKING`
  - default: `no`
  - maps to: `-o StrictHostKeyChecking=...`
- `SSH_USER_KNOWN_HOSTS_FILE`
  - default: `/tmp/ssh_known_hosts`
  - maps to: `-o UserKnownHostsFile=...`
- `SSH_SERVER_ALIVE_INTERVAL`
  - default: `30`
  - maps to: `-o ServerAliveInterval=...`
- `SSH_SERVER_ALIVE_COUNT_MAX`
  - default: `3`
  - maps to: `-o ServerAliveCountMax=...`
- `SSH_CONNECT_TIMEOUT`
  - default: `10`
  - maps to: `-o ConnectTimeout=...`
- `SSH_EXIT_ON_FORWARD_FAILURE`
  - default: `yes`
  - maps to: `-o ExitOnForwardFailure=...`
- `SSH_COMPRESSION`
  - default: `no`
- `SSH_LOG_LEVEL`
  - default: `INFO`
- `SSH_OPTIONS`
  - default: empty
  - format: semicolon separated list
  - each item becomes: `-o <item>`
  - example:
    - `PubkeyAuthentication=no;PreferredAuthentications=password`
- `SSH_EXTRA_ARGS`
  - default: empty
  - appended as raw split by spaces
  - examples:
    - `-v`
    - `-4 -v`

### F) sish command integration (`SISH_*`)

- `SSH_SEND_ENV`
  - default:
    - `SISH_PROXY_PROTOCOL,SISH_PROXYPROTO,SISH_HOST_HEADER,SISH_STRIP_PATH,SISH_SNI_PROXY,SISH_TCP_ADDRESS,SISH_TCP_ALIAS,SISH_LOCAL_FORWARD,SISH_AUTO_CLOSE,SISH_FORCE_HTTPS,SISH_TCP_ALIASES_ALLOWED_USERS,SISH_DEADLINE,SISH_ID,SISH_NOTE,SISH_NOTE64,SISH_FORCE_CONNECT`
  - each comma-separated var becomes `-o SendEnv=<VAR>` only if that env var is set and non-empty in the container
- Typical variables you can set in container env:
  - `SISH_ID`
  - `SISH_NOTE`
  - `SISH_NOTE64`
  - `SISH_FORCE_CONNECT`
  - `SISH_FORCE_HTTPS`
  - `SISH_PROXY_PROTOCOL`
  - `SISH_TCP_ADDRESS`
  - `SISH_TCP_ALIAS`
  - `SISH_LOCAL_FORWARD`
- `SISH_REMOTE_COMMAND`
  - default: empty
  - if set -> appended as remote command string

### G) Internal reconnect policy

- `SISH_CLIENT_AUTORESTART`
  - default: `true`
- `SISH_CLIENT_RESTART_DELAY_SECONDS`
  - default: `3`
- `SISH_CLIENT_MAX_RETRIES`
  - default: `0`
  - `0` means unlimited retries

### H) Log file paths

- `FORWARDER_LOG_FILE`
  - default: `/app/log/forwarder/outputs.log`
- `STATS_LOG_FILE`
  - default: `/app/log/stats/stats.log`

### I) Rotation and retention

Forwarder log:
- `FORWARDER_LOG_MAX_SIZE_MB` (default: `100`)
- `FORWARDER_LOG_MAX_AGE_HOURS` (default: `168`)
- `FORWARDER_LOG_MAX_FILES` (default: `30`)

Stats log:
- `STATS_LOG_MAX_SIZE_MB` (default: `20`)
- `STATS_LOG_MAX_AGE_HOURS` (default: `720`)
- `STATS_LOG_MAX_FILES` (default: `60`)

## Build

From repository root:

```bash
docker build -t sish-client:dev -f docker-client/Dockerfile .
```

## Usage modes and examples

### 1) Reverse HTTP tunnel with key auth

```bash
docker run --rm -it \
  -e SSH_HOST=tuns.0912345.xyz \
  -e SSH_PORT=443 \
  -e SSH_USER=alpha \
  -e SSH_AUTH_MODE=key \
  -e SSH_REMOTE_FORWARDS='mysub:80:localhost:8080' \
  -e SISH_ID='client-http-01' \
  -e SISH_FORCE_HTTPS='true' \
  -v "$PWD/docker-client/ssh:/app/ssh:ro" \
  -v "$PWD/docker-client/log:/app/log" \
  sish-client:dev
```

### 2) Reverse TCP and alias with password auth

```bash
docker run --rm -it \
  -e SSH_HOST=tuns.0912345.xyz \
  -e SSH_PORT=2222 \
  -e SSH_USER=beta \
  -e SSH_AUTH_MODE=password \
  -e SSH_PASSWORD='change-me' \
  -e SSH_REMOTE_FORWARDS='9001:localhost:9001;myalias:9111:localhost:9111' \
  -e SSH_OPTIONS='PubkeyAuthentication=no;PreferredAuthentications=password' \
  -v "$PWD/docker-client/log:/app/log" \
  sish-client:dev
```

### 3) Local forward and dynamic socks

```bash
docker run --rm -it \
  -e SSH_HOST=tuns.0912345.xyz \
  -e SSH_PORT=2222 \
  -e SSH_USER=gamma \
  -e SSH_AUTH_MODE=auto \
  -e SSH_LOCAL_FORWARDS='127.0.0.1:3307:127.0.0.1:3306' \
  -e SSH_DYNAMIC_FORWARDS='1080' \
  -e SSH_EXTRA_ARGS='-v' \
  -v "$PWD/docker-client/ssh:/app/ssh:ro" \
  -v "$PWD/docker-client/log:/app/log" \
  sish-client:dev
```

### 4) Custom full target string

```bash
docker run --rm -it \
  -e SSH_TARGET='delta@edge.example.org' \
  -e SSH_PORT=443 \
  -e SSH_AUTH_MODE=key \
  -e SSH_REMOTE_FORWARDS='prod:80:localhost:8080' \
  -v "$PWD/docker-client/ssh:/app/ssh:ro" \
  -v "$PWD/docker-client/log:/app/log" \
  sish-client:dev
```

### 5) Run as custom uid/gid

```bash
docker run --rm -it \
  -e SISH_CLIENT_UID=2001 \
  -e SISH_CLIENT_GID=2001 \
  -e SSH_HOST=tuns.0912345.xyz \
  -e SSH_USER=alpha \
  -e SSH_AUTH_MODE=key \
  -e SSH_REMOTE_FORWARDS='myapp:80:localhost:8080' \
  -v "$PWD/docker-client/ssh:/app/ssh:ro" \
  -v "$PWD/docker-client/log:/app/log" \
  sish-client:dev
```

### 6) Remote command mode (disable `-N`)

```bash
docker run --rm -it \
  -e SSH_HOST=tuns.0912345.xyz \
  -e SSH_USER=alpha \
  -e SSH_AUTH_MODE=key \
  -e SISH_REMOTE_COMMAND='id=mycmd note=run-from-remote-command' \
  -v "$PWD/docker-client/ssh:/app/ssh:ro" \
  -v "$PWD/docker-client/log:/app/log" \
  sish-client:dev
```

## Docker Compose cases

File: `docker-client/compose.yml`

Available profiles:
- `env-password`
- `env-pubkey`
- `env-httpdomain`
- `env-tcp`
- `env-tcpalias`
- `env-password-httpdomain`
- `env-password-tcp`
- `env-password-tcpalias`
- `env-pubkey-httpdomain`
- `env-pubkey-tcp`
- `env-pubkey-tcpalias`
- `env-httpdomain-forceconnect`
- `env-tcp-multiple`
- `env-tcpalias-multiple`
- `env-localforward-dynamic`

Run one profile:

```bash
docker compose -f docker-client/compose.yml --profile env-pubkey up -d
```

Example (one complete case):

```bash
docker compose -f docker-client/compose.yml --profile env-password-tcpalias up -d
```

All services in `docker-client/compose.yml` now include explicit values for:
- `SSH_STRICT_HOST_KEY_CHECKING`
- `SSH_USER_KNOWN_HOSTS_FILE`
- `SSH_SERVER_ALIVE_INTERVAL`
- `SSH_SERVER_ALIVE_COUNT_MAX`
- `SSH_CONNECT_TIMEOUT`
- `SSH_EXIT_ON_FORWARD_FAILURE`

All files in `docker-client/env-example/` include the same variables, ready to override per profile.

Run all example profiles:

```bash
docker compose -f docker-client/compose.yml \
  --profile env-password \
  --profile env-pubkey \
  --profile env-httpdomain \
  --profile env-tcp \
  --profile env-tcpalias \
  --profile env-password-httpdomain \
  --profile env-password-tcp \
  --profile env-password-tcpalias \
  --profile env-pubkey-httpdomain \
  --profile env-pubkey-tcp \
  --profile env-pubkey-tcpalias \
  --profile env-httpdomain-forceconnect \
  --profile env-tcp-multiple \
  --profile env-tcpalias-multiple \
  --profile env-localforward-dynamic up -d
```

Stop and remove:

```bash
docker compose -f docker-client/compose.yml down
```

## env-example profiles

Directory: `docker-client/env-example/`

Base requested files:
- `.env.password`
- `.env.pubkey`
- `.env.httpdomain`
- `.env.tcp`
- `.env.tcpalias`

Extended files for additional use cases:
- `.env.password.httpdomain`
- `.env.password.tcp`
- `.env.password.tcpalias`
- `.env.pubkey.httpdomain`
- `.env.pubkey.tcp`
- `.env.pubkey.tcpalias`
- `.env.httpdomain.forceconnect`
- `.env.tcp.multiple`
- `.env.tcpalias.multiple`
- `.env.localforward.dynamic`

Run with `docker run` + env file:

```bash
docker run --rm -it \
  --env-file docker-client/env-example/.env.pubkey \
  -v "$PWD/docker-client/ssh:/app/ssh:ro" \
  -v "$PWD/docker-client/log:/app/log" \
  sish-client:dev
```

Run with `docker compose` + env file:

```bash
docker compose -f docker-client/compose.yml \
  --env-file docker-client/env-example/.env.password \
  --profile env-password up -d
```

## Output files

- Forwarder output:
  - `docker-client/log/forwarder/outputs.log`
  - rotated files: `outputs.log.YYYYmmdd-HHMMSS`
- Session stats:
  - `docker-client/log/stats/stats.log`
  - rotated files: `stats.log.YYYYmmdd-HHMMSS`

Stats entries include:
- `CONNECTING`
- `DISCONNECTED`
- `RESTARTING`
- `STOP` (when max retries reached)
- `ERROR` (misconfiguration, for example password mode without password)

Startup output includes:
- the exact SSH command executed by the client (arguments quoted)
- all effective SSH input environment variables
- all effective `SendEnv` variables with values actually forwarded to SSH
- an explicit hint if remote forwarding is rejected (for example port/domain already in use)

## Operational notes

- Always mount `docker-client/log` to persist logs.
- The container does not change ownership on `/app/ssh`; read-only key mounts are supported by design.
- For key mode, mount your private key path in `/app/ssh` and set `SSH_PRIVATE_KEY_PATH` if needed.
- For password mode, use secrets or secure env-injection mechanism.
- `SSH_EXTRA_ARGS` is intentionally raw for advanced use cases.
- Keep `SSH_EXIT_ON_FORWARD_FAILURE=yes` for deterministic startup behavior.

## Security recommendations

- Prefer `SSH_STRICT_HOST_KEY_CHECKING=yes` in production.
- Provide a controlled `SSH_USER_KNOWN_HOSTS_FILE` mounted from trusted source.
- Avoid hardcoding passwords in `compose.yml`; use `.env` or secrets.
- Restrict permissions on mounted `docker-client/log` and `docker-client/ssh`.

## Troubleshooting

1. SSH exits immediately
- check `SSH_HOST`, `SSH_PORT`, network routing, firewall.
- inspect `log/forwarder/outputs.log`.

2. Password mode does not connect
- ensure `SSH_AUTH_MODE=password` and `SSH_PASSWORD` is set.
- verify server allows password auth.

3. Tunnels are missing
- verify `SSH_REMOTE_FORWARDS`, `SSH_LOCAL_FORWARDS`, `SSH_DYNAMIC_FORWARDS` format.
- use `SSH_EXTRA_ARGS='-v'` for verbose ssh logs.
- if you see `remote port forwarding failed for listen port ...`, the target is likely already occupied on server side.
- choose another forward target or set `SISH_FORCE_CONNECT=true` (server must have `--enable-force-connect=true`).

4. sish options not applied
- set matching `SISH_*` variables.
- verify variable is included in `SSH_SEND_ENV`.
- ensure server-side `AcceptEnv`/sish parsing supports expected variable.

5. Container restarts too aggressively
- increase `SISH_CLIENT_RESTART_DELAY_SECONDS`.
- set `SISH_CLIENT_MAX_RETRIES` to a finite value during diagnostics.

## Session handover (2026-03-12)

This section is a quick checkpoint to resume work tomorrow without re-reading all commits.

Completed in this session:
- Built full `docker-client` runtime with scripts, compose profiles, env examples, and docs.
- Added real-time log streaming so output appears identically in:
  - log files under `/app/log`
  - `docker compose up` (attached)
  - `docker compose logs`
  - `docker logs -f`
- Removed ANSI color escape codes from log output to keep plain text readable.
- Added startup diagnostics:
  - effective SSH-related env dump
  - effective `SendEnv` values actually forwarded
  - fully rendered SSH command line
- Added forwarding rejection hint for common server-side collisions.
- Ensured read-only `/app/ssh` mounts work (no chown on mounted key path).
- Defaulted known hosts file to writable path: `/tmp/ssh_known_hosts`.
- Exposed SSH reliability/safety options explicitly in all compose services and all env examples:
  - `SSH_STRICT_HOST_KEY_CHECKING`
  - `SSH_USER_KNOWN_HOSTS_FILE`
  - `SSH_SERVER_ALIVE_INTERVAL`
  - `SSH_SERVER_ALIVE_COUNT_MAX`
  - `SSH_CONNECT_TIMEOUT`
  - `SSH_EXIT_ON_FORWARD_FAILURE`

Quick restart sequence:

```bash
docker build -t sish-client:dev -f docker-client/Dockerfile .
docker compose -f docker-client/compose.yml --profile env-httpdomain up --build --force-recreate -d
docker compose -f docker-client/compose.yml logs -f sish-client-env-httpdomain
```

Current known-good defaults:
- keep `SSH_USER_KNOWN_HOSTS_FILE=/tmp/ssh_known_hosts` for read-only key mount setups
- keep `SSH_EXIT_ON_FORWARD_FAILURE=yes` for deterministic startup/fail-fast behavior
- prefer `SSH_STRICT_HOST_KEY_CHECKING=yes` in production with managed known_hosts
