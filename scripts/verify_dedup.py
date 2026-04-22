#!/usr/bin/env python3
"""QAJ-01 dedup verification — (phone + VIN) collapse + 60-day reopen.

Runs four scenarios against a disposable SQLite DB by repeatedly importing the
real ``app.init_db`` + ``app.schedule_job`` with ``DB_PATH`` swapped to a temp
file. Checks the row count + sent state + field values after each call. The
script is intentionally small — no unittest framework, no fixtures. Just
runnable evidence that the dedup logic behaves as described in the QAJ
task manifest.

Usage:
    python3 scripts/verify_dedup.py [/tmp/foo.db]

If no path is given, a path in ``/tmp`` is auto-picked and cleaned up on exit.

Exit code 0 on pass; 1 on any assertion failure.
"""
from __future__ import annotations

import os
import sqlite3
import sys
import tempfile
import time

# Point app.DB_PATH at a temp file BEFORE importing — schedule_job reads the
# module-level DB_PATH through get_db() at call time, so overriding after
# import also works, but flipping it up-front keeps startup logs truthful.
REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, REPO_ROOT)


def run(db_path: str) -> int:
    os.environ["DB_PATH"] = db_path
    # Dedup test MUST observe caller-supplied send_at verbatim — any override
    # (e.g. IMMEDIATE_SEND_FOR_TESTING) masks the "preserve scheduled window"
    # assertion in Case 2. Scrub it before loading app so the module constant
    # is re-evaluated against a clean env.
    os.environ.pop("IMMEDIATE_SEND_FOR_TESTING", None)
    # Fresh import so DB_PATH env is honoured by app.py top-level config.
    if "app" in sys.modules:
        del sys.modules["app"]
    import app  # type: ignore

    # Monkey-patch DB_PATH at module level too (defensive; the env-var read
    # happens at module import, but re-exec-safe against partial reloads).
    app.DB_PATH = db_path
    # app.py loads .env on import, which can re-set IMMEDIATE_SEND_FOR_TESTING
    # to "1". Force it off at the module level so the dedup test sees the
    # original caller-supplied send_at.
    app.IMMEDIATE_SEND_FOR_TESTING = False

    # Blank start
    if os.path.exists(db_path):
        os.remove(db_path)
    app.init_db()

    def rows():
        con = sqlite3.connect(db_path)
        con.row_factory = sqlite3.Row
        try:
            cur = con.cursor()
            cur.execute(
                "SELECT id, doc_id, job_type, phone, name, vin, sent, "
                "estimate_key, created_at, send_at "
                "FROM jobs ORDER BY id ASC"
            )
            return [dict(r) for r in cur.fetchall()]
        finally:
            con.close()

    def fail(msg: str):
        print(f"FAIL: {msg}", file=sys.stderr)
        sys.exit(1)

    now = int(time.time())

    # ------------------------------------------------------------------
    # Case 1 — Fresh insert. A never-seen (phone, VIN) should create a row.
    # ------------------------------------------------------------------
    app.schedule_job(
        doc_id="DOC-A",
        job_type="24h",
        phone="+17075551234",
        name="Wayne Johnston",
        send_at=now + 86400,
        vin="1C3EL45X52N1273957",
        vehicle_desc="2002 Chrysler PT Cruiser",
        ro_id="DR7QA13",
    )
    r = rows()
    if len(r) != 1:
        fail(f"Case 1: expected 1 row, got {len(r)}")
    if r[0]["estimate_key"] != "+17075551234|1C3EL45X52N1273957":
        fail(f"Case 1: estimate_key wrong: {r[0]['estimate_key']}")
    if r[0]["sent"] != 0:
        fail("Case 1: sent should be 0")
    print("PASS Case 1 — fresh insert: 1 row, estimate_key set, sent=0")

    # ------------------------------------------------------------------
    # Case 2 — Update pending. Same (phone, VIN) + same job_type but different
    # doc_id / corrected name. Must UPDATE, not INSERT.
    # ------------------------------------------------------------------
    app.schedule_job(
        doc_id="DOC-A-RESAVE",
        job_type="24h",
        phone="+17075551234",
        name="WAYNE JOHNSTON",  # name corrected to uppercase
        send_at=now + 86400 + 300,  # different send_at — should be IGNORED
        vin="1C3EL45X52N1273957",
        vehicle_desc="2002 Chrysler PT Cruiser Limited",  # corrected trim
        ro_id="DR7QA13",
    )
    r = rows()
    if len(r) != 1:
        fail(f"Case 2: expected still 1 row, got {len(r)}")
    if r[0]["doc_id"] != "DOC-A-RESAVE":
        fail(f"Case 2: doc_id should have been updated, got {r[0]['doc_id']}")
    if r[0]["name"] != "WAYNE JOHNSTON":
        fail(f"Case 2: name should have been updated, got {r[0]['name']}")
    if r[0]["send_at"] != now + 86400:
        fail(
            f"Case 2: send_at should NOT have changed (preserve scheduled "
            f"window); got {r[0]['send_at']}, expected {now + 86400}"
        )
    print("PASS Case 2 — update pending: doc_id/name refreshed, send_at preserved")

    # ------------------------------------------------------------------
    # Case 3 — Skip within 60d. Mark the row sent, then re-schedule. Should
    # remain at 1 row — the skip path MUST NOT insert a second row.
    # ------------------------------------------------------------------
    con = sqlite3.connect(db_path)
    try:
        cur = con.cursor()
        cur.execute("UPDATE jobs SET sent=1 WHERE id=?", (r[0]["id"],))
        con.commit()
    finally:
        con.close()

    app.schedule_job(
        doc_id="DOC-A-RESAVE-AGAIN",
        job_type="24h",
        phone="+17075551234",
        name="Wayne Johnston",
        send_at=now + 86400,
        vin="1C3EL45X52N1273957",
        vehicle_desc="2002 Chrysler PT Cruiser",
        ro_id="DR7QA13",
    )
    r = rows()
    if len(r) != 1:
        fail(
            f"Case 3: expected 1 row (skip path); got {len(r)} — "
            f"duplicate insertion on a recently-sent estimate_key"
        )
    if r[0]["sent"] != 1:
        fail(f"Case 3: existing row should stay sent=1")
    print("PASS Case 3 — skip within 60d: still 1 row, existing sent=1 untouched")

    # ------------------------------------------------------------------
    # Case 4 — Reopen after 60d. Time-travel the created_at of the existing
    # sent row back by 61 days, then re-schedule. Should INSERT a second row.
    # ------------------------------------------------------------------
    sixty_one_days_ago = now - 61 * 86400
    con = sqlite3.connect(db_path)
    try:
        cur = con.cursor()
        cur.execute(
            "UPDATE jobs SET created_at=? WHERE id=?",
            (sixty_one_days_ago, r[0]["id"]),
        )
        con.commit()
    finally:
        con.close()

    app.schedule_job(
        doc_id="DOC-A-NEW-VISIT",
        job_type="24h",
        phone="+17075551234",
        name="Wayne Johnston",
        send_at=now + 86400,
        vin="1C3EL45X52N1273957",
        vehicle_desc="2002 Chrysler PT Cruiser",
        ro_id="NEW-RO-999",
    )
    r = rows()
    if len(r) != 2:
        fail(
            f"Case 4: expected 2 rows (reopen after 60d); got {len(r)} — "
            f"stale sent row should have allowed a fresh insert"
        )
    if r[1]["sent"] != 0 or r[1]["doc_id"] != "DOC-A-NEW-VISIT":
        fail(
            f"Case 4: new row should be pending + have fresh doc_id; got "
            f"sent={r[1]['sent']} doc_id={r[1]['doc_id']}"
        )
    print("PASS Case 4 — reopen after 60d: 2 rows, new one is pending")

    print()
    print("ALL 4 CASES PASSED — (phone+VIN) dedup behaves per QAJ-01 spec.")
    return 0


if __name__ == "__main__":
    if len(sys.argv) > 1:
        path = sys.argv[1]
        rc = run(path)
    else:
        with tempfile.NamedTemporaryFile(suffix=".db", delete=False) as t:
            path = t.name
        try:
            rc = run(path)
        finally:
            try:
                os.remove(path)
            except OSError:
                pass
    sys.exit(rc)
