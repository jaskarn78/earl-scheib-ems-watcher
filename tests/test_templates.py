"""Unit tests for WMH-01 render_template helper.

Exercises default-vs-override lookup, per-row placeholder substitution, shop
constants, first_name derivation, missing-key safety, and unknown job_type
handling. No HTTP, no Twilio — pure function tests against app.py internals.
"""
import importlib
import sqlite3
import time

import pytest


@pytest.fixture
def app_with_db(tmp_path, monkeypatch):
    """Reload app.py against a fresh DB so init_db creates the templates
    table in a scratch location."""
    secret = "pytest-wmh-templates-secret"
    db_path = str(tmp_path / "jobs.db")
    telem_path = str(tmp_path / "telemetry.log")
    rc_path = str(tmp_path / "remote_config.json")

    monkeypatch.setenv("CCC_SECRET", secret)
    monkeypatch.setenv("DB_PATH", db_path)
    monkeypatch.setenv("TELEMETRY_LOG_PATH", telem_path)
    monkeypatch.setenv("REMOTE_CONFIG_PATH", rc_path)
    monkeypatch.setenv("PORT", "0")

    import app
    importlib.reload(app)
    app.init_db()

    return app, db_path


def _fake_row(**overrides):
    """Build a dict row with the columns render_template looks for."""
    row = {
        "first_name":    "",
        "name":          "",
        "phone":         "",
        "vin":           "",
        "vehicle_desc": "",
        "ro_id":         "",
        "doc_id":        "",
        "email":         "",
    }
    row.update(overrides)
    return row


# ---------- Default templates ----------

def test_default_template_24h_uses_shop_constants(app_with_db):
    app, _ = app_with_db
    out = app.render_template("24h", _fake_row(name="Alex Martinez"))
    assert "Alex" in out
    assert "Earl Scheib Auto Body Concord" in out
    assert "(925) 609-7780" in out
    assert "{" not in out  # no unresolved placeholders leaking to customer


def test_default_template_3day_uses_shop_constants(app_with_db):
    app, _ = app_with_db
    out = app.render_template("3day", _fake_row(name="Alex Martinez"))
    assert "Alex" in out
    assert "Earl Scheib Auto Body Concord" in out
    assert "(925) 609-7780" in out
    assert "{" not in out


def test_default_template_review_includes_review_url(app_with_db):
    app, _ = app_with_db
    out = app.render_template("review", _fake_row(name="Alex Martinez"))
    assert "Alex" in out
    assert "Earl Scheib Auto Body Concord" in out
    assert "https://g.page/r/review" in out
    assert "{" not in out


# ---------- Override path ----------

def test_override_overrides_default(app_with_db):
    app, db_path = app_with_db
    # Seed an override row directly.
    con = sqlite3.connect(db_path)
    con.execute(
        "INSERT INTO templates(job_type, body, updated_at) VALUES (?, ?, ?)",
        ("24h", "Override for {first_name} from {shop_name}.", int(time.time())),
    )
    con.commit()
    con.close()

    out = app.render_template("24h", _fake_row(name="Carol Example"))
    assert out == "Override for Carol from Earl Scheib Auto Body Concord."


def test_empty_override_falls_back_to_default(app_with_db):
    """Empty-body row is treated as 'no override' (defence-in-depth — the PUT
    handler normally DELETEs the row, but defaults must still apply if a
    row sneaks in via manual SQL)."""
    app, db_path = app_with_db
    con = sqlite3.connect(db_path)
    con.execute(
        "INSERT INTO templates(job_type, body, updated_at) VALUES (?, ?, ?)",
        ("3day", "   \n  ", int(time.time())),
    )
    con.commit()
    con.close()

    out = app.render_template("3day", _fake_row(name="Dave Example"))
    assert "Dave" in out
    assert "Earl Scheib Auto Body Concord" in out  # default fired


# ---------- Placeholder semantics ----------

def test_missing_placeholder_renders_empty(app_with_db):
    """Unknown {placeholder} must render to "" — never KeyError, never leak."""
    app, db_path = app_with_db
    con = sqlite3.connect(db_path)
    con.execute(
        "INSERT INTO templates(job_type, body, updated_at) VALUES (?, ?, ?)",
        ("24h", "Hi {first_name}, your VIN is {vin} and car is {vehicle_desc}.{mystery_key}",
         int(time.time())),
    )
    con.commit()
    con.close()

    out = app.render_template(
        "24h",
        _fake_row(name="Alex Test", vin="1HGCM82633A004352", vehicle_desc=""),
    )
    assert "Alex" in out
    assert "1HGCM82633A004352" in out
    assert "{" not in out  # {vehicle_desc} + {mystery_key} erased, no literals


def test_first_name_derivation_from_name(app_with_db):
    app, _ = app_with_db
    out = app.render_template("24h", _fake_row(name="First Middle Last"))
    assert "Hi First," in out


def test_first_name_fallback_to_there(app_with_db):
    """No name, no first_name -> "there" fallback preserved."""
    app, _ = app_with_db
    out = app.render_template("24h", _fake_row())
    assert "Hi there," in out


def test_explicit_first_name_beats_name_split(app_with_db):
    app, db_path = app_with_db
    con = sqlite3.connect(db_path)
    con.execute(
        "INSERT INTO templates(job_type, body, updated_at) VALUES (?, ?, ?)",
        ("24h", "Hello {first_name}", int(time.time())),
    )
    con.commit()
    con.close()
    # Row provides explicit first_name — must override derivation from name.
    out = app.render_template(
        "24h",
        _fake_row(first_name="Nickname", name="Legal FullName"),
    )
    assert out == "Hello Nickname"


def test_sqlite_row_supported(app_with_db):
    """render_template must accept sqlite3.Row, not just plain dicts."""
    app, db_path = app_with_db
    con = sqlite3.connect(db_path)
    con.row_factory = sqlite3.Row
    # Insert then SELECT to get a real sqlite3.Row back.
    con.execute(
        "INSERT INTO jobs(doc_id, job_type, phone, name, send_at, sent, created_at, "
        "                 vin, vehicle_desc, ro_id, email, address, sent_at, estimate_key) "
        "VALUES (?, ?, ?, ?, ?, 0, ?, ?, ?, ?, ?, '', 0, ?)",
        ("DOC-1", "24h", "+15551112222", "Grace Hopper",
         int(time.time()) + 3600, int(time.time()),
         "5YJSA1E26JF266123", "2018 Tesla S", "RO-99", "grace@example.com",
         "+15551112222|5YJSA1E26JF266123"),
    )
    con.commit()
    row = con.execute("SELECT * FROM jobs WHERE doc_id = 'DOC-1'").fetchone()
    con.close()

    out = app.render_template("24h", row)
    assert "Grace" in out
    assert "Earl Scheib Auto Body Concord" in out


# ---------- Unknown job_type ----------

def test_unknown_job_type_returns_empty(app_with_db):
    app, _ = app_with_db
    out = app.render_template("bogus", _fake_row(name="Alex"))
    assert out == ""


def test_none_row_still_renders_with_defaults(app_with_db):
    app, _ = app_with_db
    out = app.render_template("24h", None)
    # No name -> "there" fallback; shop constants still present.
    assert "Hi there" in out
    assert "Earl Scheib Auto Body Concord" in out


# ---------- Legacy aliases (back-compat) ----------

def test_legacy_aliases_still_defined(app_with_db):
    """MSG_24H / MSG_3DAY / MSG_REVIEW remain importable as the default body
    strings. Guards against external callers breaking silently."""
    app, _ = app_with_db
    assert app.MSG_24H == app.DEFAULT_TEMPLATES["24h"]
    assert app.MSG_3DAY == app.DEFAULT_TEMPLATES["3day"]
    assert app.MSG_REVIEW == app.DEFAULT_TEMPLATES["review"]
