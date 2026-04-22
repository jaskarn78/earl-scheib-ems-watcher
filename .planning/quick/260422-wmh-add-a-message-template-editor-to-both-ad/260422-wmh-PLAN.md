---
phase: quick
plan: 260422-wmh
type: quick
autonomous: true
---

# Quick Task 260422-wmh — Message Template Editor (both admin UIs)

## Objective

Let Marco customize the SMS copy for each scheduled `job_type` (24h, 3day, review) from a **new "Templates" page** in the admin UI — accessible from both the local Go admin (`earlscheib.exe --admin`) and the public `/earlscheib` operator UI. Templates are stored server-side in `jobs.db`, interpolated with customer/shop variables via stdlib `str.format_map` (no Jinja), and used by **every** Twilio send path (`_fire_due_jobs` + `/queue/send-now`). Clearing a template reverts to the current hardcoded copy (which becomes `DEFAULT_TEMPLATES` in app.py).

**Success = Marco edits one template in the browser, saves, and the next scheduled follow-up sends the new copy.**

## Non-goals

- No templating engine (Jinja / Mustache). `str.format_map` with a defaulting dict is sufficient.
- No per-customer overrides, no A/B testing, no template history. One row per `job_type`.
- No template storage in `config.ini` or `remote_config.json` (those are client-facing).
- No watcher-side changes. Templates render server-side; client still POSTs raw BMS.
- No nav restructure / no new colors. Extend the existing topbar with one "Templates" link; reuse the cream/oxblood/amber Fraunces palette locked by OH4.

## Grounded facts (verified before planning)

### Job types
Confirmed by grepping `app.py` — the **complete set** of job_types today is `24h`, `3day`, `review` (see `_fire_due_jobs` branches, line ~501; `/queue/send-now` composer, line ~2391; and the three calls to `schedule_job` in POST handler at lines ~2475/2478/2483). Front-end mirror: `internal/admin/ui/main.js` `SMS_TEMPLATES` and `JOB_TYPE_LABELS` at lines ~43 and ~60. The task description hints at `estimate_followup, three_day_followup, work_completion, review_request` — those are descriptive names; **the actual job_type tokens are `24h`, `3day`, `review`**. Plan honors the real tokens and includes a display-label map for UI copy.

### Jobs-row columns available as interpolation variables
From `app.py` `CREATE TABLE jobs` + ALTER migrations (lines 207-237):
`id, doc_id, job_type, phone, name, send_at, sent, created_at, vin, vehicle_desc, ro_id, email, address, sent_at, estimate_key`.

### Placeholder set (final — grounded in schema)
Per-row (from the jobs row at send time):
`{name}` (full name — already used today), `{first_name}` (derived: `name.split()[0]` or `"there"` if empty), `{phone}`, `{vin}`, `{vehicle_desc}`, `{ro_id}`, `{doc_id}`, `{email}`.
Shop constants (baked into `app.py`):
`{shop_name}` = `"Earl Scheib Auto Body Concord"`, `{shop_phone}` = `"(925) 609-7780"`, `{review_url}` = `"https://g.page/r/review"`.

**Missing-key semantics:** `str.format_map` with a `collections.defaultdict(str)` so unknown placeholders render **empty string**, not `KeyError`, not the literal `{key}`. Never leak template syntax to a customer.

### Auth model
Reuse `_validate_auth(self, body)` (already dual-auth — HMAC OR Basic; RJL-02 pattern). Watcher endpoints stay HMAC-only. New template endpoints are **operator-only** → dual-auth.

### UI sync requirement
`internal/admin/ui/{index.html,main.css,main.js}` is the source of truth. `ui_public/` is a runtime copy served by `app.py`. **`make sync-ui`** (Makefile line ~122) copies the three files byte-for-byte. Every UI change in this task must end with `make sync-ui`.

---

## Task 1 — Server: storage, render helper, rewire Twilio send path

**Files:**
- `app.py`

**Actions:**

1. Add a new module-level `DEFAULT_TEMPLATES` dict mapping each job_type to its current hardcoded string. **Copy the existing `MSG_24H` / `MSG_3DAY` / `MSG_REVIEW` bodies verbatim** but swap the hardcoded `"Earl Scheib Auto Body in Concord"` / `"(925) 609-7780"` / review URL for the `{shop_name}`, `{shop_phone}`, `{review_url}` placeholders so the defaults themselves are now parameterized:

   ```python
   DEFAULT_TEMPLATES = {
       "24h":
           "Hi {first_name}, this is {shop_name}. Just following up on your recent estimate. "
           "Have questions or ready to schedule? Call us at {shop_phone}.",
       "3day":
           "Hi {first_name}, {shop_name} checking in about your estimate from a few days ago. "
           "We'd love to help get your car looking great! Call {shop_phone}.",
       "review":
           "Hi {first_name}, thank you for choosing {shop_name}! Hope you're happy with your repair. "
           "Would you mind leaving us a Google review? It means a lot: {review_url}",
   }
   SHOP_CONSTANTS = {
       "shop_name":  "Earl Scheib Auto Body Concord",
       "shop_phone": "(925) 609-7780",
       "review_url": "https://g.page/r/review",
   }
   # Canonical order + metadata for the UI. Drives the Templates page card order
   # and the clickable-chip placeholder list.
   JOB_TYPE_META = [
       {"job_type": "24h",    "label": "24-hour follow-up",   "when": "~24 hours after estimate"},
       {"job_type": "3day",   "label": "3-day check-in",      "when": "~3 days after estimate"},
       {"job_type": "review", "label": "Review request",      "when": "~24 hours after job completion"},
   ]
   PLACEHOLDERS_PER_ROW = [
       "first_name", "name", "phone", "vin", "vehicle_desc", "ro_id", "doc_id", "email",
   ]
   PLACEHOLDERS_SHOP = ["shop_name", "shop_phone", "review_url"]
   ```

   Keep `MSG_24H` / `MSG_3DAY` / `MSG_REVIEW` around as **module-level aliases to `DEFAULT_TEMPLATES[...]`** so any external code that imports them (and the existing grep trail) still resolves. Delete nothing without a grep first; prefer aliases.

2. Extend `init_db()` with an **idempotent** `CREATE TABLE IF NOT EXISTS templates`. Matches the existing migration pattern — no migration tool, just guarded DDL:

   ```sql
   CREATE TABLE IF NOT EXISTS templates (
     job_type   TEXT PRIMARY KEY,
     body       TEXT NOT NULL,
     updated_at INTEGER NOT NULL
   )
   ```

   No seed insert — empty table means "use defaults for every job_type". Do NOT pre-populate rows; the override-vs-default distinction is what makes "clear to revert" work.

3. Add a `render_template(job_type: str, row: sqlite3.Row | dict) -> str` helper. Reads the override from `templates` table; if absent / empty / whitespace-only → falls back to `DEFAULT_TEMPLATES[job_type]`; if the `job_type` itself is unknown → returns `""` and logs a warning (matches existing `log.warning("Unknown job_type ...")` behaviour in `_fire_due_jobs`).

   Build the interpolation context:

   ```python
   from collections import defaultdict
   ctx = defaultdict(str)        # missing keys → "" (silent, no KeyError, no {literal})
   ctx.update(SHOP_CONSTANTS)
   for k in PLACEHOLDERS_PER_ROW:
       ctx[k] = (row[k] if (row is not None and k in row.keys() and row[k]) else "")
   # Derive first_name from name if not explicitly set.
   if not ctx["first_name"] and ctx["name"]:
       ctx["first_name"] = str(ctx["name"]).split()[0]
   if not ctx["first_name"]:
       ctx["first_name"] = "there"    # preserves current fallback UX
   body = tpl.format_map(ctx)
   ```

   `row.keys()` works on `sqlite3.Row`. For dict callers this also works (`in dict` returns key membership).

4. **Rewire `_fire_due_jobs` (line ~485):** delete the `if/elif/elif/else` chain composing `body` from `MSG_*.format(name=name)` and replace with a single `body = render_template(job_type, row)` call against the full `row` (it's already `sqlite3.Row` from `cur.fetchall()` with `row_factory = sqlite3.Row`). Keep the `log.warning("Unknown job_type ...")` fast-path: if `render_template` returns `""` for an unknown type, skip with the same warning.

5. **Rewire `/queue/send-now` (line ~2354, POST handler):** replace the identical `if/elif/elif/else` body-compose block with `sms_body = render_template(row["job_type"], row)` — but note the existing `SELECT job_type, phone, name FROM jobs WHERE id = ?` only fetches 3 columns. Widen that SELECT to `SELECT job_type, phone, name, vin, vehicle_desc, ro_id, email, doc_id, first_name_NA FROM jobs WHERE id = ?` — actually the schema has no `first_name` column, derive it from `name`; adjust the SELECT to: `SELECT job_type, phone, name, vin, vehicle_desc, ro_id, email, doc_id FROM jobs WHERE id = ?`. The `render_template` helper handles the `first_name` derivation.

6. **Do NOT change the DB write path, dedup logic, or Twilio dispatch.** Only the body-composition step is replaced. HMAC, Basic auth, rollback-on-failure, TEST_PHONE_OVERRIDE/RECIPIENTS logic stay untouched.

**Verify:**
- Linux smoke test (no Twilio needed): seed a fake job, call `render_template` with override absent → returns `DEFAULT_TEMPLATES["24h"].format_map(...)`; with override present in templates table → returns the override. Place in `tests/test_templates.py` (new) — pytest stays the project convention.
- Run full suite: `python3 -m pytest tests/ -q` — existing `test_queue_endpoint.py` must still pass unchanged (the `row_factory=sqlite3.Row` path and the full-`row` SELECT in `_fire_due_jobs` haven't altered queue/send semantics).

**Done:**
- `DEFAULT_TEMPLATES`, `SHOP_CONSTANTS`, `JOB_TYPE_META`, `render_template` live in app.py.
- `templates` table created on startup (idempotent).
- `_fire_due_jobs` and `/queue/send-now` both call `render_template`.
- Unknown placeholder → empty string (not `KeyError`, not `{literal}`).
- Existing pytest suite green.

---

## Task 2 — Server: GET /templates + PUT /templates/{job_type} endpoints

**Files:**
- `app.py`
- `tests/test_templates_endpoint.py` (new)

**Actions:**

1. **Add `GET /earlscheibconcord/templates`** in `do_GET` (near the existing `/queue` branch at line ~2040). Dual-auth via `_validate_auth(self, b"")`. Response shape (JSON):

   ```json
   {
     "job_types": [
       {
         "job_type":   "24h",
         "label":      "24-hour follow-up",
         "when":       "~24 hours after estimate",
         "body":       "…effective body (override if present, else default)…",
         "is_override": false,
         "updated_at": 0
       },
       …
     ],
     "placeholders": {
       "per_row": ["first_name", "name", "phone", "vin", "vehicle_desc", "ro_id", "doc_id", "email"],
       "shop":    ["shop_name", "shop_phone", "review_url"]
     },
     "sample_row": {
       "first_name": "Alex", "name": "Alex Martinez", "phone": "+15551234567",
       "vin": "1HGCM82633A004352", "vehicle_desc": "2018 Honda Accord",
       "ro_id": "RO-1234", "doc_id": "DOC-ABC-01", "email": "alex@example.com",
       "shop_name": "Earl Scheib Auto Body Concord",
       "shop_phone": "(925) 609-7780", "review_url": "https://g.page/r/review"
     }
   }
   ```

   **Sample row logic:** if the `jobs` table has at least one `sent=0` row, use the newest such row's columns (merged with `SHOP_CONSTANTS`) as the sample. Otherwise fall back to the hardcoded sample above. This gives Marco a realistic live preview when jobs exist.

   `is_override` = true iff a row exists in `templates` for that `job_type`. Order the `job_types` array by `JOB_TYPE_META` (not DB order).

2. **Add `PUT /earlscheibconcord/templates/{job_type}`** — `app.py` uses stdlib `http.server`, so route on `self.path.split("?")[0]` prefix in `do_PUT` (new method). Dual-auth via `_validate_auth(self, raw)` (body is HMAC-signed).

   - Path parse: `segs = path.split("/"); job_type = segs[-1]`. Validate `job_type` is one of `{m["job_type"] for m in JOB_TYPE_META}` → else 400 `{"error": "unknown job_type"}`.
   - Body: `{"body": "…"}`, max 2000 chars (enforce server-side). Missing / non-JSON → 400.
   - **Empty / whitespace-only body → DELETE from templates (revert to default).** Return `200 {"is_override": false, "body": DEFAULT_TEMPLATES[job_type], "updated_at": 0}`.
   - **Non-empty body → UPSERT** (`INSERT OR REPLACE INTO templates(job_type, body, updated_at) VALUES (?, ?, ?)`). Return `200 {"is_override": true, "body": <body>, "updated_at": <ts>}`.
   - Also validate the template is renderable by running `render_template` against `sample_row` inside a try/except; if `str.format_map` raises (malformed braces), return 400 `{"error": "template syntax error", "detail": "<exc>"}` — prevents Marco from saving a broken template that crashes a send.

3. **`do_PUT` shell:** app.py currently has `do_GET`, `do_POST`, `do_DELETE`. Add a `do_PUT` that handles only `/earlscheibconcord/templates/*` and returns 404 for everything else. Use `self._send_json` and the same `raw = self.rfile.read(content_length)` pattern used in `do_POST` / `do_DELETE`.

4. CORS: none needed — the UI is same-origin on both servers (Go admin serves /api/, app.py serves /earlscheibconcord/).

**Verify:**
- New `tests/test_templates_endpoint.py` covering: (a) GET with valid HMAC returns default bodies + all 3 job_types + placeholder catalog; (b) PUT with HMAC stores an override, subsequent GET shows `is_override: true`; (c) PUT with empty body deletes override, GET shows `is_override: false`; (d) PUT with unknown job_type → 400; (e) PUT with malformed template (`Hi {unclosed`) → 400; (f) both endpoints 401 without valid signature.
- Use the existing `conftest.py` `queue_server` fixture pattern (spin app.py on ephemeral port with test secret). Copy structure from `test_queue_endpoint.py`.
- Final: `python3 -m pytest tests/ -q` — all existing + new tests green.

**Done:**
- GET/PUT template endpoints live, dual-auth, bounded body, renderable-check on save.
- Empty PUT reverts to default.
- Full test coverage for happy path, auth failure, malformed template, unknown job_type.

---

## Task 3 — Go admin proxy: `/api/templates` + `/api/templates/{job_type}`

**Files:**
- `internal/admin/proxy.go`
- `internal/admin/server.go`
- `internal/admin/admin_test.go` (extend)

**Actions:**

1. **`internal/admin/server.go`:** Add two `remote*URL()` helpers mirroring `remoteQueueURL` / `remoteSendNowURL`:

   ```go
   func (s *server) remoteTemplatesURL() string {
       return strings.TrimRight(s.cfg.WebhookURL, "/") + "/templates"
   }
   func (s *server) remoteTemplateURL(jobType string) string {
       return s.remoteTemplatesURL() + "/" + jobType
   }
   ```

   Register routes in `Run`:
   ```go
   mux.HandleFunc("/api/templates",  s.handleTemplatesList) // GET only
   mux.HandleFunc("/api/templates/", s.handleTemplateUpsert) // PUT only (trailing slash = subtree)
   ```

2. **`internal/admin/proxy.go`:** Two new handlers — byte-for-byte modeled on existing code:

   - **`handleTemplatesList`** — mirrors `handleQueue` exactly (GET, HMAC-sign `[]byte("")`, 1 MiB body limit, forward status + JSON). No body parsing. 3 MiB limit is overkill — stick to the 1<<20 used in `handleQueue`.

   - **`handleTemplateUpsert`** — mirrors `handleSendNow`'s canonical-rewrite pattern but with HTTP method PUT and a different upstream URL:
     - `if r.Method != http.MethodPut: 405`.
     - Path parse: `jobType := strings.TrimPrefix(r.URL.Path, "/api/templates/")` — reject empty or containing `/` with 400.
     - Whitelist `jobType` against `{"24h", "3day", "review"}` (define as a const slice in proxy.go) to avoid being a free-range proxy.
     - Read raw body (limit 4 KiB — templates are ≤ 2000 chars). Parse as `struct { Body string \`json:"body"\` }`; marshal back to canonical JSON for signing (same pattern as handleCancel / handleSendNow — guarantees outbound bytes match what we signed).
     - Sign + PUT to `s.remoteTemplateURL(jobType)`. Forward status + body.

3. **`internal/admin/admin_test.go`:** Add two tests following the existing proxy-test pattern (there's already an `httptest.NewServer` fixture pointing `WebhookURL` at a local mock):
   - `TestAdminProxy_TemplatesList_Forwards` — spin mock upstream that validates the HMAC on empty body and returns a canned JSON; assert Go admin's `/api/templates` GET returns it verbatim.
   - `TestAdminProxy_TemplatePut_Forwards` — spin mock upstream that validates HMAC on the canonical `{"body":"..."}` bytes and returns `200 {"is_override": true}`; assert Go admin PUT `/api/templates/24h` forwards it.
   - `TestAdminProxy_TemplatePut_UnknownJobType` — PUT to `/api/templates/nope` → 400, **no upstream call** (check mock-hit counter is zero).

**Verify:**
- `make test` (covers `go test ./... -race`).
- Specifically: `go test ./internal/admin/ -run Template -v` prints all three new tests PASS.

**Done:**
- Go admin proxies `/api/templates` (GET) and `/api/templates/{job_type}` (PUT).
- HMAC signatures match server-side expectations (empty body for GET, canonical re-marshal for PUT).
- Unknown `job_type` rejected at proxy without upstream round-trip.

---

## Task 4 — UI: Templates page (shared, works on both Go admin and /earlscheib)

**Files (edit only `internal/admin/ui/*` — then `make sync-ui`):**
- `internal/admin/ui/index.html`
- `internal/admin/ui/main.js`
- `internal/admin/ui/main.css`
- Run `make sync-ui` (copies to `ui_public/`)

**Actions:**

1. **Topbar nav (`index.html`):** Inside `<header class="topbar">`, between `.brand` and `.stats`, add a compact tab/link group:

   ```html
   <nav class="topnav" role="tablist" aria-label="Views">
     <a class="topnav-link is-active" data-view="queue"     href="#queue">Queue</a>
     <a class="topnav-link"           data-view="templates" href="#templates">Templates</a>
   </nav>
   ```

   Work Sans uppercase, letter-spacing ≈ 0.08em, oxblood underline on `.is-active` — matches the filter-chip aesthetic already in `.filter`. **Do not** introduce new fonts, new colors, or new icon libs. Reuse `--oxblood`, `--cream`, `--ink` CSS vars.

2. **Two panels, one visible at a time.** Wrap the existing main + diagnostic + templates content in `data-view` sections so switching is CSS + `hidden` toggling:

   ```html
   <main id="view-queue" data-view="queue">  … existing queue + diagnostic … </main>
   <main id="view-templates" data-view="templates" hidden>
     <section class="templates">
       <header class="templates-header">
         <h1 class="templates-title">Message templates</h1>
         <p class="templates-sub">Edit the SMS copy Marco sends for each follow-up. Clearing a template restores the default.</p>
       </header>
       <div id="templates-list" class="templates-list" aria-live="polite"></div>
     </section>
   </main>
   ```

   Wrap the diagnostic `<section>` inside the queue view so it hides on the Templates tab.

3. **Card template** (use `<template>` like the existing estimate-card pattern):

   ```html
   <template id="template-card-template">
     <article class="tpl-card" data-job-type="">
       <header class="tpl-card__head">
         <h2 class="tpl-card__title"></h2>
         <span class="tpl-card__when"></span>
         <span class="tpl-card__badge" hidden>custom</span>
         <span class="tpl-card__dirty-dot" hidden aria-label="Unsaved changes"></span>
       </header>
       <p class="tpl-card__hint">Click a variable to insert it at the cursor.</p>
       <div class="tpl-card__chips" role="group" aria-label="Available variables"></div>
       <label class="tpl-card__label" for="">Message body</label>
       <textarea class="tpl-card__body" maxlength="2000" rows="4" spellcheck="true"></textarea>
       <div class="tpl-card__counter mono"><span class="tpl-card__count">0</span> / 2000</div>
       <figure class="tpl-card__preview">
         <figcaption>Preview (rendered against a real pending job if one exists)</figcaption>
         <div class="sms-bubble tpl-card__preview-body"></div>
       </figure>
       <footer class="tpl-card__actions">
         <button type="button" class="btn btn-primary tpl-save" disabled>Save</button>
         <button type="button" class="btn btn-ghost   tpl-reset"  disabled>Reset to default</button>
         <span class="tpl-card__status" role="status" aria-live="polite"></span>
       </footer>
     </article>
   </template>
   ```

4. **`main.js` behaviour:**

   - **Nav wiring:** clicking a `.topnav-link` toggles `hidden` on `#view-queue` / `#view-templates` and updates `.is-active`. On switch-to-templates, call `loadTemplates()` (idempotent — cache after first load, reload on manual refresh).
   - **URL paths (respect `API_BASE_PATH`):**
     ```js
     // Go admin:   GET  /api/templates              PUT  /api/templates/{jt}
     // Public UI:  GET  /earlscheibconcord/templates PUT /earlscheibconcord/templates/{jt}
     const tplListURL     = IS_LOCAL_ADMIN ? '/api/templates' : `${API_BASE}/templates`;
     const tplUpsertURL   = (jt) => IS_LOCAL_ADMIN
       ? `/api/templates/${encodeURIComponent(jt)}`
       : `${API_BASE}/templates/${encodeURIComponent(jt)}`;
     ```
   - **`loadTemplates()`**: `fetch(tplListURL)` → on 200, build one card per `job_types[]` entry. Store the server response in `templateState = { bySavedBody: {}, sample: {…}, placeholders: {…} }` for reset/preview logic.
   - **Placeholder chips:** render one `<button class="tpl-chip">{name}</button>` per entry in `placeholders.per_row.concat(placeholders.shop)`. Clicking inserts `{name}` at the textarea's current `selectionStart` — do NOT append; must respect cursor position. After insert, re-focus the textarea and dispatch a fake `input` event so the preview updates.
   - **Live preview:** attach an `input` listener to the textarea with the same 150 ms debounce constant already defined (`SEARCH_DEBOUNCE_MS`). Preview renders client-side using the same missing-key semantics as the server: substitute `{key}` with `sample[key]` if present, else empty string. **Use a regex, not a templating lib:** `body.replace(/\{(\w+)\}/g, (m, k) => sample[k] ?? '');`
   - **Dirty tracking:** compare textarea value to `templateState.bySavedBody[jobType]`; when different, show `.tpl-card__dirty-dot`, enable Save. Reset button is enabled whenever `is_override === true` (regardless of dirty) so Marco can revert after saving.
   - **Save:** PUT `{body: textarea.value}`. On 200, update `bySavedBody[jobType]`, update badge (`custom` visible iff `is_override`), clear dirty dot, flash `.tpl-card__status` "Saved · just now" (auto-clears after 3 s). On non-200, show error text in `.tpl-card__status` with `data-state="error"`.
   - **Reset to default:** PUT with empty body (`{body: ""}`). On 200, reset textarea to server's returned default body, hide badge, clear dirty dot.
   - **Guard:** before fetch on Save, run the client-side preview render and abort with an inline error if the body contains an unclosed `{` (simple: count `{` === count `}` and no `{` before a non-matching `}`). The server validates authoritatively; this is just instant feedback.

5. **`main.css`:** Add the `.topnav*`, `.templates*`, `.tpl-card*`, `.tpl-chip*` selectors. **Palette-locked:**
   - Card background: `var(--cream-soft)` (same as existing estimate-card).
   - Chips: Work Sans uppercase 10.5 px, letter-spacing 0.08em, 1px oxblood border on hover, click-state flash 120 ms.
   - Dirty dot: 6 px amber circle.
   - Preview bubble: reuse existing `.sms-bubble` class — do not re-style.
   - No gradients. No shadows beyond the existing card's shadow token. No new Google Font requests.

6. **`make sync-ui`** at the end — the three UI files must be byte-identical in `ui_public/`. If you skip this step, the public `/earlscheib` page will be missing the Templates tab.

**Verify:**
- `make test` stays green (Go side — the `go:embed ui` must still compile with the new HTML).
- Manual / headless: boot app.py with `ADMIN_UI_USER=test ADMIN_UI_PASSWORD=test CCC_SECRET=… python3 app.py`, curl:
  - `curl -s -u test:test http://localhost:8200/earlscheib/ | grep -q 'data-view="templates"'` → exit 0.
  - `curl -s -u test:test http://localhost:8200/earlscheibconcord/templates | python3 -m json.tool` → shows 3 job_types + placeholders + sample_row.
  - `curl -s -u test:test -X PUT -H 'Content-Type: application/json' -d '{"body":"Test {first_name} from {shop_name}"}' http://localhost:8200/earlscheibconcord/templates/24h` → 200 with `is_override: true`.
- For the Go admin side: `CGO_ENABLED=0 EARLSCHEIB_DATA_DIR=/tmp/wmh go run ./cmd/earlscheib --admin` in a second terminal, open the URL stdout'd, click **Templates**, verify the cards render and preview updates on keystroke.
- Diff `internal/admin/ui/*` vs `ui_public/*` — must be identical: `diff -q internal/admin/ui/index.html ui_public/index.html` (× 3 files) all silent.

**Done:**
- Templates tab visible on both UIs, one card per job_type with chips, textarea, live preview, Save/Reset.
- Dirty dot appears on edit, clears on save.
- Empty save reverts to default.
- `ui_public/` byte-identical to `internal/admin/ui/`.
- Aesthetic stays within OH4 palette — no new colors, no new fonts.

---

## Overall verification (end of task)

1. `python3 -m pytest tests/ -q` — all pass, including new `test_templates.py` (render helper) and `test_templates_endpoint.py` (GET/PUT).
2. `make test` (Go race-suite) — all pass, including new proxy tests.
3. `diff -rq internal/admin/ui ui_public | grep -v README.md` — no diffs (sync-ui ran).
4. Manual smoke: edit 24h template via both UIs, confirm the edit from one is visible in the other (same server-side source of truth). Clear the textarea + Save → reverts to default.
5. End-to-end: with `IMMEDIATE_SEND_FOR_TESTING=1` and `TEST_PHONE_RECIPIENTS=<your-number>`, POST a test BMS payload → within 60 s receive an SMS containing the **customized** template body. Revert the template → next scheduled send uses the default. This is the only spec that matters.

## Out-of-scope for this quick task

- Template versioning / audit log.
- Per-shop multi-tenancy (this project is single-shop).
- Rich-text / emoji picker.
- Separate "test send" button on the Templates page — Marco already has "Send now" on the Queue page for live verification.
- Nav item for Diagnostic (stays inside the Queue view as today).
