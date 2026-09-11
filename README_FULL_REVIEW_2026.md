# Full review — hardening, Go 1.27.1, dipendenze

Revisione completa del fork (SSH muxer, forwarder HTTP/HTTPS/TCP/SNI/alias,
metriche, console admin, frontend) con caccia a difetti, race, deadlock e leak.
Ogni difetto risolto è marcato nel codice con il tag `(Bn)` citato qui sotto.

## Esito in breve

| Voce | Stato |
|------|-------|
| Difetti risolti | 27 (`B1`–`B27`) |
| Toolchain | `go 1.27` + `toolchain go1.27.1` |
| Dipendenze | tutte aggiornate (`go get -u ./...` + `go mod tidy`) |
| `go vet ./...` | pulito |
| `go test ./... -race` | verde |
| `govulncheck ./...` | 0 vulnerabilità raggiungibili (era 13) |

## Comandi di verifica

```bash
export GOTOOLCHAIN=go1.27.1
go build ./...
go vet ./...
go test ./... -count=1 -race
govulncheck ./...        # richiede un govulncheck compilato con go1.27.1
```

## Difetti risolti

### Crash remoti (panic dell'intero processo)

| Tag | File | Difetto |
|-----|------|---------|
| `B8` | `utils/conn.go`, `sshmuxer/handle.go`, `sshmuxer/channels.go` | Un client che apriva due canali `session`, o che inviava `shell` **e** `exec` sulla stessa sessione, causava un doppio `close()` sullo stesso channel Go. Introdotti `CloseSessionChannel()` / `CloseExecChannel()` idempotenti (`sync.Once`). |
| `B9` | `sshmuxer/channels.go` | Payload `exec` più corto di 4 byte: lo slice `req.Payload[4:]` andava fuori range. Aggiunto il bound check. |
| `B12` | `utils/sshmux_listener.go` | `Close()` sul listener multiplexato chiudeva il channel su cui il dispatcher poteva essere in `send` → "send on closed channel". Ora `Close()` segnala solo `closed`; il channel non viene mai chiuso. |
| `B18` | `httpmuxer/proxy.go` | Un backend che dichiarava `Content-Encoding: gzip` senza inviare gzip valido faceva dereferenziare un `*gzip.Reader` nil nel response modifier. |

### Stalli e deadlock

| Tag | File | Difetto |
|-----|------|---------|
| `B2` | `utils/conn.go` | `SendMessage` si bloccava per sempre dopo la chiusura della connessione. |
| `B3` | `sshmuxer/strict_census.go` | L'enforcer census faceva notify+cleanup dentro la `Range`: una connessione senza reader di sessione congelava l'enforcement per tutte le altre. Ora raccoglie gli offender e agisce dopo. |
| `B11` | `utils/sshmux_listener.go` | La classificazione SSH/non-SSH era un read bloccante sul path di accept: un peer che si connetteva senza scrivere bloccava l'intera porta multiplexata. Spostata su goroutine per connessione, con `muxHandshakeTimeout` (10s) e `muxHandshakeConcurrency` (256). |
| `B13` | `httpmuxer/https.go` | Stesso schema sull'accept loop HTTPS: il peek del ClientHello per il routing SNI bloccava tutto il listener. Riscritto con `acceptLoop`/`route`, timeout e limite di concorrenza; `inFlight sync.WaitGroup` drenata prima di chiudere `ready`. |
| `B15` | `utils/utils.go` | `holderLock` restava preso per tutta la `PublicKeyCallback`, inclusa la richiesta HTTP di auth: ogni login SSH concorrente veniva serializzato dietro un backend lento. Lock ristretto allo scan in memoria. |
| `B16` | `utils/utils.go` | Response body delle sub-request di autenticazione mai drenato/chiuso: una goroutine reader e una connessione trattenute per ogni tentativo di login. |
| `B27` | `utils/state.go` | Stessa classe sul forward TCP con SNI: il peek del ClientHello gira su goroutine per connessione ma senza deadline, quindi un peer che si connetteva senza inviare nulla teneva goroutine e file descriptor per tutta la vita del processo. Aggiunto `tcpSNIHandshakeTimeout` (10s), azzerato una volta letto l'hello. |

### Race e leak

| Tag | File | Difetto |
|-----|------|---------|
| `B4` | `utils/conn.go` | `ConnectionID` letto e scritto senza lock. Ora `SetConnectionID`/`GetConnectionID` sotto `connIDLock`. |
| `B5` | `utils/conn.go` | `CloseInfo` (initiator/reason) scritto dal path di chiusura e letto dalla console. Ora `atomic.Pointer` + `CloseDetailOnce`. |
| `B7` | `utils/conn.go` + tutti i chiamanti | 14 campi di opzione del tunnel (`hostHeader`, `stripPath`, `sniProxy`, `tcpAddress`, `tcpAlias`, `localForward`, `tcpAliasesAllowedUsers`, `autoClose`, `forceHTTPS`, `forceConnect`, `deadline`, `execMode`, `proxyProto`, `connectionNote`) scritti dalla goroutine delle request di sessione — che il client può inviare in qualsiasi momento — e letti dall'hot path del proxy HTTP, dagli handler TCP/alias, dal ticker delle deadline e dalla console. Resi non esportati dietro `optLock sync.RWMutex`, con accessor Get/Set; `GetTCPAliasesAllowedUsers` e `GetDeadline` restituiscono copie. |
| `B6`, `B20` | `sshmuxer/requests.go`, `utils/utils.go` | TOCTOU nell'allocazione dei bind: `GetOpenHost`/`GetOpenPort`/`GetOpenAlias`/`GetOpenSNIHost` verificano soltanto che il target sia libero, il chiamante registra l'holder dopo. Due forward in gara vedevano entrambi il target libero e si registravano entrambi: il perdente restava in `state.Listeners` senza rotta — esattamente il "dirty forward" segnalato dalla pagina internal. Introdotta `utils.LockBindAllocation()` che rende atomici check e registrazione. |
| `B10` | `sshmuxer/requests.go` | `handleCancelRemoteForward` interrompeva la `Range` sulla prima connessione non proprietaria, lasciando aperto il tunnel da cancellare. |
| `B26` | `sshmuxer/requests.go`, `sshmuxer/forward_lifecycle.go` | La goroutine che attendeva `sshConn.Close` per il cleanup sopravviveva al forward: su una connessione longeva le goroutine si accumulavano indefinitamente. Aggiunto `forwardLifecycle.Done()`, chiuso per primo in `cleanup()`, su cui la watcher fa `select`. |
| `B1` | `utils/conn.go` | Con burst `= bytesPerSecond * burstFactor`, un utente limitato sotto la dimensione del copy buffer otteniva `WaitN(n)` con `n > burst`: la connessione veniva chiusa invece che throttlata. Burst clampato ad almeno un copy buffer. |

### Sicurezza

| Tag | File | Difetto |
|-----|------|---------|
| `B14` | `utils/utils.go` | `RandStringBytesMaskImprSrc` usava `math/rand` con una source nuova per chiamata, seminata con `time.Now().UnixNano()`: valori prevedibili e identici per chiamate nello stesso nanosecondo. Genera i token della service console oltre a subdomain, alias e connection ID — un token indovinabile è un bypass di autenticazione. Riscritta su `crypto/rand` con rejection sampling (alfabeto di 62 caratteri, distribuzione uniforme). |
| `B17` | `utils/internal_status.go` | La pagina `/_sish/internal` stampava `viper.AllSettings()` e gli argomenti di processo grezzi, esponendo passphrase della host key, token admin/service console e credenziali basic-auth degli editor. Aggiunti `isSecretFlagKey` (`passphrase`, `password`, `token`, `credential`, `secret`) e `redactStartupFlags` (forme `--flag value` e `--flag=value`). |
| `B19` | `utils/console.go`, `httpmuxer/httpmuxer.go` | Confronti di token e credenziali con `==`, byte a byte con uscita anticipata. Introdotti `secureCompare`/`secureCredentialsMatch` su `crypto/subtle.ConstantTimeCompare`, applicati alle 4 basic-auth della console, al token admin e a quello di servizio. |
| — | `utils/utils.go` | `verifyDNS` usa `net.DefaultResolver.LookupTXT` con contesto a scadenza (`verifyDNSTimeout = 2s`) invece di una lookup senza limite. |
| — | `templates/header.tmpl` | Frontend: jQuery 3.3.1 → 3.7.1 (CVE-2019-11358, CVE-2020-11022/11023), Bootstrap 4.4.1 → 4.6.2, popper 1.16.0 → 1.16.1. Tutti serviti da cdnjs con `integrity` (SRI), `crossorigin` e `referrerpolicy`; aggiunto `<meta name="referrer" content="no-referrer">`. |

### Metriche e osservabilità

| Tag | File | Difetto |
|-----|------|---------|
| `B21` | `utils/census.go` | `IsIDCensed` copiava in profondità l'intera cache census (ID, source, note) e poi la scandiva linearmente. Gira sul path di bind e, con enforcement strict, una volta al secondo per ogni connessione attiva: un `O(n)` di copia più un `O(n)` di scan per chiamata. Ora è una lookup su mappa sotto `RLock`. |
| `B22` | `utils/console.go`, `utils/internal_status.go` | La risoluzione dei forward ri-scandiva tutti gli holder HTTP/TCP/alias (e ogni server dei balancer) una volta per connessione SSH; `getActiveForwardRows` lo faceva due volte per connessione. Con la console che fa polling ogni secondo il costo era quadratico nel numero di forward. Introdotto `forwardIndex` (socket → forward) costruito una volta per richiesta. |
| `B23` | `utils/internal_status.go` | `sish_dirty_forward_total` è un gauge dei forward sporchi *adesso*, ma su `/_sish/api/internal/metrics` veniva derivato dai contatori cumulativi `dirty_forwards_stable_*`, che possono solo crescere: il gauge era sbagliato e discordava dallo stesso valore mostrato dalla pagina HTML. Ora deriva da uno scan live; lo snapshot dei contatori lifecycle viene preso **dopo** lo scan, che può incrementarli. |
| `B24` | `utils/internal_status.go` | I rate si calcolano contro un campione precedente memorizzato. Avanzarlo sia dallo scrape Prometheus sia dalla pagina HTML faceva troncare a vicenda le finestre e mostrare rate senza senso. Gli scrape usano `lifecycleRatesSnapshot`, che legge senza avanzare. |
| `B25` | `utils/internal_status.go` | I contatori cumulativi `dirty_forwards_stable_*` venivano incrementati per ogni riga stabile a ogni poll: un problema che semplicemente persisteva li gonfiava a ogni refresh della pagina e i contatori misuravano la frequenza di polling, non i forward sporchi. Ora si contano solo le issue che superano la soglia di stabilità in quel passaggio (`newlyStable`). |

### Correttezza minore

- `utils/conn.go` — `rateLimitedReader.Read` preserva l'errore del reader sottostante: un reader che restituisce dati **e** `io.EOF` insieme non viene più trasformato in un errore di trasferimento.
- `utils/conn.go` — `PeekTLSHello` restituisce `errNotTLSHandshake` per traffico non TLS; prima tornava `nil` e i chiamanti loggavano `Unable to read TLS hello: %!s(<nil>)` trattandolo come successo.
- `utils/conn.go` — `CleanUp` verifica `state != nil && state.SSHConnections != nil`.
- `httpmuxer/proxy.go` — il dialer del `RoundTripper` restituisce l'errore di decodifica base64 invece di un errore di connessione fuorviante.
- `utils/conn.go` — `TrySendMessage` (invio non bloccante) per le notifiche emesse mentre si tiene il lock di allocazione bind.
- `sshmuxer/sshmuxer.go` — coda messaggi bufferizzata (`messageQueueSize = 64`).
- `utils/console.go` — `WebClient.Handle` imposta `SetWriteDeadline` (`webClientWriteTimeout = 10s`): un client WebSocket bloccato non trattiene più la goroutine di scrittura.

## Toolchain Go 1.27.1

Aggiornamento verificato e applicato:

- `go.mod`: `go 1.27`, `toolchain go1.27.1`
- `Dockerfile`: `golang:1.26.3-alpine` → `golang:1.27.1-alpine`
- `.github/workflows/{build,release,docs}.yml`: `go-version: 1.26.1` → `1.27.1`

Nessuna API rimossa o modificata in modo incompatibile viene usata dal progetto:
build, `vet` e suite `-race` passano senza modifiche al codice dovute al
cambio di toolchain.

> `govulncheck` va ricompilato con la nuova toolchain
> (`GOTOOLCHAIN=go1.27.1 go install golang.org/x/vuln/cmd/govulncheck@latest`),
> altrimenti rifiuta di analizzare sorgenti che richiedono go1.27.

## Dipendenze

Aggiornate con `go get -u ./...` seguito da `go mod tidy`. Dirette:

| Modulo | Da | A |
|--------|----|----|
| `golang.org/x/crypto` | v0.53.0 | v0.57.0 |
| `golang.org/x/time` | v0.15.0 | v0.16.0 |
| `github.com/jpillora/ipfilter` | v1.4.0 | v1.5.4 |
| `github.com/pires/go-proxyproto` | v0.12.0 | v0.15.0 |
| `github.com/sirupsen/logrus` | v1.9.4 | v1.10.2 |
| `github.com/picosh/pdocs` | 2024-11-18 | 2026-04-15 |

Indirette rilevanti: `golang.org/x/net` v0.56.0 → v0.59.0, `golang.org/x/text`
v0.38.0 → v0.42.0, `golang.org/x/sys` v0.46.0 → v0.48.0, `quic-go` v0.60.0 →
v0.62.0, `mongo-driver/v2` v2.5.0 → v2.9.1, `zap` v1.27.1 → v1.28.0.
`github.com/tomasen/realip` rimosso perché non più referenziato.

### Stato `govulncheck`

Prima: 13 vulnerabilità. Dopo: **0 raggiungibili**. Resta segnalata una sola
vulnerabilità a livello di modulo, `GO-2026-5932` (`golang.org/x/crypto/openpgp`
non manutenuto, senza fix disponibile): il pacchetto non è importato da sish,
quindi non è raggiungibile.

## Test aggiunti

| File | Copre |
|------|-------|
| `utils/conn_hardening_test.go` | `B7`, `B8`: chiusura idempotente dei canali session/exec (anche concorrente), accessor delle opzioni race-free sotto `-race`, getter che restituiscono copie, `GetDeadline` nil quando non impostata, `TrySendMessage` non bloccante e consegna su canale bufferizzato, `rateLimitedReader` che preserva `io.EOF`. |
| `utils/sshmux_listener_test.go` | `B11`, `B12`: una connessione silenziosa non blocca il dispatch, routing per prefisso con replay dei byte peekati, `Close` durante connessioni in volo, `Close` idempotente. |
| `utils/security_hardening_test.go` | `B14`, `B17`, `B19`, `B20`: proprietà del generatore random (lunghezza, alfabeto, unicità, copertura uniforme), `secureCompare`/`secureCredentialsMatch`, redazione dei flag segreti in entrambe le forme, serializzazione e release idempotente di `LockBindAllocation`, `PeekTLSHello` che rifiuta traffico non TLS. |
| `utils/internal_status_hardening_test.go` | `B23`, `B24`, `B25`: gauge dirty live che scende a zero dopo il cleanup mentre il contatore cumulativo resta, scrape che non avanza la finestra dei rate, contatori stabili incrementati una sola volta per issue. |
| `utils/console_forward_index_test.go` | `B22`: l'index produce esattamente ciò che produceva la risoluzione diretta per HTTP, TCP, TCP+SNI, alias, socket multipli e sconosciuti; precedenza dell'etichetta visitor; deduplica. |
| `utils/state_sni_timeout_test.go` | `B27`: una connessione silenziosa verso un forward TCP+SNI viene chiusa dal server invece di restare appesa. |
| `utils/census_test.go` | `B21`: `IsIDCensed` (trim, case, assenti) e sicurezza sotto refresh concorrente, copie difensive di `GetIDSource` e `GetCensusCacheSnapshot`, `RefreshCensusCache` da directory, con census disabilitato e con entrambe le modalità strict spente. |
| `sshmuxer/handle_test.go` | Harness con `viperConfigLock` e `conns sync.WaitGroup` drenata in `t.Cleanup`: corregge un fallimento `-race` intermittente preesistente su `main` (`viper.Set` contro `viper.Get`). |

## Note per chi mantiene

- I campi di opzione di `SSHConnection` sono non esportati **di proposito**: il
  compilatore impedisce così accessi che bypassano `optLock`. Per aggiungere
  un'opzione servono campo non esportato + coppia Get/Set.
- Ogni percorso che alloca un bind deve tenere `utils.LockBindAllocation()` dal
  controllo di disponibilità fino alla registrazione dell'holder, rilasciandolo
  su **tutti** i rami di errore.
- Il cleanup dei forward passa sempre da `forwardLifecycle.cleanup()`; chi
  attende la fine di un forward deve fare `select` su `Done()` oltre che su
  `sshConn.Close`.
- La soglia di stabilità dei dirty forward (2+ cicli) è intenzionale: alza i
  contatori solo quando una issue la supera, mai a ogni poll.
