# Command Passing Guide (SSH / autossh)

This document explains how to pass tunnel options to sish, with special focus on **autossh-safe** usage.

## Why this matters

When using `autossh`, passing options as a remote command (for example `... host note=...`) can force `exec` mode and may affect restart behavior depending on client/session lifecycle.

Recommended approach for `autossh`:

- Pass options through environment variables (`SISH_*`)
- Use `-o SendEnv=...`
- Avoid remote command arguments when possible

---

## Two supported ways to pass options

## 1) Remote command arguments (works, but less ideal for autossh)

Example:

```bash
ssh -p 2222 -R nginx:80:localhost:8080 tuns.example.com 'force-https=true note=hello'
```

This mode is still supported.

---

## 2) Environment variables (recommended for autossh)

Pattern:

- client env var: `SISH_<COMMAND_NAME>`
- command name conversion:
  - lowercase
  - `_` becomes `-`

Examples:

- `SISH_FORCE_HTTPS` -> `force-https`
- `SISH_TCP_ADDRESS` -> `tcp-address`
- `SISH_HOST_HEADER` -> `host-header`

Example:

```bash
SISH_FORCE_HTTPS=true \
autossh -M0 -o SendEnv=SISH_FORCE_HTTPS -p 2222 -R nginx:80:localhost:8080 tuns.example.com
```

---

## Supported commands via SISH_* env

The following options are supported both via remote command and via `SISH_*` env mapping:

- `proxy-protocol` / `proxyproto`
- `host-header`
- `strip-path`
- `sni-proxy`
- `tcp-address`
- `tcp-alias`
- `local-forward`
- `auto-close`
- `force-https`
- `tcp-aliases-allowed-users`
- `deadline`
- `id`
- `note`
- `note64`

---

## Connection ID support

You can set a client-visible connection identifier with:

- `id=<value>` (remote command mode)
- `SISH_ID=<value>` + `-o SendEnv=SISH_ID` (autossh-safe mode)

Validation rules:

- maximum length: 50 characters
- no spaces
- allowed characters: `A-Z a-z 0-9 . _ -`

If no ID is provided, the server generates one automatically:

- `rand-xxxxxxxx` (8 alphanumeric characters)

### Valid examples

```bash
ssh -p 443 -R nginx:80:localhost:8080 tuns.example.com 'id=nginx-001'

SISH_ID=nginx-001 \
autossh -M0 -o SendEnv=SISH_ID -p 443 -R nginx:80:localhost:8080 tuns.example.com
```

### Invalid examples (rejected)

```bash
# contains space
ssh -p 443 -R nginx:80:localhost:8080 tuns.example.com 'id=nginx 001'

# too long (>50)
ssh -p 443 -R nginx:80:localhost:8080 tuns.example.com 'id=abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ1234'

# invalid character (!)
ssh -p 443 -R nginx:80:localhost:8080 tuns.example.com 'id=nginx-001!'
```

---

## Notes support

## Plain note

```bash
SISH_NOTE='Started from my laptop for staging tests' \
autossh -M0 -o SendEnv=SISH_NOTE -p 2222 -R nginx:80:localhost:8080 tuns.example.com
```

## File note (multiline, recommended)

```bash
SISH_NOTE64=$(base64 -w0 notes.txt) \
autossh -M0 -o SendEnv=SISH_NOTE64 -p 2222 -R nginx:80:localhost:8080 tuns.example.com
```

Why `note64` is recommended for files:

- preserves exact newlines/content
- avoids shell quoting/whitespace normalization issues

---

## Detailed examples

## force-https + host-header

```bash
SISH_FORCE_HTTPS=true \
SISH_HOST_HEADER=internal.example.local \
autossh -M0 \
  -o SendEnv=SISH_FORCE_HTTPS \
  -o SendEnv=SISH_HOST_HEADER \
  -p 2222 -R nginx:80:localhost:8080 tuns.example.com
```

## tcp address + sni proxy

```bash
SISH_TCP_ADDRESS=0.0.0.0 \
SISH_SNI_PROXY=true \
autossh -M0 \
  -o SendEnv=SISH_TCP_ADDRESS \
  -o SendEnv=SISH_SNI_PROXY \
  -p 2222 -R mytls:443:localhost:8443 tuns.example.com
```

## deadline (duration)

```bash
SISH_DEADLINE=2h \
autossh -M0 -o SendEnv=SISH_DEADLINE -p 2222 -R nginx:80:localhost:8080 tuns.example.com
```

## tcp aliases allowed users

```bash
SISH_TCP_ALIASES_ALLOWED_USERS='SHA256:abc123,SHA256:def456' \
autossh -M0 -o SendEnv=SISH_TCP_ALIASES_ALLOWED_USERS -p 2222 -R myalias:9000:localhost:9000 tuns.example.com
```

---

## Multiple variables at once

You can combine multiple `SISH_*` vars and include one `-o SendEnv=...` per variable.

```bash
SISH_FORCE_HTTPS=true \
SISH_STRIP_PATH=false \
SISH_NOTE='Production tunnel from node-a' \
autossh -M0 \
  -o SendEnv=SISH_FORCE_HTTPS \
  -o SendEnv=SISH_STRIP_PATH \
  -o SendEnv=SISH_NOTE \
  -p 443 -R app:80:localhost:8080 tuns.example.com
```

---

## Troubleshooting

- If a variable is not applied:
  - verify variable name mapping (`SISH_...`)
  - verify `-o SendEnv=<VAR>` is present for each variable
- For multiline notes:
  - prefer `SISH_NOTE64` with `base64 -w0`
- For autossh restart reliability:
  - prefer env-based options over remote command options

---

## Quick reference

- Reliable with autossh: `SISH_* + SendEnv`
- Best for multiline notes: `SISH_NOTE64`
- Keep remote command mode only when strictly needed
