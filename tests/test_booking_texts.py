"""BKT-01: booking confirmation (auto or drafted) and the reminder switch."""
import importlib, json, sqlite3, threading, time
from http.server import HTTPServer
from urllib.error import HTTPError
from urllib.request import Request, urlopen

import pytest

KEY = "est|BK"


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
    sent = []
    monkeypatch.setattr(app, "send_sms", lambda to, body: (sent.append((to, body)) or (True, "")))
    monkeypatch.setattr(app, "_gcal_kick", lambda: None)
    now = int(time.time())
    con = sqlite3.connect(app.DB_PATH)
    con.execute("INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at, vin, vehicle_desc, "
                "estimate_key, year, make, model, is_test) VALUES ('DB1','24h','+15105550111','bob builder',?,0,?,"
                "'VIN1','2019 Ford F-150',?, '2019','Ford','F-150',0)", (now + 3600, now - 60, KEY))
    con.commit(); con.close()
    srv = HTTPServer(("127.0.0.1", 0), app.WebhookHandler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    yield {"app": app, "base": f"http://127.0.0.1:{srv.server_address[1]}", "sent": sent}
    srv.shutdown(); srv.server_close()


def post(env, path, data):
    req = Request(env["base"] + path, data=json.dumps(data).encode(), method="POST",
                  headers={"Content-Type": "application/json"})
    with urlopen(req, timeout=5) as r:
        return json.loads(r.read().decode())


def get(env, path):
    with urlopen(env["base"] + path, timeout=5) as r:
        return json.loads(r.read().decode())


def test_defaults_off_and_book_drafts_confirmation(env):
    st = get(env, "/earlscheibconcord/booking-texts")
    assert st["confirm_auto"] is False and st["reminder_auto"] is False
    at = int(time.time()) + 3 * 86400
    r = post(env, "/earlscheibconcord/queue/book", {"estimate_key": KEY, "appointment_at": at})
    c = r["confirmation"]
    assert r["rescheduled"] is False
    assert c["auto"] is False and c["sent"] is False
    assert c["text"].startswith("Hi Bob, this is Earl Scheib Of Concord. Your 2019 F-150 is booked for ")
    assert "{" not in c["text"] and c["phone"] == "+15105550111"
    assert env["sent"] == []                                   # nothing went out
    a = get(env, f"/earlscheibconcord/appointment?estimate_key={KEY}")
    assert a["booked"] is True and a["appointment_at"] == at and a["known"] is True


def test_auto_confirm_sends_once_per_booking(env):
    st = post(env, "/earlscheibconcord/booking-texts", {"confirm_auto": True})
    assert st["confirm_auto"] is True and st["reminder_auto"] is False
    at = int(time.time()) + 2 * 86400
    r = post(env, "/earlscheibconcord/queue/book", {"estimate_key": KEY, "appointment_at": at})
    assert r["confirmation"]["sent"] is True
    assert len(env["sent"]) == 1 and env["sent"][0][0] == "+15105550111"
    r2 = post(env, "/earlscheibconcord/queue/book", {"estimate_key": KEY, "appointment_at": at + 3600})
    assert r2["rescheduled"] is True and r2["confirmation"]["sent"] is True
    assert len(env["sent"]) == 2                               # a moved date is confirmed again


def test_auto_confirm_skips_past_dropoff(env):
    post(env, "/earlscheibconcord/booking-texts", {"confirm_auto": True})
    r = post(env, "/earlscheibconcord/queue/book", {"estimate_key": KEY, "appointment_at": int(time.time()) - 3600})
    assert r["confirmation"]["sent"] is False and env["sent"] == []


def test_reminder_switch_controls_firing(env):
    app = env["app"]
    at = int(time.time()) + 3600                               # reminder due ~now (clamped to now+120)
    post(env, "/earlscheibconcord/queue/book", {"estimate_key": KEY, "appointment_at": at})
    con = sqlite3.connect(app.DB_PATH)
    con.execute("UPDATE jobs SET send_at = ? WHERE job_type = 'appt_reminder'", (int(time.time()) - 5,))
    con.commit(); con.close()
    app._fire_due_jobs(exclude_types=("appt_reminder",))       # auto-send on, reminder switch off
    assert env["sent"] == []
    app._fire_due_jobs(only_types=("appt_reminder",))          # reminder switch on (auto-send off)
    assert len(env["sent"]) == 1 and "friendly reminder" in env["sent"][0][1]


def test_booking_texts_rejects_bad_body(env):
    for bad in ({}, {"confirm_auto": "yes"}, {"other": True}):
        with pytest.raises(HTTPError) as ei:
            post(env, "/earlscheibconcord/booking-texts", bad)
        assert ei.value.code == 400


def test_confirmation_template_is_editable(env):
    tpl = get(env, "/earlscheibconcord/templates")
    jts = [t["job_type"] for t in tpl["job_types"]]
    assert "booking_confirm" in jts
    sched = get(env, "/earlscheibconcord/schedules")
    assert "booking_confirm" not in json.dumps(sched)          # no delay editor for it
