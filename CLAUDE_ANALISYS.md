# CLAUDE_ANALISYS.md

Documento di analisi tecnica completa del progetto **manprint-sish-fork**.
Generato il 2026-03-09. Da usare come contesto di ripartenza per sessioni successive.

---

## 1. Panoramica del progetto

### Cos'è

**manprint-sish-fork** è un fork esteso di [sish](https://github.com/antoniomika/sish), un **SSH reverse tunnel multiplexer** self-hosted scritto in Go. Permette di esporre servizi locali (HTTP, HTTPS, WebSocket, TCP) tramite tunnel SSH, in modo simile a ngrok o bore ma senza dipendenze esterne.

Il fork aggiunge numerose feature custom rispetto all'upstream: autenticazione per-utente da YAML, gestione header HTTP da configurazione, sistema census per inventory remoto, strict enforcement runtime, admin console estesa, API pubbliche di inserimento.

### Repository e modulo

- Modulo Go: `github.com/antoniomika/sish`
- Go: `1.26`, toolchain: `go1.26.1`
- Branch attivo: `devgo126`
- Working directory: `/mnt/fabio/dati/Git/Github-manprint/manprint-sish-fork`

### Verifica build rapida

```bash
go test ./... -run TestDoesNotExist -count=1
gofmt -w <file>
```

---

## 2. Architettura generale

```
main.go
  └── cmd/sish.go           ← Cobra CLI entrypoint, ~150 flag, Viper config
        ├── sshmuxer/       ← server SSH, sessioni, tunnels, channels, requests
        ├── httpmuxer/      ← HTTP/HTTPS muxer, Gin router, reverse proxy
        ├── utils/          ← state, conn, auth, console, census, headers
        └── templates/      ← UI admin console (Go html/template)
```

### Stack tecnologico

| Componente | Libreria |
|-----------|---------|
| SSH server | `golang.org/x/crypto/ssh` |
| HTTP server | `github.com/gin-gonic/gin` |
| Reverse proxy | `github.com/vulcand/oxy` (fork: `github.com/antoniomika/oxy`) |
| TLS / Let's Encrypt | `github.com/caddyserver/certmagic` |
| Config management | `github.com/spf13/viper` + `github.com/spf13/cobra` |
| Maps thread-safe | `github.com/antoniomika/syncmap` |
| IP filtering | `github.com/jpillora/ipfilter` |
| File watcher | `github.com/radovskyb/watcher` + `github.com/fsnotify/fsnotify` |
| WebSocket console | `github.com/gorilla/websocket` |
| YAML | `gopkg.in/yaml.v3` |
| Logging | `github.com/sirupsen/logrus` + lumberjack |
| Proxy Protocol | `github.com/pires/go-proxyproto` |

---

## 3. Struttura file per file

### `main.go`
Entry point minimale: chiama `cmd.Execute()`.

### `cmd/sish.go`
- Definisce il comando Cobra root (`sish`)
- Registra tutti i flag persistent (~150)
- `init()` → registra i flag; `initConfig()` → carica il file YAML config con Viper; `runCommand()` → avvia `sshmuxer.Start()`
- Versioning: `Version`, `Commit`, `Date` iniettati a build time

### `sshmuxer/sshmuxer.go`
Funzione `Start()` — bootstrap completo:
1. Parse indirizzi HTTP/HTTPS/SSH
2. `utils.WatchKeys()` — watcher chiavi pubbliche
3. `utils.WatchAuthUsers()` — watcher utenti YAML (**custom**)
4. `utils.WatchHeadersSettings()` — watcher header YAML (**custom**)
5. Log warning se `strict-id-censed=true` ma `census-enabled=false` (**custom**)
6. `utils.StartCensusRefresher()` — avvio refresh census (**custom**)
7. `utils.NewState()` — creazione stato globale
8. `startStrictIDCensedConnectionEnforcer(state)` — enforcer runtime (**custom**)
9. Avvio `httpmuxer.Start()` in goroutine
10. Loop accept SSH con `handleSSHConn()`:
    - IP filter check
    - Timeout unauth (`cleanup-unauthed`)
    - `ssh.NewServerConn()` → autenticazione
    - Creazione `SSHConnection` con ID random iniziale `rand-XXXXXXXX`
    - Goroutine `handleRequests()` e `handleChannels()`
    - Goroutine ping keepalive (`keepalive@sish`)
    - Goroutine deadline e cleanup-unbound

### `sshmuxer/channels.go`
Gestione canali SSH (session, direct-tcpip, forwarded-tcpip):
- `handleSession()`: gestione shell/exec/env
  - `env` request: mappa `SISH_*` env vars a comandi (es. `SISH_ID` → `id`)
  - `exec` request: parse payload come lista di `key=value` separati da spazio
  - Supporto quote singole/doppie e escape `\` nel parser
- `applyConnectionCommand()`: applica i comandi per connessione:
  - `proxy-protocol` / `proxyproto`
  - `host-header`
  - `strip-path`
  - `sni-proxy`
  - `tcp-address`
  - `tcp-alias`
  - `auto-close`
  - `force-https`
  - `force-connect`
  - `local-forward`
  - `tcp-aliases-allowed-users`
  - `deadline` (epoch, durata, o datetime)
  - `note` / `note64` (base64 auto-detect)
  - `id` ← **custom**: regex `^[A-Za-z0-9._-]{1,50}$`, setta `ConnectionIDProvided=true`
- `handleAlias()`: gestione connessioni TCP alias con controllo fingerprint

### `sshmuxer/requests.go`
- `handleRemoteForward()`: gestione `tcpip-forward` SSH request
  - **Custom strict census check** (righe 121-143):
    ```go
    if utils.IsStrictIDCensedEnabled() {
        if !sshConn.ConnectionIDProvided { → "Id is enforced server side." }
        if !utils.IsIDCensed(sshConn.ConnectionID) { → "Forwarded id is not censed." }
    }
    ```
  - Classificazione tipo listener (HTTP/HTTPS/TCP/Alias) in base alla porta
  - Creazione unix socket temporaneo come backend del tunnel
  - `forceConnect`: disconnessione forzata del client precedente
  - Routing al handler specifico: `handleHTTPListener`, `handleAliasListener`, `handleTCPListener`
- `handleCancelRemoteForward()`: cancellazione forward attivo
- `forceDisconnectTargetConnections()`: CleanUp di tutti i client su un target
- `waitForTargetRelease()`: polling con timeout 2s per attendere rilascio target

### `sshmuxer/strict_census.go` (**custom**)
- `startStrictIDCensedConnectionEnforcer(state)`:
  - Goroutine con ticker 1 secondo
  - Confronta `snapshot.LastRefresh` con l'ultimo visto: agisce solo sui refresh nuovi
  - Per ogni `SSHConnection` attiva con `ConnectionIDProvided=true` e `ListenerCount() > 0`:
    - Se `IsIDCensed(sshConn.ConnectionID)` == false:
      - `SendMessage("Forwarded id is not censed.", true)`
      - `CleanUp(state)`
  - Log del numero di connessioni chiuse

### `sshmuxer/httphandler.go`
- Gestione bind HTTP listener
- Subdomain assignment (random o richiesto)
- Round-robin balancer su `HTTPHolder`
- Registrazione in `state.HTTPListeners`

### `sshmuxer/tcphandler.go`
- Gestione bind TCP listener su porta
- Port range validation (`port-bind-range`)
- Multilistener support
- Round-robin balancer su `TCPHolder`

### `sshmuxer/aliashandler.go`
- Gestione bind alias TCP
- Random alias o alias richiesto
- Balancer su `AliasHolder`

### `httpmuxer/httpmuxer.go`
- `Start()`: avvio Gin HTTP server
- Middleware: IP filter + logger formattato
- Routing host-based:
  - Root domain (`--domain`) → route API e console admin
  - Subdomains → reverse proxy verso tunnel client
- Route admin console (`/_sish/*`)
- Route API pubblica root host:
  - `POST /api/insertkey` ← **custom**
  - `POST /api/insertuser` ← **custom**
- Apply headers custom via `utils.ApplyForwarderHeaders()` (**custom**)
- WebSocket upgrade per service console

### `httpmuxer/https.go`
- TLS listener con certmagic (Let's Encrypt on-demand)
- SNI proxy support
- SSH-over-HTTPS multiplexing

### `httpmuxer/proxy.go`
- Reverse proxy HTTP/HTTPS verso unix socket del tunnel
- Gestione host header, strip path, force HTTPS
- Applicazione headers response custom (**custom**)

### `utils/state.go`
Struttura `State` (stato globale condiviso):
```go
type State struct {
    Console        *WebConsole
    SSHConnections *syncmap.Map[string, *SSHConnection]
    Listeners      *syncmap.Map[string, net.Listener]
    HTTPListeners  *syncmap.Map[string, *HTTPHolder]
    AliasListeners *syncmap.Map[string, *AliasHolder]
    TCPListeners   *syncmap.Map[string, *TCPHolder]
    IPFilter       *ipfilter.IPFilter
    LogWriter      io.Writer
    Ports          *Ports
}
```
Holder types:
- `HTTPHolder`: url, SSHConnections, Forward, Balancer
- `AliasHolder`: host, SSHConnections, Balancer
- `TCPHolder`: host, Listener, SSHConnections, SNIProxy, Balancers, NoHandle
- `ListenerHolder`: net.Listener + metadati (Type, SSHConn, OriginalAddr, OriginalPort)

### `utils/conn.go`
```go
type SSHConnection struct {
    SSHConn                *ssh.ServerConn
    ConnectionID           string         // ID client (random default o fornito)
    ConnectionIDProvided   bool           // true solo se client ha passato id=... (custom)
    ConnectedAt            time.Time
    ConnectionNote         string
    Listeners              *syncmap.Map[string, net.Listener]
    Closed                 *sync.Once
    Close                  chan bool
    Exec                   chan bool
    Messages               chan string
    ProxyProto             byte
    HostHeader             string
    StripPath              bool
    SNIProxy               bool
    TCPAddress             string
    TCPAlias               bool
    LocalForward           bool
    TCPAliasesAllowedUsers []string
    AutoClose              bool
    ForceHTTPS             bool
    ForceConnect           bool
    Session                chan bool
    CleanupHandler         bool
    SetupLock              *sync.Mutex
    Deadline               *time.Time
    ExecMode               bool
}
```

Metodi:
- `SendMessage(message, block)`: invia messaggio al client SSH (con retry non-block)
- `ListenerCount()`: conta listener attivi (ritorna -1 se LocalForward)
- `CleanUp(state)`: chiude connessione, rimuove da state, aggiunge a history — eseguita una sola volta via `sync.Once`

Tipi extra:
- `TeeConn`: wrapper net.Conn con buffer peek per SNI
- `PeekTLSHello()`: peek del TLS ClientHello per SNI routing
- `IdleTimeoutConn`: connessione con deadline per idle timeout
- `CopyBoth()`: copia bidirezionale con cleanup

### `utils/utils.go`
Funzioni principali:
- Auth chiavi pubbliche: `WatchKeys()`, `GetSSHConfig()`, key loading/hot-reload
- **Auth utenti YAML** (**custom**):
  - `WatchAuthUsers()`: watcher su `--auth-users-directory`
  - `loadAuthUsers()`: parse tutti i file `.yml`/`.yaml` nella directory
  - Struttura: `authUsersFile{Users []authUser{Name, Password}}`
  - Thread-safe: `authUsersHolderLock sync.RWMutex`
  - Integrazione in `PasswordCallback` SSH: ordine = password globale → auth-users → URL request
- IP Filter init: `Filter *ipfilter.IPFilter`
- DNS verify: check TXT record `_sish` per fingerprint
- Generatori ID: `RandStringBytesMaskImprSrc()`
- `MatchesWildcardHost()`, `ParseAddress()`

### `utils/census.go` (**custom**)

Strutture:
```go
type censusEntry struct { ID string `yaml:"id"` }
type censusCache struct {
    IDs         []string
    LastRefresh time.Time
    LastError   string
}
```

Funzioni:
- `RefreshCensusCache()`: download + parse YAML remoto, update cache con lock
- `StartCensusRefresher()`: goroutine ticker (default 2m), chiama `RefreshCensusCache()`
- `GetCensusCacheSnapshot()`: snapshot thread-safe con `sync.RWMutex`
- `IsStrictIDCensedEnabled()`: `census-enabled && strict-id-censed`
- `IsIDCensed(id string)`: ricerca lineare nello snapshot
- `FetchCensusSource()`: download + parse per visualizzazione debug (usato da API `/api/census/source`)

Formato census YAML remoto:
```yaml
- id: seastream-demo
- id: altra-app
```

### `utils/headers_settings.go` (**custom**)

Strutture:
```go
type headerSetting struct {
    Enabled *bool  `yaml:"enabled"`
    Value   string `yaml:"value"`
    Always  *bool  `yaml:"always"`
}
type headerScope struct { Headers map[string]headerSetting }
type headerSettingsFile struct {
    Defaults   headerScope
    Subdomains map[string]headerScope
}
```

Funzioni:
- `WatchHeadersSettings()`: watcher directory, hot-reload
- `loadHeaderSettingsConfig()`: cerca config in ordine: `config.yaml`, `config.yml`, `config.headers.yaml`, `config.headers.yml`
- `resolveHeadersForSubdomain(subdomain)`: merge default + override subdomain (fallback a primo label se nested)
- `ApplyForwarderHeaders(responseHeaders, hostWithPort, statusCode)`: entry point per httpmuxer
- `shouldApplyHeaderForStatus(always, status)`: applica sempre se `always=true`, altrimenti solo su 2xx/3xx
- `normalizeHeaderKey()`: mappa nomi nginx-style a canonical HTTP (`x_frame_options` → `X-Frame-Options`)
- `ValidateHeaderSettingsConfig(content)`: validazione strutturale YAML (usata da editusers-like endpoint)

Formato file headers (`config.headers.yaml`):
```yaml
defaults:
  headers:
    x_frame_options:
      value: "DENY"
    strict_transport_security:
      value: "max-age=31536000"
      always: true

subdomains:
  myapp:
    headers:
      x_frame_options:
        enabled: false    # disabilita questo header solo per myapp
      content_security_policy:
        value: "default-src 'self'"
```

### `utils/console.go` (**custom** — file più grande)

#### Strutture
```go
type WebConsole struct {
    Clients     *syncmap.Map[string, []*WebClient]
    RouteTokens *syncmap.Map[string, string]
    History     []ConnectionHistory
    HistoryLock *sync.RWMutex
    State       *State
}

type ConnectionHistory struct {
    ID         string
    RemoteAddr string
    Username   string
    StartedAt  time.Time
    EndedAt    time.Time
    Duration   time.Duration
}
```

#### Route admin console (`/_sish/`)

| Route | Handler | Auth |
|-------|---------|------|
| `GET /_sish/console` | template console | token |
| `GET /_sish/routes` | template routes | token |
| `GET /_sish/history` | template history | token |
| `GET /_sish/editkeys` | template editkeys | token + basic auth editkeys |
| `GET /_sish/editusers` | template editusers | token + basic auth editusers |
| `GET /_sish/census` | template census | token |
| `WebSocket /_sish/ws` | live updates | token |
| `GET /_sish/api/routes` | JSON routes | token |
| `GET /_sish/api/history` | JSON history paginato | token |
| `POST /_sish/api/history/clear` | svuota history | token |
| `GET /_sish/api/history/download` | CSV download | token |
| `GET /_sish/api/editkeys/files` | lista file keys | token + basic auth |
| `GET /_sish/api/editkeys/file` | contenuto file | token + basic auth |
| `POST /_sish/api/editkeys/file` | salva file | token + basic auth |
| `GET /_sish/api/editusers/files` | lista file users | token + basic auth |
| `GET /_sish/api/editusers/file` | contenuto file | token + basic auth |
| `POST /_sish/api/editusers/validate` | valida YAML | token + basic auth |
| `POST /_sish/api/editusers/file` | salva file | token + basic auth |
| `GET /_sish/api/census` | dati census JSON | token |
| `POST /_sish/api/census/refresh` | trigger refresh | token |
| `GET /_sish/api/census/source` | source YAML + validazione | token |

#### API pubblica (root host, no `/_sish/`)

| Route | Handler | Auth |
|-------|---------|------|
| `POST /api/insertkey` | inserisce chiave SSH pubblica | basic auth editkeys |
| `POST /api/insertuser` | inserisce utente YAML | basic auth editusers |

#### History
- Slice in-memory `[]ConnectionHistory`, protetta da `sync.RWMutex`
- `AddHistoryEntry()` chiamata da `SSHConnection.CleanUp()`
- API: `?page=N&pageSize=M&q=search` (filtro case-insensitive su ID, RemoteAddr, Username, Started, Ended)
- Download CSV via `encoding/csv`
- Clear: `POST /_sish/api/history/clear`

#### API insertkey — logica
1. Basic auth check con `admin-consolle-editkeys-credentials`
2. Lettura corpo request (chiave pubblica SSH)
3. Parse con `ssh.ParseAuthorizedKey()`
4. Dedupe: scan di tutti i file nella `authentication-keys-directory`
5. Se non presente: append in `fromapi.key` con header commento
6. Commento opzionale da header `x-api-comment` (sanitizzato: rimozione newline)
7. Formato scritto:
   ```
   # Inserted by api in date: 2026-03-09-11-18-44
   # Testo commento opzionale
   ssh-ed25519 AAAA...
   ```
8. Lock globale `insertAPIKeyLock sync.Mutex`

#### API insertuser — logica
1. Basic auth check con `admin-consolle-editusers-credentials`
2. Parse form: `name`, `password`
3. Dedupe: scan di tutti i file `.yml`/`.yaml` in `auth-users-directory`
4. Validazione YAML strutturale del blocco da appendere
5. Append in `fromapi.yml` con header commento
6. Commento opzionale da header `x-api-comment`
7. Formato scritto:
   ```yaml
   users:

   # Inserted by api in date: 2026-03-09-11-37-30
   # Testo commento opzionale
     - name: username
       password: "password"
   ```
8. Lock globale `insertAPIUserLock sync.Mutex`

#### Census API — logica
- `GET /_sish/api/census`: confronto tra census IDs e forward attivi (`state.HTTPListeners`)
  - Considera solo forward con `listeners > 0`
  - Ritorna: `proxyCensed`, `proxyUncensed`, `censedNotForwarded`
  - Include campo `isCensused` per ogni client (usato dalla dashboard routes)
- `POST /_sish/api/census/refresh`: chiama `RefreshCensusCache()` sincrono
- `GET /_sish/api/census/source`: chiama `FetchCensusSource()`, ritorna URL + body raw + IDs parsati + eventuali errori

#### Sicurezza path traversal (editkeys/editusers)
- Risoluzione assoluta del path richiesto
- Verifica che il path risolto sia figlio della directory configurata
- Rifiuto con 400 se path escapa dalla directory

### `utils/listen.go`
- `Listen(addr)`: crea listener TCP con socket reuse
- `LoadProxyProtoConfig()`: configurazione proxy protocol su listener

### `utils/sshmux_listener.go`
- `NewSSHMuxListeners()`: demultiplexer SSH/TLS sullo stesso listener (per SSH-over-HTTPS)

---

## 4. Flusso connessione SSH dettagliato

```
[Client SSH]
    │
    ▼
net.Listener.Accept()
    │
    ▼
IPFilter.Blocked() → chiudi se bloccato
    │
    ▼
goroutine cleanup-unauthed (timer 5s default)
    │
    ▼
ssh.NewServerConn(conn, sshConfig)
    ├── PublicKey auth → WatchKeys() directory + URL request
    ├── Password auth → global password → auth-users YAML → URL request
    └── No auth (se authentication=false)
    │
    ▼
SSHConnection creata:
    ConnectionID = "rand-XXXXXXXX"  (random iniziale)
    ConnectionIDProvided = false
    │
    ├── goroutine: handleRequests(reqs)
    │       └── tcpip-forward → handleRemoteForward()
    │               ├── [STRICT CENSUS CHECK] ← custom
    │               │       ConnectionIDProvided? → "Id is enforced server side."
    │               │       IsIDCensed()? → "Forwarded id is not censed."
    │               ├── Classificazione tipo: HTTP/TCP/Alias
    │               ├── Creazione unix socket temporaneo
    │               └── Routing a handler specifico
    │
    ├── goroutine: handleChannels(chans)
    │       └── session → handleSession()
    │               ├── env SISH_* → applyConnectionCommand()
    │               │       └── id=VALORE → ConnectionID=VALORE, ConnectionIDProvided=true
    │               └── exec → parse flags "key=value ..."
    │
    ├── goroutine: ping keepalive (5s interval)
    ├── goroutine: deadline / cleanup-unbound checker
    └── goroutine: sshConn.Wait() → CleanUp() on disconnect
```

---

## 5. Flusso richiesta HTTP

```
[Browser/Client HTTP]
    │
    ▼
Gin middleware: IPFilter check
    │
    ▼
Host match:
    ├── root domain → route API e admin console
    │       ├── /_sish/* → WebConsole handlers (auth: x-authorization token)
    │       └── /api/* → API pubbliche (auth: basic auth credentials)
    │
    └── subdomain.domain → reverse proxy
            ├── Lookup in state.HTTPListeners
            ├── RoundRobin balancer → unix socket tunnel
            ├── Forward request
            ├── [APPLY HEADERS] ← custom
            └── Return response
```

---

## 6. Feature custom — riepilogo tecnico

### 6.1 Auth SSH per-utente da YAML

**Flag:**
- `--auth-users-enabled` (bool, default false)
- `--auth-users-directory` (string)
- `--auth-users-directory-watch-interval` (duration, default 200ms)

**File:** `utils/utils.go`

**Formato file:**
```yaml
users:
  - name: alpha
    password: "A-pass"
  - name: beta
    password: "B-pass"
```

**Logica:** Hot-reload via watcher. Merge di tutti i file nella directory (deduplicazione per nome, ultimo vince). Thread-safe con `sync.RWMutex`. Autenticazione con `crypto/subtle.ConstantTimeCompare`. Integrata nel `PasswordCallback` SSH dopo la password globale.

**Test:** `utils/authentication_users_test.go`

---

### 6.2 Managed HTTP Headers

**Flag:**
- `--headers-managed` (bool, default false)
- `--headers-setting-directory` (string)
- `--headers-setting-directory-watch-interval` (duration, default 200ms)

**File:** `utils/headers_settings.go`

**Logica:**
- Hot-reload via watcher
- Merge: default → override per subdomain (le chiavi del subdomain prevalgono)
- Campo `enabled: false` nel subdomain rimuove l'header (anche se presente nei default)
- Campo `always: true` applica l'header anche su 4xx/5xx (default: solo 2xx/3xx)
- Fallback: se subdomain è `a.b`, cerca prima `a.b` poi `a`

---

### 6.3 Census

**Flag:**
- `--census-enabled` (bool, default false)
- `--census-url` (string)
- `--census-refresh-time` (duration, default 2m)
- `--strict-id-censed` (bool, default false)

**File:** `utils/census.go`, `sshmuxer/strict_census.go`

**Formato YAML remoto:**
```yaml
- id: seastream-demo
- id: altra-app-id
```

**Enforcement in ingresso** (`sshmuxer/requests.go`):
```
strict ON → tcpip-forward request → ConnectionIDProvided? → IsIDCensed? → go / reject
```

**Enforcement post-refresh** (`sshmuxer/strict_census.go`):
- Ticker 1s, agisce solo su refresh nuovi (confronto `LastRefresh`)
- Per ogni connessione con ID esplicito e listener attivi: verifica IsIDCensed
- Se non censito: messaggio + CleanUp

**Messaggi client:**
- `"Id is enforced server side."` — ID non fornito in strict mode
- `"Forwarded id is not censed."` — ID fornito ma non nel census

---

### 6.4 Admin Console

**Flag:**
- `--admin-console` (bool)
- `--admin-console-token` (string)
- `--admin-consolle-editkeys-credentials` (string, formato `user:pass`)
- `--admin-consolle-editusers-credentials` (string, formato `user:pass`)

**Note:** `consolle` (doppia L) è il nome usato nel codice — mantenere così per coerenza.

**Accesso base:**
```
https://domain/_sish/console?x-authorization=<token>
```

**Template disponibili:**
- `console.tmpl` — dashboard main
- `routes.tmpl` — tabella tunnel attivi con dot verde/rosso per census
- `history.tmpl` — storico connessioni con paginazione, ricerca, clear, CSV
- `editkeys.tmpl` — editor file chiavi SSH
- `editusers.tmpl` — editor file utenti YAML con validate
- `census.tmpl` — sezione census (censed/uncensed/not forwarded)
- `header.tmpl` — navbar comune con link a tutte le sezioni
- `footer.tmpl` — footer comune

---

### 6.5 API Pubbliche

**Endpoint:**
- `POST /api/insertkey` — auth: `admin-consolle-editkeys-credentials`
- `POST /api/insertuser` — auth: `admin-consolle-editusers-credentials`

**Header opzionale:** `x-api-comment: Testo del commento`

**Esempio insertkey:**
```bash
cat id_ed25519.pub | curl -u user:pass -X POST \
  -H "x-api-comment: Key for CI/CD" \
  -d @- "https://tuns.domain.it/api/insertkey"
```

**Esempio insertuser:**
```bash
curl -u user:pass -X POST \
  "https://tuns.domain.it/api/insertuser" \
  -H "x-api-comment: User for staging" \
  -d "name=myuser&password=mypassword"
```

---

## 7. Comandi SSH client — opzioni runtime

I comandi vengono passati nel payload SSH exec o come variabili env `SISH_*`:

```bash
# Tramite exec SSH
ssh -p 2222 -R myapp:80:localhost:8080 tuns.domain.it \
  id=myapp \
  note="My application" \
  auto-close=true \
  force-https=true

# Tramite env vars
ssh -p 2222 -o SendEnv=SISH_ID -o SendEnv=SISH_NOTE \
  -R myapp:80:localhost:8080 tuns.domain.it
```

**Lista comandi disponibili:**

| Comando | Tipo | Descrizione |
|---------|------|-------------|
| `id` | string (regex `[A-Za-z0-9._-]{1,50}`) | ID connessione (richiesto in strict census) |
| `note` | string | Nota testuale allegata alla connessione |
| `note64` | base64 | Nota in base64 |
| `auto-close` | bool | Chiude quando tutti i forward sono rimossi |
| `deadline` | epoch/duration/datetime | Scadenza automatica della connessione |
| `force-connect` | bool | Forza takeover target in uso |
| `force-https` | bool | Forza redirect HTTPS |
| `host-header` | string | Override host header per proxy HTTP |
| `strip-path` | bool | Strip path nelle richieste proxate |
| `proxy-protocol` / `proxyproto` | version | Versione proxy protocol |
| `tcp-alias` | bool | Usa come TCP alias |
| `tcp-address` | string | Override indirizzo TCP |
| `sni-proxy` | bool | Abilita SNI proxy per TCP |
| `local-forward` | bool | Modalità local forward (logging) |
| `tcp-aliases-allowed-users` | CSV fingerprints | Fingerprint autorizzati all'alias TCP |

---

## 8. Configurazione — flag principali

### Rete
```yaml
ssh-address: localhost:2222
http-address: localhost:80
https-address: localhost:443
domain: ssi.sh
```

### Autenticazione SSH
```yaml
authentication: true
authentication-password: ""
authentication-keys-directory: deploy/pubkeys/
authentication-key-request-url: ""
auth-users-enabled: false
auth-users-directory: ""
auth-users-directory-watch-interval: 200ms
```

### Admin console
```yaml
admin-console: false
admin-console-token: ""
admin-consolle-editkeys-credentials: ""
admin-consolle-editusers-credentials: ""
service-console: false
service-console-token: ""
```

### Census (custom)
```yaml
census-enabled: false
census-url: ""
census-refresh-time: 2m
strict-id-censed: false
```

### Headers (custom)
```yaml
headers-managed: false
headers-setting-directory: ""
headers-setting-directory-watch-interval: 200ms
```

### TLS
```yaml
https: false
https-certificate-directory: deploy/ssl/
https-ondemand-certificate: false
https-ondemand-certificate-email: ""
force-all-https: false
force-https: false
```

### Tunnel behavior
```yaml
bind-random-subdomains: true
bind-random-aliases: true
bind-random-ports: true
force-requested-subdomains: false
force-requested-ports: false
enable-force-connect: false
port-bind-range: "0,1024-65535"
```

### IP filtering
```yaml
banned-ips: ""
banned-countries: ""
whitelisted-ips: ""
whitelisted-countries: ""
geodb: false
```

### Cleanup
```yaml
cleanup-unauthed: true
cleanup-unauthed-timeout: 5s
cleanup-unbound: false
cleanup-unbound-timeout: 5s
idle-connection: true
idle-connection-timeout: 5s
ping-client: true
ping-client-interval: 5s
ping-client-timeout: 5s
```

---

## 9. File modificati rispetto all'upstream

```
cmd/sish.go                      ← tutti i flag custom
config.example.yml               ← config di esempio aggiornata
Dockerfile                       ← tzdata + zoneinfo per timezone
sshmuxer/sshmuxer.go             ← bootstrap watcher/census/enforcer
sshmuxer/channels.go             ← comando id= + ConnectionIDProvided
sshmuxer/requests.go             ← enforcement strict census in handleRemoteForward
sshmuxer/strict_census.go        ← NEW: enforcer post-refresh
utils/conn.go                    ← campo ConnectionIDProvided in SSHConnection
utils/utils.go                   ← auth utenti YAML + WatchAuthUsers
utils/census.go                  ← NEW: census cache + refresh + helpers
utils/headers_settings.go        ← NEW: header management YAML
utils/console.go                 ← admin routes, API insertkey/insertuser, history, census UI
utils/authentication_users_test.go ← NEW: test auth utenti
httpmuxer/httpmuxer.go           ← routing API root host + apply headers
templates/header.tmpl            ← navbar aggiornata
templates/editkeys.tmpl          ← NEW: pagina edit chiavi
templates/editusers.tmpl         ← NEW: pagina edit utenti
templates/history.tmpl           ← aggiornata: paginazione, ricerca, clear, CSV
templates/census.tmpl            ← NEW: pagina census
templates/routes.tmpl            ← aggiornata: dot censito/non censito
go.mod / go.sum                  ← Go 1.26, toolchain go1.26.1, dipendenze aggiornate
.github/workflows/build.yml      ← Go 1.26
.github/workflows/release.yml    ← Go 1.26
.github/workflows/docs.yml       ← Go 1.26
README_USERS.md                  ← NEW: doc auth users
README_HEADERS.md                ← NEW: doc headers
README_CENSUS.md                 ← NEW: doc census + strict
README_DEV_09032026.md           ← NEW: log completo sessione 09/03/2026
```

---

## 10. Esempi di avvio completi

### Avvio development locale
```bash
go run main.go \
  --debug=true \
  --authentication=true \
  --admin-console=true \
  --admin-console-token='dev-token' \
  --ssh-address=:2222 \
  --http-address=:8080 \
  --domain=localhost
```

### Avvio completo con tutte le feature custom
```bash
go run main.go \
  --domain=tuns.example.com \
  --ssh-address=:2222 \
  --http-address=:80 \
  --authentication=true \
  --auth-users-enabled=true \
  --auth-users-directory=/srv/sish/users \
  --authentication-keys-directory=/srv/sish/pubkeys \
  --admin-console=true \
  --admin-console-token='admin-token' \
  --admin-consolle-editkeys-credentials='keyuser:keypass' \
  --admin-consolle-editusers-credentials='useruser:userpass' \
  --headers-managed=true \
  --headers-setting-directory=/srv/sish/headers \
  --census-enabled=true \
  --census-url='https://example.com/census.yaml' \
  --census-refresh-time=2m \
  --strict-id-censed=true
```

### Client SSH con ID (richiesto in strict mode)
```bash
ssh -p 2222 \
  -R seastream-demo:80:localhost:8080 \
  tuns.example.com \
  id=seastream-demo note="Production service"
```

---

## 11. Sicurezza — punti critici

| Area | Meccanismo |
|------|-----------|
| SSH auth chiavi | Directory watcher + URL remote validation |
| SSH auth password globale | `PasswordCallback` con `ConstantTimeCompare` |
| SSH auth per-utente | YAML directory + `ConstantTimeCompare` |
| Admin console | Token `x-authorization` query param/header |
| editkeys / editusers | Basic Auth extra (oltre al token admin) |
| API insertkey / insertuser | Basic Auth (editkeys/editusers credentials) |
| Path traversal | `filepath.Abs` + prefix check sulla directory radice |
| IP blocking | `jpillora/ipfilter` (ban list, country, whitelist) su SSH+HTTP+TCP |
| Strict census | Zero-trust: ID esplicito + census match per ogni bind |
| YAML insertuser | Validazione strutturale prima del write |
| Dedupe API | Scan completo della directory prima di ogni append |
| Commenti API | Sanitizzazione: stripping di `\r\n` |

---

## 12. TODO.md — note per sviluppi futuri

Verificare il file `TODO.md` nella root del progetto per la lista aggiornata delle cose in sospeso.

---

## 13. Documento sessione precedente

Il file `README_DEV_09032026.md` contiene il log cronologico completo di tutte le 20 feature sviluppate nella sessione del 2026-03-09, con specifiche, file coinvolti ed esempi curl per ogni feature. È il riferimento primario per capire le motivazioni delle scelte implementative.
