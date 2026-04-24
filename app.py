import os
import sqlite3
import threading
import time
import logging
import re
import hmac
import hashlib
import xml.etree.ElementTree as ET
from collections import defaultdict
from datetime import datetime, timezone
from http.server import BaseHTTPRequestHandler, HTTPServer
from urllib.parse import urlparse, parse_qs
from urllib.request import urlopen, Request
from urllib.error import URLError
import base64
import json

from dotenv import load_dotenv

try:
    import pytz
    SHOP_TZ = pytz.timezone("America/Los_Angeles")
    HAS_PYTZ = True
except ImportError:
    HAS_PYTZ = False

from datetime import timedelta

def next_send_window(after_ts: int) -> int:
    """Return next optimal send time after after_ts.
    Rules (America/Los_Angeles):
    - Quiet hours: no sends before 8am or after 8pm
    - Optimal windows: 10am-12pm or 2pm-4pm
    - Skip weekends (push to Monday 10am)
    If pytz not available, returns after_ts unchanged.
    """
    if not HAS_PYTZ:
        return after_ts

    dt = datetime.fromtimestamp(after_ts, tz=SHOP_TZ)

    # Advance past weekends first
    while dt.weekday() >= 5:  # 5=Sat, 6=Sun
        dt = (dt + timedelta(days=1)).replace(hour=10, minute=0, second=0, microsecond=0)

    h = dt.hour
    if h < 8:
        dt = dt.replace(hour=10, minute=0, second=0, microsecond=0)
    elif h < 10:
        dt = dt.replace(hour=10, minute=0, second=0, microsecond=0)
    elif h < 12:
        pass  # 10am-12pm: optimal
    elif h < 14:
        dt = dt.replace(hour=14, minute=0, second=0, microsecond=0)
    elif h < 16:
        pass  # 2pm-4pm: optimal
    else:
        # After 4pm: push to 10am next business day
        dt = (dt + timedelta(days=1)).replace(hour=10, minute=0, second=0, microsecond=0)
        while dt.weekday() >= 5:
            dt += timedelta(days=1)

    return int(dt.timestamp())


# Load .env from same directory as this script
load_dotenv(os.path.join(os.path.dirname(os.path.abspath(__file__)), ".env"))

# Config
CCC_SECRET = os.getenv("CCC_SECRET", "")
TWILIO_ACCOUNT_SID = os.getenv("TWILIO_ACCOUNT_SID", "")
TWILIO_API_KEY = os.getenv("TWILIO_API_KEY", "")
TWILIO_API_SECRET = os.getenv("TWILIO_API_SECRET", "")
TWILIO_FROM = os.getenv("TWILIO_FROM", "")
PORT = int(os.getenv("PORT", "8200"))
DB_PATH = os.getenv("DB_PATH", os.path.join(os.path.dirname(os.path.abspath(__file__)), "jobs.db"))
TELEMETRY_LOG_PATH = os.getenv(
    "TELEMETRY_LOG_PATH",
    os.path.join(os.path.dirname(os.path.abspath(__file__)), "telemetry.log"),
)
REMOTE_CONFIG_PATH = os.getenv(
    "REMOTE_CONFIG_PATH",
    os.path.join(os.path.dirname(os.path.abspath(__file__)), "remote_config.json"),
)
TEST_PHONE_OVERRIDE = os.getenv("TEST_PHONE_OVERRIDE", "")
# TEST_PHONE_RECIPIENTS (plural) takes precedence over TEST_PHONE_OVERRIDE when
# set. Comma-separated list of E.164 numbers that EVERY outgoing SMS is
# fan-out delivered to during testing. Lets the operator + shop owner both
# receive copies of each message. Unset the var to return to normal behaviour.
TEST_PHONE_RECIPIENTS = [
    p.strip() for p in os.getenv("TEST_PHONE_RECIPIENTS", "").split(",") if p.strip()
]
# OH4-04: collapse all scheduling (24h / 72h / review-24h) to now+60s when
# this env var is "1". Useful for inside-a-shift end-to-end testing; leave
# dedup intact so duplicate /estimate POSTs still skip.
IMMEDIATE_SEND_FOR_TESTING = os.getenv("IMMEDIATE_SEND_FOR_TESTING", "") == "1"

# BMS namespace
BMS_NS = "http://www.cieca.com/BMS"
NS = {"bms": BMS_NS}

# Job statuses
ESTIMATE_STATUSES = {"E", "EM", "EL", "EP"}
CLOSED_STATUSES = {"I", "C", "F", "FI", "FC", "WC"}

# SMS templates — WMH-01: Marco-editable via /templates endpoint.
#
# DEFAULT_TEMPLATES is the fall-back copy when the `templates` DB row for a
# given job_type is missing / empty / whitespace-only. Placeholders:
#   Per-row   : {first_name} {name} {phone} {vin} {vehicle_desc} {ro_id}
#               {doc_id} {email}
#   Shop-wide : {shop_name} {shop_phone} {review_url}
#
# The defaults parameterise shop name / phone / review URL so the literal
# brand values only live in SHOP_CONSTANTS below (single source of truth).
#
# Unknown placeholders render as empty string (see render_template with
# collections.defaultdict) — never as `{literal}`, never KeyError.
DEFAULT_TEMPLATES = {
    "24h":
        "Hi {first_name}, this is {shop_name}. Just following up on your recent estimate. "
        "Have questions or ready to schedule? Call us at {shop_phone}.",
    "3day":
        "Hi {first_name}, {shop_name} checking in about your estimate from a few days ago. "
        "We'd love to help get your car looking great! Call {shop_phone}.",
    "review":
        "Hi {first_name}, thank you for choosing {shop_name}! Hope you're happy with your repair. "
        "Would you mind leaving us a Google review? It means a lot: {review_url}",
}

SHOP_CONSTANTS = {
    "shop_name":  "Earl Scheib Of Concord",
    "shop_phone": "(925) 609-7780",
    "review_url": "https://g.page/r/review",
}

# Canonical order + metadata for the Templates admin UI. Drives the card
# order on the Templates page, the display labels, and the schedule hints.
JOB_TYPE_META = [
    {"job_type": "24h",    "label": "24-hour follow-up", "when": "~24 hours after estimate"},
    {"job_type": "3day",   "label": "3-day check-in",    "when": "~3 days after estimate"},
    {"job_type": "review", "label": "Review request",    "when": "~24 hours after job completion"},
]

# Placeholder catalog — rendered as clickable chips on the Templates page.
PLACEHOLDERS_PER_ROW = [
    "first_name", "name", "phone", "vin", "vehicle_desc", "ro_id", "doc_id", "email",
]
PLACEHOLDERS_SHOP = ["shop_name", "shop_phone", "review_url"]

# Legacy aliases — any import-time reference to MSG_24H / MSG_3DAY / MSG_REVIEW
# still resolves to the new default string. Kept intentionally to avoid breaking
# unknown importers; remove once a grep confirms no callers remain.
MSG_24H    = DEFAULT_TEMPLATES["24h"]
MSG_3DAY   = DEFAULT_TEMPLATES["3day"]
MSG_REVIEW = DEFAULT_TEMPLATES["review"]

logging.basicConfig(
    level=logging.INFO,
    format="%(asctime)s %(levelname)s %(message)s",
)
log = logging.getLogger(__name__)


# ---------------------------------------------------------------------------
# HMAC validation
# ---------------------------------------------------------------------------

def _validate_hmac(body: bytes, sig_header: str) -> bool:
    """Return True if X-EMS-Signature matches HMAC-SHA256(CCC_SECRET, body).
    Returns False if secret is unconfigured or signature is missing/wrong.
    Uses hmac.compare_digest for constant-time comparison (prevents timing attacks).
    """
    if not CCC_SECRET or not sig_header:
        return False
    expected = hmac.new(CCC_SECRET.encode("utf-8"), body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(expected, sig_header)


# ---------------------------------------------------------------------------
# Basic auth (RJL-02) — browser access to operator endpoints
# ---------------------------------------------------------------------------
#
# The watcher client POSTs with HMAC signatures; browsers cannot sign (no
# shared secret in JS). For the public /earlscheib admin UI the operator
# authenticates with HTTP Basic, and the same endpoints (GET /queue, DELETE
# /queue, POST /queue/send-now, GET /diagnostic) accept either HMAC OR
# basic-auth via _validate_auth. Machine-to-machine endpoints stay
# HMAC-only (see _validate_auth call-sites below).
#
# When ADMIN_UI_USER or ADMIN_UI_PASSWORD is unset the feature is disabled —
# /earlscheib returns 404 and basic-auth always fails, so the operational
# surface is identical to pre-RJL behaviour.

ADMIN_UI_USER = os.getenv("ADMIN_UI_USER", "")
ADMIN_UI_PASSWORD = os.getenv("ADMIN_UI_PASSWORD", "")
ADMIN_UI_ENABLED = bool(ADMIN_UI_USER and ADMIN_UI_PASSWORD)


def _validate_basic_auth(header: str) -> bool:
    """Return True if Authorization header is valid basic auth matching the
    configured ADMIN_UI_USER / ADMIN_UI_PASSWORD env vars. Uses
    hmac.compare_digest for constant-time comparison on both user AND
    password to avoid leaking the username via timing. Returns False if
    the feature is disabled (either env var empty).
    """
    if not ADMIN_UI_ENABLED or not header or not header.startswith("Basic "):
        return False
    try:
        decoded = base64.b64decode(header[6:]).decode("utf-8", errors="replace")
    except (ValueError, UnicodeDecodeError):
        return False
    user, _, pw = decoded.partition(":")
    return (
        hmac.compare_digest(user.encode("utf-8"), ADMIN_UI_USER.encode("utf-8"))
        and hmac.compare_digest(pw.encode("utf-8"), ADMIN_UI_PASSWORD.encode("utf-8"))
    )


def _validate_auth(handler, raw: bytes) -> bool:
    """Accept either a valid HMAC signature (watcher client) or valid
    basic auth (operator browser at /earlscheib). Both schemes are
    constant-time compared internally. Returns True on first success.
    """
    sig = handler.headers.get("X-EMS-Signature", "")
    if _validate_hmac(raw, sig):
        return True
    auth = handler.headers.get("Authorization", "")
    if _validate_basic_auth(auth):
        log.info("basic-auth: user=%s ip=%s path=%s",
                 ADMIN_UI_USER, handler.client_address[0], handler.path)
        return True
    return False


# ---------------------------------------------------------------------------
# Database
# ---------------------------------------------------------------------------

def init_db():
    con = sqlite3.connect(DB_PATH)
    cur = con.cursor()
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS jobs (
            id INTEGER PRIMARY KEY AUTOINCREMENT,
            doc_id TEXT NOT NULL,
            job_type TEXT NOT NULL,
            phone TEXT NOT NULL,
            name TEXT NOT NULL,
            send_at INTEGER NOT NULL,
            sent INTEGER NOT NULL DEFAULT 0,
            created_at INTEGER NOT NULL
        )
        """
    )
    con.commit()

    # OH4-01 migration: add richer customer/vehicle columns for the admin UI.
    # Each ALTER TABLE is wrapped individually so a duplicate-column error on
    # re-run is silently tolerated — safe for idempotent startup.
    #
    # QAJ-01 migration: add estimate_key for (phone + VIN)-based deduplication.
    # Collapses CCC ONE "Resave" bursts — same estimate being re-exported many
    # times a minute produces one pending job, not N. After adding the column,
    # run a one-time backfill for any existing row so the new dedup path has
    # keys to match against.
    migrations = [
        ("vin", "ALTER TABLE jobs ADD COLUMN vin TEXT DEFAULT ''"),
        ("vehicle_desc", "ALTER TABLE jobs ADD COLUMN vehicle_desc TEXT DEFAULT ''"),
        ("ro_id", "ALTER TABLE jobs ADD COLUMN ro_id TEXT DEFAULT ''"),
        ("email", "ALTER TABLE jobs ADD COLUMN email TEXT DEFAULT ''"),
        ("address", "ALTER TABLE jobs ADD COLUMN address TEXT DEFAULT ''"),
        ("sent_at", "ALTER TABLE jobs ADD COLUMN sent_at INTEGER DEFAULT 0"),
        ("estimate_key", "ALTER TABLE jobs ADD COLUMN estimate_key TEXT DEFAULT ''"),
    ]
    added = 0
    for col, stmt in migrations:
        try:
            cur.execute(stmt)
            con.commit()
            added += 1
        except sqlite3.OperationalError:
            # Column already exists — idempotent, continue.
            pass
    log.info("DB migrated: +%d columns (0 if already applied)", added)

    # QAJ-01 one-time backfill: populate estimate_key for any pre-existing
    # jobs row that still has the empty default. Key formula matches
    # schedule_job: "<phone>|<vin>" when VIN is present, otherwise
    # "<phone>|<doc_id>" so rows without a VIN still dedup on resave.
    try:
        cur.execute(
            "UPDATE jobs SET estimate_key = phone || '|' || "
            "COALESCE(NULLIF(vin, ''), doc_id) WHERE estimate_key = ''"
        )
        con.commit()
        if cur.rowcount:
            log.info("QAJ-01 backfill: populated estimate_key on %d rows", cur.rowcount)
    except sqlite3.OperationalError as exc:
        # Should not happen after the ALTER above; log and continue.
        log.warning("QAJ-01 backfill skipped: %s", exc)

    # WMH-01: Marco-editable message templates. One row per job_type; absence
    # of a row means "use DEFAULT_TEMPLATES[job_type]". No seed INSERT — the
    # override-vs-default distinction is what makes "clear to revert" work.
    cur.execute(
        """
        CREATE TABLE IF NOT EXISTS templates (
            job_type   TEXT PRIMARY KEY,
            body       TEXT NOT NULL,
            updated_at INTEGER NOT NULL
        )
        """
    )
    con.commit()

    con.close()
    log.info("DB initialised at %s", DB_PATH)


# ---------------------------------------------------------------------------
# Template rendering (WMH-01)
# ---------------------------------------------------------------------------

def _get_template_override(job_type: str) -> str:
    """Return the stored override body for a job_type, or '' if none exists.

    Empty / whitespace-only stored bodies are treated as "no override" — the
    caller will fall back to DEFAULT_TEMPLATES. This matches the PUT-empty-body
    semantics (which deletes the row); both produce default behaviour without
    the caller needing to distinguish the two cases.
    """
    try:
        con = get_db()
        try:
            cur = con.cursor()
            cur.execute(
                "SELECT body FROM templates WHERE job_type = ?", (job_type,)
            )
            row = cur.fetchone()
        finally:
            con.close()
    except sqlite3.OperationalError as exc:
        # templates table missing (e.g. tests that bypass init_db) — fall back.
        log.warning("_get_template_override: %s", exc)
        return ""
    if not row:
        return ""
    body = row["body"] if hasattr(row, "keys") else row[0]
    return body if (body and body.strip()) else ""


def render_template(job_type: str, row) -> str:
    """Render the SMS body for a job_type using the row's per-customer data.

    Lookup order:
      1. templates table row for this job_type (override, if non-empty)
      2. DEFAULT_TEMPLATES[job_type]
      3. "" for unknown job_types (caller should log.warning + skip)

    Interpolation is str.format_map with a collections.defaultdict(str) so
    unknown placeholders render as empty string — never KeyError, never
    the literal "{key}" leaking to a customer's phone.

    Supported placeholders:
      Per-row  : first_name, name, phone, vin, vehicle_desc, ro_id, doc_id,
                 email  (pulled from `row`)
      Shop     : shop_name, shop_phone, review_url  (SHOP_CONSTANTS)

    first_name is derived from name.split()[0] when the row has no explicit
    first_name column; falls back to "there" so the message never reads
    "Hi , this is ..." — preserves the existing UX of the pre-WMH templates.
    """
    tpl = _get_template_override(job_type) or DEFAULT_TEMPLATES.get(job_type, "")
    if not tpl:
        # Unknown job_type.
        return ""

    ctx = defaultdict(str)
    ctx.update(SHOP_CONSTANTS)

    # Sniff row shape once — sqlite3.Row exposes keys() like a dict but is not
    # a dict itself. For plain dicts `key in dict` is membership; sqlite3.Row
    # needs `key in row.keys()`. Normalise to a small dict.
    if row is not None:
        try:
            keys = set(row.keys()) if hasattr(row, "keys") else set()
        except (AttributeError, TypeError):
            keys = set()
        for k in PLACEHOLDERS_PER_ROW:
            if k in keys:
                try:
                    v = row[k]
                except (KeyError, IndexError):
                    v = ""
                if v:
                    ctx[k] = v

    # Derive first_name if not explicitly present on the row.
    if not ctx["first_name"] and ctx["name"]:
        ctx["first_name"] = str(ctx["name"]).split()[0]
    if not ctx["first_name"]:
        ctx["first_name"] = "there"  # preserves current fallback UX

    try:
        return tpl.format_map(ctx)
    except (KeyError, IndexError, ValueError) as exc:
        # format_map with defaultdict swallows missing keys, so the only way
        # to get here is a malformed template (unclosed brace, bad spec).
        # Log and return the default rather than crash the sender — a bad
        # template was already saved (the PUT validator should have caught
        # this) but we don't want to block every subsequent send.
        log.error("render_template: malformed template for %s: %s", job_type, exc)
        fallback = DEFAULT_TEMPLATES.get(job_type, "")
        try:
            return fallback.format_map(ctx)
        except Exception:
            return ""


def get_db():
    con = sqlite3.connect(DB_PATH)
    con.row_factory = sqlite3.Row
    return con


# ---------------------------------------------------------------------------
# Job scheduling
# ---------------------------------------------------------------------------


LAST_HEARTBEAT = {"ts": None, "host": None}
def schedule_job(
    doc_id: str,
    job_type: str,
    phone: str,
    name: str,
    send_at: int,
    vin: str = "",
    vehicle_desc: str = "",
    ro_id: str = "",
    email: str = "",
    address: str = "",
):
    """Schedule a follow-up SMS, with CCC-ONE-resave-tolerant dedup.

    QAJ-01: dedup key is now ``(phone + VIN)`` rather than ``(doc_id)``. CCC ONE
    emits a new DocumentVerCode on every "Resave" — sometimes 20+ copies of the
    same estimate inside a minute. Keying on the stable (phone, VIN) pair
    collapses the burst into a single pending job while still letting a brand
    new estimate (different phone OR different VIN) schedule cleanly.

    Behaviour by existing row state for ``(estimate_key, job_type)``:
      - No existing row                → INSERT (fresh estimate).
      - Existing row, sent=0           → UPDATE customer/vehicle fields on
                                         the pending row; keep original
                                         send_at so the window isn't reset.
                                         Useful when CCC re-saves with a
                                         corrected phone / VIN / name.
      - Existing row, sent=1, < 60d    → SKIP (already delivered recently).
      - Existing row, sent=1, >= 60d   → INSERT (genuine new visit; treat as
                                         a fresh estimate for reopen).

    Falls back to ``"<phone>|<doc_id>"`` when VIN is missing so the dedup still
    triggers across repeated doc_id sightings. All new columns default to ""
    for backward-compatible positional callers.
    """
    estimate_key = f"{phone}|{vin}" if vin else f"{phone}|{doc_id}"

    con = get_db()
    try:
        cur = con.cursor()
        cur.execute(
            "SELECT id, sent, created_at FROM jobs "
            "WHERE estimate_key = ? AND job_type = ? "
            "ORDER BY id DESC LIMIT 1",
            (estimate_key, job_type),
        )
        existing = cur.fetchone()
        now = int(time.time())

        if existing:
            eid = existing["id"]
            sent = existing["sent"]
            created_at = existing["created_at"]

            if sent == 0:
                # Refresh customer/vehicle data on the pending row; preserve
                # send_at and created_at so the scheduled window doesn't reset.
                cur.execute(
                    "UPDATE jobs SET name=?, phone=?, vin=?, vehicle_desc=?, "
                    " ro_id=?, email=?, address=?, doc_id=? "
                    "WHERE id=?",
                    (name, phone, vin, vehicle_desc, ro_id, email, address,
                     doc_id, eid),
                )
                con.commit()
                log.info(
                    "updated pending job: id=%s estimate_key=%s job_type=%s "
                    "(CCC resave collapsed)",
                    eid, estimate_key, job_type,
                )
                return

            # sent == 1: only reopen if the previous delivery is > 60 days old.
            if created_at >= now - 60 * 86400:
                log.info(
                    "skip duplicate: sent job for estimate_key=%s job_type=%s "
                    "within 60d window (created_at=%s)",
                    estimate_key, job_type, created_at,
                )
                return
            # Fall through to INSERT — stale sent row, treat as new visit.
            log.info(
                "reopening estimate_key=%s job_type=%s — previous send was "
                ">60d ago",
                estimate_key, job_type,
            )

        # OH4-04 testing override — after dedup, before INSERT. Collapses
        # 24h / 72h / review scheduling to now+60s. Dedup still works so
        # a re-POST of the same estimate_key won't double-schedule.
        if IMMEDIATE_SEND_FOR_TESTING:
            new_send_at = int(time.time()) + 60
            log.info(
                "IMMEDIATE_SEND_FOR_TESTING=1 — overriding send_at "
                "from %s to %s (now+60) for doc_id=%s job_type=%s",
                send_at, new_send_at, doc_id, job_type,
            )
            send_at = new_send_at

        cur.execute(
            "INSERT INTO jobs "
            "(doc_id, job_type, phone, name, send_at, sent, created_at, "
            " vin, vehicle_desc, ro_id, email, address, sent_at, estimate_key) "
            "VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, ?, 0, ?)",
            (doc_id, job_type, phone, name, send_at, now,
             vin, vehicle_desc, ro_id, email, address, estimate_key),
        )
        con.commit()
        log.info(
            "Scheduled job: doc_id=%s job_type=%s phone=%s send_at=%s "
            "estimate_key=%s vehicle=%r ro=%s",
            doc_id, job_type, phone, send_at, estimate_key,
            vehicle_desc, ro_id,
        )
    finally:
        con.close()


# ---------------------------------------------------------------------------
# SMS sending
# ---------------------------------------------------------------------------

def send_sms(to: str, body: str) -> bool:
    """Send an SMS/WhatsApp message via Twilio.

    During testing, recipient resolution follows:
      1. TEST_PHONE_RECIPIENTS (comma-sep list)  — fan out to every entry.
      2. TEST_PHONE_OVERRIDE  (single number)    — replace `to`.
      3. Default                                 — use `to`.

    When fan-out is active, returns True only if EVERY recipient delivered.
    """
    if TEST_PHONE_RECIPIENTS:
        recipients = [clean_phone(p) for p in TEST_PHONE_RECIPIENTS]
        log.info("TEST_PHONE_RECIPIENTS fan-out: %s -> %s", to, ",".join(recipients))
    elif TEST_PHONE_OVERRIDE:
        recipients = [clean_phone(TEST_PHONE_OVERRIDE)]
        log.info("TEST_PHONE_OVERRIDE: %s -> %s", to, recipients[0])
    else:
        recipients = [to]

    all_ok = True
    for recipient in recipients:
        if not _send_single_sms(recipient, body):
            all_ok = False
    return all_ok


def _send_single_sms(to: str, body: str) -> bool:
    """Deliver one SMS/WhatsApp message via Twilio REST API."""
    url = f"https://api.twilio.com/2010-04-01/Accounts/{TWILIO_ACCOUNT_SID}/Messages.json"
    # ===== Twilio WhatsApp (sandbox) -> SMS (production) switch =====
    # Currently using Twilio WhatsApp sandbox for dev/test.
    # To switch to production SMS:
    #   1. In .env, change TWILIO_FROM from "whatsapp:+14155238886" to your
    #      Twilio SMS number (e.g. "+15551234567")
    #   2. In this file, remove the "whatsapp:" prefix from both `from_number` and
    #      `to_number` assignments below (remove the "whatsapp:" prefix from To/From
    #      in the Twilio API call)
    # No other changes needed. The rest of the scheduler, HMAC validation, and
    # dedup logic is SMS/WhatsApp agnostic.
    # ================================================================
    from_number = TWILIO_FROM
    to_number = f"whatsapp:{to}"

    # URL-encode each field — body contains spaces, apostrophes, parens,
    # slashes, colons. Raw f-string interpolation silently produces
    # invalid form bodies and Twilio returns 400 Bad Request.
    from urllib.parse import urlencode
    data = urlencode({"From": from_number, "To": to_number, "Body": body}).encode("utf-8")

    credentials = f"{TWILIO_API_KEY}:{TWILIO_API_SECRET}"
    encoded = base64.b64encode(credentials.encode("utf-8")).decode("utf-8")

    req = Request(url, data=data, method="POST")
    req.add_header("Authorization", f"Basic {encoded}")
    req.add_header("Content-Type", "application/x-www-form-urlencoded")

    try:
        with urlopen(req, timeout=15) as resp:
            resp_body = resp.read().decode("utf-8")
            log.info("Twilio response %s to=%s: %s", resp.status, to, resp_body[:200])
            return resp.status in (200, 201)
    except URLError as exc:
        log.error("Twilio request failed to=%s: %s", to, exc)
        return False


# ---------------------------------------------------------------------------
# Scheduler loop
# ---------------------------------------------------------------------------

def scheduler_loop():
    """Background thread: fires due jobs every 30 seconds."""
    log.info("Scheduler started")
    while True:
        try:
            _fire_due_jobs()
        except Exception as exc:
            log.error("Scheduler error: %s", exc)
        time.sleep(30)


def _fire_due_jobs():
    now = int(time.time())
    con = get_db()
    try:
        cur = con.cursor()
        cur.execute(
            "SELECT * FROM jobs WHERE sent = 0 AND send_at <= ?",
            (now,),
        )
        rows = cur.fetchall()
        for row in rows:
            job_id = row["id"]
            job_type = row["job_type"]
            phone = row["phone"]

            # WMH-01: body composition is now delegated to render_template,
            # which reads any per-job_type override from the templates table
            # (falling back to DEFAULT_TEMPLATES) and fills placeholders from
            # the full row (name, vin, vehicle_desc, ro_id, email, …).
            body = render_template(job_type, row)
            if not body:
                log.warning("Unknown job_type %s for job %s", job_type, job_id)
                continue

            log.info("Firing job %s: type=%s phone=%s", job_id, job_type, phone)
            success = send_sms(phone, body)
            if success:
                cur.execute("UPDATE jobs SET sent = 1 WHERE id = ?", (job_id,))
                con.commit()
                log.info("Job %s marked sent", job_id)
            else:
                log.warning("Job %s send failed, will retry", job_id)
    finally:
        con.close()


# ---------------------------------------------------------------------------
# BMS XML parsing
# ---------------------------------------------------------------------------

def clean_phone(raw: str) -> str:
    """Normalize a phone number to E.164 format (+1XXXXXXXXXX for US)."""
    if not raw:
        return ""
    digits = re.sub(r"\D", "", raw)
    if len(digits) == 10:
        return f"+1{digits}"
    if len(digits) == 11 and digits.startswith("1"):
        return f"+{digits}"
    return ""


def parse_bms(xml_bytes: bytes) -> dict:
    """Parse CCC BMS XML and return a dict with extracted fields."""
    try:
        root = ET.fromstring(xml_bytes)
    except ET.ParseError as exc:
        log.error("XML parse error: %s", exc)
        return {}

    def find_text(elem, path):
        node = elem.find(path, NS)
        if node is not None and node.text:
            return node.text.strip()
        return ""

    # DocumentVerCode / DocumentStatus
    doc_id = find_text(root, ".//bms:DocumentVerCode")
    if not doc_id:
        doc_id = find_text(root, ".//bms:DocumentID")
    doc_ver = find_text(root, ".//bms:DocumentVerCode")
    doc_status = find_text(root, ".//bms:DocumentStatus")

    # Dates
    close_dt = find_text(root, ".//bms:EventInfo/bms:RepairEvent/bms:CloseDateTime")
    pickup_dt = find_text(root, ".//bms:ActualPickupDateTime")

    # Customer name from Owner
    first = find_text(root, ".//bms:Owner/bms:GivenName")
    last = find_text(root, ".//bms:Owner/bms:OtherOrSurName")
    name = " ".join(filter(None, [first, last])).strip() or "there"

    # Phone: first valid CommPhone, then CommCell, then CommNumber
    phone = ""
    for tag in ("bms:CommPhone", "bms:CommCell", "bms:CommNumber"):
        for node in root.iter(f"{{{BMS_NS}}}{tag.split(':')[1]}"):
            candidate = clean_phone(node.text or "")
            if candidate:
                phone = candidate
                break
        if phone:
            break

    # OH4-01: vehicle + RO + contact enrichment. Matches the extended
    # RenderBMS emission in internal/ems/bms.go (VIN/Year/Make/Model/ROId
    # under <VehicleInfo>, CommEmail under <Owner>). Uses .//bms:TAG so
    # nesting depth is irrelevant — each tag appears at most once under
    # the single BMSTrans subtree.
    vin = find_text(root, ".//bms:VIN")
    year = find_text(root, ".//bms:Year")
    make_ = find_text(root, ".//bms:Make")  # trailing underscore — avoid builtin shadow
    model = find_text(root, ".//bms:Model")
    ro_id = find_text(root, ".//bms:ROId")
    email = find_text(root, ".//bms:CommEmail")
    address = find_text(root, ".//bms:CommAddr")
    vehicle_desc = " ".join(filter(None, [year, make_, model])).strip()

    return {
        "doc_id": doc_id,
        "doc_ver": doc_ver,
        "doc_status": doc_status,
        "close_dt": close_dt,
        "pickup_dt": pickup_dt,
        "name": name,
        "phone": phone,
        "vin": vin,
        "vehicle_desc": vehicle_desc,
        "ro_id": ro_id,
        "email": email,
        "address": address,
    }


# ---------------------------------------------------------------------------
# HTTP Handler
# ---------------------------------------------------------------------------

SUPPORT_HTML = """\
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Earl Scheib Auto Body — Customer Follow-Up</title>
  <style>
    *, *::before, *::after {{ box-sizing: border-box; margin: 0; padding: 0; }}

    :root {{
      --red: #CC0000;
      --red-dark: #a30000;
      --red-light: #f5e0e0;
      --gray-50: #f9fafb;
      --gray-100: #f3f4f6;
      --gray-200: #e5e7eb;
      --gray-400: #9ca3af;
      --gray-600: #4b5563;
      --gray-700: #374151;
      --gray-900: #111827;
      --white: #ffffff;
      --shadow-sm: 0 1px 3px rgba(0,0,0,0.08), 0 1px 2px rgba(0,0,0,0.06);
      --shadow-md: 0 4px 16px rgba(0,0,0,0.10), 0 2px 6px rgba(0,0,0,0.06);
      --shadow-lg: 0 10px 40px rgba(0,0,0,0.12), 0 4px 12px rgba(0,0,0,0.06);
      --radius: 12px;
      --radius-sm: 8px;
    }}

    html {{ scroll-behavior: smooth; }}

    body {{
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
      color: var(--gray-700);
      background: var(--white);
      line-height: 1.6;
      -webkit-font-smoothing: antialiased;
    }}

    /* ── NAV ── */
    nav {{
      position: sticky;
      top: 0;
      z-index: 100;
      background: rgba(255,255,255,0.92);
      backdrop-filter: blur(12px);
      -webkit-backdrop-filter: blur(12px);
      border-bottom: 1px solid var(--gray-200);
    }}

    .nav-inner {{
      max-width: 1100px;
      margin: 0 auto;
      padding: 0 24px;
      height: 64px;
      display: flex;
      align-items: center;
      justify-content: space-between;
    }}

    .nav-brand {{
      display: flex;
      align-items: center;
      gap: 12px;
      text-decoration: none;
    }}

    .nav-logo-mark {{
      width: 36px;
      height: 36px;
      background: var(--red);
      border-radius: 8px;
      display: flex;
      align-items: center;
      justify-content: center;
      flex-shrink: 0;
    }}

    .nav-logo-mark svg {{
      width: 20px;
      height: 20px;
      fill: white;
    }}

    .nav-title {{
      font-size: 15px;
      font-weight: 700;
      color: var(--gray-900);
      letter-spacing: -0.01em;
    }}

    .nav-title span {{
      display: block;
      font-size: 11px;
      font-weight: 500;
      color: var(--gray-400);
      letter-spacing: 0.02em;
    }}

    .nav-badge {{
      font-size: 12px;
      font-weight: 600;
      color: var(--red);
      background: var(--red-light);
      padding: 4px 10px;
      border-radius: 999px;
      border: 1px solid rgba(204,0,0,0.15);
      white-space: nowrap;
    }}

    /* ── HERO ── */
    .hero {{
      background: linear-gradient(135deg, var(--gray-900) 0%, #1c1c2e 100%);
      color: var(--white);
      padding: 96px 24px 88px;
      text-align: center;
      position: relative;
      overflow: hidden;
    }}

    .hero::before {{
      content: "";
      position: absolute;
      inset: 0;
      background: radial-gradient(ellipse 80% 60% at 50% 0%, rgba(204,0,0,0.18) 0%, transparent 70%);
      pointer-events: none;
    }}

    .hero-content {{
      position: relative;
      max-width: 720px;
      margin: 0 auto;
    }}

    .hero-eyebrow {{
      display: inline-flex;
      align-items: center;
      gap: 6px;
      font-size: 13px;
      font-weight: 600;
      letter-spacing: 0.08em;
      text-transform: uppercase;
      color: var(--red);
      background: rgba(204,0,0,0.12);
      border: 1px solid rgba(204,0,0,0.25);
      padding: 5px 14px;
      border-radius: 999px;
      margin-bottom: 28px;
    }}

    .hero h1 {{
      font-size: clamp(2rem, 5vw, 3.25rem);
      font-weight: 800;
      line-height: 1.15;
      letter-spacing: -0.03em;
      margin-bottom: 20px;
      color: var(--white);
    }}

    .hero h1 em {{
      font-style: normal;
      color: var(--red);
    }}

    .hero p {{
      font-size: clamp(1rem, 2vw, 1.2rem);
      color: rgba(255,255,255,0.72);
      max-width: 580px;
      margin: 0 auto 36px;
      line-height: 1.7;
    }}

    .hero-cta {{
      display: inline-flex;
      align-items: center;
      gap: 8px;
      background: var(--red);
      color: var(--white);
      font-size: 15px;
      font-weight: 600;
      padding: 14px 28px;
      border-radius: var(--radius-sm);
      text-decoration: none;
      transition: background 0.15s, transform 0.15s;
      box-shadow: 0 4px 14px rgba(204,0,0,0.4);
    }}

    .hero-cta:hover {{ background: var(--red-dark); transform: translateY(-1px); }}

    .hero-stats {{
      display: flex;
      justify-content: center;
      gap: 40px;
      margin-top: 56px;
      padding-top: 40px;
      border-top: 1px solid rgba(255,255,255,0.1);
      flex-wrap: wrap;
    }}

    .hero-stat {{
      text-align: center;
    }}

    .hero-stat strong {{
      display: block;
      font-size: 2rem;
      font-weight: 800;
      color: var(--white);
      letter-spacing: -0.03em;
    }}

    .hero-stat span {{
      font-size: 13px;
      color: rgba(255,255,255,0.5);
      letter-spacing: 0.02em;
    }}

    /* ── SECTION WRAPPER ── */
    section {{
      padding: 80px 24px;
    }}

    .container {{
      max-width: 1100px;
      margin: 0 auto;
    }}

    .section-label {{
      font-size: 12px;
      font-weight: 700;
      letter-spacing: 0.1em;
      text-transform: uppercase;
      color: var(--red);
      margin-bottom: 12px;
    }}

    .section-title {{
      font-size: clamp(1.5rem, 3vw, 2.25rem);
      font-weight: 800;
      color: var(--gray-900);
      letter-spacing: -0.025em;
      line-height: 1.2;
      margin-bottom: 14px;
    }}

    .section-sub {{
      font-size: 1.05rem;
      color: var(--gray-600);
      max-width: 560px;
      line-height: 1.7;
    }}

    .section-header {{
      margin-bottom: 52px;
    }}

    /* ── FEATURES ── */
    #features {{
      background: var(--gray-50);
    }}

    .cards {{
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(280px, 1fr));
      gap: 24px;
    }}

    .card {{
      background: var(--white);
      border: 1px solid var(--gray-200);
      border-radius: var(--radius);
      padding: 32px 28px;
      box-shadow: var(--shadow-sm);
      transition: box-shadow 0.2s, transform 0.2s;
    }}

    .card:hover {{
      box-shadow: var(--shadow-md);
      transform: translateY(-2px);
    }}

    .card-icon {{
      width: 48px;
      height: 48px;
      background: var(--red-light);
      border-radius: var(--radius-sm);
      display: flex;
      align-items: center;
      justify-content: center;
      margin-bottom: 20px;
    }}

    .card-icon svg {{
      width: 24px;
      height: 24px;
      stroke: var(--red);
      fill: none;
      stroke-width: 2;
      stroke-linecap: round;
      stroke-linejoin: round;
    }}

    .card h3 {{
      font-size: 1.1rem;
      font-weight: 700;
      color: var(--gray-900);
      margin-bottom: 10px;
      letter-spacing: -0.01em;
    }}

    .card p {{
      font-size: 0.95rem;
      color: var(--gray-600);
      line-height: 1.65;
    }}

    .card-tag {{
      display: inline-block;
      margin-top: 16px;
      font-size: 11px;
      font-weight: 700;
      letter-spacing: 0.06em;
      text-transform: uppercase;
      color: var(--red);
    }}

    /* ── HOW IT WORKS ── */
    #how-it-works {{
      background: var(--white);
    }}

    .steps {{
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(260px, 1fr));
      gap: 0;
      position: relative;
    }}

    .steps::before {{
      content: "";
      position: absolute;
      top: 28px;
      left: calc(16.66% + 0px);
      right: calc(16.66% + 0px);
      height: 2px;
      background: linear-gradient(90deg, var(--red-light), var(--red), var(--red-light));
      display: none;
    }}

    @media (min-width: 800px) {{
      .steps::before {{ display: block; }}
    }}

    .step {{
      padding: 0 24px 0 0;
      position: relative;
    }}

    .step:last-child {{ padding-right: 0; }}

    .step-number {{
      width: 56px;
      height: 56px;
      background: var(--red);
      color: var(--white);
      font-size: 1.3rem;
      font-weight: 800;
      border-radius: 50%;
      display: flex;
      align-items: center;
      justify-content: center;
      margin-bottom: 20px;
      box-shadow: 0 4px 14px rgba(204,0,0,0.35);
      letter-spacing: -0.02em;
    }}

    .step h3 {{
      font-size: 1.05rem;
      font-weight: 700;
      color: var(--gray-900);
      margin-bottom: 10px;
      letter-spacing: -0.01em;
    }}

    .step p {{
      font-size: 0.95rem;
      color: var(--gray-600);
      line-height: 1.65;
    }}

    /* ── PRIVACY ── */
    #privacy {{
      background: var(--gray-900);
      color: var(--white);
    }}

    #privacy .section-title {{
      color: var(--white);
    }}

    #privacy .section-sub {{
      color: rgba(255,255,255,0.65);
    }}

    .privacy-grid {{
      display: grid;
      grid-template-columns: repeat(auto-fit, minmax(240px, 1fr));
      gap: 20px;
      margin-top: 40px;
    }}

    .privacy-item {{
      background: rgba(255,255,255,0.06);
      border: 1px solid rgba(255,255,255,0.1);
      border-radius: var(--radius-sm);
      padding: 24px 22px;
    }}

    .privacy-item-icon {{
      font-size: 1.4rem;
      margin-bottom: 12px;
    }}

    .privacy-item h4 {{
      font-size: 0.95rem;
      font-weight: 700;
      color: var(--white);
      margin-bottom: 8px;
    }}

    .privacy-item p {{
      font-size: 0.875rem;
      color: rgba(255,255,255,0.6);
      line-height: 1.6;
    }}

    /* ── CONTACT ── */
    #contact {{
      background: var(--gray-50);
    }}

    .contact-grid {{
      display: grid;
      grid-template-columns: 1fr 1fr;
      gap: 32px;
      align-items: start;
    }}

    @media (max-width: 700px) {{
      .contact-grid {{ grid-template-columns: 1fr; }}
    }}

    .contact-card {{
      background: var(--white);
      border: 1px solid var(--gray-200);
      border-radius: var(--radius);
      padding: 32px 28px;
      box-shadow: var(--shadow-md);
    }}

    .contact-card-label {{
      font-size: 11px;
      font-weight: 700;
      letter-spacing: 0.1em;
      text-transform: uppercase;
      color: var(--red);
      margin-bottom: 14px;
    }}

    .contact-info h3 {{
      font-size: 1.2rem;
      font-weight: 800;
      color: var(--gray-900);
      letter-spacing: -0.02em;
      margin-bottom: 4px;
    }}

    .contact-info .subtitle {{
      font-size: 0.875rem;
      color: var(--gray-400);
      margin-bottom: 22px;
    }}

    .contact-detail {{
      display: flex;
      align-items: center;
      gap: 12px;
      margin-bottom: 14px;
    }}

    .contact-detail-icon {{
      width: 36px;
      height: 36px;
      background: var(--red-light);
      border-radius: var(--radius-sm);
      display: flex;
      align-items: center;
      justify-content: center;
      flex-shrink: 0;
    }}

    .contact-detail-icon svg {{
      width: 16px;
      height: 16px;
      stroke: var(--red);
      fill: none;
      stroke-width: 2;
      stroke-linecap: round;
      stroke-linejoin: round;
    }}

    .contact-detail-text strong {{
      display: block;
      font-size: 0.875rem;
      font-weight: 700;
      color: var(--gray-900);
    }}

    .contact-detail-text span {{
      font-size: 0.8rem;
      color: var(--gray-400);
    }}

    .contact-detail-text a {{
      color: var(--red);
      text-decoration: none;
      font-size: 0.875rem;
      font-weight: 600;
    }}

    .contact-detail-text a:hover {{
      text-decoration: underline;
    }}

    /* ── FOOTER ── */
    footer {{
      background: var(--gray-900);
      color: rgba(255,255,255,0.5);
      padding: 32px 24px;
      text-align: center;
    }}

    .footer-inner {{
      max-width: 1100px;
      margin: 0 auto;
      display: flex;
      flex-direction: column;
      align-items: center;
      gap: 10px;
    }}

    .footer-brand {{
      font-size: 0.95rem;
      font-weight: 700;
      color: rgba(255,255,255,0.8);
    }}

    .footer-copy {{
      font-size: 0.8rem;
    }}

    .footer-powered {{
      font-size: 0.8rem;
      color: rgba(255,255,255,0.35);
    }}

    /* ── RESPONSIVE ── */
    @media (max-width: 640px) {{
      section {{ padding: 56px 20px; }}
      .hero {{ padding: 72px 20px 64px; }}
      .hero-stats {{ gap: 28px; }}
      .steps {{ gap: 36px; }}
      .step {{ padding-right: 0; }}
      nav .nav-badge {{ display: none; }}
    }}
  </style>
</head>
<body>

  <!-- NAV -->
  <nav>
    <div class="nav-inner">
      <a class="nav-brand" href="#">
        <div class="nav-logo-mark">
          <svg viewBox="0 0 24 24" xmlns="http://www.w3.org/2000/svg">
            <path d="M19 17H5a2 2 0 0 1-2-2V9a2 2 0 0 1 2-2h1l2-3h8l2 3h1a2 2 0 0 1 2 2v6a2 2 0 0 1-2 2z"/>
            <circle cx="7.5" cy="14.5" r="1.5"/>
            <circle cx="16.5" cy="14.5" r="1.5"/>
          </svg>
        </div>
        <div class="nav-title">
          Earl Scheib Auto Body
          <span>Customer Follow-Up</span>
        </div>
      </a>
      <div class="nav-badge">Powered by CCC ONE</div>
    </div>
  </nav>

  <!-- HERO -->
  <div class="hero">
    <div class="hero-content">
      <div class="hero-eyebrow">
        <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round" style="display:inline;vertical-align:middle;">
          <path d="M22 16.92v3a2 2 0 0 1-2.18 2 19.79 19.79 0 0 1-8.63-3.07A19.5 19.5 0 0 1 4.36 12 19.79 19.79 0 0 1 1.08 3.38a2 2 0 0 1 1.99-2.18h3a2 2 0 0 1 2 1.72c.127.96.361 1.903.7 2.81a2 2 0 0 1-.45 2.11L7.09 8.91a16 16 0 0 0 6 6l1.27-1.27a2 2 0 0 1 2.11-.45c.907.339 1.85.573 2.81.7A2 2 0 0 1 21 16z"/>
        </svg>
        Automated Customer Communication
      </div>
      <h1>Keep Every Customer<br /><em>In the Loop</em></h1>
      <p>Earl Scheib Auto Body uses automated SMS follow-ups powered by CCC ONE to keep customers informed — from estimate to final delivery — without lifting a finger.</p>
      <a class="hero-cta" href="#contact">
        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.5" stroke-linecap="round" stroke-linejoin="round">
          <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z"/>
        </svg>
        Contact Support
      </a>
      <div class="hero-stats">
        <div class="hero-stat">
          <strong>100%</strong>
          <span>Automated Follow-Up</span>
        </div>
        <div class="hero-stat">
          <strong>SMS</strong>
          <span>Direct to Customer</span>
        </div>
        <div class="hero-stat">
          <strong>CCC ONE</strong>
          <span>Integrated Platform</span>
        </div>
      </div>
    </div>
  </div>

  <!-- FEATURES -->
  <section id="features">
    <div class="container">
      <div class="section-header">
        <div class="section-label">What We Offer</div>
        <h2 class="section-title">Powerful Features,<br />Zero Extra Work</h2>
        <p class="section-sub">Our system integrates directly with CCC ONE so your team doesn't need to manage customer communication manually.</p>
      </div>
      <div class="cards">
        <!-- Card 1 -->
        <div class="card">
          <div class="card-icon">
            <svg viewBox="0 0 24 24">
              <path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>
              <polyline points="14 2 14 8 20 8"/>
              <line x1="16" y1="13" x2="8" y2="13"/>
              <line x1="16" y1="17" x2="8" y2="17"/>
              <polyline points="10 9 9 9 8 9"/>
            </svg>
          </div>
          <h3>Estimate Follow-Up</h3>
          <p>When a technician completes an estimate in CCC ONE, the customer automatically receives an SMS summarizing next steps and inviting them to approve or ask questions — keeping the repair process moving.</p>
          <div class="card-tag">Triggered by CCC ONE</div>
        </div>
        <!-- Card 2 -->
        <div class="card">
          <div class="card-icon">
            <svg viewBox="0 0 24 24">
              <path d="M22 11.08V12a10 10 0 1 1-5.93-9.14"/>
              <polyline points="22 4 12 14.01 9 11.01"/>
            </svg>
          </div>
          <h3>Job Completion Review Request</h3>
          <p>Once the repair is complete and the vehicle is delivered, the customer receives a courteous SMS asking for a Google review — helping Earl Scheib Auto Body build its online reputation effortlessly.</p>
          <div class="card-tag">Sent at Delivery</div>
        </div>
      </div>
    </div>
  </section>

  <!-- HOW IT WORKS -->
  <section id="how-it-works">
    <div class="container">
      <div class="section-header">
        <div class="section-label">The Process</div>
        <h2 class="section-title">How It Works</h2>
        <p class="section-sub">Three simple steps from estimate to review — handled automatically, every time.</p>
      </div>
      <div class="steps">
        <div class="step">
          <div class="step-number">1</div>
          <h3>Shop Completes Estimate in CCC ONE</h3>
          <p>Your team writes up the repair estimate as usual inside CCC ONE. No extra steps needed — the follow-up system is triggered automatically when the estimate is finalized.</p>
        </div>
        <div class="step">
          <div class="step-number">2</div>
          <h3>Customer Receives Follow-Up SMS</h3>
          <p>The customer gets a professional, friendly text message letting them know their estimate is ready and what to expect next. Quick, clear, and personal — without any manual effort from your staff.</p>
        </div>
        <div class="step">
          <div class="step-number">3</div>
          <h3>After Repair, Customer Receives Review Request</h3>
          <p>Once the vehicle is repaired and returned, the customer receives a follow-up SMS thanking them and asking for a Google review — helping grow your shop's reputation automatically.</p>
        </div>
      </div>
    </div>
  </section>

  <!-- PRIVACY -->
  <section id="privacy">
    <div class="container">
      <div class="section-header">
        <div class="section-label">Your Privacy</div>
        <h2 class="section-title">We Respect Your Data</h2>
        <p class="section-sub">Customer information is handled with care and is used solely for repair-related communication.</p>
      </div>
      <div class="privacy-grid">
        <div class="privacy-item">
          <div class="privacy-item-icon">&#x1F512;</div>
          <h4>Limited Use</h4>
          <p>Your phone number and contact information are used only to send follow-up messages related to your vehicle repair. We never sell or share your data with third parties.</p>
        </div>
        <div class="privacy-item">
          <div class="privacy-item-icon">&#x1F4CB;</div>
          <h4>Purpose-Specific</h4>
          <p>Messages are sent only in connection with an active or completed repair estimate at Earl Scheib Auto Body Concord. No unsolicited marketing messages.</p>
        </div>
        <div class="privacy-item">
          <div class="privacy-item-icon">&#x1F6E1;&#xFE0F;</div>
          <h4>Compliant &amp; Secure</h4>
          <p>Our SMS system follows all applicable TCPA and carrier messaging guidelines, ensuring your communication rights are fully protected at all times.</p>
        </div>
      </div>
    </div>
  </section>

  <!-- CONTACT -->
  <section id="contact">
    <div class="container">
      <div class="section-header">
        <div class="section-label">Get in Touch</div>
        <h2 class="section-title">Contact &amp; Support</h2>
        <p class="section-sub">Reach the app developer for technical support, or contact the shop directly for questions about your vehicle.</p>
      </div>
      <div class="contact-grid">

        <!-- App Support -->
        <div class="contact-card">
          <div class="contact-card-label">App Support</div>
          <div class="contact-info">
            <h3>Jas Jagpal</h3>
            <div class="subtitle">Developer &amp; App Administrator</div>

            <div class="contact-detail">
              <div class="contact-detail-icon">
                <svg viewBox="0 0 24 24">
                  <path d="M4 4h16c1.1 0 2 .9 2 2v12c0 1.1-.9 2-2 2H4c-1.1 0-2-.9-2-2V6c0-1.1.9-2 2-2z"/>
                  <polyline points="22,6 12,13 2,6"/>
                </svg>
              </div>
              <div class="contact-detail-text">
                <strong><a href="mailto:admin@jjagpal.me">admin@jjagpal.me</a></strong>
                <span>Technical questions, CCC integration issues, or app support</span>
              </div>
            </div>
          </div>
        </div>

        <!-- Business Contact -->
        <div class="contact-card">
          <div class="contact-card-label">Business Contact</div>
          <div class="contact-info">
            <h3>Earl Scheib Auto Body</h3>
            <div class="subtitle">Concord, California</div>

            <div class="contact-detail">
              <div class="contact-detail-icon">
                <svg viewBox="0 0 24 24">
                  <path d="M22 16.92v3a2 2 0 0 1-2.18 2 19.79 19.79 0 0 1-8.63-3.07A19.5 19.5 0 0 1 4.36 12 19.79 19.79 0 0 1 1.08 3.38a2 2 0 0 1 1.99-2.18h3a2 2 0 0 1 2 1.72c.127.96.361 1.903.7 2.81a2 2 0 0 1-.45 2.11L7.09 8.91a16 16 0 0 0 6 6l1.27-1.27a2 2 0 0 1 2.11-.45c.907.339 1.85.573 2.81.7A2 2 0 0 1 21 16z"/>
                </svg>
              </div>
              <div class="contact-detail-text">
                <strong>(925) 609-7780</strong>
                <span>Main Shop Line</span>
              </div>
            </div>

            <div class="contact-detail">
              <div class="contact-detail-icon">
                <svg viewBox="0 0 24 24">
                  <path d="M21 10c0 7-9 13-9 13s-9-6-9-13a9 9 0 0 1 18 0z"/>
                  <circle cx="12" cy="10" r="3"/>
                </svg>
              </div>
              <div class="contact-detail-text">
                <strong>Concord, CA</strong>
                <span>Serving the Concord &amp; Contra Costa area</span>
              </div>
            </div>

            <div class="contact-detail">
              <div class="contact-detail-icon">
                <svg viewBox="0 0 24 24">
                  <rect x="3" y="4" width="18" height="18" rx="2" ry="2"/>
                  <line x1="16" y1="2" x2="16" y2="6"/>
                  <line x1="8" y1="2" x2="8" y2="6"/>
                  <line x1="3" y1="10" x2="21" y2="10"/>
                </svg>
              </div>
              <div class="contact-detail-text">
                <strong>Mon &#x2013; Fri: 8am &#x2013; 5pm</strong>
                <span>Sat: 8am &#x2013; 12pm &middot; Sun: Closed</span>
              </div>
            </div>
          </div>
        </div>

      </div>
    </div>
  </section>

  <!-- FOOTER -->
  <footer>
    <div class="footer-inner">
      <div class="footer-brand">Earl Scheib Auto Body &mdash; Concord</div>
      <div class="footer-copy">&copy; {year} Earl Scheib Auto Body. All rights reserved.</div>
      <div class="footer-powered">Customer Follow-Up System powered by CCC ONE &amp; automated SMS</div>
    </div>
  </footer>

</body>
</html>
"""


# ---------------------------------------------------------------------------
# Marco's install + usage guide. Served at /earlscheibconcord/instructions.
# Concord Garage aesthetic — matches the admin UI: Fraunces display,
# JetBrains Mono body/data, oxblood/cream/graphite palette.
# ---------------------------------------------------------------------------
INSTRUCTIONS_HTML = """\
<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1.0" />
  <title>Earl Scheib EMS Watcher — Install & Usage Guide</title>
  <link rel="preconnect" href="https://fonts.googleapis.com">
  <link rel="preconnect" href="https://fonts.gstatic.com" crossorigin>
  <link href="https://fonts.googleapis.com/css2?family=Fraunces:ital,opsz,wght@0,9..144,400;0,9..144,600;0,9..144,700;1,9..144,500&family=JetBrains+Mono:wght@400;500;600&display=swap" rel="stylesheet">
  <style>
    :root {{
      --ink: #1B1B1B;
      --paper: #F4EDE0;
      --oxblood: #7A2E2A;
      --oxblood-dark: #5A1F1C;
      --amber: #E8A33D;
      --steel: #8B8478;
      --rule: rgba(27, 27, 27, 0.12);
    }}
    *, *::before, *::after {{ box-sizing: border-box; margin: 0; padding: 0; }}
    html {{ scroll-behavior: smooth; }}
    body {{
      font-family: "JetBrains Mono", ui-monospace, monospace;
      font-size: 15px;
      line-height: 1.7;
      color: var(--ink);
      background: var(--paper);
      background-image: url("data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='220' height='220'><filter id='n'><feTurbulence type='fractalNoise' baseFrequency='0.9' numOctaves='2' stitchTiles='stitch'/><feColorMatrix values='0 0 0 0 0.11 0 0 0 0 0.11 0 0 0 0 0.11 0 0 0 0.03 0'/></filter><rect width='100%' height='100%' filter='url(%23n)'/></svg>");
      -webkit-font-smoothing: antialiased;
    }}
    header {{
      max-width: 860px;
      margin: 0 auto;
      padding: 64px 28px 32px;
      border-bottom: 4px solid var(--oxblood);
    }}
    h1 {{
      font-family: "Fraunces", serif;
      font-weight: 700;
      font-size: clamp(2rem, 5vw, 3rem);
      line-height: 1.05;
      letter-spacing: -0.015em;
      color: var(--ink);
    }}
    h1 em {{
      font-style: italic;
      color: var(--oxblood);
      font-weight: 500;
    }}
    .subtitle {{
      margin-top: 14px;
      font-size: 14px;
      color: var(--steel);
      letter-spacing: 0.04em;
      text-transform: uppercase;
    }}
    main {{
      max-width: 860px;
      margin: 0 auto;
      padding: 48px 28px 120px;
    }}
    section {{ margin-bottom: 56px; }}
    h2 {{
      font-family: "Fraunces", serif;
      font-weight: 700;
      font-size: 1.7rem;
      color: var(--oxblood);
      margin-bottom: 8px;
      letter-spacing: -0.01em;
    }}
    h2 .num {{
      display: inline-block;
      font-family: "JetBrains Mono", monospace;
      font-weight: 600;
      font-size: 0.9rem;
      color: var(--steel);
      margin-right: 10px;
      vertical-align: 4px;
    }}
    h3 {{
      font-family: "Fraunces", serif;
      font-weight: 600;
      font-size: 1.15rem;
      color: var(--ink);
      margin-top: 28px;
      margin-bottom: 6px;
    }}
    p {{ margin-bottom: 14px; max-width: 68ch; }}
    ol, ul {{ padding-left: 22px; margin-bottom: 14px; }}
    ol li, ul li {{ margin-bottom: 8px; max-width: 68ch; }}
    strong {{ color: var(--oxblood); font-weight: 600; }}
    code {{
      font-family: "JetBrains Mono", monospace;
      font-size: 0.93em;
      background: rgba(122, 46, 42, 0.08);
      color: var(--oxblood-dark);
      padding: 2px 6px;
      border-radius: 3px;
      word-break: break-all;
    }}
    pre {{
      font-family: "JetBrains Mono", monospace;
      background: var(--ink);
      color: var(--paper);
      padding: 16px 20px;
      border-radius: 4px;
      margin: 16px 0;
      overflow-x: auto;
      font-size: 0.9em;
      line-height: 1.55;
    }}
    .download-cta {{
      display: inline-flex;
      align-items: center;
      gap: 14px;
      margin: 16px 0 8px;
      padding: 18px 28px;
      background: var(--oxblood);
      color: var(--paper);
      font-family: "Fraunces", serif;
      font-weight: 600;
      font-size: 1.25rem;
      text-decoration: none;
      border-radius: 3px;
      box-shadow: 0 3px 0 var(--oxblood-dark), 0 6px 20px rgba(0,0,0,0.15);
      transition: transform 0.15s ease, box-shadow 0.15s ease;
    }}
    .download-cta:hover {{
      transform: translateY(-1px);
      box-shadow: 0 4px 0 var(--oxblood-dark), 0 8px 24px rgba(0,0,0,0.18);
    }}
    .download-cta:active {{ transform: translateY(1px); box-shadow: 0 1px 0 var(--oxblood-dark), 0 2px 8px rgba(0,0,0,0.15); }}
    .download-cta .arrow {{ font-size: 1.4em; }}
    .download-sub {{
      display: block;
      font-size: 13px;
      color: var(--steel);
      margin-top: 6px;
    }}
    .callout {{
      margin: 20px 0;
      padding: 16px 20px;
      border-left: 3px solid var(--amber);
      background: rgba(232, 163, 61, 0.08);
      font-size: 14.5px;
    }}
    .callout strong {{ color: var(--ink); }}
    table {{
      width: 100%;
      border-collapse: collapse;
      margin: 16px 0;
      font-size: 14px;
    }}
    th, td {{
      text-align: left;
      padding: 10px 12px;
      border-bottom: 1px solid var(--rule);
      vertical-align: top;
    }}
    th {{
      font-family: "Fraunces", serif;
      font-weight: 600;
      color: var(--oxblood);
      letter-spacing: 0.01em;
      border-bottom: 2px solid var(--oxblood);
    }}
    figure {{
      margin: 24px 0 28px;
      padding: 14px;
      background: rgba(27, 27, 27, 0.03);
      border: 1px solid var(--rule);
      border-radius: 4px;
    }}
    figure img {{
      width: 100%;
      height: auto;
      display: block;
      border-radius: 2px;
      box-shadow: 0 2px 10px rgba(0,0,0,0.08);
    }}
    figcaption {{
      font-size: 13px;
      color: var(--steel);
      margin-top: 10px;
      font-style: italic;
      font-family: "Fraunces", serif;
    }}
    kbd {{
      display: inline-block;
      padding: 2px 7px;
      font-family: "JetBrains Mono", monospace;
      font-size: 0.85em;
      background: var(--paper);
      border: 1px solid var(--steel);
      border-bottom-width: 2px;
      border-radius: 3px;
      color: var(--ink);
    }}
    hr {{
      border: none;
      border-top: 1px solid var(--rule);
      margin: 56px 0;
    }}
    footer {{
      max-width: 860px;
      margin: 0 auto;
      padding: 24px 28px 64px;
      border-top: 1px solid var(--rule);
      color: var(--steel);
      font-size: 13px;
      letter-spacing: 0.02em;
    }}
    footer a {{ color: var(--oxblood); text-decoration: none; border-bottom: 1px dotted var(--oxblood); }}
    .toc {{
      padding: 20px 24px;
      background: rgba(122, 46, 42, 0.05);
      border-left: 3px solid var(--oxblood);
      margin-bottom: 48px;
    }}
    .toc h3 {{ margin-top: 0; font-size: 0.95rem; color: var(--oxblood); letter-spacing: 0.05em; text-transform: uppercase; margin-bottom: 10px; }}
    .toc ol {{ padding-left: 20px; margin: 0; }}
    .toc li {{ margin-bottom: 4px; }}
    .toc a {{ color: var(--ink); text-decoration: none; border-bottom: 1px dotted var(--steel); }}
    .toc a:hover {{ color: var(--oxblood); border-bottom-color: var(--oxblood); }}
  </style>
</head>
<body>
  <header>
    <h1>Earl Scheib EMS Watcher<br /><em>Install & Usage Guide</em></h1>
    <div class="subtitle">For Marco — Earl Scheib Auto Body Concord · v1.0</div>
  </header>

  <main>
    <p>This guide walks you through installing the EMS Watcher on your shop PC and using the built-in queue viewer. The whole setup takes about two minutes, and after that the system runs by itself every five minutes in the background. You do not need to do anything on a day-to-day basis — this page is here for the first-time setup and the occasional check.</p>

    <div class="toc">
      <h3>Contents</h3>
      <ol>
        <li><a href="#download">Download the installer</a></li>
        <li><a href="#install">Run the installer (3 steps)</a></li>
        <li><a href="#after">What happens after install</a></li>
        <li><a href="#queue">The Queue Viewer — see and cancel pending texts</a></li>
        <li><a href="#troubleshoot">Troubleshooting</a></li>
        <li><a href="#where">Where things live on your PC</a></li>
        <li><a href="#support">Support</a></li>
      </ol>
    </div>

    <section id="download">
      <h2><span class="num">1.</span>Download the installer</h2>
      <p>Click the button below on the shop PC (the same one that runs CCC ONE). A file named <code>EarlScheibWatcher-Setup.zip</code> (about 9 MB) will save to your <strong>Downloads</strong> folder.</p>
      <p>
        <a class="download-cta" href="/earlscheibconcord/download">
          <span class="arrow">↓</span> Download EarlScheibWatcher-Setup.zip
        </a>
      </p>
      <p class="download-sub">ZIP bundle · Windows 10 / 11 · ~9 MB · <a href="/earlscheibconcord/download.exe" style="color: var(--oxblood); border-bottom: 1px dotted var(--oxblood);">direct .exe</a></p>

      <div class="callout">
        <strong>Why a ZIP?</strong> Chrome blocks direct <code>.exe</code> downloads from unrecognized sites for safety. The ZIP bundle downloads without the warning. <strong>After it downloads:</strong> right-click the ZIP in your <strong>Downloads</strong> folder → <strong>"Extract All..."</strong> → accept the default location → click <strong>Extract</strong>. That gives you the <code>EarlScheibWatcher-Setup.exe</code> file.
      </div>
    </section>

    <section id="install">
      <h2><span class="num">2.</span>Run the installer</h2>
      <p>Double-click the downloaded file. Approve the Windows administrator prompt if one appears.</p>

      <div class="callout">
        <strong>"Windows protected your PC" blue screen?</strong> This is normal for new business software. Click <strong>More info</strong> (below the main text), then click <strong>Run anyway</strong>. Windows SmartScreen shows this warning for programs it hasn't seen downloaded many times yet.
      </div>

      <p>The wizard has three pages:</p>

      <table>
        <tr><th>Page</th><th>What it asks</th><th>What to do</th></tr>
        <tr>
          <td><strong>1. Folder</strong></td>
          <td>Where CCC ONE saves EMS files.</td>
          <td>The installer suggests the most likely folder. Click <strong>Browse</strong> only if you use a non-standard path. Click <strong>Next</strong>.</td>
        </tr>
        <tr>
          <td><strong>2. Connection</strong></td>
          <td>Tests the link to the follow-up service.</td>
          <td>Should show a <strong>✓ check</strong>. If it fails, check your Wi-Fi. You can also click <strong>Continue anyway</strong> — the watcher will retry automatically.</td>
        </tr>
        <tr>
          <td><strong>3. CCC ONE</strong></td>
          <td>Reminds you to turn on EMS export in CCC ONE.</td>
          <td>Open CCC ONE separately, go to <strong>Tools → Extract → EMS Extract Preferences</strong>, check both <strong>Lock Estimate</strong> and <strong>Save Workfile</strong>, save. Back in the installer, check the <strong>"I've done this"</strong> box and click <strong>Finish</strong>.</td>
        </tr>
      </table>

      <p>On the last page there is a checkbox <strong>"Launch Queue Viewer now"</strong> — leave it checked and click <strong>Finish</strong>. Your browser will open to the queue page so you can confirm the system is running.</p>

      <p>The installer also adds an <strong>Earl Scheib Queue</strong> shortcut to your <strong>Desktop</strong> and your <strong>Start Menu</strong>. Use it any time you want to open the queue later.</p>
    </section>

    <section id="after">
      <h2><span class="num">3.</span>What happens after install</h2>
      <ul>
        <li>Every 5 minutes, Windows runs the watcher silently. There is no icon, no tray, no popup. It's meant to stay out of your way.</li>
        <li>When CCC ONE exports a new estimate, the watcher picks it up within ~5 minutes and sends it to the follow-up service.</li>
        <li>The service schedules three texts automatically: a 24-hour follow-up, a 3-day follow-up, and a post-repair review request.</li>
        <li>Nothing else to do on a normal day. The guide below is for when you want to peek at the queue or something seems off.</li>
      </ul>
    </section>

    <section id="queue">
      <h2><span class="num">4.</span>The Queue Viewer — see and cancel pending texts</h2>
      <p>Double-click the <strong>Earl Scheib Queue</strong> shortcut on your Desktop (or from the Start Menu). A small black window opens briefly, and your browser auto-opens to the queue page.</p>

      <figure>
        <img src="/earlscheibconcord/static/admin-ui-default.png" alt="Queue default view showing three customer cards" loading="lazy">
        <figcaption>The queue viewer. Each card is one customer; nested rows are each pending message, grouped so you can see all of a customer's queued texts at a glance.</figcaption>
      </figure>

      <p>Each row shows:</p>
      <ul>
        <li><strong>Send time</strong> (Pacific): when the text is scheduled to go out, shortened like "Tue 2:30 PM"</li>
        <li><strong>Job type</strong>: <code>24-HOUR</code> (estimate follow-up), <code>3-DAY</code> (second follow-up), or <code>REVIEW</code> (post-repair review request)</li>
        <li><strong>Estimate/job reference</strong>: e.g. <code>EST-A4829</code></li>
        <li><strong>cancel</strong> link on the right</li>
      </ul>

      <h3>Cancelling a message</h3>
      <p>Click the <strong>cancel</strong> link on any row. The row strikes through and an amber <strong>"click to undo"</strong> pill appears with a 5-second countdown ring:</p>

      <figure>
        <img src="/earlscheibconcord/static/admin-ui-cancel-undo.png" alt="Cancel with 5-second undo countdown" loading="lazy">
        <figcaption>You have 5 seconds to click the amber pill to abort. The countdown ring on the right drains as the window closes.</figcaption>
      </figure>

      <p>If you change your mind within those 5 seconds, click the pill and the cancel is aborted — nothing is deleted. If you do nothing, the row is permanently removed:</p>

      <figure>
        <img src="/earlscheibconcord/static/admin-ui-after-settled.png" alt="Queue after a row was cancelled" loading="lazy">
        <figcaption>After the 5-second window expires, the row is gone from the queue. The next auto-refresh picks up the change.</figcaption>
      </figure>

      <h3>When nothing is queued</h3>
      <p>Overnight, on weekends, or before the first estimate of the day, the queue is empty:</p>

      <figure>
        <img src="/earlscheibconcord/static/admin-ui-empty.png" alt="Empty queue with italic 'Nothing queued right now'" loading="lazy">
        <figcaption>Empty state. Nothing to do — this is normal, not an error.</figcaption>
      </figure>

      <h3>Refreshing the page</h3>
      <ul>
        <li>The queue refreshes itself <strong>every 15 seconds</strong>. Leave the page open and it stays current.</li>
        <li>Press <kbd>R</kbd> (with no text field selected) to refresh immediately, or click the small refresh arrow in the top-right.</li>
        <li>Close the browser tab when you're done. The small black window behind the scenes closes itself about 30 seconds later.</li>
      </ul>
    </section>

    <section id="troubleshoot">
      <h2><span class="num">5.</span>Troubleshooting</h2>
      <table>
        <tr><th>If you see…</th><th>What to do</th></tr>
        <tr>
          <td>Windows SmartScreen blocked the installer</td>
          <td>Click <strong>More info</strong> → <strong>Run anyway</strong>. See callout in step 2.</td>
        </tr>
        <tr>
          <td>Customers aren't getting text messages</td>
          <td>Open <code>C:\\EarlScheibWatcher\\ems_watcher.log</code> in Notepad. Look for recent errors. If you see "connection failed" repeating, check the shop's Wi-Fi.</td>
        </tr>
        <tr>
          <td>CCC ONE isn't exporting EMS files</td>
          <td>In CCC ONE: <strong>Tools → Extract → EMS Extract Preferences</strong>. Make sure both <strong>Lock Estimate</strong> and <strong>Save Workfile</strong> are checked, and the <strong>Output Folder</strong> matches what you entered in the installer.</td>
        </tr>
        <tr>
          <td>You moved the CCC ONE folder and need to re-point the watcher</td>
          <td>Open Command Prompt as Administrator and run: <code>C:\\EarlScheibWatcher\\earlscheib.exe --configure</code></td>
        </tr>
        <tr>
          <td>Want to uninstall</td>
          <td><strong>Settings → Apps</strong>, find <strong>Earl Scheib EMS Watcher</strong>, click <strong>Uninstall</strong>.</td>
        </tr>
        <tr>
          <td>Queue viewer says <code>cannot reach local admin</code></td>
          <td>The background window closed. Double-click the <strong>Earl Scheib Queue</strong> shortcut again.</td>
        </tr>
        <tr>
          <td>Queue viewer says <code>queue fetch failed (401)</code></td>
          <td>Contact App Support — the signing key got out of sync.</td>
        </tr>
        <tr>
          <td>Anything else</td>
          <td>Contact <a href="mailto:admin@jjagpal.me"><code>admin@jjagpal.me</code></a></td>
        </tr>
      </table>
    </section>

    <section id="where">
      <h2><span class="num">6.</span>Where things live on your PC</h2>
      <pre>C:\\EarlScheibWatcher\\
  earlscheib.exe        the watcher program
  config.ini            your saved folder path + settings
  ems_watcher.log       activity log (safe to read)
  ems_watcher.db        dedup database (don't touch)</pre>
      <p>Windows Task Scheduler (search <em>"Task Scheduler"</em> in the Start Menu) lists the watcher as <code>EarlScheibEMSWatcher</code> under <strong>Task Scheduler Library</strong>. The <strong>Last Run Time</strong> column shows when it last ran; <strong>Last Run Result</strong> should say <em>"The operation completed successfully (0x0)"</em>.</p>
    </section>

    <section id="support">
      <h2><span class="num">7.</span>Support</h2>
      <p>Reach out to the app developer for any technical issue — installer won't run, messages aren't sending, queue viewer won't open, etc.</p>
      <p>Email <a href="mailto:admin@jjagpal.me" style="color: var(--oxblood); font-weight: 600;">admin@jjagpal.me</a> with a short description of what's happening. If possible, attach or paste the last 50 lines of <code>C:\\EarlScheibWatcher\\ems_watcher.log</code> — it dramatically speeds up diagnosis.</p>
    </section>
  </main>

  <footer>
    Earl Scheib Auto Body Concord · EMS Watcher v1.0 · © {year}<br />
    Source: <a href="https://github.com/jaskarn78/earl-scheib-ems-watcher">github.com/jaskarn78/earl-scheib-ems-watcher</a>
  </footer>
</body>
</html>
"""


class WebhookHandler(BaseHTTPRequestHandler):

    def log_message(self, fmt, *args):  # suppress default access log spam
        log.info("HTTP %s", fmt % args)

    def _send_json(self, status: int, data: dict):
        body = json.dumps(data).encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _send_html(self, status: int, html: str):
        body = html.encode("utf-8")
        self.send_response(status)
        self.send_header("Content-Type", "text/html; charset=utf-8")
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    # ------------------------------------------------------------------
    def do_GET(self):
        parsed = urlparse(self.path)
        path = parsed.path.rstrip("/")

        if path == "/earlscheibconcord/download" or path == "/earlscheibconcord/download.exe":
            import os
            app_dir = os.path.dirname(os.path.abspath(__file__))
            # Preferred: zip wrapper (Chrome Safe Browsing flags unsigned .exe as
            # "dangerous", forcing users to click through chrome://downloads. A
            # .zip containing the same exe downloads without interruption).
            # /download.exe explicit path → skip the zip (power users).
            installer_zip = os.path.join(app_dir, "EarlScheibWatcher-Setup.zip")
            installer_exe = os.path.join(app_dir, "EarlScheibWatcher-Setup.exe")
            legacy_zip = os.path.join(app_dir, "watcher.zip")
            if path.endswith(".exe") and os.path.exists(installer_exe):
                serve_path = installer_exe
                content_type = "application/octet-stream"
                filename = "EarlScheibWatcher-Setup.exe"
            elif os.path.exists(installer_zip):
                serve_path = installer_zip
                content_type = "application/zip"
                filename = "EarlScheibWatcher-Setup.zip"
            elif os.path.exists(installer_exe):
                serve_path = installer_exe
                content_type = "application/octet-stream"
                filename = "EarlScheibWatcher-Setup.exe"
            elif os.path.exists(legacy_zip):
                serve_path = legacy_zip
                content_type = "application/zip"
                filename = "earl-scheib-ems-watcher.zip"
            else:
                self.send_response(404); self.end_headers(); return
            file_size = os.path.getsize(serve_path)
            self.send_response(200)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Disposition", f'attachment; filename="{filename}"')
            self.send_header("Content-Length", str(file_size))
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            with open(serve_path, "rb") as f:
                while True:
                    chunk = f.read(65536)
                    if not chunk:
                        break
                    self.wfile.write(chunk)
            return

        if path == "/earlscheibconcord":
            year = datetime.now().year
            html = SUPPORT_HTML.format(year=year)
            self._send_html(200, html)
            return

        # Marco's install + usage guide — Concord Garage aesthetic, screenshots inline.
        if path == "/earlscheibconcord/instructions":
            year = datetime.now().year
            html = INSTRUCTIONS_HTML.format(year=year)
            self._send_html(200, html)
            return

        # Operator log-upload trigger (client polls, HMAC-authenticated empty body).
        # Returns {"upload_log": true} if commands.json has the flag set, else 204.
        # Trigger from this box: `echo '{"upload_log": true}' > commands.json`
        if path == "/earlscheibconcord/commands":
            # HMAC-only: machine-to-machine (watcher client polling).
            # Browser /earlscheib never hits this — no dual-auth path needed.
            import os
            sig = self.headers.get("X-EMS-Signature", "")
            if not _validate_hmac(b"", sig):
                self._send_json(401, {"error": "invalid signature"})
                return
            app_dir = os.path.dirname(os.path.abspath(__file__))
            cmd_path = os.path.join(app_dir, "commands.json")
            try:
                with open(cmd_path, "r", encoding="utf-8") as f:
                    commands = json.load(f)
            except (FileNotFoundError, json.JSONDecodeError):
                commands = {}
            if not commands or all(not v for v in commands.values()):
                self.send_response(204); self.end_headers(); return
            self._send_json(200, commands)
            return

        # Static screenshot assets referenced by /instructions.
        # Safelist: only specific PNG filenames; no path traversal.
        if path.startswith("/earlscheibconcord/static/"):
            import os
            fname = path[len("/earlscheibconcord/static/"):]
            safe_assets = {
                "admin-ui-default.png", "admin-ui-cancel-undo.png",
                "admin-ui-after-settled.png", "admin-ui-empty.png",
            }
            if fname in safe_assets:
                app_dir = os.path.dirname(os.path.abspath(__file__))
                asset_path = os.path.join(app_dir, "docs", "screenshots", fname)
                if os.path.exists(asset_path):
                    size = os.path.getsize(asset_path)
                    self.send_response(200)
                    self.send_header("Content-Type", "image/png")
                    self.send_header("Content-Length", str(size))
                    self.send_header("Cache-Control", "public, max-age=3600")
                    self.end_headers()
                    with open(asset_path, "rb") as f:
                        while True:
                            chunk = f.read(65536)
                            if not chunk:
                                break
                            self.wfile.write(chunk)
                    return
            self.send_response(404); self.end_headers(); return

        if path == "/earlscheibconcord/status":
            import json as _json
            ts = LAST_HEARTBEAT["ts"]
            host = LAST_HEARTBEAT["host"]
            if ts:
                ago = int(time.time()) - ts
                status = "online" if ago < 600 else "stale"
                last_seen = f"{ago // 60}m ago" if ago >= 60 else f"{ago}s ago"
            else:
                status = "unknown"
                last_seen = "never"
            self._send_json(200, {"status": status, "last_seen": last_seen, "host": host})
            return

        if path == "/earlscheibconcord/remote-config":
            # HMAC-only: machine-to-machine (watcher client config pull).
            # Browser /earlscheib never hits this.
            sig = self.headers.get("X-EMS-Signature", "")
            # Client signs GET with HMAC of empty body: Sign(secret, b"")
            if not _validate_hmac(b"", sig):
                self._send_json(401, {"error": "invalid signature"})
                return
            try:
                with open(REMOTE_CONFIG_PATH, "r", encoding="utf-8") as f:
                    remote_cfg = json.load(f)
            except FileNotFoundError:
                remote_cfg = {}
            except (OSError, json.JSONDecodeError) as exc:
                log.error("remote_config.json read error: %s", exc)
                self._send_json(500, {"error": "config read error"})
                return
            if not remote_cfg:
                # No overrides: respond 204 No Content so client skips merge.
                self.send_response(204)
                self.end_headers()
                return
            self._send_json(200, remote_cfg)
            return

        if path == "/earlscheibconcord/queue":
            # RJL-02: accept HMAC (Go admin proxy) OR Basic auth (public
            # /earlscheib browser). GET signs empty body b"" — matches
            # remote-config precedent.
            if not _validate_auth(self, b""):
                self._send_json(401, {"error": "invalid signature"})
                return

            # ULH-01: optional ?status= filter so the admin UI lifecycle
            # chips (pending/sent/all) can populate. Default is "pending"
            # to preserve the previous contract (bare GET returns pending
            # only — shared main.js and Go admin proxy depend on this).
            qs = parse_qs(parsed.query)
            status = qs.get("status", ["pending"])[0]
            if status not in ("all", "pending", "sent"):
                self._send_json(
                    400,
                    {"error": "invalid status; must be one of: all, pending, sent"},
                )
                return

            # ULH-01: branch on whitelisted status — parameter is validated
            # above; SQL strings are constants (not interpolated from user
            # input) so there is no injection surface even without binding.
            # VAB-03 fix: include `sent` in the projection. The frontend
            # filter chips (jobMatchesFilter at main.js:184) and per-row
            # state classification (li.dataset.state at main.js:334) both
            # depend on `job.sent`. Without it, the 'sent' chip is always
            # empty and rows render as 'pending' regardless of actual state.
            # This is additive — pre-existing clients only consume new fields.
            base_cols = (
                "SELECT id, doc_id, job_type, phone, name, send_at, sent, "
                "       created_at, vin, vehicle_desc, ro_id, email, address, "
                "       sent_at, estimate_key "
                "FROM jobs"
            )
            if status == "pending":
                sql = base_cols + " WHERE sent = 0 ORDER BY send_at ASC"
            elif status == "sent":
                sql = (
                    base_cols
                    + " WHERE sent = 1 "
                    "ORDER BY COALESCE(sent_at, send_at, created_at) DESC"
                )
            else:  # "all"
                sql = (
                    base_cols
                    + " ORDER BY COALESCE(sent_at, send_at, created_at) DESC"
                )

            con = get_db()
            try:
                cur = con.cursor()
                # QAJ-01: include estimate_key so the admin UI can group
                # pending jobs per estimate into a timeline view.
                cur.execute(sql)
                rows = [dict(r) for r in cur.fetchall()]
            finally:
                con.close()
            self._send_json(200, rows)
            return

        # WMH-02: GET /templates — returns the effective body for each
        # job_type (override-if-present, else default), the is_override flag,
        # placeholder catalog, and a sample row for client-side preview.
        # Dual-auth (HMAC or Basic) — operator-only.
        if path == "/earlscheibconcord/templates":
            if not _validate_auth(self, b""):
                self._send_json(401, {"error": "invalid signature"})
                return

            # Pull all override rows in one query so we don't hit the DB per
            # job_type.
            overrides = {}
            con = get_db()
            try:
                cur = con.cursor()
                cur.execute("SELECT job_type, body, updated_at FROM templates")
                for r in cur.fetchall():
                    body = r["body"] if (r["body"] and r["body"].strip()) else ""
                    if body:
                        overrides[r["job_type"]] = {
                            "body": body,
                            "updated_at": r["updated_at"],
                        }

                # Sample row: newest pending job (sent=0), if any.
                cur.execute(
                    "SELECT name, phone, vin, vehicle_desc, ro_id, email, doc_id "
                    "FROM jobs WHERE sent = 0 ORDER BY id DESC LIMIT 1"
                )
                sample_src = cur.fetchone()
            finally:
                con.close()

            job_types_out = []
            for meta in JOB_TYPE_META:
                jt = meta["job_type"]
                ov = overrides.get(jt)
                effective_body = ov["body"] if ov else DEFAULT_TEMPLATES[jt]
                job_types_out.append({
                    "job_type":    jt,
                    "label":       meta["label"],
                    "when":        meta["when"],
                    "body":        effective_body,
                    "is_override": ov is not None,
                    "updated_at":  ov["updated_at"] if ov else 0,
                })

            if sample_src is not None:
                name = sample_src["name"] or ""
                first = name.split()[0] if name else "there"
                sample_row = {
                    "first_name":   first,
                    "name":         name,
                    "phone":        sample_src["phone"] or "",
                    "vin":          sample_src["vin"] or "",
                    "vehicle_desc": sample_src["vehicle_desc"] or "",
                    "ro_id":        sample_src["ro_id"] or "",
                    "doc_id":       sample_src["doc_id"] or "",
                    "email":        sample_src["email"] or "",
                }
            else:
                # Static realistic fallback for live-preview on an empty queue.
                sample_row = {
                    "first_name":   "Alex",
                    "name":         "Alex Martinez",
                    "phone":        "+15551234567",
                    "vin":          "1HGCM82633A004352",
                    "vehicle_desc": "2018 Honda Accord",
                    "ro_id":        "RO-1234",
                    "doc_id":       "DOC-ABC-01",
                    "email":        "alex@example.com",
                }
            sample_row.update(SHOP_CONSTANTS)

            self._send_json(200, {
                "job_types":    job_types_out,
                "placeholders": {
                    "per_row": list(PLACEHOLDERS_PER_ROW),
                    "shop":    list(PLACEHOLDERS_SHOP),
                },
                "sample_row":   sample_row,
            })
            return

        # Live debug snapshot — consumed by Claude (not Marco). Returns heartbeat
        # freshness, current commands.json state (READ-ONLY), log tail of the
        # most recently uploaded client log, and the count of logs received.
        # HMAC-authed so only holders of CCC_SECRET can inspect.
        if path == "/earlscheibconcord/diagnostic":
            import os as _os
            # RJL-02: dual auth — HMAC or Basic. Operator browser needs this
            # endpoint for the live diagnostic panel on /earlscheib.
            if not _validate_auth(self, b""):
                self._send_json(401, {"error": "invalid signature"})
                return

            ts = LAST_HEARTBEAT["ts"]
            host = LAST_HEARTBEAT["host"]
            seconds_ago = int(time.time()) - ts if ts else None
            last_heartbeat = {"ts": ts, "host": host, "seconds_ago": seconds_ago}
            client_online = bool(ts and (int(time.time()) - ts) < 600)

            app_dir = _os.path.dirname(_os.path.abspath(__file__))

            # Read commands.json — READ-ONLY, never written by this handler.
            cmd_path = _os.path.join(app_dir, "commands.json")
            try:
                with open(cmd_path, "r", encoding="utf-8") as f:
                    commands_state = json.load(f)
            except (FileNotFoundError, json.JSONDecodeError):
                commands_state = {}

            # Tail the newest non-symlink *.log in received_logs/ (last 20 lines).
            logs_dir = _os.path.join(app_dir, "received_logs")
            tail = ""
            received_logs_count = 0
            try:
                entries = [
                    _os.path.join(logs_dir, f) for f in _os.listdir(logs_dir)
                    if f.endswith(".log") and f != "latest.log"
                ]
                received_logs_count = len(entries)
                if entries:
                    latest = max(entries, key=_os.path.getmtime)
                    with open(latest, "r", encoding="utf-8", errors="replace") as fh:
                        lines = fh.readlines()
                    tail = "".join(lines[-20:])
            except OSError:
                tail = ""

            self._send_json(200, {
                "last_heartbeat": last_heartbeat,
                "client_online": client_online,
                "commands_state": commands_state,
                "recent_log_tail": tail,
                "received_logs_count": received_logs_count,
            })
            return

        # Self-update: client polls this with HMAC-signed empty body each scan.
        # Returns the first-16 hex of SHA256(EarlScheibWatcher-Setup.exe) plus a
        # kill-switch flag. Client compares to its own os.Executable() hash and
        # downloads /earlscheibconcord/download.exe if they differ. Setting
        # AUTO_UPDATE_PAUSED=1 in the environment, or creating an empty file
        # named `update_paused` in this app dir, flips paused=True and halts
        # rollout across every deployed client within one scan cycle.
        if path == "/earlscheibconcord/version":
            # HMAC-only: self-update mechanism, client-side only.
            # Browser /earlscheib never hits this.
            import os as _os
            sig = self.headers.get("X-EMS-Signature", "")
            if not _validate_hmac(b"", sig):
                self._send_json(401, {"error": "invalid signature"})
                return

            app_dir = _os.path.dirname(_os.path.abspath(__file__))
            installer_path = _os.path.join(app_dir, "EarlScheibWatcher-Setup.exe")
            if not _os.path.exists(installer_path):
                self._send_json(404, {"error": "no installer available"})
                return

            paused = (
                _os.environ.get("AUTO_UPDATE_PAUSED") == "1"
                or _os.path.exists(_os.path.join(app_dir, "update_paused"))
            )

            # 64 KB chunk-hash — <10 ms for 6 MB installer, keeps memory flat.
            h = hashlib.sha256()
            with open(installer_path, "rb") as fh:
                while True:
                    chunk = fh.read(65536)
                    if not chunk:
                        break
                    h.update(chunk)
            version = h.hexdigest()[:16]

            # download_url is joined to webhookURL on the client
            # (webhookURL already ends in /earlscheibconcord); keep this
            # path relative to that so we don't double the prefix.
            self._send_json(200, {
                "version": version,
                "download_url": "/download.exe",
                "paused": paused,
            })
            return

        # ------------------------------------------------------------------
        # RJL-01: public admin UI at /earlscheib (basic-auth'd).
        # Only active when ADMIN_UI_USER and ADMIN_UI_PASSWORD are both set
        # in the environment. Otherwise returns 404 so the endpoint is
        # effectively invisible.
        #
        # Layout:
        #   GET /earlscheib         → index.html with API_BASE_PATH injected
        #   GET /earlscheib/main.css
        #   GET /earlscheib/main.js
        #
        # Source of truth for these assets is internal/admin/ui/*. Copies
        # live in ui_public/. See `make sync-ui`.
        # ------------------------------------------------------------------
        if path == "/earlscheib" or path.startswith("/earlscheib/"):
            # NOTE: `import os` is re-bound locally elsewhere in do_GET, which
            # turns `os` into a function-local name for the whole method and
            # causes UnboundLocalError on first reference here. Alias to a
            # local name loaded via import to dodge the scoping trap.
            import os as _os_ui
            if not ADMIN_UI_ENABLED:
                self.send_response(404); self.end_headers(); return
            auth = self.headers.get("Authorization", "")
            if not _validate_basic_auth(auth):
                self.send_response(401)
                self.send_header("WWW-Authenticate", 'Basic realm="Earl Scheib Queue"')
                self.send_header("Content-Length", "0")
                self.end_headers()
                return

            log.info("basic-auth: user=%s ip=%s path=%s",
                     ADMIN_UI_USER, self.client_address[0], path)

            app_dir = _os_ui.path.dirname(_os_ui.path.abspath(__file__))
            ui_dir = _os_ui.path.join(app_dir, "ui_public")

            # Root → serve index.html with API_BASE_PATH injection so the
            # shared main.js knows to hit /earlscheibconcord/* instead of
            # its default /api/* base.
            if path == "/earlscheib":
                index_path = _os_ui.path.join(ui_dir, "index.html")
                try:
                    with open(index_path, "r", encoding="utf-8") as f:
                        html = f.read()
                except OSError as exc:
                    log.error("ui_public/index.html read failed: %s", exc)
                    self._send_json(500, {"error": "ui unavailable"})
                    return
                # Rewrite /main.css and /main.js to namespaced paths so they
                # load from /earlscheib/* instead of the Go admin's root.
                # Then inject API_BASE_PATH before main.js loads.
                html = html.replace(
                    'href="/main.css"', 'href="/earlscheib/main.css"'
                ).replace(
                    'src="/main.js"', 'src="/earlscheib/main.js"'
                )
                injection = (
                    '<script>window.API_BASE_PATH = "/earlscheibconcord";'
                    '</script>\n  '
                )
                html = html.replace(
                    '<script src="/earlscheib/main.js"',
                    injection + '<script src="/earlscheib/main.js"',
                )
                self._send_html(200, html)
                return

            # CSS + JS: simple safelist — no traversal.
            if path == "/earlscheib/main.css":
                asset_path = _os_ui.path.join(ui_dir, "main.css")
                content_type = "text/css; charset=utf-8"
            elif path == "/earlscheib/main.js":
                asset_path = _os_ui.path.join(ui_dir, "main.js")
                content_type = "application/javascript; charset=utf-8"
            else:
                self.send_response(404); self.end_headers(); return

            try:
                with open(asset_path, "rb") as f:
                    body = f.read()
            except OSError:
                self.send_response(404); self.end_headers(); return
            self.send_response(200)
            self.send_header("Content-Type", content_type)
            self.send_header("Content-Length", str(len(body)))
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            self.wfile.write(body)
            return

        # Default: 404
        self.send_response(404); self.end_headers()
        return

    # ------------------------------------------------------------------
    def do_POST(self):
        content_length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(content_length)

        # Operator log upload: client sends tail of ems_watcher.log after seeing
        # "upload_log": true from /commands. Saved to received_logs/{host}-{iso}.log
        # and a symlink `received_logs/latest.log` for quick inspection. Clears
        # commands.json upload_log flag on successful write so the next scan
        # doesn't re-upload.
        if self.path.split("?")[0] == "/earlscheibconcord/logs":
            # HMAC-only: operator-triggered log upload from watcher client.
            # Browser /earlscheib never hits this.
            import os
            sig = self.headers.get("X-EMS-Signature", "")
            if not _validate_hmac(raw, sig):
                self._send_json(401, {"error": "invalid signature"})
                return
            try:
                payload = json.loads(raw.decode("utf-8", errors="replace"))
                host = str(payload.get("host", "unknown"))[:64]
                content = str(payload.get("log", ""))
            except Exception as exc:
                self._send_json(400, {"error": f"invalid payload: {exc}"})
                return
            safe_host = "".join(c if c.isalnum() or c in "-_" else "_" for c in host)
            app_dir = os.path.dirname(os.path.abspath(__file__))
            logs_dir = os.path.join(app_dir, "received_logs")
            os.makedirs(logs_dir, exist_ok=True)
            ts_str = datetime.now(timezone.utc).strftime("%Y%m%dT%H%M%SZ")
            fname = f"{safe_host}-{ts_str}.log"
            fpath = os.path.join(logs_dir, fname)
            try:
                with open(fpath, "w", encoding="utf-8") as f:
                    f.write(f"# received from {host} at {ts_str}\n# {len(content)} chars\n\n")
                    f.write(content)
                # Refresh latest.log symlink
                latest = os.path.join(logs_dir, "latest.log")
                try: os.remove(latest)
                except FileNotFoundError: pass
                try: os.symlink(fname, latest)
                except OSError: pass  # non-POSIX fallback unneeded on Linux VM
                log.info("Log tail received: host=%s bytes=%d path=%s", host, len(content), fname)
                # Clear the upload_log flag so we don't loop
                cmd_path = os.path.join(app_dir, "commands.json")
                try:
                    with open(cmd_path, "r", encoding="utf-8") as f:
                        commands = json.load(f)
                except (FileNotFoundError, json.JSONDecodeError):
                    commands = {}
                commands["upload_log"] = False
                with open(cmd_path, "w", encoding="utf-8") as f:
                    json.dump(commands, f)
            except OSError as exc:
                log.error("Log upload write failed: %s", exc)
                self._send_json(500, {"error": "write failed"})
                return
            self._send_json(200, {"saved": fname})
            return

        if self.path.split("?")[0] == "/earlscheibconcord/telemetry":
            # HMAC-only: crash telemetry from watcher client.
            # Browser /earlscheib never hits this.
            sig = self.headers.get("X-EMS-Signature", "")
            if not _validate_hmac(raw, sig):
                self._send_json(401, {"error": "invalid signature"})
                return
            # Append structured record to telemetry.log (one JSON line per event).
            # Log metadata alongside payload for diagnostics — no BMS XML here.
            record = {
                "ts": datetime.now(timezone.utc).isoformat(),
                "client_ip": self.client_address[0],
                "user_agent": self.headers.get("User-Agent", ""),
                "payload_bytes": len(raw),
            }
            try:
                payload_json = json.loads(raw.decode("utf-8", errors="replace"))
                record["event"] = payload_json
            except Exception:
                record["raw_preview"] = raw[:200].decode("utf-8", errors="replace")
            try:
                with open(TELEMETRY_LOG_PATH, "a", encoding="utf-8") as f:
                    f.write(json.dumps(record) + "\n")
                log.info("Telemetry event logged: type=%s", record.get("event", {}).get("type", "unknown"))
            except OSError as exc:
                log.error("Telemetry log write failed: %s", exc)
            self.send_response(204)
            self.end_headers()
            return

        # OH4-02: Send-now endpoint. Atomic claim + Twilio send; rolls back
        # sent=1 on Twilio failure so the scheduler can retry later. Mirrors
        # the DELETE /queue HMAC pattern exactly — do not introduce a new
        # auth scheme here.
        if self.path.split("?")[0] == "/earlscheibconcord/queue/send-now":
            # RJL-02: dual auth — operator can click "Send now" from the
            # browser UI at /earlscheib. Body-signed HMAC still works for the
            # Go admin proxy.
            if not _validate_auth(self, raw):
                self._send_json(401, {"error": "invalid signature"})
                return
            try:
                body = json.loads(raw.decode("utf-8"))
                job_id = int(body["id"])
            except (ValueError, KeyError, json.JSONDecodeError, UnicodeDecodeError):
                self._send_json(400, {"error": "invalid JSON"})
                return

            con = get_db()
            try:
                cur = con.cursor()
                # Atomic claim: only one concurrent caller can flip this row.
                cur.execute(
                    "UPDATE jobs SET sent = 1, sent_at = ? "
                    "WHERE id = ? AND sent = 0",
                    (int(time.time()), job_id),
                )
                con.commit()
                if cur.rowcount != 1:
                    self._send_json(404, {"error": "not_found_or_already_sent"})
                    return
                cur.execute(
                    "SELECT job_type, phone, name, vin, vehicle_desc, "
                    "       ro_id, email, doc_id "
                    "FROM jobs WHERE id = ?",
                    (job_id,),
                )
                row = cur.fetchone()
            finally:
                con.close()

            # WMH-01: Compose SMS body via render_template so Marco's edits
            # from the Templates admin page flow through. Falls back to
            # DEFAULT_TEMPLATES[job_type] when no override exists. Must stay
            # in lock-step with the UI preview (main.js previewSMS).
            sms_body = render_template(row["job_type"], row)

            phone = row["phone"]
            # Recipient redirection (TEST_PHONE_OVERRIDE / TEST_PHONE_RECIPIENTS)
            # is handled inside send_sms so admin UI shows the real customer
            # phone while testing still routes messages to the operator.
            ok = send_sms(phone, sms_body)
            if ok:
                log.info("send-now: id=%s phone=%s type=%s OK",
                         job_id, phone, row["job_type"])
                self._send_json(200, {"sent": True})
            else:
                # Twilio failed — roll back the sent flag so the scheduler
                # can retry. Without this the row is permanently "sent"
                # without an SMS ever leaving Twilio.
                con2 = get_db()
                try:
                    cur2 = con2.cursor()
                    cur2.execute(
                        "UPDATE jobs SET sent = 0, sent_at = 0 WHERE id = ?",
                        (job_id,),
                    )
                    con2.commit()
                finally:
                    con2.close()
                log.error("send-now: id=%s twilio send failed; rolled back sent flag", job_id)
                self._send_json(500, {"error": "twilio_send_failed"})
            return

        if self.path.split("?")[0] == "/earlscheibconcord/heartbeat":
            import xml.etree.ElementTree as _ET
            try:
                root = _ET.fromstring(raw)
                host = root.findtext("Host") or "unknown"
            except Exception:
                host = "unknown"
            LAST_HEARTBEAT["ts"] = int(time.time())
            LAST_HEARTBEAT["host"] = host
            log.info("Heartbeat received from %s", host)
            self._send_json(200, {"status": "ok"})
            return

        log.info("Webhook payload (first 2000 chars): %s", raw[:2000].decode("utf-8", errors="replace"))

        data = parse_bms(raw)
        if not data:
            self._send_json(400, {"error": "invalid BMS payload"})
            return

        doc_id = data.get("doc_id", "")
        doc_status = data.get("doc_status", "")
        name = data.get("name", "there")
        phone = data.get("phone", "")

        # OH4-01 extra customer context threaded through to schedule_job for
        # the admin UI (VIN masked display, vehicle_desc header, RO tag, etc.)
        vin = data.get("vin", "")
        vehicle_desc = data.get("vehicle_desc", "")
        ro_id = data.get("ro_id", "")
        email = data.get("email", "")
        address = data.get("address", "")

        # Keep the real customer phone in the jobs table so the admin UI is
        # accurate; redirection to TEST_PHONE_OVERRIDE / TEST_PHONE_RECIPIENTS
        # happens inside send_sms at dispatch time.
        # When those test flags are set we also need to schedule even if the
        # customer row itself has no phone — otherwise we'd silently drop
        # test estimates whose AD1 block was blank.
        if not phone and not (TEST_PHONE_OVERRIDE or TEST_PHONE_RECIPIENTS):
            log.warning("No valid phone for doc_id=%s, skipping", doc_id)
            self._send_json(200, {"status": "no_phone"})
            return

        now = int(time.time())

        if doc_status in ESTIMATE_STATUSES:
            log.info("Estimate status %s for doc_id=%s", doc_status, doc_id)
            schedule_job(doc_id, "24h", phone, name, next_send_window(now + 24*3600),
                         vin=vin, vehicle_desc=vehicle_desc, ro_id=ro_id,
                         email=email, address=address)
            schedule_job(doc_id, "3day", phone, name, next_send_window(now + 72*3600),
                         vin=vin, vehicle_desc=vehicle_desc, ro_id=ro_id,
                         email=email, address=address)
        elif doc_status in CLOSED_STATUSES:
            log.info("Closed status %s for doc_id=%s", doc_status, doc_id)
            schedule_job(doc_id, "review", phone, name, next_send_window(now + 24*3600),
                         vin=vin, vehicle_desc=vehicle_desc, ro_id=ro_id,
                         email=email, address=address)
        else:
            log.info("Unhandled doc_status=%s for doc_id=%s, no jobs scheduled", doc_status, doc_id)

        self._send_json(200, {"status": "ok", "doc_id": doc_id, "doc_status": doc_status})

    # ------------------------------------------------------------------
    def do_DELETE(self):
        if self.path.split("?")[0] != "/earlscheibconcord/queue":
            self.send_response(404)
            self.end_headers()
            return

        content_length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(content_length) if content_length > 0 else b""

        # RJL-02: dual auth — operator's browser cancels jobs via DELETE too.
        # HMAC signs the exact JSON body bytes (matches telemetry precedent);
        # Basic auth is accepted unconditionally of body content.
        if not _validate_auth(self, raw):
            self._send_json(401, {"error": "invalid signature"})
            return

        try:
            body = json.loads(raw.decode("utf-8"))
            job_id = int(body["id"])
        except (ValueError, KeyError, json.JSONDecodeError, UnicodeDecodeError):
            self._send_json(400, {"error": "invalid JSON"})
            return

        con = get_db()
        try:
            cur = con.cursor()
            cur.execute("DELETE FROM jobs WHERE id = ? AND sent = 0", (job_id,))
            con.commit()
            affected = cur.rowcount
        finally:
            con.close()

        if affected == 1:
            log.info("Job cancelled via admin UI: id=%s", job_id)
            self._send_json(200, {"deleted": 1})
        else:
            self._send_json(404, {"error": "not found or already sent"})

    # ------------------------------------------------------------------
    def do_PUT(self):
        """WMH-02: Marco-editable message templates.

        Routes:
          PUT /earlscheibconcord/templates/{job_type}
            Body: {"body": "..."}  (<=2000 chars)
            Empty/whitespace body → DELETE row (revert to default).
            Non-empty body        → UPSERT (validates renderability first).
            job_type must be one of the keys in JOB_TYPE_META.

        All other paths return 404. Dual-auth (HMAC over the raw body, or
        browser Basic auth).
        """
        path = urlparse(self.path).path.rstrip("/")
        prefix = "/earlscheibconcord/templates/"

        if not path.startswith(prefix):
            self.send_response(404)
            self.end_headers()
            return

        content_length = int(self.headers.get("Content-Length", 0))
        raw = self.rfile.read(content_length) if content_length > 0 else b""

        if not _validate_auth(self, raw):
            self._send_json(401, {"error": "invalid signature"})
            return

        job_type = path[len(prefix):]
        valid_types = {m["job_type"] for m in JOB_TYPE_META}
        if job_type not in valid_types:
            self._send_json(400, {"error": "unknown job_type"})
            return

        # Body parse + size guard. 2000-char cap matches the textarea
        # maxlength on the client — defence in depth, not UX.
        try:
            parsed = json.loads(raw.decode("utf-8"))
        except (ValueError, UnicodeDecodeError, json.JSONDecodeError):
            self._send_json(400, {"error": "invalid JSON"})
            return
        if not isinstance(parsed, dict) or "body" not in parsed:
            self._send_json(400, {"error": "missing body field"})
            return
        body_val = parsed["body"]
        if not isinstance(body_val, str):
            self._send_json(400, {"error": "body must be a string"})
            return
        if len(body_val) > 2000:
            self._send_json(400, {"error": "body exceeds 2000 characters"})
            return

        # Empty / whitespace-only → DELETE (revert to default).
        if not body_val.strip():
            con = get_db()
            try:
                cur = con.cursor()
                cur.execute("DELETE FROM templates WHERE job_type = ?", (job_type,))
                con.commit()
            finally:
                con.close()
            log.info("templates: %s reverted to default", job_type)
            self._send_json(200, {
                "is_override": False,
                "body":        DEFAULT_TEMPLATES[job_type],
                "updated_at":  0,
            })
            return

        # Renderability check — a malformed template (e.g. "Hi {unclosed")
        # would crash every future send. Test against a realistic sample
        # built from SHOP_CONSTANTS + a canned per-row dict.
        sample_ctx = {
            "first_name":   "Alex",
            "name":         "Alex Martinez",
            "phone":        "+15551234567",
            "vin":          "1HGCM82633A004352",
            "vehicle_desc": "2018 Honda Accord",
            "ro_id":        "RO-1234",
            "doc_id":       "DOC-ABC-01",
            "email":        "alex@example.com",
        }
        render_ctx = defaultdict(str)
        render_ctx.update(SHOP_CONSTANTS)
        render_ctx.update(sample_ctx)
        try:
            body_val.format_map(render_ctx)
        except (KeyError, IndexError, ValueError) as exc:
            self._send_json(400, {
                "error":  "template syntax error",
                "detail": str(exc),
            })
            return

        now = int(time.time())
        con = get_db()
        try:
            cur = con.cursor()
            cur.execute(
                "INSERT OR REPLACE INTO templates(job_type, body, updated_at) "
                "VALUES (?, ?, ?)",
                (job_type, body_val, now),
            )
            con.commit()
        finally:
            con.close()

        log.info("templates: %s override saved (%d chars)", job_type, len(body_val))
        self._send_json(200, {
            "is_override": True,
            "body":        body_val,
            "updated_at":  now,
        })


# ---------------------------------------------------------------------------
# Main
# ---------------------------------------------------------------------------

def main():
    init_db()

    scheduler_thread = threading.Thread(target=scheduler_loop, daemon=True)
    scheduler_thread.start()

    server_address = ("0.0.0.0", PORT)
    httpd = HTTPServer(server_address, WebhookHandler)
    log.info("Listening on 0.0.0.0:%d", PORT)
    try:
        httpd.serve_forever()
    except KeyboardInterrupt:
        log.info("Shutting down")
        httpd.server_close()


if __name__ == "__main__":
    main()
