# Hardening Lifecycle Roadmap (Post-Stage)

Questo documento riassume **cosa resta da fare** dopo la prima fase di hardening già implementata, per portare il lifecycle dei forward verso un livello "orologio svizzero" in produzione.

## Stato attuale (già fatto)

- Cleanup forward centralizzato e idempotente con callback registry per connessione.
- Integrazione cleanup nel cancel path e nel cleanup completo connessione.
- Riduzione falsi positivi in `dirtyForwards` (solo issue stabili su più cicli).
- Metriche aggregate `dirtyMetrics` nella API internal.
- Test aggiunti sulla diagnostica dirty (listener/http/tcp/alias) + race check su package `utils`.

## Obiettivo delle prossime fasi

Ridurre ulteriormente:

- finestre TOCTOU tra mappe condivise (`state.Listeners`, `state.HTTP/TCP/AliasListeners`, `sshConn.Listeners`);
- rischi residui in scenari di alta concorrenza (rapid create/destroy, force-connect concorrente);
- eventuali discrepanze tra stato runtime reale e stato osservato dalla pagina internal.

---

## Fase 2 — Hardening mirato (media invasività)

### 1) Lifecycle manager per forward (single source of truth)

**Da fare**

- Introdurre un componente dedicato (es. `ForwardLifecycleManager`) per orchestrare in modo atomico:
  - create listener holder
  - register map entries
  - register balancer server
  - unregister/cleanup in ordine inverso

**Perché**

- Evita update sparsi in più funzioni con ordering implicito.
- Riduce inconsistenze temporanee fra mappe.

**Rischio regressione**

- Medio (tocca path core di create/cleanup forward).

---

### 2) Snapshot coerenti per diagnostica internal

**Da fare**

- In `getDirtyForwardRows()`, costruire snapshot locali delle strutture rilevanti prima della valutazione (o per blocchi omogenei), evitando letture incrociate live durante mutate concorrenti.

**Perché**

- Riduce mismatch osservativi (dirty "falso" dovuto a lettura intermedia).

**Rischio regressione**

- Basso/medio.

---

### 3) Hardening force-connect concorrente

**Da fare**

- Rivedere `forceDisconnectTargetConnections` + `waitForTargetRelease` per garantire comportamento deterministico con N takeover simultanei sullo stesso target.
- Aggiungere guardrail anti-thrashing (debounce/backoff breve lato server).

**Perché**

- Il force-connect è il path più sensibile a race operative.

**Rischio regressione**

- Medio.

---

## Fase 3 — Test strategy production-grade

### 4) Stress test concorrenti dedicati

**Da fare**

- Suite stress con goroutine multiple per:
  - create/destroy forward rapidi
  - cancel remoto ripetuto
  - force-connect concorrenti
  - mix HTTP/TCP/Alias
- Asserzioni: nessun panic, nessun leak strutturale, dirty stabili = 0 a regime.

**Perché**

- Copre i casi che i test unitari non intercettano.

---

### 5) Race detector esteso in CI

**Da fare**

- Job CI separato con:
  - `go test -race ./...`
  - retry controllato su suite stress (es. `-count=5` sui test concorrenti selezionati)

**Perché**

- Intercetta regressioni di concorrenza prima del rilascio.

---

### 6) Test di robustezza lifecycle

**Da fare**

- Test su idempotenza cleanup:
  - cleanup doppio/triplo non deve lasciare stato residuo;
  - cancel su forward già chiuso deve essere safe;
  - chiusura connessione durante create forward non deve lasciare orphan.

**Perché**

- Valida i contratti del lifecycle manager.

---

## Fase 4 — Osservabilità operativa

### 7) Metriche lifecycle estese

**Da fare**

- Esporre contatori/gauge aggiuntivi:
  - `forward_create_total`
  - `forward_cleanup_total`
  - `forward_cleanup_errors_total`
  - `dirty_forwards_stable_total` (per tipo)
  - `force_connect_takeovers_total`

**Perché**

- Permette alerting proattivo e trend analysis.

---

### 8) Alert policy consigliata

**Da fare**

- Alert su:
  - dirty stabili > 0 oltre soglia temporale
  - crescita anomala cleanup error
  - spike force-connect takeover

**Perché**

- Migliora il tempo di rilevazione problemi reali.

---

## Sequenza consigliata (incrementale e sicura)

1. Snapshot internal + test robustezza (bassa invasività)
2. Stress suite + race CI (forte valore, rischio basso sul runtime)
3. Hardening force-connect
4. Lifecycle manager completo (step finale, più invasivo)

## Criteri di uscita (quando considerarlo “Swiss-grade”)

- Nessun dirty stabile in stage/prod sotto workload realistico.
- Stress suite stabile su più run consecutive.
- `go test -race ./...` green in CI con continuità.
- Nessun cleanup error significativo in finestre operative estese.

## Nota operativa

La prima fase è adatta a uno stage serio e riduce già in modo concreto il rischio operativo.  
Le attività sopra sono il percorso per arrivare a un livello production-grade avanzato, con margine robusto anche in condizioni estreme.
