---
phase: quick
plan: 260508-spn
type: execute
wave: 1
depends_on: []
files_modified:
  - app.py
  - internal/admin/admin.go
  - internal/admin/proxy.go
  - internal/admin/server.go
  - internal/admin/admin_test.go
  - internal/admin/ui/index.html
  - internal/admin/ui/main.js
  - internal/admin/ui/main.css
  - ui_public/index.html
  - ui_public/main.js
  - ui_public/main.css
  - tests/test_schedules_endpoint.py
  - tests/test_scheduler_gate.py
autonomous: true
requirements:
  - SPN-01  # schedules table + GET/PUT endpoints (mirrors templates pattern)
  - SPN-02  # rebase pending jobs' send_at when schedule changes
  - SPN-03  # SCHEDULER_ENABLED env-var gate (default off; manual send-now stays live)
  - SPN-04  # /diagnostic exposes scheduler_enabled flag for banner
  - SPN-05  # Schedules tab UI (3rd topnav, mirrors Templates card pattern)
  - SPN-06  # DEV-mode banner across both UIs when SCHEDULER_ENABLED != "1"

must_haves:
  truths:
    - "Marco sees three numeric inputs (24h, 3day, review) on a new Schedules tab and can change the delays in hours."
    - "Each input shows a parenthetical '(X day[s])' next to it that updates as he types."
    - "Saving a schedule rebases send_at for every pending job of that job_type to created_at + new_delay (run through next_send_window)."
    - "When SCHEDULER_ENABLED != '1', _fire_due_jobs is a no-op — pending jobs stay queued forever."
    - "When SCHEDULER_ENABLED != '1', both admin UIs show a top banner saying DEV mode + scheduler off."
    - "Manual send-now (POST /queue/send-now) still fires sends regardless of SCHEDULER_ENABLED."
    - "GET /earlscheibconcord/schedules returns 3 rows with effective delay_hours + is_override + updated_at."
    - "PUT /earlscheibconcord/schedules/{job_type} with empty body reverts to default and rebases pending jobs."
  artifacts:
    - path: "app.py"
      provides: "schedules table, DEFAULT_SCHEDULES, get_effective_schedule, GET/PUT /schedules endpoints, scheduler gate, /diagnostic.scheduler_enabled, recalc on PUT"
      contains: "DEFAULT_SCHEDULES"
    - path: "internal/admin/proxy.go"
      provides: "handleSchedulesList (GET /api/schedules), handleScheduleUpsert (PUT /api/schedules/{job_type}), validScheduleJobTypes whitelist"
      contains: "handleSchedulesList"
    - path: "internal/admin/server.go"
      provides: "/api/schedules + /api/schedules/{job_type} routes registered"
      contains: "/api/schedules"
    - path: "internal/admin/ui/index.html"
      provides: "Schedules tab in topnav, view-schedules wrapper, schedule-card-template"
      contains: "data-view=\"schedules\""
    - path: "internal/admin/ui/main.js"
      provides: "loadSchedules(), schedule card render, hours→days helper, dirty-dot/save/reset, dev-mode banner toggle, schedules tab swap"
      contains: "loadSchedules"
    - path: "internal/admin/ui/main.css"
      provides: ".sched-card* styles, .dev-banner styles"
      contains: ".dev-banner"
    - path: "tests/test_schedules_endpoint.py"
      provides: "GET defaults, PUT upsert + GET round-trip, PUT empty=delete, bounds (1..720), recalc-on-PUT verified, bad job_type/HMAC paths"
      contains: "test_put_schedule_rebases_pending_jobs"
    - path: "tests/test_scheduler_gate.py"
      provides: "SCHEDULER_ENABLED='0' -> _fire_due_jobs is no-op (or scheduler_loop skips); SCHEDULER_ENABLED='1' -> due jobs fire"
      contains: "test_scheduler_gate_disabled_skips_due_jobs"
  key_links:
    - from: "app.py do_POST(?trigger=ems_bundle)"
      to: "get_effective_schedule"
      via: "schedule_job(... next_send_window(now + get_effective_schedule(jt)*3600), ...)"
      pattern: "get_effective_schedule"
    - from: "app.py PUT /schedules/{job_type}"
      to: "jobs table"
      via: "UPDATE jobs SET send_at = next_send_window(created_at + delay) WHERE job_type=? AND sent=0"
      pattern: "UPDATE jobs SET send_at"
    - from: "app.py scheduler_loop"
      to: "SCHEDULER_ENABLED env-var"
      via: "if SCHEDULER_ENABLED: _fire_due_jobs(); else: log every 1h then continue"
      pattern: "SCHEDULER_ENABLED"
    - from: "internal/admin/ui/main.js renderDevBanner"
      to: "GET /diagnostic.scheduler_enabled"
      via: "fetched on every loadDiagnostic refresh; banner shown when scheduler_enabled === false"
      pattern: "scheduler_enabled"
    - from: "internal/admin/server.go (mux.HandleFunc)"
      to: "internal/admin/proxy.go (handleSchedulesList/handleScheduleUpsert)"
      via: "/api/schedules + /api/schedules/{job_type} routes"
      pattern: "/api/schedules"
---

<objective>
Add a "Schedules" tab to the admin UI that lets the operator (Marco) configure per-job-type delays — `24h` follow-up, `3day` follow-up, `review-after-close` — in **hours** (with a `(X days)` rendered helper). Persist overrides in a new `schedules` DB table and fall back to module-level defaults when no row exists. When a schedule changes, **rebase `send_at` for every pending job of that `job_type`** so the queue UI reflects the new delay immediately. Add a `SCHEDULER_ENABLED` env-var (default `"0"` = off) that gates `_fire_due_jobs`, with a clear DEV-mode banner across both admin UIs while the gate is closed. **Manual send-now (button-click) is NOT gated** — it always works for hand-fired sends during dev/testing.

Purpose: Marco wants tunable delays without code changes (today they're hardcoded `24*3600` / `72*3600` / `24*3600` literals at app.py:2896-2906), and the developer wants a kill-switch so the auto-send loop stays off during the staging-to-production migration window — but still wants to fire one-off SMS via the queue UI when smoke-testing.

Output: A new Schedules tab visible at `/earlscheib` and the local Go admin, three numeric inputs with live "(X days)" helper text, dirty-dot + Save + Reset-to-default per row (mirroring the Templates editor exactly). New `GET /earlscheibconcord/schedules` and `PUT /earlscheibconcord/schedules/{job_type}` endpoints. New `/api/schedules` Go-admin proxy routes. A `SCHEDULER_ENABLED` env-var that the scheduler loop honours every tick. A DEV-mode banner that both UIs display when the gate is closed. Tests covering all three: schedules CRUD, recalc-on-PUT, scheduler gate.
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
@$HOME/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@CLAUDE.md
@app.py
@internal/admin/admin.go
@internal/admin/proxy.go
@internal/admin/server.go
@internal/admin/ui/index.html
@internal/admin/ui/main.js
@internal/admin/ui/main.css
@tests/conftest.py
@tests/test_templates_endpoint.py
@.planning/quick/260422-wmh-add-a-message-template-editor-to-both-ad/260422-wmh-SUMMARY.md
@Makefile

<interfaces>
<!-- Key contracts the executor must mirror — extracted from existing code. -->
<!-- Do NOT explore the codebase to "find the templates pattern" — it's all here. -->

### app.py module-level pattern (lines 86-102, mirrored for SCHEDULER_ENABLED)

```python
TEST_PHONE_OVERRIDE = os.getenv("TEST_PHONE_OVERRIDE", "")
IMMEDIATE_SEND_FOR_TESTING = os.getenv("IMMEDIATE_SEND_FOR_TESTING", "") == "1"
# NEW (this plan): default OFF until go-live
SCHEDULER_ENABLED = os.getenv("SCHEDULER_ENABLED", "0") == "1"
```

### app.py DEFAULT_SCHEDULES (mirrors DEFAULT_TEMPLATES at line 129)

```python
DEFAULT_SCHEDULES = {
    "24h":    24,   # hours after estimate
    "3day":   72,
    "review": 24,   # hours after work-completed
}
```

Bounds: `1 <= delay_hours <= 720` (1 hour .. 30 days).

### app.py existing scheduler loop (line 706 — current state)

```python
def scheduler_loop():
    log.info("Scheduler started")
    while True:
        try:
            _fire_due_jobs()
        except Exception as exc:
            log.error("Scheduler error: %s", exc)
        time.sleep(30)
```

### app.py existing hardcoded delays (lines 2894-2909 — to be replaced)

```python
if doc_status in ESTIMATE_STATUSES:
    schedule_job(doc_id, "24h", phone, name, next_send_window(now + 24*3600), ...)
    schedule_job(doc_id, "3day", phone, name, next_send_window(now + 72*3600), ...)
elif doc_status in CLOSED_STATUSES:
    schedule_job(doc_id, "review", phone, name, next_send_window(now + 24*3600), ...)
```

After this plan: each `24*3600` / `72*3600` / `24*3600` literal becomes
`get_effective_schedule(<job_type>) * 3600`.

### app.py existing template GET endpoint (lines 2353-2442 — STRUCTURE TO MIRROR for /schedules)

```python
if path == "/earlscheibconcord/templates":
    if not _validate_auth(self, b""):
        self._send_json(401, {"error": "invalid signature"})
        return
    # ... build job_types[] with effective_body + is_override + updated_at ...
    self._send_json(200, {
        "job_types":   [...],
        "placeholders": {...},
        "sample_row":  {...},
    })
```

### app.py existing template PUT endpoint (lines 2955-3070 — STRUCTURE TO MIRROR for /schedules)

`do_PUT` already exists. The `prefix = "/earlscheibconcord/templates/"` branch is
in place. Add a sibling `prefix = "/earlscheibconcord/schedules/"` branch in the
same `do_PUT` method (do NOT shadow — both prefixes coexist).

PUT body shape: `{"delay_hours": 24}` (numeric, 1..720) — empty body / null /
missing field → revert to default.

### app.py /diagnostic endpoint (line 2448) — extend payload

Append one field to the existing JSON response:

```python
self._send_json(200, {
    # ... existing fields ...
    "scheduler_enabled": SCHEDULER_ENABLED,  # NEW
})
```

### app.py JOB_TYPE_META (existing — used for dropdown labels and as the
canonical job-type whitelist; reuse for schedules)

```python
JOB_TYPE_META = [
    {"job_type": "24h",    "label": "24-hour follow-up", "when": "..."},
    {"job_type": "3day",   "label": "3-day follow-up",   "when": "..."},
    {"job_type": "review", "label": "Review request",    "when": "..."},
]
```

### Go admin proxy pattern (internal/admin/proxy.go — STRUCTURE TO MIRROR)

`handleTemplatesList` (line ~230) and `handleTemplateUpsert` (line ~273) are
the canonical pattern — copy them line-by-line, swap "templates"→"schedules":

```go
var validScheduleJobTypes = map[string]bool{
    "24h":    true,
    "3day":   true,
    "review": true,
}

func (s *server) handleSchedulesList(w http.ResponseWriter, r *http.Request) {
    // GET — empty body, HMAC([]byte("")), 1 MiB upstream limit
    // identical to handleTemplatesList except URL helper
}

func (s *server) handleScheduleUpsert(w http.ResponseWriter, r *http.Request) {
    // PUT — parse {"delay_hours": N}, re-marshal to canonical JSON, HMAC,
    // forward to s.remoteScheduleURL(jobType)
    // identical to handleTemplateUpsert except body shape (Body string -> DelayHours int)
}
```

### Go server.go (internal/admin/server.go) — route registration site (line 159)

```go
mux.HandleFunc("/api/templates", s.handleTemplatesList)
mux.HandleFunc("/api/templates/", s.handleTemplateUpsert)
// NEW (this plan):
mux.HandleFunc("/api/schedules", s.handleSchedulesList)
mux.HandleFunc("/api/schedules/", s.handleScheduleUpsert)
```

Plus two new URL helpers next to `remoteTemplatesURL` (line ~317):

```go
func (s *server) remoteSchedulesURL() string {
    return strings.TrimSuffix(s.cfg.WebhookURL, "/") + "/earlscheibconcord/schedules"
}
func (s *server) remoteScheduleURL(jobType string) string {
    return s.remoteSchedulesURL() + "/" + jobType
}
```

### UI tab structure (internal/admin/ui/index.html line 25-28)

```html
<nav class="topnav" role="tablist" aria-label="Views">
  <a class="topnav-link is-active" data-view="queue"     href="#queue"     role="tab" aria-selected="true">Queue</a>
  <a class="topnav-link"           data-view="templates" href="#templates" role="tab" aria-selected="false">Templates</a>
  <!-- NEW (this plan): -->
  <a class="topnav-link"           data-view="schedules" href="#schedules" role="tab" aria-selected="false">Schedules</a>
</nav>
```

Plus a third view wrapper (mirror `<div id="view-templates" data-view="templates" hidden>` at line 96):

```html
<div id="view-schedules" data-view="schedules" hidden>
  <header class="schedules__header">
    <h1>Send Schedules</h1>
    <p class="schedules__hint">Delay (in hours) between event and SMS. Saved instantly. Pending jobs are rebased.</p>
  </header>
  <div id="schedules-list" class="schedules__list" aria-live="polite"></div>
</div>
```

Plus a `<template id="schedule-card-template">` element next to `template-card-template`
(line 184).

### UI tab swap (internal/admin/ui/main.js line 1111-1130) — extend to handle "schedules"

```js
const links = document.querySelectorAll('.topnav-link');
const viewQueue = document.getElementById('view-queue');
const viewTpl   = document.getElementById('view-templates');
const viewSched = document.getElementById('view-schedules'); // NEW
// ...
if (target === 'templates') loadTemplates(false);
if (target === 'schedules') loadSchedules(false);  // NEW
```

### UI loadTemplates pattern (internal/admin/ui/main.js line 851) — STRUCTURE TO MIRROR

`loadTemplates(force)` fetches `${API_BASE}/templates`, populates
`effectiveTemplates`, renders one `.tpl-card` per `job_type`. Mirror as
`loadSchedules(force)` → fetches `${API_BASE}/schedules`, renders one
`.sched-card` per `job_type`.

### Diagnostic banner wiring

`/diagnostic` is already polled on the queue page (main.js line 631). Extend
the existing diagnostic-fetch handler to read the new `scheduler_enabled`
field and toggle a `<div id="dev-banner" hidden>` at the top of the page
(injected once into both index.html bodies, just below `<body>`).

</interfaces>

</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Schedules table + DEFAULT_SCHEDULES + GET/PUT endpoints + scheduler gate + /diagnostic field</name>
  <files>app.py, tests/test_schedules_endpoint.py, tests/test_scheduler_gate.py</files>
  <behavior>
    Tests in `tests/test_schedules_endpoint.py` (mirror tests/test_templates_endpoint.py):
    - GET /earlscheibconcord/schedules returns 3 job_types with `delay_hours` + `is_override=False` + `updated_at=0` (no overrides seeded). Default values: 24, 72, 24.
    - GET requires HMAC or Basic — bad/missing sig → 401.
    - PUT /earlscheibconcord/schedules/24h with `{"delay_hours": 48}` upserts; subsequent GET shows `delay_hours=48, is_override=True, updated_at>0`.
    - PUT with `{"delay_hours": null}` (or missing field, or empty body `{}`) DELETEs the row → revert; subsequent GET shows the default 24 + `is_override=False`.
    - PUT with `delay_hours=0` → 400 (below min).
    - PUT with `delay_hours=721` → 400 (above max 720).
    - PUT with non-integer (`"abc"`, `1.5`, `true`) → 400.
    - PUT with unknown job_type (`/schedules/foo`) → 400.
    - PUT with bad HMAC → 401.
    - **Recalc-on-PUT (the critical test):** seed 3 pending jobs with `job_type='24h'` (each with a known `created_at`); PUT delay_hours=48; assert each row's `send_at` is now `next_send_window(created_at + 48*3600)` (NOT `created_at + 24*3600`). Sent rows (sent=1) and other job_types must be untouched.
    - End-to-end: PUT override saves → ingestion path (calling `get_effective_schedule("24h")`) returns the override.

    Tests in `tests/test_scheduler_gate.py`:
    - With `SCHEDULER_ENABLED="0"` (default after env reset + module reload): seed a due job (send_at <= now, sent=0) and call `_fire_due_jobs()` (or step the scheduler manually). Assert: row stays sent=0. Assert: no Twilio send call (monkeypatch `send_sms` to a recorder; assert it was never called).
    - With `SCHEDULER_ENABLED="1"`: seed a due job and call `_fire_due_jobs()`. Assert: row becomes sent=1 (with `send_sms` monkey-patched to return True). Assert: `send_sms` was called with the expected phone+body.
    - The gate-off log message ("scheduler gated off; SCHEDULER_ENABLED=0") fires at most once per hour — verify by tracking a module-level `_LAST_GATED_LOG_TS` and stepping `_fire_due_jobs` twice in quick succession (only one log line emitted).
    - **Manual send-now (POST /queue/send-now) MUST still work with SCHEDULER_ENABLED=0** — extend tests to call the send-now path directly and assert it calls `send_sms` regardless of the gate.
  </behavior>
  <action>
    Edit app.py with these changes (in order):

    1. **Module-level constants** (next to `IMMEDIATE_SEND_FOR_TESTING` at line ~102, per D-03):
       ```python
       SCHEDULER_ENABLED = os.getenv("SCHEDULER_ENABLED", "0") == "1"
       _GATED_LOG_INTERVAL_S = 3600  # log "scheduler gated off" at most once per hour
       _last_gated_log_ts = 0
       ```

    2. **DEFAULT_SCHEDULES** (immediately after `DEFAULT_TEMPLATES` block at line ~170, per D-01):
       ```python
       DEFAULT_SCHEDULES = {"24h": 24, "3day": 72, "review": 24}
       SCHEDULE_MIN_HOURS = 1
       SCHEDULE_MAX_HOURS = 720  # 30 days
       ```

    3. **schedules table** in `init_db()` (after the templates table at line ~330):
       ```python
       cur.execute("""
           CREATE TABLE IF NOT EXISTS schedules (
               job_type     TEXT PRIMARY KEY,
               delay_hours  INTEGER NOT NULL,
               updated_at   INTEGER NOT NULL
           )
       """)
       ```
       Idempotent — no seed INSERT (absence-of-row = use default; matches templates pattern from quick 260422-wmh).

    4. **`get_effective_schedule(job_type)` helper** (next to `_get_template_override` at line ~390):
       ```python
       def get_effective_schedule(job_type: str) -> int:
           """Return delay_hours for this job_type. Reads override row;
           falls back to DEFAULT_SCHEDULES; defends against missing table.
           """
           con = get_db()
           try:
               cur = con.cursor()
               try:
                   cur.execute(
                       "SELECT delay_hours FROM schedules WHERE job_type = ?",
                       (job_type,),
                   )
                   row = cur.fetchone()
                   if row and row["delay_hours"] is not None:
                       return int(row["delay_hours"])
               except sqlite3.OperationalError:
                   pass  # table missing in test bypass; fall through
           finally:
               con.close()
           return DEFAULT_SCHEDULES.get(job_type, 24)
       ```

    5. **Replace hardcoded literals at lines 2894-2909** with effective-schedule lookups:
       ```python
       if doc_status in ESTIMATE_STATUSES:
           h_24h  = get_effective_schedule("24h")
           h_3day = get_effective_schedule("3day")
           schedule_job(doc_id, "24h", phone, name,
                        next_send_window(now + h_24h*3600), ...)
           schedule_job(doc_id, "3day", phone, name,
                        next_send_window(now + h_3day*3600), ...)
       elif doc_status in CLOSED_STATUSES:
           h_review = get_effective_schedule("review")
           schedule_job(doc_id, "review", phone, name,
                        next_send_window(now + h_review*3600), ...)
       ```

    6. **Scheduler gate** at `scheduler_loop` (line 706):
       ```python
       def scheduler_loop():
           log.info("Scheduler started (SCHEDULER_ENABLED=%s)", SCHEDULER_ENABLED)
           global _last_gated_log_ts
           while True:
               try:
                   if SCHEDULER_ENABLED:
                       _fire_due_jobs()
                   else:
                       now = int(time.time())
                       if now - _last_gated_log_ts >= _GATED_LOG_INTERVAL_S:
                           log.info(
                               "scheduler gated off; SCHEDULER_ENABLED=0 "
                               "— manual send-now still works"
                           )
                           _last_gated_log_ts = now
               except Exception as exc:
                   log.error("Scheduler error: %s", exc)
               time.sleep(30)
       ```
       **Critically: do NOT gate `/queue/send-now` (line ~2806).** That code path bypasses `scheduler_loop` entirely and must keep firing on button-click. Add an inline comment at `/queue/send-now` reaffirming this so future readers don't move the gate.

    7. **GET /earlscheibconcord/schedules endpoint** in `do_GET` (next to the `/templates` GET at line 2357, mirroring its structure):
       ```python
       if path == "/earlscheibconcord/schedules":
           if not _validate_auth(self, b""):
               self._send_json(401, {"error": "invalid signature"})
               return
           con = get_db()
           overrides = {}
           try:
               cur = con.cursor()
               cur.execute("SELECT job_type, delay_hours, updated_at FROM schedules")
               for r in cur.fetchall():
                   overrides[r["job_type"]] = (r["delay_hours"], r["updated_at"])
           finally:
               con.close()
           job_types = []
           for meta in JOB_TYPE_META:
               jt = meta["job_type"]
               if jt in overrides:
                   delay, updated = overrides[jt]
                   job_types.append({
                       "job_type":    jt,
                       "label":       meta["label"],
                       "when":        meta["when"],
                       "delay_hours": int(delay),
                       "is_override": True,
                       "updated_at":  int(updated),
                   })
               else:
                   job_types.append({
                       "job_type":    jt,
                       "label":       meta["label"],
                       "when":        meta["when"],
                       "delay_hours": DEFAULT_SCHEDULES[jt],
                       "is_override": False,
                       "updated_at":  0,
                   })
           self._send_json(200, {
               "job_types": job_types,
               "min_hours": SCHEDULE_MIN_HOURS,
               "max_hours": SCHEDULE_MAX_HOURS,
           })
           return
       ```

    8. **PUT /earlscheibconcord/schedules/{job_type}** — extend the existing `do_PUT` method (line 2955) by adding a second prefix branch BEFORE the templates branch (so prefix-matching ordering is unambiguous):
       ```python
       sched_prefix = "/earlscheibconcord/schedules/"
       if path.startswith(sched_prefix):
           # ... auth, parse, validate job_type, validate delay_hours bounds,
           #     UPSERT or DELETE, then REBASE pending jobs (per D-02) ...
           # On UPSERT: also run
           #   UPDATE jobs SET send_at = ? WHERE job_type=? AND sent=0
           # for each pending row, computing
           #   next_send_window(created_at + delay_hours*3600)
           # in Python (next_send_window has timezone logic that's not
           # expressible in SQL). Loop over pending rows, compute new
           # send_at, executemany the updates, commit.
           # On DELETE (revert): same rebase but with delay = DEFAULT_SCHEDULES[jt].
           # Response shape: {"is_override": bool, "delay_hours": int,
           #                  "updated_at": int, "rebased_jobs": int}
           return
       ```
       Match the templates do_PUT structure (auth, JSON parse, validate, persist, log, respond). Validate `delay_hours` is an integer (reject `1.5`, `"24"`, `true`); reject `< SCHEDULE_MIN_HOURS` or `> SCHEDULE_MAX_HOURS`. Empty body or missing/null `delay_hours` field → DELETE row + rebase to default.

    9. **/diagnostic** (line 2448) — append `"scheduler_enabled": SCHEDULER_ENABLED` to the response dict (per D-04). Single-line additive change; no other changes to that handler.

    10. Create `tests/test_schedules_endpoint.py` — copy structure from `tests/test_templates_endpoint.py`, swap templates→schedules. Cover all behaviors enumerated above. Use the existing `queue_server` fixture from `tests/conftest.py` (it already reloads app.py with monkeypatched env).

    11. Create `tests/test_scheduler_gate.py` — new fixture variant (or `monkeypatch.setenv("SCHEDULER_ENABLED", "0"|"1")` + `importlib.reload(app)` per test) so module-level `SCHEDULER_ENABLED` constant picks up the value. Cover gate behavior + send-now bypass + once-per-hour log throttle.

    Do NOT touch: `internal/ems/*`, `cmd/earlscheib/*`, `internal/scanner/*`, the watcher binary, the installer, or any Go code that lives in earlscheib.exe. This change is server-side only.

    Per D-02: rebase logic uses `next_send_window` (not raw `created_at + delay`), so business-hours/weekend rules still apply after a delay change. Per D-03: gate default is `"0"` (off). Per D-05: schedules tab is appended after Templates, no insertion in the middle.
  </action>
  <verify>
    <automated>cd /home/jjagpal/projects/earl-scheib-followup &amp;&amp; python3 -m pytest tests/test_schedules_endpoint.py tests/test_scheduler_gate.py -x -q</automated>
  </verify>
  <done>
    All new pytest tests pass. `python3 -c "import app; print(app.DEFAULT_SCHEDULES, app.SCHEDULER_ENABLED, app.get_effective_schedule('24h'))"` returns `{'24h': 24, '3day': 72, 'review': 24} False 24`. The existing 35-passing test suite (templates + queue + scheduler + send-now) still passes (`pytest tests/ -q` regression — known pre-existing failure `test_get_queue_happy_path` excluded). Manual smoke: `curl -H "X-EMS-Signature: $(python3 -c '...')" http://localhost:8200/earlscheibconcord/schedules` returns 3 job_types with defaults; `curl -X PUT -d '{"delay_hours":48}' .../schedules/24h` returns `is_override:true, rebased_jobs:N`.
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Go admin proxy /api/schedules + /api/schedules/{job_type} routes</name>
  <files>internal/admin/proxy.go, internal/admin/server.go, internal/admin/admin_test.go</files>
  <behavior>
    Tests in `internal/admin/admin_test.go` (mirror the 8 templates proxy tests):
    - `TestAdminProxy_SchedulesList_Forwards`: GET /api/schedules → upstream sees GET /earlscheibconcord/schedules with HMAC over `[]byte("")` and no body. Response 200 forwarded verbatim.
    - `TestAdminProxy_ScheduleUpsert_Forwards`: PUT /api/schedules/24h with `{"delay_hours":48}` → upstream sees PUT /earlscheibconcord/schedules/24h with canonical-marshalled body `{"delay_hours":48}` and HMAC over those exact bytes.
    - `TestAdminProxy_ScheduleUpsert_RevertForwardsVerbatim`: PUT with `{}` (or `{"delay_hours":null}`) — body forwarded as-is (canonical re-marshal); upstream sees the empty/null payload.
    - `TestAdminProxy_ScheduleUpsert_UnknownJobType`: PUT /api/schedules/foo → 400, zero upstream calls (whitelist `{24h,3day,review}`).
    - `TestAdminProxy_ScheduleUpsert_EmptyJobType`: PUT /api/schedules/ → 400, zero upstream calls.
    - `TestAdminProxy_ScheduleUpsert_BadMethod`: POST /api/schedules/24h → 405.
    - `TestAdminProxy_SchedulesList_BadMethod`: PUT /api/schedules → 405.
    - `TestAdminProxy_ScheduleUpsert_PropagatesUpstream400`: when upstream returns 400 (e.g. delay out of bounds), proxy forwards the 400 + body verbatim.
  </behavior>
  <action>
    Edit `internal/admin/proxy.go`:

    1. Add `validScheduleJobTypes` map next to `validTemplateJobTypes` (line 219):
       ```go
       var validScheduleJobTypes = map[string]bool{
           "24h":    true,
           "3day":   true,
           "review": true,
       }
       ```

    2. Add `handleSchedulesList` — copy `handleTemplatesList` (line 230) verbatim; swap "templates"→"schedules" and call `s.remoteSchedulesURL()`.

    3. Add `handleScheduleUpsert` — copy `handleTemplateUpsert` (line 273); change body shape from `{Body string}` to `{DelayHours *int "json:\"delay_hours\""}` (pointer int so null/missing distinguishes from zero, matches the server-side revert semantic). Re-marshal canonically; validate jobType against `validScheduleJobTypes`; forward to `s.remoteScheduleURL(jobType)`. Body cap stays at 4 KiB (plenty for a single integer field).

    Edit `internal/admin/server.go`:

    4. After line 160 (`mux.HandleFunc("/api/templates/", s.handleTemplateUpsert)`):
       ```go
       mux.HandleFunc("/api/schedules", s.handleSchedulesList)
       mux.HandleFunc("/api/schedules/", s.handleScheduleUpsert)
       ```

    5. Add `remoteSchedulesURL` and `remoteScheduleURL` next to `remoteTemplatesURL` (line ~317):
       ```go
       func (s *server) remoteSchedulesURL() string {
           return strings.TrimSuffix(s.cfg.WebhookURL, "/") + "/earlscheibconcord/schedules"
       }
       func (s *server) remoteScheduleURL(jobType string) string {
           return s.remoteSchedulesURL() + "/" + jobType
       }
       ```

    Edit `internal/admin/admin_test.go`:

    6. Add the 8 test functions enumerated above. Mirror existing template proxy tests (`TestAdminProxy_TemplatesList_*`, `TestAdminProxy_TemplatePut_*`) — they're the canonical pattern: spin a fake-upstream `httptest.Server` that records request method + path + body + signature, then call the proxy and assert.

    Per the constraints: do NOT touch `internal/admin/admin.go` (it's a thin entry-point file) — the proxy + server.go split is the right surface. (If `admin.go` happens to contain the route registration in this codebase rather than `server.go`, register the routes there instead — check first; the listed file in the prompt's `<files_modified>` includes both as a hedge.)

    Per D-02: this proxy does NOT need to know about the rebase — it's a passthrough. Rebase happens server-side on the PUT handler in app.py.
  </action>
  <verify>
    <automated>cd /home/jjagpal/projects/earl-scheib-followup &amp;&amp; go test ./internal/admin/... -run 'Schedules|Schedule' -count=1</automated>
  </verify>
  <done>
    All 8 new Go proxy tests pass. `go vet ./...` clean. `go build ./...` succeeds. Existing 8 templates proxy tests still pass (`go test ./internal/admin/... -count=1`). `grep -n "validScheduleJobTypes\|handleSchedulesList\|handleScheduleUpsert" internal/admin/proxy.go` shows all three; `grep -n "/api/schedules" internal/admin/server.go` shows both routes registered.
  </done>
</task>

<task type="auto">
  <name>Task 3: Schedules tab UI (topnav entry, card template, JS render+save+rebase, dev-mode banner) + sync-ui</name>
  <files>internal/admin/ui/index.html, internal/admin/ui/main.js, internal/admin/ui/main.css, ui_public/index.html, ui_public/main.js, ui_public/main.css</files>
  <action>
    Mirror the Templates tab UI exactly (per quick 260422-wmh) — same card pattern, same dirty-dot, same Save + Reset, same debounced live preview. Just swap "textarea body" for "numeric input + (X days) helper".

    Edit `internal/admin/ui/index.html`:

    1. **Topnav** (line 27) — append a third link AFTER Templates (per D-05):
       ```html
       <a class="topnav-link" data-view="schedules" href="#schedules" role="tab" aria-selected="false">Schedules</a>
       ```

    2. **Dev-mode banner** — inject as the first child of `<body>`, BEFORE `.app-shell`, so it sits above all content on every tab (per D-04):
       ```html
       <div id="dev-banner" class="dev-banner" hidden role="status" aria-live="polite">
         <strong>DEV MODE</strong> — scheduler off (enable with <code>SCHEDULER_ENABLED=1</code>).
         Manual send-now still works.
       </div>
       ```

    3. **Schedules view wrapper** — after the closing `</div>` of `#view-templates` (line ~190):
       ```html
       <div id="view-schedules" data-view="schedules" hidden>
         <header class="schedules__header">
           <h1>Send Schedules</h1>
           <p class="schedules__hint">
             Delay between event and SMS — saved instantly. Pending jobs are rebased to the new delay.
           </p>
         </header>
         <div id="schedules-list" class="schedules__list" aria-live="polite"></div>
       </div>
       ```

    4. **`<template id="schedule-card-template">`** — next to `template-card-template` (line 184):
       ```html
       <template id="schedule-card-template">
         <article class="sched-card" data-job-type="">
           <header class="sched-card__head">
             <h2 class="sched-card__title"></h2>
             <span class="sched-card__when"></span>
             <span class="sched-card__badge" hidden>Custom</span>
             <span class="sched-card__dirty-dot" hidden aria-label="Unsaved changes"></span>
           </header>
           <div class="sched-card__row">
             <label class="sched-card__label" for="">Delay (hours)</label>
             <input type="number" class="sched-card__input mono" min="1" max="720" step="1" inputmode="numeric" />
             <span class="sched-card__days mono"></span>
           </div>
           <footer class="sched-card__actions">
             <button class="btn btn--primary sched-card__save" type="button">Save</button>
             <button class="btn btn--ghost   sched-card__reset" type="button">Reset to default</button>
             <span class="sched-card__status" role="status" aria-live="polite"></span>
           </footer>
         </article>
       </template>
       ```

    Edit `internal/admin/ui/main.js`:

    5. **`hoursToDaysLabel(h)` helper** — next to existing helpers near line 95:
       ```js
       function hoursToDaysLabel(h) {
         if (!Number.isFinite(h) || h <= 0) return '';
         const days = Math.round((h / 24) * 10) / 10;  // one decimal, per locked decision D-01
         const word = days === 1 ? 'day' : 'days';
         return `(${days} ${word})`;
       }
       ```

    6. **`loadSchedules(force)` function** — next to `loadSchedules`'s sibling `loadTemplates` (line 851). Mirror its structure: fetch `${API_BASE}/schedules`, store effective values in a local `effectiveSchedules = {}`, render one `.sched-card` per `job_type` from `data.job_types`. Each card:
       - Set `data-job-type`, `__title`=`label`, `__when`=`when`, `__badge` shown when `is_override`, input value=`delay_hours`, `__days` text from `hoursToDaysLabel(delay_hours)`.
       - Save button → PUT `${API_BASE}/schedules/${jt}` with `{"delay_hours": parseInt(input.value, 10)}`. On 200: clear dirty-dot, refresh badge from `is_override`, set status "Saved · N pending jobs rebased" using the response's `rebased_jobs` count, debounced 2s clear.
       - Reset button → PUT same URL with `{}` (revert). On 200: input.value = response `delay_hours` (the default), badge hidden, status "Reverted to default · N rebased".
       - Input event → recompute `__days` text live + toggle dirty-dot when value differs from saved snapshot.
       - Bounds enforcement client-side: reject save if `< 1` or `> 720`; show inline status "Must be 1–720 hours" without hitting the server.

    7. **Tab swap extension** (line 1118) — add a third branch:
       ```js
       const viewSched = document.getElementById('view-schedules');
       // ...inside the loop that toggles `hidden` per data-view...
       if (target === 'schedules') loadSchedules(false);
       ```
       Also add `viewSched.hidden = (target !== 'schedules');` to the visibility toggle alongside queue/templates.

    8. **Dev-mode banner toggle** — extend the existing diagnostic-fetch handler (around line 631 `const resp = await fetch(${API_BASE}/diagnostic, ...)`):
       ```js
       const banner = document.getElementById('dev-banner');
       if (banner) banner.hidden = data.scheduler_enabled !== false;
       // shown only when scheduler_enabled === false; hidden when true
       // (so banner stays hidden during the brief moment before the first
       // /diagnostic fetch resolves).
       ```
       The banner status updates on every `/diagnostic` poll automatically.

    Edit `internal/admin/ui/main.css`:

    9. Add `.dev-banner` styles — full-width oxblood-on-cream stripe across the top, generous padding (top: 12px, horizontal: 24px), bold label, `<code>` rendered in IBM Plex Mono. Use existing palette CSS vars (`--color-oxblood`, `--color-cream`). No motion / no animations (per CLAUDE.md design discipline plus the user wants this to be informational, not flashy).

    10. Add `.sched-card*` styles — copy `.tpl-card` rules and adapt. The new selectors: `.sched-card`, `.sched-card__head`, `.sched-card__title`, `.sched-card__when`, `.sched-card__badge`, `.sched-card__dirty-dot`, `.sched-card__row` (flex layout: label / input / days), `.sched-card__label`, `.sched-card__input` (60-80px wide, right-aligned mono), `.sched-card__days` (muted IBM Plex Mono), `.sched-card__actions`, `.sched-card__save`, `.sched-card__reset`, `.sched-card__status`. Reuse the dirty-dot styles by adding `.sched-card__dirty-dot` to the existing `.tpl-card__dirty-dot` selector list (DRY) — NOT a copy.

    11. Add `.schedules__header` + `.schedules__hint` + `.schedules__list` styles — mirror `.templates__*` if those exist, else add as a fresh section block.

    12. Run `make sync-ui` to copy the three updated files from `internal/admin/ui/` to `ui_public/`. The Makefile target already handles this.

    Per the constraints: do NOT modify any UI behavior on existing Queue or Templates tabs except for (a) adding the third topnav link and (b) the dev-banner toggle in the diagnostic handler. Both are additive.

    **CLAUDE.md design check**: bold aesthetic. The Templates editor already established the LIGHT Fraunces palette + oxblood accents (per quick 260422-oh4). Schedules should be invisible-by-design — three rows of clean numeric inputs with the parenthetical day-helper acting as the only non-functional flourish. Restraint is the design choice. No animations on save (status text is enough). No purple gradients, no SaaS blues — keep the oxblood-on-cream discipline.
  </action>
  <verify>
    <automated>cd /home/jjagpal/projects/earl-scheib-followup &amp;&amp; make sync-ui &amp;&amp; diff -rq internal/admin/ui ui_public 2>&amp;1 | grep -v 'README\|gitkeep\|logo\.png' || true</automated>
  </verify>
  <done>
    `diff -rq internal/admin/ui ui_public` (excluding README/gitkeep/logo.png) prints no differences (sync-ui ran). `grep -n 'data-view="schedules"\|schedule-card-template\|dev-banner\|sched-card' internal/admin/ui/index.html ui_public/index.html` shows matching hits in both trees. `grep -n 'loadSchedules\|hoursToDaysLabel\|scheduler_enabled' internal/admin/ui/main.js ui_public/main.js` confirms the JS landed. Manual smoke (start app.py + open http://localhost:8200/earlscheib): Queue / Templates / Schedules tabs visible; clicking Schedules loads 3 cards (24h=24, 3day=72, review=24) with correct day labels (1 day, 3 days, 1 day); editing 24h to 36 shows "(1.5 days)" live; saving returns rebased count; banner is visible when SCHEDULER_ENABLED unset, hidden when SCHEDULER_ENABLED=1. Existing Queue and Templates tabs work unchanged.
  </done>
</task>

</tasks>

<verification>
**Phase-level checks** (run after all 3 tasks complete):

1. **Python tests** — full suite green except known pre-existing failures:
   ```bash
   cd /home/jjagpal/projects/earl-scheib-followup
   python3 -m pytest tests/ -q
   ```
   Expect: all new schedule + scheduler-gate tests pass; the pre-existing
   `test_get_queue_happy_path` failure documented in the WMH SUMMARY may
   still fail — that's a separate known issue, not introduced here.

2. **Go tests** — full suite green:
   ```bash
   make test
   ```
   Expect: all 8 new admin-proxy tests pass; existing templates + queue
   proxy tests pass; scanner tests pass (note `TestRunSettleSkip` is
   timing-flaky and unrelated).

3. **UI sync** — byte-identical:
   ```bash
   diff -rq internal/admin/ui ui_public 2>&1 | grep -v -E 'README|gitkeep|logo\.png'
   ```
   Expect: silent.

4. **Build** — Go and installer-syntax green:
   ```bash
   go build ./...
   go vet ./...
   ```
   Expect: clean.

5. **End-to-end smoke** (run app.py locally):
   - `GET /earlscheibconcord/schedules` (HMAC-signed empty body) → 3 job_types with defaults.
   - `PUT /earlscheibconcord/schedules/24h {"delay_hours":48}` → 200 with `is_override:true, rebased_jobs:N`.
   - `PUT /earlscheibconcord/schedules/24h {}` → 200 with `is_override:false, delay_hours:24`.
   - `PUT /earlscheibconcord/schedules/24h {"delay_hours":0}` → 400.
   - `PUT /earlscheibconcord/schedules/24h {"delay_hours":721}` → 400.
   - `GET /earlscheibconcord/diagnostic` → response includes `scheduler_enabled` boolean.
   - With SCHEDULER_ENABLED unset, browser at /earlscheib shows the dev banner; with SCHEDULER_ENABLED=1, banner is hidden.
   - With SCHEDULER_ENABLED=0 + a due job in the queue, wait 60s; row stays sent=0; click Send-now button → row fires + becomes sent=1 (gate doesn't block manual sends).

6. **Untouched-by-design verification** — `git diff --stat` after all 3 tasks should show ZERO changes outside this list:
   - `app.py`
   - `internal/admin/proxy.go`
   - `internal/admin/server.go`
   - `internal/admin/admin_test.go`
   - `internal/admin/ui/{index.html,main.js,main.css}`
   - `ui_public/{index.html,main.js,main.css}`
   - `tests/test_schedules_endpoint.py` (new)
   - `tests/test_scheduler_gate.py` (new)

   In particular: `git diff --stat -- internal/ems internal/scanner cmd/earlscheib internal/installer internal/heartbeat internal/remoteconfig internal/telemetry internal/update` MUST be empty. The watcher binary, the installer, and any code Marco's machine runs are NOT touched by this plan. **No installer rebuild required after this change.**

</verification>

<success_criteria>
- [ ] Three tasks complete in order (1 → 2 → 3); each commits independently with conventional-commit messages (`feat(schedules): ...`).
- [ ] All new pytest tests pass (`tests/test_schedules_endpoint.py`, `tests/test_scheduler_gate.py`).
- [ ] All new Go admin-proxy tests pass (8 in `internal/admin/admin_test.go`).
- [ ] `make test` is fully green (Python + Go), modulo the documented pre-existing test_queue happy_path failure.
- [ ] `make sync-ui` runs clean; `diff -rq internal/admin/ui ui_public` is silent on tracked files.
- [ ] Manual end-to-end smoke against running app.py confirms: 3 schedule cards visible, day-label computes correctly, save+rebase works, dev-mode banner toggles correctly per `SCHEDULER_ENABLED`.
- [ ] **Marco-side untouched: ZERO changes to `internal/ems/*`, `cmd/earlscheib/*`, `internal/scanner/*`, or any other code that gets compiled into `EarlScheibWatcher.exe`. No installer rebuild required.**
- [ ] **Manual send-now still fires sends with SCHEDULER_ENABLED=0 (verified by test + manual smoke).**
- [ ] **Pending jobs are rebased on schedule change** — verified by the `test_put_schedule_rebases_pending_jobs` test.
</success_criteria>

<output>
After completion, create `.planning/quick/260508-spn-schedules-editor-scheduler-disable-flag/260508-spn-SUMMARY.md` describing:

1. What landed (server: schedules table + endpoints + gate + diagnostic field; proxy: 2 routes; UI: tab + banner; tests: 2 new test files).
2. Key decisions confirmed during execution (per D-01..D-05 in the planning context).
3. Test inventory (counts: new Python tests, new Go tests).
4. Commits (3 expected, one per task — `feat(schedules): server-side table + endpoints + scheduler gate`, `feat(schedules): admin proxy routes`, `feat(schedules): UI tab + dev-mode banner`).
5. Verification evidence (pytest + go test outputs, sync-ui diff, smoke curl results).
6. **Explicit "what we did NOT touch" list**: `internal/ems/*`, `cmd/earlscheib/*`, `internal/scanner/*`, `internal/installer/*`, `internal/heartbeat/*`, `internal/remoteconfig/*`, `internal/telemetry/*`, `internal/update/*`, `Makefile` (no installer/release-prep changes), `EarlScheibWatcher-Setup.exe` (no rebuild). Confirms Marco's machine doesn't need an update for this change.
7. Self-check: per the "Self-Check: PASSED" pattern in the WMH summary.

Then update `.planning/STATE.md` to log this quick task entry in the Quick Tasks Completed table.
</output>
