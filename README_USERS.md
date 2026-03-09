# SSH Auth Users (YAML Directory)

Questo documento descrive la funzionalita' di autenticazione SSH con utenti/password per-utente caricati da file YAML, con reload automatico senza riavvio.

## Obiettivo

Permettere login SSH con credenziali specifiche per utente, ad esempio:

- `alpha` con la sua password
- `beta` con la sua password
- `gamma` con la sua password

Il tutto mantenendo invariato il comportamento storico di `--authentication-password`.

## Flag server

Per abilitare la feature:

- `--auth-users-enabled=true`
- `--auth-users-directory=/users`

Opzionale:

- `--auth-users-directory-watch-interval=200ms`

Esempio completo:

```bash
./app \
  --authentication=true \
  --auth-users-enabled=true \
  --auth-users-directory=/users
```

## Formato YAML supportato

Ogni file `.yml` o `.yaml` nella directory configurata viene letto.

Formato:

```yaml
users:
  - name: alpha
    password: "Xk9#mP2$vL7qR4"

  - name: beta
    password: "Wd5@nJ8!hF3yT6"

  - name: gamma
    password: "Bz1&kQ9*cN6eU2"
```

Regole di parsing:

- chiave root: `users`
- ogni elemento richiede `name` e `password`
- utenti con `name` vuoto vengono ignorati
- file non YAML vengono ignorati

## Hot reload automatico

La directory `--auth-users-directory` viene osservata in continuo.

Cosa succede quando modifichi i file:

- aggiungi utente: autenticazione subito disponibile
- cambi password: nuova password valida subito
- rimuovi utente/file: autenticazione subito revocata

Non serve riavviare il processo.

## Precedenza autenticazione password

Nel callback SSH password, l'ordine e':

1. `--authentication-password` (globale, comportamento storico invariato)
2. utenti YAML (`--auth-users-*`)
3. `--authentication-password-request-url` (callback HTTP remota)

Questo garantisce compatibilita' con installazioni esistenti.

## Compatibilita' con comportamento esistente

Comportamento invariato:

- `--authentication-password` continua a funzionare identico a prima
- `--authentication-password-request-url` continua a funzionare identico a prima
- se `--authentication=false`, SSH resta senza auth (come prima)

Comportamento nuovo:

- anche con `--authentication-password=""`, il `PasswordCallback` resta attivo se `--auth-users-enabled=true`

## Casi d'uso

## Caso 1: Team con credenziali separate

Scenario:

- piu' operatori accedono allo stesso server sish
- vuoi credenziali distinte per audit e revoca selettiva

Configurazione:

```bash
./app --auth-users-enabled=true --auth-users-directory=/users
```

Connessioni:

```bash
ssh -p 443 -R nginx:80:localhost:8080 alpha@tuns.aaa.xyz
ssh -p 443 -R nginx:80:localhost:8080 beta@tuns.aaa.xyz
ssh -p 443 -R nginx:80:localhost:8080 gamma@tuns.aaa.xyz
```

Vantaggio:

- puoi disabilitare solo un utente rimuovendolo dal YAML

## Caso 2: Rotazione password senza downtime

Scenario:

- devi ruotare una password in produzione
- non vuoi fermare i tunnel in corso

Passi:

1. aggiorna `password` dell'utente nel file YAML
2. salva il file
3. i nuovi login useranno subito la nuova password

Note:

- le sessioni SSH gia' aperte restano attive finche' non vengono chiuse

## Caso 3: Onboarding rapido nuovo operatore

Scenario:

- nuovo utente da abilitare subito

Passi:

1. aggiungi blocco `- name/password` nel YAML
2. salva
3. il nuovo utente puo' autenticarsi immediatamente

## Caso 4: Offboarding immediato

Scenario:

- devi revocare accesso in tempi rapidi

Passi:

1. rimuovi utente dal YAML (o elimina il file dedicato)
2. salva
3. nuovi tentativi login falliscono subito

## Uso con Docker

Esempio:

```bash
docker run --rm -it \
  -p 443:443 -p 80:80 \
  -v /srv/sish/users:/users:ro \
  -e TZ=Europe/Rome \
  fabiop85/sish:dev \
  --ssh-address=:443 \
  --http-address=:80 \
  --auth-users-enabled=true \
  --auth-users-directory=/users
```

Nota:

- `TZ` e' opzionale ma supportato nell'immagine finale

## Uso con docker-compose

```yaml
services:
  sish:
    image: fabiop85/sish:dev
    ports:
      - "443:443"
      - "80:80"
    environment:
      - TZ=Europe/Rome
    volumes:
      - /srv/sish/users:/users:ro
    command:
      - --ssh-address=:443
      - --http-address=:80
      - --auth-users-enabled=true
      - --auth-users-directory=/users
```

## Strutturazione directory consigliata

Esempio con piu' file:

- `/users/team-a.yml`
- `/users/team-b.yml`
- `/users/emergency.yml`

Questo aiuta a separare ownership e processi di aggiornamento.

## Note operative e sicurezza

- Le password nel YAML sono plaintext: limita accessi filesystem e permessi file.
- Usa volumi in sola lettura (`:ro`) nei container quando possibile.
- Evita commit dei file utenti in repository Git pubblici.
- Per segreti ad alta criticita', valuta integrazione con provider esterno tramite `--authentication-password-request-url`.

## Troubleshooting

Se un utente non autentica:

1. verifica `--auth-users-enabled=true`
2. verifica path `--auth-users-directory`
3. verifica estensione file `.yml`/`.yaml`
4. verifica sintassi YAML (`users:` + `name/password`)
5. verifica username usato nel comando SSH (`user@host`)
6. controlla log server per errori di parsing del file

## Riepilogo

Con `auth-users` ottieni:

- credenziali SSH per-utente
- aggiornamenti runtime senza restart
- compatibilita' con i meccanismi auth password gia' esistenti
