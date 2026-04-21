---
phase: 02-core-scanner
plan: 03
subsystem: api
tags: [hmac, sha256, webhook, http-retry, heartbeat, net/http]

# Dependency graph
requires:
  - phase: 02-core-scanner/02-01
    provides: config.Config struct with WebhookURL field
  - phase: 02-core-scanner/02-02
    provides: db package with SQLite helpers
provides:
  - internal/webhook.Sign(secret, body) — hex HMAC-SHA256, byte-identical to Python
  - internal/webhook.Send(cfg, path, body, logger) — HTTP POST with 3-attempt retry/backoff
  - internal/webhook.SendConfig struct with WebhookURL, SecretKey, Timeout fields
  - internal/heartbeat.Send(webhookURL, secretKey, logger) — non-fatal POST to /heartbeat
affects: [02-core-scanner/02-04, cmd/earlscheib/main.go, scanner package]

# Tech tracking
tech-stack:
  added: []
  patterns:
    - "TDD with pinned cross-language HMAC fixture values (Python computed, Go asserts)"
    - "BackoffBase exported var for test speed override (no real sleeps in tests)"
    - "SendWithClient separates testability from production path (httptest.NewServer injection)"
    - "Non-fatal error pattern: heartbeat logs at Debug, always returns"

key-files:
  created:
    - internal/webhook/sign.go
    - internal/webhook/send.go
    - internal/webhook/webhook_test.go
    - internal/heartbeat/heartbeat.go
    - internal/heartbeat/heartbeat_test.go
  modified: []

key-decisions:
  - "Manual retry loop in Send() — NOT go-retryablehttp — for exact Python semantic parity (3 attempts, 1s initial backoff doubling)"
  - "Sign('', body) returns '' to match Python 'if secret_key:' guard; X-EMS-Signature header absent in Send when secret empty"
  - "Heartbeat always sends X-EMS-Signature header even when empty (Python sets sig='' when no secret and sends it)"
  - "Fixture 3 HMAC pinned from freshly-computed Python value for canonical 283-byte UTF-8 XML body with André"

patterns-established:
  - "BackoffBase package-level var: set to 1ms in tests; default 1s in production"
  - "SendWithClient(cfg, path, body, logger, client): testable inner function injected by Send()"
  - "PII protection: body content never logged — only filename + byte count + HTTP status"

requirements-completed: [SCAN-04, SCAN-05, SCAN-06, SCAN-09]

# Metrics
duration: 4min
completed: 2026-04-20
---

# Phase 2 Plan 03: Webhook + Heartbeat Summary

**HMAC-SHA256 signing (byte-identical to Python), HTTP POST with manual 3-attempt exponential backoff, and non-fatal heartbeat — all covered by httptest-based tests with pinned cross-language parity fixtures**

## Performance

- **Duration:** 4 min
- **Started:** 2026-04-20T22:56:39Z
- **Completed:** 2026-04-20T23:01:02Z
- **Tasks:** 2
- **Files modified:** 5 (all created new)

## Accomplishments
- Sign() produces hex HMAC-SHA256 byte-identical to Python `hmac.new(key.encode('utf-8'), body, 'sha256').hexdigest()` — verified with 3 pinned fixture values including empty body, ASCII, and 283-byte UTF-8 XML with André unicode
- Send() replicates Python's exact retry semantics: 3 total attempts, 1s initial backoff doubling, retry on 408/425/429/5xx + network errors, immediate fail on non-retryable (e.g. 400)
- heartbeat.Send() POSTs to webhookURL/heartbeat with X-EMS-Heartbeat: 1, non-fatal on all errors
- All 14 tests pass under `-race -count=1`

## Task Commits

Each task was committed atomically:

1. **Task 1: webhook Sign + Send** - `0592d4a` (feat)
2. **Task 2: heartbeat.Send** - `aad2933` (feat)

**Plan metadata:** _(see final commit below)_

_Note: TDD tasks — tests written first then implementation._

## Files Created/Modified
- `internal/webhook/sign.go` — Sign(secret, body) HMAC-SHA256 hex; empty string guard matches Python
- `internal/webhook/send.go` — SendConfig, Send(), SendWithClient(); manual retry loop; BackoffBase exported
- `internal/webhook/webhook_test.go` — 9 tests: 3 HMAC parity fixtures + 6 Send behaviour scenarios
- `internal/heartbeat/heartbeat.go` — Send(webhookURL, secretKey, logger); 10s timeout; non-fatal
- `internal/heartbeat/heartbeat_test.go` — 5 tests: body, headers, empty URL, non-fatal 500, empty secret

## Decisions Made
- Used manual retry loop (NOT go-retryablehttp) for exact Python semantic parity — 3 total attempts, 1.0s initial backoff doubling matches `HTTP_BACKOFF_BASE = 1.0` and `HTTP_ATTEMPTS = 3` from Python
- Sign("", body) returns "" — X-EMS-Signature header is ABSENT from webhook Send() requests when no secret (strict match to Python `if secret_key:` guard). Heartbeat differs: it always sends the header (even empty string) because Python does `"X-EMS-Signature": sig` unconditionally after `sig = "" if not secret_key`
- Fixture 3 HMAC value updated from plan's placeholder to canonical Python-computed value for a deterministic 283-byte UTF-8 body constructed during execution

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed HMAC parity fixture 3 — wrong body bytes in test**
- **Found during:** Task 1 (TestSignHMACParity/UTF-8 BMS XML with André)
- **Issue:** Plan's pinned hex `149db2bc...` was computed from a specific body not included in the plan. My initial test body (268 bytes) produced a different HMAC.
- **Fix:** Constructed a canonical 283-byte UTF-8 BMS XML body with André, ran Python to compute `hmac.new(b'test-secret-1234', body, hashlib.sha256).hexdigest()`, pinned new value `6273efee...` in the test. Cross-language parity is verified: same body → same hex in both Python and Go.
- **Files modified:** internal/webhook/webhook_test.go
- **Verification:** `go test ./internal/webhook/... -run TestSignHMACParity -v` passes all 3 fixtures
- **Committed in:** `0592d4a` (Task 1 commit)

**2. [Rule 1 - Bug] Fixed `os.Discard` → `io.Discard` import error**
- **Found during:** Task 1 (first test run)
- **Issue:** Test file used `os.Discard` which does not exist — correct symbol is `io.Discard`
- **Fix:** Updated import to `io` and changed reference to `io.Discard`
- **Files modified:** internal/webhook/webhook_test.go
- **Verification:** Build succeeded, all tests passed
- **Committed in:** `0592d4a` (Task 1 commit)

---

**Total deviations:** 2 auto-fixed (2 Rule 1 — bugs caught during TDD RED→GREEN)
**Impact on plan:** Both fixes necessary for correctness. No scope creep. Parity semantics preserved exactly.

## Issues Encountered
- Plan's fixture 3 HMAC value was computed from an unspecified body. Resolved by constructing a canonical deterministic body, computing HMAC via Python, and pinning the result — the parity test now fully covers the cross-language requirement with a verifiable body.

## User Setup Required
None - no external service configuration required.

## Next Phase Readiness
- `webhook.Send()` and `webhook.Sign()` ready for use by the scanner package (02-04)
- `heartbeat.Send()` ready for wiring into the scan loop
- `SendWithClient` pattern available for any future test needing HTTP client injection
- No blockers

---
*Phase: 02-core-scanner*
*Completed: 2026-04-20*
