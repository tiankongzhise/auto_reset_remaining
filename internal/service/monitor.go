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
	"math"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"auto_reset_remaining/internal/api"
	"auto_reset_remaining/internal/config"
	"auto_reset_remaining/internal/mailer"
	"auto_reset_remaining/internal/store"
)

type APIClient interface {
	QueryBalance(ctx context.Context) (api.BalanceResult, error)
	ResetQuota(ctx context.Context) (api.ResetResult, error)
}

type Monitor struct {
	cfg        config.Config
	configPath string
	api        APIClient
	mailer     mailer.Sender
	store      store.Store
	queries    *QueryLogger
	logger     *log.Logger

	mu                 sync.Mutex
	pendingManualEmail bool
	resetInFlight      bool
	lastAutoReset      time.Time

	hasLastBalance              bool
	lastBalance                 float64
	lastBalanceChangedAt        time.Time
	forceFastUntilBalanceChange bool
	forceFastReferenceBalance   float64
	lastPollingPolicyKey        string
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
	configPath := cfg.TOMLPath
	if configPath == "" {
		configPath = envPath
	}
	return &Monitor{
		cfg:        cfg,
		configPath: configPath,
		api:        apiClient,
		mailer:     sender,
		store:      dataStore,
		queries:    queries,
		logger:     logger,
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
	m.logPollingStartup(time.Now())
	return nil
}

func (m *Monitor) Run(ctx context.Context) {
	next := m.tickAndLog(ctx)
	timer := time.NewTimer(next)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			next = m.tickAndLog(ctx)
			timer.Reset(next)
		}
	}
}

func (m *Monitor) Tick(ctx context.Context) (string, error) {
	return m.handleTick(ctx)
}

func (m *Monitor) tickAndLog(ctx context.Context) time.Duration {
	status, err := m.handleTick(ctx)
	if err != nil {
		m.logger.Printf("tick status=%s error=%v", status, err)
	}
	policy := m.nextPollPolicy(time.Now())
	m.logPollingPolicy(policy, "tick", false)
	return policy.Interval
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
	m.observeBalance(start, result.Balance)

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
			if cfg.ManualConfirmWindow.Contains(time.Now()) {
				return m.maybeSendConfirmEmail(ctx, balance, cfg, "manual_confirm")
			}
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

	return m.maybeSendConfirmEmail(ctx, balance, cfg, "low_balance")
}

func (m *Monitor) maybeSendConfirmEmail(ctx context.Context, balance float64, cfg config.Config, statusPrefix string) (string, error) {
	m.mu.Lock()
	pending := m.pendingManualEmail
	m.mu.Unlock()
	if pending {
		return statusPrefix + "_email_pending", nil
	}

	hasActive, err := m.store.HasActiveConfirmToken(ctx)
	if err != nil {
		m.logger.Printf("confirm email token check failed status_prefix=%s balance=%.6f error=%v", statusPrefix, balance, err)
		return "confirm_token_check_error", err
	}
	if hasActive {
		m.mu.Lock()
		m.pendingManualEmail = true
		m.mu.Unlock()
		m.logger.Printf("confirm email skipped status_prefix=%s balance=%.6f reason=active_token_exists", statusPrefix, balance)
		return statusPrefix + "_email_pending", nil
	}

	rawToken, tokenHash, err := newConfirmToken()
	if err != nil {
		m.logger.Printf("confirm email token creation failed status_prefix=%s balance=%.6f error=%v", statusPrefix, balance, err)
		return "confirm_token_error", err
	}
	expiresAt := time.Now().Add(cfg.ConfirmTokenTTL)
	if err := m.store.CreateConfirmToken(ctx, tokenHash, balance, expiresAt); err != nil {
		m.logger.Printf("confirm email token store failed status_prefix=%s balance=%.6f token_hash_prefix=%s error=%v", statusPrefix, balance, tokenHashPrefix(tokenHash), err)
		return "confirm_token_store_error", err
	}

	link := confirmURL(cfg.PublicBaseURL, rawToken)
	subject := "余额不足，请确认重置订阅"
	body := fmt.Sprintf("当前余额 %.6f，已低于阈值 %.6f。\n\n点击下面链接确认重置订阅：\n%s\n\n链接将在 %s 过期。如果不是你本人操作，请忽略本邮件。",
		balance, cfg.LowBalanceThreshold, link, expiresAt.Format(time.RFC3339))
	m.logger.Printf("confirm email sending status_prefix=%s balance=%.6f threshold=%.6f expires_at=%s recipients=%d confirm_url=%s token_hash_prefix=%s",
		statusPrefix, balance, cfg.LowBalanceThreshold, expiresAt.Format(time.RFC3339), len(cfg.SMTP.To), redactConfirmURL(link), tokenHashPrefix(tokenHash))
	if err := m.mailer.Send(ctx, subject, body); err != nil {
		_ = m.store.DeleteConfirmToken(ctx, tokenHash)
		m.logger.Printf("confirm email send failed status_prefix=%s balance=%.6f token_hash_prefix=%s error=%v", statusPrefix, balance, tokenHashPrefix(tokenHash), err)
		return statusPrefix + "_email_error", err
	}

	m.mu.Lock()
	m.pendingManualEmail = true
	if cfg.Polling.AfterResetEmail.Enabled {
		m.forceFastUntilBalanceChange = true
		m.forceFastReferenceBalance = balance
	}
	m.mu.Unlock()
	m.logger.Printf("confirm email sent status_prefix=%s balance=%.6f expires_at=%s token_hash_prefix=%s after_reset_email_polling=%t",
		statusPrefix, balance, expiresAt.Format(time.RFC3339), tokenHashPrefix(tokenHash), cfg.Polling.AfterResetEmail.Enabled)
	return statusPrefix + "_email_sent", nil
}

func (m *Monitor) maybeAutoReset(ctx context.Context, balance float64, cooldown time.Duration) (string, error) {
	now := time.Now()
	m.mu.Lock()
	if m.resetInFlight {
		m.mu.Unlock()
		m.logger.Printf("auto reset skipped balance=%.6f reason=in_progress", balance)
		return "auto_reset_in_progress", nil
	}
	if cooldown > 0 && !m.lastAutoReset.IsZero() && now.Sub(m.lastAutoReset) < cooldown {
		remaining := cooldown - now.Sub(m.lastAutoReset)
		m.mu.Unlock()
		m.logger.Printf("auto reset skipped balance=%.6f reason=cooldown remaining=%s", balance, remaining.Round(time.Second))
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
		m.logger.Print("confirm reset URL rejected reason=missing_token")
		return ConfirmResult{}, store.ErrTokenInvalid
	}
	tokenHash := hashToken(rawToken)
	tokenPrefix := tokenHashPrefix(tokenHash)
	m.logger.Printf("confirm reset URL requested token_hash_prefix=%s", tokenPrefix)
	token, err := m.store.ConsumeConfirmToken(ctx, tokenHash)
	if err != nil {
		m.logger.Printf("confirm reset URL rejected token_hash_prefix=%s error=%v", tokenPrefix, err)
		return ConfirmResult{}, err
	}
	m.mu.Lock()
	m.pendingManualEmail = false
	m.mu.Unlock()

	result, resetLogID, err := m.executeReset(ctx, "manual", token.Balance)
	if err != nil {
		m.logger.Printf("confirm reset URL failed token_hash_prefix=%s balance=%.6f error=%v", tokenPrefix, token.Balance, err)
		return ConfirmResult{}, err
	}

	count, autoEnabled, err := m.incrementManualSuccess()
	if err != nil {
		m.logger.Printf("confirm reset URL failed token_hash_prefix=%s reset_log_id=%d error=%v", tokenPrefix, resetLogID, err)
		return ConfirmResult{}, fmt.Errorf("reset succeeded but failed to update config.toml state: %w", err)
	}
	if err := m.store.MarkConfirmTokenReset(ctx, token.ID, resetLogID, count); err != nil {
		m.logger.Printf("confirm reset URL failed token_hash_prefix=%s reset_log_id=%d error=%v", tokenPrefix, resetLogID, err)
		return ConfirmResult{}, fmt.Errorf("reset succeeded but failed to update confirm token: %w", err)
	}

	m.logger.Printf("confirm reset URL succeeded token_hash_prefix=%s subscription_id=%d balance=%.6f manual_success_count=%d auto_reset_enabled=%t reset_log_id=%d",
		tokenPrefix, result.SubscriptionID, token.Balance, count, autoEnabled, resetLogID)
	return ConfirmResult{
		Balance:                   token.Balance,
		SubscriptionID:            result.SubscriptionID,
		ManualConfirmSuccessCount: count,
		AutoResetEnabled:          autoEnabled,
		ResetLogID:                resetLogID,
	}, nil
}

func (m *Monitor) executeReset(ctx context.Context, mode string, balance float64) (api.ResetResult, int64, error) {
	m.logger.Printf("subscription reset started mode=%s balance=%.6f", mode, balance)
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
			m.logger.Printf("subscription reset failed mode=%s balance=%.6f subscription_id=%d http_status=%d reset_log_error=%v error=%v",
				mode, balance, result.SubscriptionID, result.HTTPStatus, logErr, resetErr)
			return result, 0, fmt.Errorf("%w; additionally failed to write reset log: %v", resetErr, logErr)
		}
		m.logger.Printf("subscription reset log failed mode=%s balance=%.6f subscription_id=%d http_status=%d error=%v",
			mode, balance, result.SubscriptionID, result.HTTPStatus, logErr)
		return result, 0, fmt.Errorf("reset succeeded but failed to write reset log: %w", logErr)
	}
	if resetErr != nil {
		m.logger.Printf("subscription reset failed mode=%s balance=%.6f subscription_id=%d http_status=%d reset_log_id=%d error=%v",
			mode, balance, result.SubscriptionID, result.HTTPStatus, resetLogID, resetErr)
		return result, resetLogID, resetErr
	}
	m.logger.Printf("subscription reset succeeded mode=%s balance=%.6f subscription_id=%d http_status=%d reset_log_id=%d response=%s",
		mode, balance, result.SubscriptionID, result.HTTPStatus, resetLogID, result.ResponseSummary)
	return result, resetLogID, nil
}

func (m *Monitor) incrementManualSuccess() (int, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	next := m.cfg.ManualConfirmSuccessCount + 1
	autoEnabled := m.cfg.AutoResetEnabled || next >= 3
	if err := config.UpdateRuntimeState(m.configPath, next, autoEnabled); err != nil {
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

func (m *Monitor) observeBalance(now time.Time, balance float64) {
	cfg := m.snapshot()
	epsilon := cfg.Polling.BalanceChangeEpsilon

	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.hasLastBalance {
		m.hasLastBalance = true
		m.lastBalance = balance
		m.lastBalanceChangedAt = now
		return
	}
	if !balanceChanged(m.lastBalance, balance, epsilon) {
		return
	}
	m.lastBalance = balance
	m.lastBalanceChangedAt = now
	if m.forceFastUntilBalanceChange && balanceChanged(m.forceFastReferenceBalance, balance, epsilon) {
		m.forceFastUntilBalanceChange = false
	}
}

func (m *Monitor) nextPollInterval(now time.Time) time.Duration {
	return m.nextPollPolicy(now).Interval
}

func (m *Monitor) nextPollPolicy(now time.Time) pollingPolicy {
	cfg := m.snapshot()
	m.mu.Lock()
	state := pollingState{
		HasLastBalance:              m.hasLastBalance,
		LastBalance:                 m.lastBalance,
		LastBalanceChangedAt:        m.lastBalanceChangedAt,
		ForceFastUntilBalanceChange: m.forceFastUntilBalanceChange,
	}
	m.mu.Unlock()
	return selectPollPolicy(cfg.Polling, state, now)
}

func (m *Monitor) logPollingStartup(now time.Time) {
	m.logger.Printf("balance polling strategies available: %s", describePollingStrategies(m.snapshot().Polling))
	policy := m.nextPollPolicy(now)
	m.logPollingPolicy(policy, "startup", true)
}

func (m *Monitor) logPollingPolicy(policy pollingPolicy, event string, force bool) {
	m.mu.Lock()
	previous := m.lastPollingPolicyKey
	changed := previous != policy.Key
	if force || changed {
		m.lastPollingPolicyKey = policy.Key
	}
	m.mu.Unlock()
	if force {
		m.logger.Printf("balance polling strategy active event=%s name=%s interval=%s reason=%s", event, policy.Name, policy.Interval, policy.Reason)
		return
	}
	if changed {
		previousValue := previous
		if previousValue == "" {
			previousValue = "none"
		}
		m.logger.Printf("balance polling strategy changed event=%s previous=%s current=%s interval=%s reason=%s",
			event, previousValue, policy.Key, policy.Interval, policy.Reason)
	}
}

type pollingState struct {
	HasLastBalance              bool
	LastBalance                 float64
	LastBalanceChangedAt        time.Time
	ForceFastUntilBalanceChange bool
}

func nextPollInterval(cfg config.PollingConfig, state pollingState, now time.Time) time.Duration {
	return selectPollPolicy(cfg, state, now).Interval
}

type pollingPolicy struct {
	Name     string
	Key      string
	Interval time.Duration
	Reason   string
}

func selectPollPolicy(cfg config.PollingConfig, state pollingState, now time.Time) pollingPolicy {
	if cfg.AfterResetEmail.Enabled && state.ForceFastUntilBalanceChange {
		interval := positiveDuration(cfg.AfterResetEmail.Interval, cfg.DefaultInterval)
		return pollingPolicy{
			Name:     "after_reset_email",
			Key:      "after_reset_email:" + interval.String(),
			Interval: interval,
			Reason:   "waiting for balance change after confirm email",
		}
	}
	if cfg.Sleep.Enabled && state.HasLastBalance && !state.LastBalanceChangedAt.IsZero() && now.Sub(state.LastBalanceChangedAt) >= cfg.Sleep.UnchangedFor {
		interval := positiveDuration(cfg.Sleep.Interval, cfg.DefaultInterval)
		return pollingPolicy{
			Name:     "sleep",
			Key:      "sleep:" + interval.String(),
			Interval: interval,
			Reason:   fmt.Sprintf("balance unchanged for %s", now.Sub(state.LastBalanceChangedAt).Round(time.Second)),
		}
	}
	if cfg.Subscription.Enabled && cfg.Subscription.Quota > 0 && state.HasLastBalance {
		ratio := state.LastBalance / cfg.Subscription.Quota
		if ratio > 1 {
			ratio = 1
		}
		for _, tier := range sortedTiers(cfg.Subscription.Tiers) {
			if ratio >= tier.MinRatio {
				interval := positiveDuration(tier.Interval, cfg.DefaultInterval)
				return pollingPolicy{
					Name:     "subscription",
					Key:      fmt.Sprintf("subscription:min_ratio=%.6g:%s", tier.MinRatio, interval),
					Interval: interval,
					Reason:   fmt.Sprintf("balance=%.6f quota=%.6f ratio=%.4f min_ratio=%.4f", state.LastBalance, cfg.Subscription.Quota, ratio, tier.MinRatio),
				}
			}
		}
	}
	interval := positiveDuration(cfg.DefaultInterval, time.Second)
	reason := "fallback default interval"
	if !state.HasLastBalance {
		reason = "no balance sample yet"
	}
	return pollingPolicy{
		Name:     "default",
		Key:      "default:" + interval.String(),
		Interval: interval,
		Reason:   reason,
	}
}

func describePollingStrategies(cfg config.PollingConfig) string {
	parts := []string{
		fmt.Sprintf("default(enabled interval=%s)", positiveDuration(cfg.DefaultInterval, time.Second)),
	}
	if cfg.AfterResetEmail.Enabled {
		parts = append(parts, fmt.Sprintf("after_reset_email(enabled interval=%s)", positiveDuration(cfg.AfterResetEmail.Interval, cfg.DefaultInterval)))
	} else {
		parts = append(parts, "after_reset_email(disabled)")
	}
	if cfg.Sleep.Enabled {
		parts = append(parts, fmt.Sprintf("sleep(enabled unchanged_for=%s interval=%s)", cfg.Sleep.UnchangedFor, positiveDuration(cfg.Sleep.Interval, cfg.DefaultInterval)))
	} else {
		parts = append(parts, "sleep(disabled)")
	}
	if cfg.Subscription.Enabled {
		tiers := sortedTiers(cfg.Subscription.Tiers)
		tierParts := make([]string, 0, len(tiers))
		for _, tier := range tiers {
			tierParts = append(tierParts, fmt.Sprintf("min_ratio=%.4f interval=%s", tier.MinRatio, positiveDuration(tier.Interval, cfg.DefaultInterval)))
		}
		parts = append(parts, fmt.Sprintf("subscription(enabled quota=%.6f tiers=[%s])", cfg.Subscription.Quota, strings.Join(tierParts, "; ")))
	} else {
		parts = append(parts, "subscription(disabled)")
	}
	return strings.Join(parts, ", ")
}

func sortedTiers(tiers []config.SubscriptionTier) []config.SubscriptionTier {
	sorted := append([]config.SubscriptionTier(nil), tiers...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return sorted[i].MinRatio > sorted[j].MinRatio
	})
	return sorted
}

func positiveDuration(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	if fallback > 0 {
		return fallback
	}
	return time.Second
}

func balanceChanged(a, b, epsilon float64) bool {
	return math.Abs(a-b) > epsilon
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

func tokenHashPrefix(tokenHash string) string {
	if len(tokenHash) <= 12 {
		return tokenHash
	}
	return tokenHash[:12]
}

func confirmURL(baseURL, rawToken string) string {
	return strings.TrimRight(baseURL, "/") + "/confirm-reset?token=" + url.QueryEscape(rawToken)
}

func redactConfirmURL(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid-url>"
	}
	query := parsed.Query()
	if query.Has("token") {
		query.Set("token", "<redacted>")
		parsed.RawQuery = query.Encode()
	}
	return parsed.String()
}

func IsInvalidToken(err error) bool {
	return errors.Is(err, store.ErrTokenInvalid)
}
