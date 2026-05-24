package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"auto_reset_remaining/internal/store"
)

func NewHTTPHandler(monitor *Monitor, rotator *LogRotator) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/confirm-reset", func(w http.ResponseWriter, r *http.Request) {
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
			http.Error(w, err.Error(), status)
			return
		}
		message := fmt.Sprintf("订阅已重置成功。\n订阅 ID: %d\n余额: %.6f\n人工确认成功次数: %d\n自动重置: %t\n重置日志 ID: %d\n",
			result.SubscriptionID, result.Balance, result.ManualConfirmSuccessCount, result.AutoResetEnabled, result.ResetLogID)
		_, _ = w.Write([]byte(message))
	})
	mux.HandleFunc("/resend-reset-email", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
		defer cancel()

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
	mux.HandleFunc("/rotate-logs", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
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
