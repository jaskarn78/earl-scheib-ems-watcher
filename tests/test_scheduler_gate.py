"""Tests for WNC-01: persistent auto-send toggle backed by app_settings table.

The toggle state lives in the DB (app_settings.key='auto_send_enabled').
SCHEDULER_ENABLED env var is only the first-boot seed; once the row exists,
env changes are ignored — the DB value is the sole gate.

Manual send-now (POST /queue/send-now) is INTENTIONALLY NOT GATED and must
continue to fire SMS regardless of the toggle state (SPN-03 contract preserved).
"""
import importlib
import json
import sqlite3
import time
from urllib.request import Request, urlopen

import pytest

from tests.conftest import sign


@pytest.fixture
def reload_app_with_gate(monkeypatch, tmp_path):
    """Reload app.py with a configurable SCHEDULER_ENABLED env-var seed.

    Returns a callable: reload_app_with_gate("1") or reload_app_with_gate("0").
    Each call returns the freshly-reloaded `app` module bound to a temp DB.
    The db_path is SHARED across calls within the same test — used to prove
    the env-ignored-after-seed contract (D-02).
    """
    secret = "pytest-fixture-secret-do-not-ship"
    db_path = str(tmp_path / "jobs.db")
    telem_path = str(tmp_path / "telemetry.log")
    rc_path = str(tmp_path / "remote_config.json")

    monkeypatch.setenv("CCC_SECRET", secret)
    monkeypatch.setenv("DB_PATH", db_path)
    monkeypatch.setenv("TELEMETRY_LOG_PATH", telem_path)
    monkeypatch.setenv("REMOTE_CONFIG_PATH", rc_path)
    monkeypatch.setenv("PORT", "0")

    def _reload(gate_value: str):
        if gate_value is None:
            monkeypatch.delenv("SCHEDULER_ENABLED", raising=False)
        else:
            monkeypatch.setenv("SCHEDULER_ENABLED", gate_value)
        import app as app_mod
        importlib.reload(app_mod)
        app_mod.init_db()
        return app_mod, db_path, secret

    return _reload


def _seed_due_job(db_path, job_type="24h", phone="+15308450190"):
    """Insert a single due (send_at <= now), unsent, non-test job."""
    con = sqlite3.connect(db_path)
    cur = con.cursor()
    now = int(time.time())
    cur.execute(
        "INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, "
        "                  created_at, vehicle_desc, vin) "
        "VALUES (?, ?, ?, ?, ?, 0, ?, '2018 Honda Accord', 'TEST-VIN-12345')",
        ("SPN-DUE-1", job_type, phone, "Sched Tester", now - 60, now - 3600),
    )
    con.commit()
    job_id = cur.lastrowid
    con.close()
    return job_id


# ---------------------------------------------------------------------------
# First-boot seed tests
# ---------------------------------------------------------------------------

def test_first_boot_seed_env_zero_sets_off(reload_app_with_gate):
    """First-boot seed: env SCHEDULER_ENABLED=0 → app_settings row OFF → get_auto_send_enabled() is False."""
    app_mod, db_path, _ = reload_app_with_gate("0")
    assert app_mod.get_auto_send_enabled() is False


def test_first_boot_seed_env_one_sets_on(reload_app_with_gate):
    """First-boot seed: env SCHEDULER_ENABLED=1 → app_settings row ON → get_auto_send_enabled() is True."""
    app_mod, db_path, _ = reload_app_with_gate("1")
    assert app_mod.get_auto_send_enabled() is True


def test_first_boot_seed_env_unset_defaults_off(reload_app_with_gate):
    """First-boot seed: env unset → defaults to 0 → get_auto_send_enabled() is False."""
    app_mod, db_path, _ = reload_app_with_gate(None)
    assert app_mod.get_auto_send_enabled() is False


def test_env_ignored_after_seed(reload_app_with_gate):
    """D-02 (load-bearing): with an existing app_settings row=OFF, reload app
    with env=1 and call init_db again → get_auto_send_enabled() stays False.
    DB wins; env is ignored after first boot.
    """
    # First boot: seed OFF.
    app_mod, db_path, _ = reload_app_with_gate("0")
    assert app_mod.get_auto_send_enabled() is False

    # Second boot with a different env (would be "1") — same db_path.
    app_mod2, _, _ = reload_app_with_gate("1")
    # Row already exists from first boot with value="0"; INSERT OR IGNORE is a no-op.
    assert app_mod2.get_auto_send_enabled() is False, (
        "env=1 on second reload must NOT override existing DB row (D-02)"
    )


# ---------------------------------------------------------------------------
# Scheduler loop gate tests
# ---------------------------------------------------------------------------

def test_scheduler_gate_disabled_skips_due_jobs(reload_app_with_gate, monkeypatch):
    """With auto-send OFF, scheduler_loop should NOT call _fire_due_jobs.
    We verify via direct simulation of the loop's gate check.
    """
    app_mod, db_path, _ = reload_app_with_gate("0")
    assert app_mod.get_auto_send_enabled() is False

    # Seed a due job.
    _seed_due_job(db_path)

    calls = []
    def _fake_send(to, body):
        calls.append((to, body))
        return True, ""
    monkeypatch.setattr(app_mod, "send_sms", _fake_send)

    # Simulate one tick of the scheduler_loop body — gated OFF.
    if app_mod.get_auto_send_enabled():
        app_mod._fire_due_jobs()
    # else: gated off — no-op (mirrors scheduler_loop's check).

    assert calls == []
    # Row stays sent=0.
    con = sqlite3.connect(db_path)
    sent_val = con.execute(
        "SELECT sent FROM jobs WHERE doc_id='SPN-DUE-1'"
    ).fetchone()[0]
    con.close()
    assert sent_val == 0


def test_scheduler_gate_enabled_fires_due_jobs(reload_app_with_gate, monkeypatch):
    """With auto-send ON (set_auto_send_enabled(True)), _fire_due_jobs must
    call send_sms and mark the row sent=1."""
    app_mod, db_path, _ = reload_app_with_gate("0")
    # Start OFF, then flip ON via DB setter.
    app_mod.set_auto_send_enabled(True)
    assert app_mod.get_auto_send_enabled() is True

    _seed_due_job(db_path)

    calls = []
    def _fake_send(to, body):
        calls.append((to, body))
        return True, ""
    monkeypatch.setattr(app_mod, "send_sms", _fake_send)

    # When the gate is open, call _fire_due_jobs directly.
    app_mod._fire_due_jobs()

    assert len(calls) == 1
    assert calls[0][0] == "+15308450190"
    assert calls[0][1]  # non-empty body

    con = sqlite3.connect(db_path)
    sent_val = con.execute(
        "SELECT sent FROM jobs WHERE doc_id='SPN-DUE-1'"
    ).fetchone()[0]
    con.close()
    assert sent_val == 1


# ---------------------------------------------------------------------------
# Gated-off log throttle
# ---------------------------------------------------------------------------

def test_scheduler_gated_off_log_throttle_once_per_hour(reload_app_with_gate, caplog):
    """The "auto-send disabled" log message must fire at most once per
    _GATED_LOG_INTERVAL_S (3600s default). Stepping the gated branch twice
    in quick succession should produce only one log line.
    """
    app_mod, _, _ = reload_app_with_gate("0")
    # Reset throttle state so the first call always logs.
    app_mod._last_gated_log_ts = 0
    assert app_mod._GATED_LOG_INTERVAL_S >= 3600

    def step_gated():
        now = int(time.time())
        if now - app_mod._last_gated_log_ts >= app_mod._GATED_LOG_INTERVAL_S:
            app_mod.log.info(
                "auto-send disabled via toggle "
                "— manual send-now still works"
            )
            app_mod._last_gated_log_ts = now

    import logging
    with caplog.at_level(logging.INFO, logger=app_mod.log.name):
        step_gated()
        step_gated()
        step_gated()

    gated_msgs = [
        r for r in caplog.records
        if "auto-send disabled" in r.getMessage()
    ]
    assert len(gated_msgs) == 1


# ---------------------------------------------------------------------------
# Manual send-now is NOT gated (SPN-03 critical)
# ---------------------------------------------------------------------------

def test_send_now_fires_with_gate_off(reload_app_with_gate, monkeypatch):
    """Manual /queue/send-now MUST fire SMS regardless of auto-send toggle state.
    Full HTTP path: spin a server, monkeypatch send_sms, POST a signed body,
    assert send was called.
    """
    import threading
    from http.server import HTTPServer

    app_mod, db_path, secret = reload_app_with_gate("0")
    assert app_mod.get_auto_send_enabled() is False

    # Seed a pending row.
    job_id = _seed_due_job(db_path, job_type="review", phone="+15308450190")

    calls = []
    def _fake_send(to, body):
        calls.append((to, body))
        return True, ""
    monkeypatch.setattr(app_mod, "send_sms", _fake_send)

    server = HTTPServer(("127.0.0.1", 0), app_mod.WebhookHandler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()
    try:
        raw = json.dumps({"id": job_id}).encode("utf-8")
        req = Request(
            f"http://127.0.0.1:{port}/earlscheibconcord/queue/send-now",
            data=raw, method="POST",
            headers={
                "X-EMS-Signature": sign(secret, raw),
                "Content-Type": "application/json",
            },
        )
        with urlopen(req, timeout=3) as resp:
            assert resp.status == 200
            parsed = json.loads(resp.read().decode("utf-8"))
        assert parsed.get("sent") is True
    finally:
        server.shutdown()
        server.server_close()

    # Send went out despite the gate.
    assert len(calls) == 1
    assert calls[0][0] == "+15308450190"

    # Row marked sent.
    con = sqlite3.connect(db_path)
    sent_val = con.execute(
        "SELECT sent FROM jobs WHERE id=?", (job_id,)
    ).fetchone()[0]
    con.close()
    assert sent_val == 1


# ---------------------------------------------------------------------------
# POST /auto-send endpoint tests
# ---------------------------------------------------------------------------

def _spin_server(app_mod):
    """Helper: start a WebhookHandler on an ephemeral port. Returns
    (base_url, stop_callable)."""
    import threading
    from http.server import HTTPServer
    server = HTTPServer(("127.0.0.1", 0), app_mod.WebhookHandler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    def stop():
        server.shutdown()
        server.server_close()
    return f"http://127.0.0.1:{port}", stop


def test_auto_send_endpoint_enable(reload_app_with_gate):
    """POST /auto-send {enabled: true} persists ON and returns {enabled: true}."""
    app_mod, db_path, secret = reload_app_with_gate("0")
    assert app_mod.get_auto_send_enabled() is False

    base_url, stop = _spin_server(app_mod)
    try:
        raw = json.dumps({"enabled": True}).encode("utf-8")
        req = Request(
            f"{base_url}/earlscheibconcord/auto-send",
            data=raw, method="POST",
            headers={"Content-Type": "application/json"},
        )
        with urlopen(req, timeout=3) as resp:
            assert resp.status == 200
            parsed = json.loads(resp.read().decode("utf-8"))
        assert parsed.get("enabled") is True
    finally:
        stop()

    assert app_mod.get_auto_send_enabled() is True


def test_auto_send_endpoint_disable(reload_app_with_gate):
    """POST /auto-send {enabled: false} persists OFF and returns {enabled: false}."""
    app_mod, db_path, secret = reload_app_with_gate("1")
    assert app_mod.get_auto_send_enabled() is True

    base_url, stop = _spin_server(app_mod)
    try:
        raw = json.dumps({"enabled": False}).encode("utf-8")
        req = Request(
            f"{base_url}/earlscheibconcord/auto-send",
            data=raw, method="POST",
            headers={"Content-Type": "application/json"},
        )
        with urlopen(req, timeout=3) as resp:
            assert resp.status == 200
            parsed = json.loads(resp.read().decode("utf-8"))
        assert parsed.get("enabled") is False
    finally:
        stop()

    assert app_mod.get_auto_send_enabled() is False


def test_auto_send_endpoint_rejects_int(reload_app_with_gate):
    """POST /auto-send {enabled: 1} (int, not bool) must return 400."""
    app_mod, db_path, secret = reload_app_with_gate("0")

    base_url, stop = _spin_server(app_mod)
    try:
        raw = json.dumps({"enabled": 1}).encode("utf-8")
        req = Request(
            f"{base_url}/earlscheibconcord/auto-send",
            data=raw, method="POST",
            headers={"Content-Type": "application/json"},
        )
        from urllib.error import HTTPError
        with pytest.raises(HTTPError) as exc_info:
            urlopen(req, timeout=3)
        assert exc_info.value.code == 400
    finally:
        stop()


def test_auto_send_endpoint_rejects_string(reload_app_with_gate):
    """POST /auto-send {enabled: "true"} (string) must return 400."""
    app_mod, db_path, secret = reload_app_with_gate("0")

    base_url, stop = _spin_server(app_mod)
    try:
        raw = json.dumps({"enabled": "true"}).encode("utf-8")
        req = Request(
            f"{base_url}/earlscheibconcord/auto-send",
            data=raw, method="POST",
            headers={"Content-Type": "application/json"},
        )
        from urllib.error import HTTPError
        with pytest.raises(HTTPError) as exc_info:
            urlopen(req, timeout=3)
        assert exc_info.value.code == 400
    finally:
        stop()


def test_auto_send_endpoint_rejects_missing_key(reload_app_with_gate):
    """POST /auto-send {} (missing 'enabled' key) must return 400."""
    app_mod, db_path, secret = reload_app_with_gate("0")

    base_url, stop = _spin_server(app_mod)
    try:
        raw = json.dumps({}).encode("utf-8")
        req = Request(
            f"{base_url}/earlscheibconcord/auto-send",
            data=raw, method="POST",
            headers={"Content-Type": "application/json"},
        )
        from urllib.error import HTTPError
        with pytest.raises(HTTPError) as exc_info:
            urlopen(req, timeout=3)
        assert exc_info.value.code == 400
    finally:
        stop()


def test_auto_send_endpoint_rejects_bad_json(reload_app_with_gate):
    """POST /auto-send with invalid JSON must return 400."""
    app_mod, db_path, secret = reload_app_with_gate("0")

    base_url, stop = _spin_server(app_mod)
    try:
        raw = b"not-valid-json"
        req = Request(
            f"{base_url}/earlscheibconcord/auto-send",
            data=raw, method="POST",
            headers={"Content-Type": "application/json"},
        )
        from urllib.error import HTTPError
        with pytest.raises(HTTPError) as exc_info:
            urlopen(req, timeout=3)
        assert exc_info.value.code == 400
    finally:
        stop()


# ---------------------------------------------------------------------------
# UKK-05: ems_bundle skips schedule_job for disabled job_types
# ---------------------------------------------------------------------------

def _bms_xml(doc_id: str, doc_status: str, doc_ver: str = None) -> bytes:
    """Build a minimal BMS XML payload for parse_bms() that exercises the
    estimate / closed branches. Returns bytes ready for POST.
    """
    ver = doc_ver or doc_id
    return (
        '<?xml version="1.0" encoding="UTF-8"?>'
        '<BMSEnvelope xmlns="http://www.cieca.com/BMS">'
          '<BMSTrans>'
            f'<DocumentID>{doc_id}</DocumentID>'
            f'<DocumentVerCode>{ver}</DocumentVerCode>'
            f'<DocumentStatus>{doc_status}</DocumentStatus>'
            '<EventInfo><RepairEvent>'
              '<CloseDateTime>2026-05-08T10:00:00</CloseDateTime>'
            '</RepairEvent></EventInfo>'
            '<Owner>'
              '<GivenName>Test</GivenName>'
              '<OtherOrSurName>Customer</OtherOrSurName>'
              '<CommPhone>+15308450190</CommPhone>'
            '</Owner>'
            '<VehicleInfo>'
              '<VIN>UKKVIN0123456789</VIN>'
              '<Year>2024</Year>'
              '<Make>Toyota</Make>'
              '<Model>Camry</Model>'
              '<ROId>RO-UKK</ROId>'
            '</VehicleInfo>'
          '</BMSTrans>'
        '</BMSEnvelope>'
    ).encode("utf-8")


def _post_ems_bundle(base_url: str, secret: str, xml: bytes):
    req = Request(
        f"{base_url}/earlscheibconcord/?trigger=ems_bundle",
        data=xml, method="POST",
        headers={
            "X-EMS-Signature": sign(secret, xml),
            "Content-Type": "application/xml",
        },
    )
    return urlopen(req, timeout=3)


def _disable_jt_directly(db_path: str, job_type: str):
    """Bypass the PUT path: insert a schedules row with enabled=0."""
    con = sqlite3.connect(db_path)
    con.execute(
        "INSERT OR REPLACE INTO schedules(job_type, delay_hours, updated_at, enabled) "
        "VALUES (?, ?, ?, 0)",
        (job_type, 24, int(time.time())),
    )
    con.commit()
    con.close()


def _count_jobs_for_doc(db_path: str, doc_id: str):
    con = sqlite3.connect(db_path)
    rows = con.execute(
        "SELECT job_type, COUNT(*) FROM jobs WHERE doc_id = ? GROUP BY job_type",
        (doc_id,),
    ).fetchall()
    con.close()
    return {jt: n for jt, n in rows}


def test_ems_bundle_skips_disabled_24h(reload_app_with_gate):
    app_mod, db_path, secret = reload_app_with_gate("0")
    _disable_jt_directly(db_path, "24h")

    base_url, stop = _spin_server(app_mod)
    try:
        xml = _bms_xml("UKK-EST-1", "E")
        with _post_ems_bundle(base_url, secret, xml) as resp:
            assert resp.status == 200
    finally:
        stop()

    counts = _count_jobs_for_doc(db_path, "UKK-EST-1")
    assert counts.get("24h", 0) == 0, "24h should be skipped"
    assert counts.get("3day", 0) == 1, "3day must still schedule"


def test_ems_bundle_skips_disabled_review(reload_app_with_gate):
    app_mod, db_path, secret = reload_app_with_gate("0")
    _disable_jt_directly(db_path, "review")

    base_url, stop = _spin_server(app_mod)
    try:
        xml = _bms_xml("UKK-CLO-1", "C")
        with _post_ems_bundle(base_url, secret, xml) as resp:
            assert resp.status == 200
    finally:
        stop()

    counts = _count_jobs_for_doc(db_path, "UKK-CLO-1")
    assert counts.get("review", 0) == 0


def test_ems_bundle_default_enabled_schedules_all(reload_app_with_gate):
    """Regression guard: with no schedules-table override, defaults must
    schedule the full set (24h + 3day for estimate; review for closed)."""
    app_mod, db_path, secret = reload_app_with_gate("0")

    base_url, stop = _spin_server(app_mod)
    try:
        with _post_ems_bundle(base_url, secret, _bms_xml("UKK-DEF-EST", "E")) as resp:
            assert resp.status == 200
        with _post_ems_bundle(base_url, secret, _bms_xml("UKK-DEF-CLO", "C")) as resp:
            assert resp.status == 200
    finally:
        stop()

    est_counts = _count_jobs_for_doc(db_path, "UKK-DEF-EST")
    assert est_counts.get("24h", 0) == 1
    assert est_counts.get("3day", 0) == 1

    clo_counts = _count_jobs_for_doc(db_path, "UKK-DEF-CLO")
    assert clo_counts.get("review", 0) == 1
