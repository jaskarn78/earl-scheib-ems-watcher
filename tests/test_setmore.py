"""SET-01 (R-0006): Setmore bookings (read from Marco's Google Calendar)
count as booked. _gcal_api is replaced by an in-memory calendar; nothing
here touches the real Google."""
import importlib, json, sqlite3, threading, time
from datetime import datetime, timezone
from http.server import HTTPServer
from urllib.request import Request, urlopen

import pytest

KEY = "est|SM"
PHONE = "+19255550142"


def setmore_desc(service="Dropping Off Vehicle", mobile="+19255550142", name="Pat Doe"):
    return (f"\nAPPOINTMENT DETAILS\nSERVICE: {service}\nPROVIDER: ESC\nFROM: 10:00 AM\n"
            f"TO: 10:30 AM\n---\nNAME: {name}\nMOBILE: {mobile}\nMake: Honda\nYear: 2015\n")


def iso(ts):
    return datetime.fromtimestamp(ts, tz=timezone.utc).strftime("%Y-%m-%dT%H:%M:%SZ")


class FakeCal:
    def __init__(self):
        self.events = {}
        self.page_size = 250

    def add(self, eid, start, desc, status="confirmed", **extra):
        self.events[eid] = {"id": eid, "status": status, "description": desc,
                            "summary": "x", "start": {"dateTime": iso(start)}, **extra}

    def api(self, method, path, body=None, params=None):
        import app
        assert method == "GET", "the import never writes to the calendar"
        if path.endswith("/events"):
            lo = datetime.fromisoformat(params["timeMin"].replace("Z", "+00:00")).timestamp()
            hi = datetime.fromisoformat(params["timeMax"].replace("Z", "+00:00")).timestamp()
            live = sorted(
                (e for e in self.events.values() if e["status"] != "cancelled"
                 and lo <= datetime.fromisoformat(e["start"]["dateTime"].replace("Z", "+00:00")).timestamp() < hi),
                key=lambda e: e["id"])
            off = int(params.get("pageToken") or 0)
            chunk = live[off:off + self.page_size]
            out = {"items": chunk}
            if off + self.page_size < len(live):
                out["nextPageToken"] = str(off + self.page_size)
            return 200, out
        eid = path.rsplit("/", 1)[-1]
        if eid not in self.events:
            raise app.GcalError(404, "Not Found")
        return 200, self.events[eid]


@pytest.fixture
def env(tmp_path, monkeypatch):
    monkeypatch.setenv("CCC_SECRET", "pytest-secret")
    monkeypatch.setenv("DB_PATH", str(tmp_path / "jobs.db"))
    monkeypatch.setenv("TELEMETRY_LOG_PATH", str(tmp_path / "t.log"))
    monkeypatch.setenv("REMOTE_CONFIG_PATH", str(tmp_path / "rc.json"))
    monkeypatch.setenv("PORT", "0")
    import app
    importlib.reload(app)
    app.init_db()
    cal = FakeCal()
    sent = []
    monkeypatch.setattr(app, "_gcal_api", cal.api)
    monkeypatch.setattr(app, "_gcal_connected", lambda: True)
    monkeypatch.setattr(app, "_gcal_kick", lambda: None)
    monkeypatch.setattr(app, "send_sms", lambda to, body: (sent.append((to, body)) or (True, "")))
    now = int(time.time())
    con = sqlite3.connect(app.DB_PATH)
    for jt, at in (("24h", now + 3600), ("3day", now + 3 * 86400)):
        con.execute("INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at, vin, "
                    "vehicle_desc, estimate_key, year, make, model, is_test) VALUES "
                    "('D1', ?, ?, 'pat doe', ?, 0, ?, 'VIN1', '2015 Honda Accord', ?, '2015', 'Honda', 'Accord', 0)",
                    (jt, PHONE, at, now - 3600, KEY))
    con.commit(); con.close()
    srv = HTTPServer(("127.0.0.1", 0), app.WebhookHandler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    yield {"app": app, "cal": cal, "now": now, "sent": sent,
           "base": f"http://127.0.0.1:{srv.server_address[1]}"}
    srv.shutdown(); srv.server_close()


def db(env, sql, args=()):
    con = sqlite3.connect(env["app"].DB_PATH)
    con.row_factory = sqlite3.Row
    try:
        return con.execute(sql, args).fetchall()
    finally:
        con.close()


def appt(env):
    rows = db(env, "SELECT * FROM appointments WHERE estimate_key = ? AND unbooked_at = 0", (KEY,))
    return rows[0] if rows else None


def post(env, path, data):
    req = Request(env["base"] + path, data=json.dumps(data).encode(), method="POST",
                  headers={"Content-Type": "application/json"})
    with urlopen(req, timeout=5) as r:
        return json.loads(r.read().decode())


def test_parse_setmore_and_ignore_others(env):
    app = env["app"]
    p = app._setmore_parse({"id": "e1", "status": "confirmed", "description": setmore_desc(),
                            "start": {"dateTime": "2026-10-29T09:00:00-07:00"}})
    assert p["service"] == "Dropping Off Vehicle" and p["phone10"] == "9255550142"
    assert p["start"] == int(datetime.fromisoformat("2026-10-29T09:00:00-07:00").timestamp())
    html = setmore_desc().replace("\n", "<br>")
    assert app._setmore_parse({"id": "e2", "description": html,
                               "start": {"dateTime": "2026-10-29T09:00:00-07:00"}})["phone10"] == "9255550142"
    # Marco's own note, the app's own event, an all-day event: not bookings.
    assert app._setmore_parse({"id": "e3", "description": "<p>Cliente: Ken</p>",
                               "start": {"dateTime": "2026-10-29T09:00:00-07:00"}}) is None
    assert app._setmore_parse({"id": "e4", "description": setmore_desc(),
                               "extendedProperties": {"private": {"esw_key": "k"}},
                               "start": {"dateTime": "2026-10-29T09:00:00-07:00"}}) is None
    assert app._setmore_parse({"id": "e5", "description": setmore_desc(),
                               "start": {"date": "2026-10-29"}}) is None


def test_booking_imported_pauses_followups_without_texts(env):
    app, now = env["app"], env["now"]
    at = now + 2 * 86400
    env["cal"].add("sm1", at, setmore_desc())
    st = app._setmore_import_once()
    assert st["imported"] == 1
    a = appt(env)
    assert a["source"] == "setmore" and a["source_ref"] == "sm1" and a["appointment_at"] == at
    # Follow-ups paused: the scheduler fires nothing even when due.
    con = sqlite3.connect(app.DB_PATH)
    con.execute("UPDATE jobs SET send_at = ? WHERE estimate_key = ?", (now - 60, KEY)); con.commit(); con.close()
    app._fire_due_jobs()
    assert env["sent"] == []
    # Reminder off by default (Setmore sends its own); no confirmation text.
    assert db(env, "SELECT 1 FROM jobs WHERE job_type = 'appt_reminder'") == []
    # Second pass is a no-op.
    assert app._setmore_import_once()["imported"] == 0
    # Not pushed to Google as a second event.
    con = app.get_db()
    try:
        assert app._gcal_active_rows(con) == []
    finally:
        con.close()


def test_reminder_armed_when_switched_on(env):
    app, now = env["app"], env["now"]
    app._set_setting(app.SETMORE_REMINDER_KEY, "1")
    env["cal"].add("sm1", now + 3 * 86400, setmore_desc())
    app._setmore_import_once()
    r = db(env, "SELECT * FROM jobs WHERE job_type = 'appt_reminder' AND estimate_key = ?", (KEY,))
    assert len(r) == 1 and r[0]["send_at"] == now + 3 * 86400 - app.APPT_REMINDER_LEAD_H * 3600


@pytest.mark.parametrize("case", ["estimate_visit", "other_phone", "past", "no_pending", "old_estimate", "off"])
def test_not_imported(env, case):
    app, now = env["app"], env["now"]
    if case == "estimate_visit":
        # The visit that produced the estimate: starts before the estimate exists.
        con = sqlite3.connect(app.DB_PATH)
        con.execute("UPDATE jobs SET created_at = ?", (now + 7200,)); con.commit(); con.close()
        env["cal"].add("sm1", now + 3600, setmore_desc("Free In-Person Estimate"))
    elif case == "other_phone":
        env["cal"].add("sm1", now + 86400, setmore_desc(mobile="+14155550000"))
    elif case == "past":
        env["cal"].add("sm1", now - 600, setmore_desc())
    elif case == "no_pending":
        con = sqlite3.connect(app.DB_PATH)
        con.execute("UPDATE jobs SET sent = 1"); con.commit(); con.close()
        env["cal"].add("sm1", now + 86400, setmore_desc())
    elif case == "old_estimate":
        con = sqlite3.connect(app.DB_PATH)
        con.execute("UPDATE jobs SET created_at = ?", (now - 100 * 86400,)); con.commit(); con.close()
        env["cal"].add("sm1", now + 86400, setmore_desc())
    elif case == "off":
        app._set_setting(app.SETMORE_IMPORT_KEY, "0")
        env["cal"].add("sm1", now + 86400, setmore_desc())
    app._setmore_import_once()
    assert appt(env) is None


def test_manual_booking_wins(env):
    app, now = env["app"], env["now"]
    mine = now + 5 * 86400
    post(env, "/earlscheibconcord/queue/book", {"estimate_key": KEY, "appointment_at": mine})
    env["cal"].add("sm1", now + 2 * 86400, setmore_desc())
    app._setmore_import_once()
    a = appt(env)
    assert a["source"] == "" and a["appointment_at"] == mine


def test_moved_and_cancelled_in_setmore(env):
    app, now = env["app"], env["now"]
    env["cal"].add("sm1", now + 2 * 86400, setmore_desc())
    app._setmore_import_once()
    # Moved inside the window.
    env["cal"].add("sm1", now + 4 * 86400, setmore_desc())
    assert app._setmore_import_once()["moved"] == 1
    assert appt(env)["appointment_at"] == now + 4 * 86400
    # Moved beyond the listing window: looked up by id and retimed, not unbooked.
    far = now + (app.SETMORE_LOOKAHEAD_DAYS + 10) * 86400
    env["cal"].add("sm1", far, setmore_desc())
    app._setmore_import_once()
    assert appt(env)["appointment_at"] == far
    # Cancelled: unbooked, follow-ups resume, never re-imported.
    env["cal"].events["sm1"]["status"] = "cancelled"
    assert app._setmore_import_once()["cancelled"] == 1
    assert appt(env) is None
    assert db(env, "SELECT state FROM setmore_seen WHERE event_id = 'sm1'")[0]["state"] == "cancelled"


def test_deleted_event_unbooks(env):
    app, now = env["app"], env["now"]
    env["cal"].add("sm1", now + 2 * 86400, setmore_desc())
    app._setmore_import_once()
    del env["cal"].events["sm1"]
    assert app._setmore_import_once()["cancelled"] == 1
    assert appt(env) is None


def test_listing_failure_changes_nothing(env, monkeypatch):
    app, now = env["app"], env["now"]
    env["cal"].add("sm1", now + 2 * 86400, setmore_desc())
    app._setmore_import_once()

    def boom(*a, **k):
        raise app.GcalError(500, "backend error")
    monkeypatch.setattr(app, "_gcal_api", boom)
    with pytest.raises(app.GcalError):
        app._setmore_import_once()
    assert appt(env)["source"] == "setmore"


def test_unmark_dismisses_for_good(env):
    app, now = env["app"], env["now"]
    env["cal"].add("sm1", now + 2 * 86400, setmore_desc())
    app._setmore_import_once()
    r = post(env, "/earlscheibconcord/queue/unbook", {"estimate_key": KEY})
    assert r["unbooked"] == 1
    app._setmore_import_once()
    assert appt(env) is None
    assert db(env, "SELECT state FROM setmore_seen WHERE event_id = 'sm1'")[0]["state"] == "dismissed"


def test_change_date_takes_it_over(env):
    app, now = env["app"], env["now"]
    env["cal"].add("sm1", now + 2 * 86400, setmore_desc())
    app._setmore_import_once()
    mine = now + 6 * 86400
    post(env, "/earlscheibconcord/queue/book", {"estimate_key": KEY, "appointment_at": mine})
    env["cal"].add("sm1", now + 3 * 86400, setmore_desc())     # Setmore moves it
    app._setmore_import_once()
    a = appt(env)
    assert a["source"] == "" and a["appointment_at"] == mine


def test_pagination_followed(env):
    app, now = env["app"], env["now"]
    env["cal"].page_size = 2
    for i in range(5):
        env["cal"].add(f"a{i}", now + 86400 + i, "<p>note</p>")
    env["cal"].add("z-sm", now + 2 * 86400, setmore_desc())   # sorts last: page 3
    assert app._setmore_import_once()["imported"] == 1


def test_queue_payload_marks_setmore(env):
    app, now = env["app"], env["now"]
    env["cal"].add("sm1", now + 2 * 86400, setmore_desc())
    app._setmore_import_once()
    with urlopen(env["base"] + "/earlscheibconcord/queue?status=all", timeout=5) as r:
        data = json.loads(r.read().decode())
    jobs = data["jobs"] if isinstance(data, dict) else data
    row = [j for j in jobs if j["estimate_key"] == KEY][0]
    assert row["booked"] == 1 and row["appointment_source"] == "setmore" and row["gcal_synced"] == 1


def test_calendar_pause_stops_import(env):
    app, now = env["app"], env["now"]
    app._gcal_set_sync_enabled(False)
    env["cal"].add("sm1", now + 2 * 86400, setmore_desc())
    app._setmore_import_once()
    assert appt(env) is None
