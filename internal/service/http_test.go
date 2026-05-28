package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"auto_reset_remaining/internal/config"
)

func TestRotateLogsHTTPHandler(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "logs")
	archiveDir := filepath.Join(t.TempDir(), "archives")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "query.jsonl"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rotator := NewLogRotator(config.Config{
		QueryLogDir:           sourceDir,
		LogRotationEnabled:    true,
		LogRotationArchiveDir: archiveDir,
		LogRotationKey:        "secret",
	}, NewQueryLogger(sourceDir), nil)
	monitor := newTestMonitor(t, &fakeAPI{}, &fakeMailer{}, newFakeStore(), config.Config{})
	handler := NewHTTPHandler(monitor, rotator)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/rotate-logs?key=wrong", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/rotate-logs?key=secret&replay_nonce=1", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var result LogRotationResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := len(result.Files); got != 1 {
		t.Fatalf("rotated files = %d, want 1", got)
	}
}

func TestResendResetEmailHTTPHandler(t *testing.T) {
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		ResendResetEmailKey: "resend-secret",
	})
	handler := NewHTTPHandler(monitor, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/resend-reset-email?key=wrong", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if len(sender.messages) != 0 {
		t.Fatalf("sent messages after wrong key = %d, want 0", len(sender.messages))
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/resend-reset-email?key=resend-secret&replay_nonce=1", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("resend status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var result ResendConfirmEmailResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Status != "resend_confirm_email_sent" {
		t.Fatalf("status = %s, want resend_confirm_email_sent", result.Status)
	}
	if len(sender.messages) != 1 {
		t.Fatalf("sent messages = %d, want 1", len(sender.messages))
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/resend-reset-email?key=resend-secret&replay_nonce=1", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusConflict {
		t.Fatalf("replay status = %d, want %d", recorder.Code, http.StatusConflict)
	}
	if apiClient.balanceCalls != 1 {
		t.Fatalf("balanceCalls after replay = %d, want 1", apiClient.balanceCalls)
	}
}

func TestManualResetHTTPHandler(t *testing.T) {
	apiClient := &fakeAPI{balance: 0.3}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		ExternalManualResetEnabled: true,
		ExternalManualResetKey:     "manual-secret",
	})
	handler := NewHTTPHandler(monitor, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/manual-reset-subscription?key=wrong", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/manual-reset-subscription?key=manual-secret&replay_nonce=1", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("manual reset status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var result ManualResetResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Status != "external_manual_reset_success" || !result.EmailSent {
		t.Fatalf("result = %+v, want external_manual_reset_success email sent", result)
	}
}

func TestTestResetEmailHTTPHandler(t *testing.T) {
	apiClient := &fakeAPI{balance: 0.3}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		TestResetEmailKey: "test-secret",
	})
	handler := NewHTTPHandler(monitor, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/test-reset-email?key=test-secret&replay_nonce=1", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("test reset email status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var result TestResetEmailResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Status != "test_reset_email_sent" || !result.ResetSkipped || apiClient.resetCalls != 0 {
		t.Fatalf("result = %+v resetCalls=%d, want dry-run success", result, apiClient.resetCalls)
	}
}

func TestCancelResetEmailsHTTPHandler(t *testing.T) {
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		CancelResetEmailKey: "cancel-secret",
	})
	handler := NewHTTPHandler(monitor, nil)

	if status, err := monitor.Tick(t.Context()); err != nil || status != "low_balance_email_sent" {
		t.Fatalf("Tick() status=%s err=%v", status, err)
	}

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/cancel-reset-emails?key=cancel-secret&replay_nonce=1", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("cancel status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var result CancelResetEmailResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if result.Cancelled != 1 || result.Status != "reset_emails_cancelled" {
		t.Fatalf("result = %+v, want one cancelled", result)
	}
}

func TestKeyHTTPHandlersRequireReplayNonce(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "logs")
	archiveDir := filepath.Join(t.TempDir(), "archives")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rotator := NewLogRotator(config.Config{
		QueryLogDir:           sourceDir,
		LogRotationEnabled:    true,
		LogRotationArchiveDir: archiveDir,
		LogRotationKey:        "rotate-secret",
	}, NewQueryLogger(sourceDir), nil)
	monitor := newTestMonitor(t, &fakeAPI{balance: 0.4}, &fakeMailer{}, newFakeStore(), config.Config{
		ResendResetEmailKey:        "resend-secret",
		ExternalManualResetEnabled: true,
		ExternalManualResetKey:     "manual-secret",
		TestResetEmailKey:          "test-secret",
		CancelResetEmailKey:        "cancel-secret",
	})
	handler := NewHTTPHandler(monitor, rotator)

	cases := []struct {
		name string
		path string
	}{
		{name: "resend", path: "/resend-reset-email?key=resend-secret"},
		{name: "manual", path: "/manual-reset-subscription?key=manual-secret"},
		{name: "test", path: "/test-reset-email?key=test-secret"},
		{name: "cancel", path: "/cancel-reset-emails?key=cancel-secret"},
		{name: "rotate", path: "/rotate-logs?key=rotate-secret"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, tc.path, nil)
			handler.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d: %s", recorder.Code, http.StatusBadRequest, recorder.Body.String())
			}
		})
	}
}

func TestWrongKeyDoesNotConsumeReplayNonce(t *testing.T) {
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		ResendResetEmailKey: "resend-secret",
	})
	handler := NewHTTPHandler(monitor, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/resend-reset-email?key=wrong&replay_nonce=1", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
	if dataStore.replayNonceUsed("/resend-reset-email", "1") {
		t.Fatal("wrong key consumed replay nonce")
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/resend-reset-email?key=resend-secret&replay_nonce=1", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("correct key status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
}

func TestGenerateReplayNonceHTTPHandler(t *testing.T) {
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{
		ResendResetEmailKey: "resend-secret",
		TestResetEmailKey:   "test-secret",
	})
	handler := NewHTTPHandler(monitor, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/generate-replay-nonce?endpoint=/unknown&key=resend-secret", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusBadRequest {
		t.Fatalf("unknown endpoint status = %d, want %d", recorder.Code, http.StatusBadRequest)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/generate-replay-nonce?endpoint=/resend-reset-email&key=wrong", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/generate-replay-nonce?endpoint=/resend-reset-email&key=resend-secret", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("generate status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var first ReplayNonceResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &first); err != nil {
		t.Fatalf("decode first response: %v", err)
	}
	if first.Endpoint != "/resend-reset-email" || first.ReplayNonce != "1" || first.Status != "replay_nonce_generated" {
		t.Fatalf("first result = %+v, want resend endpoint nonce 1", first)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/generate-replay-nonce?endpoint=/resend-reset-email&key=resend-secret", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("second generate status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var second ReplayNonceResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &second); err != nil {
		t.Fatalf("decode second response: %v", err)
	}
	if second.ReplayNonce != "2" {
		t.Fatalf("second nonce = %q, want 2", second.ReplayNonce)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/generate-replay-nonce?endpoint=/test-reset-email&key=test-secret", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("other endpoint generate status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var other ReplayNonceResult
	if err := json.Unmarshal(recorder.Body.Bytes(), &other); err != nil {
		t.Fatalf("decode other response: %v", err)
	}
	if other.Endpoint != "/test-reset-email" || other.ReplayNonce != "1" {
		t.Fatalf("other result = %+v, want test endpoint nonce 1", other)
	}
}

func TestBalanceDashboardHTTPHandler(t *testing.T) {
	apiClient := &fakeAPI{balance: 0.4}
	sender := &fakeMailer{}
	dataStore := newFakeStore()
	monitor := newTestMonitor(t, apiClient, sender, dataStore, config.Config{})
	if err := os.MkdirAll(monitor.queries.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := strings.Join([]string{
		`{"time":"2026-05-28T02:03:24+08:00","status":"ok","balance":82.1455066,"duration_ms":1161}`,
		`{"time":"2026-05-28T13:55:16+08:00","status":"balance_error","duration_ms":331,"error":"usage API returned HTTP 401"}`,
		`{"time":"2026-05-28T22:55:57+08:00","status":"ok","balance":45.0348754,"duration_ms":424}`,
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(monitor.queries.dir, "query-2026-05-28.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(monitor, nil)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/balance", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("api status = %d, want %d: %s", recorder.Code, http.StatusOK, recorder.Body.String())
	}
	var dashboard BalanceDashboard
	if err := json.Unmarshal(recorder.Body.Bytes(), &dashboard); err != nil {
		t.Fatalf("decode dashboard: %v", err)
	}
	if dashboard.Current == nil || dashboard.Current.Balance != 45.0348754 {
		t.Fatalf("current = %+v, want latest valid balance", dashboard.Current)
	}
	if dashboard.Ignored.ErrorRecords != 1 {
		t.Fatalf("ignored error records = %d, want 1", dashboard.Ignored.ErrorRecords)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/balance", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("page status = %d, want %d", recorder.Code, http.StatusOK)
	}
	if !strings.Contains(recorder.Body.String(), `new EventSource("/api/balance/events")`) {
		t.Fatalf("page does not connect to balance events")
	}
}

func TestBalanceDashboardEventsStopsWhenRequestIsCancelled(t *testing.T) {
	apiClient := &fakeAPI{balance: 0.4}
	monitor := newTestMonitor(t, apiClient, &fakeMailer{}, newFakeStore(), config.Config{})
	if err := os.MkdirAll(monitor.queries.dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(monitor.queries.dir, "query.jsonl"), []byte(`{"time":"2026-05-28T02:03:24+08:00","status":"ok","balance":82.1455066,"duration_ms":1161}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	handler := NewHTTPHandler(monitor, nil)
	ctx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequest(http.MethodGet, "/api/balance/events", nil).WithContext(ctx)
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		handler.ServeHTTP(recorder, request)
		close(done)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("event stream did not stop after request cancellation")
	}
	if !strings.Contains(recorder.Body.String(), "event: balance") {
		t.Fatalf("event stream body = %q, want balance event", recorder.Body.String())
	}
}
