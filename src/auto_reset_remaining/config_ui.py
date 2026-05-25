from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
import tkinter as tk
from tkinter import messagebox
from tkinter import ttk

import tomlkit
from tomlkit.items import Table

from auto_reset_remaining.config import (
    CONFIG_PATH,
    AppConfig,
    ConfigError,
    ensure_config_file,
    load_config,
    load_document,
    parse_config,
    save_document,
)


@dataclass(frozen=True)
class FieldSpec:
    section: str
    key: str
    label: str
    secret: bool = False


FIELD_SPECS = [
    FieldSpec("rayplus", "base_url", "RayPlus 地址"),
    FieldSpec("rayplus", "api_key", "RayPlus API key", secret=True),
    FieldSpec("rayplus", "email", "RayPlus 登录邮箱"),
    FieldSpec("rayplus", "password", "RayPlus 登录密码", secret=True),
    FieldSpec("rayplus", "user_agent", "User-Agent"),
    FieldSpec("rayplus", "balance_json_path", "余额 JSON 路径"),
    FieldSpec("codex", "base_url", "Codex 地址"),
    FieldSpec("codex", "subscription_id", "订阅 ID（0 为自动选择）"),
    FieldSpec("sqlite", "path", "SQLite 文件路径"),
    FieldSpec("reset", "low_balance_threshold", "低余额阈值"),
    FieldSpec("reset", "auto_reset_enabled", "自动重置"),
    FieldSpec("reset", "manual_confirm_success_count", "人工确认成功次数"),
    FieldSpec("reset", "daily_max_reset_count", "每日最大重置次数（0 不限制）"),
    FieldSpec("reset", "confirm_request_ttl", "确认有效期"),
    FieldSpec("reset", "cooldown", "自动重置冷却"),
    FieldSpec("reset", "manual_confirm_time_range", "强制人工确认时间段"),
    FieldSpec("polling", "default_interval", "查询间隔"),
    FieldSpec("polling", "balance_change_epsilon", "余额变化容差"),
    FieldSpec("logs", "query_log_dir", "查询日志目录"),
]


def confirm_config_on_startup(config_path: Path = CONFIG_PATH) -> AppConfig:
    created = ensure_config_file(config_path)
    document = load_document(config_path)

    root = tk.Tk()
    root.title("auto_reset_remaining 配置确认")
    root.geometry("720x680")
    root.minsize(640, 560)

    result: dict[str, AppConfig] = {}
    variables: dict[tuple[str, str], tk.StringVar] = {}

    intro = "首次启动已从默认模板生成 config.toml，请确认配置后再进入主界面。" if created else "请确认 config.toml 的本地运行配置。"
    ttk.Label(root, text=intro, anchor="w").pack(fill="x", padx=16, pady=(16, 8))

    frame = ttk.Frame(root)
    frame.pack(fill="both", expand=True, padx=16, pady=8)

    canvas = tk.Canvas(frame, highlightthickness=0)
    scrollbar = ttk.Scrollbar(frame, orient="vertical", command=canvas.yview)
    form = ttk.Frame(canvas)
    form.bind("<Configure>", lambda _event: canvas.configure(scrollregion=canvas.bbox("all")))
    canvas.create_window((0, 0), window=form, anchor="nw")
    canvas.configure(yscrollcommand=scrollbar.set)
    canvas.pack(side="left", fill="both", expand=True)
    scrollbar.pack(side="right", fill="y")

    for row, spec in enumerate(FIELD_SPECS):
        value = _document_value(document, spec.section, spec.key)
        variable = tk.StringVar(value=value)
        variables[(spec.section, spec.key)] = variable
        ttk.Label(form, text=spec.label).grid(row=row, column=0, sticky="w", padx=(0, 12), pady=5)
        entry = ttk.Entry(form, textvariable=variable, show="*" if spec.secret else "")
        entry.grid(row=row, column=1, sticky="ew", pady=5)
    form.columnconfigure(1, weight=1)

    buttons = ttk.Frame(root)
    buttons.pack(fill="x", padx=16, pady=(8, 16))

    def save_and_continue() -> None:
        try:
            updated = _document_from_variables(document, variables)
            parsed = parse_config(updated, config_path)
            save_document(updated, config_path)
        except ConfigError as exc:
            messagebox.showerror("配置有误", str(exc), parent=root)
            return
        result["config"] = parsed
        root.destroy()

    def cancel() -> None:
        root.destroy()

    ttk.Button(buttons, text="确认并启动", command=save_and_continue).pack(side="right")
    ttk.Button(buttons, text="退出", command=cancel).pack(side="right", padx=(0, 8))

    root.protocol("WM_DELETE_WINDOW", cancel)
    root.mainloop()

    if "config" not in result:
        raise SystemExit("用户取消启动")
    return result["config"]


def load_or_confirm_config(config_path: Path = CONFIG_PATH) -> AppConfig:
    try:
        return load_config(config_path)
    except ConfigError:
        return confirm_config_on_startup(config_path)


def _document_value(document: tomlkit.TOMLDocument, section: str, key: str) -> str:
    table = document.get(section)
    if isinstance(table, Table) and key in table:
        value = table[key]
        if isinstance(value, bool):
            return "true" if value else "false"
        return str(value)
    return ""


def _document_from_variables(
    document: tomlkit.TOMLDocument,
    variables: dict[tuple[str, str], tk.StringVar],
) -> tomlkit.TOMLDocument:
    updated = tomlkit.parse(tomlkit.dumps(document))
    for (section, key), variable in variables.items():
        table = updated.get(section)
        if not isinstance(table, Table):
            table = tomlkit.table()
            updated[section] = table
        table[key] = _coerce_value(key, variable.get())
    return updated


def _coerce_value(key: str, value: str) -> object:
    value = value.strip()
    if key in {"subscription_id", "manual_confirm_success_count", "daily_max_reset_count"}:
        return int(value or "0")
    if key in {"low_balance_threshold", "balance_change_epsilon"}:
        return float(value or "0")
    if key == "auto_reset_enabled":
        return value.lower() in {"true", "1", "yes", "y"}
    return value
