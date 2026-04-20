# Feature Research

**Domain:** Windows system tray background utility — file-watcher / webhook poster for non-technical small-business users
**Product:** Earl Scheib EMS Watcher (CCC ONE → webhook integration)
**Reference products studied:** Dropbox desktop, Backblaze, 1Password mini, SyncTrayzor, x360Recover Systray Monitor, Citrix Workspace
**Researched:** 2026-04-20
**Confidence:** MEDIUM-HIGH (tray-utility category well-documented; specifics verified against multiple product help pages and Microsoft guidelines)

---

## Feature Landscape

### Table Stakes (Users Expect These)

Features users assume exist. Missing these = product feels incomplete or broken. Non-technical users will call for support rather than diagnose the absence.

| Feature | Why Expected | Complexity | Notes |
|---------|--------------|------------|-------|
| **Colored status tray icon (green / yellow / red)** | Every background utility in this category (Backblaze, Dropbox, Syncthing, x360Recover) uses traffic-light color coding. Users don't know how to open log files; the icon IS the status. Missing = Marco has no idea if it's working. | S | Green = healthy & watching, Yellow = retrying / degraded, Red = error / auth failure / folder missing. Icon must update without requiring status window to be open. |
| **Right-click context menu (4–6 items max)** | Established Windows tray contract since Windows XP. Users right-click expecting a menu. Dropbox, Backblaze, 1Password all follow this pattern identically. | S | Standard minimal menu: "Open Status Window", "Run Now", "Settings" (disabled post-wizard), "About / Version", "Quit". Order matters — most-used first, destructive (Quit) last with separator. |
| **Single click opens status window** | Dropbox and most tray utilities open their mini-panel on left-click. Users expect left-click = "show me what's happening." | S | If left-click is not wired, users will report "the app doesn't open." |
| **Status window: last-run time + outcome** | Backblaze shows "last backup timestamp." x360Recover shows "last attempted and last successful timestamp." Users need to confirm it ran recently — this is the number-one anxiety for background utilities. | S | Show: "Last checked: [time]", "Last successful send: [time]", "Files sent today: N", "Files sent total: N". Human-readable timestamps ("2 minutes ago"), not ISO strings. |
| **"Run Now" button** | Background utilities running on a schedule create user anxiety ("has it run since I added that estimate?"). Backblaze's "Backup Now" button is the canonical example — it's in the control panel, and users discover it immediately. | S | Must be in the status window. Triggers immediate watcher cycle. Shows spinner during run. Disables during run to prevent double-execution. |
| **First-run setup wizard (3-step max)** | Users who are handed an installer expect a guided, clickable setup, not a config file. The current pain point for Marco is having to edit config.ini by hand. Every polished Windows utility (Backblaze, Citrix Workspace, 1Password) has a first-run guided flow. | M | Step 1: Folder detection + picker fallback. Step 2: Connection test with live pass/fail indicator. Step 3: CCC ONE EMS Extract Preferences guide with confirmation. Wizard advances on explicit user confirmation, not automatically. |
| **Connection test with clear pass/fail** | Users need to know "is this talking to the server?" before completing setup. Azure Arc Setup wizard, BlueConic, and every enterprise onboarding wizard include this step. Green checkmark = pass, red X + plain-English reason = fail. | S | Hit `/earlscheibconcord/status` endpoint. Show "Connected" (green check) or "Cannot reach server — check your internet connection" (red X). Must not advance wizard until green. |
| **Single-instance enforcement** | If user double-clicks the installer shortcut with the app already running, launching a second instance creates duplicate tray icons, doubled Scheduled Tasks, and corrupted state. Users won't know why they see two icons. | S | Named mutex check on startup. If already running, bring the existing status window to foreground (via named pipe signal). Do not show error dialog — silently surface the existing instance. |
| **Autostart on Windows login** | Background utilities must survive reboots without user action. Dropbox, Backblaze, and every comparable tool add themselves to autostart. Marco cannot be expected to re-launch manually after a power outage or Windows Update reboot. | S | Use `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` (user-level, no admin required for the autostart entry itself). Installer sets this; uninstaller removes it. |
| **Uninstaller via Windows Add/Remove Programs** | Windows users expect every app to appear in "Apps & features" and be uninstallable from there. Missing this = app feels malicious or broken; Marco's IT person will complain. | S | Inno Setup / NSIS registers uninstaller automatically. Must remove: Scheduled Task, autostart registry entry, tray process, optionally data directory (with confirmation prompt). |
| **Toast notification on error** | Backblaze and SyncTrayzor notify users when something goes wrong (disconnected, folder missing). Non-technical users watching no UI need a push signal when the green turns red. | S | Notify on: repeated failure after retries exhausted (3x backoff), missing watch folder, auth error. Do NOT notify on every successful send — that's notification spam. |
| **Installer as a single .exe** | Non-technical users expect one file to download and run, like Dropbox or Zoom. No "first install Python" or "run this script as admin." | M | Go + Inno Setup produces a self-contained .exe. No external runtime required. This is already in PRD — confirming it's truly table stakes, not a nice-to-have. |

---

### Differentiators (Competitive Advantage / Polish)

Features that set this product apart. Not universally expected, but elevate Marco's trust and the perceived quality of the integration.

| Feature | Value Proposition | Complexity | Notes |
|---------|-------------------|------------|-------|
| **Live "end-to-end" verification step in wizard** | Most setup wizards test "can we reach the server?" but not "did a file actually flow through?" A 2-minute live watch during wizard that confirms the first BMS XML POSTed successfully is rare in this category and directly addresses Marco's "is this actually working?" anxiety. | M | Optional final wizard step. Wizard polls for a new file in the watch folder, POSTs it, shows "Sent 1 file successfully." Timeout of 2 min with skip option. Eliminates the "I set it up but I'm not sure it's really working" call. |
| **Activity feed (human-readable, not raw log)** | x360Recover's systray monitor is "purely informational with no action capability." SyncTrayzor shows a browser-embedded status page. Neither provides a simple human-readable feed like "2:14 PM — Sent estimate #3847 for John Smith." Non-technical users cannot parse raw log lines. | M | Status window bottom section: last 10 events as plain English one-liners with timestamps. "3 hours ago — Sent estimate for Honda Civic (repair: $2,840)". This doubles as Marco's peace-of-mind view and his troubleshooting tool. XML parsing already exists in watcher; extract RO name + estimate amount. |
| **Remote config override (no reinstall needed)** | When webhook URL or HMAC secret changes, every comparable tool requires reinstallation or manual config editing. Remote config lets the developer push a config update to Marco's machine without a support call. | M | App polls a known config URL on startup or every 24h. If a new config is detected, it silently applies it. No UI required for Marco. Critical for production ops (Twilio number swap, URL change). |
| **Crash telemetry (phone-home on unhandled error)** | Most small-business utilities at this scale have no crash visibility — you find out when the customer calls. Phone-home telemetry means broken installs are visible to the developer before Marco notices. | M | Unhandled panic/error sends a POST to the webhook server's telemetry endpoint (minimal payload: error message, stack trace, app version, timestamp). No PII. Opt-in checkbox during install with plain-English explanation: "Send error reports to help us fix bugs faster." |
| **Auto-folder detection for CCC ONE paths** | Manual folder entry is the current pain point. Auto-scanning common CCC ONE install paths (`C:\CCC ONE\EMS\`, `C:\Program Files\CCC ONE\`) before falling back to a picker reduces wizard friction to near-zero for the typical case. | S | Scan known paths on wizard open. If found, pre-fill and show "We found your CCC ONE folder at [path] — does this look right?" One-click confirm or manual override. |
| **Status window shows next-scheduled-run countdown** | Users running on a Scheduled Task ("every 5 minutes") have no idea when the next run is. Showing "Next check in: 3 min 22 sec" eliminates the "is it frozen?" question. | S | Read the Scheduled Task's last-run + interval from Windows Task Scheduler API, compute next run, display as countdown. Requires Scheduled Task to be created with a deterministic trigger. |
| **Guided CCC ONE EMS Export Preferences setup** | No comparable tray utility handles this. The current README.txt instruction is a friction point. In-app diagram (screenshot or annotated image) showing exactly where to click in CCC ONE, with a checkbox confirmation ("I've configured EMS exports"), reduces the most common setup failure mode. | M | Step 3 of wizard. Embed screenshot of CCC ONE EMS Preferences dialog with numbered callouts. This is pure documentation turned into UI — high value, medium effort. |

---

### Anti-Features (Deliberately NOT Build)

Features that seem logical but add complexity, erode user trust, or violate the product's scope. At least three of these are things that "obviously good ideas" turn into real problems.

| Feature | Why Requested | Why Problematic | Alternative |
|---------|---------------|-----------------|-------------|
| **In-app "Settings" screen (user-editable config)** | Developers instinctively build settings panels. Users expect configuration to be discoverable. | Marco editing webhook URL or HMAC secret = support call when he breaks auth. The PRD explicitly says "No plaintext secret storage in config Marco can see." A settings screen invites him to experiment. There is nothing in this app that Marco legitimately needs to configure after initial setup. | Embed all config at build time. Remote config override (see differentiators) handles anything that needs changing. If folder path is wrong, provide a single "Change folder" button in the context menu — scoped, not a general settings panel. |
| **Raw log file viewer in the status window** | Developers want to expose the log for debugging; power users request it. | Raw log lines (`2026-04-20T14:32:11Z [INFO] SHA256 deduplicated file abc123...`) are meaningless to Marco and will cause confusion or worry. If he sees an ERROR line from a transient retry, he'll call thinking the app is broken. | Expose the human-readable activity feed (differentiator above). Keep raw log in `C:\EarlScheibWatcher\watcher.log` for developer use, accessible only by navigating there manually — not surfaced in the UI. |
| **"Pause watching" toggle in the tray menu** | Power users want control. Makes the app feel more configurable. | A non-technical user who pauses watching and forgets will stop getting follow-up texts. The next support call is "why aren't my customers getting texts?" with no obvious cause. There is no legitimate reason for Marco to pause watching. | Omit entirely. If Marco needs to "stop the app" he can use Windows Task Manager or the Quit menu item. |
| **Update nag toasts / "A new version is available" dialog** | Auto-update is table stakes; notifying users about updates is the natural next step. | Marco doesn't know what a version is. "Version 1.2.3 is available" triggers anxiety, not action. If he clicks "Update Now" and a UAC prompt appears, he'll cancel. If the update silently restarts the tray, he may think the app crashed. Windows users exposed to aggressive update prompts (Windows itself, Chrome) are update-fatigued. | Silent auto-update: download in background, swap binary on next scheduled task run or next login. No UI unless the update fails. Only surface "App was updated to v1.2.3" as a subtle single line in the activity feed — not a toast. |
| **Detailed backup/restore of app config** | IT-oriented utilities often expose config export/import for migration. | This is a single-shop, single-machine deployment. Config export adds UI complexity and a support surface ("I imported the wrong config") with no real user need today. | If migration is ever needed (new PC), the developer handles it. The remote config override means the developer can push fresh config to a new install. |
| **In-app upsell or "upgrade" prompts** | Hypothetically, if the tool were commercialized. | Destroys trust with a non-technical user who doesn't understand why his tool is asking for money. Violates the product's premise as an invisible background utility. | This is a bespoke single-customer deployment. Never. |
| **Customer-facing features (message status, opt-out management)** | Seems logical since the watcher triggers the SMS flow. | The PRD explicitly out-of-scopes this (server-side Twilio STOP handling). Bringing it into the client would require the client to talk to Twilio directly, adding auth complexity and a new attack surface. | Leave it server-side. If Marco needs to see message delivery status, that's a future server-side dashboard, not a tray icon feature. |

---

## Feature Dependencies

```
Tray icon (colored states)
    └──requires──> Background watcher process (runs, reports status to tray)
                       └──requires──> Config (folder path, webhook URL, HMAC secret)
                                          └──requires──> First-run wizard (collects / validates config)
                                                             └──requires──> Connection test (validates server reachable)

Status window
    └──requires──> Tray icon (entry point)
    └──requires──> Background watcher process (data source)

"Run Now" button
    └──requires──> Status window (lives inside it)
    └──requires──> Single-instance enforcement (prevents double-execution)
    └──conflicts──> "Pause watching" toggle (anti-feature — deliberately omitted)

Toast notifications (error)
    └──requires──> Background watcher process (generates events)
    └──enhances──> Tray icon (supplements visual status with push signal)

Activity feed (human-readable)
    └──requires──> Status window (display surface)
    └──requires──> Background watcher (event source)
    └──enhances──> "Run Now" button (shows result of run immediately)

Auto-update (silent)
    └──requires──> Crash telemetry (feeds update priority signal)
    └──conflicts──> "Update nag toasts" (anti-feature — omitted)

Remote config override
    └──requires──> Watcher process startup logic (polls config URL)
    └──enhances──> Crash telemetry (server knows which version is running)

First-run wizard
    └──requires──> Auto-folder detection (step 1 UX)
    └──requires──> Connection test (step 2)
    └──requires──> CCC ONE guided instructions (step 3)
    └──enhances──> Live end-to-end verification (optional step 4)

Uninstaller
    └──requires──> Installer (Inno Setup registers it)
    └──requires──> Autostart entry removal
    └──requires──> Scheduled Task removal
```

### Dependency Notes

- **Config must exist before watcher can run:** The wizard is the only path to creating valid config. The installer must block the watcher from running until wizard is completed (write a sentinel flag to config on wizard completion).
- **Single-instance enforcement is a prerequisite for "Run Now":** Without it, clicking "Run Now" while a background watcher cycle is mid-execution causes duplicate POST attempts and SHA256 dedup race conditions.
- **Live end-to-end verification (differentiator) depends on a real CCC ONE export being present:** It cannot be faked. If no export file exists when the wizard reaches step 4, the step must be skippable with a note: "We'll send your first estimate automatically when CCC ONE exports one."
- **Activity feed depends on the watcher parsing enough XML to extract human-readable fields:** Watcher must extract at minimum: RO name (or estimate ID) and dollar amount from BMS XML. This is additional work beyond the basic "POST the file" loop.

---

## MVP Definition

### Launch With (v1)

Minimum viable product that solves Marco's stated pain points: zero-terminal setup, visible confidence the watcher is running, no broken installs that are invisible.

- [ ] Single .exe installer — no pre-requisites
- [ ] First-run 3-step wizard (folder detection + connection test + CCC ONE guide)
- [ ] Background watcher (Go port of existing Python logic: dedup, settle, retry, heartbeat, HMAC)
- [ ] Tray icon with green / yellow / red states
- [ ] Right-click context menu (5 items: Open, Run Now via status, Settings [disabled], About, Quit)
- [ ] Status window: last run time, outcome, files sent today/total, "Run Now" button
- [ ] Toast notification on repeated failure (after 3 retries exhausted)
- [ ] Single-instance enforcement (mutex + foreground existing window)
- [ ] Autostart on login (HKCU Run key)
- [ ] Crash telemetry opt-in (checkbox in installer, phone-home on unhandled panic)
- [ ] Uninstaller (Add/Remove Programs, removes Task, registry entry, tray process)

### Add After Validation (v1.x)

Add once v1 is running on Marco's machine and confirmed working:

- [ ] Activity feed (human-readable last 10 events) — add when Marco asks "how do I know which estimates got sent?"
- [ ] Remote config override — add before any webhook URL or secret rotation is needed in production
- [ ] Live end-to-end verification in wizard — add if first-run completion rate has issues
- [ ] Next-scheduled-run countdown in status window — add if Marco asks "when does it run next?"

### Future Consideration (v2+)

Defer until validated need:

- [ ] Auto-update mechanism — defer until multiple updates have been deployed and the manual update process becomes friction (currently a single-machine deployment)
- [ ] Auto-folder detection scanning additional CCC ONE paths — only needed if CCC ONE changes its default install path or another shop is onboarded
- [ ] Multi-shop / multi-tenant support — explicitly out of scope today; revisit only if a second customer is onboarded

---

## Feature Prioritization Matrix

| Feature | User Value | Implementation Cost | Priority |
|---------|------------|---------------------|----------|
| Colored tray icon | HIGH | LOW | P1 |
| Right-click context menu | HIGH | LOW | P1 |
| Status window (last run, files sent, Run Now) | HIGH | LOW | P1 |
| First-run 3-step wizard | HIGH | MEDIUM | P1 |
| Connection test in wizard | HIGH | LOW | P1 |
| Background watcher (Go port) | HIGH | HIGH | P1 |
| Single .exe installer | HIGH | MEDIUM | P1 |
| Autostart on login | HIGH | LOW | P1 |
| Single-instance enforcement | HIGH | LOW | P1 |
| Toast on repeated failure | MEDIUM | LOW | P1 |
| Uninstaller | HIGH | LOW | P1 |
| Crash telemetry (opt-in) | MEDIUM | MEDIUM | P1 |
| CCC ONE guided instructions in wizard | HIGH | LOW | P1 |
| Activity feed (human-readable) | HIGH | MEDIUM | P2 |
| Remote config override | MEDIUM | MEDIUM | P2 |
| Auto-folder detection | MEDIUM | LOW | P2 |
| Next-scheduled-run countdown | LOW | LOW | P2 |
| Live end-to-end verification in wizard | MEDIUM | MEDIUM | P2 |
| Auto-update (silent) | LOW | HIGH | P3 |

**Priority key:**
- P1: Must have for launch — Marco cannot use the product without this
- P2: Should have — add in v1.x once core is confirmed working
- P3: Nice to have — future consideration, low urgency for single-machine deployment

---

## Competitor Feature Analysis

| Feature | Backblaze | Dropbox | x360Recover Systray | Our Approach |
|---------|-----------|---------|---------------------|--------------|
| Status icon states | Color-coded (green/warning) | Color-coded (solid/animated) | Green / Red | Green / Yellow / Red — three states needed (yellow = retrying distinguishes from hard failure) |
| Context menu | "Control Panel", "Check Updates", "Help", "Quit" | "Open Dropbox", "Pause", "Preferences", "Quit" | None (view-only) | "Open Status Window", "Run Now", "About", "Quit" — no pause, no settings |
| Status window | Full control panel with Backup Now, restore options | Mini-panel with activity, storage | Pure read-only status (no actions) | Hybrid: Backblaze-style "Run Now" button + x360Recover-style informational display |
| Log / activity | Backup report in control panel | Activity feed (synced files) | None user-facing | Human-readable activity feed (10 events) — NOT raw log |
| First-run wizard | Setup wizard (account + folder) | Folder + account wizard | None (agent is installed centrally) | 3-step: folder + connection test + CCC ONE config guide |
| Auto-update | Silent background update | Silent background update | Managed centrally | Silent for v2+; manual for v1 (single machine, low update frequency) |
| Telemetry | Yes (opt-in) | Yes (usage data) | Managed by MSP | Crash telemetry only, opt-in, no usage analytics |
| Single instance | Yes | Yes | N/A (service model) | Yes — named mutex + foreground existing window |

---

## Sources

- [Microsoft: Notifications design basics (Toast UX Guidance)](https://learn.microsoft.com/en-us/windows/apps/develop/notifications/app-notifications/toast-ux-guidance) — MEDIUM confidence (official, current 2025 doc)
- [Backblaze: Where is the Backblaze menu?](https://help.backblaze.com/hc/en-us/articles/217664958) — HIGH confidence (official product help)
- [Backblaze: Settings Overview (Win)](https://help.backblaze.com/hc/en-us/articles/217666508) — HIGH confidence
- [Dropbox: Find the Dropbox icon in your taskbar](https://help.dropbox.com/installs/dropbox-icon-in-taskbar) — HIGH confidence (official product help)
- [1Password: Get to know Quick Access](https://support.1password.com/quick-access/) — HIGH confidence (official)
- [x360Recover: Windows Systray Monitor Utility](https://help.axcient.com/install-an-agent/windows-systray-monitor-utility-x360recove) — HIGH confidence (official product docs, fetched directly)
- [SyncTrayzor: GitHub Repository](https://github.com/canton7/SyncTrayzor) — MEDIUM confidence (open source, archived but representative of category)
- [Microsoft: App instancing with the app lifecycle API (WinUI)](https://learn.microsoft.com/en-us/windows/apps/windows-app-sdk/applifecycle/applifecycle-instancing) — HIGH confidence (official)
- [Microsoft: Run and RunOnce Registry Keys](https://learn.microsoft.com/en-us/windows/win32/setupapi/run-and-runonce-registry-keys) — HIGH confidence (official)
- [Microsoft: Menus and context menus](https://learn.microsoft.com/en-us/windows/apps/develop/ui/controls/menus-and-context-menus) — HIGH confidence (official)
- [Inno Setup: Run at Startup Knowledge Base](https://jrsoftware.org/iskb.php?startwithwindows=) — MEDIUM confidence (official installer framework docs)
- [Wizard UX Pattern (ui-patterns.com)](https://ui-patterns.com/patterns/Wizard) — MEDIUM confidence (established UX pattern library)

---

*Feature research for: Windows system tray background utility (file-watcher / webhook poster, non-technical end user)*
*Researched: 2026-04-20*
