---
quick_id: 260421-shq
type: execute
wave: 1
depends_on: []
files_modified:
  - internal/scanner/scan.go
  - internal/scanner/scanner_test.go
  - cmd/earlscheib/main.go
  - internal/admin/server.go
  - internal/admin/diagnostic.go
  - internal/admin/ui/index.html
  - internal/admin/ui/main.css
  - internal/admin/ui/main.js
  - app.py
  - EarlScheibWatcher-Setup.exe
  - EarlScheibWatcher-Setup.zip
autonomous: false
requirements:
  - DBG-01  # Scanner error detail (path + err) + per-scan startup INFO line
  - DBG-02  # Admin UI Diagnostic panel (watch folder, heartbeat, secret presence)
  - DBG-03  # Server-side /diagnostic endpoint (HMAC-auth, JSON)
  - DBG-04  # Release: rebuild, re-zip, verify MD5, push

must_haves:
  truths:
    - "Every --scan run emits one INFO line identifying watch_folder, webhook_url, and version at cycle start."
    - "When the watch folder cannot be read, the log shows the full path and the underlying Go/OS error verbatim (no generic message)."
    - "Marco can open `earlscheib.exe --admin`, click a Diagnostic tab, and see live folder status, file count, last scan, last heartbeat, and HMAC-secret presence without any JS console errors."
    - "Curl to the server `/earlscheibconcord/diagnostic` with a valid X-EMS-Signature returns a JSON object containing last_heartbeat, client_online, commands_state, recent_log_tail, and received_logs_count."
    - "A fresh installer built with GSD_HMAC_SECRET sourced from .env produces a signed exe whose embedded secret matches the server — verified by `curl -H 'X-EMS-Signature: <sig of empty body>' /diagnostic` returning 200, not 401."
    - "The live download URL (https://support.jjagpal.me/earlscheibconcord/download) serves the same byte-for-byte EarlScheibWatcher-Setup.zip as sits in the repo root (MD5 match)."
    - "commands.json is untouched — still `{\"upload_log\": false}` after the deploy."
  artifacts:
    - path: "internal/scanner/scan.go"
      provides: "scanner.Run startup INFO line + scanner.Candidates verbose error log"
      contains: "scan start: watch_folder"
    - path: "internal/admin/diagnostic.go"
      provides: "/api/diagnostic handler for admin UI"
      exports: ["handleDiagnostic"]
    - path: "internal/admin/ui/index.html"
      provides: "Diagnostic section markup (panel or second tab)"
      contains: "diagnostic"
    - path: "internal/admin/ui/main.js"
      provides: "5s poll loop against /api/diagnostic"
      contains: "/api/diagnostic"
    - path: "app.py"
      provides: "GET /earlscheibconcord/diagnostic endpoint (HMAC-authed)"
      contains: "/earlscheibconcord/diagnostic"
    - path: "EarlScheibWatcher-Setup.exe"
      provides: "Freshly rebuilt signed installer with prod HMAC secret baked in"
    - path: "EarlScheibWatcher-Setup.zip"
      provides: "Zip wrapper around the rebuilt exe, served by /download"
  key_links:
    - from: "cmd/earlscheib/main.go runScan"
      to: "scanner.Run startup INFO line"
      via: "cfg.Logger already plumbed — only scan.go changes needed"
      pattern: "scan start: watch_folder"
    - from: "internal/admin/ui/main.js (new pollDiagnostic loop)"
      to: "internal/admin/server.go mux /api/diagnostic → diagnostic.go handler"
      via: "setInterval(5000) fetch JSON; reads config + probes watch_folder + hits webhook /diagnostic"
      pattern: "fetch.*api/diagnostic"
    - from: "internal/admin/diagnostic.go"
      to: "app.py GET /earlscheibconcord/diagnostic"
      via: "HMAC-signed empty body, same pattern as commands.Poll + handleQueue"
      pattern: "webhook.Sign.*\\[\\]byte\\(\"\"\\)"
    - from: "Makefile build-windows"
      to: "main.secretKey ldflag injection"
      via: "GSD_HMAC_SECRET=$(grep CCC_SECRET .env | cut -d= -f2) make build-windows"
      pattern: "GSD_HMAC_SECRET="
---

<objective>
Ship three debuggability improvements so Marco's live debug session with the non-technical shop owner is unblocked: (1) scanner logs say **which** folder and **what** the OS error was — not a generic "Cannot read watch folder"; (2) the existing `--admin` browser UI gains a Diagnostic panel that shows folder health, heartbeat state, and HMAC-secret presence in real time; (3) a new server-side `/diagnostic` endpoint exposes last heartbeat, command state, and the tail of the most recent uploaded log — so we can inspect the deployment without asking Marco to copy-paste anything. Plus one release task to rebuild + deploy the signed installer with the production HMAC secret baked in.

Purpose: Reduce MTTR for live production debugging with a non-technical user to zero-shell ping-pong. Each improvement makes a distinct class of failure self-diagnosing.

Output: Four atomic commits — one per improvement + one release — landing on master, a fresh signed installer live on support.jjagpal.me verified by MD5 match, commands.json untouched.
</objective>

<execution_context>
@$HOME/.claude/get-shit-done/workflows/execute-plan.md
</execution_context>

<context>
@CLAUDE.md
@.planning/STATE.md

# Scanner target
@internal/scanner/scan.go
@internal/scanner/scanner_test.go
@cmd/earlscheib/main.go

# Admin UI target
@internal/admin/server.go
@internal/admin/embed.go
@internal/admin/proxy.go
@internal/admin/ui/index.html
@internal/admin/ui/main.css
@internal/admin/ui/main.js

# Server target + existing HMAC-auth patterns to mirror
@app.py
@internal/commands/commands.go
@internal/heartbeat/heartbeat.go

# Config lookup for admin diagnostic handler
@internal/config/config.go

# Build/release
@Makefile

<interfaces>
<!-- Key types and contracts needed across tasks. Extracted from codebase — executor should NOT re-grep. -->

From internal/scanner/scan.go (current, to be modified):
```go
// Candidates is called by Run; logs "Cannot read watch folder" on ReadDir error.
func Candidates(dir string, logger *slog.Logger) []string
// Run is the single scan cycle entry point called from cmd/earlscheib/main.go runScan.
func Run(cfg RunConfig) (int, int)
type RunConfig struct {
    WatchFolder string
    DB          *sql.DB
    Logger      *slog.Logger
    Sender      func(filePath string, body []byte) bool
    SettleOpts  SettleOptions
}
```

From cmd/earlscheib/main.go (already wired — pass appVersion through):
```go
var appVersion = "dev" // injected via ldflags -X main.appVersion=$(VERSION)
// runScan calls scanner.Run — appVersion is already in scope, but NOT currently
// passed in RunConfig. Task 1 must plumb it OR log the line directly from runScan.
// Decision: log it from scan.go Run() — keep the log ownership where the scan lives.
// To get appVersion in: add `AppVersion string` field to RunConfig; main.go sets it.
```

From internal/admin/server.go:
```go
type Config struct {
    WebhookURL       string
    Secret           string
    AppVersion       string // already plumbed — reuse
    Logger           *slog.Logger
    HeartbeatTimeout time.Duration
    ShutdownGrace    time.Duration
    OpenBrowser      func(url string) error
    URLCh            chan<- string
}
type server struct {
    cfg       Config
    client    *http.Client
    logger    *slog.Logger
    lastAlive *atomicTime
}
// Mux registration lives inside Run() at ~line 86-92. Add one more HandleFunc there.
func (s *server) remoteQueueURL() string // returns cfg.WebhookURL + "/queue"
```

From internal/admin/proxy.go (HMAC-auth-to-upstream pattern to mirror):
```go
// s.cfg.Secret is the baked HMAC secret. Example for a signed-empty-body GET:
//   sig := webhook.Sign(s.cfg.Secret, []byte(""))
//   req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
//   req.Header.Set("X-EMS-Signature", sig)
//   req.Header.Set("X-EMS-Source", "EarlScheibWatcher-Admin")
```

From internal/config/config.go:
```go
type Config struct {
    WatchFolder string
    WebhookURL  string
    LogLevel    string
}
func LoadConfig(path string) (Config, error) // returns defaults on missing/malformed
func DataDir() string                        // resolves data dir (respects EARLSCHEIB_DATA_DIR)
```

From app.py (handlers to pattern-match):
- `def do_GET(self):` at line 1638 — add a new `if path == "/earlscheibconcord/diagnostic":` block
- `def _validate_hmac(body: bytes, sig_header: str) -> bool:` at line 120 — use with body=b""
- `LAST_HEARTBEAT = {"ts": None, "host": None}` at line 168 — in-memory, resets on app.py restart
- `commands.json` lives at `os.path.dirname(os.path.abspath(__file__))/commands.json` — READ ONLY, MUST NOT be modified
- received_logs dir: `os.path.join(app_dir, "received_logs")` — contains `*.log` files + `latest.log` symlink
- Existing `/status` handler at line 1747 is the lightweight precedent (no HMAC there because it's meant for browser probes); mirror `/commands` for HMAC validation instead
```

From Makefile (HMAC secret injection — the trap from earlier today):
```makefile
HMAC_SECRET ?= $(GSD_HMAC_SECRET)
ifneq ($(strip $(HMAC_SECRET)),)
LDFLAGS += -X main.secretKey=$(HMAC_SECRET)
endif
# ⚠️ If GSD_HMAC_SECRET is unset or empty, main.secretKey stays at the dev default
# "dev-test-secret-do-not-use-in-production" — silently. Every request to the live
# server then fails HMAC and returns 401. This is what we hit earlier today.
# ALWAYS source .env before building: GSD_HMAC_SECRET="$(grep CCC_SECRET .env | cut -d= -f2)"
```
</interfaces>
</context>

<tasks>

<task type="auto" tdd="true">
  <name>Task 1: Scanner error detail + per-scan startup INFO line</name>
  <files>
    internal/scanner/scan.go
    internal/scanner/scanner_test.go
    cmd/earlscheib/main.go
  </files>
  <behavior>
    - Test 1: `scanner.Run` with a valid watch folder logs ONE INFO record at cycle start with key=value fields `event=scan_start`, `watch_folder=<path>`, `webhook=<url>`, `version=<ver>` — captured via a slog handler that records Records into a slice.
    - Test 2: `scanner.Candidates` called with a non-existent directory logs a WARN record whose message contains `Cannot read watch folder` AND whose attrs include the exact `dir` path and a non-nil `err` (assert err's Error() string contains the path — this verifies the *Go* error is forwarded, not swallowed).
    - Test 3: (regression) `scanner.Run` still returns `(0, 0)` and records `"no files"` when the folder is valid but empty — prove the new INFO line does not change control flow.
  </behavior>
  <action>
    1. **Extend RunConfig in `internal/scanner/scan.go`** (current struct at lines 17–25). Add two fields:
       ```go
       WebhookURL string // logged in scan_start INFO line; no functional use
       AppVersion string // logged in scan_start INFO line; no functional use
       ```
       Both are OPTIONAL — empty string renders as empty in the log (acceptable in tests).

    2. **Add the startup INFO line at the top of `scanner.Run`** (current function starts at line 54, right after the `opts` defaulting block, BEFORE `candidates := ...` at line 61):
       ```go
       logger.Info("scan start",
           "watch_folder", cfg.WatchFolder,
           "webhook", cfg.WebhookURL,
           "version", cfg.AppVersion,
       )
       ```
       Rationale for keyed attrs (not a format string): the project's custom `emsHandler` (per STATE.md Phase 02 decisions) renders slog records as `[LEVEL] message key=value ...`. This yields the literal required format:
       `[INFO] scan start watch_folder="C:\CCC\EMS_Export" webhook="https://..." version="0.4.0"`
       Spec says `[INFO] scan start: watch_folder="..." webhook="..." version="..."` — the trailing `:` on the message is a cosmetic preference; keep the log keyed (consistent with every other log line in the codebase). If handler renders without the colon, that's acceptable — the values ARE present and parseable, which is the only contract that matters for grep.

    3. **Fix `Candidates` at line 34** — current line: `logger.Warn("Cannot read watch folder", "dir", dir, "err", err)` already includes dir and err as attrs, BUT the spec calls for the literal format `[WARNING] Cannot read watch folder: path="<path>" err=<err>`. Rename attr key `dir` → `path` to match spec:
       ```go
       logger.Warn("Cannot read watch folder", "path", dir, "err", err)
       ```
       (The spec's "path=" key name is the load-bearing change — `dir=` is the current bug.)

    4. **Wire `WebhookURL` + `AppVersion` from `cmd/earlscheib/main.go` `runScan`** (the `scanner.Run(scanner.RunConfig{...})` call at lines 139–144). Pass them through:
       ```go
       processed, errors := scanner.Run(scanner.RunConfig{
           WatchFolder: cfg.WatchFolder,
           WebhookURL:  cfg.WebhookURL,
           AppVersion:  appVersion,
           DB:          sqlDB,
           Logger:      logger,
           Sender:      sendFn,
       })
       ```
       `appVersion` is already a package-level var in main.go (line 36) set via ldflags; no new plumbing needed.

    5. **Write/extend tests in `internal/scanner/scanner_test.go`** for behaviors 1–3 above. Use a custom `slog.Handler` that captures `slog.Record` into a `[]slog.Record` — match the pattern in heartbeat_test.go if present, otherwise write a minimal capturing handler inline. For Test 2 (non-existent dir), use `filepath.Join(t.TempDir(), "does-not-exist")`. For Test 1, call `scanner.Run` with an empty `t.TempDir()` and assert the first captured record's Level == slog.LevelInfo, Message == "scan start", and the attr map contains the three expected keys.

    6. Run `go test ./internal/scanner/... -race -count=1` — all three behaviors must pass.

    **DO NOT:**
    - Add a colon to the slog Message arg (e.g., `"scan start:"`) — slog conventions discourage punctuation in Message; the handler owns rendering.
    - Log from `runScan` directly — keep log ownership inside `scanner.Run` so it fires on every scan invocation including future callers (e.g., a tray manual-scan trigger).
    - Return any new error from `Candidates` — the "return empty on error, never panic" contract is load-bearing (matches Python `_list_candidates`).
  </action>
  <verify>
    <automated>cd /home/jjagpal/projects/earl-scheib-followup && go test ./internal/scanner/... -race -count=1 && go build ./... && go vet ./...</automated>
  </verify>
  <done>
    Unit tests pass. `go build ./...` succeeds (no unused field warnings — both new fields are consumed by the INFO log). Manual grep of the test output (or a scratch log file from `EARLSCHEIB_DATA_DIR=/tmp/es-test ./dist/earlscheib --scan`) shows both new lines with the exact required keys. scan.go diff is <20 lines; scanner_test.go adds one capturing handler helper and 2–3 new test functions.
  </done>
</task>

<task type="auto" tdd="false">
  <name>Task 2: Admin UI Diagnostic panel — Go handler + HTML/CSS/JS</name>
  <files>
    internal/admin/diagnostic.go
    internal/admin/server.go
    internal/admin/ui/index.html
    internal/admin/ui/main.css
    internal/admin/ui/main.js
  </files>
  <action>
    1. **Create `internal/admin/diagnostic.go`** — a new file in package `admin` defining `handleDiagnostic(w http.ResponseWriter, r *http.Request)` as a method on `*server`. Response shape (JSON):
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
         "last_heartbeat_at": "2026-04-21T20:31:05Z",
         "hmac_secret_present": true,
         "app_version": "0.4.0"
       }
       ```
       Implementation:
       - `cfg, _ := config.LoadConfig(filepath.Join(config.DataDir(), "config.ini"))` — reuse the public API, no Marco-edited file writes happen.
       - Folder probe: `os.Stat(cfg.WatchFolder)`; on success, `os.ReadDir` and count `.xml`/`.ems` entries (case-insensitive extension — mirror `scanner.Candidates` exactly). On stat or readdir error, set `folder_exists=false` (if IsNotExist) or leave `folder_error=err.Error()` (anything else).
       - Last-scan data: open `ems_watcher.db` at `filepath.Join(dataDir, "ems_watcher.db")` read-only. Query: `SELECT run_at, processed, errors, note FROM runs ORDER BY rowid DESC LIMIT 1` — same SQL as status.Print line 76–78. Handle `sql.ErrNoRows` cleanly (leave last_scan_* as zero values + last_scan_note="no scans yet"). Close DB immediately.
       - Last heartbeat: the Go client has NO local record of the last heartbeat it sent — heartbeats are fire-and-forget (`heartbeat.Send` doesn't persist anything). So `last_heartbeat_at` **from the Go side** is the timestamp of the most recent successfully-completed `heartbeat.Send` call. Since we don't persist that today, populate this field by doing a live GET to `{cfg.WebhookURL}/status` (the app.py `/status` endpoint at line 1747 already returns `{"status": "online", "last_seen": "2m ago", "host": "..."}` without auth). Keep a 3-second timeout; on error, set `last_heartbeat_at = "(server unreachable)"`. This makes the panel directly reflect server-side reality, which is what we actually want to see when debugging.
       - HMAC secret presence: `hmac_secret_present = s.cfg.Secret != "" && s.cfg.Secret != "dev-test-secret-do-not-use-in-production"` — treat the dev default as "not present" so the panel distinguishes a real-build binary from a `make build-windows` without GSD_HMAC_SECRET.
       - `app_version = s.cfg.AppVersion` (already on Config).
       - Use `w.Header().Set("Content-Type", "application/json")` then `json.NewEncoder(w).Encode(resp)`.
       - Wrap the whole body in `if r.Method != http.MethodGet { 405; return }`.

    2. **Register the handler in `internal/admin/server.go`** — inside `Run()`, right after the existing mux registrations at lines 86–92:
       ```go
       mux.HandleFunc("/api/diagnostic", s.handleDiagnostic)
       ```
       No other changes to server.go.

    3. **Update `internal/admin/ui/index.html`** to add a Diagnostic section. **Decision — single scroll page, NOT a tab switcher**: the current UI is one scroll page with a topbar + queue list; adding a second "panel" below the queue is the least-invasive fix and matches the editorial aesthetic. Add this block AFTER the `</main>` tag at line 36 (before the `<template>` blocks):
       ```html
       <section id="diagnostic" class="diagnostic" aria-label="Diagnostic">
         <h2 class="diag-heading">Diagnostic</h2>
         <dl class="diag-grid">
           <dt>Watch folder</dt>        <dd id="diag-watch-folder">—</dd>
           <dt>Folder exists</dt>       <dd id="diag-folder-exists">—</dd>
           <dt>Files in folder</dt>     <dd id="diag-file-count">—</dd>
           <dt>Last scan</dt>           <dd id="diag-last-scan">—</dd>
           <dt>Last heartbeat (server)</dt> <dd id="diag-last-heartbeat">—</dd>
           <dt>HMAC secret baked in</dt><dd id="diag-hmac">—</dd>
           <dt>Version</dt>             <dd id="diag-version">—</dd>
         </dl>
       </section>
       ```

    4. **Update `internal/admin/ui/main.css`** — append a `============== Diagnostic ==============` section (match existing aesthetic: oxblood accent, Fraunces heading, JetBrains Mono values, paper-tint card). Roughly:
       ```css
       .diagnostic {
         max-width: 740px;
         margin: 0 auto 80px;
         padding: 24px 32px;
         background-color: var(--paper-tint);
         border: 1px solid var(--rule);
         border-top: 4px solid var(--oxblood);
         border-radius: 2px;
         box-shadow: var(--shadow);
       }
       .diag-heading {
         font-family: "Fraunces", Georgia, serif;
         font-weight: 600; font-size: 20px;
         color: var(--oxblood);
         margin: 0 0 16px;
         letter-spacing: 0.02em;
         text-transform: uppercase;
       }
       .diag-grid { display: grid; grid-template-columns: 180px 1fr; gap: 8px 24px; margin: 0; }
       .diag-grid dt { color: var(--steel); font-size: 12px; text-transform: uppercase; letter-spacing: 0.06em; }
       .diag-grid dd { margin: 0; color: var(--ink); font-variant-numeric: tabular-nums; word-break: break-all; }
       .diag-grid dd.ok   { color: #2E6A3E; }
       .diag-grid dd.bad  { color: var(--oxblood); }
       ```
       No responsive changes needed — the existing `@media (max-width: 640px)` block already handles .queue padding; reuse the same pattern for .diagnostic if collapsing the 180px column: `.diag-grid { grid-template-columns: 1fr; }` inside that media query.

    5. **Update `internal/admin/ui/main.js`** — add a `fetchDiagnostic` poll loop. At module scope, after the existing `REFRESH_MS` / `HEARTBEAT_MS` constants (line 8–10), add:
       ```js
       const DIAG_MS = 5000;
       ```
       Define a new function alongside `fetchQueue` (~line 197):
       ```js
       async function fetchDiagnostic() {
         try {
           const resp = await fetch('/api/diagnostic', { cache: 'no-store' });
           if (!resp.ok) return;
           const d = await resp.json();
           setText('diag-watch-folder', d.watch_folder || '—');
           setStatus('diag-folder-exists', d.folder_exists,
             d.folder_exists ? '✓ exists' : ('✗ missing' + (d.folder_error ? ' — ' + d.folder_error : '')));
           setText('diag-file-count', d.folder_exists
             ? String(d.file_count) + ' file(s)'
             : (d.folder_error || '—'));
           setText('diag-last-scan', d.last_scan_at
             ? `${d.last_scan_at} — ${d.last_scan_processed} processed, ${d.last_scan_errors} errors`
             : (d.last_scan_note || 'no scans yet'));
           setText('diag-last-heartbeat', d.last_heartbeat_at || '—');
           setStatus('diag-hmac', d.hmac_secret_present,
             d.hmac_secret_present ? 'yes' : 'NO — dev build or GSD_HMAC_SECRET unset');
           setText('diag-version', d.app_version || 'dev');
         } catch (_) { /* silent — transient errors acceptable at 5s poll */ }
       }
       function setText(id, txt) { const el = document.getElementById(id); if (el) el.textContent = txt; }
       function setStatus(id, ok, txt) {
         const el = document.getElementById(id); if (!el) return;
         el.textContent = txt;
         el.classList.toggle('ok', !!ok);
         el.classList.toggle('bad', !ok);
       }
       ```
       Wire it up inside the existing `DOMContentLoaded` handler (~line 252), next to the `setInterval(fetchQueue, REFRESH_MS)` line:
       ```js
       fetchDiagnostic();
       setInterval(fetchDiagnostic, DIAG_MS);
       ```
       No other JS changes. **Do not** reset the heartbeat on diagnostic polls — the `/alive` heartbeat is separate and already handles browser-close detection.

    6. **Verify** locally: `go build ./... && go vet ./...`. Then run a smoke-test with a fake config:
       ```bash
       mkdir -p /tmp/es-admin-smoke
       printf "[watcher]\nwatch_folder = /tmp/es-admin-smoke-folder\nwebhook_url = https://support.jjagpal.me/earlscheibconcord\n" > /tmp/es-admin-smoke/config.ini
       mkdir -p /tmp/es-admin-smoke-folder && touch /tmp/es-admin-smoke-folder/test1.xml
       EARLSCHEIB_DATA_DIR=/tmp/es-admin-smoke ./dist/earlscheib --admin &
       ADMIN_PID=$!
       sleep 2
       # Grep stdout for "admin UI:" line to get the port, then:
       # curl -s http://127.0.0.1:<port>/api/diagnostic | jq .
       # Expected: folder_exists=true, file_count=1, hmac_secret_present depends on build
       kill $ADMIN_PID
       ```
       Must see all 7 fields populated and file_count=1.

    **DO NOT:**
    - Add a second embed.FS — the existing `go:embed ui` directive picks up new files under ui/ automatically.
    - Register `/api/diagnostic` outside `Run()` — the mux is per-request-lifetime, not global.
    - Expose the HMAC secret itself anywhere in the JSON — only the boolean `hmac_secret_present`. This is the entire point.
    - Poll faster than 5s — anything faster risks thrashing the server-side `/status` endpoint (shared with public browser probes).
  </action>
  <verify>
    <automated>cd /home/jjagpal/projects/earl-scheib-followup && go build ./... && go vet ./... && go test ./internal/admin/... -race -count=1</automated>
  </verify>
  <done>
    `go build` passes. Existing admin_test.go and ui_test.go still pass. Manual smoke test above shows the Diagnostic panel rendering under the queue with all 7 fields populated and live-updating every 5s (verifiable by touching a new .xml file in the watch folder — file_count bumps within 5s). HMAC field reads "NO — dev build..." on a `make build-windows` without GSD_HMAC_SECRET, and "yes" on one with it set. commands.json untouched.
  </done>
</task>

<task type="auto" tdd="false">
  <name>Task 3: Server-side /earlscheibconcord/diagnostic endpoint</name>
  <files>app.py</files>
  <action>
    1. **Add a new GET handler in `app.py` `do_GET` method.** Insert the new block immediately AFTER the `/earlscheibconcord/queue` handler (currently ending at line 1801) and BEFORE the `# Default: 404` block at line 1803. The new block must:

       - Match `path == "/earlscheibconcord/diagnostic"`.
       - Validate HMAC: `sig = self.headers.get("X-EMS-Signature", ""); if not _validate_hmac(b"", sig): self._send_json(401, {"error": "invalid signature"}); return` — byte-identical to the `/commands` and `/remote-config` handlers.
       - Compute `last_heartbeat`: read from the existing `LAST_HEARTBEAT` global (line 168) — `ts=LAST_HEARTBEAT["ts"]`, `host=LAST_HEARTBEAT["host"]`. Compute `seconds_ago = int(time.time()) - ts if ts else None`. Build the sub-object `{"ts": ts, "host": host, "seconds_ago": seconds_ago}`.
       - Compute `client_online`: `bool(ts and (int(time.time()) - ts) < 600)` — matches the existing `/status` "online" threshold at line 1753.
       - Read `commands_state`: load `commands.json` the same way the `/commands` handler does (lines 1707–1713), but **READ ONLY — DO NOT WRITE**. On FileNotFoundError or JSONDecodeError, fall back to `{}`. *(Constraint: commands.json is currently `{"upload_log": false}` and belongs to a separate live debug flow. Pure read is safe; any write is prohibited.)*
       - Build `recent_log_tail`: locate the most recent file in `received_logs/` by mtime (skip `latest.log` symlink to avoid double-counting). Read its last 20 lines. Use:
         ```python
         app_dir = os.path.dirname(os.path.abspath(__file__))
         logs_dir = os.path.join(app_dir, "received_logs")
         tail = ""
         try:
             entries = [
                 os.path.join(logs_dir, f) for f in os.listdir(logs_dir)
                 if f.endswith(".log") and f != "latest.log"
             ]
             if entries:
                 latest = max(entries, key=os.path.getmtime)
                 with open(latest, "r", encoding="utf-8", errors="replace") as fh:
                     lines = fh.readlines()
                 tail = "".join(lines[-20:])
         except OSError:
             tail = ""
         ```
       - Compute `received_logs_count`: `len([f for f in os.listdir(logs_dir) if f.endswith(".log") and f != "latest.log"])` — wrap in try/except OSError → 0.
       - Respond with `self._send_json(200, {...})`. **NO HTML wrapper.** JSON only.

    2. **Manual verification against the live server** (after deploying in Task 4 — this task just writes and unit-tests the handler locally):
       ```bash
       # Start app.py locally on port 8000 (or whatever the project uses):
       # python3 app.py &
       # SECRET=$(grep CCC_SECRET .env | cut -d= -f2)
       # SIG=$(printf '' | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $2}')
       # curl -s -H "X-EMS-Signature: $SIG" http://localhost:8000/earlscheibconcord/diagnostic | python3 -m json.tool
       # Expect: JSON with 5 keys — last_heartbeat, client_online, commands_state, recent_log_tail, received_logs_count
       # Then without signature: curl -s http://localhost:8000/earlscheibconcord/diagnostic → 401
       ```

    **DO NOT:**
    - Modify `commands.json` contents in any code path in this handler (read-only).
    - Modify `LAST_HEARTBEAT` — this handler is pure reader; the POST /heartbeat handler at line 1893 is the sole writer.
    - Dump the entire log file — `recent_log_tail` is exactly 20 lines, last-20 by line count (not byte count). Larger tails push diagnostic response size past the 1 MB proxy limit set in `handleQueue`.
    - Add HTML template rendering — the spec says JSON only, and the Go admin UI will consume this endpoint later if needed (Task 2 today hits `/status` directly; a future improvement could switch it to `/diagnostic` once live).
    - Leak the secret — the endpoint returns NO secret material. HMAC validation is the only use of the secret in this handler.
  </action>
  <verify>
    <automated>cd /home/jjagpal/projects/earl-scheib-followup && python3 -c "import ast, sys; ast.parse(open('app.py').read()); print('app.py parses OK')" && python3 -m py_compile app.py && grep -n '/earlscheibconcord/diagnostic' app.py | head -5</automated>
  </verify>
  <done>
    app.py parses and compiles cleanly. grep confirms the new path literal appears exactly once in do_GET. Manual curl test (above) returns 200 with all 5 fields when signature valid, 401 when absent, and received_logs_count matches `ls received_logs/*.log | grep -v latest.log | wc -l`. commands.json content unchanged (diff clean). No imports added — the handler uses only modules already imported at the top of app.py (os, json, time).
  </done>
</task>

<task type="checkpoint:human-verify" gate="blocking">
  <name>Task 4: Rebuild, repackage, deploy, verify, push</name>
  <what-built>
    After tasks 1–3 land, rebuild the Windows installer with the production HMAC secret baked in, redeploy the zip to the live support.jjagpal.me host, and verify byte-match. This is one atomic ops task — fully scripted below. Human verifies MD5 and the live Diagnostic panel after.
  </what-built>
  <how-to-verify>
    **Claude runs this sequence autonomously — human only verifies the final MD5 + UI at the end.**

    Step 1 — Source HMAC secret from .env (the trap from earlier today):
    ```bash
    cd /home/jjagpal/projects/earl-scheib-followup
    export GSD_HMAC_SECRET="$(grep '^CCC_SECRET=' .env | cut -d= -f2-)"
    # Sanity check — must be non-empty:
    [ -n "$GSD_HMAC_SECRET" ] || { echo "ABORT: GSD_HMAC_SECRET empty — check .env"; exit 1; }
    echo "HMAC secret length: ${#GSD_HMAC_SECRET} chars (non-empty)"
    ```
    ⚠️ **Trap recap:** `make build-windows` without `GSD_HMAC_SECRET` set silently falls back to the dev-default secret → every request the new binary makes to the live server fails HMAC with 401. This is what wasted an hour earlier today. ALWAYS export the var first, ALWAYS sanity-check its length is non-zero.

    Step 2 — Rebuild binary + installer:
    ```bash
    make clean
    make build-windows   # produces dist/earlscheib.exe with prod secret baked in
    # Copy to the path the .iss expects:
    cp dist/earlscheib.exe dist/earlscheib-artifact.exe
    make installer       # produces installer/Output/EarlScheibWatcher-Setup.exe (via docker)
    ```
    If `make installer` fails on "docker: command not found", the dev host is wrong — abort and flag.
    If `make installer` succeeds but `installer/Output/EarlScheibWatcher-Setup.exe` has mtime older than `dist/earlscheib.exe`, the docker container cached — rerun with `docker run --rm` (the Makefile already uses --rm, but verify).

    Step 3 — Copy to repo root + rezip:
    ```bash
    cp installer/Output/EarlScheibWatcher-Setup.exe EarlScheibWatcher-Setup.exe
    # Re-zip — the /download handler serves .zip preferentially (Chrome Safe Browsing flags .exe).
    # Remove any old zip first to avoid "add to existing" mode:
    rm -f EarlScheibWatcher-Setup.zip
    zip EarlScheibWatcher-Setup.zip EarlScheibWatcher-Setup.exe
    md5sum EarlScheibWatcher-Setup.exe EarlScheibWatcher-Setup.zip
    ```

    Step 4 — Commit all four changes as four atomic commits (Task 1, Task 2, Task 3, Task 4 binaries):
    ```bash
    git status  # verify only expected files modified
    # Commit 1 — scanner
    git add internal/scanner/scan.go internal/scanner/scanner_test.go cmd/earlscheib/main.go
    git commit -m "fix(scanner): log full path and OS error on folder-read failure; emit scan-start INFO line per cycle"
    # Commit 2 — admin UI
    git add internal/admin/diagnostic.go internal/admin/server.go internal/admin/ui/index.html internal/admin/ui/main.css internal/admin/ui/main.js
    git commit -m "feat(admin): add Diagnostic panel — watch folder health, heartbeat, HMAC-secret presence"
    # Commit 3 — server endpoint
    git add app.py
    git commit -m "feat(server): add HMAC-authed GET /diagnostic endpoint"
    # Commit 4 — release binaries
    git add EarlScheibWatcher-Setup.exe EarlScheibWatcher-Setup.zip
    git commit -m "release: rebuild installer with prod HMAC secret + Diagnostic improvements"
    ```

    Step 5 — Push and verify byte-match against live:
    ```bash
    git push origin master
    # Wait ~10s for the live server to pick up (systemd or similar reload pattern).
    # Compare local zip MD5 against the one served at the download URL:
    LOCAL_MD5=$(md5sum EarlScheibWatcher-Setup.zip | awk '{print $1}')
    LIVE_MD5=$(curl -sL https://support.jjagpal.me/earlscheibconcord/download -o /tmp/live.zip && md5sum /tmp/live.zip | awk '{print $1}')
    echo "local: $LOCAL_MD5"
    echo "live : $LIVE_MD5"
    [ "$LOCAL_MD5" = "$LIVE_MD5" ] || { echo "ABORT: MD5 MISMATCH — deploy did not propagate"; exit 1; }
    ```

    Step 6 — Confirm commands.json untouched (constraint):
    ```bash
    # commands.json is NOT in the repo (server-side only); verify via the endpoint:
    SECRET="$GSD_HMAC_SECRET"
    SIG=$(printf '' | openssl dgst -sha256 -hmac "$SECRET" | awk '{print $2}')
    curl -s -H "X-EMS-Signature: $SIG" https://support.jjagpal.me/earlscheibconcord/commands
    # Expected: {"upload_log": false}  — NOT {"upload_log": true}. If true, STOP and flag.
    ```

    Step 7 — Confirm new /diagnostic endpoint is live:
    ```bash
    curl -s -H "X-EMS-Signature: $SIG" https://support.jjagpal.me/earlscheibconcord/diagnostic | python3 -m json.tool
    # Expected: JSON with last_heartbeat / client_online / commands_state / recent_log_tail / received_logs_count
    ```

    **Human verification — the only manual step:**
    1. Walk through the `md5sum` output — LOCAL_MD5 must equal LIVE_MD5. Paste both hashes.
    2. Paste the /diagnostic curl output — confirm all 5 keys present.
    3. Confirm /commands output is still `{"upload_log": false}` exactly.
    4. (Optional, next live debug session) Have Marco re-download, reinstall, and open `earlscheib.exe --admin`. Scroll to the Diagnostic panel. Confirm "HMAC secret baked in" shows **yes**, watch folder path + file count match reality, and last heartbeat is within the last few minutes.

    Resume-signal: Type `approved` if MD5s match and all three URLs return expected bodies. Type any issue description otherwise.
  </how-to-verify>
  <resume-signal>approved</resume-signal>
</task>

</tasks>

<verification>
Full-phase checks after all four tasks:

1. **Regression net:** `go test ./... -race -count=1` passes (all existing tests + new scanner tests).
2. **Scanner behavior live:** `EARLSCHEIB_DATA_DIR=/tmp/es-verify ./dist/earlscheib --scan` emits one `scan start` INFO line AND, if watch_folder is absent, a `Cannot read watch folder path="..." err=...` WARN line.
3. **Admin UI live:** `./dist/earlscheib --admin` serves /api/diagnostic, JS polls it, panel renders under the queue. Smoke-tested by touching a file in watch_folder and watching file_count bump within 5s.
4. **Server endpoint live:** `curl -H "X-EMS-Signature: $SIG" https://support.jjagpal.me/earlscheibconcord/diagnostic` returns 200 + JSON with 5 keys. Without sig, 401. `commands.json` still `{"upload_log": false}`.
5. **Release integrity:** Local zip MD5 == remote zip MD5 (support.jjagpal.me serves what master last pushed).
6. **HMAC baked correctly:** A newly-installed binary's heartbeat POST reaches /heartbeat and updates `LAST_HEARTBEAT["ts"]` on the server — visible via the new /diagnostic `client_online=true` field within 5 minutes of running --scan on the target machine.

No new dependencies added to go.mod. No new Python imports in app.py. No changes to commands.json (constraint).
</verification>

<success_criteria>
- Four atomic commits on master with the subject lines listed in Task 4 Step 4.
- `EarlScheibWatcher-Setup.zip` at repo root whose MD5 matches the live download URL's zip.
- Live `/earlscheibconcord/diagnostic` endpoint returns the documented 5-key JSON when HMAC-authed.
- `earlscheib.exe --admin` shows a Diagnostic panel with all 7 fields populated and live-updating every 5s.
- `earlscheib.exe --scan` log (ems_watcher.log) contains `scan start watch_folder="..." webhook="..." version="..."` at INFO level on every invocation AND `Cannot read watch folder path="..." err=...` at WARN level when the folder is missing.
- `commands.json` on the server still reads `{"upload_log": false}` — untouched.
- No Python dependency added; no Go module added.
- Human verification (Task 4 checkpoint) explicitly approved.
</success_criteria>

<output>
After completion, create `.planning/quick/260421-shq-debuggability-improvements-scanner-admin/260421-shq-SUMMARY.md` summarizing:
- Scanner: exact log-line format shipped + test names in scanner_test.go
- Admin UI: JSON schema of /api/diagnostic response + list of new DOM IDs in index.html
- Server: HMAC validation choice (sign empty body, same as /commands) + log-tail selection rule (newest non-symlink *.log file)
- Release: the `GSD_HMAC_SECRET="$(grep ^CCC_SECRET= .env | cut -d= -f2-)"` incantation, logged as a reminder to the next release
- MD5 of the deployed EarlScheibWatcher-Setup.zip
- Explicit confirmation that commands.json was neither read-with-intent-to-write nor modified
</output>
