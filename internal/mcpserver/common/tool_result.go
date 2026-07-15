package common

import (
	"encoding/json"
	"reflect"
	"sync/atomic"
)

// PlainTextRenderer 将工具结果转换为面向 LLM 的纯文本。
// handled=false 表示继续回退到 string、ToPlainText 或 raw JSON 渲染层。
type PlainTextRenderer func(value any) (text string, handled bool)

// plainTextProvider 由能自行渲染纯文本的工具结果实现，让格式逻辑贴近数据结构。
type plainTextProvider interface {
	ToPlainText() string
}

// registeredPlainTextRenderer 保存全局 fallback 渲染器，atomic.Value 允许测试安全重置。
var registeredPlainTextRenderer atomic.Value // plainTextRendererState

type plainTextRendererState struct {
	renderer PlainTextRenderer
}

// RegisterToolResultPlainTextRenderer 安装全局 fallback renderer。
// stdio 直连和 scoped MCP path 共用该 renderer；传 nil 会清空注册，便于测试复位。
func RegisterToolResultPlainTextRenderer(renderer PlainTextRenderer) {
	registeredPlainTextRenderer.Store(plainTextRendererState{renderer: renderer})
}

// currentPlainTextRenderer 返回当前全局注册的渲染器，未注册时返回 nil。
func currentPlainTextRenderer() PlainTextRenderer {
	state, _ := registeredPlainTextRenderer.Load().(plainTextRendererState)
	return state.renderer
}

// ResolveToolResultText 按固定优先级解析工具结果的 LLM 可读文本。
// 优先使用 string/ToPlainText/legacy ToolResultText/global renderer；都不命中时回退到 JSON。
// raw 可传入已编码 JSON，避免 BuildToolCallResult 重复 marshal。
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
	if renderer := currentPlainTextRenderer(); renderer != nil {
		if text, handled := renderer(value); handled {
			return text, nil
		}
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
