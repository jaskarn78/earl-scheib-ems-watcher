# Roadmap: Earl Scheib EMS Watcher — Windows Client

## Overview

Five phases deliver Marco's one-download, three-click, green-icon experience. Phase 1 establishes the cross-compile pipeline and kicks off OV cert procurement (2–10 day lead time) so code-signing is never the bottleneck at ship time. Phase 2 ports all Python watcher logic as pure-Go with no CGO, so the scanner is fully unit-testable in CI without a Windows VM. Phase 3 introduces CGO (systray + WebView2), validates the cross-compile path, and delivers the tray shell with a functional status window. Phase 4 combines the first-run wizard with the Inno Setup installer — the complete Marco-ready artifact. Phase 5 adds crash telemetry and remote config so broken installs are visible and webhook URLs can be rotated without Marco re-running the installer.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3): Planned milestone work
- Decimal phases (2.1, 2.2): Urgent insertions (marked with INSERTED)

Decimal phases appear between their surrounding integers in numeric order.

- [ ] **Phase 1: Scaffold + Signing** - Go module, CI cross-compile pipeline, OV cert procurement, and Authenticode signing infrastructure
- [ ] **Phase 2: Core Scanner** - Pure-Go CGO-free port of all Python watcher logic (dedup, settle, HMAC, retry, heartbeat, logging)
- [ ] **Phase 3: Tray Shell + Status Window** - CGO tray with three-state icon and WebView2 status window; validates cross-compile before installer
- [ ] **Phase 4: Wizard + Installer** - First-run 3-step wizard and Inno Setup single-exe installer; first Marco-ready artifact
- [ ] **Phase 5: Telemetry + Remote Config** - Crash telemetry, remote config poller, and coordinated server-side endpoints

## Phase Details

### Phase 1: Scaffold + Signing
**Goal**: A signed, runnable Windows exe ships from Linux CI on every commit — establishing the full toolchain before any feature code is written
**Depends on**: Nothing (first phase)
**Requirements**: SCAF-01, SCAF-02, SCAF-03, SCAF-04, SCAF-05, SCAF-06
**Success Criteria** (what must be TRUE):
  1. `go build` in CI produces a `windows/amd64` exe without a Windows runner; the binary is runnable on a Windows 10 VM
  2. Every CI build runs osslsigncode against the binary; `signtool verify /pa /v earlscheib.exe` on Windows shows a valid Authenticode signature from the OV certificate
  3. OV code-signing certificate is procured, HSM-provisioned, and the signing step is non-interactive in CI (cert procurement in progress from day one, provisioned before Phase 4 ships)
  4. Running `earlscheib.exe --scan` on Windows prints a "subcommand not yet implemented" stub and exits 0; all other subcommands (`--tray`, `--wizard`, `--test`, `--status`, `--install`) respond similarly — no crash
  5. The binary's Properties > Details tab on Windows shows the correct version, product name, and embedded icon from go-winres
**Plans**: 4 plans
Plans:
- [x] 01-01-PLAN.md — Go module init + subcommand dispatcher with HMAC ldflags injection
- [x] 01-02-PLAN.md — GitHub Actions CI workflow: cross-compile windows/amd64 on ubuntu-latest
- [x] 01-03-PLAN.md — go-winres resource embedding: version info, UAC manifest, placeholder icon
- [x] 01-04-PLAN.md — osslsigncode signing pipeline: conditional CI signing + dev-sign fallback + cert procurement doc

### Phase 2: Core Scanner
**Goal**: `earlscheib.exe --scan` behaves identically to the Python reference watcher — same dedup, settle check, HMAC signatures, retry logic, heartbeat, and logging — fully tested in CI without a Windows VM
**Depends on**: Phase 1
**Requirements**: SCAN-01, SCAN-02, SCAN-03, SCAN-04, SCAN-05, SCAN-06, SCAN-07, SCAN-08, SCAN-09, SCAN-10, SCAN-11, SCAN-12, SCAN-13, SCAN-14
**Success Criteria** (what must be TRUE):
  1. Running `earlscheib.exe --scan` against a test folder containing valid BMS XML files produces identical `X-EMS-Signature` HMAC values to the Python reference (`ems_watcher.py`) for the same secret and body — verified by a cross-language unit test in CI
  2. A file that appears mid-write (size changing between settle samples) is NOT POSTed; it is POSTed on the next scan once stable — verified by a test that truncates a file between settle samples
  3. A file already in `ems_watcher.db` (same filepath + mtime) is NOT re-posted; a new file with the same name but updated mtime IS posted — verified by unit test
  4. A failing webhook POST (HTTP 503) triggers exactly 3 total attempts with exponential delays; a 400 response triggers no retry and exits immediately — verified by a mock HTTP server in CI
  5. `earlscheib.exe --status` prints last run time, files processed today, and total files sent to stdout; `earlscheib.exe --test` exits 0 on a reachable server and non-zero when the endpoint is unreachable
**Plans**: 5 plans
Plans:
- [x] 02-01-PLAN.md — internal/config (INI parsing + DataDir) + internal/logging (slog + lumberjack rotation)
- [x] 02-02-PLAN.md — internal/db (SQLite WAL schema + dedup + retry + runs table)
- [ ] 02-03-PLAN.md — internal/webhook (Sign + Send + retry parity) + internal/heartbeat
- [ ] 02-04-PLAN.md — internal/scanner (settle check + scan loop + candidates)
- [ ] 02-05-PLAN.md — Wire main.go + --status + make test target + CI test job + HMAC parity test

### Phase 3: Tray Shell + Status Window
**Goal**: The tray icon lives in the system tray, reflects real scanner state (green/yellow/red), and opens a WebView2 status window — the full tray UX is functional without the installer
**Depends on**: Phase 2
**Requirements**: TRAY-01, TRAY-02, TRAY-03, TRAY-04, TRAY-05, TRAY-06, TRAY-07, TRAY-08, UI-01, UI-02, UI-03, UI-04, UI-05, UI-10
**Success Criteria** (what must be TRUE):
  1. After `earlscheib.exe --tray` runs on Windows, a colored tray icon appears; the icon turns green when the last scan succeeded within the heartbeat window, yellow when the heartbeat is stale (>65 min), and red when the last scan recorded an error — all driven solely by reading the SQLite `runs` table
  2. Double-clicking the tray icon or selecting "View Status" opens a WebView2 window showing last run time, files processed today/total, connection status (live ping), and a human-readable recent activity feed (not raw log lines); the window displays no data from the filesystem beyond what the SQLite DB provides
  3. Clicking "Run Now" in the tray menu or status window launches `earlscheib.exe --scan` as a child process; the status window refreshes to reflect the scan result within 2 seconds of scan completion
  4. Launching a second instance of `earlscheib.exe --tray` does NOT create a second tray icon; instead, it foregrounds the existing status window
  5. If `first_run.flag` exists (wizard not yet complete), clicking the tray icon or any status menu item opens the wizard shell rather than the status window
**Plans**: TBD
**UI hint**: yes

### Phase 4: Wizard + Installer
**Goal**: A signed single-file `.exe` installer, when run on a fresh Windows 10 VM, guides Marco through 3 wizard steps and results in a running tray icon, a configured watch folder, a registered Scheduled Task, and a visible wizard within 30 seconds of installer completion — no terminal, no config file editing, no prior runtime required
**Depends on**: Phase 3 (and SCAF-06 OV cert must be provisioned)
**Requirements**: UI-06, UI-07, UI-08, UI-09, INST-01, INST-02, INST-03, INST-04, INST-05, INST-06, INST-07, INST-08, INST-09, INST-10, INST-11
**Success Criteria** (what must be TRUE):
  1. Clicking the signed installer on a fresh Windows 10 VM (no Go, no Python, no WebView2 pre-installed) results in a running tray icon and the first-run wizard appearing within 30 seconds of installer completion; the wizard auto-detects the CCC ONE export folder if it exists at a known path
  2. Completing the 3-step wizard (folder confirmed, connection test passed, CCC ONE config acknowledged) writes `C:\EarlScheibWatcher\config.ini`, removes `first_run.flag`, and the tray icon turns green within one polling cycle
  3. Task Scheduler shows `EarlScheibEMSWatcher` running every 5 minutes; `schtasks /Query /TN EarlScheibEMSWatcher` confirms it; the task runs successfully as SYSTEM (or falls back to user if a mapped drive is detected and UNC enforcement is triggered)
  4. Running the installer a second time (upgrade) preserves the existing `config.ini` (does not overwrite Marco's folder path); the installer's welcome screen explains the SmartScreen "More info → Run anyway" dialog in plain English
  5. Running the uninstaller removes the Scheduled Task, HKCU Run key, and `C:\EarlScheibWatcher\`; the app no longer appears in Add/Remove Programs
**Plans**: TBD
**UI hint**: yes

### Phase 5: Telemetry + Remote Config
**Goal**: Broken installs are visible within 1 minute of an unhandled crash, and webhook URL or log level can be updated on Marco's machine without re-running the installer — both sides (client + server) are in production
**Depends on**: Phase 4
**Requirements**: OPS-01, OPS-02, OPS-03, OPS-04, OPS-05, OPS-06, OPS-07
**Success Criteria** (what must be TRUE):
  1. An unhandled panic in any goroutine or subcommand entry point appears as a telemetry POST on the server (`/earlscheibconcord/telemetry`) within 1 minute; the payload contains error type, file:line, OS version, and app version — it contains NO BMS XML content, NO variable values, NO customer PII
  2. Updating `webhook_url` in the server's remote-config response causes `C:\EarlScheibWatcher\config.ini` to reflect the new value within 20 minutes (next 15-min poll + atomic write); the next `--scan` uses the updated URL; `secret_key` and `watch_folder` are never overridden regardless of what the server sends
  3. Server-side `/earlscheibconcord/telemetry` and `/earlscheibconcord/remote-config` endpoints are deployed in `app.py`, HMAC-validated on every request, and reject unsigned requests with 401
**Plans**: TBD

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Scaffold + Signing | 4/4 | Complete |  |
| 2. Core Scanner | 2/5 | In Progress|  |
| 3. Tray Shell + Status Window | 0/TBD | Not started | - |
| 4. Wizard + Installer | 0/TBD | Not started | - |
| 5. Telemetry + Remote Config | 0/TBD | Not started | - |
