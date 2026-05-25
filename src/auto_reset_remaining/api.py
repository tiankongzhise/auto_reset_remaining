from __future__ import annotations

from dataclasses import dataclass
from datetime import datetime, timedelta, timezone
import json
import socket
from typing import Any, Protocol
import urllib.error
import urllib.request

from auto_reset_remaining.config import AppConfig


DEFAULT_BALANCE_PATHS = [
    "balance",
    "data.balance",
    "data.user.balance",
    "remaining",
    "data.remaining",
    "quota.remaining",
    "data.quota.remaining",
    "credits_remaining",
    "data.credits_remaining",
    "available_balance",
    "data.available_balance",
    "available",
    "data.available",
    "total_available",
    "data.total_available",
    "remain_quota",
    "data.remain_quota",
]


class APIError(RuntimeError):
    pass


class APINetworkError(APIError):
    pass


@dataclass(frozen=True)
class BalanceResult:
    balance: float
    raw: Any


@dataclass(frozen=True)
class Subscription:
    id: int
    status: str
    can_reset: bool


@dataclass(frozen=True)
class ResetResult:
    subscription_id: int
    http_status: int
    response_summary: str
    success: bool


@dataclass(frozen=True)
class HTTPResult:
    status: int
    body: bytes


class URLOpener(Protocol):
    def open(self, request: urllib.request.Request, timeout: float) -> Any:
        ...


class APIClient:
    def __init__(self, config: AppConfig, opener: URLOpener | None = None, timeout: float = 30.0) -> None:
        self.config = config
        self.opener = opener or urllib.request.build_opener()
        self.timeout = timeout
        self._access_token = ""
        self._token_expires_at = datetime.fromtimestamp(0, tz=timezone.utc)

    def query_balance(self) -> BalanceResult:
        result = self._request(
            "GET",
            self._join(self.config.rayplus.base_url, "/v1/usage"),
            headers={
                "Authorization": f"Bearer {self.config.rayplus.api_key}",
                "Accept": "application/json",
                "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
                "Cache-Control": "no-cache",
            },
        )
        payload = _decode_json(result.body, "usage")
        balance = parse_balance(payload, self.config.rayplus.balance_json_path)
        return BalanceResult(balance=balance, raw=payload)

    def list_subscriptions(self) -> list[Subscription]:
        result = self._codex_request("GET", "/api/subscriptions")
        payload = _decode_json(result.body, "subscriptions")
        subscriptions = payload.get("subscriptions", []) if isinstance(payload, dict) else []
        parsed: list[Subscription] = []
        for item in subscriptions:
            if not isinstance(item, dict):
                continue
            parsed.append(
                Subscription(
                    id=int(item.get("id", 0)),
                    status=str(item.get("status", "")),
                    can_reset=bool(item.get("canReset", item.get("can_reset", False))),
                )
            )
        return parsed

    def reset_quota(self) -> ResetResult:
        subscription = self._select_subscription()
        result = self._codex_request("POST", f"/api/subscriptions/{subscription.id}/reset-quota")
        success = 200 <= result.status < 300
        summary = _summarize(result.body)
        if not success:
            raise APIError(f"重置订阅额度失败：HTTP {result.status}: {summary}")
        return ResetResult(
            subscription_id=subscription.id,
            http_status=result.status,
            response_summary=summary,
            success=True,
        )

    def _select_subscription(self) -> Subscription:
        subscriptions = self.list_subscriptions()
        configured_id = self.config.codex.subscription_id
        if configured_id > 0:
            for subscription in subscriptions:
                if subscription.id == configured_id:
                    if not subscription.can_reset:
                        raise APIError(f"配置的订阅 {configured_id} 当前不可重置")
                    return subscription
            raise APIError(f"未找到配置的订阅 {configured_id}")
        for subscription in subscriptions:
            if subscription.status.lower() == "active" and subscription.can_reset:
                return subscription
        raise APIError("没有可重置的 active 订阅")

    def _codex_request(self, method: str, path: str, body: bytes | None = None) -> HTTPResult:
        token = self._ensure_token()
        try:
            return self._request(
                method,
                self._join(self.config.codex.base_url, path),
                body=body,
                headers={
                    "Authorization": f"Bearer {token}",
                    "Accept": "application/json",
                    "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
                    "Content-Type": "application/json",
                },
            )
        except APIError as exc:
            if "HTTP 401" not in str(exc):
                raise
            self._access_token = ""
            token = self._ensure_token(force=True)
            return self._request(
                method,
                self._join(self.config.codex.base_url, path),
                body=body,
                headers={
                    "Authorization": f"Bearer {token}",
                    "Accept": "application/json",
                    "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
                    "Content-Type": "application/json",
                },
            )

    def _ensure_token(self, force: bool = False) -> str:
        now = datetime.now(timezone.utc)
        if not force and self._access_token and now < self._token_expires_at - timedelta(minutes=1):
            return self._access_token
        payload = {
            "email": self.config.rayplus.email,
            "password": self.config.rayplus.password,
        }
        result = self._request(
            "POST",
            self._join(self.config.rayplus.base_url, "/api/v1/auth/login"),
            body=json.dumps(payload).encode("utf-8"),
            headers={"Accept": "application/json", "Content-Type": "application/json"},
        )
        parsed = _decode_json(result.body, "login")
        if not isinstance(parsed, dict):
            raise APIError("登录响应格式不正确")
        data = parsed.get("data", {})
        if parsed.get("code", 0) != 0:
            raise APIError(f"登录失败：{parsed.get('message', '')}")
        token = str(data.get("access_token", "")).strip() if isinstance(data, dict) else ""
        if not token:
            raise APIError("登录响应缺少 access_token")
        expires_in = int(data.get("expires_in", 3600)) if isinstance(data, dict) else 3600
        if expires_in <= 0:
            expires_in = 3600
        self._access_token = token
        self._token_expires_at = now + timedelta(seconds=expires_in)
        return token

    def _request(
        self,
        method: str,
        url: str,
        body: bytes | None = None,
        headers: dict[str, str] | None = None,
    ) -> HTTPResult:
        req = urllib.request.Request(url, data=body, method=method)
        req.add_header("User-Agent", self.config.rayplus.user_agent)
        for key, value in (headers or {}).items():
            req.add_header(key, value)
        try:
            with self.opener.open(req, timeout=self.timeout) as response:
                status = int(getattr(response, "status", response.getcode()))
                response_body = response.read()
        except urllib.error.HTTPError as exc:
            response_body = exc.read()
            raise APIError(f"HTTP {exc.code}: {_summarize(response_body)}") from exc
        except urllib.error.URLError as exc:
            raise APINetworkError(f"请求失败：{exc.reason}") from exc
        except (TimeoutError, socket.timeout, OSError) as exc:
            raise APINetworkError(f"请求失败：{exc}") from exc
        if status < 200 or status >= 300:
            raise APIError(f"HTTP {status}: {_summarize(response_body)}")
        return HTTPResult(status=status, body=response_body)

    @staticmethod
    def _join(base_url: str, path: str) -> str:
        return base_url.rstrip("/") + path


def parse_balance(payload: Any, configured_path: str = "") -> float:
    if configured_path.strip():
        found, value = lookup_path(payload, configured_path)
        if not found:
            raise APIError(f"找不到余额字段：{configured_path}")
        return number_from_any(value)

    for path in DEFAULT_BALANCE_PATHS:
        found, value = lookup_path(payload, path)
        if found:
            try:
                return number_from_any(value)
            except APIError:
                continue

    found, value = find_by_key(
        payload,
        {
            "balance",
            "remaining",
            "credits_remaining",
            "available_balance",
            "total_available",
            "remain_quota",
        },
    )
    if found:
        return number_from_any(value)
    raise APIError("无法在 usage 响应中找到余额字段，请设置 rayplus.balance_json_path")


def lookup_path(payload: Any, path: str) -> tuple[bool, Any]:
    current = payload
    for part in path.split("."):
        part = part.strip()
        if not part:
            return False, None
        if isinstance(current, dict):
            if part not in current:
                return False, None
            current = current[part]
            continue
        if isinstance(current, list):
            try:
                index = int(part)
            except ValueError:
                return False, None
            if index < 0 or index >= len(current):
                return False, None
            current = current[index]
            continue
        return False, None
    return True, current


def find_by_key(payload: Any, keys: set[str]) -> tuple[bool, Any]:
    if isinstance(payload, dict):
        for key, value in payload.items():
            if key.lower() in keys:
                return True, value
        for value in payload.values():
            found, nested = find_by_key(value, keys)
            if found:
                return True, nested
    if isinstance(payload, list):
        for value in payload:
            found, nested = find_by_key(value, keys)
            if found:
                return True, nested
    return False, None


def number_from_any(value: Any) -> float:
    if isinstance(value, bool):
        raise APIError("余额字段不是数字")
    if isinstance(value, int | float):
        return float(value)
    if isinstance(value, str):
        try:
            return float(value.strip())
        except ValueError as exc:
            raise APIError(f"余额字段不是数字：{value}") from exc
    raise APIError(f"余额字段类型不支持：{type(value).__name__}")


def _decode_json(body: bytes, label: str) -> Any:
    try:
        return json.loads(body.decode("utf-8"))
    except json.JSONDecodeError as exc:
        raise APIError(f"{label} 响应不是有效 JSON：{exc}") from exc


def _summarize(body: bytes) -> str:
    text = body.decode("utf-8", errors="replace").strip().replace("\r", " ").replace("\n", " ")
    return text[:1000] + ("...(truncated)" if len(text) > 1000 else "")
