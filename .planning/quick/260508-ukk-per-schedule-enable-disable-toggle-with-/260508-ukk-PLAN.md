---
phase: quick
plan: 260508-ukk
type: execute
wave: 1
depends_on:
  - 260508-spn  # this plan extends the schedules table + endpoints + UI
files_modified:
  - app.py
  - tests/test_schedules_endpoint.py
  - tests/test_scheduler_gate.py
  - internal/admin/proxy.go
  - internal/admin/admin_test.go
  - internal/admin/ui/index.html
  - internal/admin/ui/main.js
  - internal/admin/ui/main.css
  - ui_public/index.html
  - ui_public/main.js
  - ui_public/main.css
autonomous: true
requirements:
  - UKK-01  # schedules.enabled column with idempotent ALTER migration; default 1 (enabled)
  - UKK-02  # GET /schedules includes `enabled` field per row
  - UKK-03  # PUT /schedules/{jt} accepts {enabled, delay_hours} independently; revert keeps enabled=true (default)
  - UKK-04  # On toggle-off: cancel all pending jobs of that job_type (sent=0 → sent=1, sent_at=now); on toggle-on do NOT resurrect
  - UKK-05  # ems_bundle ingestion path skips schedule_job() for disabled job_types
  - UKK-06  # Go admin proxy round-trips `enabled` through {"delay_hours": ?, "enabled": ?}
  - UKK-07  # Schedules tab UI toggle switch per card (Fraunces/oxblood) — same row as hours input; dimmed disabled state with "Disabled" label; hours input + Save remain active
  - UKK-08  # Toggle-off shows status echo "Disabled · N pending jobs cancelled"; toggle-on shows "Enabled" with no resurrection language

must_haves:
  truths:
    - "Marco sees a toggle switch on each of the three schedule cards (24h, 3day, review) and can flip each independently."
    - "When Marco toggles 24h OFF, all pending sent=0 jobs of job_type='24h' are immediately cancelled (marked sent=1 with sent_at=now)."
    - "After 24h is toggled OFF, a fresh ems_bundle POST that would normally schedule a 24h job NO LONGER schedules one (3day still schedules if estimate)."
    - "When Marco toggles 24h back ON, previously cancelled 24h jobs DO NOT resurrect — they remain sent=1."
    - "When Marco toggles 24h back ON, the NEXT ems_bundle POST schedules a fresh 24h job at the configured delay."
    - "Disabled cards visually dim with a 'Disabled' label, but the hours input + Save button stay active so Marco can pre-tune the delay before re-enabling."
    - "Default state for all three job_types is enabled=true — no user-visible behavior change for an operator who never touches the toggles."
    - "GET /earlscheibconcord/schedules returns `enabled: bool` for each job_type."
  artifacts:
    - path: "app.py"
      provides: "schedules.enabled column + ALTER-IF-MISSING migration; get_schedule_enabled() helper; GET response includes enabled; PUT accepts enabled (independent of delay_hours); toggle-off cancels pending; ems_bundle handler skips schedule_job for disabled job_types"
      contains: "get_schedule_enabled"
    - path: "tests/test_schedules_endpoint.py"
      provides: "tests for default-enabled, GET enabled flag, PUT enabled toggle round-trip, PUT toggle-off cancels pending, PUT toggle-on does not resurrect, PUT enabled+delay independence, PUT enabled type-validation (bool only), revert keeps enabled=true"
      contains: "test_put_schedule_toggle_off_cancels_pending"
    - path: "tests/test_scheduler_gate.py"
      provides: "test that ems_bundle handler skips schedule_job when job_type is disabled (24h disabled → only 3day scheduled on estimate POST; review disabled → no review scheduled on closed POST)"
      contains: "test_ems_bundle_skips_disabled_job_type"
    - path: "internal/admin/proxy.go"
      provides: "handleScheduleUpsert struct extended with Enabled *bool; canonical re-marshal includes both fields"
      contains: "Enabled *bool"
    - path: "internal/admin/admin_test.go"
      provides: "round-trip tests for {enabled} alone, {delay_hours} alone, {enabled, delay_hours} together, {} (revert) — verifying canonical body forwarding"
      contains: "TestAdminProxy_ScheduleUpsert_EnabledRoundTrip"
    - path: "internal/admin/ui/index.html"
      provides: "Toggle switch markup added to schedule-card-template (.sched-card__toggle, .sched-card__toggle-input, .sched-card__toggle-track, .sched-card__toggle-label)"
      contains: "sched-card__toggle"
    - path: "internal/admin/ui/main.js"
      provides: "scheduleState.cards[jt].enabled tracked; buildScheduleCard renders toggle initial state; toggle change handler PUTs {enabled: bool}; applySavedSchedule applies card disabled-class + status echo with cancelled count"
      contains: "sched-card__toggle"
    - path: "internal/admin/ui/main.css"
      provides: ".sched-card.is-disabled dim styling; .sched-card__toggle (track + thumb) using oxblood-on-cream palette, NO animations"
      contains: ".sched-card__toggle"
  key_links:
    - from: "app.py do_POST(?trigger=ems_bundle) — ESTIMATE branch"
      to: "schedule_job (24h, 3day)"
      via: "if get_schedule_enabled(jt): schedule_job(...) else: skip"
      pattern: "get_schedule_enabled"
    - from: "app.py do_POST(?trigger=ems_bundle) — CLOSED branch"
      to: "schedule_job (review)"
      via: "if get_schedule_enabled('review'): schedule_job(...) else: skip"
      pattern: "get_schedule_enabled"
    - from: "app.py _do_put_schedule on toggle-off"
      to: "jobs table (cancel)"
      via: "UPDATE jobs SET sent=1, sent_at=? WHERE job_type=? AND sent=0"
      pattern: "UPDATE jobs SET sent=1, sent_at"
    - from: "internal/admin/ui/main.js toggle change handler"
      to: "PUT /api/schedules/{jt}"
      via: "fetch with body {enabled: bool}"
      pattern: 'JSON.stringify\\(\\{ enabled:'
---

<objective>
Extend the just-shipped Schedules tab (260508-spn) with a per-schedule
**enable/disable toggle**. Each of the three cards (24h, 3day, review) gets a
toggle switch in the same row as the hours input. Toggle behavior:

- **Toggle OFF (per locked decision 1):** future ems_bundle POSTs skip
  `schedule_job` for that job_type, AND existing pending jobs of that
  job_type are cancelled (`sent=0` → `sent=1` with `sent_at=now`, mirroring
  the cancellation pattern from quick 260508-q9c).
- **Toggle ON:** future ems_bundle POSTs resume scheduling for that job_type
  at the configured delay. Previously cancelled jobs are NOT resurrected
  (consistent with the rebase-on-delay-change pattern already shipped — we
  never reach into sent rows).
- **Default (per locked decision 2):** all three schedules enabled. Operators
  who never touch the toggle see zero behavior change.

Purpose: Marco wants to pause individual follow-ups (e.g. mute 3-day
check-ins for a week without disabling the whole scheduler) without code
changes or env-var fiddling. SCHEDULER_ENABLED stays the kill-switch for the
whole loop; this is per-job-type granularity on top of it.

Output: `schedules.enabled` column (idempotent ALTER migration); GET/PUT
endpoints round-trip the `enabled` field; ems_bundle handler gates
`schedule_job` per job_type; UI toggle switch with dimmed-disabled state;
proxy passthrough; tests for cancel-on-disable + no-resurrection-on-enable +
ingestion skip.

This is server-side + admin-UI only. Marco's `EarlScheibWatcher.exe` does
NOT need an installer rebuild.
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
@$HOME/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/quick/260508-spn-schedules-editor-scheduler-disable-flag/260508-spn-PLAN.md
@.planning/quick/260508-spn-schedules-editor-scheduler-disable-flag/260508-spn-SUMMARY.md
@CLAUDE.md
@app.py
@internal/admin/proxy.go
@internal/admin/admin_test.go
@internal/admin/ui/index.html
@internal/admin/ui/main.js
@internal/admin/ui/main.css
@tests/test_schedules_endpoint.py
@tests/test_scheduler_gate.py
@tests/conftest.py
@Makefile

<interfaces>
<!-- Key contracts the executor must mirror — extracted from existing code. -->
<!-- Do NOT explore the codebase to "find the schedules pattern" — it's all here. -->

### app.py — schedules table CREATE (line ~369, already shipped)

```python
cur.execute(
    """
    CREATE TABLE IF NOT EXISTS schedules (
        job_type    TEXT PRIMARY KEY,
        delay_hours INTEGER NOT NULL,
        updated_at  INTEGER NOT NULL
    )
    """
)
```

This plan ADDS `enabled INTEGER NOT NULL DEFAULT 1` via idempotent ALTER
(see Task 1 action — `PRAGMA table_info(schedules)` then `ALTER TABLE ... ADD COLUMN`).
Do NOT modify the original CREATE in place — the column appears via ALTER so
the existing single row created during 260508-spn execution gets the
DEFAULT 1 retroactively (SQLite applies the DEFAULT clause to existing rows
when adding a NOT NULL column with a DEFAULT).

### app.py — get_effective_schedule helper (line 425, already shipped)

```python
def get_effective_schedule(job_type: str) -> int:
    # Returns delay_hours; falls back to DEFAULT_SCHEDULES.
```

This plan ADDS a sibling helper next to it:

```python
def get_schedule_enabled(job_type: str) -> bool:
    """Return whether this job_type is enabled. Default True (enabled)
    when no override row exists OR the column is somehow missing."""
```

Same defensive pattern: try table read; on `sqlite3.OperationalError`
(table or column missing — relevant during conftest fixture reset),
return True.

### app.py — GET /schedules response shape (line 2580, already shipped)

```python
self._send_json(200, {
    "job_types": [
        {"job_type": "24h", "label": "...", "when": "...",
         "delay_hours": 24, "is_override": False, "updated_at": 0},
        ...
    ],
    "min_hours": 1,
    "max_hours": 720,
})
```

After this plan: each job_types entry gains `"enabled": True|False`.
`is_override` should reflect EITHER an override of delay_hours OR an
enabled override (i.e., enabled=False on default-delay still counts as
"is_override=True" — the row exists in the schedules table). Concretely:
`is_override = (row exists in schedules table)`.

### app.py — _do_put_schedule (line 3246, already shipped)

Currently parses `{"delay_hours": N | null}`. The revert path (empty body /
null delay_hours) DELETEs the row. After this plan:

- Body shape: `{"delay_hours": N|null|absent, "enabled": bool|null|absent}`.
- Both fields independently optional. Examples:
  - `{}` → revert delay AND set enabled=true (full revert)
  - `{"delay_hours": 48}` → upsert delay; enabled stays at current persisted value (or true if no row)
  - `{"enabled": false}` → upsert enabled=false; delay_hours stays at current persisted value (or default if no row)
  - `{"enabled": true, "delay_hours": 48}` → upsert both
- **CRITICAL semantics for revert:** `{}` only fully reverts (DELETE row).
  Partial fields → UPSERT. To delete a row, the operator must clear ALL
  overrides — empty body = "reset to defaults".
- Type validation: `enabled` must be a `bool` (reject string/int/null-when-present-but-explicit-non-bool).
  Use the same `isinstance(val, bool)` ordering as delay_hours validation.
- After UPSERT with `enabled=False` (transition to disabled): cancel all
  pending jobs of this job_type via:
  ```python
  cur.execute(
      "UPDATE jobs SET sent=1, sent_at=? WHERE job_type=? AND sent=0",
      (now_ts, job_type),
  )
  cancelled = cur.rowcount
  ```
  Mirrors the cancellation pattern documented in 260508-q9c-SUMMARY (false-
  positive RO cancellation).
- After UPSERT with `enabled=True` (or transition to enabled): DO NOTHING
  to existing jobs. Per locked decision 1: cancelled jobs do NOT resurrect.
  But if `delay_hours` was also changed in the same PUT, the existing
  rebase-pending-jobs logic still runs (touches sent=0 rows only — which
  exist again only because a fresh ems_bundle POST will create them after
  re-enable).

**Mutual-exclusion of cancel vs rebase:** if the PUT toggles enabled=False,
SKIP the rebase loop (there are no pending rows after cancel anyway, but
explicitly skipping makes intent clear). If enabled=True (or unchanged),
run the rebase loop on delay_hours change as before.

Response shape extension: add `"enabled": bool` and `"cancelled_jobs": int`
(0 unless toggled-off this PUT). Keep `"rebased_jobs"` as-is.

### app.py — ems_bundle gating (lines 3045-3068, already partially gated)

Current code:
```python
if doc_status in ESTIMATE_STATUSES:
    h_24h  = get_effective_schedule("24h")
    h_3day = get_effective_schedule("3day")
    schedule_job(doc_id, "24h", phone, name, next_send_window(now + h_24h*3600), ...)
    schedule_job(doc_id, "3day", phone, name, next_send_window(now + h_3day*3600), ...)
elif doc_status in CLOSED_STATUSES:
    h_review = get_effective_schedule("review")
    schedule_job(doc_id, "review", phone, name, next_send_window(now + h_review*3600), ...)
```

After this plan, each `schedule_job` call is guarded by
`if get_schedule_enabled("<jt>"):`. When False, log
`log.info("schedules: %s disabled — skipping schedule_job for doc_id=%s", jt, doc_id)`
and continue. The other job_types in the same branch must still schedule
(disabling 24h must NOT block 3day on the same estimate).

### internal/admin/proxy.go — handleScheduleUpsert (line 403, already shipped)

Current parser:
```go
var parsed struct {
    DelayHours *int `json:"delay_hours"`
}
```

After this plan:
```go
var parsed struct {
    DelayHours *int  `json:"delay_hours"`
    Enabled    *bool `json:"enabled"`
}
```

And the canonical re-marshal struct gains `Enabled *bool` with the same
omitempty-by-pointer-nil convention. This means: missing field upstream
arrives as `null`, which app.py's `_do_put_schedule` treats as "field
absent / no change". Empty body still arrives as canonical
`{"delay_hours":null,"enabled":null}` — a full revert.

Body cap stays at 4 KiB (still trivially small). job_type whitelist
unchanged.

### internal/admin/ui/index.html — schedule-card-template (line ~235, already shipped)

Current row layout:
```html
<div class="sched-card__row">
  <label class="sched-card__label">Delay</label>
  <input type="number" class="sched-card__input mono" min="1" max="720" step="1" inputmode="numeric" />
  <span class="sched-card__unit">hours</span>
  <span class="sched-card__days mono"></span>
</div>
```

After this plan, ADD a sibling toggle row INSIDE the same `<header>` area
(right of the title, before the badge/dirty-dot) so the toggle reads as a
card-level state, NOT a per-input switch. Concrete shape:

```html
<header class="sched-card__head">
  <h2 class="sched-card__title"></h2>
  <span class="sched-card__when"></span>
  <label class="sched-card__toggle">
    <input type="checkbox" class="sched-card__toggle-input" />
    <span class="sched-card__toggle-track" aria-hidden="true"></span>
    <span class="sched-card__toggle-label">Enabled</span>
  </label>
  <span class="sched-card__badge" hidden>Custom</span>
  <span class="sched-card__dirty-dot" hidden aria-label="Unsaved changes"></span>
</header>
```

Toggle label flips between "Enabled" and "Disabled" via JS based on
checkbox `.checked`.

### internal/admin/ui/main.js — buildScheduleCard (line 1204, already shipped)

After fetching `data.job_types`, each `jt` now also has `enabled: bool`.
- Set `toggleInput.checked = !!jt.enabled`.
- Set `toggleLabel.textContent = jt.enabled ? 'Enabled' : 'Disabled'`.
- Add/remove `is-disabled` class on the article based on `!jt.enabled`.
- On `toggleInput.change`: PUT `{enabled: <new bool>}` immediately (no
  Save button needed for the toggle — it's a direct action).
  - On success: update `scheduleState.cards[jt].enabled`, update label
    text, dim/undim card, status echo:
    - off: `Disabled · N pending job${plural} cancelled` (using
      `parsed.cancelled_jobs`)
    - on: `Enabled` (no rebased/cancelled count; new bundles take it from
      here)
  - On failure: revert checkbox to old state, show error in status echo.

The hours input + Save + Reset stay active even when disabled (per locked
decision 3) — Marco pre-tunes delay before flipping the toggle on. Visual
dim is opacity ~0.6 on the row; controls remain interactive.

### internal/admin/ui/main.css — palette + no-animation rule

Use existing CSS vars: `--terra` (oxblood) for active/on, `--ink-soft` for
inactive/off, `--cream` for track background. NO transitions on the toggle
thumb (per CLAUDE.md "no animations" + locked decision restraint). The
toggle is a CSS-only switch:

- Wrap the checkbox: `.sched-card__toggle-input { position: absolute; opacity: 0; }`
- Track: `.sched-card__toggle-track { width: 36px; height: 20px; border-radius: 10px; background: var(--ink-soft); position: relative; }`
- Thumb: `.sched-card__toggle-track::after { content: ''; width: 16px; height: 16px; border-radius: 50%; background: var(--cream); position: absolute; top: 2px; left: 2px; }`
- Active state: `.sched-card__toggle-input:checked ~ .sched-card__toggle-track { background: var(--terra); }`
- Active thumb: `.sched-card__toggle-input:checked ~ .sched-card__toggle-track::after { left: 18px; }`
- Focus: visible focus ring on the track when the input has `:focus-visible`.
- Disabled card dim: `.sched-card.is-disabled { opacity: 0.6; }` — overall
  card visual cue. Inputs inside stay fully opaque (override:
  `.sched-card.is-disabled .sched-card__input, .sched-card.is-disabled .sched-card__row { opacity: 1; }`).

### tests/conftest.py — queue_server fixture

Already provides a fully-configured app.py instance with HMAC + Basic auth
keys, isolated DB, and monkeypatched env. Reuse exactly as in
`tests/test_schedules_endpoint.py`. The fixture re-runs `init_db()` per
test, so the new ALTER migration must run as part of init_db (not in a
separate run-once shim).

### Cancellation pattern (from 260508-q9c-SUMMARY)

```python
cur.execute(
    "UPDATE jobs SET sent=1, sent_at=? WHERE job_type=? AND sent=0",
    (int(time.time()), job_type),
)
cancelled = cur.rowcount
```

This is the canonical "cancel a pending job" idiom. We mirror it here for
the toggle-off cancel batch. Must also `con.commit()` after.

</interfaces>

</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Server — schedules.enabled column + GET/PUT extension + ems_bundle gating + cancel-on-disable</name>
  <files>app.py, tests/test_schedules_endpoint.py, tests/test_scheduler_gate.py</files>
  <behavior>
    NEW tests in `tests/test_schedules_endpoint.py` (append; do not edit existing tests):

    - `test_get_schedules_includes_enabled_default_true`:
      GET /earlscheibconcord/schedules with no overrides → all 3 entries
      have `enabled: true, is_override: false`.

    - `test_put_schedule_toggle_off_persists`:
      PUT /schedules/24h `{"enabled": false}` → response includes
      `enabled: false, is_override: true`. Subsequent GET shows
      `enabled: false, is_override: true` for 24h, `enabled: true` for the
      other two.

    - `test_put_schedule_toggle_off_cancels_pending`:
      Seed 3 pending sent=0 rows with job_type='24h', 1 pending sent=0 row
      with job_type='3day', 1 already-sent (sent=1) row with job_type='24h'.
      PUT /schedules/24h `{"enabled": false}`. Assert response
      `cancelled_jobs == 3`. Assert DB:
        - 3 cancelled rows: sent=1, sent_at>=now-5 (set by handler)
        - 3day row: still sent=0
        - already-sent 24h row: sent=1, sent_at unchanged (was set earlier)

    - `test_put_schedule_toggle_on_does_not_resurrect`:
      Seed 1 pending sent=0 row 24h. PUT `{"enabled": false}` (cancels).
      Then PUT `{"enabled": true}`. Assert response
      `cancelled_jobs == 0`. Assert DB: row still sent=1
      (cancellation persists; toggle-on does not bring it back).

    - `test_put_schedule_enabled_only_does_not_change_delay`:
      PUT `{"delay_hours": 48}` (override delay). PUT `{"enabled": false}`.
      Subsequent GET shows `delay_hours: 48, enabled: false`.

    - `test_put_schedule_delay_only_does_not_change_enabled`:
      PUT `{"enabled": false}`. PUT `{"delay_hours": 48}`. Subsequent GET
      shows `delay_hours: 48, enabled: false`.

    - `test_put_schedule_both_fields_together`:
      PUT `{"delay_hours": 48, "enabled": false}`. Subsequent GET shows
      both. Response shape includes `cancelled_jobs >= 0` AND
      `rebased_jobs == 0` (no rebase when disabled — pending jobs were
      cancelled in the same call).

    - `test_put_schedule_empty_body_reverts_both`:
      PUT `{"delay_hours": 48, "enabled": false}` (set both). PUT `{}`.
      Subsequent GET shows defaults: `delay_hours: 24, enabled: true,
      is_override: false`.

    - `test_put_schedule_enabled_string_rejected`:
      PUT `{"enabled": "false"}` → 400.

    - `test_put_schedule_enabled_int_rejected`:
      PUT `{"enabled": 0}` → 400 (integer rejected even though it's
      truthy/falsy in Python — bool-only).

    - `test_put_schedule_enabled_null_treated_as_absent`:
      Seed enabled=false. PUT `{"delay_hours": 48, "enabled": null}` →
      enabled stays false (null = "field absent / no change").

    - `test_put_schedule_toggle_off_then_delay_change_does_not_rebase`:
      Seed 1 sent=0 24h job. PUT `{"enabled": false, "delay_hours": 48}`.
      Assert `cancelled_jobs == 1, rebased_jobs == 0`.

    NEW tests in `tests/test_scheduler_gate.py` (append):

    - `test_ems_bundle_skips_disabled_24h`:
      Disable 24h via direct schedules table INSERT. POST a synthetic
      ems_bundle with an ESTIMATE doc_status. Assert: 0 jobs created with
      job_type='24h', exactly 1 job created with job_type='3day' (the other
      estimate follow-up still schedules).

    - `test_ems_bundle_skips_disabled_review`:
      Disable review. POST a synthetic ems_bundle with a CLOSED doc_status.
      Assert: 0 jobs with job_type='review' created.

    - `test_ems_bundle_default_enabled_schedules_all`:
      Default-state (no schedules table override). POST estimate → 24h +
      3day created. POST closed → review created. (Regression guard against
      breaking the default path.)

    Use the existing `queue_server` fixture from `tests/conftest.py` which
    already runs `init_db()`. The ALTER migration runs there.
  </behavior>
  <action>
    Edit `app.py` with these changes (in order):

    **1. Add idempotent column migration in `init_db()`** — immediately after
       the existing `CREATE TABLE IF NOT EXISTS schedules ...` block (around
       line 377), BEFORE `con.commit()`. This survives the existing single
       row that 260508-spn's CREATE produced (note: 260508-spn does NOT
       seed-INSERT, but a row may exist if Marco saved an override; the
       ALTER's `DEFAULT 1` clause backfills NULL→1 for any pre-existing row):

       ```python
       # UKK-01: enabled column added via idempotent ALTER so existing rows
       # (from 260508-spn) get DEFAULT 1 backfilled. PRAGMA table_info()
       # is the SQLite-portable way to detect column presence (CREATE
       # IF NOT EXISTS only checks table existence, not column shape).
       cur.execute("PRAGMA table_info(schedules)")
       _sched_cols = {r[1] for r in cur.fetchall()}
       if "enabled" not in _sched_cols:
           cur.execute(
               "ALTER TABLE schedules ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1"
           )
           log.info("schedules: added enabled column (default 1)")
           con.commit()
       ```

    **2. Add `get_schedule_enabled(job_type)` helper** — immediately after
       `get_effective_schedule` (around line 456):

       ```python
       def get_schedule_enabled(job_type: str) -> bool:
           """UKK-04: return True iff this job_type is currently enabled.

           Defaults to True (enabled) when:
             - No override row in `schedules` for this job_type
             - Table or `enabled` column is missing (test bypass paths)
             - Row exists but enabled column is NULL (defensive)

           Mirrors get_effective_schedule's defensive shape.
           """
           try:
               con = get_db()
               try:
                   cur = con.cursor()
                   cur.execute(
                       "SELECT enabled FROM schedules WHERE job_type = ?",
                       (job_type,),
                   )
                   row = cur.fetchone()
               finally:
                   con.close()
           except sqlite3.OperationalError as exc:
               log.warning("get_schedule_enabled: %s", exc)
               return True
           if row is None:
               return True
           # SQLite returns INTEGER for the column; treat 0 as disabled,
           # everything else (including missing/NULL) as enabled.
           val = row["enabled"] if "enabled" in row.keys() else None
           if val is None:
               return True
           try:
               return bool(int(val))
           except (TypeError, ValueError):
               return True
       ```

    **3. Extend GET /schedules response** — modify the GET handler block
       (lines 2542-2585). Two changes:

       a) When reading override rows, also SELECT `enabled`:
          ```python
          cur.execute(
              "SELECT job_type, delay_hours, updated_at, enabled FROM schedules"
          )
          for r in cur.fetchall():
              overrides[r["job_type"]] = (
                  int(r["delay_hours"]),
                  int(r["updated_at"]),
                  bool(int(r["enabled"])) if r["enabled"] is not None else True,
              )
          ```

       b) Both branches of the per-job_type build emit `enabled`:
          ```python
          if jt in overrides:
              delay_h, updated, enabled = overrides[jt]
              job_types_out.append({
                  "job_type":    jt,
                  "label":       meta["label"],
                  "when":        meta["when"],
                  "delay_hours": delay_h,
                  "is_override": True,
                  "updated_at":  updated,
                  "enabled":     enabled,
              })
          else:
              job_types_out.append({
                  "job_type":    jt,
                  "label":       meta["label"],
                  "when":        meta["when"],
                  "delay_hours": int(DEFAULT_SCHEDULES[jt]),
                  "is_override": False,
                  "updated_at":  0,
                  "enabled":     True,
              })
          ```

    **4. Extend `_do_put_schedule`** (line 3246). This is the heaviest
       change in the task. New parsing + persistence logic. Replace the
       current parsing block (lines 3271-3305) with:

       ```python
       # UKK-03: parse both fields independently. Each is optional.
       # Empty body / `{}` → full revert. Otherwise, partial UPSERT.
       full_revert = False
       new_delay_hours: Optional[int] = None    # None == "do not change"
       new_enabled:     Optional[bool] = None    # None == "do not change"

       if not raw:
           full_revert = True
       else:
           try:
               parsed = json.loads(raw.decode("utf-8"))
           except (ValueError, UnicodeDecodeError, json.JSONDecodeError):
               self._send_json(400, {"error": "invalid JSON"})
               return
           if not isinstance(parsed, dict):
               self._send_json(400, {"error": "body must be a JSON object"})
               return

           # Empty dict still means full revert.
           if not parsed:
               full_revert = True
           else:
               # delay_hours: explicit-null and missing both mean "no change".
               if "delay_hours" in parsed and parsed["delay_hours"] is not None:
                   val = parsed["delay_hours"]
                   if isinstance(val, bool) or not isinstance(val, int):
                       self._send_json(400, {
                           "error": "delay_hours must be an integer",
                       })
                       return
                   if val < SCHEDULE_MIN_HOURS or val > SCHEDULE_MAX_HOURS:
                       self._send_json(400, {
                           "error": (
                               f"delay_hours must be between "
                               f"{SCHEDULE_MIN_HOURS} and {SCHEDULE_MAX_HOURS}"
                           ),
                       })
                       return
                   new_delay_hours = val

               # enabled: explicit-null and missing both mean "no change".
               # Bool-only validation; reject 0/1/"false"/etc.
               if "enabled" in parsed and parsed["enabled"] is not None:
                   eval_ = parsed["enabled"]
                   if not isinstance(eval_, bool):
                       self._send_json(400, {
                           "error": "enabled must be a boolean",
                       })
                       return
                   new_enabled = eval_
       ```

       Replace the persistence block (lines 3307-3369). New flow:

       ```python
       now_ts = int(time.time())
       cancelled = 0
       rebased = 0

       con = get_db()
       try:
           cur = con.cursor()

           if full_revert:
               cur.execute(
                   "DELETE FROM schedules WHERE job_type = ?", (job_type,)
               )
               con.commit()
               effective_delay = int(DEFAULT_SCHEDULES[job_type])
               effective_enabled = True
               response_updated_at = 0
               response_is_override = False
               log.info("schedules: %s reverted to defaults (delay=%dh, enabled=True)",
                        job_type, effective_delay)
           else:
               # Read current row state (for unchanged-field fall-through).
               cur.execute(
                   "SELECT delay_hours, enabled FROM schedules WHERE job_type = ?",
                   (job_type,),
               )
               existing = cur.fetchone()
               if existing is None:
                   prev_delay = int(DEFAULT_SCHEDULES[job_type])
                   prev_enabled = True
               else:
                   prev_delay = int(existing["delay_hours"])
                   prev_enabled = bool(int(existing["enabled"])) if existing["enabled"] is not None else True

               effective_delay = new_delay_hours if new_delay_hours is not None else prev_delay
               effective_enabled = new_enabled if new_enabled is not None else prev_enabled

               cur.execute(
                   "INSERT OR REPLACE INTO schedules"
                   "(job_type, delay_hours, updated_at, enabled) "
                   "VALUES (?, ?, ?, ?)",
                   (job_type, effective_delay, now_ts, 1 if effective_enabled else 0),
               )
               con.commit()
               response_updated_at = now_ts
               response_is_override = True
               log.info(
                   "schedules: %s upsert (delay=%dh, enabled=%s)",
                   job_type, effective_delay, effective_enabled,
               )

           # UKK-04: branch on the resulting enabled state.
           if not effective_enabled:
               # Toggle-off (or upsert that ends with enabled=False):
               # cancel ALL pending sent=0 jobs of this job_type.
               cur.execute(
                   "UPDATE jobs SET sent=1, sent_at=? "
                   "WHERE job_type=? AND sent=0",
                   (now_ts, job_type),
               )
               cancelled = cur.rowcount or 0
               con.commit()
               log.info(
                   "schedules: %s disabled — cancelled %d pending job(s)",
                   job_type, cancelled,
               )
               # Skip rebase: there are no pending rows after cancel.
           else:
               # enabled=True (or unchanged-True): run rebase IF delay changed.
               # The rebase block stays as-is — it's idempotent for unchanged
               # delays (recomputes send_at from created_at, which is stable).
               cur.execute(
                   "SELECT id, created_at FROM jobs "
                   "WHERE job_type = ? AND sent = 0",
                   (job_type,),
               )
               pending = cur.fetchall()
               updates = []
               for r in pending:
                   new_send_at = next_send_window(
                       int(r["created_at"]) + effective_delay * 3600
                   )
                   updates.append((int(new_send_at), int(r["id"])))
               if updates:
                   cur.executemany(
                       "UPDATE jobs SET send_at = ? WHERE id = ?",
                       updates,
                   )
                   con.commit()
               rebased = len(updates)
               log.info("schedules: %s rebased %d pending job(s)",
                        job_type, rebased)

       finally:
           con.close()

       self._send_json(200, {
           "is_override":    response_is_override,
           "delay_hours":    effective_delay,
           "enabled":        effective_enabled,
           "updated_at":     response_updated_at,
           "rebased_jobs":   rebased,
           "cancelled_jobs": cancelled,
       })
       ```

       Note: `Optional[int]` and `Optional[bool]` need `from typing import Optional`.
       Check if it's already imported at the top of app.py. If not, add it
       to the existing typing import line, NOT a fresh import line.

    **5. Gate `schedule_job` calls in `do_POST` ems_bundle handler**
       (lines 3045-3068). Wrap each `schedule_job` call:

       ```python
       if doc_status in ESTIMATE_STATUSES:
           log.info("Estimate status %s for doc_id=%s", doc_status, doc_id)
           h_24h  = get_effective_schedule("24h")
           h_3day = get_effective_schedule("3day")
           if get_schedule_enabled("24h"):
               schedule_job(doc_id, "24h", phone, name,
                            next_send_window(now + h_24h * 3600),
                            vin=vin, vehicle_desc=vehicle_desc, ro_id=ro_id,
                            email=email, address=address,
                            year=year, make=make, model=model)
           else:
               log.info("schedules: 24h disabled — skipping schedule_job for doc_id=%s", doc_id)
           if get_schedule_enabled("3day"):
               schedule_job(doc_id, "3day", phone, name,
                            next_send_window(now + h_3day * 3600),
                            vin=vin, vehicle_desc=vehicle_desc, ro_id=ro_id,
                            email=email, address=address,
                            year=year, make=make, model=model)
           else:
               log.info("schedules: 3day disabled — skipping schedule_job for doc_id=%s", doc_id)
       elif doc_status in CLOSED_STATUSES:
           log.info("Closed status %s for doc_id=%s", doc_status, doc_id)
           h_review = get_effective_schedule("review")
           if get_schedule_enabled("review"):
               schedule_job(doc_id, "review", phone, name,
                            next_send_window(now + h_review * 3600),
                            vin=vin, vehicle_desc=vehicle_desc, ro_id=ro_id,
                            email=email, address=address,
                            year=year, make=make, model=model)
           else:
               log.info("schedules: review disabled — skipping schedule_job for doc_id=%s", doc_id)
       ```

       Disabling 24h must NOT skip the 3day call on the same estimate (per
       must-have: independent toggles).

    **6. Append tests** to `tests/test_schedules_endpoint.py` and
       `tests/test_scheduler_gate.py` per the `<behavior>` block. Use
       existing fixtures and helper patterns (HMAC sign helper is already
       defined in those files — reuse, don't re-derive).

       For the `test_ems_bundle_skips_disabled_24h` test: directly insert
       a row into the schedules table (`INSERT INTO schedules(job_type,
       delay_hours, updated_at, enabled) VALUES ('24h', 24, ?, 0)`)
       BEFORE the synthetic POST — bypasses the PUT path so the test
       exercises the read side independently. Then POST a minimal
       BMS-like XML payload (look for `_make_bms_xml` or similar helper in
       existing tests; if missing, build a minimal payload by examining
       `do_POST` for the required fields: doc_id, doc_status, phone,
       name, vin, etc.). After POST, query the jobs table and count
       rows by job_type.

       If no helper exists: the closed-RO and BMS handler tests in
       `test_ems_endpoint.py` (or similar) will have the canonical XML
       payload shape. Reuse, do not re-derive.

    Per the constraints: do NOT touch `internal/ems/*`, `cmd/earlscheib/*`,
    `internal/scanner/*`, the watcher binary, or the installer. This is
    server-side only — no Marco-side rebuild required.

    Per locked decision 1: cancellation idiom is `UPDATE jobs SET sent=1,
    sent_at=now WHERE job_type=? AND sent=0`. Per locked decision 2:
    default is enabled=true — verified by `get_schedule_enabled` returning
    True when no row exists.
  </action>
  <verify>
    <automated>cd /home/jjagpal/projects/earl-scheib-followup &amp;&amp; python3 -m pytest tests/test_schedules_endpoint.py tests/test_scheduler_gate.py -x -q</automated>
  </verify>
  <done>
    All new + existing tests in `test_schedules_endpoint.py` and
    `test_scheduler_gate.py` pass. `python3 -c "import app; print(app.get_schedule_enabled('24h'))"`
    prints `True` (default). `python3 -c "import sqlite3, app; con = app.get_db(); con.execute('PRAGMA table_info(schedules)'); print([r[1] for r in con.execute('PRAGMA table_info(schedules)').fetchall()])"`
    includes `'enabled'` in the column list. The full pytest run shows no
    NEW regressions vs. the 260508-spn baseline (pre-existing failures
    documented in 260508-spn-SUMMARY may persist; new tests must not add
    failures).
  </done>
</task>

<task type="auto" tdd="true">
  <name>Task 2: Go admin proxy — pass `enabled` field through PUT body</name>
  <files>internal/admin/proxy.go, internal/admin/admin_test.go</files>
  <behavior>
    NEW tests in `internal/admin/admin_test.go` (append next to the existing
    `TestAdminProxy_ScheduleUpsert_*` block, lines 655-810):

    - `TestAdminProxy_ScheduleUpsert_EnabledRoundTrip`:
      PUT /api/schedules/24h with `{"enabled": false}` → upstream sees
      canonical body `{"delay_hours":null,"enabled":false}` (or
      `{"enabled":false,"delay_hours":null}` — assert via JSON parse, not
      byte equality, since Go map ordering is stable for structs but
      reviewers may misread). HMAC computed over those exact bytes.

    - `TestAdminProxy_ScheduleUpsert_BothFieldsRoundTrip`:
      PUT `{"delay_hours": 48, "enabled": true}` → upstream sees both
      fields preserved (parse upstream body, assert
      `delay_hours == 48 && enabled == true`).

    - `TestAdminProxy_ScheduleUpsert_DelayOnlyPreservesNullEnabled`:
      PUT `{"delay_hours": 48}` → upstream sees `delay_hours: 48,
      enabled: null` (the canonical representation of "no change").

    - `TestAdminProxy_ScheduleUpsert_EmptyBodyForwardsBothNulls`:
      PUT `{}` → upstream sees `{"delay_hours":null,"enabled":null}`
      (full revert).

    - `TestAdminProxy_ScheduleUpsert_BadEnabledType`:
      PUT `{"enabled": "true"}` → 400 (proxy parser rejects non-bool;
      OR forwards as-is and upstream rejects — choose forward-as-is so
      app.py owns the validation, mirroring the templates pattern where
      app.py is the source of truth for body validation. Assert: 400 from
      upstream-mocked response).

    Existing 8 schedule proxy tests must still pass (regression).
  </behavior>
  <action>
    Edit `internal/admin/proxy.go`:

    **1.** In `handleScheduleUpsert` (line 403), update the parser struct
       (line 425):

       ```go
       var parsed struct {
           DelayHours *int  `json:"delay_hours"`
           Enabled    *bool `json:"enabled"`
       }
       ```

    **2.** Update the canonical re-marshal struct (line 441):

       ```go
       outBody, err := json.Marshal(struct {
           DelayHours *int  `json:"delay_hours"`
           Enabled    *bool `json:"enabled"`
       }{DelayHours: parsed.DelayHours, Enabled: parsed.Enabled})
       ```

       Both fields are `*` pointers — `nil` marshals to `null`, which
       `_do_put_schedule` treats as "field absent / no change". Empty
       browser body still arrives upstream as
       `{"delay_hours":null,"enabled":null}` → full revert.

    **3.** Body cap stays at 4 KiB; jobType whitelist unchanged. NO other
       changes to this handler — comments updated to reflect the new
       field, but the structural pattern is identical to the existing
       templates UPSERT.

    **4.** Update the doc comment block at the top of `handleScheduleUpsert`
       (line 395) to mention the `enabled` field.

    Edit `internal/admin/admin_test.go`:

    **5.** Add the 5 new tests per the `<behavior>` block. Mirror the
       existing `TestAdminProxy_ScheduleUpsert_Forwards` (line 655) and
       `TestAdminProxy_ScheduleUpsert_RevertForwardsVerbatim` (line 690)
       — they're the canonical pattern (fake httptest.Server records
       method+path+body+sig; assert on upstream-side observation).

       For body assertions, JSON-parse the upstream body into a struct
       like `struct { DelayHours *int "json:\"delay_hours\""; Enabled *bool "json:\"enabled\"" }`
       and assert pointer values. Avoids fragile byte-level ordering
       assertions.

       For `TestAdminProxy_ScheduleUpsert_BadEnabledType`: configure the
       fake upstream to return 400 with the body `{"error":"enabled must be a boolean"}`.
       Assert proxy forwards 400 + body verbatim. The proxy itself does
       NOT type-validate (matches the templates handler pattern; app.py
       is the validator).

    Per locked decision 4: proxy is JSON-pass-through; ALL field validation
    happens server-side in app.py. The proxy's only job is canonical
    re-marshal + HMAC + forward.
  </action>
  <verify>
    <automated>cd /home/jjagpal/projects/earl-scheib-followup &amp;&amp; go test ./internal/admin/... -run 'Schedules|Schedule' -count=1 &amp;&amp; go vet ./internal/admin/... &amp;&amp; go build ./...</automated>
  </verify>
  <done>
    All new + existing schedule proxy tests pass (`go test ./internal/admin/... -run 'Schedules|Schedule'` — 8 existing + 5 new = 13 tests green). `go vet` and `go build` clean. `grep -n "Enabled \*bool" internal/admin/proxy.go` shows the new field in two places (parsed struct + outBody struct).
  </done>
</task>

<task type="auto">
  <name>Task 3: UI — toggle switch on each schedule card + sync-ui</name>
  <files>internal/admin/ui/index.html, internal/admin/ui/main.js, internal/admin/ui/main.css, ui_public/index.html, ui_public/main.js, ui_public/main.css</files>
  <action>
    Edit `internal/admin/ui/index.html`:

    **1.** Inside the existing `<template id="schedule-card-template">`
       (line ~235), add the toggle markup INSIDE `<header class="sched-card__head">`,
       positioned RIGHT of `.sched-card__when` and LEFT of `.sched-card__badge`:

       ```html
       <label class="sched-card__toggle">
         <input type="checkbox" class="sched-card__toggle-input" />
         <span class="sched-card__toggle-track" aria-hidden="true"></span>
         <span class="sched-card__toggle-label">Enabled</span>
       </label>
       ```

       The checkbox is keyboard-accessible (Tab to focus, Space to toggle)
       and screen-reader-friendly (the `<label>` wrapper associates the
       text label).

    Edit `internal/admin/ui/main.js`:

    **2.** Update `scheduleState.cards` shape comment (line 1148) to include
       `enabled: bool`. The state-write paths in `loadSchedules` and
       `applySavedSchedule` already assign by spread + cards[jt] = {...}.

       In `loadSchedules` (line 1185), extend the per-jt cache:
       ```js
       scheduleState.cards[jt.job_type] = {
         delay_hours: jt.delay_hours,
         is_override: !!jt.is_override,
         enabled:     jt.enabled !== false,  // default true
         label:       jt.label,
         when:        jt.when,
       };
       ```

    **3.** In `buildScheduleCard` (line 1204), after the existing
       `frag.querySelector('.sched-card__badge')` line:

       ```js
       const toggleInput = frag.querySelector('.sched-card__toggle-input');
       const toggleLabel = frag.querySelector('.sched-card__toggle-label');
       const enabled = jt.enabled !== false;  // default true
       toggleInput.checked = enabled;
       toggleLabel.textContent = enabled ? 'Enabled' : 'Disabled';
       article.classList.toggle('is-disabled', !enabled);
       ```

       Add the toggle change handler (after the existing `resetBtn` handler,
       before `return frag` at line 1308):

       ```js
       toggleInput.addEventListener('change', async () => {
         const next = toggleInput.checked;
         toggleInput.disabled = true;
         try {
           const resp = await fetch(schedUpsertURL(jt.job_type), {
             method: 'PUT',
             headers: { 'Content-Type': 'application/json' },
             body: JSON.stringify({ enabled: next }),
           });
           const parsed = await resp.json().catch(() => ({}));
           if (!resp.ok) {
             // Revert checkbox on failure.
             toggleInput.checked = !next;
             toggleLabel.textContent = !next ? 'Enabled' : 'Disabled';
             article.classList.toggle('is-disabled', next);
             showStatus(statusEl, parsed.error || `Toggle failed (${resp.status})`, 'error');
             return;
           }
           // Success: reflect server state.
           const newEnabled = parsed.enabled !== false;
           toggleInput.checked = newEnabled;
           toggleLabel.textContent = newEnabled ? 'Enabled' : 'Disabled';
           article.classList.toggle('is-disabled', !newEnabled);

           // Update cached state.
           const prev = scheduleState.cards[jt.job_type] || {};
           scheduleState.cards[jt.job_type] = {
             ...prev,
             enabled:     newEnabled,
             is_override: !!parsed.is_override,
           };
           badge.hidden = !parsed.is_override;

           // Status echo per locked decision 4 (visual feedback).
           const cancelled = typeof parsed.cancelled_jobs === 'number' ? parsed.cancelled_jobs : 0;
           let msg;
           if (newEnabled) {
             msg = 'Enabled';
           } else {
             msg = cancelled > 0
               ? `Disabled · ${cancelled} pending job${cancelled === 1 ? '' : 's'} cancelled`
               : 'Disabled';
           }
           showStatus(statusEl, msg, 'ok');
         } catch (_) {
           toggleInput.checked = !next;
           toggleLabel.textContent = !next ? 'Enabled' : 'Disabled';
           article.classList.toggle('is-disabled', next);
           showStatus(statusEl, 'Network error — please retry', 'error');
         } finally {
           toggleInput.disabled = false;
         }
       });
       ```

       Per locked decision 3: the hours `<input>`, Save, and Reset buttons
       remain interactive even when the card is disabled. Do NOT disable
       them when `enabled === false`. The `.is-disabled` CSS class only
       dims the visual; controls remain usable.

    **4.** In `applySavedSchedule` (line 1311), preserve `enabled` from the
       response when present:

       ```js
       const newEnabled = typeof parsed.enabled === 'boolean'
         ? parsed.enabled
         : (prev.enabled !== false);  // fallback to current cached state
       scheduleState.cards[jobType] = {
         delay_hours: newDelay,
         is_override: !!parsed.is_override,
         enabled:     newEnabled,
         label:       prev.label,
         when:        prev.when,
       };
       ```

       The Save / Reset paths don't change `enabled` from this code path
       (the toggle handler is the only enabled-mutator). But keep the cache
       in sync with the server's response in case the server returns an
       updated value.

    Edit `internal/admin/ui/main.css`:

    **5.** Add toggle styles (append to the `.sched-card*` block around line
       1414). Use existing palette CSS vars — typography is Fraunces (per
       locked decision 3) inherited from the card title; the toggle label
       inherits from `.sched-card__head`. CRITICAL: **no animations / no
       transitions** on the thumb (CLAUDE.md design discipline + restraint).

       ```css
       .sched-card__toggle {
         display: inline-flex;
         align-items: center;
         gap: 8px;
         margin-left: auto;       /* push to right of the head row */
         margin-right: 12px;       /* leave room before badge */
         font-size: 13px;
         font-weight: 500;
         color: var(--ink-soft);
         cursor: pointer;
         user-select: none;
       }

       .sched-card__toggle-input {
         /* Visually hidden but kept in the layout for screen readers and
            keyboard activation. Sibling combinator targets the track. */
         position: absolute;
         opacity: 0;
         width: 36px;
         height: 20px;
         margin: 0;
         cursor: pointer;
       }

       .sched-card__toggle-track {
         position: relative;
         display: inline-block;
         width: 36px;
         height: 20px;
         border-radius: 10px;
         background: var(--ink-soft, #8a8278);
         flex: 0 0 auto;
       }

       .sched-card__toggle-track::after {
         content: '';
         position: absolute;
         top: 2px;
         left: 2px;
         width: 16px;
         height: 16px;
         border-radius: 50%;
         background: var(--cream, #f7f1e6);
       }

       /* Active (checked) state — thumb shifts right; track flips to oxblood. */
       .sched-card__toggle-input:checked + .sched-card__toggle-track {
         background: var(--terra, #6e2a2a);
       }

       .sched-card__toggle-input:checked + .sched-card__toggle-track::after {
         left: 18px;
       }

       /* Focus-visible ring for keyboard users — uses the same palette. */
       .sched-card__toggle-input:focus-visible + .sched-card__toggle-track {
         outline: 2px solid var(--terra, #6e2a2a);
         outline-offset: 2px;
       }

       .sched-card__toggle-label {
         min-width: 56px;          /* prevent label width jitter on flip */
         font-variant-caps: all-small-caps;
         letter-spacing: 0.05em;
       }

       /* Card-level dimming when disabled. Inputs/buttons remain fully
          opaque so Marco can pre-tune the delay before re-enabling. */
       .sched-card.is-disabled {
         opacity: 0.6;
       }
       .sched-card.is-disabled .sched-card__toggle,
       .sched-card.is-disabled .sched-card__row,
       .sched-card.is-disabled .sched-card__actions {
         opacity: 1;
       }
       ```

       If `--terra` / `--cream` / `--ink-soft` don't exist by exact name in
       main.css, grep for the palette section near the top of the file and
       use the actual variable names (the executor MUST verify before
       writing the values; CLAUDE.md design discipline requires using the
       project's variable names, not inventing new ones). Fall back to the
       `var(--name, hex)` form so the rule is robust to var naming drift.

       Verify the exact palette var names by grepping `:root\s*{` and
       reading the first 30 lines of main.css. Update the toggle CSS to
       reference the correct existing var names.

    **6.** **Verify the toggle's HTML uses adjacent-sibling combinator (`+`)
       not general-sibling (`~`)** — the markup has track immediately after
       the input inside the label, so `+` is correct. If you change the
       markup ordering, update the selectors accordingly.

    Edit `ui_public/*` files:

    **7.** Run `make sync-ui` to copy the three updated files from
       `internal/admin/ui/` to `ui_public/`. Do NOT edit `ui_public/*`
       directly — they are mirrored by the Makefile target.

    Per the design discipline (CLAUDE.md): NO animations on the thumb. NO
    purple gradients. NO motion. The toggle is informational, not flashy.
    Restraint is the design choice (consistent with the dev-mode banner's
    static rendering in 260508-spn).

    Per the constraints: do NOT change Queue tab or Templates tab behavior.
    The toggle is purely additive on the Schedules cards.
  </action>
  <verify>
    <automated>cd /home/jjagpal/projects/earl-scheib-followup &amp;&amp; make sync-ui &amp;&amp; diff -rq internal/admin/ui ui_public 2>&amp;1 | grep -v -E 'README|gitkeep|logo\.png' &amp;&amp; grep -q 'sched-card__toggle' internal/admin/ui/index.html internal/admin/ui/main.js internal/admin/ui/main.css ui_public/index.html ui_public/main.js ui_public/main.css</automated>
  </verify>
  <done>
    `diff -rq internal/admin/ui ui_public` (excluding README/gitkeep/logo.png) is silent.
    `grep -n 'sched-card__toggle' internal/admin/ui/index.html ui_public/index.html` → matching hits in both trees.
    `grep -n 'toggleInput\|sched-card__toggle' internal/admin/ui/main.js ui_public/main.js` → toggle handler present in both.
    `grep -n '\.sched-card__toggle\|\.sched-card.is-disabled' internal/admin/ui/main.css ui_public/main.css` → CSS rules present in both.
    Manual smoke (start app.py + open http://localhost:8200/earlscheib): Schedules tab renders 3 cards each with a toggle in the header row showing "Enabled". Clicking the toggle on the 24h card sends PUT, returns 200, label flips to "Disabled", card dims to ~60% opacity, status echo shows "Disabled" or "Disabled · N pending jobs cancelled". Reloading the page persists the disabled state. Hours input + Save button remain interactive while disabled. Toggling back ON returns label to "Enabled" with no resurrection language.
  </done>
</task>

</tasks>

<verification>
**Phase-level checks** (run after all 3 tasks complete):

1. **Python tests** — full suite green except known pre-existing failures:
   ```bash
   cd /home/jjagpal/projects/earl-scheib-followup
   python3 -m pytest tests/test_schedules_endpoint.py tests/test_scheduler_gate.py -q
   ```
   Expect: all new toggle + cancel-on-disable + ems_bundle-skip tests pass.

2. **Go tests** — schedule proxy tests green:
   ```bash
   go test ./internal/admin/... -run 'Schedules|Schedule' -count=1
   ```
   Expect: 13 schedule tests pass (8 existing + 5 new).

3. **UI sync** — byte-identical:
   ```bash
   diff -rq internal/admin/ui ui_public 2>&1 | grep -v -E 'README|gitkeep|logo\.png'
   ```
   Expect: silent.

4. **Build** — Go clean:
   ```bash
   go build ./...
   go vet ./...
   ```
   Expect: clean.

5. **End-to-end smoke** (run app.py locally):
   - GET /earlscheibconcord/schedules → 3 job_types, each with `enabled: true`.
   - PUT /earlscheibconcord/schedules/24h `{"enabled": false}` → 200,
     `enabled: false, cancelled_jobs: N` (N = pending sent=0 24h jobs).
   - GET → 24h shows `enabled: false, is_override: true`.
   - POST a synthetic ems_bundle estimate → only 3day job created, no 24h.
   - PUT `{"enabled": true}` → 200, `enabled: true, cancelled_jobs: 0`.
   - Pre-existing cancelled rows in DB still have sent=1 (no resurrection).
   - PUT `{}` → revert; subsequent GET shows defaults: 24, true, false.
   - Browser at /earlscheib Schedules tab: toggle visually flips, card dims,
     status echo shows correct messages. Hours input + Save still work
     while disabled.

6. **Untouched-by-design verification** — `git diff --stat` after all 3
   tasks should show ZERO changes outside this list:
   - app.py
   - tests/test_schedules_endpoint.py
   - tests/test_scheduler_gate.py
   - internal/admin/proxy.go
   - internal/admin/admin_test.go
   - internal/admin/ui/{index.html,main.js,main.css}
   - ui_public/{index.html,main.js,main.css}

   `git diff --stat -- internal/ems internal/scanner cmd/earlscheib internal/installer internal/heartbeat internal/remoteconfig internal/telemetry internal/update Makefile` MUST be empty. **No installer rebuild required.**

</verification>

<success_criteria>
- [ ] Three tasks complete in order (1 → 2 → 3); each commits independently
      with conventional-commit messages (`feat(schedules): per-schedule
      enable/disable toggle — server`, `... — proxy`, `... — UI toggle`).
- [ ] All NEW pytest tests pass: cancel-on-disable, no-resurrection,
      independent toggle/delay PUTs, type validation, ems_bundle skip.
- [ ] All NEW Go proxy tests pass (5 new in admin_test.go).
- [ ] `diff -rq internal/admin/ui ui_public` is silent on tracked files.
- [ ] Manual end-to-end smoke confirms: toggle flips, card dims, status
      echo shows cancelled count, hours input remains interactive, no
      resurrection on toggle-on.
- [ ] **Default state preserved** — operators who never touch the toggle
      see zero behavior change (verified by `test_get_schedules_includes_enabled_default_true`
      and `test_ems_bundle_default_enabled_schedules_all`).
- [ ] **Marco-side untouched: ZERO changes to internal/ems/, cmd/earlscheib/,
      internal/scanner/, internal/installer/, or any other code in
      EarlScheibWatcher.exe. No installer rebuild required.**
- [ ] **Cancel-on-disable verified** by `test_put_schedule_toggle_off_cancels_pending`.
- [ ] **No-resurrection verified** by `test_put_schedule_toggle_on_does_not_resurrect`.
- [ ] **ems_bundle gating verified** by `test_ems_bundle_skips_disabled_24h`
      and `test_ems_bundle_skips_disabled_review`.
</success_criteria>

<output>
After completion, create `.planning/quick/260508-ukk-per-schedule-enable-disable-toggle-with-/260508-ukk-SUMMARY.md` describing:

1. What landed (server: `enabled` column + ALTER migration + GET/PUT
   extension + ems_bundle gate + cancel-on-disable; proxy: Enabled *bool
   field; UI: toggle switch + dimmed-disabled state + status echo).
2. Key decisions confirmed during execution (per locked decisions 1-4).
3. Test inventory (counts: new Python tests, new Go tests).
4. Commits (3 expected, one per task).
5. Verification evidence (pytest + go test outputs, sync-ui diff, smoke
   curl results, manual UI smoke).
6. **Explicit "what we did NOT touch" list** — same scope as 260508-spn
   plus a note that even the schedules CREATE statement was unchanged
   (column added via ALTER migration, preserving existing rows).
7. Self-check: same pattern as 260508-spn-SUMMARY.

Then update `.planning/STATE.md` to log this quick task entry.
</output>
