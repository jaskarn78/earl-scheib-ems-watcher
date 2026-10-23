"""CAL-04 (R-0001): Google Calendar sync.

A fake Google (token + userinfo + Calendar v3 events) runs on an ephemeral
port; app.py is reloaded with GCAL_* env pointing at it. Nothing here
touches the real Google.
"""
import importlib
import json
import os
import sqlite3
import stat
import threading
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.error import HTTPError
from urllib.parse import parse_qs, urlparse
from urllib.request import HTTPErrorProcessor, Request, build_opener, urlopen

import pytest


# ---------------------------------------------------------------------------
# Fake Google
# ---------------------------------------------------------------------------

class FakeGoogle:
    def __init__(self):
        self.events = {}        # id -> body (with "id", "status")
        self.calls = []         # (method, path, body-dict-or-None)
        self.next_id = 1
        self.valid_tokens = {"at-1", "at-2"}
        self.lock = threading.Lock()

    def live_events(self):
        return [e for e in self.events.values() if e.get("status") != "cancelled"]


def make_handler(fake: FakeGoogle):
    class H(BaseHTTPRequestHandler):
        def log_message(self, *a):  # quiet
            pass

        def _json(self, status, data):
            raw = json.dumps(data).encode()
            self.send_response(status)
            self.send_header("Content-Type", "application/json")
            self.send_header("Content-Length", str(len(raw)))
            self.end_headers()
            self.wfile.write(raw)

        def _body(self):
            n = int(self.headers.get("Content-Length") or 0)
            raw = self.rfile.read(n) if n else b""
            ctype = self.headers.get("Content-Type", "")
            if "json" in ctype:
                return json.loads(raw.decode()) if raw else {}
            return {k: v[0] for k, v in parse_qs(raw.decode()).items()}

        def _authed(self):
            auth = self.headers.get("Authorization", "")
            tok = auth.replace("Bearer ", "", 1)
            if tok in fake.valid_tokens:
                return True
            self._json(401, {"error": {"code": 401, "message": "Invalid Credentials"}})
            return False

        def do_POST(self):
            u = urlparse(self.path)
            body = self._body()
            with fake.lock:
                fake.calls.append(("POST", u.path, body))
            if u.path == "/token":
                if body.get("grant_type") == "authorization_code":
                    if body.get("code") != "good-code":
                        return self._json(400, {"error": "invalid_grant", "error_description": "Bad code"})
                    return self._json(200, {"access_token": "at-1", "refresh_token": "rt-1",
                                            "expires_in": 3600, "token_type": "Bearer"})
                if body.get("grant_type") == "refresh_token":
                    if body.get("refresh_token") == "rt-revoked":
                        return self._json(400, {"error": "invalid_grant",
                                                "error_description": "Token has been expired or revoked."})
                    return self._json(200, {"access_token": "at-2", "expires_in": 3600})
                return self._json(400, {"error": "unsupported_grant_type"})
            if u.path == "/revoke":
                return self._json(200, {})
            if u.path == "/cal/calendars/primary/events":
                if not self._authed():
                    return None
                with fake.lock:
                    eid = f"ev-{fake.next_id}"
                    fake.next_id += 1
                    ev = dict(body)
                    ev["id"] = eid
                    ev["status"] = "confirmed"
                    fake.events[eid] = ev
                return self._json(200, ev)
            return self._json(404, {"error": {"message": "no route"}})

        def do_GET(self):
            u = urlparse(self.path)
            with fake.lock:
                fake.calls.append(("GET", u.path, None))
            if u.path == "/userinfo":
                if not self._authed():
                    return None
                return self._json(200, {"email": "marco@example.com", "sub": "123"})
            if u.path == "/cal/calendars/primary/events":
                if not self._authed():
                    return None
                qs = parse_qs(u.query)
                want = (qs.get("privateExtendedProperty") or [""])[0]
                items = []
                with fake.lock:
                    for ev in fake.events.values():
                        props = ev.get("extendedProperties", {}).get("private", {})
                        if want and want != f"esw_key={props.get('esw_key')}":
                            continue
                        items.append(ev)
                return self._json(200, {"items": items})
            return self._json(404, {"error": {"message": "no route"}})

        def do_PATCH(self):
            u = urlparse(self.path)
            body = self._body()
            with fake.lock:
                fake.calls.append(("PATCH", u.path, body))
            if not self._authed():
                return None
            eid = u.path.rsplit("/", 1)[-1]
            with fake.lock:
                ev = fake.events.get(eid)
                if not ev or ev.get("status") == "cancelled":
                    return self._json(404, {"error": {"message": "Not Found"}})
                ev.update(body)
            return self._json(200, ev)

        def do_DELETE(self):
            u = urlparse(self.path)
            with fake.lock:
                fake.calls.append(("DELETE", u.path, None))
            if not self._authed():
                return None
            eid = u.path.rsplit("/", 1)[-1]
            with fake.lock:
                ev = fake.events.get(eid)
                if not ev or ev.get("status") == "cancelled":
                    return self._json(404, {"error": {"message": "Not Found"}})
                ev["status"] = "cancelled"
            self.send_response(204)
            self.send_header("Content-Length", "0")
            self.end_headers()
            return None

    return H


# ---------------------------------------------------------------------------
# Fixture
# ---------------------------------------------------------------------------

KEY = "+15551110001|VIN0000000000001"
TEST_KEY = "test|+15551110009|VINTEST"


@pytest.fixture
def gcal_env(tmp_path, monkeypatch):
    fake = FakeGoogle()
    gsrv = HTTPServer(("127.0.0.1", 0), make_handler(fake))
    gport = gsrv.server_address[1]
    threading.Thread(target=gsrv.serve_forever, daemon=True).start()
    gbase = f"http://127.0.0.1:{gport}"

    secret = "pytest-fixture-secret-do-not-ship"
    db_path = str(tmp_path / "jobs.db")
    token_path = str(tmp_path / "gcal_token.json")
    monkeypatch.setenv("CCC_SECRET", secret)
    monkeypatch.setenv("DB_PATH", db_path)
    monkeypatch.setenv("TELEMETRY_LOG_PATH", str(tmp_path / "telemetry.log"))
    monkeypatch.setenv("REMOTE_CONFIG_PATH", str(tmp_path / "remote_config.json"))
    monkeypatch.setenv("PORT", "0")
    monkeypatch.setenv("GCAL_CLIENT_ID", "cid-test")
    monkeypatch.setenv("GCAL_CLIENT_SECRET", "csec-test")
    monkeypatch.setenv("GCAL_TOKEN_PATH", token_path)
    monkeypatch.setenv("GCAL_REDIRECT_URI", "http://app.test/earlscheibconcord/gcal/callback")
    monkeypatch.setenv("GCAL_AUTH_URL", f"{gbase}/auth")
    monkeypatch.setenv("GCAL_TOKEN_URL", f"{gbase}/token")
    monkeypatch.setenv("GCAL_REVOKE_URL", f"{gbase}/revoke")
    monkeypatch.setenv("GCAL_USERINFO_URL", f"{gbase}/userinfo")
    monkeypatch.setenv("GCAL_API_BASE", f"{gbase}/cal")

    import app
    importlib.reload(app)
    app.init_db()

    now = int(time.time())
    con = sqlite3.connect(db_path)
    con.execute(
        "INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at, "
        " vin, vehicle_desc, ro_id, estimate_key, year, make, model, is_test) "
        "VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, ?, ?, 0)",
        ("DOC-A", "24h", "+15551110001", "Alice Example", now + 3600, now - 60,
         "VIN0000000000001", "13 Camry", "RO-77", KEY, "2013", "Toyota", "Camry"),
    )
    con.execute(
        "INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at, "
        " vin, vehicle_desc, estimate_key, is_test) "
        "VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, 1)",
        ("DOC-T", "24h", "+15551110009", "Testy McTest", now + 3600, now - 60,
         "VINTEST", "99 Test", TEST_KEY),
    )
    con.commit()
    con.close()

    srv = HTTPServer(("127.0.0.1", 0), app.WebhookHandler)
    threading.Thread(target=srv.serve_forever, daemon=True).start()
    base = f"http://127.0.0.1:{srv.server_address[1]}"

    yield {"app": app, "fake": fake, "base": base, "db": db_path,
           "token_path": token_path, "gbase": gbase}

    srv.shutdown(); srv.server_close()
    gsrv.shutdown(); gsrv.server_close()


# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------

class _NoRedirect(HTTPErrorProcessor):
    def http_response(self, request, response):
        return response
    https_response = http_response


def get_raw(url):
    """GET without following redirects -> (status, headers)."""
    opener = build_opener(_NoRedirect)
    with opener.open(Request(url), timeout=3) as r:
        return r.status, r.headers


def post_json(url, data):
    raw = json.dumps(data).encode()
    req = Request(url, data=raw, method="POST", headers={"Content-Type": "application/json"})
    with urlopen(req, timeout=3) as r:
        return r.status, json.loads(r.read().decode() or "{}")


def get_json(url):
    with urlopen(Request(url), timeout=3) as r:
        return json.loads(r.read().decode())


def write_token(env, refresh="rt-1", access="at-1", expires_in=3600):
    with open(env["token_path"], "w") as fh:
        json.dump({"refresh_token": refresh, "access_token": access,
                   "expires_at": int(time.time()) + expires_in,
                   "email": "marco@example.com"}, fh)


def book(env, key=KEY, at=None):
    at = at or int(time.time()) + 3 * 86400
    return post_json(f"{env['base']}/earlscheibconcord/queue/book",
                     {"estimate_key": key, "appointment_at": at})


def wait_for(pred, timeout=4.0):
    deadline = time.time() + timeout
    while time.time() < deadline:
        if pred():
            return True
        time.sleep(0.05)
    return pred()


def mapping_rows(env):
    con = sqlite3.connect(env["db"])
    try:
        return con.execute("SELECT estimate_key, event_id, appointment_at FROM gcal_events").fetchall()
    finally:
        con.close()


# ---------------------------------------------------------------------------
# OAuth flow
# ---------------------------------------------------------------------------

def test_connect_redirects_to_google_with_state(gcal_env):
    status, headers = get_raw(f"{gcal_env['base']}/earlscheibconcord/gcal/connect")
    assert status == 302
    loc = headers["Location"]
    assert loc.startswith(f"{gcal_env['gbase']}/auth?")
    qs = parse_qs(urlparse(loc).query)
    assert qs["client_id"] == ["cid-test"]
    assert qs["access_type"] == ["offline"]
    assert qs["prompt"] == ["consent"]
    assert "calendar.events" in qs["scope"][0]
    assert qs["redirect_uri"] == ["http://app.test/earlscheibconcord/gcal/callback"]
    state = qs["state"][0]
    assert len(state) > 20
    stored = gcal_env["app"]._get_setting("gcal_oauth_state", "")
    assert stored.startswith(state + ":")


def test_connect_requires_client_config(gcal_env, monkeypatch):
    monkeypatch.setattr(gcal_env["app"], "GCAL_CLIENT_ID", "")
    with pytest.raises(HTTPError) as ei:
        urlopen(f"{gcal_env['base']}/earlscheibconcord/gcal/connect", timeout=3)
    assert ei.value.code == 503
    d = get_json(f"{gcal_env['base']}/earlscheibconcord/diagnostic")
    assert d["gcal"] is None


def test_callback_rejects_bad_state(gcal_env):
    get_raw(f"{gcal_env['base']}/earlscheibconcord/gcal/connect")   # sets a nonce
    status, headers = get_raw(
        f"{gcal_env['base']}/earlscheibconcord/gcal/callback?code=good-code&state=wrong")
    assert status == 302
    assert headers["Location"].startswith("/earlscheib?gcal=error")
    assert not os.path.exists(gcal_env["token_path"])
    # The nonce is single-use: a replay with the right state must also fail.
    assert gcal_env["app"]._get_setting("gcal_oauth_state", "") == ""


def test_callback_connects_and_backfills(gcal_env):
    app, fake = gcal_env["app"], gcal_env["fake"]
    at = int(time.time()) + 2 * 86400
    assert book(gcal_env, at=at)[0] == 200          # booked before connecting
    assert fake.live_events() == []                 # not connected yet: nothing sent

    _, h = get_raw(f"{gcal_env['base']}/earlscheibconcord/gcal/connect")
    state = parse_qs(urlparse(h["Location"]).query)["state"][0]
    status, h2 = get_raw(
        f"{gcal_env['base']}/earlscheibconcord/gcal/callback?code=good-code&state={state}")
    assert status == 302
    assert h2["Location"] == "/earlscheib?gcal=connected"

    tok = json.load(open(gcal_env["token_path"]))
    assert tok["refresh_token"] == "rt-1"
    assert tok["email"] == "marco@example.com"
    assert stat.S_IMODE(os.stat(gcal_env["token_path"]).st_mode) == 0o600

    # The callback kicks a sync: the existing upcoming booking is backfilled.
    assert wait_for(lambda: len(fake.live_events()) == 1)
    ev = fake.live_events()[0]
    assert ev["summary"] == "Earl Scheib Of Concord: Alice Example (13 Camry)"
    assert ev["extendedProperties"]["private"]["esw_key"] == KEY
    assert ev["start"]["timeZone"] == "America/Los_Angeles"
    assert ev["end"]["timeZone"] == "America/Los_Angeles"
    assert "(555) 111-0001" in ev["description"]
    assert "RO RO-77" in ev["description"]
    assert wait_for(lambda: len(mapping_rows(gcal_env)) == 1)
    assert mapping_rows(gcal_env)[0][1] == ev["id"]

    st = get_json(f"{gcal_env['base']}/earlscheibconcord/gcal/status")
    assert st["connected"] is True
    assert st["account"] == "marco@example.com"
    assert st["events"] == 1
    assert st["last_error"] == ""
    d = get_json(f"{gcal_env['base']}/earlscheibconcord/diagnostic")
    assert d["gcal"]["connected"] is True


# ---------------------------------------------------------------------------
# Reconcile pass
# ---------------------------------------------------------------------------

def test_sync_create_update_delete(gcal_env, monkeypatch):
    app, fake = gcal_env["app"], gcal_env["fake"]
    monkeypatch.setattr(app, "_gcal_kick", lambda: None)   # drive sync by hand
    write_token(gcal_env)
    at = int(time.time()) + 3 * 86400
    assert book(gcal_env, at=at)[0] == 200

    s = app._gcal_sync_once()
    assert (s["created"], s["updated"], s["deleted"]) == (1, 0, 0)
    assert len(fake.live_events()) == 1
    eid = fake.live_events()[0]["id"]

    # Unchanged booking: a second pass makes no Google write.
    before = len(fake.calls)
    s = app._gcal_sync_once()
    assert (s["created"], s["updated"], s["deleted"]) == (0, 0, 0)
    assert [c for c in fake.calls[before:] if c[0] in ("POST", "PATCH", "DELETE")] == []

    # Change date -> PATCH the same event.
    at2 = at + 7200
    assert book(gcal_env, at=at2)[0] == 200
    s = app._gcal_sync_once()
    assert (s["created"], s["updated"], s["deleted"]) == (0, 1, 0)
    assert len(fake.live_events()) == 1
    assert fake.events[eid]["start"]["dateTime"] != ""
    assert mapping_rows(gcal_env)[0][2] == at2
    assert any(c[0] == "PATCH" and c[1].endswith(f"/{eid}") for c in fake.calls)

    # Unmark -> DELETE and forget the mapping.
    status, body = post_json(f"{gcal_env['base']}/earlscheibconcord/queue/unbook",
                             {"estimate_key": KEY})
    assert status == 200 and body["unbooked"] == 1
    s = app._gcal_sync_once()
    assert (s["created"], s["updated"], s["deleted"]) == (0, 0, 1)
    assert fake.live_events() == []
    assert mapping_rows(gcal_env) == []


def test_sync_skips_test_rows_and_past_bookings(gcal_env, monkeypatch):
    app, fake = gcal_env["app"], gcal_env["fake"]
    monkeypatch.setattr(app, "_gcal_kick", lambda: None)
    write_token(gcal_env)
    now = int(time.time())
    con = sqlite3.connect(gcal_env["db"])
    con.execute("INSERT INTO appointments (estimate_key, appointment_at, created_at, updated_at, unbooked_at) "
                "VALUES (?, ?, ?, ?, 0)", (TEST_KEY, now + 86400, now, now))
    con.execute("INSERT INTO appointments (estimate_key, appointment_at, created_at, updated_at, unbooked_at) "
                "VALUES (?, ?, ?, ?, 0)", (KEY, now - 5 * 86400, now - 6 * 86400, now - 6 * 86400))
    con.commit(); con.close()
    s = app._gcal_sync_once()
    assert s["created"] == 0
    assert s["skipped"] == 2
    assert fake.live_events() == []


def test_sync_adopts_event_left_by_a_crash(gcal_env, monkeypatch):
    """Insert succeeded but the id was never recorded: find it by esw_key
    and PATCH instead of creating a duplicate."""
    app, fake = gcal_env["app"], gcal_env["fake"]
    monkeypatch.setattr(app, "_gcal_kick", lambda: None)
    write_token(gcal_env)
    fake.events["ev-orphan"] = {"id": "ev-orphan", "status": "confirmed", "summary": "old",
                                "extendedProperties": {"private": {"esw_key": KEY}}}
    assert book(gcal_env)[0] == 200
    s = app._gcal_sync_once()
    assert s["created"] == 1
    assert len(fake.live_events()) == 1
    assert fake.events["ev-orphan"]["summary"].startswith("Earl Scheib Of Concord: Alice")
    assert not any(c[0] == "POST" and c[1].endswith("/events") for c in fake.calls)
    assert mapping_rows(gcal_env)[0][1] == "ev-orphan"


def test_sync_recreates_event_marco_deleted_by_hand(gcal_env, monkeypatch):
    app, fake = gcal_env["app"], gcal_env["fake"]
    monkeypatch.setattr(app, "_gcal_kick", lambda: None)
    write_token(gcal_env)
    at = int(time.time()) + 3 * 86400
    assert book(gcal_env, at=at)[0] == 200
    app._gcal_sync_once()
    eid = mapping_rows(gcal_env)[0][1]
    fake.events[eid]["status"] = "cancelled"          # deleted in Google Calendar
    assert book(gcal_env, at=at + 3600)[0] == 200     # date changed in the app
    s = app._gcal_sync_once()
    assert s["updated"] == 1
    live = fake.live_events()
    assert len(live) == 1 and live[0]["id"] != eid
    assert mapping_rows(gcal_env)[0][1] == live[0]["id"]


def test_access_token_refreshes_when_expired(gcal_env, monkeypatch):
    app, fake = gcal_env["app"], gcal_env["fake"]
    monkeypatch.setattr(app, "_gcal_kick", lambda: None)
    write_token(gcal_env, access="at-stale", expires_in=-10)
    assert book(gcal_env)[0] == 200
    s = app._gcal_sync_once()
    assert s["created"] == 1
    refreshes = [c for c in fake.calls if c[1] == "/token" and c[2].get("grant_type") == "refresh_token"]
    assert len(refreshes) == 1
    assert refreshes[0][2]["refresh_token"] == "rt-1"
    assert json.load(open(gcal_env["token_path"]))["access_token"] == "at-2"


def test_revoked_refresh_token_disconnects(gcal_env, monkeypatch):
    app = gcal_env["app"]
    monkeypatch.setattr(app, "_gcal_kick", lambda: None)
    write_token(gcal_env, refresh="rt-revoked", access="", expires_in=-10)
    assert book(gcal_env)[0] == 200
    s = app._gcal_sync_once()
    assert s["errors"] == 1 and s["created"] == 0
    assert not os.path.exists(gcal_env["token_path"])
    st = get_json(f"{gcal_env['base']}/earlscheibconcord/gcal/status")
    assert st["connected"] is False
    assert "Connect" in st["last_error"]


# ---------------------------------------------------------------------------
# Endpoints + kicks
# ---------------------------------------------------------------------------

def test_book_and_unbook_kick_sync(gcal_env):
    fake = gcal_env["fake"]
    write_token(gcal_env)
    assert book(gcal_env)[0] == 200
    assert wait_for(lambda: len(fake.live_events()) == 1)
    post_json(f"{gcal_env['base']}/earlscheibconcord/queue/unbook", {"estimate_key": KEY})
    assert wait_for(lambda: fake.live_events() == [])


def test_sync_endpoint(gcal_env):
    fake = gcal_env["fake"]
    with pytest.raises(HTTPError) as ei:
        post_json(f"{gcal_env['base']}/earlscheibconcord/gcal/sync", {})
    assert ei.value.code == 409
    write_token(gcal_env)
    con = sqlite3.connect(gcal_env["db"]); now = int(time.time())
    con.execute("INSERT INTO appointments (estimate_key, appointment_at, created_at, updated_at, unbooked_at) "
                "VALUES (?, ?, ?, ?, 0)", (KEY, now + 86400, now, now))
    con.commit(); con.close()
    status, body = post_json(f"{gcal_env['base']}/earlscheibconcord/gcal/sync", {})
    assert status == 202 and body["started"] is True
    assert wait_for(lambda: len(fake.live_events()) == 1)


def test_disconnect_forgets_token_and_mapping_but_keeps_events(gcal_env, monkeypatch):
    app, fake = gcal_env["app"], gcal_env["fake"]
    monkeypatch.setattr(app, "_gcal_kick", lambda: None)
    write_token(gcal_env)
    assert book(gcal_env)[0] == 200
    app._gcal_sync_once()
    assert len(mapping_rows(gcal_env)) == 1
    status, body = post_json(f"{gcal_env['base']}/earlscheibconcord/gcal/disconnect", {})
    assert status == 200 and body["disconnected"] is True
    assert not os.path.exists(gcal_env["token_path"])
    assert mapping_rows(gcal_env) == []
    assert len(fake.live_events()) == 1               # left on the calendar
    assert any(c[1] == "/revoke" for c in fake.calls)
    st = get_json(f"{gcal_env['base']}/earlscheibconcord/gcal/status")
    assert st == {"configured": True, "connected": False, "enabled": True, "account": "",
                  "last_sync": 0, "last_error": "", "events": 0}


# ---------------------------------------------------------------------------
# CAL-05: pause / resume toggle
# ---------------------------------------------------------------------------

def test_toggle_off_pauses_sync_and_on_resumes(gcal_env):
    fake, base = gcal_env["fake"], gcal_env["base"]
    write_token(gcal_env)
    st = get_json(f"{base}/earlscheibconcord/gcal/status")
    assert st["connected"] is True and st["enabled"] is True   # default ON

    status, body = post_json(f"{base}/earlscheibconcord/gcal/enable", {"enabled": False})
    assert status == 200 and body["enabled"] is False and body["connected"] is True
    assert os.path.exists(gcal_env["token_path"])             # sign-in kept

    # Booking while paused: nothing reaches Google.
    assert book(gcal_env)[0] == 200
    time.sleep(0.6)
    assert fake.live_events() == []
    assert not any(c[1].endswith("/events") for c in fake.calls)
    # An explicit sync pass is a no-op too.
    assert gcal_env["app"]._gcal_sync_once()["created"] == 0

    # Resume: the kick catches the booking up.
    status, body = post_json(f"{base}/earlscheibconcord/gcal/enable", {"enabled": True})
    assert status == 200 and body["enabled"] is True
    assert wait_for(lambda: len(fake.live_events()) == 1)


def test_toggle_rejects_bad_body(gcal_env):
    with pytest.raises(HTTPError) as ei:
        post_json(f"{gcal_env['base']}/earlscheibconcord/gcal/enable", {"nope": 1})
    assert ei.value.code == 400


def test_pause_does_not_survive_disconnect_or_reconnect(gcal_env):
    """A pause belongs to the sign-in it was set under. Disconnecting clears
    it, and a fresh Google sign-in always starts syncing."""
    base, fake = gcal_env["base"], gcal_env["fake"]
    write_token(gcal_env)
    post_json(f"{base}/earlscheibconcord/gcal/enable", {"enabled": False})
    assert get_json(f"{base}/earlscheibconcord/gcal/status")["enabled"] is False

    post_json(f"{base}/earlscheibconcord/gcal/disconnect", {})
    assert get_json(f"{base}/earlscheibconcord/gcal/status")["enabled"] is True

    # Pause again (connected), then reconnect through the OAuth callback.
    write_token(gcal_env)
    post_json(f"{base}/earlscheibconcord/gcal/enable", {"enabled": False})
    assert book(gcal_env)[0] == 200
    time.sleep(0.4)
    assert fake.live_events() == []                 # paused: nothing sent
    _, h = get_raw(f"{base}/earlscheibconcord/gcal/connect")
    state = parse_qs(urlparse(h["Location"]).query)["state"][0]
    status, _ = get_raw(f"{base}/earlscheibconcord/gcal/callback?code=good-code&state={state}")
    assert status == 302
    st = get_json(f"{base}/earlscheibconcord/gcal/status")
    assert st["connected"] is True and st["enabled"] is True
    assert wait_for(lambda: len(fake.live_events()) == 1)   # backfilled on reconnect


def test_queue_rows_carry_gcal_synced_flag(gcal_env, monkeypatch):
    app = gcal_env["app"]
    monkeypatch.setattr(app, "_gcal_kick", lambda: None)
    write_token(gcal_env)
    assert book(gcal_env)[0] == 200
    rows = get_json(f"{gcal_env['base']}/earlscheibconcord/queue?status=all")
    mine = [r for r in rows if r["estimate_key"] == KEY]
    assert mine and all(r["booked"] == 1 and r["gcal_synced"] == 0 for r in mine)
    app._gcal_sync_once()
    rows = get_json(f"{gcal_env['base']}/earlscheibconcord/queue?status=all")
    mine = [r for r in rows if r["estimate_key"] == KEY]
    assert mine and all(r["gcal_synced"] == 1 for r in mine)
    other = [r for r in rows if r["estimate_key"] != KEY]
    assert all(r["gcal_synced"] == 0 for r in other)
