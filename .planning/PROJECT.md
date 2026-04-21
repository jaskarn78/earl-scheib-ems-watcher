# Earl Scheib EMS Watcher — Windows Client

## What This Is

A production-ready Windows desktop application for Earl Scheib Auto Body Concord that watches the CCC ONE EMS export folder, deduplicates and POSTs BMS XML to an existing webhook, runs every 5 minutes as a Scheduled Task, and exposes an on-demand local-browser admin UI for reviewing and cancelling queued SMS messages. The end user is Marco, a non-technical auto body shop owner — the app is a product, not a developer tool: no terminal, no manual Python install, no ongoing config editing.

## Core Value

**Marco installs one file, steps through a native install wizard (folder pick + connection test + CCC ONE config), and forever after follow-up texts and review requests go out automatically. When he wants to inspect or cancel a queued message, `earlscheib.exe --admin` opens a browser for that one task.**

If everything else slips, this must hold: a signed installer that configures CCC ONE integration, starts the scheduled watcher, and hides the plumbing.

## Current State

**Shipped: v1.0 Windows Client MVP (2026-04-21).** 5 phases, 19 plans, 41/41 active requirements satisfied. See `.planning/milestones/v1.0-ROADMAP.md` for the archive and `.planning/milestones/v1.0-MILESTONE-AUDIT.md` for the ship audit (status: tech_debt — 11 non-blocking items, accepted).

Shipped surface:
- `earlscheib.exe --scan` — pure-Go scanner (dedup, settle, HMAC, retry, heartbeat) running every 5 min via Scheduled Task
- `earlscheib.exe --test` / `--status` — operator diagnostics
- `earlscheib.exe --admin` — local-browser "Concord Garage" queue UI (view pending SMS, cancel with 5s optimistic undo)
- `earlscheib.exe --install` / `--uninstall` / `--configure` — installer lifecycle entry points
- Inno Setup single-exe installer with native folder-picker wizard, ACL hardening, Scheduled Task registration
- Server-side: telemetry (`/telemetry`), remote-config (`/remote-config`), queue admin (`/queue` GET+DELETE) on `app.py`
- CI cross-compile + osslsigncode signing pipeline (OV cert slot wired, procurement pending external)

**Scope cuts during execution** (documented in v1.0-MILESTONE-AUDIT): tray + WebView2 wizard + 15-min background remote-config poller were cut. Installer wizard + per-scan remote-config fetch replaced them. The admin UI is a clean separation: opt-in, on-demand, not a persistent tray.

## Requirements

### Validated

<!-- Shipped in v1.0 and code-verified. -->

- ✓ Webhook server receives BMS XML and schedules Twilio messages — existing (`app.py`)
- ✓ Smart scheduling (business hours, PT timezone, weekday-only, 10–12 / 2–4 windows) — existing
- ✓ Heartbeat + status endpoints — existing + extended in v1.0
- ✓ Core scanner: SHA256 dedup, settle check, 3-attempt exponential backoff, HMAC-SHA256 signing — **v1.0 (pure-Go port)**
- ✓ Single-file Windows installer (.exe), no pre-installed runtime — **v1.0 (Inno Setup 6)**
- ✓ Native install wizard: folder auto-detect → connection test → CCC ONE config — **v1.0**
- ✓ Background watcher every 5 min via Scheduled Task (SYSTEM, user-fallback on mapped drives) — **v1.0**
- ✓ Pure-Go port of Python watcher logic, CGO_ENABLED=0 throughout — **v1.0**
- ✓ Crash telemetry (`telemetry.Wrap` + `/telemetry` endpoint) — **v1.0**
- ✓ Remote config override (whitelisted `webhook_url`, `log_level`, per-scan fetch) — **v1.0**
- ✓ Uninstaller included (Add/Remove Programs) — **v1.0**
- ✓ Queue admin UI via `--admin` — read-only list + cancel with 5s undo, Concord Garage aesthetic — **v1.0**

### Active (Next Milestone — Unplanned)

<!-- Known tech debt from v1.0-MILESTONE-AUDIT. Scope during /gsd:new-milestone. -->

- [ ] Full Windows VM UAT close-out: installer E2E, mapped-drive fallback, upgrade config preservation, uninstall cleanup, Docker iscc syntax check
- [ ] OV code-signing certificate procurement (2–10 day external lead) + CI wire-up via `SIGNING_CERT_B64`
- [ ] Real-browser UAT for admin UI: Fraunces rendering, grain overlay, undo countdown ring, rundll32 browser open on Windows, tab-close shutdown
- [ ] Real screenshot for CCC ONE EMS Extract Preferences dialog (replace v1.0 schematic SVG)
- [ ] Server-side: `telemetry.log` rotation, HMAC timestamp replay protection (ARCHITECTURE.md v2 note)
- [ ] Twilio WhatsApp sandbox → production SMS switch (comment block documented in `app.py`; ops task)
- [ ] Tray-form remote-config poller (OPS-03 literal text) — only if per-scan latency proves insufficient in practice

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

- **Tech stack**: Go (pure stdlib + go:embed) — compiled single-exe, zero non-stdlib runtime deps, admin UI served via embedded HTML/CSS/JS in the browser. *Why: zero-dependency install, Linux cross-compile friendly, 16 MB-class binary, browser beats WebView2 for on-demand use.*
- **Runtime environment**: No Python on Marco's machine. No Node. Everything bundled. *Why: Marco's machine stays clean; installer is the only user-facing artifact.*
- **Paths**: Program data lives at `C:\EarlScheibWatcher\` (config.ini, ems_watcher.db, log file). *Why: consistent with existing deployment.*
- **Background execution**: Windows Scheduled Task every 5 min (SYSTEM, user-fallback on mapped drives). *Why: matches current proven model; services are overkill for this cadence.*
- **Compatibility**: Windows 10 / 11 (any modern browser for `--admin`). *Why: WebView2 dropped in favor of system-browser; broadest compat.*
- **Security**: Secret key pre-baked into binary via `-ldflags "-X main.secretKey=..."` (never in config). HMAC-SHA256 over raw body; admin UI never sees the secret (Go proxy signs). *Why: Marco cannot accidentally break auth.*
- **Network**: Must tolerate flaky shop WiFi — exponential backoff, retry 3x, settle check prevents partial-read POSTs. *Why: auto body shop, not a datacenter.*
- **Dev env**: Linux primary, Windows VM for integration testing, GitHub Actions for release builds. *Why: developer's setup; Go cross-compile makes this cheap.*

## Key Decisions

| Decision | Rationale | Outcome |
|----------|-----------|---------|
| Go over Rust / Python+PyInstaller | Single static exe, mature Windows tooling, Linux cross-compile, 10MB-class binary | ✓ Good — ~16 MB signed exe, no runtime deps |
| WebView2 wizard → dropped; native Inno Setup wizard at install time + on-demand browser for `--admin` | WebView2 overhead didn't pay off for a one-time setup flow; browser-as-UI is simpler for on-demand inspection | ✓ Good — v1.0 scope cut, shipped on time |
| System tray → dropped | Marco doesn't need a persistent status surface; log file on disk + `--status` + `--admin` cover it | ✓ Good — no support calls about tray since ship |
| Port Python watcher logic instead of bundling Python runtime | Eliminates 30–50MB PyInstaller payload, removes AV false-positive risk | ✓ Good — byte-identical behavior, unit-tested in CI |
| Keep Scheduled Task model (vs Windows Service) | Proven, simpler install, survives Windows Update cycles | ✓ Good |
| Crash telemetry → `telemetry.Wrap` + `/telemetry` | Without it, broken installs are invisible until Marco calls | ✓ Good |
| Remote config override (per-scan fetch, not tray poller) | Webhook URL rotation without re-running installer; tray was cut so 5-min Scheduled Task provides the cadence | ✓ Good — simpler than planned, same practical effect |
| Linux dev + Windows VM test (dev workflow) | Developer's environment; Go cross-compile makes this cheap | ✓ Good |
| Phase 5: queue admin UI via `earlscheib.exe --admin` (local browser SPA) | Marco needs visibility + cancel for queued SMS; building a tray was rejected but a one-shot browser UI is low-overhead and doesn't reintroduce an always-on component | ✓ Good — Concord Garage aesthetic shipped, 5s undo cancel, HMAC proxy to existing server |

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
*Last updated: 2026-04-21 after v1.0 Windows Client MVP milestone*
