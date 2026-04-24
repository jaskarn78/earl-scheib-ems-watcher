---
quick_id: 260424-oyk
type: quick
description: Pre-stage Twilio WhatsApp-sandbox -> production-SMS migration on a dedicated branch (no master merge; no env edits)
wave: 1
autonomous: true
files_modified:
  - app.py
must_haves:
  truths:
    - "A branch 'twilio-prod-sms-migration' exists locally and on origin containing exactly one code-change commit"
    - "app.py line 597 reads `from_number = TWILIO_FROM` (no `whatsapp:` prefix on the From channel)"
    - "The surrounding migration docblock (lines 586-596) remains untouched"
    - "git diff against master shows exactly 1 file changed, 1 insertion, 1 deletion (app.py only)"
    - "Working tree returns to master at end of task so subsequent sessions are not sitting on the branch"
    - "No .env file, no secret material, no unrelated files (commands.json, dist/*, update_paused) were staged or committed"
    - "SUMMARY.md includes the paint-by-numbers flip-day runbook with rollback, commit hash, and the `delivered` vs `sent` distinction"
  artifacts:
    - path: "app.py"
      provides: "Twilio _send_single_sms with from_number unwrapped (SMS-ready)"
      contains: "from_number = TWILIO_FROM"
    - path: ".planning/quick/260424-oyk-pre-stage-twilio-whatsapp-sandbox-to-pro/260424-oyk-SUMMARY.md"
      provides: "Flip-day runbook, rollback steps, commit hash, open-issues boundary"
  key_links:
    - from: "app.py line 597"
      to: "Twilio REST API"
      via: "urlencode({'From': from_number, ...}) -> https://api.twilio.com/.../Messages.json"
      pattern: "from_number\\s*=\\s*TWILIO_FROM"
    - from: "branch twilio-prod-sms-migration"
      to: "origin/twilio-prod-sms-migration"
      via: "git push -u origin twilio-prod-sms-migration"
      pattern: "tracking branch set"
---

# Objective

Pre-stage the one-line fix that removes the `whatsapp:` channel wrapper from `from_number` in `app.py:597`, isolating the change on branch `twilio-prod-sms-migration`. Push the branch to origin but do NOT open a PR and do NOT merge. The env-var flip (four new Twilio values) happens later — at that point merging the branch and restarting the service completes the migration atomically.

Purpose: Decouple the code change from the env-var change so neither half can ship alone and break production sends. Marco's sandbox session expires every 24h (Twilio errors 63015/63016 confirmed); the sandbox number `+14155238886` cannot be used as a real-SMS sender (21660). Branch-then-flip ensures one switch flip on cutover day.

Output:
- A single commit on `twilio-prod-sms-migration` with the exact 1-line diff
- The branch pushed to origin, tracked, no PR
- SUMMARY.md containing the flip-day runbook
- Working tree returned to master

# Context

@.planning/STATE.md
@CLAUDE.md
@app.py

## Interfaces (target site in app.py, lines 583-620)

Executor should NOT re-read beyond the already-loaded 580-620 slice.

Line-numbered view of the function `_send_single_sms` as of the starting state of this task:

```
583  def _send_single_sms(to: str, body: str) -> bool:
584      """Deliver one SMS/WhatsApp message via Twilio REST API."""
585      url = f"https://api.twilio.com/2010-04-01/Accounts/{TWILIO_ACCOUNT_SID}/Messages.json"
586      # ===== Twilio WhatsApp (sandbox) -> SMS (production) switch =====
587      # Currently using Twilio WhatsApp sandbox for dev/test.
588      # To switch to production SMS:
589      #   1. In .env, change TWILIO_FROM from "whatsapp:+14155238886" to your
590      #      Twilio SMS number (e.g. "+15551234567")
591      #   2. In this file, remove the "whatsapp:" prefix from both `from_number` and
592      #      `to_number` assignments below (remove the "whatsapp:" prefix from To/From
593      #      in the Twilio API call)
594      # No other changes needed. The rest of the scheduler, HMAC validation, and
595      # dedup logic is SMS/WhatsApp agnostic.
596      # ================================================================
597      from_number = f"whatsapp:{TWILIO_FROM}"          <-- LINE 597: CHANGE THIS LINE ONLY
598      to_number = f"whatsapp:{to}"                      <-- LINE 598: DO NOT TOUCH IN THIS QUICK TASK
599
600      # URL-encode each field — body contains spaces, apostrophes, parens,
...
604      data = urlencode({"From": from_number, "To": to_number, "Body": body}).encode("utf-8")
```

**CRITICAL:** `to_number` on line 598 is intentionally left alone per task_boundary — only the `from_number` on line 597 is changed in this pre-stage. The docblock on lines 586-596 stays intact.

# Tasks

## Task 1: Create feature branch from master (type: auto)

**Files:** (git metadata only — no file changes)

**Action:**

Verify starting state, then create and check out a new branch.

1. Run `git status --short` and confirm the current working tree has no staged changes. Unstaged/untracked entries like `commands.json`, `dist/*`, `update_paused`, `.env` may be present — these MUST NOT be staged at any point in this task. Do not run `git add -A`, `git add .`, or `git stash` (stashing could pull in the update_paused sentinel and mask it).
2. Run `git rev-parse --abbrev-ref HEAD` and confirm the current branch is `master` (if not, run `git checkout master` — but do NOT use `-f` or `--discard-changes`).
3. Run `git checkout -b twilio-prod-sms-migration`.
4. Run `git rev-parse --abbrev-ref HEAD` and confirm output is `twilio-prod-sms-migration`.

Rationale for pre-flight check: the repo is known to have unstaged artifacts (update_paused sentinel, dist/ build output, commands.json) from unrelated in-flight work. Those must remain unstaged throughout this quick task so they are not accidentally included in the Twilio commit.

**Verify:**
- `git rev-parse --abbrev-ref HEAD` prints `twilio-prod-sms-migration`
- `git diff --cached --stat` prints nothing (empty staging area)

**Done:** Current branch is `twilio-prod-sms-migration`; no files are staged; unstaged/untracked entries from the prior state are untouched.

---

## Task 2: Apply surgical one-line edit to app.py line 597 (type: auto)

**Files:** `app.py`

**Action:**

Perform the exact string replacement at line 597.

1. Use the Read tool on `app.py` with `offset: 595, limit: 5` to confirm the current text of line 597 is literally `    from_number = f"whatsapp:{TWILIO_FROM}"` (four-space indent, exactly that text) before invoking Edit.
2. Use the Edit tool on `app.py`:
   - `old_string`: `    from_number = f"whatsapp:{TWILIO_FROM}"`
   - `new_string`: `    from_number = TWILIO_FROM`
   - Preserve the leading four-space indent exactly. Edit will fail if the string is not unique, which is desired — it confirms the target is unambiguous.
3. Do NOT modify:
   - Line 598 (`to_number = f"whatsapp:{to}"`) — the `to_number` wrapper is intentionally left for a separate future task (changing it also requires validating that recipient phones stored in pending jobs are already in E.164 format; out of scope for this pre-stage).
   - Lines 586-596 (the migration docblock) — leave the comment intact as informative code-level documentation.
   - Any other `from_number` or `TWILIO_FROM` references elsewhere in the file.
4. Do NOT touch `.env`, `commands.json`, `update_paused`, `dist/*`, or any other file.

**Why `f"whatsapp:{TWILIO_FROM}"` -> `TWILIO_FROM` and not `f"{TWILIO_FROM}"`:** the f-string with only one substitution and no prefix/suffix literal has no purpose — plain variable assignment is clearer and matches the style of the rest of the module. This is also what the existing docblock implicitly prescribes ("remove the `whatsapp:` prefix").

**Verify:**
- Python syntax: `python3 -c "import ast; ast.parse(open('app.py').read())"` exits 0
- Stat check: `git diff --stat app.py` shows exactly `1 file changed, 1 insertion(+), 1 deletion(-)`
- Presence: `grep -n 'from_number = TWILIO_FROM' app.py` returns exactly one match
- Absence: `grep -n 'from_number = f"whatsapp:' app.py` returns zero matches (exit code 1)
- Docblock intact: `grep -cF 'Twilio WhatsApp (sandbox) -> SMS (production)' app.py` returns 1

**Done:** app.py compiles; diff is exactly 1 insertion + 1 deletion; only line 597 changed; migration docblock preserved; no other files modified.

---

## Task 3: Verify no unrelated files are in the change set (type: auto)

**Files:** (git metadata only — no file changes)

**Action:**

Confirm the working-tree change is isolated to `app.py` before staging.

1. Run `git diff --name-only` (unstaged) — the ONLY file listed MUST be `app.py`. If any other file appears (notably `.env`, `commands.json`, `update_paused`, `dist/*`, anything under `.planning/`), STOP and escalate — do not attempt to fix by staging selectively; the task boundary has been violated.
2. Run `git diff --cached --name-only` (staged) — should be empty (we have not staged anything yet).
3. Run `git diff app.py` and visually confirm the diff contains:
   - Exactly one `-` line: `-    from_number = f"whatsapp:{TWILIO_FROM}"`
   - Exactly one `+` line: `+    from_number = TWILIO_FROM`
   - No other `-`/`+` lines besides diff headers (`---`/`+++`/`@@`).
4. No-live-whatsapp-reference check (informational): `grep -n '"whatsapp:' app.py` may still return matches — those are allowed only inside the comment block on lines 589-593. Confirm every match line number is in range 586-596 (docblock) and no match is inside a live `f"whatsapp:...` expression.

**Verify:**
- `git diff --name-only` output equals literally `app.py` (single line)
- `git diff --cached --name-only` output is empty
- `git diff app.py | grep -E '^[-+][^-+]' | wc -l` equals `2`

**Done:** Only `app.py` is dirty; the diff is a 1-line swap; no staged changes yet; no whatsapp: live code references remain.

---

## Task 4: Stage, commit, and verify commit isolation (type: auto)

**Files:** `app.py` (commit only; content already changed in Task 2)

**Action:**

Stage and commit ONLY `app.py`.

1. Run `git add app.py` (explicit path — do NOT use `git add .`, `git add -A`, or `git add -u`).
2. Run `git status --short` and confirm:
   - Exactly one `M  app.py` entry in staged (left column `M`, right column space).
   - Unstaged/untracked entries for `commands.json`, `dist/*`, `update_paused` etc. are still present but NOT staged.
3. Commit with the following message (use a heredoc to avoid shell-escaping issues):

   ```
   git commit -m "$(cat <<'EOF'
   fix(twilio): remove whatsapp: prefix, use TWILIO_FROM as SMS sender

   Pre-stages the WhatsApp-sandbox -> production-SMS migration. Drops the
   f"whatsapp:{TWILIO_FROM}" wrapper on the From channel so Twilio routes
   TWILIO_FROM as a regular SMS number. to_number wrapper is left intact
   and will be addressed in a follow-up once recipient-phone E.164
   validation is verified.

   This commit alone does NOT switch production — the four TWILIO_* env
   vars in .env must be repointed to the purchased SMS number at the same
   time this branch merges to master. See SUMMARY.md flip-day runbook.
   EOF
   )"
   ```

4. Run `git log -1 --stat` and confirm:
   - Exactly one file listed: `app.py`
   - `1 insertion(+), 1 deletion(-)`
5. Capture the commit hash: `git rev-parse HEAD` — record for SUMMARY.md (Task 6).

**Verify:**
- `git log -1 --name-only --pretty=format:%s` output first line starts with `fix(twilio):` and second+ lines list only `app.py`
- `git log -1 --stat | tail -1` matches `1 insertion(+), 1 deletion(-)`
- `git status --short | grep -E '^M' | wc -l` equals `0` (nothing staged post-commit)

**Done:** A single commit on `twilio-prod-sms-migration` touches only `app.py` with a 1-line diff; commit message follows conventional-commits format; commit hash captured.

---

## Task 5: Push branch to origin (no PR, no merge) (type: auto)

**Files:** (git metadata only — no file changes)

**Action:**

Publish the branch to origin so the commit survives local-machine loss and is reachable on flip-day from any clone.

1. Run `git push -u origin twilio-prod-sms-migration`.
2. If push succeeds, verify tracking: `git rev-parse --abbrev-ref --symbolic-full-name @{u}` should print `origin/twilio-prod-sms-migration`.
3. Verify remote has the commit: `git ls-remote origin twilio-prod-sms-migration` returns the same hash as local HEAD.
4. Do NOT:
   - Run `gh pr create` (explicitly out of scope — PR is not desired)
   - Run `git push origin master` (master is unchanged and should stay that way)
   - Run `git push --force` anywhere
   - Merge the branch into master locally

If push fails with auth error, STOP and surface the error to the user — do not retry with alternative remotes or credentials.

**Verify:**
- `git rev-parse origin/twilio-prod-sms-migration` equals `git rev-parse HEAD` (both return same hash)
- `git rev-parse --abbrev-ref --symbolic-full-name @{u}` outputs `origin/twilio-prod-sms-migration`

**Done:** Branch pushed to origin with upstream tracking; local and remote hashes match; no PR opened; master untouched on both local and remote.

---

## Task 6: Write SUMMARY.md with flip-day runbook (type: auto)

**Files:** `.planning/quick/260424-oyk-pre-stage-twilio-whatsapp-sandbox-to-pro/260424-oyk-SUMMARY.md`

**Action:**

Create the SUMMARY with operational content needed to execute the flip later without re-loading session context. MUST include every section below. MUST NOT include any secret material (no Twilio credentials, no HMAC secrets, no .env contents — only placeholder variable names).

Required sections (use these exact headings):

### Outcome
- Branch name: `twilio-prod-sms-migration`
- Commit hash: (captured from Task 4 — paste the full 40-char SHA)
- Diff scope: 1 file (app.py), 1 insertion, 1 deletion
- Pushed to: `origin/twilio-prod-sms-migration`
- No PR opened. Master branch unchanged locally and on origin.

### The One-Line Diff
Show the before/after (quote the existing docblock context lines 586-596 briefly for orientation, then the single-line change):

```diff
-    from_number = f"whatsapp:{TWILIO_FROM}"
+    from_number = TWILIO_FROM
```

Note: `to_number` on line 598 intentionally unchanged (separate follow-up for E.164 validation of recipient phones).

### Why Branch, Not Direct-to-Master
User does not yet have the four new Twilio values (`TWILIO_ACCOUNT_SID`, `TWILIO_API_KEY`, `TWILIO_API_SECRET`, `TWILIO_FROM`). Committing code alone to master would leave production pointed at the sandbox number `+14155238886`, which cannot send real SMS (Twilio error 21660). Branch-then-flip = one atomic cutover.

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

6. **Canned Twilio API smoke test** (sources credentials from .env — DO NOT hardcode values into this runbook or paste output back to session logs):
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
- For a newly purchased 10DLC number with no traffic history, carriers (T-Mobile/AT&T/Verizon) may silently filter messages as suspected spam even though Twilio returns `sent`. Only `delivered` confirms real end-to-end delivery.

### Rollback
```
git revert <commit_hash>
git push origin master
sudo systemctl restart earl-scheib.service
```
This restores `from_number = f"whatsapp:{TWILIO_FROM}"`. The `.env` change must also be reverted separately (restore `TWILIO_FROM=whatsapp:+14155238886` from backup before service restart) or the service will error on every send.

### A2P 10DLC Note (separate follow-up, NOT part of this quick task)
If the flip-day smoke test fails with `status=failed` on a freshly purchased 10DLC number, the most likely cause is carrier filtering for unregistered A2P traffic. Registration is in Twilio Console -> Messaging -> Regulatory Compliance -> A2P 10DLC. Registration can take hours-to-weeks depending on vetting tier. Track as its own quick task; do not absorb into this Twilio-flip runbook.

### Unrelated Open Issues (explicitly NOT in scope of this branch)
- **Self-update hash-comparison bug** — `update_paused` sentinel file is the current band-aid; sentinel is deliberately untouched by this task.
- **`/queue` endpoint hardcoded `WHERE sent=0`** — lifecycle filters return empty on the admin UI; tracked separately.
- **`to_number` WhatsApp wrapper at app.py:598** — will be addressed in a follow-up once recipient-phone E.164 validation path is verified.

### Working Tree State at End of Task
After Task 7 runs `git checkout master`, `git rev-parse --abbrev-ref HEAD` returns `master`. The next session starts on master, not on `twilio-prod-sms-migration`.

**Verify:**
- File exists at `.planning/quick/260424-oyk-pre-stage-twilio-whatsapp-sandbox-to-pro/260424-oyk-SUMMARY.md`
- `grep -c '^### ' .planning/quick/260424-oyk-pre-stage-twilio-whatsapp-sandbox-to-pro/260424-oyk-SUMMARY.md` returns at least `9` (nine required subsections present)
- Runbook step count: `grep -cE '^[1-8]\. \*\*' .planning/quick/260424-oyk-pre-stage-twilio-whatsapp-sandbox-to-pro/260424-oyk-SUMMARY.md` returns `8`
- No secret leakage: `grep -iE '(ACbbbbbbb|SK[a-z0-9]{10,}|password=|secret=)' .planning/quick/260424-oyk-pre-stage-twilio-whatsapp-sandbox-to-pro/260424-oyk-SUMMARY.md` returns zero matches (use literal regex letters; the check is for shapes of real creds, not variable names — `$TWILIO_API_KEY` as a placeholder is fine)
- Commit hash present: `grep -cE '\b[a-f0-9]{7,40}\b' .planning/quick/260424-oyk-pre-stage-twilio-whatsapp-sandbox-to-pro/260424-oyk-SUMMARY.md` returns at least `1`

**Done:** SUMMARY.md exists, contains all required subsections, embeds the real commit hash, includes the 8-step flip-day runbook + rollback + `delivered`-vs-`sent` explanation + unrelated-issues boundary note.

---

## Task 7: Return working tree to master (type: auto)

**Files:** (git metadata only — no file changes)

**Action:**

Leave a clean handoff state so the next session does not unexpectedly operate on the feature branch.

1. Run `git checkout master`.
2. Verify: `git rev-parse --abbrev-ref HEAD` returns `master`.
3. Verify `app.py` on master is unchanged from its pre-task state: `grep -n 'from_number = f"whatsapp:{TWILIO_FROM}"' app.py` should match on line 597 (the original text), and `grep -n 'from_number = TWILIO_FROM' app.py` should NOT match on master.
4. Verify the feature branch still exists: `git branch --list twilio-prod-sms-migration` returns that branch name.
5. Do NOT delete the local branch; it must remain locally AND on origin for flip-day.

**Verify:**
- `git rev-parse --abbrev-ref HEAD` outputs `master`
- `git branch --list twilio-prod-sms-migration` outputs `  twilio-prod-sms-migration`
- `grep -cF 'from_number = f"whatsapp:{TWILIO_FROM}"' app.py` returns `1` (master still has the old code)

**Done:** HEAD is on master; master's app.py is untouched; feature branch preserved locally; remote branch preserved on origin; task complete.

# Verification (overall)

1. `git log master..twilio-prod-sms-migration --oneline` shows exactly 1 commit.
2. `git diff master twilio-prod-sms-migration --stat` shows `1 file changed, 1 insertion(+), 1 deletion(-)` and the file is `app.py`.
3. `git rev-parse origin/twilio-prod-sms-migration` equals the commit from step 1.
4. `git rev-parse --abbrev-ref HEAD` returns `master`.
5. `.planning/quick/260424-oyk-pre-stage-twilio-whatsapp-sandbox-to-pro/260424-oyk-SUMMARY.md` exists with all required sections.
6. No secret material appears in any committed or written file (SUMMARY, PLAN, commit message).
7. Unrelated unstaged entries (`update_paused`, `commands.json`, `dist/*`, `.env`) are still present in the working tree and still unstaged.

# Success Criteria

- [ ] Branch `twilio-prod-sms-migration` exists locally and on origin
- [ ] Exactly one commit on branch, touching only `app.py`, 1+/1-
- [ ] Commit message follows `fix(twilio): remove whatsapp: prefix, use TWILIO_FROM as SMS sender`
- [ ] `app.py` on branch has `from_number = TWILIO_FROM` at line 597; no `from_number = f"whatsapp:` anywhere
- [ ] `app.py` docblock on lines 586-596 preserved intact
- [ ] `.env` never touched (not read, not edited, not staged)
- [ ] `update_paused`, `commands.json`, `dist/*` never staged or committed
- [ ] SUMMARY.md contains: branch name, commit hash, 1-line diff, 8-step flip-day runbook, rollback, `delivered` vs `sent` explanation, A2P 10DLC note, unrelated-issues boundary
- [ ] No secret material (API keys, passwords, tokens) anywhere in PLAN, SUMMARY, or commit message
- [ ] Working tree returned to master at end

# Output

After completion:
- `.planning/quick/260424-oyk-pre-stage-twilio-whatsapp-sandbox-to-pro/260424-oyk-SUMMARY.md` (written in Task 6)
- Branch `twilio-prod-sms-migration` on origin with one commit
- Master branch unchanged
- HEAD on master
