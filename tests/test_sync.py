import importlib
import os
import sqlite3
import time
from datetime import date

from tests.dbfw import write_dbf


def _boot(tmp_path, monkeypatch):
    monkeypatch.setenv("DB_PATH", str(tmp_path / "jobs.db"))
    monkeypatch.setenv("CCC_EXPORTS_DIR", str(tmp_path / "exports"))
    os.makedirs(tmp_path / "exports", exist_ok=True)
    import app
    app = importlib.reload(app)
    app.init_db()
    return app


def _write_set(d, doc, owner_fn, owner_ln, phone, vin, total, date_out=None, ro_in=None, create=date(2026, 6, 1)):
    base = str(d / doc)
    write_dbf(base + ".env", [("UNQFILE_ID", "C", 8, 0), ("TRANS_TYPE", "C", 2, 0), ("SUPP_NO", "C", 3, 0), ("CREATE_DT", "D", 8, 0)],
              [{"UNQFILE_ID": doc, "TRANS_TYPE": "E", "SUPP_NO": "E01", "CREATE_DT": create}])
    write_dbf(base + ".ad1", [("OWNR_FN", "C", 20, 0), ("OWNR_LN", "C", 20, 0), ("OWNR_PH1", "C", 14, 0), ("OWNR_PH2", "C", 14, 0), ("INSD_PH1", "C", 14, 0)],
              [{"OWNR_FN": owner_fn, "OWNR_LN": owner_ln, "OWNR_PH1": phone}])
    write_dbf(base + ".ad2", [("RO_IN_DATE", "D", 8, 0), ("DATE_OUT", "D", 8, 0)],
              [{"RO_IN_DATE": ro_in or "", "DATE_OUT": date_out or ""}])
    write_dbf(base + ".veh", [("V_VIN", "C", 17, 0)], [{"V_VIN": vin}])
    write_dbf(base + ".ttl", [("G_TTL_AMT", "N", 10, 2)], [{"G_TTL_AMT": total}])
    return base


def test_parse_export_set_open_and_closed(tmp_path, monkeypatch):
    app = _boot(tmp_path, monkeypatch)
    d = tmp_path / "exports"
    _write_set(d, "aaaa0001", "Kathy", "Fowler", "9255550101", "1HGCM66585A003562", 472.5)
    _write_set(d, "bbbb0002", "Lucy", "Loluo", "(925) 353-8099", "2HNYD2H45AH523838", 2422.89,
               date_out=date(2026, 5, 21), ro_in=date(2026, 5, 21), create=date(2026, 5, 29))
    a = app._parse_export_set(str(d / "aaaa0001"))
    b = app._parse_export_set(str(d / "bbbb0002"))
    assert a == {"doc_id": "aaaa0001", "phone": "+19255550101", "vin": "1HGCM66585A003562", "owner": "Kathy Fowler",
                 "trans_type": "E", "supp_no": "E01", "create_dt": "2026-06-01", "ro_in": "", "date_out": "", "g_ttl": 472.5, "closed": 0}
    assert b["closed"] == 1 and b["phone"] == "+19253538099" and b["date_out"] == "2026-05-21" and b["g_ttl"] == 2422.89
    assert app._parse_export_set(str(d / "nope")) is None


def test_sync_ro_exports_upserts_and_is_incremental(tmp_path, monkeypatch):
    app = _boot(tmp_path, monkeypatch)
    d = tmp_path / "exports"
    _write_set(d, "aaaa0001", "Kathy", "Fowler", "9255550101", "1HGCM66585A003562", 472.5)
    assert app.sync_ro_exports() == 1
    assert app.sync_ro_exports() == 0            # nothing new
    # closed re-export of the same doc: newer mtime → re-parsed, still one row
    _write_set(d, "aaaa0001", "Kathy", "Fowler", "9255550101", "1HGCM66585A003562", 1500.0, date_out=date(2026, 6, 9), ro_in=date(2026, 6, 9))
    os.utime(str(d / "aaaa0001.env"), (time.time() + 5, time.time() + 5))
    assert app.sync_ro_exports() == 1
    con = sqlite3.connect(str(tmp_path / "jobs.db"))
    rows = con.execute("SELECT doc_id, closed, g_ttl FROM ro_exports").fetchall()
    assert rows == [("aaaa0001", 1, 1500.0)]
    assert app._get_setting("ro_exports_synced_at", "0") != "0"


def test_sync_inbound_sms_inserts_only_inbound_and_paginates(tmp_path, monkeypatch):
    app = _boot(tmp_path, monkeypatch)
    pages = {
        "first": {"messages": [
            {"sid": "SM1", "direction": "inbound", "from": "+19255550101", "to": "+19256033934", "body": "Ok", "date_sent": "Wed, 03 Sep 2026 19:04:23 +0000"},
            {"sid": "SM2", "direction": "outbound-api", "from": "+19256033934", "to": "+19254215772", "body": "From +19255550101: Ok", "date_sent": "Wed, 03 Sep 2026 19:04:23 +0000"},
        ], "next_page_uri": "/2010-04-01/Accounts/AC/Messages.json?Page=1"},
        "second": {"messages": [
            {"sid": "SM3", "direction": "inbound", "from": "+19255550102", "to": "+19256033934", "body": "Hi", "date_sent": "Tue, 02 Sep 2026 22:51:00 +0000"},
        ], "next_page_uri": None},
    }
    calls = []
    def fetch(url):
        calls.append(url)
        return pages["second"] if "Page=1" in url else pages["first"]
    assert app.sync_inbound_sms(fetch=fetch) == 2
    assert app.sync_inbound_sms(fetch=fetch) == 0
    con = sqlite3.connect(str(tmp_path / "jobs.db"))
    rows = con.execute("SELECT sid, from_phone, body, date_sent FROM inbound_sms ORDER BY sid").fetchall()
    assert rows == [("SM1", "+19255550101", "Ok", 1788462263), ("SM3", "+19255550102", "Hi", 1788389460)]
    assert "DateSent%3E=" in calls[0] and calls[1].endswith("Page=1")
