# Hardening Lifecycle Roadmap (Post-Stage)

Questo documento riassume **cosa resta da fare** dopo la prima fase di hardening già implementata, per portare il lifecycle dei forward verso un livello "orologio svizzero" in produzione.

## Stato attuale (aggiornato)

- Cleanup forward centralizzato e idempotente con callback registry per connessione.
- Integrazione cleanup nel cancel path e nel cleanup completo connessione.
- Riduzione falsi positivi in `dirtyForwards` (solo issue stabili su più cicli).
- Metriche aggregate `dirtyMetrics` nella API internal.
- Test aggiunti sulla diagnostica dirty (listener/http/tcp/alias) + race check su package `utils`.
- Hardening force-connect concorrente: lock per target (`type+addr+port`) per serializzare takeover sullo stesso forward.
- Lifecycle metrics estese nel runtime state + esposizione in API internal e UI internal:
  - `forward_create_total`
  - `forward_cleanup_total`
  - `forward_cleanup_errors_total`
  - breakdown errori cleanup:
    - `forward_cleanup_listener_close_errors_total`
    - `forward_cleanup_socket_remove_errors_total`
    - `forward_cleanup_unknown_errors_total`
  - `dirty_forwards_stable_total` (+ per tipo)
  - `force_connect_takeovers_total`
- Osservabilità internal completata:
  - rate per secondo (`lifecycleRates`)
  - storico a ring buffer (`lifecycleHistory`)
  - health snapshot/alerts (`health`)
  - export Prometheus text su `/_sish/api/internal/metrics`
- Hardening reconnect rapido / autossh:
  - purge automatico stale holder in fase di bind (`http`, `alias`, `sni`, `tcp`)
  - riduzione falsi “subdomain unavailable” quando il vecchio holder è già orfano
  - fix UI clients: `isCensused` calcolato sempre su `ConnectionID` (non più dipendente da listeners>0)
  - metriche debug dedicate (vedi sezione sotto)

## Obiettivo delle prossime fasi

Ridurre ulteriormente:

- finestre TOCTOU tra mappe condivise (`state.Listeners`, `state.HTTP/TCP/AliasListeners`, `sshConn.Listeners`);
- rischi residui in scenari di alta concorrenza (rapid create/destroy, force-connect concorrente);
- eventuali discrepanze tra stato runtime reale e stato osservato dalla pagina internal.

---

## Fase 2 — Hardening mirato (media invasività)

### 1) Lifecycle manager per forward (single source of truth)

**Implementato**

- Introdotto componente dedicato `forwardLifecycle` in `sshmuxer/forward_lifecycle.go`:
  - orchestrazione centralizzata register/cleanup
  - cleanup idempotente con `sync.Once`
  - hook per cleanup specifico per tipo forward (HTTP/TCP/Alias)
  - integrazione con registry `SSHConnection.ForwardCleanups`
- `handleRemoteForward` ora usa il lifecycle manager come single source of truth per cleanup.

**Perché**

- Evita update sparsi in più funzioni con ordering implicito.
- Riduce inconsistenze temporanee fra mappe.

**Rischio regressione**

- Medio (tocca path core di create/cleanup forward), mitigato con test e race checks.

---

### 2) Snapshot coerenti per diagnostica internal ✅ (implementato)

**Implementato**

- `getDirtyForwardRows()` ora lavora su snapshot locali (`buildDirtySnapshot`) per:
  - listener forward
  - HTTP/TCP/Alias holders
  - active SSH set
- Ridotte letture live incrociate durante mutate concorrenti.

**Perché**

- Riduce mismatch osservativi (dirty "falso" dovuto a lettura intermedia).

**Rischio regressione**

- Basso.

---

### 3) Hardening force-connect concorrente ✅ (implementato)

**Implementato**

- Serializzazione per target in `sshmuxer/requests.go` con lock in-memory:
  - stesso target -> esecuzione in sequenza
  - target diversi -> esecuzione concorrente
- Metriche takeover aggiornate (`force_connect_takeovers_total`).

**Rimane (opzionale)**

- Backoff adattivo lato server per scenari estremi ad alta contesa.

**Perché**

- Il force-connect è il path più sensibile a race operative.

**Rischio regressione**

- Medio.

---

## Fase 3 — Test strategy production-grade

### 4) Stress test concorrenti dedicati ✅ (implementato in questa fase)

**Fatto**

- Test concorrenti sul lock force-connect:
  - serializzazione sullo stesso target
  - concorrenza su target diversi

**Implementato**

- Stress test concorrenti su snapshot + mutate in parallelo (`utils/internal_status_stress_test.go`)
- Stress sampling continuo su dirty metrics stabili
- Test force-connect lock concorrente (same target serializzato, target diversi concorrenti)
- Test lifecycle manager idempotenza cleanup, hook cleanup e metriche error/success
- Guardrail anti-saturazione: stress test disattivati di default e attivabili solo con
  `SISH_ENABLE_STRESS_TESTS=1`

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

### 6) Test di robustezza lifecycle ✅ (implementato)

**Implementato**

- Test su metriche lifecycle e contabilizzazione dirty stabili per tipo.
- Test su idempotenza cleanup multipla tramite goroutine concorrenti.
- Test su one-shot cleanup callback registry.
- Test su cleanup hook ed error accounting.
- Test concorrente \"light\" sempre eseguibile in sicurezza (`TestForwardLifecycleConcurrentLight`).

**Profilo esecuzione consigliato (safe su workstation)**

```bash
GOMAXPROCS=2 GOMEMLIMIT=2GiB SISH_ENABLE_LIGHT_CONCURRENCY_TESTS=1 \
  go test ./sshmuxer -run 'TestWithForceConnectTargetLock|TestForwardLifecycle' -count=1 -p 1

GOMAXPROCS=2 GOMEMLIMIT=2GiB \
  go test ./utils -run 'TestDirty|TestLifecycleMetrics|TestBuildDirtySnapshot' -count=1 -p 1
```

Per test stress più pesanti (opt-in):

```bash
SISH_ENABLE_STRESS_TESTS=1 go test ./utils -run TestStress -count=1 -p 1
```

**Perché**

- Valida i contratti del lifecycle manager.

---

## Fase 4 — Osservabilità operativa

### 7) Metriche lifecycle estese ✅ (implementato lato app)

**Implementato**

- Metriche esposte nella API internal (`lifecycleMetrics`) e visibili in UI internal (sezione \"Lifecycle Metrics\"):
  - `forward_create_total`
  - `forward_cleanup_total`
  - `forward_cleanup_errors_total`
  - `dirty_forwards_stable_total`
  - `dirty_forwards_stable_listener_total`
  - `dirty_forwards_stable_http_total`
  - `dirty_forwards_stable_tcp_total`
  - `dirty_forwards_stable_alias_total`
  - `force_connect_takeovers_total`

**Perché**

- Permette alerting proattivo e trend analysis.

---

### 8) Health/alert policy internal ✅ (implementato)

**Implementato**

- Health snapshot esposto in API internal:
  - `status`: `ok|warning|critical`
  - `alerts[]`: livello, nome, messaggio, valore
- Regole incluse:
  - warning se dirty stabili > 0
  - warning se cleanup errors totali > 0
  - critical se cleanup error ratio > 5% (dopo baseline minima)
  - critical se rate cleanup error > 0.5/s

**Perché**

- Migliora il tempo di rilevazione problemi reali e riduce troubleshooting manuale.

### 9) Reconnect debug metrics ✅ (implementato)

Nuove metriche diagnostiche per individuare in modo mirato bug di reconnessione rapida:

- `debug_bind_conflict_total`
- `debug_bind_conflict_http_total`
- `debug_bind_conflict_alias_total`
- `debug_bind_conflict_sni_total`
- `debug_bind_conflict_tcp_total`
- `debug_stale_holder_purged_total`
- `debug_stale_holder_purged_http_total`
- `debug_stale_holder_purged_alias_total`
- `debug_stale_holder_purged_sni_total`
- `debug_stale_holder_purged_tcp_total`
- `debug_force_disconnect_noop_total`
- `debug_target_release_timeout_total`

Disponibili in:
- API internal (`lifecycleMetrics` + `debugMetrics`)
- UI internal (sezione “Reconnect Debug Metrics”)
- export Prometheus (`/_sish/api/internal/metrics`)

---

## Sequenza consigliata (incrementale e sicura)

1. (Completato) Snapshot internal + test robustezza
2. (Completato) Stress suite estesa base + race checks
3. (Completato) Lifecycle manager core
4. (Resta opzionale) Stress e2e multi-processo con workload sintetico estremo

## Criteri di uscita (quando considerarlo “Swiss-grade”)

- Nessun dirty stabile in stage/prod sotto workload realistico.
- Stress suite stabile su più run consecutive.
- `go test -race ./...` green in CI con continuità.
- Nessun cleanup error significativo in finestre operative estese.

## Nota operativa

La prima fase è adatta a uno stage serio e riduce già in modo concreto il rischio operativo.  
Le attività sopra sono il percorso per arrivare a un livello production-grade avanzato, con margine robusto anche in condizioni estreme.
