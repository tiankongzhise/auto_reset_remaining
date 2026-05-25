from __future__ import annotations

from datetime import datetime
import queue
import threading
import tkinter as tk
from tkinter import messagebox
from tkinter import ttk

from auto_reset_remaining.api import APIClient
from auto_reset_remaining.config import AppConfig
from auto_reset_remaining.monitor import CONFIRM_RESULT_CANCELLED, CONFIRM_RESULT_CONFIRMED, Monitor, MonitorEvent
from auto_reset_remaining.query_log import QueryLogger
from auto_reset_remaining.store import SQLiteStore


class MainWindow:
    def __init__(self, config: AppConfig) -> None:
        self.config = config
        self.root = tk.Tk()
        self.root.title("auto_reset_remaining 本地版")
        self.root.geometry("780x520")
        self.root.minsize(700, 460)

        self.events: queue.Queue[MonitorEvent | tuple[str, object]] = queue.Queue()
        self.store = SQLiteStore(config.sqlite.path)
        self.store.init()
        self.monitor = Monitor(
            config=config,
            api_client=APIClient(config),
            store=self.store,
            query_logger=QueryLogger(config.logs.query_log_dir),
            confirm_callback=self._confirm_reset,
            event_callback=self.events.put,
        )

        self.balance_var = tk.StringVar(value="--")
        self.status_var = tk.StringVar(value="未启动")
        self.auto_reset_var = tk.StringVar(value="开启" if config.reset.auto_reset_enabled else "关闭")
        self.manual_count_var = tk.StringVar(value=str(config.reset.manual_confirm_success_count))
        self.last_error_var = tk.StringVar(value="")

        self._build()
        self.root.protocol("WM_DELETE_WINDOW", self._on_close)
        self.root.after(300, self._drain_events)
        self.root.after(1000, self._refresh_snapshot)

    def run(self) -> None:
        self.root.mainloop()

    def _build(self) -> None:
        outer = ttk.Frame(self.root, padding=16)
        outer.pack(fill="both", expand=True)

        summary = ttk.LabelFrame(outer, text="运行状态", padding=12)
        summary.pack(fill="x")
        self._add_value(summary, 0, "当前余额", self.balance_var)
        self._add_value(summary, 1, "监控状态", self.status_var)
        self._add_value(summary, 2, "自动重置", self.auto_reset_var)
        self._add_value(summary, 3, "人工确认次数", self.manual_count_var)

        controls = ttk.Frame(outer)
        controls.pack(fill="x", pady=12)
        ttk.Button(controls, text="启动监控", command=self.monitor.start).pack(side="left")
        ttk.Button(controls, text="暂停监控", command=self.monitor.stop).pack(side="left", padx=8)
        ttk.Button(controls, text="立即查询", command=self._run_tick_once).pack(side="left")
        ttk.Button(controls, text="手动重置", command=self._manual_reset).pack(side="left", padx=8)
        ttk.Button(controls, text="退出", command=self._on_close).pack(side="right")

        config_box = ttk.LabelFrame(outer, text="本地配置", padding=12)
        config_box.pack(fill="x")
        ttk.Label(config_box, text=f"SQLite：{self.config.sqlite.path}").pack(anchor="w")
        ttk.Label(config_box, text=f"日志目录：{self.config.logs.query_log_dir}").pack(anchor="w", pady=(4, 0))
        ttk.Label(config_box, text=f"低余额阈值：{self.config.reset.low_balance_threshold}").pack(anchor="w", pady=(4, 0))

        log_box = ttk.LabelFrame(outer, text="最近事件", padding=12)
        log_box.pack(fill="both", expand=True, pady=(12, 0))
        self.event_list = tk.Listbox(log_box, height=10)
        self.event_list.pack(fill="both", expand=True)

        error_label = ttk.Label(outer, textvariable=self.last_error_var, foreground="#b00020")
        error_label.pack(fill="x", pady=(8, 0))

    @staticmethod
    def _add_value(parent: ttk.Frame, row: int, label: str, variable: tk.StringVar) -> None:
        ttk.Label(parent, text=label).grid(row=row, column=0, sticky="w", padx=(0, 12), pady=4)
        ttk.Label(parent, textvariable=variable).grid(row=row, column=1, sticky="w", pady=4)
        parent.columnconfigure(1, weight=1)

    def _run_tick_once(self) -> None:
        threading.Thread(target=self.monitor.tick_once, name="manual-tick", daemon=True).start()

    def _manual_reset(self) -> None:
        if not messagebox.askyesno("确认手动重置", "确定要立即查询余额并手动重置订阅额度吗？", parent=self.root):
            return
        threading.Thread(target=self._manual_reset_worker, name="manual-reset", daemon=True).start()

    def _manual_reset_worker(self) -> None:
        try:
            result = self.monitor.manual_reset()
            self.events.put(MonitorEvent("info", f"手动重置成功，订阅 ID {result.subscription_id}", status="manual_reset_success"))
        except Exception as exc:
            self.events.put(MonitorEvent("error", f"手动重置失败：{exc}", status="manual_reset_error"))

    def _confirm_reset(self, balance: float, expires_at: datetime, reason: str) -> str:
        response_queue: queue.Queue[str] = queue.Queue(maxsize=1)
        self.events.put(("confirm", (balance, expires_at, reason, response_queue)))
        return response_queue.get()

    def _drain_events(self) -> None:
        while True:
            try:
                event = self.events.get_nowait()
            except queue.Empty:
                break
            if isinstance(event, tuple) and event[0] == "confirm":
                self._handle_confirm_event(event[1])
                continue
            self._handle_monitor_event(event)
        self.root.after(300, self._drain_events)

    def _handle_confirm_event(self, payload: object) -> None:
        balance, expires_at, reason, response_queue = payload  # type: ignore[misc]
        message = (
            f"当前余额 {balance:.6f} 已触发 {reason}。\n\n"
            f"确认请求有效期至 {expires_at:%Y-%m-%d %H:%M:%S}。\n"
            "是否现在重置订阅额度？"
        )
        confirmed = messagebox.askyesno("余额不足，请确认重置", message, parent=self.root)
        response_queue.put(CONFIRM_RESULT_CONFIRMED if confirmed else CONFIRM_RESULT_CANCELLED)

    def _handle_monitor_event(self, event: MonitorEvent) -> None:
        timestamp = datetime.now().strftime("%H:%M:%S")
        self.event_list.insert(0, f"[{timestamp}] {event.message}")
        if self.event_list.size() > 100:
            self.event_list.delete(100, tk.END)
        if event.balance is not None:
            self.balance_var.set(f"{event.balance:.6f}")
        if event.status:
            self.status_var.set(event.status)
        if event.kind == "error":
            self.last_error_var.set(event.message)
            messagebox.showerror("auto_reset_remaining", event.message, parent=self.root)
        elif event.kind == "warning":
            messagebox.showwarning("auto_reset_remaining", event.message, parent=self.root)
        elif event.status and event.status.endswith("_success"):
            messagebox.showinfo("auto_reset_remaining", event.message, parent=self.root)

    def _refresh_snapshot(self) -> None:
        snapshot = self.monitor.snapshot()
        self.status_var.set(snapshot.status)
        self.auto_reset_var.set("开启" if snapshot.auto_reset_enabled else "关闭")
        self.manual_count_var.set(str(snapshot.manual_confirm_success_count))
        if snapshot.last_balance is not None:
            self.balance_var.set(f"{snapshot.last_balance:.6f}")
        if snapshot.last_error:
            self.last_error_var.set(snapshot.last_error)
        self.root.after(1000, self._refresh_snapshot)

    def _on_close(self) -> None:
        self.monitor.stop()
        self.monitor.join(timeout=2)
        self.store.close()
        self.root.destroy()


def run_main_window(config: AppConfig) -> None:
    MainWindow(config).run()
