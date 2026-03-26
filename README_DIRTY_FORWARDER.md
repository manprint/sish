README — Dirty / Non-Working Forwards
=====================================

Scopo
-----
Questo documento descrive in dettaglio la sezione "Dirty / Non-Working Forwards" mostrata nella pagina /_sish/internal. Contiene: cosa viene segnalato, riferimenti al codice sorgente, cause plausibili, come riprodurre i casi in test (unit/integration) e suggerimenti operativi e di testing automatico.

Panoramica funzionale
---------------------
La sezione non è un semplice elenco di "forward non funzionanti" in senso lato, ma un controllo di consistenza dello stato interno dell'applicazione. I controlli vengono costruiti da `getDirtyForwardRows()` (file: utils/internal_status.go) e coprono tre macro-aree:

1. Listener orfani o con owner inconsistenti (listener-level issues)
2. Forwards HTTP/TCP/Alias con nessun backend attivo (forward-level issues)
3. Incoerenze fra mappe globali (`state.Listeners`, `state.SSHConnections`, holder internal maps)

Messaggi riportati
------------------
Le stringhe di issue rilevate (attuali nella codebase):

- "listener holder has no owning ssh connection"
- "owning ssh connection has no remote address"
- "owning ssh connection is missing from active connection map"
- "listener is not linked in owning ssh connection listener map"
- "http forward has no active backends"
- "tcp forward has no active backends"
- "alias forward has no active backends"

Questi messaggi corrispondono rispettivamente ai controlli che iterano:
- `state.Listeners` (controllo del ListenerHolder e del campo `SSHConn`) 
- `state.HTTPListeners` (controllo dei backends tramite `httpHolder.Balancer.Servers()`)
- `state.TCPListeners` (controllo tramite tcpHolder.Balancers)
- `state.AliasListeners` (controllo tramite aliasHolder.Balancer.Servers())

Riferimenti codice
-------------------
- `utils/internal_status.go`
  - func `getDirtyForwardRows()` — la funzione che costruisce l'array `internalForwardIssue` (tipo: Type, Name, Issue)
  - func `buildInternalState()` — conta le righe sporche e popola i dettagli di stato mostrati nella UI
- `utils/state.go` — definizione dei tipi `ListenerHolder`, `HTTPHolder`, `TCPHolder`, `AliasHolder`
- `sshmuxer/requests.go`, `sshmuxer/httphandler.go`, `sshmuxer/tcphandler.go`, `sshmuxer/aliashandler.go` — punti in cui vengono creati e registrati listener ed holder (`state.Listeners.Store(...)`, `state.HTTPListeners.Store(...)` ecc.)
- `utils/conn.go` — metodi `SSHConnection.CleanUp()`, `ListenerCount()` che rimuovono o contano listener; qui avviene la normale pulizia dell'applicazione

Quando si generano inconsistenze
-------------------------------
Le inconsistenze compaiono principalmente quando la creazione/cleanup dei listener o l'aggiornamento delle mappe condivise sono parziali o non atomici rispetto ad altre operazioni. Esempi tipici:

- Un listener è registrato in `state.Listeners` ma il campo `ListenerHolder.SSHConn` è nil (listener orfano)
- Un `ListenerHolder` ha `SSHConn` non nil ma l'SSH connection non ha `RemoteAddr()` (connessione parzialmente inizializzata o in stato anomalo)
- Esiste un listener registrato globalmente ma la `SSHConnection` corrispondente non è presente in `state.SSHConnections`
- Il `listenerName` non è presente nella mappa `sshConn.Listeners` dell'owner (incoerenza di linkage)
- HTTP/TCP/Alias holder esistono ma i loro balancer non contengono servers attivi (balancer.Servers() == 0)

Cause realistiche
-----------------
- Race condition fra routine che creano listener e routine che eseguono cleanup.
- Errori/interruzioni durante la fase di bind (es. Listen fallita dopo `state.Listeners.Store` o viceversa).
- Cleanup incompleto (es. `CleanUp()` non chiamato correttamente o parzialmente).
- Logica di failover o force-connect che rimuove/aggiunge backends in modo asincrono.
- Test/manuale che manipolano le mappe di stato senza seguire la sequenza completa di creazione/registrazione/cleanup.

Come riprodurre i casi (test consigliati)
----------------------------------------
Di seguito test case consigliati da implementare in futuro (unit tests in Go) per coprire ciascuna voce del `getDirtyForwardRows()`.
Per tutti i test: creare un `State` isolato (nuovo), costruire le mappe necessarie e chiamare `getDirtyForwardRows()` sul `WebConsole` che usa quello `State`.

Nota: non serve avviare il server reale — le strutture sono in-memory e testabili.

Test 1: listener_has_no_owning_ssh_connection
- Nome test: TestDirtyListener_NoOwner
- Setup:
  - creare un `ListenerHolder{Listener: fakeListener, ListenAddr: "unittest:1234", SSHConn: nil}`
  - `state.Listeners.Store("unittest:1234", listener)`
- Azione: chiamare `getDirtyForwardRows()`
- Assert: esiste una riga con Type=="listener", Name=="unittest:1234", Issue contiene "no owning ssh connection"

Test 2: owning_ssh_connection_has_no_remote_address
- Nome test: TestDirtyListener_NoRemoteAddr
- Setup:
  - creare `sshConn := &SSHConnection{SSHConn: &ssh.ServerConn{ /* stub, RemoteAddr returns nil or nil internal*/}}` oppure impostare `sshConn.SSHConn = nil` per simulare RemoteAddr nil
  - creare ListenerHolder con SSHConn = sshConn
  - store listener e assicurarsi che `state.SSHConnections` contenga la chiave corrispondente? (per questo test l'owner esiste ma ha RemoteAddr nil)
- Assert: riga con Issue "owning ssh connection has no remote address"

Test 3: owning_ssh_connection_missing_from_active_connection_map
- Nome test: TestDirtyListener_MissingSSHConnectionMap
- Setup:
  - creare ListenerHolder con SSHConn non nil e RemoteAddr non nil
  - non inserire la SSHConnection in `state.SSHConnections`
- Assert: riga con Issue "owning ssh connection is missing from active connection map"

Test 4: listener_not_linked_in_owning_connection_map
- Nome test: TestDirtyListener_NotLinked
- Setup:
  - creare sshConn e inserirlo in `state.SSHConnections` con chiave remoteAddr
  - creare ListenerHolder e registrarlo in `state.Listeners`
  - non aggiungere listenerName alla `sshConn.Listeners` (lasciare mappa vuota)
- Assert: riga con Issue "listener is not linked in owning ssh connection listener map"

Test 5: http_forward_no_active_backends
- Nome test: TestDirtyForward_HTTPNoBackends
- Setup:
  - creare `HTTPHolder{HTTPUrl: url, SSHConnections: map con almeno 0 ssh conns, Balancer: roundrobin balancer}`
  - assicurarsi che `Balancer.Servers()` sia vuoto (no UpsertServer chiamato)
  - `state.HTTPListeners.Store(url.String(), pH)`
- Assert: riga con Type=="http" e Issue contains "no active backends"

Test 6: tcp_forward_no_active_backends
- Nome test: TestDirtyForward_TCPNoBackends
- Setup:
  - creare `TCPHolder` con `Balancers` mappa vuota o con roundrobin senza servers
  - `state.TCPListeners.Store(name, tH)`
- Assert: riga con Type=="tcp" and issue text

Test 7: alias_forward_no_active_backends
- Nome test: TestDirtyForward_AliasNoBackends
- Setup: come per HTTP/TCP ma per AliasHolder
- Assert: riga con Type=="alias"

Test implementation notes
-------------------------
- Per i fake listeners usare `net.Pipe()` o una implementazione minimale di `net.Listener` che implementa `Accept/Close/Addr` per non dover bind su porte reali.
- Per i balancer (roundrobin), è possibile creare un `roundrobin.New(fwd)` e non chiamare `UpsertServer` (Servers() == 0) oppure usare un balancer reale e rimuovere servers se l'API lo permette.
- Creare helper test-level `NewTestState()` che restituisce uno `State` con tutte le map inizializzate (syncmap.New) per evitare duplicazione del setup.
- Eseguire test in modalità parallela con attenzione: i test devono isolare `State` per non condividere dati globali.

Script di esempio (bozza) — snippet Go per test di tipo "listener_no_owner":

```go
func TestDirtyListener_NoOwner(t *testing.T) {
    st := NewTestState()
    wc := &WebConsole{State: st}

    // fake listener: use net.Pipe to build a minimal listener wrapper or provide a nil that satisfies expectations
    ln := &utils.ListenerHolder{Listener: fakeListener, ListenAddr: "unittest:1234", SSHConn: nil}
    st.Listeners.Store("unittest:1234", ln)

    rows := wc.getDirtyForwardRows()
    found := false
    for _, r := range rows {
        if r.Type == "listener" && r.Name == "unittest:1234" && strings.Contains(r.Issue, "no owning ssh connection") {
            found = true
            break
        }
    }
    if !found {
        t.Fatalf("expected dirty listener row not found: %+v", rows)
    }
}
```

Manual / integration testing
---------------------------
Se preferisci test manuali in ambiente reale (non unit test), possibili metodi:

1. Simulare una connessione SSH e forzare la creazione di un listener, quindi terminare il processo che mantiene la `SSHConnection` evitando il cleanup (kill -9). Questo può lasciare listener registrati globalmente senza owner.
2. Creare un forward HTTP/TCP e rimuovere manualmente i backend dal balancer (se possibile): porta il balancer a 0 servers.
3. Usare script che manipolano la mappa `state.*` via API interne o endpoint di debug (se esistono). ATTENZIONE: manipolazioni runtime possono causare instabilità.

Verifica via API
----------------
La pagina `/_sish/internal` espone i dati JSON usati dalla UI (`HandleInternal`). Puoi chiamare l'endpoint `/ _sish/api/internal` (o il path reale esposto) con `x-authorization` e verificare il campo `dirtyForwards` nel JSON. È il modo più semplice per validare i test integrazione end-to-end.

Raccomandazioni e next steps
---------------------------
- Implementare i test unitari sopra elencati: coprono gran parte dei casi rilevati dalla funzione di diagnostica.
- Aggiungere test che simulino condizioni di race (creazione listener seguito da rimozione parallela) usando canali e sincronizzazione per riprodurre edge-case.
- Introdurre metriche e alerting: esporre il conteggio di `dirtyListenerRows` come metrica Prometheus (se usato) per avvisare quando supera soglia.
- Valutare politiche di auto-heal: routine che periodicamente valida `state.Listeners` e provi a ricondizionare o rimuovere listener orfani in modo sicuro (solo dopo logging e possibili retry).
- Documentare le invariants: come devono essere aggiornate le mappe quando si crea/rimuove un listener (ordine atomic):
  1. creare listener (local)
  2. creare holder e bilancere se necessario
  3. eseguire UpsertServer o Store su holder
  4. aggiungere entry in `sshConn.Listeners`
  5. registrare globalmente in `state.Listeners` e `state.*Listeners`
  6. sul cleanup fare l'ordine opposto (rimuovere from balancer, rimuovere da sshConn.Listeners, Close listener, delete from state maps)

Conclusione
-----------
La sezione "Dirty / Non-Working Forwards" è un utile strumento diagnostico per rilevare inconsistenze di stato che dovrebbero essere rare. Implementando i test suggeriti e aggiungendo monitoraggio/auto-heal si ridurrà il rischio di drift di stato e si faciliterà il debugging operativo.

Se vuoi, posso anche generare i bozzetti dei file di test (test helpers + test cases) in Go: dimmi se preferisci che li prepari ora (non applicherò cambiamenti al codice sorgente senza tuo esplicito consenso).