---
phase: 03-installer-native-config
plan: "02"
subsystem: build-and-ci
tags: [makefile, github-actions, inno-setup, docker, code-signing, installer]
dependency_graph:
  requires: ["03-01"]
  provides: ["installer-build-pipeline", "build-installer-ci-job"]
  affects: ["Makefile", ".github/workflows/build.yml"]
tech_stack:
  added: []
  patterns: ["docker-based-iscc", "osslsigncode-conditional-signing", "artifact-chaining"]
key_files:
  created: []
  modified:
    - Makefile
    - .github/workflows/build.yml
decisions:
  - "Used CURDIR (not PWD) in Makefile installer target for portability with recursive make calls"
  - "installer-syntax target uses /Dq /O- flags (define quiet + suppress output dir) to prevent artifact creation during parse check"
  - "build-installer job installs osslsigncode from apt — same package as build-windows, ensures identical signing environment"
  - "Signing step is conditional on SIGNING_CERT_B64 != '' — CI passes without cert secrets configured"
metrics:
  duration_seconds: 85
  completed_date: "2026-04-20T23:37:44Z"
  tasks_completed: 2
  tasks_total: 2
  files_changed: 2
---

# Phase 3 Plan 02: Makefile Installer Targets + CI Build-Installer Job Summary

**One-liner:** Docker-based iscc build pipeline wired into `make installer` and a `build-installer` CI job that chains from the signed binary artifact, conditionally Authenticode-signs the installer, and uploads it as `earlscheib-installer`.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Add Makefile installer targets | 5031750 | Makefile |
| 2 | Add build-installer CI job | 187e618 | .github/workflows/build.yml |

## What Was Built

### Task 1: Makefile installer targets

Two new targets added to `Makefile`:

- **`installer`**: Runs `docker run --rm -v "$(CURDIR):/work" amake/innosetup:6.7.1 iscc /work/installer/earlscheib.iss` — local developer workflow to build `installer/Output/EarlScheibWatcher-Setup.exe`. Requires `dist/earlscheib-artifact.exe` to exist first.

- **`installer-syntax`**: Parse-only check using `iscc /Dq /O- ...` — validates the `.iss` script without producing output, suitable for CI fast-fail without artifact storage concerns.

`.PHONY` line extended to include both new targets.

### Task 2: CI build-installer job

New `build-installer` job in `.github/workflows/build.yml`:

1. `needs: [build-windows]` — waits for binary build and signing
2. Downloads `earlscheib-windows-amd64` artifact into `dist/` (the signed binary named `earlscheib-artifact.exe`)
3. Runs `amake/innosetup:6.7.1` Docker image via `iscc /work/installer/earlscheib.iss`
4. Verifies `installer/Output/EarlScheibWatcher-Setup.exe` exists post-build
5. Installs `osslsigncode` via `apt-get`
6. Conditionally signs the installer (only if `SIGNING_CERT_B64` secret is present), verifies the signature, replaces unsigned with signed in-place
7. Uploads `installer/Output/EarlScheibWatcher-Setup.exe` as artifact `earlscheib-installer` (retention: 7 days, fails if file not found)

All existing jobs (`installer-syntax-check`, `test`, `build-windows`) are unchanged.

## Decisions Made

- **CURDIR vs PWD in Makefile**: Used `$(CURDIR)` instead of `$(PWD)` — `CURDIR` is a GNU make built-in that handles recursive make calls correctly; `$(PWD)` would need shell expansion and can behave unexpectedly in sub-makes.

- **`/Dq /O-` for installer-syntax target**: `/Dq` defines a preprocessor symbol `q` (acts as quiet mode guard), `/O-` suppresses output directory creation. Together they enable parse validation without side effects.

- **apt-get install osslsigncode in build-installer**: Both `build-windows` and `build-installer` jobs install osslsigncode independently. This is intentional — jobs run on separate ephemeral runners; no shared state between jobs beyond artifacts.

- **Signing overwrites in-place**: The signed installer replaces the unsigned one at the same path (`mv ... EarlScheibWatcher-Setup.exe`), so the upload-artifact step is unconditional — whether signed or unsigned, the same path is uploaded.

## Deviations from Plan

None — plan executed exactly as written.

## Known Stubs

None — no placeholder data or TODO stubs in the modified files.

## Self-Check: PASSED

- Makefile exists and contains installer targets: confirmed (7 lines contain "installer", 2 contain "amake/innosetup:6.7.1")
- .github/workflows/build.yml contains build-installer job: confirmed
- Existing jobs (test, build-windows, installer-syntax-check) unchanged: confirmed
- Commits 5031750 and 187e618 exist: confirmed via git log
