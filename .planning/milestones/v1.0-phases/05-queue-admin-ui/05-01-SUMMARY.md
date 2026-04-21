---
phase: 05-queue-admin-ui
plan: 01
subsystem: api
tags: [python, sqlite, hmac, pytest, http-server, queue-management]

# Dependency graph
requires:
  - phase: 04-telemetry-remote-config
    provides: "_validate_hmac helper, get_db() + jobs schema, do_GET/do_POST method pattern on WebhookHandler"
provides:
  - "GET /earlscheibconcord/queue — HMAC-validated list of pending jobs as JSON array (sent=0, ordered by send_at ASC)"
  - "DELETE /earlscheibconcord/queue — HMAC-validated job cancellation by id; guard prevents deleting sent jobs"
  - "pytest test suite (8 tests) with ephemeral HTTPServer fixture and isolated SQLite DB per test"
  - "requirements-dev.txt pinning pytest>=8.0 for server-side test runs"
affects: [05-02, 05-03, 05-04, go-admin-proxy]

# Tech tracking
tech-stack:
  added: [pytest>=8.0 (dev)]
  patterns:
    - "GET endpoint signs empty body b'' — consistent with remote-config precedent"
    - "DELETE endpoint signs exact JSON body bytes received — consistent with telemetry precedent"
    - "do_DELETE method on BaseHTTPRequestHandler dispatched automatically by Python's http.server framework"
    - "HTTPServer(('127.0.0.1', 0), handler) ephemeral port pattern for test isolation"
    - "importlib.reload(app) in conftest after monkeypatch.setenv — forces module-level constants to pick up fixture env"

key-files:
  created:
    - tests/__init__.py
    - tests/conftest.py
    - tests/test_queue_endpoint.py
    - requirements-dev.txt
  modified:
    - app.py

key-decisions:
  - "GET /queue response is bare JSON array — NOT wrapped in {queued, count} (CONTEXT.md canonical spec supersedes UI-SPEC wrapper)"
  - "DELETE success response is {\"deleted\": 1} (integer count, not boolean) — CONTEXT.md spec"
  - "DELETE 404 message collapses missing-row and already-sent cases: {\"error\": \"not found or already sent\"} — avoids leaking sent state"
  - "DELETE guard: DELETE FROM jobs WHERE id = ? AND sent = 0 — never deletes sent jobs, idempotent"
  - "Dead code block (Status internal only / pending_jobs) after default 404 return removed — was unreachable since Phase 4 audit"

patterns-established:
  - "Test fixture reloads app module after monkeypatch to capture module-level env vars"
  - "All test HTTP calls use urllib.request (stdlib only — no requests dependency in tests)"
  - "Ephemeral HTTPServer port via HTTPServer(('127.0.0.1', 0)) + server.server_address[1]"

requirements-completed: [ADMIN-05, ADMIN-06]

# Metrics
duration: 3min
completed: 2026-04-21
---

# Phase 5 Plan 01: Queue Admin UI — Server Endpoints Summary

**HMAC-authenticated GET + DELETE /earlscheibconcord/queue endpoints on app.py with 8-test pytest suite covering happy path, 401 rejection, and sent-job idempotency guard**

## Performance

- **Duration:** 3 min
- **Started:** 2026-04-21T06:41:05Z
- **Completed:** 2026-04-21T06:43:55Z
- **Tasks:** 3
- **Files modified:** 5

## Accomplishments
- Added GET /earlscheibconcord/queue inside do_GET: validates HMAC of empty body, returns pending jobs as JSON array ordered by send_at ASC, excludes the `sent` column from the response
- Added do_DELETE method on WebhookHandler: validates HMAC over JSON body bytes, deletes only sent=0 rows, returns `{"deleted": 1}` or `{"error": "not found or already sent"}` with 404
- Removed unreachable dead code block (lines 1278-1296 in pre-edit app.py: "Status internal only" comment + pending_jobs JSON) that was left over from a prior refactor
- Created pytest fixture (conftest.py) spinning up an ephemeral HTTPServer with isolated SQLite DB per test, seeded with 4 deterministic rows (3 pending, 1 already-sent)
- All 8 tests pass: GET happy path, missing sig, bad sig, ordering+filter; DELETE happy path, missing row, already-sent guard, bad sig

## Task Commits

Each task was committed atomically:

1. **Task 1: Add GET /earlscheibconcord/queue + remove dead code block** - `b424102` (feat)
2. **Task 2: Add do_DELETE method for /earlscheibconcord/queue cancellation** - `a8bb7bc` (feat)
3. **Task 3: Write pytest coverage** - (included in Task 1 commit — tests written in RED phase before implementation)

## Files Created/Modified
- `app.py` — Added GET /earlscheibconcord/queue branch in do_GET and new do_DELETE method; removed dead code block after default 404
- `tests/__init__.py` — Empty package marker for pytest discovery
- `tests/conftest.py` — queue_server fixture: ephemeral HTTPServer, isolated DB, 4 seeded rows, module reload after monkeypatch
- `tests/test_queue_endpoint.py` — 8 tests: test_get_queue_happy_path, test_get_queue_missing_signature, test_get_queue_bad_signature, test_get_queue_ordering_and_filter, test_delete_queue_happy_path, test_delete_queue_missing_row, test_delete_queue_already_sent, test_delete_queue_bad_signature
- `requirements-dev.txt` — Pins pytest>=8.0, python-dotenv>=1.0, pytz>=2024.1

## Decisions Made
- GET response is a bare JSON array (not wrapped in `{"queued": [...], "count": N}`) — CONTEXT.md GET response shape is canonical, UI-SPEC wrapper is superseded
- DELETE success response `{"deleted": 1}` uses integer count — matches CONTEXT.md; `{"deleted": true}` variant rejected
- DELETE 404 returns `{"error": "not found or already sent"}` for both missing-row and already-sent cases — prevents state leakage via error message differentiation
- Dead code removal treated as part of Task 1 (not a separate deviation) — block was explicitly listed in plan's acceptance criteria

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered
None — `python3 -m pip install` was blocked by system PEP 668 restriction, but pytest was already available system-wide so all tests ran without additional installation.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- Server endpoints fully implemented and tested — ready for Plan 05-02 (Go admin launcher / proxy)
- The Go proxy in 05-02 will call GET /earlscheibconcord/queue signing b"" and DELETE signing the JSON body bytes — both patterns validated here
- No blockers

---
*Phase: 05-queue-admin-ui*
*Completed: 2026-04-21*
