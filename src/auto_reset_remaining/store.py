from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime
from typing import Any

from .config import PGConfig


class InvalidTokenError(RuntimeError):
    pass


@dataclass
class ResetLog:
    mode: str
    balance: float
    subscription_id: int = 0
    success: bool = False
    http_status: int = 0
    error: str = ""
    response_summary: str = ""


@dataclass
class ConfirmToken:
    id: int
    balance: float
    expires_at: datetime


class PostgresStore:
    def __init__(self, config: PGConfig) -> None:
        import psycopg

        kwargs: dict[str, Any] = {
            "host": config.host,
            "port": config.port,
            "user": config.user,
            "dbname": config.database,
            "sslmode": config.sslmode,
            "autocommit": True,
        }
        if config.password:
            kwargs["password"] = config.password
        self._conn = psycopg.connect(**kwargs)

    def close(self) -> None:
        self._conn.close()

    def init(self) -> None:
        statements = [
            """CREATE TABLE IF NOT EXISTS reset_logs (
                id BIGSERIAL PRIMARY KEY,
                triggered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                mode TEXT NOT NULL,
                balance DOUBLE PRECISION NOT NULL,
                subscription_id BIGINT,
                success BOOLEAN NOT NULL,
                http_status INTEGER,
                error TEXT,
                response_summary TEXT
            )""",
            "CREATE INDEX IF NOT EXISTS reset_logs_triggered_at_idx ON reset_logs (triggered_at DESC)",
            """CREATE TABLE IF NOT EXISTS confirm_tokens (
                id BIGSERIAL PRIMARY KEY,
                token_hash TEXT NOT NULL UNIQUE,
                balance DOUBLE PRECISION NOT NULL,
                created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
                expires_at TIMESTAMPTZ NOT NULL,
                used_at TIMESTAMPTZ,
                reset_log_id BIGINT REFERENCES reset_logs(id),
                manual_success_count_after INTEGER
            )""",
            "CREATE INDEX IF NOT EXISTS confirm_tokens_active_idx ON confirm_tokens (expires_at) WHERE used_at IS NULL",
        ]
        with self._conn.cursor() as cursor:
            for statement in statements:
                cursor.execute(statement)

    def create_confirm_token(self, token_hash: str, balance: float, expires_at: datetime) -> None:
        with self._conn.cursor() as cursor:
            cursor.execute(
                "INSERT INTO confirm_tokens (token_hash, balance, expires_at) VALUES (%s, %s, %s)",
                (token_hash, balance, expires_at),
            )

    def delete_confirm_token(self, token_hash: str) -> None:
        with self._conn.cursor() as cursor:
            cursor.execute("DELETE FROM confirm_tokens WHERE token_hash = %s", (token_hash,))

    def has_active_confirm_token(self) -> bool:
        with self._conn.cursor() as cursor:
            cursor.execute(
                """SELECT EXISTS (
                    SELECT 1 FROM confirm_tokens
                    WHERE used_at IS NULL AND expires_at > now()
                )"""
            )
            return bool(cursor.fetchone()[0])

    def consume_confirm_token(self, token_hash: str) -> ConfirmToken:
        with self._conn.cursor() as cursor:
            cursor.execute(
                """UPDATE confirm_tokens
                SET used_at = now()
                WHERE token_hash = %s AND used_at IS NULL AND expires_at > now()
                RETURNING id, balance, expires_at""",
                (token_hash,),
            )
            row = cursor.fetchone()
        if row is None:
            raise InvalidTokenError("confirm token is invalid, expired, or already used")
        return ConfirmToken(id=int(row[0]), balance=float(row[1]), expires_at=row[2])

    def mark_confirm_token_reset(self, token_id: int, reset_log_id: int, manual_success_count_after: int) -> None:
        with self._conn.cursor() as cursor:
            cursor.execute(
                """UPDATE confirm_tokens
                SET reset_log_id = %s, manual_success_count_after = %s
                WHERE id = %s""",
                (reset_log_id, manual_success_count_after, token_id),
            )

    def log_reset(self, entry: ResetLog) -> int:
        with self._conn.cursor() as cursor:
            cursor.execute(
                """INSERT INTO reset_logs
                (mode, balance, subscription_id, success, http_status, error, response_summary)
                VALUES (%s, %s, %s, %s, %s, %s, %s)
                RETURNING id""",
                (
                    entry.mode,
                    entry.balance,
                    entry.subscription_id or None,
                    entry.success,
                    entry.http_status or None,
                    entry.error or None,
                    entry.response_summary or None,
                ),
            )
            return int(cursor.fetchone()[0])
