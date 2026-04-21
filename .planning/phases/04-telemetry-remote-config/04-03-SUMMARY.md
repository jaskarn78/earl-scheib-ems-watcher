---
phase: 04-telemetry-remote-config
plan: 03
subsystem: api
tags: [python, hmac, telemetry, remote-config, twilio, webhook]

# Dependency graph
requires:
  - phase: 04-01
    provides: telemetry.go client — defines the POST payload shape and X-EMS-Telemetry header
  - phase: 04-02
    provides: remoteconfig.go client — defines the GET contract and HMAC-of-empty-body signing
provides:
  - POST /earlscheibconcord/telemetry — HMAC-validated, appends to telemetry.log, 204 on success
  - GET /earlscheibconcord/remote-config — HMAC-validated (empty body), returns remote_config.json, 204 or 200+JSON
  - _validate_hmac helper reused by all HMAC-validated routes
  - remote_config.json default empty file committed to repo
  - Twilio WhatsApp->SMS switch comment in send_sms()
affects: [deployment, ops, monitoring]

# Tech tracking
tech-stack:
  added: [hmac (stdlib), hashlib (stdlib)]
  patterns:
    - _validate_hmac(body, sig_header) helper for constant-time HMAC comparison — reuse in all future routes
    - GET endpoint signed with HMAC of empty body (b"") — matches remoteconfig.go Fetch() signing contract
    - File-backed remote config (remote_config.json) — read on every request, no restart needed after edits

key-files:
  created:
    - remote_config.json
  modified:
    - app.py
    - .gitignore

key-decisions:
  - "HMAC of empty body for GET /remote-config: simplest approach, byte-identical to Python hmac.new(secret, b'', sha256) — matches Go's webhook.Sign(secret, []byte(''))"
  - "File-backed remote_config.json (not SQLite) for simplicity: Marco or developer edits the file on the server, no DB migration needed"
  - "telemetry.log as append-only JSONL file: easy to tail -f, no schema migration, rotation deferred to tech debt"
  - "204 No Content when remote_config.json is empty {}: client interprets 204 as no-op and skips merge"

patterns-established:
  - "_validate_hmac(body, sig_header) -> bool: constant-time HMAC validation via hmac.compare_digest; use for all authenticated routes"
  - "GET with HMAC-of-empty-body: sign b'' for no-body requests; server validates _validate_hmac(b'', sig)"

requirements-completed: [OPS-06, OPS-07]

# Metrics
duration: 15min
completed: 2026-04-21
---

# Phase 4 Plan 03: Server Telemetry + Remote Config Endpoints Summary

**Server-side HMAC-validated POST /telemetry (204 + JSONL log) and GET /remote-config (file-backed JSON) endpoints added to app.py, with a shared _validate_hmac helper and a Twilio WhatsApp->SMS switch comment**

## Performance

- **Duration:** ~15 min
- **Started:** 2026-04-21T00:00:00Z
- **Completed:** 2026-04-21T00:15:00Z
- **Tasks:** 2
- **Files modified:** 3 (app.py, remote_config.json, .gitignore)

## Accomplishments
- Added `_validate_hmac(body, sig_header)` helper using `hmac.compare_digest` (constant-time, prevents timing attacks); used by both new endpoints
- Added `POST /earlscheibconcord/telemetry`: validates X-EMS-Signature, appends structured JSONL record (ts, client_ip, user_agent, payload_bytes, event payload) to telemetry.log, returns 204
- Added `GET /earlscheibconcord/remote-config`: validates HMAC of empty body, loads remote_config.json, returns 204 (empty) or 200+JSON (populated)
- Added `TELEMETRY_LOG_PATH` and `REMOTE_CONFIG_PATH` module-level constants (env-overridable)
- Added clearly-labeled Twilio WhatsApp->SMS switch comment block above `from_number` in `send_sms()`
- Created `remote_config.json` with default `{}` and added `telemetry.log` to `.gitignore`

## Task Commits

Each task was committed atomically:

1. **Task 1: Add HMAC helper, /telemetry + /remote-config endpoints, Twilio comment** - `61101ec` (feat)
2. **Task 2: Create remote_config.json default and update .gitignore** - `ffaea69` (chore)

**Plan metadata:** (see final commit below)

## Files Created/Modified
- `app.py` - Added `import hmac`/`hashlib`, `TELEMETRY_LOG_PATH`, `REMOTE_CONFIG_PATH`, `_validate_hmac()` helper, `POST /earlscheibconcord/telemetry` handler (do_POST, first check), `GET /earlscheibconcord/remote-config` handler (do_GET, before default 404), Twilio SMS switch comment in `send_sms()`
- `remote_config.json` - New file; default empty `{}`; edit on server to push config overrides (no restart needed)
- `.gitignore` - Added `telemetry.log` (runtime output, never committed)

## HMAC Validation Approach

`_validate_hmac(body: bytes, sig_header: str) -> bool` is the single validation helper:

```python
def _validate_hmac(body: bytes, sig_header: str) -> bool:
    if not CCC_SECRET or not sig_header:
        return False
    expected = hmac.new(CCC_SECRET.encode("utf-8"), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, sig_header)
```

- **POST /telemetry**: `_validate_hmac(raw_body_bytes, X-EMS-Signature)` — signs actual JSON body
- **GET /remote-config**: `_validate_hmac(b"", X-EMS-Signature)` — signs empty body, matching Go client's `webhook.Sign(secret, []byte(""))`

## telemetry.log Format

One JSON line per event, appended to `TELEMETRY_LOG_PATH` (defaults to `telemetry.log` next to `app.py`):

```json
{"ts": "2026-04-21T00:12:34.567890+00:00", "client_ip": "1.2.3.4", "user_agent": "EarlScheibWatcher/0.1.0", "payload_bytes": 187, "event": {"type": "panic", "message": "...", "file": "internal/scanner/scan.go", "line": 42, "os": "windows", "app_version": "0.1.0", "ts": "2026-04-21T00:12:34Z"}}
```

Monitor with: `tail -f telemetry.log`

Log rotation is **deferred tech debt** — not implemented in this phase. If log grows large, use `logrotate` on the server.

## remote_config.json Usage

File lives at `REMOTE_CONFIG_PATH` (defaults to `remote_config.json` next to `app.py`). Edit it directly on the server — **no restart required**, changes take effect on the next client poll (~5 min).

Default (no overrides):
```json
{}
```

Example with active overrides:
```json
{
  "webhook_url": "https://support.jjagpal.me/earlscheibconcord",
  "log_level": "DEBUG"
}
```

Valid keys: `webhook_url`, `log_level` (enforced by the Go client's whitelist in `remoteconfig.AllowedKeys`).

## Twilio Comment Placement

Added at `send_sms()` function, immediately above `from_number = f"whatsapp:{TWILIO_FROM}"` (app.py line ~208). Explains the exact two-step switch needed to go from WhatsApp sandbox to production SMS.

## Deployment Note

The server (`support.jjagpal.me`) deploys from this repo. Steps to deploy:

1. `git pull` on the server
2. `sudo systemctl restart app` (or whatever the process manager is)
3. For `remote_config.json` edits only: no restart needed — file is read on every request

App.py changes (new endpoints) require a server restart to take effect.

## Manual Test Steps (no pytest fixtures in repo)

```bash
# 1. Start server locally
CCC_SECRET=testsecret python3 app.py &

# 2. Compute HMAC of a telemetry payload
BODY='{"type":"error","message":"test","file":"cmd/main.go","line":1,"os":"linux","app_version":"0.1.0","ts":"2026-04-21T00:00:00Z"}'
SIG=$(echo -n "$BODY" | python3 -c "import sys,hmac,hashlib; body=sys.stdin.buffer.read(); print(hmac.new(b'testsecret',body,hashlib.sha256).hexdigest())")

# 3. Valid telemetry POST -> expect 204
curl -s -o /dev/null -w "%{http_code}" -X POST http://localhost:8200/earlscheibconcord/telemetry \
  -H "Content-Type: application/json" \
  -H "X-EMS-Telemetry: 1" \
  -H "X-EMS-Signature: $SIG" \
  -d "$BODY"
# Expected: 204

# 4. Invalid signature -> expect 401
curl -s -o /dev/null -w "%{http_code}" -X POST http://localhost:8200/earlscheibconcord/telemetry \
  -H "Content-Type: application/json" -H "X-EMS-Signature: badhash" -d "$BODY"
# Expected: 401

# 5. Valid remote-config GET -> expect 204 (empty config)
SIG_EMPTY=$(python3 -c "import hmac,hashlib; print(hmac.new(b'testsecret',b'',hashlib.sha256).hexdigest())")
curl -s -o /dev/null -w "%{http_code}" http://localhost:8200/earlscheibconcord/remote-config \
  -H "X-EMS-Signature: $SIG_EMPTY"
# Expected: 204

# 6. Invalid signature on GET -> expect 401
curl -s -o /dev/null -w "%{http_code}" http://localhost:8200/earlscheibconcord/remote-config \
  -H "X-EMS-Signature: badhash"
# Expected: 401
```

## Decisions Made

- HMAC of empty body (`b""`) for GET /remote-config: simplest approach, byte-identical to Go client's `webhook.Sign(secret, []byte(""))`. No need for canonical request strings.
- File-backed remote_config.json over SQLite: single-file edits require no schema migration; suitable for v1 single-operator use.
- 204 No Content when remote_config.json is `{}`: client's `remoteconfig.Fetch()` treats 204 as nil and skips merge — avoids unnecessary config file writes.
- telemetry.log as JSONL append-only file: easy to `tail -f`; no schema; rotation deferred to ops.

## Deviations from Plan

None — plan executed exactly as written.

## Issues Encountered

None.

## User Setup Required

None — no external service configuration required for these endpoints. Server deployment steps documented above.

## Next Phase Readiness

- All server-side endpoints for phase 4 are now complete
- Client (04-01, 04-02) and server (04-03) are ready for end-to-end integration testing
- Deploy app.py to `support.jjagpal.me` to activate the live endpoints
- Tech debt: telemetry.log rotation/size-capping (deferred per 04-CONTEXT.md)

---
*Phase: 04-telemetry-remote-config*
*Completed: 2026-04-21*
