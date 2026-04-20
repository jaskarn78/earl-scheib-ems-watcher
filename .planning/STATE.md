---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: In Progress
stopped_at: Completed 02-core-scanner 02-01-PLAN.md
last_updated: "2026-04-20T22:55:07.380Z"
progress:
  total_phases: 5
  completed_phases: 1
  total_plans: 9
  completed_plans: 6
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-04-20)

**Core value:** Marco downloads one file, clicks through a 3-step wizard, and the tray icon turns green — forever after, follow-up texts and review requests go out automatically with zero ongoing attention.
**Current focus:** Phase 02 — Core Scanner

## Current Position

Phase: 2
Plan: 2 (completed)

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

### Pending Todos

None yet.

### Blockers/Concerns

- SCAF-06 (OV cert provisioned into CI HSM) must be complete before Phase 4 ships; Phase 4 plan must gate on cert readiness
- Phase 3 research flag: jchv/go-webview2 Dispatch() threading for background-goroutine → UI updates is error-prone; review examples before coding
- Phase 5 research flag: /remote-config JSON schema + server-side storage not yet specified; design during Phase 5 plan

## Session Continuity

Last session: 2026-04-20T22:55:30Z
Stopped at: Completed 02-core-scanner 02-02-PLAN.md
Resume file: None
