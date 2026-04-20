# Requirements — Earl Scheib EMS Watcher (Windows Client v1.0)

Scope: the Windows desktop client app. Server-side webhook (`app.py`) is out of scope except for the two new endpoints needed for Phase 5 (telemetry, remote config).

Derived from: `PROJECT.md` + `CLAUDE_CODE_PROMPT.md` (PRD) + `research/SUMMARY.md`.
REQ-ID format: `[CATEGORY]-[NN]`.

## v1 Requirements

### Scaffold & Signing (SCAF)

- [x] **SCAF-01**: Single Go binary with subcommand dispatch (`--tray`, `--scan`, `--wizard`, `--test`, `--status`, `--install`) — no separate executables
- [x] **SCAF-02**: Cross-compile from Linux to `windows/amd64` in CI (GitHub Actions or equivalent) without needing a Windows runner for the core build
- [x] **SCAF-03**: Binary is Authenticode-signed in CI using osslsigncode + cloud HSM (OV certificate) — every build, not just release
- [x] **SCAF-04**: HMAC secret key is baked into the binary via `-ldflags "-X main.secretKey=..."` — never written to `config.ini` or any user-visible file
- [x] **SCAF-05**: Binary embeds version info, application icon, and manifest via go-winres
- [x] **SCAF-06**: OV code-signing certificate is procured and provisioned into CI HSM before Phase 4 begins

### Core Scanner (SCAN)

- [x] **SCAN-01**: `--scan` mode performs a single scan of the configured watch folder and exits (matches existing Python `ems_watcher.py` behaviour for Scheduled Task invocation)
- [x] **SCAN-02**: Deduplicate by `(filepath, mtime)` using a local SQLite database at `C:\EarlScheibWatcher\ems_watcher.db`
- [x] **SCAN-03**: Settle check — require mtime + size stable for 2 consecutive samples at 2-second intervals (4 samples max) before POSTing; prevents partial-read POSTs while CCC ONE is still writing
- [x] **SCAN-04**: POST raw BMS XML bytes to `webhook_url` with headers `Content-Type: application/xml; charset=utf-8`, `X-EMS-Filename`, `X-EMS-Source: EarlScheibWatcher`, and `X-EMS-Signature: <hex HMAC-SHA256>`
- [x] **SCAN-05**: HMAC parity with existing Python — given the same secret and body, Go produces the identical `X-EMS-Signature`; verified by cross-language test
- [x] **SCAN-06**: Retry failed POSTs with exponential backoff — 3 attempts total, retry on transient network errors and HTTP 408/425/429/5xx only; permanent errors return immediately
- [x] **SCAN-07**: Track SHA256 of file bytes alongside the `(filepath, mtime)` dedup key for forensics
- [x] **SCAN-08**: Record each run in a `runs` table (timestamp, processed, errors, note)
- [x] **SCAN-09**: Send a lightweight heartbeat POST to `{webhook_url}/heartbeat` on each run (current Python behaviour preserved)
- [x] **SCAN-10**: SQLite opened in WAL mode with `busy_timeout=30000` and 5-retry DB-lock backoff (matches existing Python)
- [x] **SCAN-11**: `--test` mode sends a canned BMS payload (matching `TEST_BMS_XML` in ems_watcher.py) and reports success/failure via exit code
- [x] **SCAN-12**: `--status` mode prints last run info, files processed today, total files sent, recent files, recent warnings/errors from the log — to stdout (used by the tray process internally as well)
- [x] **SCAN-13**: Log file at `C:\EarlScheibWatcher\ems_watcher.log` with rotation at 2 MB × 5 backups; BMS XML payload content is NOT written to logs (PII — customer phone numbers / names)
- [x] **SCAN-14**: Tolerate missing / unreachable watch folder (network drive disconnect) — log and continue, do not crash

### Tray Shell (TRAY) — **OUT OF SCOPE (2026-04-20 scope cut)**

All TRAY-* requirements moved out of scope. Rationale: persistent foreground tray process is overkill for Marco's needs. Scheduled Task + log file is sufficient status surface.

- ~~TRAY-01 through TRAY-08~~: deferred indefinitely

### Installer Config Flow (UI) — **SIMPLIFIED (2026-04-20 scope cut)**

WebView2 tray/wizard removed. The Inno Setup installer now handles folder selection + connection test at install time via native pages.

- ~~UI-01, UI-02, UI-03, UI-04, UI-05, UI-10~~: out of scope (WebView2 / tray-related)
- **UI-06**: **Installer Step 1 (Folder):** Inno Setup page auto-scans common CCC ONE install paths (`C:\CCC\EMS_Export`, `C:\CCC\APPS\CCCCONE\CCCCONE\DATA`, `C:\Program Files\CCC`, `C:\Program Files (x86)\CCC`); pre-fills detected path if exactly one match; standard folder picker otherwise; validates folder exists before advancing; detects mapped-drive letter paths and warns / requires UNC path entry
- **UI-07**: **Installer Step 2 (Connection):** Shells out to `earlscheib.exe --test` with the chosen folder + default webhook; shows success checkmark or specific error; offers retry / continue-anyway on failure
- **UI-08**: **Installer Step 3 (CCC ONE config):** Info-only page showing the EMS Extract Preferences dialog screenshot, instructing Marco to check "Lock Estimate" + "Save Workfile"; "I've done this" checkbox required to finish. Live-watch deferred.
- **UI-09**: Installer writes `C:\EarlScheibWatcher\config.ini` on finish (no first_run.flag sentinel needed — installer blocks until config is written)

### Installer (INST)

- [x] **INST-01**: Single-file `.exe` installer built via Inno Setup 6 from Linux CI (`amake/innosetup-docker`)
- [x] **INST-02**: Installer extracts binary + default `config.ini` to `C:\EarlScheibWatcher\` (data dir)
- [x] **INST-03**: Installer sets directory ACLs via `icacls` — SYSTEM=Full, Users=Modify on `C:\EarlScheibWatcher\`
- [x] **INST-04**: Installer registers Scheduled Task `EarlScheibEMSWatcher` running `earlscheib.exe --scan` every 5 minutes, highest run level, as SYSTEM by default; falls back to user account if task creation as SYSTEM fails OR if a mapped drive letter is detected in the chosen folder path
- ~~INST-05~~: HKCU\Run entry removed from scope (no tray)
- ~~INST-06~~: WebView2 bootstrapper removed from scope (no WebView2)
- ~~INST-07~~: first_run.flag removed from scope (installer blocks until config is written)
- [x] **INST-08**: Installer uses `onlyifdoesntexist` on `config.ini` so upgrades preserve Marco's settings
- [x] **INST-09**: Installer runs the first `--scan` at the end of install to verify the pipeline works end-to-end before exiting
- [x] **INST-10**: Uninstaller removes Scheduled Task (`schtasks /Delete /TN EarlScheibEMSWatcher /F`) and `C:\EarlScheibWatcher\` (with confirmation to preserve data); is listed in Add/Remove Programs
- [x] **INST-11**: Installer displays a plain-English explanation of the SmartScreen "More info → Run anyway" dialog in its README / welcome screen so Marco isn't surprised

### Telemetry & Remote Config (OPS)

- [x] **OPS-01**: Global `recover()` wrapper on goroutines and command entry points; unhandled panics serialize a minimal error record (type, file:line, OS version, app version, timestamp) — NO BMS XML content, NO variable values, NO customer PII
- [x] **OPS-02**: Error record POSTed HMAC-signed to `{webhook_url}/telemetry` with header `X-EMS-Telemetry: 1`; failures to post are silent (no infinite recursion)
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
| SCAF-02 | Phase 1 | Complete |
| SCAF-03 | Phase 1 | Complete |
| SCAF-04 | Phase 1 | Complete |
| SCAF-05 | Phase 1 | Complete |
| SCAF-06 | Phase 1 | Complete |
| SCAN-01 | Phase 2 | Complete |
| SCAN-02 | Phase 2 | Complete |
| SCAN-03 | Phase 2 | Complete |
| SCAN-04 | Phase 2 | Complete |
| SCAN-05 | Phase 2 | Complete |
| SCAN-06 | Phase 2 | Complete |
| SCAN-07 | Phase 2 | Complete |
| SCAN-08 | Phase 2 | Complete |
| SCAN-09 | Phase 2 | Complete |
| SCAN-10 | Phase 2 | Complete |
| SCAN-11 | Phase 2 | Complete |
| SCAN-12 | Phase 2 | Complete |
| SCAN-13 | Phase 2 | Complete |
| SCAN-14 | Phase 2 | Complete |
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
| UI-06 | Phase 4 | Complete |
| UI-07 | Phase 4 | Complete |
| UI-08 | Phase 4 | Complete |
| UI-09 | Phase 4 | Complete |
| INST-01 | Phase 4 | Complete |
| INST-02 | Phase 4 | Complete |
| INST-03 | Phase 4 | Complete |
| INST-04 | Phase 4 | Complete |
| INST-05 | Phase 4 | Pending |
| INST-06 | Phase 4 | Pending |
| INST-07 | Phase 4 | Pending |
| INST-08 | Phase 4 | Complete |
| INST-09 | Phase 4 | Complete |
| INST-10 | Phase 4 | Complete |
| INST-11 | Phase 4 | Complete |
| OPS-01 | Phase 4 | Complete |
| OPS-02 | Phase 4 | Complete |
| OPS-03 | Phase 5 | Pending |
| OPS-04 | Phase 5 | Pending |
| OPS-05 | Phase 5 | Pending |
| OPS-06 | Phase 5 | Pending |
| OPS-07 | Phase 5 | Pending |
