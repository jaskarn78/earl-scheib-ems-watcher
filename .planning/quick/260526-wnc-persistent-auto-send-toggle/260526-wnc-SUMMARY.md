---
phase: quick-260526-wnc
plan: "01"
subsystem: backend+ui
tags: [scheduler, settings, toggle, admin-ui]
dependency_graph:
  requires: []
  provides: [app_settings-table, auto-send-toggle-ui]
  affects: [app.py, tests/test_scheduler_gate.py, internal/admin/ui/*, ui_public/*]
tech_stack:
  added: []
  patterns: [app_settings key-value table, INSERT OR IGNORE seed, DB-driven gate]
key_files:
  created: []
  modified:
    - app.py
    - tests/test_scheduler_gate.py
    - internal/admin/ui/index.html
    - internal/admin/ui/main.js
    - internal/admin/ui/main.css
    - ui_public/index.html
    - ui_public/main.js
    - ui_public/main.css
decisions:
  - "Auto-send default OFF: _AUTO_SEND_SEED reads env only at module load; INSERT OR IGNORE means DB wins on all subsequent restarts (D-02 contract)"
  - "Strict bool validation via isinstance(enabled, bool): correctly rejects ints (1/0) since bool subclasses int — isinstance(1, bool) is False"
  - "JSON key scheduler_enabled in /diagnostic preserved unchanged for UI back-compat; only the source switches to get_auto_send_enabled()"
  - "Toggle hidden in local Go admin (typeof d.scheduler_enabled !== 'boolean') — no Go files modified per plan constraint"
metrics:
  duration: "~15 minutes"
  completed: "2026-05-26"
  tasks_completed: 2
  files_changed: 8
---

# Phase quick-260526-wnc Plan 01: Persistent Auto-Send Toggle Summary

**One-liner:** Replaced env-gated SCHEDULER_ENABLED kill-switch with a DB-persisted `app_settings.auto_send_enabled` row that Marco can flip ON/OFF from the admin UI toggle (default OFF on deploy — zero behavior change until Marco clicks).

## What Was Built

### Task 1: Backend — app_settings table, getter/setter, first-boot seed, /auto-send endpoint, read-site swaps, rewritten tests

**app.py changes:**
- Added `AUTO_SEND_SETTING_KEY = "auto_send_enabled"` and `_AUTO_SEND_SEED` (computed from env at module load only)
- Removed `SCHEDULER_ENABLED` module constant — no longer a live gate anywhere
- Added `app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL)` table creation in `init_db()`
- Added `INSERT OR IGNORE` first-boot seed in `init_db()` — DB row wins on all subsequent restarts
- Added `get_auto_send_enabled() -> bool` (defensive: defaults OFF on OperationalError, missing table/row/NULL)
- Added `set_auto_send_enabled(enabled: bool) -> None` (parameterized upsert)
- Swapped `scheduler_loop` gate from `SCHEDULER_ENABLED` constant to `get_auto_send_enabled()` (fresh DB read each 30s)
- Updated startup log and gated-off log message text (no SCHEDULER_ENABLED references in messages)
- Swapped `/diagnostic` `scheduler_enabled` field source from constant to `get_auto_send_enabled()`
- Added `POST /earlscheibconcord/auto-send`: strict bool validation (`isinstance(enabled, bool)`), 400 on non-bool, persists and returns `{enabled: bool}`

**tests/test_scheduler_gate.py** fully rewritten (TDD RED→GREEN):
- First-boot seed tests (env=0/1/unset)
- D-02 env-ignored-after-seed test (DB wins)
- Loop gate OFF/ON tests via `get_auto_send_enabled`/`set_auto_send_enabled`
- Log throttle test (new message substring "auto-send disabled")
- SPN-03 manual send-now ungated test (preserved)
- POST /auto-send endpoint tests (enable, disable, reject int, reject string, reject missing key, reject bad JSON)
- UKK ems_bundle skip-disabled tests (preserved unchanged)

### Task 2: UI — auto-send toggle replacing DEV-MODE banner

**internal/admin/ui/index.html:** Replaced `#dev-banner` with `#auto-send-toggle` — a labelled switch bar above the topbar.

**internal/admin/ui/main.css:** Replaced `.dev-banner*` rules with `.auto-send-toggle*` block (cream/oxblood palette, instantaneous flip, keyboard focus ring — no transitions per CLAUDE.md design discipline).

**internal/admin/ui/main.js:** Replaced banner show/hide logic with:
- Poll sync: updates checkbox state + label text from `d.scheduler_enabled` on every `/diagnostic` poll; uses `dataset.syncing` guard to prevent re-POST
- Toggle hidden when `typeof d.scheduler_enabled !== 'boolean'` (local Go admin, pre-first-poll)
- `change` event handler: POSTs `{enabled: bool}` to `${API_BASE}/auto-send`; reverts checkbox on failure

`make sync-ui` copies all three files byte-identically to `ui_public/`.

## Commits

| Hash | Type | Description |
|------|------|-------------|
| `e2a44ff` | test (RED) | Rewrite scheduler-gate tests for DB-driven auto-send |
| `5792126` | feat (GREEN) | Replace env gate with persistent auto_send_enabled in app_settings |
| `b50d5d2` | feat | Replace DEV-MODE banner with persistent auto-send toggle in UI |

## Verification Results

```
python3 -m pytest tests/test_scheduler_gate.py -x -q
17 passed in 78.56s
```

```
make sync-ui && diff internal/admin/ui/index.html ui_public/index.html && \
  diff internal/admin/ui/main.js ui_public/main.js && \
  diff internal/admin/ui/main.css ui_public/main.css
# All diffs empty
```

```
grep -nv '^\s*#' app.py | grep -c 'SCHEDULER_ENABLED'
1   # only the _AUTO_SEND_SEED seed read
```

No Go files modified.

## Deviations from Plan

None — plan executed exactly as written.

## Known Stubs

None.

## Threat Flags

None — no new threat surface beyond what the plan's threat register already enumerated.

## Self-Check: PASSED

- `app.py` — exists, contains `AUTO_SEND_SETTING_KEY`, `get_auto_send_enabled`, `set_auto_send_enabled`, `app_settings`, `auto-send` endpoint
- `tests/test_scheduler_gate.py` — exists, 17 tests, all pass
- `internal/admin/ui/index.html` — exists, contains `auto-send-toggle`
- `internal/admin/ui/main.js` — exists, contains `auto-send`
- `ui_public/` — byte-identical to `internal/admin/ui/`
- Commit `e2a44ff` — exists (RED gate)
- Commit `5792126` — exists (GREEN gate)
- Commit `b50d5d2` — exists (T2)
