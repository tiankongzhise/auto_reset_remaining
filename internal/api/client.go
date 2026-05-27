package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
)

var ErrInvalidAPIKey = errors.New("rayplus API key is invalid")

type Config struct {
	RayPlusBaseURL  string
	RayPlusAPIKey   string
	RayPlusEmail    string
	RayPlusPassword string
	CodexBaseURL    string
	SubscriptionID  int64
	BalanceJSONPath string
	UserAgent       string
	HTTPClient      *http.Client
}

type Client struct {
	cfg        Config
	httpClient *http.Client

	mu          sync.Mutex
	accessToken string
	tokenExpiry time.Time
}

type BalanceResult struct {
	Balance float64
	Raw     json.RawMessage
}

type ResetResult struct {
	SubscriptionID  int64
	HTTPStatus      int
	ResponseSummary string
	Success         bool
}

type Subscription struct {
	ID       int64  `json:"id"`
	Status   string `json:"status"`
	CanReset bool   `json:"canReset"`
}

func NewClient(cfg Config) *Client {
	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return &Client{cfg: cfg, httpClient: httpClient}
}

func (c *Client) QueryBalance(ctx context.Context) (BalanceResult, error) {
	resp, body, err := c.doUsage(ctx)
	if err != nil {
		return BalanceResult{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return BalanceResult{}, usageHTTPError(resp.StatusCode, body)
	}
	balance, err := ParseBalance(body, c.cfg.BalanceJSONPath)
	if err != nil {
		return BalanceResult{}, err
	}
	return BalanceResult{Balance: balance, Raw: append([]byte(nil), body...)}, nil
}

func (c *Client) ValidateUsageAPIKey(ctx context.Context) error {
	resp, body, err := c.doUsage(ctx)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}
	err = usageHTTPError(resp.StatusCode, body)
	if errors.Is(err, ErrInvalidAPIKey) {
		return err
	}
	return err
}

func (c *Client) doUsage(ctx context.Context) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, joinURL(c.cfg.RayPlusBaseURL, "/v1/usage"), nil)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.RayPlusAPIKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return resp, body, err
}

func (c *Client) ResetQuota(ctx context.Context) (ResetResult, error) {
	subscription, err := c.selectSubscription(ctx)
	if err != nil {
		return ResetResult{}, err
	}

	path := fmt.Sprintf("/api/subscriptions/%d/reset-quota", subscription.ID)
	resp, body, err := c.doCodex(ctx, http.MethodPost, path, nil, true)
	result := ResetResult{
		SubscriptionID:  subscription.ID,
		ResponseSummary: summarize(body),
	}
	if resp != nil {
		result.HTTPStatus = resp.StatusCode
	}
	if err != nil {
		return result, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return result, fmt.Errorf("reset quota returned HTTP %d: %s", resp.StatusCode, summarize(body))
	}
	result.Success = true
	return result, nil
}

func (c *Client) selectSubscription(ctx context.Context) (Subscription, error) {
	subscriptions, err := c.ListSubscriptions(ctx)
	if err != nil {
		return Subscription{}, err
	}
	if c.cfg.SubscriptionID > 0 {
		for _, subscription := range subscriptions {
			if subscription.ID == c.cfg.SubscriptionID {
				if !subscription.CanReset {
					return Subscription{}, fmt.Errorf("configured subscription %d cannot reset now", subscription.ID)
				}
				return subscription, nil
			}
		}
		return Subscription{}, fmt.Errorf("configured subscription %d not found", c.cfg.SubscriptionID)
	}
	for _, subscription := range subscriptions {
		if strings.EqualFold(subscription.Status, "active") && subscription.CanReset {
			return subscription, nil
		}
	}
	return Subscription{}, fmt.Errorf("no active resettable subscription found")
}

func (c *Client) ListSubscriptions(ctx context.Context) ([]Subscription, error) {
	resp, body, err := c.doCodex(ctx, http.MethodGet, "/api/subscriptions", nil, true)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("subscriptions API returned HTTP %d: %s", resp.StatusCode, summarize(body))
	}
	var payload struct {
		Subscriptions []Subscription `json:"subscriptions"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("decode subscriptions response: %w", err)
	}
	return payload.Subscriptions, nil
}

func (c *Client) doCodex(ctx context.Context, method, path string, body []byte, retryAuth bool) (*http.Response, []byte, error) {
	token, err := c.ensureToken(ctx, false)
	if err != nil {
		return nil, nil, err
	}

	resp, respBody, err := c.doCodexWithToken(ctx, method, path, body, token)
	if err != nil {
		return resp, respBody, err
	}
	if resp.StatusCode == http.StatusUnauthorized && retryAuth {
		token, err = c.ensureToken(ctx, true)
		if err != nil {
			return resp, respBody, err
		}
		return c.doCodexWithToken(ctx, method, path, body, token)
	}
	return resp, respBody, nil
}

func (c *Client) doCodexWithToken(ctx context.Context, method, path string, body []byte, token string) (*http.Response, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, joinURL(c.cfg.CodexBaseURL, path), bytes.NewReader(body))
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9,en;q=0.8")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return resp, respBody, err
}

func (c *Client) ensureToken(ctx context.Context, force bool) (string, error) {
	c.mu.Lock()
	if !force && c.accessToken != "" && time.Now().Before(c.tokenExpiry.Add(-time.Minute)) {
		token := c.accessToken
		c.mu.Unlock()
		return token, nil
	}
	c.mu.Unlock()

	token, expiresAt, err := c.login(ctx)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.accessToken = token
	c.tokenExpiry = expiresAt
	c.mu.Unlock()
	return token, nil
}

func (c *Client) login(ctx context.Context) (string, time.Time, error) {
	payload := map[string]string{
		"email":    c.cfg.RayPlusEmail,
		"password": c.cfg.RayPlusPassword,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", time.Time{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, joinURL(c.cfg.RayPlusBaseURL, "/api/v1/auth/login"), bytes.NewReader(body))
	if err != nil {
		return "", time.Time{}, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, err
	}
	defer resp.Body.Close()
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", time.Time{}, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("login returned HTTP %d: %s", resp.StatusCode, summarize(respBody))
	}

	var parsed struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
		Data    struct {
			AccessToken string `json:"access_token"`
			ExpiresIn   int    `json:"expires_in"`
		} `json:"data"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", time.Time{}, fmt.Errorf("decode login response: %w", err)
	}
	if parsed.Code != 0 {
		return "", time.Time{}, fmt.Errorf("login failed: %s", parsed.Message)
	}
	if parsed.Data.AccessToken == "" {
		return "", time.Time{}, fmt.Errorf("login response missing access token")
	}
	expiresIn := parsed.Data.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	return parsed.Data.AccessToken, time.Now().Add(time.Duration(expiresIn) * time.Second), nil
}

func joinURL(base, path string) string {
	return strings.TrimRight(base, "/") + path
}

func summarize(body []byte) string {
	text := strings.TrimSpace(string(body))
	if text == "" {
		return ""
	}
	text = strings.ReplaceAll(text, "\n", " ")
	text = strings.ReplaceAll(text, "\r", " ")
	if len(text) > 1000 {
		return text[:1000] + "...(truncated)"
	}
	return text
}

func usageHTTPError(status int, body []byte) error {
	if usageErrorCode(body) == "INVALID_API_KEY" {
		return fmt.Errorf("%w: usage API returned HTTP %d", ErrInvalidAPIKey, status)
	}
	return fmt.Errorf("usage API returned HTTP %d: %s", status, summarize(body))
}

func usageErrorCode(body []byte) string {
	var payload struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Code)
}
