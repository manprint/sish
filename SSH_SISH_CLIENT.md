# SSH Sish Client (docker-client)

For the full operational runbook (all options, all usage modes, all compose cases), see also:

- `docker-client/README.md`

Questa documentazione descrive il client Docker per instaurare tunnel SSH verso sish in modo production-grade.

Il client vive in `docker-client/` e include:
- `Dockerfile` (base Alpine)
- `entrypoint.sh`
- script modulari in `docker-client/scripts/`
- `compose.yml` con piu` esempi

## Obiettivi coperti

- esecuzione SSH come utente non root (default `1000:1000`, configurabile)
- supporto tunnel `-R`, `-L`, `-D`
- supporto autenticazione password non interattiva e/o chiave
- comando SSH costruito dinamicamente da variabili ambiente
- autorestart interno script con delay configurabile
- log output SSH in `log/forwarder/outputs.log`
- log eventi sessione in `log/stats/stats.log`
- retention/rotation per dimensione ed eta` (ore), configurabili

## Struttura

- `docker-client/Dockerfile`
- `docker-client/entrypoint.sh`
- `docker-client/scripts/build-ssh-command.sh`
- `docker-client/scripts/log-rotate.sh`
- `docker-client/scripts/run-client.sh`
- `docker-client/compose.yml`

## Variabili ambiente principali

### Runtime utente/container

- `SISH_CLIENT_UID` (default: `1000`)
- `SISH_CLIENT_GID` (default: `1000`)
- `SISH_CLIENT_USER` (default: `sishclient`)
- `SISH_CLIENT_GROUP` (default: `sishclient`)
- `SISH_CLIENT_HOME` (default: `/home/sishclient`)

### Connessione SSH

- `SSH_HOST` (default: `tuns.0912345.xyz`)
- `SSH_PORT` (default: `2222`)
- `SSH_USER` (default: empty)
- `SSH_TARGET` (default auto: `SSH_USER@SSH_HOST` se user valorizzato, altrimenti `SSH_HOST`)

### Modalita` autenticazione

- `SSH_AUTH_MODE` (default: `auto`)
  - `auto`: usa chiave se presente, altrimenti password se presente
  - `key`: forza autenticazione con chiave
  - `password`: forza password non interattiva
- `SSH_PRIVATE_KEY_PATH` (default: `/app/ssh/id_ed25519`)
- `SSH_IDENTITY_ONLY` (default: `yes`)
- `SSH_PASSWORD` (default: empty)

### Forwarding

- `SSH_REMOTE_FORWARDS` (default: empty)
  - formato: lista separata da `;`
  - ogni elemento diventa `-R <elemento>`
- `SSH_LOCAL_FORWARDS` (default: empty)
  - formato: lista separata da `;`
  - ogni elemento diventa `-L <elemento>`
- `SSH_DYNAMIC_FORWARDS` (default: empty)
  - formato: lista separata da `;`
  - ogni elemento diventa `-D <elemento>`

Esempi:
- `SSH_REMOTE_FORWARDS="mysub:80:localhost:8080;9001:localhost:9001;myalias:9111:localhost:9111"`
- `SSH_LOCAL_FORWARDS="127.0.0.1:3307:127.0.0.1:3306"`
- `SSH_DYNAMIC_FORWARDS="1080"`

### Opzioni SSH

- `SSH_DISABLE_TTY` (default: `yes`, applica `-T`)
- `SSH_EXIT_ON_FORWARD_FAILURE` (default: `yes`)
- `SSH_SERVER_ALIVE_INTERVAL` (default: `30`)
- `SSH_SERVER_ALIVE_COUNT_MAX` (default: `3`)
- `SSH_CONNECT_TIMEOUT` (default: `10`)
- `SSH_STRICT_HOST_KEY_CHECKING` (default: `no`)
- `SSH_USER_KNOWN_HOSTS_FILE` (default: `/app/ssh/known_hosts`)
- `SSH_USER_KNOWN_HOSTS_FILE` (default: `/tmp/ssh_known_hosts`)
- `SSH_COMPRESSION` (default: `no`)
- `SSH_LOG_LEVEL` (default: `INFO`)
- `SSH_OPTIONS` (default: empty)
  - lista `;` di opzioni passate come `-o <value>`
  - esempio: `"PubkeyAuthentication=no;PreferredAuthentications=password"`
- `SSH_EXTRA_ARGS` (default: empty)
  - argomenti raw aggiuntivi, split su spazi
  - esempio: `"-v -4"`

### Integrazione comandi sish

- `SSH_SEND_ENV`
  - default: lista completa delle variabili `SISH_*` note
  - ogni valore e` passato come `-o SendEnv=<VAR>` solo se la variabile e` valorizzata
- `SISH_REMOTE_COMMAND` (default: empty)
  - se valorizzato viene passato come comando remoto SSH
  - se vuoto, il client usa `-N`

Variabili `SISH_*` utili (esempi):
- `SISH_ID`
- `SISH_NOTE`
- `SISH_NOTE64`
- `SISH_FORCE_CONNECT`
- `SISH_FORCE_HTTPS`
- `SISH_PROXY_PROTOCOL`
- `SISH_TCP_ADDRESS`
- `SISH_TCP_ALIAS`
- `SISH_LOCAL_FORWARD`

### Autorestart interno

- `SISH_CLIENT_AUTORESTART` (default: `true`)
- `SISH_CLIENT_RESTART_DELAY_SECONDS` (default: `3`)
- `SISH_CLIENT_MAX_RETRIES` (default: `0` = infinito)

### Log output e stats

- `FORWARDER_LOG_FILE` (default: `/app/log/forwarder/outputs.log`)
- `STATS_LOG_FILE` (default: `/app/log/stats/stats.log`)

Retention/rotation forwarder log:
- `FORWARDER_LOG_MAX_SIZE_MB` (default: `100`)
- `FORWARDER_LOG_MAX_AGE_HOURS` (default: `168`)
- `FORWARDER_LOG_MAX_FILES` (default: `30`)

Retention/rotation stats log:
- `STATS_LOG_MAX_SIZE_MB` (default: `20`)
- `STATS_LOG_MAX_AGE_HOURS` (default: `720`)
- `STATS_LOG_MAX_FILES` (default: `60`)

## Build e run rapidi

Dalla root del progetto:

```bash
docker build -t sish-client:dev -f docker-client/Dockerfile .
```

Esecuzione base (reverse HTTP con chiave):

```bash
docker run --rm -it \
  -e SSH_HOST=tuns.0912345.xyz \
  -e SSH_PORT=443 \
  -e SSH_USER=alpha \
  -e SSH_AUTH_MODE=key \
  -e SSH_REMOTE_FORWARDS='mysub:80:localhost:8080' \
  -v "$PWD/docker-client/ssh:/app/ssh:ro" \
  -v "$PWD/docker-client/log:/app/log" \
  sish-client:dev
```

## Esempi docker compose

Usa `docker-client/compose.yml` con profili.

1. HTTP reverse con chiave:

```bash
docker compose -f docker-client/compose.yml --profile http-key up -d
```

2. TCP/Alias reverse con password:

```bash
docker compose -f docker-client/compose.yml --profile tcp-password up -d
```

3. Local forward + dynamic socks:

```bash
docker compose -f docker-client/compose.yml --profile local-forward up -d
```

## Note operative

- Monta sempre `docker-client/log` come volume persistente per conservare output/stats.
- In modalita` password, il client usa `sshpass` con `SSHPASS` interno.
- In modalita` auto, se trova la chiave in `SSH_PRIVATE_KEY_PATH` usa quella; altrimenti prova password se presente.
- `SSH_EXTRA_ARGS` e` volutamente raw per coprire opzioni SSH avanzate.
- Per tunnel sish con opzioni applicative, usa `SISH_*` + `SSH_SEND_ENV`.

## Troubleshooting

1. Connessione rifiutata:
- verifica `SSH_HOST`, `SSH_PORT`, firewall e reachability.

2. Forward non creato:
- verifica `SSH_REMOTE_FORWARDS`/`SSH_LOCAL_FORWARDS`.
- controlla `ExitOnForwardFailure` (default `yes`).

3. Autenticazione fallita:
- `SSH_AUTH_MODE=key`: controlla file e permessi della chiave montata.
- `SSH_AUTH_MODE=password`: verifica `SSH_PASSWORD` e opzioni auth lato server.

4. Opzioni sish non applicate:
- verifica presenza variabili `SISH_*` e `SSH_SEND_ENV` coerente.

## Sicurezza

- Evita `SSH_STRICT_HOST_KEY_CHECKING=no` in produzione sensibile.
- Usa secret manager/`.env` sicuro per password e chiavi.
- Ruota credenziali periodicamente e limita i permessi del volume log.
