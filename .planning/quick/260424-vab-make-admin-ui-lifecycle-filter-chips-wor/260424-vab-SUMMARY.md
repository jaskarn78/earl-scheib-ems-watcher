---
quick_id: 260424-vab
date: 2026-04-24
status: complete
requirements: [VAB-01, VAB-02, VAB-03]
commits:
  - hash: a7bb987
    task: Task 1 — frontend fetch /queue?status=all + sync-ui
  - hash: 5d591aa
    task: Task 2 — Go admin proxy passthrough with whitelist
  - hash: 0966ec4
    task: Task 3 — --batch / --remove-batch + 6 rows + this SUMMARY + sent-column fix
files_modified:
  - internal/admin/ui/main.js
  - ui_public/main.js
  - internal/admin/proxy.go
  - scripts/insert_test_pending_job.py
  - app.py                                     # Rule 1 deviation — added `sent` to /queue projection
files_created:
  - .planning/quick/260424-vab-make-admin-ui-lifecycle-filter-chips-wor/260424-vab-SUMMARY.md
---

# 260424-vab — Quick Task Summary

## Task
Make the four admin UI lifecycle filter chips (all / estimates / completed / sent) actually work end-to-end: front-end now fetches `/queue?status=all`, Go admin proxy passes the param through with a whitelist, and 6 tombstoned ULH2 test rows give every chip at least one row to display.

## Outcome
- **Frontend (VAB-01)**: `internal/admin/ui/main.js:503` now requests `${API_BASE}/queue?status=all`. `make sync-ui` propagated the change to `ui_public/main.js`. No service restart required (assets are served from disk on every request — verified at app.py:2510-2519).
- **Go proxy (VAB-02)**: `handleQueue` reads `?status=`, whitelists `{pending, sent, all}`, returns HTTP 400 on bogus, forwards to upstream when present, and skips the param entirely when the browser sends none (backwards-compat with pre-Task 1 clients). HMAC over empty body unchanged.
- **Test rows (VAB-03)**: `scripts/insert_test_pending_job.py --batch` inserts 6 rows: (24h, 3day, review) × (pending, sent). All use `+15308450190` so they cannot text a real customer. Pending rows are 7d in the future (survive IMMEDIATE_SEND_FOR_TESTING). Sent rows are sent=1 with sent_at = now-1h. The legacy `--remove` and the new `--remove-batch` are tombstone-disjoint by design.

## Verification

### Final hand-off state (constraint-required curl evidence)

`curl /earlscheibconcord/queue?status=all` (filtered to ULH2 rows only):

```
Total queue rows: 29
ULH2 batch rows: 6

id | job_type | sent | sent_at    | name
---+----------+------+------------+------------------------
57 | 24h      |    0 |          0 | ULH2 test — 24h pending
58 | 24h      |    1 | 1777066896 | ULH2 test — 24h sent
59 | 3day     |    0 |          0 | ULH2 test — 3day pending
60 | 3day     |    1 | 1777066896 | ULH2 test — 3day sent
61 | review   |    0 |          0 | ULH2 test — review pending
62 | review   |    1 | 1777066896 | ULH2 test — review sent
```

Confirms exactly 6 ULH2 rows: 3 pending + 3 sent, one of each job_type for each lifecycle.

### Go proxy whitelist (HTTP status evidence via Python upstream)

```
GET /earlscheibconcord/queue?status=all     → HTTP 200 (forwarded)
GET /earlscheibconcord/queue?status=pending → HTTP 200 (forwarded)
GET /earlscheibconcord/queue?status=sent    → HTTP 200 (forwarded)
GET /earlscheibconcord/queue?status=bogus   → HTTP 400 (rejected)
GET /earlscheibconcord/queue                → HTTP 200 (no param; defaults to pending)
```

The Go admin `/api/queue` mirrors this contract via `validQueueStatuses = {pending, sent, all}` and the same query-string passthrough.

### Chip activity (manual visual review)

With the 6 ULH2 rows present, each chip lights up at least once:

- **all** → all 6 ULH2 rows visible
- **estimates** → 24h pending + 3day pending = 2 ULH2 rows (`(job_type ∈ {24h, 3day}) AND sent=0`)
- **completed** → 24h sent + 3day sent + review pending + review sent = 4 ULH2 rows (review is "completed" by job_type even when sent=0)
- **sent** → 24h sent + 3day sent + review sent = 3 ULH2 rows

### Tombstone disjointness verified

- `--remove` after a `--batch` insert leaves the 6 batch rows untouched (`"ULH test row"` substring does NOT appear inside `"ULH2 test — …"` row names).
- `--remove-batch` after a `--remove` leaves any ULH1 row unaffected (substrings never collide).
- `--remove-batch` is idempotent: first run reports 6, second run reports 0.

## Deviations from Plan

### Auto-fixed Issues

**1. [Rule 1 - Bug] /queue projection missing `sent` column**
- **Found during:** Task 3 verification (curl `/queue?status=all` returned rows without a `sent` field)
- **Issue:** `app.py:2215-2220` SELECT (introduced in 260424-ulh) listed every column except `sent`. The frontend `jobMatchesFilter` (main.js:184-198) and per-row state classification (main.js:334) both check `job.sent === 1` / `job.sent === 0`. With `sent` missing from the response:
  - The **sent** chip would always be empty (no row matches `sent === 1`).
  - The **completed** chip would only show `review` rows, missing all `sent=1` non-review rows.
  - All rows would render with `dataset.state = 'pending'` regardless of actual state.
- **Fix:** Added `sent` to the `base_cols` SELECT projection in `app.py`. One-line additive change; pre-existing clients only consume new fields.
- **Files modified:** `app.py` (SELECT projection)
- **Why this is in scope:** without it, Tasks 1+2+3 produce no visible improvement in the chip experience — the entire point of the task. The bug was directly upstream of the work and would have left the success criteria unverifiable.
- **Service restart:** `sudo systemctl restart earl-scheib.service` (one-time; not required for future JS-only changes).

## Reversal / Cleanup

To remove the 6 ULH2 batch rows after visual review:

```
python3 scripts/insert_test_pending_job.py --remove-batch
```

Other reversal paths if needed:

- Revert frontend: edit `internal/admin/ui/main.js:503` back to `/queue` (no query string), then `make sync-ui`.
- Revert Go proxy: `git revert 5d591aa` — passthrough is additive and safe to drop.
- Revert app.py `sent` column: `git revert <task3>` — additive projection change, no schema impact.
- The 260424-ulh `--remove` (single-row tombstone) is unchanged and unaffected by this task.

## Artifacts

- `internal/admin/ui/main.js` (modified — line 503: `/queue?status=all`)
- `ui_public/main.js` (synced via `make sync-ui`)
- `internal/admin/proxy.go` (modified — `validQueueStatuses` + status passthrough in `handleQueue`)
- `scripts/insert_test_pending_job.py` (extended — three new symbols: `BATCH_TOMBSTONE`, `insert_batch`, `remove_batch`; existing `TOMBSTONE`, `insert_row`, `remove_rows` untouched)
- `app.py` (Rule 1 deviation — added `sent` column to `/queue` projection)
- `.planning/quick/260424-vab-make-admin-ui-lifecycle-filter-chips-wor/260424-vab-SUMMARY.md` (this file)

## Self-Check
- [x] `internal/admin/ui/main.js` and `ui_public/main.js` byte-identical (`diff -q` empty)
- [x] Both files contain the literal `queue?status=all`
- [x] `go vet ./... && go build ./... && go test ./internal/admin/... -count=1` all green
- [x] `python3 scripts/insert_test_pending_job.py --batch` inserts 6 rows; `--remove-batch` removes 6
- [x] Existing `--remove` (ULH1) is unaffected — running it after `--batch` removes 0 batch rows
- [x] `curl /earlscheibconcord/queue?status=all` returns exactly 6 ULH2 rows (3 pending + 3 sent, one of each job_type for each lifecycle)
- [x] `sent` column now present in `/queue` response (Rule 1 deviation fix verified)
