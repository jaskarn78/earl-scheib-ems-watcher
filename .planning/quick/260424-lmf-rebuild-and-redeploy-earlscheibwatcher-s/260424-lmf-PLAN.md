---
phase: quick-260424-lmf
plan: 01
type: execute
wave: 1
depends_on: []
files_modified:
  - EarlScheibWatcher-Setup.exe
  - EarlScheibWatcher-Setup.zip
  - dist/earlscheib.exe
  - dist/earlscheib-artifact.exe
  - dist/earlscheib-signed.exe
  - installer/Output/EarlScheibWatcher-Setup.exe
  - cmd/earlscheib/rsrc_windows_amd64.syso
autonomous: true
requirements:
  - LMF-01  # Rebuild installer with current source (Templates tab embedded)
  - LMF-02  # Publish new installer so /version returns a new hash
  - LMF-03  # Confirm live hash differs from 5ab6724f09486b72

user_setup:
  - service: gsd-hmac-secret
    why: "Baked into earlscheib.exe so Marco's watcher can HMAC-authenticate against app.py endpoints (/version, /commands, /remote-config, /queue, etc.)"
    env_vars:
      - name: GSD_HMAC_SECRET
        source: "Developer's .env file at repo root (CCC_SECRET alias) — must match the value app.py loads from .env. Without this, the rebuilt exe ships with the in-source dev default and will 401 against every HMAC endpoint including /version, silently breaking the self-update loop for Marco."

must_haves:
  truths:
    - "Rebuilt earlscheib.exe embeds internal/admin/ui/* including the Templates tab markup (index.html contains data-view=\"templates\" and main.js contains Templates tab code)"
    - "Rebuilt EarlScheibWatcher-Setup.exe installs that same earlscheib.exe binary"
    - "EarlScheibWatcher-Setup.exe at repo root is byte-identical to installer/Output/EarlScheibWatcher-Setup.exe after the release"
    - "EarlScheibWatcher-Setup.zip at repo root contains the new EarlScheibWatcher-Setup.exe"
    - "Commit lands on master and pushes to origin (past release pattern: single release commit containing ONLY the exe + zip)"
    - "GET /earlscheibconcord/version with valid HMAC returns a sha256[:16] hash that is NOT 5ab6724f09486b72"
    - "The reported version string equals the SHA256[:16] of the freshly-committed EarlScheibWatcher-Setup.exe at repo root"
  artifacts:
    - path: "EarlScheibWatcher-Setup.exe"
      provides: "Production installer served at https://support.jjagpal.me/earlscheibconcord/download.exe"
      at_repo_root: true
    - path: "EarlScheibWatcher-Setup.zip"
      provides: "Zip wrapper served at /earlscheibconcord/download (Chrome-safe download path)"
      at_repo_root: true
      contains: "EarlScheibWatcher-Setup.exe"
    - path: "dist/earlscheib.exe"
      provides: "Cross-compiled Windows binary with HMAC secret baked in"
  key_links:
    - from: "internal/admin/ui/index.html (containing Templates tab markup)"
      to: "earlscheib.exe (embedded via //go:embed ui in internal/admin/embed.go)"
      via: "CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build"
      pattern: "go build ./cmd/earlscheib"
    - from: "dist/earlscheib.exe (+ rename to dist/earlscheib-artifact.exe)"
      to: "installer/Output/EarlScheibWatcher-Setup.exe"
      via: "Inno Setup compile via amake/innosetup:latest docker (installer/earlscheib.iss [Files] Source: ..\\dist\\earlscheib-artifact.exe)"
    - from: "installer/Output/EarlScheibWatcher-Setup.exe"
      to: "./EarlScheibWatcher-Setup.exe (repo root)"
      via: "cp + commit (matches 70be77e, c8a7544 release precedent)"
    - from: "./EarlScheibWatcher-Setup.exe (repo root)"
      to: "GET /earlscheibconcord/version response"
      via: "app.py re-hashes on every request — NO server restart needed (verified in app.py:2361-2399)"
      pattern: "hashlib.sha256(...).hexdigest()[:16]"
---

<objective>
Rebuild `EarlScheibWatcher-Setup.exe` from current source (which already contains the Templates tab from 260422-wmh) and publish it so Marco's installed watcher picks it up via the self-update loop from 260422-nk1.

**Purpose:** The installer at the repo root was last built at commit `70be77e` (before the Templates tab commits `415217f`, `dc56638`, `1653975`, `688a33f`). Its hash `5ab6724f09486b72` matches Marco's installed exe, so `update.Check()` returns "no update" every 5 minutes. Ship a fresh installer that embeds the new UI and the self-update pipeline will silently deliver it to Marco within ~7 minutes (one scan + cooldown).

**Output:** A new `EarlScheibWatcher-Setup.exe` + `.zip` at the repo root, committed and pushed, with live `/version` returning a NEW sha256[:16] that is NOT `5ab6724f09486b72`.

**Non-goal:** Any source changes. If the executor encounters a situation where source must be modified to make this work, STOP — this indicates the upstream diagnosis is wrong and the task should be escalated, not executed.
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
@$HOME/.claude/get-shit-done/templates/summary.md
</execution_context>

<context>
@.planning/STATE.md
@CLAUDE.md
@Makefile
@installer/earlscheib.iss
@internal/update/update.go
@app.py
@.planning/quick/260422-nk1-self-update-autoupdate-mechanism/260422-nk1-SUMMARY.md
@.planning/quick/260422-wmh-add-a-message-template-editor-to-both-ad/260422-wmh-SUMMARY.md

<interfaces>
<!--
Extracted from codebase to eliminate executor scavenger-hunting.
-->

## app.py — /version handler (app.py:2361-2399)

The handler hashes `EarlScheibWatcher-Setup.exe` ON EVERY REQUEST — 64 KB chunked
SHA256, <10 ms for a 6 MB installer. There is no startup cache. A freshly
committed and pushed exe is reflected in `/version` the next time the handler
is invoked. **No app.py restart is required** after publishing.

```python
# app.py lines 2361-2399 (verbatim)
if path == "/earlscheibconcord/version":
    sig = self.headers.get("X-EMS-Signature", "")
    if not _validate_hmac(b"", sig):
        self._send_json(401, {"error": "invalid signature"})
        return

    app_dir = _os.path.dirname(_os.path.abspath(__file__))
    installer_path = _os.path.join(app_dir, "EarlScheibWatcher-Setup.exe")
    if not _os.path.exists(installer_path):
        self._send_json(404, {"error": "no installer available"})
        return

    paused = (
        _os.environ.get("AUTO_UPDATE_PAUSED") == "1"
        or _os.path.exists(_os.path.join(app_dir, "update_paused"))
    )

    h = hashlib.sha256()
    with open(installer_path, "rb") as fh:
        while True:
            chunk = fh.read(65536)
            if not chunk:
                break
            h.update(chunk)
    version = h.hexdigest()[:16]

    self._send_json(200, {
        "version": version,
        "download_url": "/download.exe",
        "paused": paused,
    })
    return
```

## installer/earlscheib.iss — binary source (line 68)

```ini
; [Files] section
Source: "..\dist\earlscheib-artifact.exe"; DestDir: "{app}"; DestName: "earlscheib.exe"; Flags: ignoreversion
```

**Critical:** The installer consumes `dist/earlscheib-artifact.exe`, NOT
`dist/earlscheib.exe`. After `make build-windows` produces `dist/earlscheib.exe`
the executor must COPY it to `dist/earlscheib-artifact.exe` before running
`make installer`. (Release commits 70be77e and c8a7544 both followed this
convention.)

## installer/earlscheib.iss — silent-upgrade guards (DO NOT CHANGE)

```pascal
// NextButtonClick:  if WizardSilent() then skip folder/conn/CCC-info validation
// CurStepChanged:   if WizardSilent() then WriteConfigIni only (SKIP RegisterScheduledTask)
// UninstallInitialize: if UninstallSilent() then Exit (preserve data dir)
```

These guards let the self-update loop run `/VERYSILENT` without interactively
prompting Marco. They are already in place from 260422-nk1 — this task
rebuilds the exe unchanged.

## Makefile — release targets (Makefile:44-46, 88-89, 102-116)

```make
# build-windows: CGO_ENABLED=0 cross-compile → dist/earlscheib.exe (injects HMAC via ldflags)
# installer:     docker run amake/innosetup:latest /work/installer/earlscheib.iss
#                → installer/Output/EarlScheibWatcher-Setup.exe
# portable:      rebuilds dist/EarlScheibWatcher-Portable.zip (NOT the release zip)
```

**NOTE on `make portable`:** This produces `dist/EarlScheibWatcher-Portable.zip`
which is a DIFFERENT artifact from `EarlScheibWatcher-Setup.zip` at the repo
root. Past releases (70be77e, c8a7544) committed `EarlScheibWatcher-Setup.zip`
at the repo root — a zip containing only `EarlScheibWatcher-Setup.exe`, created
by the release operator (not by `make portable`). Task 4 below handles this
rezip correctly.

## Past release commit precedent

```
70be77e release(qaj): ... 3 files (.gitignore + Setup.exe + Setup.zip)
c8a7544 release(quick-260422-nk1): ...  2 files (Setup.exe + Setup.zip)
```

Release commits contain ONLY the exe and zip at repo root. No build artifacts
under `dist/`, `installer/Output/`, or `.syso` files.

## Known pre-existing hash to beat

```
5ab6724f09486b72   (served by /version right now — pre-Templates)
```

The rebuilt installer MUST produce a sha256[:16] that is NOT this value.
(In principle any source change to `internal/admin/ui/*` since commit 70be77e
will cause the hash to differ, which is what we want — the point of this task
is to ship those differences.)
</interfaces>
</context>

<tasks>

<!-- ================================================================== -->
<!-- TASK 1: Pre-flight gate — confirm HMAC secret + no pending source changes -->
<!-- ================================================================== -->
<task type="auto">
  <name>Task 1: Pre-flight gate — HMAC secret + clean tree + no source drift</name>
  <files>
    (read-only — no files modified)
  </files>
  <action>
**CRITICAL FIRST GATE.** Do all of the following in order. If ANY of the `ABORT` conditions fires, STOP and surface the failure to the user — do not continue to Task 2.

### 1a. Confirm `GSD_HMAC_SECRET` is available

Production builds bake the HMAC secret into the binary via `-X main.secretKey=$(GSD_HMAC_SECRET)` ldflags (see Makefile:25-27). Without this, the rebuilt exe uses the in-source dev default and will 401 against the live webhook — Marco's self-update loop silently breaks.

Load the repo-root `.env` file (which contains `CCC_SECRET=...`; the dev convention is that `GSD_HMAC_SECRET` and `CCC_SECRET` carry the same value — verified in 260422-nk1-SUMMARY.md line 92 which uses `CCC_SECRET` for the live `/version` HMAC test):

```bash
set -a
. .env
set +a

# Alias CCC_SECRET → GSD_HMAC_SECRET if the latter is unset (matches past release workflow).
if [ -z "${GSD_HMAC_SECRET:-}" ] && [ -n "${CCC_SECRET:-}" ]; then
  export GSD_HMAC_SECRET="$CCC_SECRET"
fi

if [ -z "${GSD_HMAC_SECRET:-}" ]; then
  echo "ABORT: GSD_HMAC_SECRET is not set. Cannot rebuild installer without it — the baked-in dev default will 401 against the live webhook and break the self-update loop. User must provide GSD_HMAC_SECRET (same value as CCC_SECRET in .env) before retrying."
  exit 1
fi
echo "GSD_HMAC_SECRET present (length=${#GSD_HMAC_SECRET} chars)"
```

### 1b. Confirm clean working tree

No uncommitted changes should be present — a release commit must contain ONLY the rebuilt exe + zip (per 70be77e / c8a7544 precedent).

```bash
if [ -n "$(git status --porcelain)" ]; then
  echo "ABORT: Uncommitted changes detected:"
  git status --short
  echo "Release commits must contain ONLY EarlScheibWatcher-Setup.exe + .zip. Stash or commit pending work first."
  exit 1
fi
```

### 1c. Confirm Templates-tab source is present in internal/admin/ui/

This is the hard "no code changes" gate. If the Templates markup is missing, the diagnosis is wrong — escalate instead of rebuilding.

```bash
grep -q 'data-view="templates"' internal/admin/ui/index.html \
  || { echo "ABORT: internal/admin/ui/index.html does NOT contain Templates tab markup. Diagnosis is wrong — source is unexpectedly stale. Do NOT rebuild. Escalate to user."; exit 1; }

grep -q 'template-card-template' internal/admin/ui/index.html \
  || { echo "ABORT: internal/admin/ui/index.html missing #template-card-template. Escalate."; exit 1; }

grep -q 'data-view="templates"' ui_public/index.html \
  || { echo "ABORT: ui_public/index.html does NOT contain Templates tab markup. Run 'make sync-ui' before retry, or escalate."; exit 1; }

echo "Templates tab source confirmed in both internal/admin/ui/ and ui_public/"
```

### 1d. Confirm current live /version still reports the pre-Templates hash

Sanity check: if live /version already reports a non-`5ab6724f09486b72` hash, someone else may have already deployed — investigate before duplicating work.

```bash
SIG=$(python3 -c "import hmac, hashlib, os; print(hmac.new(os.environ['GSD_HMAC_SECRET'].encode(), b'', hashlib.sha256).hexdigest())")
LIVE_VERSION=$(curl -s -H "X-EMS-Signature: $SIG" https://support.jjagpal.me/earlscheibconcord/version | python3 -c "import sys, json; print(json.load(sys.stdin).get('version',''))")
echo "Live /version reports: $LIVE_VERSION"
if [ "$LIVE_VERSION" != "5ab6724f09486b72" ]; then
  echo "WARNING: Live /version is '$LIVE_VERSION', not the expected pre-Templates '5ab6724f09486b72'."
  echo "Proceed only if user confirms this is expected (maybe a prior rebuild already happened)."
  # Don't auto-abort — record and continue; Task 6 will still verify the hash differs from 5ab6724f.
fi
```

### 1e. Record starting state

Emit a one-line summary that will be referenced by the final SUMMARY.md:

```bash
echo "PRE-FLIGHT OK — HMAC present, tree clean, Templates source present, live hash=$LIVE_VERSION, HEAD=$(git rev-parse --short HEAD)"
```
  </action>
  <verify>
    <automated>
bash -c '
set -e
set -a
[ -f .env ] && . .env
set +a
[ -z "${GSD_HMAC_SECRET:-}" ] && [ -n "${CCC_SECRET:-}" ] && export GSD_HMAC_SECRET="$CCC_SECRET"
test -n "${GSD_HMAC_SECRET:-}" || { echo "FAIL: GSD_HMAC_SECRET missing"; exit 1; }
test -z "$(git status --porcelain)" || { echo "FAIL: working tree not clean"; git status --short; exit 1; }
grep -q "data-view=\"templates\"" internal/admin/ui/index.html || { echo "FAIL: Templates markup missing"; exit 1; }
grep -q "data-view=\"templates\"" ui_public/index.html || { echo "FAIL: ui_public out of sync"; exit 1; }
echo "PRE-FLIGHT: PASS"
'
    </automated>
  </verify>
  <done>
- GSD_HMAC_SECRET is exported in the executor's current shell session (or process env for subsequent `make` calls).
- `git status` shows no uncommitted changes.
- `internal/admin/ui/index.html` AND `ui_public/index.html` both contain `data-view="templates"`.
- Current live `/version` value recorded (for comparison in Task 6).
- Executor may proceed to Task 2.
  </done>
</task>

<!-- ================================================================== -->
<!-- TASK 2: Build the Windows binary with HMAC secret baked in -->
<!-- ================================================================== -->
<task type="auto">
  <name>Task 2: Cross-compile earlscheib.exe with baked HMAC secret, verify Templates embed</name>
  <files>
    dist/earlscheib.exe (created)
    dist/earlscheib-artifact.exe (created — rename of dist/earlscheib.exe for installer input)
    cmd/earlscheib/rsrc_windows_amd64.syso (refreshed by generate-resources)
  </files>
  <action>
### 2a. Clean prior build artifacts

```bash
make clean
```

This removes `dist/`, stale `.syso` files, and `installer/Output/`. Fresh slate.

### 2b. Build for Windows amd64

`make build-windows` runs `generate-resources` (go-winres → .syso) then `go build` with ldflags injecting `main.appVersion` and (because `GSD_HMAC_SECRET` is set from Task 1) `main.secretKey`:

```bash
make build-windows
```

If go-winres is not installed (error like "go-winres: command not found"), run `make install-tools` first, then retry `make build-windows`.

**Expected output:** `dist/earlscheib.exe` (~9–12 MB, Windows PE32+ executable).

### 2c. Confirm HMAC secret was injected

Grep the binary for the literal secret — must appear at least once (the baked-in value). This mirrors the sanity check noted in commit 70be77e's message ("verified 3 matches via strings"):

```bash
# Use the secret value (not the var name) to confirm injection.
# Don't echo the secret to the screen.
MATCH_COUNT=$(strings dist/earlscheib.exe | grep -c -F "$GSD_HMAC_SECRET" || true)
if [ "$MATCH_COUNT" -lt 1 ]; then
  echo "FAIL: HMAC secret not found in dist/earlscheib.exe — ldflags injection failed. Was GSD_HMAC_SECRET set in the environment visible to make? Rerun Task 1's `set -a; . .env; set +a; export GSD_HMAC_SECRET=\"\$CCC_SECRET\"` then `make build-windows`."
  exit 1
fi
echo "HMAC injection confirmed ($MATCH_COUNT matches in binary)"
```

### 2d. Confirm Templates tab is embedded in the binary

Because `internal/admin/embed.go` uses `//go:embed ui`, the contents of `internal/admin/ui/*` are compiled into the exe verbatim. Grep for the template-card marker to confirm:

```bash
MATCHES_TPL=$(strings dist/earlscheib.exe | grep -c -F 'template-card-template' || true)
MATCHES_VIEW=$(strings dist/earlscheib.exe | grep -c -F 'data-view="templates"' || true)
echo "Templates embed: template-card-template=$MATCHES_TPL, data-view=\"templates\"=$MATCHES_VIEW"
if [ "$MATCHES_TPL" -lt 1 ] || [ "$MATCHES_VIEW" -lt 1 ]; then
  echo "FAIL: Templates tab not embedded in dist/earlscheib.exe. Check internal/admin/embed.go //go:embed directive and internal/admin/ui/index.html."
  exit 1
fi
```

### 2e. Copy to the filename the installer expects

`installer/earlscheib.iss` line 68 references `..\dist\earlscheib-artifact.exe`, not `earlscheib.exe`. Copy (don't mv — Makefile keeps `dist/earlscheib.exe` as the canonical name for other flows like `dev-sign`):

```bash
cp dist/earlscheib.exe dist/earlscheib-artifact.exe
ls -la dist/earlscheib.exe dist/earlscheib-artifact.exe
```

### 2f. (Optional fallback) dev-sign — ONLY if a real OV cert is unavailable

Per constraints, OV cert is tech debt from v1.0 — NOT available. The executor does NOT sign here; Inno Setup will emit an unsigned installer exe (same state as every prior release — 70be77e and c8a7544 were both unsigned). Record in SUMMARY.md that the installer is unsigned; Marco will see SmartScreen "More info → Run anyway" on first install but the self-update path uses `/VERYSILENT` so subsequent upgrades don't trigger SmartScreen at all.

Do NOT run `make dev-sign` — the self-signed cert would actually MAKE SmartScreen behaviour worse (red "Unknown publisher" block instead of just yellow "More info") per Makefile:83 comment.
  </action>
  <verify>
    <automated>
bash -c '
set -e
test -f dist/earlscheib.exe || { echo "FAIL: dist/earlscheib.exe missing"; exit 1; }
test -f dist/earlscheib-artifact.exe || { echo "FAIL: dist/earlscheib-artifact.exe missing"; exit 1; }
# Byte-identical rename
diff -q dist/earlscheib.exe dist/earlscheib-artifact.exe || { echo "FAIL: artifact copy differs from original"; exit 1; }
# Windows PE executable
file dist/earlscheib.exe | grep -qi "PE32" || { echo "FAIL: not a PE binary"; exit 1; }
# Templates embed present
strings dist/earlscheib.exe | grep -qF "template-card-template" || { echo "FAIL: Templates tab not embedded"; exit 1; }
strings dist/earlscheib.exe | grep -qF "data-view=\"templates\"" || { echo "FAIL: templates view marker not embedded"; exit 1; }
# HMAC injection — secret must appear in binary (compare against env var, do not echo)
set -a; [ -f .env ] && . .env; set +a
[ -z "${GSD_HMAC_SECRET:-}" ] && [ -n "${CCC_SECRET:-}" ] && export GSD_HMAC_SECRET="$CCC_SECRET"
MATCH=$(strings dist/earlscheib.exe | grep -c -F "$GSD_HMAC_SECRET" || true)
[ "$MATCH" -ge 1 ] || { echo "FAIL: HMAC secret not injected into binary"; exit 1; }
echo "BUILD: PASS (size=$(stat -c%s dist/earlscheib.exe) bytes, HMAC matches=$MATCH)"
'
    </automated>
  </verify>
  <done>
- `dist/earlscheib.exe` exists, is a valid Windows PE32+ binary, embeds `data-view="templates"` and `template-card-template`.
- `dist/earlscheib-artifact.exe` is a byte-identical copy (for installer consumption).
- HMAC secret is baked into the binary (grep confirms ≥1 match).
- No signing performed (unsigned exe is the v1.0 default).
- Ready for Task 3 (installer build).
  </done>
</task>

<!-- ================================================================== -->
<!-- TASK 3: Build the Inno Setup installer via Docker -->
<!-- ================================================================== -->
<task type="auto">
  <name>Task 3: Build EarlScheibWatcher-Setup.exe via Inno Setup docker, verify /VERYSILENT guards intact</name>
  <files>
    installer/Output/EarlScheibWatcher-Setup.exe (created)
  </files>
  <action>
### 3a. Confirm docker is available

```bash
docker --version >/dev/null 2>&1 || { echo "FAIL: Docker not available. Install Docker or run on a host with docker daemon."; exit 1; }
```

### 3b. Compile installer via amake/innosetup:latest

Per Makefile:88-89 and the `quick-260422-nk1` deviation note, the image tag pinned in the Makefile is `latest` (the `6.7.1` tag was unpublished):

```bash
make installer
```

Docker will pull `amake/innosetup:latest` if not cached (~150 MB first time). Output: `installer/Output/EarlScheibWatcher-Setup.exe` (~6 MB).

### 3c. Confirm installer exists and is non-trivial

```bash
test -f installer/Output/EarlScheibWatcher-Setup.exe \
  || { echo "FAIL: installer not produced"; exit 1; }
SIZE=$(stat -c%s installer/Output/EarlScheibWatcher-Setup.exe)
[ "$SIZE" -gt 3000000 ] \
  || { echo "FAIL: installer suspiciously small ($SIZE bytes); expected ~6 MB"; exit 1; }
echo "Installer built: $SIZE bytes"
```

### 3d. Confirm the installer is self-extracting (Inno Setup signature)

Inno Setup installers embed the "Inno Setup" marker in the compiled exe's string table. Sanity check that docker didn't silently produce a stub:

```bash
strings installer/Output/EarlScheibWatcher-Setup.exe | grep -qi "Inno Setup" \
  || { echo "FAIL: Installer does not look like an Inno Setup binary"; exit 1; }
```

### 3e. Confirm installer hash will differ from 5ab6724f09486b72

```bash
NEW_HASH=$(sha256sum installer/Output/EarlScheibWatcher-Setup.exe | awk '{print $1}' | cut -c1-16)
echo "New installer sha256[:16] = $NEW_HASH"
if [ "$NEW_HASH" = "5ab6724f09486b72" ]; then
  echo "ABORT: rebuilt installer hash matches the current server hash — no propagation will occur. Either source is identical to 70be77e state (no Templates changes compiled in) or build was non-deterministic in a lucky way. Investigate before continuing."
  exit 1
fi
```

### 3f. Confirm .iss silent-upgrade guards still present (DID NOT regress)

Re-verify on-disk .iss that the 260422-nk1 silent-upgrade guards haven't been stomped by this build process. These are source-file asserts, not build-output asserts:

```bash
grep -q 'WizardSilent()' installer/earlscheib.iss \
  || { echo "FAIL: WizardSilent() guard missing from .iss — self-update loop will break on Marco's PC"; exit 1; }
grep -q 'UninstallSilent()' installer/earlscheib.iss \
  || { echo "FAIL: UninstallSilent() guard missing from .iss"; exit 1; }
```

(These should always pass — .iss isn't touched in this task — but a regression here would silently break Marco's self-update chain.)
  </action>
  <verify>
    <automated>
bash -c '
set -e
test -f installer/Output/EarlScheibWatcher-Setup.exe || { echo "FAIL: installer not produced"; exit 1; }
SIZE=$(stat -c%s installer/Output/EarlScheibWatcher-Setup.exe)
[ "$SIZE" -gt 3000000 ] || { echo "FAIL: installer too small ($SIZE)"; exit 1; }
strings installer/Output/EarlScheibWatcher-Setup.exe | grep -qi "Inno Setup" || { echo "FAIL: not an Inno Setup binary"; exit 1; }
NEW_HASH=$(sha256sum installer/Output/EarlScheibWatcher-Setup.exe | awk "{print \$1}" | cut -c1-16)
[ "$NEW_HASH" != "5ab6724f09486b72" ] || { echo "FAIL: hash matches old 5ab6724f09486b72"; exit 1; }
grep -q "WizardSilent()" installer/earlscheib.iss || { echo "FAIL: WizardSilent guard missing"; exit 1; }
echo "INSTALLER BUILD: PASS (size=$SIZE, sha256[:16]=$NEW_HASH)"
'
    </automated>
  </verify>
  <done>
- `installer/Output/EarlScheibWatcher-Setup.exe` exists and is >3 MB.
- Contains "Inno Setup" string marker.
- New sha256[:16] is captured and is NOT `5ab6724f09486b72`.
- `.iss` silent-upgrade guards still present (no regression).
- Ready for Task 4 (stage to repo root + rezip).
  </done>
</task>

<!-- ================================================================== -->
<!-- TASK 4: Stage to repo root + rebuild the release zip -->
<!-- ================================================================== -->
<task type="auto">
  <name>Task 4: Copy installer to repo root, rebuild EarlScheibWatcher-Setup.zip</name>
  <files>
    EarlScheibWatcher-Setup.exe (modified — overwritten with new build)
    EarlScheibWatcher-Setup.zip (modified — rezipped to wrap new exe)
  </files>
  <action>
### 4a. Capture the OLD repo-root hash for commit-message comparison

```bash
OLD_ROOT_HASH=$(sha256sum EarlScheibWatcher-Setup.exe | awk '{print $1}' | cut -c1-16)
echo "Old repo-root installer sha256[:16] = $OLD_ROOT_HASH"
# Expected: 5ab6724f09486b72 (or whatever Task 1d observed)
```

### 4b. Copy the fresh installer to the repo root (served by app.py)

Per app.py:2045-2068 and past release precedent (70be77e, c8a7544), `app.py` serves `./EarlScheibWatcher-Setup.exe` from the repo root at `/earlscheibconcord/download.exe`:

```bash
cp installer/Output/EarlScheibWatcher-Setup.exe EarlScheibWatcher-Setup.exe
NEW_ROOT_HASH=$(sha256sum EarlScheibWatcher-Setup.exe | awk '{print $1}' | cut -c1-16)
echo "New repo-root installer sha256[:16] = $NEW_ROOT_HASH"

# Sanity: must match installer/Output/ hash (byte-identical copy)
INSTALLER_OUTPUT_HASH=$(sha256sum installer/Output/EarlScheibWatcher-Setup.exe | awk '{print $1}' | cut -c1-16)
[ "$NEW_ROOT_HASH" = "$INSTALLER_OUTPUT_HASH" ] \
  || { echo "FAIL: repo-root copy hash diverged from installer/Output/. Filesystem issue?"; exit 1; }

# Sanity: must NOT match the hash we're trying to replace
[ "$NEW_ROOT_HASH" != "$OLD_ROOT_HASH" ] \
  || { echo "FAIL: new hash matches old hash — no propagation will occur"; exit 1; }
```

### 4c. Rebuild EarlScheibWatcher-Setup.zip wrapping the new exe

The /tmp-staging pattern comes from 260422-nk1-SUMMARY.md (noted there as "proven pattern"). Strategy: create a clean tmp dir, copy the new exe in, zip it, move back to repo root. This avoids accidentally zipping stray files from the repo.

```bash
STAGE=$(mktemp -d /tmp/earlscheib-zip-stage.XXXXXX)
cp EarlScheibWatcher-Setup.exe "$STAGE/EarlScheibWatcher-Setup.exe"

# Build zip from inside the staging dir so the archive has no dir prefixes
( cd "$STAGE" && zip -q -9 EarlScheibWatcher-Setup.zip EarlScheibWatcher-Setup.exe )

# Overwrite the repo-root zip atomically
mv "$STAGE/EarlScheibWatcher-Setup.zip" EarlScheibWatcher-Setup.zip
rm -rf "$STAGE"

ls -la EarlScheibWatcher-Setup.exe EarlScheibWatcher-Setup.zip
```

### 4d. Verify zip contents

```bash
ZIP_LIST=$(unzip -l EarlScheibWatcher-Setup.zip | awk 'NR>3 && !/----/ && $NF != ""{print $NF}' | grep -v '^$' | head -5)
echo "Zip contents:"
echo "$ZIP_LIST"
echo "$ZIP_LIST" | grep -q '^EarlScheibWatcher-Setup\.exe$' \
  || { echo "FAIL: zip does not contain EarlScheibWatcher-Setup.exe at root"; exit 1; }

# Extract + compare against the repo-root exe — must be byte-identical
VERIFY=$(mktemp -d /tmp/earlscheib-zip-verify.XXXXXX)
unzip -q EarlScheibWatcher-Setup.zip -d "$VERIFY"
diff -q "$VERIFY/EarlScheibWatcher-Setup.exe" EarlScheibWatcher-Setup.exe \
  || { echo "FAIL: zipped exe differs from repo-root exe"; exit 1; }
rm -rf "$VERIFY"
echo "Zip verified byte-identical to repo-root exe"
```

### 4e. Record final hashes for the commit message

```bash
EXE_MD5=$(md5sum EarlScheibWatcher-Setup.exe | awk '{print $1}')
ZIP_MD5=$(md5sum EarlScheibWatcher-Setup.zip | awk '{print $1}')
EXE_SHA16=$(sha256sum EarlScheibWatcher-Setup.exe | awk '{print $1}' | cut -c1-16)
echo "RELEASE HASHES (for commit message):"
echo "  EarlScheibWatcher-Setup.exe   md5=$EXE_MD5   sha256[:16]=$EXE_SHA16"
echo "  EarlScheibWatcher-Setup.zip   md5=$ZIP_MD5"
echo "  Previous exe sha256[:16] = $OLD_ROOT_HASH (will be replaced on push)"
```

Save these values — Task 5 will embed them in the release commit message, matching the format of commits 70be77e and c8a7544.
  </action>
  <verify>
    <automated>
bash -c '
set -e
test -f EarlScheibWatcher-Setup.exe || { echo "FAIL: repo-root exe missing"; exit 1; }
test -f EarlScheibWatcher-Setup.zip || { echo "FAIL: repo-root zip missing"; exit 1; }

# exe matches installer/Output/
diff -q EarlScheibWatcher-Setup.exe installer/Output/EarlScheibWatcher-Setup.exe \
  || { echo "FAIL: repo-root exe differs from installer/Output/"; exit 1; }

# exe is NOT the old pre-Templates hash
NEW_HASH=$(sha256sum EarlScheibWatcher-Setup.exe | awk "{print \$1}" | cut -c1-16)
[ "$NEW_HASH" != "5ab6724f09486b72" ] || { echo "FAIL: exe still hashes to old 5ab6724f09486b72"; exit 1; }

# zip contains byte-identical exe
VERIFY=$(mktemp -d)
unzip -q EarlScheibWatcher-Setup.zip -d "$VERIFY"
diff -q "$VERIFY/EarlScheibWatcher-Setup.exe" EarlScheibWatcher-Setup.exe || { rm -rf "$VERIFY"; echo "FAIL: zipped exe differs"; exit 1; }
rm -rf "$VERIFY"

echo "STAGE: PASS (new sha256[:16]=$NEW_HASH)"
'
    </automated>
  </verify>
  <done>
- `./EarlScheibWatcher-Setup.exe` (repo root) is the freshly built installer, byte-identical to `installer/Output/EarlScheibWatcher-Setup.exe`.
- `./EarlScheibWatcher-Setup.zip` (repo root) wraps it, verified by extract+diff.
- New sha256[:16] differs from both the old repo-root hash AND the live-served `5ab6724f09486b72`.
- Hashes recorded for the Task 5 commit message.
- Ready for Task 5 (commit + push).
  </done>
</task>

<!-- ================================================================== -->
<!-- TASK 5: Commit and push release -->
<!-- ================================================================== -->
<task type="auto">
  <name>Task 5: Commit release (exe + zip only) and push to origin/master</name>
  <files>
    EarlScheibWatcher-Setup.exe (committed)
    EarlScheibWatcher-Setup.zip (committed)
  </files>
  <action>
### 5a. Stage ONLY the two release artifacts

Past releases (70be77e, c8a7544) committed only these two files. Staging anything else (build artifacts under `dist/`, `installer/Output/`, regenerated `.syso`, etc.) violates the release pattern:

```bash
git add EarlScheibWatcher-Setup.exe EarlScheibWatcher-Setup.zip

# Assert nothing else is staged
UNEXPECTED=$(git diff --cached --name-only | grep -v '^EarlScheibWatcher-Setup\.\(exe\|zip\)$' || true)
if [ -n "$UNEXPECTED" ]; then
  echo "ABORT: unexpected staged files:"
  echo "$UNEXPECTED"
  echo "Release commits must contain ONLY EarlScheibWatcher-Setup.exe + .zip. Unstage and retry."
  exit 1
fi

# Also assert nothing OUTSIDE the release artifacts was modified in the worktree
# (build artifacts like dist/, installer/Output/, .syso should be .gitignored or reverted)
DIRTY_OTHER=$(git status --porcelain | grep -vE '^\S\s+EarlScheibWatcher-Setup\.(exe|zip)$' || true)
if [ -n "$DIRTY_OTHER" ]; then
  echo "NOTE: other unstaged changes present (expected for dist/, installer/Output/):"
  echo "$DIRTY_OTHER"
  echo "Confirm these are all in .gitignore'd paths before continuing."
  # Don't abort — build outputs under dist/ and installer/Output/ may appear in status
  # if not fully gitignored, but should not be staged. The above UNEXPECTED check
  # already guarantees the commit is clean.
fi
```

### 5b. Create the release commit (follow 70be77e / c8a7544 format)

Use the hashes captured in Task 4e. Commit message format mirrors past releases (type `release(quick-260424-lmf)`):

```bash
git commit -m "$(cat <<'EOF'
release(quick-260424-lmf): rebuild installer with Templates tab UI

- Fresh Windows build with baked HMAC secret (verified via strings grep)
- Inno Setup compile via amake/innosetup:latest (matches 260422-nk1 pattern)
- Rezipped via /tmp staging
- Source changes shipping to Marco:
    415217f feat(templates): add storage table, render helper, rewire Twilio send path
    dc56638 feat(templates): add GET/PUT /templates endpoints with dual-auth + renderable-check
    1653975 feat(templates): add /api/templates proxy routes in Go admin
    688a33f feat(templates): add Templates tab UI with chips, live preview, dirty tracking
- Old repo-root installer sha256[:16]: 5ab6724f09486b72 (pre-Templates)
- Marco's self-update loop (260422-nk1) will pick this up within one scan +
  cooldown cycle (~2-7 min) — no manual reinstall required.
EOF
)"

git log -1 --stat
```

(Omit the `Co-Authored-By` footer — past release commits don't include one, and the project's `~/.claude/settings.json` disables attribution globally per the user's git-workflow rule.)

### 5c. Push to origin/master

```bash
# Confirm we're on master (not a detached HEAD or feature branch)
BRANCH=$(git rev-parse --abbrev-ref HEAD)
[ "$BRANCH" = "master" ] \
  || { echo "FAIL: not on master (on $BRANCH). Past release commits all landed on master directly."; exit 1; }

git push origin master
```

### 5d. Confirm the push landed

```bash
LOCAL_HEAD=$(git rev-parse HEAD)
REMOTE_HEAD=$(git rev-parse origin/master)
[ "$LOCAL_HEAD" = "$REMOTE_HEAD" ] \
  || { echo "FAIL: local HEAD ($LOCAL_HEAD) ≠ origin/master HEAD ($REMOTE_HEAD). Push may have been rejected."; exit 1; }
echo "Push confirmed: HEAD=$LOCAL_HEAD"
```
  </action>
  <verify>
    <automated>
bash -c '
set -e
# Exactly one commit ahead or at origin/master
git diff --cached --name-only | grep -q "EarlScheibWatcher-Setup" || true  # if anything staged, it is these
# Last commit touches only the two release artifacts
CHANGED=$(git log -1 --name-only --pretty=format: | grep -v "^$")
echo "Last commit touched:"
echo "$CHANGED"
echo "$CHANGED" | grep -qE "^EarlScheibWatcher-Setup\.(exe|zip)$" || { echo "FAIL: release commit does not include both artifacts"; exit 1; }
OTHER=$(echo "$CHANGED" | grep -vE "^EarlScheibWatcher-Setup\.(exe|zip)$" || true)
[ -z "$OTHER" ] || { echo "FAIL: release commit includes unexpected files: $OTHER"; exit 1; }
# Commit title matches release pattern
git log -1 --pretty=%s | grep -qE "^release\(quick-260424-lmf\):" || { echo "FAIL: commit title does not match release pattern"; exit 1; }
# Pushed
[ "$(git rev-parse HEAD)" = "$(git rev-parse origin/master)" ] || { echo "FAIL: local HEAD not synced to origin/master"; exit 1; }
echo "RELEASE COMMIT: PASS"
'
    </automated>
  </verify>
  <done>
- Single release commit exists on local master touching ONLY `EarlScheibWatcher-Setup.exe` and `EarlScheibWatcher-Setup.zip`.
- Commit title matches the `release(quick-260424-lmf):` pattern established by past releases.
- `git rev-parse HEAD` equals `git rev-parse origin/master` (push confirmed).
- Ready for Task 6 (live verification).
  </done>
</task>

<!-- ================================================================== -->
<!-- TASK 6: Live /version verification -->
<!-- ================================================================== -->
<task type="auto">
  <name>Task 6: Verify live /version returns the new hash + optionally watch for Marco's self-update</name>
  <files>
    (read-only — no files modified)
  </files>
  <action>
### 6a. Re-derive expected hash from the pushed repo-root exe

```bash
EXPECTED=$(sha256sum EarlScheibWatcher-Setup.exe | awk '{print $1}' | cut -c1-16)
echo "Expected /version response: $EXPECTED"
```

### 6b. Reload HMAC secret for the curl call

```bash
set -a
[ -f .env ] && . .env
set +a
[ -z "${GSD_HMAC_SECRET:-}" ] && [ -n "${CCC_SECRET:-}" ] && export GSD_HMAC_SECRET="$CCC_SECRET"
test -n "${GSD_HMAC_SECRET:-}" || { echo "FAIL: GSD_HMAC_SECRET not available"; exit 1; }
```

### 6c. Query live /version

Because app.py hashes the installer on every request (confirmed in app.py:2361-2399), no server restart is needed — the file on disk is read directly. However, **the push in Task 5 must have reached the server filesystem first.** If the production host pulls from origin via a webhook/cron, there may be a delay. Past releases (70be77e, c8a7544) assumed the push is reflected immediately because the production host hosts the repo directly (per 260422-nk1-SUMMARY.md, `app.py` serves from `/home/jjagpal/projects/earl-scheib-followup/`).

**Critical:** If the dev machine IS the production host (which it appears to be per the file paths in 260422-nk1-SUMMARY lines 108-113), then Task 5's push-to-origin is sufficient — `app.py` already sees the new file because the commit updated the working tree on disk. If the production host is separate, the executor would need to trigger a pull there. For THIS task, assume the prod == dev host pattern established by 260422-nk1.

```bash
SIG=$(python3 -c "import hmac, hashlib, os; print(hmac.new(os.environ['GSD_HMAC_SECRET'].encode(), b'', hashlib.sha256).hexdigest())")

RESP=$(curl -sS -H "X-EMS-Signature: $SIG" https://support.jjagpal.me/earlscheibconcord/version)
echo "Live /version response: $RESP"

LIVE_VERSION=$(echo "$RESP" | python3 -c "import sys, json; print(json.load(sys.stdin).get('version',''))")
echo "Live version = $LIVE_VERSION"
echo "Expected     = $EXPECTED"

if [ "$LIVE_VERSION" = "$EXPECTED" ]; then
  echo "SUCCESS: live /version serves the new installer."
elif [ "$LIVE_VERSION" = "5ab6724f09486b72" ]; then
  echo "FAIL: live /version still reports the OLD pre-Templates hash (5ab6724f09486b72)."
  echo "Possible causes:"
  echo "  1. Production host has not pulled origin/master (if prod != dev, a pull is needed)."
  echo "  2. app.py is serving a cached/stale installer from a different path than this repo root."
  echo "  3. HTTP cache (unlikely — /version is dynamic)."
  exit 1
else
  echo "WARN: live /version reports unexpected hash ($LIVE_VERSION). Neither the new nor the old."
  echo "This could mean a third party published something in between. Investigate before declaring success."
  exit 1
fi
```

### 6d. Confirm paused=false (kill-switch not tripped)

```bash
PAUSED=$(echo "$RESP" | python3 -c "import sys, json; print(json.load(sys.stdin).get('paused', True))")
[ "$PAUSED" = "False" ] \
  || { echo "FAIL: paused=$PAUSED — self-update kill-switch is ACTIVE. Remove update_paused sentinel file or AUTO_UPDATE_PAUSED env var and retry."; exit 1; }
echo "Kill-switch status: clear (paused=False)"
```

### 6e. Confirm download.exe serves the new installer

Independent second check — /download.exe and /version hash the same file. If they diverge, something is badly wrong:

```bash
DL_SIG=$(python3 -c "import hmac, hashlib, os; print(hmac.new(os.environ['GSD_HMAC_SECRET'].encode(), b'', hashlib.sha256).hexdigest())")
curl -sS -H "X-EMS-Signature: $DL_SIG" -o /tmp/live-download-exe https://support.jjagpal.me/earlscheibconcord/download.exe
LIVE_DL_HASH=$(sha256sum /tmp/live-download-exe | awk '{print $1}' | cut -c1-16)
LOCAL_HASH=$(sha256sum EarlScheibWatcher-Setup.exe | awk '{print $1}' | cut -c1-16)
echo "Live /download.exe sha256[:16]: $LIVE_DL_HASH"
echo "Local repo-root    sha256[:16]: $LOCAL_HASH"
[ "$LIVE_DL_HASH" = "$LOCAL_HASH" ] \
  || { echo "FAIL: live /download.exe diverges from local exe. /version vs /download serving different files."; exit 1; }
echo "Live /download.exe matches local repo-root exe — byte-identical."
rm -f /tmp/live-download-exe
```

### 6f. Stretch: watch server logs for Marco's self-update pickup (best-effort, non-blocking)

Marco's watcher runs the scan every 5 minutes. After pickup he'll GET `/version` (HMAC authed), match the hash mismatch, then GET `/download.exe` to fetch the new installer. Look for two consecutive `/version` and `/download.exe` hits from his client in app logs.

If log access is available (same box), tail briefly:

```bash
# Best-effort: look for app.py log file. Common locations include:
for LOGFILE in /tmp/app.out /var/log/earlscheib/app.log $HOME/earl-scheib-followup/app.log; do
  if [ -f "$LOGFILE" ]; then
    echo "Tailing $LOGFILE for 30s — looking for /version + /download.exe hits..."
    timeout 30 tail -f "$LOGFILE" 2>/dev/null | grep -E '/version|/download\.exe' || true
    break
  fi
done
echo ""
echo "Log-watch window closed. Marco's actual pickup may take up to ~7 min"
echo "(5-min scan cadence + ≤120s cooldown, per internal/update/update.go:57)."
```

If log files aren't accessible, emit this instruction for the user instead:

```bash
cat <<'EOF'

### Manual follow-up for the user (not blocking):

To confirm Marco's PC picked up the new installer, within ~10 min:

1. Check app.py server logs for entries like:
      GET /earlscheibconcord/version  (200)   <— Marco polls
      GET /earlscheibconcord/download.exe  (200)  <— Marco downloads
   from his client IP, 1-2 scan cycles after this push.

2. Or curl /status and watch `last_heartbeat`:
      curl -s https://support.jjagpal.me/earlscheibconcord/status
   If heartbeats continue uninterrupted, the silent-upgrade worked.

3. If Marco loads the admin UI at https://support.jjagpal.me/earlscheib
   (with basic-auth creds) and sees the Templates tab, the new binary is
   installed.

EOF
```
  </action>
  <verify>
    <automated>
bash -c '
set -e
set -a
[ -f .env ] && . .env
set +a
[ -z "${GSD_HMAC_SECRET:-}" ] && [ -n "${CCC_SECRET:-}" ] && export GSD_HMAC_SECRET="$CCC_SECRET"

EXPECTED=$(sha256sum EarlScheibWatcher-Setup.exe | awk "{print \$1}" | cut -c1-16)
SIG=$(python3 -c "import hmac, hashlib, os; print(hmac.new(os.environ[\"GSD_HMAC_SECRET\"].encode(), b\"\", hashlib.sha256).hexdigest())")
RESP=$(curl -sS -H "X-EMS-Signature: $SIG" https://support.jjagpal.me/earlscheibconcord/version)
LIVE=$(echo "$RESP" | python3 -c "import sys, json; print(json.load(sys.stdin).get(\"version\",\"\"))")
PAUSED=$(echo "$RESP" | python3 -c "import sys, json; print(json.load(sys.stdin).get(\"paused\", True))")
echo "Live=$LIVE Expected=$EXPECTED Paused=$PAUSED"
[ "$LIVE" = "$EXPECTED" ] || { echo "FAIL: /version mismatch (expected $EXPECTED, got $LIVE)"; exit 1; }
[ "$LIVE" != "5ab6724f09486b72" ] || { echo "FAIL: /version still reports old hash"; exit 1; }
[ "$PAUSED" = "False" ] || { echo "FAIL: paused=$PAUSED (should be False)"; exit 1; }
echo "LIVE VERIFY: PASS"
'
    </automated>
  </verify>
  <done>
- Live `https://support.jjagpal.me/earlscheibconcord/version` returns a sha256[:16] that:
    (a) matches the freshly committed repo-root `EarlScheibWatcher-Setup.exe`, AND
    (b) differs from the pre-Templates hash `5ab6724f09486b72`.
- Live response includes `paused=false` (kill-switch clear).
- Live `/download.exe` serves a byte-identical copy of the local repo-root exe.
- Stretch verify attempted (best-effort log tail or user-instructions handoff).
- Marco's self-update loop will now detect the mismatch on his next `/scan` cycle and apply the upgrade within ~7 minutes.
  </done>
</task>

</tasks>

<verification>
End-to-end phase checks (the test an outside observer can run to prove this task shipped):

1. `git log --oneline -1` shows a `release(quick-260424-lmf):` commit touching only the two release artifacts.
2. `git rev-parse HEAD == git rev-parse origin/master`.
3. `sha256sum EarlScheibWatcher-Setup.exe | cut -c1-16` ≠ `5ab6724f09486b72`.
4. `strings EarlScheibWatcher-Setup.exe | grep -i "inno setup"` returns ≥1 line.
5. Curl the live `/version` with a valid HMAC → response.version equals step 3's hash AND is ≠ `5ab6724f09486b72`.
6. Curl the live `/download.exe` with a valid HMAC → sha256[:16] equals step 3's hash.
7. `.iss` file still contains `WizardSilent()` and `UninstallSilent()` guards (no regression to the 260422-nk1 silent-upgrade chain).
</verification>

<success_criteria>
- Live `/version` reports a new sha256[:16] that is NOT `5ab6724f09486b72`.
- That hash equals the sha256[:16] of the freshly committed `EarlScheibWatcher-Setup.exe` at the repo root.
- Release commit on `origin/master` contains ONLY `EarlScheibWatcher-Setup.exe` + `EarlScheibWatcher-Setup.zip` (matches 70be77e / c8a7544 precedent).
- No source files were modified (build artifacts only).
- Marco's self-update loop is primed to apply the upgrade within ~7 minutes (5-min scan cadence + ≤120s cooldown, per `internal/update/update.go:57`).
</success_criteria>

<output>
After completion, create `.planning/quick/260424-lmf-rebuild-and-redeploy-earlscheibwatcher-s/260424-lmf-SUMMARY.md` with:

- Frontmatter: `phase: quick-260424-lmf`, `provides: [rebuilt EarlScheibWatcher-Setup.exe with Templates tab UI baked in]`, `affects: [EarlScheibWatcher-Setup.exe, EarlScheibWatcher-Setup.zip]`, `tech_stack.added: []` (no new deps), `key_files.modified: [EarlScheibWatcher-Setup.exe, EarlScheibWatcher-Setup.zip]`, decisions: [unsigned release (no OV cert — v1.0 tech debt); reused /tmp rezip pattern; no source changes].

Include:
- Old sha256[:16] → new sha256[:16] transition.
- Live /version curl output (HTTP 200 + JSON).
- Installer size in bytes and MD5 for audit trail.
- Which commits are being shipped to Marco (415217f, dc56638, 1653975, 688a33f).
- Propagation ETA (5-min scan + ≤120s cooldown).
- Any deviations from this plan (especially if the `dev-sign` fallback was used or log-tail was not available).

Then append a one-liner to `.planning/STATE.md` under `Quick Tasks Completed`.
</output>
