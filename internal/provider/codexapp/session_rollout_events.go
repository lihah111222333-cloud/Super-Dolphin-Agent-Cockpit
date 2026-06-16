package codexapp

import (
	"strings"
	"time"

	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/eventcore"
	tooldto "github.com/anthropic-ai/super-agent-v3/internal/dto/tool"
	shared "github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/codexapp/resultguard"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/runtimeconfig"
)

// translateCodexRolloutToolEvent 处理translatecodexrollout工具事件。
func translateCodexRolloutToolEvent(eventType string, payload map[string]any) (any, bool) {
	switch strings.TrimSpace(eventType) {
	case "response_item", "item/started", "item_started", "agent/event/item_started":
		if ev, ok := translateCodexFunctionCallBegin(payload); ok {
			return ev, true
		}
		if ev, ok := translateCodexMCPToolCallEnd(payload); ok {
			return ev, true
		}
		return translateCodexFunctionCallOutputEnd(payload)
	case "event_msg", "item/completed", "item_completed", "agent/event/item_completed", "rawResponseItem/completed":
		if ev, ok := translateCodexMCPToolCallEnd(payload); ok {
			return ev, true
		}
		return translateCodexFunctionCallOutputEnd(payload)
	default:
		return nil, false
	}
}

func translateCodexFunctionCallBegin(payload map[string]any) (any, bool) {
	item := codexToolItemPayload(payload)
	if !isCodexFunctionCallItem(item) {
		return nil, false
	}
	header := buildCodexRolloutToolCallHeader(payload, item)
	if header.CallID == "" || header.ToolName == "" {
		return nil, false
	}
	return tooldto.ToolCallBegin{
		ToolCallHeader:   header,
		ArgumentsPreview: codexFunctionCallArgumentsPreview(item),
	}, true
}

func translateCodexMCPToolCallEnd(payload map[string]any) (any, bool) {
	item := codexToolItemPayload(payload)
	switch strings.TrimSpace(stringValue(item, "type")) {
	case "mcp_tool_call_end", "tool_result":
	default:
		return nil, false
	}
	header := buildCodexRolloutToolCallHeader(payload, item)
	if header.CallID == "" || header.ToolName == "" {
		return nil, false
	}
	success, errorText := codexMCPToolResultOutcome(item)
	preview := resultguard.ApplyCodexMCPPreview(success, errorText, codexMCPToolResultPreview(item), codexRolloutToolName(item), item)
	result := captureCodexRolloutToolResult(header, eventTime(payload), preview)
	return tooldto.ToolCallEnd{
		ToolCallHeader: header,
		Success:        success,
		Error:          errorText,
		Result:         result.Preview,
		PersistedPath:  result.PersistedPath,
		Truncated:      result.Truncated,
		OriginalSize:   result.OriginalSize,
		ElapsedMS:      codexMCPToolElapsedMS(item),
	}, true
}

func translateCodexFunctionCallOutputEnd(payload map[string]any) (any, bool) {
	item := codexToolItemPayload(payload)
	if strings.TrimSpace(stringValue(item, "type")) != "function_call_output" {
		return nil, false
	}
	header := buildCodexRolloutToolCallHeader(payload, item)
	if header.CallID == "" || header.ToolName == "" {
		return nil, false
	}
	success, errorText := codexFunctionCallOutputOutcome(item)
	return tooldto.ToolCallEnd{
		ToolCallHeader: header,
		Success:        success,
		Error:          errorText,
		Result:         stringValue(item, "output"),
	}, true
}

func codexToolItemPayload(payload map[string]any) map[string]any {
	if item := nestedValue(payload, "item"); len(item) > 0 {
		return item
	}
	if nested := nestedValue(payload, "payload"); len(nested) > 0 {
		return nested
	}
	return payload
}

func isCodexFunctionCallItem(item map[string]any) bool {
	switch strings.TrimSpace(stringValue(item, "type")) {
	case "function_call", "tool_call":
		return stringValue(item, "name", "toolName", "tool_name", "tool") != ""
	default:
		return false
	}
}

func buildCodexRolloutToolCallHeader(payload, item map[string]any) shareddto.ToolCallHeader {
	threadID := shared.FirstNonEmpty(payloadAgentID(payload), payloadThreadID(payload), payloadAgentID(item), payloadThreadID(item))
	agentID := shared.FirstNonEmpty(payloadAgentID(payload), payloadAgentID(item), threadID)
	return shareddto.ToolCallHeader{
		TurnHeader: shareddto.TurnHeader{
			AgentHeader: shareddto.AgentHeader{
				ThreadHeader: shareddto.ThreadHeader{
					EventHeader: shareddto.EventHeader{Timestamp: eventTime(payload)},
					ThreadID:    threadID,
				},
				AgentID: agentID,
			},
			TurnIDHeader: shareddto.TurnIDHeader{
				TurnID: shared.FirstNonEmpty(payloadTurnID(payload), payloadTurnID(item)),
			},
		},
		CallID:   shared.FirstNonEmpty(payloadCallID(payload), payloadCallID(item)),
		ToolName: codexRolloutToolName(item),
	}
}

// codexRolloutToolName 处理codexrollout工具名称。
func codexRolloutToolName(item map[string]any) string {
	if toolName := stringValue(item, "toolName", "tool_name"); toolName != "" {
		return toolName
	}
	if invocation := nestedValue(item, "invocation"); len(invocation) > 0 {
		if tool := stringValue(invocation, "tool", "name", "toolName", "tool_name"); tool != "" {
			if server := stringValue(invocation, "server"); server != "" {
				return "mcp__" + server + "__" + tool
			}
			return tool
		}
	}
	name := payloadToolName(item)
	if name == "" {
		return ""
	}
	if namespace := stringValue(item, "namespace"); namespace != "" {
		return strings.TrimSuffix(namespace, "__") + "__" + name
	}
	return name
}

// codexFunctionCallArgumentsPreview 处理codex函数callargumentspreview。
func codexFunctionCallArgumentsPreview(item map[string]any) string {
	for _, key := range []string{"arguments", "args"} {
		if value, ok := item[key]; ok && value != nil {
			if text, ok := value.(string); ok {
				return strings.TrimSpace(text)
			}
			return previewAny(value)
		}
	}
	return ""
}

// codexMCPToolResultOutcome 处理codexMCP工具结果outcome。
func codexMCPToolResultOutcome(item map[string]any) (bool, string) {
	result := nestedValue(item, "result")
	if len(result) == 0 {
		return true, ""
	}
	if errText := codexMCPToolErrorText(result); errText != "" {
		return false, errText
	}
	resultPayload := codexMCPToolResultPayload(result)
	success := !boolValue(resultPayload, "isError", "is_error")
	if structured := resultPayload["structuredContent"]; structured != nil {
		if resultSuccess, resultError, ok := toolEventResultOutcome(structured); ok && !resultSuccess {
			success = false
			if errText := strings.TrimSpace(resultError); errText != "" {
				return false, errText
			}
		}
	}
	if success {
		return resultguard.SuccessUnlessEmptyCodexMCPFileRead(codexRolloutToolName(item), item)
	}
	return false, shared.FirstNonEmpty(codexMCPToolResultContentText(resultPayload), "tool call failed")
}

// codexMCPToolErrorText 处理codexMCP工具错误文本。
func codexMCPToolErrorText(result map[string]any) string {
	for _, key := range []string{"Err", "err", "Error", "error"} {
		value := result[key]
		switch typed := value.(type) {
		case string:
			if text := strings.TrimSpace(typed); text != "" {
				return text
			}
		case map[string]any:
			if text := shared.FirstNonEmpty(stringValue(typed, "message", "error", "reason"), jsonPreview(typed)); text != "" {
				return text
			}
		default:
			if value != nil {
				if text := previewAny(value); text != "" && text != "null" {
					return text
				}
			}
		}
	}
	return ""
}

func codexMCPToolResultPreview(item map[string]any) string {
	result := nestedValue(item, "result")
	resultPayload := codexMCPToolResultPayload(result)
	if structured := resultPayload["structuredContent"]; structured != nil {
		if text := previewAny(structured); text != "" {
			return text
		}
	}
	if text := codexMCPToolResultContentText(resultPayload); text != "" {
		return text
	}
	return jsonPreview(item, "result")
}

func codexMCPToolResultPayload(result map[string]any) map[string]any {
	if okPayload := nestedValue(result, "Ok"); len(okPayload) > 0 {
		return okPayload
	}
	if okPayload := nestedValue(result, "ok"); len(okPayload) > 0 {
		return okPayload
	}
	return result
}

func codexMCPToolResultContentText(okPayload map[string]any) string {
	content, _ := okPayload["content"].([]any)
	for _, raw := range content {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if text := stringValue(item, "text"); text != "" {
			return text
		}
	}
	return ""
}

func codexMCPToolElapsedMS(item map[string]any) int64 {
	duration := nestedValue(item, "duration")
	if len(duration) == 0 {
		return int64Value(item, "elapsedMs", "elapsed_ms")
	}
	secs := int64Value(duration, "secs", "seconds")
	nanos := int64Value(duration, "nanos", "nanoseconds")
	return secs*1000 + nanos/1_000_000
}

func codexFunctionCallOutputOutcome(item map[string]any) (bool, string) {
	output := stringValue(item, "output")
	if output == "" {
		return true, ""
	}
	if strings.Contains(strings.ToLower(output), "\"success\":false") ||
		strings.Contains(strings.ToLower(output), "\"iserror\":true") {
		return false, output
	}
	return true, ""
}

func captureCodexRolloutToolResult(header shareddto.ToolCallHeader, timestamp time.Time, preview string) providershared.ToolResultRecord {
	result := providershared.CaptureToolResult(providershared.ToolResultMeta{
		ThreadID:  header.ThreadID,
		TurnID:    header.TurnID,
		CallID:    header.CallID,
		ToolName:  header.ToolName,
		Timestamp: timestamp,
	}, preview)
	if result.Preview == "" && result.PersistedPath == "" && !result.Truncated && result.OriginalSize == 0 {
		result.Preview = preview
	}
	return result
}
