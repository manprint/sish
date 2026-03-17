# README_CLIENT_SISH_SSH

Technical handover for the dockerized sish SSH client work completed in this session.

Date: 2026-03-12
Scope: `docker-client/` implementation, runtime fixes, observability, env/compose/doc alignment.

## 1) What was implemented

- Added complete `docker-client` solution:
  - `docker-client/Dockerfile`
  - `docker-client/entrypoint.sh`
  - `docker-client/scripts/build-ssh-command.sh`
  - `docker-client/scripts/run-client.sh`
  - `docker-client/scripts/log-rotate.sh`
  - `docker-client/compose.yml`
  - `docker-client/env-example/*`
  - `docker-client/README.md`

- Added support for:
  - auth modes: `key`, `password`, `auto`
  - forwards: reverse (`-R`), local (`-L`), dynamic (`-D`)
  - internal autorestart loop independent from Docker restart
  - forwarder log + stats log with rotation

## 2) Runtime fixes applied

### 2.1 Read-only key mount compatibility

Problem:
- startup failed when `/app/ssh` was mounted read-only and script tried ownership changes.

Fix:
- `entrypoint.sh` no longer requires ownership changes on `/app/ssh`.
- ownership handling is tolerant and limited to writable runtime paths.

### 2.2 Known hosts path

Problem:
- default known_hosts location under mounted key path could fail on write.

Fix:
- default changed to writable `/tmp/ssh_known_hosts`.

### 2.3 Real-time logs in Docker streams

Problem:
- logs were visible in file but not live in `docker compose up/logs` and `docker logs -f`.

Fix:
- switched from buffered temp-file-only flow to live stream via FIFO.
- each SSH line is emitted immediately to stdout and appended to forwarder log.

Outcome:
- same lines visible in:
  - forwarder log file
  - `docker compose up` (attached)
  - `docker compose logs`
  - `docker logs -f`

### 2.4 ANSI color cleanup

Problem:
- raw ANSI color escape sequences made logs hard to read.

Fix:
- sanitize lines before write/emit by stripping ANSI escapes.
- resulting logs are plain text.

### 2.5 Diagnostics and troubleshooting improvements

Added:
- effective SSH env dump at startup
- effective `SendEnv` list with forwarded values only
- full rendered SSH command logging
- hint when remote forwarding is rejected (`remote port forwarding failed...`)

### 2.6 SendEnv behavior refinement

Change:
- `SendEnv` options are emitted only for variables that are set and non-empty.

### 2.7 SSH command flag adjustments

Change requested and applied:
- removed `-T`
- removed `-N`

## 3) SSH options now explicitly environment-driven

These parameters are configurable via env vars and mapped to SSH `-o` options:

- `SSH_STRICT_HOST_KEY_CHECKING` -> `StrictHostKeyChecking`
- `SSH_USER_KNOWN_HOSTS_FILE` -> `UserKnownHostsFile`
- `SSH_SERVER_ALIVE_INTERVAL` -> `ServerAliveInterval`
- `SSH_SERVER_ALIVE_COUNT_MAX` -> `ServerAliveCountMax`
- `SSH_CONNECT_TIMEOUT` -> `ConnectTimeout`
- `SSH_EXIT_ON_FORWARD_FAILURE` -> `ExitOnForwardFailure`

These are now present explicitly in:
- all services in `docker-client/compose.yml`
- all profiles in `docker-client/env-example/`

## 4) Compose and env examples

- `docker-client/compose.yml` includes multiple profiles for password/key/auto, httpdomain, tcp, tcpalias, forceconnect, multiple forwards, and local+dynamic forwards.
- `docker-client/env-example/` contains base and extended `.env` profiles mirroring those cases.

## 5) Logging model (current behavior)

Forwarder log:
- file path from `FORWARDER_LOG_FILE`
- receives timestamped, plain-text SSH output
- rotated by size/age/count policy

Stats log:
- file path from `STATS_LOG_FILE`
- records lifecycle events (`CONNECTING`, `DISCONNECTED`, `RESTARTING`, etc.)
- mirrored to stdout

## 6) Build and run commands (known-good)

Build:

```bash
docker build -t sish-client:dev -f docker-client/Dockerfile .
```

Run one profile:

```bash
docker compose -f docker-client/compose.yml --profile env-httpdomain up --build --force-recreate -d
```

Follow logs:

```bash
docker compose -f docker-client/compose.yml logs -f sish-client-env-httpdomain
# or
docker logs -f sish-client-env-httpdomain
```

## 7) Open operational notes for tomorrow

- If remote forward is rejected, verify target conflict server-side first; then use alternative domain/port or `SISH_FORCE_CONNECT=true` if server supports force connect.
- For production hardening, consider setting:
  - `SSH_STRICT_HOST_KEY_CHECKING=yes`
  - managed/persistent `SSH_USER_KNOWN_HOSTS_FILE`
- If desired, next improvement can normalize duplicate timestamps in access lines for cleaner display.

## 8) Files to review first tomorrow

- `docker-client/scripts/run-client.sh`
- `docker-client/scripts/build-ssh-command.sh`
- `docker-client/compose.yml`
- `docker-client/env-example/README.md`
- `docker-client/README.md`
