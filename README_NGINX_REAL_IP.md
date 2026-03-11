# Nginx + Sish: IP Reale in Console

Questo documento spiega come ottenere l'indirizzo IP reale del client nella console di sish quando sish e` dietro nginx.

Scenario tipico:
- ingresso unico su `:443`
- nginx usa `stream` con `ssl_preread` per separare traffico TLS (web) e SSH
- nginx inoltra SSH verso `sish:2222`

Problema tipico:
- in console appare `172.18.x.x:<port>` (IP del container nginx) invece dell'IP pubblico reale del client.

## Punto Chiave

La colonna `Client Remote Address` della console di sish deriva dalla connessione SSH TCP accettata da sish (`RemoteAddr`), non dagli header HTTP (`X-Forwarded-For`, `X-Real-IP`).

Quindi:
- per la parte SSH serve il PROXY protocol tra nginx e sish
- per la parte HTTP/web gli header sono utili, ma non cambiano il valore della colonna SSH in console

## Prerequisiti

1. nginx `stream` deve inviare PROXY protocol verso upstream SSH (`proxy_protocol on;`).
2. sish deve leggere PROXY protocol sul listener SSH (`proxy-protocol-listener: true`).
3. i client SSH devono entrare sempre dal path nginx (`:443`) e non direttamente su `sish:2222` pubblico.

Se uno dei tre punti manca, vedrai IP non reali o incoerenti in console.

## Configurazione Nginx (stream + http)

Riferimento file: `nginx-template/nginx.conf`

Esempio essenziale stream:

```nginx
stream {
    map $ssl_preread_protocol $backend {
        "TLSv1.3" web_backend;
        "TLSv1.2" web_backend;
        "TLSv1.1" web_backend;
        "TLSv1"   web_backend;
        default    ssh_backend;
    }

    upstream web_backend {
        server 127.0.0.1:4443;
    }

    upstream ssh_backend {
        server sish:2222;
    }

    server {
        listen 443;
        proxy_pass $backend;
        ssl_preread on;
        proxy_protocol on;
        proxy_timeout 3600s;
        proxy_responses 1;
    }
}
```

Riferimento file: `nginx-template/sish.conf`

Esempio essenziale lato HTTPS interno (`4443`):

```nginx
server {
    listen 4443 ssl proxy_protocol;
    set_real_ip_from 127.0.0.1;
    real_ip_header proxy_protocol;

    location / {
        include /nginx/common.conf;
        proxy_pass http://sish;
    }
}
```

Riferimento file: `nginx-template/common.conf`

Header HTTP utili per la parte web:

```nginx
proxy_set_header X-Real-IP $remote_addr;
proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
proxy_set_header X-Forwarded-Proto $scheme;
```

## Configurazione Sish

Impostazioni raccomandate (in config o flag):

```yaml
proxy-protocol-listener: true
proxy-protocol-policy: require
proxy-protocol-use-timeout: true
proxy-protocol-timeout: 200ms
proxy-protocol-version: "1"
```

Note:
- `proxy-protocol-listener: true` e` obbligatorio per leggere l'IP reale sul lato SSH.
- `proxy-protocol-policy: require` e` consigliato se tutto il traffico SSH passa da nginx; evita connessioni senza header PROXY.
- Se sei in migrazione e hai ancora client diretti su `:2222`, puoi usare temporaneamente `proxy-protocol-policy: use`.

## Architettura del Flusso

Percorso corretto per avere IP reale in console:

1. client SSH si connette a `dominio:443`
2. nginx stream riconosce traffico non TLS e inoltra a `sish:2222`
3. nginx invia header PROXY (`proxy_protocol on`)
4. sish parse PROXY protocol (`proxy-protocol-listener: true`)
5. sish salva `RemoteAddr` reale in console/history

## Perche` compare 172.18.x.x

Cause comuni:

1. `proxy-protocol-listener` disattivo su sish.
2. client che bypassano nginx e arrivano diretti a `sish:2222`.
3. policy PROXY troppo permissiva in ambiente misto.
4. mismatch tra config template e config realmente deployata.

Effetto:
- sish vede il peer TCP locale (container nginx), non il client Internet.

## Checklist Rapida di Verifica

1. Verifica nginx stream:
- `proxy_protocol on;` presente nel `server` su `listen 443`
- upstream SSH puntato a `sish:2222`

2. Verifica sish runtime:
- `proxy-protocol-listener=true`
- policy coerente (`require` consigliata)

3. Verifica esposizione porte:
- non esporre pubblicamente `2222` se vuoi path unico e risultati coerenti
- forzare ingresso SSH da `:443`

4. Test end-to-end:
- apri un nuovo tunnel SSH via `:443`
- controlla console `/_sish/console`
- `Client Remote Address` deve mostrare IP pubblico reale

## Strategia Consigliata in Produzione

1. Path unico: tutto SSH solo via nginx `:443`.
2. sish con `proxy-protocol-listener=true`.
3. policy `require` per evitare fallback nascosti.
4. `2222` non raggiungibile da Internet (solo rete interna tra servizi).

## Troubleshooting

Caso A: in console vedi ancora `172.18.x.x`
- controlla che la connessione test sia passata da `:443` e non da `:2222`
- controlla i parametri runtime effettivi di sish (non solo il file template)
- verifica che nginx in esecuzione corrisponda davvero al template

Caso B: con `policy=require` alcuni client non si connettono
- significa che stanno arrivando senza PROXY header
- o bypassano nginx, o c'e` un hop intermedio che non inoltra PROXY protocol

Caso C: la web console mostra IP corretto ma la colonna client no
- comportamento possibile e normale se solo HTTP header e` corretto
- per la colonna client serve la catena TCP PROXY protocol lato SSH

## Riferimenti File nel Repository

- `nginx-template/nginx.conf`
- `nginx-template/sish.conf`
- `nginx-template/common.conf`
- `config.example.yml`

## Conclusione

Per avere l'IP reale nella console sish dietro nginx non basta impostare header HTTP.
Serve una catena coerente sul traffico SSH:
- nginx `proxy_protocol on`
- sish `proxy-protocol-listener=true`
- niente bypass diretto su `2222` pubblico

Con questa combinazione la colonna `Client Remote Address` mostra l'IP reale del client in modo affidabile.
