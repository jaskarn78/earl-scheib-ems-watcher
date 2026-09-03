import json
from urllib.request import Request, urlopen

from tests.conftest import sign


def _post(qs, path, body=b""):
    req = Request(f"{qs['base_url']}{path}", data=body, method="POST",
                  headers={"X-EMS-Signature": sign(qs["secret"], body), "Content-Type": "application/json"})
    with urlopen(req, timeout=5) as resp:
        return resp.status, json.loads(resp.read().decode("utf-8"))


def test_insights_sync_route(queue_server, monkeypatch, tmp_path):
    import app
    monkeypatch.setattr(app, "EXPORTS_DIR", str(tmp_path / "empty-exports"))
    monkeypatch.setattr(app, "sync_inbound_sms", lambda fetch=None: 0)
    status, body = _post(queue_server, "/earlscheibconcord/insights/sync")
    assert status == 200 and body == {"ro_exports": 0, "inbound_sms": 0}


def _get(qs, path):
    req = Request(f"{qs['base_url']}{path}", headers={"X-EMS-Signature": sign(qs["secret"], b"")})
    with urlopen(req, timeout=5) as resp:
        return resp.status, json.loads(resp.read().decode("utf-8"))


def test_insights_get_routes(queue_server):
    status, body = _get(queue_server, "/earlscheibconcord/insights?days=90")
    assert status == 200 and body["period"]["days"] == 90 and body["funnel"][0]["label"] == "Estimates"
    status, body = _get(queue_server, "/earlscheibconcord/insights?days=ytd")
    assert status == 200 and body["period"]["from"].endswith("-01-01T00:00:00")
    try:
        _get(queue_server, "/earlscheibconcord/customer?key=nope%7Cnope")
        assert False, "expected 404"
    except Exception as exc:  # HTTPError
        assert getattr(exc, "code", None) == 404
