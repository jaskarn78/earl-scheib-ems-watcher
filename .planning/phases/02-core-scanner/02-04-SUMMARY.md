---
phase: 02-core-scanner
plan: 04
subsystem: scanner
tags: [scanner, settle-check, dedup, candidates, tdd]
dependency_graph:
  requires: [02-01, 02-02]
  provides: [internal/scanner]
  affects: [cmd/earlscheib/main.go, internal/webhook]
tech_stack:
  added: []
  patterns: [injectable-sender-func, settle-check-options-injection, tdd-red-green]
key_files:
  created:
    - internal/scanner/settle.go
    - internal/scanner/scan.go
    - internal/scanner/scanner_test.go
  modified: []
decisions:
  - "SettleCheck log func injected (not slog.Logger) to keep settle.go dependency-free and easily testable with t.Logf"
  - "RecordRun called in both empty-candidates early-return and normal scan-end paths — matching Python dual-call behaviour"
  - "itoa helper keeps strconv out of scan.go imports; scan.go intentionally imports only stdlib + internal/db"
  - "Sender func(filePath string, body []byte) bool injected in RunConfig — scanner never imports internal/webhook"
metrics:
  duration: "~12 minutes"
  completed_date: "2026-04-20T23:00:15Z"
  tasks_completed: 2
  files_created: 3
---

# Phase 2 Plan 4: Scanner Package Summary

Exact Go port of Python's `_list_candidates`, `_wait_for_settle`, and `scan_and_send` into `internal/scanner` with full unit test coverage and injectable Sender interface.

## What Was Built

### internal/scanner/settle.go

`SettleCheck(path string, opts SettleOptions, log func(...) ) (fs.FileInfo, bool)` — polls path at `opts.Interval` for `opts.Samples` iterations. Returns `(info, true)` only when `mtime` AND `size` are identical for 2 consecutive samples (`stableCount >= 2`). `stableCount` resets to 0 on any change. Returns `(nil, false)` if file becomes inaccessible. Port of Python `_wait_for_settle` — semantics preserved at the algorithmic level.

`SettleOptions{Samples int, Interval time.Duration}` is injectable so tests use `{Samples: 2..4, Interval: 1ms}` instead of the production `{Samples: 4, Interval: 2s}`.

### internal/scanner/scan.go

`Candidates(dir string, logger *slog.Logger) []string` — reads directory entries, returns full paths of `.xml` and `.ems` files (case-insensitive). On any OS/permission error, logs Warning and returns empty slice. Port of Python `_list_candidates`.

`Run(cfg RunConfig) (int, int)` — scan loop matching Python `scan_and_send`:
1. List candidates
2. Pre-settle dedup: `db.IsProcessed(path, mtime)` — skip if true
3. `SettleCheck` — skip if not settled
4. Post-settle dedup: `db.IsProcessed(path, mtime_after_settle)` — skip if already processed with updated mtime
5. `os.ReadFile` → `sha256.Sum256` → `cfg.Sender(path, bytes)`
6. On success: `db.MarkProcessed`; on failure: `errors++`
7. `db.RecordRun` always called

`RunConfig.Sender func(filePath string, body []byte) bool` — injectable to avoid importing `internal/webhook` from the scanner package. Production callers pass `webhook.Send`; tests pass a closure.

### internal/scanner/scanner_test.go

11 tests covering all required behaviours:

| Test | Covers |
|------|--------|
| `TestSettleStable` | Stable file → `(info, true)` |
| `TestSettleChanging` | Continuously growing file → `(nil, false)` |
| `TestSettleResets` | Write once then stable → stableCount resets, eventually settles |
| `TestSettleFileGone` | File deleted mid-settle → `(nil, false)` |
| `TestCandidatesEmpty` | Empty dir → `[]` |
| `TestCandidatesFilters` | .xml/.ems (case-insensitive) only; ignores .txt, .pdf |
| `TestCandidatesMissingDir` | Non-existent path → `[]` without panic |
| `TestRunDedup` | First run processed=1; second run processed=0 (mtime in DB) |
| `TestRunSettleSkip` | Continuously growing file skipped (settle fails) |
| `TestRunSenderFailure` | Sender returns false → errors=1, file NOT in DB |
| `TestRunMissingFolder` | Non-existent WatchFolder → (0, 0) without panic |

All tests use `tempdir` + goroutines, `db.Open(t.TempDir()+"/test.db")` for isolated DBs, and `SettleOptions{Samples: 2..4, Interval: 1ms}` for fast execution. No real network, no filesystem outside tempdir.

## Verification

```
$ go test ./internal/scanner -race -count=1 -v
...
PASS
ok      github.com/jjagpal/earl-scheib-watcher/internal/scanner  2.268s
```

All 11 tests pass with `-race -count=1`.

```
$ grep -n "stableCount >= 2" internal/scanner/settle.go
48:                        if stableCount >= 2 {

$ grep -n "IsProcessed" internal/scanner/scan.go
81:    already, err := db.IsProcessed(cfg.DB, fpath, mtime)
107:    already, err = db.IsProcessed(cfg.DB, fpath, mtime)
```

Two `IsProcessed` calls per candidate (before and after settle) — matching Python re-dedup guard.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 3 - Blocking] scan.go needed stub before settle tests could compile**
- **Found during:** Task 1 RED verification
- **Issue:** `scanner_test.go` references `Candidates`, `RunConfig`, `Run` — these are in Task 2. Without them the package fails to build and settle tests cannot run.
- **Fix:** Created full `scan.go` implementation alongside `settle.go` rather than a stub, allowing both test suites to compile and run together.
- **Files modified:** `internal/scanner/scan.go`
- **Commit:** e9cf273

**2. [Rule 1 - Bug] SettleCheck return value order typo in scan.go**
- **Found during:** Task 2 build check
- **Issue:** `settled, settledInfo := SettleCheck(...)` assigned `(fs.FileInfo, bool)` return values in wrong order.
- **Fix:** Corrected to `settledInfo, settled := SettleCheck(...)`.
- **Files modified:** `internal/scanner/scan.go`
- **Commit:** e9cf273 (fixed before commit)

### Notes

- Plan's grep verification says `grep RecordRun` returns "1 match" but correct Python-parity behaviour requires 2 calls (one for empty-candidate early return, one at end of normal scan loop). Kept 2-call implementation to match Python semantics.
- `SettleCheck` log parameter is `func(msg string, args ...any)` rather than `*slog.Logger` so tests can pass `t.Logf` directly without wrapping.

## Known Stubs

None — all exported functions are fully implemented and wired.

## Self-Check: PASSED

- `internal/scanner/settle.go` — FOUND
- `internal/scanner/scan.go` — FOUND
- `internal/scanner/scanner_test.go` — FOUND
- Commit `8df84c1` (settle.go) — FOUND
- Commit `e9cf273` (scan.go) — FOUND
- All 11 tests PASS with `-race -count=1`
