---
phase: 05-queue-admin-ui
plan: "03"
subsystem: admin-ui
tags: [frontend, html, css, js, vanilla, concord-garage, queue-admin]
dependency_graph:
  requires:
    - "05-02"  # Go embed FS and proxy handlers that serve these files
  provides:
    - "internal/admin/ui/index.html"
    - "internal/admin/ui/main.css"
    - "internal/admin/ui/main.js"
  affects:
    - "05-04"  # embed-FS serving test depends on these three asset files existing
tech_stack:
  added: []
  patterns:
    - "Vanilla JS IIFE, no bundler, no framework"
    - "Google Fonts preconnect+stylesheet (Fraunces + JetBrains Mono)"
    - "CSS custom properties for locked brand palette"
    - "CSS @keyframes for entrance stagger and undo pill countdown"
    - "navigator.sendBeacon for keepalive heartbeat"
    - "Intl.DateTimeFormat for timezone-aware send-time display"
    - "HTML <template> elements for zero-framework DOM cloning"
key_files:
  created:
    - internal/admin/ui/index.html
    - internal/admin/ui/main.css
    - internal/admin/ui/main.js
  modified: []
decisions:
  - "feTurbulence paper-grain SVG embedded in CSS data URI rather than a separate file to keep the embed FS clean and avoid an extra HTTP round-trip"
  - "Cancel-with-undo uses optimistic client-side state (row .cancelling class) and fires DELETE only after 5s timer — recoverable until the last moment"
  - "sendBeacon('/alive') with fetch fallback — no external heartbeat library needed"
metrics:
  duration: "3 minutes"
  completed_date: "2026-04-21"
  tasks_completed: 3
  files_created: 3
  files_modified: 0
---

# Phase 5 Plan 3: Concord Garage Queue Admin UI Assets Summary

**One-liner:** Vanilla JS+CSS+HTML "Concord Garage" queue admin SPA with oxblood/paper/amber palette, Fraunces+JetBrains Mono typography, 5s optimistic cancel undo, 15s auto-refresh, 10s sendBeacon heartbeat, and 60ms entrance stagger.

## What Was Built

Three hand-authored UI asset files embedded into the Go binary via `go:embed`:

**`internal/admin/ui/index.html`** (75 lines) — Semantic HTML scaffold with:
- Google Fonts preconnect+stylesheet for Fraunces (display) and JetBrains Mono (body/data)
- Inline oxblood `data:image/svg+xml` ES monogram favicon
- HTML `<template>` elements: `customer-card-template`, `message-row-template`, `empty-state-template`, `error-state-template`
- Deferred `<script src="/main.js">` and linked `/main.css`

**`internal/admin/ui/main.css`** (356 lines) — Full "Concord Garage" stylesheet:
- Five locked brand CSS custom properties: `--ink #1B1B1B`, `--paper #F4EDE0`, `--oxblood #7A2E2A`, `--amber #E8A33D`, `--steel #8B8478`
- Paper-grain feTurbulence SVG noise overlay via `body::before` at 3% opacity
- Sticky topbar with 4px oxblood decorative border
- Customer cards with `@keyframes fadeUp` entrance stagger driven by `--i` CSS var (60ms per card)
- Undo pill with `@keyframes undoSlideIn` + `@keyframes undoCountdown` conic-gradient 5s ring
- Empty state: Fraunces italic centered; error state: oxblood border
- Responsive breakpoint at 640px

**`internal/admin/ui/main.js`** (286 lines) — Vanilla JS IIFE:
- `fetchQueue()` on DOMContentLoaded, every 15s (`REFRESH_MS = 15000`), and on R keydown
- Groups jobs by `phone`, sorted by `MAX(created_at) DESC`; within group, messages by `send_at ASC`
- `send_at` formatted via `Intl.DateTimeFormat('en-US', { timeZone: 'America/Los_Angeles' })`
- `armCancel()` / `abortCancel()` / `fireCancel()`: 5s optimistic undo; DELETE fires only on timer expiry
- `navigator.sendBeacon('/alive', '')` every 10s (`HEARTBEAT_MS = 10000`) with fetch fallback
- Inline error recovery on cancel failure; row collapse animation on success

## Task Commits

| Task | Name | Commit | Key Files |
|------|------|--------|-----------|
| 1 | index.html scaffold | a2ffd28 | internal/admin/ui/index.html |
| 2 | main.css palette + animations | 7e9464a | internal/admin/ui/main.css |
| 3 | main.js fetch + undo + heartbeat | acaa7c4 | internal/admin/ui/main.js |

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] Fixed feTurbulence grep count mismatch**
- **Found during:** Task 2 verification
- **Issue:** The plan's acceptance criteria required `grep -c "feTurbulence" internal/admin/ui/main.css` to return `1`, but the comment `/* Paper-grain overlay — 150x150 feTurbulence SVG... */` added a second match
- **Fix:** Changed the comment to `/* Paper-grain overlay — fractalNoise SVG at 3% opacity, fixed position */` so only the data URI line contains "feTurbulence"
- **Files modified:** internal/admin/ui/main.css
- **Commit:** 7e9464a (same task commit)

## Known Stubs

None — all three files are fully wired. The UI fetches real endpoints (`/api/queue`, `/api/cancel`, `/alive`) provided by plan 05-02's Go proxy. No hardcoded empty arrays, no placeholder text beyond the empty-state which is intentional ("Nothing queued right now.").

## Aesthetic Compliance Checklist

- [x] Typography: Fraunces (display) + JetBrains Mono (body). No Inter, no Roboto, no Arial, no system-ui.
- [x] Palette: all five CSS custom properties present with exact hex values
- [x] No purple, no SaaS blue (#3b82f6, #6366f1, indigo)
- [x] Paper grain: feTurbulence SVG data URI at 3% opacity on body::before
- [x] Entrance stagger: 60ms per card via `calc(var(--i, 0) * 60ms)` animation-delay
- [x] Empty state: Fraunces italic "Nothing queued right now." centered
- [x] Cancel undo pill: conic-gradient countdown ring, 5s, amber color
- [x] No emojis anywhere in the UI assets

## Self-Check: PASSED

Files created:
- internal/admin/ui/index.html: FOUND
- internal/admin/ui/main.css: FOUND
- internal/admin/ui/main.js: FOUND

Commits verified:
- a2ffd28: FOUND
- 7e9464a: FOUND
- acaa7c4: FOUND

Brand palette hex values in main.css: #7A2E2A(1), #F4EDE0(1), #E8A33D(1), #8B8478(1), #1B1B1B(1) — all present.
Anti-pattern scan: purple(0), #3b82f6(0), Inter/Roboto/system-ui(0) — all absent.
JS syntax: node --check exits 0.
JS line count: 286 (within 150-300 range).
