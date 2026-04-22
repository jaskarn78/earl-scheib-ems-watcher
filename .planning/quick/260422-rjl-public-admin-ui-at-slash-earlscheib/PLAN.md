---
phase: quick
plan: 260422-rjl
type: quick
autonomous: true
---

# Quick Task 260422-rjl — Public Admin UI at /earlscheib

## Objective

Serve the admin UI from `app.py` at `/earlscheib` with basic-auth protection, so the operator can view the pending-jobs queue from any browser via `https://support.jjagpal.me/earlscheib`. Marco's local Go admin (served at `/api/*`) must continue to work unchanged.

## Approach

1. **Copy UI assets to `ui_public/`** and add a `sync-ui` Makefile target so the source of truth stays `internal/admin/ui/`.
2. **Refactor `main.js`** to honor `window.API_BASE_PATH` (default `/api` keeps Go admin intact; app.py injects `/earlscheibconcord`). Also branch cancel-job to DELETE when API_BASE is not `/api`.
3. **Serve `/earlscheib`, `/earlscheib/main.css`, `/earlscheib/main.js`** from `app.py` behind optional basic auth (disabled when env vars unset → 404).
4. **Introduce `_validate_auth()`** wrapping HMAC OR basic-auth, apply to operator endpoints (queue, diagnostic, send-now, DELETE queue). Watcher endpoints stay HMAC-only.
5. **Leave feature DISABLED** after verification — operator sets real creds in .env when ready.

## Tasks

- Task 1: Copy UI + Makefile sync-ui + main.js API_BASE_PATH refactor + Python routes
- Task 2: Basic-auth helper + dual-auth on operator endpoints only
- Task 3: Verification (6 curl checks) + feature-disabled final state
