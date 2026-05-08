"""Integration tests for SPN-01 /earlscheibconcord/schedules endpoints.

GET returns default delay_hours + overrides + bounds.
PUT with valid integer upserts and rebases pending jobs (SPN-02).
PUT with empty body / null / missing field deletes row (revert).
PUT with out-of-bounds, non-integer, unknown job_type, bad HMAC → 400/401.

Mirrors tests/test_templates_endpoint.py exactly. Uses the queue_server
fixture from conftest which seeds Alice (24h)/Bob (3day)/Carol (review)
pending plus Dave (24h sent).
"""
import json
import sqlite3
import time
from urllib.request import Request, urlopen
from urllib.error import HTTPError

from tests.conftest import sign


BASE = "/earlscheibconcord/schedules"


def _get_schedules(qs):
    sig = sign(qs["secret"], b"")
    req = Request(f"{qs['base_url']}{BASE}", headers={"X-EMS-Signature": sig})
    with urlopen(req, timeout=3) as resp:
        assert resp.status == 200
        return json.loads(resp.read().decode("utf-8"))


def _put_schedule(qs, job_type, body_dict):
    """PUT helper — pass body_dict=None to send empty body."""
    if body_dict is None:
        raw = b""
    else:
        raw = json.dumps(body_dict).encode("utf-8")
    sig = sign(qs["secret"], raw)
    req = Request(
        f"{qs['base_url']}{BASE}/{job_type}",
        data=raw, method="PUT",
        headers={
            "X-EMS-Signature": sig,
            "Content-Type": "application/json",
        },
    )
    return urlopen(req, timeout=3)


# ---------- GET ----------

def test_get_schedules_default_delays(queue_server):
    body = _get_schedules(queue_server)
    assert set(body.keys()) == {"job_types", "min_hours", "max_hours"}
    assert body["min_hours"] == 1
    assert body["max_hours"] == 720
    assert len(body["job_types"]) == 3
    by_type = {jt["job_type"]: jt for jt in body["job_types"]}
    assert set(by_type.keys()) == {"24h", "3day", "review"}
    assert by_type["24h"]["delay_hours"] == 24
    assert by_type["3day"]["delay_hours"] == 72
    assert by_type["review"]["delay_hours"] == 24
    for jt in body["job_types"]:
        assert jt["is_override"] is False
        assert jt["updated_at"] == 0
        assert jt["label"]
        assert jt["when"]


def test_get_schedules_bad_signature(queue_server):
    req = Request(
        f"{queue_server['base_url']}{BASE}",
        headers={"X-EMS-Signature": "00" * 32},
    )
    try:
        urlopen(req, timeout=3)
        assert False, "expected 401"
    except HTTPError as e:
        assert e.code == 401


def test_get_schedules_missing_signature(queue_server):
    req = Request(f"{queue_server['base_url']}{BASE}")
    try:
        urlopen(req, timeout=3)
        assert False, "expected 401"
    except HTTPError as e:
        assert e.code == 401


# ---------- PUT happy path ----------

def test_put_schedule_upsert_override(queue_server):
    with _put_schedule(queue_server, "24h", {"delay_hours": 48}) as resp:
        assert resp.status == 200
        parsed = json.loads(resp.read().decode("utf-8"))
    assert parsed["is_override"] is True
    assert parsed["delay_hours"] == 48
    assert parsed["updated_at"] > 0
    assert "rebased_jobs" in parsed

    # Subsequent GET reflects the override.
    body = _get_schedules(queue_server)
    by_type = {jt["job_type"]: jt for jt in body["job_types"]}
    assert by_type["24h"]["is_override"] is True
    assert by_type["24h"]["delay_hours"] == 48
    # Other rows unaffected.
    assert by_type["3day"]["is_override"] is False
    assert by_type["3day"]["delay_hours"] == 72
    assert by_type["review"]["is_override"] is False
    assert by_type["review"]["delay_hours"] == 24


def test_put_schedule_empty_body_reverts_to_default(queue_server):
    # First install an override so there's something to revert.
    _put_schedule(queue_server, "3day", {"delay_hours": 96}).close()

    # Now revert via empty body (no Content-Length).
    with _put_schedule(queue_server, "3day", None) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    assert parsed["is_override"] is False
    assert parsed["delay_hours"] == 72  # default
    assert parsed["updated_at"] == 0

    # DB row deleted.
    con = sqlite3.connect(queue_server["db_path"])
    count = con.execute(
        "SELECT COUNT(*) FROM schedules WHERE job_type='3day'"
    ).fetchone()[0]
    con.close()
    assert count == 0


def test_put_schedule_null_delay_reverts(queue_server):
    _put_schedule(queue_server, "review", {"delay_hours": 36}).close()
    with _put_schedule(queue_server, "review", {"delay_hours": None}) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    assert parsed["is_override"] is False
    assert parsed["delay_hours"] == 24  # default for review


def test_put_schedule_missing_field_reverts(queue_server):
    _put_schedule(queue_server, "24h", {"delay_hours": 36}).close()
    with _put_schedule(queue_server, "24h", {}) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    assert parsed["is_override"] is False
    assert parsed["delay_hours"] == 24


# ---------- PUT bounds ----------

def test_put_schedule_zero_rejected(queue_server):
    try:
        _put_schedule(queue_server, "24h", {"delay_hours": 0})
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400


def test_put_schedule_above_max_rejected(queue_server):
    try:
        _put_schedule(queue_server, "24h", {"delay_hours": 721})
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400


def test_put_schedule_negative_rejected(queue_server):
    try:
        _put_schedule(queue_server, "24h", {"delay_hours": -1})
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400


def test_put_schedule_at_min_accepted(queue_server):
    with _put_schedule(queue_server, "24h", {"delay_hours": 1}) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    assert parsed["delay_hours"] == 1
    assert parsed["is_override"] is True


def test_put_schedule_at_max_accepted(queue_server):
    with _put_schedule(queue_server, "24h", {"delay_hours": 720}) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    assert parsed["delay_hours"] == 720


# ---------- PUT type validation ----------

def test_put_schedule_string_rejected(queue_server):
    try:
        _put_schedule(queue_server, "24h", {"delay_hours": "24"})
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400


def test_put_schedule_float_rejected(queue_server):
    try:
        _put_schedule(queue_server, "24h", {"delay_hours": 1.5})
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400


def test_put_schedule_bool_rejected(queue_server):
    try:
        _put_schedule(queue_server, "24h", {"delay_hours": True})
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400


def test_put_schedule_unknown_job_type(queue_server):
    try:
        _put_schedule(queue_server, "foo", {"delay_hours": 24})
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400
        body = json.loads(e.read().decode("utf-8"))
        assert "unknown" in body["error"].lower()


def test_put_schedule_bad_signature(queue_server):
    raw = json.dumps({"delay_hours": 24}).encode("utf-8")
    req = Request(
        f"{queue_server['base_url']}{BASE}/24h",
        data=raw, method="PUT",
        headers={
            "X-EMS-Signature": "00" * 32,
            "Content-Type": "application/json",
        },
    )
    try:
        urlopen(req, timeout=3)
        assert False, "expected 401"
    except HTTPError as e:
        assert e.code == 401


# ---------- SPN-02: rebase-on-PUT ----------

def test_put_schedule_rebases_pending_jobs(queue_server):
    """The critical contract: a PUT against /schedules/24h MUST rebase the
    send_at of every pending 24h row to next_send_window(created_at + new_delay).
    Sent rows and rows of other job_types must be untouched.
    """
    # Seed three known-time 24h pending rows with deterministic created_at,
    # plus one 3day pending and one 24h sent — those must NOT be touched.
    con = sqlite3.connect(queue_server["db_path"])
    con.row_factory = sqlite3.Row
    cur = con.cursor()
    base_created = 1700000000  # arbitrary epoch; we just need stable arithmetic
    cur.executemany(
        "INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at) "
        "VALUES (?, ?, ?, ?, ?, ?, ?)",
        [
            ("SPN-A1", "24h",  "+15551112221", "Anna",  base_created + 24*3600, 0, base_created),
            ("SPN-A2", "24h",  "+15551112222", "Beth",  base_created + 24*3600, 0, base_created + 100),
            ("SPN-A3", "24h",  "+15551112223", "Cara",  base_created + 24*3600, 0, base_created + 200),
            ("SPN-B1", "3day", "+15551112224", "Dana",  base_created + 72*3600, 0, base_created + 300),
            ("SPN-A4", "24h",  "+15551112225", "Erin",  base_created + 24*3600, 1, base_created + 400),  # sent
        ],
    )
    con.commit()
    con.close()

    # PUT with delay_hours=48 — rebases all SPN-A1/A2/A3 (pending 24h) but
    # leaves SPN-B1 (3day) and SPN-A4 (sent) alone.
    with _put_schedule(queue_server, "24h", {"delay_hours": 48}) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))

    assert parsed["is_override"] is True
    assert parsed["delay_hours"] == 48
    # rebased_jobs is at least 3 (the test rows we seeded). Could be higher
    # since the conftest fixture seeds Alice (24h pending) too, plus
    # _seed_test_jobs_if_missing inserts Carlos (24h test pending). Ensure
    # the count is >= 3, then verify behaviour by row.
    assert parsed["rebased_jobs"] >= 3

    # Reload module-side function to compute expected next_send_window.
    import app as app_mod
    expected_a1 = app_mod.next_send_window(base_created + 48 * 3600)
    expected_a2 = app_mod.next_send_window(base_created + 100 + 48 * 3600)
    expected_a3 = app_mod.next_send_window(base_created + 200 + 48 * 3600)
    # Other job_types use their own delay; SPN-B1 must keep its original send_at.
    expected_b1_before = base_created + 72 * 3600
    expected_a4_before = base_created + 24 * 3600  # sent row

    con = sqlite3.connect(queue_server["db_path"])
    con.row_factory = sqlite3.Row
    rows = {
        r["doc_id"]: r
        for r in con.execute(
            "SELECT doc_id, send_at, sent FROM jobs "
            "WHERE doc_id LIKE 'SPN-%'"
        ).fetchall()
    }
    con.close()

    assert rows["SPN-A1"]["send_at"] == expected_a1
    assert rows["SPN-A2"]["send_at"] == expected_a2
    assert rows["SPN-A3"]["send_at"] == expected_a3
    # 3day row untouched.
    assert rows["SPN-B1"]["send_at"] == expected_b1_before
    # Sent 24h row untouched.
    assert rows["SPN-A4"]["send_at"] == expected_a4_before
    assert rows["SPN-A4"]["sent"] == 1


def test_put_schedule_revert_rebases_to_default(queue_server):
    """Reverting an override (empty body) must rebase pending jobs to the
    DEFAULT_SCHEDULES delay, not leave them at the previous override."""
    # Seed a known 24h pending row.
    con = sqlite3.connect(queue_server["db_path"])
    cur = con.cursor()
    base_created = 1700000000
    cur.execute(
        "INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at) "
        "VALUES ('SPN-REV', '24h', '+15551112299', 'Revert', ?, 0, ?)",
        (base_created + 1, base_created),
    )
    con.commit()
    con.close()

    # First override → 96 hours.
    _put_schedule(queue_server, "24h", {"delay_hours": 96}).close()

    # Now revert.
    with _put_schedule(queue_server, "24h", None) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    assert parsed["is_override"] is False
    assert parsed["delay_hours"] == 24

    import app as app_mod
    expected = app_mod.next_send_window(base_created + 24 * 3600)
    con = sqlite3.connect(queue_server["db_path"])
    row_send_at = con.execute(
        "SELECT send_at FROM jobs WHERE doc_id='SPN-REV'"
    ).fetchone()[0]
    con.close()
    assert row_send_at == expected


# ---------- End-to-end: override flows through get_effective_schedule ----------

def test_override_flows_through_get_effective_schedule(queue_server):
    """After a PUT saves an override, app.get_effective_schedule(jt) returns
    the new value. This is the core ingestion-path contract."""
    import importlib
    import app as app_mod
    importlib.reload(app_mod)  # pick up the DB path the fixture set

    _put_schedule(queue_server, "review", {"delay_hours": 12}).close()
    assert app_mod.get_effective_schedule("review") == 12
    # Other types still default.
    assert app_mod.get_effective_schedule("24h") == 24
    assert app_mod.get_effective_schedule("3day") == 72
