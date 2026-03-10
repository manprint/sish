# sish

An open source serveo/ngrok alternative.

[Read the docs.](https://docs.ssi.sh)

## Recent updates

- SSH over HTTPS multiplexing on port `443` (`--ssh-over-https`)
- Forced takeover for in-use targets (`force-connect=true`, guarded by `--enable-force-connect`)
- Unified command passing from both SSH exec args and `SISH_*` environment variables
- Connection metadata support: `id`, `note`, `note64`
- Admin dashboard improvements: ID column, live duration, notes modal, compact session/fingerprint cells
- Dedicated admin history page at `/_sish/history` (in-memory, CSV export available, gated by `--history-enabled`)

## New server flags

- `--ssh-over-https=true` enables SSH ingress on the HTTPS listener (requires `--https=true`)
- `--enable-force-connect=true` allows clients to request takeover via `force-connect=true`
- `--history-enabled=true|false` controls history page/API visibility in admin frontend

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

## Project README index

- `README_COMMANDS.md` - full command/env mapping and autossh-safe examples
- `README_FORCE_CONNECT.md` - forced takeover behavior and operational notes
- `README_SSH_SSL.md` - SSH over HTTPS setup and multiplexing behavior
- `README_CONSOLLE.md` - admin dashboard: clients, notes, stats, history page
- `README_USERS.md` - SSH authentication with per-user YAML passwords and live reload
- `README_HEADERS.md` - managed response headers by YAML with defaults/overrides
- `README_CENSUS.md` - census page, APIs, strict-id-censed behavior, runtime enforcement

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
