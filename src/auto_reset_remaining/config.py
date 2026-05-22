from __future__ import annotations

import os
import re
from dataclasses import dataclass, field

from .envfile import load_env_file


@dataclass
class SMTPConfig:
    host: str = ""
    port: int = 587
    user: str = ""
    password: str = ""
    from_addr: str = ""
    to: list[str] = field(default_factory=list)


@dataclass
class PGConfig:
    host: str = ""
    port: int = 5432
    user: str = ""
    password: str = ""
    database: str = ""
    sslmode: str = "disable"


@dataclass
class Config:
    env_path: str = ".env"
    rayplus_base_url: str = "https://rayplus.site"
    rayplus_api_key: str = ""
    rayplus_email: str = ""
    rayplus_password: str = ""
    codex_base_url: str = "https://codex.rayplus.site"
    subscription_id: int = 0
    smtp: SMTPConfig = field(default_factory=SMTPConfig)
    pg: PGConfig = field(default_factory=PGConfig)
    public_base_url: str = ""
    http_addr: str = ":8080"
    query_log_dir: str = "logs"
    user_agent: str = "auto-reset-remaining/1.0"
    low_balance_threshold: float = 0.5
    balance_json_path: str = ""
    auto_reset_enabled: bool = False
    manual_confirm_success_count: int = 0
    poll_interval_seconds: float = 1.0
    confirm_token_ttl_seconds: float = 24 * 60 * 60
    reset_cooldown_seconds: float = 60.0

    def validate(self) -> None:
        missing: list[str] = []
        required = {
            "RAYPLUS_BASE_URL": self.rayplus_base_url,
            "RAYPLUS_API_KEY": self.rayplus_api_key,
            "RAYPLUS_EMAIL": self.rayplus_email,
            "RAYPLUS_PASSWORD": self.rayplus_password,
            "CODEX_BASE_URL": self.codex_base_url,
            "PUBLIC_BASE_URL": self.public_base_url,
            "SMTP_HOST": self.smtp.host,
            "SMTP_FROM": self.smtp.from_addr,
            "pg_host": self.pg.host,
            "pg_user": self.pg.user,
            "pg_database": self.pg.database,
            "pg_sslmode": self.pg.sslmode,
        }
        for key, value in required.items():
            if not str(value).strip():
                missing.append(key)
        if not self.smtp.to:
            missing.append("SMTP_TO")
        if missing:
            raise ValueError(f"missing required config: {', '.join(missing)}")
        if not 0 < self.smtp.port <= 65535:
            raise ValueError("SMTP_PORT must be between 1 and 65535")
        if not 0 < self.pg.port <= 65535:
            raise ValueError("pg_port must be between 1 and 65535")
        if self.low_balance_threshold <= 0:
            raise ValueError("LOW_BALANCE_THRESHOLD must be greater than 0")
        if self.poll_interval_seconds <= 0:
            raise ValueError("POLL_INTERVAL must be greater than 0")
        if self.confirm_token_ttl_seconds <= 0:
            raise ValueError("CONFIRM_TOKEN_TTL must be greater than 0")

    def postgres_kwargs(self) -> dict[str, object]:
        kwargs: dict[str, object] = {
            "host": self.pg.host,
            "port": self.pg.port,
            "user": self.pg.user,
            "dbname": self.pg.database,
            "sslmode": self.pg.sslmode,
        }
        if self.pg.password:
            kwargs["password"] = self.pg.password
        return kwargs

    def http_bind(self) -> tuple[str, int]:
        value = self.http_addr.strip()
        if value.startswith(":"):
            return "", int(value[1:])
        if ":" not in value:
            return value, 8080
        host, port = value.rsplit(":", 1)
        return host, int(port)


def load_config(path: str = ".env") -> Config:
    file_values = load_env_file(path)

    def lookup(key: str) -> str:
        return os.environ.get(key, file_values.get(key, "")).strip()

    cfg = Config(
        env_path=path,
        rayplus_base_url=lookup("RAYPLUS_BASE_URL") or "https://rayplus.site",
        rayplus_api_key=lookup("RAYPLUS_API_KEY"),
        rayplus_email=lookup("RAYPLUS_EMAIL"),
        rayplus_password=lookup("RAYPLUS_PASSWORD"),
        codex_base_url=lookup("CODEX_BASE_URL") or "https://codex.rayplus.site",
        subscription_id=_int_value(lookup("SUBSCRIPTION_ID"), 0),
        smtp=SMTPConfig(
            host=lookup("SMTP_HOST"),
            port=_int_value(lookup("SMTP_PORT"), 587),
            user=lookup("SMTP_USER"),
            password=lookup("SMTP_PASSWORD"),
            from_addr=lookup("SMTP_FROM"),
            to=_split_recipients(lookup("SMTP_TO")),
        ),
        pg=PGConfig(
            host=lookup("pg_host"),
            port=_int_value(lookup("pg_port"), 5432),
            user=lookup("pg_user"),
            password=lookup("pg_password"),
            database=lookup("pg_database"),
            sslmode=lookup("pg_sslmode") or "disable",
        ),
        public_base_url=lookup("PUBLIC_BASE_URL").rstrip("/"),
        http_addr=lookup("HTTP_ADDR") or ":8080",
        query_log_dir=lookup("QUERY_LOG_DIR") or "logs",
        low_balance_threshold=_float_value(lookup("LOW_BALANCE_THRESHOLD"), 0.5),
        balance_json_path=lookup("BALANCE_JSON_PATH"),
        auto_reset_enabled=_bool_value(lookup("AUTO_RESET_ENABLED"), False),
        manual_confirm_success_count=_int_value(lookup("MANUAL_CONFIRM_SUCCESS_COUNT"), 0),
        poll_interval_seconds=_duration_seconds(lookup("POLL_INTERVAL"), 1.0),
        confirm_token_ttl_seconds=_duration_seconds(lookup("CONFIRM_TOKEN_TTL"), 24 * 60 * 60),
        reset_cooldown_seconds=_duration_seconds(lookup("RESET_COOLDOWN"), 60.0),
        user_agent=lookup("USER_AGENT") or "auto-reset-remaining/1.0",
    )
    return cfg


def _split_recipients(value: str) -> list[str]:
    return [part.strip() for part in re.split(r"[,;\n]", value) if part.strip()]


def _bool_value(value: str, default: bool) -> bool:
    if not value:
        return default
    return value.lower() in {"1", "true", "yes", "y", "on"}


def _int_value(value: str, default: int) -> int:
    if not value:
        return default
    try:
        return int(value)
    except ValueError:
        return default


def _float_value(value: str, default: float) -> float:
    if not value:
        return default
    try:
        return float(value)
    except ValueError:
        return default


def _duration_seconds(value: str, default: float) -> float:
    if not value:
        return default
    match = re.fullmatch(r"\s*(\d+(?:\.\d+)?)(ms|s|m|h)?\s*", value)
    if not match:
        return default
    amount = float(match.group(1))
    unit = match.group(2) or "s"
    multipliers = {"ms": 0.001, "s": 1.0, "m": 60.0, "h": 3600.0}
    return amount * multipliers[unit]
