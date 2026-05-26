from __future__ import annotations

import unittest

import tomlkit

from auto_reset_remaining.config import parse_config
from auto_reset_remaining.config_ui import _document_from_variables


class Variable:
    def __init__(self, value: str) -> None:
        self.value = value

    def get(self) -> str:
        return self.value


class ConfigUITests(unittest.TestCase):
    def test_nested_polling_fields_and_tiers_are_written(self) -> None:
        document = tomlkit.parse(_valid_config_text())
        variables = {
            ("polling", "default_interval"): Variable("8s"),
            ("polling", "balance_change_epsilon"): Variable("0.00001"),
            ("polling", "subscription", "enabled"): Variable("true"),
            ("polling", "subscription", "quota"): Variable("200"),
            ("polling", "sleep", "enabled"): Variable("true"),
            ("polling", "sleep", "unchanged_for"): Variable("5m"),
            ("polling", "sleep", "interval"): Variable("2m"),
            ("polling", "after_reset_email", "enabled"): Variable("true"),
            ("polling", "after_reset_email", "interval"): Variable("15s"),
        }
        tier_variables = [
            (Variable("0.80"), Variable("2m")),
            (Variable("0.20"), Variable("10s")),
        ]

        updated = _document_from_variables(document, variables, tier_variables)
        config = parse_config(updated)

        self.assertEqual(updated["polling"]["default_interval"], "8s")
        self.assertEqual(updated["polling"]["subscription"]["quota"], 200.0)
        self.assertEqual(len(updated["polling"]["subscription"]["tiers"]), 2)
        self.assertEqual(config.polling.default_interval_seconds, 8)
        self.assertTrue(config.polling.subscription.enabled)
        self.assertEqual(config.polling.subscription.quota, 200)
        self.assertEqual(config.polling.subscription.tiers[0].min_ratio, 0.80)
        self.assertEqual(config.polling.subscription.tiers[0].interval_seconds, 120)
        self.assertTrue(config.polling.sleep.enabled)
        self.assertEqual(config.polling.sleep.unchanged_for_seconds, 300)
        self.assertEqual(config.polling.sleep.interval_seconds, 120)
        self.assertTrue(config.polling.after_reset_email.enabled)
        self.assertEqual(config.polling.after_reset_email.interval_seconds, 15)


def _valid_config_text() -> str:
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
manual_confirm_time_range = ""

[polling]
default_interval = "10s"
balance_change_epsilon = 0.000001

[polling.subscription]
enabled = true
quota = 100

[[polling.subscription.tiers]]
min_ratio = 0.70
interval = "1m"

[polling.sleep]
enabled = false
unchanged_for = "10m"
interval = "1m"

[polling.after_reset_email]
enabled = false
interval = "1m"

[logs]
query_log_dir = "logs"
"""


if __name__ == "__main__":
    unittest.main()
