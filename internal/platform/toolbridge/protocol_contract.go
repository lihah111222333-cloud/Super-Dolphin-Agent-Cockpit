package toolbridge

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/toolbridge/mcpwire"
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
	ProxyProtocolVersion    = mcpwire.LatestProtocolVersion
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

const (
	mcpToolTaskSupportForbidden = "forbidden"
	mcpToolTaskSupportOptional  = "optional"
	mcpToolTaskSupportRequired  = "required"
)

type mcpToolWire struct {
	Name         string                     `json:"name"`
	Title        *string                    `json:"title,omitempty"`
	Description  *string                    `json:"description,omitempty"`
	Icons        []mcpToolIcon              `json:"icons,omitempty"`
	InputSchema  json.RawMessage            `json:"inputSchema"`
	OutputSchema json.RawMessage            `json:"outputSchema,omitempty"`
	Annotations  *mcpToolAnnotations        `json:"annotations,omitempty"`
	Meta         map[string]json.RawMessage `json:"_meta,omitempty"`
	Execution    *mcpToolExecution          `json:"execution,omitempty"`
}

type mcpToolIconTheme string

const (
	mcpToolIconThemeLight mcpToolIconTheme = "light"
	mcpToolIconThemeDark  mcpToolIconTheme = "dark"
)

type mcpToolIconThemeField struct {
	value   mcpToolIconTheme
	present bool
}

// UnmarshalJSON 区分缺失 theme 与显式值，并拒绝协议不允许的 null。
func (theme *mcpToolIconThemeField) UnmarshalJSON(raw []byte) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return fmt.Errorf("MCP tool icon theme must not be null")
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return fmt.Errorf("MCP tool icon theme must be a string: %w", err)
	}
	theme.value = mcpToolIconTheme(value)
	theme.present = true
	return nil
}

type mcpToolIcon struct {
	Src      string                `json:"src"`
	MIMEType string                `json:"mimeType,omitempty"`
	Sizes    []string              `json:"sizes,omitempty"`
	Theme    mcpToolIconThemeField `json:"theme"`
}

type mcpToolAnnotations struct {
	Title           *string `json:"title,omitempty"`
	ReadOnlyHint    *bool   `json:"readOnlyHint,omitempty"`
	DestructiveHint *bool   `json:"destructiveHint,omitempty"`
	IdempotentHint  *bool   `json:"idempotentHint,omitempty"`
	OpenWorldHint   *bool   `json:"openWorldHint,omitempty"`
}

type mcpToolExecution struct {
	TaskSupport string `json:"taskSupport,omitempty"`
}

func mcpToolWireFieldSet() (map[string]struct{}, error) {
	toolType := reflect.TypeFor[mcpToolWire]()
	fields := make(map[string]struct{}, toolType.NumField())
	for index := range toolType.NumField() {
		tag, _, _ := strings.Cut(toolType.Field(index).Tag.Get("json"), ",")
		if tag == "" || tag == "-" {
			return nil, fmt.Errorf("toolbridge: MCP tool wire field %q lacks a JSON contract", toolType.Field(index).Name)
		}
		if _, duplicate := fields[tag]; duplicate {
			return nil, fmt.Errorf("toolbridge: MCP tool wire JSON field %q is duplicated", tag)
		}
		fields[tag] = struct{}{}
	}
	return fields, nil
}

// decodeMCPToolWire 严格解码 MCP 2025-11-25 Tool，并规范 taskSupport 默认值。
func decodeMCPToolWire(raw json.RawMessage) (mcpToolWire, error) {
	var tool mcpToolWire
	if err := decodeStrictMCPJSON(raw, &tool); err != nil {
		return mcpToolWire{}, fmt.Errorf("decode MCP 2025-11-25 tool: %w", err)
	}
	if strings.TrimSpace(tool.Name) == "" || strings.TrimSpace(tool.Name) != tool.Name {
		return mcpToolWire{}, fmt.Errorf("tool name must be a non-empty trimmed string")
	}
	if len(bytes.TrimSpace(tool.InputSchema)) == 0 {
		return mcpToolWire{}, fmt.Errorf("tool %q inputSchema is required", tool.Name)
	}
	if err := validateMCPToolIcons(tool.Name, tool.Icons); err != nil {
		return mcpToolWire{}, err
	}
	if tool.Execution == nil {
		tool.Execution = &mcpToolExecution{TaskSupport: mcpToolTaskSupportForbidden}
	} else if tool.Execution.TaskSupport == "" {
		tool.Execution.TaskSupport = mcpToolTaskSupportForbidden
	}
	switch tool.Execution.TaskSupport {
	case mcpToolTaskSupportForbidden, mcpToolTaskSupportOptional, mcpToolTaskSupportRequired:
	default:
		return mcpToolWire{}, fmt.Errorf("tool %q execution.taskSupport %q is invalid", tool.Name, tool.Execution.TaskSupport)
	}
	return tool, nil
}

// validateMCPToolIcons 校验 MCP Tool 图标的必填字段与主题枚举。
func validateMCPToolIcons(toolName string, icons []mcpToolIcon) error {
	for index, icon := range icons {
		if strings.TrimSpace(icon.Src) == "" {
			return fmt.Errorf("tool %q icon %d src is required", toolName, index)
		}
		if !icon.Theme.present {
			continue
		}
		switch icon.Theme.value {
		case mcpToolIconThemeLight, mcpToolIconThemeDark:
		default:
			return fmt.Errorf("tool %q icon %d theme %q is invalid", toolName, index, icon.Theme.value)
		}
	}
	return nil
}

func decodeStrictMCPJSON(raw json.RawMessage, target any) error {
	if err := rejectDuplicateMCPJSONKeys(raw); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("contains trailing JSON")
		}
		return err
	}
	return nil
}

func rejectDuplicateMCPJSONKeys(raw json.RawMessage) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := validateUniqueMCPJSONValue(decoder); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return fmt.Errorf("contains trailing JSON")
		}
		return err
	}
	return nil
}

// validateUniqueMCPJSONValue 递归检查对象键唯一性，覆盖 Tool 的嵌套结构。
func validateUniqueMCPJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		return validateUniqueMCPJSONObject(decoder)
	case '[':
		return validateUniqueMCPJSONArray(decoder)
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
}

// validateUniqueMCPJSONObject 检查当前对象层级与所有嵌套值的字段唯一性。
func validateUniqueMCPJSONObject(decoder *json.Decoder) error {
	seen := map[string]struct{}{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return err
		}
		key, ok := keyToken.(string)
		if !ok {
			return fmt.Errorf("object key must be a string")
		}
		if _, duplicate := seen[key]; duplicate {
			return fmt.Errorf("contains duplicate field %q", key)
		}
		seen[key] = struct{}{}
		if err := validateUniqueMCPJSONValue(decoder); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

func validateUniqueMCPJSONArray(decoder *json.Decoder) error {
	for decoder.More() {
		if err := validateUniqueMCPJSONValue(decoder); err != nil {
			return err
		}
	}
	_, err := decoder.Token()
	return err
}

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

// adaptPeerContentItem 将 peer wire content block 转为内部表示，并严格校验 variant 必填字段。
func adaptPeerContentItem(item peerToolCallContent) (ToolCallContentItem, error) {
	item.Type = strings.TrimSpace(item.Type)
	if err := adaptPeerContentVariant(&item); err != nil {
		return ToolCallContentItem{}, err
	}
	annotations, err := cloneOptionalJSONObject(item.Annotations, "content annotations")
	if err != nil {
		return ToolCallContentItem{}, err
	}
	meta, err := cloneOptionalJSONObject(item.Meta, "content _meta")
	if err != nil {
		return ToolCallContentItem{}, err
	}
	item.Annotations = annotations
	item.Meta = meta
	return item, nil
}

// adaptPeerContentVariant 按 content type 分派 one-hot 字段与必填值校验。
func adaptPeerContentVariant(item *peerToolCallContent) error {
	switch item.Type {
	case "text":
		return adaptPeerTextContent(item)
	case "image", "audio":
		return adaptPeerBinaryContent(item)
	case "resource":
		return adaptPeerResourceContent(item)
	case "resource_link":
		return adaptPeerResourceLinkContent(item)
	default:
		return fmt.Errorf("unsupported content type %q", item.Type)
	}
}

func adaptPeerTextContent(item *peerToolCallContent) error {
	if err := rejectPeerContentVariantFields(*item, "text", "text"); err != nil {
		return err
	}
	item.Type = "inputText"
	return nil
}

func adaptPeerBinaryContent(item *peerToolCallContent) error {
	if strings.TrimSpace(item.Data) == "" {
		return fmt.Errorf("%s data is required", item.Type)
	}
	if strings.TrimSpace(item.MIMEType) == "" {
		return fmt.Errorf("%s mimeType is required", item.Type)
	}
	return rejectPeerContentVariantFields(*item, item.Type, "data", "mimeType")
}

func adaptPeerResourceContent(item *peerToolCallContent) error {
	if err := rejectPeerContentVariantFields(*item, "resource", "resource"); err != nil {
		return err
	}
	resource, err := cloneJSONObject(item.Resource, "resource object")
	if err != nil {
		return err
	}
	item.Resource = resource
	return nil
}

// adaptPeerResourceLinkContent 校验资源链接字段、大小和图标 JSON，禁止混入其他 variant。
func adaptPeerResourceLinkContent(item *peerToolCallContent) error {
	if err := rejectPeerContentVariantFields(
		*item,
		"resource_link",
		"mimeType",
		"uri",
		"name",
		"title",
		"description",
		"size",
		"icons",
	); err != nil {
		return err
	}
	if strings.TrimSpace(item.URI) == "" {
		return fmt.Errorf("resource_link uri is required")
	}
	if item.Size != nil && *item.Size < 0 {
		return fmt.Errorf("resource_link size must be non-negative")
	}
	icons, err := cloneOptionalJSONArray(item.Icons, "resource_link icons")
	if err != nil {
		return err
	}
	item.Icons = icons
	return nil
}

type peerContentFieldPresence struct {
	name    string
	present bool
}

// rejectPeerContentVariantFields 拒绝当前 content variant 未允许的 one-hot 字段。
func rejectPeerContentVariantFields(item peerToolCallContent, variant string, allowed ...string) error {
	for _, field := range peerContentFields(item) {
		if !field.present {
			continue
		}
		if !contentFieldAllowed(field.name, allowed) {
			return fmt.Errorf("%s content contains fields from another variant", variant)
		}
	}
	return nil
}

func peerContentFields(item peerToolCallContent) []peerContentFieldPresence {
	return []peerContentFieldPresence{
		{name: "text", present: item.Text != ""},
		{name: "data", present: item.Data != ""},
		{name: "mimeType", present: item.MIMEType != ""},
		{name: "resource", present: len(item.Resource) > 0},
		{name: "uri", present: item.URI != ""},
		{name: "name", present: item.Name != ""},
		{name: "title", present: item.Title != ""},
		{name: "description", present: item.Description != ""},
		{name: "size", present: item.Size != nil},
		{name: "icons", present: len(item.Icons) > 0},
	}
}

func contentFieldAllowed(name string, allowed []string) bool {
	return slices.Contains(allowed, name)
}

// contentItemToMCP 将内部 content block 转回 MCP wire map，保持字段和顺序无损。
func contentItemToMCP(item ToolCallContentItem) (map[string]any, error) {
	item.Type = normalizedMCPContentType(item.Type)
	validated, err := adaptPeerContentItem(item)
	if err != nil {
		return nil, err
	}
	if validated.Type == "inputText" {
		validated.Type = "text"
	}
	block := map[string]any{"type": validated.Type}
	if err := appendMCPContentVariantFields(block, validated); err != nil {
		return nil, err
	}
	appendMCPContentMetadata(block, validated)
	return block, nil
}

func normalizedMCPContentType(itemType string) string {
	switch strings.TrimSpace(itemType) {
	case "", "inputText":
		return "text"
	default:
		return strings.TrimSpace(itemType)
	}
}

func appendMCPContentVariantFields(block map[string]any, item ToolCallContentItem) error {
	switch item.Type {
	case "text":
		block["text"] = item.Text
	case "image", "audio":
		block["data"] = item.Data
		block["mimeType"] = item.MIMEType
	case "resource":
		block["resource"] = cloneRawJSON(item.Resource)
	case "resource_link":
		appendMCPResourceLinkFields(block, item)
	default:
		return fmt.Errorf("unsupported content type %q", item.Type)
	}
	return nil
}

// appendMCPResourceLinkFields 只写入资源链接实际存在的可选 wire 字段。
func appendMCPResourceLinkFields(block map[string]any, item ToolCallContentItem) {
	block["uri"] = item.URI
	if item.Name != "" {
		block["name"] = item.Name
	}
	if item.Title != "" {
		block["title"] = item.Title
	}
	if item.Description != "" {
		block["description"] = item.Description
	}
	if item.MIMEType != "" {
		block["mimeType"] = item.MIMEType
	}
	if item.Size != nil {
		block["size"] = *item.Size
	}
	if len(item.Icons) > 0 {
		block["icons"] = cloneRawJSON(item.Icons)
	}
}

func appendMCPContentMetadata(block map[string]any, item ToolCallContentItem) {
	if len(item.Annotations) > 0 {
		block["annotations"] = cloneRawJSON(item.Annotations)
	}
	if len(item.Meta) > 0 {
		block["_meta"] = cloneRawJSON(item.Meta)
	}
}

// cloneJSONObject 校验并深拷贝必填 JSON object。
func cloneJSONObject(raw json.RawMessage, field string) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '{' || !json.Valid(trimmed) {
		return nil, fmt.Errorf("%s is required and must be a JSON object", field)
	}
	return cloneRawJSON(trimmed), nil
}

// cloneOptionalJSONObject 校验并深拷贝可选 JSON object。
func cloneOptionalJSONObject(raw json.RawMessage, field string) (json.RawMessage, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	value, err := cloneJSONObject(raw, field)
	if err != nil {
		return nil, err
	}
	return value, nil
}

// cloneOptionalJSONArray 校验并深拷贝可选 JSON array。
func cloneOptionalJSONArray(raw json.RawMessage, field string) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}
	if bytes.Equal(trimmed, []byte("null")) || trimmed[0] != '[' || !json.Valid(trimmed) {
		return nil, fmt.Errorf("%s must be a JSON array", field)
	}
	return cloneRawJSON(trimmed), nil
}

func cloneRawJSON(raw json.RawMessage) json.RawMessage {
	return json.RawMessage(append([]byte(nil), raw...))
}
