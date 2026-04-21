"""Tests for /earlscheibconcord/queue GET + DELETE (ADMIN-05, ADMIN-06)."""
import json
import sqlite3
from urllib.request import Request, urlopen
from urllib.error import HTTPError

from tests.conftest import sign


# ---------- GET ----------

def test_get_queue_happy_path(queue_server):
    sig = sign(queue_server["secret"], b"")
    req = Request(f"{queue_server['base_url']}/earlscheibconcord/queue",
                  headers={"X-EMS-Signature": sig})
    with urlopen(req, timeout=3) as resp:
        assert resp.status == 200
        body = json.loads(resp.read().decode("utf-8"))
    assert isinstance(body, list)
    # Only sent=0 rows: 3 of 4 seeded
    assert len(body) == 3
    expected_keys = {"id", "doc_id", "job_type", "phone", "name", "send_at", "created_at"}
    assert set(body[0].keys()) == expected_keys
    # Ordered by send_at ASC: Carol (1800) < Alice (3600) < Bob (7200)
    assert [r["name"] for r in body] == ["Carol Example", "Alice Example", "Bob Example"]


def test_get_queue_missing_signature(queue_server):
    req = Request(f"{queue_server['base_url']}/earlscheibconcord/queue")
    try:
        urlopen(req, timeout=3)
        assert False, "expected 401"
    except HTTPError as e:
        assert e.code == 401
        body = json.loads(e.read().decode("utf-8"))
        assert body == {"error": "invalid signature"}


def test_get_queue_bad_signature(queue_server):
    req = Request(f"{queue_server['base_url']}/earlscheibconcord/queue",
                  headers={"X-EMS-Signature": "deadbeef" * 8})
    try:
        urlopen(req, timeout=3)
        assert False, "expected 401"
    except HTTPError as e:
        assert e.code == 401


def test_get_queue_ordering_and_filter(queue_server):
    """Sent=1 row is excluded; send_at ASC ordering preserved."""
    sig = sign(queue_server["secret"], b"")
    req = Request(f"{queue_server['base_url']}/earlscheibconcord/queue",
                  headers={"X-EMS-Signature": sig})
    with urlopen(req, timeout=3) as resp:
        rows = json.loads(resp.read().decode("utf-8"))
    assert all(r["name"] != "Dave Alreadysent" for r in rows)
    send_ats = [r["send_at"] for r in rows]
    assert send_ats == sorted(send_ats)


# ---------- DELETE ----------

def test_delete_queue_happy_path(queue_server):
    # Find Alice's id
    con = sqlite3.connect(queue_server["db_path"])
    alice_id = con.execute("SELECT id FROM jobs WHERE name = 'Alice Example'").fetchone()[0]
    con.close()

    body = json.dumps({"id": alice_id}).encode("utf-8")
    sig = sign(queue_server["secret"], body)
    req = Request(f"{queue_server['base_url']}/earlscheibconcord/queue",
                  data=body, method="DELETE",
                  headers={"X-EMS-Signature": sig, "Content-Type": "application/json"})
    with urlopen(req, timeout=3) as resp:
        assert resp.status == 200
        assert json.loads(resp.read().decode("utf-8")) == {"deleted": 1}

    # Verify row is gone
    con = sqlite3.connect(queue_server["db_path"])
    assert con.execute("SELECT COUNT(*) FROM jobs WHERE id = ?", (alice_id,)).fetchone()[0] == 0
    con.close()


def test_delete_queue_missing_row(queue_server):
    body = json.dumps({"id": 99999}).encode("utf-8")
    sig = sign(queue_server["secret"], body)
    req = Request(f"{queue_server['base_url']}/earlscheibconcord/queue",
                  data=body, method="DELETE",
                  headers={"X-EMS-Signature": sig, "Content-Type": "application/json"})
    try:
        urlopen(req, timeout=3)
        assert False, "expected 404"
    except HTTPError as e:
        assert e.code == 404
        assert json.loads(e.read().decode("utf-8")) == {"error": "not found or already sent"}


def test_delete_queue_already_sent(queue_server):
    con = sqlite3.connect(queue_server["db_path"])
    dave_id = con.execute("SELECT id FROM jobs WHERE name = 'Dave Alreadysent'").fetchone()[0]
    con.close()
    body = json.dumps({"id": dave_id}).encode("utf-8")
    sig = sign(queue_server["secret"], body)
    req = Request(f"{queue_server['base_url']}/earlscheibconcord/queue",
                  data=body, method="DELETE",
                  headers={"X-EMS-Signature": sig, "Content-Type": "application/json"})
    try:
        urlopen(req, timeout=3)
        assert False, "expected 404"
    except HTTPError as e:
        assert e.code == 404

    # Row must still exist (sent=1 guard prevents deletion)
    con = sqlite3.connect(queue_server["db_path"])
    assert con.execute("SELECT COUNT(*) FROM jobs WHERE id = ?", (dave_id,)).fetchone()[0] == 1
    con.close()


def test_delete_queue_bad_signature(queue_server):
    body = json.dumps({"id": 1}).encode("utf-8")
    req = Request(f"{queue_server['base_url']}/earlscheibconcord/queue",
                  data=body, method="DELETE",
                  headers={"X-EMS-Signature": "00" * 32, "Content-Type": "application/json"})
    try:
        urlopen(req, timeout=3)
        assert False, "expected 401"
    except HTTPError as e:
        assert e.code == 401
