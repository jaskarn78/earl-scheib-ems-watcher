# Requirements — Earl Scheib EMS Watcher (Windows Client v1.0)

Scope: the Windows desktop client app. Server-side webhook (`app.py`) is out of scope except for the two new endpoints needed for Phase 5 (telemetry, remote config).

Derived from: `PROJECT.md` + `CLAUDE_CODE_PROMPT.md` (PRD) + `research/SUMMARY.md`.
REQ-ID format: `[CATEGORY]-[NN]`.

## v1 Requirements

### Scaffold & Signing (SCAF)

- [x] **SCAF-01**: Single Go binary with subcommand dispatch (`--tray`, `--scan`, `--wizard`, `--test`, `--status`, `--install`) — no separate executables
- [ ] **SCAF-02**: Cross-compile from Linux to `windows/amd64` in CI (GitHub Actions or equivalent) without needing a Windows runner for the core build
- [ ] **SCAF-03**: Binary is Authenticode-signed in CI using osslsigncode + cloud HSM (OV certificate) — every build, not just release
- [x] **SCAF-04**: HMAC secret key is baked into the binary via `-ldflags "-X main.secretKey=..."` — never written to `config.ini` or any user-visible file
- [ ] **SCAF-05**: Binary embeds version info, application icon, and manifest via go-winres
- [ ] **SCAF-06**: OV code-signing certificate is procured and provisioned into CI HSM before Phase 4 begins

### Core Scanner (SCAN)

- [ ] **SCAN-01**: `--scan` mode performs a single scan of the configured watch folder and exits (matches existing Python `ems_watcher.py` behaviour for Scheduled Task invocation)
- [ ] **SCAN-02**: Deduplicate by `(filepath, mtime)` using a local SQLite database at `C:\EarlScheibWatcher\ems_watcher.db`
- [ ] **SCAN-03**: Settle check — require mtime + size stable for 2 consecutive samples at 2-second intervals (4 samples max) before POSTing; prevents partial-read POSTs while CCC ONE is still writing
- [ ] **SCAN-04**: POST raw BMS XML bytes to `webhook_url` with headers `Content-Type: application/xml; charset=utf-8`, `X-EMS-Filename`, `X-EMS-Source: EarlScheibWatcher`, and `X-EMS-Signature: <hex HMAC-SHA256>`
- [ ] **SCAN-05**: HMAC parity with existing Python — given the same secret and body, Go produces the identical `X-EMS-Signature`; verified by cross-language test
- [ ] **SCAN-06**: Retry failed POSTs with exponential backoff — 3 attempts total, retry on transient network errors and HTTP 408/425/429/5xx only; permanent errors return immediately
- [ ] **SCAN-07**: Track SHA256 of file bytes alongside the `(filepath, mtime)` dedup key for forensics
- [ ] **SCAN-08**: Record each run in a `runs` table (timestamp, processed, errors, note)
- [ ] **SCAN-09**: Send a lightweight heartbeat POST to `{webhook_url}/heartbeat` on each run (current Python behaviour preserved)
- [ ] **SCAN-10**: SQLite opened in WAL mode with `busy_timeout=30000` and 5-retry DB-lock backoff (matches existing Python)
- [ ] **SCAN-11**: `--test` mode sends a canned BMS payload (matching `TEST_BMS_XML` in ems_watcher.py) and reports success/failure via exit code
- [ ] **SCAN-12**: `--status` mode prints last run info, files processed today, total files sent, recent files, recent warnings/errors from the log — to stdout (used by the tray process internally as well)
- [ ] **SCAN-13**: Log file at `C:\EarlScheibWatcher\ems_watcher.log` with rotation at 2 MB × 5 backups; BMS XML payload content is NOT written to logs (PII — customer phone numbers / names)
- [ ] **SCAN-14**: Tolerate missing / unreachable watch folder (network drive disconnect) — log and continue, do not crash

### Tray Shell (TRAY)

- [ ] **TRAY-01**: System tray icon using fyne.io/systray, with three color states — green (healthy), yellow (warning / stale heartbeat), red (error / offline)
- [ ] **TRAY-02**: Icon state derives from local SQLite (`runs` table + last heartbeat success) via a polling goroutine (~60s)
- [ ] **TRAY-03**: Tooltip shows "Last check-in: Xm ago" and "Files today: N"
- [ ] **TRAY-04**: Right-click menu: "View Status", "Open Log", "Settings", "Run Now", "Exit"
- [ ] **TRAY-05**: Double-click tray opens the Status window
- [ ] **TRAY-06**: "Run Now" fires `--scan` as a child process; icon shows a transient "scanning" state; result is reflected in the status window within 2s of scan completion
- [ ] **TRAY-07**: Single-instance enforcement via named Win32 mutex (`EarlScheibWatcherTray`) — second launch foregrounds the existing tray's Status window, never silently exits
- [ ] **TRAY-08**: Tray process auto-starts on user login via `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` (NOT Scheduled Task — avoids session 0 isolation)

### Status Window & Wizard UI (UI)

- [ ] **UI-01**: WebView2-based UI using jchv/go-webview2, in-process `Bind()` + `Dispatch(Eval())` — no loopback HTTP server
- [ ] **UI-02**: All HTML/CSS/JS assets embedded in the binary via `go:embed` — no separate resource files to manage
- [ ] **UI-03**: Status window shows: connection status (live ping), last run time, files processed today / all time, "Run Now" button, recent activity feed (last 10 entries) rendered human-readably (not raw log lines)
- [ ] **UI-04**: Settings option re-opens the wizard for folder / connection re-configuration (not a free-form settings form)
- [ ] **UI-05**: First-run wizard is a 3-step flow rendered in WebView2: Step 1 folder, Step 2 connection, Step 3 CCC ONE config guide
- [ ] **UI-06**: **Wizard Step 1 (Folder):** Auto-scan common CCC ONE install paths (`C:\CCC\EMS_Export`, `C:\CCC\APPS\CCCCONE\CCCCONE\DATA`, `C:\Program Files\CCC`, `C:\Program Files (x86)\CCC`); pre-fill detected path if exactly one match; folder picker otherwise; validate folder exists before advancing; detect mapped-drive letter paths and warn / require UNC path entry
- [ ] **UI-07**: **Wizard Step 2 (Connection):** "Test Connection" button pings `{webhook_url}/status` with current secret; display success checkmark or specific error; advance disabled until test passes
- [ ] **UI-08**: **Wizard Step 3 (CCC ONE config):** Render a clear diagram of the EMS Extract Preferences dialog; instruct Marco to check "Lock Estimate" + "Save Workfile"; "I've done this" confirmation button; optional live-watch mode that polls the folder for 2 minutes and shows "Got it!" / "Nothing yet — check CCC ONE settings"
- [ ] **UI-09**: Wizard writes `C:\EarlScheibWatcher\config.ini` on completion and clears the `first_run.flag` sentinel
- [ ] **UI-10**: Clicking the tray icon or status menu before wizard completion re-opens the wizard instead of the status window

### Installer (INST)

- [ ] **INST-01**: Single-file `.exe` installer built via Inno Setup 6 from Linux CI (`amake/innosetup-docker`)
- [ ] **INST-02**: Installer extracts binary + default `config.ini` to `C:\EarlScheibWatcher\` (data dir)
- [ ] **INST-03**: Installer sets directory ACLs via `icacls` — SYSTEM=Full, Users=Modify on `C:\EarlScheibWatcher\`
- [ ] **INST-04**: Installer registers Scheduled Task `EarlScheibEMSWatcher` running `earlscheib.exe --scan` every 5 minutes, highest run level, as SYSTEM by default; fall back to user account if task creation as SYSTEM fails
- [ ] **INST-05**: Installer writes `HKCU\...\Run` entry so the tray auto-starts on login
- [ ] **INST-06**: Installer bundles the WebView2 Evergreen offline standalone installer and runs it silently if WebView2 runtime is not detected (fallback: Fixed Version Runtime if bundling is not viable — decided during Phase 4)
- [ ] **INST-07**: Installer creates `C:\EarlScheibWatcher\first_run.flag` so the tray launches the wizard on first start
- [ ] **INST-08**: Installer uses `onlyifdoesntexist` on `config.ini` so upgrades preserve Marco's settings
- [ ] **INST-09**: Installer triggers the tray binary at the end of install (`[Run] earlscheib.exe --tray`) so Marco sees the wizard immediately
- [ ] **INST-10**: Uninstaller removes Scheduled Task (`schtasks /Delete /TN EarlScheibEMSWatcher /F`), removes HKCU Run key, removes `C:\EarlScheibWatcher\` (with confirmation to preserve data); is listed in Add/Remove Programs
- [ ] **INST-11**: Installer displays a plain-English explanation of the SmartScreen "More info → Run anyway" dialog in its README / welcome screen so Marco isn't surprised

### Telemetry & Remote Config (OPS)

- [ ] **OPS-01**: Global `recover()` wrapper on goroutines and command entry points; unhandled panics serialize a minimal error record (type, file:line, OS version, app version, timestamp) — NO BMS XML content, NO variable values, NO customer PII
- [ ] **OPS-02**: Error record POSTed HMAC-signed to `{webhook_url}/telemetry` with header `X-EMS-Telemetry: 1`; failures to post are silent (no infinite recursion)
- [ ] **OPS-03**: Background poller in the tray process fetches `{webhook_url}/remote-config` every 15 minutes (HMAC-authenticated GET)
- [ ] **OPS-04**: Remote config supports ONLY whitelisted fields: `webhook_url`, `log_level`. Never `secret_key`, never `watch_folder`, never arbitrary keys
- [ ] **OPS-05**: On change, remote config is atomically merged into local `config.ini`; tray logs the change; next `--scan` picks up new values
- [ ] **OPS-06**: Server-side: `app.py` gains `/earlscheibconcord/telemetry` (POST) and `/earlscheibconcord/remote-config` (GET) endpoints, HMAC-validated like existing routes
- [ ] **OPS-07**: Document the Twilio WhatsApp-sandbox → production-SMS switch in `app.py` — comment block explaining: change `TWILIO_FROM` in `.env`, remove the `whatsapp:` prefix from To/From in the Twilio API call; no other changes needed

## v2 / Deferred Requirements

- Auto-update mechanism for the client binary
- Free-form settings form beyond wizard re-run
- Toast notifications for per-file events
- Next-run countdown timer in tray tooltip
- Multi-shop / multi-tenant configuration
- Authenticode EV certificate (upgrade from OV if SmartScreen reputation still trips)
- Replay-protection: server-side HMAC timestamp validation (coordinated client+server change)

## Out of Scope

- **Messaging logic in the Windows client** — Twilio integration is server-side only; client only POSTs BMS XML. *Why: separation of concerns.*
- **Rewriting the webhook server** — `app.py` is in production and reliable; only the two new endpoints above are added. *Why: focus on the client.*
- **macOS / Linux builds** — Marco runs Windows; CCC ONE is Windows-only. *Why: no user need.*
- **Web-based admin UI** — tray + wizard is the entire UX. *Why: scope creep.*
- **User-editable secret key** — baked into binary only. *Why: prevents Marco from accidentally breaking auth.*
- **Third-party SaaS crash reporter (Sentry etc.)** — telemetry goes to existing webhook server only. *Why: data egress risk with BMS PII.*
- **Customer-facing reply handling / STOP flow** — handled server-side via Twilio STOP keyword. *Why: server concern.*
- **Pause / disable toggle in tray** — non-technical user footgun. *Why: Marco can quit the tray or uninstall; a "paused" state invisibly breaks the shop's follow-up flow.*
- **Settings panel exposing log_level, watch_folder, webhook_url** to Marco — wizard re-run is the only config UI. *Why: no user-editable config surface Marco can break.*

## Traceability

<!-- Filled in by roadmap creation — REQ-ID → Phase mapping. -->

| Requirement | Phase | Status |
|-------------|-------|--------|
| SCAF-01 | Phase 1 | Complete |
| SCAF-02 | Phase 1 | Pending |
| SCAF-03 | Phase 1 | Pending |
| SCAF-04 | Phase 1 | Complete |
| SCAF-05 | Phase 1 | Pending |
| SCAF-06 | Phase 1 | Pending |
| SCAN-01 | Phase 2 | Pending |
| SCAN-02 | Phase 2 | Pending |
| SCAN-03 | Phase 2 | Pending |
| SCAN-04 | Phase 2 | Pending |
| SCAN-05 | Phase 2 | Pending |
| SCAN-06 | Phase 2 | Pending |
| SCAN-07 | Phase 2 | Pending |
| SCAN-08 | Phase 2 | Pending |
| SCAN-09 | Phase 2 | Pending |
| SCAN-10 | Phase 2 | Pending |
| SCAN-11 | Phase 2 | Pending |
| SCAN-12 | Phase 2 | Pending |
| SCAN-13 | Phase 2 | Pending |
| SCAN-14 | Phase 2 | Pending |
| TRAY-01 | Phase 3 | Pending |
| TRAY-02 | Phase 3 | Pending |
| TRAY-03 | Phase 3 | Pending |
| TRAY-04 | Phase 3 | Pending |
| TRAY-05 | Phase 3 | Pending |
| TRAY-06 | Phase 3 | Pending |
| TRAY-07 | Phase 3 | Pending |
| TRAY-08 | Phase 3 | Pending |
| UI-01 | Phase 3 | Pending |
| UI-02 | Phase 3 | Pending |
| UI-03 | Phase 3 | Pending |
| UI-04 | Phase 3 | Pending |
| UI-05 | Phase 3 | Pending |
| UI-10 | Phase 3 | Pending |
| UI-06 | Phase 4 | Pending |
| UI-07 | Phase 4 | Pending |
| UI-08 | Phase 4 | Pending |
| UI-09 | Phase 4 | Pending |
| INST-01 | Phase 4 | Pending |
| INST-02 | Phase 4 | Pending |
| INST-03 | Phase 4 | Pending |
| INST-04 | Phase 4 | Pending |
| INST-05 | Phase 4 | Pending |
| INST-06 | Phase 4 | Pending |
| INST-07 | Phase 4 | Pending |
| INST-08 | Phase 4 | Pending |
| INST-09 | Phase 4 | Pending |
| INST-10 | Phase 4 | Pending |
| INST-11 | Phase 4 | Pending |
| OPS-01 | Phase 5 | Pending |
| OPS-02 | Phase 5 | Pending |
| OPS-03 | Phase 5 | Pending |
| OPS-04 | Phase 5 | Pending |
| OPS-05 | Phase 5 | Pending |
| OPS-06 | Phase 5 | Pending |
| OPS-07 | Phase 5 | Pending |
