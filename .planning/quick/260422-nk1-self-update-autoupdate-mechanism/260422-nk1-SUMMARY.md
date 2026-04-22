---
phase: quick-260422-nk1
plan: 01
subsystem: self-update
tags: [self-update, installer, hmac, auto-update, kill-switch]
requires:
  - webhook.Sign (internal/webhook)
  - HMAC empty-body GET pattern from internal/commands
  - EarlScheibWatcher-Setup.exe served from repo root by app.py
provides:
  - internal/update.Check (public API)
  - internal/update.DefaultLauncher (production installer exec)
  - GET /earlscheibconcord/version handler on app.py
  - /VERYSILENT-compatible installer (WizardSilent guards)
affects:
  - cmd/earlscheib/main.go (runScan now polls update.Check per cycle)
  - installer/earlscheib.iss (silent-mode short-circuits)
tech-stack:
  added: []
  patterns:
    - exitFn/sleepFn unexported package vars for test-speed injection (mirrors webhook.BackoffBase / db.RetryBaseDelay)
    - Atomic temp-file + rename for cooldown persistence
    - SHA256[:16] truncated hex as compact version identifier
    - Server-side kill-switch via env var OR sentinel file
key-files:
  created:
    - internal/update/update.go
    - internal/update/update_test.go
    - .planning/quick/260422-nk1-self-update-autoupdate-mechanism/260422-nk1-PLAN.md
    - .planning/quick/260422-nk1-self-update-autoupdate-mechanism/260422-nk1-SUMMARY.md
  modified:
    - app.py
    - cmd/earlscheib/main.go
    - installer/earlscheib.iss
    - Makefile
    - EarlScheibWatcher-Setup.exe
    - EarlScheibWatcher-Setup.zip
decisions:
  - Used stdlib only (crypto/sha256, net/http, os/exec) — no new go.mod deps
  - Cooldown 3600s + max 3 failures before bail; fail_count gate checked BEFORE cooldown gate
  - exitFn/sleepFn unexported package vars (not interface injection) — consistent with webhook.BackoffBase pattern
  - Installer served from repo root; app.py update_paused sentinel file shares same dir
  - Silent-upgrade path in .iss skips RegisterScheduledTask to avoid briefly deregistering the task during exe swap
  - Makefile pinned to amake/innosetup:latest (the 6.7.1 tag is no longer published on Docker Hub — deviation Rule 3)
metrics:
  duration_minutes: ~30
  completed_date: "2026-04-22"
---

# Phase quick-260422-nk1 Plan 01: Self-Update Auto-Update Mechanism — Summary

**One-liner:** Pull-based self-update client polls `/version` each scan, SHA256-verifies the new installer, and silently re-runs `/VERYSILENT` setup so Marco never reinstalls again.

## What Shipped

Five tasks landed across four source files, plus one release build:

1. **app.py** — New `GET /earlscheibconcord/version` handler. HMAC-auth (empty body) returns `{version: sha256[:16], download_url, paused}`. Kill-switch honoured via `AUTO_UPDATE_PAUSED=1` env var or `update_paused` sentinel file next to app.py.
2. **internal/update/** — New package with `Check()` + `DefaultLauncher()`. Runs once per scan: cooldown/fail-limit gate → HMAC-signed GET `/version` → compare `os.Executable()` SHA[:16] to server hash → download + SHA-verify → launch installer with `/VERYSILENT /NORESTART /SUPPRESSMSGBOXES /SP-` → `os.Exit(0)`. 6 table-driven tests exercise every branch (same-hash, match-launch, mismatch-reject, paused, cooldown-block, fail-limit-block) under `-race`.
3. **cmd/earlscheib/main.go** — Wired `update.Check(...)` into `runScan` after `commands.Poll/Handle`, before `sendFn`. Errors logged as Warn but never block the scan.
4. **installer/earlscheib.iss** — Added `WizardSilent()` short-circuits to `NextButtonClick` (skip folder/conn/checkbox validation), `CurStepChanged` (skip RegisterScheduledTask on silent upgrade so task isn't briefly deregistered), and `UninstallSilent()` to `UninstallInitialize` (preserve data dir by default).
5. **Release** — Rebuilt installer with HMAC secret baked in, staged to repo root, rebuilt zip via `/tmp` staging, pushed to origin/master, restarted app.py, verified live `/version` endpoint returns expected JSON.

## Live Verification

**Local vs live MD5 parity (step 9 of the plan):**

```
2d93d28c1afb1398b6cf13cb04d2d4c4  EarlScheibWatcher-Setup.exe         (local)
2d93d28c1afb1398b6cf13cb04d2d4c4  /tmp/live-pre-push-installer.exe    (live-served)

8927e500a4e07efd489eadd5df479640  EarlScheibWatcher-Setup.zip         (local)
8927e500a4e07efd489eadd5df479640  /tmp/v.zip                          (live via curl)
```

Both pairs match — the installer being served from support.jjagpal.me is byte-identical to the artifact committed to master.

**Installer SHA256[:16] (the "version" string clients compare against):**

```
5ab6724f09486b72
```

Confirmed by both:
- Python: `sha256(EarlScheibWatcher-Setup.exe)[:16]` computed locally
- `curl https://support.jjagpal.me/earlscheibconcord/version` returned `"version": "5ab6724f09486b72"`

**Live /version endpoint test (Task 5 verify):**

```bash
set -a; . .env; set +a
SIG=$(python3 -c "import hmac, hashlib, os; print(hmac.new(os.environ['CCC_SECRET'].encode(), b'', hashlib.sha256).hexdigest())")
curl -s -H "X-EMS-Signature: $SIG" https://support.jjagpal.me/earlscheibconcord/version
```

Response:

```json
{"version": "5ab6724f09486b72", "download_url": "/earlscheibconcord/download.exe", "paused": false}
HTTP 200
```

Without HMAC: `HTTP 401 {"error": "invalid signature"}`

**Kill-switch verification:**

```bash
touch /home/jjagpal/projects/earl-scheib-followup/update_paused
# → {"version": "5ab6724f09486b72", "download_url": "/earlscheibconcord/download.exe", "paused": true}
rm /home/jjagpal/projects/earl-scheib-followup/update_paused
# → {"version": "5ab6724f09486b72", "download_url": "/earlscheibconcord/download.exe", "paused": false}
```

Both transitions honoured within a single HTTP round-trip. The sentinel file is a zero-cost kill-switch — no redeploy required to halt rollout.

## Restart Command (Task 5 critical follow-up)

```bash
# Kill the old app.py on port 8200
kill $(ss -tlnp 2>/dev/null | grep ':8200' | awk -F'pid=' '{print $2}' | awk -F, '{print $1}')

# Relaunch via nohup (same pattern as yesterday's quick task)
nohup /usr/bin/python3 /home/jjagpal/projects/earl-scheib-followup/app.py >/tmp/app.out 2>&1 &
disown

# Verify
curl -s -o /dev/null -w 'HTTP %{http_code}\n' https://support.jjagpal.me/earlscheibconcord/status
# Expect: HTTP 200 (last_seen shows "never" briefly until Marco's next heartbeat repopulates it)
```

**Old PID:** 3204602 → **New PID:** 158450
**/status verification:** HTTP 200 (confirmed immediately after restart)

## Test Evidence

```
=== RUN   TestCheck_SameHash_NoDownload
--- PASS: TestCheck_SameHash_NoDownload (0.06s)
=== RUN   TestCheck_DifferentHash_Match_LaunchesInstaller
--- PASS: TestCheck_DifferentHash_Match_LaunchesInstaller (0.03s)
=== RUN   TestCheck_DifferentHash_Mismatch_RejectsAndIncrementsFail
--- PASS: TestCheck_DifferentHash_Mismatch_RejectsAndIncrementsFail (0.10s)
=== RUN   TestCheck_Paused_SkipsDownload
--- PASS: TestCheck_Paused_SkipsDownload (0.06s)
=== RUN   TestCheck_CooldownActive_BlocksPoll
--- PASS: TestCheck_CooldownActive_BlocksPoll (0.00s)
=== RUN   TestCheck_FailLimit_BlocksEvenOffCooldown
--- PASS: TestCheck_FailLimit_BlocksEvenOffCooldown (0.02s)
PASS
ok  github.com/jjagpal/earl-scheib-watcher/internal/update  1.808s
```

`go build ./...`, `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./cmd/earlscheib`, and `make installer-syntax` all green.

## Deviations from Plan

### Rule 3 — Fix Blocking Issue: Docker image tag change

- **Found during:** Task 4 (`make installer-syntax`)
- **Issue:** Plan + Makefile pinned `amake/innosetup:6.7.1`, but that tag is no longer published on Docker Hub (`docker.io/amake/innosetup:6.7.1: not found`). Blocked Task 4 verification.
- **Fix:** Repointed `installer` and `installer-syntax` Makefile targets to `amake/innosetup:latest` (proven to compile the script cleanly). The `:latest` image's entrypoint is `iscc` directly and accepts a single filename, so `installer-syntax` now runs the same single-filename compile as `installer` — parse-only flags (`/Dq /O-`) are not exposed in the newer image, so the syntax check is effectively a full compile. Acceptable for this project's CI cadence (installer build is already a quick operation).
- **Files modified:** `Makefile`
- **Commit:** `c1dd873` (same commit as Task 4 — blocking issue fixed in-line)

### Minor naming adjustment in test file

- Plan suggested helper named `currentExeHash16(t *testing.T) string`. That collides with the production helper `currentExeHash16() (string, error)` in the same package. Renamed the test-only variant to `testExeHash16(t *testing.T) string`. No user-visible change.

### No other deviations

- No CLAUDE.md rule conflicts.
- No authentication gates.
- No architectural changes needed — stdlib-only implementation, exactly as spec'd.
- Pre-existing `TestSettleChanging` / `TestRunSettleSkip` scanner flakes (timing-sensitive under `-race`) are OUT OF SCOPE (SCOPE BOUNDARY rule); they pass when re-run in isolation.

## Commits

| Task | Description                                        | Commit    |
| ---- | -------------------------------------------------- | --------- |
| 1    | app.py /version endpoint                           | `ad5f0bd` |
| 2    | internal/update package + 6-case TDD               | `475acbd` |
| 3    | Wire update.Check into runScan                     | `f1ca77f` |
| 4    | Installer /VERYSILENT guards + Makefile image tag  | `c1dd873` |
| 5    | Release build + stage + push                       | `c8a7544` |

All pushed to `origin/master` via a single `git push`.

## Operational Notes

- **First propagation window:** Marco's current binary (the one from today's 7+ reinstalls) does NOT have `update.Check` — he needs to install the new `EarlScheibWatcher-Setup.exe` one final time. After that, future fixes land automatically.
- **Propagation ceiling:** scan cadence (5 min) + cooldown (3600s) = worst case **~1 h 5 min** between publish and Marco's PC picking it up.
- **Kill-switch trigger:** `touch /home/jjagpal/projects/earl-scheib-followup/update_paused` on this box halts rollout within one client scan. Remove the file to resume.
- **State file:** `C:\EarlScheibWatcher\update_last_check.json` on Marco's PC tracks `{ts, fail_count}`. Safe to delete if stuck.

## Known Stubs

None. All handlers wired to real data; no placeholder UIs or hardcoded empties.

## Self-Check: PASSED

- Files created:
  - `/home/jjagpal/projects/earl-scheib-followup/internal/update/update.go` — FOUND
  - `/home/jjagpal/projects/earl-scheib-followup/internal/update/update_test.go` — FOUND
- Files modified:
  - `/home/jjagpal/projects/earl-scheib-followup/app.py` — contains `/earlscheibconcord/version` — FOUND
  - `/home/jjagpal/projects/earl-scheib-followup/cmd/earlscheib/main.go` — contains `update.Check(` — FOUND
  - `/home/jjagpal/projects/earl-scheib-followup/installer/earlscheib.iss` — contains `WizardSilent()` — FOUND
- Commits:
  - `ad5f0bd`, `475acbd`, `f1ca77f`, `c1dd873`, `c8a7544` — all present in `git log`
- Live /version endpoint — FOUND (HTTP 200 with expected JSON shape)
- MD5 parity — MATCH
