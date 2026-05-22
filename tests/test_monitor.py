from __future__ import annotations

import tempfile
import unittest
import urllib.parse
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from pathlib import Path

from auto_reset_remaining.api import BalanceResult, ResetResult
from auto_reset_remaining.config import Config
from auto_reset_remaining.monitor import Monitor, _hash_token
from auto_reset_remaining.query_logger import QueryLogger
from auto_reset_remaining.store import ConfirmToken, InvalidTokenError, ResetLog


class MonitorTests(unittest.TestCase):
    def test_low_balance_sends_one_email(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            env_path = write_env(tmp, "AUTO_RESET_ENABLED=false\nMANUAL_CONFIRM_SUCCESS_COUNT=0\n")
            api = FakeAPI(balance=0.4)
            mailer = FakeMailer()
            store = FakeStore()
            monitor = make_monitor(tmp, env_path, api, mailer, store)

            self.assertEqual(monitor.tick(), "low_balance_email_sent")
            self.assertEqual(monitor.tick(), "low_balance_email_pending")
            self.assertEqual(len(mailer.messages), 1)

    def test_confirm_third_manual_success_enables_auto_reset(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            env_path = write_env(tmp, "AUTO_RESET_ENABLED=false\nMANUAL_CONFIRM_SUCCESS_COUNT=2\n")
            api = FakeAPI(balance=0.4, reset_result=ResetResult(subscription_id=1716, http_status=200, success=True))
            mailer = FakeMailer()
            store = FakeStore()
            raw_token = "manual-confirm-token"
            store.create_confirm_token(_hash_token(raw_token), 0.4, datetime.now(UTC) + timedelta(hours=1))
            monitor = make_monitor(tmp, env_path, api, mailer, store, manual_count=2)

            result = monitor.confirm(raw_token)
            self.assertEqual(result.manual_confirm_success_count, 3)
            self.assertTrue(result.auto_reset_enabled)
            text = env_path.read_text(encoding="utf-8")
            self.assertIn("AUTO_RESET_ENABLED=true", text)
            self.assertIn("MANUAL_CONFIRM_SUCCESS_COUNT=3", text)

    def test_confirm_clears_pending_email(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            env_path = write_env(tmp, "AUTO_RESET_ENABLED=false\nMANUAL_CONFIRM_SUCCESS_COUNT=0\n")
            api = FakeAPI(balance=0.4, reset_result=ResetResult(subscription_id=1716, http_status=200, success=True))
            mailer = FakeMailer()
            store = FakeStore()
            monitor = make_monitor(tmp, env_path, api, mailer, store)

            self.assertEqual(monitor.tick(), "low_balance_email_sent")
            token = extract_token(mailer.messages[0])
            monitor.confirm(token)
            self.assertEqual(monitor.tick(), "low_balance_email_sent")
            self.assertEqual(len(mailer.messages), 2)

    def test_auto_mode_resets_when_balance_is_zero(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            env_path = write_env(tmp, "AUTO_RESET_ENABLED=true\nMANUAL_CONFIRM_SUCCESS_COUNT=3\n")
            api = FakeAPI(balance=0.0, reset_result=ResetResult(subscription_id=1716, http_status=200, success=True))
            mailer = FakeMailer()
            store = FakeStore()
            monitor = make_monitor(tmp, env_path, api, mailer, store, auto_enabled=True, cooldown=0)

            self.assertEqual(monitor.tick(), "auto_reset_success")
            self.assertEqual(api.reset_calls, 1)
            self.assertEqual(len(store.reset_logs), 1)
            self.assertEqual(store.reset_logs[0].mode, "auto")
            self.assertTrue(store.reset_logs[0].success)


def write_env(tmp: str, content: str) -> Path:
    path = Path(tmp) / ".env"
    path.write_text(content, encoding="utf-8")
    return path


def make_monitor(
    tmp: str,
    env_path: Path,
    api: "FakeAPI",
    mailer: "FakeMailer",
    store: "FakeStore",
    *,
    manual_count: int = 0,
    auto_enabled: bool = False,
    cooldown: float = 60,
) -> Monitor:
    cfg = Config(
        env_path=str(env_path),
        public_base_url="https://service.example.com",
        low_balance_threshold=0.5,
        confirm_token_ttl_seconds=3600,
        query_log_dir=str(Path(tmp) / "logs"),
        manual_confirm_success_count=manual_count,
        auto_reset_enabled=auto_enabled,
        reset_cooldown_seconds=cooldown,
    )
    return Monitor(cfg, api, mailer, store, QueryLogger(cfg.query_log_dir))


def extract_token(message: str) -> str:
    for part in message.split():
        if "/confirm-reset?" in part:
            parsed = urllib.parse.urlparse(part)
            return urllib.parse.parse_qs(parsed.query)["token"][0]
    raise AssertionError(f"message missing confirm URL: {message}")


class FakeAPI:
    def __init__(self, balance: float, reset_result: ResetResult | None = None) -> None:
        self.balance = balance
        self.reset_result = reset_result or ResetResult(subscription_id=1716, http_status=200, success=True)
        self.reset_calls = 0

    def query_balance(self) -> BalanceResult:
        return BalanceResult(balance=self.balance, raw=b"{}")

    def reset_quota(self) -> ResetResult:
        self.reset_calls += 1
        self.reset_result.success = True
        return self.reset_result


class FakeMailer:
    def __init__(self) -> None:
        self.messages: list[str] = []

    def send(self, subject: str, body: str) -> None:
        self.messages.append(subject + "\n" + body)


@dataclass
class FakeToken:
    id: int
    balance: float
    expires_at: datetime
    used: bool = False


class FakeStore:
    def __init__(self) -> None:
        self.tokens: dict[str, FakeToken] = {}
        self.reset_logs: list[ResetLog] = []
        self.next_id = 1

    def has_active_confirm_token(self) -> bool:
        now = datetime.now(UTC)
        return any(not token.used and token.expires_at > now for token in self.tokens.values())

    def create_confirm_token(self, token_hash: str, balance: float, expires_at: datetime) -> None:
        self.tokens[token_hash] = FakeToken(self.next_id, balance, expires_at)
        self.next_id += 1

    def delete_confirm_token(self, token_hash: str) -> None:
        self.tokens.pop(token_hash, None)

    def consume_confirm_token(self, token_hash: str) -> ConfirmToken:
        token = self.tokens.get(token_hash)
        if token is None or token.used or token.expires_at <= datetime.now(UTC):
            raise InvalidTokenError("confirm token is invalid, expired, or already used")
        token.used = True
        return ConfirmToken(id=token.id, balance=token.balance, expires_at=token.expires_at)

    def mark_confirm_token_reset(self, token_id: int, reset_log_id: int, manual_success_count_after: int) -> None:
        return None

    def log_reset(self, entry: ResetLog) -> int:
        self.reset_logs.append(entry)
        self.next_id += 1
        return self.next_id - 1
