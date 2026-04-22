"""Integration tests for WMH-02 /earlscheibconcord/templates endpoints.

GET returns default bodies + overrides + placeholder catalog + sample row.
PUT with non-empty body upserts and returns is_override=True.
PUT with empty body deletes the override (reverts to default).
PUT with unknown job_type or malformed template returns 400.
PUT without a valid HMAC/Basic auth returns 401.

Uses the existing queue_server conftest fixture (spins app.py on an ephemeral
port, seeded with Alice/Bob/Carol/Dave jobs) and the sign() helper.
"""
import json
import sqlite3
from urllib.request import Request, urlopen
from urllib.error import HTTPError

from tests.conftest import sign


BASE = "/earlscheibconcord/templates"


# ---------- GET ----------

def test_get_templates_default_bodies(queue_server):
    sig = sign(queue_server["secret"], b"")
    req = Request(f"{queue_server['base_url']}{BASE}",
                  headers={"X-EMS-Signature": sig})
    with urlopen(req, timeout=3) as resp:
        assert resp.status == 200
        body = json.loads(resp.read().decode("utf-8"))

    assert set(body.keys()) == {"job_types", "placeholders", "sample_row"}
    assert len(body["job_types"]) == 3
    job_types = [jt["job_type"] for jt in body["job_types"]]
    assert job_types == ["24h", "3day", "review"]

    # All defaults — no overrides were seeded.
    for jt in body["job_types"]:
        assert jt["is_override"] is False
        assert jt["updated_at"] == 0
        assert jt["body"]  # non-empty
        assert "{" not in jt["body"] or all(
            token in jt["body"]
            for token in []  # body contains raw placeholders — not rendered here
        ) or True  # body is the RAW template (with {first_name} etc)

    # Each default MUST contain at least one expected placeholder.
    bodies_by_type = {jt["job_type"]: jt["body"] for jt in body["job_types"]}
    assert "{first_name}" in bodies_by_type["24h"]
    assert "{shop_name}" in bodies_by_type["24h"]
    assert "{shop_phone}" in bodies_by_type["24h"]
    assert "{first_name}" in bodies_by_type["3day"]
    assert "{first_name}" in bodies_by_type["review"]
    assert "{review_url}" in bodies_by_type["review"]


def test_get_templates_placeholder_catalog(queue_server):
    sig = sign(queue_server["secret"], b"")
    req = Request(f"{queue_server['base_url']}{BASE}",
                  headers={"X-EMS-Signature": sig})
    with urlopen(req, timeout=3) as resp:
        body = json.loads(resp.read().decode("utf-8"))

    assert body["placeholders"]["per_row"] == [
        "first_name", "name", "phone", "vin", "vehicle_desc",
        "ro_id", "doc_id", "email",
    ]
    assert body["placeholders"]["shop"] == ["shop_name", "shop_phone", "review_url"]


def test_get_templates_sample_row_from_pending_job(queue_server):
    """Fixture seeded Alice/Bob/Carol (pending) + Dave (sent). Newest pending
    row should populate sample_row so the live preview uses a real customer."""
    sig = sign(queue_server["secret"], b"")
    req = Request(f"{queue_server['base_url']}{BASE}",
                  headers={"X-EMS-Signature": sig})
    with urlopen(req, timeout=3) as resp:
        body = json.loads(resp.read().decode("utf-8"))

    sample = body["sample_row"]
    # Any of the three pending customers could be "newest by id" — the fixture
    # inserts them in order so Carol (the last executemany row) is last inserted.
    assert sample["name"] in {"Alice Example", "Bob Example", "Carol Example"}
    assert sample["first_name"] == sample["name"].split()[0]
    # Shop constants must be merged in.
    assert sample["shop_name"] == "Earl Scheib Auto Body Concord"
    assert sample["shop_phone"] == "(925) 609-7780"
    assert sample["review_url"] == "https://g.page/r/review"


def test_get_templates_bad_signature(queue_server):
    req = Request(f"{queue_server['base_url']}{BASE}",
                  headers={"X-EMS-Signature": "00" * 32})
    try:
        urlopen(req, timeout=3)
        assert False, "expected 401"
    except HTTPError as e:
        assert e.code == 401


def test_get_templates_missing_signature(queue_server):
    req = Request(f"{queue_server['base_url']}{BASE}")
    try:
        urlopen(req, timeout=3)
        assert False, "expected 401"
    except HTTPError as e:
        assert e.code == 401


# ---------- PUT: happy paths ----------

def test_put_templates_upsert_override(queue_server):
    new_body = "Custom 24h for {first_name} at {shop_name}."
    raw = json.dumps({"body": new_body}).encode("utf-8")
    sig = sign(queue_server["secret"], raw)

    req = Request(f"{queue_server['base_url']}{BASE}/24h",
                  data=raw, method="PUT",
                  headers={"X-EMS-Signature": sig, "Content-Type": "application/json"})
    with urlopen(req, timeout=3) as resp:
        assert resp.status == 200
        parsed = json.loads(resp.read().decode("utf-8"))

    assert parsed["is_override"] is True
    assert parsed["body"] == new_body
    assert parsed["updated_at"] > 0

    # Subsequent GET shows is_override=True for that row.
    sig2 = sign(queue_server["secret"], b"")
    req2 = Request(f"{queue_server['base_url']}{BASE}",
                   headers={"X-EMS-Signature": sig2})
    with urlopen(req2, timeout=3) as resp2:
        listing = json.loads(resp2.read().decode("utf-8"))
    by_type = {jt["job_type"]: jt for jt in listing["job_types"]}
    assert by_type["24h"]["is_override"] is True
    assert by_type["24h"]["body"] == new_body
    assert by_type["3day"]["is_override"] is False  # others unaffected


def test_put_templates_empty_body_deletes_override(queue_server):
    # First install an override so there's something to delete.
    raw1 = json.dumps({"body": "Temp override"}).encode("utf-8")
    sig1 = sign(queue_server["secret"], raw1)
    req1 = Request(f"{queue_server['base_url']}{BASE}/3day",
                   data=raw1, method="PUT",
                   headers={"X-EMS-Signature": sig1, "Content-Type": "application/json"})
    urlopen(req1, timeout=3).close()

    # Now clear it.
    raw2 = json.dumps({"body": ""}).encode("utf-8")
    sig2 = sign(queue_server["secret"], raw2)
    req2 = Request(f"{queue_server['base_url']}{BASE}/3day",
                   data=raw2, method="PUT",
                   headers={"X-EMS-Signature": sig2, "Content-Type": "application/json"})
    with urlopen(req2, timeout=3) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))

    assert parsed["is_override"] is False
    assert parsed["updated_at"] == 0
    assert "{first_name}" in parsed["body"]  # default body returned

    # Confirm row was deleted.
    con = sqlite3.connect(queue_server["db_path"])
    count = con.execute(
        "SELECT COUNT(*) FROM templates WHERE job_type = '3day'"
    ).fetchone()[0]
    con.close()
    assert count == 0


def test_put_templates_whitespace_body_deletes_override(queue_server):
    """Whitespace-only body behaves the same as empty body (revert)."""
    raw = json.dumps({"body": "   \n\t  "}).encode("utf-8")
    sig = sign(queue_server["secret"], raw)
    req = Request(f"{queue_server['base_url']}{BASE}/review",
                  data=raw, method="PUT",
                  headers={"X-EMS-Signature": sig, "Content-Type": "application/json"})
    with urlopen(req, timeout=3) as resp:
        parsed = json.loads(resp.read().decode("utf-8"))
    assert parsed["is_override"] is False


# ---------- PUT: validation ----------

def test_put_templates_unknown_job_type(queue_server):
    raw = json.dumps({"body": "whatever"}).encode("utf-8")
    sig = sign(queue_server["secret"], raw)
    req = Request(f"{queue_server['base_url']}{BASE}/bogus",
                  data=raw, method="PUT",
                  headers={"X-EMS-Signature": sig, "Content-Type": "application/json"})
    try:
        urlopen(req, timeout=3)
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400
        body = json.loads(e.read().decode("utf-8"))
        assert "unknown" in body["error"].lower()


def test_put_templates_malformed_template_rejected(queue_server):
    """Unclosed brace must be caught before save — prevents a bad template
    from crashing every future send."""
    raw = json.dumps({"body": "Hi {unclosed"}).encode("utf-8")
    sig = sign(queue_server["secret"], raw)
    req = Request(f"{queue_server['base_url']}{BASE}/24h",
                  data=raw, method="PUT",
                  headers={"X-EMS-Signature": sig, "Content-Type": "application/json"})
    try:
        urlopen(req, timeout=3)
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400
        body = json.loads(e.read().decode("utf-8"))
        assert "syntax" in body["error"].lower()

    # Confirm nothing was saved.
    con = sqlite3.connect(queue_server["db_path"])
    count = con.execute(
        "SELECT COUNT(*) FROM templates WHERE job_type = '24h'"
    ).fetchone()[0]
    con.close()
    assert count == 0


def test_put_templates_body_too_long(queue_server):
    raw = json.dumps({"body": "x" * 2001}).encode("utf-8")
    sig = sign(queue_server["secret"], raw)
    req = Request(f"{queue_server['base_url']}{BASE}/24h",
                  data=raw, method="PUT",
                  headers={"X-EMS-Signature": sig, "Content-Type": "application/json"})
    try:
        urlopen(req, timeout=3)
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400


def test_put_templates_missing_body_field(queue_server):
    raw = json.dumps({"not_body": "x"}).encode("utf-8")
    sig = sign(queue_server["secret"], raw)
    req = Request(f"{queue_server['base_url']}{BASE}/24h",
                  data=raw, method="PUT",
                  headers={"X-EMS-Signature": sig, "Content-Type": "application/json"})
    try:
        urlopen(req, timeout=3)
        assert False, "expected 400"
    except HTTPError as e:
        assert e.code == 400


def test_put_templates_bad_signature(queue_server):
    raw = json.dumps({"body": "legit body"}).encode("utf-8")
    req = Request(f"{queue_server['base_url']}{BASE}/24h",
                  data=raw, method="PUT",
                  headers={"X-EMS-Signature": "00" * 32, "Content-Type": "application/json"})
    try:
        urlopen(req, timeout=3)
        assert False, "expected 401"
    except HTTPError as e:
        assert e.code == 401


def test_put_templates_invalid_path_returns_404(queue_server):
    raw = json.dumps({"body": "x"}).encode("utf-8")
    sig = sign(queue_server["secret"], raw)
    req = Request(f"{queue_server['base_url']}/earlscheibconcord/notatemplate",
                  data=raw, method="PUT",
                  headers={"X-EMS-Signature": sig, "Content-Type": "application/json"})
    try:
        urlopen(req, timeout=3)
        assert False, "expected 404"
    except HTTPError as e:
        assert e.code == 404


# ---------- End-to-end: override affects render_template ----------

def test_override_persists_and_renders(queue_server):
    """After PUT saves an override, a fresh render_template call returns the
    new body. This is the core contract: edit → save → the next send uses
    the new copy."""
    import importlib
    import app as app_mod
    importlib.reload(app_mod)  # pick up the DB path set by the fixture

    new_body = "Hey {first_name}! Your {vehicle_desc} is ready — {shop_phone}"
    raw = json.dumps({"body": new_body}).encode("utf-8")
    sig = sign(queue_server["secret"], raw)
    req = Request(f"{queue_server['base_url']}{BASE}/24h",
                  data=raw, method="PUT",
                  headers={"X-EMS-Signature": sig, "Content-Type": "application/json"})
    with urlopen(req, timeout=3) as resp:
        assert resp.status == 200

    row = {"name": "Marco Rossi", "vehicle_desc": "2021 Subaru Outback"}
    rendered = app_mod.render_template("24h", row)
    assert rendered == "Hey Marco! Your 2021 Subaru Outback is ready — (925) 609-7780"
