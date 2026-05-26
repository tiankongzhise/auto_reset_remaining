from __future__ import annotations

from pathlib import Path
import tempfile
import unittest

import tomlkit

from auto_reset_remaining.config import (
    CONFIG_PATH,
    ConfigError,
    is_time_in_manual_confirm_range,
    load_config,
    parse_duration,
    parse_manual_confirm_range,
    update_runtime_state,
)


class ConfigTests(unittest.TestCase):
    def test_parse_duration(self) -> None:
        self.assertEqual(parse_duration("500ms"), 0.5)
        self.assertEqual(parse_duration("10s"), 10)
        self.assertEqual(parse_duration("2m"), 120)
        self.assertEqual(parse_duration("1h"), 3600)
        with self.assertRaises(ConfigError):
            parse_duration("1d")

    def test_manual_confirm_range(self) -> None:
        self.assertEqual(parse_manual_confirm_range("22:00-09:00"), (22 * 60, 9 * 60))
        self.assertTrue(is_time_in_manual_confirm_range("22:00-09:00", 23, 30))
        self.assertTrue(is_time_in_manual_confirm_range("22:00-09:00", 8, 59))
        self.assertFalse(is_time_in_manual_confirm_range("22:00-09:00", 12, 0))
        with self.assertRaises(ConfigError):
            parse_manual_confirm_range("09:00-09:00")

    def test_load_valid_config(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "config.toml"
            path.write_text(_valid_config_text(), encoding="utf-8")

            config = load_config(path)

        self.assertEqual(config.rayplus.api_key, "sk-test")
        self.assertEqual(config.codex.subscription_id, 0)
        self.assertEqual(config.sqlite.path, Path("data/test.sqlite3"))
        self.assertEqual(config.polling.default_interval_seconds, 10)
        self.assertTrue(config.polling.subscription.enabled)
        self.assertEqual(config.polling.subscription.quota, 100)
        self.assertEqual(config.polling.subscription.tiers[0].interval_seconds, 60)

    def test_load_custom_polling_strategy(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "config.toml"
            path.write_text(
                _valid_config_text()
                + """
[polling.sleep]
enabled = true
unchanged_for = "5m"
interval = "2m"

[polling.after_reset_email]
enabled = true
interval = "15s"
""",
                encoding="utf-8",
            )

            config = load_config(path)

        self.assertTrue(config.polling.sleep.enabled)
        self.assertEqual(config.polling.sleep.unchanged_for_seconds, 300)
        self.assertEqual(config.polling.sleep.interval_seconds, 120)
        self.assertTrue(config.polling.after_reset_email.enabled)
        self.assertEqual(config.polling.after_reset_email.interval_seconds, 15)

    def test_subscription_section_can_reuse_default_tiers(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "config.toml"
            path.write_text(
                _valid_config_text_without_subscription_tiers()
                + """
[polling.subscription]
enabled = true
quota = 200

[logs]
query_log_dir = "logs"
""",
                encoding="utf-8",
            )

            config = load_config(path)

        self.assertTrue(config.polling.subscription.enabled)
        self.assertEqual(config.polling.subscription.quota, 200)
        self.assertEqual(config.polling.subscription.tiers[0].interval_seconds, 60)

    def test_placeholder_config_is_rejected(self) -> None:
        with self.assertRaises(ConfigError):
            load_config(CONFIG_PATH.with_name("config.example.toml"))

    def test_update_runtime_state(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "config.toml"
            path.write_text(_valid_config_text(), encoding="utf-8")

            update_runtime_state(path, 3, True)
            document = tomlkit.parse(path.read_text(encoding="utf-8"))

        self.assertEqual(document["reset"]["manual_confirm_success_count"], 3)
        self.assertTrue(document["reset"]["auto_reset_enabled"])


def _valid_config_text() -> str:
    return _valid_config_text_without_subscription_tiers() + """
[polling.subscription]
enabled = true
quota = 100

[[polling.subscription.tiers]]
min_ratio = 0.70
interval = "1m"

[[polling.subscription.tiers]]
min_ratio = 0.40
interval = "30s"

[[polling.subscription.tiers]]
min_ratio = 0.20
interval = "10s"

[[polling.subscription.tiers]]
min_ratio = 0.000001
interval = "3s"

[[polling.subscription.tiers]]
min_ratio = 0
interval = "1s"

[logs]
query_log_dir = "logs"
"""


def _valid_config_text_without_subscription_tiers() -> str:
    return """
[rayplus]
base_url = "https://rayplus.site"
api_key = "sk-test"
email = "user@example.com"
password = "secret"
user_agent = "auto-reset-remaining-python/0.1"
balance_json_path = ""

[codex]
base_url = "https://codex.rayplus.site"
subscription_id = 0

[sqlite]
path = "data/test.sqlite3"

[reset]
low_balance_threshold = 0.5
auto_reset_enabled = false
manual_confirm_success_count = 0
daily_max_reset_count = 0
confirm_request_ttl = "24h"
cooldown = "1m"
manual_confirm_time_range = "22:00-09:00"

[polling]
default_interval = "10s"
balance_change_epsilon = 0.000001
"""


if __name__ == "__main__":
    unittest.main()
