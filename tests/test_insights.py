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


def test_timeline_orders_events(tmp_path, monkeypatch):
    app, con = _boot(tmp_path, monkeypatch)
    _seed(con)
    tl = app.compute_timeline(con, "+19255550001|VINA00000000000A1", NOW)
    assert tl["header"]["name"] == "Cust A" and tl["header"]["status"] == "closed"
    kinds = [e["kind"] for e in tl["events"]]
    labels = [e["label"] for e in tl["events"]]
    # estimate, cancelled 3-day, 24h text, customer reply, Marco reply, RO closed.
    # A's seeded 3-day follow-up is cancelled, and a cancelled follow-up is part
    # of the story the timeline tells ("we did not keep nagging them"), so it is
    # an event too — the brief's expected list omitted it.
    assert kinds == ["estimate", "text", "text", "reply", "text", "ro_closed"]
    assert labels[1] == "3day follow-up cancelled"
    assert labels[2] == "24-hour follow-up sent"
    assert tl["events"][-1]["detail"] == "$3,000 per CCC"
    # A is closed and has no open export left, so the header falls back to the
    # closed RO total rather than showing a bare $0.
    assert tl["header"]["estimate_total"] == 3000.0
    assert app.compute_timeline(con, "nope|nope", NOW) is None


def _add_job(con, doc, phone, vin, created, sent=0, sent_at=0, job_type="24h"):
    con.execute(
        "INSERT INTO jobs (doc_id, job_type, phone, name, send_at, sent, created_at, vin, estimate_key, is_test, cancelled, sent_at) "
        "VALUES (?,?,?,?,?,?,?,?,?,0,0,?)",
        (doc, job_type, phone, f"Cust {doc}", created, sent, created, vin, f"{phone}|{vin}", sent_at))


def _add_ro(con, doc, phone, vin, ro_in, date_out, g_ttl, closed):
    con.execute(
        "INSERT INTO ro_exports (doc_id, phone, vin, owner, trans_type, supp_no, create_dt, ro_in, date_out, g_ttl, closed, file_mtime, parsed_at) "
        "VALUES (?,?,?,'','E','E01','',?,?,?,?,?,?)",
        (doc, phone, vin, ro_in, date_out, g_ttl, closed, NOW, NOW))


def test_closed_ro_on_window_boundary_counted_once(tmp_path, monkeypatch):
    """A closed RO dated on the current window's first day belongs to the
    current window only. `start` is now-N*86400, not local midnight, so a naive
    prev-window end of `start - 1` lands on the same calendar day and counts it
    twice."""
    app, con = _boot(tmp_path, monkeypatch)
    _seed(con)
    boundary_day = app._day(NOW - 30 * DAY)   # first day of the 30-day window
    _add_ro(con, "BND", "+19255559001", "VINX0000000000B01", boundary_day, boundary_day, 1000.0, 1)
    con.commit()
    out = app.compute_insights(con, 30, NOW)
    assert out["tiles"]["closed_ros"] == {"value": 3, "prev": 0}
    assert out["tiles"]["revenue"]["value"] == 6000.0
    assert out["tiles"]["revenue"]["prev"] == 0.0


def test_reply_median_ignores_test_sms_log_rows(tmp_path, monkeypatch):
    """Test rows are excluded everywhere, including the Marco-response clock."""
    app, con = _boot(tmp_path, monkeypatch)
    _seed(con)
    # A test-harness "reply" one minute after Cust A's inbound. If it counted,
    # A's gap would be 1 min instead of 10 and the median would be 60.5.
    con.execute("INSERT INTO sms_log (created_at, job_id, job_type, phone, body, status, kind, is_test, error) VALUES (?,?,?,?,?,?,?,?,?)",
                (NOW - 19 * DAY + 60, None, "", "+19255550001", "harness ping", "sent", "reply", 1, ""))
    con.commit()
    out = app.compute_insights(con, 30, NOW)
    assert out["tiles"]["marco_reply_median_min"]["value"] == 65.0


def test_placeholder_vin_estimates_do_not_receive_ros(tmp_path, monkeypatch):
    """"UNK" is CCC's not-recorded placeholder, not a vehicle identifier. It must
    neither collapse estimates together nor let a garbage-VIN RO attach."""
    app, con = _boot(tmp_path, monkeypatch)
    _add_job(con, "U1", "+19255550007", "UNK", NOW - 20 * DAY, sent=1, sent_at=NOW - 19 * DAY)
    _add_job(con, "U2", "+19255550008", "UNK", NOW - 18 * DAY, sent=1, sent_at=NOW - 17 * DAY)
    _add_ro(con, "U9", "+19255559002", "UNK", "2026-08-20", "2026-08-21", 5000.0, 1)
    _add_ro(con, "U8", "+19255550007", "UNK", "", "", 800.0, 0)
    con.commit()
    idx = app._customer_index(con)
    assert set(idx) == {"+19255550007|UNK", "+19255550008|UNK"}   # not collapsed
    for c in idx.values():
        assert c["ros"] == []
        assert c["estimate_total"] == 0.0


def test_shared_vin_ro_attaches_to_latest_estimate_before_ro_in(tmp_path, monkeypatch):
    """When several estimates share a VIN and the RO's own phone matches none of
    them, the RO belongs to the most recent estimate written on or before the car
    came in — falling back to the earliest when the RO predates them all."""
    app, con = _boot(tmp_path, monkeypatch)
    vin = "VINX0000000000X01"
    old_key, new_key = f"+19255550011|{vin}", f"+19255550012|{vin}"
    _add_job(con, "S1", "+19255550011", vin, NOW - 40 * DAY, sent=1, sent_at=NOW - 39 * DAY)
    _add_job(con, "S2", "+19255550012", vin, NOW - 20 * DAY, sent=1, sent_at=NOW - 19 * DAY)
    _add_ro(con, "R-LATE", "+19255559003", vin, "2026-08-24", "2026-08-26", 3200.0, 1)
    _add_ro(con, "R-EARLY", "+19255559003", vin, "2026-07-01", "2026-07-03", 1100.0, 1)
    con.commit()
    idx = app._customer_index(con)
    assert [r["doc_id"] for r in idx[new_key]["ros"]] == ["R-LATE"]
    assert [r["doc_id"] for r in idx[old_key]["ros"]] == ["R-EARLY"]


def test_insights_sync_skips_when_already_running(tmp_path, monkeypatch):
    app, _con = _boot(tmp_path, monkeypatch)
    calls = []
    monkeypatch.setattr(app, "sync_ro_exports", lambda *a, **k: (calls.append("ro"), 0)[1])
    monkeypatch.setattr(app, "sync_inbound_sms", lambda *a, **k: 0)
    app._INSIGHTS_SYNC_LOCK.acquire()
    try:
        app._run_insights_sync()      # a previous run is still going -> skip
        assert calls == []
    finally:
        app._INSIGHTS_SYNC_LOCK.release()
    app._run_insights_sync()          # lock free -> runs
    assert calls == ["ro"]


def test_insights_sync_never_raises(tmp_path, monkeypatch):
    app, _con = _boot(tmp_path, monkeypatch)
    monkeypatch.setattr(app, "sync_ro_exports", lambda *a, **k: 1 / 0)
    app._run_insights_sync()          # must swallow, and must free the lock
    assert app._INSIGHTS_SYNC_LOCK.acquire(blocking=False)
    app._INSIGHTS_SYNC_LOCK.release()


def test_compute_insights_is_set_based_on_a_full_month(tmp_path, monkeypatch):
    """50 estimates, all texted, every other one replied to.

    The counts are hand-computable, and the query count is asserted flat: the
    reply-rate and reply-median passes used to issue a query per estimate (per
    job type) and per inbound message, so a regression there shows up here as
    a query count that scales with the number of customers.
    """
    app, con = _boot(tmp_path, monkeypatch)
    n = 50
    for i in range(n):
        phone = f"+1925555{7000 + i:04d}"
        vin = f"VIN{i:014d}"
        _add_job(con, f"P{i}", phone, vin, NOW - 10 * DAY, sent=1, sent_at=NOW - 9 * DAY)
        if i % 2 == 0:
            con.execute("INSERT INTO inbound_sms (sid, from_phone, to_phone, body, date_sent, synced_at) VALUES (?,?,?,?,?,?)",
                        (f"P{i}", phone, "+19256033934", "sounds good", NOW - 9 * DAY + 3600, NOW))
    con.commit()

    queries = []
    con.set_trace_callback(queries.append)
    out = app.compute_insights(con, 30, NOW)
    con.set_trace_callback(None)

    f = {row["label"]: row["n"] for row in out["funnel"]}
    assert f == {"Estimates": n, "Texted": n, "Replied": n // 2, "Won after text": 0, "Closed RO": 0}
    assert out["tiles"]["estimates"] == {"value": n, "prev": 0}
    assert out["tiles"]["texted"]["value"] == n
    assert out["tiles"]["replies"]["value"] == n // 2
    assert out["template_reply_rate"]["24h"] == {"sent": n, "replied": n // 2}
    assert out["template_reply_rate"]["3day"] == {"sent": 0, "replied": 0}
    # Nobody at the shop answered any of them, so there is no median to take.
    assert out["tiles"]["marco_reply_median_min"]["value"] == 0.0
    assert len(queries) < 25, f"{len(queries)} queries for {n} customers — not set-based"
