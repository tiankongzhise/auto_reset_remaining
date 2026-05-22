package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"auto_reset_remaining/internal/api"
	"auto_reset_remaining/internal/config"
	"auto_reset_remaining/internal/envfile"
	"auto_reset_remaining/internal/mailer"
	"auto_reset_remaining/internal/store"
)

type APIClient interface {
	QueryBalance(ctx context.Context) (api.BalanceResult, error)
	ResetQuota(ctx context.Context) (api.ResetResult, error)
}

type Monitor struct {
	cfg     config.Config
	envPath string
	api     APIClient
	mailer  mailer.Sender
	store   store.Store
	queries *QueryLogger
	logger  *log.Logger

	mu                 sync.Mutex
	pendingManualEmail bool
	resetInFlight      bool
	lastAutoReset      time.Time
}

type ConfirmResult struct {
	Balance                   float64
	SubscriptionID            int64
	ManualConfirmSuccessCount int
	AutoResetEnabled          bool
	ResetLogID                int64
}

func NewMonitor(cfg config.Config, envPath string, apiClient APIClient, sender mailer.Sender, dataStore store.Store, queries *QueryLogger, logger *log.Logger) *Monitor {
	if logger == nil {
		logger = log.Default()
	}
	return &Monitor{
		cfg:     cfg,
		envPath: envPath,
		api:     apiClient,
		mailer:  sender,
		store:   dataStore,
		queries: queries,
		logger:  logger,
	}
}

func (m *Monitor) Initialize(ctx context.Context) error {
	pending, err := m.store.HasActiveConfirmToken(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.pendingManualEmail = pending
	m.mu.Unlock()
	return nil
}

func (m *Monitor) Run(ctx context.Context) {
	m.tickAndLog(ctx)
	ticker := time.NewTicker(m.cfg.PollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.tickAndLog(ctx)
		}
	}
}

func (m *Monitor) Tick(ctx context.Context) (string, error) {
	return m.handleTick(ctx)
}

func (m *Monitor) tickAndLog(ctx context.Context) {
	status, err := m.handleTick(ctx)
	if err != nil {
		m.logger.Printf("tick status=%s error=%v", status, err)
	}
}

func (m *Monitor) handleTick(ctx context.Context) (string, error) {
	start := time.Now()
	result, err := m.api.QueryBalance(ctx)
	entry := QueryLogEntry{
		Time:       start,
		DurationMS: time.Since(start).Milliseconds(),
	}
	if err != nil {
		entry.Status = "balance_error"
		entry.Error = err.Error()
		_ = m.queries.Log(entry)
		return entry.Status, err
	}
	entry.Balance = &result.Balance

	status, actionErr := m.handleBalance(ctx, result.Balance)
	entry.Status = status
	entry.DurationMS = time.Since(start).Milliseconds()
	if actionErr != nil {
		entry.Error = actionErr.Error()
	}
	if err := m.queries.Log(entry); err != nil && actionErr == nil {
		actionErr = err
	}
	return status, actionErr
}

func (m *Monitor) handleBalance(ctx context.Context, balance float64) (string, error) {
	cfg := m.snapshot()
	if cfg.AutoResetEnabled {
		if balance <= 0 {
			return m.maybeAutoReset(ctx, balance, cfg.ResetCooldown)
		}
		return "ok", nil
	}

	if balance >= cfg.LowBalanceThreshold {
		m.mu.Lock()
		m.pendingManualEmail = false
		m.mu.Unlock()
		return "ok", nil
	}

	m.mu.Lock()
	pending := m.pendingManualEmail
	m.mu.Unlock()
	if pending {
		return "low_balance_email_pending", nil
	}

	hasActive, err := m.store.HasActiveConfirmToken(ctx)
	if err != nil {
		return "confirm_token_check_error", err
	}
	if hasActive {
		m.mu.Lock()
		m.pendingManualEmail = true
		m.mu.Unlock()
		return "low_balance_email_pending", nil
	}

	rawToken, tokenHash, err := newConfirmToken()
	if err != nil {
		return "confirm_token_error", err
	}
	expiresAt := time.Now().Add(cfg.ConfirmTokenTTL)
	if err := m.store.CreateConfirmToken(ctx, tokenHash, balance, expiresAt); err != nil {
		return "confirm_token_store_error", err
	}

	link := confirmURL(cfg.PublicBaseURL, rawToken)
	subject := "余额不足，请确认重置订阅"
	body := fmt.Sprintf("当前余额 %.6f，已低于阈值 %.6f。\n\n点击下面链接确认重置订阅：\n%s\n\n链接将在 %s 过期。如果不是你本人操作，请忽略本邮件。",
		balance, cfg.LowBalanceThreshold, link, expiresAt.Format(time.RFC3339))
	if err := m.mailer.Send(ctx, subject, body); err != nil {
		_ = m.store.DeleteConfirmToken(ctx, tokenHash)
		return "low_balance_email_error", err
	}

	m.mu.Lock()
	m.pendingManualEmail = true
	m.mu.Unlock()
	return "low_balance_email_sent", nil
}

func (m *Monitor) maybeAutoReset(ctx context.Context, balance float64, cooldown time.Duration) (string, error) {
	now := time.Now()
	m.mu.Lock()
	if m.resetInFlight {
		m.mu.Unlock()
		return "auto_reset_in_progress", nil
	}
	if cooldown > 0 && !m.lastAutoReset.IsZero() && now.Sub(m.lastAutoReset) < cooldown {
		m.mu.Unlock()
		return "auto_reset_cooldown", nil
	}
	m.resetInFlight = true
	m.lastAutoReset = now
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.resetInFlight = false
		m.mu.Unlock()
	}()

	_, _, err := m.executeReset(ctx, "auto", balance)
	if err != nil {
		return "auto_reset_error", err
	}
	return "auto_reset_success", nil
}

func (m *Monitor) Confirm(ctx context.Context, rawToken string) (ConfirmResult, error) {
	rawToken = strings.TrimSpace(rawToken)
	if rawToken == "" {
		return ConfirmResult{}, store.ErrTokenInvalid
	}
	tokenHash := hashToken(rawToken)
	token, err := m.store.ConsumeConfirmToken(ctx, tokenHash)
	if err != nil {
		return ConfirmResult{}, err
	}
	m.mu.Lock()
	m.pendingManualEmail = false
	m.mu.Unlock()

	result, resetLogID, err := m.executeReset(ctx, "manual", token.Balance)
	if err != nil {
		return ConfirmResult{}, err
	}

	count, autoEnabled, err := m.incrementManualSuccess()
	if err != nil {
		return ConfirmResult{}, fmt.Errorf("reset succeeded but failed to update .env state: %w", err)
	}
	if err := m.store.MarkConfirmTokenReset(ctx, token.ID, resetLogID, count); err != nil {
		return ConfirmResult{}, fmt.Errorf("reset succeeded but failed to update confirm token: %w", err)
	}

	return ConfirmResult{
		Balance:                   token.Balance,
		SubscriptionID:            result.SubscriptionID,
		ManualConfirmSuccessCount: count,
		AutoResetEnabled:          autoEnabled,
		ResetLogID:                resetLogID,
	}, nil
}

func (m *Monitor) executeReset(ctx context.Context, mode string, balance float64) (api.ResetResult, int64, error) {
	result, resetErr := m.api.ResetQuota(ctx)
	entry := store.ResetLog{
		Mode:            mode,
		Balance:         balance,
		SubscriptionID:  result.SubscriptionID,
		Success:         resetErr == nil && result.Success,
		HTTPStatus:      result.HTTPStatus,
		ResponseSummary: result.ResponseSummary,
	}
	if resetErr != nil {
		entry.Error = resetErr.Error()
	}
	resetLogID, logErr := m.store.LogReset(ctx, entry)
	if logErr != nil {
		if resetErr != nil {
			return result, 0, fmt.Errorf("%w; additionally failed to write reset log: %v", resetErr, logErr)
		}
		return result, 0, fmt.Errorf("reset succeeded but failed to write reset log: %w", logErr)
	}
	if resetErr != nil {
		return result, resetLogID, resetErr
	}
	return result, resetLogID, nil
}

func (m *Monitor) incrementManualSuccess() (int, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := m.cfg.ManualConfirmSuccessCount + 1
	autoEnabled := m.cfg.AutoResetEnabled || next >= 3
	updates := map[string]string{
		"MANUAL_CONFIRM_SUCCESS_COUNT": strconv.Itoa(next),
	}
	if autoEnabled {
		updates["AUTO_RESET_ENABLED"] = "true"
	}
	if err := envfile.UpdateValues(m.envPath, updates); err != nil {
		return m.cfg.ManualConfirmSuccessCount, m.cfg.AutoResetEnabled, err
	}
	m.cfg.ManualConfirmSuccessCount = next
	m.cfg.AutoResetEnabled = autoEnabled
	return next, autoEnabled, nil
}

func (m *Monitor) snapshot() config.Config {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cfg
}

func newConfirmToken() (raw string, hash string, err error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", "", err
	}
	raw = base64.RawURLEncoding.EncodeToString(bytes)
	return raw, hashToken(raw), nil
}

func hashToken(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

func confirmURL(baseURL, rawToken string) string {
	return strings.TrimRight(baseURL, "/") + "/confirm-reset?token=" + url.QueryEscape(rawToken)
}

func IsInvalidToken(err error) bool {
	return errors.Is(err, store.ErrTokenInvalid)
}
