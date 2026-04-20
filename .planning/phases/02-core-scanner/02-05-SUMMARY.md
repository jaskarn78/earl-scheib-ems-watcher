---
phase: 02-core-scanner
plan: 05
subsystem: cmd/earlscheib + internal/status
tags: [integration, cli, status, testing, ci]
dependency_graph:
  requires: [02-01, 02-02, 02-03, 02-04]
  provides: [working-binary, status-command, test-command, scan-command-wired]
  affects: [cmd/earlscheib/main.go, internal/status, Makefile, .github/workflows/build.yml]
tech_stack:
  added: [internal/status package]
  patterns: [io.Writer injection for testable output, TDD red-green, nil-DB status guard]
key_files:
  created:
    - internal/status/status.go
    - internal/status/status_test.go
  modified:
    - cmd/earlscheib/main.go
    - Makefile
    - .github/workflows/build.yml
    - internal/webhook/webhook_test.go
decisions:
  - "Makefile test target omits CGO_ENABLED=0 for -race (race detector requires CGO on Linux; CGO_ENABLED=0 stays for cross-compile build targets only)"
  - "CI test job uses 'go test ./... -race -count=1' (no CGO_ENABLED=0) because ubuntu-latest has GCC and -race needs cgo"
  - "runStatus passes nil sqlDB when db.Open fails (DB not yet created) so status.Print shows 'No database yet' correctly"
  - "TestSignHMACParity updated to use plan's 3 canonical pinned fixtures replacing prior fixture set"
metrics:
  duration: 245s
  completed: 2026-04-20
  tasks: 2
  files: 6
requirements: [SCAN-01, SCAN-05, SCAN-11, SCAN-12]
---

# Phase 02 Plan 05: Wire main.go + Status Package + CI Tests Summary

**One-liner:** Integration plan wiring all internal packages into main.go — real --scan, --test, --status subcommands plus internal/status package, Makefile test target, and CI test job gating the Windows build.

## What Was Built

### Task 1: internal/status package (TDD)

Created `internal/status/status.go` with `Print(cfg, dataDir, sqlDB, logger, w io.Writer)` — an exact port of `run_status()` from Python `ems_watcher.py` (lines 463–556).

Key behaviors:
- Header block with paths (watch folder, webhook URL, config/db/log file paths)
- Watch folder reachability via `os.Stat` (YES/NO + file count for .xml/.ems)
- nil sqlDB → "No database yet — watcher has not run." early return
- DB stats: last run (run_at UTC, processed, errors, note), today/total file counts, recent 5 files
- Log tail: reads `ems_watcher.log`, filters [ERROR]/[WARNING] lines, prints last 10
- All errors caught inline (no panics)

Tests (7 cases, all green):
- TestPrintNoDB, TestPrintWithRuns, TestPrintFolderReachable, TestPrintFolderUnreachable, TestPrintTodayTotalCounts, TestPrintLogTail, TestPrintWithNilDB_DBFileAbsent

### Task 2: main.go + Makefile + CI + HMAC parity fix

**main.go:** Replaced --scan, --test, --status stubs with real implementations. --tray, --wizard, --install stubs remain for later phases. var secretKey unchanged.

- `runScan()`: loads config, opens DB, InitSchema, heartbeat.Send, scanner.Run with webhook.Send sender; exits 0 on success, 1 on errors
- `runTest()`: sends exact TEST_BMS_XML bytes (port from Python, including leading newline); exits 0 on HTTP 2xx, 1 on failure
- `runStatus()`: db.Open + status.Print(os.Stdout); nil sqlDB when DB absent; exits 0

**Makefile:** Added `test` target (`go test ./... -race -count=1`); added `test` to .PHONY.

**build.yml:** Added `test` job (ubuntu-latest, go vet + go test -race) before `build-windows`; added `needs: [test]` to build-windows job.

**webhook_test.go:** Updated `TestSignHMACParity` to use the 3 canonical pinned fixtures from the plan:
- empty: `7d5e48d090279ce242b5b05aaf181049eb2ff179addbdc46df55c05a81dab082`
- ascii: `e187375b21749c469539f5196bc0dac9168f7486da30174facf29752b7a5bba6`
- unicode_bms: `149db2bc39aeafe700021d262a196b8562c06366b51d0222539c5f8f49323df2`

## Verification Results

- `go test ./... -race -count=1` passes all 7 packages
- `CGO_ENABLED=0 go build -o /dev/null ./cmd/earlscheib` succeeds
- `grep -n "scanner.Run" cmd/earlscheib/main.go` → line 80 match
- `grep -n "needs:" .github/workflows/build.yml` → line 32 `needs: [test]`
- `grep -n "go test ./..." Makefile` → line 50 test target
- `go test ./internal/webhook/... -run TestSignHMACParity -v` → PASS (all 3 subtests)
- `EARLSCHEIB_DATA_DIR=/tmp/x ./dist/earlscheib --scan` → creates ems_watcher.db, exits 0

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 2 - Missing critical functionality] Makefile test target omits CGO_ENABLED=0**
- **Found during:** Task 2 — verification
- **Issue:** Plan specified `CGO_ENABLED=0 go test ./... -race -count=1` but the `-race` flag requires CGO on Linux (the race detector is a C library). Running with `CGO_ENABLED=0 -race` fails with "go: -race requires cgo; enable cgo by setting CGO_ENABLED=1".
- **Fix:** Makefile `test` target uses `go test ./... -race -count=1` (no CGO_ENABLED override). CI test job similarly uses `go test ./... -race -count=1`. CGO_ENABLED=0 remains on the `build-windows` and `build-linux` targets. This is correct: the pure-Go constraint applies to the binary build, not to unit tests which run natively.
- **Files modified:** Makefile, .github/workflows/build.yml
- **Commit:** 2ad3c1f

## Known Stubs

- `--tray`: `runStub("tray")` — Phase 3 (tray UI)
- `--wizard`: `runStub("wizard")` — Phase 3 (wizard UI)
- `--install`: `runStub("install")` — Phase 4 (installer)

These are intentional placeholder stubs for later phases. They do not affect this plan's goals.

## Self-Check: PASSED

- internal/status/status.go: FOUND
- internal/status/status_test.go: FOUND
- cmd/earlscheib/main.go: FOUND (scanner.Run wired)
- Makefile: FOUND (test target line 50)
- .github/workflows/build.yml: FOUND (needs: [test] line 32)
- Commit 9902e1b (test RED): FOUND
- Commit 308f07f (feat GREEN): FOUND
- Commit 2ad3c1f (feat Task 2): FOUND
