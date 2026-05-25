from __future__ import annotations

from dataclasses import dataclass
from datetime import date, datetime, timedelta
import hashlib
import secrets
import threading
import time
from typing import Callable, Protocol

from auto_reset_remaining.api import APIClient, APIError, BalanceResult, ResetResult
from auto_reset_remaining.config import AppConfig, is_time_in_manual_confirm_range, update_runtime_state
from auto_reset_remaining.query_log import QueryLogEntry, QueryLogger
from auto_reset_remaining.store import SQLiteStore


class ConfirmCallback(Protocol):
    def __call__(self, balance: float, expires_at: datetime, reason: str) -> bool:
        ...


EventCallback = Callable[["MonitorEvent"], None]


@dataclass(frozen=True)
class MonitorEvent:
    kind: str
    message: str
    balance: float | None = None
    status: str | None = None


@dataclass(frozen=True)
class MonitorSnapshot:
    running: bool
    status: str
    last_balance: float | None
    auto_reset_enabled: bool
    manual_confirm_success_count: int
    last_error: str | None


class ResetInProgress(RuntimeError):
    pass


@dataclass(frozen=True)
class ExecutedReset:
    result: ResetResult
    log_id: int


class Monitor:
    def __init__(
        self,
        config: AppConfig,
        api_client: APIClient,
        store: SQLiteStore,
        query_logger: QueryLogger,
        confirm_callback: ConfirmCallback | None = None,
        event_callback: EventCallback | None = None,
        sleeper: Callable[[float], None] = time.sleep,
    ) -> None:
        self.config = config
        self.api_client = api_client
        self.store = store
        self.query_logger = query_logger
        self.confirm_callback = confirm_callback
        self.event_callback = event_callback
        self.sleeper = sleeper
        self._stop = threading.Event()
        self._thread: threading.Thread | None = None
        self._lock = threading.Lock()
        self._reset_lock = threading.Lock()
        self._last_balance: float | None = None
        self._last_error: str | None = None
        self._status = "未启动"
        self._last_auto_reset_at: datetime | None = None

    def start(self) -> None:
        with self._lock:
            if self._thread and self._thread.is_alive():
                return
            self._stop.clear()
            self._thread = threading.Thread(target=self._run_loop, name="balance-monitor", daemon=True)
            self._thread.start()
            self._status = "监控中"
        self._emit("info", "余额监控已启动", status="running")

    def stop(self) -> None:
        self._stop.set()
        with self._lock:
            self._status = "已暂停"
        self._emit("info", "余额监控已暂停", status="paused")

    def join(self, timeout: float | None = None) -> None:
        thread = self._thread
        if thread:
            thread.join(timeout)

    def snapshot(self) -> MonitorSnapshot:
        with self._lock:
            running = bool(self._thread and self._thread.is_alive() and not self._stop.is_set())
            return MonitorSnapshot(
                running=running,
                status=self._status,
                last_balance=self._last_balance,
                auto_reset_enabled=self.config.reset.auto_reset_enabled,
                manual_confirm_success_count=self.config.reset.manual_confirm_success_count,
                last_error=self._last_error,
            )

    def tick_once(self) -> str:
        started = datetime.now()
        status = "unknown"
        balance: float | None = None
        error: Exception | None = None
        try:
            status, balance = self._tick()
            return status
        except Exception as exc:
            error = exc
            status = "error"
            with self._lock:
                self._last_error = str(exc)
                self._status = f"错误：{exc}"
            self._emit("error", f"监控出错：{exc}", status=status)
            return status
        finally:
            duration_ms = int((datetime.now() - started).total_seconds() * 1000)
            self.query_logger.log(
                QueryLogEntry(
                    time=started,
                    status=status,
                    duration_ms=duration_ms,
                    balance=balance,
                    error=str(error) if error else None,
                )
            )

    def manual_reset(self, balance: float | None = None) -> ResetResult:
        if balance is None:
            result = self.api_client.query_balance()
            balance = result.balance
        return self._execute_reset("manual", balance).result

    def _run_loop(self) -> None:
        while not self._stop.is_set():
            self.tick_once()
            interval = max(self.config.polling.default_interval_seconds, 0.1)
            self.sleeper(interval)

    def _tick(self) -> tuple[str, float | None]:
        today = date.today()
        state = self.store.get_daily_reset_limit_state(today, self.config.reset.daily_max_reset_count)
        if state.balance_query_paused:
            with self._lock:
                self._status = "今日已暂停查询"
            return "balance_query_paused", None

        balance_result = self.api_client.query_balance()
        balance = balance_result.balance
        with self._lock:
            self._last_balance = balance
            self._last_error = None

        status = self._handle_balance(balance, balance_result)
        with self._lock:
            self._status = status
        self._emit("status", f"余额：{balance:.6f}，状态：{status}", balance=balance, status=status)
        return status, balance

    def _handle_balance(self, balance: float, _balance_result: BalanceResult) -> str:
        today = date.today()
        state = self.store.get_daily_reset_limit_state(today, self.config.reset.daily_max_reset_count)
        if state.max_reset_count > 0 and state.reset_count >= state.max_reset_count:
            if not state.limit_notice_shown:
                self.store.mark_daily_limit_notice_shown(today)
                self._emit("warning", f"今日重置次数已达到上限 {state.max_reset_count} 次", balance=balance, status="daily_limit")
            if balance <= 0:
                self.store.pause_balance_queries_for_day(today)
                self._emit("warning", "余额已耗尽且今日重置已达上限，暂停余额查询到明天", balance=balance, status="paused")
                return "balance_query_paused"
            return "daily_reset_limit_reached"

        if self.config.reset.auto_reset_enabled:
            if balance <= 0:
                now = datetime.now()
                if is_time_in_manual_confirm_range(
                    self.config.reset.manual_confirm_time_range,
                    now.hour,
                    now.minute,
                ):
                    return self._maybe_request_manual_confirm(balance, "manual_confirm_window")
                if self._in_auto_reset_cooldown(now):
                    return "auto_reset_cooldown"
                self._execute_reset("auto", balance)
                self._last_auto_reset_at = now
                return "auto_reset_success"
            return "ok"

        if balance < self.config.reset.low_balance_threshold:
            return self._maybe_request_manual_confirm(balance, "low_balance")
        return "ok"

    def _maybe_request_manual_confirm(self, balance: float, reason: str) -> str:
        if self.store.has_pending_confirm_request():
            return f"{reason}_confirm_pending"
        raw_token = secrets.token_urlsafe(32)
        token_hash = hash_token(raw_token)
        expires_at = datetime.now() + timedelta(seconds=self.config.reset.confirm_request_ttl_seconds)
        self.store.create_confirm_request(token_hash, balance, expires_at)
        confirmed = False
        if self.confirm_callback is not None:
            confirmed = self.confirm_callback(balance, expires_at, reason)
        if not confirmed:
            self._emit("warning", "已创建低余额确认请求，等待用户在弹窗中确认", balance=balance, status="confirm_pending")
            return f"{reason}_confirm_pending"

        request = self.store.consume_confirm_request(token_hash)
        executed = self._execute_reset("manual", request.balance)
        next_count = self.config.reset.manual_confirm_success_count + 1
        auto_enabled = self.config.reset.auto_reset_enabled or next_count >= 3
        update_runtime_state(self.config.path, next_count, auto_enabled)
        self.config = self._replace_runtime_state(next_count, auto_enabled)
        self.store.mark_confirm_request_reset(request.id, executed.log_id, next_count)
        self._emit(
            "info",
            f"人工确认重置成功，订阅 ID {executed.result.subscription_id}，累计确认 {next_count} 次",
            balance=balance,
            status="manual_reset_success",
        )
        return "manual_reset_success"

    def _execute_reset(self, mode: str, balance: float) -> ExecutedReset:
        if not self._reset_lock.acquire(blocking=False):
            raise ResetInProgress("已有重置操作正在执行")
        try:
            result = self.api_client.reset_quota()
            log_id = self.store.log_reset(
                mode=mode,
                balance=balance,
                subscription_id=result.subscription_id,
                success=result.success,
                http_status=result.http_status,
                response_summary=result.response_summary,
            )
            self._emit("info", f"订阅额度已重置，订阅 ID {result.subscription_id}", balance=balance, status=f"{mode}_reset_success")
            return ExecutedReset(result=result, log_id=log_id)
        except Exception as exc:
            subscription_id = None
            http_status = None
            response_summary = None
            if isinstance(exc, APIError):
                response_summary = str(exc)
            self.store.log_reset(
                mode=mode,
                balance=balance,
                subscription_id=subscription_id,
                success=False,
                http_status=http_status,
                error=str(exc),
                response_summary=response_summary,
            )
            self._emit("error", f"重置失败：{exc}", balance=balance, status=f"{mode}_reset_error")
            raise
        finally:
            self._reset_lock.release()

    def _in_auto_reset_cooldown(self, now: datetime) -> bool:
        if self._last_auto_reset_at is None:
            return False
        return (now - self._last_auto_reset_at).total_seconds() < self.config.reset.cooldown_seconds

    def _replace_runtime_state(self, manual_count: int, auto_enabled: bool) -> AppConfig:
        reset = self.config.reset
        updated_reset = type(reset)(
            low_balance_threshold=reset.low_balance_threshold,
            auto_reset_enabled=auto_enabled,
            manual_confirm_success_count=manual_count,
            daily_max_reset_count=reset.daily_max_reset_count,
            confirm_request_ttl_seconds=reset.confirm_request_ttl_seconds,
            cooldown_seconds=reset.cooldown_seconds,
            manual_confirm_time_range=reset.manual_confirm_time_range,
        )
        return type(self.config)(
            path=self.config.path,
            rayplus=self.config.rayplus,
            codex=self.config.codex,
            sqlite=self.config.sqlite,
            reset=updated_reset,
            polling=self.config.polling,
            logs=self.config.logs,
        )

    def _emit(self, kind: str, message: str, balance: float | None = None, status: str | None = None) -> None:
        if self.event_callback:
            self.event_callback(MonitorEvent(kind=kind, message=message, balance=balance, status=status))


def hash_token(raw_token: str) -> str:
    return hashlib.sha256(raw_token.encode("utf-8")).hexdigest()
