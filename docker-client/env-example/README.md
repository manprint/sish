# env-example profiles

This folder contains ready-to-use `.env` profiles for `docker-client`.

## Required profiles requested

- `.env.password`
- `.env.pubkey`
- `.env.httpdomain`
- `.env.tcp`
- `.env.tcpalias`

## Extended profiles (additional use cases)

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

## SSH option variables included in all profiles

Each `.env` profile includes explicit defaults for these SSH `-o` options:

- `SSH_STRICT_HOST_KEY_CHECKING` (`StrictHostKeyChecking`)
- `SSH_USER_KNOWN_HOSTS_FILE` (`UserKnownHostsFile`)
- `SSH_SERVER_ALIVE_INTERVAL` (`ServerAliveInterval`)
- `SSH_SERVER_ALIVE_COUNT_MAX` (`ServerAliveCountMax`)
- `SSH_CONNECT_TIMEOUT` (`ConnectTimeout`)
- `SSH_EXIT_ON_FORWARD_FAILURE` (`ExitOnForwardFailure`)

You can override them per profile to tune security/reliability behavior.

## Quick usage with docker run

From repository root:

```bash
docker run --rm -it \
  --env-file docker-client/env-example/.env.pubkey \
  -v "$PWD/docker-client/ssh:/app/ssh:ro" \
  -v "$PWD/docker-client/log:/app/log" \
  sish-client:dev
```

## Quick usage with docker compose

```bash
docker compose -f docker-client/compose.yml \
  --env-file docker-client/env-example/.env.password \
  --profile env-password up -d
```
