# Insights page — design spec (INS-01)

**Date:** 2026-09-03 · **Owner:** Jas · **User:** Marco (day to day) · **Status:** approved in chat, spec for review

## 1. Purpose

Give Marco one tab that answers, without digging: who should I call today, what is booked this week, who no-showed, and how are the follow-up texts doing. Secondary: keep an honest running record of what the texting program brings in (customers won after a text, revenue per CCC, Twilio cost).

Numbers must reproduce the 2026-09-03 audit for its window: 154 estimates, 95 texted, 42 replied, 10 won after text, 7 closed ROs = $17,650, 31 closed ROs = $86,390 shop total.

## 2. Decisions already made

| Question | Decision |
|---|---|
| Audience | Marco, day to day. Action lists first; ROI section below. |
| Revenue | CCC closed-RO grand total (`TTL.G_TTL_AMT`), labelled "per CCC". Closed = `AD2.DATE_OUT` set **and** total > 0 (same rule as the Go watcher). |
| History | From 2026-05-13 (first export on the Pi). No backfill from Marco's PC. |
| Extras in v1 | Customer timeline drawer, Marco's reply-time stat. |
| Out of scope (v2) | Monday digest text, CSV export, real collected amounts, Google review click tracking (dropped 2026-09-03: it would need a public short-link host; Jas chose to keep the plain Google link). |

## 3. Architecture

Everything stays in the single-file app (`app.py`, BaseHTTPRequestHandler) and the existing SPA (`ui_public/`). No new services, no new Python dependencies.

```
CCC exports (/opt/esw/ccc-exports/*.{env,ad1,ad2,veh,ttl})
        │  hourly, in scheduler_loop            Twilio Messages API (inbound only)
        ▼                                                │  hourly
  ro_exports table  ◄──────── jobs.db ────────►  inbound_sms table
        │                       │                        │
        └──────────► GET /insights?days=N ◄──────────────┘
                     GET /customer?key=K
                              │
                         Insights tab (index.html + main.js) · timeline drawer
```

### 3.1 New tables (migrations in `_migrate()`, idempotent)

**`ro_exports`** — one row per CCC export set (unique on `doc_id`).
`doc_id TEXT PK, phone TEXT, vin TEXT, owner TEXT, trans_type TEXT, supp_no TEXT, create_dt TEXT, ro_in TEXT, date_out TEXT, g_ttl REAL, closed INTEGER, file_mtime INTEGER, parsed_at INTEGER`.
`estimate_key` is derived at query time as `phone|vin` (falls back to `phone|doc_id` when VIN is blank), matching `jobs.estimate_key`.

**`inbound_sms`** — one row per Twilio inbound message (unique on `sid`).
`sid TEXT PK, from_phone TEXT, to_phone TEXT, body TEXT, date_sent INTEGER, synced_at INTEGER`.
Forward copies (`From +NNN:` outbound to Marco) are never stored; only `direction = inbound`.

### 3.2 DBF reader

`_dbf_read(path) -> list[dict]`: pure-Python dBase III/IV reader (header: record count, header length, record length; field descriptors 32 bytes each; types C/N/F/D/L; memo fields ignored; `latin-1`). ~60 lines. Only ENV, AD1, AD2, VEH, TTL are read; LIN and the profile files are ignored.

Fields used: ENV `UNQFILE_ID, TRANS_TYPE, SUPP_NO, CREATE_DT`; AD1 `OWNR_FN, OWNR_LN, OWNR_PH1, OWNR_PH2, INSD_PH1`; AD2 `RO_IN_DATE, DATE_OUT`; VEH `V_VIN`; TTL `G_TTL_AMT`. Phone normalised with the existing `clean_phone()`.

### 3.3 Sync jobs (in `scheduler_loop`, every 60 min, plus once at startup)

- `sync_ro_exports()`: for each `*.env` in `/opt/esw/ccc-exports` whose mtime is newer than the stored `file_mtime` (or unseen), parse the set and upsert. Failures on one set are logged and skipped.
- `sync_inbound_sms()`: Twilio `Messages.json?To=<shop>&DateSent>=<last synced day - 1>` paginated; insert-or-ignore. Uses the same credentials as `send_sms()`. If Twilio is unreachable the page still serves from local data.
- Both are also callable via `POST /insights/sync` (admin, CF-Access gated) for a manual refresh button.

### 3.4 (removed) Review link tracking

Dropped from v1; see §2.

### 3.5 `GET /earlscheibconcord/insights?days=30|90|ytd`

Returns one JSON object. All amounts are floats, all dates ISO strings, all counts ints. Test rows (`is_test=1`, `test|` keys, Jas's and Marco's numbers) are excluded everywhere.

```
{
  "period": {"from":"...","to":"...","days":30},
  "tiles": {  // each: {"value": n, "prev": n}  (prev = same-length window before)
    "estimates", "texted", "replies", "bookings", "closed_ros", "revenue", "avg_ticket", "marco_reply_median_min"
  },
  "funnel": [{"label":"Estimates","n":..},{"label":"Texted","n":..},{"label":"Replied","n":..},{"label":"Won after text","n":..},{"label":"Closed RO","n":..,"revenue":..}],
  "revenue_by_month": [{"month":"2026-08","closed_ros":11,"revenue":31677.0}],
  "template_reply_rate": {"24h": {"sent":72,"replied":30}, "3day": {...}, "review": {...}},
  "twilio_cost": 6.62,
  "warm_leads": [{"key","name","phone","last_reply_at","last_reply","estimate_total","days_since"}],   // replied after a text, no booking, no closed RO; newest first
  "bookings_upcoming": [{"key","name","phone","appointment_at","estimate_total","texted":bool}],
  "no_shows": [{"key","name","phone","appointment_at","days_overdue"}],   // appointment_at < now-1d and no closed RO on the key
  "placeholder_estimates": {"n":68,"of":144}
}
```

Rules:
- **Estimate universe**: distinct `jobs.estimate_key` with `first created` inside the period (job types 24h/3day/review all count as evidence of an estimate; review-only keys are same-day approvals).
- **Texted**: any 24h/3day row with `sent=1` on the key.
- **Replied**: any `inbound_sms` from the key's phone at or after the first sent follow-up.
- **Won after text**: key has a closed `ro_exports` row (matched on `phone|vin`, else on `vin`) with `ro_in >= first_sent`, OR an active booking created after `first_sent`, OR a review job created after `first_sent`.
- **Revenue**: sum of `g_ttl` for closed ROs matched to the key; unmatched closed ROs count toward shop totals only.
- **Marco reply time**: for each `inbound_sms`, the next outbound in `sms_log` with `kind='reply'` to the same phone; median of the gaps, capped at 7 days; ignores replies with no follow-on.
- **Period** filters by estimate creation date for funnel/tiles, by `date_out` for revenue-by-month.

### 3.6 `GET /earlscheibconcord/customer?key=<estimate_key>`

Returns the timeline: `[{"t": iso, "kind": "estimate|text|delivery|reply|booking|ro_closed", "label": "...", "detail": "..."}]` sorted by time, plus the header (name, phone, vehicle, estimate total, status). Sources: `jobs`, `sms_log`, `inbound_sms`, `appointments`, `ro_exports`.

### 3.7 UI

- **Tab**: "Insights" added to the top nav and the mobile bottom bar (sixth item; bar icons shrink to fit). Route `#insights` in `main.js`, same pattern as Schedules/Logs.
- **Layout (mobile-first, in this order)**: period chips (30 · 90 · YTD) with a refresh button → **Call today** (warm leads, each row: name, days since reply, last reply snippet, estimate total, tap → opens Messages thread) → **This week** (upcoming bookings) → **No-shows** (hidden when empty) → **This month** tiles with prior-window delta arrows → **Follow-ups** funnel bars + template reply rates → **Revenue per CCC** monthly bars → footer line: Twilio cost, placeholder-estimate count.
- **Charts**: plain HTML/CSS bars (no library), one accent hue plus gray, direct labels on every bar, table fallback not needed because bars are labelled.
- **Timeline drawer**: opened from any customer name in Insights and from the Messages thread header; slides up on mobile, side panel on desktop; closes with the existing `[hidden]` rule pattern (explicit `.drawer[hidden]{display:none}`).
- **Empty states**: every list has a one-line empty message; never a blank box.
- **Housekeeping (per Jas, 2026-09-03)**: hide the "Test est." and "Test work completed" chips from the top menu. They stay in the DOM and become visible only with `?test=1` in the URL, so Jas can still reach test rows for end-to-end checks.
- **Caching**: `/insights` computed on request from local tables (≤ 50 ms expected); no server cache; asset URLs already versioned by mtime.

### 3.8 Error handling

- Sync failures never break the page; the response carries `"synced_at"` for each source so the UI can show "exports synced 2h ago · texts synced 5m ago".
- A malformed export set is skipped and logged once (by doc_id) — no retries every hour.
- Twilio auth failure logs a warning and leaves `inbound_sms` as is.

### 3.9 Testing

- `tests/test_dbf.py`: reads a fixture export set (copied from a real, anonymised set) and asserts the parsed fields.
- `tests/test_insights.py`: seeds an in-memory `jobs.db` with a handful of estimates, sms_log rows, inbound_sms rows, appointments and ro_exports and asserts funnel counts, attribution, revenue, reply-time median, warm-lead and no-show membership, and period windows.
- Acceptance: run the endpoint against a copy of the production DB for window 2026-05-13 → 2026-09-03 and match the audit numbers above.

### 3.10 Rollout

1. Migrations + DBF reader + syncs (no UI yet); deploy; confirm tables fill on the Pi.
2. `/insights` + `/customer` endpoints; verify against audit numbers.
3. Insights tab + timeline drawer; deploy.
4. Rollback per existing flow: `git reset --hard <sha>` + restart; new tables are additive and harmless if unused.
