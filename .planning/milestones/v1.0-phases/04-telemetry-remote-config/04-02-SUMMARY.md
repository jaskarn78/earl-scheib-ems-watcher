---
phase: 04-telemetry-remote-config
plan: "02"
subsystem: remote-config
tags: [remote-config, config, hmac, atomic-write, whitelist, ops]
dependency_graph:
  requires: []
  provides: [internal/remoteconfig, config.Merge]
  affects: [cmd/earlscheib/main.go, internal/config/config.go]
tech_stack:
  added: [internal/remoteconfig]
  patterns: [atomic-write, whitelist-enforcement, best-effort-fetch, hmac-signed-get]
key_files:
  created:
    - internal/remoteconfig/remoteconfig.go
    - internal/remoteconfig/remoteconfig_test.go
  modified:
    - internal/config/config.go
    - cmd/earlscheib/main.go
decisions:
  - "Sign empty body []byte(\"\") for HMAC on GET requests — matches server validation pattern"
  - "Merge uses temp-file + os.Rename for atomic config.ini write"
  - "AllowedKeys contains exactly [webhook_url, log_level] — secret_key and watch_folder excluded per OPS-04"
  - "04-01 not yet applied when this plan executed — remoteconfig added directly to existing runScan (not inside telemetry.Wrap)"
metrics:
  duration: "~3 minutes"
  completed: "2026-04-20"
  tasks: 2
  files: 4
---

# Phase 4 Plan 02: Remote Config Summary

**One-liner:** HMAC-signed remote-config fetch with atomic INI merge and OPS-04 whitelist enforcement (webhook_url, log_level only).

## What Was Built

### config.Merge (internal/config/config.go)

Pure function that atomically patches an INI file with whitelisted remote values:

- **Signature:** `Merge(path string, remote map[string]string, allowed []string) (changed bool, err error)`
- **Whitelist enforcement:** builds an `allowSet` from `allowed`; any key not in the set is silently discarded
- **Atomic write:** creates a temp file (`.ini.tmp`) in the same directory via `os.CreateTemp`, writes the full updated INI, closes, then `os.Rename` replaces the original — crash-safe, no partial writes
- **No-op on empty:** returns `changed=false` immediately when `remote` is empty (no file touch)
- **Idempotent on missing file:** uses `ini.Empty()` when config.ini does not yet exist

### remoteconfig package (internal/remoteconfig/remoteconfig.go)

**`Fetch(ctx, webhookURL, secret, logger)`**
- Constructs URL as `strings.TrimRight(webhookURL, "/") + "/remote-config"`
- HMAC signing: calls `webhook.Sign(secret, []byte(""))` — signs the empty body for GET requests. This is byte-identical to the Python reference `hmac.new(secret.encode('utf-8'), b"", hashlib.sha256).hexdigest()`. Sets `X-EMS-Signature` header.
- `http.Client{Timeout: 5 * time.Second}` — scan never blocks longer than 5s for remote config
- 204 No Content → returns `nil, nil` (server signals no changes; not an error)
- Non-2xx → returns error (caller logs and continues)
- Body limited to 4096 bytes via `io.LimitReader`

**`Apply(cfgPath, remote, logger)`**
- Delegates to `config.Merge(cfgPath, remote, AllowedKeys)`
- `AllowedKeys = []string{"webhook_url", "log_level"}` — secret_key and watch_folder explicitly excluded per OPS-04

**`AllowedKeys`** — exported slice; can be iterated by callers to log which keys were applied.

### main.go integration (cmd/earlscheib/main.go)

04-01 had not been applied when this plan executed. Remote config fetch was added directly to the existing `runScan()` function before `LoadConfig`:

```
runScan():
  1. config.LoadConfig(cfgPath)         ← initial load for webhook URL
  2. remoteconfig.Fetch(...)             ← best-effort; errors → stderr, scan continues
  3. remoteconfig.Apply(cfgPath, ...)   ← if remote non-empty, atomic merge
  4. config.LoadConfig(cfgPath)         ← effective config (possibly updated)
  5. logging.SetupLogging(...)
  ... rest of scan
```

Note: When 04-01 applies `telemetry.Wrap`, the remoteconfig block is already inside `runScan()` and will be enclosed in the Wrap closure automatically — no further adjustment needed.

## Deviations from Plan

None — plan executed exactly as written. The "if 04-01 not applied" code path was used since 04-01 had not yet modified main.go when this plan ran.

## Test Coverage

12 tests across two packages:

| Test | What it verifies |
|------|-----------------|
| TestMerge_UpdatesAllowedKey | webhook_url updated, watch_folder unchanged |
| TestMerge_BlacklistedKeysNotWritten | secret_key + watch_folder dropped, changed=false |
| TestMerge_EmptyRemote_NoWrite | Empty map → no write, changed=false |
| TestMerge_AtomicWrite | No .tmp files after merge; config.ini exists and valid |
| TestFetch_SuccessfulResponse | JSON parsed and returned |
| TestFetch_EmptyJSONObject | {} → empty map, no error |
| TestFetch_404ReturnsError | Non-2xx → error returned |
| TestFetch_HMACSignatureHeader | X-EMS-Signature = Sign(secret, "") |
| TestFetch_TimeoutEnforced | 7s server → error before 6s |
| TestApply_OnlyAllowedKeysWritten | webhook_url written, secret_key dropped |
| TestApply_EmptyRemote_NothingWritten | changed=false |
| TestAllowedKeys_Contents | Exactly [webhook_url, log_level]; no secret_key, no watch_folder |

## Commits

| Hash | Description |
|------|-------------|
| daf8e70 | test(04-02): add failing tests for remoteconfig and config.Merge |
| 00e1f0e | feat(04-02): add config.Merge and internal/remoteconfig package |
| c24d5cd | feat(04-02): wire remoteconfig.Fetch+Apply into runScan before LoadConfig |

## Self-Check: PASSED
