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
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/mcpcontrol"
)

// peerReadyTimeout is the max time to wait for a peer to register after startup.
const peerReadyTimeout = 10 * time.Second
const peerPollInterval = 300 * time.Millisecond

func (h *Handler) listPeerTools(ctx context.Context, clientKind string) ([]mcpdto.MCPTool, error) {
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

func toCodexDynamicTools(tools []mcpdto.MCPTool) []contract.DynamicToolSchema {
	out := make([]contract.DynamicToolSchema, 0, len(tools))
	for _, tool := range tools {
		schema := contract.DynamicToolSchema{
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
		TurnID:     firstString(payload, "turnId", "turn_id"),
		CallID:     firstString(payload, "callId", "call_id"),
		CWD:        firstString(payload, "_cwd"),
		ClientKind: firstString(payload, "clientKind", "client_kind", "family"),
	}
	if req.Name == "" {
		req.Name = nestedString(payload, "item", "name", "tool", "toolName")
	}
	if req.ThreadID == "" {
		req.ThreadID = nestedString(payload, "thread", "id")
	}
	if req.TurnID == "" {
		req.TurnID = nestedString(payload, "turn", "id")
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

func (h *Handler) resolveCurrentToolCallCWD(ctx context.Context, req ToolCallRequest) string {
	if cwd := strings.TrimSpace(req.CWD); cwd != "" {
		return cwd
	}
	if binding, ok := h.resolveCurrentToolCallBinding(ctx, req); ok {
		return strings.TrimSpace(binding.CWD)
	}
	return ""
}

func (h *Handler) resolveAndWarnCurrentToolCallCWD(ctx context.Context, req ToolCallRequest) string {
	cwd := h.resolveCurrentToolCallCWD(ctx, req)
	h.warnPeerToolCWDTrace(ctx, req, cwd)
	return cwd
}

func shouldWarnToolCWDTrace(toolName string) bool {
	toolName = strings.TrimSpace(toolName)
	return strings.HasPrefix(toolName, "lsp_") ||
		strings.HasPrefix(toolName, "code_") ||
		toolName == "orchestration_launch_agent"
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
