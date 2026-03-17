# API Reference

## POST /api/insertuser

Inserisce un nuovo utente SSH nel file `fromapi.yml` all'interno della directory configurata con `--auth-users-directory`.

### Prerequisiti

- `--auth-users-enabled` deve essere `true`
- `--auth-users-directory` deve essere configurata
- `--admin-consolle-editusers-credentials` deve essere configurata (usata per Basic Auth)

### Autenticazione

Basic Auth con le credenziali configurate in `--admin-consolle-editusers-credentials`.

### Parametri

| Parametro | Obbligatorio | Descrizione |
|-----------|:---:|-------------|
| `name` | Si | Username dell'utente |
| `password` | Condizionale | Password dell'utente. Obbligatoria se `pubkey` non viene fornita |
| `pubkey` | Condizionale | Chiave pubblica SSH (es. `ssh-ed25519 AAAA... comment` oppure `ssh-rsa AAAA... comment`). Obbligatoria se `password` non viene fornita |
| `comment` | No | Commento inserito come `# commento` nel file YAML. Accettato anche via header `x-api-comment` o query string `?comment=...` |
| `bandwidth-upload` | No | Limite upload in Mbps (valore numerico, es. `1` = 1 Mbps, `0.5` = 500 Kbps) |
| `bandwidth-download` | No | Limite download in Mbps (valore numerico) |
| `bandwidth-burst` | No | Fattore di burst bandwidth (valore numerico, es. `1.5`) |
| `allowed-forwarder` | No | Restrizioni di forwarding: subdomains, porte o alias separati da virgola |

> **Regola credenziali**: e richiesta almeno una tra `password` e `pubkey`. Sono accettate entrambe contemporaneamente, solo una delle due, ma non nessuna.

### Content-Type supportati

L'API accetta sia `application/json` che `application/x-www-form-urlencoded`.

---

### Esempi curl

#### 1. JSON — solo password (senza pubkey)

```bash
curl -X POST -u admin:password \
  -H "Content-Type: application/json" \
  -d '{"name": "mario", "password": "secret123"}' \
  https://domain/api/insertuser
```

#### 2. JSON — solo pubkey (senza password)

```bash
curl -X POST -u admin:password \
  -H "Content-Type: application/json" \
  -d '{
    "name": "mario",
    "pubkey": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILZedGPk3ZP7bUR+39XVmluS2uBJwkUt6CGXDkcKLPyS mario@server-01"
  }' \
  https://domain/api/insertuser
```

#### 3. JSON — password + pubkey

```bash
curl -X POST -u admin:password \
  -H "Content-Type: application/json" \
  -d '{
    "name": "mario",
    "password": "secret123",
    "pubkey": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILZedGPk3ZP7bUR+39XVmluS2uBJwkUt6CGXDkcKLPyS mario@server-01"
  }' \
  https://domain/api/insertuser
```

#### 4. JSON — tutti i campi

```bash
curl -X POST -u admin:password \
  -H "Content-Type: application/json" \
  -H "x-api-comment: creato da script di provisioning" \
  -d '{
    "name": "mario",
    "password": "secret123",
    "pubkey": "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILZedGPk3ZP7bUR+39XVmluS2uBJwkUt6CGXDkcKLPyS mario@server-01",
    "bandwidth-upload": "1",
    "bandwidth-download": "2",
    "bandwidth-burst": "1.5",
    "allowed-forwarder": "app1.example.com,app2.example.com"
  }' \
  https://domain/api/insertuser
```

#### 5. JSON — con chiave RSA (senza password)

```bash
curl -X POST -u admin:password \
  -H "Content-Type: application/json" \
  -d '{
    "name": "fabio",
    "pubkey": "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAABgQCVQhvD8Sz... fabio@workstation"
  }' \
  https://domain/api/insertuser
```

#### 6. Form — solo password

```bash
curl -X POST -u admin:password \
  -d "name=mario" \
  -d "password=secret123" \
  https://domain/api/insertuser
```

#### 7. Form — solo pubkey (usare --data-urlencode)

```bash
curl -X POST -u admin:password \
  -d "name=mario" \
  --data-urlencode "pubkey=ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILZedGPk3ZP7bUR+39XVmluS2uBJwkUt6CGXDkcKLPyS mario@server-01" \
  https://domain/api/insertuser
```

#### 8. Form — password + pubkey

```bash
curl -X POST -u admin:password \
  -d "name=mario" \
  -d "password=secret123" \
  --data-urlencode "pubkey=ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILZedGPk3ZP7bUR+39XVmluS2uBJwkUt6CGXDkcKLPyS mario@server-01" \
  -H "x-api-comment: inserito manualmente" \
  https://domain/api/insertuser
```

> **Importante**: per il campo `pubkey` in modalita form, usare sempre `--data-urlencode` al posto di `-d`. Le chiavi SSH base64 contengono il carattere `+` che in URL encoding viene interpretato come spazio, corrompendo la chiave.

#### 9. Form — tutti i campi

```bash
curl -X POST -u admin:password \
  -d "name=mario" \
  -d "password=secret123" \
  --data-urlencode "pubkey=ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILZedGPk3ZP7bUR+39XVmluS2uBJwkUt6CGXDkcKLPyS mario@server-01" \
  -d "bandwidth-upload=1" \
  -d "bandwidth-download=2" \
  -d "bandwidth-burst=1.5" \
  -d "allowed-forwarder=app1.example.com,app2.example.com" \
  -H "x-api-comment: provisioning completo" \
  https://domain/api/insertuser
```

#### 10. Form — comment via query string

```bash
curl -X POST -u admin:password \
  -d "name=mario" \
  -d "password=secret123" \
  "https://domain/api/insertuser?comment=inserito+da+script"
```

### Risposte

#### Successo — utente inserito

```json
{"status": true, "inserted": true, "message": "user inserted", "file": "fromapi.yml"}
```

#### Successo — utente gia presente

```json
{"status": true, "inserted": false, "message": "user already present", "file": "fromapi.yml"}
```

#### Errore — name mancante

```json
{"status": false, "message": "name is required"}
```

#### Errore — nessuna credenziale

```json
{"status": false, "message": "at least one of password or pubkey is required"}
```

#### Errore — validazione fallita

```json
{"status": false, "message": "validation error: invalid pubkey for user mario: ssh: no key found"}
```

```json
{"status": false, "message": "validation error: invalid bandwidth-upload: must be a positive numeric string"}
```

### Note sui valori di bandwidth

I campi `bandwidth-upload` e `bandwidth-download` accettano un **numero in Mbps**:

| Valore | Significato |
|--------|------------|
| `"0.5"` | 500 Kbps |
| `"1"` | 1 Mbps |
| `"10"` | 10 Mbps |
| `"100"` | 100 Mbps |

Il campo `bandwidth-burst` e un moltiplicatore (default `1.0` se non specificato).

### Priorita del comment

Il campo `comment` viene letto in ordine di priorita:

1. Header `x-api-comment` (massima priorita)
2. Campo `comment` nel body (JSON o form)
3. Query string `?comment=...`

---

## POST /api/insertkey

Inserisce una chiave pubblica SSH nel file `fromapi.key` all'interno della directory configurata con `--authentication-keys-directory`.

### Prerequisiti

- `--authentication-keys-directory` deve essere configurata
- `--admin-consolle-editkeys-credentials` deve essere configurata (usata per Basic Auth)

### Autenticazione

Basic Auth con le credenziali configurate in `--admin-consolle-editkeys-credentials`.

### Body

Il body della request deve contenere la chiave pubblica in formato OpenSSH authorized_keys (testo raw, non JSON/form).

### Parametri opzionali

| Parametro | Dove | Descrizione |
|-----------|------|-------------|
| `comment` | Header `x-api-comment` oppure query string `?comment=...` | Commento inserito come `# commento` nel file |

### Esempi curl

#### Inserimento chiave ed25519

```bash
curl -X POST -u admin:password \
  -H "x-api-comment: chiave server di produzione" \
  -d "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILZedGPk3ZP7bUR+39XVmluS2uBJwkUt6CGXDkcKLPyS user@host" \
  https://domain/api/insertkey
```

#### Inserimento chiave RSA

```bash
curl -X POST -u admin:password \
  -H "x-api-comment: chiave dev team" \
  -d "ssh-rsa AAAAB3NzaC1yc2EAAAADAQABAAAB... user@host" \
  https://domain/api/insertkey
```

#### Inserimento da file

```bash
curl -X POST -u admin:password \
  -H "x-api-comment: chiave da file" \
  --data-binary @~/.ssh/id_ed25519.pub \
  https://domain/api/insertkey
```

### Risposte

#### Successo — chiave inserita

```json
{"status": true, "inserted": true, "message": "key inserted", "file": "fromapi.key"}
```

#### Successo — chiave gia presente

```json
{"status": true, "inserted": false, "message": "key already present", "file": "fromapi.key"}
```

#### Errore — chiave invalida

HTTP 400 — `invalid public key`
