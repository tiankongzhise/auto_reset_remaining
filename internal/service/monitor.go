package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"html"
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

var (
	ErrDailyResetLimitReached          = errors.New("daily reset limit reached")
	ErrResendUnauthorized              = errors.New("invalid resend reset email key")
	ErrResendKeyMissing                = errors.New("resend reset email key is not configured")
	ErrExternalManualResetDisabled     = errors.New("external manual reset is disabled")
	ErrExternalManualResetUnauthorized = errors.New("invalid external manual reset key")
	ErrExternalManualResetKeyMissing   = errors.New("external manual reset key is not configured")
	ErrTestResetEmailUnauthorized      = errors.New("invalid test reset email key")
	ErrTestResetEmailKeyMissing        = errors.New("test reset email key is not configured")
	ErrCancelResetEmailUnauthorized    = errors.New("invalid cancel reset email key")
	ErrCancelResetEmailKeyMissing      = errors.New("cancel reset email key is not configured")
	ErrResetInProgress                 = errors.New("subscription reset is already in progress")
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
	balanceQueryPausedUntil     time.Time
}

type ConfirmResult struct {
	Balance                   float64
	SubscriptionID            int64
	ManualConfirmSuccessCount int
	AutoResetEnabled          bool
	ResetLogID                int64
}

type ResendConfirmEmailResult struct {
	Balance             float64   `json:"balance"`
	ExpiresAt           time.Time `json:"expires_at"`
	InvalidatedOldLinks int64     `json:"invalidated_old_links"`
	Status              string    `json:"status"`
	NextPollInterval    string    `json:"next_poll_interval,omitempty"`
}

type ManualResetResult struct {
	Balance        float64 `json:"balance"`
	SubscriptionID int64   `json:"subscription_id,omitempty"`
	ResetLogID     int64   `json:"reset_log_id,omitempty"`
	EmailSent      bool    `json:"email_sent"`
	Status         string  `json:"status"`
}

type TestResetEmailResult struct {
	Balance      float64 `json:"balance"`
	EmailSent    bool    `json:"email_sent"`
	ResetSkipped bool    `json:"reset_skipped"`
	Status       string  `json:"status"`
}

type CancelResetEmailResult struct {
	Cancelled int64  `json:"cancelled"`
	Status    string `json:"status"`
}

func NewMonitor(cfg config.Config, envPath string, apiClient APIClient, sender mailer.Sender, dataStore store.Store, queries *QueryLogger, logger *log.Logger) *Monitor {
	if logger == nil {
		logger = log.Default()
	}
	if cfg.EnvPath == "" {
		cfg.EnvPath = envPath
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
	invalidated, err := m.store.InvalidateActiveConfirmTokensOnStartup(ctx)
	if err != nil {
		return err
	}
	if len(invalidated) > 0 {
		if err := m.sendStartupInvalidatedConfirmTokensEmail(ctx, invalidated); err != nil {
			m.logger.Printf("startup confirm token invalidation notification failed invalidated=%d error=%v", len(invalidated), err)
		}
	}
	pending, err := m.store.HasActiveConfirmToken(ctx)
	if err != nil {
		return err
	}
	m.mu.Lock()
	m.pendingManualEmail = pending
	m.mu.Unlock()
	m.logPollingStartup(m.businessNow(m.snapshot()))
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
	policy := m.nextPollPolicy(m.businessNow(m.snapshot()))
	m.logPollingPolicy(policy, "tick", false)
	return policy.Interval
}

func (m *Monitor) handleTick(ctx context.Context) (string, error) {
	cfg := m.snapshot()
	start := m.businessNow(cfg)
	state, stateErr := m.dailyResetLimitState(ctx, cfg, start)
	if stateErr != nil {
		entry := QueryLogEntry{
			Time:       start,
			DurationMS: time.Since(start).Milliseconds(),
			Status:     "daily_reset_limit_state_error",
			Error:      stateErr.Error(),
		}
		_ = m.queries.Log(entry)
		return entry.Status, stateErr
	}
	if state.BalanceQueryPaused {
		m.pauseBalanceQueriesUntil(state.Day.AddDate(0, 0, 1))
		entry := QueryLogEntry{
			Time:       start,
			DurationMS: time.Since(start).Milliseconds(),
			Status:     "balance_query_paused",
		}
		if err := m.queries.Log(entry); err != nil {
			return entry.Status, err
		}
		return entry.Status, nil
	}
	m.clearBalanceQueryPause()
	if status, err := m.ensureDailyLimitEmail(ctx, cfg, state); err != nil || status != "" {
		entry := QueryLogEntry{
			Time:       start,
			DurationMS: time.Since(start).Milliseconds(),
			Status:     status,
		}
		if err != nil {
			entry.Error = err.Error()
		}
		if logErr := m.queries.Log(entry); logErr != nil && err == nil {
			err = logErr
		}
		return status, err
	}

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

	status, actionErr := m.handleBalance(ctx, result.Balance, start)
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

func (m *Monitor) handleBalance(ctx context.Context, balance float64, now time.Time) (string, error) {
	cfg := m.snapshot()
	now = now.In(cfg.BusinessLocation())
	state, err := m.dailyResetLimitState(ctx, cfg, now)
	if err != nil {
		return "daily_reset_limit_state_error", err
	}
	if state.MaxResetCount > 0 && state.ResetCount >= state.MaxResetCount {
		if balance >= cfg.LowBalanceThreshold {
			if status, err := m.handleRecoveredBalance(ctx, balance, cfg); err != nil || status != "ok" {
				return status, err
			}
		}
		if balance <= 0 {
			return m.maybeSendPlanRefreshLimitEmail(ctx, balance, cfg, state)
		}
		return "daily_reset_limit_reached", nil
	}
	if balance >= cfg.LowBalanceThreshold {
		return m.handleRecoveredBalance(ctx, balance, cfg)
	}
	if cfg.AutoResetEnabled {
		if balance <= 0 {
			if cfg.ManualConfirmWindow.Contains(now) {
				return m.maybeSendConfirmEmail(ctx, balance, cfg, "manual_confirm")
			}
			return m.maybeAutoReset(ctx, balance, cfg.ResetCooldown)
		}
		return "ok", nil
	}

	return m.maybeSendConfirmEmail(ctx, balance, cfg, "low_balance")
}

func (m *Monitor) handleRecoveredBalance(ctx context.Context, balance float64, cfg config.Config) (string, error) {
	invalidated, err := m.store.InvalidateActiveConfirmTokensForOtherReset(ctx)
	if err != nil {
		m.logger.Printf("balance recovery confirm token invalidation failed balance=%.6f threshold=%.6f error=%v", balance, cfg.LowBalanceThreshold, err)
		return "balance_recovered_confirm_link_invalidation_error", err
	}

	m.mu.Lock()
	m.pendingManualEmail = false
	m.forceFastUntilBalanceChange = false
	m.mu.Unlock()

	if len(invalidated) == 0 {
		return "ok", nil
	}

	if err := m.sendBalanceRecoveredInvalidatedConfirmTokensEmail(ctx, cfg, balance, invalidated); err != nil {
		m.logger.Printf("balance recovery confirm token invalidation notification failed balance=%.6f threshold=%.6f invalidated=%d error=%v",
			balance, cfg.LowBalanceThreshold, len(invalidated), err)
		return "balance_recovered_confirm_links_notification_error", err
	}
	m.logger.Printf("balance recovery invalidated active confirm tokens balance=%.6f threshold=%.6f invalidated=%d",
		balance, cfg.LowBalanceThreshold, len(invalidated))
	return "balance_recovered_confirm_links_invalidated", nil
}

func (m *Monitor) maybeSendConfirmEmail(ctx context.Context, balance float64, cfg config.Config, statusPrefix string) (string, error) {
	m.mu.Lock()
	pending := m.pendingManualEmail
	m.mu.Unlock()
	if pending {
		hasActive, err := m.store.HasActiveConfirmToken(ctx)
		if err != nil {
			m.logger.Printf("confirm email token check failed status_prefix=%s balance=%.6f error=%v", statusPrefix, balance, err)
			return "confirm_token_check_error", err
		}
		if hasActive {
			return statusPrefix + "_email_pending", nil
		}
		m.mu.Lock()
		m.pendingManualEmail = false
		m.mu.Unlock()
		m.logger.Printf("confirm email pending state cleared status_prefix=%s balance=%.6f reason=no_active_token", statusPrefix, balance)
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

	result, err := m.sendConfirmEmail(ctx, balance, cfg, statusPrefix)
	if err != nil {
		return result.Status, err
	}
	return result.Status, nil
}

func (m *Monitor) ResendConfirmEmail(ctx context.Context, key string) (ResendConfirmEmailResult, error) {
	cfg, refreshErr := m.refreshEmailConfig()
	if refreshErr != nil {
		m.logger.Printf("resend confirm email config refresh failed error=%v", refreshErr)
		cfg = m.snapshot()
	}
	if err := requireSharedKey(cfg.ResendResetEmailKey, key, ErrResendKeyMissing, ErrResendUnauthorized); err != nil {
		m.logger.Print("resend confirm email rejected reason=invalid_key")
		return ResendConfirmEmailResult{}, err
	}

	result, err := m.api.QueryBalance(ctx)
	if err != nil {
		m.logger.Printf("resend confirm email balance query failed error=%v", err)
		return ResendConfirmEmailResult{}, err
	}
	cfg = m.snapshot()
	m.observeBalance(m.businessNow(cfg), result.Balance)

	sendResult, err := m.sendConfirmEmail(ctx, result.Balance, cfg, "resend_confirm")
	if err != nil {
		return sendResult, err
	}
	m.logger.Printf("resend confirm email succeeded balance=%.6f expires_at=%s invalidated_old_links=%d",
		sendResult.Balance, sendResult.ExpiresAt.Format(time.RFC3339), sendResult.InvalidatedOldLinks)
	return sendResult, nil
}

func (m *Monitor) ManualReset(ctx context.Context, key string) (ManualResetResult, error) {
	cfg, refreshErr := m.refreshEmailConfig()
	if refreshErr != nil {
		m.logger.Printf("external manual reset config refresh failed error=%v", refreshErr)
		cfg = m.snapshot()
	}
	if !cfg.ExternalManualResetEnabled {
		m.logger.Print("external manual reset rejected reason=disabled")
		return ManualResetResult{}, ErrExternalManualResetDisabled
	}
	if err := requireSharedKey(cfg.ExternalManualResetKey, key, ErrExternalManualResetKeyMissing, ErrExternalManualResetUnauthorized); err != nil {
		m.logger.Print("external manual reset rejected reason=invalid_key")
		return ManualResetResult{}, err
	}

	result, err := m.api.QueryBalance(ctx)
	if err != nil {
		m.logger.Printf("external manual reset balance query failed error=%v", err)
		return ManualResetResult{}, err
	}
	cfg = m.snapshot()
	m.observeBalance(m.businessNow(cfg), result.Balance)

	if err := m.ensureResetAllowed(ctx, cfg, "external manual reset"); err != nil {
		return ManualResetResult{Balance: result.Balance, Status: "external_manual_reset_rejected"}, err
	}

	resetResult, resetLogID, err := m.executeResetIfIdle(ctx, "external_manual", result.Balance)
	if err != nil {
		return ManualResetResult{Balance: result.Balance, Status: "external_manual_reset_error"}, err
	}
	if err := m.sendManualResetNotification(ctx, cfg, result.Balance, resetResult.SubscriptionID, resetLogID, false); err != nil {
		m.logger.Printf("external manual reset notification failed balance=%.6f subscription_id=%d reset_log_id=%d error=%v",
			result.Balance, resetResult.SubscriptionID, resetLogID, err)
		return ManualResetResult{
			Balance:        result.Balance,
			SubscriptionID: resetResult.SubscriptionID,
			ResetLogID:     resetLogID,
			Status:         "external_manual_reset_email_error",
		}, err
	}
	m.logger.Printf("external manual reset succeeded balance=%.6f subscription_id=%d reset_log_id=%d",
		result.Balance, resetResult.SubscriptionID, resetLogID)
	return ManualResetResult{
		Balance:        result.Balance,
		SubscriptionID: resetResult.SubscriptionID,
		ResetLogID:     resetLogID,
		EmailSent:      true,
		Status:         "external_manual_reset_success",
	}, nil
}

func (m *Monitor) TestResetEmail(ctx context.Context, key string) (TestResetEmailResult, error) {
	cfg, refreshErr := m.refreshEmailConfig()
	if refreshErr != nil {
		m.logger.Printf("test reset email config refresh failed error=%v", refreshErr)
		cfg = m.snapshot()
	}
	if err := requireSharedKey(cfg.TestResetEmailKey, key, ErrTestResetEmailKeyMissing, ErrTestResetEmailUnauthorized); err != nil {
		m.logger.Print("test reset email rejected reason=invalid_key")
		return TestResetEmailResult{}, err
	}

	result, err := m.api.QueryBalance(ctx)
	if err != nil {
		m.logger.Printf("test reset email balance query failed error=%v", err)
		return TestResetEmailResult{}, err
	}
	cfg = m.snapshot()
	m.observeBalance(m.businessNow(cfg), result.Balance)

	if _, err := m.store.LogReset(ctx, store.ResetLog{
		Mode:            "external_manual_test",
		Balance:         result.Balance,
		Success:         false,
		ResponseSummary: "test reset email dry run: reset skipped",
	}); err != nil {
		m.logger.Printf("test reset email dry-run log failed balance=%.6f error=%v", result.Balance, err)
		return TestResetEmailResult{Balance: result.Balance, ResetSkipped: true, Status: "test_reset_email_log_error"}, err
	}

	if err := m.sendManualResetNotification(ctx, cfg, result.Balance, 0, 0, true); err != nil {
		m.logger.Printf("test reset email send failed balance=%.6f error=%v", result.Balance, err)
		return TestResetEmailResult{Balance: result.Balance, ResetSkipped: true, Status: "test_reset_email_error"}, err
	}
	m.logger.Printf("test reset email succeeded balance=%.6f", result.Balance)
	return TestResetEmailResult{
		Balance:      result.Balance,
		EmailSent:    true,
		ResetSkipped: true,
		Status:       "test_reset_email_sent",
	}, nil
}

func (m *Monitor) CancelResetEmails(ctx context.Context, key string) (CancelResetEmailResult, error) {
	cfg, refreshErr := m.refreshEmailConfig()
	if refreshErr != nil {
		m.logger.Printf("cancel reset emails config refresh failed error=%v", refreshErr)
		cfg = m.snapshot()
	}
	if err := requireSharedKey(cfg.CancelResetEmailKey, key, ErrCancelResetEmailKeyMissing, ErrCancelResetEmailUnauthorized); err != nil {
		m.logger.Print("cancel reset emails rejected reason=invalid_key")
		return CancelResetEmailResult{}, err
	}
	cancelled, err := m.store.CancelUnverifiedConfirmEmails(ctx)
	if err != nil {
		m.logger.Printf("cancel reset emails failed error=%v", err)
		return CancelResetEmailResult{}, err
	}
	m.mu.Lock()
	m.pendingManualEmail = false
	m.mu.Unlock()
	m.logger.Printf("cancel reset emails succeeded cancelled=%d", cancelled)
	return CancelResetEmailResult{Cancelled: cancelled, Status: "reset_emails_cancelled"}, nil
}

func (m *Monitor) sendStartupInvalidatedConfirmTokensEmail(ctx context.Context, tokens []store.InvalidatedConfirmToken) error {
	cfg := m.snapshot()
	location := cfg.BusinessLocation()
	subject := "重置链接已因服务重启失效"
	var body strings.Builder
	body.WriteString("服务启动自检发现存在已发送但尚未点击确认的重置链接。\n\n")
	body.WriteString("为防止旧链接卡住后续正常流程，以下重置链接已经标记为服务重启自检失效。请忽略这些旧邮件，等待服务重新发送新的重置确认邮件，或按需手动处理。\n")
	for i, token := range tokens {
		body.WriteString(fmt.Sprintf("\n%d. 旧链接：/confirm-reset?token=<token_hash_prefix:%s>\n", i+1, tokenHashPrefix(token.TokenHash)))
		body.WriteString(fmt.Sprintf("   余额：%.6f\n", token.Balance))
		body.WriteString(fmt.Sprintf("   发送时间：%s\n", formatEmailTime(token.EmailSentAt, location)))
		body.WriteString(fmt.Sprintf("   原过期时间：%s\n", formatEmailTime(token.ExpiresAt, location)))
		body.WriteString(fmt.Sprintf("   失效时间：%s\n", formatEmailTime(token.InvalidatedAt, location)))
	}
	return m.mailer.Send(ctx, subject, body.String())
}

func (m *Monitor) sendBalanceRecoveredInvalidatedConfirmTokensEmail(ctx context.Context, cfg config.Config, balance float64, tokens []store.InvalidatedConfirmToken) error {
	freshCfg, refreshErr := m.refreshEmailConfig()
	if refreshErr != nil {
		m.logger.Printf("balance recovery notification config refresh failed error=%v", refreshErr)
	} else {
		cfg = freshCfg
	}

	location := cfg.BusinessLocation()
	subject := "余额已恢复，之前的重置链接已自动失效"
	var body strings.Builder
	body.WriteString(fmt.Sprintf("当前余额已经恢复到低余额阈值及以上，因此系统判定订阅额度已经通过其他方式恢复。\n\n当前余额：%.6f\n低余额阈值：%.6f\n失效链接数量：%d\n\n", balance, cfg.LowBalanceThreshold, len(tokens)))
	body.WriteString("以下未点击的重置确认链接已经自动失效。请忽略之前的重置邮件，避免同一次余额不足触发多次重置。\n")
	for i, token := range tokens {
		body.WriteString(fmt.Sprintf("\n%d. 旧链接：/confirm-reset?token=<token_hash_prefix:%s>\n", i+1, tokenHashPrefix(token.TokenHash)))
		body.WriteString(fmt.Sprintf("   触发时余额：%.6f\n", token.Balance))
		body.WriteString(fmt.Sprintf("   发送时间：%s\n", formatEmailTime(token.EmailSentAt, location)))
		body.WriteString(fmt.Sprintf("   原过期时间：%s\n", formatEmailTime(token.ExpiresAt, location)))
		body.WriteString(fmt.Sprintf("   失效时间：%s\n", formatEmailTime(token.InvalidatedAt, location)))
	}
	return m.mailer.Send(ctx, subject, body.String())
}

func (m *Monitor) sendConfirmEmail(ctx context.Context, balance float64, cfg config.Config, statusPrefix string) (ResendConfirmEmailResult, error) {
	freshCfg, refreshErr := m.refreshEmailConfig()
	if refreshErr != nil {
		m.logger.Printf("confirm email config refresh failed status_prefix=%s error=%v", statusPrefix, refreshErr)
	} else {
		cfg = freshCfg
	}

	rawToken, tokenHash, err := newConfirmToken()
	if err != nil {
		m.logger.Printf("confirm email token creation failed status_prefix=%s balance=%.6f error=%v", statusPrefix, balance, err)
		m.notifyConfirmURLFailure(ctx, cfg, balance, statusPrefix, "confirm token creation failed", err)
		return ResendConfirmEmailResult{Balance: balance, Status: "confirm_token_error"}, err
	}
	now := m.businessNow(cfg)
	expiresAt, expireReason := confirmTokenExpiresAt(now, cfg.ConfirmTokenTTL)
	if err := m.store.CreateConfirmToken(ctx, tokenHash, balance, expiresAt, expireReason); err != nil {
		m.logger.Printf("confirm email token store failed status_prefix=%s balance=%.6f token_hash_prefix=%s error=%v", statusPrefix, balance, tokenHashPrefix(tokenHash), err)
		m.notifyConfirmURLFailure(ctx, cfg, balance, statusPrefix, "confirm token store failed", err)
		return ResendConfirmEmailResult{Balance: balance, ExpiresAt: expiresAt, Status: "confirm_token_store_error"}, err
	}

	link := confirmURL(cfg.PublicBaseURL, rawToken)
	subject := "余额不足，请确认重置订阅"
	plainBody, htmlBody := confirmEmailBody(balance, cfg.LowBalanceThreshold, link, expiresAt)
	m.logger.Printf("confirm email sending status_prefix=%s balance=%.6f threshold=%.6f expires_at=%s recipients=%d confirm_url=%s token_hash_prefix=%s",
		statusPrefix, balance, cfg.LowBalanceThreshold, expiresAt.Format(time.RFC3339), len(cfg.SMTP.To), redactConfirmURL(link), tokenHashPrefix(tokenHash))
	if err := m.sendEmail(ctx, subject, plainBody, htmlBody); err != nil {
		_ = m.store.DeleteConfirmToken(ctx, tokenHash)
		m.syncPendingManualEmail(ctx, statusPrefix, balance)
		m.logger.Printf("confirm email send failed status_prefix=%s balance=%.6f token_hash_prefix=%s error=%v", statusPrefix, balance, tokenHashPrefix(tokenHash), err)
		return ResendConfirmEmailResult{Balance: balance, ExpiresAt: expiresAt, Status: statusPrefix + "_email_error"}, err
	}
	if err := m.store.MarkConfirmTokenEmailSent(ctx, tokenHash); err != nil {
		_ = m.store.DeleteConfirmToken(ctx, tokenHash)
		m.syncPendingManualEmail(ctx, statusPrefix, balance)
		m.logger.Printf("confirm email sent token state failed status_prefix=%s balance=%.6f token_hash_prefix=%s error=%v",
			statusPrefix, balance, tokenHashPrefix(tokenHash), err)
		m.notifyConfirmURLFailure(ctx, cfg, balance, statusPrefix, "confirm token email state update failed", err)
		return ResendConfirmEmailResult{Balance: balance, ExpiresAt: expiresAt, Status: "confirm_token_email_state_error"}, err
	}
	invalidated, err := m.store.DeleteOtherActiveConfirmTokens(ctx, tokenHash)
	if err != nil {
		_ = m.store.DeleteConfirmToken(ctx, tokenHash)
		m.syncPendingManualEmail(ctx, statusPrefix, balance)
		m.logger.Printf("confirm email old token invalidation failed status_prefix=%s balance=%.6f token_hash_prefix=%s error=%v",
			statusPrefix, balance, tokenHashPrefix(tokenHash), err)
		m.notifyConfirmURLFailure(ctx, cfg, balance, statusPrefix, "old confirm token invalidation failed", err)
		return ResendConfirmEmailResult{Balance: balance, ExpiresAt: expiresAt, Status: "confirm_token_invalidation_error"}, err
	}

	m.mu.Lock()
	m.pendingManualEmail = true
	if cfg.Polling.AfterResetEmail.Enabled {
		m.forceFastUntilBalanceChange = true
		m.forceFastReferenceBalance = balance
	}
	m.mu.Unlock()
	policy := m.nextPollPolicy(now)
	m.logger.Printf("confirm email sent status_prefix=%s balance=%.6f expires_at=%s token_hash_prefix=%s invalidated_old_links=%d after_reset_email_polling=%t",
		statusPrefix, balance, expiresAt.Format(time.RFC3339), tokenHashPrefix(tokenHash), invalidated, cfg.Polling.AfterResetEmail.Enabled)
	return ResendConfirmEmailResult{
		Balance:             balance,
		ExpiresAt:           expiresAt,
		InvalidatedOldLinks: invalidated,
		Status:              statusPrefix + "_email_sent",
		NextPollInterval:    policy.Interval.String(),
	}, nil
}

func (m *Monitor) notifyConfirmURLFailure(ctx context.Context, cfg config.Config, balance float64, statusPrefix string, reason string, cause error) {
	subject := "重置确认链接生成失败，需要手动处理"
	body := fmt.Sprintf("当前余额已经触发重置确认流程，但服务未能生成可用的自动重置确认链接，需要手动处理。\n\n当前余额：%.6f\n低余额阈值：%.6f\n触发场景：%s\n失败原因：%s\n错误详情：%s\n\n请检查服务日志、数据库 confirm_tokens 表和相关配置，必要时手动重置订阅额度。\n",
		balance, cfg.LowBalanceThreshold, statusPrefix, reason, summarizeError(cause))
	if err := m.mailer.Send(ctx, subject, body); err != nil {
		m.logger.Printf("confirm URL failure notification failed status_prefix=%s balance=%.6f reason=%s original_error=%v notification_error=%v",
			statusPrefix, balance, reason, cause, err)
		return
	}
	m.logger.Printf("confirm URL failure notification sent status_prefix=%s balance=%.6f reason=%s", statusPrefix, balance, reason)
}

func (m *Monitor) maybeAutoReset(ctx context.Context, balance float64, cooldown time.Duration) (string, error) {
	now := time.Now()
	if cooldown > 0 {
		m.mu.Lock()
		lastAutoReset := m.lastAutoReset
		m.mu.Unlock()
		if !lastAutoReset.IsZero() && now.Sub(lastAutoReset) < cooldown {
			remaining := cooldown - now.Sub(lastAutoReset)
			m.logger.Printf("auto reset skipped balance=%.6f reason=cooldown remaining=%s", balance, remaining.Round(time.Second))
			return "auto_reset_cooldown", nil
		}
	}

	_, _, err := m.executeResetIfIdle(ctx, "auto", balance)
	if err != nil {
		if errors.Is(err, ErrResetInProgress) {
			m.logger.Printf("auto reset skipped balance=%.6f reason=in_progress", balance)
			return "auto_reset_in_progress", nil
		}
		return "auto_reset_error", err
	}
	return "auto_reset_success", nil
}

func (m *Monitor) executeResetIfIdle(ctx context.Context, mode string, balance float64) (api.ResetResult, int64, error) {
	if !m.beginReset(mode) {
		return api.ResetResult{}, 0, ErrResetInProgress
	}
	defer m.finishReset()

	return m.executeReset(ctx, mode, balance)
}

func (m *Monitor) beginReset(mode string) bool {
	m.mu.Lock()
	if m.resetInFlight {
		m.mu.Unlock()
		return false
	}
	m.resetInFlight = true
	if mode == "auto" {
		m.lastAutoReset = time.Now()
	}
	m.mu.Unlock()
	return true
}

func (m *Monitor) finishReset() {
	m.mu.Lock()
	m.resetInFlight = false
	m.mu.Unlock()
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

	cfg := m.snapshot()
	state, err := m.dailyResetLimitState(ctx, cfg, m.businessNow(cfg))
	if err != nil {
		m.logger.Printf("confirm reset URL rejected token_hash_prefix=%s reason=daily_limit_state_error error=%v", tokenPrefix, err)
		return ConfirmResult{}, err
	}
	if state.MaxResetCount > 0 && state.ResetCount >= state.MaxResetCount {
		if _, err := m.ensureDailyLimitEmail(ctx, cfg, state); err != nil {
			m.logger.Printf("confirm reset URL rejected token_hash_prefix=%s reason=daily_limit_email_error error=%v", tokenPrefix, err)
			return ConfirmResult{}, err
		}
		m.logger.Printf("confirm reset URL rejected token_hash_prefix=%s reason=daily_limit_reached reset_count=%d max=%d",
			tokenPrefix, state.ResetCount, state.MaxResetCount)
		return ConfirmResult{}, ErrDailyResetLimitReached
	}

	if !m.beginReset("manual") {
		m.logger.Printf("confirm reset URL rejected token_hash_prefix=%s reason=in_progress", tokenPrefix)
		return ConfirmResult{}, ErrResetInProgress
	}
	defer m.finishReset()

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
	m.notifyDailyLimitAfterReset(ctx)
	return result, resetLogID, nil
}

func (m *Monitor) ensureResetAllowed(ctx context.Context, cfg config.Config, event string) error {
	state, err := m.dailyResetLimitState(ctx, cfg, m.businessNow(cfg))
	if err != nil {
		m.logger.Printf("%s rejected reason=daily_limit_state_error error=%v", event, err)
		return err
	}
	if state.MaxResetCount > 0 && state.ResetCount >= state.MaxResetCount {
		if _, err := m.ensureDailyLimitEmail(ctx, cfg, state); err != nil {
			m.logger.Printf("%s rejected reason=daily_limit_email_error error=%v", event, err)
			return err
		}
		m.logger.Printf("%s rejected reason=daily_limit_reached reset_count=%d max=%d", event, state.ResetCount, state.MaxResetCount)
		return ErrDailyResetLimitReached
	}
	return nil
}

func (m *Monitor) sendManualResetNotification(ctx context.Context, cfg config.Config, balance float64, subscriptionID int64, resetLogID int64, dryRun bool) error {
	freshCfg, refreshErr := m.refreshEmailConfig()
	if refreshErr != nil {
		m.logger.Printf("manual reset notification config refresh failed dry_run=%t error=%v", dryRun, refreshErr)
	} else {
		cfg = freshCfg
	}

	if dryRun {
		subject := "测试：手动重置订阅额度邮件"
		body := fmt.Sprintf("这是一封测试邮件，系统已经完成密钥校验、余额查询、日志记录和邮件发送链路校验，但不会真的重置订阅额度。\n\n当前余额：%.6f\n触发方式：测试接口\n", balance)
		return m.mailer.Send(ctx, subject, body)
	}

	subject := "订阅额度已通过手动方式重置"
	body := fmt.Sprintf("当前余额：%.6f\n\n用户通过手动方式重置了订阅额度。\n订阅 ID：%d\n重置日志 ID：%d\n", balance, subscriptionID, resetLogID)
	return m.mailer.Send(ctx, subject, body)
}

func (m *Monitor) sendEmail(ctx context.Context, subject string, plainBody string, htmlBody string) error {
	if htmlBody != "" {
		if sender, ok := m.mailer.(mailer.HTMLSender); ok {
			return sender.SendHTML(ctx, subject, plainBody, htmlBody)
		}
	}
	return m.mailer.Send(ctx, subject, plainBody)
}

func (m *Monitor) notifyDailyLimitAfterReset(ctx context.Context) {
	cfg := m.snapshot()
	if cfg.DailyMaxResetCount <= 0 {
		return
	}
	state, err := m.dailyResetLimitState(ctx, cfg, m.businessNow(cfg))
	if err != nil {
		m.logger.Printf("daily reset limit state check failed after reset error=%v", err)
		return
	}
	if _, err := m.ensureDailyLimitEmail(ctx, cfg, state); err != nil {
		m.logger.Printf("daily reset limit email failed after reset reset_count=%d max=%d error=%v",
			state.ResetCount, state.MaxResetCount, err)
	}
}

func (m *Monitor) refreshEmailConfig() (config.Config, error) {
	current := m.snapshot()
	envPath := current.EnvPath
	tomlPath := current.TOMLPath
	if strings.TrimSpace(envPath) == "" || strings.TrimSpace(tomlPath) == "" {
		return current, nil
	}

	fresh, err := config.Load(envPath, tomlPath)
	if err != nil {
		return current, err
	}
	if err := fresh.Validate(); err != nil {
		return current, err
	}

	m.mu.Lock()
	m.cfg = fresh
	m.mu.Unlock()
	if updater, ok := m.mailer.(interface{ SetConfig(config.SMTPConfig) }); ok {
		updater.SetConfig(fresh.SMTP)
	}
	return fresh, nil
}

func (m *Monitor) syncPendingManualEmail(ctx context.Context, statusPrefix string, balance float64) {
	hasActive, err := m.store.HasActiveConfirmToken(ctx)
	if err != nil {
		m.logger.Printf("confirm email pending sync failed status_prefix=%s balance=%.6f error=%v", statusPrefix, balance, err)
		return
	}
	m.mu.Lock()
	m.pendingManualEmail = hasActive
	m.mu.Unlock()
}

func (m *Monitor) dailyResetLimitState(ctx context.Context, cfg config.Config, now time.Time) (store.DailyResetLimitState, error) {
	if cfg.DailyMaxResetCount <= 0 {
		return store.DailyResetLimitState{}, nil
	}
	return m.store.GetDailyResetLimitState(ctx, now.In(cfg.BusinessLocation()), cfg.DailyMaxResetCount)
}

func (m *Monitor) ensureDailyLimitEmail(ctx context.Context, cfg config.Config, state store.DailyResetLimitState) (string, error) {
	if state.MaxResetCount <= 0 || state.ResetCount < state.MaxResetCount || state.DailyLimitEmailSent {
		return "", nil
	}
	subject := "今日重置次数已达到上限"
	body := fmt.Sprintf("今日已成功重置 %d 次，已达到配置的每日最大可重置次数 %d 次。\n\n服务今日不会再执行新的重置；每日 0 点后会重新计算今日重置次数。",
		state.ResetCount, state.MaxResetCount)
	m.logger.Printf("daily reset limit email sending day=%s reset_count=%d max=%d recipients=%d",
		state.Day.Format("2006-01-02"), state.ResetCount, state.MaxResetCount, len(cfg.SMTP.To))
	if err := m.mailer.Send(ctx, subject, body); err != nil {
		m.logger.Printf("daily reset limit email send failed day=%s reset_count=%d max=%d error=%v",
			state.Day.Format("2006-01-02"), state.ResetCount, state.MaxResetCount, err)
		return "daily_reset_limit_email_error", err
	}
	if err := m.store.MarkDailyLimitEmailSent(ctx, state.Day); err != nil {
		return "daily_reset_limit_email_state_error", err
	}
	m.logger.Printf("daily reset limit email sent day=%s reset_count=%d max=%d",
		state.Day.Format("2006-01-02"), state.ResetCount, state.MaxResetCount)
	return "daily_reset_limit_email_sent", nil
}

func (m *Monitor) maybeSendPlanRefreshLimitEmail(ctx context.Context, balance float64, cfg config.Config, state store.DailyResetLimitState) (string, error) {
	if state.PlanRefreshLimitEmailSent {
		return "balance_query_paused", nil
	}
	subject := "已达到当前套餐可刷新上限"
	body := fmt.Sprintf("当前余额已消费到 %.6f，且今日已达到每日最大可重置次数 %d 次。\n\n这通常表示当前套餐今日可刷新上限已达到。服务将停止查询余额，直到每日 0 点重新开启查询并刷新每日重置次数限制。",
		balance, state.MaxResetCount)
	m.logger.Printf("plan refresh limit email sending day=%s balance=%.6f reset_count=%d max=%d recipients=%d",
		state.Day.Format("2006-01-02"), balance, state.ResetCount, state.MaxResetCount, len(cfg.SMTP.To))
	if err := m.mailer.Send(ctx, subject, body); err != nil {
		m.logger.Printf("plan refresh limit email send failed day=%s balance=%.6f reset_count=%d max=%d error=%v",
			state.Day.Format("2006-01-02"), balance, state.ResetCount, state.MaxResetCount, err)
		return "plan_refresh_limit_email_error", err
	}
	if err := m.store.MarkPlanRefreshLimitEmailSent(ctx, state.Day); err != nil {
		return "plan_refresh_limit_email_state_error", err
	}
	m.pauseBalanceQueriesUntil(state.Day.AddDate(0, 0, 1))
	m.logger.Printf("plan refresh limit email sent day=%s balance=%.6f reset_count=%d max=%d balance_query_paused=true",
		state.Day.Format("2006-01-02"), balance, state.ResetCount, state.MaxResetCount)
	return "plan_refresh_limit_email_sent", nil
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

func (m *Monitor) businessNow(cfg config.Config) time.Time {
	return time.Now().In(cfg.BusinessLocation())
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
	pausedUntil := m.balanceQueryPausedUntil
	state := pollingState{
		HasLastBalance:              m.hasLastBalance,
		LastBalance:                 m.lastBalance,
		LastBalanceChangedAt:        m.lastBalanceChangedAt,
		ForceFastUntilBalanceChange: m.forceFastUntilBalanceChange,
	}
	m.mu.Unlock()
	if pausedUntil.After(now) {
		interval := time.Until(pausedUntil)
		return pollingPolicy{
			Name:     "balance_query_pause",
			Key:      "balance_query_pause:" + pausedUntil.Format(time.RFC3339),
			Interval: interval,
			Reason:   "balance query paused until daily reset",
		}
	}
	return selectPollPolicy(cfg.Polling, state, now)
}

func (m *Monitor) pauseBalanceQueriesUntil(until time.Time) {
	m.mu.Lock()
	if until.After(m.balanceQueryPausedUntil) {
		m.balanceQueryPausedUntil = until
	}
	m.mu.Unlock()
}

func (m *Monitor) clearBalanceQueryPause() {
	m.mu.Lock()
	m.balanceQueryPausedUntil = time.Time{}
	m.mu.Unlock()
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

func confirmTokenExpiresAt(now time.Time, ttl time.Duration) (time.Time, store.ConfirmTokenExpireReason) {
	expiresAt := now.Add(ttl)
	midnight := nextLocalMidnight(now)
	if midnight.Before(expiresAt) {
		return midnight, store.ConfirmTokenExpireReasonAutoReset
	}
	return expiresAt, store.ConfirmTokenExpireReasonTTL
}

func nextLocalMidnight(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, now.Location())
}

func confirmEmailBody(balance float64, threshold float64, link string, expiresAt time.Time) (string, string) {
	expiresText := expiresAt.Format("2006-01-02 15:04:05 MST")
	plainBody := fmt.Sprintf("当前余额：%.6f\n低余额阈值：%.6f\n\n请点击“重置订阅”按钮确认重置订阅额度。\n\n如果按钮不能点击或无法自动跳转，也可以复制以下网址到浏览器访问：\n%s\n\n由于 0 点系统会自动重置订阅，本链接将在 %s 失效。如果不是你本人操作，请忽略本邮件。",
		balance, threshold, link, expiresText)
	htmlBody := fmt.Sprintf(`<!doctype html>
<html>
<body style="margin:0;padding:24px;background:#f6f8fb;color:#1f2937;font-family:-apple-system,BlinkMacSystemFont,'Segoe UI',Arial,sans-serif;font-size:16px;line-height:1.6;">
  <div style="max-width:560px;margin:0 auto;background:#ffffff;border:1px solid #e5e7eb;border-radius:8px;padding:24px;">
    <h2 style="margin:0 0 16px;font-size:20px;line-height:1.35;color:#111827;">余额不足，请确认重置订阅</h2>
    <p style="margin:0 0 8px;">当前余额：<strong>%.6f</strong></p>
    <p style="margin:0 0 20px;">低余额阈值：%.6f</p>
    <p style="margin:0 0 20px;">请点击下面的按钮确认重置订阅额度。</p>
    <p style="margin:0 0 24px;">
      <a href="%s" style="display:inline-block;background:#2563eb;color:#ffffff;text-decoration:none;font-weight:700;border-radius:6px;padding:12px 20px;">重置订阅</a>
    </p>
    <p style="margin:0 0 8px;color:#4b5563;">如果按钮不能点击或无法自动跳转，也可以复制以下网址到浏览器访问：</p>
    <p style="margin:0 0 20px;word-break:break-all;"><a href="%s" style="color:#2563eb;">%s</a></p>
    <p style="margin:0;color:#4b5563;">由于 0 点系统会自动重置订阅，本链接将在 %s 失效。如果不是你本人操作，请忽略本邮件。</p>
  </div>
</body>
</html>`, balance, threshold, html.EscapeString(link), html.EscapeString(link), html.EscapeString(link), html.EscapeString(expiresText))
	return plainBody, htmlBody
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

func formatEmailTime(t time.Time, location *time.Location) string {
	if t.IsZero() {
		return ""
	}
	return t.In(location).Format("2006-01-02 15:04:05 MST")
}

func summarizeError(err error) string {
	if err == nil {
		return ""
	}
	text := strings.TrimSpace(err.Error())
	if len(text) <= 500 {
		return text
	}
	return text[:500] + "..."
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

func validSharedKey(configured string, provided string) bool {
	configured = strings.TrimSpace(configured)
	provided = strings.TrimSpace(provided)
	if configured == "" || len(provided) != len(configured) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(provided), []byte(configured)) == 1
}

func requireSharedKey(configured string, provided string, missingErr error, unauthorizedErr error) error {
	if validSharedKey(configured, provided) {
		return nil
	}
	if strings.TrimSpace(configured) == "" {
		return missingErr
	}
	return unauthorizedErr
}
