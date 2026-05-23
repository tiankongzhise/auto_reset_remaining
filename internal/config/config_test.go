package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadPostgresFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := strings.Join([]string{
		"RAYPLUS_API_KEY=sk-test",
		"RAYPLUS_EMAIL=user@example.com",
		"RAYPLUS_PASSWORD=password",
		"PUBLIC_BASE_URL=https://service.example.com",
		"SMTP_HOST=smtp.example.com",
		"SMTP_FROM=sender@example.com",
		"SMTP_TO=receiver@example.com",
		"pg_host=localhost",
		"pg_port=15432",
		"pg_user=pg user",
		"pg_password=pg password",
		"pg_database=auto_reset",
		"pg_sslmode=require",
		"MANUAL_CONFIRM_SUCCESS_COUNT=2",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.PG.Host != "localhost" || cfg.PG.Port != 15432 || cfg.PG.User != "pg user" {
		t.Fatalf("unexpected PG config: %+v", cfg.PG)
	}
	conn := cfg.PostgresConnString()
	if strings.Contains(conn, "postgres://") || strings.Contains(conn, "postgresql://") {
		t.Fatalf("PostgresConnString() used URL form: %s", conn)
	}
	for _, part := range []string{"host=localhost", "port=15432", "user='pg user'", "password='pg password'", "dbname=auto_reset", "sslmode=require"} {
		if !strings.Contains(conn, part) {
			t.Fatalf("PostgresConnString() = %q, missing %q", conn, part)
		}
	}
}

func TestLoadLogRotationFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	content := strings.Join([]string{
		"RAYPLUS_API_KEY=sk-test",
		"RAYPLUS_EMAIL=user@example.com",
		"RAYPLUS_PASSWORD=password",
		"PUBLIC_BASE_URL=https://service.example.com",
		"SMTP_HOST=smtp.example.com",
		"SMTP_FROM=sender@example.com",
		"SMTP_TO=receiver@example.com",
		"pg_host=localhost",
		"pg_user=postgres",
		"pg_database=auto_reset",
		"pg_sslmode=disable",
		"QUERY_LOG_DIR=logs",
		"LOG_ROTATION_ENABLED=true",
		"LOG_ROTATION_ARCHIVE_DIR=archives",
		"LOG_ROTATION_KEY=secret-key",
	}, "\n")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if !cfg.LogRotationEnabled || cfg.LogRotationArchiveDir != "archives" || cfg.LogRotationKey != "secret-key" {
		t.Fatalf("unexpected log rotation config: %+v", cfg)
	}
}

func TestLogRotationDisabledDoesNotRequireArchiveSettings(t *testing.T) {
	cfg := Config{
		RayPlusBaseURL:      "https://rayplus.site",
		RayPlusAPIKey:       "sk-test",
		RayPlusEmail:        "user@example.com",
		RayPlusPassword:     "password",
		CodexBaseURL:        "https://codex.example.com",
		PublicBaseURL:       "https://service.example.com",
		HTTPAddr:            "127.0.0.1:8080",
		QueryLogDir:         "logs",
		LowBalanceThreshold: 0.5,
		PollInterval:        1,
		ConfirmTokenTTL:     1,
		SMTP: SMTPConfig{
			Host: "smtp.example.com",
			Port: 587,
			From: "sender@example.com",
			To:   []string{"receiver@example.com"},
		},
		PG: PGConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "postgres",
			Database: "auto_reset",
			SSLMode:  "disable",
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}
