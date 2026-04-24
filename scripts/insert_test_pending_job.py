#!/usr/bin/env python3
"""ULH-02 — insert a realistic pending test work order into jobs.db.

The IMMEDIATE_SEND_FOR_TESTING=1 scheduler burns through real pending rows in
~60 seconds, which leaves the admin UI pending lane effectively empty during
interactive review. This script writes one reversible test row with
``send_at`` fixed 7 days in the future so it survives the test scheduler and
is visible in both the default ``/earlscheibconcord/queue`` (pending-only) and
``/earlscheibconcord/queue?status=all`` views.

Usage:
    python3 scripts/insert_test_pending_job.py           # INSERT one row
    python3 scripts/insert_test_pending_job.py --remove  # DELETE all test rows

Reversal is keyed on the ``"ULH test row"`` tombstone in the ``name`` column;
``--remove`` is idempotent (prints 0 when nothing is there). Re-running INSERT
without ``--remove`` is safe — each call uses a unique ``doc_id`` of
``TEST-ULH-<unix_ts>`` that collides only if two runs land in the same second.
If that (or a future UNIQUE constraint) does collide, the script exits 2 so
automation can distinguish that from a generic DB error (exit 1).

Script uses only stdlib (sqlite3 + argparse + datetime). The developer's
``TEST_PHONE_OVERRIDE`` number is hard-coded as ``phone`` so even if the row
were accidentally fired, it could not text a real customer.

NOTE ON COLUMN TYPES: the jobs schema stores ``send_at``, ``created_at``, and
``sent_at`` as INTEGER unix timestamps (NOT "YYYY-MM-DD HH:MM:SS" strings).
``sent_at`` uses ``0`` for "not yet sent" (not NULL). Inspect with
``sqlite3 jobs.db ".schema jobs"`` to confirm.
"""
from __future__ import annotations

import argparse
import os
import sqlite3
import sys
import time
from datetime import datetime, timedelta, timezone

# Tombstone substring stored in the ``name`` column so ``--remove`` can find
# every prior test row without needing to remember their ids.
TOMBSTONE = "ULH test row"

# Developer's TEST_PHONE_OVERRIDE — NEVER a customer number. If the test row
# were to fire by accident the delivery would land here only.
TEST_PHONE = "+15308450190"

# 7 days is comfortably past the IMMEDIATE_SEND_FOR_TESTING 60-second window
# so the scheduler cannot pick it up before a human reviews.
SEND_DELAY_DAYS = 7

# DB path — same as app.py. Service cwd is the project root, so a relative
# path resolves to <repo>/jobs.db.
DB_PATH = os.environ.get("DB_PATH", "jobs.db")


def _now_epoch() -> int:
    """Return current UTC time as unix seconds — matches jobs.created_at."""
    return int(datetime.now(tz=timezone.utc).timestamp())


def _send_at_future_epoch() -> int:
    """Return unix-seconds value SEND_DELAY_DAYS in the future."""
    future = datetime.now(tz=timezone.utc) + timedelta(days=SEND_DELAY_DAYS)
    return int(future.timestamp())


def insert_row(db_path: str) -> int:
    """Insert one realistic pending test row. Returns the new row id."""
    unix_ts = int(time.time())
    doc_id = f"TEST-ULH-{unix_ts}"
    estimate_key = f"TEST-ULH-EST-{unix_ts}"
    # Column order matches CREATE TABLE jobs. send_at / created_at / sent_at
    # are stored as INTEGER unix seconds. sent_at=0 means "not yet sent".
    values = (
        doc_id,                     # doc_id
        "24h",                      # job_type
        TEST_PHONE,                 # phone
        f"Marco Testsson ({TOMBSTONE})",  # name
        _send_at_future_epoch(),    # send_at (INT)
        0,                          # sent (0 = pending)
        _now_epoch(),               # created_at (INT)
        "1HGCM82633A004352",        # vin
        "2003 Honda Accord EX (TEST)",  # vehicle_desc
        "RO-ULH-TEST",              # ro_id
        "ulh-test@example.invalid", # email
        "123 Test Lane, Concord, CA 94520",  # address
        0,                          # sent_at (INT; 0 = not yet sent)
        estimate_key,               # estimate_key
    )

    con = sqlite3.connect(db_path)
    try:
        cur = con.cursor()
        try:
            cur.execute(
                """
                INSERT INTO jobs (
                    doc_id, job_type, phone, name, send_at, sent, created_at,
                    vin, vehicle_desc, ro_id, email, address, sent_at, estimate_key
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                """,
                values,
            )
        except sqlite3.IntegrityError as exc:
            # UNIQUE or NOT NULL constraint violation — distinct exit so
            # automation can retry with a fresh unix_ts after sleep 1s.
            print(
                f"constraint violation for doc_id={doc_id}: {exc}",
                file=sys.stderr,
            )
            sys.exit(2)
        row_id = cur.lastrowid
        con.commit()
    finally:
        con.close()
    return row_id


def remove_rows(db_path: str) -> int:
    """Delete every row whose name contains the TOMBSTONE. Returns count."""
    con = sqlite3.connect(db_path)
    try:
        cur = con.cursor()
        cur.execute(
            "DELETE FROM jobs WHERE name LIKE ?",
            (f"%{TOMBSTONE}%",),
        )
        deleted = cur.rowcount
        con.commit()
    finally:
        con.close()
    return deleted


def main() -> None:
    parser = argparse.ArgumentParser(
        description=(
            "Insert (or remove with --remove) a realistic pending test work "
            "order in jobs.db for admin-UI visual verification."
        )
    )
    parser.add_argument(
        "--remove",
        action="store_true",
        help=(
            "Delete every row whose name contains the tombstone "
            f"'{TOMBSTONE}' (idempotent; prints 0 if nothing matches)."
        ),
    )
    parser.add_argument(
        "--db",
        default=DB_PATH,
        help=f"Path to jobs.db (default: {DB_PATH})",
    )
    args = parser.parse_args()

    if not os.path.exists(args.db):
        print(f"ERROR: jobs.db not found at {args.db}", file=sys.stderr)
        sys.exit(1)

    try:
        if args.remove:
            count = remove_rows(args.db)
            print(f"removed {count} test row(s) (tombstone='{TOMBSTONE}')")
        else:
            row_id = insert_row(args.db)
            print(
                f"inserted test row id={row_id} "
                f"(send_at=+{SEND_DELAY_DAYS}d, phone={TEST_PHONE})"
            )
    except sqlite3.Error as exc:
        print(f"ERROR: sqlite failure: {exc}", file=sys.stderr)
        sys.exit(1)


if __name__ == "__main__":
    main()
