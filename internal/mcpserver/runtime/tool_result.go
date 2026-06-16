package mcpruntime

import (
	"encoding/json"
	"sync/atomic"
)

// PlainTextRenderer turns a tool result into the LLM-facing plain-text
// representation. Returning handled=false signals the caller to fall back
// to the next layer (string/ToPlainText/raw JSON).
type PlainTextRenderer func(value any) (text string, handled bool)

// plainTextProvider is implemented by tool result types that already know
// how to render themselves into plain text. Tool-side response structs use
// this to keep the formatting close to the data that produced it.
type plainTextProvider interface {
	ToPlainText() string
}

var registeredPlainTextRenderer atomic.Value // PlainTextRenderer

// RegisterToolResultPlainTextRenderer installs a global fallback renderer
// used by both the direct stdio path (toolCallResultResponse) and the
// scoped MCP path (cmd/mcp-lsp wrapScopedToolResult). Passing nil clears
// the registration so tests can reset state.
// RegisterToolResultPlainTextRenderer 注册工具结果纯文本渲染器。
func RegisterToolResultPlainTextRenderer(renderer PlainTextRenderer) {
	if renderer == nil {
		registeredPlainTextRenderer.Store(PlainTextRenderer(nil))
		return
	}
	registeredPlainTextRenderer.Store(renderer)
}

func currentPlainTextRenderer() PlainTextRenderer {
	v, _ := registeredPlainTextRenderer.Load().(PlainTextRenderer)
	return v
}

// ResolveToolResultText collapses the rendering precedence into one place:
// 1) raw string passes through as-is;
// 2) plainTextProvider.ToPlainText (per-tool struct method);
// 3) toolResultTextProvider.ToolResultText (legacy contract);
// 4) registered PlainTextRenderer (cross-package fallback);
// 5) JSON marshal of the value.
// raw is the already-marshaled JSON; pass nil to make this function do the
// marshal itself when needed. Returns the resolved text and any marshal err.
// ResolveToolResultText 解析工具结果文本。
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

// BuildToolCallResult assembles the {content, structuredContent, isError}
// envelope shared by stdio, http, and scoped MCP transports.
//
// Note: the "content" field uses []map[string]string (rather than the
// internal textContent struct) so that the rendered envelope is friendly
// to cross-package consumers and tests that have to interrogate the slice
// without importing common's unexported types. JSON marshaling is identical.
// BuildToolCallResult 构建工具call结果。
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
