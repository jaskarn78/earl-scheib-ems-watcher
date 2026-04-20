# Project Research Summary

**Project:** Earl Scheib EMS Watcher — Windows Client
**Domain:** Windows desktop tray utility (file-watcher + webhook client, non-technical end user)
**Researched:** 2026-04-20
**Confidence:** MEDIUM-HIGH

## Executive Summary

This is a bespoke, single-customer Windows system tray utility that ports existing battle-tested Python logic into a polished, zero-terminal Go application. The product pattern is well-established — Backblaze, Dropbox, 1Password mini, and SyncTrayzor all converge on the same traffic-light tray icon, first-run wizard, and status window with a "Run Now" button. Deviating from the pattern costs trust for no gain.

Research converges cleanly on a single recommended stack: **Go + fyne.io/systray + jchv/go-webview2 + modernc.org/sqlite + Inno Setup, cross-compiled from Linux.** Two residual decisions must be locked before Phase 3: Wails-vs-raw-go-webview2 (raw recommended) and WebView2 Evergreen bootstrapper strategy (bundle the ~120 MB offline standalone installer vs Fixed Version runtime).

The risk profile is front-loaded and infrastructure-heavy. Five pitfalls must be designed in from Phase 1 — they cannot be retrofitted: (1) OV code-signing certificate procurement takes 2–10 business days and must start at kickoff; (2) SYSTEM-scheduled tasks cannot see mapped drive letters, so the wizard must force UNC paths or run as the user account; (3) the WebView2 bootstrapper must be the offline standalone installer bundled inside the `.exe`; (4) the HMAC secret must be baked into the binary via `-ldflags`, never in `config.ini`; (5) the data directory ACL must grant both SYSTEM=Full and Users=Modify at install time. Marco's success condition is simple: one download, three wizard clicks, tray icon turns green.

## Key Findings

### Recommended Stack

Single Go binary with subcommand dispatch, cross-compiled from Linux via CI. Most dependencies pure-Go / CGO-free; two components (systray, webview2) require CGO with mingw-w64.

**Core technologies:**
- **Go 1.22+** with subcommand dispatch (`--tray`, `--scan`, `--wizard`, `--test`, `--status`) — single signing surface, single install artifact, no version skew between processes
- **fyne.io/systray v1.12.0** — actively maintained systray; dynamic `SetIcon()` for green/yellow/red state
- **jchv/go-webview2** — only pure-Go WebView2 binding; `Bind()` + `Dispatch(Eval())` RPC model (no loopback HTTP server)
- **modernc.org/sqlite v1.49.1** — pure-Go SQLite; WAL mode with `busy_timeout=30000` handles tray↔scan IPC
- **gopkg.in/ini.v1** — `config.ini` parsing, mirrors existing Python config shape
- **log/slog + gopkg.in/natefinch/lumberjack.v2** — structured logging with rotation
- **crypto/hmac, crypto/sha256** (stdlib) — HMAC-SHA256 signing; parity test vs Python required
- **hashicorp/go-retryablehttp** — exponential backoff for webhook POSTs
- **Inno Setup 6.7.1** (via `amake/innosetup-docker` on Linux CI) — single-exe installer
- **go-winres** — embed icon + version info + manifest
- **osslsigncode + OV cert + cloud HSM** (DigiCert KeyLocker or SSL.com eSigner) — Authenticode signing from Linux CI; EV no longer bypasses SmartScreen post March 2024
- **capnspacehook/taskmaster** (optional) — Task Scheduler COM API; fallback = `schtasks` shell-out with `/XML`

### Expected Features

**Must have (table stakes — users expect these):**
- Three-state tray icon (green/yellow/red) with tooltip
- Right-click menu: View Status, Open Log, Settings, Run Now, Exit
- First-run 3-step wizard (folder detection → connection test → CCC ONE config guide)
- Status window: last run, files processed today/total, recent activity, connection status
- "Run Now" button (single most-used discoverability feature in this category)
- Background scan every 5 min (Scheduled Task)
- Single-exe installer with uninstaller
- Single-instance enforcement (named mutex); second launch foregrounds existing window
- Auto-start tray on login (HKCU Run key, NOT Scheduled Task — session 0 isolation)

**Should have (differentiators — Marco-specific):**
- Human-readable activity feed ("Sent estimate for Honda Civic ($2,840) — 2h ago") rather than raw log lines
- CCC ONE default-path auto-detection in wizard
- Connection test before wizard advances
- Optional 2-minute live watch during wizard
- Crash telemetry (opt-in at install, minimal payload: error type + location + OS + app version only)
- Remote config override (webhook URL / log level only — never secret or watch folder)

**Defer (v1.x, post-ship validation):**
- Auto-update mechanism
- Settings panel beyond wizard re-run
- Toast notifications for every event
- Next-run countdown timer

**Anti-features (deliberately NOT build):**
- User-editable secret in config.ini
- Pause/disable toggle visible to Marco
- Raw log tail inside the app
- Third-party SaaS crash reporter (Sentry etc.) — data egress risk with BMS PII
- In-app branding chrome or upsells

### Architecture Approach

Single binary, subcommand-dispatched. Tray process is persistent (auto-started at login, HKCU Run key). Scan process is short-lived, invoked by Scheduled Task every 5 min. IPC between them is asynchronous via shared SQLite file in WAL mode — no named pipe needed for v1. WebView2 UI runs in-process using `Bind()` for JS→Go calls and `Dispatch(Eval())` for Go→JS updates (threading discipline is strict). Configuration lives in `C:\EarlScheibWatcher\config.ini` with ACLs granting both SYSTEM (for the task) and the logged-in user (for the tray) read+write. Crash telemetry and remote config call back to the existing webhook server on new endpoints (`/telemetry`, `/remote-config`), HMAC-signed like the main POST.

**Major components:**
1. **Core scanner library** (`internal/scanner`, `internal/db`, `internal/config`, `internal/webhook`) — pure Go, CGO-free, testable in CI without Windows
2. **Tray shell** (`cmd/earlscheib-tray`) — systray + DB polling goroutine for icon state; named mutex for single-instance
3. **WebView2 UI** (`cmd/earlscheib-tray` + embedded HTML/CSS/JS assets) — wizard and status window in one HTML document with hash routing
4. **Installer** (Inno Setup script) — extracts binary, sets data dir ACLs, registers Scheduled Task as SYSTEM, writes HKCU Run key, triggers first-run wizard, bundles WebView2 offline bootstrapper
5. **Telemetry + remote-config layer** — background goroutine; 15-min poll for remote config; recover()/panic wrappers route crashes to `/telemetry`

### Critical Pitfalls

1. **Code-signing cert procurement lead time (2–10 business days)** — start at Phase 1 kickoff. CA/B Forum now mandates HSM storage; use cloud HSM for CI. OV cert is sufficient post-2024; EV no longer bypasses SmartScreen.
2. **SYSTEM cannot see mapped drive letters** — existing Python installer registers task as SYSTEM; if Marco's CCC ONE export is on `Z:\...`, task silently fails forever. Wizard must detect drive-letter paths and force UNC; or fall back to running task as user (with password-change fragility).
3. **WebView2 Evergreen bootstrapper requires internet at install time** — bundle the ~120 MB offline standalone installer inside the `.exe` and run silently. Fixed Version Runtime is the fallback if installer size is unacceptable.
4. **HMAC secret in config.ini = auth footgun** — bake via `go build -ldflags "-X main.secretKey=..."`. Never write to config file Marco can see or edit.
5. **Data dir ACL split** — installer must `icacls` SYSTEM=Full + Users=Modify on `C:\EarlScheibWatcher\`. If this step is skipped, scan and tray will silently fail with permission errors on different machines unpredictably.

Additional watch list: port the existing settle check and WAL+retry logic exactly (do not simplify), PII in logs/crash dumps (strip BMS XML content), SmartScreen warning UX in installer copy, tray icon overflow in Windows 11 hidden area.

## Implications for Roadmap

Based on research, a 5-phase structure is the natural shape:

### Phase 1: Scaffold + Signing Infrastructure
**Rationale:** Cert procurement has a 2–10 day lead time — must start at kickoff. Signing every build from day one builds SmartScreen reputation progressively (not just at release). Establishes cross-compile pipeline and subcommand dispatch skeleton that every later phase depends on.
**Delivers:** Go module scaffold, subcommand dispatcher, CI that cross-compiles Linux→Windows, go-winres resource embedding, osslsigncode + cloud HSM signing, first signed dummy exe in CI.
**Avoids:** Code-signing panic at ship time; last-minute mingw-w64 toolchain debugging.

### Phase 2: Core Scanner (No UI)
**Rationale:** Pure-Go, CGO-free port of `ems_watcher.py`. Cross-language HMAC parity must be proven BEFORE the live server ever sees a Go-signed payload. Establishes DB schema used by tray in Phase 3.
**Delivers:** `internal/scanner`, `internal/db`, `internal/config`, `internal/webhook`; `earlscheib.exe --scan` behaves identically to current Python watcher; unit tests including HMAC parity against Python reference; SQLite WAL + busy_timeout + retry.
**Uses:** modernc.org/sqlite, ini.v1, retryablehttp, crypto/hmac.
**Avoids:** Partial-read POSTs (port settle check), DB lock duplicate-send (port WAL+retry).

### Phase 3: Tray Shell + Status Window
**Rationale:** Introduces CGO (systray + webview2) — de-risk cross-compile before adding installer complexity. Validates tray↔scan SQLite IPC pattern before the wizard depends on it. Wails-vs-raw decision is committed here (recommendation: raw go-webview2).
**Delivers:** `cmd/earlscheib-tray` with three ICO states + context menu + tooltip; 60s DB polling goroutine for icon color; WebView2 status window (no wizard yet) with activity feed, "Run Now", connection status; named mutex single-instance; HKCU Run key auto-start (dev-mode for now).
**Uses:** fyne.io/systray, jchv/go-webview2, embedded HTML/CSS/JS via `embed.FS`.
**Avoids:** Session 0 isolation (HKCU Run, not Scheduled Task, for tray); goroutine→UI race (all Go→JS through `Dispatch(Eval())`).

### Phase 4: First-Run Wizard + Installer
**Rationale:** Installer is the highest-risk ship artifact and must come last. First distributable end-to-end build. Marco-ready.
**Delivers:** 3-step wizard (CCC ONE path auto-detect → connection test → EMS Extract Preferences guide + optional 2-min live watch); `first_run.flag` sentinel; Inno Setup script with data dir ACLs (icacls SYSTEM=Full + Users=Modify), Scheduled Task as SYSTEM (or user if mapped-drive detected), HKCU Run key, WebView2 offline bootstrapper bundled, `onlyifdoesntexist` on config.ini for upgrades, uninstaller with `schtasks /Delete`.
**Avoids:** Mapped-drive SYSTEM blindness (UNC enforcement + fallback); WebView2-missing install failure (bundled bootstrapper); ACL mis-split (icacls in installer); config-destroying upgrades.

### Phase 5: Telemetry + Remote Config
**Rationale:** Additive to Phase 1+4, lowest risk, highest operational value. Can develop independently once server endpoints exist. Must ship before Marco goes live so broken installs are visible.
**Delivers:** `withTelemetry(fn)` recover() wrapper posting error type + file:line + OS + version (no PII, no XML payload, no variable values) to `{webhookURL}/telemetry`; 15-min remote-config poller with HMAC-authenticated GET; atomic config.ini patch on change (whitelisted fields only: webhook_url, log_level); server-side `/telemetry` + `/remote-config` endpoints added to `app.py`; documented WhatsApp sandbox → SMS production switch steps.
**Avoids:** Invisible broken installs; stuck configs requiring Marco re-install.

### Phase Ordering Rationale

- Cert procurement in Phase 1 because lead time blocks Phase 4 ship
- Pure-Go core in Phase 2 before any CGO work — test/debug without a Windows VM
- Tray shell in Phase 3 validates CGO cross-compile before installer touches it
- Installer last because it exposes every integration gap
- Telemetry+remote-config ride on the same signing/installer infra as Phase 4, but are parallelizable with Phase 4 if the server endpoints land early

### Research Flags

Phases likely needing deeper research during planning:
- **Phase 3:** `jchv/go-webview2` Dispatch() threading model for background-goroutine→UI updates — error-prone; review example code specifically before coding
- **Phase 4:** WebView2 offline installer bundling adds ~120 MB — POC-measure before committing to Inno Setup structure; Fixed Version Runtime is alternative
- **Phase 5:** `/remote-config` JSON schema + server-side storage not yet specified — must be designed before implementation

Phases with standard patterns (skip research-phase):
- **Phase 1:** Cross-compile + osslsigncode well documented
- **Phase 2:** Direct port from working Python reference with clear specs (settle, dedup, HMAC, retry, WAL)

## Confidence Assessment

| Area | Confidence | Notes |
|------|------------|-------|
| Stack | HIGH | All versions verified against pkg.go.dev; alternatives dismissed with rationale |
| Features | HIGH | Category well-documented via Backblaze/Dropbox/1Password help pages; anchored to Marco's pain |
| Architecture | MEDIUM-HIGH | Core patterns HIGH; remote-config + telemetry endpoint schemas not finalized |
| Pitfalls | HIGH | 15 pitfalls with official-source citations; 5 design-in-Phase-1 constraints are unambiguous |

**Overall confidence:** MEDIUM-HIGH

### Gaps to Address

- **CCC ONE default-path auto-detection list** — verify against Marco's actual machine or CCC ONE install docs before Phase 4
- **Wails vs raw `go-webview2`** — commit before Phase 3 plan (recommendation: raw)
- **WebView2 bootstrapper strategy** — Evergreen offline bundle vs Fixed Version; measure installer size in a Phase 3/4 spike
- **`/telemetry` + `/remote-config` endpoint schemas** — design during Phase 5 plan; requires coordinated server-side change to `app.py`
- **Twilio sandbox → production SMS switch** — server-side change that must complete before Marco ships; roadmap gate needed
- **Server-side HMAC timestamp replay protection** — currently absent; coordinated client+server change (Phase 5 or later)

## Sources

### Primary (HIGH confidence)
- pkg.go.dev — fyne.io/systray, modernc.org/sqlite, jchv/go-webview2, go-retryablehttp, lumberjack, ini.v1 (current versions and API)
- Microsoft Learn — WebView2 distribution, SmartScreen policy, Task Scheduler, icacls
- SQLite official docs — WAL mode, busy_timeout semantics
- CA/B Forum baseline (2023) — HSM key-storage mandate for code signing
- Backblaze / Dropbox / 1Password / x360Recover — public help docs for category UX conventions

### Secondary (MEDIUM confidence)
- DigiCert, SSL.com, Certera — OV cert pricing and HSM options
- GitHub issues (microsoft/go, wailsapp/wails) — SmartScreen ML heuristics on Go binaries
- capnspacehook/taskmaster — Task Scheduler COM API (commit pin recommended)
- amake/innosetup-docker — Linux CI pattern for Inno Setup

### Tertiary (LOW confidence)
- Tray icon overflow behavior on Windows 11 — community threads, no single authoritative doc
- WebView2 offline bootstrapper final installer size — needs POC measurement

---
*Research completed: 2026-04-20*
*Ready for roadmap: yes*
