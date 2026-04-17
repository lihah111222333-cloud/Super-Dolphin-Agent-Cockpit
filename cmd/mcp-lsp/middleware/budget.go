package middleware

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

const defaultOutputBudget = 64 * 1024

type Budget struct {
	MaxBytes int
	Message  string
}

func WithOutputBudget(next Handler, budget Budget) Handler {
	if next == nil {
		return nil
	}
	limit := budget.MaxBytes
	if limit <= 0 {
		limit = defaultOutputBudget
	}
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		value, err := next(ctx, params)
		if err != nil {
			return nil, err
		}
		if fitsBudget(value, limit) {
			return value, nil
		}
		return overflowEnvelope(value, limit, budget.Message), nil
	}
}

func fitsBudget(value any, maxBytes int) bool {
	raw, err := json.Marshal(value)
	return err == nil && len(raw) <= maxBytes
}

func overflowEnvelope(value any, maxBytes int, custom string) map[string]any {
	message := strings.TrimSpace(custom)
	if message == "" {
		message = fmt.Sprintf("tool response exceeded %d byte budget", maxBytes)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return defaultOverflowEnvelope(message)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return defaultOverflowEnvelope(message)
	}
	if _, ok := payload["files"]; ok {
		return grepOverflowEnvelope(payload, message)
	}
	return defaultOverflowEnvelope(message)
}

func defaultOverflowEnvelope(message string) map[string]any {
	return map[string]any{
		"success": true,
		"data":    []any{},
		"meta": map[string]any{
			"count":     0,
			"truncated": true,
			"message":   message,
		},
	}
}

func grepOverflowEnvelope(payload map[string]any, message string) map[string]any {
	envelope := map[string]any{
		"files":     map[string]any{},
		"total":     numericField(payload, "total"),
		"showing":   numericField(payload, "showing"),
		"truncated": true,
		"meta": map[string]any{
			"count":     0,
			"truncated": true,
			"message":   message,
		},
	}
	if hint, ok := payload["hint"].(string); ok && strings.TrimSpace(hint) != "" {
		envelope["hint"] = hint
	}
	return envelope
}

func numericField(payload map[string]any, key string) any {
	if value, ok := payload[key]; ok {
		return value
	}
	return 0
}
