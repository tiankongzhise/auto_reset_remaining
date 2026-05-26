from __future__ import annotations

from dataclasses import dataclass, field
from pathlib import Path
import re
import shutil
from typing import Any

import tomlkit
from tomlkit.items import Table


CONFIG_PATH = Path("config.toml")
EXAMPLE_CONFIG_PATH = Path("config.example.toml")


_DURATION_RE = re.compile(r"^\s*(\d+(?:\.\d+)?)(ms|s|m|h)\s*$", re.IGNORECASE)


@dataclass(frozen=True)
class RayPlusConfig:
    base_url: str
    api_key: str
    email: str
    password: str
    user_agent: str
    balance_json_path: str


@dataclass(frozen=True)
class CodexConfig:
    base_url: str
    subscription_id: int


@dataclass(frozen=True)
class SQLiteConfig:
    path: Path


@dataclass(frozen=True)
class ResetConfig:
    low_balance_threshold: float
    auto_reset_enabled: bool
    manual_confirm_success_count: int
    daily_max_reset_count: int
    confirm_request_ttl_seconds: float
    cooldown_seconds: float
    manual_confirm_time_range: str


@dataclass(frozen=True)
class SubscriptionTier:
    min_ratio: float
    interval_seconds: float


DEFAULT_SUBSCRIPTION_TIERS = (
    SubscriptionTier(min_ratio=0.70, interval_seconds=60),
    SubscriptionTier(min_ratio=0.40, interval_seconds=30),
    SubscriptionTier(min_ratio=0.20, interval_seconds=10),
    SubscriptionTier(min_ratio=0.000001, interval_seconds=3),
    SubscriptionTier(min_ratio=0, interval_seconds=1),
)


@dataclass(frozen=True)
class SubscriptionPollingConfig:
    enabled: bool
    quota: float
    tiers: tuple[SubscriptionTier, ...]


@dataclass(frozen=True)
class SleepPollingConfig:
    enabled: bool
    unchanged_for_seconds: float
    interval_seconds: float


@dataclass(frozen=True)
class AfterResetEmailPollingConfig:
    enabled: bool
    interval_seconds: float


@dataclass(frozen=True)
class PollingConfig:
    default_interval_seconds: float
    balance_change_epsilon: float
    subscription: SubscriptionPollingConfig = field(
        default_factory=lambda: SubscriptionPollingConfig(enabled=True, quota=100, tiers=DEFAULT_SUBSCRIPTION_TIERS)
    )
    sleep: SleepPollingConfig = field(default_factory=lambda: SleepPollingConfig(enabled=False, unchanged_for_seconds=600, interval_seconds=60))
    after_reset_email: AfterResetEmailPollingConfig = field(
        default_factory=lambda: AfterResetEmailPollingConfig(enabled=False, interval_seconds=60)
    )


@dataclass(frozen=True)
class LogsConfig:
    query_log_dir: Path


@dataclass(frozen=True)
class AppConfig:
    path: Path
    rayplus: RayPlusConfig
    codex: CodexConfig
    sqlite: SQLiteConfig
    reset: ResetConfig
    polling: PollingConfig
    logs: LogsConfig


class ConfigError(ValueError):
    pass


def ensure_config_file(config_path: Path = CONFIG_PATH, example_path: Path = EXAMPLE_CONFIG_PATH) -> bool:
    """Create config.toml from the checked-in example.

    Returns True when a new file is created.
    """

    if config_path.exists():
        return False
    if not example_path.exists():
        raise ConfigError(f"缺少默认配置模板：{example_path}")
    shutil.copyfile(example_path, config_path)
    return True


def load_config(config_path: Path = CONFIG_PATH) -> AppConfig:
    document = load_document(config_path)
    return parse_config(document, config_path)


def load_document(config_path: Path = CONFIG_PATH) -> tomlkit.TOMLDocument:
    if not config_path.exists():
        raise ConfigError(f"缺少配置文件：{config_path}")
    try:
        return tomlkit.parse(config_path.read_text(encoding="utf-8"))
    except Exception as exc:  # tomlkit raises several parse-specific errors.
        raise ConfigError(f"配置文件无法解析：{exc}") from exc


def save_document(document: tomlkit.TOMLDocument, config_path: Path = CONFIG_PATH) -> None:
    config_path.parent.mkdir(parents=True, exist_ok=True)
    config_path.write_text(tomlkit.dumps(document), encoding="utf-8")


def parse_config(document: tomlkit.TOMLDocument, config_path: Path = CONFIG_PATH) -> AppConfig:
    rayplus = _required_table(document, "rayplus")
    codex = _required_table(document, "codex")
    sqlite = _required_table(document, "sqlite")
    reset = _required_table(document, "reset")
    polling = _required_table(document, "polling")
    logs = _required_table(document, "logs")

    subscription_polling = _subscription_polling_config(polling)
    sleep_polling = _sleep_polling_config(polling)
    after_reset_email_polling = _after_reset_email_polling_config(polling)

    app_config = AppConfig(
        path=config_path,
        rayplus=RayPlusConfig(
            base_url=_required_str(rayplus, "base_url", "rayplus.base_url"),
            api_key=_required_str(rayplus, "api_key", "rayplus.api_key"),
            email=_required_str(rayplus, "email", "rayplus.email"),
            password=_required_str(rayplus, "password", "rayplus.password"),
            user_agent=_str_value(rayplus, "user_agent", "auto-reset-remaining-python/0.1"),
            balance_json_path=_str_value(rayplus, "balance_json_path", ""),
        ),
        codex=CodexConfig(
            base_url=_required_str(codex, "base_url", "codex.base_url"),
            subscription_id=_int_value(codex, "subscription_id", 0, "codex.subscription_id"),
        ),
        sqlite=SQLiteConfig(
            path=Path(_required_str(sqlite, "path", "sqlite.path")),
        ),
        reset=ResetConfig(
            low_balance_threshold=_float_value(reset, "low_balance_threshold", 0.5, "reset.low_balance_threshold"),
            auto_reset_enabled=_bool_value(reset, "auto_reset_enabled", False, "reset.auto_reset_enabled"),
            manual_confirm_success_count=_int_value(reset, "manual_confirm_success_count", 0, "reset.manual_confirm_success_count"),
            daily_max_reset_count=_int_value(reset, "daily_max_reset_count", 0, "reset.daily_max_reset_count"),
            confirm_request_ttl_seconds=parse_duration(_str_value(reset, "confirm_request_ttl", "24h"), "reset.confirm_request_ttl"),
            cooldown_seconds=parse_duration(_str_value(reset, "cooldown", "1m"), "reset.cooldown"),
            manual_confirm_time_range=_str_value(reset, "manual_confirm_time_range", ""),
        ),
        polling=PollingConfig(
            default_interval_seconds=parse_duration(_str_value(polling, "default_interval", "10s"), "polling.default_interval"),
            balance_change_epsilon=_float_value(polling, "balance_change_epsilon", 0.000001, "polling.balance_change_epsilon"),
            subscription=subscription_polling,
            sleep=sleep_polling,
            after_reset_email=after_reset_email_polling,
        ),
        logs=LogsConfig(
            query_log_dir=Path(_required_str(logs, "query_log_dir", "logs.query_log_dir")),
        ),
    )
    validate_config(app_config)
    return app_config


def validate_config(config: AppConfig) -> None:
    missing: list[str] = []
    placeholder_values = {
        "sk-your-api-key": "rayplus.api_key",
        "you@example.com": "rayplus.email",
        "your-password": "rayplus.password",
    }
    for value, field in placeholder_values.items():
        if value in {
            config.rayplus.api_key.strip(),
            config.rayplus.email.strip(),
            config.rayplus.password.strip(),
        }:
            missing.append(field)
    if config.reset.low_balance_threshold <= 0:
        raise ConfigError("reset.low_balance_threshold 必须大于 0")
    if config.reset.manual_confirm_success_count < 0:
        raise ConfigError("reset.manual_confirm_success_count 不能小于 0")
    if config.reset.daily_max_reset_count < 0:
        raise ConfigError("reset.daily_max_reset_count 不能小于 0")
    if config.reset.confirm_request_ttl_seconds <= 0:
        raise ConfigError("reset.confirm_request_ttl 必须大于 0")
    if config.reset.cooldown_seconds < 0:
        raise ConfigError("reset.cooldown 不能小于 0")
    if config.polling.default_interval_seconds <= 0:
        raise ConfigError("polling.default_interval 必须大于 0")
    if config.polling.balance_change_epsilon <= 0:
        raise ConfigError("polling.balance_change_epsilon 必须大于 0")
    if config.polling.subscription.enabled:
        if config.polling.subscription.quota <= 0:
            raise ConfigError("polling.subscription.quota 必须大于 0")
        if not config.polling.subscription.tiers:
            raise ConfigError("polling.subscription.tiers 至少需要配置一档")
    for tier in config.polling.subscription.tiers:
        if tier.min_ratio < 0:
            raise ConfigError("polling.subscription.tiers.min_ratio 不能小于 0")
        if tier.interval_seconds <= 0:
            raise ConfigError("polling.subscription.tiers.interval 必须大于 0")
    if config.polling.sleep.enabled:
        if config.polling.sleep.unchanged_for_seconds <= 0:
            raise ConfigError("polling.sleep.unchanged_for 必须大于 0")
        if config.polling.sleep.interval_seconds <= 0:
            raise ConfigError("polling.sleep.interval 必须大于 0")
    if config.polling.after_reset_email.enabled and config.polling.after_reset_email.interval_seconds <= 0:
        raise ConfigError("polling.after_reset_email.interval 必须大于 0")
    if config.reset.manual_confirm_time_range.strip():
        parse_manual_confirm_range(config.reset.manual_confirm_time_range)
    if missing:
        raise ConfigError("请先在配置窗口填写真实配置：" + "、".join(sorted(set(missing))))


def update_runtime_state(
    config_path: Path,
    manual_confirm_success_count: int,
    auto_reset_enabled: bool,
) -> None:
    document = load_document(config_path)
    reset = _required_table(document, "reset")
    reset["manual_confirm_success_count"] = manual_confirm_success_count
    reset["auto_reset_enabled"] = auto_reset_enabled
    save_document(document, config_path)


def parse_duration(value: str, field_name: str = "duration") -> float:
    match = _DURATION_RE.match(value)
    if not match:
        raise ConfigError(f"{field_name} 必须使用 ms/s/m/h 格式，例如 500ms、10s、1m、24h")
    amount = float(match.group(1))
    unit = match.group(2).lower()
    multiplier = {"ms": 0.001, "s": 1.0, "m": 60.0, "h": 3600.0}[unit]
    return amount * multiplier


def parse_manual_confirm_range(value: str) -> tuple[int, int]:
    parts = value.split("-")
    if len(parts) != 2:
        raise ConfigError("reset.manual_confirm_time_range 必须使用 start-end 格式，例如 22:00-09:00")
    start = _parse_time_of_day(parts[0])
    end = _parse_time_of_day(parts[1])
    if start == end:
        raise ConfigError("reset.manual_confirm_time_range 的开始和结束时间不能相同")
    return start, end


def is_time_in_manual_confirm_range(value: str, hour: int, minute: int) -> bool:
    if not value.strip():
        return False
    start, end = parse_manual_confirm_range(value)
    current = hour * 60 + minute
    if start < end:
        return start <= current < end
    return current >= start or current < end


def _subscription_polling_config(polling: Table) -> SubscriptionPollingConfig:
    table = _optional_table(polling, "subscription")
    if table is None:
        return SubscriptionPollingConfig(enabled=True, quota=100, tiers=DEFAULT_SUBSCRIPTION_TIERS)

    tiers: list[SubscriptionTier] = []
    raw_tiers = table.get("tiers", [])
    if raw_tiers is None:
        raw_tiers = []
    if not isinstance(raw_tiers, list):
        raise ConfigError("polling.subscription.tiers 必须是数组")
    if raw_tiers:
        for index, raw_tier in enumerate(raw_tiers):
            if not _is_table_like(raw_tier):
                raise ConfigError(f"polling.subscription.tiers[{index}] 必须是表")
            tiers.append(
                SubscriptionTier(
                    min_ratio=_float_value(raw_tier, "min_ratio", 0, f"polling.subscription.tiers[{index}].min_ratio"),
                    interval_seconds=parse_duration(
                        _str_value(raw_tier, "interval", ""),
                        f"polling.subscription.tiers[{index}].interval",
                    ),
                )
            )
    else:
        tiers.extend(DEFAULT_SUBSCRIPTION_TIERS)
    tiers.sort(key=lambda tier: tier.min_ratio, reverse=True)
    return SubscriptionPollingConfig(
        enabled=_bool_value(table, "enabled", False, "polling.subscription.enabled"),
        quota=_float_value(table, "quota", 0, "polling.subscription.quota"),
        tiers=tuple(tiers),
    )


def _sleep_polling_config(polling: Table) -> SleepPollingConfig:
    table = _optional_table(polling, "sleep")
    if table is None:
        return SleepPollingConfig(enabled=False, unchanged_for_seconds=600, interval_seconds=60)
    return SleepPollingConfig(
        enabled=_bool_value(table, "enabled", False, "polling.sleep.enabled"),
        unchanged_for_seconds=parse_duration(_str_value(table, "unchanged_for", "10m"), "polling.sleep.unchanged_for"),
        interval_seconds=parse_duration(_str_value(table, "interval", "1m"), "polling.sleep.interval"),
    )


def _after_reset_email_polling_config(polling: Table) -> AfterResetEmailPollingConfig:
    table = _optional_table(polling, "after_reset_email")
    if table is None:
        return AfterResetEmailPollingConfig(enabled=False, interval_seconds=60)
    return AfterResetEmailPollingConfig(
        enabled=_bool_value(table, "enabled", False, "polling.after_reset_email.enabled"),
        interval_seconds=parse_duration(_str_value(table, "interval", "1m"), "polling.after_reset_email.interval"),
    )


def _parse_time_of_day(value: str) -> int:
    value = value.strip()
    if not value:
        raise ConfigError("时间不能为空")
    parts = value.split(":")
    if len(parts) > 2:
        raise ConfigError(f"时间格式无效：{value}")
    try:
        hour = int(parts[0])
        minute = int(parts[1]) if len(parts) == 2 else 0
    except ValueError as exc:
        raise ConfigError(f"时间格式无效：{value}") from exc
    if hour < 0 or hour > 23 or minute < 0 or minute > 59:
        raise ConfigError(f"时间超出范围：{value}")
    return hour * 60 + minute


def _required_table(document: tomlkit.TOMLDocument, name: str) -> Any:
    value = document.get(name)
    if not _is_table_like(value):
        raise ConfigError(f"缺少配置段：[{name}]")
    return value


def _optional_table(table: Any, name: str) -> Any | None:
    value = table.get(name)
    if value is None:
        return None
    if not _is_table_like(value):
        raise ConfigError(f"polling.{name} 必须是配置段")
    return value


def _is_table_like(value: Any) -> bool:
    return isinstance(value, Table) or hasattr(value, "get")


def _required_str(table: Any, key: str, field_name: str) -> str:
    value = _str_value(table, key, "")
    if not value.strip():
        raise ConfigError(f"{field_name} 不能为空")
    return value.strip()


def _str_value(table: Any, key: str, fallback: str) -> str:
    value = table.get(key, fallback)
    if value is None:
        return fallback
    return str(value).strip()


def _int_value(table: Any, key: str, fallback: int, field_name: str) -> int:
    value: Any = table.get(key, fallback)
    try:
        return int(value)
    except (TypeError, ValueError) as exc:
        raise ConfigError(f"{field_name} 必须是整数") from exc


def _float_value(table: Any, key: str, fallback: float, field_name: str) -> float:
    value: Any = table.get(key, fallback)
    try:
        return float(value)
    except (TypeError, ValueError) as exc:
        raise ConfigError(f"{field_name} 必须是数字") from exc


def _bool_value(table: Any, key: str, fallback: bool, field_name: str) -> bool:
    value: Any = table.get(key, fallback)
    if isinstance(value, bool):
        return value
    if isinstance(value, str):
        normalized = value.strip().lower()
        if normalized in {"true", "1", "yes", "y"}:
            return True
        if normalized in {"false", "0", "no", "n"}:
            return False
    raise ConfigError(f"{field_name} 必须是布尔值")
