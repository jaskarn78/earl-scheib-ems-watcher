---
quick_id: 260421-shq
type: summary
completed: 2026-04-21T20:45:00Z
commits:
  - cd2f1a3  # Task 1: scanner path+err + scan-start INFO
  - 2997cdf  # Task 2: admin UI Diagnostic panel
  - 272f4dd  # Task 3: server /diagnostic endpoint
  - 9e3d030  # Task 4: release (installer exe + zip)
files_modified:
  - internal/scanner/scan.go
  - internal/scanner/scanner_test.go
  - cmd/earlscheib/main.go
  - internal/admin/diagnostic.go       # new
  - internal/admin/server.go
  - internal/admin/ui/index.html
  - internal/admin/ui/main.css
  - internal/admin/ui/main.js
  - app.py
  - EarlScheibWatcher-Setup.exe        # force-added (gitignored)
  - EarlScheibWatcher-Setup.zip        # force-added (gitignored)
requirements_satisfied:
  - DBG-01
  - DBG-02
  - DBG-03
  - DBG-04
artifacts:
  installer_exe_md5: aef60f889b24cf392ff92141aff62696
  installer_zip_md5: 2060ca3c1485a2855610c52b3821d019
  live_zip_md5:      2060ca3c1485a2855610c52b3821d019  # MATCH
---

# Quick Task 260421-shq: Debuggability Improvements (Scanner + Admin + Server)

**One-liner:** Scanner now logs `path=<folder> err=<OS error>` verbatim so Marco's 5-minute cycles produce actionable failures; admin UI gained a Diagnostic panel polling `/api/diagnostic` every 5s; server exposes HMAC-authed `/earlscheibconcord/diagnostic` returning heartbeat freshness + commands.json state + log tail.

---

## Scanner: exact log-line format shipped

**`scan.go Run()` — new INFO record at cycle start (every invocation):**

```
[INFO] scan start watch_folder="C:\CCC\EMS_Export" webhook="https://support.jjagpal.me/earlscheibconcord" version="0.1.0-dev"
```

Keys: `watch_folder`, `webhook`, `version`. Emitted BEFORE `Candidates()` so it fires even when the folder is missing.

**`scan.go Candidates()` — WARN record on folder-read failure (contract for ops grep):**

```
[WARNING] Cannot read watch folder path="C:\CCC\EMS_Export" err=open C:\CCC\EMS_Export: The system cannot find the path specified.
```

Attr key renamed `dir` → `path`; underlying OS error forwarded verbatim (no sanitization).

**Tests added to `internal/scanner/scanner_test.go`:**

- `TestRunEmitsScanStartINFO` — asserts level=INFO, message="scan start", attrs {watch_folder, webhook, version}
- `TestCandidatesLogsPathAndError` — asserts level=WARN, path attr is the exact dir, err attr is a real Go error whose `.Error()` contains the path
- `TestRunEmptyFolderStillRecordsRun` — regression: new INFO line does not change `(0, 0)` + `"no files"` note semantics

Plus a shared `recordingHandler` helper (slog.Handler that captures `[]slog.Record`) added to the file — reusable for future slog-assertion tests.

**Struct change:** `scanner.RunConfig` gained `WebhookURL string` and `AppVersion string` fields; `cmd/earlscheib/main.go runScan` plumbs them from the already-in-scope `cfg.WebhookURL` + package-level `appVersion` (ldflag-injected).

**Verification:** `go test ./internal/scanner/... -race -count=1` PASS; full suite `go test ./... -race -count=1` PASS.

---

## Admin UI: `/api/diagnostic` response schema + new DOM IDs

**JSON response from `GET /api/diagnostic` (see `internal/admin/diagnostic.go`):**

```json
{
  "watch_folder": "C:\\CCC\\EMS_Export",
  "folder_exists": true,
  "file_count": 3,
  "folder_error": "",
  "last_scan_at": "2026-04-21 20:30:00",
  "last_scan_processed": 2,
  "last_scan_errors": 0,
  "last_scan_note": "scan of C:\\CCC\\EMS_Export (3 candidates)",
  "last_heartbeat_at": "2m ago (WIN-8I9KME32KLC)",
  "hmac_secret_present": true,
  "app_version": "0.1.0-dev"
}
```

**Implementation notes:**

- `config.LoadConfig(filepath.Join(config.DataDir(), "config.ini"))` — read-only
- Folder probe: `os.Stat` → distinguishes ENOENT (`folder_error="folder does not exist"`) from other OS errors; then `os.ReadDir` + case-insensitive `.xml`/`.ems` count mirroring `scanner.Candidates`
- Last-scan: `sql.Open("sqlite", dbPath+"?mode=ro")`, SQL `SELECT run_at, processed, errors, note FROM runs ORDER BY rowid DESC LIMIT 1` — same string as `status.Print`. Close immediately
- Last heartbeat: live GET to `{WebhookURL}/status` with 3s timeout; on success returns the `"last_seen"` field (e.g. `"2m ago"`) + host; on failure returns a bracketed sentinel like `(server unreachable)`
- HMAC presence: `s.cfg.Secret != "" && s.cfg.Secret != "dev-test-secret-do-not-use-in-production"` — treats the in-source dev default as "not present" so a build without `GSD_HMAC_SECRET` surfaces as `hmac_secret_present=false`
- The secret VALUE is NEVER returned — only the boolean

**New DOM IDs in `internal/admin/ui/index.html`** (`<section id="diagnostic">`):

- `diag-watch-folder`
- `diag-folder-exists`
- `diag-file-count`
- `diag-last-scan`
- `diag-last-heartbeat`
- `diag-hmac`
- `diag-version`

**CSS additions (`internal/admin/ui/main.css`):** new `.diagnostic` card section (oxblood top rule, Fraunces uppercase heading, 2-column `dl.diag-grid` with 200px label column), `.ok` (green `#2E6A3E`) and `.bad` (oxblood, bold) status classes. Responsive override collapses the diag-grid to 1 column at ≤640px.

**JS additions (`internal/admin/ui/main.js`):** new `DIAG_MS = 5000` constant, `fetchDiagnostic()` async function, `setDiagText`/`setDiagStatus` helpers, wired into the existing `DOMContentLoaded` handler. `fetch('/api/diagnostic', { cache: 'no-store' })`. Transient errors silent by design (5s poll forgives flakes).

**Smoke test confirmed (local build, fake port 127.0.0.1:65535 for webhook):**
file_count=2 (xml+ems; .txt ignored), folder_exists=true, hmac_secret_present=false (dev build — expected), last_heartbeat_at="(server unreachable)".

---

## Server: HMAC validation + log-tail selection rule

**Handler:** `app.py` `do_GET` — new `if path == "/earlscheibconcord/diagnostic":` block inserted between `/queue` (line 1784) and the default 404.

**HMAC validation:** `_validate_hmac(b"", sig_header)` — empty-body signature, byte-identical to the existing `/commands` and `/remote-config` handlers. Same pattern already proven; no new crypto code.

**Response keys:**

- `last_heartbeat`: `{"ts": <unix|null>, "host": "<hostname|null>", "seconds_ago": <int|null>}` derived from the in-memory `LAST_HEARTBEAT` dict (line 168) — read-only; the POST `/heartbeat` handler at line 1893 is the sole writer.
- `client_online`: boolean, `bool(ts and (int(time.time()) - ts) < 600)` — same 10-minute threshold the existing `/status` endpoint uses.
- `commands_state`: contents of `commands.json` loaded via `json.load` — READ ONLY; on `FileNotFoundError`/`JSONDecodeError`, returns `{}`. **Never written by this handler.**
- `recent_log_tail`: last 20 lines of the NEWEST `received_logs/*.log` file by mtime. `latest.log` symlink is explicitly filtered out to avoid double-counting. On `OSError`, returns `""`.
- `received_logs_count`: `len([f for f in os.listdir('received_logs') if f.endswith('.log') and f != 'latest.log'])`.

**Log-tail selection rule:** `max(entries, key=os.path.getmtime)` over the filtered list. The 20-line cap keeps the response under the admin proxy's 1 MB LimitReader in `handleQueue`; the newest-non-symlink rule avoids the symlink-target ambiguity.

**No new imports:** uses only `os`, `json`, `time` — all already imported at top of `app.py`.

**Local smoke test (port 28299):**
- Valid sig → HTTP 200, JSON with all 5 keys, `commands_state={"upload_log": false}`, `received_logs_count=1`
- No sig → HTTP 401 `{"error": "invalid signature"}`
- `/commands` endpoint after the test still returns the same `{"upload_log": false}` (verified 204 because all values falsy)

---

## Release: the GSD_HMAC_SECRET incantation (reminder for next release)

This is the trap that cost an hour earlier today — `make build-windows` without `GSD_HMAC_SECRET` silently falls back to the dev default, producing a binary that can't auth to the live server.

**Always source from .env first:**

```bash
export GSD_HMAC_SECRET="$(grep '^CCC_SECRET=' .env | cut -d= -f2-)"
[ -n "$GSD_HMAC_SECRET" ] || { echo "ABORT: GSD_HMAC_SECRET empty"; exit 1; }
echo "HMAC secret length: ${#GSD_HMAC_SECRET} chars"
```

**Verify the ldflags show the secret before you commit:**

```bash
make -n build-windows | grep -o 'main.secretKey=[^ "]*'
# Must print: main.secretKey=<your-actual-secret>, NOT the dev default
```

This release's `make -n` confirmed: `-X main.secretKey=gruh7oul3whis3yeep2BUSH8rich` (28 chars, matches .env CCC_SECRET).

**Full release sequence (this run, for posterity):**

1. `export GSD_HMAC_SECRET="$(grep ^CCC_SECRET= .env | cut -d= -f2-)"` + length check
2. `PATH="$HOME/go/bin:$PATH" make clean && make build-windows`
3. `cp dist/earlscheib.exe dist/earlscheib-artifact.exe`
4. `docker run --rm -v "$PWD:/work" amake/innosetup:latest /work/installer/earlscheib.iss`
5. `cp installer/Output/EarlScheibWatcher-Setup.exe EarlScheibWatcher-Setup.exe`
6. Re-zip via /tmp staging (avoids "add to existing" mode):
   ```
   cd /tmp && rm -f EarlScheibWatcher-Setup.zip && \
     cp ${REPO}/EarlScheibWatcher-Setup.exe . && \
     zip EarlScheibWatcher-Setup.zip EarlScheibWatcher-Setup.exe && \
     mv EarlScheibWatcher-Setup.zip ${REPO}/ && \
     rm /tmp/EarlScheibWatcher-Setup.exe
   ```
7. `git add -f EarlScheibWatcher-Setup.exe EarlScheibWatcher-Setup.zip` (root binaries are gitignored — force-add is intentional)
8. Four atomic commits: scanner / admin / server / release
9. `git push origin master`
10. Verify: `curl -sL https://support.jjagpal.me/earlscheibconcord/download -o /tmp/live.zip && md5sum /tmp/live.zip ${REPO}/EarlScheibWatcher-Setup.zip` — both MD5s must match

---

## MD5 of the deployed EarlScheibWatcher-Setup.zip

```
2060ca3c1485a2855610c52b3821d019  EarlScheibWatcher-Setup.zip (repo root)
2060ca3c1485a2855610c52b3821d019  live (GET https://support.jjagpal.me/earlscheibconcord/download)
```

**MATCH** — the live download serves byte-for-byte the same zip that was committed in 9e3d030.

For completeness, the EXE inside:

```
aef60f889b24cf392ff92141aff62696  EarlScheibWatcher-Setup.exe
```

---

## commands.json — explicit confirmation

**Neither modified nor read-with-intent-to-write by any code in this task.**

- Task 3's new `/diagnostic` handler opens `commands.json` with `open(cmd_path, "r", ...)` ONLY — no write, no rename, no unlink.
- Working-tree `cat commands.json` before and after:
  ```
  {"upload_log": false}
  ```
- The git working-tree shows `commands.json` as "modified" only because of trailing-newline differences — contents identical; the constraint is honored. No commit in this task stages `commands.json`.
- Live server state via `curl -H "X-EMS-Signature: ..." /earlscheibconcord/commands` returns HTTP 204 — which the existing handler emits when all values in `commands.json` are falsy, exactly matching `{"upload_log": false}`.

---

## Deviations from Plan

### Auto-fixed issues

**1. [Rule 3 - Blocking] `EarlScheibWatcher-Setup.exe` + `.zip` are gitignored at repo root**

- **Found during:** Task 4 git staging
- **Issue:** The plan said `git add EarlScheibWatcher-Setup.exe EarlScheibWatcher-Setup.zip` but `.gitignore` at line 21/22 blocks these paths; they have never been tracked in git history. Normal `git add` is a silent no-op.
- **Fix:** Used `git add -f` to force-add past the gitignore. This is the same mechanism that must have been used for every previous release (since the live server's repo pull is the only plausible deploy path that explains MD5-match-with-repo).
- **Files modified:** none (approach change only).
- **Commit:** 9e3d030.

### Noted, not deviated from

**Live `/diagnostic` endpoint returns 404 as of SUMMARY write time.**

- Local app.py test at port 28299 returned a 200 with valid JSON; live returns 404 → the server-side Python process has not been restarted since the `git push` landed.
- This is outside the scope of a git-push deploy — the deploy host auto-pulls code but does not auto-restart the HTTPServer. The MD5-match of the zip proves the git pull happened.
- User-side action required: restart `app.py` on support.jjagpal.me (systemd unit, supervisor, or equivalent) to pick up the new route. Not a code change.
- Scanner improvements (Task 1) and admin UI Diagnostic panel (Task 2) ship IN the installer zip → already live as of the MD5-verified zip.

### Auth gates

None — no authentication required beyond the already-baked HMAC secret.

---

## Self-Check: PASSED

**Files created/modified — FOUND:**

- `internal/scanner/scan.go` — FOUND
- `internal/scanner/scanner_test.go` — FOUND
- `cmd/earlscheib/main.go` — FOUND
- `internal/admin/diagnostic.go` — FOUND (new file)
- `internal/admin/server.go` — FOUND
- `internal/admin/ui/index.html` — FOUND
- `internal/admin/ui/main.css` — FOUND
- `internal/admin/ui/main.js` — FOUND
- `app.py` — FOUND
- `EarlScheibWatcher-Setup.exe` — FOUND (force-added, 5924439 bytes)
- `EarlScheibWatcher-Setup.zip` — FOUND (force-added, 5225531 bytes)

**Commits — FOUND:**

- cd2f1a3 — FOUND (`git log --oneline` shows `fix(scanner): log full path...`)
- 2997cdf — FOUND (`feat(admin): add Diagnostic panel...`)
- 272f4dd — FOUND (`feat(server): add HMAC-authed GET /diagnostic endpoint`)
- 9e3d030 — FOUND (`release: rebuild installer with prod HMAC secret...`)

**Push:** `b2c5947..9e3d030 master -> master` — confirmed.

**MD5 match:** local zip == live zip (`2060ca3c1485a2855610c52b3821d019`).

**Tests:** `go test ./... -race -count=1` — all packages PASS (scanner, admin, config, db, heartbeat, install, logging, remoteconfig, status, telemetry, webhook).
