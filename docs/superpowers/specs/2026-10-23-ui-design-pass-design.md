# Earl Scheib admin app: UI/UX design pass (2026-10-23)

Branch `feat/ui-design-pass`, worktree `/home/clawcode/projects/earl-scheib-ems-watcher-redesign`.
Owner: Jas (jaskarn78). Requested 2026-10-23 05:48 PT: "an entire ui/ix design pass on the
entire site. Modern, clean, max user experience. Extreme attention to detail. Show me the
redesign before we publish." Plus: the Google Calendar connect must be an officially styled
Google button.

## 1. Intent

Marco (owner of Earl Scheib Auto Body, Concord CA) runs this app on his iPhone and at the
shop desk to keep customer follow-up texts flowing, book work, read replies and see what the
texting is earning. Every screen should read as one product: same type, same buttons, same
chips, same cards, same words for the same thing. Nothing should look like a developer tool.

Brand stays: Earl Scheib navy and red. The red is the single call-to-action colour and is
spent on one primary action per region. Everything else is quiet.

## 2. Goals / non-goals / anti-goals

Goals
- One coherent design system (tokens + components) applied to every view: Queue (Estimates,
  Work Completed, Scheduled, Sent, Cancelled, All), the appointment calendar, the Google
  Calendar banner/panel, Templates, Schedules, Logs, Insights, the customer timeline drawer,
  the Diagnostic panel, and the Messages inbox (`messages.html`).
- Clear action hierarchy on every card: one primary, one secondary, the rest tertiary.
- Plain-English copy everywhere a person reads it (labels, empty states, errors, confirms).
- Officially styled "Continue with Google" button for the calendar connect.
- Flawless on a 390px iPhone (bottom nav, thumb reach, no horizontal scroll) and on a
  1280px desktop.

Non-goals
- No new features, no API changes, no schema changes. Behaviour stays exactly as it is.
- No framework, no build step, no bundler. Vanilla HTML/CSS/JS served by app.py as today.
- No new runtime dependency. The only allowed addition is one Google Fonts `<link>` for
  Roboto (the Google button) alongside the existing Inter link.

Anti-goals (this is a failure even if it "works")
- Anything that looks like a template or a generic admin theme.
- Breaking the JS: every `id`, `data-*` attribute and class that `main.js` / inline JS
  queries must keep working. Rename nothing the JS reads unless you update the JS too.
- Decoration that encodes nothing (random gradients, numbered markers, icon soup).
- Low-contrast text, buttons that look disabled when they are not, hidden affordances.

## 3. Constraints

- Served by `app.py` (stdlib HTTP server). HTML gets `<!--SHARED_NAV_HEADER-->` replaced by
  `NAV_HEADER` (see `app.py` around line 3891) and asset URLs get `?v=<mtime>`; cache is
  `no-cache`. If you change the topbar markup, change `NAV_HEADER` in `app.py` AND keep the
  class names `nav.css` uses (`.topbar`, `.brand*`, `.topnav*`, `.stats*`, `.sync*`,
  `.refresh-btn`, `.bottomnav*`, `.bn-link`).
- `IS_LOCAL_ADMIN` paths in `main.js` (Marco's local Go binary) must keep working; do not
  remove hidden-state handling.
- The class-level `display` vs `[hidden]` trap: any element toggled with `hidden` needs an
  explicit `.foo[hidden] { display: none; }` rule when its class sets `display`.
- Tests: `python3 -m pytest -q` must stay at 108 passed / the same 12 pre-existing failures
  (`tests/test_templates_endpoint.py` and queue-projection tests; do not touch them).
- Keep comments that explain behaviour (BOK-01, CAL-04, etc.). Trim pure history only if it
  is wrong after your change.

## 4. Design system

### 4.1 Colour tokens (replace the `:root` in `main.css`; mirror in `messages.html` `:root`
and `nav.css` aliases so all three agree)

| token | value | use |
|---|---|---|
| `--canvas` | `#F5F6F8` | page background (cool, crisp; replaces the warm cream) |
| `--surface` | `#FFFFFF` | cards, panels, inputs |
| `--surface-2` | `#EEF1F5` | subtle wells: chips idle, code, calendar day cells |
| `--hairline` | `#E2E6EC` | 1px borders |
| `--hairline-strong` | `#C9D0DA` | input borders, hover borders |
| `--ink` | `#14213B` | primary text (brand navy, slightly deeper for contrast) |
| `--ink-2` | `#56657C` | secondary text |
| `--ink-3` | `#8592A6` | placeholders, meta |
| `--navy` | `#1A2D4D` | topbar, selected chip fill, secondary buttons |
| `--navy-hover` | `#243D66` | |
| `--red` | `#C8202F` | primary CTA fill, destructive text, active nav underline |
| `--red-hover` | `#A81828` | |
| `--red-soft` | `#FBE9EB` | error well background |
| `--green` | `#1F7A52` | synced / delivered / booked |
| `--green-soft` | `#E6F4EC` | |
| `--amber` | `#B7791F` | paused / pending attention / unsaved |
| `--amber-soft` | `#FBF1DC` | |
| `--blue` | `#2B5DB8` | informational (inbound messages, links) |
| `--blue-soft` | `#E8EFFB` | |
| `--focus` | `#2B5DB8` | focus ring colour (2px ring + 4px soft halo) |

Status colours always come as a pair (solid + soft) and are used the same way everywhere:
soft fill + solid text for pills; solid 3px left accent on cards/rows that carry a state.

Shadows: `--shadow-1: 0 1px 2px rgba(20,33,59,.06)`; `--shadow-2: 0 4px 14px rgba(20,33,59,.08)`;
`--shadow-3: 0 16px 40px rgba(20,33,59,.14)` (drawers, popovers only).
Radii: `--r-sm: 8px` (inputs, chips-square), `--r-md: 12px` (cards, buttons 10px),
`--r-lg: 16px` (drawers, large panels), `--r-pill: 999px`.
Spacing scale: 4, 8, 12, 16, 20, 24, 32, 40, 56 px. Page gutter `clamp(16px, 3vw, 32px)`;
content max width 1120px.

### 4.2 Type

Inter stays for everything (already loaded; weights 400/500/600/700). Add Roboto 500 only
for the Google button. Remove Fraunces and any serif fallbacks (`messages.html`, Logs title).
No uppercase letter-spaced labels except the tiny eyebrow style below.

| role | size / line / weight / tracking |
|---|---|
| Page title (h1) | 26px / 1.2 / 700 / -0.02em (mobile 22px) |
| Section title (h2 in cards) | 16px / 1.3 / 700 / -0.01em |
| Card title (customer name) | 20px / 1.25 / 700 / -0.015em |
| Body | 15px / 1.5 / 400 (mobile 15px; never below 14px for readable text) |
| Secondary | 13px / 1.45 / 400, `--ink-2` |
| Eyebrow | 11px / 1 / 600 / 0.06em uppercase, `--ink-3` (only above a title or a stat) |
| Mono (phone, VIN, RO, times) | `ui-monospace, "SF Mono", Menlo, monospace` 13px, tabular numbers |
| Stat number | 28px / 1 / 700 / -0.02em, tabular numbers |

### 4.3 Components (one implementation each, used everywhere)

Buttons: height 40px (compact 34px inside cards/rows), radius 10px, Inter 600 14px, icon
16px with 8px gap, min hit area 44px on touch.
- `.btn-primary`: red fill, white text. ONE per region. Hover darker, pressed `translateY(1px)`.
- `.btn-secondary`: white fill, 1px `--hairline-strong` border, navy text. Hover border navy.
- `.btn-tertiary` (replaces most `.btn-ghost`): no border, navy-ink text, hover `--surface-2`.
  Keep `.btn-ghost` as an alias so existing JS/markup still works.
- `.btn-danger-text`: tertiary with red text (Cancel follow-ups, Unmark, Disconnect, Delete).
- Disabled: 45% opacity, `cursor: not-allowed`, never a different hue (today's pink Save).
- Loading: optional 16px spinner replaces the icon; label stays.

Chips / filters (`.filter`, `.chip`, `.logs-filter`): one style. Pill, 34px, 13px/600
sentence case ("Work completed", not "WORK COMPLETED"), idle = `--surface-2` fill + `--ink-2`,
hover = `--hairline-strong` border, selected = navy fill + white. Count badges inside chips
allowed (`.num`).

Inputs / search / textarea / select: 40px, white, 1px `--hairline-strong`, radius 8px, 15px
text, placeholder `--ink-3`. Focus: 2px `--focus` ring + 4px halo `rgba(43,93,184,.18)`.
Search field keeps its icon. Number inputs get `inputmode="numeric"`.

Toggle (`.switch`, reuse the CAL-05 one; restyle the Schedules "ENABLED" toggle to it):
44x24 track, 20px knob, on = green (not red; red means "act", green means "on"). Label to
the right in sentence case: "On" / "Off" or the specific state text.

Cards: white, 1px `--hairline`, radius 12px, `--shadow-1`, padding 20px (16px mobile).
Card header row: title left, meta/status right. A card that carries a state gets a 3px left
accent in the status colour (synced green, paused amber, failed red, inbound blue).

Status pill (`.pill`): soft fill + solid text, 12px/600, 24px tall, 8px radius, optional
8px dot. States: Booked (green), Pending (navy-soft), Sent (green), Delivered (green),
Failed / Undelivered (red), Cancelled (neutral), Inbound (blue), Paused (amber), Unsaved
(amber).

Timeline (queue card): keep the vertical line; dots 10px; pending = hollow navy ring, sent =
green filled, cancelled = neutral, failed = red. The message preview bubble: `--surface-2`
fill, 12px radius, no coloured left border; sender eyebrow "Customer receives" stays.

Callout (`.callout`): white card with 3px left accent, icon well 36px, title 15px/700,
text 13px `--ink-2`, actions right, dismiss x top-right. Used for the Google Calendar
connect banner (blue accent), the auto-send-off notice if ever shown, and errors (red).

Empty state: 44px icon well (`--surface-2`), 16px/700 title, one 13px sentence that tells
them what to do, optional button. Error state: same shape, red icon well, plain sentence,
"Try again" button; never show HTTP codes or internal identifiers to Marco (log them to the
console instead).

Drawer (`#tl-drawer`): right sheet on desktop (420px, `--shadow-3`, radius 16px on the
left corners), bottom sheet on mobile (max 85vh, drag handle), close x 40px, Escape closes,
focus moves to the drawer title on open.

Motion: 140-180ms ease-out for hover/press/toggle; drawers 220ms; honour
`prefers-reduced-motion: reduce` (no transforms, instant toggles). Nothing animates on
scroll. Nothing bounces.

Focus: every interactive element shows the focus ring on `:focus-visible`.

### 4.4 Copy rules

Sentence case everywhere. Buttons say what happens: "Send now", "Mark scheduled",
"Save appointment", "Save changes", "Reset to default", "Connect", "Turn off auto-sync".
Confirms say the consequence: "Cancel all 2 follow-ups for Bob? Their review request after
the work still goes out." Errors say what happened and what to do, in the app's voice, no
apology, no codes. Times: "Today 2:30 PM", "Tue, Oct 27 · 10:30 AM"; relative only for
"last check 2 min ago" style meta. Never show `{placeholders}` or snake_case to Marco
except in the Templates editor where they are the feature (there, render variable chips
with a monospace label and a human tooltip, e.g. `{first_name}` → "Customer's first name").

## 5. Google button (official spec, light theme)

Replace the red "Connect Google Calendar" CTA in the banner and in the Scheduled panel with
an official-style Google button. Markup (keep `id="gcal-banner-connect"` and
`id="cal-gcal-connect"` on the `<a>`):

```html
<a class="gsi-btn" id="gcal-banner-connect" href="...">
  <span class="gsi-btn__logo" aria-hidden="true"><!-- 20x20 Google "G" SVG, official 4 colours --></span>
  <span class="gsi-btn__label">Continue with Google</span>
</a>
```

Style per Google's branding guidelines (gsi-material-button, light):
- height 40px, padding 0 12px, border 1px solid `#747775`, border-radius 20px (pill) in
  the banner and 20px in the panel (same everywhere), background `#FFFFFF`;
- font: `"Roboto", "Inter", arial, sans-serif` 500, 14px, letter-spacing 0.25px, colour
  `#1F1F1F`; label never wraps;
- logo: 20x20 official G with 12px right margin (`<svg viewBox="0 0 48 48">` with the four
  official paths: `#EA4335`, `#4285F4`, `#FBBC05`, `#34A853`);
- hover: state layer `#303030` at 8% opacity (overlay, not a border change); focus: 2px
  focus ring outside; pressed 12%; disabled 38% opacity;
- allowed labels only: "Continue with Google" (use this one). No red, no navy, no
  recolouring of the G.
Add `<link href="https://fonts.googleapis.com/css2?family=Roboto:wght@500&display=swap">`
next to the Inter link in `index.html`. Do not load Roboto in `messages.html`.

## 6. Per-view requirements

### 6.1 Topbar + navigation (`NAV_HEADER` in app.py, `nav.css`)
- Navy bar stays, 64px desktop. Brand logo left. Nav links: Inter 600 14px sentence case
  ("Queue", "Messages", "Templates", "Schedules", "Logs", "Insights"), active = white text +
  3px red underline; hover = white at 80%.
- Stats cluster: a quiet segmented strip with three items, each a number + label
  ("7 pending", "0 sent today", "0 failed"); failed > 0 turns that item's number red.
- Sync: dot + "Updated 4 s ago" + refresh icon button (36px, white 10% fill, hover 18%).
- Mobile (≤720px): topbar shows logo + sync only; bottom nav 6 items, 24px icons, 11px
  labels, active = red icon + label, safe-area padding kept. Add
  `aria-current="page"` to the active link.

### 6.2 Queue (Estimates / Work completed / Scheduled / Sent / Cancelled / All)
- Controls row: chips left, Sort + Search right on desktop; on mobile chips scroll
  horizontally with fade edges, search full width below, sort as a compact select.
- Estimate card: name 20px; phone (mono) and email as a row with icons; vehicle line:
  description 15px/600, VIN and RO as small `--surface-2` tags; date as meta right of the
  header on desktop, under on mobile.
- Booking row hierarchy: Booked pill (green, with calendar icon, mono time) or
  "Mark scheduled" (`.btn-secondary`); "Change date" tertiary when booked; "Add to Google
  Calendar" tertiary with external-link icon; "Cancel follow-ups" and "Unmark" danger-text
  and pushed to the right. On mobile the row wraps to two lines, primary actions first.
- Timeline entries: chip for job type (`24-hour follow-up`, `3-day check-in`, `Review
  request`, `Appointment reminder`; sentence case), schedule text ("Sends Tue 8:00 AM" /
  "Sent Oct 21, 9:12 AM" / "Cancelled · customer booked"), preview bubble, actions:
  "Send now" primary (only one primary visible per card: if several entries are pending
  the first pending gets primary and the rest secondary), "Cancel" danger-text, "Resend"
  secondary.
- Empty states per chip, e.g. Estimates: "No open estimates. New estimates from CCC show up
  here within a minute." Scheduled: existing copy, polished.
- Date picker (`.dtp`): white card, month grid with 36px cells, selected = navy fill,
  today = 2px navy ring, slots as chips, footer "Save appointment" primary + "Back" tertiary.

### 6.3 Scheduled view extras
- Appointment calendar: weekday row eyebrow style; day cells `--surface-2`, 10px radius,
  today = 2px navy ring (not red); appointment pills green-soft with mono time, clickable
  (existing popup CAL-03 restyled as a popover card with the customer details and the
  existing buttons).
- Google Calendar sync panel (`#cal-gcal`, `.gsync`): a card with left accent (green on,
  amber paused, red problem). Not connected: Google button (section 5) + one sentence.
  Connected: account line, "3 bookings synced · checked just now", toggle labelled
  "Auto-sync" with state text, "Sync now" secondary compact, "Disconnect" danger-text.
- Connect banner (`#gcal-banner`): becomes a `.callout` directly under the topbar, full
  content width, blue accent, calendar icon, title "Put booked appointments on your Google
  Calendar", text "One-time sign-in. Every booking you mark here lands on your calendar
  automatically.", Google button right (full width on mobile), dismiss x with
  `aria-label="Hide for now"`. Keep all existing ids and the snooze behaviour.

### 6.4 Templates
- Header: title "Message templates" + one helper sentence. Cards per template with
  left-accent amber only while unsaved (replace the mystery amber dot with an "Unsaved
  changes" pill next to the title and a visible, enabled Save). Save is primary and
  enabled only when dirty; "Reset to default" tertiary.
- Variable chips: monospace label, tooltip (`title`) in plain English, wrap in rows, 32px.
- Message body textarea 15px, min 4 rows, auto-grow; character counter right-aligned
  "164 / 2000" in `--ink-3`, turns amber at 1500 and red at 2000 (same thresholds as today).
- Preview: an iMessage-style bubble (navy-soft fill `--surface-2`, 16px radius with a
  small tail) under the eyebrow "Preview · sent to Carla's 2021 Civic" (use the sample row's
  name if present, otherwise "Preview").

### 6.5 Schedules
- Same card family as Templates. Toggle component from 4.3 (green when on), label "On" /
  "Off". Delay row: number input 88px + unit "hours" + helper "(1 day)"; helper updates
  live as today. Save primary enabled when dirty; "Reset to default" tertiary.

### 6.6 Logs
- Title "Message log" (the nav item stays "Logs"); helper sentence shorter: "Every text
  in and out, straight from Twilio, with carrier delivery status."
- Filter groups as labelled chip rows (eyebrow label + chips), collapse to one scrolling
  row per group on mobile. Freshness text right.
- Rows: a table on desktop (time, direction icon, customer, body preview, status pill),
  stacked cards on mobile. Inbound rows get the blue left accent. Error state in plain
  English: "Couldn't load the log from Twilio. Retrying in 15 seconds." (never
  `twilio_creds_missing`).

### 6.7 Insights
- Toolbar: period chips, "Refresh" tertiary, sync meta right.
- Board first: "Replied, not booked" and "This week" as two columns on desktop. Each person
  row: name (button, underline on hover only), vehicle + phone meta, timestamp right, chevron.
- "This period" tiles: 4-up grid on desktop / 2-up mobile; number 28px tabular, label
  eyebrow style under it, unit prefix inside the number ($). Zero states still show "0",
  dash only for the median reply.
- Follow-ups funnel: horizontal bars, 8px tall, navy for estimates and lighter steps for
  each stage, value right in mono; stage labels sentence case.
- Revenue chart: columns with month eyebrows; empty: "No closed repair orders yet." inside
  the empty-state shape.
- Footer line stays, 13px `--ink-3`.

### 6.8 Messages inbox (`messages.html`, self-contained)
- Adopt the tokens from 4.1 (replace its `:root`), Inter only (drop Fraunces), same chips,
  buttons, pills, empty/error shapes as the SPA. Keep every id and class the inline JS uses.
- List pane: title "Messages" 26px/700, count meta right, search 40px, chips (All / Unread
  with red dot + count / Needs attention). Thread rows: avatar 40px (initials, navy-soft),
  name 15px/600, last message 13px `--ink-2` single line ellipsis, time right, unread = bold
  name + 8px red dot. Selected row = `--surface-2` with 3px navy left accent.
- Conversation pane: header with avatar, name, phone (mono), context chip, "Timeline"
  secondary compact, call icon button. Messages: outbound = navy fill, white text, 16px
  radius with a small tail bottom-right; inbound = `--surface-2`, ink text, tail bottom-left;
  timestamps 11px `--ink-3` under groups; day separators as eyebrow pills centred.
- Composer: 44px min, textarea auto-grow, send = 40px round red button with arrow icon,
  disabled grey when empty; helper line under ("Sends from (925) 603-3934 · texts are
  forwarded to your cell too" if that is what the footer says today; keep the existing
  facts, only the wording may change).
- Error banner → `.callout` red: "Can't reach the messages server. Retrying…".
- Mobile: list → conversation push with the existing back button, 100dvh layout,
  composer above the home indicator.

### 6.9 Diagnostic panel (bottom of Queue)
- Collapsed by default behind a "System details" disclosure (`<details>`), tertiary style;
  inside, a 2-column definition grid, mono values. Nothing else changes.

## 7. Mobile rules (≤720px)
- No horizontal scrollbars anywhere (verify `document.documentElement.scrollWidth <= innerWidth`).
- 44px minimum touch targets; 16px side gutters; bottom nav safe-area padding kept.
- Cards full-bleed with 12px radius inside 16px gutters; page title 22px.
- Chip rows scroll horizontally with `scroll-snap-type: x proximity` and faded edges.

## 8. Accessibility
- Colour contrast AA for text (ink on canvas/surface, white on navy/red all pass; `--ink-3`
  is for meta only, never for body copy).
- `:focus-visible` ring on all controls; skip-to-content link at the top of `index.html`.
- `aria-live="polite"` regions stay where they are. Toggles are real checkboxes.
- `prefers-reduced-motion` honoured.

## 9. Verification (both builders)

Local rig (fake Google, seeded bookings), per worktree:
```
ESW_REPO=/home/clawcode/projects/earl-scheib-ems-watcher-redesign ESW_RIG_DIR=/tmp/esw-ui-redesign ESW_PORT=8311 \
  python3 /tmp/esw-ui/run.py connected
```
Run it in the foreground of a background shell (a detached/daemonised process gets reaped
on this box). Then `curl -s -X POST http://127.0.0.1:8311/earlscheibconcord/gcal/sync` once.
Screenshots of every view at 1280x800 and 390x844:
```
ESW_BASE=http://127.0.0.1:8311 python3 /tmp/esw-ui/shoot.py docs/superpowers/shots/after
```
(`/tmp/esw-ui/shoot.py` uses Python Playwright, already installed.) Look at every image.
Fix what looks wrong. Repeat until it is right. Also check: `node --check ui_public/main.js`,
`python3 -m py_compile app.py`, `python3 -m pytest -q` (108 passed, 12 pre-existing fails),
and that these ids still exist and work: `gcal-banner`, `gcal-banner-connect`,
`gcal-banner-close`, `cal-gcal`, `cal-gcal-text`, `cal-gcal-connect`, `cal-gcal-toggle`,
`cal-gcal-toggle-wrap`, `cal-gcal-toggle-label`, `cal-gcal-sync`, `cal-gcal-disconnect`,
`sched-calendar`, `cal-grid`, `queue`, `search`, `sort-by`, `templates-list`,
`schedules-list`, `logs-list`, `view-*`, `tl-drawer`, `auto-send-toggle`, `diagnostic`,
and on the inbox page every id in `messages.html`'s script.

Commit on the branch in small commits with clear messages. Do not push. Do not touch the
main worktree at `/home/clawcode/projects/earl-scheib-ems-watcher`.

## 10. Division of work

- Builder A owns `ui_public/index.html`, `ui_public/main.css`, `ui_public/nav.css`,
  `ui_public/main.js`, and the `NAV_HEADER` string in `app.py` (nothing else in app.py).
- Builder B owns `ui_public/messages.html` only (reads `nav.css` for the shared topbar; if
  it needs a nav.css change, write it down in its notes instead of editing).
- Decisions each builder makes beyond this document go in
  `docs/superpowers/specs/2026-10-23-ui-design-pass-decisions.md` (A) /
  `...-decisions-messages.md` (B): one line per decision, why, and what to look at.
