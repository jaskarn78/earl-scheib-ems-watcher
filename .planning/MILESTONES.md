# Milestones

## v1.0 Windows Client MVP (Shipped: 2026-04-21)

**Phases completed:** 5 phases, 19 plans, 25 tasks
**Requirements:** 41/41 active satisfied
**Audit status:** tech_debt — 11 non-blocking items (HUMAN-UAT + OV cert procurement), user-accepted

### Delivered

A production Go + Inno Setup Windows client that watches a CCC ONE EMS export folder, POSTs BMS XML to an HMAC-authenticated webhook every 5 minutes, captures unhandled panics, and exposes an on-demand local-browser queue admin UI for cancelling pending SMS messages. Zero non-stdlib Go dependencies; CGO_ENABLED=0 cross-compile from Linux CI; osslsigncode signing pipeline ready for OV cert.

### Key Accomplishments

1. **Phase 1 — Scaffold + Signing:** Go module with 6-subcommand dispatcher, CGO-free cross-compile on GitHub Actions (ubuntu-latest), go-winres resource embed (ProductName, UAC manifest, Win10 min-OS), conditional osslsigncode signing with RFC 3161 timestamp, dev-sign Makefile target using openssl self-signed cert.
2. **Phase 2 — Core Scanner:** Pure-Go port of the Python reference watcher — SHA256 dedup via `(filepath, mtime)` INSERT OR IGNORE, settle check, HMAC-SHA256 signing (byte-identical to Python), 3-attempt exponential backoff, heartbeat, slog+lumberjack rotating log. Full httptest + pinned parity fixtures, no Windows VM required for unit tests.
3. **Phase 3 — Installer + Native Config:** Inno Setup single-exe installer with Pascal wizard pages for folder auto-detect + connection test + CCC ONE config. icacls ACL hardening (SYSTEM=Full, Users=Modify). Scheduled Task registered as SYSTEM (with user-account fallback for mapped drives). Docker iscc compilation in CI.
4. **Phase 4 — Telemetry + Remote Config:** Client-side `telemetry.Wrap` captures panics and POSTs minimal records (no PII, truncated msg). Remote-config fetch at top of `--scan` merges whitelisted keys (`webhook_url`, `log_level`) atomically. Server-side `/telemetry` (POST→JSONL) and `/remote-config` (GET, HMAC-empty-body) endpoints with `_validate_hmac` helper. Twilio WhatsApp→SMS switch comment block in `app.py`.
5. **Phase 5 — Queue Admin UI:** `earlscheib.exe --admin` binds 127.0.0.1:ephemeral, auto-opens browser via rundll32, serves embedded Concord Garage SPA (Fraunces + JetBrains Mono, oxblood/paper palette, feTurbulence grain overlay, 60ms entrance stagger, 5s optimistic-undo cancel with conic-gradient countdown ring). HMAC-signing Go proxy between browser and `/earlscheibconcord/queue` (GET + DELETE). Heartbeat watchdog shuts server down 30s after tab close. 9 Go tests + 8 pytest tests.

### Scope Adjustments During Execution

- **Tray + WebView2 wizard cut (2026-04-20):** One-time setup via native Inno Setup wizard pages delivers the same outcome without WebView2 runtime footprint. Marco configures once at install time; log file + `--status` + `--admin` cover ongoing state.
- **15-min background remote-config poller cut:** Scheduled Task already runs every 5 min; per-scan fetch delivers config updates within the same cadence without a second background process.
- **Admin UI added post-scope (Phase 5):** Shipping the scanner revealed Marco needs visibility + cancel on queued messages (customer typo, duplicate job). Cheap to build via on-demand local-browser UI without reintroducing a tray.

### Known Tech Debt (Non-Blocking, User-Accepted)

- **Phase 3:** 5 HUMAN-UAT items (full installer E2E on Win10 VM, mapped-drive warning, upgrade preservation, uninstall cleanup, iscc Docker compile); CCC ONE Extract Preferences schematic SVG vs real screenshot; SCAF-06 OV cert procurement (external 2–10 day task).
- **Phase 4:** OPS-03 tray-poller literal form deferred (per-scan form delivers equivalent behavior); server `telemetry.log` rotation; server-side HMAC timestamp replay protection (v2); app.py deploy is a manual `systemctl restart`.
- **Phase 5:** 4 HUMAN-UAT items (real-browser Fraunces rendering, undo ring drain, Windows rundll32 auto-open, tab-close shutdown); no `/queue` pagination; 15s polling (not WS/SSE).
- **One BLOCKER found during v1.0 audit and fixed inline:** admin proxy `remoteQueueURL()` produced a double-path URL (tests passed only because httptest has no base path). Fixed to match convention of telemetry/remoteconfig/heartbeat — append only the leaf path `/queue`. Tests updated to use production-shape WebhookURL. Commit `bbf1f09`.

### Archives

- `.planning/milestones/v1.0-ROADMAP.md` — full phase/plan detail
- `.planning/milestones/v1.0-REQUIREMENTS.md` — final requirement status (all 41 active ✓)
- `.planning/milestones/v1.0-MILESTONE-AUDIT.md` — full ship audit

---
