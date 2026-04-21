# Phase 5: Queue Admin UI - Context

**Gathered:** 2026-04-21
**Status:** Ready for planning
**Mode:** Smart discuss (autonomous) — 3 grey areas, all accepted at recommended defaults

<domain>
## Phase Boundary

Add a Marco-facing queue-inspection UI so pending outbound SMS jobs (on the server's `jobs.db`) are visible and cancellable before they send. Delivered in two halves:

1. **Client-side launcher:** `earlscheib.exe --admin` starts a local HTTP server bound to `127.0.0.1:RANDOM_PORT`, opens Marco's default browser to that URL, and serves an embedded single-page app. The Go process acts as an HMAC-signing proxy between the browser (zero-secret) and the remote `/earlscheibconcord/queue` endpoint.
2. **Server-side endpoint:** extend `app.py` with `/earlscheibconcord/queue` supporting `GET` (list pending jobs) and `DELETE` (cancel a job by id). Both HMAC-validated like existing routes; reject unsigned with 401.

Out of scope (this phase):
- Edit/reschedule jobs (cancel only; rescheduling means a new BMS event anyway)
- View already-sent history (future phase — `sent = 1` rows stay hidden here)
- Mobile/phone UI — desktop only, local-browser only
- Auth beyond HMAC (no Marco login; the baked-in binary secret is the only factor)
- Persistent background tray (--admin is launched on demand)
- Rotating port / public access — strictly localhost

Delivers success criteria 1–6 from ROADMAP Phase 5. Introduces new req-ID block `ADMIN-*` (to be numbered during planning).

</domain>

<decisions>
## Implementation Decisions

### Admin Launcher Architecture

- **Static asset delivery:** `go:embed` the `internal/admin/ui/` directory (HTML, CSS, compiled JS) into the binary. Single-exe remains true; no on-disk UI files. The embedded FS is served via `http.FileServer(http.FS(uiFS))` on the local HTTP server.
- **Port binding:** `127.0.0.1:0` — OS picks a random ephemeral port. After `Listen()` returns, read `listener.Addr().(*net.TCPAddr).Port` and build the URL. Print the URL to stdout and open it via `runtime.GOOS`-specific shellout (Windows: `rundll32 url.dll,FileProtocolHandler`; fallback: `cmd /c start`).
- **Server lifecycle:** Ctrl+C (SIGINT) shuts down cleanly via `http.Server.Shutdown(ctx)`. Browser page posts a lightweight `POST /alive` heartbeat every 10 seconds; if no heartbeat arrives for 30 seconds, the Go process exits (covers "Marco closed the tab"). The server also cleanly exits on its own `context.Cancel`.
- **SPA → remote queue API:** the SPA calls only the **local** proxy routes (`GET /api/queue`, `POST /api/cancel`). The local Go process receives these, signs the outbound request with HMAC-SHA256 using the binary-embedded secret (same `secretKey` var as scanner/telemetry), and forwards to the configured `{webhook_url}/earlscheibconcord/queue`. The browser never sees the secret. Forward 4xx/5xx from the remote transparently; map 401 to a user-facing "authentication failed — contact support" banner.

### Server `/queue` Endpoint

- **Route:** `/earlscheibconcord/queue` on `app.py`. Both `GET` and `DELETE` validate `X-EMS-Signature` via `_validate_hmac(body, sig)`. 401 + JSON `{"error": "invalid signature"}` on failure.
- **GET response shape:** `200 OK` with JSON array:
  ```json
  [
    {
      "id": 42,
      "doc_id": "ABC123",
      "job_type": "24h",
      "phone": "+15551234567",
      "name": "John Smith",
      "send_at": 1745242200,
      "created_at": 1745155800
    },
    ...
  ]
  ```
  - `WHERE sent = 0`, ordered by `send_at ASC`
  - GET signs **empty body** `b""` (matches remote-config pattern)
- **DELETE semantics:** `DELETE /earlscheibconcord/queue` with JSON body `{"id": 42}`.
  - Server validates HMAC over the exact JSON body bytes received (matches telemetry pattern).
  - Executes `DELETE FROM jobs WHERE id = ? AND sent = 0` (guard: never delete a sent job).
  - Returns `200 OK` with `{"deleted": 1}` on success, `404` with `{"error": "not found or already sent"}` if 0 rows affected.
- **Hook into existing `do_GET` / `do_POST`:** add a new method-router (e.g., `do_DELETE`) — Python's `BaseHTTPRequestHandler` already dispatches by method name. Follow existing dead-code-free style (remove the stale post-404 block found in audit).

### UI Design — "Concord Garage"

- **Aesthetic direction:** editorial / print-inspired. The screen reads like a classic auto-body work ticket — not a SaaS dashboard. Committed, opinionated.
- **Typography:**
  - Display: **Fraunces** (Google Fonts) — authoritative serif with soft optical curves. Used for the header and customer names.
  - Body / data: **JetBrains Mono** — numeric alignment for phone numbers and send-times; evokes shop-floor precision.
  - No Inter. No Roboto.
- **Palette (CSS variables):**
  - `--ink: #1B1B1B` (graphite — near-black body text)
  - `--paper: #F4EDE0` (cream — dominant background, warm not clinical)
  - `--oxblood: #7A2E2A` (deep garage-door red — primary accent, brand anchor)
  - `--amber: #E8A33D` (caution — undo pills, warning states)
  - `--steel: #8B8478` (muted gutter text)
- **Layout — customer-grouped job cards:**
  - Sticky top bar: brand wordmark "**Earl Scheib Concord — Queue**" in Fraunces, next-refresh countdown on the right
  - Grouped stack: one card per unique customer (grouped by `phone`). Card header shows Fraunces-set customer name in oxblood + phone in mono-graphite.
  - Inside each card: nested rows for each queued message. Left gutter: send-time in mono (e.g., `Tue 2:30 PM`), aligned. Middle: job_type label pill (`24h` / `3day` / `review`). Right: "cancel" button in oxblood link-style.
  - Empty state: large Fraunces italic `"Nothing queued right now."` centered with a small line rule above a paper-texture background block.
- **Cancel interaction:** one-click cancel → row fades to 50% + strike-through + amber "Undone?" pill slides in from the right with a 5-second progress ring. If Marco clicks it within 5s, the DELETE request is never sent (optimistic-cancel-with-undo). If the timer elapses, the local Go proxy fires the DELETE and the row is removed from the DOM on success.
- **Refresh:** auto-refetch every 15s; manual refresh via keyboard `R` or a discrete icon-button in the header.
- **Motion:** CSS-only. One staggered entrance animation on initial load (each customer card fades+slides in at 60ms stagger). Undo pill uses a CSS `@keyframes` conic-gradient countdown ring. No Motion/Framer — single-exe size matters.
- **Visual atmosphere:** paper background uses a subtle `background-image: url("data:image/svg+xml;utf8,...")` grain overlay at 3% opacity. Decorative `::before` top border on the header in oxblood, 4px solid, evoking a receipt.
- **No emojis. No SaaS gradients. No purple.**

### Build / Packaging

- New Go package `internal/admin` contains: the local HTTP server (`server.go`), embed FS (`ui/` subdir with `index.html`, `main.css`, `main.js`), proxy handlers (`proxy.go`), lifecycle + browser-open (`launcher.go`), tests.
- `--admin` subcommand wired in `cmd/earlscheib/main.go` following the existing dispatcher pattern. Wrapped in `telemetry.Wrap` so any panic is captured.
- No new non-stdlib Go dependencies. Pure stdlib `net/http`, `os/exec`, `encoding/json`, `crypto/hmac`, `crypto/sha256`.
- No CGO — keep CGO_ENABLED=0.
- JS is hand-written vanilla (no framework, no bundler). Approx 150–250 lines. Fits the single-exe constraint and the BOLD-direction mandate.

### Testing

- Go unit tests: proxy handler signs outbound requests correctly; lifecycle exits on heartbeat timeout; port-bind-to-zero returns a usable URL; DELETE forwards JSON body unchanged.
- Go integration test with `httptest.Server` as the remote: end-to-end GET proxy returns 200 + the test fixture array; DELETE returns deleted count; 401 from remote propagates.
- Server-side: add pytest coverage for `/queue` GET + DELETE — HMAC 401, happy path, DELETE idempotency on already-sent rows.
- Browser E2E: deferred to human UAT — cross-browser consistency not worth automating for a single-user localhost tool.

</decisions>

<code_context>
## Existing Code Insights

### Reusable Assets
- `internal/webhook/sign.go` — `Sign(secret, body) string` returns hex HMAC-SHA256. Use for both the GET-with-empty-body and DELETE-with-JSON-body.
- `internal/webhook/send.go` — pattern for HMAC-signed outbound requests; the admin proxy can use a trimmed version (no retry policy needed — this is a local UI, fail fast and show the error).
- `internal/config` — `LoadConfig` + `DataDir()` already provides `webhook_url` and data directory lookup; reuse to determine the remote endpoint base URL.
- `internal/telemetry` — `tel.Wrap(fn)` wraps the whole command body; apply the same pattern in `runAdmin` so panics in the admin server POST home.
- `app.py` — `_validate_hmac(body, sig)` helper already exists (Phase 4 addition), used by telemetry and remote-config routes. Reuse verbatim for the new `/queue` handlers.
- `app.py` — the existing `get_db()` + `jobs` table schema is the source of truth; no schema changes needed.
- Existing dispatcher in `cmd/earlscheib/main.go` has a `default` arm printing usage — add `--admin` alongside `--scan` / `--test` / `--status` / etc.

### Established Patterns
- Pure-Go, CGO_ENABLED=0 (no systray / WebView2 — the browser IS the UI)
- Secret injection via ldflags (`-X main.secretKey=...`) — reuse same var; admin proxy reads it via a small accessor injection
- slog logging to `C:\EarlScheibWatcher\ems_watcher.log` via `logging.SetupLogging` — admin server logs there too; its own command-prefix tag (`cmd=admin`)
- Python server-side routes follow a single `do_GET`/`do_POST` method switch in `WebhookHandler`; add `do_DELETE` cleanly, do not expand existing methods with path-by-path branching
- HMAC body conventions:
  - GET → sign `b""` (telemetry `/remote-config` precedent)
  - POST/DELETE → sign the exact body bytes received

### Integration Points
- `cmd/earlscheib/main.go` — new `runAdmin(tel)` case in the subcommand switch; re-use `secretKey` var; re-init telemetry with logger before running
- `internal/admin/` — new package housing server, proxy, launcher, embedded UI assets
- `app.py` — new `/earlscheibconcord/queue` route added into `do_GET` and a new `do_DELETE` method on `WebhookHandler`; pytest or light manual test per Phase 4 convention
- `Makefile` — no changes expected (embed FS is picked up by `go build` automatically via `//go:embed` directive)
- `.github/workflows/build.yml` — no changes expected (same cross-compile path)
- `docs/` — add a 1-page `admin-ui-guide.md` documenting how to launch `earlscheib.exe --admin` (for Marco's printed laminated reference card if desired)

</code_context>

<specifics>
## Specific Ideas

- **Heartbeat endpoint path:** `POST /alive` — no body, returns 204. JS `navigator.sendBeacon("/alive", "")` every 10s.
- **Browser-open on Windows:** `exec.Command("rundll32", "url.dll,FileProtocolHandler", url)` — works on Win10+ without console window, more reliable than `cmd /c start` for URLs with query strings.
- **Graceful shutdown sequence:** on SIGINT or heartbeat timeout → `httpServer.Shutdown(ctx)` with 5-second deadline → log "admin exiting" → `os.Exit(0)`.
- **Customer grouping key:** cluster by `phone` (since one doc_id can have multiple job_types); use the most recent `name` as display. If two customers somehow share a phone, they merge — acceptable for v1.
- **Send-time formatting:** local time (Marco's shop is PT) in the format `Tue 2:30 PM`. Use the browser's `Intl.DateTimeFormat` with `timeZone: "America/Los_Angeles"` hard-coded (the server scheduler already uses PT).
- **Undo pill implementation:** optimistic cancel never fires DELETE during the 5s window — keeps the cancellation truly recoverable. Timeout via `setTimeout` with a cleared timer handle on undo click.
- **No authentication prompt in UI** — the app's trust model is: if you can launch `earlscheib.exe --admin` on Marco's shop PC, you are already trusted. The localhost-only bind is the security boundary.
- **Port is not exposed in config.ini** — it's ephemeral; the URL is printed to stdout and auto-opened. No config field added.
- **Admin mode is idempotent with --scan** — they must never run concurrently against the DB. Not a concern: `--admin` doesn't touch the local `ems_watcher.db` at all; it reads the remote `jobs.db` via the proxy.
- **Icon/favicon in UI:** small oxblood "ES" monogram SVG, inlined in the HTML head — no extra HTTP round-trip.

</specifics>

<deferred>
## Deferred Ideas

- **View sent-job history** — out of scope for v1; can be added as `/queue?include_sent=1` later.
- **Edit / reschedule** — cancel-only for v1. A new BMS event achieves "reschedule" via the existing scheduler.
- **Filter by customer / job_type** — the whole page fits on a screen for the expected 5–30 pending jobs; add filters only when the list grows.
- **Admin mode password / second factor** — not worth the friction on Marco's shop PC. Revisit if the machine becomes multi-user.
- **Audit log of cancellations** — server-side `audit.log` for "who cancelled what when" can be added if Earl Scheib Concord's ops ever exceed one operator.
- **Tray launcher entry** — tray is out of scope this milestone. A desktop shortcut created by the installer is fine.
- **Live WebSocket updates** — 15s polling is sufficient for the queue volume; SSE/WS adds code weight for no real gain.

</deferred>
