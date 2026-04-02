# Ping Client & SSH Keepalive - Analisi dettagliata

Documento di analisi dell'interazione tra i flag **server-side** di sish (`ping-client*`) e i parametri **client-side** di OpenSSH (`ServerAliveInterval`, `ServerAliveCountMax`, ecc.).

---

## 1. I tre flag sish (server-side)

### 1.1 `ping-client` (bool, default: `true`)

Abilita o disabilita il meccanismo di keepalive **lato server**. Quando attivo, sish avvia una goroutine dedicata per ogni connessione SSH accettata che:

1. Imposta una **deadline** sulla connessione TCP sottostante.
2. Invia periodicamente un pacchetto SSH `keepalive@sish` (request con `wantReply=true`).
3. Se il client non risponde entro la deadline, la connessione viene chiusa.

Codice sorgente: `sshmuxer/sshmuxer.go:407-429`

```go
if viper.GetBool("ping-client") {
    go func() {
        tickDuration := viper.GetDuration("ping-client-interval")
        ticker := time.NewTicker(tickDuration)

        for {
            err := conn.SetDeadline(time.Now().Add(tickDuration).Add(viper.GetDuration("ping-client-timeout")))
            // ...
            select {
            case <-ticker.C:
                _, _, err := sshConn.SendRequest("keepalive@sish", true, nil)
                if err != nil {
                    log.Println("Error retrieving keepalive response:", err)
                    return
                }
            case <-holderConn.Close:
                return
            }
        }
    }()
}
```

### 1.2 `ping-client-interval` (duration, default: `5s`)

L'intervallo tra un ping e il successivo. Il ticker si attiva ogni `ping-client-interval` secondi e invia un `keepalive@sish` request.

### 1.3 `ping-client-timeout` (duration, default: `5s`)

Il tempo **aggiuntivo** concesso oltre l'intervallo per ricevere una risposta. La deadline TCP viene impostata a:

```
deadline = now + ping-client-interval + ping-client-timeout
```

Con i default (`5s` + `5s`), il server attende **massimo 10 secondi** di inattivita totale prima di considerare la connessione morta.

---

## 2. Meccanismo dettagliato del ping server-side

### Ciclo di vita di una iterazione

```
t=0s    SetDeadline(now + 5s + 5s = now+10s)
        |
t=5s    ticker scatta -> SendRequest("keepalive@sish", wantReply=true)
        |
        +-- Client risponde OK?
        |   SI -> nuovo ciclo, SetDeadline aggiornata
        |   NO -> err != nil -> goroutine esce -> cleanup connessione
        |
t=10s   Se nessuna attivita TCP -> deadline scaduta -> connessione chiusa dal kernel
```

### Cosa succede in pratica

| Scenario | Comportamento |
|---|---|
| Client sano, rete OK | Risponde al keepalive, deadline rinnovata, ciclo continua |
| Client bloccato (freeze) | Non risponde, `SendRequest` ritorna errore -> goroutine esce |
| Rete interrotta (drop silenzioso) | Nessun ACK TCP -> deadline TCP scade dopo `interval + timeout` -> connessione chiusa |
| Client disconnesso pulito (TCP RST/FIN) | `SendRequest` fallisce immediatamente -> goroutine esce |

---

## 3. I parametri SSH client-side

### 3.1 `ServerAliveInterval` (default: `0` = disabilitato)

Ogni `ServerAliveInterval` secondi di inattivita, il client SSH invia un messaggio `keepalive@openssh.com` al server e attende risposta.

Con `SSH_SERVER_ALIVE_INTERVAL=30`: il client invia un keepalive ogni 30 secondi di silenzio.

### 3.2 `ServerAliveCountMax` (default: `3`)

Numero massimo di keepalive client consecutivi senza risposta prima che il client chiuda la connessione.

Con `SSH_SERVER_ALIVE_COUNT_MAX=10`: il client tollera fino a 10 keepalive senza risposta.

**Timeout totale client-side:**

```
timeout_client = ServerAliveInterval * ServerAliveCountMax
               = 30s * 10 = 300 secondi (5 minuti)
```

### 3.3 `ConnectionAttempts` (`SSH_CONNECTION_ATTEMPTS=3`)

Numero di tentativi di connessione TCP prima di arrendersi. Si applica **solo alla fase di connessione iniziale**, non ha effetto sulle connessioni gia stabilite.

Se il primo tentativo TCP fallisce (SYN timeout, connection refused), SSH riprova fino a 3 volte.

### 3.4 `TCPKeepAlive` (`SSH_TCP_KEEP_ALIVE=yes`)

Abilita i keepalive **a livello TCP** (SO_KEEPALIVE socket option). Questi sono pacchetti TCP puri (non SSH) gestiti dal kernel, tipicamente con timing molto lunghi (Linux default: primo probe dopo 7200s, poi ogni 75s).

**Differenza critica rispetto a ServerAliveInterval:**

| Aspetto | TCPKeepAlive | ServerAliveInterval |
|---|---|---|
| Livello | TCP (kernel) | SSH (applicazione) |
| Timing tipico | ~2 ore (kernel default) | Configurabile (30s nel nostro caso) |
| Attraversa NAT | Dipende dal NAT timeout | SI, e' traffico SSH regolare |
| Rileva app freeze | NO (solo stato TCP) | SI (richiede risposta applicativa) |
| Rileva rete morta | SI (lento) | SI (veloce) |

### 3.5 `ConnectTimeout` (`SSH_CONNECT_TIMEOUT=5`)

Timeout per il singolo tentativo di connessione TCP. Con `5` secondi, se il SYN non riceve risposta entro 5s, il tentativo fallisce.

Combinato con `ConnectionAttempts=3`:

```
worst_case_connect = ConnectTimeout * ConnectionAttempts = 5s * 3 = 15 secondi
```

---

## 4. Interazione server-side e client-side

### 4.1 Configurazione analizzata

**Server sish:**
```yaml
ping-client: true
ping-client-interval: 5s
ping-client-timeout: 5s
# deadline effettiva: 10s
```

**Client SSH:**
```
ServerAliveInterval=30
ServerAliveCountMax=10
ConnectionAttempts=3
TCPKeepAlive=yes
ConnectTimeout=5
```

### 4.2 Due meccanismi di keepalive indipendenti e sovrapposti

Il server e il client eseguono **ognuno il proprio ciclo di keepalive**, in modo indipendente:

```
                    TEMPO
                    |
Server: ping -------|--5s--|--5s--|--5s--|--5s--|--5s--|--5s--|
                    |  ^      ^      ^      ^      ^      ^
                    |  Ogni 5s invia keepalive@sish
                    |
Client: ping -------|-------30s-------|-------30s-------|
                    |         ^                  ^
                    |         Ogni 30s invia keepalive@openssh.com
```

**Chi rileva la morte per primo?** Il server, quasi sempre.

### 4.3 Casistiche dettagliate

#### Caso A: Rete stabile, tutto funziona

```
Server: keepalive@sish ogni 5s -> client risponde -> deadline rinnovata
Client: keepalive@openssh.com ogni 30s -> server risponde -> contatore reset
TCPKeepAlive: nessun effetto (c'e' gia traffico SSH)

Risultato: connessione viva, entrambi i lati soddisfatti
```

#### Caso B: Client crasha (processo muore, OS attivo)

```
t=0s     Client crasha. OS invia TCP FIN/RST.
t<1s     Server: SendRequest fallisce -> goroutine esce -> cleanup
         (oppure deadline TCP rileva RST)

Risultato: server rileva in <1s. Cleanup immediato.
ServerAliveInterval: irrilevante (client gia morto)
```

#### Caso C: Rete interrotta silenziosamente (cavo staccato, NAT drop)

```
t=0s     Rete cade. Nessun RST/FIN inviato.

LATO SERVER:
t=5s     Tick -> SendRequest("keepalive@sish") -> pacchetto inviato ma nessun ACK
t=10s    Deadline TCP scade (5s interval + 5s timeout)
         -> Connessione chiusa lato server
         
LATO CLIENT:
t=30s    ServerAliveInterval scatta -> invia keepalive -> nessuna risposta
t=60s    Secondo keepalive senza risposta (count=2)
...
t=300s   Decimo keepalive senza risposta (count=10)
         -> Client chiude connessione

SERVER RILEVA IN: ~10 secondi
CLIENT RILEVA IN: ~300 secondi (5 minuti)
```

**Questo e' il caso piu importante**: il server libera le risorse (listener, route, stato) in ~10s, ma il client potrebbe restare in attesa fino a 5 minuti prima di riconnettersi.

#### Caso D: Server sish crasha (processo muore, OS attivo)

```
t=0s     Server crasha. OS invia TCP FIN/RST.
t<1s     Client: connessione SSH chiusa, ssh esce con errore

Risultato: client rileva immediatamente.
ping-client: irrilevante (server gia morto)
```

#### Caso E: Server host irraggiungibile (rete lato server cade)

```
LATO CLIENT:
t=30s    keepalive@openssh.com -> nessuna risposta
t=60s    secondo keepalive (count=2)
...
t=300s   count=10 -> client chiude e tenta riconnessione

LATO SERVER:
Il server non puo raggiungere il client ma la connessione
potrebbe restare "aperta" fino alla deadline TCP.

CLIENT RILEVA IN: ~300 secondi
```

#### Caso F: Client in freeze temporaneo (GC pause, swap thrashing)

```
t=0s     Client in freeze
t=5s     Server: keepalive inviato, in attesa di risposta
t=10s    Deadline scade SE il freeze dura >10s -> connessione chiusa!
t=8s     Se freeze finisce prima della deadline -> risponde -> OK

ATTENZIONE: con ping-client-interval=5s e ping-client-timeout=5s,
un freeze di >10s causa disconnessione lato server anche se il
client e' ancora "vivo".
```

#### Caso G: Riconnessione dopo disconnessione (Caso C completo)

```
t=0s      Rete cade
t=10s     Server: cleanup (listener liberato, route rimossa)
t=300s    Client: rileva disconnessione, ssh esce
t=300s    docker-client reconnect loop (SISH_CLIENT_RESTART_DELAY_SECONDS=3)
t=303s    Primo tentativo connessione (ConnectTimeout=5s)
          Se rete tornata: connessione OK in <5s
          Se rete ancora giu: timeout dopo 5s
t=308s    Secondo tentativo (ConnectionAttempts=3)
t=313s    Terzo tentativo -> se fallisce, ssh esce
t=316s    docker-client aspetta 3s e riprova il ciclo completo

Tempo totale da caduta rete a riconnessione (rete torna dopo 30s):
  ~303s (client rileva) + ~3s (restart delay) + <5s (connect) = ~311s
```

---

## 5. Diagramma temporale con i valori in analisi

```
TIMELINE (rete cade a t=0)
==========================================================================

SERVER (ping-client-interval=5s, ping-client-timeout=5s)
  t=0     [RETE CADE]
  t=5     ping #1 inviato (nessun ACK)
  t=10    DEADLINE SCADE -> server chiude connessione, cleanup risorse
          Listener liberato, route rimossa, stato pulito.

CLIENT (ServerAliveInterval=30, ServerAliveCountMax=10)
  t=0     [RETE CADE] (client non lo sa ancora)
  t=30    keepalive #1 (nessuna risposta) count=1
  t=60    keepalive #2 count=2
  t=90    keepalive #3 count=3
  t=120   keepalive #4 count=4
  t=150   keepalive #5 count=5
  t=180   keepalive #6 count=6
  t=210   keepalive #7 count=7
  t=240   keepalive #8 count=8
  t=270   keepalive #9 count=9
  t=300   keepalive #10 count=10 -> CLIENT CHIUDE CONNESSIONE

GAP: tra t=10 (server cleanup) e t=300 (client rileva)
     il client crede di essere connesso ma non lo e'.
     ~290 secondi di "connessione fantasma" lato client.

RICONNESSIONE:
  t=303   restart delay (3s)
  t=303   ssh connect tentativo 1 (ConnectTimeout=5s)
  t=303-308  se rete OK -> connesso
  t=308   tentativo 2 se fallito
  t=313   tentativo 3 se fallito
  t=316   ciclo restart ricomincia

==========================================================================
```

---

## 6. Analisi dei parametri rispetto ai valori scelti

### 6.1 Asimmetria di rilevamento

| Lato | Tempo di rilevamento | Formula |
|---|---|---|
| Server sish | ~10s | `interval + timeout = 5s + 5s` |
| Client SSH | ~300s | `alive_interval * alive_count_max = 30s * 10` |

Il server rileva la morte del client **30 volte piu velocemente** del client. Questo e' generalmente desiderabile perche il server deve liberare risorse (porte, route HTTP, listener) il prima possibile.

### 6.2 Valutazione di ogni parametro

| Parametro | Valore | Valutazione |
|---|---|---|
| `ping-client: true` | `true` | Essenziale. Senza, connessioni zombie restano aperte indefinitamente. |
| `ping-client-interval: 5s` | `5s` | Aggressivo ma adeguato per tunnel production. Genera 12 keepalive/min per connessione. |
| `ping-client-timeout: 5s` | `5s` | Ragionevole. Totale 10s prima di dichiarare morta la connessione. |
| `ServerAliveInterval=30` | `30s` | Conservativo. Client rileva lentamente. |
| `ServerAliveCountMax=10` | `10` | Molto tollerante. 5 minuti di attesa. |
| `ConnectionAttempts=3` | `3` | Standard. Sufficiente per glitch temporanei. |
| `TCPKeepAlive=yes` | `yes` | Utile come backup ma non critico con ServerAliveInterval attivo. |
| `ConnectTimeout=5` | `5s` | Appropriato. Non troppo aggressivo, non troppo lento. |

### 6.3 Raccomandazioni

Per ridurre il gap di rilevamento client-side (attualmente 300s), si possono considerare:

**Opzione A: Ridurre ServerAliveCountMax**
```
ServerAliveInterval=30  ServerAliveCountMax=3  -> timeout = 90s
```

**Opzione B: Ridurre ServerAliveInterval**
```
ServerAliveInterval=10  ServerAliveCountMax=3  -> timeout = 30s
```

**Opzione C: Bilanciata (consigliata)**
```
ServerAliveInterval=15  ServerAliveCountMax=4  -> timeout = 60s
```

Il valore ideale dipende dalla stabilita della rete:
- **Rete stabile (datacenter)**: `Interval=10 CountMax=3` (30s timeout)
- **Rete instabile (mobile/4G)**: `Interval=30 CountMax=5` (150s timeout)
- **Valori attuali**: adatti a reti molto instabili dove si vogliono evitare disconnessioni false

---

## 7. TCPKeepAlive vs ServerAliveInterval - Quando serve cosa

### TCPKeepAlive=yes

- Opera a livello kernel con timing di default molto lunghi (~2 ore su Linux)
- Si puo tuningare via sysctl (`net.ipv4.tcp_keepalive_time`, `tcp_keepalive_intvl`, `tcp_keepalive_probes`)
- **Vantaggio**: rileva connessioni TCP morte anche senza traffico SSH
- **Svantaggio**: non attraversa NAT aggressivi, timing troppo lungo di default
- **Quando serve**: come fallback se ServerAliveInterval e' disabilitato

### ServerAliveInterval

- Opera a livello applicativo SSH
- **Vantaggio**: timing preciso, attraversa NAT, rileva freeze applicativi
- **Svantaggio**: richiede processo SSH client attivo
- **Quando serve**: SEMPRE in tunnel production

Con entrambi attivi (come nella configurazione analizzata), ServerAliveInterval domina perche ha timing molto piu stretti. TCPKeepAlive e' un safety net ridondante.

---

## 8. ConnectionAttempts e ConnectTimeout - Fase di connessione

Questi parametri operano **solo durante la connessione iniziale**, non durante la sessione.

### Flusso connessione

```
Tentativo 1:
  SYN -> attesa fino a ConnectTimeout (5s)
  -> Successo? Connesso.
  -> Timeout/Refused? Tentativo 2.

Tentativo 2:
  SYN -> attesa fino a ConnectTimeout (5s)
  -> Successo? Connesso.
  -> Timeout/Refused? Tentativo 3.

Tentativo 3:
  SYN -> attesa fino a ConnectTimeout (5s)
  -> Successo? Connesso.
  -> Timeout/Refused? ssh esce con errore.

Worst case: 3 * 5s = 15s prima di fallire.
```

### Interazione con docker-client reconnect

Il docker-client (`scripts/run-client.sh`) ha il suo loop di reconnect:

```
Loop esterno (docker-client):
  -> ssh (ConnectionAttempts=3, ConnectTimeout=5)
     -> Se fallisce dopo 15s max
  -> Aspetta SISH_CLIENT_RESTART_DELAY_SECONDS (3s)
  -> Riprova (se SISH_CLIENT_MAX_RETRIES=0, infinito)
```

Questo crea un doppio livello di retry:
1. **Interno** (SSH): 3 tentativi rapidi con 5s timeout ciascuno
2. **Esterno** (docker-client): loop infinito con 3s di pausa tra i cicli

---

## 9. Riepilogo configurazione completa

```
+------------------------------------------------------------------+
|                        SISH SERVER                                |
|  ping-client: true                                               |
|  ping-client-interval: 5s     (frequenza ping)                   |
|  ping-client-timeout: 5s      (tolleranza extra)                 |
|  -> Deadline totale: 10s                                         |
|  -> Rileva client morto in: ~10s                                 |
+------------------------------------------------------------------+
        |                              ^
        | keepalive@sish (ogni 5s)     | keepalive@openssh.com (ogni 30s)
        v                              |
+------------------------------------------------------------------+
|                        SSH CLIENT                                 |
|  ServerAliveInterval=30   (frequenza keepalive)                  |
|  ServerAliveCountMax=10   (tentativi prima di chiudere)          |
|  -> Timeout: 300s (5 min)                                        |
|  -> Rileva server morto in: ~300s                                |
|                                                                  |
|  TCPKeepAlive=yes         (keepalive TCP kernel, backup)         |
|  ConnectTimeout=5         (timeout per singolo SYN)              |
|  ConnectionAttempts=3     (retry connessione iniziale)           |
|  -> Max tempo connessione: 15s                                   |
+------------------------------------------------------------------+
```

---

## 10. Glossario

| Termine | Significato |
|---|---|
| `keepalive@sish` | Request SSH custom inviata dal server sish al client |
| `keepalive@openssh.com` | Request SSH standard inviata dal client OpenSSH al server |
| `SetDeadline` | Imposta un timeout assoluto sulla connessione TCP (Go `net.Conn`) |
| `SO_KEEPALIVE` | Opzione socket TCP per keepalive a livello kernel |
| `wantReply=true` | Il sender attende una risposta SSH dal peer |
| NAT timeout | Tempo dopo il quale un NAT dimentica una sessione TCP inattiva (tipicamente 30-300s) |
