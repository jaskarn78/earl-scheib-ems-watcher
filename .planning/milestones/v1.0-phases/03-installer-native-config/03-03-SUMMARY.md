---
phase: 03-installer-native-config
plan: "03"
subsystem: installer
tags: [installer, readme, svg, ci, inno-setup, smartscreen]
dependency_graph:
  requires: []
  provides:
    - installer/README.txt (SmartScreen instructions + first-time setup guide for Marco)
    - installer/assets/ccc-ems-extract-prefs.svg (CCC ONE EMS Extract Preferences diagram)
    - .github/workflows/build.yml installer-syntax-check job (CI fast-fail parse validation)
  affects:
    - installer/earlscheib.iss (references ccc-ems-extract-prefs.svg as [Files] asset)
    - CI pipeline (installer-syntax-check runs in parallel with test/build-windows)
tech_stack:
  added: []
  patterns:
    - SVG schematic diagram with annotation arrows for non-technical user documentation
    - Docker-based iscc parse-only validation in CI (/Dq /O- flags)
    - Placeholder binary touch for CI parse check (avoids needing real build artifact)
key_files:
  created:
    - installer/README.txt
    - installer/assets/ccc-ems-extract-prefs.svg
  modified:
    - .github/workflows/build.yml
decisions:
  - installer-syntax-check has no needs: dependency so it runs in parallel with test and build-windows for fast CI feedback
  - iscc /Dq /O- flags used: /Dq suppresses banner, /O- suppresses output file; together they parse-validate without producing a binary
  - Placeholder binary (touch dist/earlscheib-artifact.exe) satisfies [Files] Source: path existence check at parse time without requiring a real Go build
  - README.txt uses plain ASCII dashes (--) not Unicode em-dashes for compatibility with Windows Notepad
metrics:
  duration: "2 minutes"
  completed_date: "2026-04-20"
  tasks_completed: 2
  files_created_or_modified: 3
requirements_satisfied:
  - INST-11
  - UI-08
---

# Phase 03 Plan 03: Installer README, EMS SVG Diagram, and CI Syntax Check Summary

**One-liner:** Plain-text SmartScreen+setup guide for Marco, labeled SVG schematic of CCC ONE EMS Extract Preferences dialog with CHECK THIS BOX annotations, and parallel CI job that parse-validates earlscheib.iss via iscc /Dq /O- on every push.

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Write installer/README.txt and CCC ONE SVG diagram | 4a564ad | installer/README.txt, installer/assets/ccc-ems-extract-prefs.svg |
| 2 | Add installer-syntax-check CI job (fast parse validation) | 346651b | .github/workflows/build.yml |

## What Was Built

### installer/README.txt

Plain-text file that Marco will find at `C:\EarlScheibWatcher\` after install. Covers:

- What the watcher does (background task, 5-minute polling)
- SmartScreen "Windows protected your PC" walkthrough with numbered steps for "More info" then "Run anyway" (INST-11)
- Three-step first-time setup guide mirroring the installer wizard pages (Folder, Connection, CCC ONE)
- UNC path warning for mapped network drives (SYSTEM-account task cannot see lettered drive mappings)
- CCC ONE EMS Extract Preferences config steps with exact navigation path and both checkboxes called out
- File and task locations (program, config, log, Task Scheduler path `EarlScheibEMSWatcher`)
- How to verify the watcher is running (Task Scheduler Last Run Time + Last Run Result)
- Upgrade and uninstall instructions (Settings > Apps uninstaller removes task + files, preserves log)
- Support contact

Tone matches the prior `claude-code-project/README.txt`: numbered steps, plain language, no jargon, direct second-person address to Marco.

### installer/assets/ccc-ems-extract-prefs.svg

520x380 SVG schematic of the CCC ONE EMS Extract Preferences dialog. Contains:

- Title bar labeled "EMS Extract Preferences" in CCC ONE blue (#0066cc)
- Output Folder field with annotation arrow: "Set this to the same folder you chose during install"
- Extract Options section with Lock Estimate and Save Workfile checkboxes shown checked (blue checkmarks)
- Orange "CHECK THIS BOX" annotation banners with arrows pointing to each required checkbox
- Two unchecked options (Include Photos, Auto-Submit to Insurer) for visual realism
- Export Format dropdown showing "BMS XML"
- Save and Cancel buttons
- Caption: "CCC ONE -- Tools > Extract > EMS Extract Preferences"

Placeholder for a real screenshot from Marco; functional and clear as a setup guide diagram.

### .github/workflows/build.yml: installer-syntax-check job

New job added at the top of the `jobs:` block with no `needs:` dependency. Runs in parallel with `test` and `build-windows` for fast CI feedback before the full installer build. Steps:

1. Checkout repository
2. `touch dist/earlscheib-artifact.exe` -- creates zero-byte placeholder so iscc does not fail on missing [Files] Source: paths at parse time
3. `docker run amake/innosetup:6.7.1 iscc /Dq /O- /work/installer/earlscheib.iss` -- parse-validates the .iss script without producing an output installer exe

Fails CI immediately if earlscheib.iss has syntax errors. All existing jobs (`test`, `build-windows`) are unchanged.

## Deviations from Plan

None - plan executed exactly as written.

## Known Stubs

None. README.txt and the SVG diagram are complete deliverables. The SVG is explicitly a placeholder for a real CCC ONE screenshot (noted in the README and plan context) but the placeholder is intentional and functional -- it will be replaced when Marco supplies a real screenshot.

## Self-Check: PASSED

- installer/README.txt: exists, contains "More info", "Run anyway", "Lock Estimate", "Save Workfile", "EarlScheibEMSWatcher"
- installer/assets/ccc-ems-extract-prefs.svg: exists, contains "EMS Extract", "CHECK THIS BOX" (x2)
- .github/workflows/build.yml: contains "installer-syntax-check", "amake/innosetup:6.7.1", "iscc /Dq /O-", "placeholder binary"
- Commits 4a564ad and 346651b verified in git log
