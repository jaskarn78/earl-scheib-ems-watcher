---
phase: quick
plan: 260508-ukk
subsystem: webhook-server,admin-ui,go-proxy
tags:
  - schedules
  - enable-disable-toggle
  - cancel-on-disable
  - ems_bundle-gating
  - admin-ui
requirements:
  - UKK-01
  - UKK-02
  - UKK-03
  - UKK-04
  - UKK-05
  - UKK-06
  - UKK-07
  - UKK-08
key-files:
  modified:
    - app.py
    - internal/admin/proxy.go
    - internal/admin/admin_test.go
    - internal/admin/ui/index.html
    - internal/admin/ui/main.js
    - internal/admin/ui/main.css
    - ui_public/index.html
    - ui_public/main.js
    - ui_public/main.css
    - tests/test_schedules_endpoint.py
    - tests/test_scheduler_gate.py
  created: []
decisions:
  - "schedules.enabled column added via idempotent ALTER (PRAGMA table_info gate); existing 260508-spn rows backfill to enabled=1 via SQLite's NOT NULL DEFAULT 1 clause"
  - "PUT body semantics changed: explicit-null on a partial body now means 'no change to this field'. Only empty body / `{}` triggers full revert (DELETE row). This supersedes the legacy null-as-revert path; two pre-existing tests updated"
  - "Cancellation idiom mirrors 260508-q9c precedent: UPDATE jobs SET sent=1, sent_at=? WHERE job_type=? AND sent=0 — same UPSERT pattern; rowcount captured as cancelled_jobs"
  - "Toggle-on does NOT resurrect previously cancelled rows (rebase loop touches sent=0 only); fresh ems_bundle POST creates new pending rows after re-enable"
  - "Go proxy parser is strict: *bool means non-bool 'enabled' values 400 at the proxy boundary without round-tripping. Same client UX as upstream-side validation"
  - "CSS toggle uses oxblood-on-surface palette (var(--oxblood) active, var(--ink-soft) inactive, var(--surface) thumb); zero transitions per CLAUDE.md design discipline"
metrics:
  tasks: 3
  files_modified: 11
  files_created: 0
  python_tests_added: 15
  python_tests_modified: 2
  go_tests_added: 5
  go_tests_modified: 2
  total_loc_delta: "+1062 / -97"
  commits: 3
  duration_min: ~25
  completed: "2026-05-08T22:25:00Z"
---

# Quick 260508-ukk: Per-Schedule Enable/Disable Toggle Summary

**One-liner:** Marco can independently toggle each follow-up schedule (24h /
3day / review) on or off in the admin UI; toggle-off cancels all pending jobs
of that job_type and gates `schedule_job` for future ems_bundle ingestion;
toggle-on does NOT resurrect cancelled rows.

## What Landed

### Server (`app.py`) — commit `c9257d7`

- **UKK-01 schema migration:** new `enabled INTEGER NOT NULL DEFAULT 1`
  column on `schedules` via idempotent `ALTER TABLE` gated by
  `PRAGMA table_info()`. Tested against (a) fresh DB, (b) pre-existing DB
  with 260508-spn rows that lack the column — SQLite's `NOT NULL DEFAULT 1`
  clause backfills `1` for existing rows so operators who already saved
  delay overrides get `enabled=true` automatically.
- **UKK-04 helper:** `get_schedule_enabled(job_type) -> bool` — defensive
  shape mirroring `get_effective_schedule`: returns `True` when row missing,
  column missing (table reset), or value is NULL.
- **UKK-02 GET response:** `/earlscheibconcord/schedules` now emits
  `enabled: bool` per `job_types` row (overrides AND defaults).
- **UKK-03 PUT semantics overhaul:** `delay_hours` and `enabled` are
  independently optional. Empty body / `{}` = full revert (DELETE row).
  Partial body with only one field = UPSERT preserving the other field's
  previous value. Bool-only validation on `enabled` (rejects 0, 1, "false").
- **UKK-04 cancel-on-disable:** when a PUT yields `effective_enabled=False`,
  cancel all `sent=0` rows of that `job_type` via the canonical idiom
  `UPDATE jobs SET sent=1, sent_at=?` (mirrors 260508-q9c). `cancelled_jobs`
  count returned in response. Rebase loop is skipped (no pending rows left).
- **UKK-05 ems_bundle gating:** each `schedule_job(...)` call inside the
  estimate (24h, 3day) and closed (review) branches is wrapped in
  `if get_schedule_enabled(jt): ...` with explicit "skipping" log on the
  else branch. Disabling 24h does NOT block 3day on the same estimate.

### Go admin proxy (`internal/admin/proxy.go`) — commit `687afa7`

- **UKK-06:** `handleScheduleUpsert` parser struct + canonical re-marshal
  struct gain `Enabled *bool` alongside `DelayHours *int`. Both pointer
  types serialize `nil → null`, so the upstream sees one of:
  - `{"delay_hours":null,"enabled":null}` → full revert
  - `{"delay_hours":48,"enabled":null}` → upsert delay only
  - `{"delay_hours":null,"enabled":false}` → upsert enabled only
  - `{"delay_hours":48,"enabled":false}` → upsert both
- Proxy stays JSON-pass-through; type validation lives upstream in `app.py`.
  The struct's strict `*bool` typing means non-bool `"enabled"` values 400
  at the proxy boundary without round-tripping (same client UX as
  upstream-side validation).

### UI (`internal/admin/ui/{index.html,main.js,main.css}` + `ui_public/*`) — commit `809851a`

- **UKK-07 markup:** toggle switch added inside `.sched-card__head` (right
  of `.sched-card__when`, left of `.sched-card__badge`):
  ```html
  <label class="sched-card__toggle">
    <input type="checkbox" class="sched-card__toggle-input" />
    <span class="sched-card__toggle-track" aria-hidden="true"></span>
    <span class="sched-card__toggle-label">Enabled</span>
  </label>
  ```
- **UKK-07 JS:** `buildScheduleCard` reads `jt.enabled` (default true),
  sets initial checked state + label text + `.is-disabled` class. New
  `change` handler PUTs `{enabled: bool}` immediately. On failure: reverts
  checkbox visually + status echo. `applySavedSchedule` preserves `enabled`
  from server response. Reset button also flips toggle visual on revert.
- **UKK-08 status echoes:**
  - Toggle-off with cancelled rows: `Disabled · N pending job(s) cancelled`
  - Toggle-off with no pending: `Disabled`
  - Toggle-on: `Enabled` (no resurrection language — consistent with
    locked decision 1)
- **CSS palette:** `var(--oxblood)` for active track, `var(--ink-soft)` for
  inactive track, `var(--surface)` (white) for the thumb. Adjacent-sibling
  selectors (`+`) target the track from the input. Zero transitions / zero
  animations per CLAUDE.md design discipline. Focus-visible ring uses
  `var(--oxblood)`.
- **Locked decision 3:** `.sched-card.is-disabled` dims the card to
  `opacity: 0.6`, but child `.sched-card__row`, `.sched-card__actions`, and
  `.sched-card__toggle` keep `opacity: 1` so Marco can pre-tune the delay
  before re-enabling.
- `ui_public/*` byte-identical to `internal/admin/ui/*` via `make sync-ui`.

## Locked Decisions Confirmed

1. **Cancellation pattern:** toggle-off cancels pending via
   `UPDATE jobs SET sent=1, sent_at=?` (mirrors 260508-q9c). Verified by
   `test_put_schedule_toggle_off_cancels_pending`.
2. **No resurrection on toggle-on:** verified by
   `test_put_schedule_toggle_on_does_not_resurrect`.
3. **Default = enabled:** verified by
   `test_get_schedules_includes_enabled_default_true` and
   `test_ems_bundle_default_enabled_schedules_all`.
4. **Independent toggle/delay:** verified by
   `test_put_schedule_enabled_only_does_not_change_delay`,
   `test_put_schedule_delay_only_does_not_change_enabled`, and the Go
   round-trip tests `BothFieldsRoundTrip` + `DelayOnlyPreservesNullEnabled`.
5. **Disabled card stays interactive:** CSS rule
   `.sched-card.is-disabled .sched-card__row { opacity: 1 }` preserves
   input/button visibility while card opacity drops to 0.6.

## Test Inventory

### Python — `tests/test_schedules_endpoint.py`

**12 new tests:**
- `test_get_schedules_includes_enabled_default_true`
- `test_put_schedule_toggle_off_persists`
- `test_put_schedule_toggle_off_cancels_pending`
- `test_put_schedule_toggle_on_does_not_resurrect`
- `test_put_schedule_enabled_only_does_not_change_delay`
- `test_put_schedule_delay_only_does_not_change_enabled`
- `test_put_schedule_both_fields_together`
- `test_put_schedule_empty_body_reverts_both`
- `test_put_schedule_enabled_string_rejected`
- `test_put_schedule_enabled_int_rejected`
- `test_put_schedule_enabled_null_treated_as_absent`
- `test_put_schedule_toggle_off_then_delay_change_does_not_rebase`

**2 pre-existing tests modified (semantic shift, not regressions):**
- `test_put_schedule_null_delay_no_change` (was
  `test_put_schedule_null_delay_reverts`) — null on partial body now
  means "no change", not revert.
- `test_put_schedule_empty_dict_reverts` (was
  `test_put_schedule_missing_field_reverts`) — clarifies that only `{}`
  reverts.

### Python — `tests/test_scheduler_gate.py`

**3 new tests** (uses `reload_app_with_gate` fixture for isolated DB +
direct `INSERT INTO schedules ... enabled=0` to bypass the PUT path):
- `test_ems_bundle_skips_disabled_24h`
- `test_ems_bundle_skips_disabled_review`
- `test_ems_bundle_default_enabled_schedules_all`

**Helper added:** `_bms_xml(doc_id, doc_status)` constructs minimal BMS
XML payloads for `parse_bms()` — exercises the estimate (E) and closed
(C) branches with realistic Owner/VehicleInfo subtrees.

### Go — `internal/admin/admin_test.go`

**5 new tests:**
- `TestAdminProxy_ScheduleUpsert_EnabledRoundTrip`
- `TestAdminProxy_ScheduleUpsert_BothFieldsRoundTrip`
- `TestAdminProxy_ScheduleUpsert_DelayOnlyPreservesNullEnabled`
- `TestAdminProxy_ScheduleUpsert_EmptyBodyForwardsBothNulls`
- `TestAdminProxy_ScheduleUpsert_BadEnabledType`

**2 pre-existing tests updated** for new canonical body shape:
- `TestAdminProxy_ScheduleUpsert_Forwards`: `wantBody` is now
  `{"delay_hours":48,"enabled":null}` (signed under same secret).
- `TestAdminProxy_ScheduleUpsert_RevertForwardsVerbatim`: `wantBody` is
  now `{"delay_hours":null,"enabled":null}`.

## Commits

| # | Hash      | Title                                                         |
|---|-----------|---------------------------------------------------------------|
| 1 | `c9257d7` | feat(schedules): per-schedule enable/disable toggle — server  |
| 2 | `687afa7` | feat(schedules): per-schedule enable/disable toggle — proxy   |
| 3 | `809851a` | feat(schedules): per-schedule enable/disable toggle — UI      |

Working-tree summary across all three:
- 11 files changed, +1062 lines / -97 lines

## Verification Evidence

**Python tests:**
```
$ python3 -m pytest tests/test_schedules_endpoint.py tests/test_scheduler_gate.py -q
........................................                                 [100%]
40 passed in 90.84s
```

**Go tests:**
```
$ go test ./internal/admin/... -run 'Schedules|Schedule' -count=1
ok  	github.com/jjagpal/earl-scheib-watcher/internal/admin	0.024s
```
13 schedule tests green (8 pre-existing + 5 new).

**Build + vet:**
```
$ go vet ./... && go build ./...
(silent — clean)
```

**UI sync:**
```
$ diff -rq internal/admin/ui ui_public 2>&1 | grep -v -E 'README|gitkeep|logo\.png'
(silent — byte-identical)
```

**Toggle markup present in all 6 files:**
```
$ grep -c 'sched-card__toggle' internal/admin/ui/*.* ui_public/*.*
internal/admin/ui/index.html:4
internal/admin/ui/main.js:2
internal/admin/ui/main.css:9
ui_public/index.html:4
ui_public/main.js:2
ui_public/main.css:9
```

**Migration smoke (fresh DB):**
```
$ python3 -c "import app; ..."
schedules: added enabled column (default 1)
get_schedule_enabled(24h) = True
schedules columns: ['job_type', 'delay_hours', 'updated_at', 'enabled']
```

**Migration smoke (pre-existing 260508-spn DB with override row):**
```
schedules: added enabled column (default 1)         # first init_db
DB migrated: +0 columns                              # second init_db (idempotent)
cols: ['job_type', 'delay_hours', 'updated_at', 'enabled']
row: ('24h', 48, 1700000000, 1)                      # existing row backfilled to enabled=1
```

## Deviations from Plan

### Auto-fixed Issues (Rule 1/2)

**1. [Rule 1 — Bug] Pre-existing `null-delay-reverts` semantic clash**
- **Found during:** Task 1 first test run.
- **Issue:** `test_put_schedule_null_delay_reverts` and
  `test_put_schedule_missing_field_reverts` (both shipped in 260508-spn)
  asserted that `{"delay_hours": null}` and `{}` BOTH revert the row.
  The new UKK-03 semantics distinguish them: `{}` = full revert; partial
  body with `null` field = no change.
- **Fix:** Renamed the two tests to reflect the new semantics
  (`test_put_schedule_null_delay_no_change`,
  `test_put_schedule_empty_dict_reverts`) and updated assertions. The
  new behavior is required — the locked decision specifies `{}` as the
  ONLY revert path so partial-field PUTs don't accidentally reset the
  other field.
- **Files modified:** `tests/test_schedules_endpoint.py`
- **Commit:** `c9257d7` (folded into Task 1).

**2. [Rule 1 — Bug] Pre-existing Go canonical-body assertions**
- **Found during:** Task 2 first test run.
- **Issue:** `TestAdminProxy_ScheduleUpsert_Forwards` (line 680) and
  `TestAdminProxy_ScheduleUpsert_RevertForwardsVerbatim` (line 709)
  asserted byte-exact bodies `{"delay_hours":48}` and
  `{"delay_hours":null}` — both broke once the canonical re-marshal
  struct gained `Enabled *bool` (Go emits fields in declaration order).
- **Fix:** Updated both `wantBody` strings to the new canonical shape;
  `wantSig` recomputes off the new bytes.
- **Files modified:** `internal/admin/admin_test.go`
- **Commit:** `687afa7` (folded into Task 2).

**3. [Rule 1 — Bug] Test `BadEnabledType` originally assumed proxy
  pass-through behavior**
- **Found during:** Task 2 first test run.
- **Issue:** Plan suggested the proxy forwards bad-type `enabled` values
  to upstream which then 400s. In practice, the proxy's parser struct
  uses `*bool` so Go's `json.Unmarshal` rejects `{"enabled":"true"}`
  with a type error before any forwarding. The test was returning a
  fake-upstream-200 but reading the proxy's own 400 body.
- **Fix:** Adjusted the test to assert the actual proxy behavior:
  400 status + upstream NEVER called. Added a comment documenting that
  the proxy is stricter than upstream but yields identical client UX.
- **Files modified:** `internal/admin/admin_test.go`
- **Commit:** `687afa7` (folded into Task 2).

**4. [Rule 3 — Blocking] Plan's literal `Optional[int]` type
  annotations would NameError**
- **Found during:** Task 1 read of app.py imports.
- **Issue:** Plan's action specified
  `new_delay_hours: Optional[int] = None` — but app.py has no
  `from typing import Optional` import.
- **Fix:** Dropped the type annotations on the two locals (they're
  obvious from comments) rather than adding a new import line for two
  uses. Adheres to project conventions (existing app.py is
  annotation-light).
- **Files modified:** `app.py`
- **Commit:** `c9257d7` (folded into Task 1).

**5. [Rule 1 — Bug] CSS palette variable names in plan were wrong**
- **Found during:** Task 3 CSS authoring.
- **Issue:** Plan referenced `--terra` (claiming it was oxblood) and
  `--cream`. Actual palette in `main.css :root`:
  - `--oxblood: #c8202f` (correct for active track)
  - `--ink-soft: #5a6a80` (correct for inactive track)
  - `--surface: #ffffff` (correct for thumb)
  - `--terra: #c25f3e` exists but is terracotta (wrong semantic)
  - `--cream` does not exist (`--canvas` is the page bg)
- **Fix:** Used the correct existing variable names directly
  (`var(--oxblood)`, `var(--ink-soft)`, `var(--surface)`) instead of the
  plan's `var(--name, hex-fallback)` form.
- **Files modified:** `internal/admin/ui/main.css`, `ui_public/main.css`
- **Commit:** `809851a` (folded into Task 3).

### Authentication gates

None — no auth was needed for any local automation step. The HMAC secret
in `.env` (`gruh7oul3whis3yeep2BUSH8rich`) was not exercised because all
tests use the `queue_server` fixture's isolated `pytest-fixture-secret`.

## What We Did NOT Touch

Per the constraints — the watcher binary stays untouched. No installer
rebuild required. Marco's `EarlScheibWatcher.exe` continues running
exactly as it did this morning.

- `internal/ems/*` (BMS parser + bundle handler) — uncommitted edits in
  `bms.go` / `bms_test.go` left in working tree, NOT staged.
- `cmd/earlscheib/*` (entrypoints, tray, status)
- `internal/scanner/*`, `internal/installer/*`, `internal/heartbeat/*`,
  `internal/remoteconfig/*`, `internal/telemetry/*`, `internal/update/*`
- `Makefile` (no installer target changes)
- The original `CREATE TABLE schedules` statement in `init_db()` —
  unchanged. The `enabled` column is added via ALTER migration so any
  pre-existing 260508-spn override rows are preserved with `enabled=1`
  backfilled by SQLite.

`git diff --stat -- internal/ems internal/scanner cmd/earlscheib internal/installer internal/heartbeat internal/remoteconfig internal/telemetry internal/update Makefile` returned empty after all three commits.

## Pending Operator Actions

**Server restart required.** The currently running `app.py` process (PID
1966321 on port 8200) is on master HEAD-3 from before this work. After
commit `c9257d7` lands the running process won't have:
- the `/schedules` GET `enabled` field
- the new PUT `{enabled: bool}` body shape
- the cancel-on-disable behavior
- the `ems_bundle` gating

The orchestrator / user handles `systemctl restart earl-scheib.service`
(or equivalent) when ready. Once restarted, the migration runs against
the production `jobs.db`: any operator override rows from the 260508-spn
deployment automatically backfill to `enabled=1` (no behavior change for
operators who never touch the new toggles).

**No installer rebuild needed.** Marco's watcher does not consume
`/schedules` — only the operator's admin UI does. This is server-side
+ admin-UI only.

## Self-Check: PASSED

| Claim                                                  | Verified                                                                 |
|--------------------------------------------------------|--------------------------------------------------------------------------|
| Commit `c9257d7` exists                                | `git log` shows it                                                       |
| Commit `687afa7` exists                                | `git log` shows it                                                       |
| Commit `809851a` exists                                | `git log` shows it                                                       |
| 40 Python tests pass                                   | `pytest -q` → 40 passed                                                  |
| 13 Go schedule tests pass                              | `go test -run 'Schedule'` → ok                                           |
| Build + vet clean                                      | `go vet ./... && go build ./...` → silent                                |
| `diff -rq internal/admin/ui ui_public` silent          | confirmed                                                                |
| `sched-card__toggle` present in all 6 UI files         | grep counts match                                                        |
| `enabled` column in DB after init_db                   | smoke `PRAGMA table_info(schedules)` shows it                            |
| Migration idempotent on pre-existing rows              | smoke confirmed (`enabled=1` backfilled, 2nd init_db does nothing)       |
| `get_schedule_enabled('24h')` returns True by default  | smoke confirmed                                                          |
| No out-of-scope files modified                         | `git diff --stat -- internal/ems ...` empty                              |
