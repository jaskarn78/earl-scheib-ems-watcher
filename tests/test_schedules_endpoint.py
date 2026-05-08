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


def test_put_schedule_null_delay_no_change(queue_server):
    """UKK-03 supersedes legacy null-as-revert: explicit null on a partial
    body now means 'do not change this field'. Only empty body / `{}` is a
    full revert."""
    _put_schedule(queue_server, "review", {"delay_hours": 36}).close()
    with _put_schedule(queue_server, "review", {"delay_hours": None}) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    # delay_hours absent-but-null → row stays at the existing override value.
    # However when no other field is provided either, an explicit-null body
    # like {"delay_hours": null} carries zero changes; the server still
    # writes back the row (idempotent UPSERT). Either way: override persists.
    assert parsed["is_override"] is True
    assert parsed["delay_hours"] == 36


def test_put_schedule_empty_dict_reverts(queue_server):
    """Empty `{}` body fully reverts the row (delete)."""
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


# ---------- UKK-01..04: enabled toggle ----------

def _seed_jobs(db_path, rows):
    """Helper: rows is a list of (doc_id, job_type, sent, sent_at, created_at)."""
    con = sqlite3.connect(db_path)
    con.executemany(
        "INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, sent_at, created_at) "
        "VALUES (?, ?, '+15551112299', 'Tester', ?, ?, ?, ?)",
        [(d, jt, c + 3600, s, sa, c) for (d, jt, s, sa, c) in rows],
    )
    con.commit()
    con.close()


def test_get_schedules_includes_enabled_default_true(queue_server):
    body = _get_schedules(queue_server)
    for jt in body["job_types"]:
        assert jt["enabled"] is True
        assert jt["is_override"] is False


def test_put_schedule_toggle_off_persists(queue_server):
    with _put_schedule(queue_server, "24h", {"enabled": False}) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    assert parsed["enabled"] is False
    assert parsed["is_override"] is True

    body = _get_schedules(queue_server)
    by_type = {jt["job_type"]: jt for jt in body["job_types"]}
    assert by_type["24h"]["enabled"] is False
    assert by_type["24h"]["is_override"] is True
    assert by_type["3day"]["enabled"] is True
    assert by_type["review"]["enabled"] is True


def test_put_schedule_toggle_off_cancels_pending(queue_server):
    db_path = queue_server["db_path"]
    # Pre-clean any pre-existing 24h rows the conftest fixture seeded
    # (DOC-A is 24h sent=0; _seed_test_jobs_if_missing adds Carlos 24h).
    con = sqlite3.connect(db_path)
    con.execute("DELETE FROM jobs WHERE job_type = '24h'")
    con.commit()
    con.close()

    base = 1700000000
    _seed_jobs(db_path, [
        ("UKK-1", "24h",  0, 0,        base),
        ("UKK-2", "24h",  0, 0,        base + 10),
        ("UKK-3", "24h",  0, 0,        base + 20),
        ("UKK-4", "3day", 0, 0,        base + 30),
        ("UKK-5", "24h",  1, base + 5, base + 40),  # already-sent, sent_at preserved
    ])

    before = int(time.time())
    with _put_schedule(queue_server, "24h", {"enabled": False}) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    after = int(time.time())

    assert parsed["cancelled_jobs"] == 3
    assert parsed["enabled"] is False
    assert parsed.get("rebased_jobs", 0) == 0

    con = sqlite3.connect(db_path)
    con.row_factory = sqlite3.Row
    rows = {
        r["doc_id"]: r
        for r in con.execute(
            "SELECT doc_id, sent, sent_at, job_type FROM jobs WHERE doc_id LIKE 'UKK-%'"
        ).fetchall()
    }
    con.close()

    for k in ("UKK-1", "UKK-2", "UKK-3"):
        assert rows[k]["sent"] == 1, f"{k} should be cancelled"
        assert before <= rows[k]["sent_at"] <= after, f"{k} sent_at must be ~now"
    # 3day pending row untouched.
    assert rows["UKK-4"]["sent"] == 0
    # Already-sent 24h row keeps its original sent_at (= base + 5).
    assert rows["UKK-5"]["sent"] == 1
    assert rows["UKK-5"]["sent_at"] == base + 5


def test_put_schedule_toggle_on_does_not_resurrect(queue_server):
    db_path = queue_server["db_path"]
    con = sqlite3.connect(db_path)
    con.execute("DELETE FROM jobs WHERE job_type = '24h'")
    con.commit()
    con.close()

    base = 1700000000
    _seed_jobs(db_path, [("UKK-RES", "24h", 0, 0, base)])

    # Toggle off → cancels.
    with _put_schedule(queue_server, "24h", {"enabled": False}) as resp:
        parsed_off = json.loads(resp.read().decode("utf-8"))
    assert parsed_off["cancelled_jobs"] == 1

    # Toggle on → does NOT resurrect.
    with _put_schedule(queue_server, "24h", {"enabled": True}) as resp:
        parsed_on = json.loads(resp.read().decode("utf-8"))
    assert parsed_on["cancelled_jobs"] == 0
    assert parsed_on["enabled"] is True

    con = sqlite3.connect(db_path)
    sent_val = con.execute(
        "SELECT sent FROM jobs WHERE doc_id='UKK-RES'"
    ).fetchone()[0]
    con.close()
    assert sent_val == 1  # still cancelled


def test_put_schedule_enabled_only_does_not_change_delay(queue_server):
    _put_schedule(queue_server, "24h", {"delay_hours": 48}).close()
    _put_schedule(queue_server, "24h", {"enabled": False}).close()

    body = _get_schedules(queue_server)
    by_type = {jt["job_type"]: jt for jt in body["job_types"]}
    assert by_type["24h"]["delay_hours"] == 48
    assert by_type["24h"]["enabled"] is False


def test_put_schedule_delay_only_does_not_change_enabled(queue_server):
    _put_schedule(queue_server, "24h", {"enabled": False}).close()
    _put_schedule(queue_server, "24h", {"delay_hours": 48}).close()

    body = _get_schedules(queue_server)
    by_type = {jt["job_type"]: jt for jt in body["job_types"]}
    assert by_type["24h"]["delay_hours"] == 48
    assert by_type["24h"]["enabled"] is False


def test_put_schedule_both_fields_together(queue_server):
    with _put_schedule(queue_server, "24h",
                      {"delay_hours": 48, "enabled": False}) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    assert parsed["delay_hours"] == 48
    assert parsed["enabled"] is False
    assert parsed["rebased_jobs"] == 0
    assert "cancelled_jobs" in parsed


def test_put_schedule_empty_body_reverts_both(queue_server):
    _put_schedule(queue_server, "24h",
                 {"delay_hours": 48, "enabled": False}).close()
    with _put_schedule(queue_server, "24h", {}) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    assert parsed["delay_hours"] == 24
    assert parsed["enabled"] is True
    assert parsed["is_override"] is False


def test_put_schedule_enabled_string_rejected(queue_server):
    try:
        _put_schedule(queue_server, "24h", {"enabled": "false"})
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400


def test_put_schedule_enabled_int_rejected(queue_server):
    try:
        _put_schedule(queue_server, "24h", {"enabled": 0})
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400


def test_put_schedule_enabled_null_treated_as_absent(queue_server):
    _put_schedule(queue_server, "24h", {"enabled": False}).close()
    # delay_hours change with enabled=null → enabled stays False.
    _put_schedule(queue_server, "24h",
                 {"delay_hours": 48, "enabled": None}).close()

    body = _get_schedules(queue_server)
    by_type = {jt["job_type"]: jt for jt in body["job_types"]}
    assert by_type["24h"]["delay_hours"] == 48
    assert by_type["24h"]["enabled"] is False


def test_put_schedule_toggle_off_then_delay_change_does_not_rebase(queue_server):
    db_path = queue_server["db_path"]
    con = sqlite3.connect(db_path)
    con.execute("DELETE FROM jobs WHERE job_type = '24h'")
    con.commit()
    con.close()

    base = 1700000000
    _seed_jobs(db_path, [("UKK-CR", "24h", 0, 0, base)])

    with _put_schedule(queue_server, "24h",
                      {"enabled": False, "delay_hours": 48}) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    assert parsed["cancelled_jobs"] == 1
    assert parsed["rebased_jobs"] == 0
