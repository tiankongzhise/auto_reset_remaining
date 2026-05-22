from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from auto_reset_remaining.config import load_config
from auto_reset_remaining.envfile import update_values


class ConfigEnvFileTests(unittest.TestCase):
    def test_load_pg_fields(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            env_path = Path(tmp) / ".env"
            env_path.write_text(
                "\n".join(
                    [
                        "RAYPLUS_API_KEY=sk-test",
                        "RAYPLUS_EMAIL=user@example.com",
                        "RAYPLUS_PASSWORD=password",
                        "PUBLIC_BASE_URL=https://service.example.com",
                        "SMTP_HOST=smtp.example.com",
                        "SMTP_FROM=sender@example.com",
                        "SMTP_TO=receiver@example.com",
                        "pg_host=localhost",
                        "pg_port=15432",
                        'pg_user="pg user"',
                        'pg_password="pg password"',
                        "pg_database=auto_reset",
                        "pg_sslmode=require",
                        "MANUAL_CONFIRM_SUCCESS_COUNT=2",
                    ]
                ),
                encoding="utf-8",
            )
            cfg = load_config(str(env_path))
            cfg.validate()
            self.assertEqual(cfg.pg.host, "localhost")
            self.assertEqual(cfg.pg.port, 15432)
            self.assertEqual(cfg.pg.user, "pg user")
            self.assertEqual(cfg.postgres_kwargs()["dbname"], "auto_reset")
            self.assertNotIn("POSTGRES_DSN", cfg.postgres_kwargs())

    def test_update_values_preserves_and_appends(self) -> None:
        with tempfile.TemporaryDirectory() as tmp:
            env_path = Path(tmp) / ".env"
            env_path.write_text("# comment\nAUTO_RESET_ENABLED=false\nNAME=value\n", encoding="utf-8")
            update_values(
                env_path,
                {
                    "AUTO_RESET_ENABLED": "true",
                    "MANUAL_CONFIRM_SUCCESS_COUNT": "3",
                    "VALUE_WITH_SPACE": "hello world",
                },
            )
            text = env_path.read_text(encoding="utf-8")
            self.assertIn("# comment", text)
            self.assertIn("AUTO_RESET_ENABLED=true", text)
            self.assertIn("NAME=value", text)
            self.assertIn("MANUAL_CONFIRM_SUCCESS_COUNT=3", text)
            self.assertIn('VALUE_WITH_SPACE="hello world"', text)
