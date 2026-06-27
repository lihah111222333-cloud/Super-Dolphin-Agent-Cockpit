package toolbridge

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// 本文件集中定义 toolbridge 对外 JSON-RPC wire 协议常量。
// 这些值会被 peer MCP server、proxy handler 和兼容性测试共同观察；
// 修改既有值会让已启动 peer、外部 MCP client 或缓存握手的会话失配，必须先规划 schema/version 迁移。

// MetadataKeyAgentID 等私有 metadata key 会注入下游 tools/call payload。
// 前导下划线用于避开工具自有参数，并标记这些字段只服务内部归因；不能单边改名。
const (
	MetadataKeyAgentID        = "_agentId"
	MetadataKeyThreadID       = "_threadId"
	MetadataKeyCallID         = "_callId"
	MetadataKeyCWD            = "_cwd"
	MetadataKeyWorkspaceRoots = "_workspaceRoots"
)

// ProxyProtocolVersion 和 ProxyServerInfo* 是 proxy initialize 响应的固定字段。
// 外部 MCP client 可能缓存握手结果，重启后必须保持稳定。
const (
	ProxyProtocolVersion    = "2025-11-25"
	ProxyServerInfoName     = "proxy"
	ProxyServerInfoVersion  = "1.0.0"
	ProxyNotificationMethod = "notifications/initialized"
)

// 支持的 proxy JSON-RPC method 名称。
// proxy 只分发这些方法，未知 method 必须返回 method-not-found，不能静默 ACK。
const (
	ProxyMethodInitialize = "initialize"
	ProxyMethodToolsList  = "tools/list"
	ProxyMethodToolsCall  = "tools/call"
)

// storePeerToolInputSchemas 记录已发布给 proxy/Codex 的 peer schema，供后续 tools/call 预校验使用。
func (h *Handler) storePeerToolInputSchemas(clientKind string, tools []mcpdto.MCPTool) {
	if h == nil || strings.TrimSpace(clientKind) == "" {
		return
	}
	h.peerSchemaMu.Lock()
	defer h.peerSchemaMu.Unlock()
	if h.peerInputSchemas == nil {
		h.peerInputSchemas = make(map[string]map[string]json.RawMessage)
	}
	h.peerInputSchemas[clientKind] = toolInputSchemaMap(tools)
}

// toolInputSchemaMap 以原名和 legacy canonical 名同时索引 schema，保证显式 alias 能复用同一验证规则。
func toolInputSchemaMap(tools []mcpdto.MCPTool) map[string]json.RawMessage {
	out := make(map[string]json.RawMessage, len(tools))
	for _, tool := range tools {
		addToolInputSchema(out, tool)
	}
	return out
}

// addToolInputSchema 将单个工具的输入 schema 写入原名和 canonical 名索引。
func addToolInputSchema(out map[string]json.RawMessage, tool mcpdto.MCPTool) {
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		return
	}
	schema := cloneToolInputSchema(tool.InputSchema)
	out[name] = schema
	out[canonicalToolName(name)] = schema
}

// validatePeerToolCallInput 用最近一次 tools/list 暴露的 schema 阻断未知字段后再转发 peer。
func (h *Handler) validatePeerToolCallInput(req ToolCallRequest) error {
	schema, ok := h.lookupPeerToolInputSchema(req.ClientKind, req.Name)
	if !ok {
		return nil
	}
	return validateToolInputSchema(req.Name, schema, req.Arguments)
}

// lookupPeerToolInputSchema 查找 peer tools/list 缓存的输入 schema。
func (h *Handler) lookupPeerToolInputSchema(clientKind, name string) (json.RawMessage, bool) {
	if h == nil || strings.TrimSpace(clientKind) == "" {
		return nil, false
	}
	h.peerSchemaMu.Lock()
	defer h.peerSchemaMu.Unlock()
	byName := h.peerInputSchemas[strings.TrimSpace(clientKind)]
	if len(byName) == 0 {
		return nil, false
	}
	return lookupToolInputSchema(byName, name)
}

// lookupToolInputSchema 按原名和 canonical 名匹配 schema。
func lookupToolInputSchema(schemas map[string]json.RawMessage, name string) (json.RawMessage, bool) {
	if schema, ok := schemas[strings.TrimSpace(name)]; ok {
		return schema, true
	}
	schema, ok := schemas[canonicalToolName(name)]
	return schema, ok
}

// cloneToolInputSchema 复制 schema 原始字节，避免后续测试或调用方共享底层切片。
func cloneToolInputSchema(schema json.RawMessage) json.RawMessage {
	if len(schema) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), schema...)
}

// validateToolInputSchema 对 additionalProperties:false 的对象 schema 做调用前字段校验。
func validateToolInputSchema(toolName string, schema, args json.RawMessage) error {
	properties, strict, err := strictObjectSchemaProperties(schema)
	if err != nil || !strict {
		return err
	}
	fields, err := decodeToolArgumentFields(args)
	if err != nil {
		return fmt.Errorf("toolbridge: validate input for tool %q: %w", strings.TrimSpace(toolName), err)
	}
	return rejectUnknownToolFields(strings.TrimSpace(toolName), properties, fields)
}

// rejectUnknownToolFields 拒绝 schema properties 未声明的模型参数。
func rejectUnknownToolFields(toolName string, properties, fields map[string]json.RawMessage) error {
	for field := range fields {
		if _, ok := properties[field]; !ok {
			return fmt.Errorf("toolbridge: tool %q input field %q is not allowed by schema", toolName, field)
		}
	}
	return nil
}

// strictObjectSchemaProperties 返回 schema 的 properties；只有显式 additionalProperties:false 才启用严格校验。
func strictObjectSchemaProperties(schema json.RawMessage) (map[string]json.RawMessage, bool, error) {
	if len(bytes.TrimSpace(schema)) == 0 {
		return nil, false, nil
	}
	var decoded struct {
		Properties           map[string]json.RawMessage `json:"properties"`
		AdditionalProperties json.RawMessage            `json:"additionalProperties"`
	}
	if err := json.Unmarshal(schema, &decoded); err != nil {
		return nil, false, fmt.Errorf("toolbridge: decode input schema: %w", err)
	}
	strict := schemaAdditionalPropertiesFalse(decoded.AdditionalProperties)
	if !strict {
		return nil, false, nil
	}
	if decoded.Properties == nil {
		decoded.Properties = map[string]json.RawMessage{}
	}
	return decoded.Properties, true, nil
}

// schemaAdditionalPropertiesFalse 只把 JSON boolean false 识别为严格对象 schema。
func schemaAdditionalPropertiesFalse(raw json.RawMessage) bool {
	if len(bytes.TrimSpace(raw)) == 0 {
		return false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return !value
}

// decodeToolArgumentFields 解码工具 arguments 对象；空参数按空对象处理。
func decodeToolArgumentFields(args json.RawMessage) (map[string]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(args)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &fields); err != nil {
		return nil, err
	}
	return fields, nil
}
