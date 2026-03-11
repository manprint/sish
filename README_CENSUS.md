# README_CENSUS

Documentazione completa della feature `census` (censimento forward) nel frontend admin.

## Obiettivo

La pagina `census` permette di confrontare:
- gli ID censiti in un file YAML remoto
- gli ID dei forward attivi nel sistema

Con questo confronto, l'admin puo individuare:
1. forward censiti e realmente attivi
2. forward attivi ma non censiti
3. ID censiti ma non presenti come forward attivi

## Feature toggle

La feature e controllata globalmente da:
- `--census-enabled=true|false`

Regole:
- `true`: feature attiva (pagina + API + refresh automatico)
- `false`: feature disattiva (comportamento come prima, route census non disponibili)

## Flag disponibili

- `--census-enabled` (default: `false`)
- `--strict-id-censed` (default: `false`, attivo solo se `--census-enabled=true`)
- `--census-url` (URL HTTP/HTTPS del file YAML di censimento)
- `--census-refresh-time` (default: `2m`)

Esempio avvio base:

```bash
./app \
  --domain=tuns.example.com \
  --admin-console=true \
  --admin-console-token='change-me' \
  --census-enabled=true \
  --strict-id-censed=false \
  --census-url='https://miodominio/census.yaml' \
  --census-refresh-time=2m
```

## Modalita strict sugli ID

Flag:
- `--strict-id-censed=true|false`

Dipendenza:
- ha effetto solo con `--census-enabled=true`
- con `--census-enabled=false` il comportamento resta invariato (strict non applicato)

Quando `--strict-id-censed=true`:
1. il client deve passare esplicitamente `id=<valore>` in fase SSH
2. il forward parte solo se quell'ID e presente nel census cache corrente
3. se il controllo fallisce, il server rifiuta il forward **e chiude la connessione SSH**
4. se un ID gia connesso viene rimosso dal census remoto, al refresh successivo il server dealloca i forward e chiude la connessione SSH del client

Messaggi lato client SSH in caso di blocco:
- senza ID esplicito: `Id is enforced server side.`
- ID non censito: `Forwarded id is not censed.`

Effetto operativo lato client:
- dopo uno dei due messaggi sopra, la sessione SSH viene terminata dal server
- eventuali tentativi successivi nello stesso canale non sono possibili (serve nuova connessione SSH)
- in caso di rimozione ID dal census durante sessione attiva, il client riceve `Forwarded id is not censed.` e viene disconnesso automaticamente

Quando `--strict-id-censed=false`:
- comportamento invariato rispetto a prima (nessun enforcement aggiuntivo)

### Matrice comportamentale (rapida)

1. `census-enabled=false`, `strict-id-censed=false`:
- census disattivo, nessun controllo strict

2. `census-enabled=false`, `strict-id-censed=true`:
- strict non applicato (dipendenza non soddisfatta), comportamento come caso 1

3. `census-enabled=true`, `strict-id-censed=false`:
- census attivo per UI/API, ma forwarding non bloccato da regole strict

4. `census-enabled=true`, `strict-id-censed=true`:
- strict pienamente attivo: ID obbligatorio e presente nel census
- in caso di mismatch/mancanza ID: forward negato + connessione SSH chiusa

### Nota su cache census e strict

- Il check strict usa la cache census corrente in memoria.
- Se `census-url` e irraggiungibile e la cache e vuota, gli ID risulteranno non censiti fino al prossimo refresh valido.
- In ambienti strict, e consigliato verificare il primo refresh con:
  - `POST /_sish/api/census/refresh`
  - controllo `lastError` via `GET /_sish/api/census`
- Dopo ogni refresh riuscito (automatico o manuale), i client strict con forward attivi vengono rivalutati: se l'ID non e piu censito, il server chiude automaticamente i forward e la connessione.

## Formato YAML supportato (remoto)

Il file remoto deve essere una lista YAML di oggetti con `id`:

```yaml
- id: "superdufs-awsde01-natgw"
- id: "xiaomi-superdufs"
- id: "superdufs-ibg-86"
```

Note:
- ID vuoti vengono ignorati
- duplicati vengono deduplicati automaticamente
- ordine interno normalizzato lato cache

## Cosa confronta esattamente

Il census confronta gli ID del file remoto con gli ID forward attivi nel sistema.

ID forward usato nel confronto:
- `ConnectionID` della connessione SSH (stesso `id` visibile nella dashboard clients)

Filtro fondamentale richiesto:
- sono considerati solo forward con **almeno un listener attivo** (`listeners > 0`)

## Sezioni della pagina `census`

La pagina e accessibile da navbar (`census`) accanto a `editusers`.

Route pagina:
- `GET /_sish/census?x-authorization=<admin-token>`

Sezione 1: `Proxy Censed`
- forward attivi (`listeners > 0`) presenti anche nel file census

Sezione 2: `Proxy Uncensed`
- forward attivi (`listeners > 0`) non presenti nel file census

Sezione 3: `Censed Not Forwarded`
- ID presenti nel file census ma non presenti nei forward attivi

## Refresh dati

La feature supporta due modalita:

1. Refresh automatico
- avviene lato server ogni `--census-refresh-time` (default 2 minuti)
- la pagina esegue polling periodico per aggiornare la UI con lo stesso intervallo

2. Refresh manuale
- pulsante `Refresh` nella pagina
- forza immediatamente download e parsing da `--census-url`

## API census

### 1) Lettura stato census

- `GET /_sish/api/census?x-authorization=<admin-token>`

Risposta tipica:

```json
{
  "status": true,
  "proxyCensed": [
    {"id": "superdufs-awsde01-natgw", "listeners": 3, "remoteAddr": "1.2.3.4:54321"}
  ],
  "proxyUncensed": [
    {"id": "my-new-forward", "listeners": 1, "remoteAddr": "5.6.7.8:12345"}
  ],
  "censedNotForwarded": [
    {"id": "xiaomi-superdufs"}
  ],
  "censusUrl": "https://miodominio/census.yaml",
  "lastRefreshPretty": "2026/03/09 - 13:21:00",
  "lastError": "",
  "refreshEverySeconds": 120
}
```

### 2) Refresh manuale da URL remoto

- `POST /_sish/api/census/refresh?x-authorization=<admin-token>`

Risposta:

```json
{
  "status": true
}
```

Se `census-url` non e raggiungibile o il file e invalido, ritorna errore HTTP.

### 3) Visualizzazione sorgente remota + validazione YAML

- `GET /_sish/api/census/source?x-authorization=<admin-token>`

Uso:
- restituisce il contenuto raw scaricato da `census-url`
- indica se il payload e YAML valido nel formato previsto
- utile per debug veloce direttamente dalla UI (`View source`)

Esempio risposta semplificata:

```json
{
  "status": true,
  "censusUrl": "https://miodominio/census.yaml",
  "content": "- id: \"a\"\n- id: \"b\"\n",
  "validYaml": true,
  "parsedIds": ["a", "b"],
  "error": ""
}
```

## Sicurezza e permessi

- Le route census sono disponibili solo su root host admin console.
- Richiedono autenticazione admin (`x-authorization` con `admin-console-token`).
- Nessuna credenziale Basic extra per census (al momento).

## Esempi pratici

### Esempio 1: test locale rapido con server YAML statico

1. Crea file census locale:

```bash
cat > /tmp/census.yaml << 'EOF'
- id: "superdufs-awsde01-natgw"
- id: "xiaomi-superdufs"
- id: "superdufs-ibg-86"
EOF
```

2. Servilo via HTTP:

```bash
cd /tmp && python3 -m http.server 18080
```

3. Avvia sish:

```bash
./app \
  --domain=tuns.local \
  --admin-console=true \
  --admin-console-token='admin-token' \
  --census-enabled=true \
  --census-url='http://127.0.0.1:18080/census.yaml' \
  --census-refresh-time=30s
```

4. Apri pagina:

```text
http://tuns.local/_sish/census?x-authorization=admin-token
```

### Esempio 2: deploy Docker con census remoto HTTPS

```bash
docker run --rm -it \
  -p 80:80 -p 443:443 -p 2222:2222 \
  fabiop85/sish:devgo1261 \
  --ssh-address=:2222 \
  --http-address=:80 \
  --https=true \
  --https-address=:443 \
  --domain=tuns.0912345.xyz \
  --admin-console=true \
  --admin-console-token='super-secret-admin-token' \
  --census-enabled=true \
  --census-url='https://miodominio/census.yaml' \
  --census-refresh-time=2m
```

### Esempio 3: feature spenta esplicitamente

```bash
./app \
  --admin-console=true \
  --admin-console-token='admin-token' \
  --census-enabled=false
```

Risultato:
- pagina/API census non utilizzabili (feature off)

### Esempio 4: strict attivo con ID censito

Avvio server:

```bash
./app \
  --domain=tuns.local \
  --admin-console=true \
  --admin-console-token='admin-token' \
  --census-enabled=true \
  --strict-id-censed=true \
  --census-url='http://127.0.0.1:18080/census.yaml' \
  --census-refresh-time=30s
```

Connessione client (esempio HTTP forward su 80):

```bash
ssh -p 2222 -R 80:localhost:3000 serveo@tuns.local id=superdufs-awsde01-natgw
```

Se l'ID e presente nel census, il forward viene avviato normalmente.

### Esempio 5: strict attivo senza ID

```bash
ssh -p 2222 -R 80:localhost:3000 serveo@tuns.local
```

Output atteso lato client:
- `Id is enforced server side.`
- `Warning: remote port forwarding failed for listen port 80`
- chiusura della connessione SSH da parte del server

### Esempio 6: strict attivo con ID non censito

```bash
ssh -p 2222 -R 80:localhost:3000 serveo@tuns.local id=non-presente
```

Output atteso lato client:
- `Connection id set to: non-presente`
- `Forwarded id is not censed.`
- `Warning: remote port forwarding failed for listen port 80`
- chiusura della connessione SSH da parte del server

### Esempio 7: ID rimosso dal census durante una sessione attiva

Scenario:
1. client connesso con `id=seastream-demo` (inizialmente censito)
2. l'ID viene rimosso dal file remoto `census.yaml`
3. avviene refresh automatico (`--census-refresh-time`) o refresh manuale

Effetto atteso:
- il server rileva che l'ID non e piu censito
- invia al client: `Forwarded id is not censed.`
- dealloca i forward della connessione
- chiude la connessione SSH del client

## Test API con curl

### Leggere stato census

```bash
curl -sS "https://tuns.0912345.xyz/_sish/api/census?x-authorization=super-secret-admin-token" | jq
```

### Forzare refresh manuale

```bash
curl -sS -X POST "https://tuns.0912345.xyz/_sish/api/census/refresh?x-authorization=super-secret-admin-token" | jq
```

### Verifica errore con token sbagliato

```bash
curl -i "https://tuns.0912345.xyz/_sish/api/census?x-authorization=wrong-token"
```

### Leggere sorgente census e validita YAML

```bash
curl -sS "https://tuns.0912345.xyz/_sish/api/census/source?x-authorization=super-secret-admin-token" | jq
```

## Troubleshooting

### Problema: pagina census non accessibile

Checklist:
1. `--admin-console=true`
2. token admin corretto (`x-authorization`)
3. `--census-enabled=true`
4. richiesta su host root domain corretto

### Problema: tutte le sezioni vuote

Possibili cause:
1. nessun forward attivo con listener > 0
2. census non ancora refreshato
3. `census-url` non raggiungibile
4. YAML remoto vuoto o non nel formato corretto

Controlli:
- usa refresh manuale (`POST /_sish/api/census/refresh`)
- verifica `lastError` nella risposta `/_sish/api/census`

### Problema: `Censed Not Forwarded` pieno di elementi

Significa che:
- gli ID nel file census non matchano i `ConnectionID` reali dei forward

Azioni:
1. controlla gli ID reali nella pagina clients (colonna ID)
2. allinea il file `census.yaml` a quei valori

### Problema: refresh automatico sembra non funzionare

Checklist:
1. `--census-refresh-time` valido (es. `30s`, `2m`, `5m`)
2. orario ultimo refresh (`lastRefreshPretty`) cambia nel tempo
3. URL raggiungibile dal container/host dove gira sish

### Problema: strict attivo ma forwarding sempre negato

Checklist:
1. verifica combinazione flag (`census-enabled=true` e `strict-id-censed=true`)
2. verifica che il client passi davvero `id=<valore>`
3. verifica che l'ID sia presente nel file census remoto
4. forza refresh manuale (`POST /_sish/api/census/refresh`)
5. controlla `lastError` e, se necessario, `/_sish/api/census/source`

Nota:
- con strict attivo, un tentativo fallito chiude la connessione SSH: riprova con una nuova sessione.

### Problema: `census-url` HTTPS con certificato non trusted

Sintomi:
- errori durante refresh
- `lastError` valorizzato

Azioni:
1. assicurati che i CA cert siano presenti nell'immagine/runtime
2. usa endpoint con certificato valido

## Comportamento in caso di errore download/parsing

- il refresh fallito non blocca l'app
- la cache precedente resta disponibile
- `lastError` espone l'ultimo errore riscontrato
- puoi forzare nuovo tentativo dal pulsante `Refresh`

## Best practice operative

1. Mantieni `census.yaml` versionato (git) per audit.
2. Usa naming coerente tra processi che impostano `ConnectionID` e census.
3. Evita refresh troppo aggressivi su URL remoti (2m e un default equilibrato).
4. Monitora periodicamente `Proxy Uncensed` e `Censed Not Forwarded` come indicatori di drift.
5. Proteggi sempre il token admin (evita condivisione in chiaro).

## Riepilogo

La feature `census` ti permette di:
- controllare coerenza tra inventario atteso e forward reali
- trovare rapidamente deviazioni operative
- avere vista aggiornata automatica e refresh manuale on-demand
- lavorare in sicurezza senza impattare i flussi esistenti quando disabilitata
