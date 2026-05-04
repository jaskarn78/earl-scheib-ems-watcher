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

# VPL-02: Sample preview tombstone. Distinct from both ULH tombstones so the
# three insert/remove flag groups never interfere. Used by --samples to show
# Marco-style realistic estimates + work orders with the new {year}/{make}/
# {model} columns populated, so the operator can see what real rows render
# like in the admin UI before any live customer data flows.
SAMPLES_TOMBSTONE = "SAMPLE preview"

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


# VPL-02: realistic sample customers used by --samples. Mix of estimates
# (24h + 3day flow) and closed work orders (review flow), with year/make/model
# populated so the new template placeholders render against real-looking data.
# Phone is overridden to TEST_PHONE on insert so accidental fires are safe.
SAMPLE_ESTIMATES = [
    {
        "first": "Carlos", "last": "Mendoza",
        "vin":  "1HGCV1F30LA012345",
        "year": "2020", "make": "Honda",        "model": "Accord Sport",
        "ro":   "RO-18472",
        "addr": "1142 Salvio St, Concord, CA 94520",
    },
    {
        "first": "Priya",  "last": "Ramaswamy",
        "vin":  "5YJ3E1EA8KF317890",
        "year": "2019", "make": "Tesla",        "model": "Model 3 Long Range",
        "ro":   "RO-18488",
        "addr": "2050 Diamond Blvd, Concord, CA 94520",
    },
    {
        "first": "Derek",  "last": "O'Sullivan",
        "vin":  "JTMBFREV3HJ134567",
        "year": "2017", "make": "Toyota",       "model": "RAV4 XLE AWD",
        "ro":   "RO-18501",
        "addr": "775 Ygnacio Valley Rd, Concord, CA 94518",
    },
]

# Closed work orders → review job pending. These represent jobs we'd want to
# request a Google review for after delivery.
SAMPLE_WORK_ORDERS = [
    {
        "first": "Janelle", "last": "Foster",
        "vin":  "1FTFW1ET5DKE12345",
        "year": "2013", "make": "Ford",         "model": "F-150 XLT SuperCrew",
        "ro":   "RO-18399",
        "addr": "330 Galindo St, Concord, CA 94520",
    },
    {
        "first": "Marcus",  "last": "Whitfield",
        "vin":  "WBA8E9G54GNT98765",
        "year": "2016", "make": "BMW",          "model": "328i xDrive",
        "ro":   "RO-18412",
        "addr": "1490 Treat Blvd, Walnut Creek, CA 94597",
    },
]


def insert_samples(db_path: str) -> list[int]:
    """VPL-02 — insert a realistic sample of pending rows for admin-UI preview.

    Creates 3 estimate flows × (24h + 3day) = 6 estimate-stage rows, plus 2
    review-stage rows from closed work orders, for 8 pending rows total. Each
    row populates the new ``year`` / ``make`` / ``model`` columns alongside the
    legacy ``vehicle_desc``, so the template preview bubble in the admin UI
    renders realistic copy whether Marco's saved override uses ``{vehicle_desc}``
    or the new ``{year} {make} {model}`` placeholders.

    Send_at is fixed 7 days in the future so IMMEDIATE_SEND_FOR_TESTING cannot
    fire them before review. Phone is forced to TEST_PHONE so accidental
    delivery routes to the dev number, never a real customer.

    Returns the list of inserted row ids (length 8 on success).
    """
    now = _now_epoch()
    send_at_future = _send_at_future_epoch()  # +7d
    rows: list[tuple] = []

    def _row(jt: str, sample: dict, slot: int) -> tuple:
        # Unique doc_id / estimate_key per row keep dedup from collapsing the
        # batch (estimate_key collisions would UPDATE-merge into one row).
        doc_id = f"sample-{jt}-{slot}-{now}"
        estimate_key = f"{TEST_PHONE}|{sample['vin']}|{slot}"
        vehicle_desc = f"{sample['year']} {sample['make']} {sample['model']}"
        full_name = f"{sample['first']} {sample['last']} ({SAMPLES_TOMBSTONE})"
        last_clean = sample["last"].lower().replace("'", "")
        email = f"{sample['first'].lower()}.{last_clean}@example.invalid"
        return (
            doc_id,                    # doc_id
            jt,                        # job_type
            TEST_PHONE,                # phone
            full_name,                 # name
            send_at_future,            # send_at (INT, +7d)
            0,                         # sent (0 = pending)
            now,                       # created_at (INT)
            sample["vin"],             # vin
            vehicle_desc,              # vehicle_desc
            sample["ro"],              # ro_id
            email,                     # email
            sample["addr"],            # address
            0,                         # sent_at (INT; 0 = not yet sent)
            estimate_key,              # estimate_key
            sample["year"],            # year   (VPL-01)
            sample["make"],            # make   (VPL-01)
            sample["model"],           # model  (VPL-01)
        )

    # Estimate flow: each estimate produces a 24h + 3day pending pair.
    for slot, sample in enumerate(SAMPLE_ESTIMATES):
        rows.append(_row("24h",  sample, slot))
        rows.append(_row("3day", sample, slot))
    # Work-order review flow: one review row per closed RO.
    for slot, sample in enumerate(SAMPLE_WORK_ORDERS, start=len(SAMPLE_ESTIMATES)):
        rows.append(_row("review", sample, slot))

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
                        vin, vehicle_desc, ro_id, email, address, sent_at, estimate_key,
                        year, make, model
                    ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
                    """,
                    values,
                )
                inserted_ids.append(cur.lastrowid)
            except sqlite3.IntegrityError as exc:
                print(
                    f"constraint violation for sample row doc_id={values[0]}: {exc}",
                    file=sys.stderr,
                )
                con.rollback()
                sys.exit(2)
        con.commit()
    finally:
        con.close()
    return inserted_ids


def remove_samples(db_path: str) -> int:
    """Delete every row whose name contains SAMPLES_TOMBSTONE. Returns count.

    Tombstone disjointness: 'SAMPLE preview' substring-matches neither
    'ULH test row' nor 'ULH2 test', so this is safe alongside the other
    --remove* flags.
    """
    con = sqlite3.connect(db_path)
    try:
        cur = con.cursor()
        cur.execute(
            "DELETE FROM jobs WHERE name LIKE ?",
            (f"%{SAMPLES_TOMBSTONE}%",),
        )
        deleted = cur.rowcount
        con.commit()
    finally:
        con.close()
    return deleted


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
        "--samples",
        action="store_true",
        help=(
            "VPL-02: insert 8 realistic pending rows (3 estimates × 2 jobs + "
            "2 review work orders) with year/make/model populated, so the "
            "admin-UI preview shows what real estimates and work orders look "
            "like. Use --remove-samples to delete them."
        ),
    )
    parser.add_argument(
        "--remove-samples",
        action="store_true",
        help=(
            f"Delete every row whose name contains '{SAMPLES_TOMBSTONE}' "
            "(idempotent). Does NOT affect --remove or --remove-batch rows."
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
    modes = sum(int(x) for x in (
        args.remove, args.batch, args.remove_batch,
        args.samples, args.remove_samples,
    ))
    if modes > 1:
        print(
            "ERROR: --remove, --batch, --remove-batch, --samples, "
            "--remove-samples are mutually exclusive",
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
        elif args.remove_samples:
            count = remove_samples(args.db)
            print(f"removed {count} sample row(s) (tombstone='{SAMPLES_TOMBSTONE}')")
        elif args.samples:
            ids = insert_samples(args.db)
            print(
                f"inserted {len(ids)} sample row(s) ids={ids} "
                f"(tombstone='{SAMPLES_TOMBSTONE}', phone={TEST_PHONE})"
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
