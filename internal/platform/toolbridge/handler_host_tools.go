package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

// Peer-list/decode helpers live in handler_peer_decode.go.

type peerToolsListOutcome struct {
	clientKind string
	tools      []dto.MCPTool
	err        error
}

// listPeerToolsForCodex 为codex列出peer工具。
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
	for index, kind := range kinds {
		go func() {
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
		}()
	}
	out := make([]peerToolsListOutcome, len(kinds))
	for range kinds {
		result := <-ch
		out[result.index] = result.outcome
	}
	return out
}

func joinPeerToolErrors(outcomes []peerToolsListOutcome) error {
	errs := make([]error, 0, len(outcomes))
	for _, outcome := range outcomes {
		if outcome.err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", outcome.clientKind, outcome.err))
		}
	}
	return errors.Join(errs...)
}

// ListToolsForCodex 为codex列出工具。
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
	for _, outcome := range outcomes {
		merged = h.appendDynamicToolsWithShadowWarning(merged, seenToolSources, outcome.clientKind, outcome.tools)
	}
	if len(merged) == 0 {
		return nil, ErrNoPeerAvailable
	}
	return toCodexDynamicTools(merged), nil
}

func (h *Handler) appendDynamicToolsWithShadowWarning(dst []dto.MCPTool, seen map[string]string, source string, tools []dto.MCPTool) []dto.MCPTool {
	return h.appendMCPToolsWithShadowWarning(dst, seen, source, tools)
}

// appendMCPToolsWithShadowWarning 追加带shadowwarning的MCP工具。
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

func isReservedHostOnlyToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case ToolNameMemoryRead, ToolNameMemoryWrite, ToolNameObservabilityTraceGet:
		return true
	default:
		return false
	}
}

func reservedHostOnlyToolCanonicalName(name string) (string, bool) {
	return reservedHostOnlyToolCanonicalNameForFamily("", name)
}

func reservedHostOnlySurfaceToolCanonicalName(family, name string) (string, bool) {
	return reservedHostOnlyToolCanonicalNameForFamily(family, name)
}

func reservedHostOnlyToolCanonicalNameForFamily(family, name string) (string, bool) {
	for _, candidate := range reservedHostOnlyToolNameCandidates(family, name) {
		candidate = strings.TrimSpace(candidate)
		if isReservedHostOnlyToolName(candidate) {
			return candidate, true
		}
	}
	return "", false
}

func reservedHostOnlyToolNameCandidates(family, name string) []string {
	candidates := []string{strings.TrimSpace(name)}
	if family = strings.TrimSpace(family); family != "" {
		candidates = append(candidates, canonicalCodexToolName(family, name))
	}
	if wrappedFamily, inner := mcpWrappedToolName(name); wrappedFamily != "" {
		candidates = append(candidates, strings.TrimSpace(inner), canonicalCodexToolName(wrappedFamily, inner))
	}
	candidates = append(candidates, canonicalToolName(name))
	return candidates
}

func isRemovedSkillToolName(name string) bool {
	switch strings.TrimSpace(name) {
	case ToolNameReadSection, ToolNameLegacySkillExpandBody, ToolNameLegacySkillReadResource:
		return true
	default:
		return false
	}
}

func removedSkillToolResult(name string) *ToolCallResult {
	return toolCallTextResult(false, strings.TrimSpace(name)+" is no longer available to Codex")
}

// validateHostToolGuards checks the common pre-conditions shared by all
// host-direct memory tool implementations (enabled, toolsEnabled, tool name
// match). The caller is responsible for nil-receiver / nil-dependency
// checks before calling this function.
func validateHostToolGuards(enabled, toolsEnabled bool, callName, expectedName, unavailableCode string) error {
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
	outcome = skillmetrics.HostToolOutcomeOK
	return &ToolCallResult{
		Success:           true,
		StructuredContent: structured,
		ContentItems: []ToolCallContentItem{{
			Type: "inputText",
			Text: string(payload),
		}},
	}, nil
}

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

// hostToolErrorResult 生成host工具错误结果。
func hostToolErrorResult(req ToolCallRequest, err error) *ToolCallResult {
	envelope := map[string]any{
		"kind":  "host_tool_error",
		"tool":  strings.TrimSpace(req.Name),
		"error": err.Error(),
	}
	if code := contract.AgentMemoryErrorCode(err); code != "" {
		envelope["code"] = code
	}
	var required contract.SkillApprovalRequiredError
	switch {
	case errors.As(err, &required):
		envelope["kind"] = "approval_required"
		envelope["approval"] = approvalRequestEnvelope(required.Request)
	case isSkillApprovalDenied(err):
		envelope["kind"] = "approval_denied"
	}
	payload, marshalErr := json.Marshal(envelope)
	structured := json.RawMessage(append([]byte(nil), payload...))
	if marshalErr != nil {
		payload = []byte(err.Error())
		structured = nil
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

type skillApprovalDeniedMarker interface {
	SkillApprovalDenied() bool
}

func isSkillApprovalDenied(err error) bool {
	var marker skillApprovalDeniedMarker
	return errors.As(err, &marker) && marker.SkillApprovalDenied()
}

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

func (h *Handler) resolveHostToolCWD(ctx context.Context, req ToolCallRequest) (string, error) {
	if hostToolRequiresCWD(h.hostTools, req.Name) {
		return h.resolveRequiredHostToolCWD(ctx, req)
	}
	return normalizeToolCallCWD(req.CWD), nil
}

func hostToolRequiresCWD(registry HostToolRegistry, name string) bool {
	policy, ok := registry.(HostToolCWDPolicy)
	return !ok || policy.RequiresCWD(name)
}

// resolveRequiredHostToolCWD 解析必需host工具工作目录。
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
		return "", fmt.Errorf("%w: host tool cwd stat %q: %w", contract.ErrSkillMissingCWD, cwd, err)
	} else if !info.IsDir() {
		return "", fmt.Errorf("%w: host tool cwd is not a directory: %q", contract.ErrSkillMissingCWD, cwd)
	}
	return cwd, nil
}

func (h *Handler) resolveAgentCWD(ctx context.Context, agentID string) (string, error) {
	if h == nil || h.resolver == nil {
		return "", fmt.Errorf("%w: work dir resolver is not configured", contract.ErrSkillMissingCWD)
	}
	cwd, err := h.resolver.ResolveAgentCWD(ctx, agentID)
	if err != nil {
		return "", fmt.Errorf("%w: resolve agent cwd: %w", contract.ErrSkillMissingCWD, err)
	}
	return normalizeToolCallCWD(cwd), nil
}
