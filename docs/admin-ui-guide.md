# Earl Scheib Queue Admin UI — Guide for Marco

The **Queue Admin UI** is a small built-in viewer that shows every text message
waiting to go out to a customer, and lets you cancel one before it sends.

It opens in your normal web browser. You do not need the internet for the window
itself — the window is served from `earlscheib.exe` running on your own PC. The
window does talk to the Earl Scheib support server (over HTTPS) to fetch the
list of queued messages, so the PC does need to be online.

## Opening the window

1. Double-click the **Earl Scheib Queue** shortcut on the desktop (or Start menu).
   If there is no shortcut yet, open a Command Prompt and run:

   ```
   "C:\EarlScheibWatcher\earlscheib.exe" --admin
   ```

2. A small black console window will appear and print a line like:

   ```
   admin UI: http://127.0.0.1:54321
   ```

3. Your default web browser (Edge or Chrome) will automatically open to that
   address and show the queue.

4. **Leave the black console window open while you use the queue.** Closing it
   will close the queue view. The console will close itself about 30 seconds
   after you close the browser tab.

## What you see

- **Cream page with a dark red "Earl Scheib Concord — Queue" header.**
- Each customer with pending messages gets a card. The customer's name is at
  the top of the card in deep red; the phone number is on the right.
- Each pending message lists: the send time (Pacific time, e.g. "Tue 2:30 PM"),
  the kind of message (24-hour follow-up, 3-day follow-up, or review request),
  and the repair job reference.
- If nothing is queued, the page says "*Nothing queued right now.*"

## Cancelling a message

1. Click the **cancel** link on the row you want to stop.
2. The row will dim and a small orange **"cancelled — click to undo"** pill
   appears on the right.
3. You have **5 seconds** to change your mind: click the pill to undo. The
   message stays queued.
4. If you do nothing for 5 seconds, the message is permanently cancelled — it
   will not go out.

This is the only action the page supports. You cannot edit, reschedule, or
re-send a message from here. If a message went out to the wrong person,
contact your app administrator.

## Refreshing the list

- The page refreshes itself every 15 seconds.
- Press **R** on your keyboard (with no text field selected) or click the small
  refresh arrow in the top-right to refresh immediately.

## Closing the window

Close the browser tab. About 30 seconds later, the black console window
will close itself. Nothing you were doing stops — the follow-up texts continue
to send from the Scheduled Task in the background, exactly as before.

## Troubleshooting

| If you see… | Do this |
|---|---|
| **"cannot reach local admin"** | The console window closed. Reopen `earlscheib.exe --admin`. |
| **"queue fetch failed (401)"** | Call App Support — the signing key is out of sync. |
| **An empty page / "Nothing queued right now."** | Nothing is scheduled. This is normal overnight and on weekends. |
| **The window never opens** | Double-click the URL printed in the console window to open it manually. |

## What this window **cannot** do

- It cannot show messages that already went out.
- It cannot change a customer's phone number or name.
- It cannot pause the system. Closing the window does not stop follow-ups
  from sending — they send on the Scheduled Task regardless.

If you need any of those, contact App Support (see the main support page).
