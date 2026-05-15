---
quick_id: 260515-ush
slug: replace-logs-tab-with-twilio-backed-view
date: 2026-05-15
status: done
commits:
  - d8cebb9 — feat: Twilio-backed Messages tab + fix 401 regression on admin endpoints
  - 734b2da — fix: correct customer enrichment for Twilio Messages view
---

# 260515-ush — Replace admin Logs tab with Twilio-backed view

## What shipped

**USH-01: Twilio-backed Messages tab** (replaces the local `sms_log` view that couldn't
distinguish "we tried to send to the customer" from "we actually sent to the customer"
during the TEST_PHONE_RECIPIENTS fan-out era).

- **New endpoint `GET /earlscheibconcord/twilio-messages`** — queries Twilio's
  Messages API server-side; returns outbound + inbound, filterable by
  `days`/`status`/`direction`/`limit`. 60s in-memory cache keyed by
  `(days, status, direction, limit)` so multi-operator UIs don't hammer Twilio.
  CF Access at the edge — no origin-side auth check.
- **Two-stage customer enrichment** (post `734b2da`):
  1. **Body match against `sms_log`** is authoritative for any send routed
     through `send_sms()` — including fan-out residue (same body, multiple
     recipients) which the original phone-only logic mislabeled.
  2. **`From +NNN:` prefix parsing** for messages with `to = OPERATOR_FORWARD_NUMBER`
     (Marco's cell) — handles TwiML Bin forwards. Replier looked up in jobs
     only when the phone is unambiguous.
  3. **Phone fallback** only when the phone maps to a single customer name
     (otherwise un-enrichable rather than wrong).
- **Admin UI Logs tab rewritten** — new columns `When · ↑↓ · Customer · Phone ·
  Type · Status · Body · SID`; chip filters for status/direction/range;
  freshness indicator (`N messages · Xs ago`); responsive collapse <1100px.
- **Local `sms_log` retained** as a diagnostic record (still captures
  `allowlist_blocked` failures Twilio never sees); `POST /sms-log` endpoint
  preserved for future diagnostic access.

**LAE-regression fix** (folded into the same commit because the new tab
couldn't work without it). `260515-lae` stripped Basic auth from `_validate_auth`
and updated `/queue` to drop the origin check, but missed every other
browser-facing endpoint. All 8 stragglers were returning 401:

- `GET /templates`, `GET /schedules`, `POST /sms-log`
- `POST /queue/resend`, `POST /queue/uncancel`, `POST /reset-test-jobs`
- `PUT /templates/{job_type}`, `PUT /schedules/{job_type}`

`_validate_auth` is now unused (no callers); deleted. Tests rewritten to
match the no-origin-auth contract, mirroring commit `83d0302`.

## What we verified live

Round-trip SMS test confirmed end-to-end via Twilio API poller (10s cadence):

- `15:46:23 UTC` — outbound from `+19256033934` to Jas `+15308450190`, queued.
- `15:47:25 UTC` — Jas replied "Hi Marco" → `inbound received`.
- `15:47:25 UTC` — TwiML Bin fired same poll cycle → `outbound delivered`
  from `+19256033934` to Marco `+19254215772`, body `From +15308450190: Hi Marco`.

Forward latency was effectively zero (TwiML Bin executes synchronously
inside Twilio's cloud).

## Catch-up forwards to Marco

Twilio Messages API showed 4 inbound messages total. Pre-TwiML-Bin replies
that got Twilio's dev auto-reply ("Configure your number's SMS URL...")
instead of reaching Marco:

- **Jim Nelson `+19255884764`** — 5/15 10:31 AM PT — full estimate-comparison reply
- **Anthony Smalls `+19254359099`** — 5/13 4:13 PM PT — "Hello, Still deciding on things"

Both forwarded as `[Catch-up reply, MM/DD HH:MM PT] From +NNN: <body>`;
both delivered to Marco `+19254215772`. The other two inbound were Jas's
test (already forwarded via TwiML Bin) and one from Marco's own phone
to his own Twilio number (skipped — self-forward).

## Files touched

- `app.py` — +Twilio API reader, enrichment pipeline, `/twilio-messages` route,
  `_validate_auth` removed, browser endpoints unguarded.
- `internal/admin/ui/{index.html, main.js, main.css}` + mirror in `ui_public/`
  via `make sync-ui` — Messages tab rewrite.
- `tests/test_templates_endpoint.py`, `tests/test_schedules_endpoint.py` —
  no-origin-auth contract.

## Out of scope / followups

- `test_get_templates_placeholder_catalog` and `test_get_templates_sample_row_from_pending_job`
  fail on pre-existing doc-drift (placeholder list missing `short_model`;
  test expects `Earl Scheib Auto Body Concord` but prod renamed to
  `Earl Scheib Of Concord`). Worth a cleanup pass.
- Go admin proxy `/api/twilio-messages` not added (Marco hits the Pi UI
  exclusively).
- Pagination beyond 200 rows deferred (current volume ≪ 200/month).
- Body-search / customer-search on the Messages tab — deferred.
- Two-way SMS threading (Marco replying from his phone routes back to the
  customer) would require a `/sms-inbound` endpoint in `app.py` with a
  per-customer session map. Not needed for current scope.
