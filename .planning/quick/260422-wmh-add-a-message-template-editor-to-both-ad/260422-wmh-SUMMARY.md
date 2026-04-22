---
phase: quick
plan: 260422-wmh
subsystem: admin-ui / webhook-server
tags: [templates, sms, admin, dual-auth, ui, go-proxy]
dependency_graph:
  requires:
    - 260422-rjl (public admin UI + dual-auth helper)
    - 260422-oh4 (Fraunces palette + send-now endpoint)
    - 260422-qaj (full row in /queue, estimate_key dedup)
  provides:
    - GET /earlscheibconcord/templates (dual-auth, operator-only)
    - PUT /earlscheibconcord/templates/{job_type} (dual-auth, renderable-check)
    - /api/templates + /api/templates/{job_type} Go admin proxy routes
    - app.render_template(job_type, row) canonical render helper
    - Templates tab in admin UI (both Go admin and /earlscheib)
  affects:
    - _fire_due_jobs (now uses render_template)
    - /queue/send-now (now uses render_template; widened SELECT)
    - Queue page SMS preview bubble (hydrates from /templates cache)
tech_stack:
  added: []
  patterns:
    - Server-side render via str.format_map + collections.defaultdict(str)
      for KeyError-free missing-placeholder semantics
    - templates table — one row per job_type, absence = default
    - Client-side mirror of server render (regex replace against sample_row)
    - Canonical re-marshal + HMAC over outbound bytes (Go admin proxy)
    - do_PUT method on WebhookHandler (prior routes used do_POST/DELETE)
key_files:
  created:
    - tests/test_templates.py (13 unit tests for render_template)
    - tests/test_templates_endpoint.py (15 integration tests for GET/PUT)
  modified:
    - app.py (+DEFAULT_TEMPLATES, SHOP_CONSTANTS, JOB_TYPE_META, PLACEHOLDERS_*, render_template, templates table, GET /templates, do_PUT)
    - internal/admin/server.go (+remoteTemplatesURL, remoteTemplateURL, /api/templates routes)
    - internal/admin/proxy.go (+handleTemplatesList, handleTemplateUpsert, validTemplateJobTypes)
    - internal/admin/admin_test.go (+8 proxy tests)
    - internal/admin/ui/index.html (.topnav, view wrappers, #template-card-template)
    - internal/admin/ui/main.js (Templates page, chips, preview, dirty tracking, effectiveTemplates cache)
    - internal/admin/ui/main.css (.topnav, .templates*, .tpl-card*, .tpl-chip*)
    - ui_public/{index.html,main.css,main.js} (synced via `make sync-ui`)
decisions:
  - Placeholders use Python str.format syntax ({first_name}) — no Jinja,
    no Mustache, no new template engine dependency
  - collections.defaultdict(str) wraps the interpolation context so unknown
    placeholders silently render as empty string — protects against Marco
    typing {mystery_var} and crashing every future send
  - Empty-body PUT = DELETE row (revert to default); non-empty body = UPSERT
    with renderable-check
  - MSG_24H / MSG_3DAY / MSG_REVIEW kept as module-level aliases to
    DEFAULT_TEMPLATES[*] for back-compat — no callers broken
  - Proxy whitelists {24h, 3day, review} to avoid free-range pass-through
  - Templates tab lazy-loaded on first click; queue page primes effective
    cache once on page load so bubbles show Marco's edits immediately
  - Sample row drawn from newest pending job (real VIN/name/vehicle) when
    the queue is non-empty — falls back to hardcoded Alex Martinez stub
  - UI tab uses CSS `hidden` + aria-selected, not a framework router —
    zero additional deps, zero build step
metrics:
  duration_seconds: 831
  tasks_completed: 4
  files_created: 2
  files_modified: 10
  tests_added: 36
  commits: 4
  completed_date: 2026-04-22
---

# Quick Task 260422-wmh: Message Template Editor Summary

SMS copy for each `job_type` (24h / 3day / review) is now Marco-editable from a
new "Templates" tab in the admin UI (both the local Go admin and the public
`/earlscheib` operator view). Templates live in a new `templates` table on
`jobs.db`; absence of a row means "use default". Every Twilio send path
(`_fire_due_jobs` + `/queue/send-now`) now flows through a single
`render_template(job_type, row)` helper that fills `{first_name}`, `{name}`,
`{vin}`, `{vehicle_desc}`, `{ro_id}`, `{email}`, `{doc_id}`, `{phone}`,
`{shop_name}`, `{shop_phone}`, and `{review_url}` placeholders via
`str.format_map` backed by a `defaultdict(str)` — so unknown placeholders
render as empty string and never crash a send.

## What landed

1. **Server storage + render helper** (`app.py`)
   - `DEFAULT_TEMPLATES` + `SHOP_CONSTANTS` + `JOB_TYPE_META` +
     `PLACEHOLDERS_PER_ROW` + `PLACEHOLDERS_SHOP` module constants. Default
     templates parameterise the shop name / phone / review URL so those three
     strings only live in `SHOP_CONSTANTS` (single source of truth).
   - `init_db()` idempotently creates `CREATE TABLE IF NOT EXISTS templates
     (job_type PRIMARY KEY, body, updated_at)`. No seed rows.
   - `render_template(job_type, row)` reads an override (if present, if
     non-empty), falls back to `DEFAULT_TEMPLATES[job_type]`, builds a
     `defaultdict(str)` merged with `SHOP_CONSTANTS` and per-row columns,
     derives `first_name` from `name.split()[0]` (or `"there"` fallback), and
     returns the rendered body. Accepts `sqlite3.Row` or plain dict.
   - `_fire_due_jobs` and `/queue/send-now` both call `render_template` — the
     old `if/elif/elif/else` body-compose blocks are gone.
   - `MSG_24H / MSG_3DAY / MSG_REVIEW` kept as module-level aliases to
     `DEFAULT_TEMPLATES[*]` so any external importer still resolves.

2. **GET /templates + PUT /templates/{job_type} endpoints** (`app.py`)
   - `GET /earlscheibconcord/templates` (dual-auth, operator-only): returns
     `job_types[]` with effective body + is_override + updated_at, the full
     placeholder catalog, and a `sample_row` drawn from the newest pending
     job (hardcoded fallback if the queue is empty) merged with
     `SHOP_CONSTANTS`.
   - `PUT /earlscheibconcord/templates/{job_type}` (new `do_PUT` method):
     validates `job_type ∈ {24h, 3day, review}`, caps body at 2000 chars,
     DELETEs the row on empty/whitespace (revert), UPSERTs otherwise —
     with a renderable-check against the canonical sample context that
     rejects malformed format strings (`"Hi {unclosed"`) with HTTP 400 and
     `{"error": "template syntax error", "detail": "..."}`.

3. **Go admin proxy** (`internal/admin/proxy.go` + `server.go`)
   - `handleTemplatesList` (GET `/api/templates`) mirrors `handleQueue`
     verbatim — empty-body HMAC, 1 MiB forward.
   - `handleTemplateUpsert` (PUT `/api/templates/{job_type}`) mirrors
     `handleSendNow`'s canonical-rewrite pattern — parses `{body: string}`,
     re-marshals to compact JSON, signs those exact bytes, PUTs upstream.
   - `validTemplateJobTypes` whitelist rejects unknown `job_type` at the
     proxy (zero upstream calls) so the admin is never a free-range pass-
     through to arbitrary upstream paths.

4. **Templates tab UI** (`internal/admin/ui/*` + `ui_public/*`)
   - Topbar grows a `.topnav` between `.brand` and `.stats`: Queue +
     Templates tabs, Work Sans uppercase + oxblood underline on active.
   - Two `#view-queue` / `#view-templates` wrappers around the existing
     queue content and the new templates list. JS toggles `hidden`.
   - One `.tpl-card` per `job_type` with variable chips (clickable —
     insert `{name}` at cursor), bounded textarea (maxlength 2000 + live
     counter), preview bubble (reuses `.sms-bubble`), Save + Reset buttons,
     and a 6px amber dirty-dot that appears when the textarea diverges
     from the saved body.
   - Live preview debounced at 150 ms (same constant as search). Client-
     side render mirrors server semantics (regex `{key}` replace against
     `sample_row` + `SHOP_CONSTANTS` + derived `first_name`).
   - Queue page SMS preview bubble now uses the full job row (not just
     `{name}`) and hydrates `effectiveTemplates[job_type]` from a
     one-shot GET `/templates` call on page load so Marco's edits show
     immediately.
   - `make sync-ui` ensures `ui_public/*` is byte-identical to
     `internal/admin/ui/*`.

## Deviations from Plan

### None material

Plan executed as written. Minor variations worth flagging:

- Plan said "delete nothing without grep first; prefer aliases" for
  `MSG_*`. Grep showed only aliases and comments/tests referencing the
  names; kept aliases as module-level bindings to `DEFAULT_TEMPLATES[*]`.
- Plan suggested padding the proxy PUT body limit at 4 KiB — implemented
  as that exactly.
- Added an extra proxy test (`TestAdminProxy_TemplatePut_EmptyBodyForwardsVerbatim`)
  to cover the `{"body":""}` → DELETE path, and
  `TestAdminProxy_TemplatePut_BadMethod` + `TestAdminProxy_TemplatesList_BadMethod`
  for 405 coverage. Not strictly required by the plan but cheap insurance.
- Plan listed `/api/templates/` subtree registration. `trimPrefix("/api/templates/")`
  on an exact `/api/templates/` (trailing slash, empty job_type) returns
  empty string; that case is explicitly 400'd at the proxy — test
  `TestAdminProxy_TemplatePut_EmptyJobType` confirms.

### Deferred (out of scope)

See `deferred-items.md`:

- `tests/test_queue_endpoint.py::test_get_queue_happy_path` was already
  failing on master before this task started. The test asserts `/queue`
  returns only 7 columns but the endpoint returns 14 after QAJ-01 added
  `vin`, `vehicle_desc`, `ro_id`, `email`, `address`, `sent_at`,
  `estimate_key`. One-line fix in the expected_keys set, but belongs to
  a dedicated debug task per SCOPE BOUNDARY rules. Not caused by this
  change.
- Flaky `internal/scanner.TestRunSettleSkip` observed once during
  validation — retried clean. Timing-sensitive file-settle test
  unrelated to templates.

## Test inventory

**New Python tests** (`tests/test_templates.py` — 13):
- Default templates include shop constants (24h, 3day, review)
- Review template includes review_url
- Override overrides default
- Empty override row falls back to default
- Missing placeholder renders empty (never leaks `{literal}`)
- first_name derived from name.split()[0]
- first_name fallback to "there" when no name
- Explicit first_name beats name-derivation
- sqlite3.Row inputs supported
- Unknown job_type returns ""
- None row still renders with defaults
- Legacy aliases still defined (MSG_24H / MSG_3DAY / MSG_REVIEW)

**New Python tests** (`tests/test_templates_endpoint.py` — 15):
- GET default bodies + placeholder presence
- Placeholder catalog shape (per_row + shop)
- sample_row drawn from newest pending job
- Missing / bad HMAC → 401
- PUT upsert override + GET round-trip
- Empty body PUT → DELETE, returns default body
- Whitespace-only body → DELETE
- Unknown job_type → 400
- Malformed template (`Hi {unclosed`) → 400, no row saved
- Body > 2000 chars → 400
- Missing body field → 400
- Bad HMAC on PUT → 401
- Invalid PUT path → 404
- End-to-end: override saves → render_template returns the override

**New Go tests** (`internal/admin/admin_test.go` — 8):
- TemplatesList_Forwards: GET signs empty body + forwards verbatim
- TemplatePut_Forwards: canonical-JSON HMAC + PUT upstream
- TemplatePut_EmptyBodyForwardsVerbatim: `{"body":""}` preserved
- TemplatePut_UnknownJobType: 400 with zero upstream calls
- TemplatePut_EmptyJobType: empty segment → 400
- TemplatePut_BadMethod: POST on /api/templates/24h → 405
- TemplatesList_BadMethod: PUT on /api/templates → 405
- TemplatePut_PropagatesUpstream400: syntax-error 400 forwarded

## Commits

| # | Hash    | Message |
| - | ------- | ------- |
| 1 | 415217f | `feat(templates): add storage table, render helper, rewire Twilio send path` |
| 2 | dc56638 | `feat(templates): add GET/PUT /templates endpoints with dual-auth + renderable-check` |
| 3 | 1653975 | `feat(templates): add /api/templates proxy routes in Go admin` |
| 4 | 688a33f | `feat(templates): add Templates tab UI with chips, live preview, dirty tracking` |

## Verification evidence

- `python3 -m pytest tests/ -q` → **35 passed, 1 failed** (the failure is
  pre-existing `test_get_queue_happy_path`, not caused by this task).
  All 28 new template tests pass.
- `make test` → **all packages pass** (including 8 new admin proxy tests).
  One flaky `TestRunSettleSkip` observed once, retried clean — unrelated
  timing-sensitive test.
- `diff -rq internal/admin/ui ui_public | grep -v README | grep -v gitkeep`
  → silent (byte-identical).
- End-to-end smoke test against a live `app.py`: index.html contains
  `data-view="templates"` + `template-card-template` + `templates-list` +
  `API_BASE_PATH`; GET `/templates` returns 3 job_types + placeholders +
  sample; PUT with override returns `is_override: true`; PUT with
  `"Hi {unclosed"` rejected with 400; PUT with empty body reverts to
  default and returns `is_override: false`.
- Grep-verify on `MSG_*`:
  - `app.py:155-157` — alias definitions (intended)
  - No remaining `MSG_*.format(...)` call sites (per-task grep)
  - UI source/ui_public comments reference the legacy names — will drift
    naturally as future templates evolve; left for a doc pass

## Self-Check: PASSED

- [x] Task 1 commit 415217f exists
- [x] Task 2 commit dc56638 exists
- [x] Task 3 commit 1653975 exists
- [x] Task 4 commit 688a33f exists
- [x] `tests/test_templates.py` created
- [x] `tests/test_templates_endpoint.py` created
- [x] `.planning/quick/260422-wmh-.../260422-wmh-SUMMARY.md` created (this file)
- [x] `ui_public/*` byte-identical to `internal/admin/ui/*`
- [x] Renderable-check verified: PUT `"Hi {unclosed"` returns 400
- [x] Empty-body PUT verified: returns `is_override: false` + row deleted
- [x] No `MSG_*.format(...)` calls in code (only aliases and comments)
