---
quick_id: 260508-q9c
type: quick
plan: 01
subsystem: internal/ems (dBASE bundle parser + BMS DocumentStatus selection)
tags: [bug, parser, false-positive, document-status, regression-test, tdd]
requires:
  - "internal/ems/parse.go readFields type-asserting tbl.Interpret return"
  - "go-dbase v1.12.10 normalising empty Date columns to time.Time{}"
provides:
  - "Empty dBASE date columns serialise as \"\" in Bundle.AD2 (not the zero-time string)"
  - "pickDocumentStatus reliably returns \"E\" for fresh estimates with empty DATE_OUT"
  - "Two new regression tests pinning the parse-level + bms-level contracts"
affects:
  - "Closed-RO classification path in pickDocumentStatus (bms.go)"
  - "Server-side job-routing: estimates now fire 24h/3day SMS instead of review SMS"
tech-stack:
  added: []
  patterns:
    - "Type-assertion on go-dbase Interpret return value before fmt.Sprint, with IsZero short-circuit for empty Date columns"
    - "Opt-in DataType field on test fixture columnSpec (zero value defaults to dbase.Character — backwards-compatible with all existing fixture call sites)"
    - "Companion makeTestDBFTyped helper accepting map[string]interface{} for typed row values (e.g. time.Time on Date columns)"
key-files:
  created: []
  modified:
    - "internal/ems/parse.go (added time import + IsZero short-circuit before fmt.Sprint in readFields)"
    - "internal/ems/parse_test.go (extended columnSpec with optional DataType; added makeTestDBFTyped; added two regression tests + converted unkeyed columnSpec literals to keyed form)"
    - "internal/ems/bms_test.go (added empty-date-out-with-bill-regression-q9c sub-case to Test_pickDocumentStatus_ClosedROOverride)"
    - "internal/ems/bms.go (preserved working-tree edit dropping RO_CMPDATE from close detection — paired correctly with this fix)"
decisions:
  - "Type-assertion approach (over peeking at raw cellBytes) chosen because go-dbase already normalises both empty representations (8 spaces from CCC ONE; 8 zeros from other writers) to time.Time{} — single point of intervention, no duplication of go-dbase's parsing logic"
  - "columnSpec.DataType added as opt-in field with zero-value defaulting to dbase.Character — preserved fixture API for all existing tests; converted unkeyed struct literals to keyed form to satisfy the new field"
  - "makeTestDBFTyped added as a separate helper rather than mutating makeTestDBF's signature — avoids cascading edits to ~10 existing call sites that pass map[string]string"
  - "Existing ad2-present-but-blank case already covers DATE_OUT=\"\" + G_TTL_AMT>0, but a named regression sub-case (empty-date-out-with-bill-regression-q9c) was added as defence-in-depth — the explicit name + comment guard against silent re-regression and document the production bug for future readers"
  - "False-positive review jobs already created in jobs.db (113, 115, 150-154) are NOT cancelled by this commit — that is operator cleanup work, deliberately out of scope per plan"
  - "Installer rebuild + Marco-machine push is a separate follow-up (consistent with prior q2t/rei pattern: source commit lands first, installer release second once the next code change is ready to bundle)"
metrics:
  duration: "~25 min"
  completed: "2026-05-08"
  tasks_completed: 3
  files_modified: 4
  tests_added: 3
---

# Quick Task 260508-q9c: Fix empty-date false-positive in pickDocumentStatus Summary

One-liner: Empty dBASE Date columns no longer serialise as the zero-time string `"0001-01-01 00:00:00 +0000 UTC"` — type-assert `tbl.Interpret`'s return value in `readFields`, leave `out[name]` at `""` for `time.Time{}.IsZero()` cells, so `pickDocumentStatus`'s `lookup(b.AD2,"DATE_OUT") != ""` check no longer trips on fresh estimates and the server's CLOSED_STATUSES branch stops misrouting them to review SMS.

## Root Cause

The bug lived in `internal/ems/parse.go:166-170` (the column-loop inside `readFields`):

```go
val, ierr := tbl.Interpret(cellBytes, col)
if ierr != nil || val == nil {
    continue
}
out[name] = strings.TrimSpace(fmt.Sprint(val))
```

For a dBase Date column (DataType `D`, length 8) whose raw row bytes were 8 spaces (CCC ONE's representation of an unfilled date), go-dbase's `Interpret` correctly returned a `time.Time` zero value (`time.Time{}`). Then `fmt.Sprint(time.Time{})` produced the 35-character string `"0001-01-01 00:00:00 +0000 UTC"` — non-empty after `TrimSpace`. That non-empty string was stored in `bundle.AD2["DATE_OUT"]`.

Downstream in `bms.go:pickDocumentStatus`:

```go
isClosed := lookup(b.AD2, "DATE_OUT") != ""
hasFinalBill := parseAmount(lookup(b.TTL, "G_TTL_AMT")) > 0
if isClosed && hasFinalBill {
    return "C"
}
```

The non-empty zero-time string tripped `isClosed`. Combined with any non-zero `G_TTL_AMT` (every estimate has a total), `pickDocumentStatus` returned `"C"`, so the BMS payload's `<DocumentStatus>C</DocumentStatus>` routed via app.py's `CLOSED_STATUSES` to schedule a review SMS instead of the expected 24h/3day estimate follow-ups.

Confirmed false-positive jobs already in `jobs.db`: 113 (Russell Rosete), 115 (Jason Rigor), 150–154 (May 11 pending). These are flagged as separate operator cleanup work — see `Out of Scope` below.

## The Fix (one-line behavioural change)

Inside `readFields`, immediately before the `fmt.Sprint` line, type-assert the value and short-circuit for zero `time.Time`:

```go
if t, ok := val.(time.Time); ok {
    if t.IsZero() {
        continue // out[name] stays at the default "" set above
    }
}
```

`"time"` was added to the import block. Single point of intervention; handles both space-padded and zero-padded empty representations because go-dbase already normalises both to `time.Time{}`.

## Tasks Completed

| # | Task | Files | Result |
|---|------|-------|--------|
| 1 | TDD RED + GREEN — fix `parse.go` and add parse-level regression test | `internal/ems/parse.go`, `internal/ems/parse_test.go` | RED confirmed (got zero-time string); GREEN confirmed after fix |
| 2 | Add `pickDocumentStatus` defence-in-depth regression sub-case | `internal/ems/bms_test.go` | New `empty-date-out-with-bill-regression-q9c` sub-case passes |
| 3 | Run full suite, atomic commit (4 files, no unrelated working-tree files) | `internal/ems/{parse,parse_test,bms_test,bms}.go` | `go test ./...` green; commit `5aeb229` |

## Tests Added

1. `Test_ParseBundle_EmptyDateColumn_StoresEmptyString` (parse_test.go) — round-trips an `ad2` fixture with a Date column whose row value is `time.Time{}`. Asserts:
   - `b.AD2["DATE_OUT"] == ""` (was `"0001-01-01 00:00:00 +0000 UTC"` pre-fix)
   - `pickDocumentStatus(b) == "E"` (was `"C"` pre-fix) — belt-and-suspenders that the fix closes the loop with bms.go
2. `Test_ParseBundle_PopulatedDateColumn_NonEmpty` (parse_test.go) — companion positive case, populated date round-trips as a non-empty string. Guards against an over-eager regression that swallows ALL date columns.
3. `Test_pickDocumentStatus_ClosedROOverride/empty-date-out-with-bill-regression-q9c` (bms_test.go) — pins the downstream contract independently of the parse-level fix; an empty `AD2[DATE_OUT]` with `G_TTL_AMT="1500.00"` returns `"E"`, not `"C"`.

## Test Infrastructure Changes

- `columnSpec` (the test-fixture column recipe) gained an optional `DataType dbase.DataType` field. Zero value defaults to `dbase.Character` so existing call sites that build only Character columns retain their semantics. All existing unkeyed `columnSpec{"NAME", N}` literals were converted to keyed form (`{Name: "NAME", Length: N}`) — purely a syntactic change forced by adding the new field.
- `makeTestDBFTyped` helper added — accepts `map[string]interface{}` so Date columns can carry `time.Time` values directly. The original `makeTestDBF(... map[string]string)` is preserved as a thin wrapper around the typed inner function — all ~10 existing fixture call sites continue to work unchanged.

## Verification

- `go test ./internal/ems/...` — all 9 test functions pass (3 new tests + all pre-existing tests untouched).
- `go test ./...` — full project suite green: `admin`, `config`, `db`, `ems`, `heartbeat`, `install`, `logging`, `remoteconfig`, `scanner`, `status`, `telemetry`, `update`, `webhook` all OK; no regressions from adding `"time"` import to parse.go.
- `go build ./...` — clean.
- `git log -1 --name-only` shows commit `5aeb229` touches exactly 4 files, all under `internal/ems/`. Unrelated working-tree mods (`app.py`, `internal/admin/ui/*`, `ui_public/*`, `.gitignore`, installer .bak files, untracked `.md` scaffolds, `extract_logo.py`, etc.) remain in the working tree, NOT in this commit.

## Deviations from Plan

None — plan executed exactly as written. The TDD RED step produced the predicted failure (the zero-time string), the GREEN step produced the predicted pass, and the bms_test.go sub-case landed without surprises. The `columnSpec` literal conversion (unkeyed → keyed) was a minor syntactic-only consequence of adding the opt-in `DataType` field; it didn't change any test semantics and is the cleanest way to satisfy go vet's struct-literal check.

## Out of Scope (Follow-Up Work)

1. **Cancel false-positive review jobs in `jobs.db`**: jobs 113, 115, 150–154. These were created before this fix landed; they will fire review SMS to fresh-estimate customers if not cancelled. Operator action via `/queue` cancel endpoint, or a one-shot `dump_folder`-style operator command. Track as a separate quick task or operator runbook step.
2. **Installer rebuild + Marco-machine push**: This commit is source-only. Per the prior `260505-q2t` / `260505-rei` pattern, the installer release is a follow-up commit (`make release-prep`, Inno Setup compile via `amake/innosetup-docker`, sign with `osslsigncode`, push to `EarlScheibWatcher-Setup.exe` in repo root, regenerate sidecar SHA256). Marco's self-update loop will pull the new installer within ~7 min once the release commit lands AND the `update_paused` sentinel is removed on his machine.
3. **Unrelated working-tree changes** (`app.py`, `internal/admin/ui/*`, `ui_public/*`, `.gitignore`, untracked PLAN.md scaffolds in `.planning/quick/`, installer `.bak` files): not addressed by this fix, deliberately preserved in the working tree per critical_context.

## Commit

- `5aeb229` — `fix(ems): empty dBASE date columns now store "" instead of zero-time string`

## Self-Check

`5aeb229` exists in `git log`: confirmed.
`internal/ems/parse.go` modified: confirmed (added `"time"` import + IsZero short-circuit).
`internal/ems/parse_test.go` modified: confirmed (added 2 new tests + extended `columnSpec` + `makeTestDBFTyped`).
`internal/ems/bms_test.go` modified: confirmed (added regression sub-case).
`internal/ems/bms.go` working-tree edit preserved in commit: confirmed (`git diff --cached` showed RO_CMPDATE removal).
SUMMARY.md path: `.planning/quick/260508-q9c-fix-empty-date-false-positive-in-pickdoc/260508-q9c-SUMMARY.md`.

## Self-Check: PASSED
