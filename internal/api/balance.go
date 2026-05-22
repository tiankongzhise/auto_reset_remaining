package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var defaultBalancePaths = []string{
	"balance",
	"data.balance",
	"data.user.balance",
	"remaining",
	"data.remaining",
	"quota.remaining",
	"data.quota.remaining",
	"credits_remaining",
	"data.credits_remaining",
	"available_balance",
	"data.available_balance",
	"available",
	"data.available",
	"total_available",
	"data.total_available",
	"remain_quota",
	"data.remain_quota",
}

func ParseBalance(body []byte, configuredPath string) (float64, error) {
	var payload any
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.UseNumber()
	if err := decoder.Decode(&payload); err != nil {
		return 0, fmt.Errorf("decode usage response: %w", err)
	}

	if configuredPath != "" {
		value, ok := lookupPath(payload, configuredPath)
		if !ok {
			return 0, fmt.Errorf("BALANCE_JSON_PATH %q not found", configuredPath)
		}
		return numberFromAny(value)
	}

	for _, path := range defaultBalancePaths {
		value, ok := lookupPath(payload, path)
		if !ok {
			continue
		}
		balance, err := numberFromAny(value)
		if err == nil {
			return balance, nil
		}
	}

	if value, ok := findByKey(payload, map[string]bool{
		"balance":           true,
		"remaining":         true,
		"credits_remaining": true,
		"available_balance": true,
		"total_available":   true,
		"remain_quota":      true,
	}); ok {
		return numberFromAny(value)
	}
	return 0, errors.New("could not find balance field in usage response; set BALANCE_JSON_PATH")
}

func lookupPath(payload any, path string) (any, bool) {
	current := payload
	for _, part := range strings.Split(path, ".") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, false
		}
		switch typed := current.(type) {
		case map[string]any:
			next, ok := typed[part]
			if !ok {
				return nil, false
			}
			current = next
		case []any:
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 || idx >= len(typed) {
				return nil, false
			}
			current = typed[idx]
		default:
			return nil, false
		}
	}
	return current, true
}

func findByKey(payload any, keys map[string]bool) (any, bool) {
	switch typed := payload.(type) {
	case map[string]any:
		for key, value := range typed {
			if keys[strings.ToLower(key)] {
				return value, true
			}
		}
		for _, value := range typed {
			if found, ok := findByKey(value, keys); ok {
				return found, true
			}
		}
	case []any:
		for _, value := range typed {
			if found, ok := findByKey(value, keys); ok {
				return found, true
			}
		}
	}
	return nil, false
}

func numberFromAny(value any) (float64, error) {
	switch typed := value.(type) {
	case json.Number:
		return typed.Float64()
	case float64:
		return typed, nil
	case float32:
		return float64(typed), nil
	case int:
		return float64(typed), nil
	case int64:
		return float64(typed), nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		if err != nil {
			return 0, fmt.Errorf("balance value %q is not numeric", typed)
		}
		return parsed, nil
	default:
		return 0, fmt.Errorf("balance value has unsupported type %T", value)
	}
}
