"""Shared fixtures for Queue endpoint tests.

Spins up a WebhookHandler on 127.0.0.1:EPHEMERAL in a daemon thread,
using an isolated DB_PATH and REMOTE_CONFIG_PATH inside a tmp dir,
and a known CCC_SECRET. Yields a base URL + raw secret.
"""
import hashlib
import hmac
import importlib
import os
import sqlite3
import threading
import time
from http.server import HTTPServer

import pytest


@pytest.fixture
def queue_server(tmp_path, monkeypatch):
    secret = "pytest-fixture-secret-do-not-ship"
    db_path = str(tmp_path / "jobs.db")
    telem_path = str(tmp_path / "telemetry.log")
    rc_path = str(tmp_path / "remote_config.json")

    monkeypatch.setenv("CCC_SECRET", secret)
    monkeypatch.setenv("DB_PATH", db_path)
    monkeypatch.setenv("TELEMETRY_LOG_PATH", telem_path)
    monkeypatch.setenv("REMOTE_CONFIG_PATH", rc_path)
    monkeypatch.setenv("PORT", "0")

    # Reload app.py with the patched env so module-level constants pick up
    import app
    importlib.reload(app)

    app.init_db()

    # Seed deterministic rows
    con = sqlite3.connect(db_path)
    cur = con.cursor()
    now = int(time.time())
    cur.executemany(
        "INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at) "
        "VALUES (?, ?, ?, ?, ?, ?, ?)",
        [
            ("DOC-A", "24h",    "+15551110001", "Alice Example",    now + 3600, 0, now - 60),
            ("DOC-B", "3day",   "+15551110002", "Bob Example",      now + 7200, 0, now - 30),
            ("DOC-C", "review", "+15551110003", "Carol Example",    now + 1800, 0, now - 10),
            ("DOC-D", "24h",    "+15551110004", "Dave Alreadysent", now - 1000, 1, now - 500),
        ],
    )
    con.commit()
    con.close()

    server = HTTPServer(("127.0.0.1", 0), app.WebhookHandler)
    port = server.server_address[1]
    thread = threading.Thread(target=server.serve_forever, daemon=True)
    thread.start()

    base_url = f"http://127.0.0.1:{port}"
    yield {"base_url": base_url, "secret": secret, "db_path": db_path}

    server.shutdown()
    server.server_close()


def sign(secret: str, body: bytes) -> str:
    return hmac.new(secret.encode("utf-8"), body, hashlib.sha256).hexdigest()
