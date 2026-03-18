# Context Snapshot — 18/03/2026 21:40

Questo file contiene tutto il contesto necessario per riprendere lo sviluppo del fork sish da questo punto.

---

## 1. Cos'è il progetto

Fork di [sish](https://github.com/antoniomika/sish) — un multiplexer SSH reverse tunnel che espone tunnel HTTP/HTTPS/WS/TCP. Go module path: `github.com/antoniomika/sish`. Go 1.26.

### Build & Test

```bash
go build -o sish .                           # build binary
go test ./... -count=1                        # run all tests
go test ./utils/ -run TestName -count=1       # run single test
go test ./... -run TestDoesNotExist -count=1  # compile-check only
gofmt -w <file>                              # format before commit
```

Docker: `docker build -t sish .` (multi-stage, Alpine 3.23 finale con ca-certificates, tzdata, nano, wget; TZ=Europe/Rome).

---

## 2. Architettura

```
main.go → cmd/sish.go         Cobra CLI + Viper config. TUTTI i flag definiti qui.
sshmuxer/                      SSH server, session handling, channel/request multiplexing
  sshmuxer.go                  Start(), handleSSHConn(), ingress ssh/https
  requests.go                  Bind-time auth, allowed-forwarder, strict census
  channels.go                  Channel handlers, TCP alias runtime
  strict_census.go             Strict census enforcement on active connections
  httphandler.go               HTTP forward start events
  tcphandler.go                TCP forward start events
  aliashandler.go              TCP Alias forward start events
httpmuxer/                     HTTP/HTTPS reverse proxy (Gin + certmagic + oxy)
  httpmuxer.go                 Route dispatch, admin root host, API endpoints /api/insertkey, /api/insertuser
  proxy.go                     Forward proxy, transport, body capture
utils/                         Shared state and logic
  conn.go                      SSHConnection struct (33 campi), UserBandwidthProfile, CopyBoth, CleanUp
  console.go                   WebConsole, ALL admin routes/handlers (41 funzioni Handle*), ConnectionHistory
  utils.go                     Per-user YAML auth (authUser struct), loadAuthUsers, watcher
  census.go                    Census cache, RefreshCensusCache, CensusIDSource, IDNotes, dual-source merge
  headers_settings.go          Response headers management, YAML config, watcher
  forwarder_logs.go            Per-forwarder logging, rotation, naming
  internal_status.go           Internal runtime status page (/internal)
  state.go                     State struct (SSHConnections, Listeners, HTTPListeners, etc.)
  authentication_users_test.go Test per auth users
templates/                     Go HTML templates per admin console
  audit.tmpl, census.tmpl, console.tmpl, editcensus.tmpl, editheaders.tmpl,
  editkeys.tmpl, editusers.tmpl, footer.tmpl, header.tmpl, history.tmpl,
  internal.tmpl, logs.tmpl, routes.tmpl
```

---

## 3. Struct principali

### SSHConnection (`utils/conn.go` linee 148-181)

```go
type SSHConnection struct {
    SSHConn                *ssh.ServerConn
    ConnectionID           string
    ConnectionIDProvided   bool
    UserBandwidthProfile   *UserBandwidthProfile
    BandwidthProfileLock   *sync.RWMutex
    BandwidthProfileVer    atomic.Int64
    BandwidthProfileUnixNs atomic.Int64
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
    Ingress                string    // "ssh" o "https"
    IngressPort            string    // porta (es. "2222" o "443")
}
```

### ConnectionHistory (`utils/console.go` linee 62-73)

```go
type ConnectionHistory struct {
    ID           string
    RemoteAddr   string
    Username     string
    StartedAt    time.Time
    EndedAt      time.Time
    Duration     time.Duration
    DataInBytes  int64
    DataOutBytes int64
    Ingress      string
    IngressPort  string
}
```

### WebConsole (`utils/console.go` linee 53-59)

```go
type WebConsole struct {
    Clients     *syncmap.Map[string, []*WebClient]
    RouteTokens *syncmap.Map[string, string]
    History     []ConnectionHistory
    HistoryLock *sync.RWMutex
    State       *State
}
```

### State (`utils/state.go` linee 312-322)

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

### authUser (`utils/utils.go` linee 105-113)

```go
type authUser struct {
    Name              string `yaml:"name"`
    Password          string `yaml:"password"`
    PubKey            string `yaml:"pubkey"`
    BandwidthUpload   string `yaml:"bandwidth-upload"`
    BandwidthDownload string `yaml:"bandwidth-download"`
    BandwidthBurst    string `yaml:"bandwidth-burst"`
    AllowedForwarder  string `yaml:"allowed-forwarder"`
}
```

### UserBandwidthProfile (`utils/conn.go` linee 25-33)

```go
type UserBandwidthProfile struct {
    UploadBytesPerSecond   int64
    DownloadBytesPerSecond int64
    BurstFactor            float64
    UploadLimiter          *rate.Limiter
    DownloadLimiter        *rate.Limiter
    DataInBytes            atomic.Int64
    DataOutBytes           atomic.Int64
}
```

### censusCache (`utils/census.go` linee 36-43)

```go
type censusCache struct {
    IDs         []string
    IDSources   map[string]CensusIDSource
    IDNotes     map[string]string
    LastRefresh time.Time
    LastError   string
    URLFiles    []string
}
```

### CensusIDSource (`utils/census.go` linee 25-28)

```go
type CensusIDSource struct {
    URL   bool     `json:"url"`
    Files []string `json:"files"`
}
```

### Header structs (`utils/headers_settings.go` linee 18-37)

```go
type headerSetting struct {
    Enabled *bool  `yaml:"enabled"`
    Value   string `yaml:"value"`
    Always  *bool  `yaml:"always"`
}
type headerScope struct {
    Headers map[string]headerSetting `yaml:"headers"`
}
type headerSettingsFile struct {
    Defaults   headerScope            `yaml:"defaults"`
    Subdomains map[string]headerScope `yaml:"subdomains"`
}
type resolvedHeaderSetting struct {
    Enabled bool
    Value   string
    Always  bool
}
```

---

## 4. Tutti i CLI flags (cmd/sish.go)

### String flags (51)

| Flag | Default | Descrizione |
|------|---------|-------------|
| `config` / `c` | `config.yml` | Config file |
| `ssh-address` / `a` | `localhost:2222` | SSH listen address |
| `http-address` / `i` | `localhost:80` | HTTP listen address |
| `https-address` / `t` | `localhost:443` | HTTPS listen address |
| `tcp-address` | `""` | TCP listen address |
| `redirect-root-location` / `r` | `https://github.com/antoniomika/sish` | Root domain redirect |
| `https-certificate-directory` / `s` | `deploy/ssl/` | HTTPS cert dir |
| `https-ondemand-certificate-email` | `""` | Let's Encrypt email |
| `domain` / `d` | `ssi.sh` | Root domain |
| `banned-subdomains` / `b` | `localhost` | Banned subdomains |
| `banned-aliases` | `""` | Banned aliases |
| `banned-ips` / `x` | `""` | Banned IPs |
| `banned-countries` / `o` | `""` | Banned countries |
| `whitelisted-ips` / `w` | `""` | Whitelisted IPs |
| `whitelisted-countries` / `y` | `""` | Whitelisted countries |
| `private-key-passphrase` / `p` | `S3Cr3tP4$$phrAsE` | Server key passphrase |
| `private-keys-directory` / `l` | `deploy/keys` | SSH private keys dir |
| `authentication-password` / `u` | `""` | Global SSH password |
| `auth-users-directory` | `""` | Per-user YAML auth directory |
| `authentication-keys-directory` / `k` | `deploy/pubkeys/` | Public keys dir |
| `authentication-key-request-url` | `""` | Remote key auth URL |
| `authentication-password-request-url` | `""` | Remote password auth URL |
| `port-bind-range` / `n` | `0,1024-65535` | TCP port range |
| `proxy-protocol-version` / `q` | `1` | Proxy protocol version |
| `proxy-protocol-policy` | `use` | Proxy protocol policy |
| `admin-console-token` / `j` | `""` | Admin console token |
| `admin-consolle-editkeys-credentials` | `""` | Editkeys Basic Auth |
| `admin-consolle-editusers-credentials` | `""` | Editusers Basic Auth |
| `admin-consolle-editheaders-credentials` | `""` | Editheaders Basic Auth |
| `admin-consolle-editcensus-credentials` | `""` | Editcensus Basic Auth |
| `service-console-token` / `m` | `""` | Service console token |
| `append-user-to-subdomain-separator` | `-` | User-subdomain separator |
| `time-format` | `2006/01/02 - 15:04:05` | Time format |
| `log-to-file-path` | `/tmp/sish.log` | Log file path |
| `forwarders-log` | `disable` | Per-forwarder logs (enable/disable) |
| `forwarders-log-dir` | `/fwlogs` | Per-forwarder logs dir |
| `forwarders-log-time-format` | `""` | Per-forwarder time format |
| `bind-hosts` | `""` | Additional bindable hosts |
| `load-templates-directory` | `templates/*` | Templates glob |
| `headers-setting-directory` | `""` | Headers config dir |
| `census-url` | `""` | Remote census URL |
| `census-directory` | `""` | Local census dir |
| `welcome-message` | `Press Ctrl-C to close the session.` | Welcome msg |

### Boolean flags (65)

| Flag | Default | Descrizione breve |
|------|---------|-------------------|
| `force-requested-ports` | `false` | Forza porte richieste |
| `force-requested-aliases` | `false` | Forza alias richiesti |
| `force-requested-subdomains` | `false` | Forza subdomains richiesti |
| `enable-force-connect` | `false` | Abilita takeover (force-connect=true) |
| `force-tcp-address` | `false` | Forza TCP address |
| `bind-random-subdomains` | `true` | Random subdomains |
| `bind-random-aliases` | `true` | Random aliases |
| `verify-ssl` | `true` | Verifica SSL upstream |
| `verify-dns` | `true` | Verifica DNS |
| `cleanup-unauthed` | `true` | Cleanup connessioni non autenticate |
| `cleanup-unbound` | `false` | Cleanup connessioni senza forward |
| `bind-random-ports` | `true` | Porte TCP random |
| `append-user-to-subdomain` | `false` | Append user a subdomain |
| `debug` | `false` | Debug mode |
| `ping-client` | `true` | Ping SSH clients |
| `geodb` | `false` | GeoIP database |
| `authentication` | `true` | SSH auth required |
| `auth-users-enabled` | `false` | Per-user YAML auth |
| `proxy-protocol` | `false` | Proxy protocol outbound |
| `proxy-protocol-use-timeout` | `false` | PP timeout |
| `proxy-protocol-listener` | `false` | PP inbound |
| `proxy-ssl-termination` | `false` | Behind SSL proxy |
| `https` | `false` | HTTPS listener |
| `ssh-over-https` | `false` | SSH multiplex su HTTPS |
| `force-all-https` | `false` | Redirect tutto a HTTPS |
| `force-https` | `false` | Per-bind HTTPS enforcement |
| `redirect-root` | `true` | Redirect root domain |
| `admin-console` | `false` | Admin console |
| `service-console` | `false` | Service console per tunnel |
| `tcp-aliases` | `false` | TCP aliasing |
| `sni-proxy` | `false` | SNI proxy |
| `sni-proxy-https` | `false` | SNI proxy su HTTPS |
| `log-to-client` | `false` | Log a client SSH |
| `idle-connection` | `true` | Idle timeout |
| `http-load-balancer` | `false` | HTTP LB |
| `tcp-load-balancer` | `false` | TCP LB |
| `sni-load-balancer` | `false` | SNI LB |
| `alias-load-balancer` | `false` | Alias LB |
| `localhost-as-all` | `true` | localhost = 0.0.0.0 |
| `log-to-stdout` | `true` | Log a stdout |
| `log-to-file` | `false` | Log a file |
| `log-to-file-compress` | `false` | Comprimi log |
| `forwarders-log-compress` | `false` | Comprimi forwarder log |
| `https-ondemand-certificate` | `false` | Let's Encrypt on-demand |
| `https-ondemand-certificate-accept-terms` | `false` | Accetta LE terms |
| `bind-http-auth` | `true` | HTTP auth su forward |
| `bind-http-path` | `true` | Path specifico su forward |
| `strip-http-path` | `true` | Strip path |
| `bind-any-host` | `false` | Bind any host |
| `bind-root-domain` | `false` | Bind root domain |
| `bind-wildcards` | `false` | Bind wildcards |
| `load-templates` | `true` | Carica templates |
| `headers-managed` | `false` | Managed response headers |
| `history-enabled` | `false` | Pagina history |
| `census-enabled` | `false` | Feature census |
| `show-internal-state` | `false` | Pagina internal |
| `user-bandwidth-limiter-enabled` | `false` | Limiter banda per utente |
| `bandwidth-hot-reload-enabled` | `false` | Hot-reload limiti banda |
| `strict-id-censed` | `false` | Strict census (legacy, abilita entrambi) |
| `strict-id-censed-url` | `false` | Strict census da URL |
| `strict-id-censed-files` | `false` | Strict census da files + editcensus |
| `rewrite-host-header` | `true` | Riscrivi host header |
| `tcp-aliases-allowed-users` | `false` | Allowed users su TCP aliases |

### Integer flags (7)

| Flag | Default | Descrizione |
|------|---------|-------------|
| `http-port-override` | `0` | Override porta HTTP output |
| `https-port-override` | `0` | Override porta HTTPS output |
| `http-request-port-override` | `0` | Override porta HTTP request |
| `https-request-port-override` | `0` | Override porta HTTPS request |
| `bind-random-subdomains-length` | `3` | Lunghezza subdomains random |
| `bind-random-aliases-length` | `3` | Lunghezza aliases random |
| `log-to-file-max-size` | `500` | Max size log MB |
| `log-to-file-max-backups` | `3` | Max file log rotated |
| `log-to-file-max-age` | `28` | Max giorni log |
| `forwarders-log-max-size` | `100` | Max size forwarder log MB |
| `forwarders-log-max-backups` | `10` | Max file forwarder log |
| `forwarders-log-max-age` | `30` | Max giorni forwarder log |
| `service-console-max-content-length` | `-1` | Max content length console |

### Duration flags (12)

| Flag | Default | Descrizione |
|------|---------|-------------|
| `debug-interval` | `2s` | Intervallo debug loop |
| `idle-connection-timeout` | `5s` | Idle timeout |
| `ping-client-interval` | `5s` | Ping interval |
| `ping-client-timeout` | `5s` | Ping timeout |
| `cleanup-unauthed-timeout` | `5s` | Cleanup unauthed timeout |
| `cleanup-unbound-timeout` | `5s` | Cleanup unbound timeout |
| `proxy-protocol-timeout` | `200ms` | PP header timeout |
| `authentication-keys-directory-watch-interval` | `200ms` | Watch keys interval |
| `auth-users-directory-watch-interval` | `200ms` | Watch users interval |
| `bandwidth-hot-reload-time` | `20s` | Hot-reload banda interval |
| `headers-setting-directory-watch-interval` | `200ms` | Watch headers interval |
| `census-refresh-time` | `2m` | Census refresh interval |
| `https-certificate-directory-watch-interval` | `200ms` | Watch certs interval |
| `authentication-key-request-timeout` | `5s` | Remote key auth timeout |
| `authentication-password-request-timeout` | `5s` | Remote password auth timeout |

---

## 5. Pagine frontend e route console

### Template → Pagina → Route

| Template | Pagina | Route | Protezione |
|----------|--------|-------|------------|
| `routes.tmpl` | Clients (sish) | `/_sish/console` | Admin token |
| `history.tmpl` | History | `/_sish/history` | Admin token + `history-enabled` |
| `audit.tmpl` | Audit | `/_sish/audit` | Admin token |
| `logs.tmpl` | Logs | `/_sish/logs` | Admin token + `forwarders-log=enable` |
| `census.tmpl` | Census | `/_sish/census` | Admin token + `census-enabled` |
| `internal.tmpl` | Internal | `/_sish/internal` | Admin token + `show-internal-state` |
| `editkeys.tmpl` | Edit Keys | `/_sish/editkeys` | Basic Auth (`admin-consolle-editkeys-credentials`) |
| `editusers.tmpl` | Edit Users | `/_sish/editusers` | Basic Auth (`admin-consolle-editusers-credentials`) |
| `editheaders.tmpl` | Edit Headers | `/_sish/editheaders` | Basic Auth (`admin-consolle-editheaders-credentials`) |
| `editcensus.tmpl` | Edit Census | `/_sish/editcensus` | Basic Auth (`admin-consolle-editcensus-credentials`) + `census-enabled` + `strict-id-censed-files` + `census-directory` |

### Navbar visibility (templateData Show* fields, console.go ~linea 1433-1452)

| Campo | Condizione |
|-------|------------|
| `ShowHistory` | admin + `history-enabled=true` |
| `ShowCensus` | admin + `census-enabled=true` |
| `ShowInternal` | admin + `show-internal-state=true` |
| `ShowAudit` | admin (sempre) |
| `ShowLogs` | admin + `forwarders-log=enable` |
| `ShowEditKeys` | admin + `admin-consolle-editkeys-credentials` valido |
| `ShowEditUsers` | admin + `admin-consolle-editusers-credentials` valido |
| `ShowEditHeaders` | admin + `admin-consolle-editheaders-credentials` valido |
| `ShowEditCensus` | admin + `admin-consolle-editcensus-credentials` valido + `census-enabled` + `strict-id-censed-files` + `census-directory` |

---

## 6. API endpoints

### History
- `GET /_sish/api/history` — lista paginata + search (ID, remoteAddr, username, ingress, date)
- `POST /_sish/api/history/clear` — cancella history
- `GET /_sish/api/history/download` — CSV export

### Census
- `GET /_sish/api/census` — stato census (proxyCensed, proxyUncensed, censedNotForwarded, source tracking)
- `POST /_sish/api/census/refresh` — refresh manuale
- `GET /_sish/api/census/source` — sorgente raw + validazione YAML

### Audit
- `GET /_sish/api/audit` — bandwidth snapshot + origin IP stats

### Logs
- `GET /_sish/api/logs/files` — lista file forwarder
- `GET /_sish/api/logs/file?file=<path>&lines=<n>` — tail
- `GET /_sish/api/logs/download?file=<path>` — download

### Edit Keys
- `GET /_sish/api/editkeys/files` — lista file chiavi
- `GET /_sish/api/editkeys/file?file=<name>` — leggi file
- `POST /_sish/api/editkeys/file` — scrivi file

### Edit Users
- `GET /_sish/api/editusers/files` — lista file utenti
- `GET /_sish/api/editusers/file?file=<name>` — leggi file
- `POST /_sish/api/editusers/file` — scrivi file
- `POST /_sish/api/editusers/validate` — valida YAML

### Edit Headers
- `GET /_sish/api/editheaders/files` — lista file headers
- `GET /_sish/api/editheaders/file?file=<name>` — leggi file
- `POST /_sish/api/editheaders/file` — scrivi file
- `POST /_sish/api/editheaders/validate` — valida YAML

### Edit Census
- `GET /_sish/api/editcensus/files` — lista file census locali
- `GET /_sish/api/editcensus/file?file=<name>` — leggi file
- `POST /_sish/api/editcensus/file` — scrivi file (+ refresh cache)
- `POST /_sish/api/editcensus/validate` — valida YAML

### Internal
- `GET /_sish/api/internal` — runtime status completo (memory, goroutines, flags, forwards, dirty)

### Clients
- `GET /_sish/api/clients` — lista client connessi con clientInfo, configInfo, ingressInfo, listeners, routeListeners
- `GET /_sish/api/clients/disconnect/<remoteAddr>` — disconnetti client
- WebSocket: `/_sish/console/ws` — real-time updates

### External APIs (httpmuxer)
- `POST /api/insertkey` — inserisci chiave SSH
- `POST /api/insertuser` — inserisci utente SSH

---

## 7. Frontend: pagina sish (routes.tmpl)

### Colonne tabella client
ID | CID | Client Remote Address | Username | SSH Version | Info | Listeners | Connection Stats | Notes | Session | Fingerprint | Disconnect

### Modal Info (showInfo)
Tre sezioni renderizzate con `renderInfoSection()`:
1. **INGRESS** — type (SSH/Multiplexer), port
2. **SEZIONE CLIENT** — 15 campi: id, id-provided, force-connect, force-https, proxy-protocol, host-header, strip-path, sni-proxy, tcp-address, tcp-alias, local-forward, auto-close, tcp-aliases-allowed-users, deadline, exec-mode
3. **SEZIONE CONFIG** — 7 campi: name, password (REDACTED), pubkey (REDACTED), bandwidth-upload, bandwidth-download, bandwidth-burst, allowed-forwarder

### Modal Disconnect
Conferma con pulsanti Disconnect/Cancel prima di procedere.

### Observables KnockoutJS per client
`pubKeyFingerprint`, `isCensused`, `dataInBytes`, `dataOutBytes`, `clientInfo`, `configInfo`, `ingressInfo`, `listeners`, `listenerCount`, `connectionDuration`, `connectionStatsTooltip`, `hasNotes`

---

## 8. Frontend: pagina history (history.tmpl)

### Colonne tabella
ID | Client Remote Address | Username | Ingress | Started | Ended | Duration | Transfer

### Funzionalità
- Paginazione (10 per pagina)
- Search (min 2 caratteri, case-insensitive) su: ID, remoteAddr, username, ingress, started, ended
- Download CSV
- Clear all (con conferma)

---

## 9. Frontend: pagina census (census.tmpl)

### Pannello stato
URL Active/Disabled, Files Active/Disabled, Last refresh, Auto refresh

### Tabelle
- **Proxy Censed**: ID, Listeners, Remote Addr, Source (badge url/file), Note (pulsante), Forward
- **Proxy Uncensed**: ID, Listeners, Remote Addr, Forward
- **Censed Not Forwarded**: ID, Source, Note

---

## 10. Frontend: pagina internal (internal.tmpl)

### Sezioni
- **Header**: Generated at, AppVersion, Go, Goroutines, Total Allocated Memory, Dirty Listeners
- **Runtime Overview**: Memory (MB), Runtime Counters (leggibili)
- **Startup Flags**: tutti i flag passati all'avvio
- **State Counts**: sshConnections, listeners, httpListeners, tcpListeners, aliasListeners, dirtyListeners
- **Active Forwards**: tabella con Type, Listener Address, Client Remote, Connection ID, Started, Data Usage
- **Dirty Listeners**: listener orfani

---

## 11. Ingress tracking (Spec 15 + 16)

### Come funziona
- `sshmuxer/sshmuxer.go`: `handleSSHConn(conn, ingress)` riceve `"ssh"` o `"https"` come parametro
- I valori vengono salvati in `SSHConnection.Ingress` e `SSHConnection.IngressPort`
- Al cleanup, vengono copiati in `ConnectionHistory.Ingress` e `ConnectionHistory.IngressPort`

### Dove viene mostrato
- **Pagina sish**: nel modal Info, sezione "INGRESS" con type (SSH/Multiplexer) e port
- **Pagina history**: colonna "Ingress" con formato `"SSH (:2222)"` o `"Multiplexer (:443)"`
- **CSV download**: colonna "Ingress"

### Helper condiviso
`ingressLabel(ingress string) string` in `utils/console.go`: mappa `"ssh"`→`"SSH"`, `"https"`→`"Multiplexer"`, default→`"Unknown"`

---

## 12. Bandwidth hot-reload (Spec 14)

### Come funziona
- Flag: `--bandwidth-hot-reload-enabled=true` + `--bandwidth-hot-reload-time=20s`
- `utils/utils.go`: dopo `loadAuthUsers()`, se hot-reload è attivo, una goroutine periodica fa reconcile su `state.SSHConnections`
- Per ogni connessione attiva, confronta il profilo corrente con quello aggiornato in `authUsersBandwidthHolder`
- Se diverso, aggiorna il profilo via `SSHConnection.SetBandwidthProfile()` (thread-safe con `BandwidthProfileLock`)
- `rateLimitedReader` in `utils/conn.go` è dinamico: risolve il limiter ad ogni read tramite getter atomico
- Campi tracking: `BandwidthProfileVer` e `BandwidthProfileUnixNs` per versioning

---

## 13. Census dual-source (Spec 02 + 03)

### Come funziona
- `RefreshCensusCache()` in `utils/census.go` legge da entrambe le sorgenti
- `--strict-id-censed-url=true` + `--census-url`: scarica da URL
- `--strict-id-censed-files=true` + `--census-directory`: legge file YAML locali
- Gli ID vengono mergiati e deduplicati. Ogni ID traccia la sorgente in `CensusIDSource`
- Note opzionali per ID (`IDNotes`)

---

## 14. Specs implementate (tutte completate)

| Spec | Descrizione |
|------|-------------|
| 01 | Edit Headers page (`/_sish/editheaders`) |
| 02 | Census da directory locale + Edit Census page |
| 03 | Census source control (`strict-id-censed-url/files` split) |
| 04 | Census per-ID note opzionale |
| 05 | API insertuser — tutti i parametri |
| 06 | Dockerfile da scratch a Alpine 3.23 |
| 07 | Pulsante Notes condizionale (sish + census) |
| 08 | Colonna Forward in census |
| 09 | Conferma disconnessione con modal |
| 10 | Internal runtime status page |
| 11 | Miglioramenti Memory e State Counts in internal |
| Bug 10/11 | Fix totalAllocMB e memoria in testata |
| 12 | Colonna Data Usage in Active Forwards |
| 13 | Runtime Counters leggibili |
| 14 | Bandwidth hot-reload |
| 15 | Sezione Ingress nel modal info (sish) |
| 16 | Colonna Ingress nella pagina history |

---

## 15. Convenzioni di sviluppo

- **Flag**: definiti in `cmd/sish.go`, acceduti con `viper.GetString("flag-name")` ovunque
- **Thread-safety**: `sync.RWMutex` per cache (census, headers), `syncmap.Map` per strutture condivise
- **Test**: in `utils/`, accanto al sorgente
- **Frontend**: KnockoutJS, Bootstrap 4, jQuery. Polling periodico con guard anti-overlap
- **Stili**: riusare quelli esistenti (Bootstrap btn-*, table-*, modal, badge)
- **Note pulsanti**: visibili solo se nota non vuota (`.trim().length > 0`)
- **Sensitive data**: password e pubkey mostrati come "REDACTED" nel frontend

---

## 16. File di documentazione nel progetto

| File | Contenuto |
|------|-----------|
| `CLAUDE.md` | Guida AI per contesto progetto |
| `README.md` | README principale con indice |
| `README_CONSOLLE.md` | Dashboard admin: pagine, visibility matrix |
| `README_CENSUS.md` | Census: flag, sorgenti, strict, API |
| `README_HEADERS.md` | Response headers: YAML, merge, subdomain override |
| `README_API.md` | API insertuser/insertkey |
| `README_USERS.md` | Per-user YAML auth, hot-reload |
| `README_COMMANDS.md` | Comandi SSH/autossh, env SISH_* |
| `README_FORCE_CONNECT.md` | Takeover force-connect |
| `README_SSH_SSL.md` | SSH over HTTPS multiplexing |
| `README_FWLOGS.md` | Forwarder logs, rotation, console logs |
| `README_USER_BANDWIDTH_LIMIT.md` | Limiti banda per utente + hot-reload |
| `README_NGINX_REAL_IP.md` | Nginx + proxy-protocol per IP reale |
| `README_SPECS_01_02.md` | Doc sviluppo Spec 01 e 02 |
| `README_SPECS_03.md` | Doc sviluppo Spec 03 |
| `PLAN_BANDW_DEV.md` | Piano hot-reload banda (implementato) |
| `README_ANALISYS.md` | Analisi production-grade |
| `README_SYNC_UPSTREAM.md` | Sync fork con upstream |
| `TODO.md` | Todo list con tutte le spec completate |
| `Specs.md` | Specifiche di sviluppo (Spec 1-16) |
| `SSH_SISH_CLIENT.md` | Docker SSH client |
| `README_CLIENT_SISH_SSH.md` | Handoff docker-client |

---

## 17. Dockerfile attuale

```dockerfile
FROM golang:1.26.1-alpine AS builder
# ... build stage ...
FROM alpine:3.23 AS release
RUN apk add --no-cache ca-certificates tzdata nano wget
ENV TZ=Europe/Rome
# ... copy binary + templates + deploy ...
ENTRYPOINT ["/app/app"]
```

---

## 18. Come riprendere

1. Leggi questo file per contesto completo
2. `go build -o sish .` per verificare che compili
3. `go test ./... -count=1` per verificare i test
4. Consulta `Specs.md` per eventuali nuove specifiche
5. Per ogni modifica, segui le convenzioni esistenti:
   - Flag in `cmd/sish.go`
   - Logica in `utils/` (file dedicato o in `console.go`)
   - Frontend in `templates/`
   - Template data in `console.go` → `templateData` map
   - Route in `console.go` → dispatcher `if/else if`
