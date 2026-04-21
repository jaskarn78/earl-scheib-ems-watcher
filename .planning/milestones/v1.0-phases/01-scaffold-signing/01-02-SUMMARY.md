---
phase: 01-scaffold-signing
plan: 02
subsystem: infra
tags: [github-actions, ci, cross-compile, windows, go, hmac]

# Dependency graph
requires:
  - phase: 01-scaffold-signing/01-01
    provides: Makefile with build-windows target, GSD_HMAC_SECRET ldflags injection, go.mod at 1.22.2

provides:
  - GitHub Actions CI workflow that cross-compiles earlscheib.exe on every push
  - Artifact upload of dist/earlscheib.exe as earlscheib-windows-amd64 (7-day retention)
  - HMAC secret injection from repository secret GSD_HMAC_SECRET

affects:
  - 01-scaffold-signing/01-03
  - 01-scaffold-signing/01-04

# Tech tracking
tech-stack:
  added: [actions/checkout@v4, actions/setup-go@v5, actions/upload-artifact@v4]
  patterns: [linux-cross-compile-to-windows, secret-injection-via-env, makefile-driven-ci]

key-files:
  created:
    - .github/workflows/build.yml
  modified: []

key-decisions:
  - "CGO_ENABLED=0 kept in Makefile (not workflow) — workflow calls make build-windows which already embeds it; no mingw-w64 needed until Phase 3"
  - "go-version-file: go.mod used instead of hardcoded version — stays in sync as go.mod evolves"
  - "if-no-files-found: error on artifact upload — ensures a missing exe fails loudly rather than silently uploading nothing"
  - "Signing step intentionally absent — Plan 04 adds osslsigncode after winres embedding (Plan 03)"

patterns-established:
  - "make-driven-ci: Workflow calls Makefile targets rather than repeating go build flags — single source of truth for build configuration"
  - "secret-as-env-not-arg: GSD_HMAC_SECRET passed as env var to make; Makefile reads it via HMAC_SECRET ?= $(GSD_HMAC_SECRET)"

requirements-completed:
  - SCAF-02

# Metrics
duration: 5min
completed: 2026-04-20
---

# Phase 01 Plan 02: GitHub Actions Cross-Compile CI Summary

**GitHub Actions workflow on ubuntu-latest cross-compiles earlscheib.exe for windows/amd64, injects GSD_HMAC_SECRET from repository secrets, and uploads the artifact on every push and pull_request**

## Performance

- **Duration:** ~5 min
- **Started:** 2026-04-20T22:22:30Z
- **Completed:** 2026-04-20T22:23:40Z
- **Tasks:** 1
- **Files modified:** 1

## Accomplishments

- `.github/workflows/build.yml` created — triggers on `push` to any branch and all `pull_request` events
- Cross-compiles `windows/amd64` on `ubuntu-latest` via `make build-windows` (CGO_ENABLED=0 embedded in Makefile)
- Injects `GSD_HMAC_SECRET` from repository secrets; build succeeds using dev-default when secret is absent (forks, local dev)
- Uploads `dist/earlscheib.exe` as artifact `earlscheib-windows-amd64` with 7-day retention and `if-no-files-found: error`

## Task Commits

Each task was committed atomically:

1. **Task 1: GitHub Actions cross-compile workflow** - `16798a2` (feat)

**Plan metadata:** (committed with SUMMARY/STATE/ROADMAP update)

## Files Created/Modified

- `.github/workflows/build.yml` — CI workflow: checkout → setup-go (go-version-file: go.mod) → go vet → make build-windows → upload artifact

## Decisions Made

- Used `go-version-file: go.mod` instead of hardcoding `go-version: "1.22.2"` so the workflow stays in sync automatically as go.mod evolves.
- `if-no-files-found: error` on artifact upload ensures a missing exe (failed build that somehow exits 0) fails loudly.
- No `apt-get install gcc-mingw-w64-x86-64` step — not needed for Phase 1 pure-Go build; Plan 03 adds it when CGO is introduced for systray+webview2.

## Deviations from Plan

None - plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

One manual step before CI can inject the production HMAC secret:

1. Go to the repository **Settings → Secrets and variables → Actions**
2. Click **New repository secret**
3. Name: `GSD_HMAC_SECRET`
4. Value: the same HMAC key used by the production webhook server
5. Save

Without this secret set, CI will build successfully using the dev-default key baked into source — the artifact will work for smoke testing but will not pass the production webhook's HMAC verification.

## Next Phase Readiness

- CI pipeline is live — every push now produces a downloadable `earlscheib.exe`
- Plan 03 (winres — Windows version info + icon embedding) extends this same workflow file
- Plan 04 (osslsigncode signing) extends it further once the OV cert is provisioned

---
*Phase: 01-scaffold-signing*
*Completed: 2026-04-20*
