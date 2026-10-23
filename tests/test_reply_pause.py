"""RPL-01 (R-0007): a customer text after the estimate pauses the 24h / 3-day
check-ins (like a booking does), so canned texts never land mid-conversation.
Appointment reminders and review requests are unaffected; Send now still
sends a paused row on purpose; app_settings reply_pause_enabled=0 turns it off.
"""
import importlib
import json
import sqlite3
import time
from urllib.request import Request, urlopen

import pytest

PHONE = "+15305550101"


@pytest.fixture
def app_env(monkeypatch, tmp_path):
    db_path = str(tmp_path / "jobs.db")
    monkeypatch.setenv("CCC_SECRET", "pytest-fixture-secret-do-not-ship")
    monkeypatch.setenv("DB_PATH", db_path)
    monkeypatch.setenv("TELEMETRY_LOG_PATH", str(tmp_path / "telemetry.log"))
    monkeypatch.setenv("REMOTE_CONFIG_PATH", str(tmp_path / "remote_config.json"))
    monkeypatch.setenv("PORT", "0")
    import app as app_mod
    importlib.reload(app_mod)
    app_mod.init_db()
    sent = []
    monkeypatch.setattr(app_mod, "send_sms", lambda phone, body: (sent.append((phone, body)) or (True, "")))
    # No Twilio in tests: the pre-send refresh is a clean no-op read.
    syncs = []

    def _fake_sync(fetch=None):
        syncs.append(1)
        app_mod._inbound_last_ok = time.time()
        return 0
    monkeypatch.setattr(app_mod, "sync_inbound_sms", _fake_sync)
    return app_mod, db_path, sent, syncs


def _job(db_path, job_type, phone=PHONE, created_ago=86400 * 3, send_ago=60, key="EK-1"):
    now = int(time.time())
    con = sqlite3.connect(db_path)
    cur = con.execute(
        "INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at, "
        " vehicle_desc, estimate_key, year, make, model) "
        "VALUES ('DOC-1', ?, ?, 'Gerardo Tester', ?, 0, ?, '2015 Toyota Camry', ?, '2015', 'TOYOTA', 'CAMRY')",
        (job_type, phone, now - send_ago, now - created_ago, key),
    )
    con.commit()
    jid = cur.lastrowid
    con.close()
    return jid


def _inbound(db_path, ago, phone=PHONE, sid=None):
    now = int(time.time())
    con = sqlite3.connect(db_path)
    con.execute(
        "INSERT INTO inbound_sms (sid, from_phone, to_phone, body, date_sent, synced_at) VALUES (?,?,?,?,?,?)",
        (sid or f"SM{ago}{phone}", phone, "+19256097780", "for sure we're going to have you do the job", now - ago, now),
    )
    con.commit()
    con.close()


def _sent(db_path, jid):
    con = sqlite3.connect(db_path)
    v = con.execute("SELECT sent FROM jobs WHERE id = ?", (jid,)).fetchone()[0]
    con.close()
    return v


def test_reply_after_estimate_pauses_24h_and_3day(app_env):
    app_mod, db, sent, _ = app_env
    j24 = _job(db, "24h")
    j3 = _job(db, "3day")
    _inbound(db, ago=3600)
    app_mod._fire_due_jobs()
    assert sent == []
    assert _sent(db, j24) == 0 and _sent(db, j3) == 0


def test_no_reply_still_sends(app_env):
    app_mod, db, sent, _ = app_env
    j = _job(db, "3day")
    _inbound(db, ago=3600, phone="+15305559999")   # someone else
    app_mod._fire_due_jobs()
    assert len(sent) == 1 and _sent(db, j) == 1


def test_reply_before_estimate_does_not_pause(app_env):
    app_mod, db, sent, _ = app_env
    j = _job(db, "24h", created_ago=86400)
    _inbound(db, ago=86400 * 5)   # texted about the estimate visit, days before it
    app_mod._fire_due_jobs()
    assert len(sent) == 1 and _sent(db, j) == 1


def test_reminder_and_review_unaffected(app_env):
    app_mod, db, sent, _ = app_env
    con = sqlite3.connect(db)
    con.execute(
        "INSERT INTO appointments (estimate_key, appointment_at, created_at, updated_at, unbooked_at) VALUES ('EK-1', ?, ?, ?, 0)",
        (int(time.time()) + 3600 * 5, int(time.time()) - 60, int(time.time()) - 60),
    )
    con.commit()
    con.close()
    rem = _job(db, "appt_reminder")
    rev = _job(db, "review", key="EK-2")
    _inbound(db, ago=3600)
    app_mod._fire_due_jobs()
    assert _sent(db, rem) == 1 and _sent(db, rev) == 1


def test_switch_off_restores_old_behaviour(app_env):
    app_mod, db, sent, _ = app_env
    j = _job(db, "3day")
    _inbound(db, ago=3600)
    app_mod._set_setting(app_mod.REPLY_PAUSE_KEY, "0")
    app_mod._fire_due_jobs()
    assert len(sent) == 1 and _sent(db, j) == 1


def test_inbound_reread_before_send_catches_fresh_reply(app_env, monkeypatch):
    """A reply that lands after the last refresh is caught by the pre-send read."""
    app_mod, db, sent, _ = app_env
    j = _job(db, "3day")
    app_mod._inbound_last_ok = time.time() - 600   # last read 10 min ago

    def _sync_finds_reply(fetch=None):
        _inbound(db, ago=30)
        app_mod._inbound_last_ok = time.time()
        return 1
    monkeypatch.setattr(app_mod, "sync_inbound_sms", _sync_finds_reply)
    app_mod._fire_due_jobs()
    assert sent == [] and _sent(db, j) == 0


def test_twilio_unreadable_holds_followups_only(app_env, monkeypatch):
    app_mod, db, sent, _ = app_env
    j = _job(db, "24h")
    rev = _job(db, "review", key="EK-2")
    app_mod._inbound_last_ok = time.time() - app_mod.INBOUND_STALE_HOLD_S - 5
    monkeypatch.setattr(app_mod, "sync_inbound_sms", lambda fetch=None: 0)  # read fails, stamp not refreshed
    app_mod._fire_due_jobs()
    assert _sent(db, j) == 0 and _sent(db, rev) == 1


def test_no_refresh_when_nothing_to_send(app_env):
    app_mod, db, sent, syncs = app_env
    _job(db, "3day")
    _inbound(db, ago=3600)
    app_mod._inbound_last_ok = 0
    app_mod._fire_due_jobs()
    assert syncs == []   # already paused: no Twilio read needed


def test_sync_marks_last_ok_and_failure(app_env, monkeypatch):
    app_mod, db, _, _ = app_env
    importlib.reload(app_mod)   # real sync_inbound_sms
    app_mod.init_db()
    app_mod._inbound_last_ok = 0
    page = {"messages": [{"sid": "SMx", "direction": "inbound", "from": PHONE, "to": "+19256097780",
                          "body": "ok", "date_sent": "Fri, 23 Oct 2026 17:00:00 +0000"}], "next_page_uri": None}
    app_mod.sync_inbound_sms(fetch=lambda url: page)
    assert app_mod._inbound_last_ok > 0
    app_mod._inbound_last_ok = 0

    def _boom(url):
        raise OSError("twilio down")
    app_mod.sync_inbound_sms(fetch=_boom)
    assert app_mod._inbound_last_ok == 0


def test_queue_payload_flags_paused_rows(app_env):
    app_mod, db, _, _ = app_env
    from http.server import HTTPServer
    import threading
    j3 = _job(db, "3day")
    other = _job(db, "24h", phone="+15305559999", key="EK-3")
    rev = _job(db, "review", key="EK-1")
    _inbound(db, ago=3600)
    server = HTTPServer(("127.0.0.1", 0), app_mod.WebhookHandler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        url = f"http://127.0.0.1:{server.server_address[1]}/earlscheibconcord/queue"
        rows = {r["id"]: r for r in json.loads(urlopen(Request(url), timeout=5).read())}
    finally:
        server.shutdown()
        server.server_close()
    assert rows[j3]["reply_paused"] == 1 and rows[j3]["customer_replied_at"] > 0
    assert rows[other]["reply_paused"] == 0
    assert rows[rev]["reply_paused"] == 0


def test_send_now_still_sends_paused_row(app_env):
    app_mod, db, sent, _ = app_env
    from http.server import HTTPServer
    import threading
    j = _job(db, "3day")
    _inbound(db, ago=3600)
    server = HTTPServer(("127.0.0.1", 0), app_mod.WebhookHandler)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    try:
        url = f"http://127.0.0.1:{server.server_address[1]}/earlscheibconcord/queue/send-now"
        req = Request(url, data=json.dumps({"id": j}).encode(), headers={"Content-Type": "application/json"}, method="POST")
        assert urlopen(req, timeout=5).status == 200
    finally:
        server.shutdown()
        server.server_close()
    assert len(sent) == 1 and _sent(db, j) == 1
