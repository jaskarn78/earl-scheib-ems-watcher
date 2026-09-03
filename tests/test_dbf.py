import importlib
from datetime import date

from tests.dbfw import write_dbf


def _app():
    import app
    return importlib.reload(app)


def test_dbf_read_types_and_blanks(tmp_path):
    p = tmp_path / "t.ttl"
    write_dbf(p, [("NAME", "C", 12, 0), ("G_TTL_AMT", "N", 10, 2), ("DATE_OUT", "D", 8, 0), ("FLAG", "L", 1, 0)],
              [{"NAME": "Lucy Loluo", "G_TTL_AMT": 2422.89, "DATE_OUT": date(2026, 5, 21), "FLAG": True},
               {"NAME": "", "G_TTL_AMT": "", "DATE_OUT": "", "FLAG": False}])
    rows = _app()._dbf_read(str(p))
    assert rows == [
        {"NAME": "Lucy Loluo", "G_TTL_AMT": 2422.89, "DATE_OUT": "2026-05-21", "FLAG": True},
        {"NAME": "", "G_TTL_AMT": 0.0, "DATE_OUT": "", "FLAG": False},
    ]


def test_dbf_read_skips_deleted_and_tolerates_missing(tmp_path):
    p = tmp_path / "d.ad1"
    write_dbf(p, [("OWNR_FN", "C", 5, 0)], [{"OWNR_FN": "A"}, {"OWNR_FN": "B"}])
    raw = bytearray(p.read_bytes())
    header_len = int.from_bytes(raw[8:10], "little")
    raw[header_len] = 0x2A  # mark first record deleted
    p.write_bytes(bytes(raw))
    app = _app()
    assert [r["OWNR_FN"] for r in app._dbf_read(str(p))] == ["B"]
    assert app._dbf_read(str(tmp_path / "missing.ad1")) == []
