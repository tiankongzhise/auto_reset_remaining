package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"auto_reset_remaining/internal/envfile"
)

type Config struct {
	EnvPath string

	RayPlusBaseURL  string
	RayPlusAPIKey   string
	RayPlusEmail    string
	RayPlusPassword string

	CodexBaseURL   string
	SubscriptionID int64

	SMTP SMTPConfig
	PG   PGConfig

	PublicBaseURL         string
	HTTPAddr              string
	QueryLogDir           string
	LogRotationEnabled    bool
	LogRotationArchiveDir string
	LogRotationKey        string
	UserAgent             string

	LowBalanceThreshold       float64
	BalanceJSONPath           string
	AutoResetEnabled          bool
	ManualConfirmSuccessCount int
	PollInterval              time.Duration
	ConfirmTokenTTL           time.Duration
	ResetCooldown             time.Duration
}

type SMTPConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	From     string
	To       []string
}

type PGConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Database string
	SSLMode  string
}

func Load(path string) (Config, error) {
	fileValues, err := envfile.Load(path)
	if err != nil {
		return Config{}, err
	}
	lookup := func(key string) string {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		return fileValues[key]
	}
	cfg := Config{
		EnvPath:               path,
		RayPlusBaseURL:        defaultString(lookup("RAYPLUS_BASE_URL"), "https://rayplus.site"),
		RayPlusAPIKey:         lookup("RAYPLUS_API_KEY"),
		RayPlusEmail:          lookup("RAYPLUS_EMAIL"),
		RayPlusPassword:       lookup("RAYPLUS_PASSWORD"),
		CodexBaseURL:          defaultString(lookup("CODEX_BASE_URL"), "https://codex.rayplus.site"),
		PublicBaseURL:         strings.TrimRight(lookup("PUBLIC_BASE_URL"), "/"),
		HTTPAddr:              defaultString(lookup("HTTP_ADDR"), ":8080"),
		QueryLogDir:           defaultString(lookup("QUERY_LOG_DIR"), "logs"),
		LogRotationEnabled:    parseBoolDefault(lookup("LOG_ROTATION_ENABLED"), false),
		LogRotationArchiveDir: strings.TrimSpace(lookup("LOG_ROTATION_ARCHIVE_DIR")),
		LogRotationKey:        strings.TrimSpace(lookup("LOG_ROTATION_KEY")),
		UserAgent:             defaultString(lookup("USER_AGENT"), "auto-reset-remaining/1.0"),
		LowBalanceThreshold:   parseFloatDefault(lookup("LOW_BALANCE_THRESHOLD"), 0.5),
		BalanceJSONPath:       lookup("BALANCE_JSON_PATH"),
		AutoResetEnabled:      parseBoolDefault(lookup("AUTO_RESET_ENABLED"), false),
		PollInterval:          parseDurationDefault(lookup("POLL_INTERVAL"), time.Second),
		ConfirmTokenTTL:       parseDurationDefault(lookup("CONFIRM_TOKEN_TTL"), 24*time.Hour),
		ResetCooldown:         parseDurationDefault(lookup("RESET_COOLDOWN"), time.Minute),
		SMTP: SMTPConfig{
			Host:     lookup("SMTP_HOST"),
			Port:     parseIntDefault(lookup("SMTP_PORT"), 587),
			User:     lookup("SMTP_USER"),
			Password: lookup("SMTP_PASSWORD"),
			From:     lookup("SMTP_FROM"),
			To:       splitRecipients(lookup("SMTP_TO")),
		},
		PG: PGConfig{
			Host:     lookup("pg_host"),
			Port:     parseIntDefault(lookup("pg_port"), 5432),
			User:     lookup("pg_user"),
			Password: lookup("pg_password"),
			Database: lookup("pg_database"),
			SSLMode:  defaultString(lookup("pg_sslmode"), "disable"),
		},
	}
	cfg.SubscriptionID = parseInt64Default(lookup("SUBSCRIPTION_ID"), 0)
	cfg.ManualConfirmSuccessCount = parseIntDefault(lookup("MANUAL_CONFIRM_SUCCESS_COUNT"), 0)
	return cfg, nil
}

func (c Config) Validate() error {
	var missing []string
	required := map[string]string{
		"RAYPLUS_BASE_URL": c.RayPlusBaseURL,
		"RAYPLUS_API_KEY":  c.RayPlusAPIKey,
		"RAYPLUS_EMAIL":    c.RayPlusEmail,
		"RAYPLUS_PASSWORD": c.RayPlusPassword,
		"CODEX_BASE_URL":   c.CodexBaseURL,
		"PUBLIC_BASE_URL":  c.PublicBaseURL,
		"SMTP_HOST":        c.SMTP.Host,
		"SMTP_FROM":        c.SMTP.From,
		"pg_host":          c.PG.Host,
		"pg_user":          c.PG.User,
		"pg_database":      c.PG.Database,
		"pg_sslmode":       c.PG.SSLMode,
	}
	for key, value := range required {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(c.SMTP.To) == 0 {
		missing = append(missing, "SMTP_TO")
	}
	if c.SMTP.Port <= 0 || c.SMTP.Port > 65535 {
		return fmt.Errorf("SMTP_PORT must be between 1 and 65535")
	}
	if c.PG.Port <= 0 || c.PG.Port > 65535 {
		return fmt.Errorf("pg_port must be between 1 and 65535")
	}
	if c.LowBalanceThreshold <= 0 {
		return fmt.Errorf("LOW_BALANCE_THRESHOLD must be greater than 0")
	}
	if c.LogRotationEnabled {
		if strings.TrimSpace(c.LogRotationArchiveDir) == "" {
			missing = append(missing, "LOG_ROTATION_ARCHIVE_DIR")
		} else if samePath(c.QueryLogDir, c.LogRotationArchiveDir) {
			return fmt.Errorf("LOG_ROTATION_ARCHIVE_DIR must be different from QUERY_LOG_DIR")
		}
		if strings.TrimSpace(c.LogRotationKey) == "" {
			missing = append(missing, "LOG_ROTATION_KEY")
		}
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("POLL_INTERVAL must be greater than 0")
	}
	if c.ConfirmTokenTTL <= 0 {
		return fmt.Errorf("CONFIRM_TOKEN_TTL must be greater than 0")
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

func (c Config) PostgresConnString() string {
	parts := []string{
		"host=" + quotePGValue(c.PG.Host),
		"port=" + strconv.Itoa(c.PG.Port),
		"user=" + quotePGValue(c.PG.User),
		"dbname=" + quotePGValue(c.PG.Database),
		"sslmode=" + quotePGValue(c.PG.SSLMode),
	}
	if c.PG.Password != "" {
		parts = append(parts, "password="+quotePGValue(c.PG.Password))
	}
	return strings.Join(parts, " ")
}

func samePath(left, right string) bool {
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	if leftErr != nil || rightErr != nil {
		return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
	}
	return strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
}

func (c Config) SMTPAddress() string {
	return net.JoinHostPort(c.SMTP.Host, strconv.Itoa(c.SMTP.Port))
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func parseBoolDefault(value string, fallback bool) bool {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func parseIntDefault(value string, fallback int) int {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func parseInt64Default(value string, fallback int64) int64 {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseFloatDefault(value string, fallback float64) float64 {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func parseDurationDefault(value string, fallback time.Duration) time.Duration {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(strings.TrimSpace(value))
	if err != nil {
		return fallback
	}
	return parsed
}

func splitRecipients(value string) []string {
	fields := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n'
	})
	recipients := make([]string, 0, len(fields))
	for _, field := range fields {
		if trimmed := strings.TrimSpace(field); trimmed != "" {
			recipients = append(recipients, trimmed)
		}
	}
	return recipients
}

func quotePGValue(value string) string {
	if value == "" {
		return "''"
	}
	if !strings.ContainsAny(value, " \t\r\n'\\") {
		return value
	}
	value = strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(value)
	return "'" + value + "'"
}
