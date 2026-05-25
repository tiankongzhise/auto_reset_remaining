from __future__ import annotations

from dataclasses import dataclass
from datetime import date, datetime, timezone
from pathlib import Path
import sqlite3


def utc_now() -> datetime:
    return datetime.now(timezone.utc)


@dataclass(frozen=True)
class ConfirmRequest:
    id: int
    token_hash: str
    balance: float
    status: str
    expires_at: datetime


@dataclass(frozen=True)
class ResetLog:
    id: int
    mode: str
    balance: float
    subscription_id: int | None
    success: bool
    error: str | None
    created_at: datetime


@dataclass(frozen=True)
class DailyResetLimitState:
    day: date
    reset_count: int
    max_reset_count: int
    limit_notice_shown: bool
    balance_query_paused: bool


class StoreError(RuntimeError):
    pass


class InvalidConfirmRequest(StoreError):
    pass


class SQLiteStore:
    def __init__(self, path: Path) -> None:
        self.path = Path(path)
        self.path.parent.mkdir(parents=True, exist_ok=True)
        self._connection = sqlite3.connect(self.path, check_same_thread=False)
        self._connection.row_factory = sqlite3.Row
        self._connection.execute("PRAGMA foreign_keys = ON")

    def close(self) -> None:
        self._connection.close()

    def init(self) -> None:
        statements = [
            """
            CREATE TABLE IF NOT EXISTS reset_logs (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                created_at TEXT NOT NULL,
                mode TEXT NOT NULL,
                balance REAL NOT NULL,
                subscription_id INTEGER,
                success INTEGER NOT NULL,
                http_status INTEGER,
                error TEXT,
                response_summary TEXT
            )
            """,
            "CREATE INDEX IF NOT EXISTS reset_logs_created_at_idx ON reset_logs (created_at DESC)",
            """
            CREATE TABLE IF NOT EXISTS confirm_requests (
                id INTEGER PRIMARY KEY AUTOINCREMENT,
                token_hash TEXT NOT NULL UNIQUE,
                balance REAL NOT NULL,
                created_at TEXT NOT NULL,
                expires_at TEXT NOT NULL,
                status TEXT NOT NULL,
                confirmed_at TEXT,
                cancelled_at TEXT,
                reset_log_id INTEGER REFERENCES reset_logs(id),
                manual_success_count_after INTEGER
            )
            """,
            "CREATE INDEX IF NOT EXISTS confirm_requests_active_idx ON confirm_requests (expires_at) WHERE status = 'pending'",
            """
            CREATE TABLE IF NOT EXISTS daily_reset_limit_state (
                day TEXT PRIMARY KEY,
                limit_notice_shown INTEGER NOT NULL DEFAULT 0,
                balance_query_paused INTEGER NOT NULL DEFAULT 0
            )
            """,
            """
            CREATE TABLE IF NOT EXISTS confirm_prompt_suppression (
                id INTEGER PRIMARY KEY CHECK (id = 1),
                suppressed INTEGER NOT NULL,
                reason TEXT NOT NULL,
                balance REAL NOT NULL,
                created_at TEXT NOT NULL,
                cleared_at TEXT
            )
            """,
        ]
        with self._connection:
            for statement in statements:
                self._connection.execute(statement)
        self.expire_pending_confirm_requests()

    def create_confirm_request(self, token_hash: str, balance: float, expires_at: datetime) -> int:
        now = utc_now()
        with self._connection:
            cursor = self._connection.execute(
                """
                INSERT INTO confirm_requests
                    (token_hash, balance, created_at, expires_at, status)
                VALUES (?, ?, ?, ?, 'pending')
                """,
                (token_hash, balance, _format_dt(now), _format_dt(expires_at)),
            )
            self._connection.execute(
                """
                UPDATE confirm_requests
                SET status = 'cancelled', cancelled_at = ?
                WHERE status = 'pending' AND token_hash <> ?
                """,
                (_format_dt(now), token_hash),
            )
        return int(cursor.lastrowid)

    def has_pending_confirm_request(self) -> bool:
        self.expire_pending_confirm_requests()
        row = self._connection.execute(
            """
            SELECT 1
            FROM confirm_requests
            WHERE status = 'pending' AND expires_at > ?
            LIMIT 1
            """,
            (_format_dt(utc_now()),),
        ).fetchone()
        return row is not None

    def get_latest_pending_confirm_request(self) -> ConfirmRequest | None:
        self.expire_pending_confirm_requests()
        row = self._connection.execute(
            """
            SELECT id, token_hash, balance, status, expires_at
            FROM confirm_requests
            WHERE status = 'pending' AND expires_at > ?
            ORDER BY id DESC
            LIMIT 1
            """,
            (_format_dt(utc_now()),),
        ).fetchone()
        return _confirm_request_from_row(row) if row else None

    def consume_confirm_request(self, token_hash: str) -> ConfirmRequest:
        self.expire_pending_confirm_requests()
        now = utc_now()
        with self._connection:
            row = self._connection.execute(
                """
                SELECT id, token_hash, balance, status, expires_at
                FROM confirm_requests
                WHERE token_hash = ? AND status = 'pending' AND expires_at > ?
                """,
                (token_hash, _format_dt(now)),
            ).fetchone()
            if row is None:
                raise InvalidConfirmRequest("确认请求已失效、已使用或不存在")
            self._connection.execute(
                """
                UPDATE confirm_requests
                SET status = 'confirmed', confirmed_at = ?
                WHERE id = ?
                """,
                (_format_dt(now), row["id"]),
            )
        return _confirm_request_from_row(row)

    def mark_confirm_request_reset(self, request_id: int, reset_log_id: int, manual_success_count_after: int) -> None:
        with self._connection:
            self._connection.execute(
                """
                UPDATE confirm_requests
                SET status = 'reset',
                    reset_log_id = ?,
                    manual_success_count_after = ?
                WHERE id = ?
                """,
                (reset_log_id, manual_success_count_after, request_id),
            )

    def cancel_pending_confirm_requests(self, status: str = "cancelled") -> int:
        now = utc_now()
        with self._connection:
            cursor = self._connection.execute(
                """
                UPDATE confirm_requests
                SET status = ?, cancelled_at = ?
                WHERE status = 'pending'
                """,
                (status, _format_dt(now)),
            )
        return cursor.rowcount

    def suppress_confirm_prompt(self, reason: str, balance: float) -> None:
        now = utc_now()
        with self._connection:
            self._connection.execute(
                """
                INSERT INTO confirm_prompt_suppression
                    (id, suppressed, reason, balance, created_at, cleared_at)
                VALUES (1, 1, ?, ?, ?, NULL)
                ON CONFLICT(id) DO UPDATE SET
                    suppressed = 1,
                    reason = excluded.reason,
                    balance = excluded.balance,
                    created_at = excluded.created_at,
                    cleared_at = NULL
                """,
                (reason, balance, _format_dt(now)),
            )

    def is_confirm_prompt_suppressed(self) -> bool:
        row = self._connection.execute(
            """
            SELECT suppressed
            FROM confirm_prompt_suppression
            WHERE id = 1
            """
        ).fetchone()
        return bool(row and row["suppressed"])

    def clear_confirm_prompt_suppression(self) -> None:
        now = utc_now()
        with self._connection:
            self._connection.execute(
                """
                UPDATE confirm_prompt_suppression
                SET suppressed = 0, cleared_at = ?
                WHERE id = 1
                """,
                (_format_dt(now),),
            )

    def expire_pending_confirm_requests(self) -> int:
        now = utc_now()
        with self._connection:
            cursor = self._connection.execute(
                """
                UPDATE confirm_requests
                SET status = 'expired'
                WHERE status = 'pending' AND expires_at <= ?
                """,
                (_format_dt(now),),
            )
        return cursor.rowcount

    def log_reset(
        self,
        mode: str,
        balance: float,
        subscription_id: int | None,
        success: bool,
        http_status: int | None = None,
        error: str | None = None,
        response_summary: str | None = None,
    ) -> int:
        with self._connection:
            cursor = self._connection.execute(
                """
                INSERT INTO reset_logs
                    (created_at, mode, balance, subscription_id, success, http_status, error, response_summary)
                VALUES (?, ?, ?, ?, ?, ?, ?, ?)
                """,
                (
                    _format_dt(utc_now()),
                    mode,
                    balance,
                    subscription_id,
                    1 if success else 0,
                    http_status,
                    error,
                    response_summary,
                ),
            )
        return int(cursor.lastrowid)

    def recent_reset_logs(self, limit: int = 20) -> list[ResetLog]:
        rows = self._connection.execute(
            """
            SELECT id, created_at, mode, balance, subscription_id, success, error
            FROM reset_logs
            ORDER BY id DESC
            LIMIT ?
            """,
            (limit,),
        ).fetchall()
        return [
            ResetLog(
                id=int(row["id"]),
                mode=str(row["mode"]),
                balance=float(row["balance"]),
                subscription_id=int(row["subscription_id"]) if row["subscription_id"] is not None else None,
                success=bool(row["success"]),
                error=str(row["error"]) if row["error"] else None,
                created_at=_parse_dt(str(row["created_at"])),
            )
            for row in rows
        ]

    def get_daily_reset_limit_state(self, day: date, max_reset_count: int) -> DailyResetLimitState:
        self._ensure_daily_row(day)
        next_day = date.fromordinal(day.toordinal() + 1)
        reset_count = int(
            self._connection.execute(
                """
                SELECT COUNT(*)
                FROM reset_logs
                WHERE success = 1 AND created_at >= ? AND created_at < ?
                """,
                (_format_day(day), _format_day(next_day)),
            ).fetchone()[0]
        )
        row = self._connection.execute(
            """
            SELECT limit_notice_shown, balance_query_paused
            FROM daily_reset_limit_state
            WHERE day = ?
            """,
            (_format_day(day),),
        ).fetchone()
        limit_notice_shown = bool(row["limit_notice_shown"]) if row else False
        balance_query_paused = bool(row["balance_query_paused"]) if row else False
        return DailyResetLimitState(
            day=day,
            reset_count=reset_count,
            max_reset_count=max_reset_count,
            limit_notice_shown=limit_notice_shown,
            balance_query_paused=max_reset_count > 0 and reset_count >= max_reset_count and balance_query_paused,
        )

    def mark_daily_limit_notice_shown(self, day: date) -> None:
        self._ensure_daily_row(day)
        with self._connection:
            self._connection.execute(
                "UPDATE daily_reset_limit_state SET limit_notice_shown = 1 WHERE day = ?",
                (_format_day(day),),
            )

    def pause_balance_queries_for_day(self, day: date) -> None:
        self._ensure_daily_row(day)
        with self._connection:
            self._connection.execute(
                "UPDATE daily_reset_limit_state SET balance_query_paused = 1 WHERE day = ?",
                (_format_day(day),),
            )

    def _ensure_daily_row(self, day: date) -> None:
        with self._connection:
            self._connection.execute(
                """
                INSERT INTO daily_reset_limit_state (day)
                VALUES (?)
                ON CONFLICT(day) DO NOTHING
                """,
                (_format_day(day),),
            )


def _confirm_request_from_row(row: sqlite3.Row) -> ConfirmRequest:
    return ConfirmRequest(
        id=int(row["id"]),
        token_hash=str(row["token_hash"]),
        balance=float(row["balance"]),
        status=str(row["status"]),
        expires_at=_parse_dt(str(row["expires_at"])),
    )


def _format_dt(value: datetime) -> str:
    if value.tzinfo is None:
        value = value.replace(tzinfo=timezone.utc)
    return value.astimezone(timezone.utc).isoformat()


def _parse_dt(value: str) -> datetime:
    return datetime.fromisoformat(value)


def _format_day(value: date) -> str:
    return value.isoformat()
