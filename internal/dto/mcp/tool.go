package mcp

import "encoding/json"

// MCPTool 是 MCP peer 之间交换的工具描述 schema。
// 定义在 DTO 层，使各架构层无需依赖协议特定包即可引用。
type MCPTool struct {
	Name         string          `json:"name"`                   // 工具唯一名称。
	Description  string          `json:"description,omitempty"`  // 工具功能说明，供 LLM 理解用途。
	InputSchema  json.RawMessage `json:"inputSchema"`            // JSON Schema，描述工具入参结构。
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"` // JSON Schema，描述工具输出结构（可选）。
}
