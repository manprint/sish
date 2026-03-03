# Force Connect

This document describes the `force-connect` feature.

## Goal

`force-connect=true` allows a client to forcibly take over a target already used by other SSH connections (HTTP subdomain, TCP port, TCP alias, SNI target).

## Server requirement (mandatory)

`force-connect` works **only** if the server is started with:

```bash
--enable-force-connect
```

If a client sends `force-connect=true` while the server flag is disabled, the request is ignored and normal allocation behavior is used.

---

## Client-side usage

## A) Command argument mode

```bash
ssh -p 443 -R aaaaaa:80:localhost:8004 sish.mydomain.link force-connect=true
```

Works, but for autossh reliability it is recommended to use env mode below.

## B) autossh-safe env mode (recommended)

```bash
SISH_FORCE_CONNECT=true \
autossh -M0 -o SendEnv=SISH_FORCE_CONNECT -p 443 -R aaaaaa:80:localhost:8004 sish.mydomain.link
```

---

## Behavior when force-connect=true

When enabled and requested:

0. Bypasses restrictive allocation behavior for requested target selection (random fallback/load-balancer fallback paths are disabled for this connection).
1. Finds and closes other SSH connections currently bound to the same requested target.
2. Waits for deallocation/cleanup to complete.
3. Binds the requested target and starts forwarding with explicit forced evidence in client messages.

The startup message includes `(forced)` and a line showing how many existing connections were disconnected for takeover.

---

## Supported target types

- HTTP/HTTPS host/subdomain targets
- TCP ports
- TCP aliases
- SNI host mappings

---

## Examples

## HTTP subdomain takeover

```bash
SISH_FORCE_CONNECT=true \
autossh -M0 -o SendEnv=SISH_FORCE_CONNECT -p 443 -R nginx:80:localhost:8080 tuns.example.com
```

## TCP port takeover

```bash
SISH_FORCE_CONNECT=true \
autossh -M0 -o SendEnv=SISH_FORCE_CONNECT -p 443 -R 0.0.0.0:9000:localhost:9000 tuns.example.com
```

## TCP alias takeover

```bash
SISH_FORCE_CONNECT=true \
autossh -M0 -o SendEnv=SISH_FORCE_CONNECT -p 443 -R myalias:9001:localhost:9001 tuns.example.com
```

## Combined with notes

```bash
SISH_FORCE_CONNECT=true \
SISH_NOTE='Takeover for emergency maintenance' \
autossh -M0 \
  -o SendEnv=SISH_FORCE_CONNECT \
  -o SendEnv=SISH_NOTE \
  -p 443 -R nginx:80:localhost:8080 tuns.example.com
```

---

## Operational notes

- Force connect is intentionally disruptive for existing sessions using the same target.
- Only sessions mapped to the same requested target (address+port and listener type) are disconnected.
- Recommended for operational takeover/recovery workflows.

---

## Quick checklist

- Server has `--enable-force-connect`
- Client sends `force-connect=true` (or `SISH_FORCE_CONNECT=true` + `SendEnv`)
- Verify startup message contains `(forced)`
