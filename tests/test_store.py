from __future__ import annotations

from datetime import date, timedelta
from pathlib import Path
import json
import tempfile
import unittest

from auto_reset_remaining.query_log import QueryLogEntry, QueryLogger
from auto_reset_remaining.store import InvalidConfirmRequest, SQLiteStore, utc_now


class StoreTests(unittest.TestCase):
    def test_confirm_request_lifecycle(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            store = SQLiteStore(Path(tmp) / "app.sqlite3")
            store.init()
            expires_at = utc_now() + timedelta(minutes=5)

            request_id = store.create_confirm_request("hash-1", 0.25, expires_at)

            self.assertTrue(store.has_pending_confirm_request())
            pending = store.get_latest_pending_confirm_request()
            self.assertIsNotNone(pending)
            consumed = store.consume_confirm_request("hash-1")
            log_id = store.log_reset("manual", consumed.balance, 123, True)
            store.mark_confirm_request_reset(request_id, log_id, 1)

            self.assertFalse(store.has_pending_confirm_request())
            self.assertEqual(store.recent_reset_logs()[0].subscription_id, 123)
            store.close()

    def test_expired_confirm_request_is_rejected(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            store = SQLiteStore(Path(tmp) / "app.sqlite3")
            store.init()
            store.create_confirm_request("hash-1", 0.25, utc_now() - timedelta(seconds=1))

            with self.assertRaises(InvalidConfirmRequest):
                store.consume_confirm_request("hash-1")
            self.assertFalse(store.has_pending_confirm_request())
            store.close()

    def test_pending_confirm_request_can_be_manual_cancelled(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            store = SQLiteStore(Path(tmp) / "app.sqlite3")
            store.init()
            store.create_confirm_request("hash-1", 0.25, utc_now() + timedelta(minutes=5))

            cancelled = store.cancel_pending_confirm_requests("manual_cancelled")

            self.assertEqual(cancelled, 1)
            self.assertFalse(store.has_pending_confirm_request())
            with self.assertRaises(InvalidConfirmRequest):
                store.consume_confirm_request("hash-1")
            store.close()

    def test_confirm_prompt_suppression_lifecycle(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            store = SQLiteStore(Path(tmp) / "app.sqlite3")
            store.init()

            self.assertFalse(store.is_confirm_prompt_suppressed())
            store.suppress_confirm_prompt("low_balance", 0.1)
            self.assertTrue(store.is_confirm_prompt_suppressed())
            store.clear_confirm_prompt_suppression()
            self.assertFalse(store.is_confirm_prompt_suppressed())
            store.close()

    def test_daily_reset_limit_state(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            store = SQLiteStore(Path(tmp) / "app.sqlite3")
            store.init()
            today = date.today()
            store.log_reset("auto", 0, 456, True)

            state = store.get_daily_reset_limit_state(today, 1)
            self.assertEqual(state.reset_count, 1)
            self.assertFalse(state.balance_query_paused)

            store.pause_balance_queries_for_day(today)
            paused = store.get_daily_reset_limit_state(today, 1)
            self.assertTrue(paused.balance_query_paused)
            store.close()


class QueryLoggerTests(unittest.TestCase):
    def test_log_writes_jsonl(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            logger = QueryLogger(Path(tmp))
            path = logger.log(QueryLogEntry(time=utc_now(), status="ok", duration_ms=12, balance=1.5))

            payload = json.loads(path.read_text(encoding="utf-8").strip())

        self.assertEqual(payload["status"], "ok")
        self.assertEqual(payload["balance"], 1.5)


if __name__ == "__main__":
    unittest.main()
