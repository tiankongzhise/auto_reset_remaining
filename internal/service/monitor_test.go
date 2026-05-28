package service

import (
	"context"
	"errors"
	"fmt"
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

func TestRecoveredBalanceInvalidatesPendingConfirmURLAndNotifies(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("low balance Tick() error = %v", err)
	}
	if status != "low_balance_email_sent" {
		t.Fatalf("low balance Tick() status = %s, want low_balance_email_sent", status)
	}
	rawToken := extractToken(t, sender.messages[0])
	tokenHash := hashToken(rawToken)

	apiClient.balance = 0.75
	status, err = monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("recovered Tick() error = %v", err)
	}
	if status != "balance_recovered_confirm_links_invalidated" {
		t.Fatalf("recovered Tick() status = %s, want balance_recovered_confirm_links_invalidated", status)
	}
	if apiClient.resetCalls != 0 {
		t.Fatalf("resetCalls = %d, want 0", apiClient.resetCalls)
	}
	if got := len(sender.messages); got != 2 {
		t.Fatalf("sent messages = %d, want confirm email plus recovery notification", got)
	}
	if !strings.Contains(sender.messages[1], "余额已恢复") || !strings.Contains(sender.messages[1], tokenHashPrefix(tokenHash)) {
		t.Fatalf("recovery notification missing expected copy or token prefix:\n%s", sender.messages[1])
	}
	token := dataStore.tokens[tokenHash]
	if !token.otherResetExpired || token.expireReason != store.ConfirmTokenExpireReasonOtherReset {
		t.Fatalf("token was not marked other reset expired: %+v", token)
	}
	if _, err := monitor.Confirm(ctx, rawToken); !errors.Is(err, store.ErrTokenInvalid) {
		t.Fatalf("Confirm(old token) error = %v, want ErrTokenInvalid", err)
	}

	status, err = monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("second recovered Tick() error = %v", err)
	}
	if status != "ok" {
		t.Fatalf("second recovered Tick() status = %s, want ok", status)
	}
	if got := len(sender.messages); got != 2 {
		t.Fatalf("sent messages after second recovered Tick = %d, want 2", got)
	}
}

func TestRecoveredBalanceWithoutActiveConfirmTokenIsOKAndDoesNotNotify(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.75}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if status != "ok" {
		t.Fatalf("Tick() status = %s, want ok", status)
	}
	if got := len(sender.messages); got != 0 {
		t.Fatalf("sent messages = %d, want 0", got)
	}
}

func TestConfirmEmailUsesButtonCopyFallbackAndMidnightExpiry(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if status != "low_balance_email_sent" {
		t.Fatalf("Tick() status = %s", status)
	}
	if got := len(sender.messages); got != 1 {
		t.Fatalf("plain messages = %d, want 1", got)
	}
	if got := len(sender.html); got != 1 {
		t.Fatalf("html messages = %d, want 1", got)
	}
	if !strings.Contains(sender.html[0], ">重置订阅</a>") {
		t.Fatalf("html message missing reset button:\n%s", sender.html[0])
	}
	if !strings.Contains(sender.messages[0], "如果按钮不能点击或无法自动跳转，也可以复制以下网址到浏览器访问") {
		t.Fatalf("plain message missing copy fallback:\n%s", sender.messages[0])
	}
	token := extractToken(t, sender.messages[0])
	stored := dataStore.tokens[hashToken(token)]
	midnight := nextLocalMidnight(time.Now())
	if stored.expiresAt.After(midnight) {
		t.Fatalf("token expiresAt = %s, want no later than midnight %s", stored.expiresAt, midnight)
	}
}

func TestConfirmEmailExpiryUsesShorterTTLBeforeMidnight(t *testing.T) {
	now := time.Date(2026, 5, 25, 10, 0, 0, 0, time.Local)
	got, reason := confirmTokenExpiresAt(now, time.Hour)
	want := now.Add(time.Hour)
	if !got.Equal(want) {
		t.Fatalf("confirmTokenExpiresAt() = %s, want %s", got, want)
	}
	if reason != store.ConfirmTokenExpireReasonTTL {
		t.Fatalf("expire reason = %s, want ttl", reason)
	}
}

func TestConfirmEmailExpiryCapsAtMidnight(t *testing.T) {
	now := time.Date(2026, 5, 25, 23, 0, 0, 0, time.Local)
	got, reason := confirmTokenExpiresAt(now, 24*time.Hour)
	want := time.Date(2026, 5, 26, 0, 0, 0, 0, time.Local)
	if !got.Equal(want) {
		t.Fatalf("confirmTokenExpiresAt() = %s, want %s", got, want)
	}
	if reason != store.ConfirmTokenExpireReasonAutoReset {
		t.Fatalf("expire reason = %s, want auto_reset", reason)
	}
}

func TestConfirmEmailExpiryUsesConfiguredBusinessTimeZone(t *testing.T) {
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		nowUTC time.Time
		want   time.Time
	}{
		{
			name:   "before local midnight",
			nowUTC: time.Date(2026, 5, 26, 15, 43, 0, 0, time.UTC),
			want:   time.Date(2026, 5, 27, 0, 0, 0, 0, shanghai),
		},
		{
			name:   "after local midnight",
			nowUTC: time.Date(2026, 5, 26, 22, 43, 0, 0, time.UTC),
			want:   time.Date(2026, 5, 28, 0, 0, 0, 0, shanghai),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := confirmTokenExpiresAt(tc.nowUTC.In(shanghai), 24*time.Hour)
			if !got.Equal(tc.want) {
				t.Fatalf("confirmTokenExpiresAt() = %s, want %s", got, tc.want)
			}
			if reason != store.ConfirmTokenExpireReasonAutoReset {
				t.Fatalf("expire reason = %s, want auto_reset", reason)
			}
		})
	}
}

func TestConfirmThirdManualSuccessEnablesAutoReset(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4, resetResult: api.ResetResult{SubscriptionID: 1716, HTTPStatus: 200, Success: true}}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	rawToken := "manual-confirm-token"
	if err := dataStore.CreateConfirmToken(ctx, hashToken(rawToken), 0.4, time.Now().Add(time.Hour), store.ConfirmTokenExpireReasonTTL); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.MarkConfirmTokenEmailSent(ctx, hashToken(rawToken)); err != nil {
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

func TestManualResetRequiresEnabledAndKeyThenSendsNotification(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.37, resetResult: api.ResetResult{SubscriptionID: 1716, HTTPStatus: 200, Success: true}}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		ExternalManualResetEnabled: true,
		ExternalManualResetKey:     "manual-secret",
	})

	_, err := monitor.ManualReset(ctx, "wrong")
	if !errors.Is(err, ErrExternalManualResetUnauthorized) {
		t.Fatalf("ManualReset(wrong key) error = %v, want ErrExternalManualResetUnauthorized", err)
	}
	if apiClient.balanceCalls != 0 || apiClient.resetCalls != 0 || len(sender.messages) != 0 {
		t.Fatalf("wrong key touched chain: balance=%d reset=%d messages=%d", apiClient.balanceCalls, apiClient.resetCalls, len(sender.messages))
	}

	result, err := monitor.ManualReset(ctx, "manual-secret")
	if err != nil {
		t.Fatalf("ManualReset() error = %v", err)
	}
	if result.Status != "external_manual_reset_success" || !result.EmailSent {
		t.Fatalf("ManualReset() result = %+v", result)
	}
	if apiClient.balanceCalls != 1 || apiClient.resetCalls != 1 {
		t.Fatalf("calls balance=%d reset=%d, want 1/1", apiClient.balanceCalls, apiClient.resetCalls)
	}
	if got := len(dataStore.resetLogs); got != 1 || dataStore.resetLogs[0].Mode != "external_manual" || !dataStore.resetLogs[0].Success {
		t.Fatalf("reset logs = %+v", dataStore.resetLogs)
	}
	if got := len(sender.messages); got != 1 || !strings.Contains(sender.messages[0], "用户通过手动方式重置了订阅额度") || !strings.Contains(sender.messages[0], "0.370000") {
		t.Fatalf("manual reset notification = %q", sender.messages)
	}
}

func TestManualResetRejectsWhenDisabled(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.37}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		ExternalManualResetKey: "manual-secret",
	})

	_, err := monitor.ManualReset(ctx, "manual-secret")
	if !errors.Is(err, ErrExternalManualResetDisabled) {
		t.Fatalf("ManualReset() error = %v, want ErrExternalManualResetDisabled", err)
	}
	if apiClient.balanceCalls != 0 || apiClient.resetCalls != 0 || len(sender.messages) != 0 {
		t.Fatalf("disabled reset touched chain: balance=%d reset=%d messages=%d", apiClient.balanceCalls, apiClient.resetCalls, len(sender.messages))
	}
}

func TestTestResetEmailValidatesChainWithoutReset(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.25, resetResult: api.ResetResult{SubscriptionID: 1716, HTTPStatus: 200, Success: true}}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		TestResetEmailKey: "test-secret",
	})

	_, err := monitor.TestResetEmail(ctx, "wrong")
	if !errors.Is(err, ErrTestResetEmailUnauthorized) {
		t.Fatalf("TestResetEmail(wrong key) error = %v, want ErrTestResetEmailUnauthorized", err)
	}
	if apiClient.balanceCalls != 0 || len(sender.messages) != 0 {
		t.Fatalf("wrong key touched chain: balance=%d messages=%d", apiClient.balanceCalls, len(sender.messages))
	}

	result, err := monitor.TestResetEmail(ctx, "test-secret")
	if err != nil {
		t.Fatalf("TestResetEmail() error = %v", err)
	}
	if result.Status != "test_reset_email_sent" || !result.EmailSent || !result.ResetSkipped {
		t.Fatalf("TestResetEmail() result = %+v", result)
	}
	if apiClient.balanceCalls != 1 {
		t.Fatalf("balanceCalls = %d, want 1", apiClient.balanceCalls)
	}
	if apiClient.resetCalls != 0 {
		t.Fatalf("resetCalls = %d, want 0", apiClient.resetCalls)
	}
	if got := len(dataStore.resetLogs); got != 1 || dataStore.resetLogs[0].Mode != "external_manual_test" || dataStore.resetLogs[0].Success {
		t.Fatalf("dry-run reset logs = %+v", dataStore.resetLogs)
	}
	if got := len(sender.messages); got != 1 || !strings.Contains(sender.messages[0], "这是一封测试邮件") || !strings.Contains(sender.messages[0], "不会真的重置订阅额度") {
		t.Fatalf("test reset email = %q", sender.messages)
	}
}

func TestCancelResetEmailsMarksManualCancelledAndDoesNotBlockFlow(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		CancelResetEmailKey: "cancel-secret",
	})

	if status, err := monitor.Tick(ctx); err != nil || status != "low_balance_email_sent" {
		t.Fatalf("Tick() status=%s err=%v", status, err)
	}
	oldToken := extractToken(t, sender.messages[0])

	result, err := monitor.CancelResetEmails(ctx, "cancel-secret")
	if err != nil {
		t.Fatalf("CancelResetEmails() error = %v", err)
	}
	if result.Cancelled != 1 || result.Status != "reset_emails_cancelled" {
		t.Fatalf("CancelResetEmails() = %+v, want one cancelled", result)
	}
	if _, err := monitor.Confirm(ctx, oldToken); !errors.Is(err, store.ErrTokenInvalid) {
		t.Fatalf("Confirm(cancelled token) error = %v, want ErrTokenInvalid", err)
	}

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() after cancel error = %v", err)
	}
	if status != "low_balance_email_sent" {
		t.Fatalf("Tick() after cancel status = %s, want low_balance_email_sent", status)
	}
	if got := len(sender.messages); got != 2 {
		t.Fatalf("sent messages after cancel = %d, want 2", got)
	}
}

func TestOtherResetInvalidationOnlyTouchesActiveConfirmTokens(t *testing.T) {
	ctx := context.Background()
	dataStore := newFakeStore()
	now := time.Now()

	activeHash := hashToken("active")
	if err := dataStore.CreateConfirmToken(ctx, activeHash, 0.4, now.Add(time.Hour), store.ConfirmTokenExpireReasonTTL); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.MarkConfirmTokenEmailSent(ctx, activeHash); err != nil {
		t.Fatal(err)
	}

	usedHash := hashToken("used")
	if err := dataStore.CreateConfirmToken(ctx, usedHash, 0.4, now.Add(time.Hour), store.ConfirmTokenExpireReasonTTL); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.MarkConfirmTokenEmailSent(ctx, usedHash); err != nil {
		t.Fatal(err)
	}
	if _, err := dataStore.ConsumeConfirmToken(ctx, usedHash); err != nil {
		t.Fatal(err)
	}

	cancelledHash := hashToken("cancelled")
	if err := dataStore.CreateConfirmToken(ctx, cancelledHash, 0.4, now.Add(time.Hour), store.ConfirmTokenExpireReasonTTL); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.MarkConfirmTokenEmailSent(ctx, cancelledHash); err != nil {
		t.Fatal(err)
	}
	cancelled := dataStore.tokens[cancelledHash]
	cancelled.cancelled = true
	dataStore.tokens[cancelledHash] = cancelled

	expiredHash := hashToken("expired")
	if err := dataStore.CreateConfirmToken(ctx, expiredHash, 0.4, now.Add(-time.Minute), store.ConfirmTokenExpireReasonTTL); err != nil {
		t.Fatal(err)
	}
	expired := dataStore.tokens[expiredHash]
	expired.emailSent = true
	expired.emailSentAt = now.Add(-time.Hour)
	dataStore.tokens[expiredHash] = expired

	unsentHash := hashToken("unsent")
	if err := dataStore.CreateConfirmToken(ctx, unsentHash, 0.4, now.Add(time.Hour), store.ConfirmTokenExpireReasonTTL); err != nil {
		t.Fatal(err)
	}

	invalidated, err := dataStore.InvalidateActiveConfirmTokensForOtherReset(ctx)
	if err != nil {
		t.Fatalf("InvalidateActiveConfirmTokensForOtherReset() error = %v", err)
	}
	if len(invalidated) != 1 || invalidated[0].TokenHash != activeHash {
		t.Fatalf("invalidated = %+v, want only active token", invalidated)
	}
	if !dataStore.tokens[activeHash].otherResetExpired {
		t.Fatal("active token was not marked other reset expired")
	}
	for _, tokenHash := range []string{usedHash, cancelledHash, expiredHash, unsentHash} {
		if dataStore.tokens[tokenHash].otherResetExpired {
			t.Fatalf("non-active token %s was marked other reset expired: %+v", tokenHash, dataStore.tokens[tokenHash])
		}
	}
}

func TestAutoResetExpiredConfirmTokenDoesNotBlockFlow(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	expiredHash := hashToken("expired-by-auto-reset")
	dataStore.tokens[expiredHash] = fakeToken{
		id:           1,
		balance:      0.4,
		expiresAt:    time.Now().Add(-time.Minute),
		emailSent:    true,
		expireReason: store.ConfirmTokenExpireReasonAutoReset,
	}
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	if _, err := monitor.Confirm(ctx, "expired-by-auto-reset"); !errors.Is(err, store.ErrTokenInvalid) {
		t.Fatalf("Confirm(expired token) error = %v, want ErrTokenInvalid", err)
	}
	if !dataStore.tokens[expiredHash].expired {
		t.Fatal("expired token was not marked auto-reset expired")
	}
	if !dataStore.tokens[expiredHash].autoResetExpired {
		t.Fatal("expired token was not marked as auto-reset invalidated")
	}
	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if status != "low_balance_email_sent" {
		t.Fatalf("Tick() status = %s, want low_balance_email_sent", status)
	}
}

func TestAutoResetExpiredConfirmTokenNotificationSendsOnceAndDoesNotLeakRawToken(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.75}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	rawToken := "expired-by-midnight"
	tokenHash := hashToken(rawToken)
	dataStore.tokens[tokenHash] = fakeToken{
		id:           1,
		balance:      0.4,
		createdAt:    time.Now().Add(-2 * time.Hour),
		expiresAt:    time.Now().Add(-time.Minute),
		emailSent:    true,
		emailSentAt:  time.Now().Add(-time.Hour),
		expireReason: store.ConfirmTokenExpireReasonAutoReset,
	}
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})
	monitor.mu.Lock()
	monitor.pendingManualEmail = true
	monitor.mu.Unlock()

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if status != "ok" {
		t.Fatalf("Tick() status = %s, want ok", status)
	}
	if got := len(sender.messages); got != 1 {
		t.Fatalf("sent messages = %d, want 1 auto-reset notification", got)
	}
	if !strings.Contains(sender.messages[0], "0 点自动重置导致旧重置链接失效") || !strings.Contains(sender.messages[0], tokenHashPrefix(tokenHash)) {
		t.Fatalf("auto-reset notification missing subject or token prefix:\n%s", sender.messages[0])
	}
	if strings.Contains(sender.messages[0], rawToken) {
		t.Fatalf("auto-reset notification leaked raw token:\n%s", sender.messages[0])
	}
	if !dataStore.tokens[tokenHash].autoResetNotificationSent {
		t.Fatal("auto-reset notification was not marked sent")
	}
	monitor.mu.Lock()
	pending := monitor.pendingManualEmail
	monitor.mu.Unlock()
	if pending {
		t.Fatal("pendingManualEmail was not cleared")
	}

	status, err = monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("second Tick() error = %v", err)
	}
	if status != "ok" {
		t.Fatalf("second Tick() status = %s, want ok", status)
	}
	if got := len(sender.messages); got != 1 {
		t.Fatalf("sent messages after second Tick = %d, want no duplicate notification", got)
	}
}

func TestAutoResetExpiredConfirmTokenNotificationFailureRetries(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.75}
	notifyErr := errors.New("smtp unavailable")
	sender := &fakeMailer{err: notifyErr}
	dataStore := newFakeStore()
	tokenHash := hashToken("retry-auto-reset-notification")
	dataStore.tokens[tokenHash] = fakeToken{
		id:           1,
		balance:      0.4,
		createdAt:    time.Now().Add(-2 * time.Hour),
		expiresAt:    time.Now().Add(-time.Minute),
		emailSent:    true,
		emailSentAt:  time.Now().Add(-time.Hour),
		expireReason: store.ConfirmTokenExpireReasonAutoReset,
	}
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if status != "ok" {
		t.Fatalf("Tick() status = %s, want ok despite notification failure", status)
	}
	if dataStore.tokens[tokenHash].autoResetNotificationSent {
		t.Fatal("failed notification should not be marked sent")
	}

	sender.err = nil
	status, err = monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("retry Tick() error = %v", err)
	}
	if status != "ok" {
		t.Fatalf("retry Tick() status = %s, want ok", status)
	}
	if got := len(sender.messages); got != 1 {
		t.Fatalf("sent messages after retry = %d, want 1", got)
	}
	if !dataStore.tokens[tokenHash].autoResetNotificationSent {
		t.Fatal("retry notification was not marked sent")
	}
}

func TestTickLogsBalanceSource(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 97.60419, balanceSource: "subscription_fallback"}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if status != "ok" {
		t.Fatalf("Tick() status = %s, want ok", status)
	}
	entries, err := os.ReadDir(monitor.cfg.QueryLogDir)
	if err != nil {
		t.Fatalf("read query log dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("query log files = %d, want 1", len(entries))
	}
	body, err := os.ReadFile(filepath.Join(monitor.cfg.QueryLogDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read query log: %v", err)
	}
	if !strings.Contains(string(body), `"balance_source":"subscription_fallback"`) {
		t.Fatalf("query log missing balance source:\n%s", body)
	}
}

func TestStartupInvalidatesActiveConfirmTokensAndNotifies(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	oldRawToken := "startup-active-token"
	oldTokenHash := hashToken(oldRawToken)
	if err := dataStore.CreateConfirmToken(ctx, oldTokenHash, 0.4, time.Now().Add(time.Hour), store.ConfirmTokenExpireReasonTTL); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.MarkConfirmTokenEmailSent(ctx, oldTokenHash); err != nil {
		t.Fatal(err)
	}
	usedRawToken := "startup-used-token"
	usedTokenHash := hashToken(usedRawToken)
	if err := dataStore.CreateConfirmToken(ctx, usedTokenHash, 0.3, time.Now().Add(time.Hour), store.ConfirmTokenExpireReasonTTL); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.MarkConfirmTokenEmailSent(ctx, usedTokenHash); err != nil {
		t.Fatal(err)
	}
	usedToken := dataStore.tokens[usedTokenHash]
	usedToken.used = true
	dataStore.tokens[usedTokenHash] = usedToken
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	if err := monitor.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if !dataStore.tokens[oldTokenHash].startupSelfCheckExpired {
		t.Fatal("active token was not marked startup self-check expired")
	}
	if dataStore.tokens[usedTokenHash].startupSelfCheckExpired {
		t.Fatal("used token should not be startup self-check expired")
	}
	if got := len(sender.messages); got != 1 {
		t.Fatalf("startup notification messages = %d, want 1", got)
	}
	if !strings.Contains(sender.messages[0], "服务重启自检失效") || !strings.Contains(sender.messages[0], tokenHashPrefix(oldTokenHash)) {
		t.Fatalf("startup notification missing reason or token prefix:\n%s", sender.messages[0])
	}
	if strings.Contains(sender.messages[0], oldRawToken) {
		t.Fatalf("startup notification leaked raw token:\n%s", sender.messages[0])
	}

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if status != "low_balance_email_sent" {
		t.Fatalf("Tick() status = %s, want low_balance_email_sent", status)
	}
	if got := len(sender.messages); got != 2 {
		t.Fatalf("messages after Tick = %d, want startup notification plus new confirm email", got)
	}
}

func TestStartupInvalidationNotificationFailureDoesNotBlockInitialize(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4}
	notifyErr := errors.New("smtp unavailable")
	sender := &fakeMailer{err: notifyErr}
	dataStore := newFakeStore()
	tokenHash := hashToken("startup-notify-failure")
	if err := dataStore.CreateConfirmToken(ctx, tokenHash, 0.4, time.Now().Add(time.Hour), store.ConfirmTokenExpireReasonTTL); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.MarkConfirmTokenEmailSent(ctx, tokenHash); err != nil {
		t.Fatal(err)
	}
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	if err := monitor.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}
	if !dataStore.tokens[tokenHash].startupSelfCheckExpired {
		t.Fatal("active token was not marked startup self-check expired")
	}
}

func TestResendConfirmEmailRefreshesSMTPConfigFromTOML(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	envPath := filepath.Join(dir, ".env")
	configPath := filepath.Join(dir, "config.toml")
	writeServiceEnv(t, envPath)
	writeTestServiceConfig(t, configPath, 2525)

	cfg, err := config.Load(envPath, configPath)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := NewMonitor(cfg, envPath, apiClient, sender, dataStore, NewQueryLogger(filepath.Join(dir, "logs")), nil)

	writeTestServiceConfig(t, configPath, 465)
	result, err := monitor.ResendConfirmEmail(ctx, "resend-secret")
	if err != nil {
		t.Fatalf("ResendConfirmEmail() error = %v", err)
	}
	if result.Status != "resend_confirm_email_sent" {
		t.Fatalf("status = %s, want resend_confirm_email_sent", result.Status)
	}
	if len(sender.configs) == 0 {
		t.Fatal("mailer config was not refreshed")
	}
	if got := sender.configs[len(sender.configs)-1].Port; got != 465 {
		t.Fatalf("refreshed SMTP port = %d, want 465", got)
	}
}

func TestUnsentConfirmTokenDoesNotBlockNewEmail(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	legacyTokenHash := hashToken("legacy-unsent-token")
	if err := dataStore.CreateConfirmToken(ctx, legacyTokenHash, 0.4, time.Now().Add(time.Hour), store.ConfirmTokenExpireReasonTTL); err != nil {
		t.Fatal(err)
	}
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})
	if err := monitor.Initialize(ctx); err != nil {
		t.Fatalf("Initialize() error = %v", err)
	}

	status, err := monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("Tick() error = %v", err)
	}
	if status != "low_balance_email_sent" {
		t.Fatalf("Tick() status = %s, want low_balance_email_sent", status)
	}
	if got := len(sender.messages); got != 1 {
		t.Fatalf("sent messages = %d, want 1", got)
	}
	if _, err := monitor.Confirm(ctx, "legacy-unsent-token"); !errors.Is(err, store.ErrTokenInvalid) {
		t.Fatalf("Confirm(legacy unsent token) error = %v, want ErrTokenInvalid", err)
	}
}

func TestFailedConfirmEmailClearsPendingAndCanRetry(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4}
	sendErr := errors.New("smtp auth failed")
	sender := &fakeMailer{err: sendErr}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	status, err := monitor.Tick(ctx)
	if !errors.Is(err, sendErr) {
		t.Fatalf("first Tick() error = %v, want %v", err, sendErr)
	}
	if status != "low_balance_email_error" {
		t.Fatalf("first Tick() status = %s, want low_balance_email_error", status)
	}
	if got := len(dataStore.tokens); got != 0 {
		t.Fatalf("tokens after failed send = %d, want 0", got)
	}

	monitor.mu.Lock()
	monitor.pendingManualEmail = true
	monitor.mu.Unlock()
	sender.err = nil

	status, err = monitor.Tick(ctx)
	if err != nil {
		t.Fatalf("retry Tick() error = %v", err)
	}
	if status != "low_balance_email_sent" {
		t.Fatalf("retry Tick() status = %s, want low_balance_email_sent", status)
	}
	if got := len(sender.messages); got != 1 {
		t.Fatalf("sent messages = %d, want 1", got)
	}
}

func TestConfirmTokenStoreFailureSendsManualHandlingAlert(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0}
	storeErr := errors.New("duplicate key value violates unique constraint")
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	dataStore.createErr = storeErr
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	status, err := monitor.Tick(ctx)
	if !errors.Is(err, storeErr) {
		t.Fatalf("Tick() error = %v, want %v", err, storeErr)
	}
	if status != "confirm_token_store_error" {
		t.Fatalf("Tick() status = %s, want confirm_token_store_error", status)
	}
	if got := len(sender.messages); got != 1 {
		t.Fatalf("sent messages = %d, want one alert", got)
	}
	if !strings.Contains(sender.messages[0], "重置确认链接生成失败") || !strings.Contains(sender.messages[0], "0.000000") || !strings.Contains(sender.messages[0], "duplicate key") {
		t.Fatalf("manual handling alert missing balance or reason:\n%s", sender.messages[0])
	}
}

func TestConfirmURLFailureAlertErrorKeepsOriginalError(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0}
	storeErr := errors.New("duplicate token hash")
	notifyErr := errors.New("smtp unavailable")
	sender := &fakeMailer{err: notifyErr}
	dataStore := newFakeStore()
	dataStore.createErr = storeErr
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	status, err := monitor.Tick(ctx)
	if !errors.Is(err, storeErr) {
		t.Fatalf("Tick() error = %v, want original store error %v", err, storeErr)
	}
	if status != "confirm_token_store_error" {
		t.Fatalf("Tick() status = %s, want confirm_token_store_error", status)
	}
}

func TestConfirmEmailSMTPFailureDoesNotSendSecondAlert(t *testing.T) {
	ctx := context.Background()
	apiClient := &fakeAPI{balance: 0.4}
	sendErr := errors.New("smtp auth failed")
	sender := &fakeMailer{err: sendErr}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})

	status, err := monitor.Tick(ctx)
	if !errors.Is(err, sendErr) {
		t.Fatalf("Tick() error = %v, want %v", err, sendErr)
	}
	if status != "low_balance_email_error" {
		t.Fatalf("Tick() status = %s, want low_balance_email_error", status)
	}
	if sender.sendAttempts != 1 {
		t.Fatalf("send attempts = %d, want only original confirm email attempt", sender.sendAttempts)
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

func TestDailyLimitStateUsesBusinessTimeZone(t *testing.T) {
	ctx := context.Background()
	shanghai, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, &fakeAPI{}, &fakeMailer{}, dataStore, config.Config{
		DailyMaxResetCount: 1,
		TimeZone:           "Asia/Shanghai",
		Location:           shanghai,
	})

	nowUTC := time.Date(2026, 5, 26, 22, 43, 0, 0, time.UTC)
	state, err := monitor.dailyResetLimitState(ctx, monitor.snapshot(), nowUTC)
	if err != nil {
		t.Fatalf("dailyResetLimitState() error = %v", err)
	}
	if got := state.Day.Format(time.RFC3339); got != "2026-05-27T00:00:00+08:00" {
		t.Fatalf("state day = %s, want 2026-05-27T00:00:00+08:00", got)
	}
	if _, ok := dataStore.daily["2026-05-27"]; !ok {
		t.Fatalf("daily state keys = %+v, want 2026-05-27", dataStore.daily)
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
	if err := dataStore.CreateConfirmToken(ctx, tokenHash, 0.4, time.Now().Add(time.Hour), store.ConfirmTokenExpireReasonTTL); err != nil {
		t.Fatal(err)
	}
	if err := dataStore.MarkConfirmTokenEmailSent(ctx, tokenHash); err != nil {
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

func TestReplayNonceStoreSemantics(t *testing.T) {
	ctx := context.Background()
	dataStore := newFakeStore()

	first, err := dataStore.NextReplayNonce(ctx, "/resend-reset-email")
	if err != nil {
		t.Fatalf("first NextReplayNonce() error = %v", err)
	}
	second, err := dataStore.NextReplayNonce(ctx, "/resend-reset-email")
	if err != nil {
		t.Fatalf("second NextReplayNonce() error = %v", err)
	}
	if first != "1" || second != "2" {
		t.Fatalf("same scope nonces = %q, %q; want 1, 2", first, second)
	}

	other, err := dataStore.NextReplayNonce(ctx, "/rotate-logs")
	if err != nil {
		t.Fatalf("other scope NextReplayNonce() error = %v", err)
	}
	if other != "1" {
		t.Fatalf("different scope nonce = %q, want 1", other)
	}

	if err := dataStore.ConsumeReplayNonce(ctx, "/resend-reset-email", first); err != nil {
		t.Fatalf("ConsumeReplayNonce(first) error = %v", err)
	}
	if err := dataStore.ConsumeReplayNonce(ctx, "/resend-reset-email", first); !errors.Is(err, store.ErrReplayNonceConsumed) {
		t.Fatalf("ConsumeReplayNonce(replay) error = %v, want ErrReplayNonceConsumed", err)
	}

	next, err := dataStore.NextReplayNonce(ctx, "/resend-reset-email")
	if err != nil {
		t.Fatalf("next after consumed error = %v", err)
	}
	if next != "3" {
		t.Fatalf("next after consumed = %q, want 3", next)
	}
}

func newTestMonitor(t *testing.T, apiClient *fakeAPI, sender *fakeMailer, dataStore *fakeStore, overrides config.Config) *Monitor {
	t.Helper()
	configPath := writeConfig(t, overrides.ManualConfirmSuccessCount, overrides.AutoResetEnabled)
	location := overrides.Location
	if location == nil {
		location = time.FixedZone("Asia/Shanghai", 8*60*60)
	}
	timeZone := overrides.TimeZone
	if timeZone == "" {
		timeZone = "Asia/Shanghai"
	}
	cfg := config.Config{
		TOMLPath:                   configPath,
		TimeZone:                   timeZone,
		Location:                   location,
		PublicBaseURL:              "https://service.example.com",
		LowBalanceThreshold:        0.5,
		ConfirmTokenTTL:            time.Hour,
		PollInterval:               time.Second,
		QueryLogDir:                filepath.Join(t.TempDir(), "logs"),
		ResendResetEmailKey:        overrides.ResendResetEmailKey,
		ExternalManualResetEnabled: overrides.ExternalManualResetEnabled,
		ExternalManualResetKey:     overrides.ExternalManualResetKey,
		TestResetEmailKey:          overrides.TestResetEmailKey,
		CancelResetEmailKey:        overrides.CancelResetEmailKey,
		ManualConfirmSuccessCount:  0,
		AutoResetEnabled:           false,
		ResetCooldown:              time.Minute,
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
	body = strings.ReplaceAll(body, "{{SMTP_PORT}}", "587")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeServiceEnv(t *testing.T, path string) {
	t.Helper()
	body := strings.Join([]string{
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
		"EXTERNAL_MANUAL_RESET_ENABLED=true",
		"EXTERNAL_MANUAL_RESET_KEY=manual-secret",
		"TEST_RESET_EMAIL_KEY=test-secret",
		"CANCEL_RESET_EMAIL_KEY=cancel-secret",
	}, "\n")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeTestServiceConfig(t *testing.T, path string, smtpPort int) {
	t.Helper()
	body := strings.ReplaceAll(testServiceConfigTOML, "{{AUTO_RESET_ENABLED}}", "false")
	body = strings.ReplaceAll(body, "{{MANUAL_CONFIRM_SUCCESS_COUNT}}", "0")
	body = strings.ReplaceAll(body, "{{SMTP_PORT}}", strconv.Itoa(smtpPort))
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
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
[app]
timezone = "Asia/Shanghai"

[rayplus]
base_url = "https://rayplus.site"
user_agent = "auto-reset-remaining/1.0"
balance_json_path = ""

[codex]
base_url = "https://codex.example.com"
subscription_id = 1716

[smtp]
host = "smtp.example.com"
port = {{SMTP_PORT}}
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
	balance       float64
	balanceSource string
	balanceErr    error
	resetResult   api.ResetResult
	resetErr      error
	balanceCalls  int
	resetCalls    int
}

func (f *fakeAPI) QueryBalance(context.Context) (api.BalanceResult, error) {
	f.balanceCalls++
	if f.balanceErr != nil {
		return api.BalanceResult{}, f.balanceErr
	}
	source := f.balanceSource
	if source == "" {
		source = "usage"
	}
	return api.BalanceResult{Balance: f.balance, Source: source}, nil
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
	messages     []string
	html         []string
	err          error
	configs      []config.SMTPConfig
	sendAttempts int
}

func (f *fakeMailer) Send(_ context.Context, subject string, body string) error {
	f.sendAttempts++
	if f.err != nil {
		return f.err
	}
	f.messages = append(f.messages, subject+"\n"+body)
	return nil
}

func (f *fakeMailer) SendHTML(_ context.Context, subject string, plainBody string, htmlBody string) error {
	f.sendAttempts++
	if f.err != nil {
		return f.err
	}
	f.messages = append(f.messages, subject+"\n"+plainBody)
	f.html = append(f.html, htmlBody)
	return nil
}

func (f *fakeMailer) SetConfig(cfg config.SMTPConfig) {
	cfg.To = append([]string(nil), cfg.To...)
	f.configs = append(f.configs, cfg)
}

type fakeStore struct {
	tokens                    map[string]fakeToken
	resetLogs                 []store.ResetLog
	daily                     map[string]*fakeDailyState
	replayNonces              map[string]map[string]bool
	replayNonceCounters       map[string]int64
	nextID                    int64
	createErr                 error
	markEmailSentErr          error
	deleteOtherActiveTokenErr error
}

type fakeToken struct {
	id                            int64
	balance                       float64
	createdAt                     time.Time
	expiresAt                     time.Time
	emailSentAt                   time.Time
	invalidatedAt                 time.Time
	autoResetNotificationRequired bool
	autoResetNotificationSent     bool
	used                          bool
	emailSent                     bool
	cancelled                     bool
	expired                       bool
	startupSelfCheckExpired       bool
	autoResetExpired              bool
	otherResetExpired             bool
	expireReason                  store.ConfirmTokenExpireReason
}

func newFakeStore() *fakeStore {
	return &fakeStore{
		tokens:              make(map[string]fakeToken),
		daily:               make(map[string]*fakeDailyState),
		replayNonces:        make(map[string]map[string]bool),
		replayNonceCounters: make(map[string]int64),
		nextID:              1,
	}
}

func (f *fakeStore) Init(context.Context) error {
	return nil
}

func (f *fakeStore) CreateConfirmToken(_ context.Context, tokenHash string, balance float64, expiresAt time.Time, expireReason store.ConfirmTokenExpireReason) error {
	if f.createErr != nil {
		return f.createErr
	}
	if expireReason == "" {
		expireReason = store.ConfirmTokenExpireReasonTTL
	}
	f.tokens[tokenHash] = fakeToken{id: f.nextID, balance: balance, createdAt: time.Now(), expiresAt: expiresAt, expireReason: expireReason}
	f.nextID++
	return nil
}

func (f *fakeStore) DeleteConfirmToken(_ context.Context, tokenHash string) error {
	delete(f.tokens, tokenHash)
	return nil
}

func (f *fakeStore) DeleteOtherActiveConfirmTokens(_ context.Context, keepTokenHash string) (int64, error) {
	if f.deleteOtherActiveTokenErr != nil {
		return 0, f.deleteOtherActiveTokenErr
	}
	var deleted int64
	now := time.Now()
	for tokenHash, token := range f.tokens {
		if tokenHash == keepTokenHash || token.used || token.cancelled || !token.emailSent || !token.expiresAt.After(now) {
			continue
		}
		delete(f.tokens, tokenHash)
		deleted++
	}
	return deleted, nil
}

func (f *fakeStore) CancelUnverifiedConfirmEmails(context.Context) (int64, error) {
	var cancelled int64
	for tokenHash, token := range f.tokens {
		if !token.emailSent || token.used || token.cancelled {
			continue
		}
		token.cancelled = true
		f.tokens[tokenHash] = token
		cancelled++
	}
	return cancelled, nil
}

func (f *fakeStore) InvalidateActiveConfirmTokensOnStartup(context.Context) ([]store.InvalidatedConfirmToken, error) {
	var invalidated []store.InvalidatedConfirmToken
	now := time.Now()
	for tokenHash, token := range f.tokens {
		if !token.emailSent || token.used || token.cancelled || token.expired || token.startupSelfCheckExpired || !token.expiresAt.After(now) {
			continue
		}
		token.startupSelfCheckExpired = true
		token.invalidatedAt = now
		f.tokens[tokenHash] = token
		invalidated = append(invalidated, store.InvalidatedConfirmToken{
			TokenHash:     tokenHash,
			Balance:       token.balance,
			CreatedAt:     token.createdAt,
			EmailSentAt:   token.emailSentAt,
			ExpiresAt:     token.expiresAt,
			InvalidatedAt: token.invalidatedAt,
		})
	}
	return invalidated, nil
}

func (f *fakeStore) InvalidateActiveConfirmTokensForOtherReset(context.Context) ([]store.InvalidatedConfirmToken, error) {
	var invalidated []store.InvalidatedConfirmToken
	now := time.Now()
	for tokenHash, token := range f.tokens {
		if !token.emailSent || token.used || token.cancelled || token.expired || token.startupSelfCheckExpired || token.autoResetExpired || token.otherResetExpired || !token.expiresAt.After(now) {
			continue
		}
		token.otherResetExpired = true
		token.expireReason = store.ConfirmTokenExpireReasonOtherReset
		token.invalidatedAt = now
		f.tokens[tokenHash] = token
		invalidated = append(invalidated, store.InvalidatedConfirmToken{
			TokenHash:     tokenHash,
			Balance:       token.balance,
			CreatedAt:     token.createdAt,
			EmailSentAt:   token.emailSentAt,
			ExpiresAt:     token.expiresAt,
			InvalidatedAt: token.invalidatedAt,
		})
	}
	return invalidated, nil
}

func (f *fakeStore) ExpireAutoResetInvalidatedConfirmTokens(context.Context) (int64, error) {
	var expired int64
	now := time.Now()
	for tokenHash, token := range f.tokens {
		if !token.emailSent || token.used || token.cancelled || token.expired || token.expiresAt.After(now) {
			continue
		}
		token.expired = true
		token.autoResetExpired = token.expireReason == store.ConfirmTokenExpireReasonAutoReset
		if token.autoResetExpired {
			token.autoResetNotificationRequired = true
			if token.invalidatedAt.IsZero() {
				token.invalidatedAt = now
			}
		}
		f.tokens[tokenHash] = token
		expired++
	}
	return expired, nil
}

func (f *fakeStore) PendingAutoResetExpiredConfirmTokenNotifications(ctx context.Context) ([]store.InvalidatedConfirmToken, error) {
	if _, err := f.ExpireAutoResetInvalidatedConfirmTokens(ctx); err != nil {
		return nil, err
	}
	var tokens []store.InvalidatedConfirmToken
	for tokenHash, token := range f.tokens {
		if !token.autoResetExpired || !token.autoResetNotificationRequired || token.autoResetNotificationSent {
			continue
		}
		tokens = append(tokens, store.InvalidatedConfirmToken{
			TokenHash:     tokenHash,
			Balance:       token.balance,
			CreatedAt:     token.createdAt,
			EmailSentAt:   token.emailSentAt,
			ExpiresAt:     token.expiresAt,
			InvalidatedAt: token.invalidatedAt,
		})
	}
	return tokens, nil
}

func (f *fakeStore) MarkAutoResetExpiredConfirmTokenNotificationsSent(_ context.Context, tokenHashes []string) error {
	for _, tokenHash := range tokenHashes {
		token, ok := f.tokens[tokenHash]
		if !ok {
			continue
		}
		if token.autoResetNotificationRequired && !token.autoResetNotificationSent {
			token.autoResetNotificationSent = true
			f.tokens[tokenHash] = token
		}
	}
	return nil
}

func (f *fakeStore) HasActiveConfirmToken(context.Context) (bool, error) {
	_, _ = f.ExpireAutoResetInvalidatedConfirmTokens(context.Background())
	now := time.Now()
	for _, token := range f.tokens {
		if token.emailSent && !token.used && !token.cancelled && !token.expired && !token.startupSelfCheckExpired && !token.otherResetExpired && token.expiresAt.After(now) {
			return true, nil
		}
	}
	return false, nil
}

func (f *fakeStore) MarkConfirmTokenEmailSent(_ context.Context, tokenHash string) error {
	if f.markEmailSentErr != nil {
		return f.markEmailSentErr
	}
	token, ok := f.tokens[tokenHash]
	if !ok || token.used || token.cancelled || token.expired || token.expiresAt.Before(time.Now()) {
		return store.ErrTokenInvalid
	}
	token.emailSent = true
	token.emailSentAt = time.Now()
	f.tokens[tokenHash] = token
	return nil
}

func (f *fakeStore) ConsumeConfirmToken(_ context.Context, tokenHash string) (store.ConfirmToken, error) {
	_, _ = f.ExpireAutoResetInvalidatedConfirmTokens(context.Background())
	token, ok := f.tokens[tokenHash]
	if !ok || !token.emailSent || token.used || token.cancelled || token.expired || token.startupSelfCheckExpired || token.otherResetExpired || token.expiresAt.Before(time.Now()) {
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

func (f *fakeStore) NextReplayNonce(_ context.Context, scope string) (string, error) {
	for {
		next := f.replayNonceCounters[scope] + 1
		f.replayNonceCounters[scope] = next
		candidate := fmt.Sprintf("%d", next)
		if !f.replayNonceUsed(scope, candidate) {
			return candidate, nil
		}
	}
}

func (f *fakeStore) ConsumeReplayNonce(_ context.Context, scope string, replayNonce string) error {
	if f.replayNonceUsed(scope, replayNonce) {
		return store.ErrReplayNonceConsumed
	}
	if f.replayNonces[scope] == nil {
		f.replayNonces[scope] = make(map[string]bool)
	}
	f.replayNonces[scope][replayNonce] = true
	return nil
}

func (f *fakeStore) replayNonceUsed(scope string, replayNonce string) bool {
	return f.replayNonces[scope] != nil && f.replayNonces[scope][replayNonce]
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
