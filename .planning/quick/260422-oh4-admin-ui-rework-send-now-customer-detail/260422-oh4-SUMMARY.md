---
phase: quick-260422-oh4
plan: 01
subsystem: admin-ui + webhook
tags: [oh4, admin-ui, send-now, schema-migration, self-update-cadence, customer-detail]
dependency_graph:
  requires:
    - "quick-260422-nk1 self-update mechanism (live; OH4 ships the 120s cooldown tuning)"
    - "Phase 5 queue admin UI scaffold (HTML/CSS/JS + /api/queue proxy)"
    - "BMS XML emission (internal/ems/bms.go) + app.py parse_bms"
  provides:
    - "Rich per-job customer context in admin UI (VIN, vehicle desc, RO tag, email)"
    - "Send-now endpoint POST /earlscheibconcord/queue/send-now (HMAC-gated)"
    - "Light-palette polished admin UI (Fraunces + Work Sans + IBM Plex Mono)"
    - "30-minute heartbeat timeout + friendly sleep panel on poll failure"
    - "IMMEDIATE_SEND_FOR_TESTING env flag for 60s scheduling override"
    - "120s self-update cooldown for fast test cycles"
  affects:
    - app.py (schema migration, parse_bms, schedule_job, /estimate, /queue, /queue/send-now)
    - internal/ems/bms.go (VehicleInfo + CommEmail emission)
    - internal/admin/server.go (HeartbeatTimeout 5m -> 30m; /api/send-now route)
    - internal/admin/proxy.go (handleSendNow)
    - internal/admin/ui/* (full aesthetic rework + send-now button + sleep panel)
    - internal/update/update.go (cooldownSeconds 3600 -> 120)
tech_stack:
  added: []
  patterns:
    - "Idempotent schema migration via per-ALTER try/except sqlite3.OperationalError"
    - "Atomic send-now claim: UPDATE ... WHERE id=? AND sent=0 -> rowcount gate"
    - "Twilio rollback on failure: sent=0, sent_at=0 so scheduler can retry"
    - "Session-local UI counters (sent today / failed) — no server history endpoint"
    - "Retry-then-sleep-panel UX: 20 * 3s retries before giving up with friendly message"
key_files:
  created:
    - internal/admin/ui/index.html   # full rewrite
  modified:
    - app.py
    - internal/ems/bms.go
    - internal/ems/bundle.go
    - internal/admin/server.go
    - internal/admin/proxy.go
    - internal/admin/admin_test.go
    - internal/admin/ui/main.css
    - internal/admin/ui/main.js
    - internal/update/update.go
    - Makefile
    - EarlScheibWatcher-Setup.exe
    - EarlScheibWatcher-Setup.zip
decisions:
  - "plan_override accepted: LIGHT 'Polished Small-Business Admin' palette replaces industrial-garage dark — cream + oxblood + amber + Fraunces/Work Sans/IBM Plex Mono"
  - "Heartbeat timeout 5m -> 30m (internal/admin/server.go) as a deviation Rule 3 to fix the runtime bug reported in plan_override #2"
  - "120s cooldown stays as int64 var (not ldflags-string + Atoi) — simpler, TODO left for production GA"
  - "Admin UI retry window = 20 x 3s (60s) before showing sleep panel; matches the user's 'retry every 3s for 60s' spec"
  - "send-now returns {\"sent\": true} without twilio sid — send_sms only returns bool"
  - "Rolls back sent=1 -> 0 on Twilio failure to keep the scheduler's retry path intact"
  - "Session-local sent/failed counters in JS; pending is recomputed from /api/queue on each poll"
metrics:
  duration: 52 minutes
  completed: 2026-04-22T18:00Z
  tasks_total: 5
  tasks_completed: 5
  commits: 5
  files_touched: 12
---

# Quick Task 260422-oh4: Admin UI Rework, Send-Now, Customer Detail — Summary

One-liner: shipped richer per-job customer context (VIN/vehicle/RO/email/sent_at) end-to-end, an HMAC-gated send-now endpoint, a full admin UI rework to a light warm Fraunces-based aesthetic (replacing the rejected industrial-garage dark scheme), a 30-minute heartbeat + friendly sleep panel, an IMMEDIATE_SEND_FOR_TESTING env flag, and a 120-second self-update cooldown — all live in production and MD5-parity-verified against the running download.exe.

## What Changed

### Schema migration (OH4-01)

`app.py init_db` now runs six idempotent `ALTER TABLE jobs ADD COLUMN` statements wrapped individually in `try/except sqlite3.OperationalError`. Safe to run repeatedly — duplicate-column errors are silently swallowed and `log.info("DB migrated: +N columns")` confirms how many were actually added.

New columns:

| Column         | Type    | Default | Purpose                                            |
| -------------- | ------- | ------- | -------------------------------------------------- |
| `vin`          | TEXT    | `''`    | Full 17-char VIN; admin UI masks to last 6         |
| `vehicle_desc` | TEXT    | `''`    | `"Year Make Model"` (e.g. "2022 Tesla Model S")    |
| `ro_id`        | TEXT    | `''`    | Shop repair order number (RO tag chip in UI)       |
| `email`        | TEXT    | `''`    | Customer email from Owner.CommEmail                |
| `address`      | TEXT    | `''`    | Street address (stored; not yet displayed)         |
| `sent_at`      | INTEGER | `0`     | Unix ts of actual SMS send (0 until sent)          |

Live schema verified with `sqlite3 jobs.db 'PRAGMA table_info(jobs)'` — 13 columns total on the production DB.

### BMS XML ↔ parse_bms field mapping

New `<VehicleInfo>` element under `<BMSTrans>` plus `<CommEmail>` inside `<Owner>`:

| Bundle source          | BMS XML tag               | parse_bms dict key | UI surface                      |
| ---------------------- | ------------------------- | ------------------ | ------------------------------- |
| `VEH.V_VIN`            | `<VIN>`                   | `vin`              | `VIN · <last6>` (hover = full)  |
| `VEH.V_MODEL_YR`       | `<Year>`                  | (synthesised)      | part of `vehicle_desc`          |
| `VEH.V_MAKEDESC`       | `<Make>`                  | (synthesised)      | part of `vehicle_desc`          |
| `VEH.V_MODEL`          | `<Model>`                 | (synthesised)      | part of `vehicle_desc`          |
| `ENV.RO_ID`            | `<ROId>`                  | `ro_id`            | `RO DR7QA13` pill chip          |
| `AD1.OWNR_EA → INSD_EA`| `<Owner><CommEmail>`      | `email`            | sub-line under phone number     |
| `AD1.OWNR_ADDR1`       | `<Owner><CommAddr>`       | `address`          | stored only (not yet displayed) |

`parse_bms` synthesizes `vehicle_desc = " ".join(filter(None, [year, make_, model])).strip()`. `omitempty` on every new Go field keeps the wire payload clean when source data is blank.

### Send-now endpoint (OH4-02)

`POST /earlscheibconcord/queue/send-now`, HMAC-gated (mirrors DELETE /queue).

Request: `{"id": N}`, HMAC-SHA256 signature over the raw body.

Flow:
1. Validate HMAC (401 on fail).
2. Parse `id` from JSON (400 on fail).
3. Atomic `UPDATE jobs SET sent = 1, sent_at = NOW WHERE id = ? AND sent = 0`.
4. If rowcount != 1 → 404 `not_found_or_already_sent`.
5. Fetch job_type + phone + name; compose SMS body from MSG_24H/MSG_3DAY/MSG_REVIEW.
6. Call `send_sms`. If True → 200 `{"sent": true}`.
7. If Twilio fails → roll back `sent=0, sent_at=0` so scheduler_loop picks it up on retry; return 500 `twilio_send_failed`.

TEST_PHONE_OVERRIDE is honoured so send-now during dev goes to the override phone (mirrors `/estimate`).

Go admin proxy: new `/api/send-now` handler in `internal/admin/proxy.go` — byte-for-byte mirror of `handleCancel` except method=POST and URL suffix=`/queue/send-now`. Three new tests pass.

### Admin UI rework (OH4-03) — aesthetic direction

User rejected the planned industrial-garage dark scheme. Replaced with "Polished Small-Business Admin":

**Palette (CSS custom properties at :root):**

| Token           | Hex       | Usage                                |
| --------------- | --------- | ------------------------------------ |
| `--canvas`      | `#faf7f2` | Warm cream page background           |
| `--surface`     | `#ffffff` | Card surface                         |
| `--surface-alt` | `#f4efe8` | Subtle stat pill / vehicle panel bg  |
| `--ink`         | `#2a2826` | Warm near-black text (never #000)    |
| `--ink-soft`    | `#6b6560` | Secondary text                       |
| `--hairline`    | `#e8e2d8` | Borders                              |
| `--oxblood`     | `#8b2a2a` | Brand / primary CTA                  |
| `--oxblood-hot` | `#a8332e` | Hover state                          |
| `--amber`       | `#c8953f` | Sent/scheduled warm accent           |
| `--terra`       | `#c25f3e` | Alert/retry (warm, not fire-red)     |

**Typography (Google Fonts, preconnected):**

- Fraunces (variable serif 500/600/700, opsz 9..144) — headings, customer names, brand
- Work Sans (400/500/600) — body text, buttons
- IBM Plex Mono (400/500/600) — phone numbers, VIN, timestamps, stat values

Zero Inter / Roboto / Arial / system-ui / Fraunces-mixed-with-industrial. `grep -c Fraunces internal/admin/ui/*` returns 17 occurrences across index.html + main.css.

**Layout:**

- Single-column on mobile, 2-column grid at ≥960px.
- Cards: 16px radius, 28-30px padding, soft layered shadows (0 2px 8px + 0 8px 24px).
- Sticky top bar with brand (ES monogram + "Earl Scheib · Queue"), stats pill (Pending · Sent today · Failed), sync dot (amber, pulses on each successful poll), refresh button.
- Stats hidden under 720px to keep mobile topbar clean.

**Per-job card structure:**

1. **Header** — Fraunces 28-30px customer name, mono-phone + email sub-line, chip pill (24H/3DAY/REVIEW) + amber "in Xm → H:MM PM" scheduled-send pill.
2. **Vehicle panel** — soft cream sub-panel with `vehicle_desc` (Fraunces 18px) + masked VIN (mono, hover-reveal full) + RO tag (mono uppercase pill). Panel removed entirely if all three are blank.
3. **SMS preview bubble** — cream background, 3px amber left-border, 14.5px Work Sans; `previewSMS(jobType, name)` in main.js replicates the app.py MSG_24H/MSG_3DAY/MSG_REVIEW templates byte-for-byte. Comment in the JS reminds future maintainers to keep them in sync.
4. **Actions** — `[Send now →]` oxblood primary (soft shadow + 1px translateY on hover) + `[Cancel]` ghost button with hairline border.

**Interactions:**

- Send-now click → native confirm dialog → POST `/api/send-now` → on 200: mark card `data-state="sent"` (55% opacity + amber "Sent at H:MM PM" pill overlay) + re-fetch queue.
- Cancel → confirm → POST `/api/cancel` → card fades out + re-fetch.
- Counters: pending recomputed each poll; sent-today and failed are session-local since no server-side history endpoint exists.

**Empty state:** warm cream card with amber dot + Fraunces "All caught up" — replaces the "NO PENDING JOBS" brutalist caps.

**Sleep state (new):** after 60s of failed polls, queue replaced with a Fraunces "Queue Viewer is resting" card explaining the 30-min timeout and how to wake it up. Uses the same warm palette — not a scary error page.

### Heartbeat timeout bug (OH4 plan_override #2)

`internal/admin/server.go` — `HeartbeatTimeout` default raised from 5 min to 30 min. Ship this separately-observable to the UI: JS retries the poll every 3 s for 60 s on failure; any successful retry dismisses the sleep message; after the retry window expires, the queue area is replaced by the sleep panel. Zero "cannot reach local admin — is earlscheib.exe --admin still running?" strings remain in the UI.

### IMMEDIATE_SEND_FOR_TESTING (OH4-04)

Set `IMMEDIATE_SEND_FOR_TESTING=1` in the server environment and restart app.py. `schedule_job` will override `send_at = now + 60` for every 24h / 72h / review job — after the dedup check (so re-POSTs still skip). Logs each override at INFO: `IMMEDIATE_SEND_FOR_TESTING=1 — overriding send_at from X to Y (now+60) for doc_id=... job_type=...`. No Go client restart needed.

Verified: `IMMEDIATE_SEND_FOR_TESTING=1 python3 -c "... schedule_job(...)"` produced send_at 60 s ahead; unset yielded 86400.

### Update cooldown (OH4-05)

`internal/update/update.go` — `const cooldownSeconds = 3600` promoted to `var cooldownSeconds int64 = 120`. Existing `now - state.Ts < cooldownSeconds` comparison still compiles (all-int64). Marco's watcher picks up a newly-uploaded installer within one scan cycle (Scheduled Task every 5 min) + 120s cooldown ≈ 5-7 min end-to-end.

**Production raise path:** edit the int64 default directly. Ldflags `-X` only supports string vars — migrating would need an `init()` with `strconv.Atoi`. TODO comment in the source.

## Artifacts — MD5 chain

| File                                           | MD5                                |
| ---------------------------------------------- | ---------------------------------- |
| EarlScheibWatcher-Setup.exe                    | `1cd2fa71deba74d175bb55a60de99cc3` |
| dist/earlscheib.exe                            | `e772383d79e508440684ae9efee5e7e6` |
| EarlScheibWatcher-Setup.zip                    | `4eb59512cefb24c171712f32ef576bab` |
| dist/EarlScheibWatcher-Portable.zip            | `b10440dda6ab110eb734d5cda0316fb6` |

**Live parity:** `curl -I https://support.jjagpal.me/earlscheibconcord/download.exe` returned `content-length: 6083640` matching local stat. Full download MD5 = `1cd2fa71deba74d175bb55a60de99cc3`. **PARITY CONFIRMED.**

SHA256[:16] version tag emitted by `/version`: `62dc5789b79be0b4`. Cooldown = 120 s means the next client scan picks up this version within ~2 minutes.

## Commits

| # | Task               | Hash      | Files                                                                 |
| - | ------------------ | --------- | --------------------------------------------------------------------- |
| 1 | Schema + BMS + /queue | `016762f` | app.py, internal/ems/bms.go, internal/ems/bundle.go                   |
| 2 | Send-now + proxy   | `083442b` | app.py, internal/admin/server.go, proxy.go, admin_test.go             |
| 3 | UI rework + 30-min heartbeat + sleep panel | `d89bfcb` | internal/admin/server.go, internal/admin/ui/*         |
| 4 | IMMEDIATE_SEND + 120s cooldown | `57118de` | app.py, internal/update/update.go, Makefile                           |
| 5 | Release artifacts  | `3620fe4` | EarlScheibWatcher-Setup.exe, EarlScheibWatcher-Setup.zip              |

Pushed to `origin/master` (HEAD = `3620fe4`).

## Deviations from Plan

### Plan overrides accepted from the user

**OVERRIDE 1 — Aesthetic direction rejected and replaced.** The original plan's dark industrial-garage palette (Archivo Black + concrete/oxblood dark) was rejected pre-execution. Replaced end-to-end with a warm light "Polished Small-Business Admin" direction:

- Font stack: Fraunces + Work Sans + IBM Plex Mono (not Archivo Black).
- Palette: cream + oxblood + amber on light surfaces (not concrete dark with acid-green accents).
- Corners: 8-16 px radius (not 0 px brutalist).
- Shadows: soft layered warm shadows (not 6px 6px 0 oxblood hard drop).
- Empty state: friendly Fraunces "All caught up" card (not 120 px outlined "NO PENDING JOBS").

All functional requirements from the original plan preserved — schema, BMS emit, send-now endpoint, stats strip, send-now button, sync dot, SMS preview, VIN mask, chip variants. Only the visual language differs.

**OVERRIDE 2 — Heartbeat timeout bug bundled into task 3.** Added:
- `HeartbeatTimeout` default 5m → 30m in `internal/admin/server.go`.
- JS retry logic: 20 × 3s before showing a friendly sleep panel.
- New `<template id="sleep-state-template">` with warm-aesthetic "Queue Viewer is resting" message.

### Auto-fixed (deviation rules)

**[Rule 3 — Blocking] Preserved commands.json.** Git flagged commands.json as modified with only a trailing-newline diff (the heartbeat handler rewrites the file without a newline). Per constraint "DO NOT modify commands.json", `git checkout commands.json` restored the exact on-disk state before each of the five commits. No functional change.

**[Rule 3 — Blocking] Restarted app.py with >>append redirection** per the constraints so server logs are not truncated. Used `nohup /usr/bin/python3 app.py >>/tmp/app.out 2>&1 & disown`. The old PID was killed cleanly via `lsof -ti :8200 | xargs kill`.

**[Rule 2 — Correctness] TEST_PHONE_OVERRIDE in send-now.** The plan didn't explicitly mention it, but omitting it would have meant send-now fires SMS to the real customer phone even in dev mode (while /estimate correctly redirects to the override). Added the same `if TEST_PHONE_OVERRIDE:` branch to keep dev isolation consistent.

**[Rule 2 — Correctness] Twilio-failure rollback.** On `send_sms` returning False, the handler rolls back `sent=0, sent_at=0` so `scheduler_loop` can retry the job. Without this, a transient Twilio 500 would permanently mark the row sent with no SMS ever delivered.

### No architectural deviations (Rule 4)

No Rule 4 decisions required — all work fit within the existing server/client architecture.

## Operator Notes

**Auto-update cadence live.** With `cooldownSeconds = 120`, Marco's client watcher will poll `/version`, detect the new hash `62dc5789b79be0b4`, download the installer, and silently reinstall within one Scheduled-Task cycle (5 min) + 120 s cooldown — expect complete rollout in ≈5-7 minutes from push time.

**Revert path:** if anything misbehaves, `AUTO_UPDATE_PAUSED=1` on the server (or `touch update_paused` in the app directory) freezes all client self-updates within one poll cycle while the incident is investigated.

**Production GA todos:**
1. Raise `cooldownSeconds` back to ~3600 in `internal/update/update.go` (or migrate to ldflags-string + Atoi).
2. Unset `IMMEDIATE_SEND_FOR_TESTING` from the server environment before real customers start receiving SMS.
3. Build a server-side sent-history endpoint so the admin UI "Sent today" counter survives page reloads.

## Self-Check: PASSED

- `git log --oneline -5` lists: `3620fe4 chore(oh4) part 5/5`, `57118de feat(oh4) part 4/5`, `d89bfcb feat(oh4) part 3/5`, `083442b feat(oh4) part 2/5`, `016762f feat(oh4) part 1/5` — **all 5 commits present on master, pushed to origin.**
- `EarlScheibWatcher-Setup.exe` MD5 = `1cd2fa71deba74d175bb55a60de99cc3` matches live download.exe MD5 (curl vs local) — **MD5 parity verified.**
- `dist/EarlScheibWatcher-Portable.zip` exists; `EarlScheibWatcher-Setup.zip` exists; `dist/earlscheib.exe` exists.
- `go test ./... -race -count=1` — **full project green** (admin, ems, update, webhook, etc.).
- `python3 -m py_compile app.py` — **clean.**
- `sqlite3 jobs.db 'PRAGMA table_info(jobs)'` — **13 columns** including vin/vehicle_desc/ro_id/email/address/sent_at.
- `curl /queue` (HMAC) — **returns JSON rows with the 7 new fields** populated for new bundles, empty for legacy rows.
- `curl /queue/send-now` with id=999 → **404 `not_found_or_already_sent`**; with bad sig → **401 `invalid signature`**.
- `grep -rE "Inter|Roboto|Arial|system-ui" internal/admin/ui/` — **zero font-family hits** (only `setInterval` JS calls match the substring, which is irrelevant).
- `grep -cE "Fraunces|Work Sans|IBM Plex Mono" internal/admin/ui/*` — **17 occurrences across 2 files.**
