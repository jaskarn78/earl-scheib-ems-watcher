"""Tiny dBase III writer for test fixtures (no memo support)."""
import struct
from datetime import date


def write_dbf(path, fields, records):
    """fields: list of (name, type, length, decimals); records: list of dicts."""
    header_len = 32 + 32 * len(fields) + 1
    record_len = 1 + sum(f[2] for f in fields)
    with open(path, "wb") as fh:
        fh.write(struct.pack("<BBBBIHH20x", 0x03, 26, 1, 1, len(records), header_len, record_len))
        for name, ftype, length, dec in fields:
            fh.write(struct.pack("<11sc4xBB14x", name.encode("ascii").ljust(11, b"\0"), ftype.encode("ascii"), length, dec))
        fh.write(b"\r")
        for rec in records:
            fh.write(b" ")
            for name, ftype, length, dec in fields:
                v = rec.get(name, "")
                if ftype == "N" or ftype == "F":
                    s = ("" if v in ("", None) else f"{float(v):.{dec}f}").rjust(length)
                elif ftype == "D":
                    s = (v.strftime("%Y%m%d") if isinstance(v, date) else (v or "")).ljust(length)
                elif ftype == "L":
                    s = "T" if v else "F"
                else:
                    s = str(v or "").ljust(length)[:length]
                fh.write(s.encode("latin-1")[:length].ljust(length, b" "))
        fh.write(b"\x1a")
