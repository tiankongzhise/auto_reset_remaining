package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"auto_reset_remaining/internal/config"
	"auto_reset_remaining/internal/store"
)

var (
	ErrReplayNonceEndpointInvalid = errors.New("unsupported replay nonce endpoint")
	ErrReplayNonceStoreMissing    = errors.New("replay nonce store is not configured")
	ErrReplayNonceMissing         = errors.New("replay_nonce is required")
	ErrReplayNonceInvalid         = errors.New("replay_nonce must be a positive integer")
)

type ReplayNonceResult struct {
	Endpoint    string `json:"endpoint"`
	ReplayNonce string `json:"replay_nonce"`
	Status      string `json:"status"`
}

func NewHTTPHandler(monitor *Monitor, rotator *LogRotator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/generate-replay-nonce", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		endpoint, err := replayEndpointFor(r.URL.Query().Get("endpoint"))
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := endpoint.validateKey(monitor, rotator, r.URL.Query().Get("key")); err != nil {
			http.Error(w, err.Error(), replayKeyHTTPStatus(err))
			return
		}
		replayStore := replayNonceStore(monitor)
		if replayStore == nil {
			http.Error(w, ErrReplayNonceStoreMissing.Error(), http.StatusServiceUnavailable)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()

		replayNonce, err := replayStore.NextReplayNonce(ctx, endpoint.path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(ReplayNonceResult{
			Endpoint:    endpoint.path,
			ReplayNonce: replayNonce,
			Status:      "replay_nonce_generated",
		})
	})
	mux.HandleFunc("/confirm-reset", func(w http.ResponseWriter, r *http.Request) {
		if monitor == nil {
			http.Error(w, "monitor is not configured", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		result, err := monitor.Confirm(ctx, r.URL.Query().Get("token"))
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrTokenInvalid) {
				status = http.StatusBadRequest
			}
			if errors.Is(err, ErrDailyResetLimitReached) {
				status = http.StatusTooManyRequests
			}
			if errors.Is(err, ErrResetInProgress) {
				status = http.StatusConflict
			}
			http.Error(w, err.Error(), status)
			return
		}
		message := fmt.Sprintf("订阅已重置成功。\n订阅 ID: %d\n余额: %.6f\n人工确认成功次数: %d\n自动重置: %t\n重置日志 ID: %d\n",
			result.SubscriptionID, result.Balance, result.ManualConfirmSuccessCount, result.AutoResetEnabled, result.ResetLogID)
		_, _ = w.Write([]byte(message))
	})
	mux.HandleFunc("/resend-reset-email", func(w http.ResponseWriter, r *http.Request) {
		if monitor == nil {
			http.Error(w, "monitor is not configured", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		if err := validateResendResetEmailKey(monitor, rotator, r.URL.Query().Get("key")); err != nil {
			http.Error(w, err.Error(), replayKeyHTTPStatus(err))
			return
		}
		if err := requireReplayNonce(ctx, monitor, "/resend-reset-email", r.URL.Query().Get("replay_nonce")); err != nil {
			http.Error(w, err.Error(), replayNonceHTTPStatus(err))
			return
		}
		result, err := monitor.ResendConfirmEmail(ctx, r.URL.Query().Get("key"))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrResendUnauthorized) || errors.Is(err, ErrResendKeyMissing) {
				status = http.StatusUnauthorized
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("/manual-reset-subscription", func(w http.ResponseWriter, r *http.Request) {
		if monitor == nil {
			http.Error(w, "monitor is not configured", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		if err := validateManualResetKey(monitor, rotator, r.URL.Query().Get("key")); err != nil {
			http.Error(w, err.Error(), replayKeyHTTPStatus(err))
			return
		}
		if err := requireReplayNonce(ctx, monitor, "/manual-reset-subscription", r.URL.Query().Get("replay_nonce")); err != nil {
			http.Error(w, err.Error(), replayNonceHTTPStatus(err))
			return
		}
		result, err := monitor.ManualReset(ctx, r.URL.Query().Get("key"))
		if err != nil {
			http.Error(w, err.Error(), manualResetHTTPStatus(err))
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("/test-reset-email", func(w http.ResponseWriter, r *http.Request) {
		if monitor == nil {
			http.Error(w, "monitor is not configured", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		if err := validateTestResetEmailKey(monitor, rotator, r.URL.Query().Get("key")); err != nil {
			http.Error(w, err.Error(), replayKeyHTTPStatus(err))
			return
		}
		if err := requireReplayNonce(ctx, monitor, "/test-reset-email", r.URL.Query().Get("replay_nonce")); err != nil {
			http.Error(w, err.Error(), replayNonceHTTPStatus(err))
			return
		}
		result, err := monitor.TestResetEmail(ctx, r.URL.Query().Get("key"))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrTestResetEmailUnauthorized) || errors.Is(err, ErrTestResetEmailKeyMissing) {
				status = http.StatusUnauthorized
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("/cancel-reset-emails", func(w http.ResponseWriter, r *http.Request) {
		if monitor == nil {
			http.Error(w, "monitor is not configured", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

		if err := validateCancelResetEmailKey(monitor, rotator, r.URL.Query().Get("key")); err != nil {
			http.Error(w, err.Error(), replayKeyHTTPStatus(err))
			return
		}
		if err := requireReplayNonce(ctx, monitor, "/cancel-reset-emails", r.URL.Query().Get("replay_nonce")); err != nil {
			http.Error(w, err.Error(), replayNonceHTTPStatus(err))
			return
		}
		result, err := monitor.CancelResetEmails(ctx, r.URL.Query().Get("key"))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrCancelResetEmailUnauthorized) || errors.Is(err, ErrCancelResetEmailKeyMissing) {
				status = http.StatusUnauthorized
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(result)
	})
	mux.HandleFunc("/rotate-logs", func(w http.ResponseWriter, r *http.Request) {
		if rotator == nil {
			http.Error(w, "log rotator is not configured", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := validateLogRotationKey(monitor, rotator, r.URL.Query().Get("key")); err != nil {
			http.Error(w, err.Error(), replayKeyHTTPStatus(err))
			return
		}
		if err := requireReplayNonce(r.Context(), monitor, "/rotate-logs", r.URL.Query().Get("replay_nonce")); err != nil {
			http.Error(w, err.Error(), replayNonceHTTPStatus(err))
			return
		}
		result, err := rotator.RotateWithKey(r.URL.Query().Get("key"))
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, ErrLogRotationUnauthorized) {
				status = http.StatusUnauthorized
			}
			if errors.Is(err, ErrLogRotationDisabled) {
				status = http.StatusServiceUnavailable
			}
			http.Error(w, err.Error(), status)
			return
		}
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(result)
	})
	return mux
}

func manualResetHTTPStatus(err error) int {
	switch {
	case errors.Is(err, ErrExternalManualResetUnauthorized), errors.Is(err, ErrExternalManualResetKeyMissing):
		return http.StatusUnauthorized
	case errors.Is(err, ErrExternalManualResetDisabled):
		return http.StatusServiceUnavailable
	case errors.Is(err, ErrDailyResetLimitReached):
		return http.StatusTooManyRequests
	case errors.Is(err, ErrResetInProgress):
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

type replayEndpoint struct {
	path        string
	validateKey func(monitor *Monitor, rotator *LogRotator, key string) error
}

func replayEndpointFor(endpoint string) (replayEndpoint, error) {
	endpoint = strings.TrimSpace(endpoint)
	switch endpoint {
	case "/resend-reset-email":
		return replayEndpoint{path: endpoint, validateKey: validateResendResetEmailKey}, nil
	case "/manual-reset-subscription":
		return replayEndpoint{path: endpoint, validateKey: validateManualResetKey}, nil
	case "/test-reset-email":
		return replayEndpoint{path: endpoint, validateKey: validateTestResetEmailKey}, nil
	case "/cancel-reset-emails":
		return replayEndpoint{path: endpoint, validateKey: validateCancelResetEmailKey}, nil
	case "/rotate-logs":
		return replayEndpoint{path: endpoint, validateKey: validateLogRotationKey}, nil
	default:
		return replayEndpoint{}, ErrReplayNonceEndpointInvalid
	}
}

func validateResendResetEmailKey(monitor *Monitor, _ *LogRotator, key string) error {
	cfg, err := replayMonitorConfig(monitor, "/resend-reset-email")
	if err != nil {
		return err
	}
	return requireSharedKey(cfg.ResendResetEmailKey, key, ErrResendKeyMissing, ErrResendUnauthorized)
}

func validateManualResetKey(monitor *Monitor, _ *LogRotator, key string) error {
	cfg, err := replayMonitorConfig(monitor, "/manual-reset-subscription")
	if err != nil {
		return err
	}
	return requireSharedKey(cfg.ExternalManualResetKey, key, ErrExternalManualResetKeyMissing, ErrExternalManualResetUnauthorized)
}

func validateTestResetEmailKey(monitor *Monitor, _ *LogRotator, key string) error {
	cfg, err := replayMonitorConfig(monitor, "/test-reset-email")
	if err != nil {
		return err
	}
	return requireSharedKey(cfg.TestResetEmailKey, key, ErrTestResetEmailKeyMissing, ErrTestResetEmailUnauthorized)
}

func validateCancelResetEmailKey(monitor *Monitor, _ *LogRotator, key string) error {
	cfg, err := replayMonitorConfig(monitor, "/cancel-reset-emails")
	if err != nil {
		return err
	}
	return requireSharedKey(cfg.CancelResetEmailKey, key, ErrCancelResetEmailKeyMissing, ErrCancelResetEmailUnauthorized)
}

func validateLogRotationKey(_ *Monitor, rotator *LogRotator, key string) error {
	if rotator == nil {
		return ErrLogRotationDisabled
	}
	if !rotator.validKey(key) {
		return ErrLogRotationUnauthorized
	}
	return nil
}

func replayMonitorConfig(monitor *Monitor, endpoint string) (config.Config, error) {
	if monitor == nil {
		return config.Config{}, ErrReplayNonceStoreMissing
	}
	cfg, refreshErr := monitor.refreshEmailConfig()
	if refreshErr != nil {
		monitor.logger.Printf("replay nonce config refresh failed endpoint=%s error=%v", endpoint, refreshErr)
		cfg = monitor.snapshot()
	}
	return cfg, nil
}

func replayNonceStore(monitor *Monitor) store.Store {
	if monitor == nil {
		return nil
	}
	return monitor.store
}

func replayKeyHTTPStatus(err error) int {
	switch {
	case errors.Is(err, ErrResendUnauthorized), errors.Is(err, ErrResendKeyMissing),
		errors.Is(err, ErrExternalManualResetUnauthorized), errors.Is(err, ErrExternalManualResetKeyMissing),
		errors.Is(err, ErrTestResetEmailUnauthorized), errors.Is(err, ErrTestResetEmailKeyMissing),
		errors.Is(err, ErrCancelResetEmailUnauthorized), errors.Is(err, ErrCancelResetEmailKeyMissing),
		errors.Is(err, ErrLogRotationUnauthorized):
		return http.StatusUnauthorized
	case errors.Is(err, ErrReplayNonceStoreMissing), errors.Is(err, ErrLogRotationDisabled):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}

func normalizeReplayNonce(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", ErrReplayNonceMissing
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return "", ErrReplayNonceInvalid
	}
	return strconv.FormatInt(value, 10), nil
}

func requireReplayNonce(ctx context.Context, monitor *Monitor, endpoint string, rawReplayNonce string) error {
	replayNonce, err := normalizeReplayNonce(rawReplayNonce)
	if err != nil {
		return err
	}
	replayStore := replayNonceStore(monitor)
	if replayStore == nil {
		return ErrReplayNonceStoreMissing
	}
	return replayStore.ConsumeReplayNonce(ctx, endpoint, replayNonce)
}

func replayNonceHTTPStatus(err error) int {
	switch {
	case errors.Is(err, store.ErrReplayNonceConsumed):
		return http.StatusConflict
	case errors.Is(err, ErrReplayNonceMissing), errors.Is(err, ErrReplayNonceInvalid):
		return http.StatusBadRequest
	case errors.Is(err, ErrReplayNonceStoreMissing):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
