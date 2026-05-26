from __future__ import annotations

import json
from pathlib import Path
import tempfile
import unittest
from datetime import datetime, timedelta

from auto_reset_remaining.api import APINetworkError, BalanceResult, ResetResult
from auto_reset_remaining.config import (
    AfterResetEmailPollingConfig,
    AppConfig,
    CodexConfig,
    LogsConfig,
    PollingConfig,
    RayPlusConfig,
    ResetConfig,
    SQLiteConfig,
    SleepPollingConfig,
    SubscriptionPollingConfig,
    SubscriptionTier,
)
from auto_reset_remaining.monitor import (
    BALANCE_QUERY_NETWORK_ERROR_STATUS,
    CONFIRM_RESULT_CANCELLED,
    CONFIRM_RESULT_CONFIRMED,
    Monitor,
    MonitorEvent,
    PollingState,
    select_poll_policy,
)
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


class FlakyAPI(FakeAPI):
    def __init__(self, outcomes: list[float | Exception]) -> None:
        super().__init__([])
        self.outcomes = outcomes

    def query_balance(self) -> BalanceResult:
        outcome = self.outcomes.pop(0)
        if isinstance(outcome, Exception):
            raise outcome
        return BalanceResult(balance=outcome, raw={})


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

    def test_balance_query_network_error_is_logged_without_error_event(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            config = _config(Path(tmp), auto_reset_enabled=False)
            store = SQLiteStore(config.sqlite.path)
            store.init()
            api = FlakyAPI([APINetworkError("timed out")])
            events: list[MonitorEvent] = []
            monitor = Monitor(
                config,
                api,  # type: ignore[arg-type]
                store,
                QueryLogger(config.logs.query_log_dir),
                event_callback=events.append,
            )

            status = monitor.tick_once()
            logs = list(config.logs.query_log_dir.glob("query-*.jsonl"))
            payload = json.loads(logs[0].read_text(encoding="utf-8").strip())

            self.assertEqual(status, BALANCE_QUERY_NETWORK_ERROR_STATUS)
            self.assertEqual(payload["status"], BALANCE_QUERY_NETWORK_ERROR_STATUS)
            self.assertIn("timed out", payload["error"])
            self.assertFalse(any(event.kind == "error" for event in events))
            self.assertTrue(any(event.status == BALANCE_QUERY_NETWORK_ERROR_STATUS for event in events))
            store.close()

    def test_balance_query_recovers_after_network_error_on_next_tick(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            config = _config(Path(tmp), auto_reset_enabled=False)
            store = SQLiteStore(config.sqlite.path)
            store.init()
            api = FlakyAPI([APINetworkError("timed out"), 1.0])
            events: list[MonitorEvent] = []
            monitor = Monitor(
                config,
                api,  # type: ignore[arg-type]
                store,
                QueryLogger(config.logs.query_log_dir),
                event_callback=events.append,
            )

            first_status = monitor.tick_once()
            second_status = monitor.tick_once()
            snapshot = monitor.snapshot()

            self.assertEqual(first_status, BALANCE_QUERY_NETWORK_ERROR_STATUS)
            self.assertEqual(second_status, "ok")
            self.assertEqual(snapshot.last_balance, 1.0)
            self.assertIsNone(snapshot.last_error)
            self.assertTrue(any(event.status == "ok" for event in events))
            store.close()

    def test_subscription_polling_uses_balance_tiers(self) -> None:
        config = _polling_config()
        now = datetime.now()

        cases = [
            (76, 60),
            (50, 30),
            (25, 10),
            (10, 3),
            (0, 1),
        ]
        for balance, expected_interval in cases:
            with self.subTest(balance=balance):
                policy = select_poll_policy(
                    config,
                    PollingState(
                        has_last_balance=True,
                        last_balance=balance,
                        last_balance_changed_at=now,
                        force_fast_until_balance_change=False,
                    ),
                    now,
                )
                self.assertEqual(policy.name, "subscription")
                self.assertEqual(policy.interval_seconds, expected_interval)

    def test_polling_defaults_before_first_balance_sample(self) -> None:
        config = _polling_config()
        policy = select_poll_policy(
            config,
            PollingState(
                has_last_balance=False,
                last_balance=0,
                last_balance_changed_at=None,
                force_fast_until_balance_change=False,
            ),
            datetime.now(),
        )

        self.assertEqual(policy.name, "default")
        self.assertEqual(policy.interval_seconds, 10)

    def test_sleep_polling_has_priority_over_subscription_tiers(self) -> None:
        config = _polling_config(
            sleep=SleepPollingConfig(enabled=True, unchanged_for_seconds=600, interval_seconds=120),
        )
        now = datetime.now()
        policy = select_poll_policy(
            config,
            PollingState(
                has_last_balance=True,
                last_balance=76,
                last_balance_changed_at=now - timedelta(minutes=20),
                force_fast_until_balance_change=False,
            ),
            now,
        )

        self.assertEqual(policy.name, "sleep")
        self.assertEqual(policy.interval_seconds, 120)

    def test_after_reset_email_polling_has_highest_priority(self) -> None:
        config = _polling_config(
            sleep=SleepPollingConfig(enabled=True, unchanged_for_seconds=600, interval_seconds=120),
            after_reset_email=AfterResetEmailPollingConfig(enabled=True, interval_seconds=15),
        )
        now = datetime.now()
        policy = select_poll_policy(
            config,
            PollingState(
                has_last_balance=True,
                last_balance=76,
                last_balance_changed_at=now - timedelta(minutes=20),
                force_fast_until_balance_change=True,
            ),
            now,
        )

        self.assertEqual(policy.name, "after_reset_email")
        self.assertEqual(policy.interval_seconds, 15)

    def test_run_loop_sleeps_using_dynamic_interval(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            config = _config(Path(tmp), auto_reset_enabled=False)
            store = SQLiteStore(config.sqlite.path)
            store.init()
            api = FakeAPI([76])
            sleeps: list[float] = []
            monitor = Monitor(
                config,
                api,  # type: ignore[arg-type]
                store,
                QueryLogger(config.logs.query_log_dir),
                sleeper=lambda seconds: sleeps.append(seconds) or monitor.stop(),
            )

            monitor._run_loop()

            self.assertEqual(sleeps, [60])
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
        polling=_polling_config(),
        logs=LogsConfig(query_log_dir=root / "logs"),
    )


def _polling_config(
    *,
    sleep: SleepPollingConfig | None = None,
    after_reset_email: AfterResetEmailPollingConfig | None = None,
) -> PollingConfig:
    return PollingConfig(
        default_interval_seconds=10,
        balance_change_epsilon=0.000001,
        subscription=SubscriptionPollingConfig(
            enabled=True,
            quota=100,
            tiers=(
                SubscriptionTier(0.70, 60),
                SubscriptionTier(0.40, 30),
                SubscriptionTier(0.20, 10),
                SubscriptionTier(0.000001, 3),
                SubscriptionTier(0, 1),
            ),
        ),
        sleep=sleep or SleepPollingConfig(enabled=False, unchanged_for_seconds=600, interval_seconds=60),
        after_reset_email=after_reset_email or AfterResetEmailPollingConfig(enabled=False, interval_seconds=60),
    )


if __name__ == "__main__":
    unittest.main()
