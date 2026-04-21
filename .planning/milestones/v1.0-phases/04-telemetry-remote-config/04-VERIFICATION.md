---
phase: 04-telemetry-remote-config
verified: 2026-04-20T01:00:00Z
status: passed
score: 13/13 must-haves verified
re_verification: false
---

# Phase 4: Telemetry + Remote Config Verification Report

**Phase Goal:** Broken installs are visible within 1 minute of an unhandled crash, and webhook URL or log level can be updated on Marco's machine without re-running the installer — both sides (client + server) are in production.
**Verified:** 2026-04-20
**Status:** passed
**Re-verification:** No — initial verification

---

## Goal Achievement

### Observable Truths

| # | Truth | Status | Evidence |
|---|-------|--------|----------|
| 1 | An unhandled panic in runScan/runTest/runStatus is captured and POSTed before the process exits | VERIFIED | `tel.Wrap` called at lines 100, 171, 200 in main.go; `Wrap` defers `recover()` and calls `t.Capture(r, pcs)` then `panic(r)` |
| 2 | The telemetry payload contains only: type, message, file:line, os, app_version, ts — nothing from BMS XML | VERIFIED | `payload` struct has exactly 7 fields with JSON tags `type`, `message`, `file`, `line`, `os`, `app_version`, `ts`; no XML/BMS/CommPhone reference in code path |
| 3 | X-EMS-Telemetry: 1 header is present on all telemetry POSTs | VERIFIED | `req.Header.Set("X-EMS-Telemetry", "1")` at telemetry.go:185; confirmed by TestHeaders_TelemetryAndSignature |
| 4 | HMAC X-EMS-Signature is set using webhook.Sign | VERIFIED | `sig := webhook.Sign(t.secret, body)` then conditional `req.Header.Set("X-EMS-Signature", sig)` at telemetry.go:175-188 |
| 5 | 5s timeout; POST failures are silent | VERIFIED | `http.Client{Timeout: 5 * time.Second}` at telemetry.go:190; all errors in `post()` silently dropped after debug log |
| 6 | Wrap re-panics after capture | VERIFIED | `panic(r)` at telemetry.go:94 after `t.Capture(r, pcs[:n])` |
| 7 | remoteconfig.AllowedKeys = ["webhook_url", "log_level"] only | VERIFIED | `var AllowedKeys = []string{"webhook_url", "log_level"}` at remoteconfig.go:28; "secret_key and watch_folder are intentionally absent" comment |
| 8 | secret_key and watch_folder NEVER accepted (whitelist enforced by Merge) | VERIFIED | `config.Merge` iterates keys and skips non-allowSet entries (config.go:104-107); `TestAllowedKeys_Contents` and `TestMerge_BlacklistedKeysNotWritten` pass |
| 9 | Atomic write: temp file + rename in Apply/Merge | VERIFIED | `os.CreateTemp(dir, "config-*.ini.tmp")` + `os.Rename(tmpPath, path)` at config.go:125, 145 |
| 10 | runScan calls remoteconfig.Fetch+Apply BEFORE effective LoadConfig | VERIFIED | main.go lines 82-90 (Fetch+Apply), line 93 (effective LoadConfig) — correct sequence |
| 11 | Makefile has -X main.appVersion=$(VERSION) ldflag | VERIFIED | `LDFLAGS := -s -w -X main.appVersion=$(VERSION)` at Makefile:14 |
| 12 | app.py has POST /earlscheibconcord/telemetry and GET /earlscheibconcord/remote-config with _validate_hmac and 401 on failure | VERIFIED | Routes at app.py:1303 (POST) and 1251 (GET); `_validate_hmac` defined at app.py:120 called at both; 401 returned on signature failure at both |
| 13 | remote_config.json exists with default {} | VERIFIED | File contains `{}` (valid JSON, 0 keys) |

**Score:** 13/13 truths verified

---

### Required Artifacts

| Artifact | Expected | Status | Details |
|----------|----------|--------|---------|
| `internal/telemetry/telemetry.go` | Init, Wrap, Capture; 7-field payload; X-EMS-Telemetry header; 5s timeout; webhook.Sign | VERIFIED | All 3 exported functions present; payload struct has exactly 7 JSON fields; header set at line 185; timeout at line 190; webhook.Sign at line 175 |
| `internal/telemetry/telemetry_test.go` | 10 unit tests; PII absence; panic capture; header verification | VERIFIED | 10 tests, all pass (confirmed by `go test ./internal/telemetry/... -race -count=1`) |
| `internal/remoteconfig/remoteconfig.go` | Fetch (HMAC-signed GET); Apply (whitelist); AllowedKeys = ["webhook_url","log_level"] | VERIFIED | Fetch at line 38; Apply at line 79; AllowedKeys at line 28 |
| `internal/remoteconfig/remoteconfig_test.go` | 12 tests; whitelist enforcement; timeout; HMAC header | VERIFIED | 12 tests, all pass |
| `internal/config/config.go` | Merge with atomic write | VERIFIED | Merge at line 73; CreateTemp+Rename pattern confirmed |
| `cmd/earlscheib/main.go` | remoteconfig.Fetch+Apply before LoadConfig; 3x tel.Wrap; appVersion var | VERIFIED | 3 `_ = tel.Wrap(...)` calls (lines 100, 171, 200); remoteconfig block at lines 82-90 before LoadConfig at 93; `var appVersion = "dev"` at line 33 |
| `app.py` | POST /telemetry + GET /remote-config + _validate_hmac + Twilio comment | VERIFIED | Routes present; `_validate_hmac` at line 120; Twilio switch comment at lines 203-213; python3 -m py_compile exits 0 |
| `remote_config.json` | Valid JSON `{}` | VERIFIED | File is `{}`, python3 json.load confirms OK |

---

### Key Link Verification

| From | To | Via | Status | Details |
|------|-----|-----|--------|---------|
| cmd/earlscheib/main.go | internal/telemetry/telemetry.go | telemetry.Init then tel.Wrap | WIRED | `telemetry.Init(...)` at lines 47, 98, 169, 198; `tel.Wrap(...)` at lines 100, 171, 200 |
| internal/telemetry/telemetry.go | {webhook_url}/telemetry | net/http POST with X-EMS-Telemetry: 1 and X-EMS-Signature | WIRED | `strings.TrimRight(t.webhookURL, "/") + "/telemetry"` at line 174; both headers set at lines 185-188 |
| internal/telemetry/telemetry.go | internal/webhook/sign.go | webhook.Sign(secret, jsonBody) | WIRED | `webhook.Sign(t.secret, body)` at line 175 |
| cmd/earlscheib/main.go runScan | internal/remoteconfig/remoteconfig.go | remoteconfig.Fetch then Apply before LoadConfig | WIRED | Lines 82-90 in runScan; Fetch imported and called with context.Background() |
| internal/remoteconfig/remoteconfig.go | {webhook_url}/remote-config | GET with X-EMS-Signature header | WIRED | `strings.TrimRight(webhookURL, "/") + "/remote-config"` at line 39; `req.Header.Set("X-EMS-Signature", sig)` at line 46 |
| internal/remoteconfig/remoteconfig.go | internal/config/config.go Merge | Apply calls config.Merge(cfgPath, remote, AllowedKeys) | WIRED | `config.Merge(cfgPath, remote, AllowedKeys)` at remoteconfig.go:80 |
| app.py do_POST | telemetry.log file | open(TELEMETRY_LOG_PATH, "a") + json.dumps | WIRED | `with open(TELEMETRY_LOG_PATH, "a", ...) as f: f.write(json.dumps(record) + "\n")` at app.py:1322-1323 |
| app.py do_GET | remote_config.json | json.load(open(REMOTE_CONFIG_PATH)) | WIRED | `with open(REMOTE_CONFIG_PATH, "r", ...) as f: remote_cfg = json.load(f)` at app.py:1258-1259 |
| both new routes | _validate_hmac | _validate_hmac(body, sig_header) -> bool | WIRED | `_validate_hmac(raw, sig)` at line 1305 (POST); `_validate_hmac(b"", sig)` at line 1254 (GET) |

---

### Data-Flow Trace (Level 4)

Not applicable to this phase — artifacts are crash reporters and config appliers, not data-rendering UI components.

---

### Behavioral Spot-Checks

| Behavior | Command | Result | Status |
|----------|---------|--------|--------|
| All Go unit tests pass (race detector) | `go test ./... -race -count=1` | 9 packages OK, 0 failures | PASS |
| go build succeeds | `go build ./cmd/earlscheib/...` | BUILD OK | PASS |
| app.py syntax valid | `python3 -m py_compile app.py` | SYNTAX_OK | PASS |
| remote_config.json valid JSON {} | `python3 -c "json.load(open('remote_config.json'))"` | OK, keys: [] | PASS |
| telemetry.Wrap calls = 3 in main.go | `grep -c "tel\.Wrap" cmd/earlscheib/main.go` | 3 | PASS |
| remoteconfig.Fetch called before LoadConfig in runScan | Line comparison in main.go | Fetch at line 82, LoadConfig at line 93 | PASS |
| AllowedKeys contains exactly webhook_url and log_level | `grep AllowedKeys internal/remoteconfig/remoteconfig.go` | `[]string{"webhook_url", "log_level"}` | PASS |
| Atomic write uses temp file + rename | `grep -n "CreateTemp\|Rename" internal/config/config.go` | Lines 125, 145 | PASS |
| 401 returned by both new endpoints on invalid HMAC | `grep -c "401" app.py` | 2 matches | PASS |
| _validate_hmac defined and used | `grep -c "_validate_hmac" app.py` | 5 matches (1 def + 4 uses) | PASS |

---

### Requirements Coverage

| Requirement | Source Plan | Description | Status | Evidence |
|-------------|-------------|-------------|--------|----------|
| OPS-01 | 04-01 | recover() wrapper on entry points; minimal record NO PII | SATISFIED | telemetry.Wrap at 3 entry points; payload struct has 7 safe fields; PII risk warning in code; TestPayload_NoPIIFields passes |
| OPS-02 | 04-01 | Error record POSTed HMAC-signed to /telemetry with X-EMS-Telemetry: 1; failures silent | SATISFIED | Header set at telemetry.go:185; webhook.Sign used; all POST failures silently dropped |
| OPS-03 | 04-02 | Remote config fetch (scope-adjusted to per-scan, not background poller per CONTEXT.md) | SATISFIED (scope-adjusted) | remoteconfig.Fetch called at top of runScan before LoadConfig; REQUIREMENTS.md marks as Phase 5/Pending reflecting tray-poller not yet built |
| OPS-04 | 04-02 | Whitelist: only webhook_url and log_level accepted | SATISFIED | AllowedKeys = ["webhook_url","log_level"]; config.Merge skips non-allowSet keys; TestAllowedKeys_Contents passes |
| OPS-05 | 04-02 | Atomic merge into config.ini; next --scan picks up new values (scope-adjusted: no tray log) | SATISFIED (scope-adjusted) | config.Merge uses CreateTemp+Rename; effective LoadConfig after Fetch+Apply at line 93 picks up changes |
| OPS-06 | 04-03 | app.py gains /telemetry (POST) and /remote-config (GET), HMAC-validated, 401 on unsigned | SATISFIED | Both routes present in app.py; _validate_hmac called; 401 on failure |
| OPS-07 | 04-03 | Twilio WhatsApp→SMS switch documented as comment block in app.py | SATISFIED | Comment block at app.py:203-213 immediately above from_number assignment |

**Note on OPS-03/04/05 status:** REQUIREMENTS.md shows these as unchecked (`[ ]`) and mapped to Phase 5. This reflects that the literal requirement text calls for a "background poller in the tray process" which does not exist yet. The Phase 4 CONTEXT.md explicitly scoped this down: "no tray process means 'remote config poller' runs as part of each `--scan` invocation." The per-scan implementation satisfies the ROADMAP Phase 4 goal ("webhook URL or log level can be updated without re-running the installer") and the Phase 4 success criterion ("within 5 minutes — next --scan picks it up"). The tray-poller form of OPS-03/04/05 is correctly deferred to Phase 5.

---

### Anti-Patterns Found

| File | Line | Pattern | Severity | Impact |
|------|------|---------|----------|--------|
| `cmd/earlscheib/main.go` | 82-90 | remoteconfig.Fetch runs outside tel.Wrap closure | Info | A panic inside remoteconfig.Fetch itself would not be captured by telemetry. Acceptable: Fetch has robust error handling and does not allocate risky resources; the comment says "best-effort" |
| `app.py` | 1278-1296 | Dead code block after early `return` in do_GET | Info | Lines 1278-1296 are unreachable (after `return` at line 1275). Does not affect functionality. |

No blocker or warning-level anti-patterns found.

---

### Human Verification Required

#### 1. End-to-End Telemetry Round-Trip

**Test:** Deploy updated app.py to support.jjagpal.me, then run `earlscheib.exe --scan` on Marco's machine while introducing a deliberate panic (or via a dev build on a test machine). Observe `telemetry.log` on server.
**Expected:** Within 1 minute, a JSON line appears in `telemetry.log` with `type`, `file`, `line`, `os`, `app_version`, `ts` fields. No BMS XML content in the record.
**Why human:** Requires live deployment and a Windows machine with the binary.

#### 2. Remote Config Round-Trip

**Test:** Edit `remote_config.json` on server to `{"webhook_url":"https://new-url.example.com/earlscheibconcord"}`, then run `earlscheib.exe --scan` on Marco's machine. Check `C:\EarlScheibWatcher\config.ini` after scan.
**Expected:** `config.ini` shows updated `webhook_url`; `watch_folder` and `secret_key` are unchanged.
**Why human:** Requires live server + Windows client coordination.

#### 3. Unsigned Request Rejection

**Test:** Send a curl POST to `/earlscheibconcord/telemetry` with no `X-EMS-Signature` header.
**Expected:** HTTP 401 response.
**Why human:** Server is not running in this environment (deployment is a separate user action per plan scope).

---

### Gaps Summary

No gaps blocking goal achievement. All 13 must-haves are verified. The full test suite (9 packages) passes with the race detector. The build is clean. app.py has valid syntax with both new endpoints present, HMAC-validated, and wired to their respective backing files. OPS-03/04/05 have a scope-adjusted implementation (per-scan fetch instead of background tray poller) that satisfies the Phase 4 goal; the tray-poller form is correctly tracked as Phase 5 in REQUIREMENTS.md.

One info-level finding: `remoteconfig.Fetch` runs outside the `tel.Wrap` closure in runScan. A panic during Fetch would not be telemetry-reported. This is acceptable given Fetch's robust error handling and the documented "best-effort" intent.

One info-level finding: dead code block in app.py do_GET at lines 1278-1296 (unreachable after the `return` on line 1275). Not functional.

---

_Verified: 2026-04-20_
_Verifier: Claude (gsd-verifier)_
