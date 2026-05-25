# auto_reset_remaining Python 本地版

这是一个面向 Windows 本地运行的 Python 小工具，用来监控 RayPlus API 余额，并在余额不足时辅助重置 Codex 订阅额度。

本项目功能参考自原项目：[tiankongzhise/auto_reset_remaining](https://github.com/tiankongzhise/auto_reset_remaining)。

## 功能

- 使用 `uv` 管理 Python 环境和依赖。
- 使用 Tkinter 提供简单本地 UI。
- 使用 SQLite 保存本地确认、重置和每日限制记录。
- 使用 Windows 本地弹窗代替 Web 服务和邮件系统。
- 保留余额监控、低余额确认、自动重置、每日重置上限和查询日志。

本地版不包含原 Go 服务、HTTP 接口、SMTP 邮件和 PostgreSQL 依赖。

## 环境准备

需要：

- Windows
- `uv`
- Python 3.12 或由 `uv` 管理的 Python

如果当前机器上 `uv` 的默认缓存目录不可写，可以临时指定本项目内的缓存目录：

```powershell
$env:UV_CACHE_DIR=(Join-Path (Get-Location) ".uv-cache")
$env:UV_PYTHON_INSTALL_DIR=(Join-Path (Get-Location) ".uv-python")
```

依赖添加和移除请使用：

```powershell
uv add <package>
uv remove <package>
```

## 运行方式

```powershell
uv run auto-reset-remaining
```

首次启动会基于 `config.example.toml` 生成本地 `config.toml`，并打开配置确认窗口。`config.toml` 可能包含账号、密码和 API key，不会提交到 Git。

主界面提供：

- 启动监控
- 暂停监控
- 立即查询
- 手动重置
- 最近事件查看

余额低于阈值时，程序会通过本地弹窗询问是否立即重置订阅额度。

## 配置说明

`config.example.toml` 是默认模板。首次启动后请在配置窗口中确认：

- `rayplus.api_key`：RayPlus API key
- `rayplus.email` / `rayplus.password`：RayPlus 登录信息
- `codex.subscription_id`：指定订阅 ID；填 `0` 时自动选择 active 且可重置的订阅
- `sqlite.path`：本地 SQLite 文件路径，默认 `data/auto_reset_remaining.sqlite3`
- `reset.low_balance_threshold`：低余额确认阈值
- `reset.auto_reset_enabled`：是否启用自动重置
- `reset.daily_max_reset_count`：每日最大重置次数；`0` 表示不限制
- `polling.default_interval`：余额查询间隔
- `logs.query_log_dir`：每日 JSONL 查询日志目录

人工确认重置成功累计 3 次后，程序会自动把 `reset.auto_reset_enabled` 写为 `true`。

## 测试

```powershell
uv lock
uv run python -m unittest discover
```

如果本机没有可被 `uv` 自动发现的 Python，可显式指定解释器：

```powershell
uv run --python "C:\Path\To\python.exe" python -m unittest discover
```

## Git 约定

仓库只提交 Python 本地版文件，不提交原 Go 项目源码。运行时生成的 `config.toml`、`data/`、`logs/`、`.venv/` 和 uv 本地缓存目录均已忽略。
