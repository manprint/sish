# Console Features: Connection Stats and Notes

This document describes the dashboard features added to the admin console:

1. Connection Stats
2. Connection Notes

It also includes practical usage examples for `ssh` and `autossh`.

## Scope

The features are available in the admin dashboard (`/_sish/console` with admin token) and are shown in the **Clients** table.

- **Connection Stats**: start time + live duration
- **Notes**: user-provided note set at tunnel startup

---

## 1) Connection Stats

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

## 2) Connection Notes

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

- **Connection Stats** button:
  - text: live `dd:hh:mm:ss`
  - tooltip: started timestamp + duration
- **Notes** button:
  - opens modal with full note text

These features are read-only dashboard aids and do not alter tunnel routing behavior.
