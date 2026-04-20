# Phase 2: Core Scanner - Context

**Gathered:** 2026-04-20
**Status:** Ready for planning
**Mode:** Port from Python reference (discuss skipped — behaviour is locked by existing working implementation)

<domain>
## Phase Boundary

`earlscheib.exe --scan` behaves identically to the Python reference watcher `claude-code-project/ems_watcher.py` — same dedup, same settle check, same HMAC signatures, same retry/backoff, same heartbeat, same `runs` table schema, same log rotation behaviour. Fully testable in CI without a Windows VM.

Delivers requirements: SCAN-01..14.

Out of this phase: tray UI, wizard UI, installer — all later phases.

</domain>

<decisions>
## Implementation Decisions (anchored by Python reference)

### Porting Principle
The Python reference file is the SPEC. Behaviour must be identical at the byte level for HMAC signatures and at the semantic level for everything else. Do NOT improve, simplify, or refactor behaviour in this port. Cross-language parity tests required.

### Module layout
- `internal/config` — ini parsing, `LoadConfig(path) (Config, error)`
- `internal/db` — SQLite access (modernc.org/sqlite pure-Go), `runs` + `processed_files` schema, WAL mode, busy_timeout=30000, 5-retry lock backoff
- `internal/scanner` — the scan loop: list candidates, settle check, dedup, read, hand off to webhook
- `internal/webhook` — HMAC signing + POST + retry/backoff (3 attempts, exp backoff, retry on 408/425/429/5xx + transient network errors only)
- `internal/heartbeat` — lightweight POST to `/heartbeat`
- `internal/logging` — slog + lumberjack rotation; 2MB × 5 backups; console + file handlers
- `cmd/earlscheib/main.go` — updated to wire `--scan`, `--test`, `--status` subcommands to internal/ packages (remove stubs for these; leave other stubs for later phases)

### Exact behaviour to preserve

| Aspect | Python reference | Go requirement |
|--------|-----------------|----------------|
| Candidate extensions | `.xml`, `.ems` (case-insensitive) | Match |
| Dedup key | `(filepath, mtime)` PRIMARY KEY | Match |
| Settle check | 4 samples at 2s; require 2 consecutive matches of BOTH mtime and size | Match |
| HMAC algorithm | `hmac.new(key.encode('utf-8'), body, 'sha256').hexdigest()` | Must produce byte-identical signatures |
| HTTP headers | `Content-Type: application/xml; charset=utf-8`, `X-EMS-Filename: <basename>`, `X-EMS-Source: EarlScheibWatcher`, `X-EMS-Signature: <hex>` | Match exactly (same casing, same order-insensitive) |
| Retry count | 3 total attempts; exp backoff starting 1.0s, doubling | Match |
| Retryable HTTP status | 408, 425, 429, and 500-599 | Match |
| Heartbeat | POST `{webhook_url}/heartbeat` with body `<Heartbeat><Host>{hostname}</Host></Heartbeat>`, headers include `X-EMS-Heartbeat: 1` | Match |
| SQLite schema | `processed_files(filepath TEXT, mtime REAL, size INTEGER, sha256 TEXT, sent_at TEXT DEFAULT datetime('now'))` PK (filepath,mtime); `runs(run_at TEXT DEFAULT datetime('now'), processed INTEGER, errors INTEGER, note TEXT)` | Match — keep column names and types compatible so an existing .db can be opened |
| Log format | `%(asctime)s [%(levelname)s] %(message)s` with `%Y-%m-%d %H:%M:%S` | Match shape — use slog with custom handler format to match |
| Log rotation | 2 MB, 5 backups | Match |
| PII protection | BMS XML content NOT logged | Match — log filename + size only |

### Tests
- Unit tests for each package with Go's standard testing
- **Cross-language HMAC parity test** (MANDATORY per PITFALLS): given a fixed secret + body, Python HMAC output must equal Go HMAC output. Use a table of 3 fixtures: empty body, small ASCII, and a realistic BMS XML payload with unicode characters. Assert exact hex equality.
- Webhook test uses `httptest.NewServer` — no real network
- Settle check test uses a tempdir + goroutine that writes, then stops writing
- Retry test uses `httptest.Server` that returns 500/503 first N times
- Dedup test exercises `(filepath, mtime)` re-insertion and confirms no re-post

### Dev override for Linux testing
The real `C:\EarlScheibWatcher\` paths are Windows-only. Provide an env var `EARLSCHEIB_DATA_DIR` that overrides the data directory location so tests and Linux dev runs work. If unset on Windows, default to `C:\EarlScheibWatcher\`; if unset on non-Windows, default to `$HOME/.earlscheib-dev/`.

### --test payload
Use identical BMS test payload as `TEST_BMS_XML` in `ems_watcher.py` (document ID "TEST-EMS-WATCHER"). Port verbatim.

### --status
Must reproduce the same output shape as the Python version's `run_status` (folder reachability, file count, last run, today/total counts, recent files, recent log errors). The tray process (Phase 3) will read this info from the DB directly rather than shelling out — but `--status` subcommand stays as the human-facing CLI.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- Python reference to port: `claude-code-project/ems_watcher.py`
- Config shape: `claude-code-project/config.ini`
- Phase 1 delivered: `cmd/earlscheib/main.go` dispatcher; `Makefile build-linux` target (for CI unit tests); `var secretKey string` already wired via ldflags

### Established Patterns
- Pure-Go stack: modernc.org/sqlite, gopkg.in/ini.v1, hashicorp/go-retryablehttp, gopkg.in/natefinch/lumberjack.v2, log/slog (stdlib)
- CGO_ENABLED=0 — verified in Phase 1 CI

### Integration Points
- `--scan`: new implementation, replaces stub
- `--test`: new implementation, replaces stub
- `--status`: new implementation, replaces stub
- `--tray`, `--wizard`, `--install` stubs remain for later phases

</code_context>

<specifics>
## Specific Ideas

- The Python reference has an `--install` that registers a Scheduled Task — that stays in Phase 4's installer scope, not here; keep the Go `--install` as a stub
- The Python `--loop` mode (foreground polling) is useful for dev; port it behind a dev-only flag so we don't accidentally ship it as a user feature
- Unit tests run in CI via `make test` (add this target to Makefile); `go test ./... -race -count=1`
- Cross-language parity test: commit a small Python script `scripts/hmac-parity.py` that prints canonical HMAC hexes for the fixtures; the Go test calls it via `os/exec` in CI (Python 3 is available on ubuntu-latest) — or, simpler, hardcode the expected hex strings after computing them once and pinning them (same result, no exec dependency). Pick simpler.

</specifics>

<deferred>
## Deferred Ideas

- Log-level runtime override via remote config — Phase 5 (OPS)
- Prometheus/OpenTelemetry instrumentation — out of v1 scope
- Parallel scanning of multiple folders — single folder only in v1

</deferred>
