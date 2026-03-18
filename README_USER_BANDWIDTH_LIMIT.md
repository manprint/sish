# README_USER_BANDWIDTH_LIMIT

Documentazione completa della limitazione banda per utente, configurabile tramite file `auth-users` YAML.

## Obiettivo

Limitare banda upload/download in modo selettivo per utenti definiti in `--auth-users-directory`, con impatto minimo e senza alterare i flussi non coinvolti.

La feature e' utile per:
- evitare saturazione banda da parte di utenti non prioritari
- applicare policy diverse per utenti diversi
- mantenere utenti senza limitazioni dove necessario

## Flag di abilitazione

La feature e' controllata globalmente da:
- `--user-bandwidth-limiter-enabled=true|false`

Regole:
- `true`: i parametri banda nello YAML utenti hanno effetto
- `false`: i parametri banda nello YAML utenti sono ignorati

Nota:
- la feature dipende da `--auth-users-enabled=true` e dal caricamento utenti da `--auth-users-directory`

## Hot-reload dei limiti di banda

Per applicare modifiche ai limiti di banda senza riavviare i tunnel attivi:

- `--bandwidth-hot-reload-enabled=true|false` (default: `false`)
- `--bandwidth-hot-reload-time=20s` (default: `20s`, intervallo di reconcile)

Comportamento:
- quando `--bandwidth-hot-reload-enabled=true`, l'applicazione verifica periodicamente (ogni `--bandwidth-hot-reload-time`) se i limiti configurati negli YAML sono cambiati rispetto al profilo attivo della connessione.
- se rileva una modifica, aggiorna il profilo di banda **a caldo** sulla connessione SSH senza chiuderla.
- i nuovi limiti si applicano sia alle stream nuove che a quelle gia' in corso (limiter dinamico).
- se i limiti vengono rimossi per un utente, il profilo diventa "stats-only" (mantiene le metriche data-in/out senza limiti attivi).
- l'update viene loggato (old -> new values).

Esempio avvio con hot-reload:

```bash
./app \
  --authentication=true \
  --auth-users-enabled=true \
  --auth-users-directory=/users \
  --user-bandwidth-limiter-enabled=true \
  --bandwidth-hot-reload-enabled=true \
  --bandwidth-hot-reload-time=30s
```

Esempio avvio senza hot-reload (serve restart del client per applicare nuovi limiti):

```bash
./app \
  --authentication=true \
  --auth-users-enabled=true \
  --auth-users-directory=/users \
  --user-bandwidth-limiter-enabled=true \
  --bandwidth-hot-reload-enabled=false
```

## Parametri YAML supportati

Ogni utente in `users:` puo' avere campi opzionali:
- `bandwidth-upload`
- `bandwidth-download`
- `bandwidth-burst`

Esempio:

```yaml
users:
  - name: guest
    password: "guest"
    bandwidth-upload: "10"
    bandwidth-download: "20"

  - name: pippo
    password: "synclab2023"
    pubkey: "ssh-rsa AAAAB3NzaC1yc2E..."
    bandwidth-upload: "10"
    bandwidth-download: "20"
    bandwidth-burst: "1.5"

  - name: pluto
    pubkey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI..."
    bandwidth-upload: "50"

  - name: paperino
    pubkey: "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAI..."
```

## Significato dei campi

- `bandwidth-upload`: limite upload in megabit/s
- `bandwidth-download`: limite download in megabit/s
- `bandwidth-burst`: fattore burst iniziale (moltiplicatore), opzionale

Valori accettati:
- stringhe numeriche positive (`"10"`, `"20"`, `"1.5"`)
- se omessi upload/download: nessun limite su quella direzione
- se omesso `bandwidth-burst`: default `1.0` (nessun burst aggiuntivo)

## Tabella valori tipici (scenari)

Riferimento pratico per scegliere i limiti iniziali in base al tipo di utenza.

| Scenario | bandwidth-download (Mbps) | bandwidth-upload (Mbps) | bandwidth-burst | Note |
|---|---:|---:|---:|---|
| Test/lab molto limitato | 2 | 1 | 1.0 | Evita saturazione anche su linee lente |
| Utente guest base | 10 | 5 | 1.0 | Navigazione e test HTTP standard |
| Team sviluppo standard | 20 | 10 | 1.2 | Buon compromesso tra fluidita' e controllo |
| CI/CD o webhook moderato | 50 | 20 | 1.2 | Build artifact piccoli/medi |
| Integrazione API intensiva | 100 | 50 | 1.3 | Alto volume ma ancora controllato |
| Servizi critici/prioritari | 200 | 100 | 1.5 | Quasi illimitato in ambienti medi |
| Nessun limite applicato | omesso | omesso | omesso | L'utente resta unlimited |

## Conversione equivalente in Megabyte

Formula rapida:
- `MB/s = Mbps / 8`
- `MB/min = MB/s * 60`

Tabella conversione tipica:

| Mbps | MB/s equivalenti | MB/min equivalenti |
|---:|---:|---:|
| 1 | 0.125 | 7.5 |
| 2 | 0.25 | 15 |
| 5 | 0.625 | 37.5 |
| 10 | 1.25 | 75 |
| 20 | 2.5 | 150 |
| 50 | 6.25 | 375 |
| 100 | 12.5 | 750 |
| 200 | 25 | 1500 |

## Semantica direzioni (utente tunnel)

Per una connessione utente:
- `download`: traffico dalla rete verso il servizio esposto dal client (internet -> client tunnel)
- `upload`: traffico dal servizio del client verso la rete (client tunnel -> internet)

## Ambito applicazione

Ambito scelto:
- per connessione SSH utente (globale sulla connessione)

Impatti:
- se un utente apre piu' forward nella stessa connessione SSH, il limite e' condiviso
- utenti senza campi banda restano senza limitazione
- utenti non provenienti da `auth-users` YAML non sono soggetti a questa policy

## Compatibilita' e non regressione

Comportamento invariato:
- autenticazione password globale (`--authentication-password`)
- autenticazione chiavi globali (`--authentication-keys-directory`)
- callback auth remoti (`--authentication-password-request-url`, `--authentication-key-request-url`)
- instradamento tunnel e gestione console

Comportamento nuovo:
- utenti YAML con campi banda validi possono ricevere limiter solo se `--user-bandwidth-limiter-enabled=true`

## Esempi avvio

### 1) Feature attiva

```bash
./app \
  --authentication=true \
  --auth-users-enabled=true \
  --auth-users-directory=/users \
  --user-bandwidth-limiter-enabled=true
```

### 2) Feature disattiva (campi YAML ignorati)

```bash
./app \
  --authentication=true \
  --auth-users-enabled=true \
  --auth-users-directory=/users \
  --user-bandwidth-limiter-enabled=false
```

## Esempi operativi

### Utente limitato su entrambe le direzioni

```yaml
- name: guest
  password: "guest"
  bandwidth-upload: "10"
  bandwidth-download: "20"
```

### Utente limitato solo in upload

```yaml
- name: pluto
  pubkey: "ssh-ed25519 AAAAC3..."
  bandwidth-upload: "50"
```

### Utente senza limiti

```yaml
- name: paperino
  pubkey: "ssh-ed25519 AAAAC3..."
```

## Validazione e reload

- i file YAML utenti continuano a essere ricaricati a caldo (watch directory)
- i campi banda vengono validati come numeri positivi
- valori invalidi non rompono il processo: viene loggato warning e il profilo banda dell'utente viene ignorato

## Troubleshooting

1. I limiti non sembrano applicati
- verifica `--user-bandwidth-limiter-enabled=true`
- verifica che utente sia definito in `auth-users` YAML
- verifica valori numerici validi nei campi banda

2. Solo una direzione limitata
- se hai definito solo upload o solo download, l'altra direzione resta unlimited

3. Burst inatteso
- se `bandwidth-burst` non e' presente, valore effettivo e' `1.0`

## Verifiche consigliate

- test utenti con e senza limiti
- test utente con password + pubkey + limiti
- compile check:

```bash
go test ./... -run TestDoesNotExist -count=1
```
