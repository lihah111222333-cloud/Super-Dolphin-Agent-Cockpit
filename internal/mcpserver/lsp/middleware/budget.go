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
		return overflowEnvelope(limit, budget.Message), nil
	}
}

func fitsBudget(value any, maxBytes int) bool {
	raw, err := json.Marshal(value)
	return err == nil && len(raw) <= maxBytes
}

func overflowEnvelope(maxBytes int, custom string) map[string]any {
	message := strings.TrimSpace(custom)
	if message == "" {
		message = fmt.Sprintf("tool response exceeded %d byte budget", maxBytes)
	}
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
