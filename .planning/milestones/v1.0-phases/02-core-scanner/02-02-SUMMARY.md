---
phase: 02-core-scanner
plan: 02
subsystem: database
tags: [sqlite, modernc, wal, dedup, retry, backoff]

# Dependency graph
requires:
  - phase: 01-scaffold-signing
    provides: go.mod module path, CGO_ENABLED=0 constraint
provides:
  - internal/db package with Open, InitSchema, IsProcessed, MarkProcessed, RecordRun, DBRetry
  - SQLite WAL schema compatible with existing Python ems_watcher.db
  - Exponential retry helper for database lock contention
affects:
  - 02-03 (scanner uses db.IsProcessed/MarkProcessed/RecordRun)
  - 02-05 (cmd/main.go wires db.Open/db.InitSchema)

# Tech tracking
tech-stack:
  added:
    - modernc.org/sqlite v1.49.1 (pure-Go SQLite, CGO_ENABLED=0)
  patterns:
    - TDD: failing tests committed before implementation
    - RetryBaseDelay exported package var for test-speed control (no mocking needed)
    - Pure-Go driver registered via blank import: _ "modernc.org/sqlite"

key-files:
  created:
    - internal/db/db.go
    - internal/db/db_test.go
  modified:
    - go.mod (go upgraded to 1.25.0 for modernc.org/sqlite v1.49.1 requirement)
    - go.sum

key-decisions:
  - "go.mod upgraded from 1.22.2 to 1.25.0 automatically when modernc.org/sqlite v1.49.1 required it"
  - "RetryBaseDelay exported (not unexported) so tests set it to 1ns without exec overhead"
  - "DBRetry returns last error after 5 attempts matching Python _db_retry raise behaviour"

patterns-established:
  - "Package-level var for delays (RetryBaseDelay) — lets tests skip real sleeps without interface injection"
  - "db functions accept *sql.DB not a wrapper struct — keeps API surface minimal and avoids over-abstraction"

requirements-completed: [SCAN-02, SCAN-07, SCAN-08, SCAN-10]

# Metrics
duration: 3min
completed: 2026-04-20
---

# Phase 2 Plan 02: DB Package Summary

**Pure-Go SQLite db package with WAL mode, (filepath, mtime) dedup via INSERT OR IGNORE, and 5-attempt exponential retry on lock errors — schema byte-compatible with existing Python ems_watcher.db**

## Performance

- **Duration:** 3 min
- **Started:** 2026-04-20T22:50:38Z
- **Completed:** 2026-04-20T22:53:30Z
- **Tasks:** 1 (TDD: RED + GREEN)
- **Files modified:** 4

## Accomplishments

- Open() applies PRAGMA busy_timeout=30000, journal_mode=WAL, synchronous=NORMAL matching Python open_db()
- InitSchema() creates processed_files and runs tables with IF NOT EXISTS; idempotent on existing Python DB
- INSERT OR IGNORE dedup on (filepath, mtime) PRIMARY KEY — second call for same key is a silent no-op
- DBRetry() retries 5 times on "locked" errors with 500ms base delay doubling each attempt; exits immediately on non-lock errors
- 8 tests all pass under -race: schema idempotency, WAL assertion, dedup (same mtime no-op + new mtime second row), RecordRun, retry success/exhausted/non-lock, PRIMARY KEY constraint

## Task Commits

1. **RED — failing tests** - `5902a51` (test)
2. **GREEN — implementation** - `99f7c26` (feat)

## Files Created/Modified

- `internal/db/db.go` - Open, InitSchema, IsProcessed, MarkProcessed, RecordRun, DBRetry
- `internal/db/db_test.go` - 8 unit tests covering all public API + schema verification
- `go.mod` - modernc.org/sqlite v1.49.1 added; go version raised to 1.25.0
- `go.sum` - generated checksums

## Decisions Made

- go.mod go version raised from 1.22.2 to 1.25.0 automatically by `go get` (modernc.org/sqlite v1.49.1 requires go >= 1.25.0). No manual action required; toolchain upgrade is transparent.
- RetryBaseDelay is an exported package-level variable (not interface injection) — simplest testable pattern for a single delay knob.
- DBRetry returns the last error (not a wrapped sentinel) after 5 attempts, matching Python's re-raise behavior.

## Deviations from Plan

None — plan executed exactly as written. The one minor adaptation was the automatic go version bump from 1.22.2 to 1.25.0 required by modernc.org/sqlite v1.49.1; this is a transparent toolchain upgrade with no behavioral impact.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- internal/db is ready for use by 02-03 (scanner) and 02-05 (cmd wiring)
- go.mod now includes modernc.org/sqlite; subsequent plans do not need to re-add it
- No blockers

---
*Phase: 02-core-scanner*
*Completed: 2026-04-20*
