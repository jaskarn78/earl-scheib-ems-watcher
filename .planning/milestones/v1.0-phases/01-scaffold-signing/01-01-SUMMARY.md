---
phase: 01-scaffold-signing
plan: 01
subsystem: infra
tags: [go, cross-compile, windows, makefile, ldflags, hmac]

# Dependency graph
requires: []
provides:
  - Go module declaration at github.com/jjagpal/earl-scheib-watcher (go 1.22)
  - cmd/earlscheib/main.go — single entry point dispatching all 6 subcommands
  - Makefile with build-windows (CGO_ENABLED=0 cross-compile) and build-linux targets
  - GSD_HMAC_SECRET env var → -X main.secretKey ldflags injection pattern
  - dist/ output directory; .gitignore excluding binaries and signing artifacts
affects: [02-scaffold-signing, 03-scaffold-signing, 04-scaffold-signing, all future phases]

# Tech tracking
tech-stack:
  added: [go 1.22.2, stdlib only (fmt, os)]
  patterns: [subcommand dispatch via os.Args switch, ldflags var injection for secrets]

key-files:
  created:
    - go.mod
    - cmd/earlscheib/main.go
    - Makefile
    - dist/.gitkeep
    - .gitignore
  modified: []

key-decisions:
  - "ifneq(strip) guard in Makefile prevents empty-string ldflags override when GSD_HMAC_SECRET is unset — ensures dev-default in source is preserved"
  - "CGO_ENABLED=0 for Phase 1 (no systray/webview2); CGO introduced in Phase 3"
  - "No third-party dependencies in Phase 1 — dispatcher uses only fmt and os"

patterns-established:
  - "Subcommand dispatch: os.Args[1] switch with runStub() for each flag"
  - "Secret injection: var secretKey = dev-default; override via -ldflags -X main.secretKey=$(GSD_HMAC_SECRET)"
  - "Cross-compile: CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build from Linux"

requirements-completed: [SCAF-01, SCAF-04]

# Metrics
duration: 2min
completed: 2026-04-20
---

# Phase 1 Plan 01: Scaffold Go Module and Stub Dispatcher Summary

**Go module github.com/jjagpal/earl-scheib-watcher with 6-subcommand stub dispatcher cross-compiled to windows/amd64 via CGO_ENABLED=0 Makefile with GSD_HMAC_SECRET ldflags injection**

## Performance

- **Duration:** 2 min
- **Started:** 2026-04-20T22:19:14Z
- **Completed:** 2026-04-20T22:21:12Z
- **Tasks:** 2
- **Files modified:** 5

## Accomplishments
- Initialized Go module at `github.com/jjagpal/earl-scheib-watcher` (go 1.22.2, no third-party deps)
- Single-binary subcommand dispatcher in `cmd/earlscheib/main.go` — all 6 subcommands (--tray, --scan, --wizard, --test, --status, --install) print "not yet implemented" and exit 0
- `secretKey` var declared at package scope for `-ldflags -X main.secretKey=` injection; dev-only default set in source
- Makefile `build-windows` produces `dist/earlscheib.exe` (1.3 MB, PE32+ windows/amd64); `build-linux` produces `dist/earlscheib` (1.2 MB)
- `GSD_HMAC_SECRET` env var injected via ldflags; `ifneq(strip)` guard ensures unset env falls back to in-source dev default (not empty string)
- `.gitignore` excludes `dist/*.exe`, `dist/earlscheib`, `signing.pfx`, `*.pfx`

## Task Commits

Each task was committed atomically:

1. **Task 1: Initialize Go module and stub dispatcher** - `8c7d9db` (feat)
2. **Task 2: Makefile with cross-compile and ldflags injection** - `eadb8b1` (feat)

**Plan metadata:** (see final commit below)

## Files Created/Modified
- `go.mod` — module github.com/jjagpal/earl-scheib-watcher, go 1.22.2
- `cmd/earlscheib/main.go` — 6-subcommand dispatcher with secretKey ldflags var
- `Makefile` — build-windows, build-linux, clean, help targets; GSD_HMAC_SECRET injection
- `dist/.gitkeep` — establishes output directory in git
- `.gitignore` — excludes dist binaries and PFX signing artifacts

## Decisions Made

- **ifneq vs ifdef for HMAC_SECRET guard:** `ifdef HMAC_SECRET` would fire even when the variable is defined as empty string (which happens when `GSD_HMAC_SECRET` is unset and `?=` expands to empty). Using `ifneq ($(strip $(HMAC_SECRET)),)` correctly treats empty as "not provided" so the dev default in `main.go` is preserved.
- **CGO_ENABLED=0 in Phase 1:** No systray or webview2 in this phase — pure CLI stubs. CGO will be introduced in Phase 3 with mingw-w64.
- **No third-party deps:** Phase 1 intentionally uses only stdlib (`fmt`, `os`). External deps (systray, sqlite, ini, retryablehttp) are added in later phases as features are wired.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed empty-string ldflags override when GSD_HMAC_SECRET is unset**
- **Found during:** Task 2 (Makefile creation)
- **Issue:** Plan-specified `ifdef HMAC_SECRET` guard fires even when `HMAC_SECRET` is empty string (Make treats defined-empty as truthy for `ifdef`), causing `-X main.secretKey=` to override the in-source dev default with an empty string
- **Fix:** Replaced `ifdef HMAC_SECRET` with `ifneq ($(strip $(HMAC_SECRET)),)` to only inject when value is non-empty
- **Files modified:** Makefile
- **Verification:** `make build-windows --dry-run` shows no `-X` flag when unset; `GSD_HMAC_SECRET=test123 make build-windows --dry-run` shows `-X main.secretKey=test123`
- **Committed in:** eadb8b1 (Task 2 commit)

---

**Total deviations:** 1 auto-fixed (Rule 1 bug in Makefile guard logic)
**Impact on plan:** Fix essential for correctness — without it, unset `GSD_HMAC_SECRET` would produce a binary with an empty secretKey, silently breaking HMAC signing in all dev builds. No scope creep.

## Issues Encountered
- Go was not installed on the system; installed via `sudo apt-get install golang-go` (Go 1.22.2 from Ubuntu 24.04 repos) before executing the plan.

## Known Stubs
- All 6 subcommands (`--tray`, `--scan`, `--wizard`, `--test`, `--status`, `--install`) are intentional stubs printing "not yet implemented" — this is the entire deliverable for Phase 1 Plan 1. Feature implementation begins in Phase 1 Plans 02–04 and beyond.

## User Setup Required
- Set `GSD_HMAC_SECRET` env var before running `make build-windows` in CI. Local dev builds use the in-source dev default. See Makefile header comment for CI usage with GitHub Actions.

## Next Phase Readiness
- Module path, subcommand dispatch skeleton, and build tooling are fully established
- Phase 1 Plans 02–04 can add packages under `internal/` and import them from `cmd/earlscheib/main.go`
- Phase 3 will introduce CGO_ENABLED=1 + mingw-w64 for systray; Makefile `build-windows` target will need `CC=x86_64-w64-mingw32-gcc` added at that point

---
*Phase: 01-scaffold-signing*
*Completed: 2026-04-20*
