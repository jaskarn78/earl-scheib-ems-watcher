# Earl Scheib EMS Watcher

A Windows desktop application for **Earl Scheib Auto Body Concord** that watches the CCC ONE EMS export folder, deduplicates and HMAC-signs BMS XML payloads, POSTs them to a follow-up webhook every 5 minutes via a Windows Scheduled Task, and ships with an on-demand local-browser queue admin UI for the shop owner.

```
┌─────────────────────────────────────────────────────────────────┐
│  MARCO'S PC                           │ support.jjagpal.me (VPS)│
│  (Windows 10 / 11)                    │                         │
│                                       │                         │
│  ┌──────────────────────┐             │  ┌──────────────────┐   │
│  │ earlscheib.exe --scan│─── HMAC ───────▶│  app.py          │   │
│  │ (Scheduled Task,     │    POST BMS │  │  /earlscheibconcord   │
│  │  every 5 min)        │    XML      │  │  → Twilio SMS    │   │
│  └──────────────────────┘             │  │  → jobs.db       │   │
│                                       │  └──────────────────┘   │
│  ┌──────────────────────┐    HMAC GET │         ▲               │
│  │earlscheib.exe --admin│─── /queue ──────────── │               │
│  │(local :random port;  │    HMAC DEL │         │               │
│  │ browser = UI)        │    /queue   │         │               │
│  └──────────────────────┘             │                         │
└─────────────────────────────────────────────────────────────────┘
```

## Status

**v1.0 shipped 2026-04-21** — 5 phases, 19 plans, 41/41 active requirements satisfied. See [`.planning/MILESTONES.md`](.planning/MILESTONES.md) for the ship notes and [`.planning/milestones/v1.0-MILESTONE-AUDIT.md`](.planning/milestones/v1.0-MILESTONE-AUDIT.md) for the audit.

## For the end user (Marco)

- **Install:** [`https://support.jjagpal.me/earlscheibconcord/download`](https://support.jjagpal.me/earlscheibconcord/download) → double-click `EarlScheibWatcher-Setup.exe` → 3-step wizard.
- **See what's queued:** run `earlscheib.exe --admin` — your browser opens to a list of pending SMS messages, with a 5-second undo cancel on each.
- **Guide:** [`docs/admin-ui-guide.md`](docs/admin-ui-guide.md)

## For developers

### Layout

```
cmd/earlscheib/          Go entrypoint (subcommand dispatcher)
internal/
  admin/                 --admin local HTTP server + browser UI (Phase 5)
  config/                INI parsing + data-dir resolution
  db/                    SQLite dedup + run history (pure-Go driver)
  heartbeat/             /heartbeat POST
  install/               Native install/uninstall/configure (Phase 3)
  logging/               slog + lumberjack rotation
  remoteconfig/          Whitelisted config merge via /remote-config
  scanner/               Settle check + BMS POST loop
  status/                --status output
  telemetry/             Wrap(func) recover → /telemetry
  webhook/               Sign + Send
installer/               Inno Setup .iss + Pascal wizard pages
portable/                Alternative portable-zip distribution
winres/                  Windows resource file + icon
.github/workflows/       CI: cross-compile, sign (conditional), Docker iscc
app.py                   Webhook server — BMS receive, Twilio dispatch,
                         /queue + /telemetry + /remote-config endpoints
tests/                   pytest for /queue endpoint
docs/                    End-user + ops docs
.planning/               GSD planning history (requirements, roadmap, audit)
```

### Tech stack

| Layer | Choice | Why |
|-------|--------|-----|
| Language | Go 1.22+ (client), Python 3 (server) | One static exe on Windows, cross-compiles from Linux, no runtime bundling |
| Build | `go build` + `go-winres` + Inno Setup 6 | Single signed `.exe` for Marco |
| SQLite | `modernc.org/sqlite` (pure Go) | No CGO, no mingw-w64 |
| HTTP | stdlib `net/http` | Zero non-stdlib deps |
| UI | HTML + CSS + vanilla JS (embedded via `go:embed`) | No framework, no bundler, no CDN at runtime |
| Auth | HMAC-SHA256 over raw body, baked-in secret via `-ldflags -X` | Marco cannot accidentally break auth by editing config |
| Scheduling | Windows Scheduled Task every 5 min | Proven model, survives Windows Update |
| Signing | `osslsigncode` (Linux CI) + OV cert | Docker-based, no Windows runner needed |

### Quick start — developer

```bash
# Prereqs on Linux
sudo apt-get install gcc-mingw-w64-x86-64 docker.io
go install github.com/tc-hib/go-winres@v0.3.3

# Build Windows binary (CGO_ENABLED=0, cross-compiled from Linux)
make generate-resources          # generates rsrc_windows_amd64.syso from winres/
make build-windows               # dist/earlscheib-artifact.exe

# Build the installer (Docker + Inno Setup 6.7.1)
docker run --rm -v "$PWD:/work" -w /work amake/innosetup:latest iscc installer/earlscheib.iss
# → installer/Output/EarlScheibWatcher-Setup.exe

# Test
make test                         # go test ./... -race
python3 -m pytest tests/ -q       # server-side /queue endpoint tests

# Locally run the webhook server
python3 app.py                    # listens on :80 (edit PORT env var)

# Locally run the admin UI (on Linux — opens xdg-open instead of rundll32)
CGO_ENABLED=0 go build -o /tmp/esc ./cmd/earlscheib
EARLSCHEIB_DATA_DIR=/tmp/data /tmp/esc --admin
```

### Secret injection

The HMAC secret is baked into the Windows exe at build time. **Never committed**:

```bash
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 \
  go build -ldflags "-X main.secretKey=$GSD_HMAC_SECRET -X main.appVersion=1.0.0 -H windowsgui" \
  -o dist/earlscheib-artifact.exe ./cmd/earlscheib
```

CI reads `GSD_HMAC_SECRET` from GitHub Actions secrets; local dev uses the dev fallback string.

### Subcommands

| Command | Purpose |
|---------|---------|
| `--scan` | Watches the folder, POSTs new BMS XML to the webhook. Runs from Scheduled Task. |
| `--test` | Sends a canned BMS payload to verify connectivity. Exits 0 on 2xx. |
| `--status` | Prints folder reachability, run counts, recent files, recent log errors. |
| `--admin` | Launches local HTTP server + browser for the Queue Admin UI. |
| `--install` | Runs the native install wizard (folder pick, connection test, CCC ONE config). |
| `--uninstall` | Removes Scheduled Task + (optionally) data dir. |
| `--configure` | Re-runs folder selection + connection test without reinstalling. |

## Server side

`app.py` is a single-file stdlib `http.server` on a Linux VM (this box). Routes:

| Route | Method | Auth | Purpose |
|-------|--------|------|---------|
| `/earlscheibconcord` | GET | — | Customer-facing landing page |
| `/earlscheibconcord/download` | GET | — | Download the signed installer exe |
| `/earlscheibconcord/status` | GET | — | Last heartbeat JSON |
| `/earlscheibconcord/heartbeat` | POST | (none, legacy) | Records last-seen timestamp |
| `/earlscheibconcord` | POST | HMAC body | Receives BMS XML, schedules Twilio job |
| `/earlscheibconcord/telemetry` | POST | HMAC body | Client crash report ingest |
| `/earlscheibconcord/remote-config` | GET | HMAC empty body | Returns whitelisted config overrides |
| `/earlscheibconcord/queue` | GET | HMAC empty body | List pending SMS jobs (Phase 5) |
| `/earlscheibconcord/queue` | DELETE | HMAC body | Cancel one pending job by id |

Twilio sends WhatsApp (sandbox) today; switching to production SMS is documented as a one-line change in `app.py`.

## Deploying this server

This repo is both the client source AND the running production server — `app.py` is served by the `earl-scheib.service` systemd unit on `support.jjagpal.me`. Pulling new changes:

```bash
cd /home/jjagpal/projects/earl-scheib-followup
git pull
# If the installer exe changed, rebuild it:
make build-windows && \
  docker run --rm -v "$PWD:/work" -w /work amake/innosetup:latest iscc installer/earlscheib.iss && \
  cp installer/Output/EarlScheibWatcher-Setup.exe .
# Restart
sudo systemctl restart earl-scheib.service
```

## Planning history

This project was built using [GSD](https://github.com/jjagpal/get-shit-done) (structured planning with planning/research/execution/verification phases). The full artifact trail is in [`.planning/`](.planning/):

- [`PROJECT.md`](.planning/PROJECT.md) — product vision + validated requirements + key decisions
- [`MILESTONES.md`](.planning/MILESTONES.md) — v1.0 ship notes
- [`milestones/v1.0-ROADMAP.md`](.planning/milestones/v1.0-ROADMAP.md) — per-phase details
- [`milestones/v1.0-MILESTONE-AUDIT.md`](.planning/milestones/v1.0-MILESTONE-AUDIT.md) — final audit (tech_debt, user-accepted)
- `milestones/v1.0-phases/` — every `PLAN.md`, `SUMMARY.md`, `VERIFICATION.md` from execution

## License & contact

Private / single-customer deployment. No license; not open for external contribution.

**Support:** [support.jjagpal.me](https://support.jjagpal.me) · **Dev:** `admin@jjagpal.me`
