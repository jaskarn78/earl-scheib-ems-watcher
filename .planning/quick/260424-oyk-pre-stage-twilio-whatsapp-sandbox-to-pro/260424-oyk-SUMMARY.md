---
quick_id: 260424-oyk
type: quick
status: complete
completed: 2026-04-24
branch: twilio-prod-sms-migration
commit: 54cb771af6839ef2bf2f86ea7d30bbf4234b5152
files_modified:
  - app.py
---

# Quick Task 260424-oyk: Pre-Stage Twilio WhatsApp-Sandbox -> Production-SMS Migration

One-liner: Isolated a one-line `f"whatsapp:{TWILIO_FROM}"` -> `TWILIO_FROM` edit on branch `twilio-prod-sms-migration`, pushed to origin, zero master impact; flip-day merges branch + repoints four `TWILIO_*` env vars atomically.

### Outcome
- Branch name: `twilio-prod-sms-migration`
- Commit hash: `54cb771af6839ef2bf2f86ea7d30bbf4234b5152`
- Diff scope: 1 file (`app.py`), 1 insertion, 1 deletion
- Pushed to: `origin/twilio-prod-sms-migration` (upstream tracking set)
- No PR opened. Master branch unchanged locally and on origin.

### The One-Line Diff

Context — the docblock on lines 586-596 of `app.py` (unchanged by this commit) prescribes the migration path:

```
# ===== Twilio WhatsApp (sandbox) -> SMS (production) switch =====
# Currently using Twilio WhatsApp sandbox for dev/test.
# To switch to production SMS:
#   1. In .env, change TWILIO_FROM from "whatsapp:+14155238886" to your
#      Twilio SMS number (e.g. "+15551234567")
#   2. In this file, remove the "whatsapp:" prefix from both `from_number` and
#      `to_number` assignments below (remove the "whatsapp:" prefix from To/From
#      in the Twilio API call)
# No other changes needed. The rest of the scheduler, HMAC validation, and
# dedup logic is SMS/WhatsApp agnostic.
# ================================================================
```

The one-line change at `app.py:597`:

```diff
-    from_number = f"whatsapp:{TWILIO_FROM}"
+    from_number = TWILIO_FROM
```

Note: `to_number` on line 598 is intentionally unchanged (separate follow-up for E.164 validation of recipient phones stored in pending jobs).

### Why Branch, Not Direct-to-Master
User does not yet have the four new Twilio values (`TWILIO_ACCOUNT_SID`, `TWILIO_API_KEY`, `TWILIO_API_SECRET`, `TWILIO_FROM`). Committing code alone to master would leave production pointed at the sandbox number `+14155238886`, which cannot send real SMS (Twilio error 21660) and whose sandbox session expires every 24h (Twilio errors 63015/63016). Branch-then-flip = one atomic cutover where merge + env swap + restart happen together.

### Flip-Day Runbook (paint-by-numbers, do all 8 steps in order)

1. **Obtain 4 new Twilio values** (user action in Twilio Console):
   - `TWILIO_ACCOUNT_SID` — starts with `AC...`
   - `TWILIO_API_KEY` — starts with `SK...` (create via Console -> Account -> API keys & tokens; use "Standard" key)
   - `TWILIO_API_SECRET` — shown once at API-key creation; store in password manager
   - `TWILIO_FROM` — E.164 format, e.g. `+15551234567` — the purchased SMS-capable Twilio number

2. **Update `.env` surgically** at `/home/jjagpal/projects/earl-scheib-followup/.env`:
   - Replace ONLY those four variables.
   - PRESERVE all other vars: `TEST_PHONE_RECIPIENTS`, `IMMEDIATE_SEND_FOR_TESTING`, `CCC_SECRET`, `ADMIN_UI_USER`, `ADMIN_UI_PASSWORD`, and anything else present.
   - Do not commit `.env` to git (it should already be in `.gitignore`; verify if unsure).

3. **Merge the branch to master:**
   ```
   git checkout master
   git merge --ff-only twilio-prod-sms-migration
   git push origin master
   ```
   The `--ff-only` flag guarantees no merge commit is created; if fast-forward is not possible, STOP and investigate (master diverged).

4. **Restart the service:**
   ```
   sudo systemctl restart earl-scheib.service
   ```

5. **Verify service healthy:**
   ```
   systemctl is-active earl-scheib.service
   # expected: active

   ss -tlnp | grep :8200
   # expected: a listening line for the service
   ```

6. **Canned Twilio API smoke test** (sources credentials from `.env` — DO NOT hardcode values into this runbook or paste output back to session logs):
   ```
   set -a && . /home/jjagpal/projects/earl-scheib-followup/.env && set +a
   SID_RESP=$(curl -sS -u "$TWILIO_API_KEY:$TWILIO_API_SECRET" \
     -X POST "https://api.twilio.com/2010-04-01/Accounts/$TWILIO_ACCOUNT_SID/Messages.json" \
     --data-urlencode "From=$TWILIO_FROM" \
     --data-urlencode "To=+15308450190" \
     --data-urlencode "Body=Prod SMS test from Earl Scheib watcher")
   SID=$(echo "$SID_RESP" | python3 -c "import sys,json; print(json.load(sys.stdin)['sid'])")
   sleep 10
   curl -sS -u "$TWILIO_API_KEY:$TWILIO_API_SECRET" \
     "https://api.twilio.com/2010-04-01/Accounts/$TWILIO_ACCOUNT_SID/Messages/$SID.json" \
     | python3 -c "import sys,json; d=json.load(sys.stdin); print('status=', d['status'], 'error=', d.get('error_code'))"
   ```

7. **Observable success:** Smoke test prints `status= delivered` (NOT just `status= sent`) AND the phone at +1-530-845-0190 physically receives the message.

8. **On failure** (`status=failed` or `undelivered`): Execute rollback (next section).

### `delivered` vs `sent` — Why the Distinction Matters
- `sent` = Twilio handed the message to the carrier. Twilio's job is done from their side.
- `delivered` = the carrier reported back that the message landed on the handset.
- For a newly purchased 10DLC number with no traffic history, carriers (T-Mobile/AT&T/Verizon) may silently filter messages as suspected spam even though Twilio returns `sent`. Only `delivered` confirms real end-to-end delivery. Treat `sent` as a yellow light, not green.

### Rollback
```
git revert 54cb771af6839ef2bf2f86ea7d30bbf4234b5152
git push origin master
sudo systemctl restart earl-scheib.service
```
This restores `from_number = f"whatsapp:{TWILIO_FROM}"`. The `.env` change must also be reverted separately (restore `TWILIO_FROM=whatsapp:+14155238886` from backup before service restart) or the service will error on every send. Keep a timestamped `.env` backup made in step 2 so rollback is mechanical.

### A2P 10DLC Note (separate follow-up, NOT part of this quick task)
If the flip-day smoke test fails with `status=failed` on a freshly purchased 10DLC number, the most likely cause is carrier filtering for unregistered A2P traffic. Registration is in Twilio Console -> Messaging -> Regulatory Compliance -> A2P 10DLC. Registration can take hours-to-weeks depending on vetting tier. Track as its own quick task; do not absorb into this Twilio-flip runbook.

### Unrelated Open Issues (explicitly NOT in scope of this branch)
- **Self-update hash-comparison bug** — `update_paused` sentinel file is the current band-aid; sentinel is deliberately untouched by this task.
- **`/queue` endpoint hardcoded `WHERE sent=0`** — lifecycle filters return empty on the admin UI; tracked separately.
- **`to_number` WhatsApp wrapper at `app.py:598`** — will be addressed in a follow-up once recipient-phone E.164 validation path is verified.

### Working Tree State at End of Task
After Task 7 runs `git checkout master`, `git rev-parse --abbrev-ref HEAD` returns `master`. The next session starts on master, not on `twilio-prod-sms-migration`. The feature branch is preserved both locally (`git branch --list twilio-prod-sms-migration`) and on origin (`git ls-remote origin twilio-prod-sms-migration`) for flip-day.
