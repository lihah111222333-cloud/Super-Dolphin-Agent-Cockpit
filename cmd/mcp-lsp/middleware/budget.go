package middleware

import (
	"context"
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
)

const defaultOutputBudget = 64 * 1024

var defaultToolBudgets = map[string]int{
	"grep":       16 * 1024,
	"file":       50 * 1024,
	"inspect":    8 * 1024,
	"xref":       16 * 1024,
	"structure":  16 * 1024,
	"edit":       32 * 1024,
	"completion": 16 * 1024,
}

type Budget struct {
	MaxBytes int
}

// ToolBudget 处理工具budget。
func ToolBudget(toolName string) int {
	if v, ok := defaultToolBudgets[toolName]; ok {
		return v
	}
	return defaultOutputBudget
}

// WithOutputBudget 设置outputbudget。
func WithOutputBudget(toolName string, next Handler, budget Budget) Handler {
	if next == nil {
		return nil
	}
	limit := budget.MaxBytes
	if limit <= 0 {
		if v, ok := defaultToolBudgets[toolName]; ok {
			limit = v
		} else {
			limit = defaultOutputBudget
		}
	}
	return func(ctx context.Context, params json.RawMessage) (any, error) {
		value, err := next(ctx, params)
		if err != nil {
			return value, err
		}
		if fitsBudget(value, limit) {
			return value, nil
		}
		return overflowEnvelope(toolName, value, limit), nil
	}
}

func fitsBudget(value any, maxBytes int) bool {
	_, actualBytes, err := budgetTextBytes(value)
	return err == nil && actualBytes <= maxBytes
}

func overflowEnvelope(toolName string, value any, maxBytes int) map[string]any {
	raw, err := json.Marshal(value)
	if err != nil {
		return structuredOverflow(toolName, nil, 0, maxBytes)
	}
	actualBytes := len(raw)
	if text, err := common.ResolveToolResultText(value, raw); err == nil {
		actualBytes = len([]byte(text))
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return structuredOverflow(toolName, nil, actualBytes, maxBytes)
	}
	return structuredOverflow(toolName, payload, actualBytes, maxBytes)
}

// budgetTextBytes 按模型最终看到的文本计算输出大小，而不是 handler 的中间
// JSON 表达；这样 string 结果不会因为换行转义被误判超预算。
func budgetTextBytes(value any) ([]byte, int, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, 0, err
	}
	text, err := common.ResolveToolResultText(value, raw)
	if err != nil {
		return raw, len(raw), err
	}
	return raw, len([]byte(text)), nil
}

func structuredOverflow(toolName string, payload map[string]any, actualBytes, budgetBytes int) map[string]any {
	switch toolName {
	case "edit":
		return editOverflowEnvelope(toolName, payload, actualBytes, budgetBytes)
	default:
		hint := lookupHint(toolName)
		envelope := map[string]any{
			"success":          false,
			"original_success": originalSuccess(payload),
			"error":            "tool result exceeded output budget",
			"error_code":       "result_too_large",
			"tool":             toolName,
			"actual_bytes":     actualBytes,
			"budget_bytes":     budgetBytes,
			"summary":          extractSummary(toolName, payload),
			"hint":             hint.Hint,
		}
		if hint.NextAction != nil {
			envelope["next_action"] = hint.NextAction
		}
		return envelope
	}
}

// editOverflowEnvelope 处理编辑overflow包装。
func editOverflowEnvelope(toolName string, payload map[string]any, actualBytes, budgetBytes int) map[string]any {
	hint := lookupHint(toolName)
	envelope := map[string]any{
		"error_code":            "result_too_large",
		"tool":                  toolName,
		"actual_bytes":          actualBytes,
		"budget_bytes":          budgetBytes,
		"hint":                  hint.Hint,
		"success":               false,
		"original_success":      originalSuccess(payload),
		"action":                payload["action"],
		"error":                 "tool result exceeded output budget",
		"original_error":        payload["error"],
		"status":                payload["status"],
		"applied":               payload["applied"],
		"applied_count":         payload["applied_count"],
		"persisted":             payload["persisted"],
		"lsp_sync":              payload["lsp_sync"],
		"warning":               payload["warning"],
		"diagnostic_generation": payload["diagnostic_generation"],
	}
	if ctx, ok := payload["edit_context"].(string); ok && len(ctx) > 2048 {
		mid := len(ctx) / 2
		start := max(0, mid-1024)
		end := min(len(ctx), mid+1024)
		envelope["edit_context"] = ctx[start:end]
	} else if ok {
		envelope["edit_context"] = ctx
	}
	if current, ok := payload["current_content"].(string); ok {
		envelope["current_content_excerpt"] = centerExcerpt(current, 2048)
		envelope["current_content_truncated"] = len(current) > 2048
	}
	if body, ok := payload["func_body"].(string); ok {
		envelope["func_body"] = centerExcerpt(body, 2048)
		envelope["func_body_truncated"] = len(body) > 2048
	}
	for _, key := range []string{
		"matched_by",
		"resolved_start_offset",
		"resolved_end_offset",
		"resolved_lsp_line",
		"affected_start_line",
		"affected_end_line",
		"replaced_len",
		"replacement_len",
		"func_start",
		"func_end",
	} {
		if v, ok := payload[key]; ok {
			envelope[key] = v
		}
	}
	return envelope
}

func originalSuccess(payload map[string]any) any {
	if payload == nil {
		return nil
	}
	return payload["success"]
}

func centerExcerpt(text string, maxBytes int) string {
	if len(text) <= maxBytes {
		return text
	}
	mid := len(text) / 2
	start := max(0, mid-(maxBytes/2))
	end := min(len(text), start+maxBytes)
	return text[start:end]
}

func numericField(payload map[string]any, key string) any {
	if value, ok := payload[key]; ok {
		return value
	}
	return 0
}
