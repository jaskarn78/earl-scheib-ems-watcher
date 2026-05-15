---
quick_id: 260515-uid
slug: go-live-with-real-customer-sms-remove-te
date: 2026-05-15
status: in-progress
---

# Quick task: Go live with real-customer SMS

## Goal

Remove the test-mode safety net so SMS reach actual customer phones instead of fanning out to Jas (+15308450190) and Marco (+19254215772). Manual-send only — `SCHEDULER_ENABLED` stays unset.

## Preconditions (confirmed by user 2026-05-15)

- A2P 10DLC registration: **complete** (Twilio campaign APPROVED for `+19256033934`)
- Marco signed off on real-customer sends: **yes**
- Scheduler stays off: **yes** (manual `/queue/send-now` only for the first wave)

## Three gates currently blocking real-customer SMS

1. `/opt/esw/app/.env` — `TEST_PHONE_RECIPIENTS=+15308450190,+19254215772` fans every send to Jas + Marco (Pi-only, git-ignored)
2. `/opt/esw/app/.env` — `TEST_PHONE_OVERRIDE=+15308450190` (redundant given #1 but still present)
3. `app.py:98` — `SMS_ALLOWLIST = {"+15308450190", "+19254215772"}` hardcoded fail-closed guard

## Tasks

### 1. Repo: clear SMS_ALLOWLIST in app.py
- Edit `app.py:98`: `SMS_ALLOWLIST = {"+15308450190", "+19254215772"}` → `SMS_ALLOWLIST = set()`
- Update inline comment to note the gate is now open per user sign-off 2026-05-15
- Commit + push to `origin/master`

### 2. Pi: disable env-level test redirects
- SSH `esw@100.107.40.59`
- Backup: `cp /opt/esw/app/.env /opt/esw/app/.env.bak-pre-golive-$(date +%s)`
- Comment out `TEST_PHONE_OVERRIDE` and `TEST_PHONE_RECIPIENTS` (preserve lines so we can flip back)
- Update the "do NOT remove until A2P" guard comment to reflect the 2026-05-15 sign-off

### 3. Pi: deploy + restart
- `cd /opt/esw/app && git pull --ff-only`
- `sudo systemctl restart earlscheib.service`
- `systemctl status earlscheib.service` — confirm `active (running)` with no crash loop
- `journalctl -u earlscheib.service -n 50 --no-pager` — confirm clean startup, no Twilio config errors

### 4. Verify gates are actually open
- Hit `/healthz` (or equivalent) to confirm process is serving
- Inspect process environment to verify `TEST_PHONE_*` are unset
- Quick code sanity: `grep '^SMS_ALLOWLIST' /opt/esw/app/app.py` shows `set()`
- Do NOT send a live test SMS to a customer as part of verification — manual operator click via admin UI is the first real-customer send and is owned by Jas/Marco, not this task

## Rollback (if anything looks off)

- `cp /opt/esw/app/.env.bak-pre-golive-* /opt/esw/app/.env`
- `git revert <commit-sha>` in repo and pull on Pi
- `sudo systemctl restart earlscheib.service`

## Out of scope

- `SCHEDULER_ENABLED` stays unset (user said no)
- No customer-facing test SMS from this task
- No changes to webhook auth, admin UI, or templates
