#!/usr/bin/env python3
"""ULH-02 / VAB-03 — insert reversible test work orders into jobs.db.

The IMMEDIATE_SEND_FOR_TESTING=1 scheduler burns through real pending rows in
~60 seconds, which leaves the admin UI pending lane effectively empty during
interactive review. This script writes reversible test rows with
``send_at`` fixed 7 days in the future (pending) or sent=1 with sent_at in the
recent past (sent), so they survive the test scheduler and populate every
admin-UI lifecycle chip.

Four modes (mutually exclusive):
    python3 scripts/insert_test_pending_job.py                # INSERT one row (ULH1)
    python3 scripts/insert_test_pending_job.py --remove       # DELETE ULH1 row(s)
    python3 scripts/insert_test_pending_job.py --batch        # INSERT 6 ULH2 rows
    python3 scripts/insert_test_pending_job.py --remove-batch # DELETE ULH2 rows

The two flag groups are tombstone-disjoint by design:
- ULH1 rows carry ``"ULH test row"`` in the ``name`` column.
- ULH2 batch rows carry ``"ULH2 test"`` in the ``name`` column.
``--remove`` and ``--remove-batch`` therefore never interfere with each other.

Reversal is idempotent (running ``--remove*`` twice prints 0 the second time).
Re-running INSERT without removal is safe — each call uses a unique ``doc_id``
of ``TEST-ULH-<unix_ts>`` (single mode) or ``ulh2-<jt>-<state>-<ts>`` (batch
mode) that collides only if two runs land in the same second. UNIQUE
constraint violations exit 2 so automation can distinguish them from a generic
DB error (exit 1).

Script uses only stdlib (sqlite3 + argparse + datetime). The developer's
``TEST_PHONE_OVERRIDE`` number is hard-coded as ``phone`` so even if a row
were accidentally fired, it could not text a real customer.

The ``--batch`` mode (VAB-03) inserts exactly 6 rows:
    (job_type ∈ {24h, 3day, review}) × (state ∈ {pending, sent}) = 6 rows
so all four admin-UI chips (all / estimates / completed / sent) light up with
at least one row each during visual review.

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

# VAB-03: ULH2 batch tombstone — distinct from TOMBSTONE so --remove (ULH1)
# and --remove-batch (ULH2) never interfere with each other. The two flag
# groups are intentionally decoupled: substring "ULH test row" never matches
# "ULH2 test" and vice versa.
BATCH_TOMBSTONE = "ULH2 test"

# Job types and states covered by the ULH2 batch insert. Order matters for the
# 6-row layout: (24h, 3day, review) × (pending, sent) = 6 rows.
BATCH_JOB_TYPES = ("24h", "3day", "review")
BATCH_STATES = ("pending", "sent")

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


def insert_batch(db_path: str) -> list[int]:
    """VAB-03 — insert 6 ULH2 test rows: (24h, 3day, review) × (pending, sent).

    Each pending row has ``send_at`` 7 days in the future (survives
    IMMEDIATE_SEND_FOR_TESTING) and ``sent=0, sent_at=0``. Each sent row has
    ``sent=1, sent_at=now-1h, send_at=now-2h`` so it appears in the
    'completed' and 'sent' admin-UI chips. All 6 rows use TEST_PHONE
    (+15308450190) so accidental delivery cannot reach a real customer.

    Returns the list of inserted row ids (length 6 on success).
    """
    now = _now_epoch()
    send_at_future = _send_at_future_epoch()  # +7d
    rows: list[tuple] = []
    for idx, jt in enumerate(BATCH_JOB_TYPES):
        for state in BATCH_STATES:
            sent_flag = 1 if state == "sent" else 0
            if state == "sent":
                send_at = now - 7200      # 2 hours ago (already-fired job)
                sent_at = now - 3600      # 1 hour ago
            else:
                send_at = send_at_future  # +7d future
                sent_at = 0               # not yet sent
            # Unique doc_id and estimate_key per row — index/state suffix
            # prevents UNIQUE collision within a single --batch invocation.
            doc_id = f"ulh2-{jt}-{state}-{now}"
            estimate_key = f"ulh2-{jt}-{state}-{now}"
            ro_id = f"ULH2-{idx}-{state[:1].upper()}"
            # Distinct VINs per row so any phone+VIN dedup path also leaves 6 rows.
            vin = f"TESTVIN12345ULH2{idx}{state[:1].upper()}"
            rows.append(
                (
                    doc_id,                                          # doc_id
                    jt,                                              # job_type
                    TEST_PHONE,                                      # phone
                    f"{BATCH_TOMBSTONE} — {jt} {state}",             # name
                    send_at,                                         # send_at
                    sent_flag,                                       # sent
                    now,                                             # created_at
                    vin,                                             # vin
                    "2024 Test Vehicle",                             # vehicle_desc
                    ro_id,                                           # ro_id
                    "ulh2-test@example.invalid",                     # email
                    "456 Batch Lane, Concord, CA 94520",             # address
                    sent_at,                                         # sent_at
                    estimate_key,                                    # estimate_key
                )
            )

    inserted_ids: list[int] = []
    con = sqlite3.connect(db_path)
    try:
        cur = con.cursor()
        for values in rows:
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
                inserted_ids.append(cur.lastrowid)
            except sqlite3.IntegrityError as exc:
                print(
                    f"constraint violation for batch row doc_id={values[0]}: {exc}",
                    file=sys.stderr,
                )
                con.rollback()
                sys.exit(2)
        con.commit()
    finally:
        con.close()
    return inserted_ids


def remove_batch(db_path: str) -> int:
    """Delete every row whose name contains BATCH_TOMBSTONE. Returns count.

    Tombstone disjointness: BATCH_TOMBSTONE='ULH2 test' does NOT substring-
    match TOMBSTONE='ULH test row' or vice versa, so this is safe to run
    independently of --remove.
    """
    con = sqlite3.connect(db_path)
    try:
        cur = con.cursor()
        cur.execute(
            "DELETE FROM jobs WHERE name LIKE ?",
            (f"%{BATCH_TOMBSTONE}%",),
        )
        deleted = cur.rowcount
        con.commit()
    finally:
        con.close()
    return deleted


def main() -> None:
    parser = argparse.ArgumentParser(
        description=(
            "Insert (or remove) reversible test work orders in jobs.db for "
            "admin-UI visual verification. Four mutually-exclusive modes: "
            "default (insert one ULH1 row), --remove (delete ULH1 rows), "
            "--batch (insert 6 ULH2 rows), --remove-batch (delete ULH2 rows)."
        )
    )
    parser.add_argument(
        "--remove",
        action="store_true",
        help=(
            "Delete every row whose name contains the tombstone "
            f"'{TOMBSTONE}' (idempotent; prints 0 if nothing matches). "
            "Independent of --remove-batch — different tombstone."
        ),
    )
    parser.add_argument(
        "--batch",
        action="store_true",
        help=(
            "VAB-03: insert 6 ULH2 test rows: (24h, 3day, review) × "
            "(pending, sent). Use --remove-batch to delete them. "
            "Independent of --remove — different tombstone."
        ),
    )
    parser.add_argument(
        "--remove-batch",
        action="store_true",
        help=(
            f"Delete every row whose name contains '{BATCH_TOMBSTONE}' "
            "(idempotent). Does NOT affect rows from --remove."
        ),
    )
    parser.add_argument(
        "--db",
        default=DB_PATH,
        help=f"Path to jobs.db (default: {DB_PATH})",
    )
    args = parser.parse_args()

    # Mutual-exclusivity guard. Friendlier than argparse mutually_exclusive_group
    # because the error names all three flags explicitly.
    modes = sum(int(x) for x in (args.remove, args.batch, args.remove_batch))
    if modes > 1:
        print(
            "ERROR: --remove, --batch, --remove-batch are mutually exclusive",
            file=sys.stderr,
        )
        sys.exit(1)

    if not os.path.exists(args.db):
        print(f"ERROR: jobs.db not found at {args.db}", file=sys.stderr)
        sys.exit(1)

    try:
        if args.remove:
            count = remove_rows(args.db)
            print(f"removed {count} test row(s) (tombstone='{TOMBSTONE}')")
        elif args.remove_batch:
            count = remove_batch(args.db)
            print(f"removed {count} batch row(s) (tombstone='{BATCH_TOMBSTONE}')")
        elif args.batch:
            ids = insert_batch(args.db)
            print(
                f"inserted {len(ids)} batch row(s) ids={ids} "
                f"(tombstone='{BATCH_TOMBSTONE}', phone={TEST_PHONE})"
            )
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
