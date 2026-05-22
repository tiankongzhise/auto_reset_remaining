from __future__ import annotations

import json
import threading
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from typing import Any


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


@dataclass
class BalanceResult:
    balance: float
    raw: bytes


@dataclass
class ResetResult:
    subscription_id: int = 0
    http_status: int = 0
    response_summary: str = ""
    success: bool = False


class APIError(RuntimeError):
    pass


class ResetQuotaError(APIError):
    def __init__(self, message: str, result: ResetResult):
        super().__init__(message)
        self.result = result


class APIClient:
    def __init__(
        self,
        *,
        rayplus_base_url: str,
        rayplus_api_key: str,
        rayplus_email: str,
        rayplus_password: str,
        codex_base_url: str,
        subscription_id: int = 0,
        balance_json_path: str = "",
        user_agent: str = "auto-reset-remaining/1.0",
        timeout: float = 30.0,
    ) -> None:
        self.rayplus_base_url = rayplus_base_url.rstrip("/")
        self.rayplus_api_key = rayplus_api_key
        self.rayplus_email = rayplus_email
        self.rayplus_password = rayplus_password
        self.codex_base_url = codex_base_url.rstrip("/")
        self.subscription_id = subscription_id
        self.balance_json_path = balance_json_path
        self.user_agent = user_agent
        self.timeout = timeout
        self._token = ""
        self._token_expires_at = 0.0
        self._lock = threading.Lock()

    def query_balance(self) -> BalanceResult:
        status, body = self._request(
            "GET",
            f"{self.rayplus_base_url}/v1/usage",
            headers={
                "Authorization": f"Bearer {self.rayplus_api_key}",
                "Accept": "application/json",
                "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
                "Cache-Control": "no-cache",
            },
        )
        if not 200 <= status < 300:
            raise APIError(f"usage API returned HTTP {status}: {_summarize(body)}")
        return BalanceResult(balance=parse_balance(body, self.balance_json_path), raw=body)

    def reset_quota(self) -> ResetResult:
        subscription = self._select_subscription()
        status, body = self._codex_request("POST", f"/api/subscriptions/{subscription['id']}/reset-quota", b"")
        result = ResetResult(
            subscription_id=int(subscription["id"]),
            http_status=status,
            response_summary=_summarize(body),
            success=False,
        )
        if not 200 <= status < 300:
            raise ResetQuotaError(f"reset quota returned HTTP {status}: {_summarize(body)}", result)
        result.success = True
        return result

    def list_subscriptions(self) -> list[dict[str, Any]]:
        status, body = self._codex_request("GET", "/api/subscriptions")
        if not 200 <= status < 300:
            raise APIError(f"subscriptions API returned HTTP {status}: {_summarize(body)}")
        payload = json.loads(body.decode("utf-8"))
        subscriptions = payload.get("subscriptions", [])
        if not isinstance(subscriptions, list):
            raise APIError("subscriptions response has invalid shape")
        return subscriptions

    def _select_subscription(self) -> dict[str, Any]:
        subscriptions = self.list_subscriptions()
        if self.subscription_id:
            for subscription in subscriptions:
                if int(subscription.get("id", 0)) == self.subscription_id:
                    if not bool(subscription.get("canReset")):
                        raise APIError(f"configured subscription {self.subscription_id} cannot reset now")
                    return subscription
            raise APIError(f"configured subscription {self.subscription_id} not found")
        for subscription in subscriptions:
            if str(subscription.get("status", "")).lower() == "active" and bool(subscription.get("canReset")):
                return subscription
        raise APIError("no active resettable subscription found")

    def _codex_request(self, method: str, path: str, body: bytes | None = None) -> tuple[int, bytes]:
        token = self._ensure_token()
        status, response_body = self._request(
            method,
            f"{self.codex_base_url}{path}",
            body=body,
            headers={
                "Authorization": f"Bearer {token}",
                "Accept": "application/json",
                "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
                "Content-Type": "application/json",
            },
        )
        if status == 401:
            token = self._ensure_token(force=True)
            status, response_body = self._request(
                method,
                f"{self.codex_base_url}{path}",
                body=body,
                headers={
                    "Authorization": f"Bearer {token}",
                    "Accept": "application/json",
                    "Accept-Language": "zh-CN,zh;q=0.9,en;q=0.8",
                    "Content-Type": "application/json",
                },
            )
        return status, response_body

    def _ensure_token(self, force: bool = False) -> str:
        with self._lock:
            if not force and self._token and time.time() < self._token_expires_at - 60:
                return self._token
        token, expires_at = self._login()
        with self._lock:
            self._token = token
            self._token_expires_at = expires_at
        return token

    def _login(self) -> tuple[str, float]:
        payload = json.dumps({"email": self.rayplus_email, "password": self.rayplus_password}).encode("utf-8")
        status, body = self._request(
            "POST",
            f"{self.rayplus_base_url}/api/v1/auth/login",
            body=payload,
            headers={"Accept": "application/json", "Content-Type": "application/json"},
        )
        if not 200 <= status < 300:
            raise APIError(f"login returned HTTP {status}: {_summarize(body)}")
        parsed = json.loads(body.decode("utf-8"))
        if int(parsed.get("code", 0)) != 0:
            raise APIError(f"login failed: {parsed.get('message', '')}")
        data = parsed.get("data") or {}
        token = data.get("access_token", "")
        if not token:
            raise APIError("login response missing access token")
        expires_in = int(data.get("expires_in") or 3600)
        return token, time.time() + expires_in

    def _request(
        self,
        method: str,
        url: str,
        *,
        body: bytes | None = None,
        headers: dict[str, str] | None = None,
    ) -> tuple[int, bytes]:
        request = urllib.request.Request(url, data=body, method=method)
        request.add_header("User-Agent", self.user_agent)
        for key, value in (headers or {}).items():
            request.add_header(key, value)
        try:
            with urllib.request.urlopen(request, timeout=self.timeout) as response:
                return response.status, response.read(2 * 1024 * 1024)
        except urllib.error.HTTPError as exc:
            return exc.code, exc.read(2 * 1024 * 1024)
        except urllib.error.URLError as exc:
            raise APIError(f"{method} {url} failed: {exc}") from exc


def parse_balance(body: bytes, configured_path: str = "") -> float:
    payload = json.loads(body.decode("utf-8"))
    if configured_path:
        found, value = _lookup_path(payload, configured_path)
        if not found:
            raise APIError(f'BALANCE_JSON_PATH "{configured_path}" not found')
        return _number(value)

    for path in DEFAULT_BALANCE_PATHS:
        found, value = _lookup_path(payload, path)
        if found:
            try:
                return _number(value)
            except (TypeError, ValueError):
                pass

    found, value = _find_by_key(
        payload,
        {"balance", "remaining", "credits_remaining", "available_balance", "total_available", "remain_quota"},
    )
    if found:
        return _number(value)
    raise APIError("could not find balance field in usage response; set BALANCE_JSON_PATH")


def _lookup_path(payload: Any, path: str) -> tuple[bool, Any]:
    current = payload
    for part in path.split("."):
        if isinstance(current, dict):
            if part not in current:
                return False, None
            current = current[part]
        elif isinstance(current, list):
            try:
                index = int(part)
            except ValueError:
                return False, None
            if not 0 <= index < len(current):
                return False, None
            current = current[index]
        else:
            return False, None
    return True, current


def _find_by_key(payload: Any, keys: set[str]) -> tuple[bool, Any]:
    if isinstance(payload, dict):
        for key, value in payload.items():
            if key.lower() in keys:
                return True, value
        for value in payload.values():
            found, nested = _find_by_key(value, keys)
            if found:
                return True, nested
    elif isinstance(payload, list):
        for value in payload:
            found, nested = _find_by_key(value, keys)
            if found:
                return True, nested
    return False, None


def _number(value: Any) -> float:
    if isinstance(value, bool):
        raise TypeError("boolean is not a numeric balance")
    return float(value)


def _summarize(body: bytes) -> str:
    text = body.decode("utf-8", errors="replace").strip().replace("\n", " ").replace("\r", " ")
    if len(text) > 1000:
        return text[:1000] + "...(truncated)"
    return text
