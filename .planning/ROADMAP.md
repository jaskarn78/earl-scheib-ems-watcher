# Roadmap: Earl Scheib EMS Watcher — Windows Client

## Overview

Four phases deliver Marco's one-download install. Phase 1 established the cross-compile pipeline and kicked off OV cert procurement so code-signing is never the bottleneck at ship time. Phase 2 ported all Python watcher logic to pure-Go with no CGO — the scanner is fully unit-testable in CI without a Windows VM. Phase 3 (Installer + Native Config) packages everything into an Inno Setup installer with a simple native config flow (folder picker + connection test at install time, no tray, no WebView2). Phase 4 adds crash telemetry and remote config so broken installs are visible and webhook URLs can be rotated without Marco re-running the installer.

**Scope change (2026-04-20):** After Phase 2 shipped, scope was reduced — the tray app and WebView2 wizard were cut as overkill. Marco gets a one-shot configure-at-install-time flow instead of a persistent tray. Log file on disk is the only ongoing status surface.

## Phases

**Phase Numbering:**
- Integer phases (1, 2, 3, 4): Planned milestone work

- [x] **Phase 1: Scaffold + Signing** - Go module, CI cross-compile pipeline, OV cert procurement, and Authenticode signing infrastructure (completed 2026-04-20)
- [x] **Phase 2: Core Scanner** - Pure-Go CGO-free port of all Python watcher logic (dedup, settle, HMAC, retry, heartbeat, logging) (completed 2026-04-20)
- [x] **Phase 3: Installer + Native Config** - Inno Setup single-exe installer with install-time folder picker + connection test + Scheduled Task registration; no tray, no WebView2 (completed 2026-04-20)
- [x] **Phase 4: Telemetry + Remote Config** - Crash telemetry, remote config poller, and coordinated server-side endpoints (completed 2026-04-21)
- [ ] **Phase 5: Queue Admin UI** - Marco-facing window to view & cancel queued outbound SMS messages; `earlscheib.exe --admin` launches a local HTTP server + opens default browser to an embedded SPA; new server-side `/queue` endpoint reads/deletes against jobs.db

## Phase Details

### Phase 1: Scaffold + Signing
**Goal**: A signed, runnable Windows exe ships from Linux CI on every commit — establishing the full toolchain before any feature code is written
**Depends on**: Nothing (first phase)
**Requirements**: SCAF-01, SCAF-02, SCAF-03, SCAF-04, SCAF-05, SCAF-06
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
**Plans**: 5 plans
Plans:
- [x] 02-01-PLAN.md — internal/config (INI parsing + DataDir) + internal/logging (slog + lumberjack rotation)
- [x] 02-02-PLAN.md — internal/db (SQLite WAL schema + dedup + retry + runs table)
- [x] 02-03-PLAN.md — internal/webhook (Sign + Send + retry parity) + internal/heartbeat
- [x] 02-04-PLAN.md — internal/scanner (settle check + scan loop + candidates)
- [x] 02-05-PLAN.md — Wire main.go + --status + make test target + CI test job + HMAC parity test

### Phase 3: Installer + Native Config
**Goal**: A signed single-file `.exe` installer, when run on a fresh Windows 10 VM, prompts Marco once for the CCC ONE export folder, tests the webhook connection, registers the Scheduled Task, and leaves a running `earlscheib.exe --scan` on a 5-minute schedule — no terminal, no tray, no prior runtime required.
**Depends on**: Phase 2 (and SCAF-06 OV cert must be provisioned)
**Requirements**: INST-01, INST-02, INST-03, INST-04, INST-08, INST-09, INST-10, INST-11, UI-06, UI-07, UI-08, UI-09
**Success Criteria** (what must be TRUE):
  1. Running the signed installer on a fresh Windows 10 VM (no Go, no Python, no WebView2 pre-installed) results in: (a) files extracted to `C:\EarlScheibWatcher\`, (b) a Scheduled Task `EarlScheibEMSWatcher` running every 5 minutes, (c) `config.ini` written with Marco's folder and the webhook URL, (d) the first scan either succeeds or surfaces a clear error before the installer closes
  2. The installer prompts for the CCC ONE export folder with an auto-detected default (scans `C:\CCC\EMS_Export`, `C:\CCC\APPS\CCCCONE\CCCCONE\DATA`, etc.); if multiple paths exist Marco picks one, if none exist Marco browses
  3. The installer runs a connection test against `{webhook_url}/status` before exiting; failure shows a plain-English error message with retry / continue-anyway options
  4. Running the installer a second time (upgrade) preserves Marco's existing `config.ini`; the installer's welcome screen explains the SmartScreen "More info → Run anyway" dialog in plain English
  5. Running the uninstaller removes the Scheduled Task and `C:\EarlScheibWatcher\`; the app no longer appears in Add/Remove Programs
**Plans**: 3 plans
Plans:
- [x] 03-01-PLAN.md — Inno Setup script (earlscheib.iss): all wizard pages, Pascal code, Scheduled Task XMLs, uninstaller hooks
- [x] 03-02-PLAN.md — Build pipeline: Makefile installer target + CI build-installer job + installer Authenticode signing
- [x] 03-03-PLAN.md — CCC ONE diagram (SVG) + installer README.txt + CI syntax-check job (iscc parse-only)

### Phase 4: Telemetry + Remote Config
**Goal**: Broken installs are visible within 1 minute of an unhandled crash, and webhook URL or log level can be updated on Marco's machine without re-running the installer — both sides (client + server) are in production
**Depends on**: Phase 3
**Requirements**: OPS-01, OPS-02, OPS-03, OPS-04, OPS-05, OPS-06, OPS-07
**Success Criteria** (what must be TRUE):
  1. An unhandled panic in the scan process appears as a telemetry POST on the server (`/earlscheibconcord/telemetry`) within 1 minute; the payload contains error type, file:line, OS version, and app version — it contains NO BMS XML content, NO variable values, NO customer PII
  2. Updating `webhook_url` in the server's remote-config response causes `C:\EarlScheibWatcher\config.ini` to reflect the new value within 5 minutes (next --scan picks it up); the next `--scan` uses the updated URL; `secret_key` and `watch_folder` are never overridden
  3. Server-side `/earlscheibconcord/telemetry` and `/earlscheibconcord/remote-config` endpoints are deployed in `app.py`, HMAC-validated on every request, and reject unsigned requests with 401
  4. Twilio WhatsApp→SMS switch documented in `app.py` as a clearly-labeled comment block
**Plans**: 3 plans
Plans:
- [x] 04-01-PLAN.md — internal/telemetry package (panic recovery + HMAC POST) + wire into main.go runScan/runTest/runStatus
- [x] 04-02-PLAN.md — internal/remoteconfig (Fetch + Apply) + config.Merge atomic helper + wire into main.go runScan
- [x] 04-03-PLAN.md — app.py: /telemetry + /remote-config endpoints + HMAC validation + remote_config.json + Twilio SMS comment

### Phase 5: Queue Admin UI
**Goal**: `earlscheib.exe --admin` launches a local HTTP server, opens Marco's default browser, and shows a clean modern UI listing all queued/pending outbound SMS messages from the server's `jobs.db`, grouped by customer with scheduled send time and repair-job reference. Marco can cancel a queued message before it sends.
**Depends on**: Phase 4 (reuses HMAC signing + config patterns)
**Requirements**: ADMIN-01, ADMIN-02, ADMIN-03, ADMIN-04, ADMIN-05, ADMIN-06, ADMIN-07, ADMIN-08, ADMIN-09, ADMIN-10, ADMIN-11
**Success Criteria** (what must be TRUE):
  1. Running `earlscheib.exe --admin` on Windows starts a local HTTP server bound to `127.0.0.1:RANDOM_PORT`, opens the default browser to that URL, and serves a single-page app
  2. The page lists all pending (not yet sent) SMS jobs from the server's `jobs.db` — grouped by customer, showing scheduled send time, customer name, phone, repair-job reference
  3. Marco can cancel a queued message via a single click; the server removes it from `jobs.db` and the UI updates
  4. New server endpoint `/earlscheibconcord/queue` (GET + DELETE) is HMAC-authenticated like existing routes; rejects unsigned with 401
  5. The UI is visually distinct and polished — not generic SaaS templates; per `ui-brand.md` (committed palette, distinctive typography, intentional layout)
  6. Closing the browser tab / pressing Ctrl+C on the launcher exits the HTTP server cleanly
**Plans**: 4 plans
Plans:
- [ ] 05-01-PLAN.md — Server-side /earlscheibconcord/queue GET + DELETE on app.py + pytest coverage + dead-code cleanup
- [ ] 05-02-PLAN.md — internal/admin Go package: local HTTP server, HMAC-signing proxy, cross-platform launcher, tests
- [ ] 05-03-PLAN.md — Embedded UI assets: index.html + main.css + main.js (Concord Garage aesthetic)
- [ ] 05-04-PLAN.md — Wire --admin into cmd/earlscheib/main.go + docs/admin-ui-guide.md + REQUIREMENTS.md ADMIN-01..11 block
**UI hint**: yes (run /gsd:ui-phase 5 before /gsd:plan-phase 5)

## Progress

**Execution Order:**
Phases execute in numeric order: 1 → 2 → 3 → 4 → 5

| Phase | Plans Complete | Status | Completed |
|-------|----------------|--------|-----------|
| 1. Scaffold + Signing | 4/4 | Complete | 2026-04-20 |
| 2. Core Scanner | 5/5 | Complete | 2026-04-20 |
| 3. Installer + Native Config | 3/3 | Complete   | 2026-04-20 |
| 4. Telemetry + Remote Config | 3/3 | Complete   | 2026-04-21 |
| 5. Queue Admin UI | 0/4 | Not started | - |

