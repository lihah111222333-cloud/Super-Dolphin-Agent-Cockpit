package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	providerdto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

var errMCPSurfaceClientNotConfigured = errors.New("MCP client is not configured")

var toolCWDTraceCanonicalTools = map[string]struct{}{
	"file":                       {},
	"grep":                       {},
	"inspect":                    {},
	"xref":                       {},
	"structure":                  {},
	"edit":                       {},
	"format_preview":             {},
	"completion":                 {},
	"orchestration_launch_agent": {},
}

func (h *Handler) resolveCurrentToolCallCWD(ctx context.Context, req ToolCallRequest) string {
	if cwd := normalizeToolCallCWD(req.CWD); cwd != "" {
		return cwd
	}
	if binding, ok := h.resolveCurrentToolCallBinding(ctx, req); ok {
		return normalizeToolCallCWD(binding.CWD)
	}
	return ""
}

func normalizeToolCallCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return ""
	}
	return normalizeToolCallWorkspaceRoot("", cwd)
}

func (h *Handler) resolveAndWarnCurrentToolCallCWD(ctx context.Context, req ToolCallRequest) string {
	cwd := h.resolveCurrentToolCallCWD(ctx, req)
	h.warnPeerToolCWDTrace(ctx, req, cwd)
	return cwd
}

func shouldWarnToolCWDTrace(toolName string) bool {
	trimmed := strings.TrimSpace(toolName)
	if _, ok := toolCWDTraceCanonicalTools[canonicalToolName(trimmed)]; ok {
		return true
	}
	return strings.HasPrefix(trimmed, "lsp_")
}

func (h *Handler) warnPeerToolCWDTrace(ctx context.Context, req ToolCallRequest, forwardedCWD string) {
	if !shouldWarnToolCWDTrace(req.Name) {
		return
	}
	bindingCWD := ""
	if binding, ok := h.resolveCurrentToolCallBinding(ctx, req); ok {
		bindingCWD = strings.TrimSpace(binding.CWD)
	}
	h.warn("toolbridge: peer tool cwd trace",
		"tool", strings.TrimSpace(req.Name),
		"agent_id", strings.TrimSpace(req.AgentID),
		"thread_id", strings.TrimSpace(req.ThreadID),
		"call_id", strings.TrimSpace(req.CallID),
		"req_cwd", strings.TrimSpace(req.CWD),
		"binding_cwd", bindingCWD,
		"forwarded_cwd", strings.TrimSpace(forwardedCWD),
		"client_kind", strings.TrimSpace(req.ClientKind),
	)
}

func wrapMCPSurfaceBinaryError(binary providerdto.MCPBinary, err error) error {
	if err == nil {
		return nil
	}
	name := strings.TrimSpace(binary.Name)
	if name == "" {
		return fmt.Errorf("toolbridge: prepare unnamed MCP server: %w", err)
	}
	return fmt.Errorf("toolbridge: prepare MCP server %q: %w", name, err)
}

func firstString(payload map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		if value := decodeString(payload[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstRaw(payload map[string]json.RawMessage, keys ...string) json.RawMessage {
	for _, key := range keys {
		if value := bytes.TrimSpace(payload[key]); len(value) != 0 {
			return value
		}
	}
	return nil
}

func nestedString(payload map[string]json.RawMessage, field string, keys ...string) string {
	raw := bytes.TrimSpace(payload[field])
	if len(raw) == 0 {
		return ""
	}
	var nested map[string]json.RawMessage
	if err := json.Unmarshal(raw, &nested); err != nil {
		return ""
	}
	return firstString(nested, keys...)
}

func decodeString(raw json.RawMessage) string {
	if len(bytes.TrimSpace(raw)) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

func toolDeferLoading(tool mcpdto.MCPTool) bool {
	value := reflect.ValueOf(tool)
	field := value.FieldByName("DeferLoading")
	return field.IsValid() && field.Kind() == reflect.Bool && field.Bool()
}

// shouldNamespaceExternalMCPTool 判断第三方 MCP 工具裸名是否已经被占用。
// LSP 和 orchestration 仍沿用严格重复检测，普通外部 server 冲突时才回退到 mcp__server__tool。
func shouldNamespaceExternalMCPTool(surface *codexToolSurface, family, canonical string) bool {
	if !isExternalMCPFamily(family) {
		return false
	}
	canonical = strings.TrimSpace(canonical)
	if canonical == "" {
		return false
	}
	if _, exists := surface.tools[canonical]; exists {
		return true
	}
	existing, hasAlias := surface.aliases[canonical]
	return hasAlias && existing != canonical
}

func isExternalMCPFamily(family string) bool {
	switch strings.TrimSpace(family) {
	case "", mcpdto.ClientKindLSP, mcpdto.ClientKindOrch:
		return false
	default:
		return true
	}
}

// addMCPToolAlias 给工具补短别名；第三方 server 的短别名冲突时跳过。
// 这样 sqlite.query 和 postgres.query 能同时存在，模型仍可用命名空间名调用后者。
func addMCPToolAlias(surface *codexToolSurface, family, alias, canonical string) error {
	if isExternalMCPFamily(family) && surfaceAliasConflicts(surface, alias, canonical) {
		return nil
	}
	return addSurfaceAlias(surface, alias, canonical)
}

// addSkillSurfaceTools 把当前项目启用的 Skill 工具加入 Codex 动态工具面。
// 它只负责 surface 暴露和冲突检测，真正读取 SKILL.md 的动作在调用阶段完成。
func (h *Handler) addSkillSurfaceTools(
	ctx context.Context,
	scope contract.CodexToolSurfaceScope,
	surface *codexToolSurface,
	out *[]contract.DynamicToolSchema,
) error {
	if h == nil || h.skillTools == nil {
		return nil
	}
	tools, err := h.skillTools.ListSkillToolsForSurface(ctx, scope.CWD)
	if err != nil {
		return fmt.Errorf("toolbridge: list skill tools for codex surface: %w", err)
	}
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		entry := codexToolEntry{name: name, realName: name, executionKind: "skill", family: "skill"}
		schema := mcpdto.MCPTool{
			Name:         name,
			Description:  strings.TrimSpace(tool.Description),
			InputSchema:  skillToolInputSchema(tool.InputSchema),
			OutputSchema: tool.OutputSchema,
		}
		if err := addSurfaceTool(surface, out, schema, entry); err != nil {
			return err
		}
	}
	return nil
}

func skillToolInputSchema(schema json.RawMessage) json.RawMessage {
	if len(bytes.TrimSpace(schema)) != 0 {
		return schema
	}
	return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
}

func surfaceAliasConflicts(surface *codexToolSurface, alias, canonical string) bool {
	alias = strings.TrimSpace(alias)
	canonical = strings.TrimSpace(canonical)
	if alias == "" || alias == canonical {
		return false
	}
	existing, ok := surface.aliases[alias]
	return ok && existing != canonical
}

func requiresCodexToolSurface(name string) bool {
	name = strings.TrimSpace(name)
	if namespace, ok := SplitMCPToolName(name); ok {
		return requiresCodexSurfaceFamilyTool(namespace.Server, namespace.Tool)
	}
	if strings.HasPrefix(name, "lsp_") {
		_, ok := legacyLSPToolAliases[name]
		return ok
	}
	if strings.HasPrefix(name, "orchestration_") {
		return requiresCanonicalCodexSurfaceTool(canonicalOrchestrationToolName(name))
	}
	return requiresCanonicalCodexSurfaceTool(name)
}

func requiresCodexSurfaceFamilyTool(family, name string) bool {
	switch strings.TrimSpace(family) {
	case mcpdto.ClientKindLSP:
		return requiresCanonicalCodexSurfaceTool(canonicalToolName(name))
	case mcpdto.ClientKindOrch:
		return requiresCanonicalCodexSurfaceTool(canonicalOrchestrationToolName(name))
	default:
		return strings.TrimSpace(name) != ""
	}
}

func requiresCanonicalCodexSurfaceTool(name string) bool {
	_, ok := canonicalCodexSurfaceTools[strings.TrimSpace(name)]
	return ok
}

var canonicalCodexSurfaceTools = map[string]struct{}{
	"file":              {},
	"inspect":           {},
	"xref":              {},
	"grep":              {},
	"structure":         {},
	"edit":              {},
	"format_preview":    {},
	"completion":        {},
	"launch_agent":      {},
	"send_message":      {},
	"stop_agent":        {},
	"recover_agent":     {},
	"interrupt_agent":   {},
	"list_agents":       {},
	"get_agent_report":  {},
	"get_agent_reports": {},
	ToolNameMemoryRead:  {},
	ToolNameMemoryWrite: {},
	ToolNameReadSection: {},
	"skill_expand_body": {},
}

// setDynamicToolDeferLoading 设置 dynamic 工具 defer loading。
func setDynamicToolDeferLoading(schema *contract.DynamicToolSchema, enabled bool) {
	if schema == nil {
		return
	}
	value := reflect.ValueOf(schema)
	if value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	field := value.FieldByName("DeferLoading")
	if field.IsValid() && field.CanSet() && field.Kind() == reflect.Bool {
		field.SetBool(enabled)
	}
}

func callIDFromRawJSONRPCID(id json.RawMessage) string {
	trimmed := bytes.TrimSpace(id)
	if len(trimmed) == 0 {
		return ""
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err == nil {
		return strings.TrimSpace(value)
	}
	var number json.Number
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.UseNumber()
	if err := decoder.Decode(&number); err == nil {
		return strings.TrimSpace(number.String())
	}
	return strings.TrimSpace(string(trimmed))
}

// callSkillSurfaceTool 用准备好的 Codex surface 调用项目级 Skill 工具。
// cwd 优先使用调用元数据，缺省时回退到 surface 准备阶段的 cwd，缺失会由 skill 模块报错。
func (h *Handler) callSkillSurfaceTool(ctx context.Context, surface *codexToolSurface, req ToolCallRequest) (*ToolCallResult, error) {
	if h == nil || h.skillTools == nil {
		return nil, fmt.Errorf("toolbridge: skill tool provider is not configured")
	}
	cwd := normalizeToolCallCWD(req.CWD)
	if cwd == "" && surface != nil {
		cwd = normalizeToolCallCWD(surface.cwd)
	}
	content, err := h.skillTools.CallSkillTool(ctx, contract.SkillToolCall{
		Name:     strings.TrimSpace(req.Name),
		CWD:      cwd,
		AgentID:  strings.TrimSpace(req.AgentID),
		ThreadID: strings.TrimSpace(req.ThreadID),
		TurnID:   strings.TrimSpace(req.TurnID),
		CallID:   strings.TrimSpace(req.CallID),
	})
	if err != nil {
		return nil, fmt.Errorf("toolbridge: call skill tool %q: %w", strings.TrimSpace(req.Name), err)
	}
	return toolCallTextResult(true, content), nil
}
