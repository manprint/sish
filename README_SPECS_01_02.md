# Spec 01 & Spec 02 — Development Documentation

## Spec 01: Admin Console — Edit Headers Page

### Overview
Added a new "headers" page to the admin console that allows editing the headers settings YAML configuration file directly from the web UI, without requiring SSH access to the server.

### New Flag
| Flag | Default | Description |
|------|---------|-------------|
| `--admin-consolle-editheaders-credentials` | `""` | Basic auth credentials for the editheaders page (`username:password`) |

### How it works
- The page is accessible at `/_sish/editheaders` when the flag is configured.
- It lists all YAML files found in the `--headers-setting-directory` directory.
- Each file can be viewed (read-only modal) or edited (edit modal with save).
- On save, the YAML content is validated using `ValidateHeaderSettingsConfig()` before writing to disk. Invalid YAML is rejected with a descriptive error.
- A standalone "Validate" button allows checking YAML validity without saving.
- The existing file watcher (`WatchHeadersSettings`) automatically detects the change and reloads the headers configuration — no restart required.
- Protected by Basic Auth, independent from the other console pages.

### Files Modified/Created
| File | Change |
|------|--------|
| `cmd/sish.go` | Added `--admin-consolle-editheaders-credentials` flag |
| `templates/header.tmpl` | Added "headers" nav link with `ShowEditHeaders` condition |
| `templates/editheaders.tmpl` | **New** — page template (view/edit/validate modals, Knockout.js bindings) |
| `utils/console.go` | Added `CheckEditHeadersBasicAuth`, `HandleEditHeadersTemplate`, `HandleEditHeadersFiles`, `HandleEditHeadersFileRead`, `HandleEditHeadersFileWrite`, `HandleEditHeadersValidate`, `resolveEditHeadersFile`, `ShowEditHeaders` in templateData, and route entries |

### Usage Example
```bash
sish --headers-managed \
     --headers-setting-directory=/etc/sish/headers \
     --admin-console \
     --admin-console-token=mytoken \
     --admin-consolle-editheaders-credentials="admin:secret"
```

---

## Spec 02: Census from Local Directory + Edit Census Page

### Overview
Extended the census system to support reading census entries from local YAML files in addition to the remote `--census-url`. Added a new "editcensus" admin console page for managing local census files.

### New Flags
| Flag | Default | Description |
|------|---------|-------------|
| `--census-directory` | `""` | Local directory containing YAML census files. Entries are merged with `--census-url` results |
| `--admin-consolle-editcensus-credentials` | `""` | Basic auth credentials for the editcensus page (`username:password`) |

### Census Merge Logic
- Both `--census-url` (remote) and `--census-directory` (local) are evaluated during each refresh cycle.
- IDs from both sources are **merged and deduplicated**. If an ID appears in either source, it is considered censed.
- At least one of the two sources must be configured when `--census-enabled` is true.
- If one source fails (e.g., network error on census-url) but the other succeeds, the successfully loaded IDs are still cached. Errors are recorded in `LastError`.
- All existing logic controlled by `--census-enabled` and `--strict-id-censed` continues to work unchanged — `IsIDCensed()` and `IsStrictIDCensedEnabled()` use the merged cache.
- The census refresh timer (`--census-refresh-time`) re-reads both sources on each tick.

### Census Directory File Format
Files in `--census-directory` must be YAML (`.yaml` or `.yml`) with the same format as `--census-url`:

```yaml
- id: "my-tunnel-id"
- id: "another-id"
```

Multiple files are supported — all entries across all files are merged.

### Edit Census Console Page
- Accessible at `/_sish/editcensus` when `--census-enabled`, `--census-directory`, and `--admin-consolle-editcensus-credentials` are all configured.
- Same UI pattern as editheaders/editusers: list files, view, edit, validate, save.
- YAML validation ensures the content is a valid census entry list before saving.
- After saving, `RefreshCensusCache()` is called immediately so changes take effect without waiting for the next refresh cycle.
- Protected by independent Basic Auth.

### Files Modified/Created
| File | Change |
|------|--------|
| `cmd/sish.go` | Added `--census-directory` and `--admin-consolle-editcensus-credentials` flags |
| `utils/census.go` | Added `loadCensusDirectoryIDs()`, `mergeIDSets()`, `ValidateCensusYAML()`; refactored `RefreshCensusCache()` to merge URL + directory sources |
| `templates/header.tmpl` | Added "editcensus" nav link with `ShowEditCensus` condition |
| `templates/editcensus.tmpl` | **New** — page template (view/edit/validate modals, Knockout.js bindings) |
| `utils/console.go` | Added `CheckEditCensusBasicAuth`, `HandleEditCensusTemplate`, `HandleEditCensusFiles`, `HandleEditCensusFileRead`, `HandleEditCensusFileWrite`, `HandleEditCensusValidate`, `resolveEditCensusFile`, `ShowEditCensus` in templateData, and route entries |

### Usage Example
```bash
sish --census-enabled \
     --census-url="https://pastebin.com/raw/q7syxcH5" \
     --census-directory=/census \
     --strict-id-censed \
     --admin-console \
     --admin-console-token=mytoken \
     --admin-consolle-editcensus-credentials="admin:secret"
```

Using only local directory (no remote URL):
```bash
sish --census-enabled \
     --census-directory=/census \
     --admin-console \
     --admin-console-token=mytoken \
     --admin-consolle-editcensus-credentials="admin:secret"
```
