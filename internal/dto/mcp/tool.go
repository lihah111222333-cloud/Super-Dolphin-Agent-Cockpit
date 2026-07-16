package mcp

import "encoding/json"

// MCPTool 是 MCP peer 之间交换的工具描述 schema。
// 定义在 DTO 层，使各架构层无需依赖协议特定包即可引用。
type MCPTool struct {
	Name         string          `json:"name"`                   // 工具唯一名称。
	Description  string          `json:"description,omitempty"`  // 工具功能说明，供 LLM 理解用途。
	InputSchema  json.RawMessage `json:"inputSchema"`            // JSON Schema，描述工具入参结构。
	OutputSchema json.RawMessage `json:"outputSchema,omitempty"` // JSON Schema，描述工具输出结构（可选）。
	raw          json.RawMessage
}

// NewRawTool 保留 tools/list 单项原始字节，并只做宽松字段投影。
// transport 已保证 raw 是合法 JSON；身份与 schema 由 toolbridge admission 严格校验。
func NewRawTool(raw json.RawMessage) MCPTool {
	tool := MCPTool{raw: append(json.RawMessage(nil), raw...)}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	tool.Name = decodeMCPToolString(fields["name"])
	tool.Description = decodeMCPToolString(fields["description"])
	tool.InputSchema = append(json.RawMessage(nil), fields["inputSchema"]...)
	tool.OutputSchema = append(json.RawMessage(nil), fields["outputSchema"]...)
	return tool
}

func decodeMCPToolString(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}

// RawJSON 返回 transport 收到的完整 tool 对象副本。
func (t MCPTool) RawJSON() json.RawMessage {
	return append(json.RawMessage(nil), t.raw...)
}
