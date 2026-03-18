# Header Policy per Forwarder HTTP/HTTPS

Questo documento descrive in dettaglio la feature di gestione header di risposta per i forwarder HTTP/HTTPS di sish.

Obiettivo:
- definire header di sicurezza di default per tutti i subdomain forwardati
- permettere override granulari per subdomain specifici
- applicare le policy anche su status non-2xx quando richiesto (`always: true`)

## Panoramica

La feature legge una configurazione YAML da directory dedicata e applica gli header sulle risposte HTTP restituite dai forwarder.

Caratteristiche principali:
- configurazione centralizzata via file YAML
- default globali (`defaults`)
- override per singolo subdomain (`subdomains.<nome>`)
- reload automatico runtime quando il file cambia
- supporto alias nomi header in stile nginx (`x_frame_options`, ecc.)
- interruttore globale di attivazione/disattivazione (`--headers-managed=true|false`)

## Flag disponibili

- `--headers-setting-directory`
- `--headers-setting-directory-watch-interval`
- `--headers-managed`

Esempio avvio:

```bash
./app \
  --domain=tuns.example.com \
  --headers-managed=true \
  --headers-setting-directory=/headers \
  --headers-setting-directory-watch-interval=200ms
```

## Attivazione globale con `--headers-managed`

Questo flag governa l'intera feature.

Regole:
- `--headers-managed=true`: feature attiva, file YAML letto, header applicati ai forwarder subdomain
- `--headers-managed=false`: feature disattiva, nessun header gestito viene applicato, comportamento uguale a prima dello sviluppo

Nota:
- valore di default e `false`
- se e `false`, anche con `--headers-setting-directory` valorizzato e file YAML corretto, non viene applicato nulla

Esempio ON:

```bash
./app \
  --domain=tuns.0912345.xyz \
  --headers-managed=true \
  --headers-setting-directory=/headers
```

Esempio OFF:

```bash
./app \
  --domain=tuns.0912345.xyz \
  --headers-managed=false \
  --headers-setting-directory=/headers
```

## Dove viene cercato il file

Dentro la directory passata con `--headers-setting-directory`, il loader cerca in questo ordine:

1. `config.yaml`
2. `config.yml`
3. `config.headers.yaml`
4. `config.headers.yml`

Esempio con Docker:

```bash
docker run --rm -it \
  -v $(pwd)/headers:/headers \
  -p 80:80 -p 443:443 -p 2222:2222 \
  fabiop85/sish:devgo1261 \
  --domain=tuns.0912345.xyz \
  --headers-managed=true \
  --headers-setting-directory=/headers
```

## Formato YAML

Struttura generale:

```yaml
defaults:
  headers:
    <chiave_header>:
      enabled: <true|false>
      value: "..."
      always: <true|false>

subdomains:
  <subdomain>:
    headers:
      <chiave_header>:
        enabled: <true|false>
        value: "..."
        always: <true|false>
```

Esempio completo:

```yaml
defaults:
  headers:
    x_frame_options:
      enabled: true
      value: "SAMEORIGIN"
      always: true
    x_xss_protection:
      enabled: true
      value: "1; mode=block"
      always: true
    referrer_policy:
      enabled: true
      value: "no-referrer-when-downgrade"
      always: true
    strict_transport_security:
      enabled: true
      value: "max-age=31536000; includeSubDomains"
      always: true
    permissions_policy:
      enabled: true
      value: "geolocation=(self), microphone=(self), camera=(self), fullscreen=(self)"
      always: true
    content_security_policy:
      enabled: true
      value: "default-src * 'unsafe-inline' 'unsafe-eval' data: blob:;"
      always: true
    x_content_type_options:
      enabled: true
      value: "nosniff"
      always: true

subdomains:
  app:
    headers:
      x_frame_options:
        enabled: false
      content_security_policy:
        enabled: true
        value: "default-src 'self'; script-src 'self' 'unsafe-inline'"

  api:
    headers:
      x_frame_options:
        enabled: false
      x_xss_protection:
        enabled: false
      permissions_policy:
        enabled: false

  static:
    headers:
      strict_transport_security:
        enabled: true
        value: "max-age=63072000; includeSubDomains; preload"
      content_security_policy:
        enabled: false
```

## Significato preciso di `enabled`, `value`, `always`

### `enabled`

Controlla se un header deve essere applicato.

Regole:
- `enabled: true`: header attivo
- `enabled: false`: header disattivo
- `enabled` assente:
  - se `value` non e vuoto, il sistema considera header attivo
  - se `value` e vuoto, il sistema considera header non attivo

Nell'override per subdomain:
- `enabled: false` rimuove esplicitamente l'header ereditato dai default

### `value`

Valore dell'header di risposta.

Regole:
- se l'header e attivo, `value` deve essere non vuoto
- nell'override subdomain, se `enabled: true` e `value` manca:
  - il sistema eredita il valore dal default (se presente)

Esempio:
- default CSP definita
- in `subdomains.app` metti solo `enabled: true` senza `value`
- il valore resta quello di default

### `always`

Controlla quando applicare l'header rispetto allo status HTTP.

Regole:
- `always: true`: applica sempre (anche 4xx/5xx)
- `always: false` o assente: applica solo su una whitelist di status "success/redirect"

Status coperti quando `always` non e true:
- 200, 201, 204, 206
- 301, 302, 303, 304, 307, 308

Conseguenza pratica:
- se vuoi vedere gli header anche su `401`, `403`, `404`, `500`, imposta `always: true`.

## Mapping chiavi YAML -> header HTTP

Le chiavi stile nginx vengono convertite cosi:

- `x_frame_options` -> `X-Frame-Options`
- `x_xss_protection` -> `X-XSS-Protection`
- `referrer_policy` -> `Referrer-Policy`
- `strict_transport_security` -> `Strict-Transport-Security`
- `permissions_policy` -> `Permissions-Policy`
- `content_security_policy` -> `Content-Security-Policy`
- `x_content_type_options` -> `X-Content-Type-Options`

Per chiavi non mappate esplicitamente:
- underscore `_` diventa `-`
- viene applicata canonicalizzazione HTTP (es. `x_custom_header` -> `X-Custom-Header`)

## Come funziona il merge `defaults` + `subdomains`

Ordine logico:
1. carica tutti gli header in `defaults.headers`
2. applica override da `subdomains.<nome>`

Comportamento override:
- `enabled: false` nel subdomain elimina l'header dal risultato finale
- `enabled: true` + `value` sostituisce il default
- `enabled: true` senza `value` mantiene il valore default (se esiste)
- `always` nel subdomain:
  - se presente, sovrascrive il default
  - se assente, eredita dal default

## Come viene identificato il subdomain

Il sistema usa host della richiesta e `--domain`.

Esempio:
- `--domain=tuns.0912345.xyz`
- richiesta a `awsdufs125.tuns.0912345.xyz`
- subdomain estratto: `awsdufs125`

Note:
- la root `tuns.0912345.xyz` non e trattata come subdomain
- se il prefisso e annidato (es. `a.b.tuns...`), viene prima cercato `a.b`; se non presente, fallback su `a`

## Casi d'uso pratici

### Caso 1: sicurezza standard su tutti i forwarder

Scopo:
- applicare un baseline security policy uniforme a tutte le app pubblicate

Configurazione:
- definire tutti gli header in `defaults.headers`
- nessun `subdomains` necessario

### Caso 2: frontend web con CSP dedicata

Scopo:
- mantenere default globali, ma usare CSP piu restrittiva su `app`

Configurazione:

```yaml
subdomains:
  app:
    headers:
      content_security_policy:
        enabled: true
        value: "default-src 'self'; script-src 'self' 'unsafe-inline'"
```

### Caso 3: API backend con header browser-specific disabilitati

Scopo:
- rimuovere header non utili per endpoint API machine-to-machine

Configurazione:

```yaml
subdomains:
  api:
    headers:
      x_frame_options:
        enabled: false
      x_xss_protection:
        enabled: false
      permissions_policy:
        enabled: false
```

### Caso 4: static assets con HSTS piu aggressivo

Scopo:
- differenziare HSTS su `static`

Configurazione:

```yaml
subdomains:
  static:
    headers:
      strict_transport_security:
        enabled: true
        value: "max-age=63072000; includeSubDomains; preload"
```

### Caso 5: header presenti anche su errori auth (401)

Scopo:
- vedere gli header anche quando upstream risponde con errore

Configurazione:

```yaml
defaults:
  headers:
    x_frame_options:
      enabled: true
      value: "SAMEORIGIN"
      always: true
```

## Verifica con curl

Controllo full headers:

```bash
curl -sSI https://awsdufs125.tuns.0912345.xyz
```

Controllo singolo header:

```bash
curl -sSI https://awsdufs125.tuns.0912345.xyz | grep -i x-frame-options
```

Controllo piu header:

```bash
curl -sSI https://awsdufs125.tuns.0912345.xyz | grep -Ei "content-security-policy|strict-transport-security|x-frame-options|referrer-policy"
```

## Troubleshooting

### Problema: nessun header viene applicato

Checklist:
1. `--headers-setting-directory` impostato correttamente
2. `--headers-managed=true` (se e `false`, la feature e volutamente spenta)
3. file presente con nome supportato (`config.yaml`, `config.yml`, `config.headers.yaml`, `config.headers.yml`)
4. `--domain` coerente con host richiesto
5. richiesta fatta su subdomain forwardato, non su root domain
6. YAML valido

### Problema: un header manca su 401/404/500

Causa tipica:
- `always` non impostato a `true`

Fix:
- aggiungi `always: true` a quell'header

### Problema: override subdomain non applicato

Checklist:
1. nome subdomain corretto in `subdomains.<nome>`
2. attenzione a prefissi annidati (`a.b`) e fallback su prima label (`a`)
3. `enabled: false` rimuove il default

## Implicazioni di sicurezza

- Gli header vengono impostati lato sish sulla risposta finale: possono sovrascrivere valori upstream.
- `Content-Security-Policy` troppo permissiva (es. `unsafe-inline`, `unsafe-eval`) riduce la protezione XSS.
- `Strict-Transport-Security` con `includeSubDomains` impatta tutti i sottodomini: usalo con consapevolezza.
- `X-XSS-Protection` e legacy su browser moderni, ma puo essere richiesto da policy aziendali.

## Suggerimenti operativi

1. Parti con default semplici e sicuri.
2. Aggiungi override solo quando strettamente necessari.
3. Per ogni override, documenta il motivo (compliance, compatibilita, performance).
4. Versiona il file YAML in git per audit e rollback.
5. Verifica sempre con `curl -I` dopo modifiche.

## Riepilogo

La policy headers permette di ottenere:
- baseline di sicurezza centralizzata per tutti i subdomain forwardati
- eccezioni controllate per servizi specifici
- comportamento prevedibile su successi/errori grazie a `always`
- gestione dinamica a caldo tramite watcher su file YAML
- spegnimento totale della feature all'avvio con `--headers-managed=false`
- modifica del file YAML direttamente dal frontend admin tramite la pagina `editheaders` (`/_sish/editheaders`, protetta da Basic Auth con `--admin-consolle-editheaders-credentials`)
