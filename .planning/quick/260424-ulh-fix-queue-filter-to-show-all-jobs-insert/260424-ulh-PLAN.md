---
quick_id: 260424-ulh
type: quick
wave: 1
depends_on: []
files_modified:
  - app.py
  - scripts/insert_test_pending_job.py
  - .planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md
autonomous: true
requirements:
  - ULH-01  # Backend: /earlscheibconcord/queue honors optional ?status= filter (default preserves old behavior)
  - ULH-02  # Realistic pending test row inserted via reversible script so admin UI has a visible pending job
  - ULH-03  # Read-only diagnostic of WIN-8I9KME32KLC /estimate silence (Apr 20 last POST despite heartbeats) written to SUMMARY.md

must_haves:
  truths:
    - "GET /earlscheibconcord/queue (no query param) returns the same list as before the change (pending only)"
    - "GET /earlscheibconcord/queue?status=all returns pending AND sent rows, ordered such that UI lifecycle filter chips populate"
    - "GET /earlscheibconcord/queue?status=sent returns only sent=1 rows"
    - "GET /earlscheibconcord/queue?status=pending explicitly returns pending (matches default)"
    - "Unknown ?status= values (e.g. ?status=bogus) return HTTP 400 with a clear error, not a silent fallback"
    - "A realistic pending test work order row exists in jobs.db visible via GET /queue?status=all (and via the default pending filter)"
    - "The test row survives the IMMEDIATE_SEND_FOR_TESTING=1 60-second scheduler because send_at is set far enough in the future"
    - "The test row is trivially removable (documented one-liner SQL / script flag)"
    - "SUMMARY.md contains a concrete written diagnosis of why WIN-8I9KME32KLC has not POSTed /estimate since Apr 20, based on on-box evidence (journalctl + webhook.log + jobs table) — not just hypotheses"
  artifacts:
    - path: "app.py"
      provides: "GET /earlscheibconcord/queue handler honoring optional ?status= query param"
      contains: "WHERE sent"
    - path: "scripts/insert_test_pending_job.py"
      provides: "Idempotent, reversible test-row injector (insert + --remove flag)"
      min_lines: 40
    - path: ".planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md"
      provides: "Evidence-based diagnostic of Windows watcher /estimate silence"
      contains: "WIN-8I9KME32KLC"
  key_links:
    - from: "app.py GET /earlscheibconcord/queue handler (line ~2191)"
      to: "jobs table WHERE clause"
      via: "query string parser on self.path (urllib.parse.urlparse + parse_qs)"
      pattern: "parse_qs|status.*=.*(all|sent|pending)"
    - from: "scripts/insert_test_pending_job.py"
      to: "jobs.db jobs table"
      via: "sqlite3 connection to ./jobs.db (same path app.py uses)"
      pattern: "INSERT INTO jobs"
    - from: "SUMMARY.md diagnostic section"
      to: "Windows watcher behavior hypothesis"
      via: "journalctl grep of WIN-8I9KME32KLC + received_logs/webhook.log timestamps + jobs table last_row_for_this_sender query"
      pattern: "WIN-8I9KME32KLC|heartbeat|estimate"
---

<objective>
Unblock the admin UI lifecycle filter chips (pending/sent/completed) by making GET /earlscheibconcord/queue accept an optional ?status= query parameter, insert a realistic pending test work order so Marco (and the developer) can visually verify the queue view end-to-end, and produce an evidence-based diagnosis of why Marco's Windows watcher (WIN-8I9KME32KLC) has stopped POSTing /estimate despite heartbeating successfully every 5 minutes.

Purpose: The 260424-oyk summary flagged the hardcoded `WHERE sent = 0` filter as an open item — the admin UI cannot render non-pending jobs because they never arrive at the client. Combined with IMMEDIATE_SEND_FOR_TESTING=1 forcing new jobs through the pipe in 60s, Marco's queue is effectively invisible. A working queue filter, a visible test row, and a written cause analysis for the watcher silence leave this project demonstrably operational.

Output:
  - app.py /queue handler accepts ?status=all|sent|pending (default: pending — preserves existing behavior)
  - scripts/insert_test_pending_job.py: a Python script that inserts one realistic pending row with send_at 7 days in the future (so IMMEDIATE_SEND_FOR_TESTING cannot fire it before review), and supports --remove to delete it
  - 260424-ulh-SUMMARY.md with a concrete, evidence-based diagnosis of the WIN-8I9KME32KLC /estimate silence — rooted in journalctl entries, received_logs/webhook.log, and jobs table rows actually observed
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
@$HOME/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@CLAUDE.md
@app.py

Relevant code sections (for executor reference — DO NOT re-discover):

**Filter bug location:** `app.py` line 2191 opens the GET `/earlscheibconcord/queue` handler. The bug is on line 2207: `"FROM jobs WHERE sent = 0 ORDER BY send_at ASC"` — hardcoded, no optional ?status= support.

**Nearby patterns to mimic:**
- `app.py` uses `urllib.parse.urlparse(self.path)` + `parse_qs` elsewhere (e.g. /version, /commands). The new handler should keep the existing `path == "/earlscheibconcord/queue"` check (comparing against path-only, not full path) and parse query separately.
- Dual-auth helper `_validate_auth(self, b"")` already handles HMAC OR Basic.
- Response format is a bare JSON array via `self._send_json(200, rows)`. Do NOT change this shape — the admin UI depends on it (Phase 05-queue-admin-ui decision).

**DB schema (jobs table) — real columns already in production:**
`id, doc_id, job_type, phone, name, send_at, sent, created_at, vin, vehicle_desc, ro_id, email, address, sent_at, estimate_key`

**Existing similar script to model after:** `scripts/verify_dedup.py` — pattern for standalone Python CLI scripts that open jobs.db.

**Evidence sources for the watcher diagnostic (read-only — do NOT change anything on the watcher or server):**
- `journalctl -u earl-scheib.service --since "2026-04-20" | grep -i WIN-8I9KME32KLC`  → heartbeat cadence
- `journalctl -u earl-scheib.service --since "2026-04-20" | grep -iE "estimate|POST /earlscheibconcord"`  → inbound POST traffic
- `received_logs/webhook.log` and any app.log rotations — timestamp of last /estimate from the shop
- `sqlite3 jobs.db "SELECT doc_id, job_type, created_at, sent_at, estimate_key FROM jobs ORDER BY created_at DESC LIMIT 10"`  → most recent ingestions
- `ls -lat received_logs/ | head -20`  → last time any payload hit disk

Claude should form a diagnostic by triangulating these three: heartbeats present, /estimate absent, last DB row timestamp. Possible root-cause categories to evaluate (pick the one the evidence supports, do not list all):
1. No new EMS files in Marco's CCC ONE export folder (slow week / closed shop / holiday)
2. Client-side dedup preventing re-POST of previously-seen doc_ids (check processed_files timestamps on the Windows side? — likely not visible from server, note as a follow-up action)
3. Watcher's HTTP POST failing silently (would show 4xx/5xx in journalctl if request arrived)
4. Watcher reaching a different endpoint (wrong URL, path mismatch) — but heartbeat works, so URL host is correct
5. Watcher only processing specific file extensions / names that CCC ONE stopped producing
6. Twilio/downstream failure masking as no-new-POSTs (unlikely since /estimate is pre-Twilio)

Expected deliverable: 4-8 sentences in SUMMARY.md stating the most likely cause, the evidence supporting it, and a single recommended follow-up action (not to be executed in this task).

**Auth for local curl testing:**
- Basic auth with `ADMIN_UI_USER` / `ADMIN_UI_PASSWORD` from `.env` (preferred for interactive verification)
- Example: `curl -u "$ADMIN_UI_USER:$ADMIN_UI_PASSWORD" "http://localhost:8200/earlscheibconcord/queue?status=all" | jq .`

**Service restart:** `sudo systemctl restart earl-scheib.service` after app.py edits. Then verify `systemctl is-active earl-scheib.service` returns `active`.

**IMMEDIATE_SEND_FOR_TESTING caveat:** The env var is ON; new ingestions' send_at gets overridden to now+60s. The test row insertion script must therefore set send_at to a far-future value directly in SQL (bypassing the ingestion pipeline) — for example, 7 days in the future. The script should NOT re-enable or disable this env var.
</context>

<tasks>

<task type="auto">
  <name>Task 1: Add ?status= query param to /queue handler (app.py)</name>
  <files>app.py</files>
  <action>
Modify the GET `/earlscheibconcord/queue` handler at `app.py:2191-2213` to accept an optional `?status=` query parameter.

Implementation details:
1. At the top of the handler (after the `_validate_auth` check), parse the query string from `self.path`. Use `urllib.parse.urlparse` and `parse_qs` — `urllib.parse` is already imported elsewhere in app.py but verify at your edit site; if not imported in this module, add `from urllib.parse import urlparse, parse_qs` at the top of app.py alongside existing stdlib imports.
2. Extract `status = qs.get("status", ["pending"])[0]` — default to "pending" to preserve the existing contract (backwards-compatible; current admin UI and Go proxy expect pending-only when no param is provided).
3. Validate `status` against the whitelist `{"all", "pending", "sent"}`. On any other value, call `self._send_json(400, {"error": "invalid status; must be one of: all, pending, sent"})` and return.
4. Build the WHERE clause:
   - `status == "pending"` → `WHERE sent = 0`
   - `status == "sent"`    → `WHERE sent = 1`
   - `status == "all"`     → no WHERE clause
5. Keep the SELECT column list IDENTICAL to what is already there (id, doc_id, job_type, phone, name, send_at, created_at, vin, vehicle_desc, ro_id, email, address, sent_at, estimate_key) — do NOT add or remove fields. The front-end (shared `main.js`) depends on this shape.
6. ORDER BY logic: when status is "pending" keep `ORDER BY send_at ASC` (matches current). When status is "sent" or "all", use `ORDER BY COALESCE(sent_at, send_at, created_at) DESC` so the newest activity is first (consistent with how operators expect to see history). Put this behind a simple if/elif in Python — do NOT try to encode it in one SQL string.
7. Use parameterized queries — do NOT string-interpolate `status` into SQL (even though the whitelist makes it safe, parameterization is the project standard and avoids future drift). Example: two separate `cur.execute()` calls in an if/elif is acceptable; a single `cur.execute(sql, ())` with the WHERE clause built from the whitelist constants is also acceptable. Do NOT use f-strings for SQL.
8. Leave the JSON response shape as a bare list (`self._send_json(200, rows)`) — UNCHANGED. Adding a wrapper object would break the Go admin proxy and shared main.js.
9. Add a brief comment referencing `ULH-01` above the new block so the reason is traceable.

Do not touch any other handler. Do not modify the Go admin proxy — it already forwards query strings intact (and this change is backwards-compatible, so the Go side needs no update).

After the edit, restart the service:
  `sudo systemctl restart earl-scheib.service`
and verify it's active:
  `systemctl is-active earl-scheib.service`
  </action>
  <verify>
<automated>
# 1. service restarts cleanly
sudo systemctl restart earl-scheib.service && sleep 2 && systemctl is-active earl-scheib.service | grep -qx active

# 2. backwards compat — no query param returns same as before (pending only, 3 rows at time of planning)
BASIC=$(grep -E '^ADMIN_UI_USER=|^ADMIN_UI_PASSWORD=' .env | tr '\n' ' ' | awk -F= '{print $2":"$4}' | tr -d ' "')
DEFAULT=$(curl -sf -u "$BASIC" http://localhost:8200/earlscheibconcord/queue | python3 -c 'import json,sys; d=json.load(sys.stdin); print(all(r.get("sent_at") is None or r.get("send_at") for r in d) and len(d)>=0)')
[ "$DEFAULT" = "True" ] || { echo "default endpoint broken"; exit 1; }

# 3. ?status=all returns MORE rows than default (should be ~23 at planning time)
DEF_COUNT=$(curl -sf -u "$BASIC" 'http://localhost:8200/earlscheibconcord/queue' | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
ALL_COUNT=$(curl -sf -u "$BASIC" 'http://localhost:8200/earlscheibconcord/queue?status=all' | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
[ "$ALL_COUNT" -gt "$DEF_COUNT" ] || { echo "status=all did not expand results"; exit 1; }

# 4. ?status=sent returns ONLY sent rows (spot-check: all rows should have sent_at non-null OR at least the count should match ALL - DEFAULT)
SENT_COUNT=$(curl -sf -u "$BASIC" 'http://localhost:8200/earlscheibconcord/queue?status=sent' | python3 -c 'import json,sys; print(len(json.load(sys.stdin)))')
[ "$SENT_COUNT" -eq "$((ALL_COUNT - DEF_COUNT))" ] || { echo "sent count mismatch: sent=$SENT_COUNT, all-default=$((ALL_COUNT - DEF_COUNT))"; exit 1; }

# 5. invalid status returns 400
HTTP=$(curl -s -o /dev/null -w '%{http_code}' -u "$BASIC" 'http://localhost:8200/earlscheibconcord/queue?status=bogus')
[ "$HTTP" = "400" ] || { echo "expected 400 on invalid status, got $HTTP"; exit 1; }

echo "OK: /queue filter works for default, all, sent, and rejects bogus"
</automated>
  </verify>
  <done>
- app.py line ~2191 handler parses ?status=, validates against {all, pending, sent}, and returns 400 on any other value
- default (no query param) returns the exact same rows as before this change (pending-only)
- ?status=all returns pending + sent rows together, newest activity first
- ?status=sent returns only sent=1 rows
- SELECT column list and JSON response shape are UNCHANGED (no breakage of Go admin proxy or shared main.js)
- Service restarted and healthy (systemctl is-active = active)
- Curl matrix (see verify) all passes
  </done>
</task>

<task type="auto">
  <name>Task 2: Insert a realistic pending test work order via reversible Python script</name>
  <files>scripts/insert_test_pending_job.py</files>
  <action>
Create `scripts/insert_test_pending_job.py` — a standalone Python 3 script that inserts ONE realistic pending test work order into jobs.db so the admin UI has something visible in the pending lane even when IMMEDIATE_SEND_FOR_TESTING=1 is burning through real rows in 60 seconds.

The script:
1. Uses `sqlite3` from stdlib (no external deps). Opens `./jobs.db` (same path app.py uses — the service cwd is the project root per systemd unit file).
2. Default mode (`python3 scripts/insert_test_pending_job.py`) INSERTS a row with these values:
   - `doc_id`: `"TEST-ULH-<unix_ts>"` where `<unix_ts>` is `int(time.time())` — guarantees uniqueness on re-runs so the UNIQUE/dedup constraints don't reject it.
   - `job_type`: `"24h"` (a job_type that exists in DEFAULT_TEMPLATES so the SMS preview bubble will render).
   - `phone`: `"+15308450190"` (the developer's TEST_PHONE_OVERRIDE — never rings a real customer).
   - `name`: `"Marco Testsson (ULH test row)"` — the "(ULH test row)" suffix is the unambiguous tombstone for the cleanup pass.
   - `send_at`: 7 days in the future, formatted as `"YYYY-MM-DD HH:MM:SS"` (match the existing send_at format in the DB). This bypasses the IMMEDIATE_SEND_FOR_TESTING 60s window cleanly.
   - `sent`: `0`
   - `created_at`: `datetime.now(tz=timezone.utc).strftime("%Y-%m-%d %H:%M:%S")`.
   - `vin`: `"1HGCM82633A004352"` (valid 17-char Honda test VIN — looks real to a human reviewer).
   - `vehicle_desc`: `"2003 Honda Accord EX (TEST)"`.
   - `ro_id`: `"RO-ULH-TEST"`.
   - `email`: `"ulh-test@example.invalid"`.
   - `address`: `"123 Test Lane, Concord, CA 94520"`.
   - `sent_at`: `NULL`.
   - `estimate_key`: same pattern the real ingestion uses — for simplicity `"TEST-ULH-EST-<unix_ts>"`.
3. Reversible mode: `python3 scripts/insert_test_pending_job.py --remove` deletes all rows where `name LIKE '%ULH test row%'` (idempotent — removes all prior test rows, not just one). Print the deleted count.
4. Print a single-line confirmation on success (insert or remove), including the affected row id(s). On ANY error, print a human-readable message to stderr and exit 1.
5. Handle re-runs gracefully: if the INSERT trips a UNIQUE constraint, print the message and exit 2 (distinct from general errors). Test by running the script twice in the verify step.
6. Add a one-line shebang (`#!/usr/bin/env python3`) and make the script executable (`chmod +x`). Include a docstring at the top with usage instructions and the --remove hint.
7. Use argparse for the --remove flag — no hand-rolled argv parsing.
8. Do NOT touch app.py. Do NOT restart the service. This task is purely a DB mutation + a reversal path.

Reference the pattern of `scripts/verify_dedup.py` for the project style (stdlib-only, CLI-friendly, connect/commit/close hygiene).
  </action>
  <verify>
<automated>
set -e
# 1. remove any prior test rows (idempotent; exit 0 even if zero found)
python3 scripts/insert_test_pending_job.py --remove

# 2. insert a fresh test row
python3 scripts/insert_test_pending_job.py

# 3. it appears in ?status=pending (default)
BASIC=$(grep -E '^ADMIN_UI_USER=|^ADMIN_UI_PASSWORD=' .env | tr '\n' ' ' | awk -F= '{print $2":"$4}' | tr -d ' "')
curl -sf -u "$BASIC" 'http://localhost:8200/earlscheibconcord/queue' | python3 -c 'import json,sys; d=json.load(sys.stdin); assert any("ULH test row" in (r.get("name") or "") for r in d), "test row not found in default pending queue"; print("OK: test row visible in pending queue")'

# 4. it also appears in ?status=all
curl -sf -u "$BASIC" 'http://localhost:8200/earlscheibconcord/queue?status=all' | python3 -c 'import json,sys; d=json.load(sys.stdin); assert any("ULH test row" in (r.get("name") or "") for r in d), "test row not found in ?status=all"; print("OK: test row visible in all queue")'

# 5. send_at is far in the future so IMMEDIATE_SEND_FOR_TESTING can't fire it
curl -sf -u "$BASIC" 'http://localhost:8200/earlscheibconcord/queue' | python3 -c 'import json, sys, datetime; d=json.load(sys.stdin); row=[r for r in d if "ULH test row" in (r.get("name") or "")][0]; sa=datetime.datetime.strptime(row["send_at"],"%Y-%m-%d %H:%M:%S"); now=datetime.datetime.utcnow(); assert (sa - now).days >= 6, f"send_at too soon: {row[\"send_at\"]}"; print("OK: send_at >= 6 days out")'

# 6. reversal works
python3 scripts/insert_test_pending_job.py --remove
curl -sf -u "$BASIC" 'http://localhost:8200/earlscheibconcord/queue?status=all' | python3 -c 'import json,sys; d=json.load(sys.stdin); assert not any("ULH test row" in (r.get("name") or "") for r in d), "test row still present after --remove"; print("OK: test row removed cleanly")'

# 7. re-insert for final state (so the SUMMARY capture + human review has a visible row)
python3 scripts/insert_test_pending_job.py
echo "OK: test row present for review"
</automated>
  </verify>
  <done>
- scripts/insert_test_pending_job.py exists, is executable, and uses only stdlib
- Running with no args inserts one realistic pending row (valid VIN, real-looking vehicle_desc, TEST_PHONE_OVERRIDE number, send_at 7 days future)
- The test row is visible via GET /queue (default) AND GET /queue?status=all
- send_at is far enough in the future that IMMEDIATE_SEND_FOR_TESTING cannot fire it before human review
- Running with `--remove` deletes all test rows (tombstone = "ULH test row" substring in name) and prints a count
- Re-running INSERT is deterministic (no UNIQUE collision thanks to unix_ts in doc_id)
- Final state after this task: one test row present (re-inserted in step 7 of verify) for visual confirmation
  </done>
</task>

<task type="auto">
  <name>Task 3: Read-only diagnostic — why WIN-8I9KME32KLC stopped POSTing /estimate + SUMMARY.md</name>
  <files>.planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md</files>
  <action>
Produce a written, evidence-based diagnostic of Marco's Windows watcher silence. STRICTLY READ-ONLY — no changes to the Windows client, no config edits, no service restarts. This task exists because the 260424-oyk SUMMARY flagged `WIN-8I9KME32KLC` heartbeating without any /estimate POSTs since Apr 20 17:34, and the question needs a grounded answer before we can decide whether to ship a fix.

Evidence-gathering commands (run all, capture outputs):

1. Heartbeat cadence — confirm the watcher is alive and the agent string / source IP:
   ```
   journalctl -u earl-scheib.service --since "2026-04-20" --no-pager | grep -i "WIN-8I9KME32KLC" | head -40
   journalctl -u earl-scheib.service --since "2026-04-20" --no-pager | grep -i "heartbeat" | grep -i "WIN-8I9KME32KLC" | tail -10
   ```

2. /estimate activity from the same source — in journalctl AND received_logs/webhook.log:
   ```
   journalctl -u earl-scheib.service --since "2026-04-19" --no-pager | grep -iE "POST /earlscheibconcord/estimate" | tail -30
   ls -lat received_logs/ | head -30
   tail -200 received_logs/webhook.log 2>/dev/null || ls received_logs/
   ```

3. Last DB ingestion row(s) — the jobs table is the ground truth for what /estimate actually did:
   ```
   sqlite3 jobs.db "SELECT id, doc_id, job_type, phone, name, created_at, estimate_key FROM jobs ORDER BY created_at DESC LIMIT 15"
   sqlite3 jobs.db "SELECT MIN(created_at), MAX(created_at), COUNT(*) FROM jobs"
   ```

4. Any 4xx/5xx responses to WIN-8I9KME32KLC between Apr 20 and now — a silent failure would show here:
   ```
   journalctl -u earl-scheib.service --since "2026-04-20" --no-pager | grep -iE "WIN-8I9KME32KLC.*(400|401|403|500|503)" | head -20
   journalctl -u earl-scheib.service --since "2026-04-20" --no-pager | grep -iE "signature|hmac|invalid" | tail -20
   ```

5. Correlate the last /estimate POST with the jobs table and the last EMS payload on disk:
   ```
   ls -lat payloads/ 2>/dev/null | head -10
   grep -iE "POST /earlscheibconcord/estimate" app.log 2>/dev/null | tail -20
   ```

After gathering evidence, create `.planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md` with this structure:

```
# 260424-ulh — Quick Task Summary

## Task
Fix /queue filter to show all jobs, insert a realistic pending test work order, and diagnose WIN-8I9KME32KLC /estimate silence.

## Outcome
- [brief 1-2 sentence outcome for each of the 3 deliverables]

## Changes
- app.py: [brief description of the ?status= change]
- scripts/insert_test_pending_job.py: [brief description + reversal usage]

## Verification
[Commands run + results summary]

## Windows watcher diagnostic (WIN-8I9KME32KLC)

### Evidence observed

**Heartbeats:** [first/last timestamp seen + cadence — e.g. "every 5 min from Apr 20 18:00 through Apr 24 17:xx"]

**Last /estimate POST:** [exact timestamp from logs AND exact timestamp of newest jobs.db row + doc_id]

**Failed requests between last /estimate and now:** [none / list of 4xx/5xx with timestamps]

**EMS payload files on disk (received_logs/ or payloads/):** [last mtime seen]

### Diagnosis

[4-8 sentences stating the MOST LIKELY root cause based on the evidence above, NOT a list of hypotheses. Reference specific log lines / timestamps. Rule out the alternatives that the evidence contradicts.]

### Recommended follow-up action (NOT executed in this task)

[One concrete, single-step next action — e.g. "Ask Marco to confirm whether he has opened any new estimates in CCC ONE since Apr 20" OR "Request the client-side ems_watcher.log tail from Marco's machine (C:\EarlScheibWatcher\ems_watcher.log)" — whatever the evidence points to.]

## Reversal
How to roll back if needed:
- Remove test row: `python3 scripts/insert_test_pending_job.py --remove`
- Revert /queue filter: `git revert <commit-hash>` (the ?status= addition is backwards-compatible; old clients were unaffected, so revert is low-risk)

## Artifacts
- app.py (modified)
- scripts/insert_test_pending_job.py (new)
- .planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md (this file)
```

Critical writing rules:
- Every claim in the diagnosis MUST cite a specific log line, timestamp, or DB row observed in evidence-gathering. No hedging with "probably" without a pointer.
- If the evidence is inconclusive, state that plainly and list the ONE question Marco needs to answer to close the loop — do not invent a root cause.
- Keep SUMMARY.md under 150 lines total. Executive density over exhaustiveness.
  </action>
  <verify>
<automated>
# 1. SUMMARY.md exists and mentions the required cross-cutting IDs
test -f .planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md

# 2. contains all three deliverables
grep -q "WIN-8I9KME32KLC" .planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md
grep -q "insert_test_pending_job" .planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md
grep -qE "status=(all|sent|pending)" .planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md

# 3. diagnosis section is non-empty (has at least one concrete timestamp or log line)
grep -qE "202[56]-04-[0-9]{2}" .planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md

# 4. has a Recommended follow-up action section
grep -qi "follow-up\|followup\|recommended" .planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md

# 5. under 200 lines (executive density)
[ $(wc -l < .planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md) -lt 200 ] || { echo "SUMMARY.md too long"; exit 1; }

echo "OK: SUMMARY.md complete"
</automated>
  </verify>
  <done>
- SUMMARY.md exists at the quick task directory
- Contains outcome summary for all three deliverables (filter fix, test row, watcher diagnostic)
- The watcher diagnostic section cites specific timestamps / log lines — not just hypotheses
- Every claim is backed by evidence from journalctl, received_logs, or jobs.db (no speculative reasoning without a pointer)
- Includes exactly ONE recommended follow-up action, concrete and single-step
- Includes reversal instructions for both the test row and the /queue change
- Under 200 lines total
  </done>
</task>

</tasks>

<verification>
**End-to-end proof:**

1. `systemctl is-active earl-scheib.service` returns `active`
2. `curl -u $USER:$PASS http://localhost:8200/earlscheibconcord/queue` returns ~4 pending rows (3 original + 1 test row)
3. `curl -u $USER:$PASS 'http://localhost:8200/earlscheibconcord/queue?status=all'` returns ~24 rows (all 23 real + 1 test row)
4. `curl -u $USER:$PASS 'http://localhost:8200/earlscheibconcord/queue?status=sent'` returns exactly 20 rows
5. `curl -u $USER:$PASS 'http://localhost:8200/earlscheibconcord/queue?status=bogus'` returns HTTP 400
6. The test row is visible in both default and ?status=all responses with `name` containing "ULH test row"
7. `python3 scripts/insert_test_pending_job.py --remove` followed by the same remove command is idempotent (second call reports 0 deletions)
8. SUMMARY.md exists and contains concrete timestamps for the Windows watcher diagnostic

**Backwards-compatibility check (do NOT skip):**
- The default behavior (no query param) MUST return pending-only — the Go admin proxy and existing shared main.js assume this. Verify by counting rows before and after the change with NO query param: count must match 3 (the pending rows that existed at planning time) + 1 (the new test row) = 4. If it returns more rows without the query param, the change has broken the implicit contract.
</verification>

<success_criteria>
Quick task is complete when:

- [ ] `app.py` /earlscheibconcord/queue handler accepts optional ?status=all|sent|pending, defaults to pending, rejects anything else with 400
- [ ] Default response shape (bare JSON array, same column list) is UNCHANGED — Go admin proxy and shared main.js continue to work without modification
- [ ] `scripts/insert_test_pending_job.py` exists, inserts a realistic pending test row with send_at 7 days in the future, and supports `--remove` for clean reversal
- [ ] The test row is visible via GET /queue (default) AND GET /queue?status=all
- [ ] `.planning/quick/260424-ulh-.../260424-ulh-SUMMARY.md` exists with an evidence-backed diagnosis of WIN-8I9KME32KLC /estimate silence (not a hypothesis list) and a single recommended follow-up action
- [ ] Service is healthy (`systemctl is-active earl-scheib.service` = active)
- [ ] Three atomic commits, one per task, each with conventional-commit prefix (fix:, feat:, docs:)
</success_criteria>

<output>
After completion, `.planning/quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/260424-ulh-SUMMARY.md` should contain the evidence-based watcher diagnostic plus outcome notes for all three tasks. Update `.planning/STATE.md` "Quick Tasks Completed" table with the new row (date 2026-04-24, commit hash, directory link).
</output>
