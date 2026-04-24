---
phase: quick-260424-lmf
plan: 01
subsystem: release-pipeline
tags: [release, installer, self-update, templates]

provides:
  - "Rebuilt EarlScheibWatcher-Setup.exe with Templates tab UI baked in (sha256[:16] d0be23a1e5a2aaa1)"

requires: []

affects:
  - EarlScheibWatcher-Setup.exe
  - EarlScheibWatcher-Setup.zip

tech_stack:
  added: []
  patterns:
    - "Rebuild-only release (no source changes)"
    - "Unsigned installer (OV cert still v1.0 tech debt — matches 70be77e, c8a7544 precedent)"
    - "/tmp staging for zip (pattern from 260422-nk1)"

key_files:
  created: []
  modified:
    - EarlScheibWatcher-Setup.exe
    - EarlScheibWatcher-Setup.zip

decisions:
  - "Unsigned release (no OV cert — v1.0 tech debt; dev-sign skipped per plan because self-signed makes SmartScreen worse)"
  - "Reused /tmp rezip pattern from 260422-nk1"
  - "No source changes — build artifacts only (objective non-goal honored)"

metrics:
  tasks_completed: 6
  tasks_total: 6
  duration_minutes: ~5
  completed_date: "2026-04-24"

requirements_completed:
  - LMF-01
  - LMF-02
  - LMF-03
---

# Quick Task 260424-lmf: Rebuild and Redeploy EarlScheibWatcher-Setup.exe Summary

Rebuilt `EarlScheibWatcher-Setup.exe` from current master (post-Templates-tab commits) and pushed to `origin/master` so Marco's installed watcher auto-updates via the `/version` polling loop from 260422-nk1; live `/version` now returns the new hash `d0be23a1e5a2aaa1`.

## Hash Transition

| Stage | sha256[:16] | Source |
| --- | --- | --- |
| Pre-rebuild repo-root exe | `8d586028e9c4143f` | Committed in `70be77e` (release(qaj), pre-Templates) |
| Pre-rebuild live `/version` | `8d586028e9c4143f` | Confirmed by curl before Task 2 |
| **New repo-root exe (this task)** | **`d0be23a1e5a2aaa1`** | Commit `4810e41` |
| Live `/version` after push | `d0be23a1e5a2aaa1` | Confirmed by curl after Task 5 (no app.py restart) |
| Live `/download.exe` after push | `d0be23a1e5a2aaa1` | Byte-identical to local repo-root exe |

Note: The plan referenced `5ab6724f09486b72` as the "pre-Templates" hash. Actual live hash before this task was `8d586028e9c4143f` (from `70be77e`, shipped 2026-04-22 22:15 per app.py logs). The plan's success criterion — "NEW hash, not `5ab6724f09486b72`" — is satisfied AND strengthened: the new hash also differs from the real previous hash `8d586028e9c4143f`, so propagation will actually occur.

## Artifacts

| File | Size (bytes) | MD5 | SHA256 |
| --- | --- | --- | --- |
| `EarlScheibWatcher-Setup.exe` (repo root) | 6,097,190 | `3787f1fdfacc564ea6a9f7ed44f585e0` | `d0be23a1e5a2aaa1beaecf96484f893ce7fdf7b64ba31d8d178fbfd2f12b4851` |
| `EarlScheibWatcher-Setup.zip` (repo root) | 5,396,753 | `d02602d161dc676ed5649c08f83fc0a9` | `803abf054bf03e2fe2b041e6c652402045caa4357a50a125f6121143af2a2b4d` |
| `dist/earlscheib.exe` (intermediate) | 11,999,744 | — | — |

## Templates Tab Confirmation

Grep evidence from `dist/earlscheib.exe` (byte-identical to the exe bundled inside `EarlScheibWatcher-Setup.exe` via Inno Setup):

```
$ strings dist/earlscheib.exe | grep -c -F 'template-card-template'
2
$ strings dist/earlscheib.exe | grep -c -F 'data-view="templates"'
2

$ strings dist/earlscheib.exe | grep -F 'data-view="templates"' | head -1
      <a class="topnav-link"           data-view="templates" href="#templates" role="tab" aria-selected="false">Templates</a>

$ strings dist/earlscheib.exe | grep -F 'template-card-template' | head -1
  <template id="template-card-template">
```

Both markers embedded twice each (once in `internal/admin/ui/index.html`, once in the compile's `ui_public/index.html` copy). This confirms the four Templates commits from 260422-wmh are shipping to Marco:

- `415217f` feat(templates): add storage table, render helper, rewire Twilio send path
- `dc56638` feat(templates): add GET/PUT /templates endpoints with dual-auth + renderable-check
- `1653975` feat(templates): add /api/templates proxy routes in Go admin
- `688a33f` feat(templates): add Templates tab UI with chips, live preview, dirty tracking

HMAC secret injection was also verified: `strings dist/earlscheib.exe | grep -c -F "$GSD_HMAC_SECRET"` returned **3** (same match count 70be77e observed — expected 1 ldflags-injected occurrence plus 2 read-only data copies from constant pooling).

## Live /version Transcript

```
$ SIG=$(python3 -c "import hmac, hashlib, os; print(hmac.new(os.environ['GSD_HMAC_SECRET'].encode(), b'', hashlib.sha256).hexdigest())")
$ echo $SIG
9bf80292319824f7029959df1af698128df0fc9b09a5cea6e593bdd3fb323afa

$ curl -sS -H "X-EMS-Signature: $SIG" https://support.jjagpal.me/earlscheibconcord/version
{"version": "d0be23a1e5a2aaa1", "download_url": "/download.exe", "paused": false}
```

- `version=d0be23a1e5a2aaa1` — matches the freshly committed repo-root exe
- `download_url=/download.exe` — correct
- `paused=false` — kill-switch clear

`/download.exe` cross-check: sha256[:16] of downloaded bytes equals `d0be23a1e5a2aaa1`, byte-identical to local (6,097,190 bytes).

## app.py Restart

**Not needed.** Per plan (verified in `app.py:2361-2399`), the `/version` handler re-hashes `EarlScheibWatcher-Setup.exe` on every request with a 64 KB chunked SHA256 read. The response updates the moment the file on disk changes — no startup cache, no process restart required. This was confirmed empirically: the push in Task 5 updated the working-tree file at `/home/jjagpal/projects/earl-scheib-followup/EarlScheibWatcher-Setup.exe` (dev host == prod host, as established by 260422-nk1), and the very next `/version` curl returned the new hash.

The app.py process is still PID 800384, started Apr 23 (`ps aux | grep app.py`). No restart performed.

## Release Commit

```
commit 4810e41f6b10386541fb66827b967ddaa94462e9
Author: Jaskarn Jagpal <jjagpal101@gmail.com>
Date:   Fri Apr 24 15:44:43 2026 +0000

    release(quick-260424-lmf): rebuild installer with Templates tab UI
    [...]
 EarlScheibWatcher-Setup.exe | Bin 6087764 -> 6097190 bytes
 EarlScheibWatcher-Setup.zip | Bin 5388880 -> 5396753 bytes
 2 files changed, 0 insertions(+), 0 deletions(-)
```

- Touches ONLY the two release artifacts (matches 70be77e / c8a7544 precedent exactly)
- Pushed fast-forward `0ba6181..4810e41` to `origin/master`
- Remote HEAD confirmed synced with local HEAD

## Marco's Self-Update Observability

**From this session:** Not directly observable — Marco's PC is behind his shop NAT and his `/version` polls will hit the server over the next 5 min cycle. The /tmp/app.out log present on the dev host contains entries only up to 2026-04-23 00:01 (one-day stale), suggesting app.py may have rotated logs or be writing elsewhere after the Apr 23 restart. Historical log grep shows `/version` + `/download.exe` polling on the expected 5-minute cadence — the self-update loop mechanism is sound.

**What the user should check on Marco's install within the next ~10 minutes:**

1. Server logs for entries like:
   ```
   GET /earlscheibconcord/version  (200)   <- Marco polls
   GET /earlscheibconcord/download.exe  (200)  <- Marco downloads
   ```
   from Marco's client IP, 1-2 scan cycles after this push.

2. Or curl `/status` and watch `last_heartbeat` — if heartbeats continue uninterrupted, the /VERYSILENT silent-upgrade worked.

3. If Marco loads `https://support.jjagpal.me/earlscheib` (basic-auth) and sees the **Templates** tab in the topnav, the new binary is installed.

Propagation ETA: ≤7 minutes from push (5-min scan cadence + ≤120s cooldown per `internal/update/update.go:57`).

## Execution Timeline

| Task | Gate outcome |
| --- | --- |
| 1. Pre-flight gate | PASS (with deviation — plan's pre-Templates hash `5ab6724f09486b72` was stale; actual live hash was `8d586028e9c4143f` from 70be77e; plan's 1d explicitly handles this case non-fatally, continue) |
| 2. Cross-compile earlscheib.exe | PASS — go-winres installed via `make install-tools`; HMAC injected (3 matches); Templates embedded (2+2 matches); dist/earlscheib-artifact.exe byte-identical copy made |
| 3. Inno Setup docker build | PASS — installer 6,097,190 bytes, "Inno Setup" marker present, WizardSilent() + UninstallSilent() guards intact |
| 4. Stage + rezip | PASS — repo-root exe byte-identical to installer/Output/, zip contains byte-identical exe |
| 5. Commit + push | PASS — commit 4810e41 (2 files only), fast-forward push to origin/master |
| 6. Live verification | PASS — `/version` returns d0be23a1e5a2aaa1 (matches local), `/download.exe` byte-identical to local, paused=false |

## Deviations from Plan

### [Rule 3 — Blocking] go-winres not on PATH at start of Task 2

**Found during:** Task 2b first attempt.
**Issue:** `make build-windows` failed with `go-winres: No such file or directory`. `go-winres` is a go install target — not in PATH by default.
**Fix:** Ran plan's documented fallback `make install-tools` (installed go-winres to `~/go/bin`), then prepended `~/go/bin` to `PATH` in the executor's environment for subsequent `make` calls.
**Files modified:** none (tool binary installed to `~/go/bin/go-winres`)
**Commit:** N/A (tooling install, not source change)

### [Documentation — non-fatal] Plan's assumed old hash was stale

**Found during:** Task 1d sanity-check curl.
**Issue:** Plan asserted live `/version` should return `5ab6724f09486b72` (pre-Templates baseline). Actual returned value was `8d586028e9c4143f` (from `70be77e`, shipped 2026-04-22 19:12). The plan's assumption that `70be77e` was "pre-Templates" appears incorrect — or a different installer than documented was committed in that release.
**Impact:** None on task outcome. The plan explicitly handles this case in Task 1d ("Don't auto-abort — record and continue; Task 6 will still verify the hash differs from 5ab6724f"). The success criterion "new hash != 5ab6724f09486b72" is satisfied, and the strengthened criterion "new hash != the actual previous hash 8d586028e9c4143f" is ALSO satisfied. Marco's self-update loop will detect the mismatch and apply the upgrade.
**Fix:** Recorded in SUMMARY; no remediation needed.

### [Pre-flight — tree cleanliness] Plan directory untracked at start

**Found during:** Task 1b.
**Issue:** Plan's 1b strict check `git status --porcelain` must be empty would abort because the `/gsd:quick` workflow itself creates the untracked `.planning/quick/260424-lmf-.../` directory containing this plan before spawning the executor.
**Fix:** Narrowed the dirty-check to "no uncommitted changes OTHER than the expected new plan directory". Explicit plan-artifact path was excluded. Staging in Task 5a used `git add EarlScheibWatcher-Setup.exe EarlScheibWatcher-Setup.zip` explicitly, plus an UNEXPECTED assertion to guarantee the release commit stayed clean regardless of worktree state. Past releases (70be77e, c8a7544) landed via the same narrow-stage pattern.
**Files modified:** none
**Commit:** The release commit `4810e41` touched only the 2 expected files, confirmed via `git log -1 --stat` showing `2 files changed`.

### Not performed

- `make dev-sign` — intentionally skipped per plan: self-signed certs make SmartScreen WORSE (red "Unknown publisher" block vs yellow "More info"). Installer ships unsigned, matching 70be77e / c8a7544 precedent. OV cert remains v1.0 tech debt.
- Log-tail stretch check (Task 6f) — log file `/tmp/app.out` contains only historical entries (last ~2026-04-23 00:01 UTC), not live activity; possibly app.py has rotated/redirected logs since the Apr 23 restart. Not blocking — the definitive signal is the `/version` curl response itself (which proved the server is serving the new file).

## Self-Check: PASSED

- `EarlScheibWatcher-Setup.exe` exists at repo root (FOUND)
- `EarlScheibWatcher-Setup.zip` exists at repo root (FOUND)
- Commit `4810e41` present in `git log` (FOUND)
- Commit pushed to `origin/master` (FOUND — `git rev-parse HEAD == git rev-parse origin/master`)
- Live `/version` returns `d0be23a1e5a2aaa1` (matches local exe; differs from both `5ab6724f09486b72` and `8d586028e9c4143f`)
- Live `/download.exe` byte-identical to local exe
- No source files modified (verified — `git log -1 --stat` shows only the 2 release artifacts)
