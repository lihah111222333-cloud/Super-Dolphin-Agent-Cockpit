package common

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
)

// PlainTextRenderer 将工具结果转换为面向 LLM 的纯文本。
// handled=false 表示由结果策略继续尝试值自身提供的文本表示。
type PlainTextRenderer func(value any) (text string, handled bool)

// ToolCallResultPolicy 控制单个 MCP server 的 tools/call 结果信封。
// text-only policy 必须显式提供严格 renderer，零值沿用共享结果构造。
type ToolCallResultPolicy struct {
	textOnly bool
	renderer PlainTextRenderer
}

// NewTextOnlyToolCallResultPolicy 创建只返回 MCP content/isError 的显式策略。
// renderer 未处理的值只能使用 string 或实现文本 provider 的类型。
func NewTextOnlyToolCallResultPolicy(renderer PlainTextRenderer) ToolCallResultPolicy {
	return ToolCallResultPolicy{textOnly: true, renderer: renderer}
}

// plainTextProvider 由能自行渲染纯文本的工具结果实现，让格式逻辑贴近数据结构。
type plainTextProvider interface {
	ToPlainText() string
}

// ResolveToolResultText 按固定优先级解析工具结果的 LLM 可读文本。
// 优先使用 string 和值自身的文本 provider，否则复用调用方编码或执行默认编码。
func ResolveToolResultText(value any, raw []byte) (string, error) {
	if value == nil {
		return "null", nil
	}
	if str, ok := value.(string); ok {
		return str, nil
	}
	if provider, ok := value.(plainTextProvider); ok {
		return provider.ToPlainText(), nil
	}
	if provider, ok := value.(toolResultTextProvider); ok {
		return provider.ToolResultText(), nil
	}
	if raw != nil {
		return string(raw), nil
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	return string(encoded), nil
}

// BuildToolCallResult 生成 stdio、HTTP 和 scoped MCP 共用的工具调用结果 envelope。
// content 使用 []map[string]string，方便跨包测试读取，同时保持 MCP text content 线格式。
func BuildToolCallResult(value any) (map[string]any, error) {
	return BuildToolCallResultWithPolicy(value, ToolCallResultPolicy{})
}

// BuildToolCallResultWithPolicy 是 stdio、HTTP 和 scoped 路径共用的唯一结果信封 builder。
func BuildToolCallResultWithPolicy(value any, policy ToolCallResultPolicy) (map[string]any, error) {
	if policy.textOnly {
		text, err := resolveTextOnlyToolResult(value, policy.renderer)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"content": []map[string]string{{"type": "text", "text": text}},
			"isError": ToolResultIsError(value),
		}, nil
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	text, err := ResolveToolResultText(value, raw)
	if err != nil {
		return nil, err
	}
	structured, err := StructuredContentFromRaw(raw)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"content":           []map[string]string{{"type": "text", "text": text}},
		"structuredContent": structured,
		"isError":           ToolResultIsError(value),
	}, nil
}

func resolveTextOnlyToolResult(value any, renderer PlainTextRenderer) (string, error) {
	if renderer != nil {
		if text, handled := renderer(value); handled {
			return text, nil
		}
	}
	if value == nil {
		return "", errors.New("text-only tool result cannot be nil")
	}
	if text, ok := value.(string); ok {
		return text, nil
	}
	if provider, ok := value.(plainTextProvider); ok {
		return provider.ToPlainText(), nil
	}
	if provider, ok := value.(toolResultTextProvider); ok {
		return provider.ToolResultText(), nil
	}
	return "", fmt.Errorf("text-only tool result has no renderer: %T", value)
}

// isNilToolResult 判断工具返回值是否为语义 nil（处理泛型 handler 返回的有类型 nil）。
func isNilToolResult(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return v.IsNil()
	default:
		return false
	}
}
