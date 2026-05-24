package service

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"auto_reset_remaining/internal/api"
	"auto_reset_remaining/internal/config"
	"auto_reset_remaining/internal/store"
)

func TestLowBalanceSendsOneEmail(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("first Tick() error = %v", err)
	}
	if status != "low_balance_email_sent" {
		t.Fatalf("first Tick() status = %s", status)
	}
	status, err = monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("second Tick() error = %v", err)
	}
	if status != "low_balance_email_pending" {
		t.Fatalf("second Tick() status = %s", status)
	}
	if got := len(sender.messages); got != 1 {
		t.Fatalf("sent messages = %d, want 1", got)
	}
}

func TestConfirmThirdManualSuccessEnablesAutoReset(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4, resetResult: api.ResetResult{SubscriptionID: 1716, HTTPStatus: 200, Success: true}}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	rawToken := "manual-confirm-token"
	if err := dataStore.CreateConfirmToken(ctx, hashToken(rawToken), 0.4, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		ManualConfirmSuccessCount: 2,
	})

	result, err := monitor.Confirm(ctx, rawToken)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if result.ManualConfirmSuccessCount != 3 || !result.AutoResetEnabled {
		t.Fatalf("Confirm() = %+v, want count 3 and auto enabled", result)
	}
	body, err := os.ReadFile(monitor.configPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "auto_reset_enabled = true") || !strings.Contains(text, "manual_confirm_success_count = 3") {
		t.Fatalf("config.toml not updated correctly:\n%s", text)
	}
}

func TestConfirmClearsPendingEmail(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4, resetResult: api.ResetResult{SubscriptionID: 1716, HTTPStatus: 200, Success: true}}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	if status, err := monitor.Tick(ctx); err != nil || status != "low_balance_email_sent" {
		t.Fatalf("Tick() status=%s err=%v", status, err)
	}
	if len(dataStore.tokens) != 1 {
		t.Fatalf("tokens = %d, want 1", len(dataStore.tokens))
	}
	rawToken := extractToken(t, sender.messages[0])
	if _, err := monitor.Confirm(ctx, rawToken); err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if status, err := monitor.Tick(ctx); err != nil || status != "low_balance_email_sent" {
		t.Fatalf("Tick() after confirm status=%s err=%v", status, err)
	}
	if got := len(sender.messages); got != 2 {
		t.Fatalf("sent messages = %d, want 2", got)
	}
}

func TestResendConfirmEmailInvalidatesOldConfirmURL(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4, resetResult: api.ResetResult{SubscriptionID: 1716, HTTPStatus: 200, Success: true}}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		ResendResetEmailKey: "resend-secret",
	})

	if status, err := monitor.Tick(ctx); err != nil || status != "low_balance_email_sent" {
		t.Fatalf("Tick() status=%s err=%v", status, err)
	}
	oldToken := extractToken(t, sender.messages[0])

	result, err := monitor.ResendConfirmEmail(ctx, "resend-secret")
	if err != nil {
		t.Fatalf("ResendConfirmEmail() error = %v", err)
	}
	if result.Status != "resend_confirm_email_sent" {
		t.Fatalf("ResendConfirmEmail() status = %s", result.Status)
	}
	if result.InvalidatedOldLinks != 1 {
		t.Fatalf("InvalidatedOldLinks = %d, want 1", result.InvalidatedOldLinks)
	}
	if len(sender.messages) != 2 {
		t.Fatalf("sent messages = %d, want 2", len(sender.messages))
	}
	newToken := extractToken(t, sender.messages[1])
	if newToken == oldToken {
		t.Fatal("resend reused the old confirm token")
	}

	if _, err := monitor.Confirm(ctx, oldToken); !errors.Is(err, store.ErrTokenInvalid) {
		t.Fatalf("Confirm(oldToken) error = %v, want ErrTokenInvalid", err)
	}
	if _, err := monitor.Confirm(ctx, newToken); err != nil {
		t.Fatalf("Confirm(newToken) error = %v", err)
	}
}

func TestResendConfirmEmailRejectsWrongKey(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		ResendResetEmailKey: "resend-secret",
	})

	_, err := monitor.ResendConfirmEmail(ctx, "wrong")
	if !errors.Is(err, ErrResendUnauthorized) {
		t.Fatalf("ResendConfirmEmail() error = %v, want ErrResendUnauthorized", err)
	}
	if apiClient.balanceCalls != 0 {
		t.Fatalf("balanceCalls = %d, want 0", apiClient.balanceCalls)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("sent messages = %d, want 0", len(sender.messages))
	}
}

func TestAutoModeResetsWhenBalanceIsZero(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0, resetResult: api.ResetResult{SubscriptionID: 1716, HTTPStatus: 200, Success: true}}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		AutoResetEnabled: true,
		ResetCooldown:    -1,
	})

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if status != "auto_reset_success" {
		t.Fatalf("Tick() status = %s", status)
	}
	if apiClient.resetCalls != 1 {
		t.Fatalf("resetCalls = %d, want 1", apiClient.resetCalls)
	}
	if got := len(dataStore.resetLogs); got != 1 || !dataStore.resetLogs[0].Success || dataStore.resetLogs[0].Mode != "auto" {
		t.Fatalf("reset logs = %+v", dataStore.resetLogs)
	}
}

func TestAutoModeUsesManualConfirmWindow(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		AutoResetEnabled:    true,
		ManualConfirmWindow: mustManualConfirmWindow(t, "00:00-23:59"),
	})

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if status != "manual_confirm_email_sent" {
		t.Fatalf("Tick() status = %s", status)
	}
	if apiClient.resetCalls != 0 {
		t.Fatalf("resetCalls = %d, want 0", apiClient.resetCalls)
	}
	if got := len(sender.messages); got != 1 {
		t.Fatalf("sent messages = %d, want 1", got)
	}
}

func TestDailyMaxResetSendsLimitEmailAfterReset(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0, resetResult: api.ResetResult{SubscriptionID: 1716, HTTPStatus: 200, Success: true}}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		AutoResetEnabled:   true,
		ResetCooldown:      -1,
		DailyMaxResetCount: 1,
	})

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if status != "auto_reset_success" {
		t.Fatalf("Tick() status = %s", status)
	}
	if apiClient.resetCalls != 1 {
		t.Fatalf("resetCalls = %d, want 1", apiClient.resetCalls)
	}
	if got := len(sender.messages); got != 1 {
		t.Fatalf("sent messages = %d, want 1", got)
	}
	if !strings.Contains(sender.messages[0], "今日重置次数已达到上限") {
		t.Fatalf("daily limit email not sent:\n%s", sender.messages[0])
	}
}

func TestDailyLimitSendsPlanEmailThenPausesQueries(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	dataStore.resetLogs = append(dataStore.resetLogs, store.ResetLog{Mode: "auto", Success: true})
	dataStore.dailyState(todayKey()).dailyLimitEmailSent = true
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		AutoResetEnabled:   true,
		DailyMaxResetCount: 1,
	})

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if status != "plan_refresh_limit_email_sent" {
		t.Fatalf("Tick() status = %s", status)
	}
	if got := len(sender.messages); got != 1 {
		t.Fatalf("sent messages = %d, want 1", got)
	}
	if !strings.Contains(sender.messages[0], "已达到当前套餐可刷新上限") {
		t.Fatalf("plan limit email not sent:\n%s", sender.messages[0])
	}
	if apiClient.balanceCalls != 1 {
		t.Fatalf("balanceCalls = %d, want 1 before pause", apiClient.balanceCalls)
	}

	status, err = monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("second Tick() error = %v", err)
	}
	if status != "balance_query_paused" {
		t.Fatalf("second Tick() status = %s", status)
	}
	if apiClient.balanceCalls != 1 {
		t.Fatalf("balanceCalls = %d, want still 1 after pause", apiClient.balanceCalls)
	}
	if policy := monitor.nextPollPolicy(time.Now()); policy.Name != "balance_query_pause" || policy.Interval <= 0 {
		t.Fatalf("nextPollPolicy() after pause = %+v, want balance query pause with positive interval", policy)
	}
}

func TestConfirmRejectsWhenDailyLimitReachedWithoutConsumingToken(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4, resetResult: api.ResetResult{SubscriptionID: 1716, HTTPStatus: 200, Success: true}}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	dataStore.resetLogs = append(dataStore.resetLogs, store.ResetLog{Mode: "auto", Success: true})
	rawToken := "manual-confirm-token"
	tokenHash := hashToken(rawToken)
	if err := dataStore.CreateConfirmToken(ctx, tokenHash, 0.4, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		DailyMaxResetCount: 1,
	})

	_, err := monitor.Confirm(ctx, rawToken)
	if !errors.Is(err, ErrDailyResetLimitReached) {
		t.Fatalf("Confirm() error = %v, want ErrDailyResetLimitReached", err)
	}
	if apiClient.resetCalls != 0 {
		t.Fatalf("resetCalls = %d, want 0", apiClient.resetCalls)
	}
	if dataStore.tokens[tokenHash].used {
		t.Fatal("confirm token was consumed")
	}
}

func TestNextPollIntervalUsesSubscriptionTiers(t *testing.T) {
	cfg := config.PollingConfig{
		DefaultInterval:      time.Second,
		BalanceChangeEpsilon: 0.000001,
		Subscription: config.SubscriptionPollingConfig{
			Enabled: true,
			Quota:   100,
			Tiers: []config.SubscriptionTier{
				{MinRatio: 0, Interval: time.Second},
				{MinRatio: 0.70, Interval: time.Minute},
				{MinRatio: 0.40, Interval: 30 * time.Second},
			},
		},
	}
	got := nextPollInterval(cfg, pollingState{HasLastBalance: true, LastBalance: 76}, time.Now())
	if got != time.Minute {
		t.Fatalf("nextPollInterval() = %s, want 1m", got)
	}
	got = nextPollInterval(cfg, pollingState{HasLastBalance: true, LastBalance: 50}, time.Now())
	if got != 30*time.Second {
		t.Fatalf("nextPollInterval() = %s, want 30s", got)
	}
}

func TestNextPollIntervalPriorityAndSleep(t *testing.T) {
	now := time.Now()
	cfg := config.PollingConfig{
		DefaultInterval:      time.Second,
		BalanceChangeEpsilon: 0.000001,
		Subscription: config.SubscriptionPollingConfig{
			Enabled: true,
			Quota:   100,
			Tiers:   []config.SubscriptionTier{{MinRatio: 0, Interval: 5 * time.Second}},
		},
		Sleep: config.SleepPollingConfig{
			Enabled:      true,
			UnchangedFor: 10 * time.Minute,
			Interval:     time.Minute,
		},
		AfterResetEmail: config.AfterResetEmailPollingConfig{
			Enabled:  true,
			Interval: 2 * time.Minute,
		},
	}
	state := pollingState{
		HasLastBalance:              true,
		LastBalance:                 10,
		LastBalanceChangedAt:        now.Add(-30 * time.Minute),
		ForceFastUntilBalanceChange: true,
	}
	if got := nextPollInterval(cfg, state, now); got != 2*time.Minute {
		t.Fatalf("force fast interval = %s, want 2m", got)
	}
	state.ForceFastUntilBalanceChange = false
	if got := nextPollInterval(cfg, state, now); got != time.Minute {
		t.Fatalf("sleep interval = %s, want 1m", got)
	}
}

func TestBalanceChangedUsesEpsilon(t *testing.T) {
	if balanceChanged(76.8342548, 76.8342558, 0.000001) {
		t.Fatal("difference equal to epsilon should not count as change")
	}
	if !balanceChanged(76.9694068, 76.8342548, 0.000001) {
		t.Fatal("real balance movement should count as change")
	}
}

func newTestMonitor(t *testing.T, apiClient *fakeAPI, sender *fakeMailer, dataStore *fakeStore, overrides config.Config) *Monitor {
	t.Helper()
	configPath := writeConfig(t, overrides.ManualConfirmSuccessCount, overrides.AutoResetEnabled)
	cfg := config.Config{
		TOMLPath:                  configPath,
		PublicBaseURL:             "https://service.example.com",
		LowBalanceThreshold:       0.5,
		ConfirmTokenTTL:           time.Hour,
		PollInterval:              time.Second,
		QueryLogDir:               filepath.Join(t.TempDir(), "logs"),
		ResendResetEmailKey:       overrides.ResendResetEmailKey,
		ManualConfirmSuccessCount: 0,
		AutoResetEnabled:          false,
		ResetCooldown:             time.Minute,
		Polling: config.PollingConfig{
			DefaultInterval:      time.Second,
			BalanceChangeEpsilon: 0.000001,
		},
	}
	if overrides.ManualConfirmSuccessCount != 0 {
		cfg.ManualConfirmSuccessCount = overrides.ManualConfirmSuccessCount
	}
	if overrides.AutoResetEnabled {
		cfg.AutoResetEnabled = true
	}
	if overrides.ResetCooldown < 0 {
		cfg.ResetCooldown = 0
	}
	if overrides.ManualConfirmWindow.Enabled {
		cfg.ManualConfirmWindow = overrides.ManualConfirmWindow
	}
	if overrides.DailyMaxResetCount != 0 {
		cfg.DailyMaxResetCount = overrides.DailyMaxResetCount
	}
	return NewMonitor(cfg, "", apiClient, sender, dataStore, NewQueryLogger(cfg.QueryLogDir), nil)
}

func writeConfig(t *testing.T, manualConfirmSuccessCount int, autoResetEnabled bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.toml")
	body := strings.ReplaceAll(testServiceConfigTOML, "{{AUTO_RESET_ENABLED}}", boolString(autoResetEnabled))
	body = strings.ReplaceAll(body, "{{MANUAL_CONFIRM_SUCCESS_COUNT}}", intString(manualConfirmSuccessCount))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func boolString(value bool) string {
	if value {
		return "true"
	}
	return "false"
}

func intString(value int) string {
	return strconv.Itoa(value)
}

func mustManualConfirmWindow(t *testing.T, value string) config.ManualConfirmWindow {
	t.Helper()
	window, err := config.ParseManualConfirmWindow(value)
	if err != nil {
		t.Fatal(err)
	}
	return window
}

const testServiceConfigTOML = `
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
sslmode = "disable"

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
auto_reset_enabled = {{AUTO_RESET_ENABLED}}
manual_confirm_success_count = {{MANUAL_CONFIRM_SUCCESS_COUNT}}
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

type fakeAPI struct {
	balance      float64
	balanceErr   error
	resetResult  api.ResetResult
	resetErr     error
	balanceCalls int
	resetCalls   int
}

func (f *fakeAPI) QueryBalance(context.Context) (api.BalanceResult, error) {
	f.balanceCalls++
	if f.balanceErr != nil {
		return api.BalanceResult{}, f.balanceErr
	}
	return api.BalanceResult{Balance: f.balance}, nil
}

func (f *fakeAPI) ResetQuota(context.Context) (api.ResetResult, error) {
	f.resetCalls++
	if f.resetErr != nil {
		return f.resetResult, f.resetErr
	}
	if f.resetResult.SubscriptionID == 0 {
		f.resetResult.SubscriptionID = 1716
	}
	f.resetResult.Success = true
	return f.resetResult, nil
}

type fakeMailer struct {
	messages []string
}

func (f *fakeMailer) Send(_ context.Context, subject string, body string) error {
	f.messages = append(f.messages, subject+"\n"+body)
	return nil
}

type fakeStore struct {
	tokens    map[string]fakeToken
	resetLogs []store.ResetLog
	daily     map[string]*fakeDailyState
	nextID    int64
}

type fakeToken struct {
	id        int64
	balance   float64
	expiresAt time.Time
	used      bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{tokens: make(map[string]fakeToken), daily: make(map[string]*fakeDailyState), nextID: 1}
}

func (f *fakeStore) Init(context.Context) error {
	return nil
}

func (f *fakeStore) CreateConfirmToken(_ context.Context, tokenHash string, balance float64, expiresAt time.Time) error {
	f.tokens[tokenHash] = fakeToken{id: f.nextID, balance: balance, expiresAt: expiresAt}
	f.nextID++
	return nil
}

func (f *fakeStore) DeleteConfirmToken(_ context.Context, tokenHash string) error {
	delete(f.tokens, tokenHash)
	return nil
}

func (f *fakeStore) DeleteOtherActiveConfirmTokens(_ context.Context, keepTokenHash string) (int64, error) {
	var deleted int64
	now := time.Now()
	for tokenHash, token := range f.tokens {
		if tokenHash == keepTokenHash || token.used || !token.expiresAt.After(now) {
			continue
		}
		delete(f.tokens, tokenHash)
		deleted++
	}
	return deleted, nil
}

func (f *fakeStore) HasActiveConfirmToken(context.Context) (bool, error) {
	now := time.Now()
	for _, token := range f.tokens {
		if !token.used && token.expiresAt.After(now) {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) ConsumeConfirmToken(_ context.Context, tokenHash string) (store.ConfirmToken, error) {
	token, ok := f.tokens[tokenHash]
	if !ok || token.used || token.expiresAt.Before(time.Now()) {
		return store.ConfirmToken{}, store.ErrTokenInvalid
	}
	token.used = true
	f.tokens[tokenHash] = token
	return store.ConfirmToken{ID: token.id, Balance: token.balance, ExpiresAt: token.expiresAt}, nil
}

func (f *fakeStore) MarkConfirmTokenReset(context.Context, int64, int64, int) error {
	return nil
}

func (f *fakeStore) LogReset(_ context.Context, entry store.ResetLog) (int64, error) {
	if entry.Mode == "" {
		return 0, errors.New("mode required")
	}
	f.resetLogs = append(f.resetLogs, entry)
	id := f.nextID
	f.nextID++
	return id, nil
}

func (f *fakeStore) GetDailyResetLimitState(_ context.Context, day time.Time, maxResetCount int) (store.DailyResetLimitState, error) {
	state := f.dailyState(dayKey(day))
	count := 0
	for _, entry := range f.resetLogs {
		if entry.Success {
			count++
		}
	}
	return store.DailyResetLimitState{
		Day:                       truncateTestDay(day),
		ResetCount:                count,
		MaxResetCount:             maxResetCount,
		DailyLimitEmailSent:       state.dailyLimitEmailSent,
		PlanRefreshLimitEmailSent: state.planRefreshLimitEmailSent,
		BalanceQueryPaused:        maxResetCount > 0 && count >= maxResetCount && state.planRefreshLimitEmailSent,
	}, nil
}

func (f *fakeStore) MarkDailyLimitEmailSent(_ context.Context, day time.Time) error {
	f.dailyState(dayKey(day)).dailyLimitEmailSent = true
	return nil
}

func (f *fakeStore) MarkPlanRefreshLimitEmailSent(_ context.Context, day time.Time) error {
	f.dailyState(dayKey(day)).planRefreshLimitEmailSent = true
	return nil
}

type fakeDailyState struct {
	dailyLimitEmailSent       bool
	planRefreshLimitEmailSent bool
}

func (f *fakeStore) dailyState(key string) *fakeDailyState {
	state := f.daily[key]
	if state == nil {
		state = &fakeDailyState{}
		f.daily[key] = state
	}
	return state
}

func todayKey() string {
	return dayKey(time.Now())
}

func dayKey(t time.Time) string {
	return truncateTestDay(t).Format("2006-01-02")
}

func truncateTestDay(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func extractToken(t *testing.T, message string) string {
	t.Helper()
	for _, field := range strings.Fields(message) {
		if !strings.Contains(field, "/confirm-reset?") {
			continue
		}
		parsed, err := url.Parse(strings.TrimSpace(field))
		if err != nil {
			t.Fatalf("parse confirm URL: %v", err)
		}
		token := parsed.Query().Get("token")
		if token == "" {
			t.Fatal("confirm URL missing token")
		}
		return token
	}
	t.Fatalf("message missing confirm URL:\n%s", message)
	return ""
}
