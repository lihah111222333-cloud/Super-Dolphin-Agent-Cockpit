package middleware

import (
	"context"
	"encoding/json"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
)

// defaultOutputBudget 是未配置工具时的默认输出字节上限。
const defaultOutputBudget = 64 * 1024

// toolBudgetDescriptor 描述单个工具的输出字节上限。
type toolBudgetDescriptor struct {
	name     string
	maxBytes int
}

// defaultToolBudgets 构造工具输出字节上限描述，避免共享可变包级状态。
func defaultToolBudgets() []toolBudgetDescriptor {
	return []toolBudgetDescriptor{
		{name: "grep", maxBytes: 16 * 1024},
		{name: "structure", maxBytes: 16 * 1024},
		{name: "xref", maxBytes: 16 * 1024},
		{name: "diagnostics", maxBytes: 50 * 1024},
	}
}

// lookupToolBudget 从本地描述中查找工具输出预算。
func lookupToolBudget(toolName string) (int, bool) {
	for _, descriptor := range defaultToolBudgets() {
		if descriptor.name == toolName {
			return descriptor.maxBytes, true
		}
	}
	return 0, false
}

// Budget 定义工具输出的字节上限。
type Budget struct {
	MaxBytes int
}

// ToolBudget 查找指定工具的输出预算。
// 未配置的工具使用默认上限，保证新增工具也受统一截断保护。
func ToolBudget(toolName string) int {
	if v, ok := lookupToolBudget(toolName); ok {
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
		if v, ok := lookupToolBudget(toolName); ok {
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

// fitsBudget 计算值序列化后的可见文本是否在预算内。
// 序列化失败按不适合处理，让调用方走结构化溢出路径。
func fitsBudget(value any, maxBytes int) bool {
	_, actualBytes, err := budgetTextBytes(value)
	return err == nil && actualBytes <= maxBytes
}

// overflowEnvelope 构造结果超预算时的结构化错误包装。
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

// structuredOverflow 根据工具类型选择合适的溢出包装格式。
func structuredOverflow(toolName string, payload map[string]any, actualBytes, budgetBytes int) map[string]any {
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

// originalSuccess 保留原始 payload 中的 success 字段。
// payload 为空时返回 nil，让溢出响应仍能区分未知状态和显式失败。
func originalSuccess(payload map[string]any) any {
	if payload == nil {
		return nil
	}
	return payload["success"]
}

// numericField 从 payload 中取 key 对应的数值。
// 字段不存在时返回 0，供溢出 envelope 保持稳定 JSON 形状。
func numericField(payload map[string]any, key string) any {
	if value, ok := payload[key]; ok {
		return value
	}
	return 0
}
