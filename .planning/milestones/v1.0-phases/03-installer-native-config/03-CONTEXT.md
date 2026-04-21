# Phase 3: Installer + Native Config - Context

**Gathered:** 2026-04-20
**Status:** Ready for planning
**Mode:** Scope-reduced — Inno Setup native pages replace WebView2 tray/wizard

<domain>
## Phase Boundary

A signed single-file `.exe` installer (Inno Setup 6, built from Linux via Docker) that — on a fresh Windows 10 VM with no runtimes pre-installed:
1. Prompts Marco for the CCC ONE export folder (auto-detected default + browse fallback + mapped-drive warning)
2. Shells out to `earlscheib.exe --test` to verify the webhook connection works with the chosen config, offers retry / continue-anyway on failure
3. Shows a static info page with the EMS Extract Preferences screenshot + "Lock Estimate" / "Save Workfile" instructions + "I've done this" checkbox
4. Extracts files to `C:\EarlScheibWatcher\`
5. Sets directory ACLs (SYSTEM=Full, Users=Modify)
6. Registers the Scheduled Task `EarlScheibEMSWatcher` every 5 minutes (SYSTEM by default, user fallback if a mapped drive is detected or SYSTEM registration fails)
7. Runs one scan at the end of install to prove the pipeline is live
8. Provides an uninstaller that removes the Scheduled Task + data dir + Add/Remove Programs entry

Delivers requirements: INST-01..04, INST-08..11, UI-06 through UI-09 (installer-native variants).

Explicitly OUT of this phase (permanently cut from v1):
- No tray icon, no TRAY-* reqs
- No WebView2, no HKCU Run key, no WebView2 bootstrapper bundling, no first_run.flag sentinel
- No persistent GUI at all — scheduled task + log file are the ongoing interface

</domain>

<decisions>
## Implementation Decisions

### Installer toolchain
- **Inno Setup 6.7.1** via `amake/innosetup-docker` on GitHub Actions (Linux runner)
- Output: `EarlScheibWatcher-Setup.exe` (single file), Authenticode-signed via the same osslsigncode pipeline from Phase 1

### Page sequence (Inno Setup native UI)
1. **Welcome** — brief description + SmartScreen explanation
2. **License / Terms** — short (2 paragraphs); skip if no legal requirement
3. **Folder Selection** — custom page with auto-scan preview + folder picker + UNC/mapped-drive warning
4. **Connection Test** — runs `earlscheib.exe --test` via temp config; success/retry/skip handling
5. **CCC ONE Info** — screenshot + instructions + "I've done this" checkbox (required)
6. **Install** — extract files, set ACLs, register Scheduled Task, run first scan
7. **Finish** — shows log file path + Task Scheduler location for Marco's records

### Path auto-detection (for Folder Selection page)
Scan in order; the first existing path wins:
- `C:\CCC\EMS_Export`
- `C:\CCC\APPS\CCCCONE\CCCCONE\DATA`
- `C:\Program Files\CCC`
- `C:\Program Files (x86)\CCC`
If none exist → show a folder picker with a default of `C:\CCC\EMS_Export`.
If the chosen path starts with a mapped-drive letter (e.g., `Z:\`) → warn with a dialog explaining SYSTEM-scheduled tasks can't see mapped drives; offer: (a) enter UNC path instead, (b) configure task to run as current user.

### Connection test page
Pascal-script handler that writes a temp `config.ini` into `%TEMP%`, runs `earlscheib.exe --test` with that config, captures exit code:
- 0 → "Connected" checkmark, Next button enabled
- Non-zero → shows the last few lines of the test output (`%TEMP%\earlscheib-install-test.log`), offers "Retry" / "Skip this check" / "Cancel install"

### Scheduled Task registration
Via `schtasks /Create /XML` using a pre-written XML template. Template chooses user vs SYSTEM based on whether a mapped drive was detected:
- **SYSTEM (default)**: `/RU SYSTEM /RL HIGHEST`
- **User fallback**: `/RU "{user}" /IT` — interactive (sees mapped drives)
The XML template specifies 5-minute repetition with an indefinite duration.

### ACL step
```powershell
icacls "C:\EarlScheibWatcher" /grant "SYSTEM:(OI)(CI)F" /grant "Users:(OI)(CI)M" /T
```
(Runs via `[Run]` section in Inno Setup with runhidden flag.)

### `config.ini` write on finish
Keys written: `watch_folder` (Marco's chosen path), `webhook_url` (baked default, matches the `earlscheib.exe` dev-default), `secret_key` (empty — secret is baked into the binary already), `log_level=INFO`. `onlyifdoesntexist` guard ensures upgrades preserve Marco's settings.

### First scan at install end
`[Run] Filename: "{app}\earlscheib.exe"; Parameters: "--scan"; Flags: runhidden` — if this fails silently, the Scheduled Task will pick up where it left off; the log file at `C:\EarlScheibWatcher\ems_watcher.log` is the diagnostic surface.

### Uninstaller
- Deletes the Scheduled Task via `[UninstallRun] schtasks /Delete /TN EarlScheibEMSWatcher /F; Flags: runhidden`
- Optionally preserves `C:\EarlScheibWatcher\` (asks Marco via `[Code]` section) for log forensics post-uninstall

### SmartScreen copy
Welcome page includes: *"Windows may show a 'Windows protected your PC' dialog the first time you run this installer. Click 'More info' then 'Run anyway'. This is normal for new business software."*

### Build artifacts from Linux CI
- Docker: `docker run --rm -v "$PWD:/work" amake/innosetup:6.7.1 iscc /work/installer/earlscheib.iss`
- Output: `installer/Output/EarlScheibWatcher-Setup.exe`
- Same Authenticode signing as the binary (Phase 1 signing step is extended to the installer exe)

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets (from earlier phases)
- `dist/earlscheib.exe` — the Windows binary (Phase 1+2)
- `earlscheib.exe --test` — canned BMS POST, exit-code based
- `earlscheib.exe --scan` — single-shot scanner
- `earlscheib.exe --status` — human-readable status (not used by installer, but available)

### Established Patterns
- Config shape (`[watcher]` INI with `watch_folder`, `webhook_url`, `secret_key`, `log_level`) — mirror in installer's `config.ini` writer
- HMAC secret injected into binary via `-ldflags` in Phase 1 — installer doesn't touch secrets
- Existing `claude-code-project/install.bat` has prior art on `schtasks` invocation (SYSTEM vs user fallback) — port that logic to Pascal-script

### Integration Points
- Phase 1 CI signing step must be extended to also sign `EarlScheibWatcher-Setup.exe`
- Phase 4 (telemetry) will piggyback on the installed binary — no installer changes needed for Phase 4

</code_context>

<specifics>
## Specific Ideas

- The installer should be under 20 MB (mostly the Go binary ~1.4 MB + screenshots + Inno Setup overhead)
- The CCC ONE screenshot is a placeholder for now — Marco can send a real one later; embed a clear diagram even if not pixel-perfect
- Consider adding `--version` subcommand to the Go binary so the installer can verify the bundled binary matches the installer version (defensive)
- The Scheduled Task XML should use `UserId="S-1-5-18"` for SYSTEM (avoids locale-specific name resolution issues)

</specifics>

<deferred>
## Deferred Ideas

- EV code-signing (OV sufficient per research SUMMARY.md)
- MSI-based installer (Inno Setup .exe is simpler and sufficient for a single-customer deployment)
- Silent / unattended install mode (`/SILENT`) — nice-to-have for mass deployment; defer
- Auto-update mechanism — defer to v2
- Live folder-watch verification in the installer (would block Inno Setup main thread; defer to post-install step)

</deferred>
