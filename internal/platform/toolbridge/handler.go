package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	codexprotocol "github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/protocol"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type Handler struct {
	registry     activePeerRegistry
	emitter      difftracker.DiffEmitter
	resolver     difftracker.WorkDirResolver
	diffFallback *diffFallbackTracker
	bindingStore bindingstore.Store
	threadStore  threadRuntimeConfigStore
	cfg          *platformconfig.Config
	logger       *pkglogger.Logger
}

type activePeerRegistry interface {
	FindActiveByKind(clientKind string) []*mcpcontrol.ToolInstance
}

type threadRuntimeConfigStore interface {
	GetByThreadID(ctx context.Context, threadID string) (*threadstore.Thread, error)
}

type storedThreadRuntime struct {
	Runtime map[string]any `json:"runtime,omitempty"`
}

func NewHandler(in handlerIn) *Handler {
	logger := in.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &Handler{
		registry:     in.Registry,
		emitter:      in.Emitter,
		resolver:     in.Resolver,
		diffFallback: in.DiffFallback,
		bindingStore: in.BindingStore,
		threadStore:  in.ThreadStore,
		cfg:          in.Config,
		logger:       logger,
	}
}

func (h *Handler) HandleToolCall(ctx context.Context, msg codexapp.RawMessage) (any, error) {
	req, err := decodeToolCallRequest(msg.Params)
	if err != nil {
		return nil, err
	}
	return h.routeToolCall(ctx, req)
}

func (h *Handler) routeToolCall(ctx context.Context, req ToolCallRequest) (*ToolCallResult, error) {
	if h == nil || h.registry == nil {
		return nil, ErrNoPeerAvailable
	}
	if blocked, err := h.spawnAgentPolicyMessage(ctx, req); err != nil {
		return nil, err
	} else if blocked != "" {
		return &ToolCallResult{
			Success: false,
			ContentItems: []ToolCallContentItem{{
				Type: "inputText",
				Text: blocked,
			}},
		}, nil
	}
	clientKind, err := resolveToolClientKind(req)
	if err != nil {
		return nil, err
	}
	peers := h.registry.FindActiveByKind(clientKind)
	if len(peers) == 0 {
		return nil, ErrNoPeerAvailable
	}
	if len(peers) > 1 {
		return nil, ErrAmbiguousPeer
	}

	callCtx, cancel := platformconfig.WithPeerTimeout(ctx, toolCallTimeout)
	defer cancel()

	peer := peers[0].Peer
	snapshot := h.beginToolDiffSnapshot(ctx, req)

	var resp peerToolCallResponse
	err = peer.Callback(callCtx, "tools/call", map[string]any{
		"name":      req.Name,
		"arguments": req.Arguments,
		"_agentId":  req.AgentID,
		"_threadId": req.ThreadID,
		"_callId":   req.CallID,
	}, &resp)
	if err != nil {
		return &ToolCallResult{
			Success: false,
			ContentItems: []ToolCallContentItem{{
				Type: "inputText",
				Text: err.Error(),
			}},
		}, nil
	}

	result := adaptMCPResponse(resp)
	h.emitToolDiff(ctx, req, snapshot)
	return result, nil
}

func (h *Handler) spawnAgentPolicyMessage(ctx context.Context, req ToolCallRequest) (string, error) {
	if strings.TrimSpace(req.Name) != "spawn_agent" {
		return "", nil
	}
	required, err := h.persistentSubagentRequired(ctx, req)
	if err != nil {
		return "", err
	}
	if !required {
		return "", nil
	}
	return "当前会话启用了 persistent_subagent_default：禁止使用 `spawn_agent` 创建临时子 agent。请改用 `orchestration_launch_agent` 创建持续化 UI 子 agent。", nil
}

func (h *Handler) persistentSubagentRequired(ctx context.Context, req ToolCallRequest) (bool, error) {
	runtime, err := h.requireToolCallRuntimeConfig(ctx, req)
	if err != nil {
		return false, err
	}
	required, present := persistentSubagentFlagFromRuntime(runtime)
	if !present {
		required = h.cfg != nil && h.cfg.Agent.PersistentSubagentDefault
	}
	if !required {
		return false, nil
	}
	managedAvailable, known := runtimeHasTool(runtime, "orchestration_launch_agent")
	if known {
		return managedAvailable, nil
	}
	return true, nil
}

func (h *Handler) requireToolCallRuntimeConfig(ctx context.Context, req ToolCallRequest) (map[string]any, error) {
	threadID, err := h.requireToolCallThreadID(ctx, req)
	if err != nil {
		return nil, err
	}
	return h.requireToolCallRuntime(ctx, threadID)
}

func (h *Handler) toolCallRuntimeConfig(ctx context.Context, req ToolCallRequest) (map[string]any, bool) {
	runtime, err := h.requireToolCallRuntimeConfig(ctx, req)
	if err != nil {
		return nil, false
	}
	return runtime, true
}

func (h *Handler) requireToolCallThreadID(ctx context.Context, req ToolCallRequest) (string, error) {
	threadID, ok := h.resolveToolCallThreadID(ctx, req)
	if !ok {
		return "", contract.ErrThreadRuntimeRequired
	}
	return threadID, nil
}

func (h *Handler) requireToolCallRuntime(ctx context.Context, threadID string) (map[string]any, error) {
	runtime, ok := h.readToolCallRuntime(ctx, threadID)
	if !ok {
		return nil, contract.ErrPersistentSubagentRuntimeRequired
	}
	return runtime, nil
}

func (h *Handler) resolveToolCallThreadID(ctx context.Context, req ToolCallRequest) (string, bool) {
	if threadID, ok := resolveToolCallThreadIDFromRequest(req); ok {
		return threadID, true
	}
	return h.resolveToolCallThreadIDFromAgent(ctx, req)
}

func resolveToolCallThreadIDFromRequest(req ToolCallRequest) (string, bool) {
	threadID := strings.TrimSpace(req.ThreadID)
	if threadID == "" {
		return "", false
	}
	return threadID, true
}

func (h *Handler) resolveToolCallThreadIDFromAgent(ctx context.Context, req ToolCallRequest) (string, bool) {
	if h == nil || h.bindingStore == nil {
		return "", false
	}
	agentID := strings.TrimSpace(req.AgentID)
	if agentID == "" {
		return "", false
	}
	threadID, err := h.bindingStore.GetThreadByAgent(ctx, agentID)
	if err != nil {
		return "", false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", false
	}
	return threadID, true
}

func (h *Handler) readToolCallRuntime(ctx context.Context, threadID string) (map[string]any, bool) {
	if h == nil || h.threadStore == nil {
		return nil, false
	}
	row, err := h.threadStore.GetByThreadID(ctx, threadID)
	if err != nil || row == nil || len(row.ConfigOverride) == 0 {
		return nil, false
	}
	return decodeStoredThreadRuntime(row.ConfigOverride)
}

func decodeStoredThreadRuntime(raw json.RawMessage) (map[string]any, bool) {
	var stored storedThreadRuntime
	if err := json.Unmarshal(raw, &stored); err != nil || len(stored.Runtime) == 0 {
		return nil, false
	}
	return stored.Runtime, true
}

func persistentSubagentFlagFromRuntime(runtime map[string]any) (bool, bool) {
	for _, key := range []string{"sessionFlags", "session_flags"} {
		raw, ok := runtime[key]
		if !ok {
			continue
		}
		flags, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		for name, value := range flags {
			switch normalizeToolbridgeSessionFlagName(name) {
			case "persistentsubagentdefault", "managedsubagentdefault", "uipersistentsubagentdefault":
				boolean, ok := value.(bool)
				if ok {
					return boolean, true
				}
			}
		}
	}
	return false, false
}

func runtimeHasTool(runtime map[string]any, want string) (bool, bool) {
	for _, key := range []string{"enabledTools", "enabled_tools", "tools"} {
		raw, ok := runtime[key]
		if !ok {
			continue
		}
		switch typed := raw.(type) {
		case []string:
			for _, value := range typed {
				if strings.TrimSpace(value) == want {
					return true, true
				}
			}
			return false, true
		case []any:
			for _, value := range typed {
				text, _ := value.(string)
				if strings.TrimSpace(text) == want {
					return true, true
				}
			}
			return false, true
		}
	}
	return false, false
}

func normalizeToolbridgeSessionFlagName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	return replacer.Replace(name)
}

func (h *Handler) ListToolsForCodex(ctx context.Context) ([]codexprotocol.DynamicToolSchema, error) {
	orchTools, err := h.listPeerTools(ctx, dto.ClientKindOrch)
	if err != nil {
		return nil, err
	}
	lspTools, err := h.listPeerTools(ctx, dto.ClientKindLSP)
	if err != nil {
		return nil, err
	}
	merged := append(append([]common.MCPTool(nil), orchTools...), lspTools...)
	return toCodexDynamicTools(merged), nil
}

// peerReadyTimeout is the max time to wait for a peer to register after startup.
const peerReadyTimeout = 10 * time.Second
const peerPollInterval = 300 * time.Millisecond

func (h *Handler) listPeerTools(ctx context.Context, clientKind string) ([]common.MCPTool, error) {
	if h == nil || h.registry == nil {
		return nil, ErrNoPeerAvailable
	}
	// Peer processes may still be starting up. Poll with a short timeout.
	peers, err := h.waitForPeer(ctx, clientKind)
	if err != nil {
		return nil, err
	}

	// Bootstrap peer callback returns {"tools":[...]} wrapper.
	var result peerToolsListResult
	if err := peers[0].Peer.Callback(ctx, "tools/list", nil, &result); err != nil {
		return nil, err
	}
	return result.Tools, nil
}

func (h *Handler) waitForPeer(ctx context.Context, clientKind string) ([]*mcpcontrol.ToolInstance, error) {
	deadline := time.Now().Add(peerReadyTimeout)
	for {
		peers := h.registry.FindActiveByKind(clientKind)
		if len(peers) >= 1 {
			// Use the first active peer. Multiple peers can exist when the
			// codex app-server also spawns MCP sidecars from a resumed thread.
			return peers[:1], nil
		}
		if time.Now().After(deadline) {
			return nil, ErrNoPeerAvailable
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(peerPollInterval):
		}
	}
}

func adaptMCPResponse(resp peerToolCallResponse) *ToolCallResult {
	items := make([]ToolCallContentItem, 0, len(resp.Content))
	for _, item := range resp.Content {
		items = append(items, ToolCallContentItem{
			Type: "inputText",
			Text: strings.TrimSpace(item.Text),
		})
	}
	return &ToolCallResult{ContentItems: items, Success: true}
}

func toCodexDynamicTools(tools []common.MCPTool) []codexprotocol.DynamicToolSchema {
	out := make([]codexprotocol.DynamicToolSchema, 0, len(tools))
	for _, tool := range tools {
		schema := codexprotocol.DynamicToolSchema{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: tool.InputSchema,
		}
		setDynamicToolDeferLoading(&schema, toolDeferLoading(tool))
		out = append(out, schema)
	}
	return out
}

func decodeToolCallRequest(params json.RawMessage) (ToolCallRequest, error) {
	if len(bytes.TrimSpace(params)) == 0 {
		return ToolCallRequest{}, fmt.Errorf("toolbridge: missing tool call params")
	}

	var payload map[string]json.RawMessage
	if err := json.Unmarshal(params, &payload); err != nil {
		return ToolCallRequest{}, fmt.Errorf("toolbridge: decode tool call params: %w", err)
	}

	req := ToolCallRequest{
		Name:       firstString(payload, "name", "tool", "toolName", "tool_name"),
		Arguments:  firstRaw(payload, "arguments", "args"),
		AgentID:    firstString(payload, "agentId", "agent_id"),
		ThreadID:   firstString(payload, "threadId", "thread_id"),
		CallID:     firstString(payload, "callId", "call_id"),
		ClientKind: firstString(payload, "clientKind", "client_kind", "family"),
	}
	if req.Name == "" {
		req.Name = nestedString(payload, "item", "name", "tool", "toolName")
	}
	if req.ThreadID == "" {
		req.ThreadID = nestedString(payload, "thread", "id")
	}
	if req.CallID == "" {
		req.CallID = nestedString(payload, "item", "callId", "call_id")
	}
	if len(bytes.TrimSpace(req.Arguments)) == 0 {
		req.Arguments = json.RawMessage(`{}`)
	}
	if strings.TrimSpace(req.Name) == "" {
		return ToolCallRequest{}, fmt.Errorf("toolbridge: missing tool name")
	}
	return req, nil
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

func toolDeferLoading(tool common.MCPTool) bool {
	value := reflect.ValueOf(tool)
	field := value.FieldByName("DeferLoading")
	return field.IsValid() && field.Kind() == reflect.Bool && field.Bool()
}

func setDynamicToolDeferLoading(schema *codexprotocol.DynamicToolSchema, enabled bool) {
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

func lspEditAction(arguments json.RawMessage) string {
	if len(bytes.TrimSpace(arguments)) == 0 {
		return ""
	}
	var payload struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(arguments, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Action)
}

func (h *Handler) warn(msg string, args ...any) {
	logger := h.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	logger.Warn(msg, args...)
}

func (h *Handler) debug(msg string, args ...any) {
	logger := h.logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	logger.Debug(msg, args...)
}
