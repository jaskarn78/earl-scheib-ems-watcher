---
phase: 04-telemetry-remote-config
plan: 01
subsystem: telemetry
tags: [telemetry, panic-capture, hmac, observability, crash-reporting]
dependency_graph:
  requires: []
  provides: [internal/telemetry, appVersion-ldflag, main.go-telemetry-wrapping]
  affects: [cmd/earlscheib/main.go, Makefile]
tech_stack:
  added: [internal/telemetry package]
  patterns: [panic-recover-and-repanic, fire-and-forget-http, hmac-signing-reuse]
key_files:
  created:
    - internal/telemetry/telemetry.go
    - internal/telemetry/telemetry_test.go
  modified:
    - cmd/earlscheib/main.go
    - Makefile
decisions:
  - "Wrap re-panics after Capture so process exits non-zero — telemetry capture does not swallow panics"
  - "Message truncated to 200 chars max to cap accidental PII exposure per OPS-01"
  - "Telemetry POST failures are completely silent (debug log only) — broken endpoint must never break scan"
  - "appVersion injected via ldflags; Makefile default changed from 'dev' to '0.1.0-dev' for clarity"
  - "tel re-init inside each command (after logger is available) gives crash-in-Wrap a real logger"
  - "Additive integration with 04-02 remoteconfig: both changes in main.go are orthogonal"
metrics:
  duration: "5 minutes"
  completed: "2026-04-20T23:54:53Z"
  tasks: 2
  files: 4
requirements: [OPS-01, OPS-02]
---

# Phase 4 Plan 01: Telemetry Package Summary

One-liner: HMAC-signed fire-and-forget panic/error capture POSTing minimal structured payloads (type, message, file:line, os, app_version, ts) to {webhookURL}/telemetry, wired into all three main.go entry points via tel.Wrap.

## Telemetry Package API

### Exported API (`internal/telemetry/telemetry.go`)

```go
// Init creates a Telemetry reporter. Lightweight — no network calls.
func Init(webhookURL, secret, appVersion string, logger *slog.Logger) *Telemetry

// Wrap executes fn in a deferred recover; captures panic then re-panics.
// Non-nil error returns are also reported. POST failure is silent.
func (t *Telemetry) Wrap(fn func() error) (retErr error)

// Capture serialises and POSTs a panic record.
// r is recover() value; pcs is from runtime.Callers (captured in defer).
func (t *Telemetry) Capture(r any, pcs []uintptr)
```

### Payload JSON Shape

Exactly these fields, nothing else (OPS-01/OPS-02 compliance):

```json
{
  "type":        "panic" | "error",
  "message":     "<error string, truncated to 200 chars max>",
  "file":        "internal/scanner/scan.go",
  "line":        123,
  "os":          "windows",
  "app_version": "0.1.0",
  "ts":          "2026-04-20T12:34:56Z"
}
```

### HTTP Details

- POST to `{webhookURL}/telemetry` (trailing slashes stripped)
- `X-EMS-Telemetry: 1` header always present
- `X-EMS-Signature: <hex HMAC-SHA256>` when secret is non-empty (reuses `internal/webhook.Sign`)
- `Content-Type: application/json`
- 5-second HTTP timeout
- Any failure (marshal error, network error, non-2xx) logged at debug level and silently dropped

## How main.go Was Modified

### Pattern

```go
// main() — before routing:
dataDir := config.DataDir()
cfg, _ := config.LoadConfig(filepath.Join(dataDir, "config.ini"))
tel := telemetry.Init(cfg.WebhookURL, secretKey, appVersion, nil) // nil logger initially

// Each command function:
func runScan(tel *telemetry.Telemetry) {
    // ... load config, set up logger ...
    tel = telemetry.Init(cfg.WebhookURL, secretKey, appVersion, logger) // re-init with real logger
    _ = tel.Wrap(func() error {
        // ... entire command body ...
        return nil
    })
}
```

### Variables Added

- `var appVersion = "dev"` — injected via `-X main.appVersion=$(VERSION)` at build time

### Functions Changed

| Function | Before | After |
|----------|--------|-------|
| `runScan` | `runScan()` | `runScan(tel *telemetry.Telemetry)` |
| `runTest` | `runTest()` | `runTest(tel *telemetry.Telemetry)` |
| `runStatus` | `runStatus()` | `runStatus(tel *telemetry.Telemetry)` |

All three functions wrap their entire body in `tel.Wrap(func() error { ... })`.

### Makefile

Changed `VERSION ?= dev` to `VERSION ?= 0.1.0-dev` and added `-X main.appVersion=$(VERSION)` to the base LDFLAGS (always injected, not gated on env var like secretKey).

## Integration with 04-02 (remoteconfig)

04-02 modified main.go before Task 2 ran, adding remoteconfig.Fetch/Apply inside runScan (before loading effective config). This plan's changes are additive:
- The remoteconfig block remains before the logger setup
- telemetry.Wrap wraps the DB/scanner work that occurs after config is loaded
- Both changes coexist without conflict

## Deviations from Plan

### Deviation 1: Peer agent 04-02 pre-modified main.go

**Found during:** Task 2 (write attempt)
**Type:** Parallel execution merge scenario (documented in plan)
**Issue:** 04-02 committed changes to main.go (adding context, remoteconfig import, and Fetch/Apply block) before this plan's Task 2 ran.
**Resolution:** Read fresh state of main.go, merged our telemetry additions additively, preserved all 04-02 changes.
**Result:** Both feature sets present in final main.go with no conflicts.

### Auto-fixed Issues

None — plan executed with one merge deviation handled per plan instructions.

## Test Coverage

10 unit tests in `internal/telemetry/telemetry_test.go`:

| Test | Verifies |
|------|---------|
| `TestWrap_PanicCapture` | Panic triggers POST with type="panic"; no PII field names in payload |
| `TestWrap_NoPostOnSuccess` | No POST when fn returns nil |
| `TestWrap_ErrorCapture` | Non-nil error triggers POST with type="error" |
| `TestPayload_NoPIIFields` | Payload has exactly the whitelisted fields; no xml/watch_folder/secret |
| `TestWrap_SilentOnHTTPFailure` | 500 response does not cause Wrap to error or panic |
| `TestHeaders_TelemetryAndSignature` | X-EMS-Telemetry: 1 always present; X-EMS-Signature matches webhook.Sign |
| `TestWrap_FileFieldPopulated` | file field is non-empty (stack capture working) |
| `TestMessageTruncation` | 300-char panic message truncated to ≤204 chars |
| `TestWrap_NoBMSXMLInMessage` | BMS XML panic value is length-capped; full XML not propagated |
| `TestNoRaceOnConcurrentWraps` | Race detector passes for concurrent Wrap calls |

## Known Stubs

None — all fields are fully populated from runtime data at crash time.

## Self-Check: PASSED

- `internal/telemetry/telemetry.go` exists: FOUND
- `internal/telemetry/telemetry_test.go` exists: FOUND
- Commit `cdebe7c` (telemetry package): FOUND
- Commit `026d241` (main.go + Makefile): FOUND
- `go test ./internal/telemetry/... -race -count=1`: PASSED (10/10)
- `go build ./cmd/earlscheib/...`: BUILD OK
- `telemetry.Wrap` count in main.go: 3
- `X-EMS-Telemetry: 1` in telemetry.go: FOUND
- `PII risk` warning in telemetry.go: FOUND
