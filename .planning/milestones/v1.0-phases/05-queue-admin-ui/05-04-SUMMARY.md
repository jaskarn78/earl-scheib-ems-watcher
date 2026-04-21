---
phase: 05-queue-admin-ui
plan: 04
subsystem: ui
tags: [go, admin-ui, embed-fs, dispatcher, docs, requirements]

# Dependency graph
requires:
  - phase: 05-queue-admin-ui 05-02
    provides: internal/admin package with Run(), Open(), Config, uiFS() accessor
  - phase: 05-queue-admin-ui 05-03
    provides: UI assets (index.html, main.css, main.js) in internal/admin/ui/

provides:
  - "--admin subcommand wired into cmd/earlscheib/main.go dispatcher"
  - "runAdmin function following exact runStatus/runScan telemetry pattern"
  - "internal/admin/ui_test.go proving embed FS serves all three UI files"
  - "docs/admin-ui-guide.md user guide for Marco"
  - "REQUIREMENTS.md ADMIN-01..11 block with Phase 5 traceability rows"
  - "PROJECT.md and REQUIREMENTS.md Out of Scope updated to qualify web-admin-UI"

affects:
  - cmd/earlscheib
  - internal/admin
  - docs
  - .planning/REQUIREMENTS.md
  - .planning/PROJECT.md

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "runAdmin mirrors runStatus: config load, logging.SetupLogging, telemetry.Init re-init, tel.Wrap body"
    - "In-package test (package admin, not admin_test) accesses unexported uiFS() for embed FS verification"
    - "Go 1.22+ builtin min() used in test — no local shim declared"

key-files:
  created:
    - cmd/earlscheib/main.go (modified — admin import, case, runAdmin, printUsage)
    - internal/admin/ui_test.go
    - docs/admin-ui-guide.md
  modified:
    - .planning/REQUIREMENTS.md
    - .planning/PROJECT.md

key-decisions:
  - "runAdmin uses context.Background() because admin.Run has its own SIGINT watchdog via signal.NotifyContext internally"
  - "ui_test.go placed in plan 05-04 (Wave 3) to sequence after both 05-02 (uiFS) and 05-03 (UI assets) — prevents parallel-wave compile race"
  - "Go 1.25.0 (already in go.mod) provides the min builtin; no go.mod bump needed"

patterns-established:
  - "Subcommand pattern: import package, add case in switch, implement runXxx(tel) mirroring runStatus"
  - "Embed FS test pattern: package admin in-package test, http.FileServer(http.FS(uiFS())), httptest.NewServer"

requirements-completed:
  - ADMIN-11

# Metrics
duration: 4min
completed: 2026-04-21
---

# Phase 05 Plan 04: Queue Admin UI — Wire-up, Tests, Docs, Requirements Summary

**`earlscheib.exe --admin` is a first-class subcommand: wired in dispatcher with full telemetry wrapping, backed by embed-FS test, documented for Marco, and tracked in REQUIREMENTS.md as ADMIN-01..11**

## Performance

- **Duration:** 4 min
- **Started:** 2026-04-21T06:53:12Z
- **Completed:** 2026-04-21T06:57:00Z
- **Tasks:** 4
- **Files modified:** 5

## Accomplishments

- `--admin` subcommand wired into `cmd/earlscheib/main.go` dispatcher — follows exact `runStatus` pattern (config load, `logging.SetupLogging`, `telemetry.Init` re-init, `tel.Wrap` body), calls `admin.Run` with `context.Background()` and `admin.Open` as browser opener
- `internal/admin/ui_test.go` added as in-package test (`package admin`) confirming `uiFS()` + all three UI files (`index.html`, `main.css`, `main.js`) are served correctly via `http.FileServer(http.FS(uiFS()))` — uses Go 1.25 builtin `min()`, no local shim
- `docs/admin-ui-guide.md` written in plain English for Marco covering launch, queue view, cancel-with-undo (5s window), manual refresh, close lifecycle, and troubleshooting table (85 lines)
- `REQUIREMENTS.md` updated with full `ADMIN-01..11` block and 11 Phase 5 traceability rows; stale "Web-based admin UI — tray + wizard" Out of Scope bullet qualified to distinguish settings UI (still out of scope) from read-only queue inspector (in scope)
- `PROJECT.md` Out of Scope bullet updated to match and Phase 5 scope-reversal row added to Key Decisions table

## Task Commits

Each task was committed atomically:

1. **Task 1: Wire --admin into the dispatcher** - `5e5a38a` (feat)
2. **Task 2: ui_test.go — verify embed FS serves all three files** - `2efa522` (test)
3. **Task 3: Write docs/admin-ui-guide.md** - `4ab2b97` (docs)
4. **Task 4: Update REQUIREMENTS.md and PROJECT.md** - `1d323fa` (docs)

## Files Created/Modified

- `cmd/earlscheib/main.go` - Added `internal/admin` import, `case "--admin": runAdmin(tel)`, `runAdmin` function, updated `printUsage()` with `--admin`
- `internal/admin/ui_test.go` - In-package test (56 lines) verifying embed FS serves index.html, main.css, main.js
- `docs/admin-ui-guide.md` - User-facing 85-line guide for Marco covering full launch/use/close lifecycle
- `.planning/REQUIREMENTS.md` - ADMIN-01..11 section added, Out of Scope qualified, 11 traceability rows appended
- `.planning/PROJECT.md` - Out of Scope bullet updated, Phase 5 scope-reversal Key Decision row added

## Decisions Made

- `runAdmin` passes `context.Background()` to `admin.Run` because the admin server manages its own SIGINT/SIGTERM shutdown via `signal.NotifyContext` internally — no external cancellation context needed from the dispatcher
- `ui_test.go` sequenced in plan 05-04 (Wave 3) strictly after 05-02 (uiFS provider) and 05-03 (UI asset provider) to avoid parallel-wave compile failure
- Go 1.25.0 already in `go.mod` satisfies the Go 1.22+ requirement for the `min` builtin; no `go.mod` changes needed

## Deviations from Plan

None — plan executed exactly as written. All four tasks completed as specified with all acceptance criteria met.

## Issues Encountered

None. Both Linux and Windows cross-compile targets passed on first attempt. Full test suite (`make test` with `-race`) green throughout.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

Phase 5 (queue-admin-ui) is now complete. All four plans (05-01 through 05-04) have been executed:
- 05-01: server-side `/queue` endpoints in `app.py`
- 05-02: `internal/admin` package (server, proxy, launcher, embed FS)
- 05-03: UI assets (index.html, main.css, main.js) with "Concord Garage" aesthetic
- 05-04: dispatcher wiring, embed test, docs, requirements tracking

`earlscheib.exe --admin` is production-ready: single ephemeral-port server, HMAC-signed proxy, 30s heartbeat watchdog, 5s cancel-undo flow, and Authenticode-signed binary (via CI). Marco can run it by double-clicking a shortcut or from the command line.

---
*Phase: 05-queue-admin-ui*
*Completed: 2026-04-21*

## Self-Check: PASSED

All files verified present and all commits verified in git log.

| Item | Status |
|------|--------|
| cmd/earlscheib/main.go | FOUND |
| internal/admin/ui_test.go | FOUND |
| docs/admin-ui-guide.md | FOUND |
| .planning/phases/05-queue-admin-ui/05-04-SUMMARY.md | FOUND |
| commit 5e5a38a (Task 1) | FOUND |
| commit 2efa522 (Task 2) | FOUND |
| commit 4ab2b97 (Task 3) | FOUND |
| commit 1d323fa (Task 4) | FOUND |
