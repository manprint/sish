# Console Features: Clients, History, and Conditional Pages

This document describes dashboard features in the admin console, including
feature-gated page visibility in navbar and routes page.

Main topics:

1. Client table enhancements
2. Dedicated history page
3. Audit page (origin IP stats + bandwidth snapshot)
4. Logs page for forwarders (tail/search/download)
5. Conditional visibility matrix for `history`, `census`, `audit`, `logs`, `editkeys`, `editusers`, `editheaders`, `editcensus`, `internal`

It also includes practical usage examples for `ssh` and `autossh` commands that populate dashboard metadata.

## Scope

The features are available in the admin dashboard with admin token:

- Clients page: `/_sish/console?x-authorization=<admin-token>`
- History page: `/_sish/history?x-authorization=<admin-token>`
- Audit page: `/_sish/audit?x-authorization=<admin-token>`
- Logs page: `/_sish/logs?x-authorization=<admin-token>` (only when `forwarders-log=enable`)
- Census page: `/_sish/census?x-authorization=<admin-token>` (only when `census-enabled=true`)
- Internal page: `/_sish/internal?x-authorization=<admin-token>` (only when `show-internal-state=true`)
- Internal Prometheus export: `/_sish/api/internal/metrics?x-authorization=<admin-token>` (only when `show-internal-state=true`)
- Edit Keys: `/_sish/editkeys` (Basic Auth via `--admin-consolle-editkeys-credentials`)
- Edit Users: `/_sish/editusers` (Basic Auth via `--admin-consolle-editusers-credentials`)
- Edit Headers: `/_sish/editheaders` (Basic Auth via `--admin-consolle-editheaders-credentials`)
- Edit Census: `/_sish/editcensus` (Basic Auth via `--admin-consolle-editcensus-credentials`)

Visibility of navbar links and some UI columns depends on startup flags.
See the matrix section below.

On the clients table:

- **ID**: client-provided or auto-generated connection ID
- **Connection Stats**: start time + live duration
- **Notes**: user-provided note set at tunnel startup
- **Session/Fingerprint compact view**: ellipsis + tooltip + copy action

---

## 1) Client table enhancements

### Connection ID

Each client row now includes an `ID` column.

ID can be provided at startup:

- `id=<value>`
- `SISH_ID=<value>` with `-o SendEnv=SISH_ID`

Validation:

- max 50 characters
- no spaces
- allowed chars: `A-Z a-z 0-9 . _ -`

If omitted, server generates: `rand-xxxxxxxx`.

### Connection Stats

### What it shows

In the Clients table, a **Connection Stats** button is displayed for each SSH client.

- Button text: live duration in format `dd:hh:mm:ss`
- Tooltip (Bootstrap):
  - `Started: <formatted timestamp>`
  - `Duration: <dd:hh:mm:ss>`

### Data source

Stats are derived from the SSH connection start timestamp stored server-side when the SSH session is established.

### UI behavior

- Duration updates every second.
- Uses existing dashboard styles/components (Bootstrap button + tooltip).

---

### Connection Notes

### What it shows

In the Clients table, a **Notes** button is displayed for each SSH client.

On click, a Bootstrap modal opens and shows the note text associated with that connection.

If no note is set, the modal shows:

- `No notes provided.`

### Accepted startup parameters

Two note input modes are supported:

- `note="plain text note"`
- `note64="<base64-encoded note>"`

Additionally, for convenience:

- If `note=` receives a base64 string, the server auto-detects and decodes it.

---

### Compact Session and Fingerprint cells

Long values are compacted in the table for readability.

- full value available in tooltip
- copy button available directly in row

---

## 2) History page

The history page shows completed connection sessions tracked in memory:

- ID
- client remote address
- username
- ingress (SSH or Multiplexer with port)
- started
- ended
- duration (`dd:hh:mm:ss`)
- transfer (`IN x.y MB / OUT x.y MB`)

Features:

- pagination with configurable page size
- search by ID, remote address, username, ingress, dates (min 2 characters, case-insensitive)
- CSV export with Download button (includes all columns)
- Clear all history with confirmation
- history is in-memory and resets on process restart

---

## Usage Examples

## A) Plain text note (single line)

### SSH

```bash
ssh -p 443 -R aaaaaa:80:localhost:8004 sish.mydomain.link 'note=started from local workstation'
```

### autossh

```bash
autossh -M 0 -p 443 -R aaaaaa:80:localhost:8004 sish.mydomain.link 'note=started from local workstation'
```

---

## B) Plain text note with spaces

### SSH

```bash
ssh -p 443 -R aaaaaa:80:localhost:8004 sish.mydomain.link 'note=This tunnel is for staging API tests'
```

### autossh

```bash
autossh -M 0 -p 443 -R aaaaaa:80:localhost:8004 sish.mydomain.link 'note=This tunnel is for staging API tests'
```

Notes:

- Quote the whole `note=...` argument.
- Server-side parsing supports multi-word notes robustly.

---

## C) Note loaded from file (recommended for multiline)

Use `note64` to preserve exact content, including newlines.

### Linux

```bash
note64=$(base64 -w0 notes.txt)
autossh -M 0 -p 443 -R aaaaaa:80:localhost:8004 sish.mydomain.link "note64=$note64"
```

### SSH equivalent

```bash
note64=$(base64 -w0 notes.txt)
ssh -p 443 -R aaaaaa:80:localhost:8004 sish.mydomain.link "note64=$note64"
```

Why this is recommended:

- Preserves multiline text exactly.
- Avoids shell/exec payload normalization issues.

---

## D) Convenience mode: base64 passed via `note=`

If you already have a base64 value in `note64`, this also works:

```bash
note64=$(base64 -w0 notes.txt)
autossh -M 0 -p 443 -R aaaaaa:80:localhost:8004 sish.mydomain.link "note=$note64"
```

Server behavior:

- Auto-detects that `note=` is base64.
- Decodes and stores the decoded note text.

---

## E) SSH standard port vs HTTPS port

All note examples work on either port depending on server config:

- Standard SSH ingress:
  - `-p 2222`
- HTTPS multiplexed SSH ingress (if enabled):
  - `-p 443`

Example:

```bash
autossh -M 0 -p 2222 -R aaaaaa:80:localhost:8004 sish.mydomain.link 'note=local test'
autossh -M 0 -p 443  -R aaaaaa:80:localhost:8004 sish.mydomain.link 'note=local test'
```

---

## Multiline Notes and Newlines

If you need to preserve line breaks from a file, use `note64`.

- `note="$(cat notes.txt)"` may not preserve all newline semantics in every shell/SSH payload case.
- `note64` is stable and exact for multiline text.

---

## Dashboard Summary

In the admin Clients table:

- **ID** column:
  - client-set (`id` / `SISH_ID`) or auto-generated

- **Connection Stats** button:
  - text: live `dd:hh:mm:ss`
  - tooltip: started timestamp + duration
- **Notes** button:
  - opens modal with full note text

In the admin History page:

- read-only in-memory list of finished connections
- duration formatted as `dd:hh:mm:ss`
- transfer column with in/out MB summary
- ingress column showing connection type (SSH/Multiplexer) and port
- CSV export available from page button (`Download`)
- search, pagination, clear all

These features are read-only dashboard aids and do not alter tunnel routing behavior.

---

## 3) Audit page

La pagina audit e` dedicata a visibilita` operativa e sicurezza, con refresh manuale.

Route:
- `GET /_sish/audit`
- `GET /_sish/api/audit`

Sezioni disponibili:
- Bandwidth snapshot aggregato di tutti i forwarder
- Origin IP stats (attempts/success/rejected/reasons/last seen/country)
- Ingress evidence per Origin IP (SSH standard vs Multiplexer, con porta osservata)

Dettaglio metriche bandwidth:
- `Total Upload` usa i byte `DataInBytes`
- `Total Download` usa i byte `DataOutBytes`

Nota importante:
- risolto bug di inversione upload/download nella snapshot audit; ora i valori visualizzati sono allineati al verso reale del traffico.

---

## 4) Logs page (forwarder logs)

La pagina logs fornisce consultazione operativa dei file per-forwarder.

Route:
- `GET /_sish/logs`
- `GET /_sish/api/logs/files`
- `GET /_sish/api/logs/file?file=<relpath>&lines=<n>`
- `GET /_sish/api/logs/download?file=<relpath>`

Funzionalita`:
- lista file in `forwarders-log-dir`
- tail con default `100` righe
- massimo `5000` righe per richiesta
- ricerca testuale lato browser sul contenuto gia` caricato
- download del file completo

Sicurezza:
- accesso solo admin console su root host
- path traversal bloccato tramite risoluzione path confinata in `forwarders-log-dir`

Leggibilita` log:
- le sequenze ANSI (colori terminale) vengono rimosse automaticamente
- la rimozione avviene sia in scrittura dei nuovi log sia nella lettura API per rendere leggibili anche contenuti storici nella pagina web

---

## 5) Conditional visibility matrix (frontend + console routes)

The admin frontend now hides or shows entries based on runtime configuration,
to avoid exposing pages that are disabled server-side.

### Rules by feature

1. `census`
- Flag: `--census-enabled=true|false`
- If `true`:
  - navbar shows `census`
  - page/API are available for admin on root host
  - `CID` column is visible in `/_sish/console`
- If `false`:
  - navbar hides `census`
  - census routes are not available
  - `CID` column is hidden in `/_sish/console`

2. `editkeys`
- Flag: `--admin-consolle-editkeys-credentials="user:pass"`
- Visibility condition:
  - credentials must be syntactically valid (`user` and `pass` both non-empty)
- If invalid/missing:
  - navbar hides `editkeys`
  - page remains inaccessible

3. `editusers`
- Flag: `--admin-consolle-editusers-credentials="user:pass"`
- Visibility condition:
  - credentials must be syntactically valid (`user` and `pass` both non-empty)
- If invalid/missing:
  - navbar hides `editusers`
  - page remains inaccessible

4. `history`
- Flag: `--history-enabled=true|false` (default: `true`)
- If `true`:
  - navbar shows `history`
  - page/API are available
- If `false`:
  - navbar hides `history`
  - history routes/API are disabled

- Flag implicito: sempre disponibile per admin su root host
- If admin token valido:
  - navbar shows `audit`
  - page/API are available

5. `logs`
- Flag: `--forwarders-log=enable|disable`
- If `enable`:
  - navbar shows `logs`
  - page/API are available
- If `disable`:
  - navbar hides `logs`
  - logs routes are not available

6. `editheaders`
- Flag: `--admin-consolle-editheaders-credentials="user:pass"`
- Visibility condition:
  - credentials must be syntactically valid (`user` and `pass` both non-empty)
  - `--headers-setting-directory` must be configured
- If invalid/missing:
  - navbar hides `editheaders`
  - page remains inaccessible

7. `editcensus`
- Flags: `--admin-consolle-editcensus-credentials="user:pass"`, `--census-enabled=true`, `--strict-id-censed-files=true`, `--census-directory` configured
- Visibility condition:
  - all four conditions must be met
- If any missing:
  - navbar hides `editcensus`
  - page remains inaccessible

8. `internal`
- Flag: `--show-internal-state=true|false` (default: `false`)
- If `true`:
  - navbar shows `internal`
  - page/API are available
  - Prometheus text export is available on `/_sish/api/internal/metrics`
- If `false`:
  - navbar hides `internal`
  - internal routes are not available

### Matrix (admin su root host)

| Feature | Condition | Navbar | Page/API | Extra UI impact |
|---|---|---|---|---|
| `audit` | admin token valido | shown | enabled | origin IP + bandwidth snapshot |
| `history` | `history-enabled=true` | shown | enabled | none |
| `history` | `history-enabled=false` | hidden | disabled | none |
| `logs` | `forwarders-log=enable` | shown | enabled | tail/search/download |
| `logs` | `forwarders-log=disable` | hidden | disabled | none |
| `census` | `census-enabled=true` | shown | enabled | `CID` shown |
| `census` | `census-enabled=false` | hidden | disabled | `CID` hidden |
| `editkeys` | valid `admin-consolle-editkeys-credentials` | shown | enabled (with Basic Auth) | none |
| `editkeys` | invalid/empty credentials | hidden | disabled | none |
| `editusers` | valid `admin-consolle-editusers-credentials` | shown | enabled (with Basic Auth) | none |
| `editusers` | invalid/empty credentials | hidden | disabled | none |
| `editheaders` | valid `admin-consolle-editheaders-credentials` + `headers-setting-directory` | shown | enabled (with Basic Auth) | none |
| `editheaders` | invalid/empty credentials | hidden | disabled | none |
| `editcensus` | `census-enabled` + `strict-id-censed-files` + `census-directory` + valid credentials | shown | enabled (with Basic Auth) | none |
| `editcensus` | any condition missing | hidden | disabled | none |
| `internal` | `show-internal-state=true` | shown | enabled | runtime status page |
| `internal` | `show-internal-state=false` | hidden | disabled | none |

### Recommended startup example

```bash
go run main.go \
  --admin-console=true \
  --admin-console-token='admin-token' \
  --history-enabled=true \
  --census-enabled=true \
  --show-internal-state=true \
  --admin-consolle-editkeys-credentials='editkeys:strongpass' \
  --admin-consolle-editusers-credentials='editusers:strongpass' \
  --admin-consolle-editheaders-credentials='editheaders:strongpass' \
  --admin-consolle-editcensus-credentials='editcensus:strongpass'
```

---

## 4) UI Changelog (2026-03-10 / 2026-03-11)

This section summarizes the latest frontend-console behavior changes and fixes.

### Sish page (`/_sish/console`)

1. Connection transfer stats in tooltip
- Connection Stats tooltip now includes:
  - `DATA IN: x.y MB`
  - `DATA OUT: x.y MB`

2. Live updates without page refresh
- Transfer values are refreshed automatically.
- Clients/listeners table is refreshed automatically every second.

3. New `Info` column
- Added after `SSH Version`.
- Opens a modal with:
  - `INGRESS`: connection type (SSH or Multiplexer) and port
  - `SEZIONE CLIENT`: connection-level runtime parameters
  - `SEZIONE CONFIG`: auth-users YAML parameters
- Sensitive fields are masked:
  - `password` -> `REDACTED`
  - `pubkey` -> `REDACTED`

4. Tooltip stability fixes under polling
- Tooltip no longer remains stuck after mouse leave.
- Tooltip no longer closes forcibly every second while hovering.
- Native one-line browser tooltip flicker removed (Bootstrap tooltip only).

5. Polling robustness
- Added anti-overlap guard: if one poll request is still running, the next tick is skipped.

6. Disconnect confirmation
- When clicking `Disconnect`, a confirmation modal appears asking the user to confirm the action.
- Buttons: `Disconnect` (proceed) and `Cancel` (abort).
- This prevents accidental disconnections.

### History page (`/_sish/history`)

1. Transfer column
- Added `Transfer` column per row with in/out MB summary.
- CSV download also includes `Transfer`.

### Census page (`/_sish/census`)

1. Language cleanup
- Italian description strings translated to English.

2. Red banner flash fix on browser refresh
- Fixed transient red error banner flash at initial render.
- Error alert now appears only after first load attempt and only when an actual error is present.

### Quick validation checklist

1. Hover Connection Stats for >3s while polling runs: tooltip stays stable and closes correctly on mouse leave.
2. Keep mouse over Connection Stats: no extra native one-line tooltip appears.
3. Open `Info` modal: INGRESS/CLIENT/CONFIG data visible, secrets masked.
4. Add/remove listeners from an active tunnel: `sish` table updates without manual browser refresh.
5. Refresh `census` from browser: no transient red error flash.
