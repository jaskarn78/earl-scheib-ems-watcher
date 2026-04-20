# Architecture Research

**Domain:** Windows desktop tray app + scheduled background scanner (Go + WebView2)
**Researched:** 2026-04-20
**Confidence:** HIGH (core patterns), MEDIUM (NSIS installer specifics, remote config), LOW (crash telemetry integration detail)

---

## Standard Architecture

### System Overview

```
┌──────────────────────────────────────────────────────────────────────────────┐
│  Windows Session (Marco logged in)                                            │
│                                                                               │
│  ┌─────────────────────────────────────┐                                      │
│  │  TRAY PROCESS (startup app)         │                                      │
│  │  earls-watcher.exe --tray           │                                      │
│  │                                     │                                      │
│  │  ┌─────────────┐  ┌──────────────┐  │  ┌───────────────────────────────┐  │
│  │  │ Systray icon│  │ WebView2 win │  │  │  SCAN PROCESS (every 5 min)   │  │
│  │  │ green/yel/  │  │ wizard or    │  │  │  earls-watcher.exe --scan     │  │
│  │  │ red         │  │ status UI    │  │  │                               │  │
│  │  └─────────────┘  └──────────────┘  │  │  scan → settle → HMAC POST   │  │
│  │       │                  │          │  │  write DB → exit               │  │
│  │       │            Go Bind()        │  └──────────┬────────────────────┘  │
│  │       │            in-process RPC   │             │                        │
│  │       │                             │             │ Windows Scheduled Task  │
│  │       ▼                             │             │ runs every 5 min       │
│  │  Named mutex                        │             │ (SYSTEM user)          │
│  │  "EarlScheibWatcher"                │             │                        │
│  └──────────────────────┬──────────────┘             │                        │
│                          │                           │                        │
│  ┌───────────────────────▼───────────────────────────▼────────────────────┐  │
│  │  DATA DIR: C:\EarlScheibWatcher\                                        │  │
│  │  config.ini   ems_watcher.db (WAL)   ems_watcher.log                   │  │
│  │  first_run.flag  (deleted after wizard completes)                       │  │
│  └─────────────────────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────────────────┘
                                   │
                         HTTPS POST (webhook)
                                   │
                    ┌──────────────▼─────────────┐
                    │  Linux VM webhook server   │
                    │  https://support.jjagpal   │
                    │  .me/earlscheibconcord     │
                    │                            │
                    │  /heartbeat   /status      │
                    │  /earlscheibconcord (POST) │
                    │  /remote-config (GET)      │
                    └────────────────────────────┘
```

### Component Responsibilities

| Component | Responsibility | Implementation |
|-----------|----------------|----------------|
| `main.go` dispatch | Parse subcommand, route to subsystem | `os.Args[1]` switch; single binary |
| Tray manager | Named mutex, systray icon, color updates, menu | `fyne.io/systray` + Win32 via `golang.org/x/sys/windows` |
| WebView2 UI | Wizard and status window; bidirectional JS↔Go | `github.com/jchv/go-webview2`, `Bind()` + `Dispatch()` |
| Config store | Read/write `config.ini` in data dir; memory cache | Go standard `encoding/ini` or hand-rolled INI parser |
| Scanner | File walk, settle check, SHA256 dedup, HMAC POST | Port of `ems_watcher.py` logic, pure Go |
| SQLite layer | `processed_files` + `runs` tables; WAL mode | `modernc.org/sqlite` (pure Go, no CGo) |
| Heartbeat sender | Periodic POST to `/heartbeat` from tray process | goroutine in tray, 5-min ticker |
| Remote config poller | GET `/remote-config`, compare, patch config.ini | goroutine in tray, 15-min ticker |
| Crash telemetry | Defer/recover wrapper, POST structured JSON to `/telemetry` | thin wrapper around existing webhook base URL |
| Installer (NSIS) | Extract exe, create data dir, register Scheduled Task, Startup, first-run flag, uninstaller | NSIS script + `schtasks` call |

---

## Process Model Decision: Single Binary with Subcommands

**Recommendation: single binary, subcommand dispatch via `os.Args[1]`.**

Subcommands:
- `--tray` — foreground tray process (startup app, long-lived)
- `--scan` — oneshot file scan (run by Scheduled Task every 5 min)
- `--wizard` — alias for `--tray` on first-run; tray opens wizard immediately
- `--install` — register Scheduled Task + Startup shortcut (called by NSIS postinstall)
- `--uninstall` — remove Scheduled Task + Startup shortcut (called by NSIS uninstaller)
- `--test` — POST test BMS payload, exit with status code
- `--status` — print last-run summary to stdout, exit (CI/debugging only)

**Why single binary:**
- One file for NSIS to extract and sign; simpler update path
- No ambiguity about which binary the Scheduled Task points to
- Subcommand overhead is zero at runtime (parsed once, then the irrelevant code paths are never touched)
- Consistent with existing Python `ems_watcher.py` which already uses this pattern (`--install`, `--test`, `--status`, `--loop`)

**Why not separate binaries:**
- Two binaries doubles signing surface and installer complexity
- Updating one while the other is stale creates version skew bugs
- No performance benefit at this scale (two 10–15 MB binaries vs one 15 MB binary)

---

## IPC Between Tray and Scan Processes

**Recommendation: shared SQLite file with WAL mode as the primary IPC channel. No named pipe needed for normal operation.**

### Why SQLite-as-IPC is sufficient here

The two processes have an asymmetric relationship:
- **Scan process** is a writer: it inserts rows, then exits in under 30 seconds.
- **Tray process** is a reader: it queries the DB to update the status icon and UI.

SQLite WAL mode (already in the Python watcher) guarantees:
- Readers never block writers; writers never block readers.
- `PRAGMA busy_timeout = 30000` handles the rare case where two scan instances collide (Scheduled Task overlap).
- The tray process can query `runs` and `processed_files` any time without locking the scanner out.

### Race conditions to prevent

1. **Two scan processes overlapping**: Windows Scheduled Task can trigger a second instance before the first exits if the scan takes longer than 5 min (very unlikely given the 30s settle check + bounded retry). Mitigation: scan process acquires a named mutex `EarlScheibScan` on startup; if it cannot acquire (ERROR_ALREADY_EXISTS), it logs and exits immediately. This costs two Win32 calls and zero IPC infrastructure.

2. **Tray reads during scan write**: Handled by WAL mode + busy_timeout. No application-level locking needed.

3. **Tray writes config while scan reads config**: Both processes read config.ini at startup and do not hold it open. Atomic write pattern: scan process reads config once at launch and caches in memory; tray writes config atomically (write to `config.ini.tmp`, rename). File rename on Windows is not atomic but is fast enough; scan reads it at start of each 5-min window, not mid-run.

### Named pipe: when to add it

A named pipe (`\\.\pipe\EarlScheibWatcher`) is worth adding only if you later need push notification from scan to tray (e.g., "a file was just sent — update the icon immediately"). For v1, polling the DB every 60 seconds from the tray goroutine is sufficient and avoids the named pipe complexity. Sockets or named pipes can be layered on top later with `github.com/microsoft/go-winio`.

---

## Configuration State

### Location

All state lives at `C:\EarlScheibWatcher\` (created by installer, SYSTEM-writable, user-readable):

```
C:\EarlScheibWatcher\
  config.ini          <- tray writes (wizard completion), scan reads
  ems_watcher.db      <- scan writes, tray reads
  ems_watcher.log     <- both append (rotating, 2 MB × 5 backups)
  first_run.flag      <- created by installer, deleted by tray after wizard completes
```

### config.ini shape (from existing deployment)

```ini
[watcher]
watch_folder = C:\CCC\EMS_Export
webhook_url  = https://support.jjagpal.me/earlscheibconcord
secret_key   = <pre-baked at build time, not user-visible>
log_level    = INFO
```

**Secret key handling**: bake the HMAC secret into the binary at compile time via `-ldflags "-X main.secretKey=..."` in CI. The config.ini key field is left blank or omitted; the binary falls back to the compiled-in value. This prevents Marco from accidentally editing or exposing the secret.

### Who writes config.ini

- **Installer (NSIS)**: writes the initial file with `watch_folder` set to auto-detected CCC ONE path (or blank).
- **Wizard (tray process)**: overwrites `watch_folder` after Marco confirms it in step 1. Writes using rename-on-tmp pattern.
- **Remote config poller (tray process)**: patches `webhook_url` if the server sends an override. Never patches `secret_key` (compiled in).
- **Scan process**: reads only, never writes.

### Wizard handoff sequence

```
1. Installer writes first_run.flag to C:\EarlScheibWatcher\
2. Installer registers Startup shortcut: earls-watcher.exe --tray
3. Marco reboots (or installer launches tray immediately post-install)
4. Tray starts, checks for first_run.flag → opens wizard window (WebView2)
5. Wizard step 1: auto-scan common CCC ONE paths, show picker if not found
6. Wizard step 2: POST to /earlscheibconcord/status → show green checkmark
7. Wizard step 3: CCC ONE EMS Extract Preferences guidance + optional 2-min live watch
8. On wizard completion: write config.ini with confirmed watch_folder, delete first_run.flag
9. Tray transitions to idle state; icon turns green
```

---

## UI Process Model: WebView2

**Recommendation: in-process WebView2 with Go `Bind()` callbacks — no loopback HTTP server.**

### Architecture

```
Go main goroutine (UI thread)
  └── webview2.New() → creates WebView2 window
        ├── wv.SetHtml(embeddedHTML)   <- HTML/CSS/JS compiled into binary via go:embed
        ├── wv.Bind("getStatus", ...)  <- Go func exposed as JS function
        ├── wv.Bind("saveConfig", ...) <- wizard writes config.ini
        ├── wv.Bind("testConnection",..) <- fires POST to /status endpoint
        └── wv.Run()                   <- blocks; Windows message loop

Background goroutines (non-UI)
  └── wv.Dispatch(func() { wv.Eval("updateIcon('green')") })
      <- only safe way to call Eval() from non-UI goroutine
```

### Why in-process, not loopback HTTP

- No TCP port to conflict with other software or Windows firewall
- No HTTP server lifecycle to manage (no cleanup on crash)
- No CORS complications (the CORS bug between WebView2 and localhost is documented and annoying)
- `Bind()` + JSON is sufficient for the 4–5 RPC calls this UI needs
- `SetHtml()` with `go:embed` keeps assets in the binary; no extracted files on disk

### Threading rule (critical)

WebView2's `Run()` call blocks the goroutine it runs on and owns the Windows message loop. All UI mutations (`Eval`, `SetTitle`) must arrive on that thread via `Dispatch()`. Background goroutines that want to push a status update call `wv.Dispatch(func() { wv.Eval(...) })`.

### Embedded assets

Use `//go:embed ui/` to bundle all HTML/CSS/JS at compile time. WebView2 loads via `SetHtml()` for single-page content. For multi-page wizard flow, use JS routing within a single HTML document (hash-based routing, no navigation).

---

## Installer Responsibilities (NSIS)

**Recommended installer: NSIS (Nullsoft Scriptable Install System)**

NSIS produces a single `.exe` that contains the Go binary + NSIS script. It is well-understood, widely used for Windows apps of this size, and has a proven `schtasks` integration path (see go-ethereum's NSIS installer as a reference). WiX is an alternative but adds XML overhead and MSI ceremony unnecessary at this project scale.

### Install sequence

```
1. Extract earls-watcher.exe to C:\EarlScheibWatcher\earls-watcher.exe
2. Create data dir C:\EarlScheibWatcher\ (if not exists); set ACL: SYSTEM=Full, Users=Read
3. Auto-detect CCC ONE export path; write initial config.ini (watch_folder may be blank)
4. Write first_run.flag to C:\EarlScheibWatcher\first_run.flag
5. Register Scheduled Task (as SYSTEM): schtasks /Create /TN EarlScheibEMSWatcher
     /TR "C:\EarlScheibWatcher\earls-watcher.exe --scan"
     /SC MINUTE /MO 5 /RU SYSTEM /RL HIGHEST /F
6. Register Startup shortcut in HKCU\Software\Microsoft\Windows\CurrentVersion\Run:
     "EarlScheibWatcher" = "C:\EarlScheibWatcher\earls-watcher.exe --tray"
   (or drop .lnk into %APPDATA%\Microsoft\Windows\Start Menu\Programs\Startup\)
7. Launch tray immediately post-install: exec earls-watcher.exe --tray
8. Register uninstaller: write uninstall.exe, add Add/Remove Programs entry
```

### Uninstall sequence

```
1. Kill tray process (FindWindow or taskkill)
2. Delete Scheduled Task: schtasks /Delete /TN EarlScheibEMSWatcher /F
3. Remove Startup registry key or .lnk
4. Optionally preserve C:\EarlScheibWatcher\ems_watcher.db + logs (offer checkbox)
5. Remove earls-watcher.exe + uninstall.exe
6. Remove Add/Remove Programs entry
```

### Data dir permission decision

Run the Scheduled Task as SYSTEM (matches existing Python deployment). The data dir `C:\EarlScheibWatcher\` must be writable by SYSTEM. The tray process runs as the logged-in user (Marco) — it needs Read+Write to the same dir. Grant both during install via `icacls C:\EarlScheibWatcher /grant "SYSTEM:(OI)(CI)F" "Users:(OI)(CI)M"`. This avoids UAC prompts on every tray launch.

**Do not use `C:\Program Files\` for the data dir** — standard users cannot write there, and the Scheduled Task (SYSTEM) can but the tray (user context) cannot without UAC elevation. `C:\EarlScheibWatcher\` or `C:\ProgramData\EarlScheibWatcher\` are both valid; the existing deployment uses `C:\EarlScheibWatcher\` so keep it for continuity.

---

## First-Run Detection

**Recommendation: sentinel file `first_run.flag` in data dir.**

On tray startup:

```go
func isFirstRun(dataDir string) bool {
    _, err := os.Stat(filepath.Join(dataDir, "first_run.flag"))
    return err == nil  // file exists → first run
}
```

When wizard completes successfully:

```go
os.Remove(filepath.Join(dataDir, "first_run.flag"))
```

**Why a file, not a registry key:**
- Consistent with existing data-dir-as-source-of-truth pattern
- Deletable by the app without elevation
- Survives registry resets; visible in the data dir for debugging
- Uninstaller can clean it up trivially

**Edge case — wizard interrupted**: if Marco closes the wizard mid-flow, `first_run.flag` remains. Next tray launch re-opens the wizard. This is correct behavior.

---

## Single-Instance Guard (Tray Process)

```go
// On tray startup:
mu, err := windows.CreateMutex(nil, false, windows.StringToUTF16Ptr("EarlScheibWatcherTray"))
if err == nil && windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
    // Another tray instance is running — surface its window and exit
    PostMessage to the existing tray window, then os.Exit(0)
}
// mu handle held for lifetime of process; released on exit by OS
```

The scan process uses a separate, shorter-lived mutex `EarlScheibWatcherScan` acquired at start and released on exit, preventing overlapping scan runs.

---

## Crash Telemetry

**Recommendation: lightweight — POST structured JSON to a new `/telemetry` endpoint on the existing webhook server, using the same HMAC-SHA256 signing. No third-party SDK.**

### Why not Sentry

- Sentry requires an outbound connection to `sentry.io` (external dependency, potential firewall block in auto body shop)
- Introduces a privacy concern (stack traces containing file paths go to a third party)
- This is a single-shop deployment; a custom endpoint on the already-trusted server is sufficient

### Implementation

```go
func withTelemetry(name string, fn func() error) {
    defer func() {
        if r := recover(); r != nil {
            buf := make([]byte, 4096)
            n := runtime.Stack(buf, false)
            postTelemetry(TelemetryEvent{
                Component: name,
                Error:     fmt.Sprintf("%v", r),
                Stack:     string(buf[:n]),
                Version:   buildVersion,
                Host:      hostname(),
            })
            os.Exit(1)
        }
    }()
    if err := fn(); err != nil {
        postTelemetry(TelemetryEvent{Component: name, Error: err.Error()})
    }
}
```

`postTelemetry` POSTs JSON to `{webhookURL}/telemetry` with `X-EMS-Signature` header, non-blocking (fire-and-forget goroutine), 5s timeout. If the POST fails, write to log and continue.

The webhook server adds a `/telemetry` endpoint that logs the event and optionally emails the developer (single-line addition to `app.py`).

---

## Remote Config Override

**Recommendation: tray process polls `{webhookURL}/remote-config` every 15 minutes via GET with HMAC-authenticated request. Response is a JSON object.**

### Flow

```
Tray goroutine (ticker: 15 min)
  └── GET /remote-config
        Headers: X-EMS-Signature: HMAC(hostname + timestamp, secret)
        Response: {"webhook_url": "...", "log_level": "DEBUG"}  (or 204 No Content if no changes)
  └── If response has fields → patch config.ini (rename-on-tmp)
  └── Tray reloads its in-memory config cache
  └── Does NOT restart scan process (scan reads config fresh each run)
```

**Fields that can be overridden remotely:**
- `webhook_url` (critical — allows redirecting to new server without re-install)
- `log_level` (diagnostics)
- `scan_interval_min` (future: if moving off Scheduled Task)

**Fields that cannot be overridden remotely:**
- `secret_key` (compiled into binary; not in config.ini)
- `watch_folder` (Marco's local path; must be set by wizard)

**Failure modes:**
- Network unreachable → log at DEBUG, skip, retry in 15 min
- Non-2xx response → log at WARN, skip
- Malformed JSON → log at ERROR, discard response entirely (never partially apply)
- Remote config applied → log at INFO: "Remote config applied: webhook_url updated"

---

## Log and Data Retention

| File | Location | Rotation | Retention |
|------|----------|----------|-----------|
| `ems_watcher.log` | `C:\EarlScheibWatcher\` | `lumberjack` (2 MB × 5 backups = ~10 MB) | ~10 MB total; oldest rotated out |
| `ems_watcher.db` | `C:\EarlScheibWatcher\` | No automatic rotation | `processed_files` rows are permanent (dedup requires it); `runs` table pruned to last 500 rows on each scan run |
| `ems_watcher.db-wal` | Same dir | Managed by SQLite; checkpointed after each scan | Auto-cleaned by SQLite WAL checkpoint |
| `ems_watcher.db-shm` | Same dir | Managed by SQLite | Deleted when all connections close cleanly |

**Privacy**: log files contain file paths, HTTP status codes, hostname. They do not contain BMS XML content (file names only, not body). Telemetry payloads contain stack traces and filenames — document this in a brief privacy notice shown in wizard step 3.

---

## Recommended Project Structure

```
earls-watcher/
├── main.go                    # subcommand dispatch only
├── cmd/
│   ├── tray.go                # tray process entry: systray + heartbeat + remote config
│   ├── scan.go                # scan process entry: single scan run
│   ├── install.go             # Scheduled Task + Startup registration
│   ├── wizard.go              # first-run wizard state machine
│   └── status.go              # --status stdout reporter
├── internal/
│   ├── config/
│   │   ├── config.go          # load/save config.ini, atomic write
│   │   └── paths.go           # data dir paths, first_run.flag
│   ├── db/
│   │   ├── db.go              # open_db, init_db, WAL mode, busy_timeout
│   │   ├── processed.go       # is_already_processed, mark_processed
│   │   └── runs.go            # record_run, prune_runs
│   ├── scanner/
│   │   ├── scanner.go         # scan_and_send orchestration
│   │   ├── settle.go          # _wait_for_settle
│   │   ├── webhook.go         # send_to_webhook, HMAC signing, retry/backoff
│   │   └── heartbeat.go       # send_heartbeat
│   ├── telemetry/
│   │   └── telemetry.go       # withTelemetry wrapper, postTelemetry
│   ├── remoteconfig/
│   │   └── poller.go          # poll /remote-config, patch config.ini
│   └── ui/
│       ├── ui.go              # WebView2 window lifecycle, Bind() registrations
│       ├── tray.go            # systray icon, color updates, menu items
│       └── embed.go           # //go:embed ui/dist
├── ui/
│   ├── index.html             # single-page app (wizard + status views)
│   ├── style.css
│   └── app.js
├── installer/
│   └── earls-watcher.nsi      # NSIS installer script
├── build/
│   └── Makefile               # cross-compile targets, sign, produce installer
└── secrets/
    └── (gitignored) secret.env  # HMAC secret for -ldflags injection in CI
```

---

## Data Flow

### Scan cycle (every 5 min, via Scheduled Task)

```
Windows Task Scheduler
  └── exec earls-watcher.exe --scan
        ├── acquire mutex EarlScheibWatcherScan (exit if already held)
        ├── load config.ini
        ├── open ems_watcher.db (WAL, busy_timeout=30s)
        ├── init schema if needed
        ├── send heartbeat → POST /heartbeat (non-blocking)
        ├── list .xml/.ems files in watch_folder
        ├── for each file:
        │     ├── query processed_files (filepath, mtime) → skip if found
        │     ├── wait for settle (4 samples × 2s)
        │     ├── re-query dedupe
        │     ├── read file bytes
        │     ├── SHA256 hash
        │     ├── POST to webhook (HMAC header, 3 attempts × backoff)
        │     └── on 2xx: INSERT processed_files row
        ├── INSERT runs row (processed, errors, note)
        ├── prune runs table to last 500 rows
        └── close DB, release mutex, exit 0
```

### Tray status update cycle (every 60 s)

```
Tray goroutine (ticker: 60s)
  └── query ems_watcher.db:
        SELECT run_at, processed, errors FROM runs ORDER BY rowid DESC LIMIT 1
  └── determine icon color:
        green  = last run < 10 min ago AND errors == 0
        yellow = last run < 10 min ago AND errors > 0 (OR no run yet)
        red    = last run >= 10 min ago (scan may be stuck)
  └── wv.Dispatch(func() { wv.Eval("updateIcon('" + color + "')") })
  └── systray.SetIcon(iconBytes[color])
```

### Wizard → config handoff

```
Marco in wizard UI (WebView2)
  └── JS: window.saveConfig({watch_folder: "C:\\CCC\\EMS_Export"})
        └── Go Bind handler: saveConfig
              ├── validate path exists
              ├── write config.ini (atomic rename)
              ├── delete first_run.flag
              └── return {ok: true}
        └── JS: navigate to step 2
  └── JS: window.testConnection()
        └── Go Bind handler: testConnection
              ├── GET /earlscheibconcord/status
              └── return {ok: true, latency_ms: 42}
        └── JS: show green checkmark or error
```

### Remote config override flow

```
Tray goroutine (ticker: 15 min)
  └── GET {webhookURL}/remote-config
        Headers: X-EMS-Machine: hostname, X-EMS-Signature: HMAC(hostname+ts, secret)
  └── 204 No Content → no-op
  └── 200 OK + JSON → validate fields
        ├── patch config.ini (atomic rename)
        └── reload in-memory config cache in tray process
            (scan process will pick up on next Task Scheduler invocation)
```

---

## Suggested Build Order (with dependency hints)

**Phase 1 — Core scanner (no UI)**
- `internal/config`, `internal/db`, `internal/scanner` packages
- `cmd/scan.go` entry point
- Unit tests against real SQLite (use temp dir)
- Cross-compile target: `GOOS=windows GOARCH=amd64`
- Validate: run in Windows VM, point at real CCC ONE path
- Dependency: nothing external; this is the critical path item

**Phase 2 — Tray shell (no wizard)**
- `internal/ui/tray.go` (systray, icon states, menu)
- Tray status polling goroutine (read `runs` table, update icon)
- Single-instance mutex guard
- `cmd/tray.go` entry point
- Dependency on Phase 1: needs `internal/db` for status polling

**Phase 3 — WebView2 UI (wizard + status window)**
- `ui/index.html` + JS routing
- `internal/ui/ui.go` WebView2 window + Bind registrations
- Wizard state machine (`cmd/wizard.go`)
- `first_run.flag` detection and wizard auto-open
- Dependency on Phase 1+2: needs config, DB schema, tray shell

**Phase 4 — Installer**
- NSIS script: extract, data dir, Scheduled Task, Startup, first-run flag
- `cmd/install.go` / `--install` subcommand
- `cmd/uninstall.go` / `--uninstall` subcommand
- Build pipeline: cross-compile → NSIS → sign (makensis on CI)
- Dependency on Phase 1+2+3: must package the complete binary

**Phase 5 — Telemetry + remote config**
- `internal/telemetry` package
- `internal/remoteconfig` poller
- `/telemetry` and `/remote-config` endpoints on `app.py` (server-side, minimal)
- Dependency on Phase 1: needs webhook URL from config; can be added to scan + tray independently

---

## Architecture Anti-Patterns

### Anti-Pattern 1: Windows Service instead of Scheduled Task

**What people do:** Register a Windows Service for background scanning because it "feels more professional."
**Why it's wrong:** Services require admin install/start/stop, interact poorly with user sessions, are killed differently by Windows Update cycles, and are harder to debug. The existing Python watcher runs via Scheduled Task and it works.
**Do this instead:** Keep Scheduled Task. The `--scan` process exits in <30s; there's no reason to keep it alive as a service.

### Anti-Pattern 2: Loopback HTTP server for WebView2 IPC

**What people do:** Spin up `net/http` on `localhost:PORT` and navigate WebView2 to it for all UI data.
**Why it's wrong:** Port conflicts, firewall dialogs, CORS issues specific to WebView2 localhost (documented bug), and the overhead of HTTP for a single-user local UI.
**Do this instead:** Use `Bind()` for Go→JS RPC and `Eval()` / `Dispatch()` for JS push updates. All in-process. Reserve loopback HTTP only if you need to serve large binary assets (unlikely here).

### Anti-Pattern 3: Config written by the scan process

**What people do:** Let the scan process update config (e.g., record last successful run settings).
**Why it's wrong:** SYSTEM-user writes to config.ini can create a file owned by SYSTEM, making it unwritable by the tray process (user context). Config state and run state are different concerns.
**Do this instead:** Scan process writes only to `ems_watcher.db`. Config is owned by the tray process. Status is read from the DB.

### Anti-Pattern 4: Storing the HMAC secret in config.ini

**What people do:** Put `secret_key` in config.ini so it's "configurable."
**Why it's wrong:** Marco can see it, edit it, break authentication, or accidentally share it in a screenshot.
**Do this instead:** Bake it into the binary via `-ldflags "-X main.secretKey=..."` at CI build time. The config.ini field is absent or blank; the binary falls back to the compiled-in value. Remote config override uses the same compiled secret for auth.

### Anti-Pattern 5: Mutating global state from background goroutines without Dispatch()

**What people do:** Call `wv.Eval()` or `wv.SetTitle()` from a non-UI goroutine directly.
**Why it's wrong:** WebView2 operations must run on the thread that owns the message loop. Direct calls from other goroutines cause crashes or silent no-ops.
**Do this instead:** Always use `wv.Dispatch(func() { wv.Eval(...) })` from any goroutine other than the main UI goroutine.

---

## Integration Points

### External Services

| Service | Integration Pattern | Notes |
|---------|---------------------|-------|
| Webhook server (`/earlscheibconcord`) | HTTPS POST, HMAC-SHA256, raw XML body | Existing; no changes needed for scan |
| Heartbeat (`/heartbeat`) | HTTPS POST, XML payload, HMAC header | Existing endpoint; called from scan process |
| Status check (`/status`) | HTTPS GET (or POST), called from wizard | Used by wizard step 2 to verify connectivity |
| Remote config (`/remote-config`) | HTTPS GET, HMAC auth, JSON response | New endpoint on server; minimal (add to app.py) |
| Crash telemetry (`/telemetry`) | HTTPS POST, JSON, HMAC auth | New endpoint on server; log + email alert |

### Internal Boundaries

| Boundary | Communication | Notes |
|----------|---------------|-------|
| Tray ↔ Scan | SQLite DB (WAL mode); no direct IPC | Tray reads `runs`; scan writes `runs` + `processed_files` |
| WebView2 JS ↔ Go | `Bind()` (JS→Go) + `Dispatch(Eval())` (Go→JS) | In-process; JSON serialization |
| Config ↔ All components | File read at startup; atomic write on change | Scan reads once per invocation; tray re-reads after wizard or remote-config patch |
| NSIS ↔ Binary | `earls-watcher.exe --install` / `--uninstall` subcommands | Installer calls binary for Task registration to leverage Go Win32 bindings |

---

## Sources

- go-webview2 API docs: https://pkg.go.dev/github.com/jchv/go-webview2 (HIGH confidence)
- SQLite WAL mode: https://www.sqlite.org/wal.html (HIGH confidence)
- SQLite busy_timeout SQLITE_BUSY analysis: https://berthub.eu/articles/posts/a-brief-post-on-sqlite3-database-locked-despite-timeout/ (MEDIUM confidence)
- Win32 CreateMutex single-instance pattern: https://learn.microsoft.com/en-us/windows/win32/api/synchapi/nf-synchapi-createmutexa (HIGH confidence)
- go-winio named pipes: https://github.com/microsoft/go-winio (HIGH confidence)
- fyne.io/systray: https://pkg.go.dev/fyne.io/systray (HIGH confidence)
- NSIS for Go binaries (go-ethereum reference): https://github.com/ethereum/go-ethereum/blob/master/build/nsis.install.nsh (MEDIUM confidence)
- ProgramData vs C:\\ data dir for SYSTEM + user access: https://medium.com/@boutnaru/windows-programdata-directory-b2aaa9c71a38 (MEDIUM confidence)
- WebView2 CORS/localhost issues: https://github.com/MicrosoftEdge/WebView2Feedback/issues/4709 (MEDIUM confidence)
- golang-ipc (named pipe IPC, for future extension): https://github.com/james-barrow/golang-ipc (MEDIUM confidence)
- Existing watcher logic reference: `claude-code-project/ems_watcher.py` (HIGH confidence — already in production)

---

*Architecture research for: Earl Scheib EMS Watcher Windows client (Go + WebView2)*
*Researched: 2026-04-20*
