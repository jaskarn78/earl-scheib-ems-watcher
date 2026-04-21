---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: Ready to execute
stopped_at: Completed 05-queue-admin-ui 05-02-PLAN.md
last_updated: "2026-04-21T06:51:47.365Z"
progress:
  total_phases: 5
  completed_phases: 4
  total_plans: 19
  completed_plans: 18
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-04-20)

**Core value:** Marco downloads one file, clicks through a 3-step wizard, and the tray icon turns green — forever after, follow-up texts and review requests go out automatically with zero ongoing attention.
**Current focus:** Phase 05 — queue-admin-ui

## Current Position

Phase: 05 (queue-admin-ui) — EXECUTING
Plan: 4 of 4

## Performance Metrics

**Velocity:**

- Total plans completed: 0
- Average duration: -
- Total execution time: 0 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| - | - | - | - |

**Recent Trend:**

- Last 5 plans: -
- Trend: -

*Updated after each plan completion*
| Phase 01-scaffold-signing P01 | 2 | 2 tasks | 5 files |
| Phase 01-scaffold-signing P02 | 5 | 1 tasks | 1 files |
| Phase 01-scaffold-signing P03 | 15 | 2 tasks | 4 files |
| Phase 01-scaffold-signing P04 | 5 | 2 tasks | 3 files |
| Phase 02-core-scanner P02 | 3 | 1 task | 4 files |
| Phase 02-core-scanner P02-01 | 4 | 2 tasks | 6 files |
| Phase 02-core-scanner P04 | 12 | 2 tasks | 3 files |
| Phase 02-core-scanner P03 | 4 | 2 tasks | 5 files |
| Phase 02-core-scanner P05 | 245 | 2 tasks | 6 files |
| Phase 03-installer-native-config P03 | 2 | 2 tasks | 3 files |
| Phase 03-installer-native-config P01 | 2 | 2 tasks | 4 files |
| Phase 03-installer-native-config P02 | 1 | 2 tasks | 2 files |
| Phase 04-telemetry-remote-config P02 | 3 | 2 tasks | 4 files |
| Phase 04-telemetry-remote-config P01 | 5 | 2 tasks | 4 files |
| Phase 04-telemetry-remote-config P03 | 15 | 2 tasks | 3 files |
| Phase 05-queue-admin-ui P01 | 3 | 3 tasks | 5 files |
| Phase 05-queue-admin-ui P03 | 3 | 3 tasks | 3 files |
| Phase 05-queue-admin-ui P02 | 249 | 3 tasks | 8 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- Phase 1: OV cert procurement must begin at kickoff — 2–10 business day lead time; use cloud HSM (DigiCert KeyLocker or SSL.com eSigner) for non-interactive CI signing
- Phase 3: Use raw jchv/go-webview2 (not Wails) — Bind() + Dispatch(Eval()) threading model; review example code before coding
- Phase 4: WebView2 bootstrapper strategy (Evergreen offline bundle ~120 MB vs Fixed Version Runtime) — decide during Phase 4 plan via POC size measurement
- [Phase 01-scaffold-signing]: ifneq(strip) guard in Makefile prevents empty-string ldflags override when GSD_HMAC_SECRET is unset
- [Phase 01-scaffold-signing]: CGO_ENABLED=0 for Phase 1 stubs; CGO introduced in Phase 3 for systray+webview2
- [Phase 01-scaffold-signing]: No third-party deps in Phase 1 — only stdlib fmt and os in dispatcher
- [Phase 01-scaffold-signing]: CGO_ENABLED=0 kept in Makefile not workflow; no mingw-w64 needed until Phase 3 (systray+webview2)
- [Phase 01-scaffold-signing]: go-version-file: go.mod used in CI setup-go step to stay in sync as go.mod evolves
- [Phase 01-scaffold-signing]: go-winres .syso output to cwd; Makefile mv step moves to cmd/earlscheib/ for Go auto-linking (Go only links .syso from the compiled package directory)
- [Phase 01-scaffold-signing]: go-winres v0.3.3 schema uses #1 resource keys and 0409 LCID (not 0000/1) — plan template was incorrect; corrected from go-winres init output
- [Phase 01-scaffold-signing]: osslsigncode on ubuntu-latest (not signtool.exe on windows-latest) — Authenticode from Linux; CI signing conditional on SIGNING_CERT_B64 secret; RFC 3161 timestamp for post-expiry validity; dev-sign uses openssl ephemeral self-signed cert + /tmp for temp files
- [Phase 02-core-scanner]: Custom emsHandler implements slog.Handler directly for exact Python log format match (UTC YYYY-MM-DD HH:MM:SS [LEVEL] message)
- [Phase 02-core-scanner]: LoadConfig returns defaults on missing/malformed INI without error — matches Python fall-through behaviour
- [Phase 02-core-scanner]: SecretKey absent from Config struct — baked into binary via ldflags per SCAF-04; EARLSCHEIB_DATA_DIR env var enables cross-platform dev testing
- [Phase 02-core-scanner 02-02]: go.mod upgraded 1.22.2 → 1.25.0 automatically (modernc.org/sqlite v1.49.1 requires go >= 1.25.0); transparent toolchain upgrade
- [Phase 02-core-scanner 02-02]: RetryBaseDelay exported package var for test-speed control — 1ns in tests vs 500ms in prod; no interface injection needed
- [Phase 02-core-scanner 02-02]: db functions accept *sql.DB not a wrapper struct — minimal API surface, avoids over-abstraction
- [Phase 02-core-scanner]: Sender func injected in RunConfig; scanner never imports internal/webhook — clean boundary for unit testing
- [Phase 02-core-scanner]: SettleCheck log param is func(string, ...any) not *slog.Logger — allows t.Logf injection in tests without wrapping
- [Phase 02-core-scanner]: Manual retry loop in webhook.Send() — NOT go-retryablehttp — for exact Python semantic parity (3 attempts, 1s backoff doubling)
- [Phase 02-core-scanner]: BackoffBase exported package var in webhook + RetryBaseDelay in db: test-speed override pattern established
- [Phase 02-core-scanner]: Heartbeat sends X-EMS-Signature even when empty (matches Python); webhook Send omits header entirely when secret empty (matches Python 'if secret_key:' guard)
- [Phase 02-core-scanner]: Makefile test target omits CGO_ENABLED=0 for -race (race detector requires CGO; CGO_ENABLED=0 stays for cross-compile build targets only)
- [Phase 02-core-scanner]: runStatus passes nil sqlDB when db.Open fails so status.Print shows 'No database yet' correctly
- [Phase 03-installer-native-config]: installer-syntax-check CI job has no needs: dependency -- runs in parallel with test and build-windows for fast feedback without waiting for binary build
- [Phase 03-installer-native-config]: iscc /Dq /O- flags used for parse-only validation: /Dq suppresses banner, /O- suppresses output file -- together validate .iss syntax without producing a binary
- [Phase 03-installer-native-config]: Placeholder binary (touch dist/earlscheib-artifact.exe) satisfies [Files] Source: path existence check at parse time -- no real Go build needed for CI syntax check
- [Phase 03-installer-native-config]: Task XMLs use UserId=S-1-5-18 (SYSTEM SID) to avoid locale-specific name resolution; User fallback uses LogonType=InteractiveToken
- [Phase 03-installer-native-config]: Connection test uses EARLSCHEIB_DATA_DIR env var override to point earlscheib.exe --test at {tmp} during install
- [Phase 03-installer-native-config 03-02]: CURDIR (not PWD) in Makefile installer target — GNU make built-in handles recursive make calls correctly
- [Phase 03-installer-native-config 03-02]: build-installer CI job installs osslsigncode independently on each ephemeral runner; signing step conditional on SIGNING_CERT_B64
- [Phase 03-installer-native-config 03-02]: Installer signing overwrites in-place so upload-artifact step is unconditional regardless of signing
- [Phase 04-telemetry-remote-config 04-02]: HMAC-sign empty body []byte("") for GET remote-config requests — byte-identical to Python reference
- [Phase 04-telemetry-remote-config 04-02]: AllowedKeys = [webhook_url, log_level] only — secret_key and watch_folder excluded per OPS-04 (safety boundary)
- [Phase 04-telemetry-remote-config 04-02]: config.Merge uses temp-file + os.Rename for atomic INI write — crash-safe
- [Phase 04-telemetry-remote-config 04-02]: remoteconfig.Fetch+Apply added directly to runScan (04-01 not yet applied); will be enclosed in telemetry.Wrap automatically when 04-01 lands
- [Phase 04-telemetry-remote-config 04-01]: Wrap re-panics after Capture — telemetry capture does not swallow panics; process exits non-zero
- [Phase 04-telemetry-remote-config 04-01]: Message truncated to 200 chars max to cap accidental PII exposure per OPS-01
- [Phase 04-telemetry-remote-config 04-01]: tel re-init inside each command after logger is available gives crash-in-Wrap a real logger
- [Phase 04-telemetry-remote-config 04-01]: appVersion injected via ldflags; Makefile VERSION default changed to 0.1.0-dev
- [Phase 04-telemetry-remote-config 04-03]: _validate_hmac(body, sig_header) helper uses hmac.compare_digest for constant-time comparison — prevents timing attacks
- [Phase 04-telemetry-remote-config 04-03]: GET /remote-config validates HMAC of empty body b"" — matches Go client's webhook.Sign(secret, []byte(""))
- [Phase 04-telemetry-remote-config 04-03]: 204 No Content when remote_config.json is {} — client skips merge; avoids unnecessary file writes
- [Phase 04-telemetry-remote-config 04-03]: telemetry.log is JSONL append-only; rotation deferred as tech debt
- [Phase 05-queue-admin-ui]: GET /queue response is bare JSON array, not wrapped object — CONTEXT.md canonical spec
- [Phase 05-queue-admin-ui]: DELETE /queue response {"deleted": 1} integer count; 404 collapses missing-row and already-sent into same error message
- [Phase 05-queue-admin-ui]: do_DELETE on WebhookHandler dispatched automatically by Python http.server — no route registration needed
- [Phase 05-queue-admin-ui]: feTurbulence paper-grain SVG embedded in CSS data URI for single-HTTP-trip favicon + grain; cancel-with-undo fires DELETE only after 5s timer expires
- [Phase 05-queue-admin-ui]: URLCh chan<- string in admin.Config is the sole test-startup mechanism — no stdout capture or port scanning
- [Phase 05-queue-admin-ui]: admin proxy re-marshals incoming JSON to canonical compact form before HMAC signing (no whitespace; stable field order)
- [Phase 05-queue-admin-ui]: signal.NotifyContext wraps parent ctx with SIGINT/SIGTERM for clean integration with the heartbeat watchdog context

### Roadmap Evolution

- 2026-04-21: Phase 5 added (Queue Admin UI) — post-v1.0 audit extension. Client-side launcher (`earlscheib.exe --admin`) opens local-browser SPA backed by a new server-side `/queue` endpoint on app.py. Run `/gsd:ui-phase 5` before `/gsd:plan-phase 5`.

### Pending Todos

None yet.

### Blockers/Concerns

- SCAF-06 (OV cert provisioned into CI HSM) must be complete before Phase 4 ships; Phase 4 plan must gate on cert readiness
- Phase 3 research flag: jchv/go-webview2 Dispatch() threading for background-goroutine → UI updates is error-prone; review examples before coding
- Phase 5 research flag: /remote-config JSON schema + server-side storage not yet specified; design during Phase 5 plan

## Session Continuity

Last session: 2026-04-21T06:51:47.362Z
Stopped at: Completed 05-queue-admin-ui 05-02-PLAN.md
Resume file: None
