---
phase: quick-260515-lae
plan: "01"
subsystem: server
tags: [auth, security, cloudflare, basic-auth, hmac]
dependency_graph:
  requires: []
  provides: [no-origin-basic-auth]
  affects: [app.py, tests/test_queue_endpoint.py]
tech_stack:
  added: []
  patterns: [CF-Access-edge-gate, HMAC-only-machine-endpoints]
key_files:
  modified:
    - app.py
    - tests/test_queue_endpoint.py
decisions:
  - "Templates and schedules endpoints retain HMAC-only auth — plan's must_haves.truths is authoritative; stale line numbers in plan's interfaces block were overridden by canonical endpoint list"
  - "Two pre-existing test failures (test_get_queue_happy_path, test_get_queue_ordering_and_filter) are NOT regressions from this change — confirmed failing on main branch before edits"
  - "3 queue endpoint auth tests updated to assert the new no-origin-auth contract instead of deleting them"
metrics:
  duration: "~45 minutes"
  completed: "2026-05-15"
  tasks_completed: 2
  files_modified: 2
---

# Phase quick-260515-lae Plan 01: Strip Origin Basic Auth Summary

**One-liner:** Removed HTTP Basic Auth layer from all origin-facing endpoints; CF Access at the edge is now the sole gate for operator/browser access to the admin UI and 4 operator endpoints.

## What Was Built

Eliminated the vestigial origin-side HTTP Basic Auth scheme (RJL-02) from `app.py`. The dual-auth gate that re-challenged Marco's Pi browser even when the CF Access cookie was valid is now gone.

**Changes to `app.py` (85 lines removed, 13 added):**
- Removed `ADMIN_UI_USER`, `ADMIN_UI_PASSWORD`, `ADMIN_UI_ENABLED` env var reads
- Deleted `_validate_basic_auth()` function (18 lines)
- Simplified `_validate_auth()` to HMAC-only (from 14 lines to 5 lines)
- Removed `WWW-Authenticate` header insertion from `_send_json()` 401 responses
- Stripped auth gate from 4 operator endpoints with `# Auth: CF Access (edge gate)` comments:
  - GET `/earlscheibconcord/queue`
  - GET `/earlscheibconcord/diagnostic`
  - POST `/earlscheibconcord/queue/send-now`
  - DELETE `/earlscheibconcord/queue`
- Removed `ADMIN_UI_ENABLED` 404 gate and `_validate_basic_auth` 401 gate from `/earlscheib` handler

**HMAC-only endpoints left untouched:**
- GET/PUT `/earlscheibconcord/templates`, `/earlscheibconcord/templates/{job_type}`
- GET/PUT `/earlscheibconcord/schedules`, `/earlscheibconcord/schedules/{job_type}`
- POST `/earlscheibconcord/sms-log`
- POST `/earlscheibconcord/queue/resend`
- POST `/earlscheibconcord/queue/uncancel`
- POST `/earlscheibconcord/reset-test-jobs`
- All watcher-side endpoints (BMS ingest, telemetry, /commands, /remote-config, /version, /logs, /heartbeat)

**Changes to `tests/test_queue_endpoint.py` (24 lines changed):**
Updated 3 tests that verified old Basic Auth behavior to assert the new no-origin-auth contract:
- `test_get_queue_missing_signature` → `test_get_queue_no_signature_allowed` (asserts 200)
- `test_get_queue_bad_signature` → `test_get_queue_with_signature_still_works` (asserts HMAC path still works)
- `test_delete_queue_bad_signature` → `test_delete_queue_no_signature_allowed` (asserts unsigned DELETE processes)

## Deviations from Plan

### Plan Line Number Discrepancy (Resolved Inline)

**Rule: None (design clarification, not a bug fix)**

The plan's `<interfaces>` block listed lines 2602, 2677, 2768, 2830 as the "4 operator endpoints." Actual code at 2677 was `/earlscheibconcord/templates` and at 2768 was `/earlscheibconcord/schedules` — added in 260422-wmh and 260508-spn after the plan's line numbers were written.

**Resolution:** Used the `must_haves.truths` as the authoritative contract. The 4 operator endpoints (GET/DELETE /queue, POST /queue/send-now, GET /diagnostic) were identified by path string grep, not stale line numbers. Templates and schedules were left with HMAC-only auth.

**Impact on browser UX:** After this change, the Templates and Schedules tabs in the public `/earlscheib` UI are no longer accessible from a browser (they now require HMAC). The Go admin proxy (which HMAC-signs server-side) is unaffected. The plan's must_haves.truths explicitly list templates/schedules as HMAC-keep — this is by design. If the public browser needs access to these tabs, a future plan would need to either expose them via CF-Access-only or implement another mechanism.

### Pre-Existing Test Failures (Not Introduced by This Plan)

The following test failures existed on `master` before any edits in this plan. They are pre-existing regressions:

| Test | Failure Cause |
|------|--------------|
| `test_get_queue_happy_path` | DB row count 7 ≠ 3 (env/isolation issue) |
| `test_get_queue_ordering_and_filter` | Ordering mismatch (env/isolation issue) |
| `test_default_template_*` | SHOP_CONSTANTS "Earl Scheib Of Concord" ≠ "Earl Scheib Auto Body Concord" in test expectation |
| `test_templates_endpoint.*` | Same SHOP_CONSTANTS mismatch |
| `test_schedules_endpoint.*` | Timeout/isolation in full suite (pass individually) |

Confirmed by running the same tests against the main repo (`/home/jjagpal/projects/earl-scheib-followup/`) before any changes.

## Verification Results

- `python3 -c "import app"` succeeds with no errors
- `grep -n "_validate_basic_auth|ADMIN_UI_USER|ADMIN_UI_PASSWORD|ADMIN_UI_ENABLED" app.py` returns zero matches
- `grep -n "WWW-Authenticate" app.py` returns zero matches
- 5 `# Auth: CF Access (edge gate)` comments placed at operator endpoint sites
- `_validate_auth` remains as HMAC-only gate; called at 8 machine-to-machine sites
- Tests updated for new contract pass individually

## Commits

| Commit | Description |
|--------|-------------|
| `9500d38` | fix(quick-260515-lae): strip origin-side Basic Auth — CF Access is sole gate |
| `83d0302` | test(quick-260515-lae): update queue auth tests to match no-origin-auth contract |

## Self-Check: PASSED

- app.py modified: found at `/home/jjagpal/projects/earl-scheib-followup/.claude/worktrees/agent-a884d80ee02e08c53/app.py`
- tests/test_queue_endpoint.py modified: found
- Commits `9500d38` and `83d0302` exist in git log
- No banned symbols (`_validate_basic_auth`, `ADMIN_UI_*`, `WWW-Authenticate`) in app.py
- 5 CF Access gate comments present in app.py
