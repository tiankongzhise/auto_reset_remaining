package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

var ErrTokenInvalid = errors.New("confirm token is invalid, expired, or already used")

type ResetLog struct {
	Mode            string
	Balance         float64
	SubscriptionID  int64
	Success         bool
	HTTPStatus      int
	Error           string
	ResponseSummary string
}

type ConfirmToken struct {
	ID        int64
	Balance   float64
	ExpiresAt time.Time
}

type Store interface {
	Init(ctx context.Context) error
	CreateConfirmToken(ctx context.Context, tokenHash string, balance float64, expiresAt time.Time) error
	DeleteConfirmToken(ctx context.Context, tokenHash string) error
	HasActiveConfirmToken(ctx context.Context) (bool, error)
	ConsumeConfirmToken(ctx context.Context, tokenHash string) (ConfirmToken, error)
	MarkConfirmTokenReset(ctx context.Context, tokenID int64, resetLogID int64, manualSuccessCountAfter int) error
	LogReset(ctx context.Context, entry ResetLog) (int64, error)
}

type Postgres struct {
	db *sql.DB
}

func NewPostgres(db *sql.DB) *Postgres {
	return &Postgres{db: db}
}

func (p *Postgres) Init(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS reset_logs (
			id BIGSERIAL PRIMARY KEY,
			triggered_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			mode TEXT NOT NULL,
			balance DOUBLE PRECISION NOT NULL,
			subscription_id BIGINT,
			success BOOLEAN NOT NULL,
			http_status INTEGER,
			error TEXT,
			response_summary TEXT
		)`,
		`CREATE INDEX IF NOT EXISTS reset_logs_triggered_at_idx ON reset_logs (triggered_at DESC)`,
		`CREATE TABLE IF NOT EXISTS confirm_tokens (
			id BIGSERIAL PRIMARY KEY,
			token_hash TEXT NOT NULL UNIQUE,
			balance DOUBLE PRECISION NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
			expires_at TIMESTAMPTZ NOT NULL,
			used_at TIMESTAMPTZ,
			reset_log_id BIGINT REFERENCES reset_logs(id),
			manual_success_count_after INTEGER
		)`,
		`CREATE INDEX IF NOT EXISTS confirm_tokens_active_idx ON confirm_tokens (expires_at) WHERE used_at IS NULL`,
	}
	for _, statement := range statements {
		if _, err := p.db.ExecContext(ctx, statement); err != nil {
			return err
		}
	}
	return nil
}

func (p *Postgres) CreateConfirmToken(ctx context.Context, tokenHash string, balance float64, expiresAt time.Time) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO confirm_tokens (token_hash, balance, expires_at) VALUES ($1, $2, $3)`,
		tokenHash, balance, expiresAt,
	)
	return err
}

func (p *Postgres) DeleteConfirmToken(ctx context.Context, tokenHash string) error {
	_, err := p.db.ExecContext(ctx, `DELETE FROM confirm_tokens WHERE token_hash = $1`, tokenHash)
	return err
}

func (p *Postgres) HasActiveConfirmToken(ctx context.Context) (bool, error) {
	var exists bool
	err := p.db.QueryRowContext(ctx,
		`SELECT EXISTS (
			SELECT 1 FROM confirm_tokens
			WHERE used_at IS NULL AND expires_at > now()
		)`,
	).Scan(&exists)
	return exists, err
}

func (p *Postgres) ConsumeConfirmToken(ctx context.Context, tokenHash string) (ConfirmToken, error) {
	var token ConfirmToken
	err := p.db.QueryRowContext(ctx,
		`UPDATE confirm_tokens
		 SET used_at = now()
		 WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()
		 RETURNING id, balance, expires_at`,
		tokenHash,
	).Scan(&token.ID, &token.Balance, &token.ExpiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ConfirmToken{}, ErrTokenInvalid
	}
	return token, err
}

func (p *Postgres) MarkConfirmTokenReset(ctx context.Context, tokenID int64, resetLogID int64, manualSuccessCountAfter int) error {
	_, err := p.db.ExecContext(ctx,
		`UPDATE confirm_tokens
		 SET reset_log_id = $2, manual_success_count_after = $3
		 WHERE id = $1`,
		tokenID, resetLogID, manualSuccessCountAfter,
	)
	return err
}

func (p *Postgres) LogReset(ctx context.Context, entry ResetLog) (int64, error) {
	var id int64
	var subscriptionID any
	if entry.SubscriptionID > 0 {
		subscriptionID = entry.SubscriptionID
	}
	var httpStatus any
	if entry.HTTPStatus > 0 {
		httpStatus = entry.HTTPStatus
	}
	err := p.db.QueryRowContext(ctx,
		`INSERT INTO reset_logs
		 (mode, balance, subscription_id, success, http_status, error, response_summary)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 RETURNING id`,
		entry.Mode, entry.Balance, subscriptionID, entry.Success, httpStatus, nullString(entry.Error), nullString(entry.ResponseSummary),
	).Scan(&id)
	return id, err
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}
