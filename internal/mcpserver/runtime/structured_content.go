package mcpruntime

import (
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpwire"
)

// StructuredContentForToolResult 为工具结果生成structured内容。
func StructuredContentForToolResult(value any) (json.RawMessage, error) {
	return mcpwire.StructuredContentForToolResult(value)
}

// StructuredContentFromRaw normalizes raw JSON into MCP structuredContent.
func StructuredContentFromRaw(raw json.RawMessage) (json.RawMessage, error) {
	return mcpwire.StructuredContentFromRaw(raw)
}
