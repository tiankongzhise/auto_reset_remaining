from __future__ import annotations

from pathlib import Path
import tempfile
import unittest

from auto_reset_remaining.api import BalanceResult, ResetResult
from auto_reset_remaining.config import (
    AppConfig,
    CodexConfig,
    LogsConfig,
    PollingConfig,
    RayPlusConfig,
    ResetConfig,
    SQLiteConfig,
)
from auto_reset_remaining.monitor import CONFIRM_RESULT_CANCELLED, CONFIRM_RESULT_CONFIRMED, Monitor, MonitorEvent
from auto_reset_remaining.query_log import QueryLogger
from auto_reset_remaining.store import SQLiteStore


class FakeAPI:
    def __init__(self, balances: list[float]) -> None:
        self.balances = balances
        self.reset_calls = 0

    def query_balance(self) -> BalanceResult:
        return BalanceResult(balance=self.balances.pop(0), raw={})

    def reset_quota(self) -> ResetResult:
        self.reset_calls += 1
        return ResetResult(subscription_id=99, http_status=200, response_summary="{}", success=True)


class MonitorTests(unittest.TestCase):
    def test_low_balance_manual_confirm_resets(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            config = _config(Path(tmp), auto_reset_enabled=False)
            store = SQLiteStore(config.sqlite.path)
            store.init()
            api = FakeAPI([0.1])
            events: list[MonitorEvent] = []
            monitor = Monitor(
                config,
                api,  # type: ignore[arg-type]
                store,
                QueryLogger(config.logs.query_log_dir),
                confirm_callback=lambda _balance, _expires_at, _reason: CONFIRM_RESULT_CONFIRMED,
                event_callback=events.append,
            )

            status = monitor.tick_once()

            self.assertEqual(status, "manual_reset_success")
            self.assertEqual(api.reset_calls, 1)
            self.assertEqual(store.recent_reset_logs()[0].mode, "manual")
            self.assertTrue(any(event.status == "manual_reset_success" for event in events))
            store.close()

    def test_low_balance_manual_cancel_suppresses_followup_prompt(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            config = _config(Path(tmp), auto_reset_enabled=False)
            store = SQLiteStore(config.sqlite.path)
            store.init()
            api = FakeAPI([0.1, 0.1])
            callback_count = 0

            def cancel_callback(_balance: float, _expires_at: object, _reason: str) -> str:
                nonlocal callback_count
                callback_count += 1
                return CONFIRM_RESULT_CANCELLED

            monitor = Monitor(
                config,
                api,  # type: ignore[arg-type]
                store,
                QueryLogger(config.logs.query_log_dir),
                confirm_callback=cancel_callback,
            )

            first_status = monitor.tick_once()
            second_status = monitor.tick_once()

            self.assertEqual(first_status, "manual_confirm_cancelled")
            self.assertEqual(second_status, "low_balance_manual_cancelled")
            self.assertEqual(callback_count, 1)
            self.assertEqual(api.reset_calls, 0)
            self.assertFalse(store.has_pending_confirm_request())
            self.assertTrue(store.is_confirm_prompt_suppressed())
            store.close()

    def test_auto_reset_when_enabled_and_balance_zero(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            config = _config(Path(tmp), auto_reset_enabled=True)
            store = SQLiteStore(config.sqlite.path)
            store.init()
            api = FakeAPI([0])
            monitor = Monitor(config, api, store, QueryLogger(config.logs.query_log_dir))  # type: ignore[arg-type]

            status = monitor.tick_once()

            self.assertEqual(status, "auto_reset_success")
            self.assertEqual(api.reset_calls, 1)
            store.close()

    def test_manual_reset_success_clears_prompt_suppression(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            config = _config(Path(tmp), auto_reset_enabled=False)
            store = SQLiteStore(config.sqlite.path)
            store.init()
            store.suppress_confirm_prompt("low_balance", 0.1)
            api = FakeAPI([0.1])
            monitor = Monitor(config, api, store, QueryLogger(config.logs.query_log_dir))  # type: ignore[arg-type]

            monitor.manual_reset(0.1)

            self.assertFalse(store.is_confirm_prompt_suppressed())
            store.close()

    def test_balance_recovery_clears_prompt_suppression(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            config = _config(Path(tmp), auto_reset_enabled=False)
            store = SQLiteStore(config.sqlite.path)
            store.init()
            store.suppress_confirm_prompt("low_balance", 0.1)
            api = FakeAPI([1.0])
            monitor = Monitor(config, api, store, QueryLogger(config.logs.query_log_dir))  # type: ignore[arg-type]

            status = monitor.tick_once()

            self.assertEqual(status, "ok")
            self.assertFalse(store.is_confirm_prompt_suppressed())
            store.close()

    def test_daily_limit_pauses_when_zero(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            config = _config(Path(tmp), auto_reset_enabled=True, daily_max_reset_count=1)
            store = SQLiteStore(config.sqlite.path)
            store.init()
            store.log_reset("auto", 0, 99, True)
            api = FakeAPI([0])
            monitor = Monitor(config, api, store, QueryLogger(config.logs.query_log_dir))  # type: ignore[arg-type]

            status = monitor.tick_once()

            self.assertEqual(status, "balance_query_paused")
            self.assertEqual(api.reset_calls, 0)
            store.close()


def _config(
    root: Path,
    auto_reset_enabled: bool,
    daily_max_reset_count: int = 0,
) -> AppConfig:
    config_path = root / "config.toml"
    config_path.write_text(
        """
[reset]
manual_confirm_success_count = 0
auto_reset_enabled = false
""",
        encoding="utf-8",
    )
    return AppConfig(
        path=config_path,
        rayplus=RayPlusConfig(
            base_url="https://rayplus.example",
            api_key="sk-test",
            email="user@example.com",
            password="secret",
            user_agent="test-agent",
            balance_json_path="",
        ),
        codex=CodexConfig(base_url="https://codex.example", subscription_id=0),
        sqlite=SQLiteConfig(path=root / "app.sqlite3"),
        reset=ResetConfig(
            low_balance_threshold=0.5,
            auto_reset_enabled=auto_reset_enabled,
            manual_confirm_success_count=0,
            daily_max_reset_count=daily_max_reset_count,
            confirm_request_ttl_seconds=3600,
            cooldown_seconds=60,
            manual_confirm_time_range="",
        ),
        polling=PollingConfig(default_interval_seconds=10, balance_change_epsilon=0.000001),
        logs=LogsConfig(query_log_dir=root / "logs"),
    )


if __name__ == "__main__":
    unittest.main()
