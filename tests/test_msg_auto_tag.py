"""MSG-04: only texts the system sent as follow-ups carry a job_type
(the inbox's "Auto follow-up" tag). Replies Marco types in the Messages
tab still get the customer's name via the phone match, but no job_type."""
import importlib, io, json, sqlite3, time

import pytest


@pytest.fixture
def app_mod(tmp_path, monkeypatch):
    monkeypatch.setenv("CCC_SECRET", "pytest-secret")
    monkeypatch.setenv("DB_PATH", str(tmp_path / "jobs.db"))
    monkeypatch.setenv("TELEMETRY_LOG_PATH", str(tmp_path / "t.log"))
    monkeypatch.setenv("REMOTE_CONFIG_PATH", str(tmp_path / "rc.json"))
    import app
    importlib.reload(app)
    app.init_db()
    monkeypatch.setattr(app, "TWILIO_ACCOUNT_SID", "AC1")
    monkeypatch.setattr(app, "TWILIO_API_KEY", "SK1")
    monkeypatch.setattr(app, "TWILIO_API_SECRET", "sec")
    return app


def _fake_twilio(monkeypatch, app, messages):
    class R(io.BytesIO):
        status = 200
        def __enter__(self): return self
        def __exit__(self, *a): return False
    monkeypatch.setattr(app, "urlopen", lambda req, timeout=15: R(json.dumps({"messages": messages}).encode()))


def test_typed_reply_is_not_tagged_auto(app_mod, monkeypatch):
    app = app_mod
    now = int(time.time())
    con = sqlite3.connect(app.DB_PATH)
    con.execute("INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at, estimate_key, is_test) "
                "VALUES ('D1', '3day', '+15105550100', 'Johnny Cortez', ?, 1, ?, 'est|J', 0)", (now - 3600, now - 7200))
    job_id = con.execute("SELECT id FROM jobs WHERE doc_id = 'D1'").fetchone()[0]
    cols = [r[1] for r in con.execute("PRAGMA table_info(sms_log)")]
    def log_row(body, jid):
        vals = {"body": body, "job_id": jid, "created_at": now - 300, "to_phone": "+15105550100",
                "phone": "+15105550100", "status": "sent", "kind": "reply" if jid is None else "job"}
        keys = [k for k in vals if k in cols]
        con.execute(f"INSERT INTO sms_log ({','.join(keys)}) VALUES ({','.join('?' * len(keys))})", [vals[k] for k in keys])
    log_row("Hi Johnny, checking in about your estimate.", job_id)   # automatic follow-up
    log_row("Usually it takes 7-10 working days.", None)             # typed in Messages
    con.commit(); con.close()
    sent = time.strftime("%a, %d %b %Y %H:%M:%S +0000", time.gmtime(now - 300))
    _fake_twilio(monkeypatch, app, [
        {"sid": "SM1", "direction": "outbound-api", "status": "delivered", "from": "+19256033934", "to": "+15105550100",
         "body": "Hi Johnny, checking in about your estimate.", "date_sent": sent},
        {"sid": "SM2", "direction": "outbound-api", "status": "delivered", "from": "+19256033934", "to": "+15105550100",
         "body": "Usually it takes 7-10 working days.", "date_sent": sent},
        {"sid": "SM3", "direction": "inbound", "status": "received", "from": "+15105550100", "to": "+19256033934",
         "body": "How's next week looking?", "date_sent": sent},
    ])
    rows, err = app._fetch_twilio_messages(7, "all", "all", 100)
    assert err == ""
    by = {r["sid"]: r for r in rows}
    assert by["SM1"]["job_type"] == "3day"                       # real auto follow-up keeps its tag
    assert by["SM2"]["job_type"] is None                         # typed reply: no Auto follow-up tag
    assert by["SM2"]["customer_name"] == "Johnny Cortez"         # ...but still named
    assert by["SM2"]["estimate_key"] == "est|J"
    assert by["SM3"]["job_type"] == "3day"                       # inbound keeps the thread context
