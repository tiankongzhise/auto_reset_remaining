package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BurntSushi/toml"

	"auto_reset_remaining/internal/envfile"
)

type Config struct {
	EnvPath  string
	TOMLPath string

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
	ResendResetEmailKey   string
	UserAgent             string

	LowBalanceThreshold       float64
	BalanceJSONPath           string
	AutoResetEnabled          bool
	ManualConfirmSuccessCount int
	DailyMaxResetCount        int
	PollInterval              time.Duration
	ConfirmTokenTTL           time.Duration
	ResetCooldown             time.Duration

	ManualConfirmWindow ManualConfirmWindow
	Polling             PollingConfig
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

type PollingConfig struct {
	DefaultInterval      time.Duration
	BalanceChangeEpsilon float64
	Subscription         SubscriptionPollingConfig
	Sleep                SleepPollingConfig
	AfterResetEmail      AfterResetEmailPollingConfig
}

type SubscriptionPollingConfig struct {
	Enabled bool
	Quota   float64
	Tiers   []SubscriptionTier
}

type SubscriptionTier struct {
	MinRatio float64
	Interval time.Duration
}

type SleepPollingConfig struct {
	Enabled      bool
	UnchangedFor time.Duration
	Interval     time.Duration
}

type AfterResetEmailPollingConfig struct {
	Enabled  bool
	Interval time.Duration
}

type ManualConfirmWindow struct {
	Enabled bool
	Start   timeOfDay
	End     timeOfDay
}

type timeOfDay struct {
	minutes int
}

type FileConfig struct {
	RayPlus FileRayPlusConfig `toml:"rayplus"`
	Codex   FileCodexConfig   `toml:"codex"`
	SMTP    FileSMTPConfig    `toml:"smtp"`
	PG      FilePGConfig      `toml:"postgres"`
	HTTP    FileHTTPConfig    `toml:"http"`
	Logs    FileLogsConfig    `toml:"logs"`
	Reset   FileResetConfig   `toml:"reset"`
	Polling FilePollingConfig `toml:"polling"`
}

type FileRayPlusConfig struct {
	BaseURL         string `toml:"base_url"`
	UserAgent       string `toml:"user_agent"`
	BalanceJSONPath string `toml:"balance_json_path"`
}

type FileCodexConfig struct {
	BaseURL        string `toml:"base_url"`
	SubscriptionID int64  `toml:"subscription_id"`
}

type FileSMTPConfig struct {
	Host string   `toml:"host"`
	Port int      `toml:"port"`
	From string   `toml:"from"`
	To   []string `toml:"to"`
}

type FilePGConfig struct {
	SSLMode string `toml:"sslmode"`
}

type FileHTTPConfig struct {
	PublicBaseURL string `toml:"public_base_url"`
	Addr          string `toml:"addr"`
}

type FileLogsConfig struct {
	QueryLogDir string                `toml:"query_log_dir"`
	Rotation    FileLogRotationConfig `toml:"rotation"`
}

type FileLogRotationConfig struct {
	Enabled    bool   `toml:"enabled"`
	ArchiveDir string `toml:"archive_dir"`
}

type FileResetConfig struct {
	LowBalanceThreshold       float64 `toml:"low_balance_threshold"`
	AutoResetEnabled          bool    `toml:"auto_reset_enabled"`
	ManualConfirmSuccessCount int     `toml:"manual_confirm_success_count"`
	DailyMaxResetCount        int     `toml:"daily_max_reset_count"`
	ConfirmTokenTTL           string  `toml:"confirm_token_ttl"`
	Cooldown                  string  `toml:"cooldown"`
	ManualConfirmTimeRange    string  `toml:"manual_confirm_time_range"`
}

type FilePollingConfig struct {
	DefaultInterval      string                           `toml:"default_interval"`
	BalanceChangeEpsilon float64                          `toml:"balance_change_epsilon"`
	Subscription         FileSubscriptionPollingConfig    `toml:"subscription"`
	Sleep                FileSleepPollingConfig           `toml:"sleep"`
	AfterResetEmail      FileAfterResetEmailPollingConfig `toml:"after_reset_email"`
}

type FileSubscriptionPollingConfig struct {
	Enabled bool                   `toml:"enabled"`
	Quota   float64                `toml:"quota"`
	Tiers   []FileSubscriptionTier `toml:"tiers"`
}

type FileSubscriptionTier struct {
	MinRatio float64 `toml:"min_ratio"`
	Interval string  `toml:"interval"`
}

type FileSleepPollingConfig struct {
	Enabled      bool   `toml:"enabled"`
	UnchangedFor string `toml:"unchanged_for"`
	Interval     string `toml:"interval"`
}

type FileAfterResetEmailPollingConfig struct {
	Enabled  bool   `toml:"enabled"`
	Interval string `toml:"interval"`
}

func Load(envPath string, tomlPath ...string) (Config, error) {
	configPath := "config.toml"
	if len(tomlPath) > 0 && strings.TrimSpace(tomlPath[0]) != "" {
		configPath = tomlPath[0]
	}

	secretValues, err := envfile.Load(envPath)
	if err != nil {
		return Config{}, err
	}
	fileCfg, err := LoadFileConfig(configPath)
	if err != nil {
		return Config{}, err
	}

	lookupSecret := func(key string) string {
		if value, ok := os.LookupEnv(key); ok {
			return value
		}
		return secretValues[key]
	}
	cfg, err := merge(envPath, configPath, fileCfg, lookupSecret)
	if err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func LoadFileConfig(path string) (FileConfig, error) {
	var cfg FileConfig
	if _, err := os.Stat(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return FileConfig{}, fmt.Errorf("missing config file %s; copy config.example.toml to %s and edit it", path, path)
		}
		return FileConfig{}, err
	}
	if _, err := toml.DecodeFile(path, &cfg); err != nil {
		return FileConfig{}, err
	}
	return cfg, nil
}

func UpdateRuntimeState(path string, manualConfirmSuccessCount int, autoResetEnabled bool) error {
	cfg, err := LoadFileConfig(path)
	if err != nil {
		return err
	}
	cfg.Reset.ManualConfirmSuccessCount = manualConfirmSuccessCount
	cfg.Reset.AutoResetEnabled = autoResetEnabled
	return updateResetState(path, manualConfirmSuccessCount, autoResetEnabled)
}

func WriteFileConfig(path string, cfg FileConfig) error {
	var out bytes.Buffer
	encoder := toml.NewEncoder(&out)
	encoder.Indent = ""
	if err := encoder.Encode(cfg); err != nil {
		return err
	}
	return replaceFile(path, out.Bytes())
}

func replaceFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".config-*.toml")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err == nil {
		return nil
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(tmpName, path)
}

func (c *FileConfig) ApplyDefaults() {
	c.RayPlus.BaseURL = defaultString(c.RayPlus.BaseURL, "https://rayplus.site")
	c.RayPlus.UserAgent = defaultString(c.RayPlus.UserAgent, "auto-reset-remaining/1.0")
	c.Codex.BaseURL = defaultString(c.Codex.BaseURL, "https://codex.rayplus.site")
	c.SMTP.Port = defaultInt(c.SMTP.Port, 587)
	c.PG.SSLMode = defaultString(c.PG.SSLMode, "disable")
	c.HTTP.Addr = defaultString(c.HTTP.Addr, ":8080")
	c.Logs.QueryLogDir = defaultString(c.Logs.QueryLogDir, "logs")
	c.Reset.LowBalanceThreshold = defaultFloat(c.Reset.LowBalanceThreshold, 0.5)
	c.Reset.ConfirmTokenTTL = defaultString(c.Reset.ConfirmTokenTTL, "24h")
	c.Reset.Cooldown = defaultString(c.Reset.Cooldown, "1m")
	c.Polling.DefaultInterval = defaultString(c.Polling.DefaultInterval, "1s")
	c.Polling.BalanceChangeEpsilon = defaultFloat(c.Polling.BalanceChangeEpsilon, 0.000001)
	c.Polling.Sleep.UnchangedFor = defaultString(c.Polling.Sleep.UnchangedFor, "10m")
	c.Polling.Sleep.Interval = defaultString(c.Polling.Sleep.Interval, "1m")
	c.Polling.AfterResetEmail.Interval = defaultString(c.Polling.AfterResetEmail.Interval, "1m")
}

func updateResetState(path string, manualConfirmSuccessCount int, autoResetEnabled bool) error {
	input, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	updates := map[string]string{
		"auto_reset_enabled":           strconv.FormatBool(autoResetEnabled),
		"manual_confirm_success_count": strconv.Itoa(manualConfirmSuccessCount),
	}

	var lines []string
	scanner := bufio.NewScanner(bytes.NewReader(input))
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	inReset := false
	foundReset := false
	seen := make(map[string]bool, len(updates))
	insertAt := len(lines)
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inReset {
				insertAt = i
				inReset = false
			}
			if trimmed == "[reset]" {
				inReset = true
				foundReset = true
				insertAt = i + 1
			}
			continue
		}
		if !inReset {
			continue
		}
		key, ok := tomlKey(line)
		if !ok {
			continue
		}
		value, exists := updates[key]
		if !exists {
			continue
		}
		lines[i] = key + " = " + value
		seen[key] = true
		insertAt = i + 1
	}

	if !foundReset {
		if len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) != "" {
			lines = append(lines, "")
		}
		lines = append(lines, "[reset]")
		insertAt = len(lines)
	}
	var missing []string
	for key := range updates {
		if !seen[key] {
			missing = append(missing, key)
		}
	}
	sort.Strings(missing)
	for _, key := range missing {
		line := key + " = " + updates[key]
		lines = append(lines[:insertAt], append([]string{line}, lines[insertAt:]...)...)
		insertAt++
	}

	var out bytes.Buffer
	for _, line := range lines {
		out.WriteString(line)
		out.WriteByte('\n')
	}
	return replaceFile(path, out.Bytes())
}

func tomlKey(line string) (string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || strings.HasPrefix(trimmed, "#") {
		return "", false
	}
	idx := strings.IndexByte(trimmed, '=')
	if idx <= 0 {
		return "", false
	}
	key := strings.TrimSpace(trimmed[:idx])
	if strings.ContainsAny(key, " .\t") {
		return "", false
	}
	return key, true
}

func merge(envPath string, configPath string, fileCfg FileConfig, lookupSecret func(string) string) (Config, error) {
	fileCfg.ApplyDefaults()

	tiers := make([]SubscriptionTier, 0, len(fileCfg.Polling.Subscription.Tiers))
	for _, tier := range fileCfg.Polling.Subscription.Tiers {
		tiers = append(tiers, SubscriptionTier{
			MinRatio: tier.MinRatio,
			Interval: parseDurationDefault(tier.Interval, 0),
		})
	}
	sort.SliceStable(tiers, func(i, j int) bool {
		return tiers[i].MinRatio > tiers[j].MinRatio
	})

	cfg := Config{
		EnvPath:                   envPath,
		TOMLPath:                  configPath,
		RayPlusBaseURL:            fileCfg.RayPlus.BaseURL,
		RayPlusAPIKey:             lookupSecret("RAYPLUS_API_KEY"),
		RayPlusEmail:              lookupSecret("RAYPLUS_EMAIL"),
		RayPlusPassword:           lookupSecret("RAYPLUS_PASSWORD"),
		CodexBaseURL:              fileCfg.Codex.BaseURL,
		SubscriptionID:            fileCfg.Codex.SubscriptionID,
		PublicBaseURL:             strings.TrimRight(fileCfg.HTTP.PublicBaseURL, "/"),
		HTTPAddr:                  fileCfg.HTTP.Addr,
		QueryLogDir:               fileCfg.Logs.QueryLogDir,
		LogRotationEnabled:        fileCfg.Logs.Rotation.Enabled,
		LogRotationArchiveDir:     strings.TrimSpace(fileCfg.Logs.Rotation.ArchiveDir),
		LogRotationKey:            strings.TrimSpace(lookupSecret("LOG_ROTATION_KEY")),
		ResendResetEmailKey:       strings.TrimSpace(lookupSecret("RESEND_RESET_EMAIL_KEY")),
		UserAgent:                 fileCfg.RayPlus.UserAgent,
		LowBalanceThreshold:       fileCfg.Reset.LowBalanceThreshold,
		BalanceJSONPath:           fileCfg.RayPlus.BalanceJSONPath,
		AutoResetEnabled:          fileCfg.Reset.AutoResetEnabled,
		ManualConfirmSuccessCount: fileCfg.Reset.ManualConfirmSuccessCount,
		DailyMaxResetCount:        fileCfg.Reset.DailyMaxResetCount,
		PollInterval:              parseDurationDefault(fileCfg.Polling.DefaultInterval, time.Second),
		ConfirmTokenTTL:           parseDurationDefault(fileCfg.Reset.ConfirmTokenTTL, 24*time.Hour),
		ResetCooldown:             parseDurationDefault(fileCfg.Reset.Cooldown, time.Minute),
		SMTP: SMTPConfig{
			Host:     fileCfg.SMTP.Host,
			Port:     fileCfg.SMTP.Port,
			User:     lookupSecret("SMTP_USER"),
			Password: lookupSecret("SMTP_PASSWORD"),
			From:     fileCfg.SMTP.From,
			To:       append([]string(nil), fileCfg.SMTP.To...),
		},
		PG: PGConfig{
			Host:     lookupSecret("pg_host"),
			Port:     parseIntDefault(lookupSecret("pg_port"), 5432),
			User:     lookupSecret("pg_user"),
			Password: lookupSecret("pg_password"),
			Database: lookupSecret("pg_database"),
			SSLMode:  fileCfg.PG.SSLMode,
		},
		Polling: PollingConfig{
			DefaultInterval:      parseDurationDefault(fileCfg.Polling.DefaultInterval, time.Second),
			BalanceChangeEpsilon: fileCfg.Polling.BalanceChangeEpsilon,
			Subscription: SubscriptionPollingConfig{
				Enabled: fileCfg.Polling.Subscription.Enabled,
				Quota:   fileCfg.Polling.Subscription.Quota,
				Tiers:   tiers,
			},
			Sleep: SleepPollingConfig{
				Enabled:      fileCfg.Polling.Sleep.Enabled,
				UnchangedFor: parseDurationDefault(fileCfg.Polling.Sleep.UnchangedFor, 10*time.Minute),
				Interval:     parseDurationDefault(fileCfg.Polling.Sleep.Interval, time.Minute),
			},
			AfterResetEmail: AfterResetEmailPollingConfig{
				Enabled:  fileCfg.Polling.AfterResetEmail.Enabled,
				Interval: parseDurationDefault(fileCfg.Polling.AfterResetEmail.Interval, time.Minute),
			},
		},
	}
	if strings.TrimSpace(fileCfg.Reset.ManualConfirmTimeRange) != "" {
		window, err := parseManualConfirmWindow(fileCfg.Reset.ManualConfirmTimeRange)
		if err != nil {
			return Config{}, err
		}
		cfg.ManualConfirmWindow = window
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var missing []string
	required := map[string]string{
		"RAYPLUS_API_KEY":        c.RayPlusAPIKey,
		"RAYPLUS_EMAIL":          c.RayPlusEmail,
		"RAYPLUS_PASSWORD":       c.RayPlusPassword,
		"RESEND_RESET_EMAIL_KEY": c.ResendResetEmailKey,
		"rayplus.base_url":       c.RayPlusBaseURL,
		"codex.base_url":         c.CodexBaseURL,
		"http.public_base_url":   c.PublicBaseURL,
		"smtp.host":              c.SMTP.Host,
		"smtp.from":              c.SMTP.From,
		"pg_host":                c.PG.Host,
		"pg_user":                c.PG.User,
		"pg_database":            c.PG.Database,
		"postgres.sslmode":       c.PG.SSLMode,
	}
	for key, value := range required {
		if strings.TrimSpace(value) == "" {
			missing = append(missing, key)
		}
	}
	if len(c.SMTP.To) == 0 {
		missing = append(missing, "smtp.to")
	}
	if c.SMTP.Port <= 0 || c.SMTP.Port > 65535 {
		return fmt.Errorf("smtp.port must be between 1 and 65535")
	}
	if c.PG.Port <= 0 || c.PG.Port > 65535 {
		return fmt.Errorf("pg_port must be between 1 and 65535")
	}
	if c.LowBalanceThreshold <= 0 {
		return fmt.Errorf("reset.low_balance_threshold must be greater than 0")
	}
	if c.DailyMaxResetCount < 0 {
		return fmt.Errorf("reset.daily_max_reset_count must be greater than or equal to 0")
	}
	if c.LogRotationEnabled {
		if strings.TrimSpace(c.LogRotationArchiveDir) == "" {
			missing = append(missing, "logs.rotation.archive_dir")
		} else if samePath(c.QueryLogDir, c.LogRotationArchiveDir) {
			return fmt.Errorf("logs.rotation.archive_dir must be different from logs.query_log_dir")
		}
		if strings.TrimSpace(c.LogRotationKey) == "" {
			missing = append(missing, "LOG_ROTATION_KEY")
		}
	}
	if c.PollInterval <= 0 {
		return fmt.Errorf("polling.default_interval must be greater than 0")
	}
	if c.Polling.BalanceChangeEpsilon <= 0 {
		return fmt.Errorf("polling.balance_change_epsilon must be greater than 0")
	}
	if c.Polling.Subscription.Enabled {
		if c.Polling.Subscription.Quota <= 0 {
			return fmt.Errorf("polling.subscription.quota must be greater than 0 when subscription polling is enabled")
		}
		if len(c.Polling.Subscription.Tiers) == 0 {
			return fmt.Errorf("polling.subscription.tiers must contain at least one tier when subscription polling is enabled")
		}
	}
	for _, tier := range c.Polling.Subscription.Tiers {
		if tier.MinRatio < 0 {
			return fmt.Errorf("polling.subscription.tiers min_ratio must be greater than or equal to 0")
		}
		if tier.Interval <= 0 {
			return fmt.Errorf("polling.subscription.tiers interval must be greater than 0")
		}
	}
	if c.Polling.Sleep.Enabled {
		if c.Polling.Sleep.UnchangedFor <= 0 {
			return fmt.Errorf("polling.sleep.unchanged_for must be greater than 0")
		}
		if c.Polling.Sleep.Interval <= 0 {
			return fmt.Errorf("polling.sleep.interval must be greater than 0")
		}
	}
	if c.Polling.AfterResetEmail.Enabled && c.Polling.AfterResetEmail.Interval <= 0 {
		return fmt.Errorf("polling.after_reset_email.interval must be greater than 0")
	}
	if c.ConfirmTokenTTL <= 0 {
		return fmt.Errorf("reset.confirm_token_ttl must be greater than 0")
	}
	if c.ResetCooldown < 0 {
		return fmt.Errorf("reset.cooldown must not be negative")
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

func ParseManualConfirmWindow(value string) (ManualConfirmWindow, error) {
	parts := strings.Split(value, "-")
	if len(parts) != 2 {
		return ManualConfirmWindow{}, fmt.Errorf("reset.manual_confirm_time_range must use start-end format")
	}
	start, err := parseTimeOfDay(parts[0])
	if err != nil {
		return ManualConfirmWindow{}, fmt.Errorf("reset.manual_confirm_time_range start: %w", err)
	}
	end, err := parseTimeOfDay(parts[1])
	if err != nil {
		return ManualConfirmWindow{}, fmt.Errorf("reset.manual_confirm_time_range end: %w", err)
	}
	if start.minutes == end.minutes {
		return ManualConfirmWindow{}, fmt.Errorf("reset.manual_confirm_time_range start and end must be different")
	}
	return ManualConfirmWindow{Enabled: true, Start: start, End: end}, nil
}

func parseManualConfirmWindow(value string) (ManualConfirmWindow, error) {
	return ParseManualConfirmWindow(value)
}

func parseTimeOfDay(value string) (timeOfDay, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return timeOfDay{}, fmt.Errorf("time is empty")
	}
	parts := strings.Split(value, ":")
	if len(parts) > 2 {
		return timeOfDay{}, fmt.Errorf("invalid time %q", value)
	}
	hour, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return timeOfDay{}, fmt.Errorf("invalid hour %q", parts[0])
	}
	minute := 0
	if len(parts) == 2 {
		minute, err = strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return timeOfDay{}, fmt.Errorf("invalid minute %q", parts[1])
		}
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 {
		return timeOfDay{}, fmt.Errorf("time %q out of range", value)
	}
	return timeOfDay{minutes: hour*60 + minute}, nil
}

func (w ManualConfirmWindow) Contains(t time.Time) bool {
	if !w.Enabled {
		return false
	}
	minute := t.Hour()*60 + t.Minute()
	if w.Start.minutes < w.End.minutes {
		return minute >= w.Start.minutes && minute < w.End.minutes
	}
	return minute >= w.Start.minutes || minute < w.End.minutes
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

func defaultInt(value int, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func defaultFloat(value float64, fallback float64) float64 {
	if value == 0 {
		return fallback
	}
	return value
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
