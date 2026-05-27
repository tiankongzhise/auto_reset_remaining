# auto_reset_remaining 使用指南

这是一个用于监控 RayPlus API 余额，并在余额耗尽或低于阈值时重置 Codex 订阅额度的 Go 服务。服务会定期查询余额、发送人工确认邮件、记录查询和重置日志，并把确认令牌与重置记录写入 PostgreSQL。

## 工作流程

1. 服务按配置查询 `GET /v1/usage` 获取当前余额。该接口是唯一余额来源；如果启动校验明确返回 `INVALID_API_KEY`，服务会直接退出。
2. 未开启自动重置时，余额低于 `reset.low_balance_threshold` 会发送一封确认邮件。
3. 用户点击邮件中的 `/confirm-reset?token=...` 链接后，服务调用 Codex 重置订阅额度。
4. 人工确认重置成功累计 3 次后，服务会在 `config.toml` 中开启 `reset.auto_reset_enabled`。
5. 开启自动重置后，余额 `<= 0` 时会自动重置；如果当前时间落入 `reset.manual_confirm_time_range`，仍然改为发送确认邮件。
6. `reset.daily_max_reset_count` 大于 `0` 时，服务会限制每天成功重置次数；达到上限后发送邮件，余额再次消费到 `0` 后发送套餐刷新上限邮件并暂停余额查询，每日 0 点恢复。

## 准备环境

需要准备：

- Go 1.22 或更新版本
- PostgreSQL 数据库
- RayPlus API key、RayPlus 登录邮箱和密码
- 可发送邮件的 SMTP 账号
- 可被邮件收件人访问的服务公网地址，用于生成确认链接

安装依赖并检查测试：

```powershell
go mod tidy
$env:GOCACHE=(Resolve-Path .gocache).Path
go test ./...
```

如果你的 Go 构建缓存目录权限正常，可以不设置 `GOCACHE`。

## 初始化配置

复制模板：

```powershell
Copy-Item .env.example .env
Copy-Item config.example.toml config.toml
```

`.env` 只放敏感信息和访问控制 key，不提交到仓库：

```env
RAYPLUS_API_KEY=sk-your-api-key
RAYPLUS_EMAIL=you@example.com
RAYPLUS_PASSWORD=your-password

SMTP_USER=you@example.com
SMTP_PASSWORD=your-smtp-password

pg_host=127.0.0.1
pg_port=5432
pg_user=postgres
pg_password=postgres-password
pg_database=auto_reset_remaining

LOG_ROTATION_KEY=change-this-log-rotation-key
RESEND_RESET_EMAIL_KEY=change-this-resend-reset-email-key
EXTERNAL_MANUAL_RESET_ENABLED=false
EXTERNAL_MANUAL_RESET_KEY=change-this-external-manual-reset-key
TEST_RESET_EMAIL_KEY=change-this-test-reset-email-key
CANCEL_RESET_EMAIL_KEY=change-this-cancel-reset-email-key
```

`config.toml` 放非敏感运行配置。重点修改这些字段：

- `http.public_base_url`：外部访问本服务的地址，例如 `https://your-domain.example.com`。
- `http.addr`：服务监听地址，例如 `127.0.0.1:8080`。
- `app.timezone`：业务时区；每日 0 点、确认链接过期、人工确认时间段、数据库会话时区和日志展示都使用它，默认 `Asia/Shanghai`。
- `smtp.host`、`smtp.port`、`smtp.from`、`smtp.to`：邮件服务器和收件人。
- `postgres.sslmode`：PostgreSQL SSL 模式。
- `reset.low_balance_threshold`：低余额邮件阈值。
- `reset.daily_max_reset_count`：每日最大可重置次数；`0` 表示不限制。
- `rayplus.balance_json_path`：当余额字段无法自动识别时设置，例如 `data.balance`。
- `codex.subscription_id`：指定要重置的订阅 ID；填 `0` 时自动选择可重置的 active 订阅。

也可以用环境变量指定配置文件路径：

```powershell
$env:ENV_FILE="C:\path\to\.env"
$env:CONFIG_FILE="C:\path\to\config.toml"
```

## 启动服务

```powershell
go run ./cmd/auto-reset
```

服务启动后会：

- 连接 PostgreSQL
- 自动创建 `reset_logs` 和 `confirm_tokens` 表
- 启动余额监控循环
- 启动 HTTP 服务
- 如果启用了日志轮转，则启动每日 02:00 的轮转任务

健康检查：

```text
GET /healthz
```

确认重置链接由邮件生成，格式为：

```text
GET /confirm-reset?token=<token>
```

防重放参数生成接口会校验目标接口对应的 key，并按目标接口生成递增的 `replay_nonce`：

```text
GET /generate-replay-nonce?endpoint=/resend-reset-email&key=<RESEND_RESET_EMAIL_KEY>
```

返回示例：

```json
{"endpoint":"/resend-reset-email","replay_nonce":"1","status":"replay_nonce_generated"}
```

生成接口只生成参数，不会预占用这个参数。真正的防重放记录会在目标接口处理请求前写入数据库；同一个 endpoint 下重复使用相同 `replay_nonce` 会返回 409，并提示防重放参数已经被处理过。不同 endpoint 可以使用相同的 `replay_nonce`。`/confirm-reset?token=...` 不需要 `replay_nonce`，因为它不是直接通过 key 访问的接口。

确认邮件会同时发送 HTML 按钮和纯文本兜底。支持 HTML 的邮箱客户端会显示“重置订阅”按钮；如果按钮不能点击，邮件正文下方也会提供可复制访问的网址。由于每日 0 点系统会自动重置订阅，确认链接最晚会在 `app.timezone` 的 0 点失效；因此失效的邮件记录会在数据库中标记为 `auto_reset_expired`。如果发送确认邮件后余额查询发现当前余额已经恢复到 `reset.low_balance_threshold` 及以上，服务会将未点击的确认链接标记为 `other_reset_expired`，并发送邮件提示余额已经通过其他方式恢复，旧重置链接已自动失效。

如果自动发送确认邮件失败，或需要让旧确认链接作废并重新发送一封确认邮件，可以调用补发接口：

```text
GET /resend-reset-email?key=<RESEND_RESET_EMAIL_KEY>&replay_nonce=<REPLAY_NONCE>
```

`RESEND_RESET_EMAIL_KEY` 来自 `.env`。key 验证成功后，服务会重新查询当前余额、发送一封新的重置确认邮件，并使之前未使用的确认链接失效。

外部手动重置接口默认关闭，需同时满足 `.env` 中 `EXTERNAL_MANUAL_RESET_ENABLED=true` 且 key 正确：

```text
GET /manual-reset-subscription?key=<EXTERNAL_MANUAL_RESET_KEY>&replay_nonce=<REPLAY_NONCE>
```

执行成功后会查询当前余额、调用订阅重置接口，并发送一封邮件提示当前余额和“用户通过手动方式重置了订阅额度”。

测试重置邮件接口不会真的重置订阅，但会校验 key、查询余额、写入 dry-run 日志并发送测试邮件：

```text
GET /test-reset-email?key=<TEST_RESET_EMAIL_KEY>&replay_nonce=<REPLAY_NONCE>
```

撤销未验证的重置邮件：

```text
GET /cancel-reset-emails?key=<CANCEL_RESET_EMAIL_KEY>&replay_nonce=<REPLAY_NONCE>
```

撤销后，数据库中的确认邮件记录会标记为 `manual_cancelled`；它不会再被视为待验证邮件，也不会阻塞后续正常发送新的确认邮件。

## 动态查询配置

查询间隔按优先级从高到低计算：

1. `polling.after_reset_email`：发送重置确认邮件后，固定按该间隔查询，直到余额发生变化；如果余额恢复到 `reset.low_balance_threshold` 及以上，未点击的重置链接会自动失效并退出该临时查询策略。
2. `polling.sleep`：余额持续不变达到 `unchanged_for` 后，使用休眠查询间隔。
3. `polling.subscription`：按剩余额度比例匹配 tiers。
4. `polling.default_interval`：默认查询间隔。

订阅模式示例：

```toml
[polling]
default_interval = "1s"
balance_change_epsilon = 0.000001

[polling.subscription]
enabled = true
quota = 100

[[polling.subscription.tiers]]
min_ratio = 0.70
interval = "1m"

[[polling.subscription.tiers]]
min_ratio = 0.40
interval = "30s"

[[polling.subscription.tiers]]
min_ratio = 0
interval = "1s"
```

`balance_change_epsilon` 用于判断余额是否真的变化。默认 `0.000001` 可以过滤浮点误差，同时不会吞掉正常消耗变化。

## 手动确认时间段

`reset.manual_confirm_time_range` 为空表示不启用。该时间段按 `app.timezone` 解释。

```toml
[reset]
manual_confirm_time_range = "22:00-09:00"
```

规则：

- 支持 `22-9` 和 `22:00-09:00`。
- 结束时间小于开始时间表示跨天。
- 结束时间等于开始时间是非法配置。
- 在该时间段内，即使已经开启自动重置，也会发送确认邮件等待人工点击。

## 日志和轮转

余额查询日志写入 `logs.query_log_dir`，格式为每日一个 JSONL 文件：

```json
{"time":"2026-05-23T22:02:09+08:00","status":"ok","balance":76.8342548,"duration_ms":377}
```

日志轮转配置在 `config.toml`：

```toml
[logs]
query_log_dir = "logs"

[logs.rotation]
enabled = false
archive_dir = "log_archives"
```

手动轮转接口：

```text
GET /rotate-logs?key=<LOG_ROTATION_KEY>&replay_nonce=<REPLAY_NONCE>
```

`LOG_ROTATION_KEY` 来自 `.env`。`archive_dir` 不能和 `query_log_dir` 指向同一目录。

## 常见问题

- 启动时报 `missing config file config.toml`：复制 `config.example.toml` 为 `config.toml`。
- 启动时报缺少配置：检查 `.env` 是否按 `.env.example` 填写，`config.toml` 是否按 `config.example.toml` 填写。
- 邮件确认链接打不开：检查 `http.public_base_url` 是否是收件人能访问的公网地址。
- 查询余额解析失败：设置 `rayplus.balance_json_path` 指向 usage 响应里的余额字段；如果 `/v1/usage` 明确返回 `INVALID_API_KEY`，请修复 `.env` 里的 `RAYPLUS_API_KEY` 后重启。
- PostgreSQL 连接失败：检查 `.env` 中的 `pg_host`、`pg_port`、`pg_user`、`pg_password`、`pg_database`，以及 `config.toml` 中的 `postgres.sslmode`。
