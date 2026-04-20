# Pitfalls Research

**Domain:** Go-based Windows tray + file-watcher app with WebView2 UI and single-file installer, deployed to non-technical end users
**Researched:** 2026-04-20
**Confidence:** HIGH (most items corroborated by official documentation, GitHub issues, and multiple independent sources)

---

## Critical Pitfalls

### Pitfall 1: Antivirus / SmartScreen False Positives on Unsigned Go Binaries

**What goes wrong:**
Windows Defender's ML model flags Go binaries as Trojan or suspicious because Go's runtime produces PE structure patterns that overlap with known malware families. Microsoft SmartScreen additionally shows a "Windows protected your PC" modal because an unsigned or low-reputation binary has never been downloaded before. Marco cannot run the installer. This is the most common "first-hour" failure for Go desktop apps distributed outside the Microsoft Store.

**Why it happens:**
Go binaries are statically linked and include goroutine scheduler code that some heuristic models misidentify as packer/dropper behavior. SmartScreen separately tracks download reputation per certificate hash — a freshly signed or unsigned binary has zero reputation and triggers the block dialog. Wails apps and plain Go binaries both hit this; there are active GitHub issues on both `microsoft/go` and `wailsapp/wails` repositories documenting repeated Defender false-positive cycles.

**How to avoid:**
1. Sign the installer and the embedded executable with an OV code-signing certificate (DigiCert, Sectigo). HSM-stored key is mandatory post-June 2023 per CA/B Forum — plan for a USB hardware token or cloud HSM workflow in CI.
2. After signing, submit the binary to Microsoft Security Intelligence ("Submit a file") before release, and submit false-positive reports to VirusTotal to propagate to downstream AV vendors.
3. Use `goversioninfo` to embed a valid VERSIONINFO resource — binaries without version metadata are rated higher risk by heuristics.
4. On the installer landing page, pre-warn Marco with a screenshot of the SmartScreen dialog and "click More info → Run anyway" instructions.
5. Note: as of 2024 EV certificates no longer bypass SmartScreen automatically — EV and OV now go through the same reputation-building process. Do not pay the EV premium ($400+/yr) expecting instant bypass.

**Warning signs:**
- Manual test on a fresh Windows 10/11 VM blocks the installer before first run.
- VirusTotal scan of the build artifact shows any AV hits.
- Marco calls or messages saying "Windows says the file is dangerous."

**Phase to address:**
Phase covering Installer + Distribution. Build CI pipeline with signing before any external user testing. Budget at minimum 2 weeks for cert procurement given validation timelines.

---

### Pitfall 2: WebView2 Runtime Absence on Older Windows 10 / Offline Systems

**What goes wrong:**
The tray app loads, attempts to open the wizard or status window via WebView2, and crashes silently or shows a blank window. Marco sees nothing and cannot complete setup. This is especially likely on Windows 10 builds before 21H1 on machines that have never had Edge updated, or on machines behind an aggressive firewall that blocked the Windows Update that pre-installed the Evergreen Runtime.

**Why it happens:**
Microsoft mass-deployed the Evergreen WebView2 Runtime to Windows 10 devices, but this happened via Windows Update — an older machine that was offline for months, or a corporate image that locked out Windows Update, may not have it. The Evergreen Bootstrapper that ships with many apps requires internet access to download at install time, which fails in offline shops.

**How to avoid:**
1. Bundle the Evergreen Standalone Installer (the ~120 MB offline full installer) inside the app installer and run it silently during setup if WebView2 is not detected. This adds size but eliminates the runtime gap.
2. Alternatively, use the Fixed Version Runtime: embed a specific WebView2 version directory alongside the binary. Tradeoff: no automatic security updates, ~80 MB added to installer, you own the patching burden.
3. Recommended for this deployment: use the standalone Evergreen installer bundled in the .exe (silent install at first run). Show progress in the installer wizard so Marco does not think it hung.
4. In the Go app startup code, call the WebView2 availability check API before creating a webview window. If WebView2 is absent, show a message box using the Win32 MessageBox API (no WebView2 needed) explaining what to do, rather than crashing.

**Warning signs:**
- Testing on a freshly installed Windows 10 21H1 VM without internet shows blank window.
- Error code `HRESULT 0x80070002` or similar in logs on webview creation.
- App log shows "Failed to create WebView2 environment" at startup.

**Phase to address:**
Phase 1 / Scaffold. Decide on Evergreen vs Fixed before writing any webview code — it affects how the installer and startup logic are structured. Test on a clean Windows 10 1809 VM before any beta testing with Marco.

---

### Pitfall 3: Scheduled Task Running as SYSTEM Cannot See Mapped Drive Letters

**What goes wrong:**
The watcher Scheduled Task runs every 5 minutes as SYSTEM. The CCC ONE export folder is on a network share that Marco (or the shop IT person) mapped as `Z:\CCC_Export`. SYSTEM cannot see `Z:` because drive letter mappings are per-logon-session DosDevice symbolic links — they exist only in Marco's user session token, not in the SYSTEM session. The watcher logs "Watch folder does not exist" every cycle, files are never processed, and no texts go out. This is silent — the task reports "success" because the watcher script exits cleanly.

**Why it happens:**
Windows stores mapped drive letters as `\Sessions\<N>\DosDevices\Z:` objects scoped to the user's logon session. SYSTEM runs in session 0, which has no user-mapped drives. Even `EnableLinkedConnections` (a registry workaround) only applies to the elevated vs. non-elevated split within a single user session — it does not bridge to SYSTEM.

**How to avoid:**
1. Do not allow Marco to configure a drive-letter path if the watch task will run as SYSTEM. During the wizard folder picker, detect if the selected path is a mapped network drive letter and proactively warn: "This folder is on a network drive. Please use the full network path (\\\\server\\share\\...) instead."
2. Alternatively, run the Scheduled Task as Marco's user account (with the "Run whether user is logged on or not" flag and credentials stored by the task). This means the task runs in Marco's session context and can see mapped drives — but it requires storing Marco's Windows password in the task, which is fragile (password changes break the task silently).
3. Recommended: store a UNC path (`\\server\share\CCC_Export`) in config, not a drive letter. Expose this as the canonical form in the wizard. The Python watcher already handles `OSError` on network folder access — preserve this in the Go port.
4. On first run, verify that the configured path is reachable from a non-elevated context (simulate what SYSTEM sees by trying a `net use` check or by documenting that UNC paths must be used).

**Warning signs:**
- Marco's CCC ONE export folder is under a drive letter rather than a UNC path.
- Watcher log contains "Watch folder does not exist" on every cycle despite files being present when Marco opens Explorer.
- No files processed for days; tray icon shows yellow/stale.

**Phase to address:**
Phase 2 / Wizard. Folder path validation and UNC enforcement must be built into the wizard step, not added later. The "Run Now" button in the status window should also test folder reachability and report the result explicitly.

---

### Pitfall 4: File-Settle Race Condition — Reading a File CCC ONE Is Still Writing

**What goes wrong:**
CCC ONE writes a BMS XML file to the export folder. The watcher fires 5 minutes later, sees the file, reads it immediately, and POSTs a truncated or malformed XML document to the webhook. The server rejects it or, worse, accepts a partial payload that schedules a garbled follow-up text to a customer.

**Why it happens:**
CCC ONE writes export files non-atomically — it opens the file, writes progressively, and closes it. On a fast local disk the write is near-instant, but on a network share or under heavy load the write window can be several seconds. The watcher task interval is 5 minutes, but nothing guarantees the file is complete when the task fires.

**How to avoid:**
The existing Python watcher already implements a correct settle check: poll `mtime` and `size` at 2-second intervals for 4 consecutive samples, require 2 stable consecutive readings before accepting the file. Port this logic exactly to Go — do not simplify it. Key detail: the dedup key is `(filepath, mtime)` and must be re-checked after the settle loop because `mtime` may have changed during settling.

Additionally: before reading bytes, attempt to open the file with `os.Open` (shared read). If CCC ONE holds an exclusive write lock, `Open` will fail with a lock error — treat this as "not settled" and skip until next cycle.

**Warning signs:**
- Server receives XML parsing errors for payloads from this client.
- Log shows `settled=False` for a file that keeps changing across cycles (indicates CCC ONE is an unusually slow writer on this hardware).
- Customer receives a text with a garbled name or missing vehicle info.

**Phase to address:**
Phase 3 / Core Watcher Logic. This is the most business-critical correctness issue. Port the exact Python settle logic first, before any other feature work, and add a unit test that mocks a file that changes mid-settle.

---

### Pitfall 5: SQLite Locking When Tray and Scan Processes Both Open the DB

**What goes wrong:**
The tray app (always-on) reads the SQLite DB to display status (last run, files sent today). Simultaneously, the Scheduled Task fires and the watcher process opens the same DB to write processed file records. One process gets `SQLITE_BUSY` or `database is locked`, the operation fails, and either the tray shows stale data or the watcher fails to record a successful send (causing a re-send on the next cycle).

**Why it happens:**
SQLite WAL mode allows concurrent readers and one writer, but on Windows, WAL's shared-memory file (`.db-shm`) and WAL log (`.db-wal`) must both be accessible. Windows sometimes holds file locks beyond `close()` calls (a known Windows-specific SQLite WAL bug documented in multiple bun/sqlite3 GitHub issues). Two processes opening the same WAL-mode DB is supported but requires both to use generous busy timeouts and exponential backoff retry logic.

**How to avoid:**
1. Use WAL mode (already set in the Python watcher — preserve in Go port).
2. Set `PRAGMA busy_timeout = 30000` (30 seconds) on every connection. The Python watcher already does this.
3. Implement retry with backoff on any write operation that returns `SQLITE_BUSY` — the Python watcher has a `_db_retry` function with 5 attempts and exponential backoff. Port this exactly.
4. Design the tray app to treat DB reads as best-effort: if a read fails due to lock contention, show the last cached value rather than showing an error. Never let a DB read failure crash or freeze the tray UI.
5. Keep the tray's DB connection open only for the duration of each read, then close it. Do not hold a persistent open connection in the tray process — this dramatically reduces lock contention windows.

**Warning signs:**
- Log shows `database is locked` entries, especially during task execution windows.
- The tray status window shows stale counts that don't match the log.
- A file gets re-sent to the webhook (server deduplication should catch it, but it indicates the mark-processed write failed).

**Phase to address:**
Phase 3 / Core Watcher Logic, concurrent with Phase 4 / Tray App. Must be addressed at schema design time, not retrofitted.

---

### Pitfall 6: HMAC Signing Gotchas — Raw Body vs. Re-encoded, Clock Skew, Secret Rotation

**What goes wrong:**
A subtly wrong HMAC implementation causes every request to fail auth verification on the server. The most common mistake: reading the XML file as bytes, then re-encoding or reformatting it before signing (e.g., parsing then marshaling), so the signed bytes differ from the sent bytes. A second failure mode: the existing Python server verifies `hmac.new(secret.encode(), raw_body, sha256).hexdigest()` but the Go port accidentally encodes the secret differently (e.g., hex-decodes a hex string instead of using the raw string), producing a different digest. Third: no replay protection, so a captured request can be replayed indefinitely.

**How to avoid:**
1. Sign the exact bytes that will be sent in the HTTP body — never parse-then-marshal. Read the file as `[]byte`, compute HMAC over those bytes, then POST those same bytes. This matches the Python watcher exactly.
2. The secret key in the existing system is used as a raw UTF-8 string: `secret_key.encode("utf-8")` in Python. In Go: `[]byte(secretKey)`. Do not hex-decode it.
3. Add a timestamp to signed data for replay attack resistance: include an `X-EMS-Timestamp` header with Unix epoch seconds, sign `raw_body + "|" + timestamp_string`, and have the server reject requests older than 5 minutes with clock skew tolerance of 60 seconds. This requires a server-side change documented before client work begins.
4. For secret rotation: support a grace period where the server accepts signatures from both old and new secrets simultaneously. The remote config override feature in the PRD enables key rotation without reinstalling.
5. Use `hmac.Equal()` (Go: `hmac.Equal(computed, received)`) for constant-time comparison to prevent timing attacks. Never use `==`.

**Warning signs:**
- All POST requests return 401 or 403 after porting to Go.
- Heartbeat succeeds but file POSTs fail (the heartbeat uses a different body construction path).
- HMAC validation works in unit tests but fails against the live server (usually means secret encoding mismatch).

**Phase to address:**
Phase 3 / Core Watcher Logic. Write a cross-language HMAC test: generate a known payload in Go, compute the HMAC, verify it using the Python `hmac.new` logic to confirm byte-level equivalence before touching the server.

---

### Pitfall 7: Installer UAC, Per-User vs. Per-Machine, and Config Survival on Upgrade

**What goes wrong:**
Three distinct failure modes:
(a) Per-machine install (writes to `C:\Program Files`) requires UAC elevation. Marco clicks "Yes" for admin prompt, but the UAC dialog is confusing to a non-technical user and sometimes gets dismissed, stalling the install.
(b) Upgrade re-runs the installer, which overwrites `config.ini` at `C:\EarlScheibWatcher\` with defaults, deleting Marco's configured folder path. He must re-run the wizard.
(c) Uninstaller fails to remove the Scheduled Task (documented Microsoft/PowerToys bug pattern), leaving an orphan task that runs a binary that no longer exists, producing "task failed" events.

**How to avoid:**
(a) Use per-machine install to `C:\EarlScheibWatcher\` (matching the existing deployment). Require elevation upfront, at installer launch, not mid-install. Use Inno Setup's `[Setup] PrivilegesRequired=admin` to elevate immediately. Show a clear friendly message before the UAC prompt: "This installer needs permission to set up the watcher." The UAC prompt itself cannot be customized, but framing it helps.
(b) Separate program files from user data. Installer writes the executable to `C:\EarlScheibWatcher\` but must NOT overwrite `config.ini` if it already exists. Use Inno Setup's `onlyifdoesntexist` flag on the config file. On upgrade, skip config file installation entirely and only replace the binary.
(c) The uninstaller must explicitly call `schtasks /Delete /TN EarlScheibEMSWatcher /F` as an admin process before removing program files. Add this as an Inno Setup `[UninstallRun]` step and verify it succeeds (check exit code). Test uninstall → reinstall cycle end-to-end.

**Warning signs:**
- User reports needing to re-configure the folder path after every update.
- Task Scheduler shows the task entry pointing at a non-existent binary after uninstall.
- Marco sees "Windows protected your PC" during upgrade (means the updated binary has a different file hash with no existing reputation).

**Phase to address:**
Phase 5 / Installer. Treat upgrade and uninstall paths as first-class test scenarios, not afterthoughts.

---

### Pitfall 8: Tray Icon Not Appearing — Windows 11 Notification Area Overflow

**What goes wrong:**
Marco installs the app, it starts, but he cannot find the tray icon. It is hidden in the notification area overflow (the "^" chevron). He thinks the app did not install correctly and calls for support. On Windows 11 23H2+, Microsoft partially removed the "always show all icons" Control Panel toggle that was the historical fix for this.

**Why it happens:**
Windows auto-hides newly registered tray icons into the overflow area on first appearance. The user must manually drag the icon to the visible area. The "Show all icons in notification area" setting that previously forced all icons visible was deprecated in Windows 11. After a reboot or Explorer restart, icons can re-hide themselves.

**How to avoid:**
1. In the onboarding wizard, add a step after "everything is working" that shows a screenshot of the Windows 11 taskbar overflow area and instructs Marco explicitly: "You may need to drag the green icon to your taskbar."
2. The tray tooltip should be distinctive ("Earl Scheib Watcher — Active") so it is identifiable when Marco finds it in the overflow.
3. The installer can add a Start Menu shortcut or desktop shortcut that opens the status window directly (via `earls-watcher --show-status`), providing an alternative access path even when the tray icon is buried.
4. Do not attempt to manipulate the registry to force icon visibility — this is fragile across Windows versions and flagged by antivirus.

**Warning signs:**
- Marco's first support message: "I don't see anything after install."
- Testing on Windows 11 24H2 VM shows icon not immediately visible in taskbar.

**Phase to address:**
Phase 4 / Tray App. Write the wizard onboarding step specifically about the overflow area before beta testing.

---

### Pitfall 9: Single-Instance Enforcement — Second Launch Must Show Status Window, Not Silently Exit

**What goes wrong:**
Marco double-clicks the tray app shortcut (or it launches twice at startup). Without single-instance enforcement, two tray processes run simultaneously, creating duplicate tray icons, DB lock conflicts, and confused state. Alternatively, with naive single-instance code that just exits on detecting a second instance, Marco clicks the icon in Start and nothing happens — he thinks the app is broken.

**How to avoid:**
1. On startup, attempt to create a named Windows mutex (`Global\EarlScheibWatcher_SingleInstance`). If the mutex already exists, another instance is running.
2. On detecting a second instance: instead of silent exit, send a message to the existing instance to bring its status window to the foreground. The standard pattern is a named Win32 window message or a local TCP loopback socket for IPC. Wails has a documented single-instance-lock guide for its Go webview apps — review it, but the underlying Win32 mutex + window-find approach works for any Go Windows app.
3. After signaling the existing instance, exit immediately (no duplicate tray icon).

**Warning signs:**
- Task Manager shows two `earls-watcher.exe` processes.
- Two tray icons appear simultaneously.
- Marco clicks the Start shortcut and nothing visually happens (silent exit path).

**Phase to address:**
Phase 4 / Tray App. Mutex and IPC must be implemented before any public testing.

---

### Pitfall 10: Startup Registration — Choosing the Wrong Mechanism for the Tray App

**What goes wrong:**
The tray app (always-on, user-facing) is registered for startup via a Scheduled Task with trigger "At logon." This works but shows the app in Task Manager's Startup tab as a scheduled task entry, which is confusing and not disableable by Marco via the normal startup management UI. Worse, if registered as a SYSTEM-level task at logon, the tray cannot interact with the desktop (session 0 isolation on Windows Vista+).

**Why it happens:**
The existing watcher uses Scheduled Tasks for the 5-minute scan (correct). Developers copy this pattern to also register the tray via a Scheduled Task, not realizing that interactive/user-session apps (tray icons, GUI windows) cannot run from SYSTEM-level Scheduled Tasks.

**How to avoid:**
Use separate mechanisms for the two components:
- **5-minute watcher scan:** Scheduled Task running as `SYSTEM` (or the logged-on user if network drives are required), trigger Every 5 minutes. Correct.
- **Tray app at startup:** Registry Run key `HKCU\Software\Microsoft\Windows\CurrentVersion\Run` (per-user, no admin required, shows correctly in Task Manager > Startup tab, user can disable it there). This is the standard mechanism for interactive tray apps.

Do not use the Startup folder — it is less controllable and less visible. Do not use a Logon Scheduled Task for the tray — session 0 isolation will prevent the window from rendering.

**Warning signs:**
- Tray app doesn't appear at login despite being "registered."
- Task Scheduler shows "last run result: 0xC000013A" (process terminated) for the tray task.
- Marco disables it in Task Manager > Startup and the tray still launches (means it is registered twice via different mechanisms).

**Phase to address:**
Phase 5 / Installer. Decide on the dual-mechanism (Run key for tray, Scheduled Task for watcher) at installer design time and document it explicitly.

---

### Pitfall 11: Config File Editable by Marco Could Break Auth or Point at Wrong URL

**What goes wrong:**
Marco (or a curious IT person) opens `C:\EarlScheibWatcher\config.ini`, sees `webhook_url` or a secret key field, and changes it — either accidentally pointing at the wrong server or corrupting the HMAC secret. The watcher runs silently, sends to nowhere, and no texts go out. This is invisible until a customer complains days later.

**Why it happens:**
The current Python watcher stores `secret_key` in `config.ini` alongside user-editable settings. This was acceptable for a developer-managed deployment but not for a Marco-facing product.

**How to avoid:**
1. The PRD already specifies: "Secret key pre-baked into binary (not user-facing)." Implement this — compile the HMAC secret as a Go constant or load it from a separate locked-down source, never from a user-editable config file.
2. `config.ini` (or equivalent) should contain ONLY Marco-editable settings: `watch_folder`. The webhook URL and secret must not appear in it.
3. For remote config override (PRD requirement), deliver config updates via a signed config payload from the server, not by having Marco edit a file.
4. Add config validation at startup: if `watch_folder` does not exist or is not a directory, immediately set tray to red and log a clear error. Do not silently proceed with a missing folder.
5. Consider protecting `C:\EarlScheibWatcher\` from non-admin writes (installer sets ACLs so only SYSTEM and Administrators can write the binary and DB, while the config file allows Marco's user account to write only the folder path setting).

**Warning signs:**
- Support call: "The green light went away and I didn't change anything." (Someone edited config.ini.)
- Log shows HTTP 401 (bad HMAC) after a previously working period.
- Watch folder path in config contains drive letter after a network remapping.

**Phase to address:**
Phase 2 / Wizard and Phase 3 / Core Watcher. Config design decisions made before any user-facing work begins.

---

### Pitfall 12: Crash Telemetry Becoming a Data-Exfiltration Liability

**What goes wrong:**
The crash telemetry implementation captures panic stack traces and POSTs them to a telemetry endpoint. Stack traces contain variable values from the goroutine frames. If any frame contains a BMS XML payload (customer name, phone number, vehicle info), or the config map (containing the secret key), that data is sent to an external server — potentially a GDPR and CCPA data-exfiltration violation, and definitely a liability risk if the telemetry endpoint is ever compromised.

**Why it happens:**
Go's `recover()` + goroutine dump captures everything in scope at crash time. Developers copy generic telemetry patterns without reviewing what data ends up in the payload.

**How to avoid:**
1. Collect only: error message string, Go version, OS version, app version, and a minimal crash location (file:line). Do not send stack frames with variable values.
2. Never include: file paths (may reveal customer data from filename), XML content, config values, or any data that was being processed at crash time.
3. The telemetry endpoint must be under the developer's control (e.g., a minimal endpoint on `support.jjagpal.me`) and not a third-party SaaS crash reporting service unless you have verified its data processing agreement.
4. Add a comment in code at every telemetry callsite: `// WARNING: do not add variable contents here — PII risk`.
5. For a single-user deployment like this, simple crash telemetry (error type + version) is sufficient. Do not add Sentry, Rollbar, or similar until there are multiple users and you have a DPA in place.

**Warning signs:**
- Telemetry payload contains `xml_bytes`, `filepath`, `cfg`, or any customer data string in the POST body.
- Telemetry is sent to a third-party endpoint you do not control.
- Stack trace in telemetry contains customer phone numbers.

**Phase to address:**
Phase 4 / Tray App (where telemetry is implemented). Write a telemetry payload review checklist and test against a sample crash that includes dummy customer data.

---

### Pitfall 13: Log File Grows Unboundedly or Contains Customer PII

**What goes wrong:**
Two distinct failure modes:
(a) The log file grows without bound on a long-running install. At 5 runs/hour × 24h × 365 days, even a modest log verbosity can fill a small SSD partition over months.
(b) DEBUG-level logging, or an overly verbose INFO log line, writes the BMS XML payload or customer name/phone into `ems_watcher.log`. This log file is in `C:\EarlScheibWatcher\` — accessible to any admin-level process or a curious IT technician who opens the folder.

**How to avoid:**
(a) The Python watcher already uses `RotatingFileHandler` with 2 MB × 5 backups (10 MB max). Port this exactly to Go: use `lumberjack` or equivalent rotating log library. Set max size 2 MB, max backups 5, compress old backups.
(b) Audit every log line: log filename and byte count, never log XML content. Log HTTP response status code, never log response body in INFO — only log a truncated preview (first 200 chars) at DEBUG level, and only if the response is an error. The Python watcher already does `body_preview = resp.text[:200]` only on error — replicate this exactly.
(c) Set default log level to INFO, not DEBUG, in the shipped binary. Provide a mechanism (via tray right-click menu) to enable DEBUG mode temporarily for troubleshooting.

**Warning signs:**
- Log file exceeds 10 MB.
- Grep for any customer name in the log file finds a match.
- Log contains the literal text `<GivenName>` or `<CommPhone>` (BMS XML field names).

**Phase to address:**
Phase 3 / Core Watcher Logic. Log design must be part of the initial port, not added later.

---

### Pitfall 14: Code-Signing Certificate Cost, Renewal, and HSM Requirement Post-2023

**What goes wrong:**
Developer defers signing to "after we test." Testing reveals the binary works but shipping to Marco requires signing. Certificate procurement from DigiCert or Sectigo now requires:
- Identity verification (takes 3–10 business days for OV)
- A hardware security module (USB token shipped by CA, or cloud HSM subscription)
- Annual renewal (as of Feb 15, 2026, maximum certificate lifespan is 1 year — multi-year certs are eliminated)
- HSM-compatible signing workflow in CI (cannot just `cp` the key onto a build server)

This creates a 2-week procurement blocker late in the project if not planned early.

**How to avoid:**
1. Start OV certificate procurement at the beginning of Phase 5 / Installer, not at the end.
2. Expect costs: DigiCert Standard OV ~$370/yr; Sectigo OV ~$200–$300/yr via resellers. Cloud HSM options exist (DigiCert KeyLocker, SSL.com eSigner) that work in CI without a physical USB token — prefer these for CI/CD automation.
3. Set up the signing workflow in CI (GitHub Actions or equivalent) using the cloud HSM's CLI tool (e.g., `smctl sign`) before any release candidate build.
4. The `signtool.exe` command needed for signing is Windows-only — cross-compilation from Linux can produce the binary, but signing must happen on a Windows runner (GitHub Actions windows-latest) or via cloud HSM remote signing.
5. Keep a calendar reminder 60 days before annual renewal — do not let the cert expire while the app is in production.

**Warning signs:**
- The release plan has "sign the binary" as the last step with no time buffer.
- No HSM workflow exists in CI yet.
- The certificate is stored as a `.pfx` file on a CI server disk (violates CA/B Forum post-2023 requirements and is a security risk).

**Phase to address:**
Phase 1 / Project Setup. Add cert procurement as a task in the first sprint. Sign every build from day one to build download reputation over time.

---

### Pitfall 15: Time Zone Mismatch Between Client Timestamps and Server Business-Hour Logic

**What goes wrong:**
The server schedules Twilio messages during Pacific Time business hours (10–12, 14–16 PT weekdays). The Windows client logs timestamps using its local clock without timezone awareness. If Marco's PC has the wrong time zone set (or if the shop has the PC set to UTC, a common enterprise image default), the heartbeat timestamps and run-at records in the DB are wrong. A support call about "texts going out at the wrong time" leads to incorrect diagnosis based on misleading client log timestamps.

Additionally: SQLite's `datetime('now')` function records UTC (no timezone suffix). The Python status command prints "UTC" explicitly — the Go port must preserve this or it will confuse debugging.

**How to avoid:**
1. The server handles all business-hour scheduling. The client's only time-sensitive responsibility is heartbeat frequency and log timestamps.
2. Log timestamps in Go using `time.Now().UTC().Format(time.RFC3339)` — always UTC, always with explicit "Z" suffix. Never use local time in logs.
3. In the status window, display "Last run: 2026-04-20 14:32:01 UTC" with explicit timezone label.
4. The Scheduled Task trigger runs every 5 minutes — this is time-zone-agnostic and does not need adjustment.
5. Heartbeat POSTs should include an `X-EMS-Timestamp` header in RFC 3339 UTC format so the server can detect if the client clock is badly skewed.

**Warning signs:**
- Client log timestamps don't match server receipt timestamps by more than a few minutes.
- `sent_at` values in the DB are in local time (missing UTC suffix) or show impossible times (e.g., 3 AM when the shop is closed).
- Marco's PC clock shows the wrong time in the system tray.

**Phase to address:**
Phase 3 / Core Watcher Logic. Enforce UTC-everywhere in the Go port from the first line of timestamp code.

---

## Technical Debt Patterns

| Shortcut | Immediate Benefit | Long-term Cost | When Acceptable |
|----------|-------------------|----------------|-----------------|
| Skip code signing for internal testing | Faster first iteration | First external test with Marco is blocked by SmartScreen; requires full restart | Never — sign from day one, even with a test cert |
| Bundle WebView2 bootstrapper (online only) instead of standalone installer | Smaller installer binary | Marco's shop PC may not have internet access during setup; install fails silently | Never for this deployment — bundle the offline standalone |
| Store HMAC secret in config.ini | Easier remote key rotation | Marco or IT can accidentally expose or break the secret | Never — secret must be in binary or server-delivered encrypted config |
| Run both tray and watcher scan as the same process | Simpler architecture | Scheduled Task model broken; always-on process is harder to keep alive across reboots and sessions | Acceptable only if switching to a Windows Service model (out of scope) |
| Use drive-letter path for watch folder | Easier for Marco to configure via folder picker | SYSTEM account task cannot see mapped drives; silent failure | Never — enforce UNC path in UI |
| Verbose DEBUG logging in production binary | Easier remote troubleshooting | Customer phone numbers appear in log; log fills disk | Never as default; provide a toggle |
| Defer uninstaller testing | Saves time during development | Orphan Scheduled Tasks after uninstall; future reinstalls create duplicate tasks | Never — test uninstall/reinstall cycle before any beta |

---

## Integration Gotchas

| Integration | Common Mistake | Correct Approach |
|-------------|----------------|------------------|
| Webhook HMAC | Re-encoding XML before signing | Sign the raw file bytes exactly as read; never parse-then-marshal |
| Webhook HMAC | Using string `==` for comparison | Use `hmac.Equal()` (constant-time) |
| WebView2 | Assuming it is always installed | Detect at startup; bundle offline installer; fallback to Win32 MessageBox on absence |
| SQLite WAL on Windows | Assuming close() releases lock immediately | Use 30s busy timeout, retry with backoff, keep connections short-lived |
| Scheduled Task (SYSTEM) | Using a mapped drive letter in watch path | Use UNC path (`\\server\share\path`) |
| CCC ONE EMS export | Reading file immediately on detection | Use settle check (mtime+size stability over multiple samples) |
| Crash telemetry | Sending full goroutine dump | Strip to error type + location only; no variable values |
| Log rotation | Using standard `log` package | Use `lumberjack` or equivalent rotating logger; cap at 10 MB total |

---

## Security Mistakes

| Mistake | Risk | Prevention |
|---------|------|------------|
| HMAC secret in user-editable config.ini | Marco accidentally deletes or changes it; auth breaks silently | Compile secret into binary or deliver via signed server config; remove from config.ini |
| Full XML payload in crash telemetry | Customer PII (name, phone) exfiltrated to telemetry endpoint | Collect only error type, location, app version — never payload content |
| Customer phone numbers in INFO logs | PII in a plaintext file accessible to any admin on the PC | Log only filenames and byte counts; redact all XML field values from logs |
| Unsigned binary | SmartScreen block; Marco cannot run installer | Sign with OV cert; submit to Microsoft Security Intelligence |
| Non-HSM cert storage (pre-2023 `.pfx` on disk) | Violates CA/B Forum baseline requirements; cert revoked | Use cloud HSM or hardware token per CA/B Forum 2023 mandates |
| No replay protection on HMAC | Captured request can be replayed indefinitely | Add timestamp to signed data; server rejects requests older than 5 minutes |

---

## UX Pitfalls

| Pitfall | User Impact | Better Approach |
|---------|-------------|-----------------|
| Tray icon hidden in overflow on first install | Marco thinks app failed; calls for support | Wizard includes explicit "find your tray icon" step with screenshot |
| Silent success with no visible confirmation | Marco unsure if app is working | Tray icon turns green and shows tooltip "Earl Scheib Watcher — Active" immediately after wizard |
| Config.ini is the only way to change watch folder | Marco accidentally broke it by hand-editing | Provide "Change folder" option in tray right-click menu that opens a folder picker |
| "Run Now" button with no feedback | Marco clicks it, nothing appears to happen | Show a progress indicator or toast notification: "Scanning now…" / "Scan complete: 1 file sent" |
| Second launch silently does nothing | Marco thinks app is not responding | Second instance signals first instance to bring status window to front |
| UAC prompt appears without warning | Marco dismisses it thinking it is malware | Display friendly message immediately before the UAC trigger: "Windows will ask for permission — click Yes to continue" |

---

## "Looks Done But Isn't" Checklist

- [ ] **Installer:** Verify upgrade does not overwrite `config.ini` — run install → configure → reinstall → check config preserved
- [ ] **Installer:** Verify uninstall removes the Scheduled Task — run uninstall → check Task Scheduler → confirm task is gone
- [ ] **WebView2:** Test on a clean Windows 10 VM with no internet connection — verify wizard loads or shows a helpful fallback
- [ ] **Signing:** VirusTotal scan of signed installer shows zero detections
- [ ] **SYSTEM task:** Configure a UNC path, run the task as SYSTEM, confirm files are processed — do not test only under the logged-on user
- [ ] **Settle check:** Unit test that simulates a file changing size mid-settle — confirm it is skipped until stable
- [ ] **SQLite:** Simultaneously run the watcher scan and open the status window during a scan cycle — confirm no lock errors in log
- [ ] **HMAC parity:** Generate a known payload in Go, compute HMAC, verify the result matches the Python `hmac.new(secret.encode('utf-8'), payload, 'sha256').hexdigest()` output for the same secret and payload
- [ ] **Tray icon:** On Windows 11 24H2 VM, confirm icon appears (or is findable in overflow) after install
- [ ] **Single instance:** Launch the app twice simultaneously — confirm only one tray icon, second launch brings status window to front
- [ ] **Log PII audit:** After processing a real (or realistic test) BMS XML file, grep the log for the customer's name and phone number — confirm zero matches
- [ ] **Telemetry audit:** Trigger a simulated crash while a BMS XML payload is in scope — confirm telemetry POST body contains no XML content

---

## Recovery Strategies

| Pitfall | Recovery Cost | Recovery Steps |
|---------|---------------|----------------|
| SmartScreen false positive blocking Marco | MEDIUM | Submit file to Microsoft Security Intelligence; instruct Marco via screenshot to click "More info → Run anyway"; re-sign if cert was changed |
| WebView2 absent, app window blank | LOW | Remote-instruct Marco to download and run the standalone WebView2 installer from Microsoft; or push an updated installer that bundles it |
| SYSTEM task cannot see mapped drive | MEDIUM | Remote-edit config.ini to replace drive letter with UNC path; test via "Run Now" button |
| Orphan Scheduled Task after uninstall | LOW | Remote-instruct Marco to open Task Scheduler, find "EarlScheibEMSWatcher", delete it; or push a cleanup script |
| Config.ini corrupted by editing | LOW | Push a pre-configured replacement config.ini via the remote config override feature |
| Log file filled disk | LOW | Manually delete old log backups; push updated binary with correct log rotation settings |
| HMAC auth broken after secret rotation | MEDIUM | Server must accept both old and new secrets during overlap window; client gets new secret via remote config override |
| Code signing cert expired | HIGH | No new releases can ship until renewed; plan renewal 60 days in advance; existing installs continue running but SmartScreen reputation resets for new downloads |

---

## Pitfall-to-Phase Mapping

| Pitfall | Prevention Phase | Verification |
|---------|------------------|--------------|
| AV / SmartScreen false positives | Phase 1 (infra/signing setup) + Phase 5 (installer) | VirusTotal clean scan; manual install test on fresh Windows 10/11 VM |
| WebView2 runtime absence | Phase 1 (architecture decision) + Phase 5 (installer) | Install test on offline Windows 10 1809 VM |
| SYSTEM task / mapped drive letter | Phase 2 (wizard) + Phase 5 (installer) | Run task as SYSTEM with UNC path configured; confirm files processed |
| File-settle race condition | Phase 3 (core watcher) | Unit test with simulated slow-write file |
| SQLite locking | Phase 3 (core watcher) + Phase 4 (tray app) | Concurrent tray + scan test; check for lock errors |
| HMAC signing gotchas | Phase 3 (core watcher) | Cross-language HMAC parity test |
| Installer UAC / upgrade / uninstall | Phase 5 (installer) | Upgrade and uninstall end-to-end test cycles |
| Tray icon hidden in overflow | Phase 4 (tray app) + Phase 5 (installer wizard) | Fresh Windows 11 install test; onboarding step coverage |
| Single-instance enforcement | Phase 4 (tray app) | Double-launch test |
| Startup registration mechanism | Phase 5 (installer) | Verify Run key present; verify tray launches at login in user session |
| Config editable by user breaking auth | Phase 2 (wizard) + Phase 3 (watcher) | Confirm secret not in config.ini; config validation test |
| Crash telemetry PII | Phase 4 (tray app) | Telemetry payload audit with synthetic crash |
| Log PII and disk growth | Phase 3 (core watcher) | Log PII audit after test run; log size check after 1000 simulated runs |
| Code-signing cert / HSM requirement | Phase 1 (project setup) — procurement | Cert in hand before Phase 5; CI signing pipeline working |
| Time zone mismatch | Phase 3 (core watcher) | Confirm all log timestamps end in "Z"; DB sent_at values are UTC |

---

## Sources

- [Microsoft Go issue: Defender false positive on Go binaries](https://github.com/microsoft/go/issues/1255)
- [Wails issue: Windows Defender false detects empty initial project](https://github.com/wailsapp/wails/issues/3308)
- [Ctrl.blog: How to report a false SmartScreen positive](https://www.ctrl.blog/entry/how-to-false-smartscreen-positive.html)
- [Microsoft Learn: Distribute your app and the WebView2 Runtime](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/distribution)
- [Microsoft Learn: Evergreen vs. fixed version of the WebView2 Runtime](https://learn.microsoft.com/en-us/microsoft-edge/webview2/concepts/evergreen-vs-fixed-version)
- [Microsoft Q&A: Can I access a mapped network drive in task scheduler when user is logged off?](https://learn.microsoft.com/en-us/answers/questions/828861/can-i-access-a-mapped-network-drive-in-task-schedu)
- [WinHelpOnline: Mapped drives not seen from elevated Command Prompt and Task Scheduler](https://www.winhelponline.com/blog/mapped-drives-not-seen-elevated-command-prompt-task-scheduler/)
- [DigiCert: Code Signing Certificates](https://www.digicert.com/signing/code-signing-certificates) — OV ~$370/yr, 1-year max lifespan from Feb 2026
- [Melatonin: How to code sign Windows installers with an EV cert on GitHub Actions](https://melatonin.dev/blog/how-to-code-sign-windows-installers-with-an-ev-cert-on-github-actions/)
- [SQLite: Write-Ahead Logging](https://www.sqlite.org/wal.html)
- [GitHub: SQLite WAL file locked on Windows after close()](https://github.com/oven-sh/bun/issues/25964)
- [DEV: HMAC Webhook Signing Formats](https://dev.to/sendotltd/hmac-webhook-signing-isnt-complicated-the-formats-are-2di4)
- [Hooque: Webhook Security Best Practices](https://hooque.io/guides/webhook-security/) — secret rotation overlap window, clock skew tolerance
- [Wails: Single Instance Lock guide](https://wails.io/docs/guides/single-instance-lock/)
- [Microsoft Tech Community: Windows 11 system tray icons hiding](https://techcommunity.microsoft.com/discussions/windows11/why-does-windows-11-keep-hiding-my-system-tray-icons-taskbar-icons-missing/4472629)
- [GitHub: PowerToys issue — installer fails to remove/create Scheduled Task](https://github.com/microsoft/PowerToys/issues/3103)
- [Microsoft Learn: Code signing options for Windows app developers](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/code-signing-options)
- [Piiano: Why Spilling PII to Logs Is Bad](https://www.piiano.com/blog/spilling-pii)
- [Microsoft Learn: Windows Task Scheduler "Synchronize across time zones"](https://learn.microsoft.com/en-us/answers/questions/790592/windows-task-scheduler-synchronize-accross-time-zo)
- [CCC Knowledge Base: Working with EMS (Estimate Management Standard)](https://cccis.zendesk.com/hc/en-us/articles/360042735691-Working-with-EMS-Estimate-Management-Standard)

---
*Pitfalls research for: Go Windows tray + watcher + WebView2 installer — Earl Scheib EMS Watcher*
*Researched: 2026-04-20*
