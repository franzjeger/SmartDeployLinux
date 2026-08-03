"""Behavior tests: a real threaded HTTP server exercised through the
SDK's real urllib transport. Standard library only."""

from __future__ import annotations

import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, HTTPServer
from typing import Callable, Optional

from deployserver_sdk import ApiError, DeployClient, is_forbidden, is_not_found

# Per-test response hook and last-request record, shared with the handler.
Responder = Callable[[BaseHTTPRequestHandler], None]
_respond: Responder = lambda h: _send(h, 200, "{}")
_captured: dict[str, str] = {}


def _send(handler: BaseHTTPRequestHandler, status: int, body: str, ctype: str = "application/json") -> None:
    payload = body.encode("utf-8")
    handler.send_response(status)
    handler.send_header("Content-Type", ctype)
    handler.send_header("Content-Length", str(len(payload)))
    handler.end_headers()
    handler.wfile.write(payload)


class _Handler(BaseHTTPRequestHandler):
    def _handle(self) -> None:
        length = int(self.headers.get("Content-Length", "0") or "0")
        raw = self.rfile.read(length) if length else b""
        path, _, query = self.path.partition("?")
        _captured.clear()
        _captured.update(
            method=self.command,
            path=path,
            query=query,
            auth=self.headers.get("Authorization", ""),
            accept=self.headers.get("Accept", ""),
            ctype=self.headers.get("Content-Type", ""),
            body=raw.decode("utf-8"),
        )
        _respond(self)

    do_GET = _handle
    do_POST = _handle
    do_PUT = _handle
    do_PATCH = _handle
    do_DELETE = _handle

    def log_message(self, *args: object) -> None:  # silence
        pass


_server: Optional[HTTPServer] = None
_base_url = ""


def setUpModule() -> None:
    global _server, _base_url
    _server = HTTPServer(("127.0.0.1", 0), _Handler)
    threading.Thread(target=_server.serve_forever, daemon=True).start()
    _base_url = f"http://127.0.0.1:{_server.server_address[1]}"


def tearDownModule() -> None:
    if _server is not None:
        _server.shutdown()


def _set(responder: Responder) -> None:
    global _respond
    _respond = responder


def _client(token: Optional[str] = "tok-123") -> DeployClient:
    return DeployClient(_base_url, token=token)


class TestBehavior(unittest.TestCase):
    def test_rejects_empty_base_url(self) -> None:
        with self.assertRaises(ValueError):
            DeployClient("")

    def test_trims_trailing_slash(self) -> None:
        _set(lambda h: _send(h, 200, "[]"))
        DeployClient(_base_url + "/", token="t").list_machines()
        self.assertEqual(_captured["path"], "/api/v1/machines")

    def test_list_machines_shape(self) -> None:
        _set(lambda h: _send(h, 200, '[{"ID":"m1","AssetTag":"A-1","CreatedAt":"2026-01-02T03:04:05Z"}]'))
        got = _client().list_machines()
        self.assertEqual(_captured["method"], "GET")
        self.assertEqual(_captured["path"], "/api/v1/machines")
        self.assertEqual(_captured["auth"], "Bearer tok-123")
        self.assertEqual(_captured["accept"], "application/json")
        self.assertEqual(got[0]["ID"], "m1")
        self.assertEqual(got[0]["AssetTag"], "A-1")

    def test_path_param_is_percent_encoded(self) -> None:
        _set(lambda h: _send(h, 200, '{"ID":"weird/id"}'))
        _client().get_machine("weird/id")
        self.assertEqual(_captured["path"], "/api/v1/machines/weird%2Fid")

    def test_create_machine_sends_json_body(self) -> None:
        _set(lambda h: _send(h, 201, '{"ID":"new"}'))
        out = _client().create_machine({"asset_tag": "asset-9"})
        self.assertEqual(_captured["method"], "POST")
        self.assertEqual(_captured["ctype"], "application/json")
        self.assertEqual(json.loads(_captured["body"]), {"asset_tag": "asset-9"})
        self.assertEqual(out["ID"], "new")

    def test_404_classified(self) -> None:
        _set(lambda h: _send(h, 404, '{"error":"machine not found"}'))
        with self.assertRaises(ApiError) as ctx:
            _client().get_machine("nope")
        self.assertTrue(is_not_found(ctx.exception))
        self.assertFalse(is_forbidden(ctx.exception))
        self.assertIn("machine not found", str(ctx.exception))

    def test_403_classified(self) -> None:
        _set(lambda h: _send(h, 403, '{"error":"missing permission machines.write"}'))
        with self.assertRaises(ApiError) as ctx:
            _client().delete_machine("m1")
        self.assertTrue(is_forbidden(ctx.exception))

    def test_list_jobs_query_params(self) -> None:
        _set(lambda h: _send(h, 200, "[]"))
        _client().list_jobs(state="running", machine_id="m1", limit=25)
        q = _captured["query"]
        for want in ("state=running", "machine=m1", "limit=25"):
            self.assertIn(want, q)

    def test_bulk_deploy_unwraps_results(self) -> None:
        _set(lambda h: _send(h, 200, '{"results":[{"machine_id":"m1","code":"A1B2C3"},{"machine_id":"m2","error":"no route"}]}'))
        res = _client().bulk_deploy({"machine_ids": ["m1", "m2"], "profile_id": "p1"})
        self.assertEqual(len(res), 2)
        self.assertEqual(res[0]["code"], "A1B2C3")
        self.assertEqual(res[1]["error"], "no route")

    def test_report_jobs_csv_returns_raw_text(self) -> None:
        csv = "id,state\nj1,completed\nj2,failed\n"
        _set(lambda h: _send(h, 200, csv, ctype="text/csv"))
        got = _client().report_jobs_csv("2026-01-01T00:00:00Z")
        self.assertEqual(got, csv)
        self.assertIn("since=", _captured["query"])

    def test_no_auth_header_when_token_none(self) -> None:
        _set(lambda h: _send(h, 200, '{"issuer":"x","client_id":"y","dev_mode":true}'))
        DeployClient(_base_url, token=None).auth_config()
        self.assertEqual(_captured["auth"], "")

    def test_non_json_error_falls_back_to_raw(self) -> None:
        _set(lambda h: _send(h, 502, "upstream exploded", ctype="text/plain"))
        with self.assertRaises(ApiError) as ctx:
            _client().delete_machine("m1")
        self.assertIn("upstream exploded", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
