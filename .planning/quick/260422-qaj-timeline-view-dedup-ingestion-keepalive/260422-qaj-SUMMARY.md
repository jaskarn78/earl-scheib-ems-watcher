---
quick_task: 260422-qaj
title: Timeline view + (phone+VIN) dedup + 24h admin keepalive
date: 2026-04-22
commits:
  - 09a3af8  # feat(qaj): dedup jobs by (phone+VIN)
  - 616a932  # feat(qaj): lifecycle timeline + filter chips + live search
  - 87aca6a  # chore(qaj): bump admin heartbeat timeout to 24h
  - 70be77e  # release(qaj): installer rebuild + app.py migration + restart
tags: [admin-ui, dedup, scheduler, installer, migration]
---

# Quick Task 260422-qaj — Timeline View, Dedup, Keepalive

## One-liner

Collapse CCC ONE "Resave" bursts into a single pending job via (phone+VIN)
dedup, render the queue as per-estimate timelines with filter chips and
live search, and extend the admin-UI heartbeat to 24h for the field test.

## What changed

### Task 1 — Backend dedup by (phone + VIN) — `09a3af8`

The old schedule_job keyed on `(doc_id, job_type)`, but CCC ONE issues a new
DocumentVerCode every time a user clicks "Save" on an estimate — sometimes
20+ copies inside a minute. Every one of those duplicates was inserting a
new pending job, producing a queue full of near-identical follow-ups.

New dedup key: `phone|VIN` (or `phone|doc_id` when VIN is missing). Behaviour
for an existing row at the same key + job_type:

- **sent=0** → UPDATE the pending row with fresh customer/vehicle fields
  (name corrections, phone changes) but **preserve send_at** so the
  scheduled window doesn't reset.
- **sent=1, < 60d ago** → SKIP. Already delivered recently; don't re-schedule.
- **sent=1, ≥ 60d ago** → INSERT. Treat as a genuine new visit — reopen path.
- **No existing row** → INSERT. Fresh estimate.

`init_db` adds `estimate_key TEXT DEFAULT ''` (idempotent ALTER) and runs a
one-time backfill (`UPDATE jobs SET estimate_key = phone || '|' ||
COALESCE(NULLIF(vin,''), doc_id) WHERE estimate_key = ''`) so the 11
pre-existing rows in prod migrate cleanly.

`/queue` GET now includes `estimate_key` per row for timeline grouping.

Evidence script at `scripts/verify_dedup.py` — runs 4 cases (fresh / update
pending / skip <60d / reopen >60d) against a temp SQLite DB and prints PASS
lines. All 4 pass.

### Task 2 — Timeline + filter chips + live search — `616a932`

Admin UI rewrite. Three new concepts on the same light Fraunces/Work Sans
palette (no dark theme, no Inter):

- **Estimate cards** — one card per customer+vehicle. Grouped client-side
  by `estimate_key`. The card header shows the customer, contact, and vehicle
  once; a vertical timeline inside lists each follow-up job (24h / 3day /
  review) in chronological order.
- **Timeline** — `<ol>` with a spine line (2px `--hairline` border-left via
  `::before`) and coloured dots per entry: oxblood = pending, amber = sent,
  ink-soft = cancelling/cancelled. Each entry has its own chip + scheduled
  send + SMS preview + Send-now/Cancel buttons.
- **Filter chips** — All / Estimates / Work Completed / Sent. Groups are
  visible if ANY nested job matches; individual timeline entries also
  filter so a card only shows relevant rows.
- **Live search** — debounced 150ms. Matches across name, phone, VIN,
  doc_id, RO, email, vehicle_desc. Empty-state copy adapts ("No matches"
  instead of "All caught up" when filters are active).

Send-now and Cancel are re-wired to operate on the `<li class="timeline__entry">`
so a single estimate can have multiple jobs acted on independently.

### Task 3 — Admin heartbeat bump to 24h — `87aca6a`

One-line change: `HeartbeatTimeout` default extended from 30m → 24h in
`internal/admin/server.go`. TODO tagged in-code to revert to 30m before the
next prod re-release. The existing sleep panel (main.js) still triggers when
the timer finally elapses; users reach it far less often during overnight
field testing.

Chose Option A (simple bump) over Option B (env-gated flag): one-line
change, revertable in one commit, no new env var for Marco to deal with.

### Task 4 — Release + restart — `70be77e`

1. `make build-windows` with `GSD_HMAC_SECRET="$CCC_SECRET"` produced
   `dist/earlscheib.exe` (11.96 MB); `strings` confirmed secret baked in
   (3 matches).
2. Copied to `dist/earlscheib-artifact.exe` for the installer's [Files]
   step.
3. `docker run amake/innosetup:latest` compiled
   `EarlScheibWatcher-Setup.exe` (6.09 MB).
4. Rezipped via `/tmp` staging (the established pattern): final zip 5.39 MB.
5. MD5 parity verified against
   `https://support.jjagpal.me/earlscheibconcord/download`:
   `67e7c681adae18fe73520ee910b1d1ef` (both local + remote).
6. Killed old app.py (PID 220328), relaunched with APPEND redirection
   (`nohup python3 app.py >>/tmp/app.out 2>&1`). New PID 262716 bound
   0.0.0.0:8200. Migration log shows `+0 columns (0 if already applied)`
   because the column was added in the prior start cycle; the estimate_key
   column IS present in the schema (`PRAGMA table_info(jobs)` confirms).
7. Verified `/queue` returns `estimate_key` on existing rows (sample:
   `{"id": 29, "estimate_key": "+15308450190|7c7360d7", ...}`).
8. `git push origin master` succeeded (`ce6e3d8..70be77e`).

## Self-check

- `scripts/verify_dedup.py` — all 4 cases PASS
- `go test ./internal/admin/...` — PASS (admin + ui_test)
- MD5 parity — PASS (both `67e7c681adae18fe73520ee910b1d1ef`)
- Live `/queue` response includes `estimate_key` field — PASS
- Pre-existing jobs (ids 21-23, 29-36) still render + function — PASS
  (all have `estimate_key` populated via backfill)

## Deviations from the task manifest

### Auto-fixed issues (Rule 1 — Bug)

**1. `verify_dedup.py` initially failed Case 2 because IMMEDIATE_SEND_FOR_TESTING=1 in .env**
- `app.py` loads .env on import and flips the env flag to truthy.
- The dedup test needs to observe caller-supplied `send_at` verbatim to assert
  "preserve scheduled window" behaviour.
- **Fix:** Added `os.environ.pop("IMMEDIATE_SEND_FOR_TESTING", None)` before
  importing app, plus a defensive `app.IMMEDIATE_SEND_FOR_TESTING = False`
  after import to handle .env-reload.
- Committed in the same commit (`09a3af8`) — no separate patch needed.

### Rule 3 — Blocking fix: .gitignore runtime dirs

- `.claude/` + `received_logs/` were untracked and left in every `git status`.
- Added both to `.gitignore` under the existing "Runtime data / logs" section.
- Included in release commit `70be77e`.

## Files touched

| File | Change |
|------|--------|
| `app.py` | estimate_key column + backfill; schedule_job rewrite; /queue includes key |
| `scripts/verify_dedup.py` | NEW — 4-case dedup evidence script |
| `internal/admin/ui/index.html` | Controls bar (filters + search); estimate-card + timeline-entry templates |
| `internal/admin/ui/main.css` | `.filter`, `.search`, `.estimate-card`, `.timeline*` styles |
| `internal/admin/ui/main.js` | Group-by-estimate; filter + search predicates; re-wired Send-now/Cancel |
| `internal/admin/server.go` | HeartbeatTimeout default 30m → 24h |
| `.gitignore` | + `received_logs/`, + `.claude/` |
| `EarlScheibWatcher-Setup.exe` | Rebuilt |
| `EarlScheibWatcher-Setup.zip` | Rebuilt |

## Live verification

```bash
# /queue response showing estimate_key on a pre-existing pending row
$ curl -H "X-EMS-Signature: $SIG" http://127.0.0.1:8200/earlscheibconcord/queue \
    | python3 -m json.tool | head -16
[
    {
        "id": 29,
        "doc_id": "7c7360d7",
        "job_type": "24h",
        "phone": "+15308450190",
        "name": "JAS J",
        "send_at": 1776965161,
        "created_at": 1776878761,
        "vin": "",
        "vehicle_desc": "",
        "ro_id": "",
        "email": "",
        "address": "",
        "sent_at": 0,
        "estimate_key": "+15308450190|7c7360d7"
    },
    ...

# MD5 parity
$ md5sum /tmp/v.zip EarlScheibWatcher-Setup.zip
67e7c681adae18fe73520ee910b1d1ef  /tmp/v.zip
67e7c681adae18fe73520ee910b1d1ef  EarlScheibWatcher-Setup.zip

# dedup evidence
$ python3 scripts/verify_dedup.py
PASS Case 1 — fresh insert: 1 row, estimate_key set, sent=0
PASS Case 2 — update pending: doc_id/name refreshed, send_at preserved
PASS Case 3 — skip within 60d: still 1 row, existing sent=1 untouched
PASS Case 4 — reopen after 60d: 2 rows, new one is pending
ALL 4 CASES PASSED — (phone+VIN) dedup behaves per QAJ-01 spec.
```

## Self-Check: PASSED
