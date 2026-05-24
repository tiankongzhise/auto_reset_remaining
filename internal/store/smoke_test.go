package store_test

import (
	"context"
	"database/sql"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"

	"auto_reset_remaining/internal/config"
	"auto_reset_remaining/internal/store"
)

func TestSmokeLocalConfigAndPostgresSchema(t *testing.T) {
	if os.Getenv("AUTO_RESET_SMOKE") != "1" {
		t.Skip("set AUTO_RESET_SMOKE=1 to validate local .env, config.toml, and PostgreSQL schema")
	}

	cfg, err := config.Load("../../.env", "../../config.toml")
	if err != nil {
		t.Fatalf("load local config: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate local config: %v", err)
	}
	if cfg.TestResetEmailKey == "" || cfg.CancelResetEmailKey == "" {
		t.Fatal("local .env must include TEST_RESET_EMAIL_KEY and CANCEL_RESET_EMAIL_KEY")
	}

	db, err := sql.Open("postgres", cfg.PostgresConnString())
	if err != nil {
		t.Fatalf("open postgres: %v", err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetConnMaxLifetime(time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("ping postgres: %v", err)
	}
	if err := store.NewPostgres(db).Init(ctx); err != nil {
		t.Fatalf("init postgres schema: %v", err)
	}

	requiredColumns := map[string]bool{
		"status":        false,
		"cancelled_at":  false,
		"email_sent_at": false,
	}
	rows, err := db.QueryContext(ctx, `
		SELECT column_name
		FROM information_schema.columns
		WHERE table_schema = current_schema()
		  AND table_name = 'confirm_tokens'
		  AND column_name IN ('status', 'cancelled_at', 'email_sent_at')`)
	if err != nil {
		t.Fatalf("query confirm_tokens columns: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatalf("scan confirm_tokens column: %v", err)
		}
		requiredColumns[column] = true
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("confirm_tokens column rows: %v", err)
	}
	for column, found := range requiredColumns {
		if !found {
			t.Fatalf("confirm_tokens missing column %s", column)
		}
	}
}
