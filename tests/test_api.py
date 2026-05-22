from __future__ import annotations

import json
import threading
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

from auto_reset_remaining.api import APIClient, parse_balance


class ParseBalanceTests(unittest.TestCase):
    def test_default_paths(self) -> None:
        cases = [
            (b'{"balance":0.42}', 0.42),
            (b'{"data":{"user":{"balance":"-0.002658"}}}', -0.002658),
            (b'{"quota":{"remaining":12.5}}', 12.5),
        ]
        for body, expected in cases:
            with self.subTest(body=body):
                self.assertEqual(parse_balance(body), expected)

    def test_configured_path(self) -> None:
        body = b'{"payload":{"usage":[{"left":"1.25"}]}}'
        self.assertEqual(parse_balance(body, "payload.usage.0.left"), 1.25)

    def test_missing_configured_path(self) -> None:
        with self.assertRaises(Exception):
            parse_balance(b'{"usage":123}', "data.balance")


class APIClientTests(unittest.TestCase):
    def test_query_balance(self) -> None:
        class Handler(BaseHTTPRequestHandler):
            def do_GET(self) -> None:  # noqa: N802
                self.server.seen_auth = self.headers.get("Authorization")  # type: ignore[attr-defined]
                self.send_response(200)
                self.end_headers()
                self.wfile.write(b'{"data":{"balance":0.25}}')

            def log_message(self, _fmt: str, *_args: object) -> None:
                return

        with server_context(Handler) as server:
            client = APIClient(
                rayplus_base_url=server.url,
                rayplus_api_key="sk-test",
                rayplus_email="user@example.com",
                rayplus_password="password",
                codex_base_url=server.url,
                balance_json_path="data.balance",
            )
            result = client.query_balance()
            self.assertEqual(result.balance, 0.25)
            self.assertEqual(server.seen_auth, "Bearer sk-test")

    def test_reset_quota_flow(self) -> None:
        class Handler(BaseHTTPRequestHandler):
            reset_calls = 0

            def do_POST(self) -> None:  # noqa: N802
                if self.path == "/api/v1/auth/login":
                    self.send_json({"code": 0, "data": {"access_token": "access-token", "expires_in": 3600}})
                    return
                if self.path == "/api/subscriptions/222/reset-quota":
                    self.server.reset_auth = self.headers.get("Authorization")  # type: ignore[attr-defined]
                    Handler.reset_calls += 1
                    self.send_json({"ok": True})
                    return
                self.send_response(404)
                self.end_headers()

            def do_GET(self) -> None:  # noqa: N802
                if self.path == "/api/subscriptions":
                    self.server.subscriptions_auth = self.headers.get("Authorization")  # type: ignore[attr-defined]
                    self.send_json(
                        {
                            "subscriptions": [
                                {"id": 111, "status": "active", "canReset": False},
                                {"id": 222, "status": "active", "canReset": True},
                            ]
                        }
                    )
                    return
                self.send_response(404)
                self.end_headers()

            def send_json(self, payload: object) -> None:
                body = json.dumps(payload).encode("utf-8")
                self.send_response(200)
                self.send_header("Content-Type", "application/json")
                self.send_header("Content-Length", str(len(body)))
                self.end_headers()
                self.wfile.write(body)

            def log_message(self, _fmt: str, *_args: object) -> None:
                return

        with server_context(Handler) as server:
            client = APIClient(
                rayplus_base_url=server.url,
                rayplus_api_key="sk-test",
                rayplus_email="user@example.com",
                rayplus_password="password",
                codex_base_url=server.url,
            )
            result = client.reset_quota()
            self.assertTrue(result.success)
            self.assertEqual(result.subscription_id, 222)
            self.assertEqual(result.http_status, 200)
            self.assertEqual(Handler.reset_calls, 1)
            self.assertEqual(server.subscriptions_auth, "Bearer access-token")
            self.assertEqual(server.reset_auth, "Bearer access-token")


class server_context:
    def __init__(self, handler: type[BaseHTTPRequestHandler]) -> None:
        self.server = ThreadingHTTPServer(("127.0.0.1", 0), handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)

    def __enter__(self) -> ThreadingHTTPServer:
        host, port = self.server.server_address
        self.server.url = f"http://{host}:{port}"  # type: ignore[attr-defined]
        self.thread.start()
        return self.server

    def __exit__(self, *_args: object) -> None:
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=5)
