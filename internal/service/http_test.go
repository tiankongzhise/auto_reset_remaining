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
