# Earl Scheib EMS Watcher — Windows Client

## What This Is

A polished, production-ready Windows desktop application for Earl Scheib Auto Body Concord that watches the CCC ONE EMS export folder, deduplicates and POSTs BMS XML to an existing webhook, and runs quietly in the system tray. The end user is Marco, a non-technical auto body shop owner — the app is a product, not a developer tool: no terminal, no config files, no Python required.

## Core Value

**Marco downloads one file, clicks through a 3-step wizard, and the tray icon turns green — forever after, follow-up texts and review requests go out automatically with zero ongoing attention.**

If everything else slips, this must hold: a single installer that configures CCC ONE integration, starts the watcher, and hides the plumbing.

## Requirements

### Validated

<!-- Shipped and confirmed valuable. Server-side components already running in production. -->

- ✓ Webhook server receives BMS XML and schedules Twilio messages — existing (`app.py` on Linux VM)
- ✓ Smart scheduling (business hours, PT timezone, weekday-only, 10–12 / 2–4 windows) — existing
- ✓ Heartbeat + status endpoints (`/earlscheibconcord/heartbeat`, `/earlscheibconcord/status`) — existing
- ✓ Python file watcher core logic (SHA256 dedup, settle check, retry w/ backoff, HMAC-SHA256 signing) — existing (`ems_watcher.py`)

### Active

<!-- Current scope. All are hypotheses until shipped and validated with Marco. -->

- [ ] Single-file Windows installer (.exe) that requires no pre-installed runtime
- [ ] System tray app with colored status icon (green/yellow/red) and context menu
- [ ] First-run 3-step setup wizard: folder detection → connection test → CCC ONE guided config
- [ ] Status window showing last run, files processed today/total, recent activity, "Run Now"
- [ ] Background file watcher runs every 5 minutes via Scheduled Task (or always-on service)
- [ ] Port existing Python logic (dedup, settle, retry, heartbeat, HMAC signing) to Go
- [ ] Auto-scan common CCC ONE install paths; folder picker fallback
- [ ] Connection test hits `/earlscheibconcord/status` before wizard advances
- [ ] Guided CCC ONE EMS Extract Preferences instructions (diagram + confirmation)
- [ ] Optional 2-minute live watch during wizard to verify end-to-end
- [ ] Crash telemetry — unhandled errors phone home so broken installs are visible
- [ ] Remote config override — ability to update webhook URL / secret without re-running installer
- [ ] Uninstaller included

### Out of Scope

- **Messaging logic in the Windows client** — server handles Twilio. Client only POSTs BMS XML. *Why: separation of concerns; swapping WhatsApp sandbox → real SMS happens server-side only.*
- **macOS or Linux client builds** — Marco runs Windows; CCC ONE is Windows-only. *Why: no user need.*
- **Web-based admin UI for settings / configuration** — settings changes still happen via `--configure` or installer re-run. *Why: no user-editable config surface Marco can break.* (Phase 5 added a **read-only queue admin UI** via `earlscheib.exe --admin` — an opt-in local-browser SPA with a single cancel action — see ADMIN-01..11. This is in scope and distinct from a settings UI.)
- **Multi-shop / multi-tenant support** — one-off deployment for Earl Scheib Concord. *Why: single customer today; revisit if others want it.*
- **Customer-facing features (reply handling, opt-outs beyond STOP)** — handled server-side via Twilio's built-in STOP keyword. *Why: server concern.*
- **Rewriting the webhook server** — already in production, reliable. *Why: focus on the client.*

## Context

**Problem driving this:** Current watcher works but feels like a dev tool — Marco has to edit `config.ini` by hand to set the CCC ONE export folder path, `check_status.bat` opens a raw cmd window, `install.bat` requires admin + manual Python install on machines that don't have it. Marco is non-technical; every friction point is a support call.

**Existing assets (reference material for the port, not code to extend):**
- `claude-code-project/ems_watcher.py` — battle-tested watcher logic (dedup, settle, retry, heartbeat)
- `claude-code-project/config.ini` — current config shape
- `claude-code-project/install.bat` / `README.txt` — current install flow + setup instructions (becomes in-app wizard)
- `app.py` — server webhook (read-only reference; document the WhatsApp → SMS switch)

**Technical environment:**
- Target: Windows 10/11 (Marco's shop PC)
- Webhook already live at `https://support.jjagpal.me/earlscheibconcord`
- Auth: HMAC-SHA256 over raw body, `X-EMS-Signature` header
- BMS namespace: `http://www.cieca.com/BMS`, root element `VehicleDamageEstimateAddRq`
- Twilio currently WhatsApp sandbox (`whatsapp:+14155238886`); production swap documented but not yet done

**Dev/test environment:** Cross-compile from Linux to Windows; verify in a Windows VM. CI can produce signed (or at least reproducible) Windows builds.

**User research:** Marco has been running the current watcher manually. Pain points: having to find the CCC ONE export path, not knowing if the thing is working, occasional Scheduled Task failures that are invisible until a customer doesn't get a text.

## Constraints

- **Tech stack**: Go + WebView2 (wry/webview) — compiled single-exe, minimal deps, reuse system WebView2 for UI. *Why: zero-dependency install, modern UI flexibility, Linux cross-compile friendly, small binary.*
- **Runtime environment**: No Python on Marco's machine. No Node. Everything bundled. *Why: Marco's machine stays clean; installer is the only user-facing artifact.*
- **Paths**: Program data lives at `C:\EarlScheibWatcher\` (config.ini, ems_watcher.db, log file). *Why: consistent with existing deployment; Marco already knows this path.*
- **Background execution**: Windows Scheduled Task every 5 min, plus startup app for tray. *Why: matches current proven model; avoids service-installation complexity.*
- **Compatibility**: Windows 10 1809+ (WebView2 availability). *Why: WebView2 requirement.*
- **Security**: Secret key pre-baked into binary (not user-facing) — same key as current watcher. HMAC-SHA256 over raw XML body. No plaintext secret storage in config Marco can see. *Why: Marco must not be able to break auth by editing config.*
- **Network**: Must tolerate flaky shop WiFi — exponential backoff, retry 3x, settle check prevents partial-read POSTs. *Why: auto body shop, not a datacenter.*
- **Dev env**: Linux primary, Windows VM for integration testing, CI for release builds. *Why: developer's setup.*

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Go over Rust / Python+PyInstaller | Single static exe, mature Windows tooling, Linux cross-compile, strong systray/webview libs, 10MB-class binary | — Pending |
| WebView2 for wizard/status UI | Max design flexibility (HTML/CSS), WebView2 pre-installed on modern Windows, keeps binary small vs Fyne | — Pending |
| Port Python watcher logic instead of bundling Python runtime | Eliminates 30–50MB PyInstaller payload, removes antivirus false-positive risk, faster startup | — Pending |
| Keep Scheduled Task model (vs Windows Service) | Proven in current deployment; services require more install ceremony and can be killed by Windows Update cycles | — Pending |
| Add crash telemetry (beyond PRD) | Without it, broken installs are invisible until Marco calls — delaying fixes | — Pending |
| Add remote config override (beyond PRD) | Webhook URL / secret rotation without re-running installer; critical for production ops | — Pending |
| Linux dev + Windows VM test (dev workflow) | Developer's environment; Go cross-compile makes this cheap | — Pending |
| Phase 5: scope_reversal — read-only queue admin UI in scope | A **read-only queue admin UI** with a single cancel action is now in scope. Settings/config UI remains out of scope. Implemented via `earlscheib.exe --admin` → local browser SPA + existing HMAC auth. See ADMIN-01..11. | — Completed Phase 5 |

## Evolution

This document evolves at phase transitions and milestone boundaries.

**After each phase transition** (via `/gsd:transition`):
1. Requirements invalidated? → Move to Out of Scope with reason
2. Requirements validated? → Move to Validated with phase reference
3. New requirements emerged? → Add to Active
4. Decisions to log? → Add to Key Decisions
5. "What This Is" still accurate? → Update if drifted

**After each milestone** (via `/gsd:complete-milestone`):
1. Full review of all sections
2. Core Value check — still the right priority?
3. Audit Out of Scope — reasons still valid?
4. Update Context with current state

---
*Last updated: 2026-04-20 after initialization*
