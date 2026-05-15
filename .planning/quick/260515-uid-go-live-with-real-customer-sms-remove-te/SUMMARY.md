---
quick_id: 260515-uid
slug: go-live-with-real-customer-sms-remove-te
date: 2026-05-15
status: complete
---

# Summary: Go live with real-customer SMS

## Outcome

Three test-mode gates removed. Real customer phones from the queue now reach Twilio for the first time on this Pi deployment.

## What changed

### Repo (committed `22c809c`, pushed to origin/master)
- `app.py:98` — `SMS_ALLOWLIST = {"+15308450190", "+19254215772"}` → `SMS_ALLOWLIST = set()`
- Inline comment updated noting A2P + Marco sign-off on 2026-05-15

### Pi (`esw@100.107.40.59:/opt/esw/app/.env`, git-ignored)
- Backed up: `/opt/esw/app/.env.bak-pre-golive-1778882351`
- Commented out (kept lines for fast rollback):
  ```
  #TEST_PHONE_OVERRIDE=+15308450190
  #TEST_PHONE_RECIPIENTS=+15308450190,+19254215772
  ```
- Header comment rewritten to reflect the 2026-05-15 sign-off and how to re-enable test mode

### Deploy
- `git pull` on Pi pulled to `22c809c`
- `sudo systemctl restart earlscheib.service`
- Service `active (running)` since 14:59:51 PDT, listening on `0.0.0.0:8200`
- Running-process env confirmed via `/proc/<pid>/environ`: `TEST_PHONE_*` absent, `SCHEDULER_ENABLED` absent (stays gated off as requested), `TWILIO_FROM=+19256033934` (production long code)

## Out of scope (per user 2026-05-15)
- `SCHEDULER_ENABLED` left unset — manual `/queue/send-now` only for the first wave of real customer sends
- No automated test SMS sent — first real-customer send is owned by Jas/Marco via admin UI

## Historical impact — 6 customers whose messages were redirected

Between 2026-05-13 and 2026-05-15, six job rows were marked `sent=1` in the queue but the actual Twilio delivery was fanned out to Jas/Marco instead of the customer:

| Job ID | Customer        | Phone          | Type   | Fired at (local)    |
|--------|-----------------|----------------|--------|---------------------|
| 312    | Tammie Knight   | +14152151851   | 24h    | 2026-05-15 14:47:27 |
| 171    | John Harrison   | +19253256925   | 24h    | 2026-05-14 11:14:00 |
| 162    | JIM NELSON      | +19255884764   | 3day   | 2026-05-14 11:13:46 |
| 152    | Charles Deavers | +14152540287   | review | 2026-05-13 15:03:12 |
| 147    | Bobby Mazaheri  | +19258999527   | 3day   | 2026-05-13 15:21:03 |
| 143    | ANTHONY SMALLS  | +19254359099   | 3day   | 2026-05-13 15:28:05 |

Charles Deavers (id 152) also had 2 earlier `allowlist_blocked` failures on 2026-05-13 14:59 before the redirect fan-out path succeeded — Twilio never sent anything to the customer in either case.

**Recovery options** (user decision):
1. Click **Resend** on each row in the admin UI — `/queue/resend?id=N` runs through `send_sms()` which, now that gates are open, will deliver to the real customer phone.
2. Skip — if the lateness now exceeds the value of the message (e.g., a 24h follow-up sent 4 days late may feel weird), let it ride.

## Rollback

- `cp /opt/esw/app/.env.bak-pre-golive-1778882351 /opt/esw/app/.env`
- `git revert 22c809c` and pull on Pi
- `sudo systemctl restart earlscheib.service`
