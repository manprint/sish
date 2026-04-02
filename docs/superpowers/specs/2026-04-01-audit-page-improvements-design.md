# Audit Page Improvements — Design Spec

**Date:** 2026-04-01
**Status:** Implemented
**File modified:** `templates/audit.tmpl`
**Backend changes:** none

---

## Overview

Three UI improvements to the `/_sish/audit` admin console page. All changes are purely frontend (KnockoutJS + Bootstrap). The Go backend (`utils/console.go`, `/_sish/api/audit` endpoint) is unchanged.

---

## Feature 1 — Bandwidth Totals

### What changed

Two new rows added to the **Bandwidth** table, after the existing Upload and Download rows:

| Row | Formula |
|-----|---------|
| Total Upload/Download | `totalUploadBytes + totalDownloadBytes` |
| Total Sish Bandwidth Consumed | `round((upload + download) * 2.1)` |

Both values are formatted as `X.X MB (Y bytes)`, consistent with the existing rows.

### Implementation

Two new `ko.computed` observables added to `auditView`:

```js
auditView.totalCombinedBytes = ko.computed(function() {
    return auditView.totalUploadBytes() + auditView.totalDownloadBytes();
});

auditView.totalSishBytes = ko.computed(function() {
    return Math.round(auditView.totalCombinedBytes() * 2.1);
});
```

A module-level helper `auditFormatBytesLabel(bytes)` formats bytes to `X.X MB (Y bytes)`. The two new label computeds use this helper.

The multiplier `2.1` accounts for sish protocol overhead (bidirectional relay traffic).

---

## Feature 2 — Green Highlight + Sort for Successful IPs

### What changed

- Rows in **Origin IP Stats** where `success > 0` are highlighted with Bootstrap's `table-success` class (light green background).
- These rows are sorted to the top of the list. Within each group (success / no-success) the original server-side order is preserved (stable sort).

### Implementation

Each row object built in `refresh()` gains a plain boolean property:

```js
isSuccess: successCount > 0
```

The `<tr>` in the template uses a KO `css` binding:

```html
<tr data-bind="css: { 'table-success': isSuccess }">
```

After building all rows, a single `Array.sort` separates the two groups:

```js
builtRows.sort(function(a, b) {
    var aSuccess = a.isSuccess ? 0 : 1;
    var bSuccess = b.isSuccess ? 0 : 1;
    return aSuccess - bSuccess;
});
```

The sort runs once per `refresh`, before handing the array to the pagination observable.

---

## Feature 3 — Client-side Pagination (30 rows/page)

### What changed

The **Origin IP Stats** table is now paginated. A Previous / label / Next control bar appears below the table, styled identically to the History page pagination.

Page size: **30 rows**.

### Implementation

The previous `originIPStats: ko.observableArray([])` pattern (push per row) is replaced with:

| Observable | Role |
|---|---|
| `allOriginIPStats` | `ko.observableArray` — full sorted dataset, replaced atomically on each refresh |
| `originPage` | `ko.observable(1)` — current page, reset to 1 on refresh |
| `originPageSize` | plain constant `30` |
| `originTotal` | `ko.computed` — `allOriginIPStats().length` |
| `originTotalPages` | `ko.computed` — `max(1, ceil(total / pageSize))` |
| `pagedOriginIPStats` | `ko.computed` — slice of `allOriginIPStats` for current page |
| `originPaginationLabel` | `ko.computed` — `"Page X of Y (Z entries)"` |

`pagedOriginIPStats` is what the template `foreach` binding consumes. Row objects (including their `showLastRejectReason` / `showRejectReasonsSummary` closures) are untouched — they move with the rows.

`refresh()` now builds the entire row array locally (`builtRows`), sorts it, then calls `allOriginIPStats(builtRows)` and `originPage(1)` in one shot. This avoids intermediate KO notifications for individual row pushes.

---

## Regression considerations

- The existing modal (`#auditReasonModal`) and its `show`/`escapeHtml` helpers are unchanged.
- `totalUploadLabel` / `totalDownloadLabel` computeds are unchanged.
- The `x-authorization` token forwarding (`getToken`, `apiUrl`) is unchanged.
- The `loading` observable and `refresh` error path (`.always`) are unchanged.
- The `hasLastRejectReason` / `hasRejectReasonsSummary` observables per row are unchanged.
- Template `foreach` target renamed from `originIPStats` → `pagedOriginIPStats`; the old variable is no longer declared.
