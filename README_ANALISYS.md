# README_ANALISYS

Data analisi: 4 marzo 2026  
Repository: `manprint-sish-fork`

## Obiettivo
Questa analisi raccoglie in modo strutturato i risultati emersi durante la revisione tecnica del progetto, con focus su:
- architettura applicativa interna
- robustezza production-grade
- rischi di race condition, memory leak/goroutine leak
- colli di bottiglia prestazionali su tunnel HTTP/TCP/TCP-alias
- implicazioni di streaming/compressione (gzip, brotli, zstd)

---

## 1) Architettura sintetica

Il progetto è un server SSH multiplexer che espone forwarding:
- HTTP/HTTPS
- TCP
- TCP alias/SNI proxy

Componenti principali:
- bootstrap/flag: `cmd/sish.go`
- orchestrazione connessioni SSH: `sshmuxer/sshmuxer.go`
- gestione richieste/channels SSH: `sshmuxer/requests.go`, `sshmuxer/channels.go`, `sshmuxer/handle.go`
- mux/proxy HTTP: `httpmuxer/httpmuxer.go`, `httpmuxer/proxy.go`, `httpmuxer/https.go`
- stato globale condiviso: `utils/state.go`
- lifecycle connessioni/copy loop: `utils/conn.go`
- listener multiplexer SSH su stessa porta: `utils/sshmux_listener.go`

Frontend console (non prioritaria al momento, ma analizzata):
- template: `templates/*.tmpl`
- controller/API console: `utils/console.go`

---

## 2) Flusso runtime (alto livello)

1. `sshmuxer.Start()` inizializza stato e listener.
2. Ogni connessione SSH genera una `SSHConnection` con canali (`Close`, `Exec`, `Messages`, `Session`) e handler goroutine dedicati.
3. Le richieste di forward (`tcpip-forward`) vengono risolte in listener locali Unix/TCP e collegate a balancer/holder.
4. Il traffico effettivo è inoltrato con copy bidirezionale (`utils.CopyBoth`) e, per HTTP, tramite Oxy forwarder+roundrobin.
5. Cleanup: `CleanUp()` chiude risorse e rimuove entry da mappe condivise.

---

## 3) Stato attuale robustezza: punti critici

## 3.1 Concorrenza / race condition

### A) Console WS clients (critico in area console)
In `utils/console.go` la mappa è thread-safe (`syncmap`), ma il valore memorizzato è `[]*WebClient` non protetto da lock dedicato durante append/remove/broadcast.

Rischio:
- data race
- panics sporadici sotto carico
- comportamento non deterministico su disconnessioni concorrenti

Nota: la console non è prioritaria ora, ma il rischio esiste nel codice.

### B) Canali non bufferizzati con send bloccanti
Sono presenti molti `SendMessage(..., true)` (invio bloccante), con `Messages` non bufferizzato.

Rischio:
- backpressure forte
- stalli se consumer rallenta o non è più attivo
- accumulo goroutine in attesa di write

---

## 3.2 Lifecycle goroutine / cleanup

### A) Ticker non stoppati esplicitamente
In `sshmuxer/sshmuxer.go` ci sono ticker in loop (deadline/cleanup e ping client). Anche se il loop termina, manca `ticker.Stop()` esplicito.

Rischio:
- leak di risorse nel lungo periodo (in scenari churn elevato)

### B) Pattern `go func` molto diffuso
Ci sono molte goroutine per connessione/richiesta (design corretto per I/O), ma servono policy rigorose di uscita e timeout per evitare “goroutine tombstoned” in condizioni anomale.

---

## 3.3 Memoria

### A) History in-memory non limitata
`WebConsole.History` cresce senza limite.

Rischio:
- crescita RAM nel tempo
- pressione GC

(Area console, non prioritaria ora, ma va annotata.)

### B) Body capture HTTP con `ReadAll`
In `httpmuxer/httpmuxer.go` e `httpmuxer/proxy.go`, request/response body possono essere letti interamente per console capture.

Rischio:
- picchi memoria su payload grandi
- aumento latenza e CPU

Mitigazione parziale già presente:
- `service-console-max-content-length`

---

## 3.4 Timeout e hardening server

I server HTTP/HTTPS in `httpmuxer/httpmuxer.go` non mostrano tuning completo di timeout applicativi (`ReadTimeout`, `WriteTimeout`, `IdleTimeout`, `ReadHeaderTimeout`, `MaxHeaderBytes`) nel costruttore server.

Rischio:
- maggiore esposizione a slow client / resource exhaustion
- peggior resilienza sotto carico ostile o bursty

---

## 3.5 Transport HTTP upstream

In `httpmuxer/proxy.go`, il transport custom è funzionale ma con tuning minimo.

Opportunità:
- migliorare pooling/timeout dial/TLS/idle per throughput più stabile in produzione.

---

## 4) Streaming HTTP (PR upstream #255)

Riferimento: `https://github.com/antoniomika/sish/pull/255/commits`

Sintesi:
- lo streaming è stato introdotto nel forwarder HTTP (`forward.Stream(true)`), riducendo buffering nel percorso di inoltro.
- la logica gzip è nel percorso di console-capture, non nel datapath puro di forwarding.

Impatto:
- positivo per latenza e memoria nel traffico streamabile
- non implica di per sé un cambio algoritmo di compressione del tunnel

---

## 5) Compressione: gzip vs brotli vs zstd

## 5.1 Stato attuale
- gzip viene trattato nel percorso di decode dei payload catturati per console (`httpmuxer/proxy.go`).
- non è una compressione “di sistema” applicata da sish al traffico tunnelato in modo generalizzato.

## 5.2 Issue upstream #348 (brotli)
Riferimento: `https://github.com/antoniomika/sish/issues/348`

Significato pratico:
- aggiungere decode brotli per payload catturati (simile al ramo gzip).

## 5.3 Valutazione zstd
- Nel datapath di forwarding di sish: beneficio limitato, perché sish inoltra principalmente byte pass-through.
- Nel ramo console/ispezione: possibile utilità (leggibilità payload), ma con trade-off in complessità e costo CPU/memoria.

Conclusione:
- zstd non è la leva principale per aumentare banda passante dei tunnel.
- La leva principale è ottimizzazione runtime/network e riduzione overhead fuori datapath.

---

## 6) Come aumentare banda passante HTTP/TCP/TCP-alias

## 6.1 Priorità immediate (alto impatto)
1. Disattivare feature non necessarie sul datapath:
   - debug/log verbose
   - console capture in produzione ad alto traffico
2. Mantenere streaming HTTP attivo.
3. Tuning OS/network VM:
   - `ulimit -n`
   - backlog (`somaxconn`, `netdev_max_backlog`)
   - buffer TCP (`rmem/wmem`)
4. Scala orizzontale (più istanze sish + LB L4) per sfruttare banda aggregate e più CPU.

## 6.2 HTTP specifico
- minimizzare trasformazioni request/response non necessarie
- ridurre overhead di path rewrite/feature opzionali se non richieste
- se possibile, terminare TLS a edge ottimizzato

## 6.3 TCP / TCP-alias specifico
- usare TCP diretto quando alias non serve
- evitare `sni-proxy` e `proxy-protocol` se non necessari
- distribuire i flussi su più connessioni/tunnel per miglior parallelismo

## 6.4 Runtime Go
- allineare `GOMAXPROCS` ai vCPU
- valutare `GOGC` in base al profilo memoria/throughput
- misurare prima/dopo con benchmark reali

---

## 7) Piano di intervento proposto (senza applicazione immediata)

## Fase 1 — Stabilità core (P0)
- hardening timeout server HTTP/HTTPS
- mitigazione send bloccanti su canali critici (buffer + policy non-bloccante)
- revisione punti di cleanup e chiusura risorse in loop long-running

## Fase 2 — Throughput & resilienza (P1)
- tuning transport upstream (pool/timeout)
- revisione copy/backpressure su traffico ad alto parallelismo
- profiling CPU/heap/goroutine sotto carico

## Fase 3 — Ottimizzazioni avanzate (P2)
- eventuale supporto decode extra encodings (br/zstd) solo in capture path
- limiti/cap espliciti per strutture in-memory non bounded (es. history)
- test mirati race/load (`go test -race`, benchmark sintetici + workload reale)

---

## 8) KPI consigliati per validare miglioramenti

- Throughput per protocollo (HTTP/TCP/TCP-alias)
- p95/p99 latency
- goroutine count stabile nel tempo
- heap live e pause GC
- error rate (timeout/reset/EOF)
- CPU user/system per Gbps

---

## 9) Nota su regressione console già corretta

Durante l’analisi è stata individuata e corretta una regressione di routing console su subdomain con token admin (404 su `/_sish/console` del servizio).  
Fix applicata in `httpmuxer/httpmuxer.go` limitando il bypass admin immediato al root host.

---

## 10) Sintesi finale

Per un profilo davvero production-grade orientato a massima banda:
1. ridurre overhead funzionale fuori datapath
2. hardenizzare timeout/backpressure/concurrency
3. misurare sistematicamente con benchmark e profiling
4. scalare orizzontalmente su VM ad alta capacità rete

Le ottimizzazioni di compressione (brotli/zstd) sono secondarie rispetto a questi interventi e, in questa architettura, non rappresentano la prima leva di incremento banda passante.