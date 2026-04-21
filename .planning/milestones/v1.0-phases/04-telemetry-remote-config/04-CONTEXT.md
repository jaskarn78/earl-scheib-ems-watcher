# Phase 4: Telemetry + Remote Config - Context

**Gathered:** 2026-04-20
**Status:** Ready for planning
**Mode:** Scope-adjusted — no tray process means "remote config poller" runs as part of each `--scan` invocation (every 5 min), not as a persistent background loop

<domain>
## Phase Boundary

Add observability and remote control to the shipped installer without changing the install-time flow:
1. **Crash telemetry**: any unhandled panic in `--scan` / `--test` / `--status` serializes a minimal record (type, file:line, OS, version, ts) and posts HMAC-signed to `{webhook_url}/telemetry`. No PII, no payload contents.
2. **Remote config polling**: at the start of each `--scan` invocation, fetch `{webhook_url}/remote-config` via HMAC-authenticated GET. If the server returns a whitelisted field change (`webhook_url` or `log_level` only), atomically merge into `config.ini`. Never accept `secret_key`, `watch_folder`, or arbitrary keys.
3. **Server-side endpoints**: extend `app.py` with `/earlscheibconcord/telemetry` (POST, HMAC-validated, stores to a local log file or db) and `/earlscheibconcord/remote-config` (GET, HMAC-validated, returns a JSON object from a file-backed config).
4. **Twilio sandbox→production SMS switch**: add a documented comment block in `app.py` explaining the one-line change needed to switch from WhatsApp sandbox to real SMS (change `TWILIO_FROM`, drop `whatsapp:` prefix).

Delivers requirements: OPS-01, OPS-02, OPS-03, OPS-04, OPS-05, OPS-06, OPS-07.

Out of scope (permanently):
- Persistent background poller (no tray exists — no long-running process to host it)
- Third-party SaaS crash reporters (Sentry, Rollbar) — PII egress risk
- Fetching telemetry from the server (one-way push only)
- Remote config for `secret_key` or `watch_folder` (safety)

</domain>

<decisions>
## Implementation Decisions

### Client side

**Go package: `internal/telemetry`**
- `Init(webhookURL, secret, logger *slog.Logger) *Telemetry` — install recover wrapper
- `Telemetry.Capture(r any, stackTrace string)` — called from deferred recover
- `Telemetry.Wrap(fn func() error) error` — helper that defers a recover that routes to Capture
- Payload shape:
  ```json
  {
    "type": "panic" | "error",
    "message": "runtime error: index out of range",
    "file": "internal/scanner/scan.go",
    "line": 123,
    "os": "windows-10",
    "app_version": "0.1.0",
    "ts": "2026-04-20T12:34:56Z"
  }
  ```
- NO BMS XML body, NO goroutine dumps with variable values, NO customer PII
- POST to `{webhook_url}/telemetry` with `X-EMS-Telemetry: 1` and HMAC-SHA256 of JSON body
- Failures are logged at debug level and silently dropped — no infinite retry, no recursion

**Go package: `internal/remoteconfig`**
- `Fetch(ctx, webhookURL, secret, logger *slog.Logger) (map[string]string, error)` — GET `{webhook_url}/remote-config`
- `Apply(cfgPath string, remote map[string]string, allowed []string, logger *slog.Logger) (changed bool, err error)` — merges ONLY whitelisted keys (`webhook_url`, `log_level`) into the ini file via atomic write (temp file + rename)
- Called at the top of `cmd/earlscheib/main.go` `runScan()` BEFORE loading the effective config. Best-effort — if remote-config fetch fails, log and continue with local config.
- HMAC signing: same pattern as webhook POST; sign the empty body for GET (`""`) or use a canonical string (`GET\n/earlscheibconcord/remote-config`) — pick one and document. Simplest: GET with `X-EMS-Signature: <hex of empty string HMAC>` and server validates the same way.

**Integration: `cmd/earlscheib/main.go`**
- `runScan()` wraps the entire scan operation in `telemetry.Wrap(...)` so any panic is captured before exit
- Before loading config: best-effort `remoteconfig.Fetch` + `Apply`; then load the (possibly updated) `config.ini`
- `runTest()` and `runStatus()` also wrap with telemetry but do NOT run remote-config fetch (they're idempotent / local)

### Server side

**Extend `app.py`**
- New route: `POST /earlscheibconcord/telemetry`
  - HMAC-validate like other routes
  - Parse JSON, append to `telemetry.log` with request metadata (ts, client_ip, user-agent) or store in SQLite `telemetry` table
  - Respond 204 No Content on success
- New route: `GET /earlscheibconcord/remote-config`
  - HMAC-validate
  - Load `remote_config.json` from app dir (file-backed, edit with any text editor)
  - Respond with JSON body: `{"webhook_url": "...", "log_level": "INFO"}` or `{}` if no overrides
- Default `remote_config.json` is empty `{}` shipped in repo
- Document both endpoints in `app.py` module docstring

**Twilio SMS switch block** in `app.py`:
```python
# ===== Twilio WhatsApp (sandbox) → SMS (production) switch =====
# Currently using Twilio WhatsApp sandbox for dev/test.
# To switch to production SMS:
#   1. In .env, change TWILIO_FROM from "whatsapp:+14155238886" to your Twilio SMS number (e.g. "+15551234567")
#   2. In this file, remove the "whatsapp:" prefix from both `to=` and `from_=` in the Twilio API call below
# No other changes needed. The rest of the scheduler, HMAC validation, and dedup logic is SMS/WhatsApp agnostic.
# ================================================================
```
Add as a clearly-labeled comment block immediately above the `client.messages.create(...)` call.

### Testing

- Client unit tests for `telemetry.Wrap` (panic → Capture called with correct fields; no PII), `remoteconfig.Apply` (whitelist enforced, secret_key/watch_folder never overridden), `remoteconfig.Fetch` (httptest server)
- Server-side: pytest (or light manual test) for both endpoints — HMAC validation, whitelist enforcement, 204 response

### Server-side dev/test environment
The server `app.py` is on a Linux VM (`support.jjagpal.me`). Editing it from this dev environment is out of scope — commit the change to the repo and document deployment steps. The server deploys from the repo.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/webhook/sign.go` — HMAC-SHA256 Sign(secret, body) — reuse for telemetry POST and remote-config GET
- `internal/webhook/send.go` — SendWithClient pattern reusable for telemetry POST
- `internal/config` — ini read/write; will gain `Merge(remote map[string]string)` helper
- `app.py` — existing HMAC validation pattern can be extracted into a decorator; new routes mirror the existing `/earlscheibconcord` main route

### Established Patterns
- Pure-Go, CGO_ENABLED=0 still holds (all new packages are net/http + stdlib)
- Secret injection via ldflags — already in place
- Logging via slog — already wired

### Integration Points
- `cmd/earlscheib/main.go` `runScan()` — wrap with telemetry.Wrap + call remoteconfig at start
- `app.py` — add two new @app.route handlers, one new file `remote_config.json` in repo
- `docs/cert-procurement.md` stays; no signing changes this phase

</code_context>

<specifics>
## Specific Ideas

- The remote-config fetch uses a 5-second timeout — never block `--scan` for long if the server is slow
- On client startup, if the binary was launched by the Scheduled Task (detect via `--scan`), telemetry init reads GSD_APP_VERSION from ldflags-injected build info (add a `var appVersion = "dev"` variable, injected via `-X main.appVersion=0.1.0`)
- Make telemetry failures completely silent in production — a broken telemetry endpoint must NEVER break the scan
- The server should cap telemetry log file growth (rotate or size-cap) — out of scope for this phase, document as tech debt

</specifics>

<deferred>
## Deferred Ideas

- Server-side telemetry dashboard / aggregation — Marco/dev just `tail -f telemetry.log` for v1
- Client auto-update via remote-config signal — could be added later
- Remote config schema versioning — simple key-value for v1; v2 if scope grows
- Replay-protection via HMAC timestamp — ARCH research noted server-side lacks it; add at v2

</deferred>
