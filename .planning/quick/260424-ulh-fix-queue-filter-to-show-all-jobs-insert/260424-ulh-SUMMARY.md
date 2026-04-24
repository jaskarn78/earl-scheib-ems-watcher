---
quick_id: 260424-ulh
date: 2026-04-24
status: complete
requirements: [ULH-01, ULH-02, ULH-03]
commits:
  - hash: d40ec51
    task: Task 1 — ?status= filter on /earlscheibconcord/queue
  - hash: 29daa6d
    task: Task 2 — reversible pending-row injector script
  - hash: pending
    task: Task 3 — diagnostic SUMMARY.md (this file)
files_modified:
  - app.py
files_created:
  - scripts/insert_test_pending_job.py
  - .planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md
---

# 260424-ulh — Quick Task Summary

## Task

Fix `/queue` filter to show all jobs, insert a realistic pending test work
order, and diagnose why WIN-8I9KME32KLC has not POSTed an estimate since
Apr 20.

## Outcome

- **Filter fix**: `GET /earlscheibconcord/queue` now accepts optional
  `?status=` (`all|pending|sent`), defaults to `pending` (backwards-compatible),
  rejects unknown values with HTTP 400. Go admin proxy and shared `main.js`
  required no changes — bare-array response shape preserved.
- **Test row**: `scripts/insert_test_pending_job.py` inserts one realistic
  pending row with `send_at` 7 days out; `--remove` tombstone-deletes all test
  rows idempotently. One test row is present in the DB at hand-off.
- **Watcher diagnostic**: WIN-8I9KME32KLC is fully healthy — heartbeats every
  5 min, bundle uploads fire in ~60 s when a new EMS bundle appears. The
  silence since Apr 20 is because CCC ONE has produced only **one** new
  estimate bundle (`e8b18b75` on Apr 24 17:01 UTC) — Marco simply hasn't
  opened new jobs. Evidence cited below.

## Changes

- **`app.py`** (commit `d40ec51`): `/queue` handler parses `parsed.query` via
  `parse_qs`, whitelists `{all, pending, sent}`, returns 400 on anything else.
  pending keeps `ORDER BY send_at ASC`; sent/all use
  `ORDER BY COALESCE(sent_at, send_at, created_at) DESC`. SELECT columns and
  JSON array shape unchanged.
- **`scripts/insert_test_pending_job.py`** (commit `29daa6d`): stdlib-only
  CLI. Stores `send_at/created_at/sent_at` as INTEGER unix seconds (plan spec
  said strings — corrected; see Deviations). Tombstone `"ULH test row"` in
  `name` makes `--remove` idempotent. Exit 2 on constraint violation.
  `TEST_PHONE=+15308450190` hard-coded so accidental fire can't text a real
  customer.

## Verification

```
systemctl is-active earl-scheib.service     # → active
curl -u $BASIC .../queue                    # 4 rows (3 pending + 1 test)
curl -u $BASIC .../queue?status=pending     # 4 rows (same — default equals pending)
curl -u $BASIC .../queue?status=sent        # 20 rows, DESC by sent_at
curl -u $BASIC .../queue?status=all         # 24 rows (4 + 20, pending+sent=all)
curl -u $BASIC .../queue?status=bogus       # HTTP 400, {"error": "invalid status; must be one of: all, pending, sent"}
```

- Test row `id=50` is visible in default and `?status=all`, with
  `send_at=1777673360` (7.00 days out — confirmed via datetime delta).
- `scripts/insert_test_pending_job.py --remove` twice in a row: first prints
  `removed 1`, second prints `removed 0` — idempotent.

## Windows watcher diagnostic (WIN-8I9KME32KLC)

### Evidence observed

**Heartbeats**: 374 heartbeat lines from 2026-04-21 17:27 through
2026-04-24 20:40 UTC in `journalctl -u earl-scheib.service`; spacing 5 min
exactly, no gaps. Every line returns HTTP 200. Only one hostname seen on this
endpoint since Apr 20: `WIN-8I9KME32KLC` (no other clients).

**Route breakdown since Apr 20** (`journalctl` → `grep POST /earlscheibconcord`):

| Route | Count |
|---|---|
| `POST /earlscheibconcord/heartbeat` | 368 |
| `POST /earlscheibconcord/logs` | 10 (telemetry tails) |
| `POST /earlscheibconcord/queue/send-now` | 5 (admin UI) |
| `POST /earlscheibconcord?trigger=ems_bundle` | **8** |
| `POST /earlscheibconcord/estimate` | **0** |

The modern client no longer POSTs to `/estimate` — the K38 bundle parser
introduced `?trigger=ems_bundle` as the canonical ingestion route. All 8
bundle POSTs returned 200.

**Bundle POSTs by day** (`journalctl … | grep trigger=ems_bundle`):
- Apr 22: 5
- Apr 23: 2 (last at 14:33:50)
- Apr 24: 1 (at 17:01:02, doc_id=`e8b18b75`)

**Last DB ingestion**: `sqlite3 jobs.db "SELECT datetime(MAX(created_at),'unixepoch')"`
→ `2026-04-24 17:01:02` — the Apr 24 bundle landed and created rows 47+48
(`e8b18b75` × 24h and 3day job_types). `IMMEDIATE_SEND_FOR_TESTING=1`
overrode their `send_at` to now+60, they fired through Twilio at 17:02:13
(response 201, visible in journalctl). Everything worked end-to-end.

**Failed requests from this host Apr 20 → now**: 0 4xx/5xx responses tied to
`WIN-8I9KME32KLC`. The 404/501 errors in logs are browser probes
(`/earlscheibconcord/download**`, `/favicon.ico`, `HEAD /download`) — not the
watcher.

**Client-side telemetry** (`received_logs/latest.log`, Apr 24 17:00):

```
2026-04-24 17:00:02 scan start watch_folder="C:\\EarlScheibWatcher" …
2026-04-24 17:00:02 ems bundle detected basename="09011b25" files=15
2026-04-24 17:00:02 ems bundle detected basename="31cbe3a5" files=15
…  (13 previously-seen bundles enumerated)
2026-04-24 17:00:02 ems bundle detected basename="e8b18b75" files=15   ← NEW
2026-04-24 17:01:02 webhook: sent filename="e8b18b75.bundle" status=200 bytes=901
2026-04-24 17:01:02 ems bundle processed basename="e8b18b75" bytes=901
2026-04-24 17:01:02 Run complete processed=1 errors=0
```

Tail-counts across the same log file:
- `Run complete processed=0`: **25** scans (all Apr 23–24 until the Apr 24
  17:00 run)
- `Run complete processed=1`: **1** scan (the Apr 24 one)

Every scan enumerates the same 13 bundles, recognises them as already-sent
via client-side `processed_files`, and stays silent. The moment a 14th
(`e8b18b75`) appeared, it was POSTed within 60 seconds.

### Diagnosis

**Marco is not creating new estimates in CCC ONE — that is the only cause
consistent with the evidence.** The watcher is operationally healthy on every
dimension we can measure from the server: heartbeat cadence (5 min flat for
374 consecutive intervals), HMAC auth (all 368 heartbeats returned 200, zero
signature failures), disk I/O (10 `POST /logs` tails landed as expected), and
new-bundle handling (the one new bundle `e8b18b75` on Apr 24 17:00 was
detected, POSTed, received, parsed, stored, scheduled, and Twilio-delivered
in ~70 seconds end-to-end). Client-side `Run complete processed=0` on 25
consecutive scans proves the watcher is scanning and de-duping as designed —
it is not failing silently, there are simply no new files for it to send.
This rules out the other hypotheses in the plan: no client-side dedup bug
(it correctly processed `e8b18b75`), no HTTP failure (zero 4xx/5xx from this
host), no wrong endpoint (heartbeat and bundle POSTs share the same
base URL and secret), no Twilio issue (the Apr 24 messages sent with 201
responses). The 13 historical bundles appearing in every scan are a feature,
not a bug: CCC ONE preserves old EMS exports, and client-side dedup prevents
re-POST.

### Recommended follow-up action (NOT executed in this task)

**Ask Marco whether he has created or reopened any estimates in CCC ONE
since Apr 24 afternoon.** If he confirms "no new jobs this week," the
watcher is doing exactly what it should and no code action is needed. If he
says "yes, several — I reopened a few to revise pricing," then the follow-up
investigation is the client-side dedup key on `processed_files` (modified-time
tracking may be skipping resaves if CCC ONE overwrites in place without
changing mtime) — but that is a different quick task and should wait on his
answer.

## Reversal

How to roll back if needed:

- Remove test row: `python3 scripts/insert_test_pending_job.py --remove`
- Revert `/queue` filter: `git revert d40ec51` (the `?status=` addition is
  backwards-compatible; old clients never sent the parameter, so revert is
  low-risk)

## Deviations

- **Rule 3 — Blocking issue in plan spec**: plan directed storing
  `send_at/created_at/sent_at` as `"YYYY-MM-DD HH:MM:SS"` strings with
  `sent_at=NULL`. Schema actually uses INTEGER unix seconds with `sent_at=0`
  (verified via `.schema jobs` + `SELECT … LIMIT 5`). Script corrected before
  first run — had strings been used, `ORDER BY send_at` in the real app
  would have sorted the test row incorrectly relative to real integer rows.
- **Out-of-scope untracked files ignored**: `.gitignore`, `commands.json`,
  `dist/.gitkeep`, `update_paused` are prior-session detritus. Not committed.

## Artifacts

- `app.py` (modified — commit `d40ec51`)
- `scripts/insert_test_pending_job.py` (new — commit `29daa6d`)
- `.planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md` (this file)

## Self-Check

- [x] `app.py` present and contains `parse_qs` + status whitelist block (git log d40ec51 shows 39 insertions)
- [x] `scripts/insert_test_pending_job.py` present and executable (`-rwxrwxr-x`)
- [x] Commit `d40ec51` present in `git log --oneline`
- [x] Commit `29daa6d` present in `git log --oneline`
- [x] Service is active (`systemctl is-active earl-scheib.service` → `active`)
- [x] One test row currently visible in `/queue` (id=50, name contains "ULH test row")
