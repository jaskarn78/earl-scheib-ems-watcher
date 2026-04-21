---
phase: 05-queue-admin-ui
plan: "02"
subsystem: internal/admin
tags: [go, http, proxy, hmac, embed, heartbeat, testing]
dependency_graph:
  requires:
    - 05-01  # server-side queue endpoints (app.py)
  provides:
    - admin.Run  # called by 05-04 (cmd/earlscheib/main.go --admin)
    - internal/admin package  # consumed by 05-04
  affects:
    - go build (CGO_ENABLED=0)
    - make test
tech_stack:
  added: []
  patterns:
    - URLCh test hook for deterministic HTTP server startup in tests
    - atomicTime mutex-guarded time.Time for concurrent lastAlive updates
    - HMAC-signed proxy: empty body for GET, canonical compact JSON for DELETE
    - Heartbeat watchdog goroutine with configurable timeout and graceful shutdown
key_files:
  created:
    - internal/admin/embed.go
    - internal/admin/server.go
    - internal/admin/proxy.go
    - internal/admin/launcher.go
    - internal/admin/launcher_windows.go
    - internal/admin/launcher_other.go
    - internal/admin/admin_test.go
    - internal/admin/ui/.gitkeep
  modified: []
decisions:
  - URLCh chan<- string field in Config is the sole test-startup mechanism; no stdout capture or port scanning
  - atomicTime uses sync.RWMutex (not sync/atomic) for time.Time since typed atomic generics require >1.22 gymnastics
  - Heartbeat watchdog ticks at HeartbeatTimeout/3 for responsive timeout detection with minimal overhead
  - proxy.go re-marshals incoming JSON to canonical compact form before signing (no whitespace; stable field order)
  - server.go uses signal.NotifyContext so SIGINT/SIGTERM integrates cleanly with context cancellation tree
metrics:
  duration: 249s
  completed_date: "2026-04-21"
  tasks_completed: 3
  files_created: 8
---

# Phase 05 Plan 02: Admin Package — HTTP Server, HMAC Proxy, Heartbeat Summary

**One-liner:** Local HTTP admin server on 127.0.0.1:0 with HMAC-signing proxy (empty-body GET, canonical-JSON DELETE), heartbeat watchdog shutdown, go:embed UI assets, and cross-platform browser launcher.

## Objective Achieved

Built the complete `internal/admin` Go package that:
1. Binds `127.0.0.1:0` (ephemeral port) so Marco's browser never leaves localhost
2. Serves the embedded `internal/admin/ui/` assets at `/` via `http.FileServer(http.FS(uiFS()))`
3. Proxies `GET /api/queue` to the remote webhook server with HMAC signing of empty body
4. Proxies `POST /api/cancel` (browser) → `DELETE /queue` (remote) with HMAC signing of canonical JSON body
5. Runs a heartbeat watchdog: 30s idle triggers `http.Server.Shutdown` with 5s grace
6. Opens the default browser cross-platform (Windows: `rundll32 url.dll,FileProtocolHandler`; Linux: `xdg-open`)

## Tasks Completed

| Task | Name | Commit | Files |
|------|------|--------|-------|
| 1 | Scaffold internal/admin with embed + launcher + build-tag files | d771902 | embed.go, launcher.go, launcher_windows.go, launcher_other.go, server.go (stub), ui/.gitkeep |
| 2 | Replace Run stub with full server.go + implement proxy.go | 713ab2e | server.go (full), proxy.go |
| 3 | admin_test.go — proxy signing, heartbeat lifecycle, port bind, /alive | a2503af | admin_test.go |

## Verification Results

- `CGO_ENABLED=0 go build ./...` — PASS
- `CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build ./...` — PASS
- `go vet ./...` — PASS
- `go test ./internal/admin/... -race -count=1 -timeout 30s` — PASS (9 tests)
- `make test` — PASS (all 11 packages green)
- No non-stdlib deps in `go list -deps ./internal/admin/...` (internal + stdlib only)
- `grep -c "func waitForAdmin" admin_test.go` = 0
- `grep -c "^func startAdmin(" admin_test.go` = 0

## Decisions Made

1. **URLCh test hook**: `Config.URLCh chan<- string` is the only test-startup mechanism — no stdout capture, no port scanning. Non-blocking send (`select { case ch <- url: default: }`) prevents goroutine leaks if the channel is full.

2. **atomicTime**: Uses `sync.RWMutex` over a `time.Time` value rather than `sync/atomic` typed generics, which would require Go 1.19+ type parameter gymnastics. Simpler and safe for once-per-10s heartbeat updates.

3. **Heartbeat watchdog interval**: Ticks at `HeartbeatTimeout / 3` (min 10ms) so the watchdog fires within one tick window after timeout. For the production 30s timeout this means a 10s tick — low overhead.

4. **Canonical JSON re-encoding**: `handleCancel` parses `{"id": N}` then re-marshals to `{"id":42}` (compact, no whitespace, stable field order) before HMAC signing. This ensures the signature covers byte-identical JSON on both sides of the proxy.

5. **signal.NotifyContext**: Wraps the parent ctx with `os.Interrupt + syscall.SIGTERM` handling. The watchdog uses a derived `cancelWatchdog` context from `sigCtx`, so SIGINT cleanly terminates the watchdog goroutine too.

## Deviations from Plan

None — plan executed exactly as written. All three tasks followed the specified content precisely.

## Known Stubs

None. The `internal/admin/ui/.gitkeep` placeholder is intentional and documented — plan 05-03 writes `index.html`, `main.css`, and `main.js` into this directory. The `//go:embed ui` directive picks them up at compile time.

## Self-Check: PASSED
