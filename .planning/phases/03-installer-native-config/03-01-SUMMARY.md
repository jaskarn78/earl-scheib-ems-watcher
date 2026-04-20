---
phase: 03-installer-native-config
plan: "01"
subsystem: installer
tags: [inno-setup, installer, scheduled-task, windows, pascal]
dependency_graph:
  requires: []
  provides: [installer/earlscheib.iss, installer/tasks/EarlScheibEMSWatcher-SYSTEM.xml, installer/tasks/EarlScheibEMSWatcher-User.xml, installer/config.ini.template]
  affects: [03-02-PLAN.md]
tech_stack:
  added: [Inno Setup 6.7.1]
  patterns: [schtasks /Create /XML, icacls ACL, Pascal wizard pages, onlyifdoesntexist upgrade guard]
key_files:
  created:
    - installer/earlscheib.iss
    - installer/config.ini.template
    - installer/tasks/EarlScheibEMSWatcher-SYSTEM.xml
    - installer/tasks/EarlScheibEMSWatcher-User.xml
  modified: []
decisions:
  - "Task XML files use UserId=S-1-5-18 (SYSTEM SID) to avoid locale-specific name resolution"
  - "User fallback XML uses LogonType=InteractiveToken (no UserId) so schtasks resolves current user at install time"
  - "config.ini.template serves as [Files] source; WriteConfigIni() in [Code] overwrites it with Marco's chosen path on fresh installs only"
  - "Connection test uses EARLSCHEIB_DATA_DIR env var override to point earlscheib.exe at {tmp} during install"
  - "UninstallInitialize() prompts to keep or delete data dir for log forensics post-uninstall"
metrics:
  duration_minutes: 2
  completed_date: "2026-04-20"
  tasks_completed: 2
  files_created: 4
requirements_satisfied:
  - INST-01
  - INST-02
  - INST-03
  - INST-04
  - INST-08
  - INST-09
  - INST-10
  - INST-11
  - UI-06
  - UI-07
  - UI-08
  - UI-09
---

# Phase 3 Plan 01: Inno Setup Script + Scheduled Task XMLs Summary

**One-liner:** Complete Inno Setup 6.7.1 script with Pascal wizard pages (folder picker, connection test, CCC ONE guide), SYSTEM/user task XMLs, and onlyifdoesntexist config upgrade guard.

## What Was Built

Three installer artifacts that together produce a working `EarlScheibWatcher-Setup.exe` when compiled by `iscc`:

### installer/tasks/EarlScheibEMSWatcher-SYSTEM.xml
Task Scheduler XML that runs `C:\EarlScheibWatcher\earlscheib.exe --scan` as the SYSTEM account (`UserId="S-1-5-18"`) every 5 minutes with `HighestAvailable` run level, indefinite duration (`P9999D`), and `IgnoreNew` multi-instance policy.

### installer/tasks/EarlScheibEMSWatcher-User.xml
Identical structure but uses `LogonType=InteractiveToken` instead of a UserId — schtasks resolves the logged-on user at registration time. Used when a mapped drive is detected (SYSTEM can't see mapped drives) or when SYSTEM registration fails.

### installer/earlscheib.iss (471 lines)
Complete Inno Setup 6.7.1 script covering all wizard steps and post-install logic:

**[Setup]:** `AppName=Earl Scheib EMS Watcher`, `DefaultDirName=C:\EarlScheibWatcher`, `PrivilegesRequired=admin`, `OutputBaseFilename=EarlScheibWatcher-Setup`.

**[Messages]:** Welcome page override includes SmartScreen explanation: "click 'More info' then 'Run anyway'" (INST-11).

**[Files]:** `earlscheib.exe` with `ignoreversion`, `config.ini` with `onlyifdoesntexist uninsneveruninstall` (preserves Marco's settings on upgrade, INST-08), task XMLs extracted to `{tmp}` with `deleteafterinstall`.

**[Run]:** `icacls` sets `SYSTEM:(OI)(CI)F` and `Users:(OI)(CI)M` on the data dir (INST-03); first `--scan` runs hidden after install (INST-09).

**[UninstallRun]:** `schtasks /Delete /TN "EarlScheibEMSWatcher" /F` removes the task before binary deletion (INST-10).

**[Code] Pascal functions:**
- `DetectCCCOnePath()` — scans 4 CCC ONE candidate paths, returns first found (UI-06)
- `IsMappedDrive()` — detects single drive letters other than C: (INST-04)
- `RunConnectionTest()` — writes temp config.ini, sets EARLSCHEIB_DATA_DIR, runs `earlscheib.exe --test`, returns exit code 0 = success (UI-07)
- `RegisterScheduledTask()` — tries SYSTEM XML first, falls back to User XML on failure or when mapped drive detected (INST-04)
- `WriteConfigIni()` — writes `[watcher]` section with `watch_folder`, `webhook_url`, `log_level`; no `secret_key` (UI-09, INST-02)
- `UninstallInitialize()` — prompts Marco to confirm data dir deletion on uninstall (INST-10)

**Custom wizard pages (UI-06..09):**
1. `FolderPage` (after License): auto-detects CCC ONE path, browse picker, validates dir exists, mapped-drive warning dialog offering UNC or user-mode fallback
2. `ConnPage`: runs connection test on Next click, offers Retry/Ignore/Cancel on failure
3. `CCCInfoPage`: displays EMS Extract Preferences instructions ("Lock Estimate", "Save Workfile") with required `CCCCheckBox` confirmation before advancing

### installer/config.ini.template
Default config.ini shipped as the `onlyifdoesntexist` source file; `WriteConfigIni()` overwrites with Marco's actual folder path on fresh installs.

## Commits

| Task | Description | Hash |
|------|-------------|------|
| 1 | Scheduled Task XML templates (SYSTEM + User) | f1b02a6 |
| 2 | Complete Inno Setup script + config.ini.template | 8bf0ee3 |

## Deviations from Plan

None — plan executed exactly as written.

## Known Stubs

None. The installer script is complete with all referenced files. The binary (`dist/earlscheib-artifact.exe`) is produced by the build pipeline (Plan 03-02) and is intentionally absent from this plan's scope.

## Self-Check: PASSED

- [x] `installer/earlscheib.iss` exists (471 lines, >= 200 required)
- [x] `installer/config.ini.template` exists
- [x] `installer/tasks/EarlScheibEMSWatcher-SYSTEM.xml` exists with `S-1-5-18`
- [x] `installer/tasks/EarlScheibEMSWatcher-User.xml` exists with `InteractiveToken`
- [x] `AppName=Earl Scheib EMS Watcher` present
- [x] `onlyifdoesntexist` present (2 occurrences)
- [x] All 5 Pascal functions present: DetectCCCOnePath, IsMappedDrive, RunConnectionTest, RegisterScheduledTask, WriteConfigIni
- [x] `schtasks` with Delete/EarlScheibEMSWatcher present
- [x] "Run anyway" SmartScreen copy present
- [x] `SYSTEM.xml` reference present
- [x] `CCCCheckBox` present
- [x] Commits f1b02a6 and 8bf0ee3 verified in git log
