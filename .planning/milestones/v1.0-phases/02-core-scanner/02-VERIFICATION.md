---
phase: 02-core-scanner
verified: 2026-04-20T23:13:00Z
status: passed
score: 14/14 must-haves verified
re_verification: false
---

# Phase 2: Core Scanner Verification Report

**Phase Goal:** `earlscheib.exe --scan` behaves identically to the Python reference watcher — same dedup, settle check, HMAC signatures, retry logic, heartbeat, and logging — fully tested in CI without a Windows VM.
**Verified:** 2026-04-20T23:13:00Z
**Status:** PASSED
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|---------|
| 1 | `make test` passes (`go test ./... -race -count=1`) | VERIFIED | All 7 packages pass: config, db, heartbeat, logging, scanner, status, webhook — confirmed by live run |
| 2 | `internal/config` loads INI [watcher] section, missing file returns defaults | VERIFIED | `LoadConfig` uses `ini.Load`, missing path returns defaults without error; DataDir() uses EARLSCHEIB_DATA_DIR override |
| 3 | `internal/db` uses `modernc.org/sqlite`, WAL mode, busy_timeout=30000, schema matching Python | VERIFIED | `_ "modernc.org/sqlite"` import; PRAGMA WAL + busy_timeout=30000 + synchronous=NORMAL; `PRIMARY KEY (filepath, mtime)`; both tables created |
| 4 | `internal/webhook.Sign` + `internal/webhook.Send` (3 attempts, exp backoff, retry policy) | VERIFIED | Sign uses `hmac.New(sha256.New, ...)` + hex; Send manual retry loop: 3 attempts, 1s base doubling; 408/425/429/5xx retry; 4xx immediate fail |
| 5 | Cross-language HMAC parity test with pinned fixtures passes | VERIFIED | `TestSignHMACParity` passes all 3 subtests: empty, ascii, unicode_bms — confirmed live |
| 6 | `internal/heartbeat.Send` posts to /heartbeat with `X-EMS-Heartbeat: 1` | VERIFIED | Posts to `webhookURL+"/heartbeat"`, sets `X-EMS-Heartbeat: 1` header, 10s timeout, non-fatal |
| 7 | `internal/scanner.SettleCheck` requires 4 samples, 2 consecutive mtime+size matches | VERIFIED | `stableCount >= 2` at line 48; loops `opts.Samples` times; `DefaultSettleOptions{Samples:4, Interval:2s}` |
| 8 | `internal/scanner.Candidates` lists .xml/.ems files | VERIFIED | `strings.ToLower(filepath.Ext(e.Name()))` == ".xml" or ".ems"; missing dir returns [] with Warning log |
| 9 | `internal/scanner.Run` does full scan loop with double dedup | VERIFIED | Two `db.IsProcessed` calls (line 81 and line 107) — before and after settle; sha256 + MarkProcessed + RecordRun |
| 10 | `internal/status.Print` outputs reachability, run counts, recent files, recent errors | VERIFIED | Matches Python `run_status` output shape: header, YES/NO, last run, today/total, recent 5 files, last 10 error lines |
| 11 | `internal/logging` uses slog + lumberjack (2MB x 5), format matches Python | VERIFIED | `MaxSize:2, MaxBackups:5, Compress:false`; custom `emsHandler` format `YYYY-MM-DD HH:MM:SS [LEVEL] msg` UTC |
| 12 | No BMS XML body content logged (PII check) | VERIFIED | logging package accepts only message strings; scan.go logs only `filepath.Base(fpath)` and size; send.go logs only filename + byte count + status |
| 13 | `cmd/earlscheib/main.go` wires --scan, --test, --status to real packages; --tray, --wizard, --install remain stubs | VERIFIED | `runScan()` calls `scanner.Run`; `runTest()` sends TEST_BMS_XML; `runStatus()` calls `status.Print`; tray/wizard/install call `runStub()` |
| 14 | `.github/workflows/build.yml` has test job; build-windows has `needs: [test]` | VERIFIED | `test` job runs `go test ./... -race -count=1`; `build-windows` has `needs: [test]` at line 32 |

**Score:** 14/14 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|---------|--------|---------|
| `internal/config/config.go` | LoadConfig + DataDir | VERIFIED | 80 lines; exports Config, LoadConfig, DataDir; no SecretKey field |
| `internal/config/config_test.go` | Test INI parsing + env override | VERIFIED | Tests: missing file, valid INI, whitespace strip, malformed INI, no SecretKey, env override, platform default |
| `internal/logging/logging.go` | SetupLogging + custom handler | VERIFIED | 126 lines; emsHandler implements slog.Handler; lumberjack MaxSize=2 MaxBackups=5 |
| `internal/logging/logging_test.go` | Format + rotation tests | VERIFIED | Tests: file creation, INFO format, DEBUG absent at INFO, case-insensitive level, bogus fallback |
| `internal/db/db.go` | Open, InitSchema, IsProcessed, MarkProcessed, RecordRun, DBRetry | VERIFIED | 166 lines; all 6 functions implemented; RetryBaseDelay exported for test control |
| `internal/db/db_test.go` | WAL, dedup, retry tests | VERIFIED | WAL assertion query; INSERT OR IGNORE dedup; DBRetry 5-attempt loop |
| `internal/webhook/sign.go` | Sign(secret, body) string | VERIFIED | 26 lines; `hmac.New(sha256.New, []byte(secret))`; empty secret guard |
| `internal/webhook/send.go` | Send + SendConfig + SendWithClient | VERIFIED | 139 lines; manual 3-attempt retry; BackoffBase exported; all 4 required headers |
| `internal/webhook/webhook_test.go` | HMAC parity + Send behavior tests | VERIFIED | 3 pinned HMAC fixtures; 200/503/400/network error tests; header assertions |
| `internal/heartbeat/heartbeat.go` | Send(webhookURL, secretKey, logger) | VERIFIED | Posts to /heartbeat; X-EMS-Heartbeat: 1; 10s timeout; non-fatal |
| `internal/heartbeat/heartbeat_test.go` | Body, headers, empty URL, 500 | VERIFIED | 5 tests covering all behavior cases |
| `internal/scanner/settle.go` | SettleCheck(path, opts, log) | VERIFIED | stableCount >= 2; DefaultSettleOptions{4, 2s}; SettleOptions injectable |
| `internal/scanner/scan.go` | Candidates + Run + RunConfig | VERIFIED | Double IsProcessed calls; sha256; injectable Sender; RecordRun always called |
| `internal/scanner/scanner_test.go` | 11 tests covering settle, candidates, run | VERIFIED | TestSettleStable, TestSettleChanging, TestSettleResets, TestSettleFileGone, TestCandidatesEmpty, TestCandidatesFilters, TestCandidatesMissingDir, TestRunDedup, TestRunSettleSkip, TestRunSenderFailure, TestRunMissingFolder |
| `internal/status/status.go` | Print(cfg, dataDir, sqlDB, logger, w) | VERIFIED | 150 lines; io.Writer injection; nil sqlDB guard; all Python output sections ported |
| `internal/status/status_test.go` | NoDB, WithRuns, Reachable, Unreachable | VERIFIED | 7 tests including TodayTotalCounts, LogTail, NilDB |
| `cmd/earlscheib/main.go` | Wired --scan, --test, --status | VERIFIED | scanner.Run at line 80; TEST_BMS_XML verbatim; runStatus calls status.Print |
| `Makefile` | test target | VERIFIED | `go test ./... -race -count=1` at line 50; test in .PHONY |
| `.github/workflows/build.yml` | test job + needs guard | VERIFIED | test job at line 10; `needs: [test]` at line 32 on build-windows |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|----|-----|--------|---------|
| `internal/config/config.go` | `gopkg.in/ini.v1` | `ini.Load(path)` | WIRED | `ini.Load(path)` at line 43 |
| `internal/logging/logging.go` | `gopkg.in/natefinch/lumberjack.v2` | `lumberjack.Logger` | WIRED | `&lumberjack.Logger{...}` at line 105 |
| `internal/db/db.go` | `modernc.org/sqlite` | `_ "modernc.org/sqlite"` | WIRED | Blank import at line 18 |
| `internal/db/db.go` | `processed_files` table | `PRIMARY KEY (filepath, mtime)` | WIRED | Schema at line 74 of db.go |
| `internal/webhook/sign.go` | `crypto/hmac + crypto/sha256` | `hmac.New(sha256.New, ...)` | WIRED | Line 22 of sign.go |
| `internal/webhook/send.go` | `net/http` | `http.Client` manual retry | WIRED | `http.Client{Timeout: timeout}` at line 54 |
| `internal/scanner/settle.go` | `os.Stat` | `stableCount >= 2` | WIRED | Line 48; stableCount resets on mtime/size change |
| `internal/scanner/scan.go` | `internal/db` | `db.IsProcessed + db.MarkProcessed` | WIRED | Lines 81 and 107 (two calls), line 130 (mark) |
| `cmd/earlscheib/main.go` | `internal/scanner` | `scanner.Run(scanner.RunConfig{...})` | WIRED | Line 80 of main.go |
| `cmd/earlscheib/main.go` | `internal/webhook` | `webhook.Send` as `RunConfig.Sender` | WIRED | Lines 72-78 of main.go |

---

### Data-Flow Trace (Level 4)

| Artifact | Data Variable | Source | Produces Real Data | Status |
|----------|--------------|--------|-------------------|--------|
| `internal/scanner/scan.go` | `candidates []string` | `os.ReadDir(dir)` | Yes — live filesystem | FLOWING |
| `internal/scanner/scan.go` | `xmlBytes []byte` | `os.ReadFile(fpath)` | Yes — live file read | FLOWING |
| `internal/db/db.go` | `processed_files` rows | `INSERT OR IGNORE ... VALUES (?, ?, ?, ?)` | Yes — parameterized DB write | FLOWING |
| `internal/status/status.go` | `runAt, note, runProcessed` | `SELECT ... FROM runs ORDER BY rowid DESC LIMIT 1` | Yes — live DB query | FLOWING |
| `internal/status/status.go` | `todayCount, totalCount` | `SELECT COUNT(*) FROM processed_files WHERE ...` | Yes — live DB query | FLOWING |

---

### Behavioral Spot-Checks

| Behavior | Command / Check | Result | Status |
|----------|----------------|--------|--------|
| `--scan` creates db, logs "no files", exits 0 | `mkdir -p $DIR && EARLSCHEIB_DATA_DIR=$DIR ./dist/earlscheib --scan; echo $?` | `[WARNING] Cannot read watch folder` + `[INFO] Run complete` + exit 0 | PASS |
| `make test` passes all packages | `make test` | All 7 packages PASS, no race conditions | PASS |
| HMAC parity test: 3 pinned fixtures | `go test ./internal/webhook/... -run TestSignHMACParity -v` | 3/3 subtests PASS | PASS |
| `go vet ./...` clean | `go vet ./...` | No output (no issues) | PASS |
| `build-linux` produces binary | `make build-linux` | `dist/earlscheib` built successfully | PASS |

---

### Requirements Coverage

| Requirement | Source Plans | Description | Status | Evidence |
|-------------|-------------|-------------|--------|---------|
| SCAN-01 | 02-04, 02-05 | --scan performs single scan of watch folder and exits | SATISFIED | `runScan()` calls `scanner.Run()` which lists candidates, processes, exits |
| SCAN-02 | 02-02 | Deduplicate by (filepath, mtime) using SQLite | SATISFIED | `PRIMARY KEY (filepath, mtime)` + `INSERT OR IGNORE` + double `IsProcessed` calls |
| SCAN-03 | 02-04 | Settle check: mtime+size stable 2 consecutive samples at 2s intervals | SATISFIED | `stableCount >= 2`; `DefaultSettleOptions{Samples:4, Interval:2s}` |
| SCAN-04 | 02-03 | POST with Content-Type, X-EMS-Filename, X-EMS-Source, X-EMS-Signature headers | SATISFIED | All 4 headers set in send.go; X-EMS-Signature absent when secret empty |
| SCAN-05 | 02-03, 02-05 | HMAC parity with Python; verified by cross-language test | SATISFIED | 3 pinned fixtures PASS in TestSignHMACParity |
| SCAN-06 | 02-03 | Retry: 3 attempts, exp backoff, retry 408/425/429/5xx; permanent errors immediate | SATISFIED | Manual retry loop; `isRetryableStatus`; `BackoffBase` doubling |
| SCAN-07 | 02-02 | sha256 tracked in processed_files alongside (filepath, mtime) | SATISFIED | `sha256 TEXT` column in schema; `sha256.Sum256(xmlBytes)` computed and stored |
| SCAN-08 | 02-02 | Record each run in runs table | SATISFIED | `RecordRun(db, processed, errors, note)` called in both empty-candidate and normal-scan paths |
| SCAN-09 | 02-03 | Heartbeat POST to {webhook_url}/heartbeat on each run | SATISFIED | `heartbeat.Send(cfg.WebhookURL, secretKey, logger)` in `runScan()` |
| SCAN-10 | 02-02 | SQLite WAL mode, busy_timeout=30000, 5-retry DB-lock backoff | SATISFIED | PRAGMA WAL + busy_timeout=30000 in Open(); DBRetry 5 attempts 500ms base |
| SCAN-11 | 02-05 | --test sends canned BMS payload, exits 0 on 2xx | SATISFIED | `testPayload` = verbatim TEST_BMS_XML from Python; `webhook.Send()` return drives exit code |
| SCAN-12 | 02-05 | --status prints last run info, today/total counts, recent files, recent errors | SATISFIED | `status.Print` mirrors Python `run_status` output shape |
| SCAN-13 | 02-01 | Log rotation 2MB x5; BMS XML NOT in logs | SATISFIED | `MaxSize:2, MaxBackups:5`; no body content passed to logger anywhere in the call chain |
| SCAN-14 | 02-01, 02-04 | Tolerate missing/unreachable watch folder — log and continue, no crash | SATISFIED | `Candidates` returns [] with Warning log; `Run` returns (0,0) on missing folder; functional test confirms exit 0 |

**Coverage: 14/14 SCAN requirements satisfied**

---

### Anti-Patterns Found

| File | Pattern | Severity | Impact |
|------|---------|---------|--------|
| `cmd/earlscheib/main.go` line 33, 37, 43 | `runStub("tray")`, `runStub("wizard")`, `runStub("install")` | INFO | Intentional placeholder — tray/wizard/install are Phase 3/4 scope; --scan, --test, --status are fully wired |

No blockers or warnings found. The three stubs are expected per plan scope.

---

### Human Verification Required

None for the scope of Phase 2. All Phase 2 behaviors are automatable and were verified programmatically or via live binary execution.

The following are out-of-scope for Phase 2 and will be verified in their respective phases:
- Visual tray icon states (Phase 3)
- Wizard UI flow (Phase 3/4)
- Windows installer behavior (Phase 4)

---

### Gaps Summary

No gaps. All 14 SCAN-* requirements are satisfied. All must-have artifacts exist, are substantive (non-stub), wired into the binary, and produce real data.

**One note on functional test precondition:** The `EARLSCHEIB_DATA_DIR` directory must exist before running `--scan` because SQLite cannot create a database in a non-existent directory. This matches the Python reference behavior (which uses the script's own directory) and the production deployment (installer creates `C:\EarlScheibWatcher\` first). The pre-created-dir pattern is the intended dev/test usage.

---

_Verified: 2026-04-20T23:13:00Z_
_Verifier: Claude (gsd-verifier)_
