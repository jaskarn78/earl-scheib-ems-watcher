# UI design pass: decisions beyond the spec (Builder B, `ui_public/messages.html`)

One line per decision: what, why, where to look. Screenshots: `docs/superpowers/shots/after/messages-*.png`.

- Both panes are `--surface` white with a hairline divider (not canvas + cards): a full-height inbox has no cards, and the spec's "selected row = `--surface-2`" only reads on white. See `messages-desktop-list.png`.
- Thread rows are full-bleed with a hairline separator inset past the avatar (no rounded row cards); the selected accent is `box-shadow: inset 3px 0 0 var(--navy)` so it stays a straight bar. See `messages-desktop-conversation.png`.
- Avatars are 40px circles with per-customer tints from a 4-entry navy-family palette (`AVATAR_COLORS` is now `[background, text]`), so adjacent rows still differ but no avatar can be mistaken for a green/amber/red status. Inline `style` still carries the colour, as before.
- The unread count badge became the spec's 8px red dot (`.badge` kept as the class); the count stays in the row's `aria-label` and in the Unread chip's `.num`.
- The "Messages" title row now also shows on the phone (22px) instead of being hidden; the SPA views show their titles on mobile, and the count is useful there. See `messages-mobile-list.png`.
- Pane collapse to the push layout stays at 880px (two panes are cramped between 720 and 880); the shared bottom nav still switches at 720px via nav.css.
- `#chCtx` is hidden on phones with `display:none !important` because the JS sets `display:inline-flex` inline (class-level display vs inline style trap, same family as the `[hidden]` one in spec section 3).
- Both panes get `min-width:0` and the grid uses `minmax(0,1fr)`: the single-line nowrap snippet otherwise widened the list pane to 1083px on a 390px phone (verified with `scrollWidth`).
- Error callout is static markup (icon well, title "Can't reach the messages server", text "Retrying every 15 seconds. Texts customers send in the meantime are not lost."); the JS only toggles `hidden` and logs the HTTP detail with `console.warn`. The "Retrying every 15 seconds" matches `POLL_MS`. See `messages-*-error.png`.
- Delivery-failure ticks no longer print the Twilio error code ("Not delivered (#30007)" -> "Not delivered"); the code is kept as `data-error-code` on the span for debugging. See `messages-*-failed.png`.
- List empty states use the shared icon-well/title/sentence shape via `innerHTML` (was a bare sentence): "No conversations yet / Customer texts show up here as they arrive.", "All caught up / No unread replies right now.", "Nothing needs attention / Every text went through.", "No matches / Try a different name, phone number, or word." The empty state fills the remaining list height (`.thread-list:empty{display:none}`).
- Header meta copy "not in jobs" -> "not on file" for unknown numbers.
- Send button is icon-only (arrow up, `aria-label="Send reply"` kept); disabled fill is `--hairline-strong` with a white arrow per 6.8 ("disabled grey"), not the 45% opacity rule, because the two rules conflict and 6.8 is the more specific.
- Bubble tails are drawn with `::before/::after` on the last bubble of a group (iMessage technique); they rely on the messages pane being solid `--surface`. Failed bubbles (red-soft fill + red inset border) have no tail.
- Line status pill ("Texting line active" / "Line offline") kept as a green-soft/red-soft pill; the spec does not mention it but the JS drives it and it is useful.
- Composer helper keeps today's fact ("Sends as a text from your shop line"); the shop number is not asserted anywhere in the page today, so it was not added.
- Timeline drawer: focus moves to `#tl-name` (now `tabindex="-1"`) on open, per spec 4.3. The drawer toggles via `hidden`, so there is no 220ms open animation without JS changes; left as is.
- Mobile header controls are 40px with a transparent `::after` inset -4px for a 48px effective touch target, so the Timeline button and the call button are the same height.

## nav.css notes for Builder A (not edited here)

- Nothing blocking. `messages.html` defines `--navy`, `--red`, `--amber` with the spec values so the nav.css fallbacks agree.
- The topbar `box-shadow: 0 2px 8px rgba(26,45,77,.3)` reads heavy on the white inbox panes; `var(--shadow-2)` would match the spec tokens.
