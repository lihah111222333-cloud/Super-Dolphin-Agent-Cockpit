package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

// Peer-list/decode helpers live in handler_peer_decode.go.

// peerToolsListOutcome 保存单类 peer 的 tools/list 结果，保留 clientKind 方便聚合错误。
type peerToolsListOutcome struct {
	clientKind string
	tools      []dto.MCPTool
	err        error
}

// listPeerToolsForCodex 并发查询 Codex 需要暴露的 peer 工具列表。
// 输出顺序仍按 kinds 入参排序，避免动态工具面因 goroutine 完成顺序发生抖动。
func (h *Handler) listPeerToolsForCodex(ctx context.Context, kinds ...string) []peerToolsListOutcome {
	if ctx == nil {
		ctx = context.Background()
	}
	if len(kinds) == 0 {
		return nil
	}
	type indexedOutcome struct {
		index   int
		outcome peerToolsListOutcome
	}
	ch := make(chan indexedOutcome, len(kinds))
	var toolsWG sync.WaitGroup
	for index, kind := range kinds {
		toolsWG.Go(func() {
			defer func() {
				if recovered := recover(); recovered != nil {
					ch <- indexedOutcome{
						index: index,
						outcome: peerToolsListOutcome{
							clientKind: kind,
							err:        fmt.Errorf("toolbridge list peer tools panic: %v", recovered),
						},
					}
				}
			}()
			tools, err := h.listPeerTools(ctx, kind)
			ch <- indexedOutcome{
				index: index,
				outcome: peerToolsListOutcome{
					clientKind: kind,
					tools:      tools,
					err:        err,
				},
			}
		})
	}
	out := make([]peerToolsListOutcome, len(kinds))
	for range kinds {
		result := <-ch
		out[result.index] = result.outcome
	}
	toolsWG.Wait()
	return out
}

// joinPeerToolErrors 合并各类 peer 的 tools/list 错误，并在错误中保留来源。
func joinPeerToolErrors(outcomes []peerToolsListOutcome) error {
	errs := make([]error, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", outcome.clientKind, outcome.err))
		}
	}
	return errors.Join(errs...)
}

// ListToolsForCodex 聚合 host-direct、orchestration 和 LSP 工具，生成 Codex 动态工具面。
// host-direct 先加入并拥有同名优先级，peer 发现失败直接返回错误而不是发布半可用工具面。
func (h *Handler) ListToolsForCodex(ctx context.Context) ([]contract.DynamicToolSchema, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Host-direct 工具优先加入列表：dedup 遵守“先加入者胜出”原则，同名 peer 工具会被忽略。
	// 这保证调用阶段 hostTools.HasTool 命中与列表阶段优先级一致。
	var hostTools []dto.MCPTool
	if h != nil && h.hostTools != nil {
		hostTools = h.hostTools.ListHostTools()
	}
	seenToolSources := make(map[string]string, len(hostTools))
	merged := h.appendDynamicToolsWithShadowWarning(nil, seenToolSources, "host", hostTools)
	outcomes := h.listPeerToolsForCodex(ctx, dto.ClientKindOrch, dto.ClientKindLSP)
	if err := joinPeerToolErrors(outcomes); err != nil {
		return nil, fmt.Errorf("toolbridge dynamic tools peer discovery failed: %w", err)
	}
	if err := h.backfillListToolsForCodexPeerDiscovery(ctx, outcomes); err != nil {
		return nil, err
	}
	if err := h.filterListToolsForCodexMCPPolicy(ctx, outcomes); err != nil {
		return nil, err
	}
	for _, outcome := range outcomes {
		merged = h.appendDynamicToolsWithShadowWarning(merged, seenToolSources, outcome.clientKind, outcome.tools)
	}
	if len(merged) == 0 {
		return nil, ErrNoPeerAvailable
	}
	return toCodexDynamicTools(merged), nil
}

// backfillListToolsForCodexPeerDiscovery 为旧版 Codex 动态工具列表入口补写 peer owner。
// 这个入口没有显式 scope，只能使用当前进程工作目录；生产 Codex 会优先走带 CWD 的 surface 准备路径。
func (h *Handler) backfillListToolsForCodexPeerDiscovery(ctx context.Context, outcomes []peerToolsListOutcome) error {
	if !h.hasMCPToolLifecycleBackfiller() {
		return nil
	}
	workspaceRoot, err := currentMCPToolLifecycleWorkspaceRoot()
	if err != nil {
		return err
	}
	for _, outcome := range outcomes {
		if err := h.backfillMCPToolLifecycle(ctx, workspaceRoot, outcome.clientKind, outcome.clientKind, outcome.tools); err != nil {
			return err
		}
	}
	return nil
}

// filterListToolsForCodexMCPPolicy 在旧版动态工具列表入口完成 backfill 后执行 lifecycle filtering。
func (h *Handler) filterListToolsForCodexMCPPolicy(ctx context.Context, outcomes []peerToolsListOutcome) error {
	if len(outcomes) == 0 || !h.requiresMCPToolLifecyclePolicy() {
		return nil
	}
	workspaceRoot, err := currentMCPToolLifecycleWorkspaceRoot()
	if err != nil {
		return err
	}
	for i := range outcomes {
		filtered, err := h.filterMCPToolLifecycleTools(
			ctx,
			workspaceRoot,
			outcomes[i].clientKind,
			outcomes[i].clientKind,
			outcomes[i].tools,
		)
		if err != nil {
			return err
		}
		outcomes[i].tools = filtered
	}
	return nil
}

// appendDynamicToolsWithShadowWarning 是测试可替换的动态工具合并入口。
func (h *Handler) appendDynamicToolsWithShadowWarning(dst []dto.MCPTool, seen map[string]string, source string, tools []dto.MCPTool) []dto.MCPTool {
	return h.appendMCPToolsWithShadowWarning(dst, seen, source, tools)
}

// appendMCPToolsWithShadowWarning 追加 MCP 工具并记录同名 shadow 情况。
// 保留“先加入者胜出”规则，确保列表阶段与调用阶段的 host-direct 优先级一致。
func (h *Handler) appendMCPToolsWithShadowWarning(dst []dto.MCPTool, seen map[string]string, source string, tools []dto.MCPTool) []dto.MCPTool {
	if seen == nil {
		seen = make(map[string]string, len(tools))
	}
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
			continue
		}
		if isRemovedSkillToolName(name) {
			h.warn("toolbridge removed skill tool blocked from dynamic list",
				"tool", name,
				"source", source,
			)
			continue
		}
		if previousSource, ok := seen[name]; ok {
			h.warn("toolbridge dynamic tool shadowed by earlier source",
				"tool", name,
				"source", source,
				"shadowed_by", previousSource,
			)
			continue
		}
		if _, reserved := reservedHostOnlySurfaceToolCanonicalName(source, name); reserved && source != "host" {
			h.warn("toolbridge peer tool blocked by host-only reservation",
				"tool", name,
				"source", source,
			)
			continue
		}
		seen[name] = source
		dst = append(dst, tool)
	}
	return dst
}

// isReservedHostOnlyToolName 判断工具名是否只能由 host-direct registry 处理。
func isReservedHostOnlyToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case ToolNameMemoryRead, ToolNameMemoryWrite, ToolNameHistoryRead, ToolNameObservabilityTraceGet:
		return true
	default:
		return false
	}
}

// reservedHostOnlyToolCanonicalName 将调用名折叠为保留工具的 canonical 名称。
func reservedHostOnlyToolCanonicalName(name string) (string, bool) {
	return reservedHostOnlyToolCanonicalNameForFamily("", name)
}

// reservedHostOnlySurfaceToolCanonicalName 在 Codex surface family 作用域内识别 host-only 工具。
func reservedHostOnlySurfaceToolCanonicalName(family, name string) (string, bool) {
	return reservedHostOnlyToolCanonicalNameForFamily(family, name)
}

// reservedHostOnlyToolCanonicalNameForFamily 枚举裸名、family 包装名和 legacy 名称后做保留匹配。
func reservedHostOnlyToolCanonicalNameForFamily(family, name string) (string, bool) {
	for _, candidate := range reservedHostOnlyToolNameCandidates(family, name) {
		candidate = strings.TrimSpace(candidate)
		if isReservedHostOnlyToolName(candidate) {
			return candidate, true
		}
	}
	return "", false
}

// reservedHostOnlyToolNameCandidates 生成可能指向同一 host-only 工具的别名集合。
func reservedHostOnlyToolNameCandidates(family, name string) []string {
	candidates := []string{strings.TrimSpace(name)}
	if family = strings.TrimSpace(family); family != "" {
		candidates = append(candidates, canonicalCodexToolName(family, name))
	}
	if namespace, ok := SplitMCPToolName(name); ok {
		candidates = append(candidates, namespace.Tool, canonicalCodexToolName(namespace.Server, namespace.Tool))
	}
	candidates = append(candidates, canonicalToolName(name))
	return candidates
}

// isRemovedSkillToolName 判断旧 skill 工具名是否已从 Codex surface 下线。
func isRemovedSkillToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case ToolNameReadSection, ToolNameLegacySkillExpandBody, ToolNameLegacySkillReadResource:
		return true
	default:
		return false
	}
}

// removedSkillToolResult 为被移除的旧 skill 工具返回明确失败文本。
func removedSkillToolResult(name string) *ToolCallResult {
	return toolCallTextResult(false, strings.TrimSpace(name)+" is no longer available to Codex")
}

// validateHostToolGuards 校验 host-direct memory 工具共享的启用开关和工具名。
// 调用方必须先处理 nil receiver / nil 依赖；这里专注返回统一的 memory 错误类型。
func validateHostToolGuards(enabled, toolsEnabled bool, callName, expectedName string) error {
	if !enabled {
		return contract.NewAgentMemoryError("feature_disabled", contract.ErrFeatureDisabled)
	}
	if !toolsEnabled {
		return contract.NewAgentMemoryError("tools_disabled", contract.ErrFeatureDisabled)
	}
	if callName != expectedName {
		return contract.NewAgentMemoryError("invalid_input", fmt.Errorf("host tools: unknown tool %q", callName))
	}
	return nil
}

// callHostTool 是 routeToolCall 的 host-direct 分支：在调用 hostTools.CallHostTool 之前
// 优先使用请求携带的可信 cwd，缺省时再从 agentID 解析 cwd，打包返回值为 ToolCallResult。
func (h *Handler) callHostTool(ctx context.Context, req ToolCallRequest) (*ToolCallResult, error) {
	started := time.Now()
	outcome := skillmetrics.HostToolOutcomeError
	defer func() {
		skillmetrics.IncHostToolCallOutcome(outcome)
		h.info("toolbridge host-direct tool call",
			"tool", strings.TrimSpace(req.Name),
			"agent_id", strings.TrimSpace(req.AgentID),
			"thread_id", strings.TrimSpace(req.ThreadID),
			"call_id", strings.TrimSpace(req.CallID),
			"outcome", outcome,
			"duration_ms", time.Since(started).Milliseconds(),
		)
	}()
	if err := h.validateHostToolInput(req); err != nil {
		outcome = hostToolErrorOutcome(err)
		return hostToolErrorResult(req, err), nil
	}
	cwd, cwdErr := h.resolveHostToolCWD(ctx, req)
	if cwdErr != nil {
		outcome = hostToolErrorOutcome(cwdErr)
		return hostToolErrorResult(req, cwdErr), nil
	}
	result, err := h.hostTools.CallHostTool(ctx, HostToolCall{
		Name:      req.Name,
		Arguments: req.Arguments,
		CWD:       cwd,
		AgentID:   strings.TrimSpace(req.AgentID),
		ThreadID:  strings.TrimSpace(req.ThreadID),
		TurnID:    strings.TrimSpace(req.TurnID),
		CallID:    strings.TrimSpace(req.CallID),
	})
	if err != nil {
		outcome = hostToolErrorOutcome(err)
		return hostToolErrorResult(req, err), nil
	}
	payload, structured, marshalErr := marshalHostToolResult(result)
	if marshalErr != nil {
		return nil, marshalErr
	}
	success := hostToolStructuredResultSuccess(payload)
	if success {
		outcome = skillmetrics.HostToolOutcomeOK
	}
	return &ToolCallResult{
		Success:           success,
		StructuredContent: structured,
		ContentItems: []ToolCallContentItem{{
			Type: "inputText",
			Text: string(payload),
		}},
	}, nil
}

// marshalHostToolResult 同时生成文本 payload 和 structuredContent，保证 host-direct 返回格式一致。
func marshalHostToolResult(result any) ([]byte, json.RawMessage, error) {
	payload, err := json.Marshal(result)
	if err != nil {
		return nil, nil, err
	}
	structured, err := common.StructuredContentFromRaw(payload)
	if err != nil {
		return nil, nil, err
	}
	return payload, structured, nil
}

// hostToolStructuredResultSuccess 从 host tool 的结构化返回中识别显式失败。
// success=false 与 partial=true 必须向上暴露为失败；degraded 可表示成功返回的降级诊断，不能全局误判。
func hostToolStructuredResultSuccess(payload []byte) bool {
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return true
	}
	if success, ok := hostToolStructuredBool(envelope, "success"); ok && !success {
		return false
	}
	if partial, ok := hostToolStructuredBool(envelope, "partial"); ok && partial {
		return false
	}
	return true
}

func hostToolStructuredBool(envelope map[string]json.RawMessage, key string) (bool, bool) {
	raw, ok := envelope[key]
	if !ok {
		return false, false
	}
	var flag bool
	if err := json.Unmarshal(raw, &flag); err != nil {
		return false, false
	}
	return flag, true
}

// hostToolErrorOutcome 将 host-direct 错误归类到 metrics label。
func hostToolErrorOutcome(err error) string {
	var required contract.SkillApprovalRequiredError
	switch {
	case errors.Is(err, contract.ErrSkillMissingCWD):
		return skillmetrics.HostToolOutcomeCWDMissing
	case errors.As(err, &required):
		return skillmetrics.HostToolOutcomeApprovalRequired
	default:
		return skillmetrics.HostToolOutcomeError
	}
}

// validateHostToolInput 使用 host-direct 对外发布的 InputSchema 在调用 handler 前拦截未知字段。
func (h *Handler) validateHostToolInput(req ToolCallRequest) error {
	if h == nil || h.hostTools == nil {
		return nil
	}
	schema, ok := hostToolInputSchema(h.hostTools.ListHostTools(), req.Name)
	if !ok {
		return nil
	}
	if err := validateToolInputSchema(req.Name, schema, req.Arguments); err != nil {
		return contract.NewAgentMemoryError("invalid_input", err)
	}
	return nil
}

// hostToolInputSchema 查找 host-direct 工具的输入 schema。
func hostToolInputSchema(tools []dto.MCPTool, name string) (json.RawMessage, bool) {
	schemas := toolInputSchemaMap(tools)
	return lookupToolInputSchema(schemas, name)
}

// hostToolErrorResult 将 host-direct 错误包装成模型可读的结构化 ToolCallResult。
// approval_required/denied 需要保留专门 kind，前端据此显示授权交互。
func hostToolErrorResult(req ToolCallRequest, err error) *ToolCallResult {
	if err == nil {
		err = errors.New("host tool failed")
	}
	toolName := strings.TrimSpace(req.Name)
	code, retryable, hint, meta := hostToolErrorEnvelopeFields(req, err)
	kind := "host_tool_error"
	envelope := map[string]any{
		"kind":      kind,
		"tool":      toolName,
		"error":     err.Error(),
		"success":   false,
		"code":      code,
		"retryable": retryable,
		"hint":      hint,
		"meta":      meta,
	}
	var required contract.SkillApprovalRequiredError
	switch {
	case errors.As(err, &required):
		kind = "approval_required"
		envelope["kind"] = kind
		envelope["code"] = "approval_required"
		envelope["hint"] = hostToolErrorHint("approval_required")
		meta["kind"] = kind
		envelope["approval"] = approvalRequestEnvelope(required.Request)
	case isSkillApprovalDenied(err):
		kind = "approval_denied"
		envelope["kind"] = kind
		envelope["code"] = "approval_denied"
		envelope["hint"] = hostToolErrorHint("approval_denied")
		meta["kind"] = kind
	}
	payload, marshalErr := json.Marshal(envelope)
	var structured json.RawMessage
	if marshalErr != nil {
		payload = []byte(err.Error())
	} else {
		structured = json.RawMessage(append([]byte(nil), payload...))
	}
	return &ToolCallResult{
		Success:           false,
		StructuredContent: structured,
		ContentItems: []ToolCallContentItem{{
			Type: "inputText",
			Text: string(payload),
		}},
	}
}

// hostToolErrorEnvelopeFields 生成 host-direct 错误兼容期新增的 ToolErrorEnvelope 字段。
func hostToolErrorEnvelopeFields(req ToolCallRequest, err error) (string, bool, string, map[string]any) {
	code, retryable, hint, classifiedMeta := common.ClassifyToolError(req.Name, err)
	if stableCode := contract.AgentMemoryErrorCode(err); stableCode != "" {
		code = stableCode
		retryable = false
	}
	code = firstNonEmptyHostToolString(code, "host_tool_error")
	if strings.TrimSpace(hint) == "" {
		hint = hostToolErrorHint(code)
	}
	meta := hostToolErrorMeta(req, classifiedMeta)
	meta["kind"] = "host_tool_error"
	return code, retryable, hint, meta
}

// hostToolErrorMeta 只记录工具调用身份，不把参数或敏感路径写入错误 envelope。
func hostToolErrorMeta(req ToolCallRequest, classified map[string]any) map[string]any {
	meta := map[string]any{"tool": strings.TrimSpace(req.Name)}
	for key, value := range classified {
		if strings.TrimSpace(key) != "" {
			meta[key] = value
		}
	}
	addHostToolMetaString(meta, "agent_id", req.AgentID)
	addHostToolMetaString(meta, "thread_id", req.ThreadID)
	addHostToolMetaString(meta, "call_id", req.CallID)
	return meta
}

func addHostToolMetaString(meta map[string]any, key, value string) {
	if value = strings.TrimSpace(value); value != "" {
		meta[key] = value
	}
}

func firstNonEmptyHostToolString(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

// hostToolErrorHint 为 host-direct 错误 code 生成模型可执行的下一步提示。
func hostToolErrorHint(code string) string {
	switch strings.TrimSpace(code) {
	case "invalid_input":
		return "next: check the host tool schema and retry with documented field names and JSON value types"
	case "missing_thread_id":
		return "next: retry from a bound current thread so trusted thread metadata is available"
	case "history_unavailable", "reader_unavailable", "writer_unavailable", "trace_unavailable":
		return "next: retry after the host-direct tool registry is configured"
	case "history_read_failed":
		return "next: verify the current thread exists and retry with a smaller bounded page if needed"
	case "feature_disabled", "tools_disabled":
		return "next: enable the host-direct tool feature before retrying"
	case "approval_required":
		return "next: wait for explicit approval before retrying the same host tool call"
	case "approval_denied":
		return "next: change the request or ask the user for approval before retrying"
	default:
		return "next: inspect the host tool error and retry only after the local issue is fixed"
	}
}

// skillApprovalDeniedMarker 是 approval denial 错误的窄接口标记。
type skillApprovalDeniedMarker interface {
	SkillApprovalDenied() bool
}

// isSkillApprovalDenied 判断错误链中是否带有 approval denied 标记。
func isSkillApprovalDenied(err error) bool {
	var marker skillApprovalDeniedMarker
	return errors.As(err, &marker) && marker.SkillApprovalDenied()
}

// approvalRequestEnvelope 生成传给模型和前端的授权请求摘要。
func approvalRequestEnvelope(req contract.ApprovalRequest) map[string]any {
	return map[string]any{
		"callId":       strings.TrimSpace(req.CallID),
		"approvalId":   strings.TrimSpace(req.ApprovalID),
		"toolName":     strings.TrimSpace(req.ToolName),
		"agentId":      strings.TrimSpace(req.AgentID),
		"threadId":     strings.TrimSpace(req.ThreadID),
		"turnId":       strings.TrimSpace(req.TurnID),
		"reason":       strings.TrimSpace(req.Reason),
		"kind":         strings.TrimSpace(req.Kind),
		"sourceMethod": strings.TrimSpace(req.SourceMethod),
		"payload":      req.Payload,
	}
}

// resolveHostToolCWD 根据 registry 策略决定是否必须解析 cwd。
func (h *Handler) resolveHostToolCWD(ctx context.Context, req ToolCallRequest) (string, error) {
	if hostToolRequiresCWD(h.hostTools, req.Name) {
		return h.resolveRequiredHostToolCWD(ctx, req)
	}
	return normalizeToolCallCWD(req.CWD), nil
}

// hostToolRequiresCWD 默认要求 cwd；只有实现 HostToolCWDPolicy 的 registry 可以显式豁免。
func hostToolRequiresCWD(registry HostToolRegistry, name string) bool {
	policy, ok := registry.(HostToolCWDPolicy)
	return !ok || policy.RequiresCWD(name)
}

// resolveRequiredHostToolCWD 解析并校验 host-direct 工具所需 cwd。
// 请求未携带 cwd 时才通过 agentID 反查；最终必须存在且是目录。
func (h *Handler) resolveRequiredHostToolCWD(ctx context.Context, req ToolCallRequest) (string, error) {
	cwd := normalizeToolCallCWD(req.CWD)
	if cwd == "" {
		var err error
		cwd, err = h.resolveAgentCWD(ctx, req.AgentID)
		if err != nil {
			return "", err
		}
	}
	if cwd == "" {
		return "", fmt.Errorf("%w: host tool cwd is required", contract.ErrSkillMissingCWD)
	}
	if info, err := os.Stat(cwd); err != nil {
		return "", fmt.Errorf("%w: host tool cwd stat %q: %v", contract.ErrSkillMissingCWD, cwd, err)
	} else if !info.IsDir() {
		return "", fmt.Errorf("%w: host tool cwd is not a directory: %q", contract.ErrSkillMissingCWD, cwd)
	}
	return cwd, nil
}

// resolveAgentCWD 通过 WorkDirResolver 反查 agent cwd，并把所有失败折叠为 cwd 缺失错误。
func (h *Handler) resolveAgentCWD(ctx context.Context, agentID string) (string, error) {
	if h == nil || h.resolver == nil {
		return "", fmt.Errorf("%w: work dir resolver is not configured", contract.ErrSkillMissingCWD)
	}
	cwd, err := h.resolver.ResolveAgentCWD(ctx, agentID)
	if err != nil {
		return "", fmt.Errorf("%w: resolve agent cwd: %v", contract.ErrSkillMissingCWD, err)
	}
	return normalizeToolCallCWD(cwd), nil
}
