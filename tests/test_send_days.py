"""R-0005: Saturday as a send day (switch, default on) and re-timing pending texts."""
import importlib, json, sqlite3, threading
from datetime import datetime
from http.server import HTTPServer
from urllib.error import HTTPError
from urllib.request import Request, urlopen

import pytest
import pytz

TZ = pytz.timezone("America/Los_Angeles")


def ts(y, mo, d, h, mi=0):
    return int(TZ.localize(datetime(y, mo, d, h, mi)).timestamp())


FRI_0842 = ts(2026, 10, 23, 8, 42)
NOW = ts(2026, 10, 23, 9, 30)          # Fri
SAT_10 = ts(2026, 10, 24, 10)
MON_10 = ts(2026, 10, 26, 10)


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
    srv = HTTPServer(("127.0.0.1", 0), app.WebhookHandler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    yield {"app": app, "base": f"http://127.0.0.1:{srv.server_address[1]}"}
    srv.shutdown(); srv.server_close()


def post(env, data):
    req = Request(env["base"] + "/earlscheibconcord/booking-texts", data=json.dumps(data).encode(),
                  method="POST", headers={"Content-Type": "application/json"})
    with urlopen(req, timeout=5) as r:
        return json.loads(r.read().decode())


def get(env):
    with urlopen(env["base"] + "/earlscheibconcord/booking-texts", timeout=5) as r:
        return json.loads(r.read().decode())


@pytest.mark.parametrize("after, sat_on, sat_off", [
    (ts(2026, 10, 23, 16, 30), SAT_10, MON_10),              # Fri after 4pm
    (FRI_0842 + 24 * 3600, SAT_10, MON_10),                  # the Fri 8:42 close + 24h
    (ts(2026, 10, 24, 11), ts(2026, 10, 24, 11), MON_10),    # Sat in the morning window
    (ts(2026, 10, 24, 12, 30), ts(2026, 10, 24, 14), MON_10),
    (ts(2026, 10, 24, 17), MON_10, MON_10),                  # Sat after 4pm skips Sunday
    (ts(2026, 10, 25, 9), MON_10, MON_10),                   # Sunday always skipped
    (ts(2026, 10, 22, 11), ts(2026, 10, 22, 11), ts(2026, 10, 22, 11)),  # weekday unchanged
])
def test_window_with_and_without_saturday(env, after, sat_on, sat_off):
    app = env["app"]
    assert app.next_send_window(after, saturday=True) == sat_on
    assert app.next_send_window(after, saturday=False) == sat_off


def test_switch_defaults_on_and_drives_window(env):
    app = env["app"]
    after = ts(2026, 10, 23, 16, 30)
    assert get(env)["saturday_sends"] is True
    assert app.next_send_window(after) == SAT_10
    st = post(env, {"saturday_sends": False})
    assert st["saturday_sends"] is False and st["retimed"] == 0
    assert app.next_send_window(after) == MON_10


def test_switch_rejects_non_bool(env):
    with pytest.raises(HTTPError) as e:
        post(env, {"saturday_sends": "yes"})
    assert e.value.code == 400
    assert get(env)["saturday_sends"] is True


def _seed(app, job_type, created_at, send_at, manual=0):
    con = sqlite3.connect(app.DB_PATH)
    cur = con.execute(
        "INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at, send_at_manual) "
        "VALUES (?, ?, '+15105550111', 'bob', ?, 0, ?, ?)",
        (f"D{created_at}{job_type}{manual}", job_type, send_at, created_at, manual))
    con.commit(); rid = cur.lastrowid; con.close()
    return rid


def _send_at(app, rid):
    con = sqlite3.connect(app.DB_PATH)
    v = con.execute("SELECT send_at FROM jobs WHERE id = ?", (rid,)).fetchone()[0]
    con.close()
    return v


def test_retime_moves_monday_rows_to_saturday_and_back(env, monkeypatch):
    app = env["app"]
    monkeypatch.setattr(app.time, "time", lambda: NOW)
    d_review = app.get_effective_schedule("review")
    d_3day = app.get_effective_schedule("3day")
    review = _seed(app, "review", SAT_10 - d_review * 3600, MON_10)       # closed Fri morning
    three = _seed(app, "3day", ts(2026, 10, 24, 9) - d_3day * 3600, MON_10)
    manual = _seed(app, "review", SAT_10 - d_review * 3600, MON_10, manual=1)
    rebased = _seed(app, "24h", NOW - 30 * 86400, MON_10)                 # slot long past: stays
    weekday = _seed(app, "24h", ts(2026, 10, 27, 11) - 24 * 3600, ts(2026, 10, 27, 11))

    assert app._retime_for_send_days(True) == 2
    assert _send_at(app, review) == SAT_10 and _send_at(app, three) == SAT_10
    assert _send_at(app, manual) == MON_10 and _send_at(app, rebased) == MON_10
    assert _send_at(app, weekday) == ts(2026, 10, 27, 11)
    assert app._retime_for_send_days(True) == 0                           # idempotent

    assert app._retime_for_send_days(False) == 2
    assert _send_at(app, review) == MON_10 and _send_at(app, three) == MON_10
    assert _send_at(app, weekday) == ts(2026, 10, 27, 11)


def test_post_retimes_even_when_unchanged(env, monkeypatch):
    app = env["app"]
    monkeypatch.setattr(app.time, "time", lambda: NOW)
    rid = _seed(app, "review", SAT_10 - app.get_effective_schedule("review") * 3600, MON_10)
    st = post(env, {"saturday_sends": True})
    assert st["saturday_sends"] is True and st["retimed"] == 1
    assert _send_at(app, rid) == SAT_10
