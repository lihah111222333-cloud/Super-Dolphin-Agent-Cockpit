package toolbridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	"github.com/anthropic-ai/super-agent-v3/pkg/skillmetrics"
)

// Peer-list/decode helpers live in handler_peer_decode.go.

type peerToolsListOutcome struct {
	clientKind string
	tools      []common.MCPTool
	err        error
}

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

func (h *Handler) ListToolsForCodex(ctx context.Context) ([]codexprotocol.DynamicToolSchema, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	// Host-direct 工具优先加入列表：dedup 遵守“先加入者胜出”原则，同名 peer 工具会被忽略。
	// 这保证调用阶段 hostTools.HasTool 命中与列表阶段优先级一致。
	var hostTools []common.MCPTool
	if h != nil && h.hostTools != nil {
		hostTools = h.hostTools.ListHostTools()
	}
	seenToolSources := make(map[string]string, len(hostTools))
	merged := h.appendDynamicToolsWithShadowWarning(nil, seenToolSources, "host", hostTools)
	peerSucceeded := false
	outcomes := h.listPeerToolsForCodex(ctx, dto.ClientKindOrch, dto.ClientKindLSP)
	for _, outcome := range outcomes {
		if outcome.err != nil {
			if h != nil {
				h.warn("toolbridge dynamic tools peer degraded", "client_kind", outcome.clientKind, "error", outcome.err)
			}
			continue
		}
		peerSucceeded = true
		merged = h.appendDynamicToolsWithShadowWarning(merged, seenToolSources, outcome.clientKind, outcome.tools)
	}
	if len(merged) == 0 && !peerSucceeded {
		if err := joinPeerToolErrors(outcomes); err != nil {
			return nil, fmt.Errorf("toolbridge: no dynamic tools available: %w", err)
		}
		return nil, ErrNoPeerAvailable
	}
	return toCodexDynamicTools(merged), nil
}

func (h *Handler) appendDynamicToolsWithShadowWarning(dst []common.MCPTool, seen map[string]string, source string, tools []common.MCPTool) []common.MCPTool {
	if seen == nil {
		seen = make(map[string]string, len(tools))
	}
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name == "" {
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
		seen[name] = source
		dst = append(dst, tool)
	}
	return dst
}

// dedupToolsByName 按 name 去重，保留首次出现的入口（所以调用时要把 host-direct
// 放在列表最前）。补 host_tools 的 dedup 优先级语义。
func dedupToolsByName(in []common.MCPTool) []common.MCPTool {
	seen := make(map[string]struct{}, len(in))
	out := make([]common.MCPTool, 0, len(in))
	for _, t := range in {
		if _, ok := seen[t.Name]; ok {
			continue
		}
		seen[t.Name] = struct{}{}
		out = append(out, t)
	}
	return out
}

// callHostTool 是 routeToolCall 的 host-direct 分支：在调用 hostTools.CallHostTool 之前
// 从 agentID 解析 cwd，打包返回值为 ToolCallResult，路径上与 peer 分支对齐。
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
	cwd := h.resolveAgentCWD(ctx, req.AgentID)
	if strings.TrimSpace(cwd) == "" {
		h.warn("toolbridge host-direct cwd missing before call",
			"tool", strings.TrimSpace(req.Name),
			"agent_id", strings.TrimSpace(req.AgentID),
			"thread_id", strings.TrimSpace(req.ThreadID),
			"call_id", strings.TrimSpace(req.CallID),
		)
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
	payload, mErr := json.Marshal(sanitizeHostToolResult(result, cwd))
	if mErr != nil {
		return nil, mErr
	}
	outcome = skillmetrics.HostToolOutcomeOK
	return &ToolCallResult{
		Success: true,
		ContentItems: []ToolCallContentItem{{
			Type: "inputText",
			Text: string(payload),
		}},
	}, nil
}

func sanitizeHostToolResult(result any, cwd string) any {
	switch typed := result.(type) {
	case skillpkg.ExpandBodyResult:
		typed.Path = hostToolRelativePath(cwd, typed.Path)
		return typed
	case skillpkg.ReadResourceResult:
		typed.SkillDir = hostToolRelativePath(cwd, typed.SkillDir)
		return typed
	default:
		return result
	}
}

func hostToolRelativePath(cwd, path string) string {
	path = strings.TrimSpace(path)
	if path == "" || !filepath.IsAbs(path) {
		return path
	}
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return filepath.Base(path)
	}
	base, err := filepath.EvalSymlinks(filepath.Clean(cwd))
	if err != nil {
		base = filepath.Clean(cwd)
	}
	target, err := filepath.EvalSymlinks(filepath.Clean(path))
	if err != nil {
		target = filepath.Clean(path)
	}
	rel, err := filepath.Rel(base, target)
	if err != nil || rel == "." || rel == "" || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == ".." || filepath.IsAbs(rel) {
		return filepath.Base(path)
	}
	return filepath.ToSlash(rel)
}

func hostToolErrorOutcome(err error) string {
	var required skillpkg.SkillApprovalRequiredError
	switch {
	case errors.Is(err, skillpkg.ErrMissingCWD):
		return skillmetrics.HostToolOutcomeCWDMissing
	case errors.As(err, &required):
		return skillmetrics.HostToolOutcomeApprovalRequired
	default:
		return skillmetrics.HostToolOutcomeError
	}
}

func hostToolErrorResult(req ToolCallRequest, err error) *ToolCallResult {
	envelope := map[string]any{
		"kind":  "host_tool_error",
		"tool":  strings.TrimSpace(req.Name),
		"error": err.Error(),
	}
	var required skillpkg.SkillApprovalRequiredError
	switch {
	case errors.As(err, &required):
		envelope["kind"] = "approval_required"
		envelope["approval"] = approvalRequestEnvelope(required.Request)
	case isSkillApprovalDenied(err):
		envelope["kind"] = "approval_denied"
	}
	payload, marshalErr := json.Marshal(envelope)
	if marshalErr != nil {
		payload = []byte(err.Error())
	}
	return &ToolCallResult{
		Success: false,
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

// resolveAgentCWD 包装 WorkDirResolver 调用，失败时返回空串（下游 service 会返 ErrMissingCWD，
// 该错误会被 callHostTool 打包成带说明的失败 ToolCallResult）。resolver 为 nil 同理空串。
func (h *Handler) resolveAgentCWD(ctx context.Context, agentID string) string {
	if h == nil || h.resolver == nil {
		return ""
	}
	cwd, err := h.resolver.ResolveAgentCWD(ctx, agentID)
	if err != nil {
		return ""
	}
	return cwd
}
