package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

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

// setDynamicToolDeferLoading 设置dynamic工具deferloading。
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
