from __future__ import annotations

from dataclasses import dataclass
from pathlib import Path
import tkinter as tk
from tkinter import messagebox
from tkinter import ttk
from typing import Protocol

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

FieldPath = tuple[str, ...]


class ValueVariable(Protocol):
    def get(self) -> str:
        ...


@dataclass(frozen=True)
class FieldSpec:
    path: FieldPath
    label: str
    secret: bool = False


FIELD_SPECS = [
    FieldSpec(("rayplus", "base_url"), "RayPlus 地址"),
    FieldSpec(("rayplus", "api_key"), "RayPlus API key", secret=True),
    FieldSpec(("rayplus", "email"), "RayPlus 登录邮箱"),
    FieldSpec(("rayplus", "password"), "RayPlus 登录密码", secret=True),
    FieldSpec(("rayplus", "user_agent"), "User-Agent"),
    FieldSpec(("rayplus", "balance_json_path"), "余额 JSON 路径"),
    FieldSpec(("codex", "base_url"), "Codex 地址"),
    FieldSpec(("codex", "subscription_id"), "订阅 ID（0 为自动选择）"),
    FieldSpec(("sqlite", "path"), "SQLite 文件路径"),
    FieldSpec(("reset", "low_balance_threshold"), "低余额阈值"),
    FieldSpec(("reset", "auto_reset_enabled"), "自动重置"),
    FieldSpec(("reset", "manual_confirm_success_count"), "人工确认成功次数"),
    FieldSpec(("reset", "daily_max_reset_count"), "每日最大重置次数（0 不限制）"),
    FieldSpec(("reset", "confirm_request_ttl"), "确认有效期"),
    FieldSpec(("reset", "cooldown"), "自动重置冷却"),
    FieldSpec(("reset", "manual_confirm_time_range"), "强制人工确认时间段"),
    FieldSpec(("polling", "default_interval"), "查询间隔"),
    FieldSpec(("polling", "balance_change_epsilon"), "余额变化容差"),
    FieldSpec(("polling", "subscription", "enabled"), "动态分档查询"),
    FieldSpec(("polling", "subscription", "quota"), "订阅总额度"),
    FieldSpec(("polling", "sleep", "enabled"), "余额不变降频"),
    FieldSpec(("polling", "sleep", "unchanged_for"), "余额不变时长"),
    FieldSpec(("polling", "sleep", "interval"), "余额不变查询间隔"),
    FieldSpec(("polling", "after_reset_email", "enabled"), "确认请求后固定轮询"),
    FieldSpec(("polling", "after_reset_email", "interval"), "确认请求后查询间隔"),
    FieldSpec(("logs", "query_log_dir"), "查询日志目录"),
]


def confirm_config_on_startup(config_path: Path = CONFIG_PATH) -> AppConfig:
    created = ensure_config_file(config_path)
    document = load_document(config_path)

    root = tk.Tk()
    root.title("auto_reset_remaining 配置确认")
    root.geometry("720x680")
    root.minsize(640, 560)

    result: dict[str, AppConfig] = {}
    variables: dict[FieldPath, tk.StringVar] = {}
    tier_variables: list[tuple[tk.StringVar, tk.StringVar]] = []

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
        value = _document_value(document, spec.path)
        variable = tk.StringVar(value=value)
        variables[spec.path] = variable
        ttk.Label(form, text=spec.label).grid(row=row, column=0, sticky="w", padx=(0, 12), pady=5)
        entry = ttk.Entry(form, textvariable=variable, show="*" if spec.secret else "")
        entry.grid(row=row, column=1, sticky="ew", pady=5)

    tier_start_row = len(FIELD_SPECS)
    ttk.Label(form, text="订阅分档").grid(row=tier_start_row, column=0, sticky="nw", padx=(0, 12), pady=5)
    tiers_frame = ttk.Frame(form)
    tiers_frame.grid(row=tier_start_row, column=1, sticky="ew", pady=5)
    tiers_frame.columnconfigure(0, weight=1)
    tiers_frame.columnconfigure(1, weight=1)

    def render_tiers() -> None:
        for child in tiers_frame.winfo_children():
            child.destroy()
        ttk.Label(tiers_frame, text="min_ratio").grid(row=0, column=0, sticky="w", padx=(0, 8))
        ttk.Label(tiers_frame, text="interval").grid(row=0, column=1, sticky="w", padx=(0, 8))
        for index, (min_ratio_var, interval_var) in enumerate(tier_variables, start=1):
            ttk.Entry(tiers_frame, textvariable=min_ratio_var).grid(row=index, column=0, sticky="ew", padx=(0, 8), pady=3)
            ttk.Entry(tiers_frame, textvariable=interval_var).grid(row=index, column=1, sticky="ew", padx=(0, 8), pady=3)
            ttk.Button(
                tiers_frame,
                text="删除",
                command=lambda row_index=index - 1: delete_tier(row_index),
            ).grid(row=index, column=2, sticky="ew", pady=3)
        ttk.Button(tiers_frame, text="新增分档", command=add_tier).grid(row=len(tier_variables) + 1, column=0, sticky="w", pady=(6, 0))

    def add_tier() -> None:
        tier_variables.append((tk.StringVar(value="0"), tk.StringVar(value="1m")))
        render_tiers()

    def delete_tier(index: int) -> None:
        del tier_variables[index]
        render_tiers()

    for min_ratio, interval in _document_tier_values(document):
        tier_variables.append((tk.StringVar(value=min_ratio), tk.StringVar(value=interval)))
    render_tiers()
    form.columnconfigure(1, weight=1)

    buttons = ttk.Frame(root)
    buttons.pack(fill="x", padx=16, pady=(8, 16))

    def save_and_continue() -> None:
        try:
            updated = _document_from_variables(document, variables, tier_variables)
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


def _document_value(document: tomlkit.TOMLDocument, path: FieldPath) -> str:
    table: object = document
    for key in path[:-1]:
        if not _is_table_like(table):
            return ""
        table = table.get(key)  # type: ignore[attr-defined]
    if _is_table_like(table) and path[-1] in table:
        value = table[path[-1]]
        if isinstance(value, bool):
            return "true" if value else "false"
        return str(value)
    return ""


def _document_tier_values(document: tomlkit.TOMLDocument) -> list[tuple[str, str]]:
    subscription = _nested_table(document, ("polling", "subscription"))
    if subscription is None:
        return []
    raw_tiers = subscription.get("tiers", [])
    if not raw_tiers:
        return []
    values: list[tuple[str, str]] = []
    for raw_tier in raw_tiers:
        if not _is_table_like(raw_tier):
            continue
        values.append((str(raw_tier.get("min_ratio", "")), str(raw_tier.get("interval", ""))))
    return values


def _document_from_variables(
    document: tomlkit.TOMLDocument,
    variables: dict[FieldPath, ValueVariable],
    tier_variables: list[tuple[ValueVariable, ValueVariable]] | None = None,
) -> tomlkit.TOMLDocument:
    updated = tomlkit.parse(tomlkit.dumps(document))
    for path, variable in variables.items():
        table = _ensure_nested_table(updated, path[:-1])
        table[path[-1]] = _coerce_value(path, variable.get())
    if tier_variables is not None:
        subscription = _ensure_nested_table(updated, ("polling", "subscription"))
        tiers = tomlkit.aot()
        for min_ratio_var, interval_var in tier_variables:
            tier = tomlkit.table()
            tier["min_ratio"] = _coerce_value(("polling", "subscription", "tiers", "min_ratio"), min_ratio_var.get())
            tier["interval"] = interval_var.get().strip()
            tiers.append(tier)
        subscription["tiers"] = tiers
    return updated


def _coerce_value(path: FieldPath, value: str) -> object:
    value = value.strip()
    key = path[-1]
    try:
        if path in {
            ("codex", "subscription_id"),
            ("reset", "manual_confirm_success_count"),
            ("reset", "daily_max_reset_count"),
        }:
            return int(value or "0")
        if path in {
            ("reset", "low_balance_threshold"),
            ("polling", "balance_change_epsilon"),
            ("polling", "subscription", "quota"),
            ("polling", "subscription", "tiers", "min_ratio"),
        }:
            return float(value or "0")
    except ValueError as exc:
        raise ConfigError(f"{'.'.join(path)} 必须填写数字") from exc
    if path in {
        ("reset", "auto_reset_enabled"),
        ("polling", "subscription", "enabled"),
        ("polling", "sleep", "enabled"),
        ("polling", "after_reset_email", "enabled"),
    }:
        return value.lower() in {"true", "1", "yes", "y"}
    return value


def _nested_table(document: tomlkit.TOMLDocument, path: FieldPath) -> object | None:
    table: object = document
    for key in path:
        if not _is_table_like(table):
            return None
        table = table.get(key)  # type: ignore[attr-defined]
    return table if _is_table_like(table) else None


def _ensure_nested_table(document: tomlkit.TOMLDocument, path: FieldPath) -> Table:
    table: object = document
    for key in path:
        if not _is_table_like(table):
            raise ConfigError(f"{'.'.join(path)} 必须是配置段")
        child = table.get(key)  # type: ignore[attr-defined]
        if not _is_table_like(child):
            child = tomlkit.table()
            table[key] = child  # type: ignore[index]
        table = child
    if not isinstance(table, Table):
        raise ConfigError(f"{'.'.join(path)} 必须是配置段")
    return table


def _is_table_like(value: object) -> bool:
    return isinstance(value, Table) or hasattr(value, "get")
