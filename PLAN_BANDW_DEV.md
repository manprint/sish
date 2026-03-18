# Spec 14 Dettagli e specifiche implementative. - Hot reload bandwidth limits

> **STATO: IMPLEMENTATO** — Lo sviluppo e' stato completato. I flag `--bandwidth-hot-reload-enabled` e `--bandwidth-hot-reload-time` sono operativi. Il reconcile periodico aggiorna i profili di banda sulle connessioni attive senza disservizio.

## Problema osservato
I valori `bandwidth-upload`, `bandwidth-download`, `bandwidth-burst` nei file YAML utenti vengono ricaricati da watcher (`WatchAuthUsers`/`loadAuthUsers`), ma l'effetto sui tunnel attivi non è immediato: i limiti vengono applicati in pratica al login (tramite `buildAuthUserPermissions` -> `UserBandwidthProfileFromPermissions`), quindi spesso serve riavviare il forwarder/client per vedere i nuovi vincoli.

## Stato attuale (analisi tecnica)
- Parsing/configurazione utenti: `utils/utils.go`
  - `loadAuthUsers()` aggiorna mappe in memoria (`authUsersBandwidthHolder`) quando i file YAML cambiano.
  - `parseAuthUserBandwidthConfig()` converte Mbps + burst in valori runtime.
- Applicazione limiti per connessione SSH:
  - In fase di handshake, `buildAuthUserPermissions()` salva i limiti negli extension fields SSH.
  - In fase creazione sessione, `sshmuxer/sshmuxer.go` crea `SSHConnection.UserBandwidthProfile` da quelle permissions.
- Enforcement reale:
  - `utils.CopyBoth()` applica i limiter tramite `rateLimitedReader` usando `sshConn.UserBandwidthProfile`.

## Root cause del "serve restart"
I limiti runtime sono legati al `UserBandwidthProfile` creato al login; quando YAML cambia, la mappa auth si aggiorna ma i profili già presenti nelle `SSHConnection` restano invariati.

## Vincoli importanti per hot-apply
1. **No disservizio**: non chiudere connessioni SSH/tunnel esistenti.
2. **Thread-safety**: oggi `sshConn.UserBandwidthProfile` viene letto in goroutine diverse; un update live richiede sincronizzazione.
3. **Connessioni dati già in corso**:
   - se una stream è già partita senza limiter (nil), non può diventare limitata "magicamente" senza cambiare il wrapping del reader.
   - stream nuove possono usare subito profilo aggiornato.

## Piano dettagliato proposto DA IMPLEMENTARE.

### Fase 1 - Preparazione modello runtime
- Introdurre API interna su `SSHConnection` per accesso profilo banda thread-safe (es. lock dedicato o atomic pointer):
  - `GetBandwidthProfile()`
  - `SetBandwidthProfile()`
- Evitare letture/scritture dirette concorrenti del campo.

### Fase 2 - Propagazione hot config ai client già autenticati
- Dopo ogni `loadAuthUsers()` riuscito, eseguire una routine di reconcile sugli `state.SSHConnections`:
  - identificare user (`sshConn.SSHConn.User()`),
  - leggere nuovo profilo da `authUsersBandwidthHolder`,
  - aggiornare il profilo runtime della connessione senza disconnetterla.
- Se utente non ha più limiti: impostare profilo "stats-only" (non nil) per preservare metriche data-in/out.

### Fase 3 - Coerenza su stream attive e nuove
- Per stream **nuove**: usare sempre `GetBandwidthProfile()` al momento di `CopyBoth`.
- Per stream **già attive** (requisito più forte):
  - *IMPLEMENTARE* :opzione B (completa): rendere `rateLimitedReader` dinamico (risolve limiter ad ogni read via getter/atomic), così anche stream in corso recepiscono cambi limite.
- Raccomandazione: opzione B se "senza disservizio" implica anche sessioni TCP già aperte -> SI

### Fase 4 - Osservabilità e UX operativa
- Loggare update applicati per utente/connessione (old -> new upload/download/burst).
- (Obbligatorio) esporre nel pannello internal uno stato "bandwidth profile version/updated at" per troubleshooting.

### Fase 5 - Test plan (regressioni)
- Unit test parsing già presente: estendere con casi hot-update.
- Nuovi test su reconcile:
  - update profilo esistente,
  - rimozione limiti,
  - utente non trovato,
  - update concorrenti.
- Test integrazione di comportamento:
  - connessione attiva + modifica YAML => nessuna disconnessione,
  - nuove stream rispettano subito i nuovi limiti,
  - se implementata opzione B: stream già in corso cambiano throughput senza restart.
- Verifiche finali: `go test ./... -count=1` + `go build`.

## File impattati (stima)
- `utils/conn.go` (accesso profilo + dinamica limiter)
- `utils/utils.go` (`loadAuthUsers` + hook reconcile)
- `sshmuxer/sshmuxer.go` e/o `sshmuxer/requests.go`/`sshmuxer/channels.go` (uso getter profilo)
- `utils/authentication_users_test.go` + nuovi test mirati runtime

## Rischi e mitigazioni
- **Race condition** su profilo: mitigare con lock/atomic e API dedicata.
- **Incoerenza contatori traffico** durante swap profilo: mantenere contatori nel profilo persistente o migrare valori in modo atomico.
- **Costo runtime** (lookup dinamico ad ogni read): minimizzare con accessi atomici leggeri.

## Importante: ULTERIORI SPECIFICHE
- Non è necessario che i nuovi valori siano caricati all'istante, puoi pure precedere un flag da passare all'avvio dell'applicazione --bandwidth-hot-reload-time=20s.
- L'applicazione quindi ogni 20s (configurabile), applica i vincoli. In questo modo si riduce il costo di overhead a runtime
- Inoltre questa funzionalità di hotrealod deve essere attivabile/disattivabile tramite un flag  all'avvio: --bandwidth-hot-reload-enabled=true|false. Se è true, allora la funzionalità è operativa. Se è false, tutto resta esattamente com'è adesso.