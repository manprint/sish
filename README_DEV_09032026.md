# README_DEV_09032026

Documento di sviluppo completo della sessione del `2026-03-09`.

Obiettivo di questo file:
- tracciare tutte le feature richieste e implementate in ordine cronologico
- indicare per ogni feature: specifiche richieste, modifiche fatte, file coinvolti
- fornire esempi completi di utilizzo, inclusi i flag da passare all'app all'avvio

## Timeline commit della sessione

- `abcb4b2` Timezone configurabile e Gestione Utenti con password
- `37052c8` Edit Keys FilesImplementato
- `12b7b86` Implementata edit users.
- `3777e50` upgrade sezione history
- `f0f4615` Implementata ricerca in history
- `02dcbfc` api per inseriemnto chiavi pubbliche.
- `333e14f` api per inserimento utenti.
- `3c6143e` Upgrade api.
- `5a0882a` Census. Implemented.

Note timeline:
- dopo `5a0882a` sono state implementate ulteriori estensioni e hardening (strict-id-censed e enforcement runtime) attualmente documentate in questo file, anche se non necessariamente gia aggregate in un commit unico.

---

## Feature 1 - Analisi approfondita autenticazione (fase iniziale)

### 1) Specifiche richieste
- Richiesta iniziale: analizzare in dettaglio il progetto con focus forte sulla fase di autenticazione.
- Mappare autenticazione SSH, autenticazione console web e controlli API/route.

### 2) Modifiche fatte e file coinvolti
- Nessuna modifica codice in questa fase (attivita di analisi).
- Output della fase: mappa completa dei punti auth e dei flussi.

### 3) Esempi di uso (flag avvio)
- Non applicabile (feature di analisi).
- Comando utile per ispezione runtime auth:

```bash
go run main.go \
  --debug=true \
  --authentication=true \
  --admin-console=true \
  --admin-console-token='change-me'
```

---

## Feature 2 - Auth SSH per utente da YAML + hot reload

### 1) Specifiche richieste
- Introdurre autenticazione password SSH per utente, letta da directory YAML.
- Aggiungere flag:
  - `--auth-users-enabled`
  - `--auth-users-directory`
  - `--auth-users-directory-watch-interval`
- Mantenere comportamento storico di `--authentication-password` senza regressioni.
- Reload automatico runtime quando cambiano file nella directory utenti.

### 2) Modifiche fatte e file coinvolti
- Aggiunti flag CLI:
  - `cmd/sish.go`
- Aggiunte chiavi config di esempio:
  - `config.example.yml`
- Avvio watcher auth users in bootstrap SSH:
  - `sshmuxer/sshmuxer.go`
- Implementazione caricamento YAML, lock/thread-safety, compare costante, integrazione in `PasswordCallback`, logica callback nil-safe:
  - `utils/utils.go`
- Test dedicati di regressione e reload:
  - `utils/authentication_users_test.go`
- Documentazione dedicata:
  - `README_USERS.md`
  - indice aggiornato in `README.md`

### 3) Esempi di uso (flag avvio)

Esempio avvio completo:

```bash
go run main.go \
  --authentication=true \
  --ssh-address=:2222 \
  --http-address=:80 \
  --auth-users-enabled=true \
  --auth-users-directory=/srv/sish/users \
  --auth-users-directory-watch-interval=200ms \
  --authentication-password='fallback-global-pass'
```

Esempio file YAML supportato (`/srv/sish/users/team.yml`):

```yaml
users:
  - name: alpha
    password: "A-pass"

  - name: beta
    password: "B-pass"
```

Login SSH di esempio:

```bash
ssh -p 2222 alpha@your-domain.tld
```

Note operative:
- ordine autenticazione password: `authentication-password` -> `auth-users` -> `authentication-password-request-url`
- file `.yml` e `.yaml` supportati
- reload automatico su modifica/aggiunta/rimozione file

---

## Feature 3 - Supporto timezone nell'immagine Docker finale

### 1) Specifiche richieste
- Aggiungere supporto timezone nel container finale.
- Consentire uso `TZ=Europe/Rome` (o altro timezone) in runtime.

### 2) Modifiche fatte e file coinvolti
- Installato `tzdata` nello stage builder.
- Copiata `/usr/share/zoneinfo` nello stage `scratch` finale.
- Definita variabile ambiente `TZ` di default.
- File:
  - `Dockerfile`

### 3) Esempi di uso (flag avvio)

Build ed esecuzione esempio:

```bash
docker build -t sish:dev .

docker run --rm -it \
  -e TZ=Europe/Rome \
  -p 2222:2222 -p 80:80 \
  -v /srv/sish/users:/srv/sish/users \
  sish:dev \
  --ssh-address=:2222 \
  --http-address=:80 \
  --authentication=true \
  --auth-users-enabled=true \
  --auth-users-directory=/srv/sish/users
```

---

## Feature 4 - Documentazione auth users dedicata

### 1) Specifiche richieste
- Documentare feature auth-users in un README dedicato.

### 2) Modifiche fatte e file coinvolti
- Creato documento completo:
  - `README_USERS.md`
- Aggiunto riferimento nell'indice principale:
  - `README.md`

### 3) Esempi di uso (flag avvio)
- Esempi gia presenti nel file `README_USERS.md`.
- Esempio rapido:

```bash
go run main.go \
  --authentication=true \
  --auth-users-enabled=true \
  --auth-users-directory=/users
```

---

## Feature 5 - Analisi frontend dettagliata pre-sviluppo

### 1) Specifiche richieste
- Analizzare frontend admin/console prima delle nuove feature UI.
- Verificare template, token auth, route API e limiti/rischi.

### 2) Modifiche fatte e file coinvolti
- Nessuna modifica codice in questa fase (attivita di analisi).
- Output: mappa di template, chiamate AJAX/WS, propagazione `x-authorization`, aree sensibili.

### 3) Esempi di uso (flag avvio)
- Non applicabile direttamente (analisi).
- Avvio utile per test UI:

```bash
go run main.go \
  --admin-console=true \
  --admin-console-token='admin-token' \
  --service-console=true
```

---

## Feature 6 - Nuova pagina console `editkeys` con basic auth extra

### 1) Specifiche richieste
- Aggiungere pagina `editkeys` per gestire file in `--authentication-keys-directory`.
- Funzioni richieste: lista file, View, Edit, Save.
- Sicurezza: oltre al token admin console, imporre Basic Auth extra con flag:
  - `--admin-consolle-editkeys-credentials` (`username:password`)

### 2) Modifiche fatte e file coinvolti
- Aggiunto flag CLI:
  - `cmd/sish.go`
- Aggiunta chiave config:
  - `config.example.yml`
- Nuovo template pagina edit keys:
  - `templates/editkeys.tmpl`
- Navbar e propagazione token verso nuova pagina:
  - `templates/header.tmpl`
- Backend route/handler + file operations + path traversal protection + basic auth check:
  - `utils/console.go`

Route principali:
- `GET /_sish/editkeys`
- `GET /_sish/api/editkeys/files`
- `GET /_sish/api/editkeys/file?file=<relpath>`
- `POST /_sish/api/editkeys/file`

### 3) Esempi di uso (flag avvio)

Avvio server:

```bash
go run main.go \
  --admin-console=true \
  --admin-console-token='admin-token' \
  --authentication-keys-directory=/srv/sish/pubkeys \
  --admin-consolle-editkeys-credentials='editkeysuser:editkeyspass'
```

Accesso pagina (browser):

```text
https://your-domain.tld/_sish/editkeys?x-authorization=admin-token
```

Esempio API lista file (token + basic auth):

```bash
curl -u editkeysuser:editkeyspass \
  "https://your-domain.tld/_sish/api/editkeys/files?x-authorization=admin-token"
```

---

## Feature 7 - Nuova pagina console `editusers` con validate obbligatoria

### 1) Specifiche richieste
- Aggiungere pagina `editusers` simile a `editkeys`, ma su `--auth-users-directory`.
- Funzioni richieste: lista file YAML, View, Edit, Save.
- Aggiungere bottone Validate.
- Vincolo: salvataggio consentito solo con YAML valido.
- Sicurezza: Basic Auth extra con flag:
  - `--admin-consolle-editusers-credentials`

### 2) Modifiche fatte e file coinvolti
- Aggiunto flag CLI:
  - `cmd/sish.go`
- Aggiunta chiave config:
  - `config.example.yml`
- Nuovo template pagina edit users con validate:
  - `templates/editusers.tmpl`
- Navbar e token propagation:
  - `templates/header.tmpl`
- Backend route/handler validate/read/write/list + basic auth check + sicurezza path:
  - `utils/console.go`

Route principali:
- `GET /_sish/editusers`
- `GET /_sish/api/editusers/files`
- `GET /_sish/api/editusers/file?file=<relpath>`
- `POST /_sish/api/editusers/validate`
- `POST /_sish/api/editusers/file`

### 3) Esempi di uso (flag avvio)

Avvio server:

```bash
go run main.go \
  --admin-console=true \
  --admin-console-token='admin-token' \
  --auth-users-enabled=true \
  --auth-users-directory=/srv/sish/users \
  --admin-consolle-editusers-credentials='editusersuser:edituserspass'
```

Accesso pagina (browser):

```text
https://your-domain.tld/_sish/editusers?x-authorization=admin-token
```

Esempio validate API:

```bash
curl -u editusersuser:edituserspass \
  -H 'Content-Type: application/json' \
  -d '{"content":"users:\n  - name: alpha\n    password: \"secret\"\n"}' \
  "https://your-domain.tld/_sish/api/editusers/validate?x-authorization=admin-token"
```

---

## Feature 8 - Upgrade sezione history: paginazione, clear con conferma, download CSV

### 1) Specifiche richieste
- Aggiungere paginazione (10 righe per pagina).
- Aggiungere clear history con conferma lato UI.
- Aggiungere download CSV storico.

### 2) Modifiche fatte e file coinvolti
- Backend:
  - paginazione server-side su API history
  - endpoint clear
  - endpoint download CSV
  - `utils/console.go`
- Frontend:
  - controlli Previous/Next
  - bottone Clear con confirm
  - bottone Download
  - `templates/history.tmpl`

Route:
- `GET /_sish/api/history?page=1&pageSize=10`
- `POST /_sish/api/history/clear`
- `GET /_sish/api/history/download`

### 3) Esempi di uso (flag avvio)

Avvio server:

```bash
go run main.go \
  --admin-console=true \
  --admin-console-token='admin-token'
```

Esempi API:

```bash
curl "https://your-domain.tld/_sish/api/history?page=1&pageSize=10&x-authorization=admin-token"
```

```bash
curl -X POST "https://your-domain.tld/_sish/api/history/clear?x-authorization=admin-token"
```

```bash
curl -L "https://your-domain.tld/_sish/api/history/download?x-authorization=admin-token" -o history.csv
```

---

## Feature 9 - Ricerca history case-insensitive tipo LIKE (auto da 2 caratteri)

### 1) Specifiche richieste
- Ricerca case-insensitive senza bottone.
- Trigger automatico da almeno 2 caratteri.
- Campi cercati: `ID`, `RemoteAddr`, `Username`, `Started`, `Ended`.

### 2) Modifiche fatte e file coinvolti
- Backend filtering prima della paginazione:
  - `utils/console.go`
- Frontend:
  - input ricerca live
  - debounce
  - trigger da 2 caratteri
  - `templates/history.tmpl`

### 3) Esempi di uso (flag avvio)

Avvio server:

```bash
go run main.go \
  --admin-console=true \
  --admin-console-token='admin-token'
```

Esempio API query ricerca:

```bash
curl "https://your-domain.tld/_sish/api/history?page=1&pageSize=10&q=alpha&x-authorization=admin-token"
```

---

## Feature 10 - Analisi completa API e modello auth pre-estensione API

### 1) Specifiche richieste
- Analizzare tutto il perimetro API e autenticazione prima di aggiungere nuove API pubbliche.

### 2) Modifiche fatte e file coinvolti
- Nessuna modifica codice (attivita di analisi).
- Output: mappa auth API (token admin/route, host root checks, basic auth extra su moduli sensibili).

### 3) Esempi di uso (flag avvio)
- Non applicabile direttamente (analisi).

---

## Feature 11 - API pubblica autenticata per inserire chiavi SSH

### 1) Specifiche richieste
- Nuova API per inserire chiave pubblica in `fromapi.key`.
- Dedupe (non reinserire chiave gia presente).
- Aggiungere commento timestamp `Inserted by api`.
- Gestire creazione file/directory se mancanti.
- Richiesta iniziale includeva alias pratico `/api/insert`.

### 2) Modifiche fatte e file coinvolti
- Routing root host verso handler API key:
  - `httpmuxer/httpmuxer.go`
- Handler API key, validazione chiave, dedupe su tutta la directory, append block, lock:
  - `utils/console.go`
- Sicurezza auth:
  - usa Basic Auth extra di `--admin-consolle-editkeys-credentials`

Endpoint finale attuale:
- `POST /api/insertkey`

Nota importante:
- l'alias `/api/insert` e stato rimosso nella feature di upgrade API successiva.

### 3) Esempi di uso (flag avvio)

Avvio server:

```bash
go run main.go \
  --domain=tuns.mydomain.it \
  --authentication-keys-directory=/srv/sish/pubkeys \
  --admin-consolle-editkeys-credentials='apiuser:apipassword'
```

Uso senza commento:

```bash
cat id_ed25519.pub | curl -u apiuser:apipassword -X POST \
  -d @- "https://tuns.mydomain.it/api/insertkey"
```

Uso con commento (header):

```bash
cat id_ed25519.pub | curl -u apiuser:apipassword -X POST \
  -H "x-api-comment: Public key for testing new website" \
  -d @- "https://tuns.mydomain.it/api/insertkey"
```

Formato scritto in `fromapi.key`:

```text
# Inserted by api in date: 2026-03-09-11-18-44
# Public key for testing new website
ssh-rsa AAAAB3NzaC1yc...
```

---

## Feature 12 - API pubblica autenticata per inserire utenti auth-users

### 1) Specifiche richieste
- Nuova API per inserire utenti in `fromapi.yml`.
- Vincoli:
  - usare `--auth-users-enabled` e `--auth-users-directory`
  - autenticazione basic con `--admin-consolle-editusers-credentials`
  - dedupe user per nome
  - commento timestamp `Inserted by api`
  - validazione YAML prima del write
  - mantenere formattazione leggibile

### 2) Modifiche fatte e file coinvolti
- Routing root host verso handler API user:
  - `httpmuxer/httpmuxer.go`
- Handler API user, parse form, dedupe su directory YAML, append blocco, validazione YAML strutturale, lock:
  - `utils/console.go`

Endpoint:
- `POST /api/insertuser`

### 3) Esempi di uso (flag avvio)

Avvio server:

```bash
go run main.go \
  --domain=tuns.0912345.xyz \
  --auth-users-enabled=true \
  --auth-users-directory=/srv/sish/users \
  --admin-consolle-editusers-credentials='myusername:mypassword'
```

Uso senza commento:

```bash
curl -u myusername:mypassword -X POST \
  "https://tuns.0912345.xyz/api/insertuser" \
  -d "name=myuser&password=mysecretpassword"
```

Uso con commento (header):

```bash
curl -u myusername:mypassword -X POST \
  "https://tuns.0912345.xyz/api/insertuser" \
  -H "x-api-comment: User for test webhooks" \
  -d "name=myuser&password=mysecretpassword"
```

Formato scritto in `fromapi.yml`:

```yaml
users:

# Inserted by api in date: 2026-03-09-11-37-30
# Comment from new api comment parameter
  - name: fromapi
    password: "mysecretpassword"

# Inserted by api in date: 2026-03-09-11-38-47
# User for test webhooks
  - name: pippo
    password: "mysecretpassword"
```

---

## Feature 13 - Upgrade API finale: rimozione alias `insert` + commento opzionale user friendly

### 1) Specifiche richieste
- Rimuovere alias `/api/insert` (non piu necessario).
- Aggiungere parametro opzionale commento per `insertkey` e `insertuser`.
- Se commento assente, comportamento invariato.
- Commento su nuova riga subito sotto `Inserted by api`.
- Migliorare UX evitando `%20` manuale nelle URL.

### 2) Modifiche fatte e file coinvolti
- Rimozione alias `/api/insert` dal mux:
  - `httpmuxer/httpmuxer.go`
- Supporto commento opzionale nei due handler e nei formatter block append:
  - `utils/console.go`
- Supporto header `x-api-comment` (preferito) per passare spazi naturali senza URL encoding manuale:
  - `utils/console.go`
- Sanitizzazione commento in singola riga (rimozione newline) per formato stabile:
  - `utils/console.go`

### 3) Esempi di uso (flag avvio)

Avvio server completo per entrambe API:

```bash
go run main.go \
  --domain=tuns.mydomain.it \
  --authentication-keys-directory=/srv/sish/pubkeys \
  --auth-users-enabled=true \
  --auth-users-directory=/srv/sish/users \
  --admin-consolle-editkeys-credentials='apiuser:apipassword' \
  --admin-consolle-editusers-credentials='myusername:mypassword'
```

Le 4 curl di riferimento (con e senza commento):

```bash
# 1) insertkey senza commento
cat id_ed25519.pub | curl -u apiuser:apipassword -X POST \
  -d @- "https://tuns.mydomain.it/api/insertkey"

# 2) insertkey con commento
cat id_ed25519.pub | curl -u apiuser:apipassword -X POST \
  -H "x-api-comment: Public key for testing new website" \
  -d @- "https://tuns.mydomain.it/api/insertkey"

# 3) insertuser senza commento
curl -u myusername:mypassword -X POST \
  "https://tuns.0912345.xyz/api/insertuser" \
  -d "name=myuser&password=mysecretpassword"

# 4) insertuser con commento
curl -u myusername:mypassword -X POST \
  "https://tuns.0912345.xyz/api/insertuser" \
  -H "x-api-comment: User for test webhooks" \
  -d "name=myuser&password=mysecretpassword"
```

---

## Feature 14 - Aggiornamento toolchain Go + dipendenze (fase hardening build)

### 1) Specifiche richieste
- Aggiornare runtime/build chain a Go `1.26.1`.
- Aggiornare dipendenze in modo aggressivo ma mantenendo compilazione stabile.
- Evitare regressioni su build locale, CI e immagine Docker.

### 2) Modifiche fatte e file coinvolti
- Aggiornamento versione Go e toolchain in modulo:
  - `go.mod`
  - `go.sum`
- Aggiornamenti build/runtime image:
  - `Dockerfile`
- Aggiornamento workflow CI/CD legati a build/release/docs:
  - `.github/workflows/build.yml`
  - `.github/workflows/release.yml`
  - `.github/workflows/docs.yml`

Dettagli operativi:
- allineamento a `go 1.26` + `toolchain go1.26.1`
- refresh dipendenze con `go mod tidy`
- verifica compilazione end-to-end post-upgrade

### 3) Esempi di uso (flag avvio)
- Nessun nuovo flag runtime applicativo.
- Verifica standard dopo upgrade:

```bash
go test ./... -run TestDoesNotExist -count=1
```

---

## Feature 15 - Managed security headers da YAML (default + override per subdomain)

### 1) Specifiche richieste
- Gestire response headers dei forward via config YAML esterna.
- Supportare:
  - blocco `default`
  - override per specifici subdomain
  - applicazione condizionata per status code (campo `always`)
- Non alterare comportamento se feature disattivata.

### 2) Modifiche fatte e file coinvolti
- Nuovi flag/config:
  - `--headers-managed=true|false`
  - `--headers-setting-directory`
  - `--headers-setting-directory-watch-interval`
  - file: `cmd/sish.go`, `config.example.yml`
- Implementazione parser/watcher/apply headers:
  - `utils/headers_settings.go`
- Integrazione bootstrap runtime:
  - `sshmuxer/sshmuxer.go`
- Hook applicazione headers nel path HTTP forwarding:
  - `httpmuxer/httpmuxer.go` (e punti associati al reverse forwarding)
- Documentazione dedicata:
  - `README_HEADERS.md`

Fix evolutivi nella stessa area:
- supporto naming file compatibile (`config.headers.yaml` oltre naming base)
- chiarita semantica `always: true` per includere anche risposte non 2xx/3xx

### 3) Esempi di uso (flag avvio)

```bash
go run main.go \
  --domain=tuns.example.com \
  --headers-managed=true \
  --headers-setting-directory=/srv/sish/headers
```

---

## Feature 16 - Census completo (backend + UI + refresh + source validation)

### 1) Specifiche richieste
- Introdurre feature `census` per confronto tra inventory YAML remoto e forward attivi.
- Nuova pagina frontend con 3 sezioni:
  1. `Proxy Censed`
  2. `Proxy Uncensed`
  3. `Censed Not Forwarded`
- Considerare solo forward con listener attivi (`listeners > 0`).
- Supportare refresh automatico e refresh manuale.
- Supportare `--census-url` arbitrario (valido per contenuto, non per nome file).
- Aggiungere endpoint/source viewer con validazione YAML per debug.

### 2) Modifiche fatte e file coinvolti
- Nuovi flag/config:
  - `--census-enabled`
  - `--census-url`
  - `--census-refresh-time`
  - file: `cmd/sish.go`, `config.example.yml`
- Nuovo motore census (download/parse/cache/refresh):
  - `utils/census.go`
- Nuove route/handler backend census:
  - `utils/console.go`
  - endpoint template + API + refresh + source
- Integrazione startup refresher:
  - `sshmuxer/sshmuxer.go`
- Nuova pagina UI:
  - `templates/census.tmpl`
- Aggiornamento navbar:
  - `templates/header.tmpl`
- Documentazione dedicata:
  - `README_CENSUS.md`

Route principali:
- `GET /_sish/census`
- `GET /_sish/api/census`
- `POST /_sish/api/census/refresh`
- `GET /_sish/api/census/source`

### 3) Esempi di uso (flag avvio)

```bash
go run main.go \
  --domain=tuns.example.com \
  --admin-console=true \
  --admin-console-token='admin-token' \
  --census-enabled=true \
  --census-url='https://example.com/census.yaml' \
  --census-refresh-time=2m
```

---

## Feature 17 - Dashboard clients: indicatore CID censito/non censito

### 1) Specifiche richieste
- Mostrare nella dashboard principale (`routes`) un indicatore visuale accanto all'ID client:
  - verde se censito
  - rosso se non censito

### 2) Modifiche fatte e file coinvolti
- Backend payload esteso con campo booleano (`isCensused`) per ogni client:
  - `utils/console.go`
- Frontend tabella clients aggiornata con colonna `CID` + dot colorato:
  - `templates/routes.tmpl`

### 3) Esempi di uso (flag avvio)
- Effetto visibile in dashboard con `census-enabled=true`.

---

## Feature 18 - Strict ID censito in fase di bind forward

### 1) Specifiche richieste
- Nuova modalita runtime:
  - `--strict-id-censed=true|false`
- Dipendenza:
  - ha effetto solo con `--census-enabled=true`
- Regole richieste in strict:
  1. il client deve passare esplicitamente `id=<valore>`
  2. il forward parte solo se l'ID e censito
  3. in caso di violazione, messaggio al client + chiusura connessione

Messaggi richiesti e implementati:
- `Id is enforced server side.`
- `Forwarded id is not censed.`

### 2) Modifiche fatte e file coinvolti
- Nuovo flag:
  - `cmd/sish.go`
  - `config.example.yml`
- Tracking ID realmente fornito dal client (non random di default):
  - aggiunto `ConnectionIDProvided` in `utils/conn.go`
  - valorizzato solo su comando `id=` in `sshmuxer/channels.go`
- Helper strict/census:
  - `utils/census.go` (`IsStrictIDCensedEnabled`, `IsIDCensed`)
- Enforcement nel path bind (`tcpip-forward`):
  - `sshmuxer/requests.go`
  - rifiuto request + messaggio + cleanup connessione
- Log esplicativo se strict abilitato ma census disabilitato:
  - `sshmuxer/sshmuxer.go`

### 3) Esempi di uso (flag avvio)

```bash
go run main.go \
  --domain=tuns.example.com \
  --census-enabled=true \
  --strict-id-censed=true \
  --census-url='https://example.com/census.yaml'
```

Client con ID:

```bash
ssh -p 443 -R seastream-demo:80:localhost:8080 tuns.0912345.xyz id=seastream-demo
```

---

## Feature 19 - Enforcement runtime post-refresh: deallocazione automatica forward non piu censiti

### 1) Specifiche richieste
- In strict mode, se un ID inizialmente valido viene rimosso dal census remoto, il server deve:
  1. accorgersene al refresh successivo (`--census-refresh-time` o refresh manuale)
  2. deallocare i forward della connessione
  3. chiudere la connessione client
  4. inviare messaggio `Forwarded id is not censed.`

### 2) Modifiche fatte e file coinvolti
- Nuovo enforcer runtime dedicato:
  - `sshmuxer/strict_census.go`
- Hook startup dell'enforcer:
  - `sshmuxer/sshmuxer.go`

Logica implementata:
- watcher leggero con ticker
- trigger solo su refresh census riuscito (`LastRefresh` aggiornato)
- check limitato a connessioni con listener attivi e ID esplicito client
- su mismatch:
  - invio messaggio al client
  - cleanup completo (`CleanUp`) con rilascio listener/forward

### 3) Esempi di uso (flag avvio)

```bash
go run main.go \
  --census-enabled=true \
  --strict-id-censed=true \
  --census-url='https://example.com/census.yaml' \
  --census-refresh-time=30s
```

Scenario operativo:
1. client con `id=seastream-demo` connesso e attivo
2. ID rimosso dal file census remoto
3. refresh successivo: forward deallocato + connessione chiusa

---

## Feature 20 - Documentazione tecnica estesa (census + session context)

### 1) Specifiche richieste
- Mantenere traccia dettagliata, riprendibile, di tutto il lavoro sessione.

### 2) Modifiche fatte e file coinvolti
- Estensione documentazione dedicata census/strict:
  - `README_CENSUS.md`
- Aggiornamento documento dev di sessione (questo file):
  - `README_DEV_09032026.md`

### 3) Esempi di uso (flag avvio)
- Non applicabile (feature documentale).

---

## Feature 21 - Frontend console feature gating + nuovo flag `history-enabled`

### 1) Specifiche richieste
- Rendere visibili nel frontend console solo le pagine abilitate dalle rispettive feature.
- Requisiti:
  1. `census` visibile solo con `--census-enabled=true`
  2. se `census` disabilitato, nascondere anche colonna `CID` in pagina `sish`
  3. `editkeys` visibile solo con `--admin-consolle-editkeys-credentials` valido (`user:pass` non vuoti)
  4. `editusers` visibile solo con `--admin-consolle-editusers-credentials` valido (`user:pass` non vuoti)
  5. introdurre `--history-enabled=true|false` per mostrare/nascondere history
- Vincolo: nessuna regressione.

### 2) Modifiche fatte e file coinvolti
- Nuovo flag runtime:
  - `cmd/sish.go`: aggiunto `--history-enabled` (default `true`)
  - `config.example.yml`: aggiunta chiave `history-enabled: true`

- Backend gating e context template condiviso:
  - `utils/console.go`
    - aggiunti helper:
      - `parseConsoleCredentials(...)`
      - `templateData(...)` con chiavi:
        - `ShowHistory`
        - `ShowCensus`
        - `ShowEditKeys`
        - `ShowEditUsers`
    - route history ora condizionate da `history-enabled`
    - handler history (`template/api/clear/download`) difesi anche internamente con check esplicito
    - `HandleTemplate` e altri render passano il context al template (non piu `nil`)
    - check credenziali editkeys/editusers uniformato su parser robusto (`user` e `pass` obbligatori)

- Frontend templates:
  - `templates/header.tmpl`
    - navbar links condizionali su `.ShowHistory`, `.ShowCensus`, `.ShowEditKeys`, `.ShowEditUsers`
  - `templates/routes.tmpl`
    - colonna `CID` e dot visibili solo con `.ShowCensus`

- Documentazione:
  - `README_CONSOLLE.md`
    - nuova sezione con matrice completa visibilita feature/route/UI
  - `README.md`
    - aggiornata sezione flag e indice README

### 3) Esempi di uso (flag avvio)

Tutte le pagine abilitate:

```bash
go run main.go \
  --admin-console=true \
  --admin-console-token='admin-token' \
  --history-enabled=true \
  --census-enabled=true \
  --admin-consolle-editkeys-credentials='editkeys:strongpass' \
  --admin-consolle-editusers-credentials='editusers:strongpass'
```

History disabilitata e census disabilitato:

```bash
go run main.go \
  --admin-console=true \
  --admin-console-token='admin-token' \
  --history-enabled=false \
  --census-enabled=false
```

Effetti attesi nel frontend:
- link `history` assente
- link `census` assente
- colonna `CID` assente nella pagina `sish`

---

## Riepilogo file toccati in sessione

- `Dockerfile`
- `README.md`
- `README_USERS.md`
- `README_HEADERS.md`
- `README_CENSUS.md`
- `README_CONSOLLE.md`
- `cmd/sish.go`
- `config.example.yml`
- `sshmuxer/sshmuxer.go`
- `sshmuxer/channels.go`
- `sshmuxer/requests.go`
- `sshmuxer/strict_census.go`
- `utils/utils.go`
- `utils/authentication_users_test.go`
- `utils/census.go`
- `utils/headers_settings.go`
- `utils/conn.go`
- `templates/header.tmpl`
- `templates/editkeys.tmpl`
- `templates/editusers.tmpl`
- `templates/history.tmpl`
- `templates/census.tmpl`
- `templates/routes.tmpl`
- `utils/console.go`
- `httpmuxer/httpmuxer.go`
- `go.mod`
- `go.sum`
- `.github/workflows/build.yml`
- `.github/workflows/release.yml`
- `.github/workflows/docs.yml`

## Verifiche anti-regressione effettuate durante sessione

- compile check ripetuti con:

```bash
go test ./... -run TestDoesNotExist -count=1
```

- risultato: build pacchetti OK, nessun errore compilazione.

- formattazione file Go quando necessario con:

```bash
gofmt -w <file-go-modificati>
```

- validazioni runtime eseguite durante sessione anche via curl/docker (per headers e census/strict).

## Note finali

- Endpoint chiavi valido: solo `POST /api/insertkey`.
- Endpoint utenti: `POST /api/insertuser`.
- `x-api-comment` e opzionale; se assente, comportamento invariato.
- Le API di insert operano sul root host (`--domain`) nel mux HTTP.
- La visibilita frontend delle sezioni console e ora allineata ai flag runtime, evitando link verso pagine disabilitate.
- In strict mode (`census-enabled=true` + `strict-id-censed=true`) il ciclo di enforcement avviene sia:
  - in ingresso (bind forward)
  - post-refresh (connessioni gia attive)
- Comportamento desiderato attuale in strict:
  - ID mancante/non censito => forward negato, messaggio client, connessione chiusa
  - ID rimosso successivamente dal census => forward deallocato, messaggio client, connessione chiusa

---

## Addendum sessione 2026-03-10 / 2026-03-11 (frontend console hardening)

Questa sezione documenta gli sviluppi successivi alla fase iniziale del 09/03,
con focus su pagina `sish`, pagina `history` e pagina `census`.
Include anche le regressioni emerse durante i test UI e le relative correzioni.

## Feature 22 - Auth users: supporto `pubkey` opzionale per utente YAML

### Specifiche richieste
- Consentire in `auth-users` YAML l'uso opzionale di `pubkey` per ogni utente.
- Supportare utenti con:
  - solo password
  - solo pubkey
  - password + pubkey
- Nessuna regressione su auth pre-esistente.

### Modifiche fatte
- Esteso modello `authUser` con campo `PubKey`.
- Esteso caricamento directory utenti con parsing chiavi per utente.
- Esteso callback `PublicKeyCallback` per validare chiavi per utente YAML.
- Validazione YAML strutturale aggiornata: obbligo almeno una credenziale (`password` o `pubkey`).

File coinvolti:
- `utils/utils.go`
- `utils/console.go`
- `utils/authentication_users_test.go`
- `README_USERS.md`

---

## Feature 23 - Limite banda selettivo per utenti YAML + kill switch globale

### Specifiche richieste
- Parametri opzionali per utente:
  - `bandwidth-upload`
  - `bandwidth-download`
  - `bandwidth-burst`
- Applicazione solo a utenti da `auth-users`.
- Nuovo flag globale abilitazione/disabilitazione limiter.
- Approccio a minimo impatto/no regressioni.

### Modifiche fatte
- Nuovo flag:
  - `--user-bandwidth-limiter-enabled`
- Parsing/validazione campi banda in YAML utenti.
- Trasporto profilo banda in permissions SSH.
- Enforcement a livello `CopyBoth` con limiter per direzione.
- Persistenza contatori transfer per connessione (`DataInBytes`, `DataOutBytes`).

File coinvolti:
- `cmd/sish.go`
- `config.example.yml`
- `utils/utils.go`
- `utils/conn.go`
- `sshmuxer/sshmuxer.go`
- `sshmuxer/requests.go`
- `sshmuxer/channels.go`
- `utils/authentication_users_test.go`
- `README_USER_BANDWIDTH_LIMIT.md`
- `README.md`

---

## Feature 24 - Sish: tooltip `Connection Stats` con transfer IN/OUT in MB

### Specifiche richieste
- Nel tooltip mostrare anche:
  - `DATA IN: x.y MB`
  - `DATA OUT: x.y MB`

### Modifiche fatte
- API clients estesa con `dataInBytes`/`dataOutBytes`.
- Tooltip frontend aggiornato con conversione MB a 1 decimale.

File coinvolti:
- `utils/conn.go`
- `utils/console.go`
- `templates/routes.tmpl`

---

## Feature 25 - History: colonna `Transfer`

### Specifiche richieste
- Mantenere in history i dati transfer della connessione.
- Mostrare colonna `Transfer` nelle righe.

### Modifiche fatte
- `ConnectionHistory` estesa con `DataInBytes`/`DataOutBytes`.
- Popolamento history in `CleanUp` da contatori connessione.
- API history estesa con campo `transfer`.
- CSV history esteso con colonna `Transfer`.
- Tabella frontend history aggiornata con colonna dedicata.

File coinvolti:
- `utils/conn.go`
- `utils/console.go`
- `templates/history.tmpl`

---

## Feature 26 - Census UI: traduzione descrizioni in inglese

### Specifiche richieste
- Tradurre in inglese le stringhe descrittive della pagina census.

### Modifiche fatte
- Aggiornati i testi descrittivi delle 3 sezioni census.

File coinvolti:
- `templates/census.tmpl`

---

## Feature 27 - Sish: refresh automatico transfer tooltip ogni 5s

### Specifiche richieste
- Aggiornare `DATA IN/OUT` senza refresh pagina.

### Modifiche fatte
- Polling API clients lato frontend.
- Campi transfer resi osservabili e aggiornati periodicamente.

File coinvolti:
- `templates/routes.tmpl`

---

## Feature 28 - Sish: nuova colonna `Info` + modal dettagli CLIENT/CONFIG

### Specifiche richieste
- Ridurre colonna `SSH Version`.
- Aggiungere colonna `Info` con pulsante `i`.
- Mostrare nel modal:
  - `SEZIONE CLIENT` con parametri connection-level (es. force-connect, force-https, ecc.)
  - `SEZIONE CONFIG` con parametri da YAML utenti
- `password` e `pubkey` devono essere `REDACTED`.
- Aggiornamento automatico senza refresh in caso di modifica YAML.
- Struttura facilmente estendibile per nuovi parametri.

### Modifiche fatte
- Introdotte liste centralizzate di field descriptor backend per CLIENT/CONFIG.
- Introdotto helper default/not-defined unificato.
- Introdotto storage runtime dei campi raw `auth-users` per introspezione console.
- Frontend modal info con rendering tabellare sezioni CLIENT/CONFIG.

File coinvolti:
- `utils/utils.go`
- `utils/console.go`
- `templates/routes.tmpl`

---

## Feature 29 - Sish: auto-refresh lista client/listener ogni 1s

### Specifiche richieste
- La pagina `sish` deve aggiornarsi automaticamente (nuovi listener/client) senza refresh browser.

### Modifiche fatte
- Polling `/_sish/api/clients` ogni 1 secondo.
- Sync client/listener in pagina.
- Preservazione selezione client attiva quando possibile.

File coinvolti:
- `templates/routes.tmpl`

---

## Feature 30 - Regressioni UI emerse e fix applicati

### Regressione A
- Sintomo: tooltip `Connection Stats` che resta appeso / multiplo.
- Causa: re-render aggressivo righe durante polling.
- Fix:
  - update in-place dei client esistenti
  - riduzione riassegnazioni struttura quando non necessarie

### Regressione B
- Sintomo: tooltip che spariva ogni secondo mentre il mouse restava sopra.
- Causa: hide forzato tooltip durante sync.
- Fix:
  - rimosso hide forzato
  - mantenuta indipendenza visualizzazione tooltip dal polling

### Regressione C
- Sintomo: comparsa riga testuale nativa browser che lampeggia ogni secondo.
- Causa: aggiornamento dinamico attributo `title`.
- Fix:
  - rimosso binding `title`
  - mantenuto solo `data-original-title` (tooltip Bootstrap)

### Regressione D (census)
- Sintomo: flash breve banner rosso al reload pagina.
- Causa: render iniziale HTML prima del binding knockout (`visible`).
- Fix:
  - `ko-cloak` + stato `hasLoaded`
  - banner errore visibile solo dopo primo load attempt completato

### Hardening aggiuntivo
- Anti-overlap polling: skip se request precedente ancora in-flight.

File coinvolti:
- `templates/routes.tmpl`
- `templates/census.tmpl`

---

## Stato corrente (ripartenza rapida contesto)

Se si riprende lo sviluppo frontend console, il comportamento attuale atteso e:

1. `sish` aggiorna client/listener ogni 1s senza reload pagina.
2. tooltip `Connection Stats` resta stabile sotto mouse e mostra solo popup Bootstrap.
3. transfer (`DATA IN/OUT`) e info CLIENT/CONFIG si aggiornano live.
4. polling protetto da overlap richieste in caso rete lenta.
5. `history` mostra transfer per connessione anche in CSV.
6. `census` non mostra piu il flash rosso transitorio al reload pagina.
