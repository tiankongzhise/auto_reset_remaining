# auto_reset_remaining

Go service for monitoring RayPlus API balance and resetting Codex subscription quota.

## Features

- Queries `GET /v1/usage` every second.
- Sends one low-balance confirmation email when balance is below `LOW_BALANCE_THRESHOLD`.
- Resets subscription after the user clicks the email confirmation link.
- After three successful manual confirmations, updates `.env` to enable automatic reset.
- In automatic mode, resets directly when balance is `<= 0`.
- Writes balance query logs to local JSONL files.
- Writes reset logs and confirmation tokens to PostgreSQL.

## Setup

1. Copy `.env.example` to `.env`.
2. Fill all secrets and connection fields in `.env`.
3. Create the PostgreSQL database configured by `pg_host`, `pg_port`, `pg_user`, `pg_password`, `pg_database`, and `pg_sslmode`.
4. Run:

```powershell
go mod tidy
go test ./...
go run ./cmd/auto-reset
```

The service creates its required PostgreSQL tables on startup.

## Notes

- Do not commit `.env`; it is ignored by git.
- `PUBLIC_BASE_URL` must be reachable by the email recipient because confirmation links are generated from it.
- If the usage response does not expose a recognizable balance field, set `BALANCE_JSON_PATH`, for example `data.balance`.
