# Todo

[x] - Possibile client ID per ogni connessione, nome univoco
[x] - Ridimensionare le colonne Session ID e Pubkey Fingerprints
[x] - Implementazione di force-connect
[x] - Analisi del memory leak e di potenziali bug

## Handoff 2026-03-11 (fine giornata)

[x] - Nuova pagina `/_sish/logs` con tail/search/download
[x] - Sanitizzazione sequenze ANSI nei forwarder logs per migliorare leggibilita`
[x] - Fix inversione Upload/Download nella pagina `/_sish/audit`
[x] - Test regressione eseguiti con successo (`go test ./...`)

## Specs implementate

[x] - Spec 01: Edit Headers page (`/_sish/editheaders`)
[x] - Spec 02: Census from local directory + Edit Census page (`/_sish/editcensus`)
[x] - Spec 03: Census source control (`--strict-id-censed-url`, `--strict-id-censed-files`)
[x] - Spec 04: Census per-ID note opzionale
[x] - Spec 05: API insertuser — tutti i parametri gestiti
[x] - Spec 06: Dockerfile migrato da scratch a Alpine 3.23
[x] - Spec 07: Pulsante Notes condizionale (sish + census)
[x] - Spec 08: Colonna Forward in census (Proxy Censed + Proxy Uncensed)
[x] - Spec 09: Conferma disconnessione con modal
[x] - Spec 10: Internal runtime status page (`/_sish/internal`)
[x] - Spec 11: Miglioramenti sezione Memory e State Counts nella pagina internal
[x] - Bug Spec 10/11: Fix totalAllocMB e memoria in testata internal
[x] - Spec 12: Colonna Data Usage in Active Forwards (internal)
[x] - Spec 13: Formattazione Runtime Counters leggibile (internal)
[x] - Spec 14: Bandwidth hot-reload (`--bandwidth-hot-reload-enabled`)
[x] - Spec 15: Sezione Ingress nel modal info (sish page)
[x] - Spec 16: Colonna Ingress nella pagina history
[x] - Spec 17: Strict unique IP (`--strict-unique-ip`) — rifiuta forward se ID già in uso

## Prossimi miglioramenti (opzionali)

[ ] - Verifica e2e manuale su ambiente reale:
	- `/_sish/logs` (tail, search locale, download)
	- `/_sish/audit` (coerenza valori upload/download)
[ ] - Valutare endpoint opzionale per ricerca server-side su file molto grandi
[ ] - Valutare utility amministrativa per riscrittura/sanitizzazione file storici scaricati
