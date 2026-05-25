# auto_reset_remaining Python 本地版

这是一个面向 Windows 本地运行的 Python 工具，用来定时查询 RayPlus API 余额，并在余额不足时通过本地窗口辅助重置 Codex 订阅额度。

本项目功能参考自原项目：[tiankongzhise/auto_reset_remaining](https://github.com/tiankongzhise/auto_reset_remaining)。当前版本是新的 Python 本地版，不包含原 Go 服务、HTTP 接口、SMTP 邮件和 PostgreSQL 依赖。

## 适合谁使用

- 想在本机后台监控 RayPlus 余额。
- 不想部署 Web 服务、数据库服务或邮件系统。
- 希望低余额时由 Windows 弹窗确认是否重置。
- 希望用 SQLite 在本地保存确认、重置和每日限制记录。

## 功能概览

- `uv` 管理 Python 环境、依赖和运行入口。
- Tkinter 本地 UI，启动时确认配置，运行时显示状态。
- SQLite 本地数据库，默认保存到 `data/auto_reset_remaining.sqlite3`。
- 低余额弹窗确认：点“是”立即重置，点“否”会取消本次确认并抑制重复弹窗。
- 自动重置：开启后余额小于等于 `0` 时自动重置。
- 每日重置上限：可限制每天成功重置次数。
- 查询日志：按天写入 `query-YYYY-MM-DD.jsonl`。

## 环境准备

需要准备：

- Windows
- `uv`
- Python 3.12 或由 `uv` 自动管理的 Python
- RayPlus API Key
- RayPlus 登录邮箱和密码

本项目只通过 `uv` 管理依赖。添加或移除依赖时请使用：

```powershell
uv add <package>
uv remove <package>
```

如果当前机器上 `uv` 的默认缓存目录不可写，可以在项目目录临时指定本地缓存目录：

```powershell
$env:UV_CACHE_DIR=(Join-Path (Get-Location) ".uv-cache")
$env:UV_PYTHON_INSTALL_DIR=(Join-Path (Get-Location) ".uv-python")
```

如果本机没有被 `uv` 自动发现的 Python，也可以显式指定解释器运行：

```powershell
uv run --python "C:\Path\To\python.exe" auto-reset-remaining
```

## 快速开始

1. 进入项目目录：

```powershell
cd C:\Users\3700x\Desktop\ai\auto_reset_python
```

2. 启动程序：

```powershell
uv run auto-reset-remaining
```

3. 首次启动会自动复制 `config.example.toml` 为 `config.toml`，并打开配置确认窗口。

4. 在配置窗口中至少填写：

- `RayPlus API key`
- `RayPlus 登录邮箱`
- `RayPlus 登录密码`

5. 点击“确认并启动”，进入主界面。

6. 点击“启动监控”，程序开始按配置轮询余额。

## 主界面怎么用

主界面会显示：

- 当前余额
- 监控状态
- 自动重置是否开启
- 人工确认成功次数
- SQLite 文件路径
- 查询日志目录
- 最近事件

按钮说明：

- “启动监控”：按 `polling.default_interval` 定时查询余额。
- “暂停监控”：暂停后台轮询。
- “立即查询”：马上执行一次余额查询和判断。
- “手动重置”：主动查询余额并执行一次重置。
- “退出”：停止监控并关闭程序。

低余额弹窗行为：

- 点“是”：执行重置。
- 点“否”：本次确认请求会被标记为 `manual_cancelled`，程序提示需要手动发起重置；在余额被重置或恢复到阈值以上之前，不会重复弹出确认窗口。

## 配置文件位置

模板文件：

```text
config.example.toml
```

本地真实配置：

```text
config.toml
```

`config.toml` 会包含 API key、邮箱和密码，已被 `.gitignore` 忽略，请不要提交。

## 配置项说明

### rayplus

`rayplus.base_url`

RayPlus API 基础地址。通常保持默认：

```toml
base_url = "https://rayplus.site"
```

`rayplus.api_key`

RayPlus API Key，用于查询当前余额。必须替换为自己的真实 key。

`rayplus.email`

RayPlus 登录邮箱。程序会用它登录并获取 Codex 重置接口需要的访问令牌。

`rayplus.password`

RayPlus 登录密码。只保存在本地 `config.toml`。

`rayplus.user_agent`

请求接口时发送的 User-Agent。通常保持默认即可。

`rayplus.balance_json_path`

余额字段的 JSON 路径。留空时程序会自动尝试常见字段，例如 `data.balance`、`balance`、`remaining`。如果查询时报“无法找到余额字段”，再填写明确路径。

### codex

`codex.base_url`

Codex 订阅接口基础地址。通常保持默认：

```toml
base_url = "https://codex.rayplus.site"
```

`codex.subscription_id`

要重置的订阅 ID。推荐先填 `0`，让程序自动选择 active 且可重置的订阅。

如果你有多个订阅，并且只想重置指定订阅，再填写具体 ID。

### sqlite

`sqlite.path`

SQLite 数据库文件路径。默认：

```toml
path = "data/auto_reset_remaining.sqlite3"
```

数据库里保存确认请求、重置日志、每日限制状态和取消弹窗后的抑制状态。

### reset

`reset.low_balance_threshold`

低余额阈值。未开启自动重置时，余额低于该值会弹窗请求确认。

推荐：

```toml
low_balance_threshold = 0.5
```

`reset.auto_reset_enabled`

是否启用自动重置。建议初次使用保持 `false`，先手动确认几次流程是否正常。

人工确认重置成功累计 3 次后，程序会自动把它改为 `true`。

`reset.manual_confirm_success_count`

人工确认重置成功次数。一般不要手动修改。

`reset.daily_max_reset_count`

每日最大成功重置次数。`0` 表示不限制。

建议：

- 不确定套餐刷新规则时填 `1` 或 `2`。
- 明确不需要限制时填 `0`。

`reset.confirm_request_ttl`

低余额确认请求有效期。支持 `ms`、`s`、`m`、`h`，例如：

```toml
confirm_request_ttl = "24h"
```

`reset.cooldown`

自动重置冷却时间，避免余额持续为 `0` 时频繁重复重置。推荐：

```toml
cooldown = "1m"
```

`reset.manual_confirm_time_range`

强制人工确认的本地时间段。留空表示不启用。

例如夜间不想自动重置，可以设置：

```toml
manual_confirm_time_range = "22:00-09:00"
```

### polling

`polling.default_interval`

余额查询间隔。支持 `ms`、`s`、`m`、`h`。

推荐先使用：

```toml
default_interval = "10s"
```

如果不需要频繁查询，可以改为 `30s` 或 `1m`。

`polling.balance_change_epsilon`

判断余额是否发生真实变化的最小差值，用于过滤浮点误差。通常保持默认：

```toml
balance_change_epsilon = 0.000001
```

### logs

`logs.query_log_dir`

查询日志目录。默认：

```toml
query_log_dir = "logs"
```

程序会写入类似：

```text
logs/query-2026-05-25.jsonl
```

## 推荐初始配置

第一次使用建议：

```toml
[reset]
low_balance_threshold = 0.5
auto_reset_enabled = false
manual_confirm_success_count = 0
daily_max_reset_count = 1
confirm_request_ttl = "24h"
cooldown = "1m"
manual_confirm_time_range = ""

[polling]
default_interval = "10s"
balance_change_epsilon = 0.000001
```

这样程序会先走人工确认流程，不会一上来自动重置。确认流程稳定后，累计 3 次人工确认成功会自动开启自动重置。

## 本地文件说明

这些文件和目录是运行时生成的，不会提交到 Git：

- `config.toml`：本地真实配置，包含机密信息。
- `data/`：SQLite 数据库目录。
- `logs/`：余额查询 JSONL 日志目录。
- `.venv/`：uv 创建的虚拟环境。
- `.uv-cache/`、`.uv-python/`：可选的本地 uv 缓存目录。

## 测试

```powershell
uv lock
uv run python -m unittest discover
```

如果需要显式指定 Python：

```powershell
uv run --python "C:\Path\To\python.exe" python -m unittest discover
```

## 常见问题

### 启动时提示配置占位符未替换

请在配置窗口中把 `sk-your-api-key`、`you@example.com`、`your-password` 替换成真实信息。

### 查询余额失败

检查：

- `rayplus.api_key` 是否正确。
- `rayplus.base_url` 是否可访问。
- 如果接口返回字段特殊，设置 `rayplus.balance_json_path`。

### 重置失败

检查：

- `rayplus.email` 和 `rayplus.password` 是否能正常登录。
- `codex.subscription_id` 是否正确。
- 如果填了具体订阅 ID，确认该订阅当前可重置。

### 点了低余额弹窗的“否”后不再弹窗

这是预期行为。取消后程序认为你暂时不想自动处理本次低余额，需要你点击“手动重置”主动处理。余额被重置成功，或后续余额恢复到阈值以上后，重复弹窗抑制状态会自动清除。

### uv 默认目录权限异常

可以在当前 PowerShell 会话里使用本地缓存目录：

```powershell
$env:UV_CACHE_DIR=(Join-Path (Get-Location) ".uv-cache")
$env:UV_PYTHON_INSTALL_DIR=(Join-Path (Get-Location) ".uv-python")
```

## Git 约定

仓库只提交 Python 本地版文件，不提交原 Go 项目源码。

请不要提交：

- `config.toml`
- `data/`
- `logs/`
- `.venv/`
- `.uv-cache/`
- `.uv-python/`
