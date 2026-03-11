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

## Ripartenza consigliata per domani

[ ] - Verifica e2e manuale su ambiente reale:
	- `/_sish/logs` (tail, search locale, download)
	- `/_sish/audit` (coerenza valori upload/download)
[ ] - Valutare endpoint opzionale per ricerca server-side su file molto grandi
[ ] - Valutare utility amministrativa per riscrittura/sanitizzazione file storici scaricati
