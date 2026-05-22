package service

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"auto_reset_remaining/internal/api"
	"auto_reset_remaining/internal/config"
	"auto_reset_remaining/internal/store"
)

func TestLowBalanceSendsOneEmail(t *testing.T) {
	ctx := context.Background()
	envPath := writeEnv(t, "AUTO_RESET_ENABLED=false\nMANUAL_CONFIRM_SUCCESS_COUNT=0\n")
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, envPath, apiClient, sender, dataStore, config.Config{})

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
	envPath := writeEnv(t, "AUTO_RESET_ENABLED=false\nMANUAL_CONFIRM_SUCCESS_COUNT=2\n")
	apiClient := &fakeAPI{balance: 0.4, resetResult: api.ResetResult{SubscriptionID: 1716, HTTPStatus: 200, Success: true}}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	rawToken := "manual-confirm-token"
	if err := dataStore.CreateConfirmToken(ctx, hashToken(rawToken), 0.4, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	monitor := newTestMonitor(t, envPath, apiClient, sender, dataStore, config.Config{
		ManualConfirmSuccessCount: 2,
	})

	result, err := monitor.Confirm(ctx, rawToken)
	if err != nil {
		t.Fatalf("Confirm() error = %v", err)
	}
	if result.ManualConfirmSuccessCount != 3 || !result.AutoResetEnabled {
		t.Fatalf("Confirm() = %+v, want count 3 and auto enabled", result)
	}
	body, err := os.ReadFile(envPath)
	if err != nil {
		t.Fatal(err)
	}
	text := string(body)
	if !strings.Contains(text, "AUTO_RESET_ENABLED=true") || !strings.Contains(text, "MANUAL_CONFIRM_SUCCESS_COUNT=3") {
		t.Fatalf(".env not updated correctly:\n%s", text)
	}
}

func TestConfirmClearsPendingEmail(t *testing.T) {
	ctx := context.Background()
	envPath := writeEnv(t, "AUTO_RESET_ENABLED=false\nMANUAL_CONFIRM_SUCCESS_COUNT=0\n")
	apiClient := &fakeAPI{balance: 0.4, resetResult: api.ResetResult{SubscriptionID: 1716, HTTPStatus: 200, Success: true}}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, envPath, apiClient, sender, dataStore, config.Config{})

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

func TestAutoModeResetsWhenBalanceIsZero(t *testing.T) {
	ctx := context.Background()
	envPath := writeEnv(t, "AUTO_RESET_ENABLED=true\nMANUAL_CONFIRM_SUCCESS_COUNT=3\n")
	apiClient := &fakeAPI{balance: 0, resetResult: api.ResetResult{SubscriptionID: 1716, HTTPStatus: 200, Success: true}}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, envPath, apiClient, sender, dataStore, config.Config{
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

func newTestMonitor(t *testing.T, envPath string, apiClient *fakeAPI, sender *fakeMailer, dataStore *fakeStore, overrides config.Config) *Monitor {
	t.Helper()
	cfg := config.Config{
		EnvPath:                   envPath,
		PublicBaseURL:             "https://service.example.com",
		LowBalanceThreshold:       0.5,
		ConfirmTokenTTL:           time.Hour,
		PollInterval:              time.Second,
		QueryLogDir:               filepath.Join(t.TempDir(), "logs"),
		ManualConfirmSuccessCount: 0,
		AutoResetEnabled:          false,
		ResetCooldown:             time.Minute,
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
	return NewMonitor(cfg, envPath, apiClient, sender, dataStore, NewQueryLogger(cfg.QueryLogDir), nil)
}

func writeEnv(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".env")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type fakeAPI struct {
	balance     float64
	balanceErr  error
	resetResult api.ResetResult
	resetErr    error
	resetCalls  int
}

func (f *fakeAPI) QueryBalance(context.Context) (api.BalanceResult, error) {
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
	nextID    int64
}

type fakeToken struct {
	id        int64
	balance   float64
	expiresAt time.Time
	used      bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{tokens: make(map[string]fakeToken), nextID: 1}
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
