---
phase: 02-core-scanner
plan: 01
subsystem: infra
tags: [go, ini, slog, lumberjack, logging, config, file-rotation]

# Dependency graph
requires:
  - phase: 01-scaffold-signing
    provides: go.mod module declaration, cmd/earlscheib/main.go dispatcher skeleton
provides:
  - internal/config: LoadConfig(path) parses [watcher] INI section, DataDir() resolves data directory
  - internal/logging: SetupLogging(dataDir, level) returns *slog.Logger with rotating file + console
affects:
  - 02-02 (db package imports config for DataDir)
  - 02-03 (scanner imports config + logging)
  - 02-04 (webhook imports logging)
  - 02-05 (heartbeat imports logging)

# Tech tracking
tech-stack:
  added:
    - gopkg.in/ini.v1 v1.67.1 — INI config parsing
    - gopkg.in/natefinch/lumberjack.v2 v2.2.1 — rotating file log handler
  patterns:
    - Custom slog.Handler (emsHandler) for exact Python-matching log format
    - LoadConfig returns defaults on missing/malformed file (no-crash tolerance)
    - DataDir() env-override pattern for cross-platform dev testing

key-files:
  created:
    - internal/config/config.go
    - internal/config/config_test.go
    - internal/logging/logging.go
    - internal/logging/logging_test.go
  modified:
    - go.mod (added ini.v1 + lumberjack deps)
    - go.sum

key-decisions:
  - "Custom emsHandler implements slog.Handler directly for exact Python log format match (YYYY-MM-DD HH:MM:SS [LEVEL] message UTC)"
  - "LoadConfig returns defaults (not error) on missing or malformed INI — matches Python load_config() fall-through behaviour"
  - "SecretKey intentionally absent from Config struct — baked into binary via ldflags per SCAF-04 security constraint"
  - "EARLSCHEIB_DATA_DIR env var overrides data directory on any OS for Linux dev/test runs"
  - "WARNING maps to slog.LevelWarn; Python uses WARNING not WARN in log output — emsHandler outputs [WARNING]"

patterns-established:
  - "Config package: return defaults on missing/malformed config, never crash — matches Python tolerance"
  - "Logging package: custom slog.Handler + io.MultiWriter for file+console dual output"
  - "All internal packages use t.TempDir() for fixture isolation in tests"

requirements-completed: [SCAN-13, SCAN-14]

# Metrics
duration: 4min
completed: 2026-04-20
---

# Phase 02 Plan 01: Config and Logging Packages Summary

**slog-based rotating logger (lumberjack 2MB x5) and INI config loader ported exactly from Python ems_watcher.py, with EARLSCHEIB_DATA_DIR dev override and custom UTC timestamp format**

## Performance

- **Duration:** 4 min
- **Started:** 2026-04-20T22:50:31Z
- **Completed:** 2026-04-20T22:54:04Z
- **Tasks:** 2
- **Files modified:** 6

## Accomplishments

- `internal/config.LoadConfig` parses [watcher] INI section; missing or malformed file returns defaults without error (matches Python fall-through)
- `internal/config.DataDir()` returns EARLSCHEIB_DATA_DIR override or platform default (Windows `C:\EarlScheibWatcher\`, Linux `$HOME/.earlscheib-dev/`)
- `internal/logging.SetupLogging` produces rotating log at `{dataDir}/ems_watcher.log` via lumberjack (MaxSize=2MB, MaxBackups=5, Compress=false) plus console stdout, format exactly matching Python: `YYYY-MM-DD HH:MM:SS [LEVEL] message` in UTC
- All 15 unit tests pass under `-race -count=1`; `go vet` clean

## Task Commits

Each task was committed atomically:

1. **Task 1: internal/config — INI parsing + DataDir resolver** - `bfa584b` (feat)
2. **Task 2: internal/logging — slog + lumberjack rotation** - `d53b4f2` (feat)

_Note: Both tasks followed TDD (RED → GREEN) pattern._

## Files Created/Modified

- `internal/config/config.go` — Config struct, LoadConfig(), DataDir() with env override
- `internal/config/config_test.go` — 7 tests: missing file, valid INI, whitespace strip, malformed INI, no SecretKey field, env override, platform default
- `internal/logging/logging.go` — SetupLogging(), emsHandler custom slog.Handler, lumberjack rotation
- `internal/logging/logging_test.go` — 8 tests: file creation, INFO format, DEBUG absent at INFO, case-insensitive level, bogus level fallback, stdout output, *slog.Logger type, WARN level
- `go.mod` — added gopkg.in/ini.v1 v1.67.1 and gopkg.in/natefinch/lumberjack.v2 v2.2.1
- `go.sum` — updated checksums

## Decisions Made

- Used a custom `emsHandler` struct implementing `slog.Handler` rather than `slog.NewTextHandler` with `ReplaceAttr` — the custom handler gives exact control over the output format to match Python's `%(asctime)s [%(levelname)s] %(message)s` pattern. `slog.NewTextHandler` would produce key=value format.
- `SecretKey` is deliberately absent from `Config` struct — it is injected at build time via `-ldflags "-X main.secretKey=..."` per SCAF-04. This prevents config files from ever containing the secret.
- `WARNING` is used as the level bracket string (not `WARN`) to match Python's `logging.WARNING` level name output.

## Deviations from Plan

None — plan executed exactly as written. Both packages implemented with TDD (RED → GREEN) and all verification criteria met.

## Known Stubs

None — both packages are fully wired and functional with no placeholder data.

## Issues Encountered

None — implementation was straightforward. The `time` package import was unused after removing a redundant sentinel variable; cleaned up before final commit.

## User Setup Required

None — no external service configuration required.

## Next Phase Readiness

- `internal/config` and `internal/logging` are ready to import by all other Phase 2 packages (db, scanner, webhook, heartbeat)
- `DataDir()` provides the single source of truth for data directory resolution across all packages
- No blockers for 02-02 (db package)

---
*Phase: 02-core-scanner*
*Completed: 2026-04-20*
