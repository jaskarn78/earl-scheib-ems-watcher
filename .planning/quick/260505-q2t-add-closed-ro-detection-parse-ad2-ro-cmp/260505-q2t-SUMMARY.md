---
phase: quick-260505-q2t
plan: 01
subsystem: ems-parser
tags: [closed-ro, ems, bms, document-status, installer]
key-files:
  modified:
    - internal/ems/bundle.go
    - internal/ems/parse.go
    - internal/ems/bms.go
    - internal/ems/bms_test.go
    - internal/ems/parse_test.go
decisions:
  - "AD2 + TTL fields added as optional nil-when-absent maps on Bundle, matching ENV pattern"
  - "parseAmount in bms.go (not a separate file) — only one caller, cohesion wins"
  - "Closed-RO override fires BEFORE TRANS_TYPE check — CCC ONE writes E for both open+closed"
metrics:
  duration: ~15m
  completed: "2026-05-05"
  tasks_completed: 1
  tasks_pending: 1
---

# Quick Task 260505-q2t: Closed-RO Detection (AD2/TTL → DocumentStatus=C)

**One-liner:** Parse AD2 (RO_CMPDATE/DATE_OUT) + TTL (G_TTL_AMT) from EMS bundles; override DocumentStatus to "C" when RO is complete AND final bill is present, routing to review job instead of estimate follow-ups.

## Task 1: Source Changes (COMPLETE)

**RED commit:** `383d65a` — test(quick-260505-q2t-01): add failing tests for closed-RO detection
**GREEN commit:** `aca49db` — feat(quick-260505-q2t-01): parse AD2/TTL and override DocumentStatus to "C" for closed ROs

### Changes Made

**`internal/ems/bundle.go`** — Added `AD2` and `TTL` fields to Bundle struct (optional, nil when absent, same nil-tolerance as ENV). Updated doc-comment to describe the new fields.

**`internal/ems/parse.go`** — Added `ad2Fields = []string{"RO_CMPDATE","DATE_OUT","LOC_NM","LOC_PH"}` and `ttlFields = []string{"G_TTL_AMT"}`. Added two optional-file read blocks in ParseBundle after ENV, following the identical pattern.

**`internal/ems/bms.go`** — Added `parseAmount` helper (stdlib strconv/strings, tolerates `$1,234.56`, `1234.56`, `1,234`, `""`) and the closed-RO override at the top of `pickDocumentStatus`: emits `"C"` when `(RO_CMPDATE!="" OR DATE_OUT!="") AND G_TTL_AMT>0`. Existing TRANS_TYPE fallback is unchanged.

**`internal/ems/bms_test.go`** — Added `Test_pickDocumentStatus_ClosedROOverride` (6 sub-tests) and `Test_parseAmount` (11 sub-tests).

**`internal/ems/parse_test.go`** — Added `Test_ParseBundle_AD2_TTL_Optional` confirming nil AD2/TTL when files absent.

### Test Results

```
go test ./internal/ems/... -race -count=1
ok  github.com/jjagpal/earl-scheib-watcher/internal/ems  1.024s
```

All new tests pass. All pre-existing `Test_RenderBMS_*` tests unchanged (regression-free). No race warnings.

## Task 2: Installer Rebuild (BLOCKED — awaiting CI credentials)

**Status:** BLOCKED. Cannot proceed locally.

**Reason:** Two required secrets are absent from the local environment:
- `GSD_HMAC_SECRET` — not set. Plan is explicit: "do NOT silently ship a dev secret to Marco". Build was refused per plan constraint.
- `SIGNING_CERT_B64` — not set. The OV certificate lives only in the CI HSM (per SCAF-06 decision in STATE.md). A dev-signed installer would be blocked by SmartScreen and rejected by Marco's self-update verification.

**Previous installer SHA256[:16]:** `621e7c7cb08354e5` (df55201, the "was" hash)

**Installer rebuild path (to be completed by user):**

The code changes from Task 1 are committed. To ship the installer:

1. Push the branch: `git push origin master` (or current branch)
2. CI builds `dist/earlscheib.exe` with `GSD_HMAC_SECRET` injected + signs the installer with the OV cert
3. Download the signed artifact: `gh run download <run-id> -n earlscheib-installer -D installer/Output/`
4. Copy to repo root + recreate zip:
   ```bash
   cp -f installer/Output/EarlScheibWatcher-Setup.exe ./EarlScheibWatcher-Setup.exe
   rm -f ./EarlScheibWatcher-Setup.zip
   ( cd /tmp && rm -rf esw-zip && mkdir esw-zip && \
     cp /home/jjagpal/projects/earl-scheib-followup/EarlScheibWatcher-Setup.exe esw-zip/ && \
     cd esw-zip && zip ../EarlScheibWatcher-Setup.zip EarlScheibWatcher-Setup.exe )
   cp -f /tmp/EarlScheibWatcher-Setup.zip ./EarlScheibWatcher-Setup.zip
   ```
5. Commit using the release pattern:
   ```bash
   NEW16=$(sha256sum EarlScheibWatcher-Setup.exe | cut -c1-16)
   git add EarlScheibWatcher-Setup.exe EarlScheibWatcher-Setup.zip
   git commit -m "release: rebuild installer with closed-RO detection (AD2/TTL → DocumentStatus=C)

   New SHA256[:16]: $NEW16 (was 621e7c7cb08354e5).
   Marco's self-update will pull within ~7min after update_paused
   sentinel is removed."
   ```

**Note:** Once the release commit lands, update the STATE.md "Quick Tasks Completed" table and remove `update_paused` sentinel to let Marco's self-update loop pick it up.

## Deviations from Plan

None — plan executed exactly as written for Task 1. Task 2 blocked by missing CI secrets (documented in STATE.md decisions, expected per SCAF-06).

## Known Stubs

None. The closed-RO detection is fully wired. The installer rebuild is blocked by missing credentials, not by stubbed code.

## STATE.md Quick Tasks Completed entry

```
| 260505-q2t | Closed-RO detection: parse AD2 (RO_CMPDATE/DATE_OUT) + TTL (G_TTL_AMT), override DocumentStatus → "C" when bundle is closed AND has final bill, so review jobs fire instead of estimate follow-ups. Server unchanged. Source committed; installer rebuild pending CI signing. | 2026-05-05 | aca49db | [260505-q2t-add-closed-ro-detection-parse-ad2-ro-cmp](./quick/260505-q2t-add-closed-ro-detection-parse-ad2-ro-cmp/) |
```
