---
phase: quick
plan: 260508-spn
subsystem: admin-ui + python-server + go-admin-proxy
tags: [schedules, scheduler-gate, admin-ui, dev-mode-banner, marco-self-serve]
requires:
  - tests/conftest.py (queue_server fixture from WMH plan)
  - app.py /templates pattern (260422-wmh) — mirrored 1:1 for /schedules
  - internal/admin/proxy.go templates handlers (260422-wmh) — mirrored
provides:
  - app.py: schedules table, DEFAULT_SCHEDULES, get_effective_schedule(),
    GET/PUT /earlscheibconcord/schedules endpoints, scheduler gate,
    /diagnostic.scheduler_enabled, recalc-on-PUT
  - internal/admin/proxy.go: handleSchedulesList + handleScheduleUpsert
  - internal/admin/server.go: /api/schedules + /api/schedules/{job_type}
    routes; remoteSchedulesURL + remoteScheduleURL helpers
  - UI: Schedules topnav entry, view-schedules wrapper, schedule-card-template,
    .sched-card / .schedules / .dev-banner styles, loadSchedules() + supporting
    JS, hoursToDaysLabel() helper, dev-mode banner toggle in fetchDiagnostic
affects:
  - internal/admin/ui/* and ui_public/* (synced byte-for-byte)
  - app.py ingestion path (24*3600/72*3600/24*3600 literals replaced with
    get_effective_schedule()*3600)
  - app.py scheduler_loop (now gated on SCHEDULER_ENABLED env-var)
  - /queue/send-now stays NOT GATED — manual sends still work with gate off
key-files:
  created:
    - tests/test_schedules_endpoint.py
    - tests/test_scheduler_gate.py
  modified:
    - app.py
    - internal/admin/proxy.go
    - internal/admin/server.go
    - internal/admin/admin_test.go
    - internal/admin/ui/index.html
    - internal/admin/ui/main.js
    - internal/admin/ui/main.css
    - ui_public/index.html
    - ui_public/main.js
    - ui_public/main.css
decisions:
  - D-01: DEFAULT_SCHEDULES uses hours (24, 72, 24) not seconds — matches
    operator mental model; SCHEDULE_MIN_HOURS=1, SCHEDULE_MAX_HOURS=720
  - D-02: Rebase on PUT uses next_send_window(created_at + delay*3600)
    not raw arithmetic — preserves business-hours/weekend rules
  - D-03: SCHEDULER_ENABLED default is "0" (off) for the migration window
  - D-04: /diagnostic.scheduler_enabled drives the dev-mode banner; banner
    hides when scheduler_enabled === false logic resolves to true (i.e.
    when scheduler_enabled is true, missing, or undefined). Strict ===
    false comparison so the banner doesn't flash before /diagnostic loads.
  - D-05: Schedules tab appended after Templates (third position), no
    insertion in the middle of the topnav order
  - Manual /queue/send-now is INTENTIONALLY NOT GATED — preserves smoke
    testing capability while auto-send is paused
  - validScheduleJobTypes whitelist {24h, 3day, review} — proxy never
    fans out to arbitrary upstream paths
  - DelayHours field is *int (pointer) in the Go proxy parser so missing
    field / null / empty body all round-trip as canonical
    {"delay_hours":null} for the server-side revert path
metrics:
  duration: ~70 minutes (single session)
  tasks_completed: 3 / 3
  tests_added:
    python: 25 (19 schedules endpoint + 6 scheduler gate)
    go: 8 (proxy passthrough + whitelist + bad-method + propagation)
  files_created: 2
  files_modified: 10
  completed_date: 2026-05-08
---

# Quick 260508-spn: Schedules Editor + Scheduler Disable Flag

**One-liner:** Marco-editable per-job-type send delays (hours, with live "(X
days)" helper) backed by a new `schedules` DB table; server-side rebase of
pending jobs on schedule change; SCHEDULER_ENABLED env-var kill-switch
(default off) with DEV-mode banner across both admin UIs; manual send-now
remains live so the developer can fire one-off SMS while auto-send is paused.

## What Landed

### Server (Task 1, commit `44a5c50`)

- **`schedules` DB table** — `(job_type PK, delay_hours INTEGER, updated_at INTEGER)`,
  idempotent CREATE in `init_db()`, no seed INSERT. Mirrors the templates
  override pattern exactly: absence of a row = use `DEFAULT_SCHEDULES`.
- **`DEFAULT_SCHEDULES = {"24h": 24, "3day": 72, "review": 24}`** at module
  scope next to `DEFAULT_TEMPLATES`. `SCHEDULE_MIN_HOURS = 1`,
  `SCHEDULE_MAX_HOURS = 720` (30 days).
- **`get_effective_schedule(job_type)`** helper next to `_get_template_override`.
  Reads override row → falls back to default; defends against missing table.
- **`GET /earlscheibconcord/schedules`** — returns `{job_types, min_hours,
  max_hours}`, 3 rows with `delay_hours + is_override + updated_at + label + when`.
  Dual-auth (HMAC or Basic).
- **`PUT /earlscheibconcord/schedules/{job_type}`** — accepts `{"delay_hours": N}`
  with bounds + integer check (rejects float, bool, string, out-of-range).
  Empty body / missing field / null delay_hours → DELETE row (revert).
  **Rebases pending jobs**: every `sent=0` row of the same `job_type` gets
  `send_at = next_send_window(created_at + delay_hours * 3600)`. Sent rows
  and other job_types untouched. Response: `{is_override, delay_hours,
  updated_at, rebased_jobs}`.
- **`SCHEDULER_ENABLED` env-var** (default `"0"`) gates `_fire_due_jobs` inside
  `scheduler_loop`. When closed, scheduler logs once per hour ("scheduler
  gated off; SCHEDULER_ENABLED=0 — manual send-now still works") via a
  module-level `_last_gated_log_ts` throttle.
- **`/diagnostic` extended** with `scheduler_enabled: <bool>` field for the UI banner.
- **Hardcoded literals replaced** at ingestion path: `24*3600` / `72*3600` /
  `24*3600` in `do_POST` are now `get_effective_schedule(jt) * 3600`.
- **Inline comment added at `/queue/send-now`** reaffirming it bypasses the
  gate so future readers don't accidentally move the SCHEDULER_ENABLED check.

### Go admin proxy (Task 2, commit `7367dc7`)

- **`validScheduleJobTypes`** whitelist `{24h, 3day, review}` next to
  `validTemplateJobTypes`.
- **`handleSchedulesList`** — GET passthrough mirroring `handleTemplatesList`.
  HMAC over empty body, 1 MiB response cap.
- **`handleScheduleUpsert`** — PUT with canonical re-marshal. Body parsed as
  `struct { DelayHours *int }` so `nil` (missing field, null, or empty body)
  round-trips as `{"delay_hours":null}` — server treats this as the revert
  signal. job_type whitelisted; method enforced (PUT-only); body cap 4 KiB.
- **`/api/schedules` + `/api/schedules/`** routes registered in `server.go`
  alongside `/api/templates*`.
- **`remoteSchedulesURL` + `remoteScheduleURL`** helpers mirror
  `remoteTemplatesURL` / `remoteTemplateURL`. **Important**: WebhookURL
  already ends in `/earlscheibconcord`, so these just append `/schedules`
  (the plan's example showing `+ "/earlscheibconcord/schedules"` would
  double the prefix; matched the actual codebase pattern instead).

### UI (Task 3, commit `d1f57ad`)

- **Topnav** — third link "Schedules" appended after Templates.
- **Dev-mode banner** — `<div id="dev-banner">` injected as first child of
  `<body>`. Oxblood-on-white stripe with bold "DEV MODE" label and a
  `<code>`-styled `SCHEDULER_ENABLED=1` hint. Toggled in `fetchDiagnostic`
  on every 5s poll: hidden when `data.scheduler_enabled !== false`.
- **`view-schedules` wrapper** — `.schedules` section with header + hint
  text + `#schedules-list` injection point.
- **`<template id="schedule-card-template">`** — matches `.tpl-card` shape:
  title + when + Custom badge + dirty-dot, numeric input + "(X days)" span,
  Save + Reset-to-default + status echo.
- **`hoursToDaysLabel(h)`** helper — one-decimal day formatter, "1 day" vs
  "1.5 days" pluralization.
- **`loadSchedules() / buildScheduleCard() / applySavedSchedule()`** mirror
  the templates equivalents. Save sends `{"delay_hours": N}`, Reset sends
  `{}`. Status echoes `rebased_jobs` count from the server.
- **`wireTopnav()` extended** — adds `viewSched` toggle, `loadSchedules` on
  click, `#schedules` deep-link.
- **CSS** — new `.dev-banner`, `.schedules*`, `.sched-card*` selectors using
  the existing palette CSS vars (oxblood, amber dirty-dot, cream surface,
  IBM Plex Mono for the numeric input). Mobile responsive at 720px.
- **`make sync-ui`** ran clean; `ui_public/*` is byte-identical to
  `internal/admin/ui/*` on tracked files.

### Bundled UI rework

The working tree on master had pre-existing uncommitted UI changes (Inter
font swap, brand logo link replacing the ES wordmark, queue filter reorder
with new Test Est. / Test Work Compl. chips + reset button, estimate-card
date row, palette restyle). Per the critical_context spec these were
bundled into Task 3's commit since the plan touches the same UI files.

## Deviations from Plan

### Auto-fixed during execution

**1. [Rule 3 - Blocking] URL helpers don't double the prefix**

- **Found during:** Task 2
- **Issue:** Plan example showed `remoteSchedulesURL = WebhookURL + "/earlscheibconcord/schedules"`,
  but `WebhookURL` already ends in `/earlscheibconcord` (per `server.go:305`
  and the test setup in `admin_test.go:64` which appends
  `/earlscheibconcord` to the fake remote URL).
- **Fix:** Mirrored the existing `remoteTemplatesURL` pattern: just append
  `/schedules`. The Go proxy tests then assert `strings.HasSuffix(cr.path,
  "/earlscheibconcord/schedules")` — that suffix is what's verified.
- **Files modified:** `internal/admin/server.go`
- **Commit:** `7367dc7`

**2. [Rule 1 - Bug] Bool rejection ordering**

- **Found during:** Task 1
- **Issue:** Python `bool` is a subclass of `int`, so `isinstance(True, int)`
  is `True`. Without an explicit `isinstance(val, bool)` check first,
  `{"delay_hours": true}` would be accepted as `1`.
- **Fix:** In `_do_put_schedule`, check `isinstance(val, bool)` BEFORE
  `isinstance(val, int)` so booleans fall to the type-error branch.
- **Files modified:** `app.py`
- **Commit:** `44a5c50`
- **Test:** `test_put_schedule_bool_rejected` confirms.

### Intentional scope inclusion

**3. Bundled pre-existing UI rework into Task 3's commit**

- The working tree had uncommitted UI changes unrelated to SPN (Inter font,
  logo link, filter reorder, etc.) that the plan's critical_context block
  explicitly said to include with my UI work. Documented in the commit
  message; not a deviation from the plan's intent, just unusual scope.

## Test Inventory

### Python (`tests/test_schedules_endpoint.py` — 19 tests)

GET defaults + auth; PUT happy paths (upsert, empty-body revert, null
revert, missing-field revert); bounds (0 / 721 / -1 rejected, 1 / 720
accepted); type validation (string, float, bool rejected); unknown job_type;
bad HMAC; **rebase-on-PUT** (the critical test — verifies `send_at`
recomputed via `next_send_window` for every pending row of that job_type
while sent rows and other job_types stay untouched); revert-rebases-to-default;
end-to-end override flowing through `get_effective_schedule`.

### Python (`tests/test_scheduler_gate.py` — 6 tests)

Gate off skips due jobs (no `send_sms` call); gate on fires due jobs (single
`send_sms` call, row marked sent=1); default-off when env-var unset; the
once-per-hour log throttle (only 1 log line for 3 quick gated steps);
**manual send-now bypasses the gate** (POST to `/queue/send-now` with
SCHEDULER_ENABLED=0 still calls `send_sms`, marks row sent=1).

### Go (`internal/admin/admin_test.go` — 8 new tests)

`TestAdminProxy_SchedulesList_Forwards`, `_ScheduleUpsert_Forwards`,
`_RevertForwardsVerbatim` (canonical `{"delay_hours":null}` for `{}` input),
`_UnknownJobType`, `_EmptyJobType` (trailing slash → 400 without upstream
call), `_ScheduleUpsert_BadMethod` (POST → 405),
`_SchedulesList_BadMethod` (PUT → 405),
`_ScheduleUpsert_PropagatesUpstream400`.

## Verification Evidence

```
$ python3 -m pytest tests/test_schedules_endpoint.py tests/test_scheduler_gate.py -q
.........................                                                [100%]
25 passed in 44.41s

$ go test ./internal/admin/... -run 'Schedules|Schedule' -count=1
ok  	github.com/jjagpal/earl-scheib-watcher/internal/admin	0.015s

$ go test ./internal/admin/... -count=1
ok  	github.com/jjagpal/earl-scheib-watcher/internal/admin	0.551s

$ go build ./...   # clean

$ make sync-ui && diff -rq internal/admin/ui ui_public ...
DIFF CLEAN

$ python3 -c "import app; print(app.DEFAULT_SCHEDULES, app.SCHEDULER_ENABLED, app.get_effective_schedule('24h'))"
{'24h': 24, '3day': 72, 'review': 24} False 24
```

### Pre-existing test failures (NOT introduced)

11 failures in the full pytest run pre-date this plan and are documented
elsewhere:

- `test_queue_endpoint.py::test_get_queue_happy_path` (WMH SUMMARY
  documented this as known-flaky)
- `test_queue_endpoint.py::test_get_queue_ordering_and_filter`
- 7 in `test_templates.py` and 2 in `test_templates_endpoint.py` —
  shop-name string drift ("Auto Body Concord" → "Of Concord") and the
  `short_model` placeholder addition that came after the tests were written.

None of these failures touch schedules or the scheduler gate; my 25 new
tests all pass.

## What We Did NOT Touch

Per the plan's untouched-by-design verification, **zero changes** to:

- `internal/ems/*` (the dBase BMS bundle parser — but note `bms.go` /
  `bms_test.go` had pre-existing uncommitted edits that were left in the
  working tree, NOT included in any of my commits)
- `cmd/earlscheib/*` (binary entry point)
- `internal/scanner/*`
- `internal/installer/*` (installer scripts)
- `internal/heartbeat/*`
- `internal/remoteconfig/*`
- `internal/telemetry/*`
- `internal/update/*` (self-update mechanism)
- `Makefile`
- `EarlScheibWatcher-Setup.exe` — **no installer rebuild required**.
  Marco's machine doesn't need an update for this change; everything
  ships server-side.

`git diff --stat` over my three commits is contained to:
`app.py`, `tests/test_schedules_endpoint.py`, `tests/test_scheduler_gate.py`,
`internal/admin/proxy.go`, `internal/admin/server.go`,
`internal/admin/admin_test.go`, and the six UI files in
`internal/admin/ui/*` + `ui_public/*`.

## Operator Action Required

1. **Restart `app.py`** so the new module-level constants
   (`SCHEDULER_ENABLED`, `DEFAULT_SCHEDULES`, `_last_gated_log_ts`),
   the new GET/PUT routes, and the `scheduler_enabled` field in
   `/diagnostic` go live. The existing process (PID 3208473 on port 8200
   per the environmental note) is still running the pre-SPN code.

2. **Decide gate state for production:**
   - To go live with auto-send: set `SCHEDULER_ENABLED=1` in the server
     env (e.g., systemd unit, .env, or supervisor) and restart.
   - To stay paused (default): leave `SCHEDULER_ENABLED` unset or set
     to `"0"`. The dev-mode banner will appear on both admin UIs as a
     visible reminder, and manual `/queue/send-now` clicks still fire SMS.

3. **Optional smoke test** after restart:
   ```
   curl -H "X-EMS-Signature: $(python3 -c \
     'import hmac,hashlib,os,sys; \
      print(hmac.new(b"gruh7oul3whis3yeep2BUSH8rich", b"", hashlib.sha256).hexdigest())')" \
     http://localhost:8200/earlscheibconcord/schedules
   # Expect: 3 job_types with delay_hours 24/72/24, is_override=false
   ```

## Commits

| # | Task                                                          | Hash      |
|---|---------------------------------------------------------------|-----------|
| 1 | Server-side schedules table + endpoints + scheduler gate      | `44a5c50` |
| 2 | Admin proxy routes for /api/schedules + /api/schedules/{jt}   | `7367dc7` |
| 3 | UI tab + dev-mode banner (+ bundled pre-existing UI rework)   | `d1f57ad` |

## Self-Check: PASSED

- [x] `app.py` exists and `python3 -c "import app"` returns OK
- [x] `tests/test_schedules_endpoint.py` exists (19 tests, all pass)
- [x] `tests/test_scheduler_gate.py` exists (6 tests, all pass)
- [x] Commit `44a5c50` exists and contains app.py + 2 test files
- [x] Commit `7367dc7` exists and contains proxy.go + server.go + admin_test.go
- [x] Commit `d1f57ad` exists and contains 6 UI files
- [x] `go build ./...` clean
- [x] `go vet ./internal/admin/...` clean
- [x] `make sync-ui` ran clean; `diff -rq internal/admin/ui ui_public` silent
  on tracked files (excluding README/gitkeep/logo.png)
- [x] All 25 new Python tests pass; all 8 new Go tests pass
- [x] Existing 8 templates proxy tests still pass; existing scheduler /
  send-now tests still pass
