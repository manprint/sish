# README_ASSESMENT

Data: 21 aprile 2026

## Obiettivo

Questo file riassume:

- l'assessment tecnico eseguito sul progetto
- i bug confermati durante l'analisi
- i fix implementati nel remediation pass
- i test di regressione aggiunti
- la validazione finale eseguita per ridurre il rischio di regressioni

L'assessment ha coperto sia backend sia frontend console, con focus specifico su:

- race condition
- deadlock e blocchi su path sensibili
- goroutine leak / ticker leak
- crescita memoria non bounded
- incongruenze frontend e admin console
- robustezza delle scritture file e hot-reload runtime

## Aree analizzate

- `httpmuxer/`
- `sshmuxer/`
- `utils/`
- `templates/`

In particolare:

- listener HTTPS/SNI
- registry websocket console
- history/audit/log caches
- hot-reload utenti YAML
- API admin e scritture file gestite
- pagine frontend `sish`, `audit`, `internal`

## Bug confermati durante l'assessment

### 1. Listener HTTPS con flusso ricorsivo

File coinvolto:

- `httpmuxer/https.go`

Problema confermato:

- `proxyListener.Accept()` richiamava ricorsivamente sé stesso sia dopo errori sia dopo connessioni proxy gestite correttamente.

Rischio:

- crescita stack
- degradazione runtime nel lungo periodo
- comportamento fragile sotto churn elevato

### 2. Console websocket con mutazioni concorrenti e broadcast bloccante

File coinvolto:

- `utils/console.go`

Problemi confermati:

- la mappa `Clients` era thread-safe, ma il valore `[]*WebClient` non era protetto
- `AddClient`, `RemoveClient` e `BroadcastRoute` potevano competere sugli stessi slice
- il broadcast scriveva su channel non bufferizzato, quindi un client lento poteva bloccare il path chiamante

Rischio:

- race condition
- freeze della console o di path applicativi che invocano `BroadcastRoute`
- leak di websocket/goroutine lato admin console

### 3. Hot-reload utenti YAML non atomico

File coinvolto:

- `utils/utils.go`

Problema confermato:

- `loadAuthUsers()` costruiva lo snapshot ma, in presenza di file invalidi o campi invalidi, poteva scartare singole parti e continuare il reload.

Rischio:

- stato parziale in memoria
- comportamento fail-open/fail-inconsistent
- differenze tra file su disco e stato effettivo runtime

### 4. Scritture file admin/API non atomiche

File coinvolti:

- `utils/console.go`

Problemi confermati:

- scritture dirette con `os.WriteFile`
- assenza di replace atomico same-directory
- assenza di refresh immediato consistente dopo il salvataggio

Rischio:

- file parziali in caso di errore/interruzione
- inconsistenze tra contenuto su disco e cache runtime

### 5. Escape tramite symlink nella risoluzione file gestiti

File coinvolto:

- `utils/console.go`

Problema confermato:

- la validazione del path gestiva `..`, ma non risolveva completamente i symlink prima del check finale.

Rischio:

- accesso o overwrite di file fuori dalla directory gestita

### 6. History in-memory non limitata e lock hold troppo lungo

File coinvolti:

- `utils/console.go`
- `utils/conn.go`

Problemi confermati:

- `WebConsole.History` cresceva senza limite
- alcune API history iteravano tenendo il lock per tutta la serializzazione
- l'append history avveniva sul path di cleanup della connessione

Rischio:

- crescita RAM nel tempo
- maggiore pressione GC
- cleanup rallentato o accodato

### 7. Audit origin IP non limitato

File coinvolto:

- `utils/audit.go`

Problema confermato:

- la mappa globale degli origin IP poteva crescere indefinitamente.

Rischio:

- crescita memoria non bounded in scenari con molti IP sorgente

### 8. Cache writer forwarder log senza lifecycle

File coinvolto:

- `utils/forwarder_logs.go`

Problemi confermati:

- lock globale serializzante
- writer cache non ripulita
- nessun meccanismo di idle cleanup

Rischio:

- crescita oggetti/file handle nel tempo
- contesa inutile tra scritture indipendenti

### 9. Ticker non stoppato nel cleanup watchdog

File coinvolto:

- `sshmuxer/sshmuxer.go`

Problema confermato:

- ticker creato senza `Stop()` esplicito.

Rischio:

- leak di risorse nel tempo

### 10. Incongruenze frontend console

File coinvolti:

- `templates/routes.tmpl`
- `templates/internal.tmpl`
- `templates/audit.tmpl`

Problemi confermati:

- costruzione URL API fragile con `replace("console", ...)`
- disconnect route/client basato su path poco robusti
- aggiornamento live dei campi client non completamente coerente
- metrica memoria `totalAllocMB` visualizzata usando `allocBytes`
- refresh audit che resettava sempre la pagina corrente

Rischio:

- UX incoerente
- azioni admin fragili
- metriche fuorvianti

## Fix implementati

### Backend / runtime

#### HTTPS / SNI

- `httpmuxer/https.go`

Intervento:

- sostituito il flusso ricorsivo di `Accept()` con loop iterativo
- mantenuto lo skip delle connessioni invalide/bloccate senza crescita stack

#### SSH cleanup watchdog

- `sshmuxer/sshmuxer.go`

Intervento:

- aggiunto `defer ticker.Stop()` nel watchdog per connessione

#### History lifecycle

- `utils/conn.go`
- `utils/console.go`

Interventi:

- la history entry viene costruita prima del cleanup, ma aggiunta dopo il rilascio delle risorse principali
- introdotto limite globale `maxHistoryEntries`
- introdotto snapshot copy-on-read per ridurre il tempo sotto lock
- corretta la gestione delle pagine fuori range nelle API history

#### Audit cache

- `utils/audit.go`

Intervento:

- introdotto limite `maxOriginIPAuditEntries`
- eviction dell'entry più vecchia quando il limite è raggiunto

#### Forwarder logs

- `utils/forwarder_logs.go`

Interventi:

- sostituito lock globale write-only con `RWMutex`
- introdotto lock per writer
- introdotto idle cleanup dei writer inutilizzati
- ridotta la serializzazione globale delle scritture

### Auth / file management

#### Hot-reload utenti YAML

- `utils/utils.go`

Interventi:

- validazione strutturale completa del file prima dell'applicazione
- reload atomico dell'intera directory
- se un file è invalido, lo snapshot precedente resta attivo
- eliminati gli apply parziali su pubkey/bandwidth/allowed-forwarder invalidi

#### Scritture file atomiche

- `utils/console.go`

Interventi:

- introdotta helper `writeFileAtomic()`
- introduzione di replace atomico via temp file nella stessa directory
- riuso del mode esistente con fallback sicuro
- refresh immediato dello stato runtime dopo save/insert API:
  - `loadKeys()`
  - `loadAuthUsers()`
  - `loadHeaderSettingsConfig()`
  - `RefreshCensusCache()` già mantenuto lato census

#### Path hardening contro symlink escape

- `utils/console.go`

Intervento:

- `resolveManagedFile()` ora risolve `EvalSymlinks()` su base dir, file esistente o parent dir prima del controllo finale di containment

### Console / websocket / admin actions

#### Registry websocket robusto

- `utils/console.go`

Interventi:

- aggiunto `ClientsLock`
- bufferizzato `WebClient.Send`
- aggiunti `Done`, `Close()` e `ReadPump()`
- `BroadcastRoute()` ora non blocca su client lenti
- i client saturi/disconnessi vengono chiusi e rimossi
- `AddRoute`, `RemoveRoute`, `AddClient`, `RemoveClient` ora lavorano su slice copiati e sincronizzati

#### Disconnect client / route

- `utils/console.go`
- `templates/routes.tmpl`

Interventi:

- supporto sia ai path legacy sia a endpoint POST JSON strutturati
- supporto route singola o multipla nel payload
- risposta con contatore `closed`

### Frontend console

#### Sish page

- `templates/routes.tmpl`

Interventi:

- costruzione URL API con `URL` e propagazione token `x-authorization`
- fix del redirect `forwardToConsole()` con gestione robusta di host e credenziali
- campi client principali convertiti in observable aggiornabili live
- fix del refresh client senza inconsistenze sui dati mostrati

#### Internal page

- `templates/internal.tmpl`

Intervento:

- `totalAllocatedMB` e `totalAllocMB` ora usano `memory.totalAllocBytes`

#### Audit page

- `templates/audit.tmpl`

Intervento:

- il refresh mantiene la pagina corrente quando possibile invece di resettarla sempre a 1

## Test di regressione aggiunti

### Nuovi file test

- `httpmuxer/https_test.go`
- `utils/console_regression_test.go`

### File test estesi

- `utils/authentication_users_test.go`
- `utils/audit_test.go`
- `utils/forwarder_logs_test.go`

### Coperture aggiunte

- skip iterativo della connessione rifiutata nel proxy listener HTTPS
- broadcast websocket non bloccante su client lento
- rejection di escape via symlink in `resolveManagedFile()`
- supporto payload JSON array per disconnect route
- reload immediato utenti dopo save admin
- clamp della history su pagina fuori range
- history bounded retention
- mantenimento dello snapshot auth-users precedente dopo reload invalido
- eviction dell'origin IP audit più vecchio quando il limite è raggiunto
- cleanup dei writer log inattivi

## Validazione finale eseguita

### Test standard

Comando eseguito:

```bash
go test ./... -count=1
```

Esito:

- suite passata
- 109 test passati

### Race detector

Comando eseguito:

```bash
go test -race ./... -count=1
```

Esito:

- passata su tutto il repository

Package verificati dal race detector:

- `httpmuxer`
- `sshmuxer`
- `utils`

## Stato finale

Alla chiusura del remediation pass risultano corretti i difetti più critici emersi nell'assessment relativi a:

- robustezza runtime del listener HTTPS
- concorrenza e backpressure della console websocket
- coerenza dell'hot-reload utenti
- atomicità delle scritture file amministrative
- containment dei path gestiti
- crescita memoria non bounded in history e audit
- lifecycle dei writer log
- accuratezza di alcune viste frontend/admin

## Residual notes

Questo remediation pass non ha cercato di rifattorizzare in modo esteso parti non direttamente coinvolte dai bug confermati.

Obiettivo del lavoro svolto:

- correggere i problemi alla radice quando confermati
- aggiungere regression test sui punti fragili
- rivalidare l'intero repository in modalità standard e race detector
