package common

import (
	"encoding/json"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/mcpwire"
)

// StructuredContentForToolResult 为工具结果生成structured内容。
func StructuredContentForToolResult(value any) (json.RawMessage, error) {
	return mcpwire.StructuredContentForToolResult(value)
}

// StructuredContentFromRaw 将工具结果原始 JSON 规范化为 MCP structuredContent。
// 对象原样保留，数组包装为 items，标量包装为 value，空输入返回 nil。
func StructuredContentFromRaw(raw json.RawMessage) (json.RawMessage, error) {
	return mcpwire.StructuredContentFromRaw(raw)
}
