# Spec 03 — Census Source Control & Enhanced Census Dashboard

## Overview

Split the legacy `--strict-id-censed` flag into two independent flags that control which census sources are active:
- `--strict-id-censed-url`: controls reading from the remote `--census-url`
- `--strict-id-censed-files`: controls reading from local `--census-directory` files and shows the `editcensus` page

Enhanced the census dashboard to show full visibility into active sources, per-ID source tracking, and operational status.

## New Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--strict-id-censed-url` | `false` | Enable strict census enforcement reading from `--census-url` |
| `--strict-id-censed-files` | `false` | Enable strict census enforcement reading from `--census-directory` and show the editcensus console page |

## Backward Compatibility

The legacy `--strict-id-censed` flag is preserved. When set to `true`, it automatically enables both `--strict-id-censed-url` (if `--census-url` is configured) and `--strict-id-censed-files` (if `--census-directory` is configured). This ensures existing deployments continue to work without changes.

## Census Refresh Logic

`RefreshCensusCache()` now respects the two new flags:

- If `--strict-id-censed-url` is `true` and `--census-url` is configured, IDs are fetched from the remote URL.
- If `--strict-id-censed-files` is `true` and `--census-directory` is configured, IDs are read from all YAML files in that directory.
- Both sources are merged. An ID found in **either** source is considered censed.
- If **neither** flag is enabled, the cache is cleared and no enforcement happens.

## Per-ID Source Tracking

Each census ID now tracks its origin:
- `CensusIDSource.URL` (bool): the ID was found in the remote URL
- `CensusIDSource.Files` ([]string): list of local file names where the ID was found

This information is stored in the cache (`censusCache.IDSources`) and returned by the census API.

## Enhanced Census Dashboard

### Status Panel

The dashboard status panel now shows:

| Field | Description |
|-------|-------------|
| **URL Active/Disabled** | Whether `--strict-id-censed-url` is enabled, with the configured URL |
| **Files Active/Disabled** | Whether `--strict-id-censed-files` is enabled, with directory path and list of loaded files |
| **Last refresh** | Timestamp of last cache refresh |
| **Auto refresh** | Shows "Active" badge and the configured refresh interval |

### Source Column in Tables

The **Proxy Censed** and **Censed Not Forwarded** tables now include a **Source** column showing the origin of each ID:

- `url` badge (blue): ID comes from the remote census URL
- File name badge (cyan): ID comes from a local file — one badge per file

An ID can show both if it appears in both sources.

The **Proxy Uncensed** table is unchanged (these IDs are not in census, so no source applies).

### View Source Button

The "View Source" button is only shown when URL mode is active (`--strict-id-censed-url`).

## editcensus Page Visibility

The `editcensus` nav link is now shown only when ALL of these conditions are met:
- `--census-enabled` is `true`
- `--strict-id-censed-files` is `true`
- `--census-directory` is configured
- `--admin-consolle-editcensus-credentials` is configured

## Files Modified

| File | Change |
|------|--------|
| `cmd/sish.go` | Added `--strict-id-censed-url` and `--strict-id-censed-files` flags |
| `utils/census.go` | Added `CensusIDSource` type, per-ID source tracking in `censusCache`, source-aware `RefreshCensusCache()`, `GetIDSource()` |
| `sshmuxer/sshmuxer.go` | Updated startup warning and legacy flag propagation logic |
| `utils/console.go` | Updated `HandleCensus` to return source info per ID and status fields; updated `ShowEditCensus` condition |
| `templates/census.tmpl` | Added source status panel, Source column in tables, `formatSource()` helper, conditional View Source button |

## Usage Examples

Enable both sources:
```bash
sish --census-enabled \
     --strict-id-censed-url \
     --strict-id-censed-files \
     --census-url="https://pastebin.com/raw/q7syxcH5" \
     --census-directory=/census
```

URL only (files disabled, editcensus hidden):
```bash
sish --census-enabled \
     --strict-id-censed-url \
     --census-url="https://pastebin.com/raw/q7syxcH5"
```

Files only (no remote URL):
```bash
sish --census-enabled \
     --strict-id-censed-files \
     --census-directory=/census \
     --admin-consolle-editcensus-credentials="admin:secret"
```

Legacy mode (backward compatible):
```bash
sish --census-enabled \
     --strict-id-censed \
     --census-url="https://pastebin.com/raw/q7syxcH5" \
     --census-directory=/census
```
