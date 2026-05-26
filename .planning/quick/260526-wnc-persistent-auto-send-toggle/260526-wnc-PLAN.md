---
phase: quick-260526-wnc
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - app.py
  - tests/test_scheduler_gate.py
  - internal/admin/ui/index.html
  - internal/admin/ui/main.js
  - internal/admin/ui/main.css
  - ui_public/index.html
  - ui_public/main.js
  - ui_public/main.css
autonomous: true
requirements: [WNC-01]
must_haves:
  truths:
    - "On deploy, auto-send stays OFF (env SCHEDULER_ENABLED=0 seeds the DB row to OFF; no behavior change until Marco clicks)"
    - "Marco can flip auto-send ON/OFF from the /earlscheib admin UI and the choice persists across server restart"
    - "After the app_settings row exists, changing the SCHEDULER_ENABLED env var has no effect — the DB value is the sole gate"
    - "Manual Send now still fires SMS regardless of the toggle state"
    - "ui_public/* copies are byte-identical to internal/admin/ui/* after make sync-ui"
  artifacts:
    - path: "app.py"
      provides: "app_settings table, get/set_auto_send_enabled, first-boot seed, POST /earlscheibconcord/auto-send endpoint, read-site swaps"
      contains: "AUTO_SEND_SETTING_KEY"
    - path: "tests/test_scheduler_gate.py"
      provides: "DB-driven gate tests including env-ignored-after-seed contract"
      contains: "set_auto_send_enabled"
    - path: "internal/admin/ui/main.js"
      provides: "Auto-send toggle wired to /diagnostic poll + POST /auto-send"
      contains: "auto-send"
  key_links:
    - from: "scheduler_loop"
      to: "get_auto_send_enabled()"
      via: "fresh DB read each 30s iteration"
      pattern: "get_auto_send_enabled\\(\\)"
    - from: "internal/admin/ui/main.js auto-send toggle"
      to: "/earlscheibconcord/auto-send"
      via: "POST {enabled: bool} on click"
      pattern: "auto-send"
---

<objective>
Replace the env-gated "DEV MODE" SCHEDULER_ENABLED kill-switch with a persistent, Marco-controllable auto-send toggle backed by a generic `app_settings` key-value table in jobs.db.

Purpose: This is a LIVE production server sending real-customer SMS. Today the scheduler is paused only because an env var is unset on the Pi — Marco cannot turn auto-send on himself, and the gate is fragile (an env change flips it silently). After this change, the toggle state lives in the DB, Marco flips it from the admin UI, and the env var becomes a one-time first-boot seed only (default OFF on deploy = zero behavior change until Marco clicks).

Output: `app_settings` table + getter/setter + first-boot seed in app.py; new POST `/earlscheibconcord/auto-send` endpoint; all SCHEDULER_ENABLED read sites swapped to fresh DB reads; rewritten test_scheduler_gate.py; a real toggle replacing the DEV-MODE banner in the admin UI; byte-identical ui_public/ copies.
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
@$HOME/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@CLAUDE.md

<!-- The closest analog is the UKK per-schedule enable/disable toggle (commit 809851a)
     and the SPN scheduler gate (commit d1f57ad). Reuse those patterns exactly. -->

<interfaces>
<!-- Defensive getter to MIRROR for get_auto_send_enabled (app.py:470) -->
def get_schedule_enabled(job_type: str) -> bool:
    try:
        con = get_db()
        try:
            cur = con.cursor()
            cur.execute("SELECT enabled FROM schedules WHERE job_type = ?", (job_type,))
            row = cur.fetchone()
        finally:
            con.close()
    except sqlite3.OperationalError as exc:
        log.warning("get_schedule_enabled: %s", exc)
        return True
    if row is None:
        return True
    val = row["enabled"] if "enabled" in row.keys() else None
    ...

<!-- Read sites to swap (current SCHEDULER_ENABLED module constant, app.py:112) -->
app.py:1137  log.info("Scheduler started (SCHEDULER_ENABLED=%s)", SCHEDULER_ENABLED)
app.py:1141  if SCHEDULER_ENABLED:           # scheduler_loop gate (each 30s)
app.py:1147  "scheduler gated off; SCHEDULER_ENABLED=0 — manual send-now still works"
app.py:3093  "scheduler_enabled": SCHEDULER_ENABLED,   # /diagnostic JSON field
app.py:3354  send-now endpoint — LEAVE UNGATED (manual always works)

<!-- Endpoint pattern to mirror exactly (POST /queue/send-now, app.py:3354) -->
if self.path.split("?")[0] == "/earlscheibconcord/queue/send-now":
    # Auth: CF Access (edge gate) — no origin-side check needed
    try:
        body = json.loads(raw.decode("utf-8"))
        job_id = int(body["id"])
    except (ValueError, KeyError, json.JSONDecodeError, UnicodeDecodeError):
        self._send_json(400, {"error": "invalid JSON"})
        return

<!-- Public-UI API base remap (main.js:24-27); browser path prefix is /earlscheibconcord.
     send-now URL is `${API_BASE}/queue/send-now`; new toggle URL is `${API_BASE}/auto-send`. -->
const API_BASE = (window.API_BASE_PATH) ? window.API_BASE_PATH : '/api';
const IS_LOCAL_ADMIN = API_BASE === '/api';

<!-- Diagnostic poll already drives the banner (main.js:858-865). The Go local admin
     (renderLocalDiagnostic) does NOT return scheduler_enabled, so d.scheduler_enabled
     is undefined in local mode. Gate the toggle on `typeof d.scheduler_enabled === 'boolean'`. -->

<!-- Reusable CSS switch already exists: .sched-card__toggle* (main.css:1498+, UKK).
     The DEV-MODE banner styling is .dev-banner (main.css:1320). -->
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Backend — app_settings table, getter/setter, first-boot seed, /auto-send endpoint, read-site swaps, rewritten tests</name>
  <files>app.py, tests/test_scheduler_gate.py</files>
  <behavior>
    Drive these via the rewritten tests/test_scheduler_gate.py (DB-driven, no SCHEDULER_ENABLED constant):
    - First-boot seed: reload+init_db with env SCHEDULER_ENABLED="0" → app_settings row value="0" → get_auto_send_enabled() is False.
    - First-boot seed: env="1" → row="1" → get_auto_send_enabled() is True.
    - First-boot seed: env unset → defaults to "0" → get_auto_send_enabled() is False.
    - ENV-IGNORED-AFTER-SEED (load-bearing, per D-02): with an existing app_settings row=OFF, reload app with env="1" and call init_db again → get_auto_send_enabled() stays False (DB wins, env ignored).
    - Loop gate OFF: get_auto_send_enabled() False → _fire_due_jobs not called (mirror scheduler_loop branch), due row stays sent=0.
    - Loop gate ON: set_auto_send_enabled(True) → _fire_due_jobs() fires send_sms once, row sent=1.
    - Manual send-now with toggle OFF: full HTTP POST /queue/send-now still fires send_sms (SPN-03 contract preserved).
    - Throttled "gated off" log fires at most once per _GATED_LOG_INTERVAL_S; only the message text changes (no longer references SCHEDULER_ENABLED).
    - New endpoint POST /earlscheibconcord/auto-send: {"enabled": true} persists + returns the new state JSON; {"enabled": false} persists OFF; non-bool value (0, "true", missing key, bad JSON) → 400.
    - Keep the UKK ems_bundle-skips-disabled tests intact (they already pass; only the fixture's gate handling changes).
  </behavior>
  <action>
    In app.py:
    1. Add module constant `AUTO_SEND_SETTING_KEY = "auto_send_enabled"` near the existing SCHEDULER_ENABLED block (app.py:106-116). Keep reading the env var at module load ONLY to compute the first-boot seed value: `_AUTO_SEND_SEED = "1" if os.getenv("SCHEDULER_ENABLED", "0") == "1" else "0"`. Remove the live `SCHEDULER_ENABLED` module constant — it must not be a live gate anywhere.
    2. In init_db() (app.py:249), after the existing CREATE TABLE statements, idempotently create: `app_settings (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at INTEGER NOT NULL)`. Then, in the SAME connection at the END of init_db (per advisor: keeps the existing reload→init_db test flow working with one call), seed the auto-send key only if absent: `INSERT OR IGNORE INTO app_settings(key, value, updated_at) VALUES (?, ?, ?)` with (AUTO_SEND_SETTING_KEY, _AUTO_SEND_SEED, now). INSERT OR IGNORE makes it a no-op when the row already exists → DB stays sole source of truth.
    3. Add `get_auto_send_enabled() -> bool` MIRRORING the exact defensive shape of get_schedule_enabled (app.py:470): wrap get_db in try/except sqlite3.OperationalError, default safely (return False on missing table/row/NULL — note: default-safe here means OFF, the conservative choice for a live SMS server). SELECT value FROM app_settings WHERE key = AUTO_SEND_SETTING_KEY; parse "1"/"0" → bool.
    4. Add `set_auto_send_enabled(enabled: bool) -> None` that upserts: `INSERT INTO app_settings(key, value, updated_at) VALUES (?, ?, ?) ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`. Store "1"/"0". Use parameterized SQL (codebase norm).
    5. Swap every read site (per D-03):
       - app.py:1137 startup log → log the initial DB value: `log.info("Scheduler started (auto_send_enabled=%s)", get_auto_send_enabled())`.
       - app.py:1141 scheduler_loop gate → `if get_auto_send_enabled():` (fresh DB read each 30s iteration; cheap).
       - app.py:1146-1149 gated-off log → reword to not reference SCHEDULER_ENABLED, e.g. "auto-send disabled via toggle — manual send-now still works". Preserve the throttle (_last_gated_log_ts / _GATED_LOG_INTERVAL_S) unchanged.
       - app.py:3093 /diagnostic → KEEP the JSON key name `scheduler_enabled` for UI back-compat, but populate from `get_auto_send_enabled()`.
       - app.py:3354 send-now endpoint → LEAVE UNGATED; preserve the SPN-03 comment intent (do not add a gate check).
    6. Add the new endpoint in do_POST, mirroring the send-now pattern EXACTLY (CF-Access edge gate, no origin auth). Route on `self.path.split("?")[0] == "/earlscheibconcord/auto-send"`. Parse `json.loads(raw.decode("utf-8"))`; require key "enabled" whose value is a real Python bool (reject 0/1 ints, "true" strings, missing key, bad JSON) → on any deviation `self._send_json(400, {"error": "invalid JSON"})` matching send-now's shape. On valid bool: `set_auto_send_enabled(enabled)`, then `self._send_json(200, {"enabled": enabled})`.

    In tests/test_scheduler_gate.py: rewrite per the <behavior> block above. Drop all `assert app_mod.SCHEDULER_ENABLED is True/False`. The reload_app_with_gate fixture's env-var monkeypatch now only sets the FIRST-BOOT SEED — keep it for the seed tests, but add a dedicated test that reloads twice with differing env against the SAME db_path to prove env-ignored-after-seed. Use set_auto_send_enabled (or direct app_settings write) to flip the gate in the loop ON/OFF tests. Keep the two known pre-existing unrelated test failures (test_get_templates_placeholder_catalog, test_get_templates_sample_row_from_pending_job) OUT OF SCOPE — do not touch them.
  </action>
  <verify>
    <automated>python -m pytest tests/test_scheduler_gate.py -x -q</automated>
  </verify>
  <done>All test_scheduler_gate.py tests pass; SCHEDULER_ENABLED module constant removed; get_auto_send_enabled/set_auto_send_enabled/app_settings present; POST /earlscheibconcord/auto-send persists state and 400s on non-bool; /diagnostic still returns key `scheduler_enabled` populated from DB; send-now remains ungated.</done>
</task>

<task type="auto">
  <name>Task 2: UI — replace DEV-MODE banner with a real auto-send toggle, sync to ui_public</name>
  <files>internal/admin/ui/index.html, internal/admin/ui/main.js, internal/admin/ui/main.css, ui_public/index.html, ui_public/main.js, ui_public/main.css</files>
  <action>
    Source of truth is internal/admin/ui/ (per D-05). Edit those three files, then run `make sync-ui` to copy to ui_public/.

    index.html (replace the DEV-MODE banner at index.html:17-23): swap the red alarming `#dev-banner` block for an `#auto-send-toggle` control. Keep it above the topbar where the banner sits. Render a labelled switch (reuse the existing markup shape of the UKK switch so the CSS applies): a checkbox input + track + a state label element. role="status" aria-live="polite". The label shows ON: "Auto-send ON — texts go out automatically" / OFF: "Auto-send OFF — paused; manual Send now still works".

    main.css: add an `.auto-send-toggle` block. Copy the oxblood-on-white CSS-only switch styling from `.sched-card__toggle*` (main.css:1498+) into a generic non-card-scoped class (the .sched-card__toggle rules are scoped under .sched-card context). No animations/transitions (CLAUDE.md design discipline). Make it visually unmissable (a clear bar/strip with the switch) but NOT the alarming red full-width DEV bar — use the cream/oxblood palette already in main.css. You may remove the now-unused `.dev-banner*` rules.

    main.js (replace the banner logic at main.js:858-865 inside fetchDiagnostic): 
    - PUBLIC-UI-ONLY SCOPE (per advisor point 1): the Go local admin's renderLocalDiagnostic does NOT return scheduler_enabled. Gate the toggle's visibility/state on `typeof d.scheduler_enabled === 'boolean'`. When it is not a boolean (local admin, or pre-first-poll), hide the toggle. Do NOT add a Go-admin proxy or /api/auto-send path — this task touches NO Go files.
    - Reflect current state: set the checkbox checked = d.scheduler_enabled and update the label text accordingly on every diagnostic poll.
    - On toggle click/change: POST to the new endpoint. URL: `${API_BASE}/auto-send` (public path → /earlscheibconcord/auto-send). Body: `JSON.stringify({ enabled: <new checkbox state> })`, method POST, Content-Type application/json. On success, update the label immediately; on failure, revert the checkbox to the prior state and let the next diagnostic poll reconcile. Guard so the programmatic state-sync from polling does not re-fire a POST (e.g. only POST on user-initiated change events).

    After all three internal edits, run `make sync-ui`.
  </action>
  <verify>
    <automated>make sync-ui && diff internal/admin/ui/index.html ui_public/index.html && diff internal/admin/ui/main.js ui_public/main.js && diff internal/admin/ui/main.css ui_public/main.css && grep -q 'auto-send' internal/admin/ui/main.js && grep -q 'auto-send-toggle' internal/admin/ui/index.html</automated>
  </verify>
  <done>DEV-MODE banner replaced by a working auto-send toggle in internal/admin/ui/; toggle reflects /diagnostic scheduler_enabled and POSTs {enabled} to /auto-send on click; toggle hidden when scheduler_enabled is not a boolean (local admin); ui_public/ copies byte-identical to internal/admin/ui/ (all three diffs empty); no Go files modified.</done>
</task>

</tasks>

<threat_model>
## Trust Boundaries

| Boundary | Description |
|----------|-------------|
| browser → /earlscheibconcord/auto-send | Operator (Marco) input crosses here; CF Access at the edge is the auth gate (same as send-now) |

## STRIDE Threat Register

| Threat ID | Category | Component | Disposition | Mitigation Plan |
|-----------|----------|-----------|-------------|-----------------|
| T-wnc-01 | Tampering | POST /auto-send body | mitigate | Strict bool validation — reject non-bool (ints, strings, missing key, bad JSON) with 400; parameterized SQL upsert |
| T-wnc-02 | Elevation of Privilege | /auto-send endpoint | accept | No origin-side auth by design — CF Access (Zero Trust email-OTP) is the sole edge gate, identical to the established send-now / queue endpoints |
| T-wnc-03 | Denial of Service | scheduler_loop accidentally left ON at deploy | mitigate | First-boot seed defaults OFF (env SCHEDULER_ENABLED=0 on the Pi) → no auto-send until Marco explicitly flips it; DB value is sole source of truth thereafter |
| T-wnc-SC | Tampering | npm/pip/cargo installs | accept | No new packages — stdlib sqlite3/json only; no install tasks in this plan |
</threat_model>

<verification>
- `python -m pytest tests/test_scheduler_gate.py -x -q` — all green (DB-driven gate, env-ignored-after-seed, manual send-now ungated).
- `make sync-ui` then three `diff` commands return empty (byte-identical ui_public copies).
- Grep confirms `SCHEDULER_ENABLED` no longer used as a live gate: `grep -nv '^\s*#' app.py | grep -c 'SCHEDULER_ENABLED'` — the only remaining reference should be the first-boot seed read (`os.getenv("SCHEDULER_ENABLED", ...)`). No constant gating the loop or diagnostic.
- No Go source files modified (this is a Python + shared-UI change only).
</verification>

<success_criteria>
- app_settings key-value table created idempotently in init_db; future toggles reuse it.
- First-boot seed from SCHEDULER_ENABLED env (currently "0" on Pi) → default OFF on deploy, zero behavior change until Marco clicks.
- After seeding, env changes are ignored — DB value is the sole gate (proven by test).
- scheduler_loop, /diagnostic, startup log, and gated-off log all read fresh from get_auto_send_enabled(); /queue/send-now stays ungated.
- POST /earlscheibconcord/auto-send persists Marco's choice and validates strictly.
- Admin UI shows an unmissable (not alarming-red) toggle that reflects and controls auto-send; visible only on the Pi-served /diagnostic (boolean scheduler_enabled), hidden in local Go admin.
- ui_public/* byte-identical to internal/admin/ui/*.
</success_criteria>

<output>
Create `.planning/quick/260526-wnc-persistent-auto-send-toggle/260526-wnc-SUMMARY.md` when done.
</output>
