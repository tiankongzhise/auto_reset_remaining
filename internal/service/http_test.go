package service

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

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
	handler := NewHTTPHandler(nil, rotator)

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/rotate-logs?key=wrong", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("wrong key status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}

	recorder = httptest.NewRecorder()
	request = httptest.NewRequest(http.MethodGet, "/rotate-logs?key=secret", nil)
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
	request = httptest.NewRequest(http.MethodGet, "/resend-reset-email?key=resend-secret", nil)
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
	request = httptest.NewRequest(http.MethodGet, "/manual-reset-subscription?key=manual-secret", nil)
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
	request := httptest.NewRequest(http.MethodGet, "/test-reset-email?key=test-secret", nil)
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
	request := httptest.NewRequest(http.MethodGet, "/cancel-reset-emails?key=cancel-secret", nil)
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
