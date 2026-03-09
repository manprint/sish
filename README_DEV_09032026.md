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

## Riepilogo file toccati in sessione

- `Dockerfile`
- `README.md`
- `README_USERS.md`
- `cmd/sish.go`
- `config.example.yml`
- `sshmuxer/sshmuxer.go`
- `utils/utils.go`
- `utils/authentication_users_test.go`
- `templates/header.tmpl`
- `templates/editkeys.tmpl`
- `templates/editusers.tmpl`
- `templates/history.tmpl`
- `utils/console.go`
- `httpmuxer/httpmuxer.go`

## Verifiche anti-regressione effettuate durante sessione

- compile check ripetuti con:

```bash
go test ./... -run TestDoesNotExist -count=1
```

- risultato: build pacchetti OK, nessun errore compilazione.

## Note finali

- Endpoint chiavi valido: solo `POST /api/insertkey`.
- Endpoint utenti: `POST /api/insertuser`.
- `x-api-comment` e opzionale; se assente, comportamento invariato.
- Le API di insert operano sul root host (`--domain`) nel mux HTTP.
