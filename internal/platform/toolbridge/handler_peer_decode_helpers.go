package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	mcpdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/mcp"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
)

var errMCPSurfaceClientNotConfigured = errors.New("MCP client is not configured")

// MCPToolNamespace 描述 mcp__server__tool 形式工具名拆出的 server 和 tool。
type MCPToolNamespace struct {
	Server string
	Tool   string
}

// WrapMCPToolName 生成 Codex 动态工具面使用的 MCP 命名空间工具名。
func WrapMCPToolName(server, tool string) string {
	server = strings.TrimSpace(server)
	tool = strings.TrimSpace(tool)
	if server == "" || tool == "" {
		return tool
	}
	return "mcp__" + server + "__" + tool
}

// SplitMCPToolName 解析 mcp__server__tool 工具名；非法或非命名空间名返回 false。
func SplitMCPToolName(name string) (MCPToolNamespace, bool) {
	trimmed := strings.TrimSpace(name)
	if !strings.HasPrefix(trimmed, "mcp__") {
		return MCPToolNamespace{}, false
	}
	rest := strings.TrimPrefix(trimmed, "mcp__")
	parts := strings.SplitN(rest, "__", 2)
	if len(parts) != 2 || strings.TrimSpace(parts[0]) == "" || strings.TrimSpace(parts[1]) == "" {
		return MCPToolNamespace{}, false
	}
	return MCPToolNamespace{Server: strings.TrimSpace(parts[0]), Tool: strings.TrimSpace(parts[1])}, true
}

var toolCWDTraceCanonicalTools = map[string]struct{}{
	"file":         {},
	"grep":         {},
	"inspect":      {},
	"xref":         {},
	"structure":    {},
	"patch_edit":   {},
	"completion":   {},
	"launch_agent": {},
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
	if _, ok := toolCWDTraceCanonicalTools[canonicalOrchestrationToolName(trimmed)]; ok {
		return true
	}
	return false
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

func wrappedMCPToolName(family, name string) string {
	return WrapMCPToolName(family, name)
}

func canonicalSkillToolName(name string) string {
	return "skill__" + strings.TrimSpace(name)
}

// addMCPToolAlias 给工具补短别名；第三方 server 的短别名冲突时跳过，
// 冲突工具仍可通过完整命名空间名调用。
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
	disabled codexDisabledToolSet,
) error {
	if h == nil || h.skillTools == nil {
		return nil
	}
	tools, err := h.skillTools.ListSkillToolsForSurface(ctx, scope.CWD)
	if err != nil {
		return fmt.Errorf("toolbridge: list skill tools for codex surface: %w", err)
	}
	for _, tool := range tools {
		realName := strings.TrimSpace(tool.Name)
		name := canonicalSkillToolName(realName)
		if disabledName, ok := disabled.match(realName, name); ok {
			if err := addDisabledSurfaceToolAliases(surface, disabledName, name); err != nil {
				return err
			}
			continue
		}
		entry := codexToolEntry{name: name, realName: realName, executionKind: "skill", family: "skill"}
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
	"patch_edit":        {},
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
	ToolNameHistoryRead: {},
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

// backfillMCPToolLifecycle 将可信 tools/list 结果写入 owner 表。
// 单元测试和 standalone Handler 可以不注入 backfiller；生产 Fx 图由 app 层测试保证该端口存在。
func (h *Handler) backfillMCPToolLifecycle(ctx context.Context, workspaceRoot, serverName, manifestName string, tools []mcpdto.MCPTool) error {
	if len(tools) == 0 || h == nil || h.lifecycle == nil {
		return nil
	}
	workspaceRoot, err := normalizeMCPToolLifecycleWorkspaceRoot(workspaceRoot)
	if err != nil {
		return err
	}
	serverName = strings.TrimSpace(serverName)
	if serverName == "" {
		return fmt.Errorf("toolbridge: mcp tool lifecycle server name is required")
	}
	manifestName = strings.TrimSpace(manifestName)
	if manifestName == "" {
		manifestName = serverName
	}
	req := MCPToolLifecycleBackfillRequest{
		WorkspaceRoot: workspaceRoot,
		ServerName:    serverName,
		ManifestName:  manifestName,
		Tools:         observedMCPToolLifecycleTools(tools, manifestName),
	}
	if err := h.lifecycle.BackfillMCPTools(ctx, req); err != nil {
		return fmt.Errorf("toolbridge: backfill mcp tool lifecycle for %s: %w", serverName, err)
	}
	return nil
}

func (h *Handler) hasMCPToolLifecycleBackfiller() bool {
	return h != nil && h.lifecycle != nil
}

func (h *Handler) hasMCPToolLifecyclePolicyReader() bool {
	return h != nil && h.lifecyclePolicy != nil
}

func (h *Handler) requiresMCPToolLifecyclePolicy() bool {
	return h.hasMCPToolLifecycleBackfiller() || h.hasMCPToolLifecyclePolicyReader()
}

func observedMCPToolLifecycleTools(tools []mcpdto.MCPTool, manifestName string) []contract.MCPToolLifecycleObservedTool {
	if len(tools) == 0 {
		return nil
	}
	out := make([]contract.MCPToolLifecycleObservedTool, 0, len(tools))
	for _, tool := range tools {
		out = append(out, contract.MCPToolLifecycleObservedTool{
			ManifestName: manifestName,
			Name:         tool.Name,
		})
	}
	return out
}

// filterMCPToolLifecycleTools 对 discovery 后的 MCP 工具列表执行 lifecycle policy。
// disabled/suspended/removed 只隐藏；缺 row、store 错误、未知状态和身份解析错误都向上返回错误。
func (h *Handler) filterMCPToolLifecycleTools(
	ctx context.Context,
	workspaceRoot string,
	serverName string,
	manifestName string,
	tools []mcpdto.MCPTool,
) ([]mcpdto.MCPTool, error) {
	if len(tools) == 0 || !h.requiresMCPToolLifecyclePolicy() {
		return tools, nil
	}
	policy, err := h.requireMCPToolLifecyclePolicyReader()
	if err != nil {
		return nil, err
	}
	workspaceRoot, err = normalizeMCPToolLifecycleWorkspaceRoot(workspaceRoot)
	if err != nil {
		return nil, err
	}
	serverName = strings.TrimSpace(serverName)
	if serverName == "" {
		return nil, fmt.Errorf("toolbridge: mcp tool lifecycle server name is required")
	}
	manifestName = strings.TrimSpace(manifestName)
	if manifestName == "" {
		manifestName = serverName
	}
	out := make([]mcpdto.MCPTool, 0, len(tools))
	for _, tool := range tools {
		allowed, err := h.allowsListedMCPToolLifecycle(ctx, policy, workspaceRoot, serverName, manifestName, tool)
		if err != nil {
			return nil, err
		}
		if allowed {
			out = append(out, tool)
		}
	}
	return out, nil
}

func (h *Handler) allowsListedMCPToolLifecycle(
	ctx context.Context,
	policy mcpToolLifecyclePolicyReader,
	workspaceRoot string,
	serverName string,
	manifestName string,
	tool mcpdto.MCPTool,
) (bool, error) {
	toolName := strings.TrimSpace(tool.Name)
	if toolName == "" {
		return false, fmt.Errorf("toolbridge: mcp tool lifecycle tool name is required")
	}
	decision, err := policy.ResolveMCPToolLifecycle(ctx, contract.MCPToolLifecyclePolicyRequest{
		WorkspaceRoot:       workspaceRoot,
		WorkspaceRootSource: "toolbridge_discovery",
		ServerName:          serverName,
		ManifestName:        manifestName,
		ToolName:            toolName,
		CallName:            toolName,
	})
	if err != nil {
		return false, fmt.Errorf("toolbridge: resolve mcp tool lifecycle for %s/%s: %w", serverName, toolName, err)
	}
	return mcpToolLifecycleDecisionAllows(decision)
}

func (h *Handler) requireMCPToolLifecyclePolicyReader() (mcpToolLifecyclePolicyReader, error) {
	if h == nil || h.lifecyclePolicy == nil {
		return nil, fmt.Errorf("toolbridge: mcp tool lifecycle policy reader is not configured")
	}
	return h.lifecyclePolicy, nil
}

func mcpToolLifecycleDecisionAllows(decision contract.MCPToolLifecycleDecision) (bool, error) {
	switch decision.State {
	case contract.MCPToolLifecycleEnabled:
		return true, nil
	case contract.MCPToolLifecycleDisabled,
		contract.MCPToolLifecycleSuspended,
		contract.MCPToolLifecycleRemoved:
		return false, nil
	default:
		return false, fmt.Errorf("toolbridge: unknown mcp tool lifecycle state %q for %s/%s", decision.State, decision.ServerName, decision.ToolName)
	}
}

// denyCodexSurfaceMCPToolLifecycleCall 在 Codex surface stdio client 调用前读取 owner 决策。
// surface 已保存真实 server/tool 身份，所以这里不依赖调用名猜测，拒绝时返回稳定工具结果。
func (h *Handler) denyCodexSurfaceMCPToolLifecycleCall(
	ctx context.Context,
	surface *codexToolSurface,
	entry codexToolEntry,
	req ToolCallRequest,
	callName string,
) (*ToolCallResult, bool, error) {
	if !h.requiresMCPToolLifecyclePolicy() {
		return nil, false, nil
	}
	workspaceRoot := ""
	if surface != nil {
		workspaceRoot = surface.cwd
	}
	if workspaceRoot == "" {
		workspaceRoot = req.CWD
	}
	identity := mcpToolLifecycleIdentity{
		serverName:   strings.TrimSpace(entry.family),
		manifestName: strings.TrimSpace(entry.family),
		toolName:     strings.TrimSpace(entry.realName),
		callName:     strings.TrimSpace(callName),
	}
	decision, denied, err := h.resolveMCPToolLifecycleCallDecision(ctx, workspaceRoot, identity)
	if err != nil || !denied {
		return nil, denied, err
	}
	return mcpToolLifecycleDeniedResult(req, decision), true, nil
}

// denyMCPToolLifecycleCall 在非 surface peer 路径选择 peer 前执行 lifecycle deny。
// 这里会把 legacy alias 和 mcp__server__tool 名称还原到 owner key，避免列表隐藏被直连绕过。
func (h *Handler) denyMCPToolLifecycleCall(
	ctx context.Context,
	req ToolCallRequest,
	clientKind string,
) (*ToolCallResult, bool, error) {
	if !h.requiresMCPToolLifecyclePolicy() {
		return nil, false, nil
	}
	workspaceRoot, err := h.resolveMCPToolLifecycleCallWorkspaceRoot(ctx, req)
	if err != nil {
		return nil, false, err
	}
	identity, err := resolveMCPToolLifecycleCallIdentity(req.Name, clientKind)
	if err != nil {
		return nil, false, err
	}
	decision, denied, err := h.resolveMCPToolLifecycleCallDecision(ctx, workspaceRoot, identity)
	if err != nil || !denied {
		return nil, denied, err
	}
	return mcpToolLifecycleDeniedResult(req, decision), true, nil
}

type mcpToolLifecycleIdentity struct {
	serverName   string
	manifestName string
	toolName     string
	callName     string
}

func resolveMCPToolLifecycleCallIdentity(callName string, clientKind string) (mcpToolLifecycleIdentity, error) {
	callName = strings.TrimSpace(callName)
	if callName == "" {
		return mcpToolLifecycleIdentity{}, fmt.Errorf("toolbridge: mcp tool lifecycle call name is required")
	}
	serverName := strings.TrimSpace(clientKind)
	toolName := callName
	if namespace, ok := SplitMCPToolName(callName); ok {
		serverName = strings.TrimSpace(namespace.Server)
		toolName = strings.TrimSpace(namespace.Tool)
	}
	if serverName == "" {
		return mcpToolLifecycleIdentity{}, fmt.Errorf("toolbridge: mcp tool lifecycle server name is required for %q", callName)
	}
	toolName = mcpToolLifecycleCanonicalToolName(serverName, toolName)
	if strings.TrimSpace(toolName) == "" {
		return mcpToolLifecycleIdentity{}, fmt.Errorf("toolbridge: mcp tool lifecycle tool name is required for %q", callName)
	}
	return mcpToolLifecycleIdentity{
		serverName:   serverName,
		manifestName: serverName,
		toolName:     toolName,
		callName:     callName,
	}, nil
}

func mcpToolLifecycleCanonicalToolName(serverName string, toolName string) string {
	toolName = strings.TrimSpace(toolName)
	switch strings.TrimSpace(serverName) {
	case mcpdto.ClientKindLSP:
		return canonicalToolName(toolName)
	case mcpdto.ClientKindOrch:
		return canonicalOrchestrationToolName(toolName)
	default:
		return toolName
	}
}

func (h *Handler) resolveMCPToolLifecycleCallWorkspaceRoot(ctx context.Context, req ToolCallRequest) (string, error) {
	if cwd := normalizeToolCallCWD(req.CWD); cwd != "" {
		return normalizeMCPToolLifecycleWorkspaceRoot(cwd)
	}
	result := h.resolveCurrentToolCallBindingResult(ctx, req)
	switch result.status {
	case toolCallLookupFound:
		if cwd := normalizeToolCallCWD(result.binding.CWD); cwd != "" {
			return normalizeMCPToolLifecycleWorkspaceRoot(cwd)
		}
		return "", fmt.Errorf("toolbridge: mcp tool lifecycle workspace root is required")
	case toolCallLookupFailed:
		return "", toolCallPolicyUnavailable("resolve lifecycle workspace root", result.err)
	default:
		return "", fmt.Errorf("toolbridge: mcp tool lifecycle workspace root is required")
	}
}

// resolveMCPToolLifecycleCallDecision 统一封装直接调用前的 owner 查询和状态解释。
// 读取失败或未知状态直接返回错误；只有 disabled/suspended/removed 会转换成工具级拒绝结果。
func (h *Handler) resolveMCPToolLifecycleCallDecision(
	ctx context.Context,
	workspaceRoot string,
	identity mcpToolLifecycleIdentity,
) (contract.MCPToolLifecycleDecision, bool, error) {
	policy, err := h.requireMCPToolLifecyclePolicyReader()
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, false, err
	}
	workspaceRoot, err = normalizeMCPToolLifecycleWorkspaceRoot(workspaceRoot)
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, false, err
	}
	decision, err := policy.ResolveMCPToolLifecycle(ctx, contract.MCPToolLifecyclePolicyRequest{
		WorkspaceRoot:       workspaceRoot,
		WorkspaceRootSource: "toolbridge_call",
		ServerName:          identity.serverName,
		ManifestName:        identity.manifestName,
		ToolName:            identity.toolName,
		CallName:            identity.callName,
	})
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, false, fmt.Errorf(
			"toolbridge: resolve mcp tool lifecycle for %s/%s: %w",
			identity.serverName,
			identity.toolName,
			err,
		)
	}
	allowed, err := mcpToolLifecycleDecisionAllows(decision)
	if err != nil {
		return contract.MCPToolLifecycleDecision{}, false, err
	}
	return decision, !allowed, nil
}

// mcpToolLifecycleDeniedResult 生成模型可断言的生命周期拒绝结果。
// 字段保持 machine-readable，调用方可基于 code/state/server/tool 做稳定判断。
func mcpToolLifecycleDeniedResult(req ToolCallRequest, decision contract.MCPToolLifecycleDecision) *ToolCallResult {
	denyCode := strings.TrimSpace(decision.DenyCode)
	if denyCode == "" {
		denyCode = mcpToolLifecycleDenyCode(decision.State)
	}
	envelope := map[string]any{
		"kind":   "mcp_tool_lifecycle_denied",
		"tool":   strings.TrimSpace(decision.ToolName),
		"server": strings.TrimSpace(decision.ServerName),
		"state":  string(decision.State),
		"code":   denyCode,
	}
	if reason := strings.TrimSpace(decision.Reason); reason != "" {
		envelope["reason"] = reason
	}
	if replacement := strings.TrimSpace(decision.ReplacementTool); replacement != "" {
		envelope["replacementTool"] = replacement
	}
	if callName := strings.TrimSpace(req.Name); callName != "" {
		envelope["callName"] = callName
	}
	payload, err := json.Marshal(envelope)
	if err != nil {
		return toolCallErrorResult("toolbridge: mcp tool lifecycle denied")
	}
	return &ToolCallResult{
		Success:           false,
		StructuredContent: json.RawMessage(append([]byte(nil), payload...)),
		ContentItems: []ToolCallContentItem{{
			Type: "inputText",
			Text: string(payload),
		}},
	}
}

func mcpToolLifecycleDenyCode(state contract.MCPToolLifecycleState) string {
	switch state {
	case contract.MCPToolLifecycleDisabled:
		return contract.MCPToolLifecycleDenyCodeDisabled
	case contract.MCPToolLifecycleSuspended:
		return contract.MCPToolLifecycleDenyCodeSuspended
	case contract.MCPToolLifecycleRemoved:
		return contract.MCPToolLifecycleDenyCodeRemoved
	default:
		return "mcp_tool_lifecycle_denied"
	}
}

func currentMCPToolLifecycleWorkspaceRoot() (string, error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("toolbridge: get working directory: %w", err)
	}
	return normalizeMCPToolLifecycleWorkspaceRoot(workingDir)
}

func normalizeMCPToolLifecycleWorkspaceRoot(cwd string) (string, error) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", fmt.Errorf("toolbridge: mcp tool lifecycle workspace root is required")
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		cwd = abs
	}
	return filepath.Clean(cwd), nil
}

// proxyMCPToolLifecycleWorkspaceRoot 解析 proxy tools/list 所属工作区。
// 优先使用 agent binding 中的 CWD，再退到已注入 resolver；找不到时阻断回填，避免写到错误 owner。
func (h *Handler) proxyMCPToolLifecycleWorkspaceRoot(ctx context.Context, agentID string) (string, error) {
	if !h.requiresMCPToolLifecyclePolicy() {
		return "", nil
	}
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", fmt.Errorf("toolbridge: proxy agent id is required for mcp tool lifecycle backfill")
	}
	if h.bindingStore != nil {
		if lookup, ok := h.bindingStore.(toolCallBindingLookup); ok {
			binding, err := lookup.GetBindingByAgent(ctx, agentID)
			if err != nil {
				return "", fmt.Errorf("toolbridge: resolve lifecycle workspace for agent %q: %w", agentID, err)
			}
			if cwd := strings.TrimSpace(binding.CWD); cwd != "" {
				return normalizeMCPToolLifecycleWorkspaceRoot(cwd)
			}
		}
	}
	if h.resolver != nil {
		cwd, err := h.resolver.ResolveAgentCWD(ctx, agentID)
		if err != nil {
			return "", fmt.Errorf("toolbridge: resolve lifecycle cwd for agent %q: %w", agentID, err)
		}
		if strings.TrimSpace(cwd) != "" {
			return normalizeMCPToolLifecycleWorkspaceRoot(cwd)
		}
	}
	return "", fmt.Errorf("toolbridge: lifecycle workspace root is required for proxy agent %q", agentID)
}
