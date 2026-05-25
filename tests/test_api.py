from __future__ import annotations

from pathlib import Path
from typing import Any
import json
import unittest
import urllib.request

from auto_reset_remaining.api import APIClient, APIError, parse_balance
from auto_reset_remaining.config import AppConfig, CodexConfig, LogsConfig, PollingConfig, RayPlusConfig, ResetConfig, SQLiteConfig


class FakeResponse:
    def __init__(self, status: int, payload: Any) -> None:
        self.status = status
        self._body = json.dumps(payload).encode("utf-8")

    def __enter__(self) -> "FakeResponse":
        return self

    def __exit__(self, *_args: object) -> None:
        return None

    def read(self) -> bytes:
        return self._body

    def getcode(self) -> int:
        return self.status


class FakeOpener:
    def __init__(self) -> None:
        self.requests: list[urllib.request.Request] = []

    def open(self, request: urllib.request.Request, timeout: float) -> FakeResponse:
        self.requests.append(request)
        url = request.full_url
        if url.endswith("/v1/usage"):
            return FakeResponse(200, {"data": {"balance": "3.5"}})
        if url.endswith("/api/v1/auth/login"):
            return FakeResponse(200, {"code": 0, "data": {"access_token": "token", "expires_in": 3600}})
        if url.endswith("/api/subscriptions"):
            return FakeResponse(200, {"subscriptions": [{"id": 42, "status": "active", "canReset": True}]})
        if url.endswith("/api/subscriptions/42/reset-quota"):
            return FakeResponse(200, {"ok": True})
        raise AssertionError(f"unexpected URL: {url}")


class APITests(unittest.TestCase):
    def test_parse_balance_paths(self) -> None:
        self.assertEqual(parse_balance({"data": {"balance": "1.25"}}), 1.25)
        self.assertEqual(parse_balance({"items": [{"quota": {"remaining": 2}}]}, "items.0.quota.remaining"), 2)
        with self.assertRaises(APIError):
            parse_balance({"data": {"balance": "abc"}})

    def test_client_query_and_reset(self) -> None:
        opener = FakeOpener()
        client = APIClient(_config(), opener=opener)

        balance = client.query_balance()
        reset = client.reset_quota()

        self.assertEqual(balance.balance, 3.5)
        self.assertEqual(reset.subscription_id, 42)
        self.assertTrue(reset.success)
        self.assertEqual(len(opener.requests), 4)


def _config() -> AppConfig:
    return AppConfig(
        path=Path("config.toml"),
        rayplus=RayPlusConfig(
            base_url="https://rayplus.example",
            api_key="sk-test",
            email="user@example.com",
            password="secret",
            user_agent="test-agent",
            balance_json_path="",
        ),
        codex=CodexConfig(base_url="https://codex.example", subscription_id=0),
        sqlite=SQLiteConfig(path=Path("data/test.sqlite3")),
        reset=ResetConfig(
            low_balance_threshold=0.5,
            auto_reset_enabled=False,
            manual_confirm_success_count=0,
            daily_max_reset_count=0,
            confirm_request_ttl_seconds=3600,
            cooldown_seconds=60,
            manual_confirm_time_range="",
        ),
        polling=PollingConfig(default_interval_seconds=10, balance_change_epsilon=0.000001),
        logs=LogsConfig(query_log_dir=Path("logs")),
    )


if __name__ == "__main__":
    unittest.main()
