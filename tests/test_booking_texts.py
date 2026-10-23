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


# ---------------------------------------------------------------------------
# R-0003: review requests follow their own switch, like the reminder.
# ---------------------------------------------------------------------------

class _StopLoop(BaseException):
    pass


def _one_scheduler_tick(app, monkeypatch):
    def _stop(_s):
        raise _StopLoop
    with monkeypatch.context() as m:
        m.setattr(app, "_insights_last_sync", time.time())
        m.setattr(app, "_gcal_configured", lambda: False)
        m.setattr(app.time, "sleep", _stop)
        with pytest.raises(_StopLoop):
            app.scheduler_loop()


def _seed_due(app, job_type, phone):
    now = int(time.time())
    con = sqlite3.connect(app.DB_PATH)
    con.execute("INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at, vin, vehicle_desc, "
                "estimate_key, year, make, model, is_test) VALUES (?,?,?,'ann a',?,0,?,'VIN2','2020 Toyota Camry',"
                "?, '2020','Toyota','Camry',0)", ("RV-" + job_type, job_type, phone, now - 5, now - 3600, "est|RV-" + job_type))
    con.commit(); con.close()


@pytest.mark.parametrize("auto_send,review_auto,want", [
    (False, False, set()),
    (False, True, {"review"}),
    (True, False, {"24h"}),
    (True, True, {"24h", "review"}),
])
def test_review_switch_routes_scheduler(env, monkeypatch, auto_send, review_auto, want):
    app = env["app"]
    phones = {"+15105550201": "review", "+15105550202": "24h"}
    for p, jt in phones.items():
        _seed_due(app, jt, p)
    app.set_auto_send_enabled(auto_send)
    st = post(env, "/earlscheibconcord/booking-texts", {"review_auto": review_auto})
    assert st["review_auto"] is review_auto and st["reminder_auto"] is False
    _one_scheduler_tick(app, monkeypatch)
    assert {phones[to] for to, _ in env["sent"] if to in phones} == want


def test_review_switch_defaults_off(env):
    st = get(env, "/earlscheibconcord/booking-texts")
    assert st["review_auto"] is False
    with pytest.raises(HTTPError) as ei:
        post(env, "/earlscheibconcord/booking-texts", {"review_auto": "on"})
    assert ei.value.code == 400


# ---------------------------------------------------------------------------
# R-0004: the scheduler skips stale texts instead of blasting a backlog.
# ---------------------------------------------------------------------------

def _age_job(app, job_type, hours, phone=None):
    con = sqlite3.connect(app.DB_PATH)
    con.execute("UPDATE jobs SET send_at = ? WHERE job_type = ? AND (? IS NULL OR phone = ?)",
                (int(time.time()) - int(hours * 3600), job_type, phone, phone))
    con.commit(); con.close()


def _unsent(app, phone, job_type):
    con = sqlite3.connect(app.DB_PATH)
    n = con.execute("SELECT COUNT(*) FROM jobs WHERE phone = ? AND job_type = ? AND sent = 0 AND cancelled = 0",
                    (phone, job_type)).fetchone()[0]
    con.close()
    return n


@pytest.mark.parametrize("hours,setting,fires", [
    (1, None, True),          # fresh: fires
    (47, None, True),         # inside the default 48 h window
    (49, None, False),        # past it: left pending
    (24 * 15, None, False),   # the Oct 8 backlog case
    (49, "72", True),         # window widened in app_settings
    (49, "0", True),          # 0 turns the guard off
    (49, "junk", False),      # bad value falls back to 48
])
def test_stale_rows_skipped_by_scheduler(env, monkeypatch, hours, setting, fires):
    app = env["app"]
    _seed_due(app, "24h", "+15105550301")
    _age_job(app, "24h", hours, "+15105550301")
    if setting is not None:
        app._set_setting(app.STALE_SKIP_HOURS_KEY, setting)
    app.set_auto_send_enabled(True)
    _one_scheduler_tick(app, monkeypatch)
    assert (("+15105550301" in [to for to, _ in env["sent"]])) is fires
    assert _unsent(app, "+15105550301", "24h") == (0 if fires else 1)


def test_stale_row_still_sends_with_send_now(env, monkeypatch):
    app = env["app"]
    _seed_due(app, "review", "+15105550302")
    _age_job(app, "review", 24 * 10, "+15105550302")
    post(env, "/earlscheibconcord/booking-texts", {"review_auto": True})
    _one_scheduler_tick(app, monkeypatch)
    assert env["sent"] == [] and _unsent(app, "+15105550302", "review") == 1    # skipped, still pending
    con = sqlite3.connect(app.DB_PATH)
    job_id = con.execute("SELECT id FROM jobs WHERE phone = '+15105550302'").fetchone()[0]
    con.close()
    r = post(env, "/earlscheibconcord/queue/send-now", {"id": job_id})
    assert r == {"sent": True}
    assert [to for to, _ in env["sent"]] == ["+15105550302"] and _unsent(app, "+15105550302", "review") == 0


def test_reminder_skipped_once_appointment_passed(env, monkeypatch):
    app = env["app"]
    post(env, "/earlscheibconcord/booking-texts", {"reminder_auto": True})
    post(env, "/earlscheibconcord/queue/book", {"estimate_key": KEY, "appointment_at": int(time.time()) + 3600})
    _age_job(app, "appt_reminder", 0.01, "+15105550111")                      # due now, appointment still ahead
    con = sqlite3.connect(app.DB_PATH)
    con.execute("UPDATE appointments SET appointment_at = ? WHERE estimate_key = ?", (int(time.time()) - 600, KEY))
    con.commit(); con.close()
    _one_scheduler_tick(app, monkeypatch)
    assert env["sent"] == [] and _unsent(app, "+15105550111", "appt_reminder") == 1
    con = sqlite3.connect(app.DB_PATH)
    con.execute("UPDATE appointments SET appointment_at = ? WHERE estimate_key = ?", (int(time.time()) + 3600, KEY))
    con.commit(); con.close()
    _one_scheduler_tick(app, monkeypatch)
    assert len(env["sent"]) == 1 and "friendly reminder" in env["sent"][0][1]
