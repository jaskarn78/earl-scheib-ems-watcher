---
phase: quick-260515-lae
plan: 01
type: execute
wave: 1
depends_on: []
files_modified: [app.py]
autonomous: true
requirements: [LAE-01]
must_haves:
  truths:
    - "Basic Auth is no longer presented at the origin — /earlscheib never returns 401 with WWW-Authenticate"
    - "/earlscheib always serves the admin UI HTML (no ADMIN_UI_ENABLED gate)"
    - "The 4 operator endpoints (GET/DELETE /queue, POST /queue/send-now, GET /diagnostic) accept any request without origin-side auth (CF Access is the edge gate)"
    - "All watcher-signed HMAC endpoints (BMS ingest, /commands, /remote-config, /version, /logs, /telemetry, /sms-log POST, templates, schedules) still reject unsigned requests"
    - "`python3 -c \"import app\"` succeeds (no syntax errors)"
    - "Existing pytest suite passes unchanged"
  artifacts:
    - path: "app.py"
      provides: "Single-gate auth: HMAC for machine endpoints, no origin check for operator endpoints"
      contains: "_validate_hmac"
  key_links:
    - from: "/earlscheib handler"
      to: "admin UI HTML response"
      via: "direct serve, no auth gate"
      pattern: "def.*earlscheib"
    - from: "operator endpoint handlers (queue/send-now/diagnostic)"
      to: "handler body"
      via: "no _validate_auth call"
      pattern: "# Auth: CF Access"
    - from: "HMAC endpoint handlers"
      to: "_validate_hmac"
      via: "preserved signature check"
      pattern: "_validate_hmac\\(raw, sig\\)"
---

<objective>
Strip the origin-side HTTP Basic Auth layer from `app.py`. Cloudflare Access at `jjagpal.cloudflareaccess.com` is now the sole gate for `/earlscheib` and the four operator endpoints. The dual-auth scheme (RJL-02) leaves a vestigial Basic Auth prompt that re-challenges Marco's Pi browser even when the CF cookie is valid — this plan removes that prompt entirely.

Purpose: Eliminate the leftover Basic Auth gate that breaks Marco's UX. Security boundary moves entirely to the edge (CF Access).

Output: Modified `app.py` with `_validate_basic_auth`, `ADMIN_UI_USER`, `ADMIN_UI_PASSWORD`, `ADMIN_UI_ENABLED` removed; `_validate_auth` reduced to HMAC-only; `/earlscheib` always serves; the four operator endpoints no longer gate on origin auth. HMAC-only endpoints (BMS ingest, telemetry, /commands, etc.) are UNCHANGED.
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
</execution_context>

<context>
@.planning/STATE.md
@app.py

<interfaces>
<!-- Key code landmarks the executor will edit. Pre-verified line numbers. -->
<!-- Some offsets may shift by ±2 lines during edits — re-Read before each Edit. -->

Lines 220-237 — comment block + env var reads:
  ADMIN_UI_USER, ADMIN_UI_PASSWORD, ADMIN_UI_ENABLED — DELETE all three.
  Comment block 220-233 — REPLACE with a short comment noting that CF Access is the gate.

Lines 240-257 — `_validate_basic_auth(header)` — DELETE the entire function.

Lines 260-273 — `_validate_auth(handler, raw)` — SIMPLIFY to HMAC-only:
  Remove the `_validate_basic_auth(auth)` branch and the basic-auth log line.
  Keep the function name so existing call sites do not break.
  Resulting body should be equivalent to:
    sig = handler.headers.get("X-EMS-Signature", "")
    return _validate_hmac(raw, sig)

Line 2406 — `if status == 401 and ADMIN_UI_ENABLED:` — DELETE this branch entirely
  (it sends a WWW-Authenticate header; with ADMIN_UI_ENABLED gone, the branch is dead.
  The surrounding 401 response path should keep working — only remove the
  `WWW-Authenticate` header insertion, not the 401 itself).

Lines 2602, 2677, 2768, 2830 — `if not _validate_auth(self, b""):` on the 4 operator GET/DELETE/POST endpoints
  (GET /earlscheibconcord/queue, DELETE /earlscheibconcord/queue,
   POST /earlscheibconcord/queue/send-now, GET /earlscheibconcord/diagnostic).
  REMOVE the auth check (the `if not _validate_auth(...)` block and its 401 response).
  Replace with a one-line comment: `# Auth: CF Access (edge gate) — no origin-side check needed`.

Lines 2962-2991 — `/earlscheib` handler:
  REMOVE the `if not ADMIN_UI_ENABLED: 404` gate.
  REMOVE the `_validate_basic_auth(auth)` gate and the associated 401/WWW-Authenticate response.
  REMOVE the basic-auth log line referencing ADMIN_UI_USER.
  Handler should serve the admin UI HTML directly.

Lines 3162, 3252, 3300, 3359, 3390, 3507, 3582, 3691 — remaining `_validate_auth(self, raw)` call sites.
  CLASSIFICATION: After the simplification in step on lines 260-273, `_validate_auth` is now HMAC-only.
  These call sites are machine-to-machine endpoints (BMS ingest, telemetry, /commands, /remote-config,
  /version, /logs, /sms-log POST from watcher, templates, schedules, send-now-when-called-by-watcher).
  Therefore: KEEP these calls AS-IS. They will continue to enforce HMAC via the now-simplified
  `_validate_auth`. Do not touch them.

  HOWEVER — the executor MUST Read the surrounding context at each of these 8 call sites BEFORE
  deciding to leave them alone. If any of them is in fact one of the 4 operator endpoints
  (queue/send-now/diagnostic) that was missed in the b"" list, REMOVE the auth check there too.
  The b"" vs raw distinction is just empty-body vs request-body — it does not by itself classify
  the endpoint. Use the HTTP method + path string in the surrounding handler context as ground truth.
</interfaces>
</context>

<tasks>

<task type="auto">
  <name>Task 1: Remove Basic Auth code and simplify _validate_auth</name>
  <files>app.py</files>
  <action>
Edit `app.py` to remove origin-side Basic Auth. Work top-down so later line numbers stay stable as you edit.

Step A — Comment block + env vars (lines ~220-237):
- Replace the entire 220-233 comment block with a single short comment:
  `# Auth: CF Access is the sole gate for the admin UI and operator endpoints.`
  `# Machine-to-machine endpoints (watcher → server) are HMAC-signed; see _validate_hmac.`
- Delete `ADMIN_UI_USER`, `ADMIN_UI_PASSWORD`, `ADMIN_UI_ENABLED` (lines 235-237).

Step B — Delete `_validate_basic_auth` (lines ~240-257) entirely, including the blank line(s) that follow.

Step C — Simplify `_validate_auth` (lines ~260-273) to HMAC-only:
```
def _validate_auth(handler, raw: bytes) -> bool:
    """Return True if the request carries a valid HMAC signature.
    Operator/browser access is gated by Cloudflare Access at the edge;
    this function is for machine-to-machine endpoints only.
    """
    sig = handler.headers.get("X-EMS-Signature", "")
    return _validate_hmac(raw, sig)
```

Step D — Remove the WWW-Authenticate branch (around line 2406):
- Find `if status == 401 and ADMIN_UI_ENABLED:` and delete the branch that sends the
  `WWW-Authenticate: Basic realm=...` header. Do NOT remove the 401 status itself —
  only the header insertion that was conditional on ADMIN_UI_ENABLED.

Step E — Strip auth from the 4 operator endpoints (around lines 2602, 2677, 2768, 2830):
For each `if not _validate_auth(self, b""):` block:
- Read the surrounding 10-20 lines to confirm it is one of:
  GET /earlscheibconcord/queue, DELETE /earlscheibconcord/queue,
  POST /earlscheibconcord/queue/send-now, GET /earlscheibconcord/diagnostic.
- Delete the `if not _validate_auth(...)` block and its 401 response body.
- Replace with: `# Auth: CF Access (edge gate) — no origin-side check needed`

Step F — Strip auth and ADMIN_UI_ENABLED gate from `/earlscheib` handler (around lines 2962-2991):
- Delete the `if not ADMIN_UI_ENABLED:` 404 block.
- Delete the `if not _validate_basic_auth(auth):` 401 block and its WWW-Authenticate/log lines.
- The handler should fall through directly to serving the admin UI HTML.
- Add a one-line comment above the handler: `# Auth: CF Access (edge gate) — no origin-side check needed`

Step G — Audit remaining `_validate_auth(self, raw)` call sites (around lines 3162, 3252, 3300, 3359, 3390, 3507, 3582, 3691):
- Read 10-20 lines of context at EACH site to identify the HTTP method + path.
- If the endpoint is one of the 4 operator endpoints listed in Step E (somehow missed because
  it uses `raw` instead of `b""`), strip the auth check the same way.
- Otherwise (BMS ingest, telemetry, /commands, /remote-config, /version, /logs, /sms-log POST,
  templates, schedules, watcher-side send-now), LEAVE THE CALL UNCHANGED — `_validate_auth` is
  now HMAC-only and continues to gate these machine endpoints correctly.
- Document classifications inline only if helpful; do not add gratuitous comments.

Do NOT introduce new imports, helpers, or refactors beyond what Steps A–G specify. The goal is the
minimum diff that removes Basic Auth and the ADMIN_UI_ENABLED gate.
  </action>
  <verify>
    <automated>cd /home/jjagpal/projects/earl-scheib-followup && python3 -c "import app" && grep -nc "_validate_basic_auth\|ADMIN_UI_USER\|ADMIN_UI_PASSWORD\|ADMIN_UI_ENABLED" app.py | grep -v '^#' | awk -F: '{ if ($NF != "0") { print "FAIL: residual basic-auth symbols found"; exit 1 } else { print "OK: basic-auth symbols removed" } }'</automated>
  </verify>
  <done>
- `python3 -c "import app"` succeeds with no SyntaxError or ImportError.
- `grep -n "_validate_basic_auth\|ADMIN_UI_USER\|ADMIN_UI_PASSWORD\|ADMIN_UI_ENABLED" app.py` returns zero matches.
- `grep -n "_validate_hmac" app.py` still shows HMAC validation in place (count should be unchanged or +1 from the simplified `_validate_auth`).
- The 4 operator endpoints have `# Auth: CF Access (edge gate)` comments where the auth check used to be.
- `/earlscheib` handler serves HTML directly without any `_validate_basic_auth` or `ADMIN_UI_ENABLED` reference.
  </done>
</task>

<task type="auto">
  <name>Task 2: Verify pytest suite still passes</name>
  <files></files>
  <action>
Run the existing pytest suite to confirm no regressions. No tests in the current suite reference Basic Auth or `ADMIN_UI_*` env vars (verified pre-plan via `grep -rn "ADMIN_UI\|_validate_basic_auth\|Authorization.*Basic" tests/`), so all tests should pass unchanged.

If any test fails:
- If the failure is related to Basic Auth (unlikely — none reference it), the test was testing the
  removed behaviour and should be deleted or updated to assert the new no-origin-auth contract.
- If the failure is unrelated (e.g., HMAC endpoint), investigate — the edit may have inadvertently
  touched an HMAC call site. Re-read the surrounding handler and restore `_validate_auth(self, raw)`
  if removed in error.

Do not add new tests in this plan — the orchestrator will deploy and validate end-to-end on the Pi
after this plan closes (deployment is out of scope per the constraints).
  </action>
  <verify>
    <automated>cd /home/jjagpal/projects/earl-scheib-followup && python3 -m pytest tests/ -x --tb=short 2>&1 | tail -30</automated>
  </verify>
  <done>
- `pytest tests/` exits 0 with all tests passing.
- No new test failures introduced by the auth-stripping edits.
  </done>
</task>

</tasks>

<verification>
End-to-end correctness checks (run after both tasks complete):

1. **No residual Basic Auth code**:
   ```
   grep -nE "_validate_basic_auth|ADMIN_UI_USER|ADMIN_UI_PASSWORD|ADMIN_UI_ENABLED|WWW-Authenticate" app.py
   ```
   Expected: zero matches (or only the residual 401-path that no longer references ADMIN_UI_ENABLED).

2. **HMAC validation still intact**:
   ```
   grep -cE "_validate_hmac|_validate_auth" app.py
   ```
   Expected: count is ≥ pre-edit count for `_validate_hmac` (since `_validate_auth` now calls it
   directly); `_validate_auth` call sites at lines ~3162, 3252, 3300, 3359, 3390, 3507, 3582, 3691
   remain (8 sites, give or take).

3. **Operator endpoints unguarded at origin**:
   The 4 sites that previously called `_validate_auth(self, b"")` should now have a comment
   `# Auth: CF Access (edge gate)` instead.

4. **`/earlscheib` handler serves unconditionally**:
   Reading the handler should show it constructing and writing the HTML response without any
   401/404 short-circuit based on ADMIN_UI_ENABLED or basic auth.

5. **Import + pytest green**:
   `python3 -c "import app"` succeeds; `pytest tests/ -x` exits 0.
</verification>

<success_criteria>
- `app.py` no longer contains `_validate_basic_auth`, `ADMIN_UI_USER`, `ADMIN_UI_PASSWORD`, or `ADMIN_UI_ENABLED`.
- `_validate_auth` is HMAC-only.
- The 4 operator endpoints (GET/DELETE /queue, POST /queue/send-now, GET /diagnostic) have no origin-side auth check.
- `/earlscheib` handler serves admin UI HTML directly with no gate.
- All HMAC-only call sites (BMS ingest, telemetry, /commands, /remote-config, /version, /logs, /sms-log POST, templates, schedules) are untouched and continue to enforce HMAC.
- `python3 -c "import app"` succeeds.
- `pytest tests/` passes with no regressions.
- Diff is minimal — no unrelated refactors, no new dependencies, no new helpers.
</success_criteria>

<output>
Quick task complete when both tasks pass verification. The orchestrator handles deployment
(`git push` + `ssh root@earlscheib-pi` + `.env` cleanup of `ADMIN_UI_USER`/`ADMIN_UI_PASSWORD`)
after this plan closes — deployment is out of scope for the plan itself.
</output>
