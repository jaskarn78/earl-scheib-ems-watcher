---
gsd_state_version: 1.0
milestone: v1.0
milestone_name: milestone
status: v1.0 milestone complete
stopped_at: Three quick tasks shipped 2026-05-08 — 260508-q9c (empty-date fix + installer release 700cf13, Marco self-updated), 260508-spn (Schedules editor tab + SCHEDULER_ENABLED gate, default off), 260508-ukk (per-schedule enable/disable toggle with cancel-on-disable). Server is in dev mode: SCHEDULER_ENABLED=0, TEST_PHONE_* still redirecting to operator + Marco. Only manual button-click sends fire.
last_updated: "2026-05-08T22:30:00Z"
progress:
  total_phases: 0
  completed_phases: 0
  total_plans: 0
  completed_plans: 0
---

# Project State

## Project Reference

See: .planning/PROJECT.md (updated 2026-04-21 after v1.0 milestone)

**Core value:** Marco installs one file, steps through a native install wizard, and forever after follow-up texts and review requests go out automatically. When he wants to inspect or cancel a queued message, `earlscheib.exe --admin` opens a browser for that one task.
**Current focus:** v1.0 complete — awaiting /gsd:new-milestone for v1.1+

## Current Position

Phase: 05
Plan: Not started

## Performance Metrics

**Velocity:**

- Total plans completed: 0
- Average duration: -
- Total execution time: 0 hours

**By Phase:**

| Phase | Plans | Total | Avg/Plan |
|-------|-------|-------|----------|
| - | - | - | - |

**Recent Trend:**

- Last 5 plans: -
- Trend: -

*Updated after each plan completion*
| Phase 01-scaffold-signing P01 | 2 | 2 tasks | 5 files |
| Phase 01-scaffold-signing P02 | 5 | 1 tasks | 1 files |
| Phase 01-scaffold-signing P03 | 15 | 2 tasks | 4 files |
| Phase 01-scaffold-signing P04 | 5 | 2 tasks | 3 files |
| Phase 02-core-scanner P02 | 3 | 1 task | 4 files |
| Phase 02-core-scanner P02-01 | 4 | 2 tasks | 6 files |
| Phase 02-core-scanner P04 | 12 | 2 tasks | 3 files |
| Phase 02-core-scanner P03 | 4 | 2 tasks | 5 files |
| Phase 02-core-scanner P05 | 245 | 2 tasks | 6 files |
| Phase 03-installer-native-config P03 | 2 | 2 tasks | 3 files |
| Phase 03-installer-native-config P01 | 2 | 2 tasks | 4 files |
| Phase 03-installer-native-config P02 | 1 | 2 tasks | 2 files |
| Phase 04-telemetry-remote-config P02 | 3 | 2 tasks | 4 files |
| Phase 04-telemetry-remote-config P01 | 5 | 2 tasks | 4 files |
| Phase 04-telemetry-remote-config P03 | 15 | 2 tasks | 3 files |
| Phase 05-queue-admin-ui P01 | 3 | 3 tasks | 5 files |
| Phase 05-queue-admin-ui P03 | 3 | 3 tasks | 3 files |
| Phase 05-queue-admin-ui P02 | 249 | 3 tasks | 8 files |
| Phase 05-queue-admin-ui P04 | 4 | 4 tasks | 5 files |

## Accumulated Context

### Decisions

Decisions are logged in PROJECT.md Key Decisions table.
Recent decisions affecting current work:

- Phase 1: OV cert procurement must begin at kickoff — 2–10 business day lead time; use cloud HSM (DigiCert KeyLocker or SSL.com eSigner) for non-interactive CI signing
- Phase 3: Use raw jchv/go-webview2 (not Wails) — Bind() + Dispatch(Eval()) threading model; review example code before coding
- Phase 4: WebView2 bootstrapper strategy (Evergreen offline bundle ~120 MB vs Fixed Version Runtime) — decide during Phase 4 plan via POC size measurement
- [Phase 01-scaffold-signing]: ifneq(strip) guard in Makefile prevents empty-string ldflags override when GSD_HMAC_SECRET is unset
- [Phase 01-scaffold-signing]: CGO_ENABLED=0 for Phase 1 stubs; CGO introduced in Phase 3 for systray+webview2
- [Phase 01-scaffold-signing]: No third-party deps in Phase 1 — only stdlib fmt and os in dispatcher
- [Phase 01-scaffold-signing]: CGO_ENABLED=0 kept in Makefile not workflow; no mingw-w64 needed until Phase 3 (systray+webview2)
- [Phase 01-scaffold-signing]: go-version-file: go.mod used in CI setup-go step to stay in sync as go.mod evolves
- [Phase 01-scaffold-signing]: go-winres .syso output to cwd; Makefile mv step moves to cmd/earlscheib/ for Go auto-linking (Go only links .syso from the compiled package directory)
- [Phase 01-scaffold-signing]: go-winres v0.3.3 schema uses #1 resource keys and 0409 LCID (not 0000/1) — plan template was incorrect; corrected from go-winres init output
- [Phase 01-scaffold-signing]: osslsigncode on ubuntu-latest (not signtool.exe on windows-latest) — Authenticode from Linux; CI signing conditional on SIGNING_CERT_B64 secret; RFC 3161 timestamp for post-expiry validity; dev-sign uses openssl ephemeral self-signed cert + /tmp for temp files
- [Phase 02-core-scanner]: Custom emsHandler implements slog.Handler directly for exact Python log format match (UTC YYYY-MM-DD HH:MM:SS [LEVEL] message)
- [Phase 02-core-scanner]: LoadConfig returns defaults on missing/malformed INI without error — matches Python fall-through behaviour
- [Phase 02-core-scanner]: SecretKey absent from Config struct — baked into binary via ldflags per SCAF-04; EARLSCHEIB_DATA_DIR env var enables cross-platform dev testing
- [Phase 02-core-scanner 02-02]: go.mod upgraded 1.22.2 → 1.25.0 automatically (modernc.org/sqlite v1.49.1 requires go >= 1.25.0); transparent toolchain upgrade
- [Phase 02-core-scanner 02-02]: RetryBaseDelay exported package var for test-speed control — 1ns in tests vs 500ms in prod; no interface injection needed
- [Phase 02-core-scanner 02-02]: db functions accept *sql.DB not a wrapper struct — minimal API surface, avoids over-abstraction
- [Phase 02-core-scanner]: Sender func injected in RunConfig; scanner never imports internal/webhook — clean boundary for unit testing
- [Phase 02-core-scanner]: SettleCheck log param is func(string, ...any) not *slog.Logger — allows t.Logf injection in tests without wrapping
- [Phase 02-core-scanner]: Manual retry loop in webhook.Send() — NOT go-retryablehttp — for exact Python semantic parity (3 attempts, 1s backoff doubling)
- [Phase 02-core-scanner]: BackoffBase exported package var in webhook + RetryBaseDelay in db: test-speed override pattern established
- [Phase 02-core-scanner]: Heartbeat sends X-EMS-Signature even when empty (matches Python); webhook Send omits header entirely when secret empty (matches Python 'if secret_key:' guard)
- [Phase 02-core-scanner]: Makefile test target omits CGO_ENABLED=0 for -race (race detector requires CGO; CGO_ENABLED=0 stays for cross-compile build targets only)
- [Phase 02-core-scanner]: runStatus passes nil sqlDB when db.Open fails so status.Print shows 'No database yet' correctly
- [Phase 03-installer-native-config]: installer-syntax-check CI job has no needs: dependency -- runs in parallel with test and build-windows for fast feedback without waiting for binary build
- [Phase 03-installer-native-config]: iscc /Dq /O- flags used for parse-only validation: /Dq suppresses banner, /O- suppresses output file -- together validate .iss syntax without producing a binary
- [Phase 03-installer-native-config]: Placeholder binary (touch dist/earlscheib-artifact.exe) satisfies [Files] Source: path existence check at parse time -- no real Go build needed for CI syntax check
- [Phase 03-installer-native-config]: Task XMLs use UserId=S-1-5-18 (SYSTEM SID) to avoid locale-specific name resolution; User fallback uses LogonType=InteractiveToken
- [Phase 03-installer-native-config]: Connection test uses EARLSCHEIB_DATA_DIR env var override to point earlscheib.exe --test at {tmp} during install
- [Phase 03-installer-native-config 03-02]: CURDIR (not PWD) in Makefile installer target — GNU make built-in handles recursive make calls correctly
- [Phase 03-installer-native-config 03-02]: build-installer CI job installs osslsigncode independently on each ephemeral runner; signing step conditional on SIGNING_CERT_B64
- [Phase 03-installer-native-config 03-02]: Installer signing overwrites in-place so upload-artifact step is unconditional regardless of signing
- [Phase 04-telemetry-remote-config 04-02]: HMAC-sign empty body []byte("") for GET remote-config requests — byte-identical to Python reference
- [Phase 04-telemetry-remote-config 04-02]: AllowedKeys = [webhook_url, log_level] only — secret_key and watch_folder excluded per OPS-04 (safety boundary)
- [Phase 04-telemetry-remote-config 04-02]: config.Merge uses temp-file + os.Rename for atomic INI write — crash-safe
- [Phase 04-telemetry-remote-config 04-02]: remoteconfig.Fetch+Apply added directly to runScan (04-01 not yet applied); will be enclosed in telemetry.Wrap automatically when 04-01 lands
- [Phase 04-telemetry-remote-config 04-01]: Wrap re-panics after Capture — telemetry capture does not swallow panics; process exits non-zero
- [Phase 04-telemetry-remote-config 04-01]: Message truncated to 200 chars max to cap accidental PII exposure per OPS-01
- [Phase 04-telemetry-remote-config 04-01]: tel re-init inside each command after logger is available gives crash-in-Wrap a real logger
- [Phase 04-telemetry-remote-config 04-01]: appVersion injected via ldflags; Makefile VERSION default changed to 0.1.0-dev
- [Phase 04-telemetry-remote-config 04-03]: _validate_hmac(body, sig_header) helper uses hmac.compare_digest for constant-time comparison — prevents timing attacks
- [Phase 04-telemetry-remote-config 04-03]: GET /remote-config validates HMAC of empty body b"" — matches Go client's webhook.Sign(secret, []byte(""))
- [Phase 04-telemetry-remote-config 04-03]: 204 No Content when remote_config.json is {} — client skips merge; avoids unnecessary file writes
- [Phase 04-telemetry-remote-config 04-03]: telemetry.log is JSONL append-only; rotation deferred as tech debt
- [Phase 05-queue-admin-ui]: GET /queue response is bare JSON array, not wrapped object — CONTEXT.md canonical spec
- [Phase 05-queue-admin-ui]: DELETE /queue response {"deleted": 1} integer count; 404 collapses missing-row and already-sent into same error message
- [Phase 05-queue-admin-ui]: do_DELETE on WebhookHandler dispatched automatically by Python http.server — no route registration needed
- [Phase 05-queue-admin-ui]: feTurbulence paper-grain SVG embedded in CSS data URI for single-HTTP-trip favicon + grain; cancel-with-undo fires DELETE only after 5s timer expires
- [Phase 05-queue-admin-ui]: URLCh chan<- string in admin.Config is the sole test-startup mechanism — no stdout capture or port scanning
- [Phase 05-queue-admin-ui]: admin proxy re-marshals incoming JSON to canonical compact form before HMAC signing (no whitespace; stable field order)
- [Phase 05-queue-admin-ui]: signal.NotifyContext wraps parent ctx with SIGINT/SIGTERM for clean integration with the heartbeat watchdog context
- [Phase 05-queue-admin-ui]: runAdmin uses context.Background() — admin.Run manages its own SIGINT watchdog via signal.NotifyContext internally
- [Phase 05-queue-admin-ui]: ui_test.go sequenced in plan 05-04 (Wave 3) to compile after both 05-02 (uiFS) and 05-03 (UI assets) — prevents parallel-wave compile race
- [Phase 05-queue-admin-ui]: Go 1.25.0 already in go.mod satisfies Go 1.22+ for min builtin; no go.mod bump needed for ui_test.go
- [Quick 260422-k38]: Valentin-Kaiser/go-dbase v1.12.10 (pure Go, CGO_ENABLED=0) chosen over LindsayBradford fork (404 on pkg.go.dev); FoxBasePlus + Untested=true opens CCC ONE EMS 2.01 dBase III files
- [Quick 260422-k38]: Virtual-path dedup key (<dir>/<basename>.bundle) for EMS bundles — guaranteed no collision with plain-file processed_files rows
- [Quick 260422-k38]: DocumentVerCode priority E_DOC_NUM > E_RO > E_EST_NUM > E_DOC_ID > E_REF > basename; guaranteed non-empty so app.py DocumentID fallback never fires
- [Quick 260422-k38]: sendFn URL wrapping (strings.HasSuffix ".bundle" → append ?trigger=ems_bundle) keeps RunConfig stable; no new BundleSender field
- [Quick 260422-k38]: Tests chdir into uppercase-safe workdir to work around go-dbase v1.12.10 NewTable full-path ToUpper behaviour; tests drop t.Parallel (conflict with t.Chdir in Go 1.25)
- [Phase quick-260422-nk1]: Self-update mechanism shipped: client polls /version HMAC-signed each scan, SHA256-verifies installer, launches /VERYSILENT reinstall; kill-switch via update_paused sentinel file or AUTO_UPDATE_PAUSED env; cooldown 3600s + 3-strike fail limit
- [Quick 260505-rei]: Sidecar EarlScheibWatcher-Setup.sha256 decouples watcher-binary hash from installer hash — server /version `version` field is now the watcher binary SHA for correct client self-comparison; installer SHA goes in `installer_hash` for download integrity
- [Quick 260505-rei]: 24h failCooldownSeconds replaces permanent fail_count silence gate — watcher self-heals after 86400s without operator intervention
- [Quick 260505-rei]: ForceCheck shared via runUpdate(touchTs bool) — touchTs=false preserves Ts for ForceCheck so normal cooldown scheduling is not disrupted by a force-update operator run
- [Phase quick-260422-oh4]: Admin UI rework shipped — LIGHT Fraunces/Work Sans/IBM Plex Mono palette (cream + oxblood + amber), rejected the planned industrial-garage dark aesthetic per user override; added VIN/vehicle_desc/ro_id/email/address/sent_at columns via idempotent ALTER TABLE; send-now endpoint with Twilio-failure rollback; 30-min heartbeat + friendly sleep panel; IMMEDIATE_SEND_FOR_TESTING env flag; update cooldown 3600s -> 120s for testing cadence
- [Phase quick-260422-qaj]: Dedup by (phone+VIN) with doc_id fallback collapses CCC ONE "Resave" bursts into one pending row; update-pending path preserves send_at on resave; 60-day reopen window; per-estimate timeline UI groups jobs by estimate_key client-side; filter chips (All/Estimates/Work Completed/Sent) + debounced live search (150ms); HeartbeatTimeout bumped 30m→24h (TODO revert before next prod release); verify_dedup.py runnable 4-case evidence script; added .claude/ + received_logs/ to .gitignore
- [Phase quick-260422-rjl]: Shared main.js across Go admin + Python app.py via window.API_BASE_PATH injection (unset → `/api/*`; `"/earlscheibconcord"` → remaps queue fetch, cancel becomes DELETE, send-now path shifts, /alive skipped); ui_public/ runtime copy + `make sync-ui` target keeps it byte-identical to internal/admin/ui/; `_validate_auth` accepts HMAC OR Basic on 4 operator endpoints (GET /queue, DELETE /queue, POST /queue/send-now, GET /diagnostic); watcher endpoints (/commands, /remote-config, /version, /logs, /telemetry) stay HMAC-only; feature disabled when ADMIN_UI_USER or ADMIN_UI_PASSWORD env vars unset (/earlscheib → 404, basic-auth rejected); Cloudflare tunnel at https://support.jjagpal.me/earlscheib verified reachable; deviation: `import os as _os_ui` local alias needed inside new branch because a later `import os` in the same do_GET method makes `os` function-local across all branches
- [Phase quick-260422-wmh]: Marco-editable SMS templates shipped — new `templates` DB table (job_type PK, body, updated_at; absence of row = use default), `render_template(job_type, row)` helper using str.format_map + defaultdict(str) so missing placeholders render "" (never KeyError, never leaking {literal}); `_fire_due_jobs` + `/queue/send-now` now both route body composition through render_template; DEFAULT_TEMPLATES parameterises shop_name/shop_phone/review_url via SHOP_CONSTANTS single source of truth; MSG_24H/MSG_3DAY/MSG_REVIEW kept as module-level aliases for back-compat. New dual-auth endpoints: GET `/earlscheibconcord/templates` returns effective bodies + is_override + placeholder catalog + sample_row (drawn from newest pending job), PUT `/earlscheibconcord/templates/{job_type}` upserts with server-side renderable-check (rejects "Hi {unclosed" before save) and 2000-char cap; empty/whitespace body DELETEs row (revert to default). Go admin proxies `/api/templates` (GET) + `/api/templates/{job_type}` (PUT) with job_type whitelist {24h,3day,review} so proxy is never a free-range pass-through. Templates tab UI on both Go admin and public /earlscheib: topbar grows .topnav (Queue/Templates), one .tpl-card per job_type with clickable variable chips, 150ms-debounced live preview rendered client-side against server sample_row, 6px amber dirty-dot on edit, Save + Reset-to-default, maxlength-2000 textarea with live counter; queue-page SMS preview bubble now hydrates from effectiveTemplates cache so Marco's edits show immediately without refresh. 36 new tests: 13 render helper unit tests + 15 endpoint integration tests (Python) + 8 Go proxy tests (list/put forwarding, canonical-JSON HMAC, job_type whitelist, 405 on wrong method, 400 propagation)

### Roadmap Evolution

- 2026-04-21: Phase 5 added (Queue Admin UI) — post-v1.0 audit extension. Client-side launcher (`earlscheib.exe --admin`) opens local-browser SPA backed by a new server-side `/queue` endpoint on app.py. Run `/gsd:ui-phase 5` before `/gsd:plan-phase 5`.

### Pending Todos

None yet.

### Blockers/Concerns

- SCAF-06 (OV cert provisioned into CI HSM) must be complete before Phase 4 ships; Phase 4 plan must gate on cert readiness
- Phase 3 research flag: jchv/go-webview2 Dispatch() threading for background-goroutine → UI updates is error-prone; review examples before coding
- Phase 5 research flag: /remote-config JSON schema + server-side storage not yet specified; design during Phase 5 plan

### Quick Tasks Completed

| # | Description | Date | Commit | Directory |
|---|-------------|------|--------|-----------|
| 260421-shq | Debuggability improvements — scanner error detail, admin Diagnostic panel, server /diagnostic endpoint | 2026-04-21 | b54eb96 | [260421-shq-debuggability-improvements-scanner-admin](./quick/260421-shq-debuggability-improvements-scanner-admin/) |
| 260422-k38 | EMS dBase bundle parser — CCC ONE AD1/VEH/ENV → BMS XML, scanner bundle track, ?trigger=ems_bundle routing | 2026-04-22 | 967770b | [260422-k38-ems-bundle-parser-dbase-cccone](./quick/260422-k38-ems-bundle-parser-dbase-cccone/) |
| 260422-nk1 | Self-update mechanism — client polls /version each scan, SHA256-verifies installer, silently reinstalls via /VERYSILENT; kill-switch via update_paused sentinel | 2026-04-22 | c8a7544 | [260422-nk1-self-update-autoupdate-mechanism](./quick/260422-nk1-self-update-autoupdate-mechanism/) |
| 260422-oh4 | Admin UI rework (LIGHT Fraunces palette, not dark) + send-now endpoint + VIN/vehicle/RO/email schema + 30m heartbeat + IMMEDIATE_SEND flag + 120s update cooldown | 2026-04-22 | 3620fe4 | [260422-oh4-admin-ui-rework-send-now-customer-detail](./quick/260422-oh4-admin-ui-rework-send-now-customer-detail/) |
| 260422-qaj | Dedup jobs by (phone+VIN) to collapse CCC resave bursts + per-estimate timeline UI with filter chips + live search + bump admin heartbeat 30m→24h | 2026-04-22 | 70be77e | [260422-qaj-timeline-view-dedup-ingestion-keepalive](./quick/260422-qaj-timeline-view-dedup-ingestion-keepalive/) |
| 260422-rjl | Public admin UI at `/earlscheib` with HTTP Basic auth — shared main.js between Go admin (`/api/*`) and app.py (`/earlscheibconcord/*`) via window.API_BASE_PATH; dual-auth helper (HMAC OR Basic) on operator endpoints only; watcher endpoints stay HMAC-only; feature disabled-by-default (404 when env vars unset) | 2026-04-22 | 66f7ccf | [260422-rjl-public-admin-ui-at-slash-earlscheib](./quick/260422-rjl-public-admin-ui-at-slash-earlscheib/) |
| 260422-wmh | Marco-editable message templates — new `templates` DB table + `render_template` helper (str.format_map + defaultdict(str)); dual-auth GET/PUT `/earlscheibconcord/templates/{job_type}` with renderable-check; Go admin proxies `/api/templates*` with job_type whitelist; Templates tab in admin UI (chips, 150ms-debounced live preview, amber dirty-dot, Save + Reset-to-default); 36 new tests | 2026-04-22 | 688a33f | [260422-wmh-add-a-message-template-editor-to-both-ad](./quick/260422-wmh-add-a-message-template-editor-to-both-ad/) |
| 260424-lmf | Rebuild and redeploy EarlScheibWatcher-Setup.exe — fresh Windows build (Templates tab baked in, HMAC secret injected 3 matches), Inno Setup compile via amake/innosetup:latest, /tmp rezip; old live hash `8d586028e9c4143f` → new `d0be23a1e5a2aaa1`; /version + /download.exe confirmed serving new installer on-demand (no app.py restart needed); Marco's self-update loop primed to pull within ~7 min | 2026-04-24 | 4810e41 | [260424-lmf-rebuild-and-redeploy-earlscheibwatcher-s](./quick/260424-lmf-rebuild-and-redeploy-earlscheibwatcher-s/) |
| 260424-oyk | Pre-stage Twilio WhatsApp-sandbox → production-SMS migration on branch `twilio-prod-sms-migration` — one-line `app.py:597` change (drop `whatsapp:` prefix so TWILIO_FROM is used as SMS sender as-is); branch pushed to origin, NOT merged to master; master still on sandbox path until user provides new ACCOUNT_SID/API_KEY/API_SECRET/TWILIO_FROM and flip-day runbook is executed | 2026-04-24 | 54cb771 | [260424-oyk-pre-stage-twilio-whatsapp-sandbox-to-pro](./quick/260424-oyk-pre-stage-twilio-whatsapp-sandbox-to-pro/) |
| 260424-ulh | Fix `/queue` lifecycle filter + reversible test-row injector + evidence-cited Windows-watcher diagnostic — `GET /earlscheibconcord/queue?status=all\|pending\|sent` (default pending, backwards-compat; 400 on bogus); `scripts/insert_test_pending_job.py` (stdlib-only; 7d-future send_at survives IMMEDIATE_SEND; idempotent `--remove` via `"ULH test row"` tombstone); SUMMARY.md concludes WIN-8I9KME32KLC silence is because CCC ONE folder has 1 new bundle since Apr 23 (`e8b18b75` Apr 24 17:01), not a watcher bug — 374 heartbeats, 0 4xx/5xx, client-side dedup working as designed | 2026-04-24 | cab062b | [260424-ulh-fix-queue-filter-to-show-all-jobs-insert](./quick/260424-ulh-fix-queue-filter-to-show-all-jobs-insert/) |
| 260424-vab | Wire admin UI lifecycle filter chips end-to-end — frontend `internal/admin/ui/main.js:503` now requests `/queue?status=all` (synced via `make sync-ui` to `ui_public/main.js`); Go admin proxy `handleQueue` passes `?status=` through with whitelist `{pending,sent,all}` (HTTP 400 on bogus, empty status preserves legacy no-param upstream call for backwards-compat; HMAC over empty body unchanged); `scripts/insert_test_pending_job.py` extended with `--batch` / `--remove-batch` (BATCH_TOMBSTONE `"ULH2 test"` disjoint from existing TOMBSTONE; mutex with `--remove`); 6 ULH2 rows = `(24h, 3day, review) × (pending, sent)` all using `+15308450190`; Rule-1 deviation: `/queue` SELECT in app.py was missing the `sent` column, breaking `jobMatchesFilter` chip semantics (frontend reads `job.sent === 0\|1`) — added one column to projection, additive change, requires earl-scheib.service restart once | 2026-04-24 | 0966ec4 | [260424-vab-make-admin-ui-lifecycle-filter-chips-wor](./quick/260424-vab-make-admin-ui-lifecycle-filter-chips-wor/) |
| 260505-q2t | Closed-RO detection: parse AD2 (RO_CMPDATE/DATE_OUT) + TTL (G_TTL_AMT), override DocumentStatus → "C" when bundle is closed AND has final bill, so review jobs fire instead of estimate follow-ups. Server unchanged (CLOSED_STATUSES already routes "C" → review). 17 new sub-tests (6 override cases, 11 parseAmount cases) + AD2/TTL parse test all green. Source aca49db; CI build #25396318584 success; installer release 3012fd7 at repo root with new SHA256[:16] da3029f88f659ea2 (was 621e7c7cb08354e5). Marco's self-update will pull within ~7min once update_paused sentinel is removed. | 2026-05-05 | 3012fd7 | [260505-q2t-add-closed-ro-detection-parse-ad2-ro-cmp](./quick/260505-q2t-add-closed-ro-detection-parse-ad2-ro-cmp/) |
| 260505-rei | Fix self-update hash bug: sidecar SHA256 (watcher binary hash) + installer_hash field; 24h fail-cooldown replaces permanent silence; force_update one-shot operator command; make release-prep target; 5 new update tests. Source 5942c97; installer release 9722f6a (SHA256[:16] a62ed72bc7de3a87, was da3029f88f659ea2). | 2026-05-05 | 9722f6a | [260505-rei-add-dump-folder-operator-command-server-](./quick/260505-rei-add-dump-folder-operator-command-server-/) |
| 260508-q9c | Fix empty-date false-positive in pickDocumentStatus: parse.go now stores "" instead of zero-time string ("0001-01-01...") when go-dbase Interpret returns time.Time{} for empty date columns. Root cause: lookup(b.AD2, "DATE_OUT") != "" was always true, misclassifying every estimate as closed RO and firing review SMS prematurely (already hit Russell Rosete c238ce78, Jason Rigor c903a6d0; 5 more pending May 11). Diagnosed from Marco's tar archive after manual EMS export proved DATE_OUT is the correct discriminator. 2 new tests in parse_test.go + 1 in bms_test.go; preserves uncommitted RO_CMPDATE-removal in bms.go. Source 5aeb229; installer release 700cf13 (sidecar bf43d1fbe792a484, was a62ed72bc7de3a87). False-positive jobs 150-154 cancelled in jobs.db; jobs 113/115 already shipped (Russell Rosete, Jason Rigor) — not recoverable. Marco self-updated within 2min of release; verified via /commands upload_log → log shows scans post-19:15 no longer log "update: new version detected". | 2026-05-08 | 5aeb229 / 700cf13 | [260508-q9c-fix-empty-date-false-positive-in-pickdoc](./quick/260508-q9c-fix-empty-date-false-positive-in-pickdoc/) |
| 260508-spn | Schedules editor + SCHEDULER_ENABLED gate: new `schedules` DB table (`job_type`, `delay_hours`, `updated_at`); `get_effective_schedule()` helper replaces 3 hardcoded delays in ems_bundle handler; GET/PUT `/earlscheibconcord/schedules{,/{job_type}}` with HMAC+Basic dual auth; PUT rebases `send_at` for all pending jobs of that job_type via `next_send_window(created_at + delay*3600)`. SCHEDULER_ENABLED env var (default "0") gates `_fire_due_jobs` — manual send-now path stays live. New /diagnostic field `scheduler_enabled` drives a dev-mode banner. UI: 3rd topnav tab (Queue/Templates/Schedules) with hours input + "(X day(s))" display, defaults 24/72/24, bounds 1-720h. 25 new Python + 8 new Go tests, all green. 3 commits: 44a5c50 server, 7367dc7 proxy, d1f57ad UI. Server-side only — no installer rebuild. | 2026-05-08 | d1f57ad | [260508-spn-schedules-editor-scheduler-disable-flag](./quick/260508-spn-schedules-editor-scheduler-disable-flag/) |
| 260508-ukk | Per-schedule enable/disable toggle: extends 260508-spn with `enabled INTEGER NOT NULL DEFAULT 1` column on schedules table (idempotent ALTER TABLE migration); GET returns `enabled` field; PUT accepts `{enabled: bool}` and on toggle-OFF cancels all pending jobs of that job_type via `UPDATE jobs SET sent=1, sent_at=now WHERE job_type=? AND sent=0`; toggle-ON does NOT resurrect previously cancelled jobs (only new bundles get scheduled). ems_bundle handler skips schedule_job() for any disabled job_type. UI: toggle switch on each schedule card, dimmed disabled state, hours input + Save remain active when disabled. 15 new Python tests + 5 new Go tests, all green. 3 commits: c9257d7 server, 687afa7 proxy, 809851a UI. Server-side only — no installer rebuild. | 2026-05-08 | 809851a | [260508-ukk-per-schedule-enable-disable-toggle-with-](./quick/260508-ukk-per-schedule-enable-disable-toggle-with-/) |
| 260515-lae | Strip origin Basic Auth — CF Access (Zero Trust email-OTP at jjagpal.cloudflareaccess.com) is now the sole gate for `notify.earlscheibconcord.com/earlscheib*`. Deletes `_validate_basic_auth`, `ADMIN_UI_USER/PASSWORD/ENABLED` env vars, and the `WWW-Authenticate` 401 path. `_validate_auth` simplified to HMAC-only (HMAC branch only, name preserved). `/earlscheib` handler no longer 404s on missing env vars or prompts for Basic — serves the admin UI HTML directly. The 4 dual-auth operator endpoints (GET /queue, DELETE /queue, POST /queue/send-now, GET /diagnostic) drop the `_validate_auth` call entirely; security boundary moves to the CF Access edge. Watcher-only HMAC endpoints (/commands, /remote-config, /version, /logs, /telemetry, BMS ingest POST /earlscheibconcord, templates/schedules/sms-log writes) preserved unchanged. 3 queue auth tests updated to assert the new no-origin-auth contract. Was: Marco's RPi hit the Basic Auth prompt even after authing through CF Access (stacked gates). 2 commits: 9500d38 strip + 83d0302 test updates. Server-side only — requires `git pull && systemctl restart earlscheib.service` on earlscheib-pi + remove `ADMIN_UI_USER`/`ADMIN_UI_PASSWORD`/`ADMIN_UI_AUTH_DISABLED` from `/opt/esw/app/.env` (now dead config). | 2026-05-15 | 83d0302 | [260515-lae-strip-origin-basic-auth-from-app-py-cf-a](./quick/260515-lae-strip-origin-basic-auth-from-app-py-cf-a/) |

## Session Continuity

Last session: 2026-05-15
Last activity: 2026-05-15 - Completed quick task 260515-lae: Strip origin Basic Auth from app.py — CF Access is the sole gate

Stopped at: Three quick tasks shipped end-to-end. (1) 260508-q9c: empty-date false-positive fix in parse.go, installer rebuilt + shipped (sidecar bf43d1fbe792a484), Marco's machine self-updated within 2 min, false-positive review jobs 150-154 cancelled in jobs.db. (2) 260508-spn: Schedules editor tab with per-job-type delay configuration + SCHEDULER_ENABLED env-var gate (default off — dev mode). (3) 260508-ukk: per-schedule enable/disable toggle with cancel-on-disable behavior. app.py restarted to PID 2037910 after each server-side change. Status: dev-mode locked (SCHEDULER_ENABLED=0), only manual button-click sends fire, TEST_PHONE_OVERRIDE/RECIPIENTS continue to redirect to operator + Marco's phones. Production cutover requires (a) flipping SCHEDULER_ENABLED=1, (b) clearing TEST_PHONE_* env vars, (c) Twilio prod migration via the 260424-oyk pre-staged branch.
Resume file: None
