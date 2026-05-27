package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var defaultBalancePaths = []string{
	"data.user.balance",
	"data.balance",
	"balance",
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

	candidates := findByKey(payload, map[string]bool{
		"balance":           true,
		"remaining":         true,
		"credits_remaining": true,
		"available_balance": true,
		"total_available":   true,
		"remain_quota":      true,
	})
	switch len(candidates) {
	case 0:
		return 0, errors.New("could not find balance field in usage response; set BALANCE_JSON_PATH")
	case 1:
		return numberFromAny(candidates[0].value)
	default:
		return balanceFromCandidates(candidates)
	}
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

type balanceCandidate struct {
	path  string
	value any
}

func findByKey(payload any, keys map[string]bool) []balanceCandidate {
	var candidates []balanceCandidate
	findByKeyAt(payload, keys, "", &candidates)
	return candidates
}

func findByKeyAt(payload any, keys map[string]bool, path string, candidates *[]balanceCandidate) {
	switch typed := payload.(type) {
	case map[string]any:
		for key, value := range typed {
			if keys[strings.ToLower(key)] {
				*candidates = append(*candidates, balanceCandidate{
					path:  appendPath(path, key),
					value: value,
				})
			}
		}
		for key, value := range typed {
			findByKeyAt(value, keys, appendPath(path, key), candidates)
		}
	case []any:
		for index, value := range typed {
			findByKeyAt(value, keys, appendPath(path, strconv.Itoa(index)), candidates)
		}
	}
}

func appendPath(base string, part string) string {
	if base == "" {
		return part
	}
	return base + "." + part
}

func balanceFromCandidates(candidates []balanceCandidate) (float64, error) {
	var first float64
	firstSet := false
	firstPath := ""
	for _, candidate := range candidates {
		value, err := numberFromAny(candidate.value)
		if err != nil {
			continue
		}
		if !firstSet {
			first = value
			firstSet = true
			firstPath = candidate.path
			continue
		}
		if value != first {
			return 0, fmt.Errorf("found multiple possible balance fields (%s and %s); set BALANCE_JSON_PATH", firstPath, candidate.path)
		}
	}
	if !firstSet {
		return 0, fmt.Errorf("found possible balance fields but none were numeric; set BALANCE_JSON_PATH")
	}
	return first, nil
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
