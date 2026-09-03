import importlib
import sqlite3

DAY = 86400
NOW = 1788480000  # 2026-09-03 ~17:00 PDT


def _boot(tmp_path, monkeypatch):
    monkeypatch.setenv("DB_PATH", str(tmp_path / "jobs.db"))
    import app
    app = importlib.reload(app)
    app.init_db()
    return app, sqlite3.connect(str(tmp_path / "jobs.db"))


def _seed(con):
    # Six estimates. Keys are phone|vin. Times relative to NOW.
    jobs = [
        # A: texted (24h sent day -20), replied day -19, RO closed day -10 -> won after text, $3,000
        ("A", "+19255550001", "VINA00000000000A1", "24h", 1, NOW - 20 * DAY, 0, NOW - 21 * DAY),
        ("A", "+19255550001", "VINA00000000000A1", "3day", 0, 0, 1, NOW - 21 * DAY),
        ("A", "+19255550001", "VINA00000000000A1", "review", 0, 0, 0, NOW - 10 * DAY),
        # B: texted day -8, replied day -7, no booking, no RO -> warm lead
        ("B", "+19255550002", "VINB00000000000B1", "24h", 1, NOW - 8 * DAY, 0, NOW - 9 * DAY),
        # C: never texted (follow-ups cancelled), review job same day -> same-day approval, RO $2,000
        ("C", "+19255550003", "VINC00000000000C1", "24h", 0, 0, 1, NOW - 15 * DAY),
        ("C", "+19255550003", "VINC00000000000C1", "review", 1, NOW - 14 * DAY, 0, NOW - 15 * DAY),
        # D: texted day -5, booked day -4 (appointment day -2), no RO yet -> won after text via booking; no-show
        ("D", "+19255550004", "VIND00000000000D1", "24h", 1, NOW - 5 * DAY, 0, NOW - 6 * DAY),
        # E: test row -> excluded
        ("E", "+15308450190", "VINE00000000000E1", "24h", 1, NOW - 3 * DAY, 0, NOW - 3 * DAY),
        # F: old estimate outside a 30-day window (created day -50), texted day -49
        ("F", "+19255550006", "VINF00000000000F1", "24h", 1, NOW - 49 * DAY, 0, NOW - 50 * DAY),
    ]
    for doc, phone, vin, jt, sent, sent_at, cancelled, created in jobs:
        con.execute(
            "INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at, vin, estimate_key, is_test, cancelled, sent_at) "
            "VALUES (?,?,?,?,?,?,?,?,?,?,?,?)",
            (doc, jt, phone, f"Cust {doc}", created, sent, created, vin, f"{phone}|{vin}", 1 if doc == "E" else 0, cancelled, sent_at))
    con.executemany("INSERT INTO inbound_sms (sid, from_phone, to_phone, body, date_sent, synced_at) VALUES (?,?,?,?,?,?)", [
        ("S1", "+19255550001", "+19256033934", "yes lets do it", NOW - 19 * DAY, NOW),
        ("S2", "+19255550002", "+19256033934", "how much?", NOW - 7 * DAY, NOW),
    ])
    con.executemany("INSERT INTO sms_log (created_at, job_id, job_type, phone, body, status, kind, is_test, error) VALUES (?,?,?,?,?,?,?,?,?)", [
        (NOW - 19 * DAY + 600, None, "", "+19255550001", "Great, come by Monday", "sent", "reply", 0, ""),   # Marco replied in 10 min
        (NOW - 7 * DAY + 7200, None, "", "+19255550002", "$900", "sent", "reply", 0, ""),                 # 2 h
    ])
    con.execute("INSERT INTO appointments (estimate_key, appointment_at, created_at, updated_at, unbooked_at) VALUES (?,?,?,?,?)",
                ("+19255550004|VIND00000000000D1", NOW - 2 * DAY, NOW - 4 * DAY, NOW - 4 * DAY, 0))
    con.executemany("INSERT INTO ro_exports (doc_id, phone, vin, owner, trans_type, supp_no, create_dt, ro_in, date_out, g_ttl, closed, file_mtime, parsed_at) VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)", [
        ("A", "+19255550001", "VINA00000000000A1", "Cust A", "E", "E01", "2026-08-13", "2026-08-24", "2026-08-24", 3000.0, 1, NOW, NOW),
        ("C", "+19255550003", "VINC00000000000C1", "Cust C", "E", "E01", "2026-08-19", "2026-08-19", "2026-08-20", 2000.0, 1, NOW, NOW),
        ("B", "+19255550002", "VINB00000000000B1", "Cust B", "E", "E01", "2026-08-25", "", "", 472.5, 0, NOW, NOW),
    ])
    con.commit()


def test_funnel_and_attribution_30_days(tmp_path, monkeypatch):
    app, con = _boot(tmp_path, monkeypatch)
    _seed(con)
    out = app.compute_insights(con, 30, NOW)
    f = {row["label"]: row for row in out["funnel"]}
    assert f["Estimates"]["n"] == 4            # A B C D (E test, F too old)
    assert f["Texted"]["n"] == 3               # A B D
    assert f["Replied"]["n"] == 2              # A B
    assert f["Won after text"]["n"] == 2       # A (RO), D (booking)
    assert f["Closed RO"]["n"] == 1 and f["Closed RO"]["revenue"] == 3000.0
    assert out["tiles"]["closed_ros"]["value"] == 2          # A and C (shop total in window)
    assert out["tiles"]["revenue"]["value"] == 5000.0
    assert out["tiles"]["avg_ticket"]["value"] == 2500.0
    assert out["tiles"]["marco_reply_median_min"]["value"] == 65.0   # median of 10 and 120
    assert [w["name"] for w in out["warm_leads"]] == ["Cust B"]
    assert out["warm_leads"][0]["estimate_total"] == 472.5
    assert [n["name"] for n in out["no_shows"]] == ["Cust D"]
    assert out["bookings_upcoming"] == []
    assert out["template_reply_rate"]["24h"] == {"sent": 3, "replied": 2}
    # The rule counts OPEN exports only; the seed has exactly one open export
    # (B at the 472.5 placeholder), so of == 1.
    assert out["placeholder_estimates"] == {"n": 1, "of": 1}


def test_ytd_includes_old_estimate_and_prev_window(tmp_path, monkeypatch):
    app, con = _boot(tmp_path, monkeypatch)
    _seed(con)
    out = app.compute_insights(con, None, NOW)
    assert out["period"]["from"].startswith("2026-01-01")
    assert out["funnel"][0]["n"] == 5          # F now inside
    out30 = app.compute_insights(con, 30, NOW)
    assert out30["tiles"]["estimates"] == {"value": 4, "prev": 1}   # F sits in the previous 30-day window
