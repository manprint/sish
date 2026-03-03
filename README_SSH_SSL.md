# SSH over HTTPS (SSL Port Multiplexing)

This document explains how to enable SSH tunnel connections over the HTTPS listener port (for example `443`) while preserving normal HTTPS behavior.

## Overview

When enabled, sish multiplexes traffic on the HTTPS listener:

- If the incoming connection starts with the SSH protocol preface (`SSH-`), it is routed to the SSH tunnel server.
- Otherwise, it is handled as normal HTTPS/TLS traffic.

This allows SSH tunnel clients to connect from restrictive networks where only `443` is allowed.

## New Flag

- `--ssh-over-https` (default: `false`)

Behavior:

- `false` → SSH is accepted only on `--ssh-address`.
- `true` + `--https=true` → SSH is accepted on both:
  - `--ssh-address` (standard SSH ingress)
  - `--https-address` (multiplexed SSH over HTTPS ingress)

## Enable the Feature

Example:

```bash
sish \
  --ssh-address=:2222 \
  --http-address=:80 \
  --https-address=:443 \
  --https=true \
  --ssh-over-https=true
```

With this setup, both commands work:

```bash
autossh -M 0 -p 2222 -R aaaaaa:80:localhost:8004 sish.mydomain.link
autossh -M 0 -p 443  -R aaaaaa:80:localhost:8004 sish.mydomain.link
```

## Disable the Feature

Use either of these options:

1. Explicitly disable it:

```bash
sish \
  --ssh-address=:2222 \
  --http-address=:80 \
  --https-address=:443 \
  --https=true \
  --ssh-over-https=false
```

2. Omit `--ssh-over-https` (default is disabled).

## Logging and Visibility

### Startup summary

At startup, sish logs the SSH ingress endpoints, for example:

- `SSH ingress enabled on: 2222`
- `SSH ingress enabled on: 2222, 443 (multiplexed)`

### Per-connection ingress source

For each accepted SSH connection, logs include ingress origin:

- `ingress: ssh` → accepted from standard SSH listener (`--ssh-address`)
- `ingress: https` → accepted from HTTPS listener (`--https-address`, multiplexed)

## Compatibility Notes

- HTTPS behavior for non-SSH traffic remains unchanged.
- If `--ssh-address` and `--https-address` are equal, sish avoids double binding and uses the shared multiplexed listener.
- SSH-over-HTTPS requires `--https=true`.
