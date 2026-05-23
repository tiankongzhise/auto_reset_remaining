# auto_reset_remaining

用于监控 RayPlus API 余额，并在需要时重置 Codex 订阅额度的 Go 服务。

## 功能

- 查询 `GET /v1/usage` 并记录本地 JSONL 查询日志。
- 低余额时发送确认邮件，并避免重复发送。
- 用户点击邮件确认链接后执行订阅重置。
- 人工确认重置成功累计 3 次后，自动开启自动重置。
- 自动重置模式下，余额 `<= 0` 时执行重置。
- 支持必须人工确认的时间段，在该时间段内即使处于自动阶段也会改发确认邮件。
- 支持按订阅剩余额度比例、休眠状态、重置邮件后状态动态调整查询频率。
- 支持每天本地时间 02:00 轮转本地查询日志。
- 将重置日志和确认令牌写入 PostgreSQL。

## 配置

配置拆成两个文件：

- `.env`：只放机密、身份、访问控制、数据库连接敏感信息。
- `config.toml`：放非机密运行配置和查询策略，支持注释。

`.env.example` 是最新 `.env` 格式模板；不要从本机旧 `.env` 推断完整配置格式。`config.example.toml` 是最新 TOML 配置模板。

初始化配置：

```powershell
Copy-Item .env.example .env
Copy-Item config.example.toml config.toml
```

默认读取 `.env` 和 `config.toml`。也可以用环境变量指定路径：

```powershell
$env:ENV_FILE="C:\path\to\.env"
$env:CONFIG_FILE="C:\path\to\config.toml"
go run ./cmd/auto-reset
```

## 运行

```powershell
go mod tidy
$env:GOCACHE=(Resolve-Path .gocache).Path
go test ./...
go run ./cmd/auto-reset
```

服务启动时会自动创建需要的 PostgreSQL 表。

## 动态查询

查询间隔优先级从高到低：

1. 重置确认邮件发送后快速查询：`polling.after_reset_email`。
2. 余额长期不变后的休眠查询：`polling.sleep`。
3. 订阅额度比例分档查询：`polling.subscription.tiers`。
4. 默认查询间隔：`polling.default_interval`。

订阅模式开启时，`polling.subscription.quota` 必须大于 0，tiers 会按 `min_ratio` 从高到低匹配。余额变化判断使用 `polling.balance_change_epsilon`，默认 `0.000001`。

## 日志轮转

日志轮转目录和开关在 `config.toml`：

```toml
[logs]
query_log_dir = "logs"

[logs.rotation]
enabled = false
archive_dir = "log_archives"
```

外部调用轮转接口的 key 在 `.env`：

```env
LOG_ROTATION_KEY=change-this-log-rotation-key
```

手动轮转接口：

```text
GET /rotate-logs?key=<LOG_ROTATION_KEY>
```

`archive_dir` 不能和 `query_log_dir` 指向同一目录。
