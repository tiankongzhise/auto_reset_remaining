from __future__ import annotations

import hashlib
import logging
import secrets
import threading
import time
import urllib.parse
from dataclasses import dataclass
from datetime import UTC, datetime, timedelta
from typing import Protocol

from .api import BalanceResult, ResetQuotaError, ResetResult
from .config import Config
from .envfile import update_values
from .query_logger import QueryLogEntry, QueryLogger
from .store import ConfirmToken, InvalidTokenError, ResetLog


class APIClientProtocol(Protocol):
    def query_balance(self) -> BalanceResult: ...

    def reset_quota(self) -> ResetResult: ...


class MailerProtocol(Protocol):
    def send(self, subject: str, body: str) -> None: ...


class StoreProtocol(Protocol):
    def has_active_confirm_token(self) -> bool: ...

    def create_confirm_token(self, token_hash: str, balance: float, expires_at: datetime) -> None: ...

    def delete_confirm_token(self, token_hash: str) -> None: ...

    def consume_confirm_token(self, token_hash: str) -> ConfirmToken: ...

    def mark_confirm_token_reset(self, token_id: int, reset_log_id: int, manual_success_count_after: int) -> None: ...

    def log_reset(self, entry: ResetLog) -> int: ...


@dataclass
class ConfirmResult:
    balance: float
    subscription_id: int
    manual_confirm_success_count: int
    auto_reset_enabled: bool
    reset_log_id: int


class Monitor:
    def __init__(
        self,
        config: Config,
        api_client: APIClientProtocol,
        mailer: MailerProtocol,
        store: StoreProtocol,
        query_logger: QueryLogger,
        logger: logging.Logger | None = None,
    ) -> None:
        self.config = config
        self.api_client = api_client
        self.mailer = mailer
        self.store = store
        self.query_logger = query_logger
        self.logger = logger or logging.getLogger(__name__)
        self._lock = threading.Lock()
        self._pending_manual_email = False
        self._reset_in_flight = False
        self._last_auto_reset_at = 0.0

    def initialize(self) -> None:
        with self._lock:
            self._pending_manual_email = self.store.has_active_confirm_token()

    def run(self, stop_event: threading.Event) -> None:
        self.tick()
        while not stop_event.wait(self.config.poll_interval_seconds):
            self.tick()

    def tick(self) -> str:
        started = time.perf_counter()
        now = datetime.now(UTC).isoformat()
        try:
            result = self.api_client.query_balance()
        except Exception as exc:
            self.query_logger.log(
                QueryLogEntry(
                    time=now,
                    status="balance_error",
                    duration_ms=int((time.perf_counter() - started) * 1000),
                    error=str(exc),
                )
            )
            self.logger.warning("balance query failed: %s", exc)
            return "balance_error"

        error = ""
        try:
            status = self._handle_balance(result.balance)
        except Exception as exc:
            status = "action_error"
            error = str(exc)
            self.logger.warning("balance action failed: %s", exc)

        self.query_logger.log(
            QueryLogEntry(
                time=now,
                status=status,
                balance=result.balance,
                duration_ms=int((time.perf_counter() - started) * 1000),
                error=error,
            )
        )
        return status

    def confirm(self, raw_token: str) -> ConfirmResult:
        if not raw_token.strip():
            raise InvalidTokenError("confirm token is invalid, expired, or already used")
        token = self.store.consume_confirm_token(_hash_token(raw_token.strip()))
        with self._lock:
            self._pending_manual_email = False

        reset_result, reset_log_id = self._execute_reset("manual", token.balance)
        count, auto_enabled = self._increment_manual_success()
        self.store.mark_confirm_token_reset(token.id, reset_log_id, count)
        return ConfirmResult(
            balance=token.balance,
            subscription_id=reset_result.subscription_id,
            manual_confirm_success_count=count,
            auto_reset_enabled=auto_enabled,
            reset_log_id=reset_log_id,
        )

    def _handle_balance(self, balance: float) -> str:
        if self.config.auto_reset_enabled:
            if balance <= 0:
                return self._maybe_auto_reset(balance)
            return "ok"

        if balance >= self.config.low_balance_threshold:
            with self._lock:
                self._pending_manual_email = False
            return "ok"

        with self._lock:
            pending = self._pending_manual_email
        if pending:
            return "low_balance_email_pending"

        if self.store.has_active_confirm_token():
            with self._lock:
                self._pending_manual_email = True
            return "low_balance_email_pending"

        raw_token = secrets.token_urlsafe(32)
        token_hash = _hash_token(raw_token)
        expires_at = datetime.now(UTC) + timedelta(seconds=self.config.confirm_token_ttl_seconds)
        self.store.create_confirm_token(token_hash, balance, expires_at)
        try:
            self.mailer.send(
                "余额不足，请确认重置订阅",
                (
                    f"当前余额 {balance:.6f}，已低于阈值 {self.config.low_balance_threshold:.6f}。\n\n"
                    f"点击下面链接确认重置订阅：\n{_confirm_url(self.config.public_base_url, raw_token)}\n\n"
                    f"链接将在 {expires_at.isoformat()} 过期。如果不是你本人操作，请忽略本邮件。"
                ),
            )
        except Exception:
            self.store.delete_confirm_token(token_hash)
            raise

        with self._lock:
            self._pending_manual_email = True
        return "low_balance_email_sent"

    def _maybe_auto_reset(self, balance: float) -> str:
        now = time.monotonic()
        with self._lock:
            if self._reset_in_flight:
                return "auto_reset_in_progress"
            if self.config.reset_cooldown_seconds > 0 and self._last_auto_reset_at:
                if now - self._last_auto_reset_at < self.config.reset_cooldown_seconds:
                    return "auto_reset_cooldown"
            self._reset_in_flight = True
            self._last_auto_reset_at = now
        try:
            self._execute_reset("auto", balance)
            return "auto_reset_success"
        except Exception:
            return "auto_reset_error"
        finally:
            with self._lock:
                self._reset_in_flight = False

    def _execute_reset(self, mode: str, balance: float) -> tuple[ResetResult, int]:
        result = ResetResult()
        reset_error: Exception | None = None
        try:
            result = self.api_client.reset_quota()
        except ResetQuotaError as exc:
            result = exc.result
            reset_error = exc
        except Exception as exc:
            reset_error = exc

        reset_log_id = self.store.log_reset(
            ResetLog(
                mode=mode,
                balance=balance,
                subscription_id=result.subscription_id,
                success=reset_error is None and result.success,
                http_status=result.http_status,
                error=str(reset_error) if reset_error else "",
                response_summary=result.response_summary,
            )
        )
        if reset_error is not None:
            raise reset_error
        return result, reset_log_id

    def _increment_manual_success(self) -> tuple[int, bool]:
        with self._lock:
            next_count = self.config.manual_confirm_success_count + 1
            auto_enabled = self.config.auto_reset_enabled or next_count >= 3
            updates = {"MANUAL_CONFIRM_SUCCESS_COUNT": str(next_count)}
            if auto_enabled:
                updates["AUTO_RESET_ENABLED"] = "true"
            update_values(self.config.env_path, updates)
            self.config.manual_confirm_success_count = next_count
            self.config.auto_reset_enabled = auto_enabled
            return next_count, auto_enabled


def _hash_token(raw_token: str) -> str:
    return hashlib.sha256(raw_token.encode("utf-8")).hexdigest()


def _confirm_url(public_base_url: str, raw_token: str) -> str:
    return f"{public_base_url.rstrip('/')}/confirm-reset?token={urllib.parse.quote(raw_token)}"
