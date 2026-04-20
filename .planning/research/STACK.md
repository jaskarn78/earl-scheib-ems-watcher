# Stack Research

**Domain:** Windows desktop tray app — Go + WebView2, single-exe installer, background file-watcher
**Researched:** 2026-04-20
**Confidence:** MEDIUM-HIGH (all versions verified against pkg.go.dev and official sources; WebView2 binding CGO behaviour confirmed via GitHub)

---

## Recommended Stack

### Core Technologies

| Technology | Version | Purpose | Why Recommended |
|------------|---------|---------|-----------------|
| Go | 1.22+ | Language | Stable, strong Windows cross-compile story with CGO_ENABLED=1 + mingw-w64; 1.22 is current LTS-equivalent |
| fyne.io/systray | v1.12.0 | System tray icon + context menu | Actively maintained fork of getlantern/systray; removes GTK dependency; SetIcon() works at runtime on Windows for colour-state swaps; stable v1 release (Dec 2025) |
| github.com/jchv/go-webview2 | pseudo-version (Feb 2026) | WebView2 HTML/JS UI (wizard + status window) | Only CGO-free Go binding to WebView2; no formal release tags but actively developed; used by Wails internals; no Cgo = simpler cross-compile |
| modernc.org/sqlite | v1.49.1 | Embedded SQLite — dedup DB + run history | Pure-Go SQLite (C transpiled to Go); CGO-free; supports Windows amd64/386/arm64; SQLite 3.53.0; published Apr 2026; no mingw-w64 needed for DB layer |
| Inno Setup | 6.7.1 | Single-exe installer | Generates one setup.exe; proven Windows ecosystem standard; supports dark mode, pre-signed exe bundling, Scheduled Task commands via [Run] section; Docker-based iscc on Linux CI |

### Supporting Libraries

| Library | Version | Purpose | When to Use |
|---------|---------|---------|-------------|
| github.com/hashicorp/go-retryablehttp | v0.7.8 (Jun 2025) | HTTP client with automatic retry + exponential backoff | Replace raw net/http for all webhook POST and heartbeat calls; configurable retry count, backoff policy, retryable status codes |
| gopkg.in/ini.v1 | v1.67.1 (Jan 2026) | Config file parsing and writing | Matches existing config.ini format; round-trip read/write preserves comments; minimal dependency footprint; Windows-native INI format Marco already knows |
| log/slog (stdlib) + gopkg.in/natefinch/lumberjack.v2 | slog: Go 1.21+ stdlib; lumberjack v2.2.1 | Structured logging + file rotation | slog is the stdlib standard since Go 1.21 — no external logger dep; pair with lumberjack as io.Writer for 2 MB x 5 backup rotation matching Python watcher's RotatingFileHandler config |
| github.com/tc-hib/go-winres | v0.3.3 (Apr 2024) | Embed Windows app icon, manifest, version info into exe | Produces _windows_amd64.syso picked up automatically by go build; run from Linux CI before go build; required for proper UAC manifest and file-properties version string |
| crypto/hmac + crypto/sha256 (stdlib) | Go stdlib | HMAC-SHA256 request signing | No external dep needed; identical to Python's hmac.new(key, body, hashlib.sha256) |
| github.com/capnspacehook/taskmaster | master (152 stars) | Windows Scheduled Task registration via COM API | Full Task Scheduler 2.0 API access from Go; more reliable than shelling out to schtasks.exe for SYSTEM-account tasks; needed for first-run wizard task creation and repair |

### Development Tools

| Tool | Purpose | Notes |
|------|---------|-------|
| mingw-w64 (x86_64-w64-mingw32-gcc) | C cross-compiler for Windows from Linux | Required because fyne.io/systray needs CGO_ENABLED=1; install via `apt-get install gcc-mingw-w64-x86-64` |
| go-winres CLI | Pre-build step: generate .syso resource files | Run `go-winres make` before `go build`; outputs rsrc_windows_amd64.syso |
| Inno Setup via Docker (amake/innosetup-docker or Vrex123/inno_setup) | Build installer on Linux CI | Wine-based Docker image runs iscc.exe; no Windows runner needed in CI |
| osslsigncode | Code-sign PE/EXE from Linux CI | Open-source Authenticode signing; uses OpenSSL + PFX/PKCS#11; alternative to Windows-only signtool.exe |
| GitHub Actions windows-latest runner (optional) | Alternative signing path | If using Windows runner for signing only; Inno Setup 6.4.1 is available on the image as of Feb 2025 |

---

## Installation

```bash
# Go toolchain (Linux dev machine)
# Install Go 1.22+ from https://go.dev/dl/

# Cross-compile dependencies
sudo apt-get install gcc-mingw-w64-x86-64

# go-winres (resource file generator)
go install github.com/tc-hib/go-winres@latest

# Core Go dependencies (go.mod)
go get fyne.io/systray@v1.12.0
go get github.com/jchv/go-webview2
go get modernc.org/sqlite@v1.49.1
go get github.com/hashicorp/go-retryablehttp@v0.7.8
go get gopkg.in/ini.v1@v1.67.1
go get gopkg.in/natefinch/lumberjack.v2@v2.2.1
go get github.com/tc-hib/winres@v0.3.3
go get github.com/capnspacehook/taskmaster

# Cross-compile for Windows (amd64)
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 \
  CC=x86_64-w64-mingw32-gcc \
  go build -ldflags "-H windowsgui -s -w" -o EarlScheibWatcher.exe .

# Installer build (Linux CI via Docker)
docker run --rm -v "$PWD:/work" amake/innosetup installer.iss
```

---

## Alternatives Considered

| Recommended | Alternative | When to Use Alternative |
|-------------|-------------|-------------------------|
| fyne.io/systray | getlantern/systray | Never — getlantern is the unmaintained upstream; fyne-io/systray is the active fork |
| fyne.io/systray | energyde/systray | Only if you need the energyde desktop framework ecosystem; overkill for tray-only use |
| github.com/jchv/go-webview2 | wailsapp/wails (full framework) | If you want full hot-reload dev server, routing, and JS bridge out of the box; Wails adds ~10 MB overhead and couples you to their IPC model. For this project the UI is minimal; raw go-webview2 gives full control with less magic |
| github.com/jchv/go-webview2 | Fyne (fyne.io/fyne) | If WebView2 runtime availability is a concern; Fyne uses its own OpenGL renderer so no WebView2 dependency, but HTML/CSS flexibility is lost — you'd write Fyne widgets instead of HTML |
| modernc.org/sqlite | github.com/mattn/go-sqlite3 | Only if benchmark shows mattn 2-3x faster for your workload matters (it won't for 5-min polling intervals); mattn requires cgo + mingw-w64 and breaks cross-compile purity |
| gopkg.in/ini.v1 | github.com/BurntSushi/toml | If you are willing to change the config file format from INI to TOML and Marco's config.ini is replaced entirely during migration; TOML has no advantage for this project |
| gopkg.in/ini.v1 | github.com/spf13/viper | Viper is heavyweight (brings in cobra, pflag, many transitive deps) for a simple INI file; overkill for a single-section config |
| capnspacehook/taskmaster | schtasks.exe shell-out | schtasks is fine for the installer's [Run] section (one-time task creation); use taskmaster from Go code only when you need programmatic task inspection/repair from the tray app at runtime |
| Inno Setup | NSIS | NSIS has a more complex scripting language (NSIS script vs Pascal-like ISS); Inno Setup has better Unicode support, simpler UAC elevation, and recent dark-mode support; both are viable but Inno Setup is lower friction |
| Inno Setup | WiX (MSI) | WiX produces MSI packages with proper Windows Installer database — only needed for enterprise Group Policy / SCCM deployment. Marco is a single shop owner; a setup.exe is correct format. WiX adds XML verbosity and a steeper learning curve |
| Inno Setup | go-msi | go-msi wraps WiX; same MSI caveats apply; less adoption than Inno Setup for non-enterprise tools |
| osslsigncode (Linux) | signtool.exe on Windows runner | signtool.exe requires Windows and the Windows SDK; osslsigncode handles Authenticode PE/EXE signing from Linux using a PFX file; both produce identical signed output |
| log/slog + lumberjack | uber-go/zap or zerolog | zap/zerolog are faster at high throughput but the difference is irrelevant for a 5-minute polling app; slog is stdlib (no dep), and its Handler interface plugs into lumberjack cleanly |

---

## What NOT to Use

| Avoid | Why | Use Instead |
|-------|-----|-------------|
| getlantern/systray | Unmaintained upstream; last significant commits are old; fyne-io forked and removed GTK dep | fyne.io/systray v1.12.0 |
| wailsapp/go-webview2 (standalone) | Explicitly documented as "not intended to be used as a standalone package" — for Wails internal use only | github.com/jchv/go-webview2 |
| github.com/mattn/go-sqlite3 | Requires CGO; on Linux cross-compile you need mingw-w64 AND a C build; adds complexity to CI for zero benefit at 5-min polling frequency | modernc.org/sqlite |
| github.com/spf13/viper | ~20 transitive dependencies for INI file reading; overkill; config format change not justified | gopkg.in/ini.v1 |
| logrus / zerolog | External logger deps when log/slog ships in stdlib since Go 1.21; no advantage for this project size | log/slog + lumberjack |
| WiX / go-msi | MSI installer complexity (GUIDs, Component tables, upgrade codes) adds hours of setup for a single-shop app | Inno Setup |
| Self-signed certificate | SmartScreen shows red "Unknown publisher" block screen on every install; non-technical user (Marco) may not know to click through | OV or EV code signing certificate ($220–$300/yr) |
| Python bundled via PyInstaller | 30–50 MB payload, antivirus false positives, requires maintaining Python toolchain on dev machine | Port to Go (this project) |

---

## Stack Patterns by Variant

**For the background watcher process (invoked by Scheduled Task every 5 min):**
- Build as a separate binary or as a mode flag (e.g., `--scan`) on the same binary
- Use `modernc.org/sqlite` with WAL mode + busy_timeout for concurrent access from tray and watcher
- Use `log/slog` + lumberjack writing to `C:\EarlScheibWatcher\ems_watcher.log`
- No systray or webview2 needed in this mode — pure CLI

**For the tray + UI process (startup app):**
- Build with `-ldflags "-H windowsgui"` to suppress the console window
- Load fyne.io/systray with three ICO byte arrays (green/yellow/red) and call `systray.SetIcon()` on state changes
- Spawn go-webview2 window on "Open Status" or "Setup Wizard" menu clicks; keep window reference to avoid garbage collection
- go-webview2 uses `embed.FS` or `http.FileServer` on a localhost port to serve the HTML/JS UI

**For the installer (Inno Setup):**
- Bundle `EarlScheibWatcher.exe` (tray app + watcher combined)
- Use `[Run]` section to register the Scheduled Task via schtasks or a `--install` flag on the exe
- Use `[Registry]` section to add tray binary to `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`
- Pre-sign `EarlScheibWatcher.exe` with osslsigncode before passing to iscc

**For code signing in CI (Linux):**
- Store PFX (password-protected) as a GitHub Actions secret
- `echo "$PFX_B64" | base64 -d > signing.pfx`
- `osslsigncode sign -pkcs12 signing.pfx -pass "$PFX_PASS" -n "EarlScheibWatcher" -i "https://support.jjagpal.me" -t http://timestamp.digicert.com -in app.exe -out app-signed.exe`
- Then pass signed exe to iscc

---

## Version Compatibility

| Package | Compatible With | Notes |
|---------|-----------------|-------|
| fyne.io/systray v1.12.0 | Go 1.17+ | Requires CGO_ENABLED=1 + mingw-w64 on Linux cross-compile |
| github.com/jchv/go-webview2 | Go 1.18+; Windows 10 1803+ | WebView2 runtime pre-installed on Win10 1803+; for older machines use the WebView2 Evergreen bootstrapper |
| modernc.org/sqlite v1.49.1 | Go 1.11+; CGO_ENABLED=0 | Pure Go; no mingw-w64 needed; supports windows/amd64, windows/386, windows/arm64 |
| gopkg.in/natefinch/lumberjack.v2 v2.2.1 | Go 1.13+ | Used as io.Writer passed to slog.NewTextHandler() |
| gopkg.in/ini.v1 v1.67.1 | Go 1.13+ | Read/write preserves section order; compatible with existing config.ini structure |
| github.com/hashicorp/go-retryablehttp v0.7.8 | Go 1.13+ | Pre-v1 but stable; used in production by many HashiCorp products |
| capnspacehook/taskmaster | Go 1.16+; Windows only | Uses go-ole for COM; Windows-only build tag applies automatically; API not stable (pin commit) |
| tc-hib/go-winres v0.3.3 | Go 1.17+ | Runs on Linux; produces _windows_amd64.syso; use as pre-build step |
| Inno Setup 6.7.1 | Windows exe output; runs on Windows or via Wine/Docker on Linux | amake/innosetup-docker or Vrex123/inno_setup Docker images work in GitHub Actions |

---

## Cross-Compile Build Matrix

```
Dev machine (Linux):
  CGO_ENABLED=1
  GOOS=windows
  GOARCH=amd64
  CC=x86_64-w64-mingw32-gcc
  → go build -ldflags "-H windowsgui -s -w" → EarlScheibWatcher.exe

Pre-build steps (Linux):
  go-winres make    → rsrc_windows_amd64.syso (icon + manifest + version info)

Post-build steps (Linux CI):
  osslsigncode sign → EarlScheibWatcher-signed.exe
  iscc (Docker)    → EarlScheibWatcherSetup.exe
```

Note: The CGO requirement comes entirely from fyne.io/systray. If systray is removed (e.g., CLI-only mode), the whole build becomes CGO_ENABLED=0 and cross-compile is trivial. Keeping the CGO requirement contained to the tray binary (vs the watcher binary) is worth considering for CI simplicity.

---

## Code Signing Decision

| Option | Cost/yr | SmartScreen | HSM Required | Linux CI Compatible | Recommendation |
|--------|---------|-------------|--------------|---------------------|----------------|
| No signing | $0 | Red "Unknown publisher" block | No | N/A | Unacceptable for non-technical user |
| Self-signed | $0 | Red block | No | Yes | Unacceptable |
| OV certificate (Sectigo/Certera) | ~$200–$225 | Yellow warning on first install; reputation builds organically | Since 2023, yes (USB token or cloud HSM) | Via osslsigncode + PKCS#11 | Acceptable for single-customer deployment |
| EV certificate | ~$270–$300 | As of Mar 2024, same SmartScreen reputation behaviour as OV (Microsoft changed this) | Yes (HSM mandatory) | Via osslsigncode + PKCS#11 | Higher cost, same UX as OV post-2024; only worth it if you need CAB/kernel-mode signing |

**Recommendation:** OV certificate from Certera (~$200/yr) stored in a cloud HSM (DigiCert KeyLocker or SSL.com eSigner). Sign from Linux CI using osslsigncode with PKCS#11. EV is no longer worth the premium for SmartScreen reputation since March 2024.

---

## Confidence Assessment

| Area | Confidence | Basis |
|------|------------|-------|
| fyne.io/systray v1.12.0 | HIGH | Version confirmed pkg.go.dev; CGO requirement confirmed GitHub README |
| modernc.org/sqlite v1.49.1 | HIGH | Version confirmed pkg.go.dev (Apr 2026); Windows amd64 confirmed |
| Inno Setup 6.7.1 | HIGH | Confirmed via official site and community group posts |
| gopkg.in/ini.v1 v1.67.1 | HIGH | Version confirmed pkg.go.dev (Jan 2026) |
| hashicorp/go-retryablehttp v0.7.8 | HIGH | Version confirmed pkg.go.dev (Jun 2025) |
| lumberjack v2.2.1 | MEDIUM | Version confirmed but note "not latest in module" warning on pkg.go.dev — check gopkg.in vs github canonical |
| github.com/jchv/go-webview2 | MEDIUM | No formal release tags; CGO-free claim confirmed README; used by Wails but API may shift |
| capnspacehook/taskmaster | MEDIUM | 152 stars; "API not stable" per author; pins commit required; limited recent maintenance signals |
| go-winres v0.3.3 | MEDIUM | Last release Apr 2024; no blockers found; cross-platform Go tool |
| Code signing OV recommendation | MEDIUM | SmartScreen EV/OV change confirmed via multiple Microsoft Q&A sources (2024); pricing verified via vendor sites |
| Cross-compile approach (mingw-w64) | HIGH | Standard Go + CGO cross-compile; documented on go.dev/wiki/WindowsCrossCompiling |

---

## Sources

- [fyne.io/systray pkg.go.dev](https://pkg.go.dev/fyne.io/systray) — version v1.12.0, CGO requirement confirmed
- [fyne-io/systray GitHub](https://github.com/fyne-io/systray) — maintenance status (updated Mar 2026)
- [jchv/go-webview2 GitHub](https://github.com/jchv/go-webview2) — CGO-free claim, Windows 10+ requirement
- [wailsapp/go-webview2 GitHub](https://github.com/wailsapp/go-webview2) — confirmed "not standalone" warning, v1.0.23
- [modernc.org/sqlite pkg.go.dev](https://pkg.go.dev/modernc.org/sqlite) — v1.49.1, SQLite 3.53.0, Windows amd64 confirmed
- [hashicorp/go-retryablehttp pkg.go.dev](https://pkg.go.dev/github.com/hashicorp/go-retryablehttp) — v0.7.8 (Jun 2025)
- [gopkg.in/ini.v1 pkg.go.dev](https://pkg.go.dev/gopkg.in/ini.v1) — v1.67.1 (Jan 2026)
- [gopkg.in/natefinch/lumberjack.v2 pkg.go.dev](https://pkg.go.dev/gopkg.in/natefinch/lumberjack.v2) — v2.2.1
- [capnspacehook/taskmaster GitHub](https://github.com/capnspacehook/taskmaster) — Task Scheduler COM API, "API not stable" warning
- [tc-hib/go-winres GitHub](https://github.com/tc-hib/go-winres) — v0.3.3, .syso generation for icon/manifest/version
- [Inno Setup Downloads](https://jrsoftware.org/isdl.php) — v6.7.1 confirmed; dark mode in 6.6.0
- [amake/innosetup-docker](https://github.com/amake/innosetup-docker) — Docker-based iscc for Linux CI
- [mtrojnar/osslsigncode](https://github.com/mtrojnar/osslsigncode) — Authenticode signing from Linux
- [DigiCert osslsigncode docs](https://docs.digicert.com/en/software-trust-manager/signing-tools/osslsigncode-with-pkcs11.html) — PKCS#11 HSM signing workflow
- [go.dev/wiki/WindowsCrossCompiling](https://go.dev/wiki/WindowsCrossCompiling) — CGO_ENABLED=1 + mingw-w64 approach
- [Microsoft Q&A: EV/OV SmartScreen change (2024)](https://learn.microsoft.com/en-us/answers/questions/417016/reputation-with-ov-certificates-and-are-ev-certifi) — OV now builds reputation organically
- [ngrok blog: so you want to sign for Windows](https://ngrok.com/blog/so-you-want-to-sign-for-windows) — comprehensive code signing landscape

---
*Stack research for: Earl Scheib EMS Watcher — Go + WebView2 Windows desktop tray app*
*Researched: 2026-04-20*
