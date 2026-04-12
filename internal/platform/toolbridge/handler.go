package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/mcpserver/common"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/difftracker"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type Handler struct {
	registry     activePeerRegistry
	emitter      difftracker.DiffEmitter
	resolver     difftracker.WorkDirResolver
	diffFallback *diffFallbackTracker
	bindingStore bindingstore.Store
	logger       *pkglogger.Logger
}

type activePeerRegistry interface {
	FindActiveByKind(clientKind string) []*mcpcontrol.ToolInstance
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

func (h *Handler) ListToolsForCodex(ctx context.Context) ([]codexapp.DynamicToolSchema, error) {
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

func toCodexDynamicTools(tools []common.MCPTool) []codexapp.DynamicToolSchema {
	out := make([]codexapp.DynamicToolSchema, 0, len(tools))
	for _, tool := range tools {
		schema := codexapp.DynamicToolSchema{
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

func setDynamicToolDeferLoading(schema *codexapp.DynamicToolSchema, enabled bool) {
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
