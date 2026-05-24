package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadSplitsSecretsAndTOMLConfig(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	tomlPath := filepath.Join(dir, "config.toml")
	writeTestEnv(t, envPath, strings.Join([]string{
		"RAYPLUS_API_KEY=sk-test",
		"RAYPLUS_EMAIL=user@example.com",
		"RAYPLUS_PASSWORD=password",
		"SMTP_USER=smtp-user@example.com",
		"SMTP_PASSWORD=smtp-password",
		"pg_host=localhost",
		"pg_port=15432",
		"pg_user=pg user",
		"pg_password=pg password",
		"pg_database=auto_reset",
		"LOG_ROTATION_KEY=secret-key",
		"RESEND_RESET_EMAIL_KEY=resend-secret",
	}, "\n"))
	writeTestConfig(t, tomlPath, baseConfigTOML())

	cfg, err := Load(envPath, tomlPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if cfg.PG.Host != "localhost" || cfg.PG.Port != 15432 || cfg.PG.User != "pg user" {
		t.Fatalf("unexpected PG config: %+v", cfg.PG)
	}
	if cfg.PG.SSLMode != "require" {
		t.Fatalf("PG.SSLMode = %q, want require", cfg.PG.SSLMode)
	}
	if cfg.LogRotationEnabled || cfg.LogRotationKey != "secret-key" {
		t.Fatalf("unexpected log rotation config: enabled=%t key=%q", cfg.LogRotationEnabled, cfg.LogRotationKey)
	}
	if cfg.ResendResetEmailKey != "resend-secret" {
		t.Fatalf("ResendResetEmailKey = %q, want resend-secret", cfg.ResendResetEmailKey)
	}
	conn := cfg.PostgresConnString()
	for _, part := range []string{"host=localhost", "port=15432", "user='pg user'", "password='pg password'", "dbname=auto_reset", "sslmode=require"} {
		if !strings.Contains(conn, part) {
			t.Fatalf("PostgresConnString() = %q, missing %q", conn, part)
		}
	}
}

func TestLoadMissingConfigFileHasHelpfulError(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	writeTestEnv(t, envPath, "RAYPLUS_API_KEY=sk-test\n")

	_, err := Load(envPath, filepath.Join(dir, "config.toml"))
	if err == nil || !strings.Contains(err.Error(), "copy config.example.toml") {
		t.Fatalf("Load() error = %v, want helpful missing config error", err)
	}
}

func TestValidateSubscriptionRequiresQuotaAndTiers(t *testing.T) {
	cfg := validConfig()
	cfg.Polling.Subscription.Enabled = true
	cfg.Polling.Subscription.Quota = 0
	cfg.Polling.Subscription.Tiers = []SubscriptionTier{{MinRatio: 0, Interval: time.Second}}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "quota") {
		t.Fatalf("Validate() error = %v, want quota error", err)
	}

	cfg.Polling.Subscription.Quota = 100
	cfg.Polling.Subscription.Tiers = nil
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "tiers") {
		t.Fatalf("Validate() error = %v, want tiers error", err)
	}
}

func TestValidateRejectsNegativeDailyMaxResetCount(t *testing.T) {
	cfg := validConfig()
	cfg.DailyMaxResetCount = -1
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "daily_max_reset_count") {
		t.Fatalf("Validate() error = %v, want daily_max_reset_count error", err)
	}
}

func TestManualConfirmWindowParsing(t *testing.T) {
	window, err := parseManualConfirmWindow("22:00-09:00")
	if err != nil {
		t.Fatalf("parseManualConfirmWindow() error = %v", err)
	}
	if !window.Contains(time.Date(2026, 5, 23, 23, 0, 0, 0, time.Local)) {
		t.Fatal("window should contain same-night time")
	}
	if !window.Contains(time.Date(2026, 5, 24, 8, 59, 0, 0, time.Local)) {
		t.Fatal("window should contain next-morning time")
	}
	if window.Contains(time.Date(2026, 5, 24, 9, 0, 0, 0, time.Local)) {
		t.Fatal("window should exclude end time")
	}
	if _, err := parseManualConfirmWindow("22-22"); err == nil {
		t.Fatal("parseManualConfirmWindow() should reject equal start and end")
	}
}

func TestLogRotationDisabledDoesNotRequireArchiveSettings(t *testing.T) {
	cfg := validConfig()
	cfg.LogRotationEnabled = false
	cfg.LogRotationArchiveDir = ""
	cfg.LogRotationKey = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func validConfig() Config {
	return Config{
		RayPlusBaseURL:      "https://rayplus.site",
		RayPlusAPIKey:       "sk-test",
		RayPlusEmail:        "user@example.com",
		RayPlusPassword:     "password",
		CodexBaseURL:        "https://codex.example.com",
		PublicBaseURL:       "https://service.example.com",
		HTTPAddr:            "127.0.0.1:8080",
		QueryLogDir:         "logs",
		ResendResetEmailKey: "resend-secret",
		LowBalanceThreshold: 0.5,
		PollInterval:        time.Second,
		ConfirmTokenTTL:     time.Hour,
		ResetCooldown:       time.Minute,
		Polling: PollingConfig{
			DefaultInterval:      time.Second,
			BalanceChangeEpsilon: 0.000001,
		},
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
}

func baseConfigTOML() string {
	return `
[rayplus]
base_url = "https://rayplus.site"
user_agent = "auto-reset-remaining/1.0"
balance_json_path = ""

[codex]
base_url = "https://codex.example.com"
subscription_id = 1716

[smtp]
host = "smtp.example.com"
port = 587
from = "sender@example.com"
to = ["receiver@example.com"]

[postgres]
sslmode = "require"

[http]
public_base_url = "https://service.example.com"
addr = "127.0.0.1:8080"

[logs]
query_log_dir = "logs"

[logs.rotation]
enabled = false
archive_dir = "archives"

[reset]
low_balance_threshold = 0.5
auto_reset_enabled = false
manual_confirm_success_count = 2
daily_max_reset_count = 0
confirm_token_ttl = "24h"
cooldown = "1m"
manual_confirm_time_range = ""

[polling]
default_interval = "1s"
balance_change_epsilon = 0.000001

[polling.subscription]
enabled = false
quota = 0

[[polling.subscription.tiers]]
min_ratio = 0
interval = "1s"

[polling.sleep]
enabled = false
unchanged_for = "10m"
interval = "1m"

[polling.after_reset_email]
enabled = false
interval = "1m"
`
}

func writeTestEnv(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestConfig(t *testing.T, path string, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}
