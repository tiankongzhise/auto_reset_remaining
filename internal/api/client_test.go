package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestClientQueryBalance(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/usage" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer sk-test" {
			t.Fatalf("Authorization = %q", got)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"balance": 0.25}})
	}))
	defer server.Close()

	client := NewClient(Config{
		RayPlusBaseURL:  server.URL,
		RayPlusAPIKey:   "sk-test",
		BalanceJSONPath: "data.balance",
		UserAgent:       "test-agent",
	})
	result, err := client.QueryBalance(context.Background())
	if err != nil {
		t.Fatalf("QueryBalance() error = %v", err)
	}
	if result.Balance != 0.25 {
		t.Fatalf("Balance = %v, want 0.25", result.Balance)
	}
}

func TestClientResetQuotaLoginSubscriptionsAndReset(t *testing.T) {
	var resetCalls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			if r.Method != http.MethodPost {
				t.Fatalf("login method = %s", r.Method)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code":    0,
				"message": "success",
				"data": map[string]any{
					"access_token": "access-token",
					"expires_in":   3600,
				},
			})
		case "/api/subscriptions":
			if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
				t.Fatalf("subscriptions Authorization = %q", got)
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"subscriptions": []map[string]any{
					{"id": 111, "status": "active", "canReset": false},
					{"id": 222, "status": "active", "canReset": true},
				},
			})
		case "/api/subscriptions/222/reset-quota":
			if got := r.Header.Get("Authorization"); got != "Bearer access-token" {
				t.Fatalf("reset Authorization = %q", got)
			}
			atomic.AddInt32(&resetCalls, 1)
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(Config{
		RayPlusBaseURL:  server.URL,
		RayPlusEmail:    "user@example.com",
		RayPlusPassword: "password",
		CodexBaseURL:    server.URL,
		UserAgent:       "test-agent",
	})
	result, err := client.ResetQuota(context.Background())
	if err != nil {
		t.Fatalf("ResetQuota() error = %v", err)
	}
	if result.SubscriptionID != 222 || !result.Success || result.HTTPStatus != http.StatusOK {
		t.Fatalf("ResetQuota() = %+v", result)
	}
	if atomic.LoadInt32(&resetCalls) != 1 {
		t.Fatalf("resetCalls = %d, want 1", resetCalls)
	}
}

func TestClientResetQuotaUsesConfiguredSubscription(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/auth/login":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"access_token": "access-token", "expires_in": 3600},
			})
		case "/api/subscriptions":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"subscriptions": []map[string]any{
					{"id": 111, "status": "active", "canReset": true},
					{"id": 222, "status": "active", "canReset": true},
				},
			})
		case "/api/subscriptions/111/reset-quota":
			w.WriteHeader(http.StatusOK)
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client := NewClient(Config{
		RayPlusBaseURL:  server.URL,
		RayPlusEmail:    "user@example.com",
		RayPlusPassword: "password",
		CodexBaseURL:    server.URL,
		SubscriptionID:  111,
		UserAgent:       "test-agent",
	})
	result, err := client.ResetQuota(context.Background())
	if err != nil {
		t.Fatalf("ResetQuota() error = %v", err)
	}
	if result.SubscriptionID != 111 {
		t.Fatalf("SubscriptionID = %d, want 111", result.SubscriptionID)
	}
}
