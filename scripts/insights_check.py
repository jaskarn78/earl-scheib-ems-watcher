#!/usr/bin/env python3
"""Run compute_insights against a copy of the production DB and print the audit window.

usage: DB_PATH=/path/to/jobs.db CCC_EXPORTS_DIR=/path/to/ccc-exports \
           python3 scripts/insights_check.py [--twilio-json PATH]

Expected for 2026-05-13..2026-09-03 (audit): Estimates 154 · Texted 95 ·
Replied 42 · Won after text 10 · Closed RO 7 = $17,650 · shop closed 31 = $86,390

--twilio-json loads a saved Twilio Messages dump (a JSON list of message dicts)
straight into inbound_sms, so the Replied numbers are real on a machine that has
no Twilio credentials. On the Pi, drop the flag and let sync_inbound_sms() run.
"""
import os, sys, sqlite3, time
sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
import app


def load_twilio_json(path: str) -> int:
    """Ad-hoc offline replacement for sync_inbound_sms()."""
    import json
    from email.utils import parsedate_to_datetime
    with open(path, "r", encoding="utf-8") as fh:
        messages = json.load(fh)
    con = app.get_db()
    inserted = 0
    try:
        for m in messages:
            if not str(m.get("direction", "")).startswith("inbound"):
                continue
            raw_dt = m.get("date_sent") or m.get("date_created")
            try:
                ts = int(parsedate_to_datetime(raw_dt).timestamp())
            except Exception:
                continue
            cur = con.execute(
                "INSERT OR IGNORE INTO inbound_sms (sid, from_phone, to_phone, body, date_sent, synced_at) VALUES (?,?,?,?,?,?)",
                (m.get("sid", ""), m.get("from", ""), m.get("to", ""), (m.get("body") or "")[:2000], ts, int(time.time())),
            )
            inserted += cur.rowcount
        con.commit()
    finally:
        con.close()
    return inserted


twilio_json = None
if "--twilio-json" in sys.argv:
    twilio_json = sys.argv[sys.argv.index("--twilio-json") + 1]

app.init_db()
print("ro_exports +", app.sync_ro_exports())
print("inbound_sms +", (load_twilio_json(twilio_json) if twilio_json else app.sync_inbound_sms()))
con = app.get_db()
now = int(time.mktime(time.strptime("2026-09-03 17:00", "%Y-%m-%d %H:%M")))
out = app.compute_insights(con, (now - int(time.mktime(time.strptime("2026-05-13", "%Y-%m-%d")))) // 86400, now)
for row in out["funnel"]:
    print(f"{row['label']:<16} {row['n']:>4}  {('$%s' % format(row.get('revenue', 0), ',.0f')) if 'revenue' in row else ''}")
print("shop closed", out["tiles"]["closed_ros"]["value"], "revenue", out["tiles"]["revenue"]["value"])
print("warm leads", len(out["warm_leads"]), "upcoming", len(out["bookings_upcoming"]), "no-shows", len(out["no_shows"]))
