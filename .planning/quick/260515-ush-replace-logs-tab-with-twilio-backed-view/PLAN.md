---
quick_id: 260515-ush
slug: replace-logs-tab-with-twilio-backed-view
date: 2026-05-15
status: in-progress
---

# Quick task: Replace admin Logs tab with Twilio-backed view

## Why

The current Logs tab reads the Pi's local `sms_log` table. That table records *what we tried to send*, including the customer phone in the `phone` column even when `TEST_PHONE_RECIPIENTS` fan-out was active. So the operator can't distinguish "Marco actually got it" from "the customer actually got it" — exactly the confusion that surfaced during the SMS go-live audit on 2026-05-15. Twilio's own Messages API is the source of truth for what actually went out on the wire.

Replace the Logs tab with a Twilio-backed view. Keep the local `sms_log` table untouched (still useful for diagnostics — e.g., showing `allowlist_blocked` failures that Twilio never sees).

## Endpoint contract

`GET /earlscheibconcord/twilio-messages?days=N&status=...&direction=...`

- **Auth:** HMAC-only via `_validate_auth(self, raw)` (post-260515-lae, CF Access handles the perimeter).
- **Query params:**
  - `days` (int, 1–90, default 30) — `DateSentAfter` filter passed to Twilio
  - `status` (`all` | `delivered` | `failed` | `undelivered` | `sent` | `queued`, default `all`)
  - `direction` (`all` | `outbound` | `inbound`, default `all`)
  - `limit` (int, 1–500, default 200) — Twilio `PageSize` cap
- **Response:**
  ```json
  {
    "rows": [
      {
        "sid": "SM...",
        "date_sent": 1778882347,         // unix seconds, server-converted
        "direction": "outbound|inbound",
        "status": "delivered|failed|...",
        "from": "+19256033934",
        "to": "+1...",
        "body": "Hi …",
        "error_code": null,
        "error_message": null,
        "price": "-0.0075",
        "price_unit": "USD",
        "customer_name": "Tammie Knight",  // null if no jobs row matches
        "job_id": 312,                     // null if no match
        "job_type": "24h"                  // null if no match
      }
    ],
    "count": 19,
    "cached_at": 1778882500,
    "stale_seconds": 23,
    "cache_ttl_s": 60
  }
  ```
- **Caching:** Module-level dict keyed by `(days, status, direction, limit)` with 60s TTL. Cache hits return immediately with `cached_at`/`stale_seconds` so the UI can show "fetched X seconds ago".
- **Error handling:** 502 with `{"error": "twilio_<code>", "detail": "..."}` if Twilio request fails. Don't crash the whole admin UI — failure is recoverable on next poll.
- **Customer enrichment:** After fetching from Twilio, single `SELECT id, phone, name, job_type FROM jobs WHERE phone IN (?, ?, ...)` then in-memory match by phone. Most-recent job wins on ties (jobs ordered DESC).

## Frontend changes

### `internal/admin/ui/main.js`
- Rename `loadSmsLog`/`renderSmsLog` → `loadTwilioMessages`/`renderTwilioMessages`.
- Switch URL: `${API_BASE}/twilio-messages` (GET, query string), drop the empty-body POST pattern.
- Replace single-axis filter state with `currentLogsFilters = { status: 'all', direction: 'all', days: 30 }`.
- New columns: When (PT) · Direction (↑/↓) · Customer · Phone · Type · Status · Body · SID(short).

### `internal/admin/ui/index.html`
- Replace 3-button filter row with 3 chip groups: Status (All/Delivered/Failed/Undelivered), Direction (All/Out/In), Range (24h/7d/30d).
- Update sub-text to mention this is Twilio data, not local DB.

### `internal/admin/ui/main.css`
- Update `.log-row` grid template to accommodate the new column set: `130px 28px 140px 130px 70px 90px 1fr 90px` (When/Direction/Customer/Phone/Type/Status/Body/SID).
- Add `[data-status="delivered"]` (green) and `[data-status="undelivered"]` (terracotta) status colors.
- Add `.log-row__direction` styled with an arrow glyph and color (↑ ink-soft for out, ↓ accent for in).

### Sync to `ui_public/`
- `cp internal/admin/ui/{main.js,main.css,index.html} ui_public/`
- (There's a `make sync-ui` target per past commits — check if present, use it.)

## File list

- `app.py` — add endpoint handler block under existing `do_GET` (or wherever `/sms-log` lives — actually `/sms-log` is in `do_POST` per `sms_log_path = self.path.split("?")[0]`. Re-check: the existing route accepts POST; the new one will be GET since there's no body to sign. Use `do_GET`.)
- `internal/admin/ui/main.js`
- `internal/admin/ui/index.html`
- `internal/admin/ui/main.css`
- `ui_public/main.js` (sync)
- `ui_public/main.css` (sync)
- `ui_public/index.html` (sync)

## Deploy

1. Edit + commit on dev machine (jjagpal@earl-scheib-followup, master branch)
2. Push to origin/master
3. SSH Pi: `cd /opt/esw/app && git pull --ff-only`
4. `sudo systemctl restart earlscheib.service`
5. `journalctl -u earlscheib.service -n 30 --no-pager` — confirm clean startup
6. `curl -s ...` smoke test the endpoint with proper HMAC
7. Load `https://notify.earlscheibconcord.com/earlscheib` via headless browser, click Logs tab, verify rows render

## Out of scope

- Inbound webhook handling (separate work — user is setting up TwiML Bin in Twilio console)
- Go admin proxy `/api/twilio-messages` (Marco uses Pi UI exclusively; can add later if needed)
- Pagination beyond the first 200 rows (current volume is ~20 messages, fits comfortably)
- Body-search / customer-search (deferred)
