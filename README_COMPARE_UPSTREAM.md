# README_COMPARE_UPSTREAM

Analisi di retrocompatibilita tra:

- **Upstream**: `https://github.com/antoniomika/sish`
- **Fork corrente**: `manprint-sish-fork`

Obiettivo: valutare se il fork puo essere avviato in modalita upstream-like, quali differenze operative esistono, e cosa succede quando i flag custom non sono passati.

---

## 1) Sintesi esecutiva

### Risposta breve alle tue 3 domande

1. **Se avvii il fork con comando upstream-like, e retrocompatibile?**  
   **Quasi si**, ma con un caveat critico: nel comando upstream-like che hai scritto c'e `--force-channel-reconnect=true`, che nel fork **non esiste** (errore `unknown flag`).

2. **Ci sono problemi di funzionamento del fork?**  
   Con set di flag compatibili, il core funziona regolarmente. Le differenze principali sono funzionalita aggiuntive gated da flag e hardening interno (lifecycle/reconnect) trasparente.

3. **Senza tutti i flag nuovi le funzionalita sono disabilitate?**  
   **Si**: i nuovi moduli principali sono disabilitati di default (`false` o `disable`).

---

## 2) Verifica concreta: compatibilita del comando upstream-like

### Esito

Il comando upstream-like che hai incollato **non parte** sul fork in forma identica, perche include:

- `--force-channel-reconnect=true` → **flag non supportata** nel fork (e non presente in `cmd/sish.go`).

Verifica locale:

```bash
go run . --force-channel-reconnect=true --version
# Error: unknown flag: --force-channel-reconnect
```

### Conclusione pratica

Per avvio retrocompatibile sul fork, rimuovi quella flag oppure sostituiscila con le flag realmente presenti (`--enable-force-connect` e relative opzioni client, se vuoi takeover forzato).

---

## 3) Stato delle funzionalita custom (default e impatto)

Di seguito le principali estensioni del fork con default da `cmd/sish.go`.

## 3.1 Autenticazione utenti YAML

- `--auth-users-enabled=false`
- `--auth-users-directory=""`
- `--auth-users-directory-watch-interval=200ms`

Se non abiliti `auth-users-enabled`, il path custom resta spento e rimangono validi i meccanismi classici (`authentication-password`, `authentication-keys-directory`, request-url auth).

Riferimenti:

- `cmd/sish.go:70-72`
- `utils/utils.go:748-752`, `924-947`

## 3.2 Headers managed

- `--headers-managed=false`
- `--headers-setting-directory=""`

Se disabilitato, non applica policy headers custom.

Riferimenti:

- `cmd/sish.go:91,147`
- `utils/headers_settings.go:163-169`

## 3.3 Census + strict modes

- `--census-enabled=false`
- `--strict-id-censed=false`
- `--strict-id-censed-url=false`
- `--strict-id-censed-files=false`
- `--census-url=""`
- `--census-directory=""`

Se disabilitato, nessuna enforcement censimento.  
Nota: c'e logica legacy che, se `strict-id-censed=true`, puo accendere url/files in base a `census-url`/`census-directory`.

Riferimenti:

- `cmd/sish.go:92-93,149,153-155`
- `sshmuxer/sshmuxer.go:76-87`
- `utils/census.go:190-210`

## 3.4 Force connect

- `--enable-force-connect=false`

Se il client manda `force-connect=true` e lato server la flag e `false`, richiesta ignorata (fallback comportamento normale).

Riferimenti:

- `cmd/sish.go:99`
- `sshmuxer/channels.go:358`
- `README_FORCE_CONNECT.md`

## 3.5 SSH over HTTPS

- `--ssh-over-https=false`

Attivo solo se anche `--https=true`.

Riferimenti:

- `cmd/sish.go:118`
- `sshmuxer/sshmuxer.go:104-123`

## 3.6 History / Internal pages

- `--history-enabled=false`
- `--show-internal-state=false`

Se false, endpoint/pagine relative non esposte.

Riferimenti:

- `cmd/sish.go:148,150`
- `utils/console.go:1300+`, `1579+`, `2716+`

## 3.7 Bandwidth limiter / hot reload

- `--user-bandwidth-limiter-enabled=false`
- `--bandwidth-hot-reload-enabled=false`
- `--bandwidth-hot-reload-time=20s`

Con limiter disabilitato, il profilo resta stats-only.

Riferimenti:

- `cmd/sish.go:151-152,183`
- `utils/bandwidth_hot_reload.go:15-17`, `73-75`
- `README_USER_BANDWIDTH_LIMIT.md`

## 3.8 Strict unique ID

- `--strict-unique-ip=false`

Se false, non applica reject per ID gia in uso.

Riferimenti:

- `cmd/sish.go:156`
- `sshmuxer/requests.go:174`

## 3.9 Forwarder logs dedicati

- `--forwarders-log="disable"`
- `--forwarders-log-dir="/fwlogs"`

Attivi solo se `forwarders-log=enable`.

Riferimenti:

- `cmd/sish.go:86-89`
- `utils/forwarder_logs.go:26`

---

## 4) Compatibilita del tuo comando upstream-like (flag-by-flag, punti importanti)

Le flag core usate da te (`ssh/http/https address`, cert dir, auth keys/password, bind-random*, force-requested*, admin console, proxy-protocol-listener, domain, verify-ssl, log-to-client, ecc.) risultano supportate dal fork.

### Incompatibilita certa

- `--force-channel-reconnect=true` → **non supportata** (startup fail).

### Osservazioni operative (non blocco, ma da sapere)

- `--service-console-max-content-length=0` non equivale a "illimitato"; nei check del proxy il body viene catturato solo con `-1` oppure `content-length < max`. Con `0`, di fatto la cattura e molto limitata/nulla.
  - Riferimenti: `httpmuxer/httpmuxer.go:372`, `httpmuxer/proxy.go:52`, `cmd/sish.go:172`.

---

## 5) Differenze runtime anche senza flag custom

Il fork include hardening lifecycle/reconnect non legato a una singola feature-flag custom, ma il comportamento atteso resta compatibile lato utente.

Esempi:

- cleanup forward piu robusto/idempotente
- diagnostica interna e metriche estese
- miglior gestione scenari reconnect rapidi

Riferimenti:

- `README_HARDENING_LIFECYCLE.md`
- `sshmuxer/forward_lifecycle.go`

Impatto: tipicamente migliorativo/trasparente, non un breaking change API/CLI.

---

## 6) Compatibilita del comando "fork completo"

Il tuo comando completo del fork usa estensioni non presenti upstream (auth-users, headers-managed, census, strict unique ID, history/internal, forwarders log, hot-reload, ssh-over-https, force-connect, ecc.).

Conclusione:

- quel comando e **corretto per il fork**
- non e trasponibile 1:1 su upstream

---

## 7) Raccomandazione pratica

Se vuoi far partire il fork in modalita il piu possibile simile a upstream:

1. Rimuovi `--force-channel-reconnect=true` (flag invalida sul fork).
2. Mantieni disabilitate (o non passare) le flag custom del fork.
3. Se usi `service-console-max-content-length`, usa `-1` per "unlimited" (non `0`).

---

## 8) Conclusione finale

- **Retrocompatibilita generale**: buona/alta.
- **Blocco attuale nel comando upstream-like**: solo la flag `--force-channel-reconnect`.
- **Feature custom senza flag**: rimangono disabilitate nella quasi totalita dei casi (default espliciti `false`/`disable`).

In pratica: il fork puo funzionare come upstream, ma il comando va allineato rimuovendo la flag non supportata.
