# auto_reset_remaining

用于监控 RayPlus API 余额，并在需要时重置 Codex 订阅额度的 Go 服务。

## 功能

- 按 `POLL_INTERVAL` 配置周期查询 `GET /v1/usage`。
- 当余额低于 `LOW_BALANCE_THRESHOLD` 时，只发送一封低余额确认邮件，避免重复打扰。
- 用户点击邮件确认链接后，执行订阅重置。
- 人工确认重置成功累计 3 次后，自动更新 `.env` 并开启自动重置。
- 自动重置模式下，当余额 `<= 0` 时直接执行重置。
- 将余额查询日志写入本地 JSONL 文件。
- 可选开启本地日志轮转，每天本地时间 02:00 归档日志文件。
- 将重置日志和确认令牌写入 PostgreSQL。

## 安装与运行

1. 复制 `.env.example` 为 `.env`。
2. 在 `.env` 中填写所有密钥、邮箱、数据库和服务配置。
3. 创建 PostgreSQL 数据库，连接信息由 `pg_host`、`pg_port`、`pg_user`、`pg_password`、`pg_database`、`pg_sslmode` 控制。
4. 运行服务：

```powershell
go mod tidy
go test ./...
go run ./cmd/auto-reset
```

服务启动时会自动创建需要的 PostgreSQL 表。

## 日志轮转

日志轮转只处理本地日志目录 `QUERY_LOG_DIR` 下的普通文件，不会轮转 PostgreSQL 中的数据库日志。

相关 `.env` 配置：

```env
QUERY_LOG_DIR=logs
LOG_ROTATION_ENABLED=false
LOG_ROTATION_ARCHIVE_DIR=log_archives
LOG_ROTATION_KEY=change-this-log-rotation-key
```

- `LOG_ROTATION_ENABLED=true` 时，服务会在每天本地时间 02:00 自动轮转本地日志。
- `LOG_ROTATION_ARCHIVE_DIR` 是归档目录；如果目录不存在，服务会在轮转时尝试创建。
- 如果归档目录不可用或创建失败，本次轮转会降级为跳过，不会影响服务继续运行。
- 每次成功轮转会在归档目录下创建一个时间戳子目录，并把 `QUERY_LOG_DIR` 下的普通文件移动进去。
- `LOG_ROTATION_ARCHIVE_DIR` 不能和 `QUERY_LOG_DIR` 指向同一目录。

也可以通过 GET 接口手动触发日志轮转：

```text
GET /rotate-logs?key=<LOG_ROTATION_KEY>
```

密钥与 `.env` 中的 `LOG_ROTATION_KEY` 一致时才会触发。接口返回 JSON，包含归档目录、轮转文件列表、总字节数；如果本次未轮转，会返回 `skipped_reason` 说明原因。

## 配置说明

- `.env` 不要提交到仓库，当前已由 `.gitignore` 忽略。
- `HTTP_ADDR` 控制服务监听地址；如果放在 nginx 后面，建议绑定到本地地址，例如 `127.0.0.1:8080`。
- `PUBLIC_BASE_URL` 只用于生成邮件中的确认链接，应设置为 nginx 暴露给外部访问的地址，例如 `https://your-domain.example.com`。
- 如果 usage 响应中没有可识别的余额字段，可以设置 `BALANCE_JSON_PATH`，例如 `data.balance`。
