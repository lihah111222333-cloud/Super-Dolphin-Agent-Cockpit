package codexapp

import (
	"strings"
	"time"

	shareddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	tooldto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/tool"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/codexapp/resultguard"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
)

// translateCodexRolloutToolEvent 把 Codex rollout tool 事件转换为内部 tool DTO。
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

// translateCodexMCPToolCallEnd 转换 MCP 工具终态，并显式上报结果捕获错误。
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
	result, captureErr := captureCodexRolloutToolResult(header, eventTime(payload), preview)
	if captureErr != nil {
		success = false
		errorText = appendProviderRuntimeError(errorText, captureErr)
	}
	return tooldto.ToolCallEnd{
		ToolCallHeader: header,
		Success:        success,
		Error:          errorText,
		Result:         result.Preview,
		PersistedPath:  result.PersistedPath,
		PersistFailed:  result.PersistFailed,
		PersistError:   result.PersistError,
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
	result, captureErr := captureCodexRolloutToolResult(header, eventTime(payload), stringValue(item, "output"))
	if captureErr != nil {
		success = false
		errorText = appendProviderRuntimeError(errorText, captureErr)
	}
	return tooldto.ToolCallEnd{
		ToolCallHeader: header,
		Success:        success,
		Error:          errorText,
		Result:         result.Preview,
		PersistedPath:  result.PersistedPath,
		PersistFailed:  result.PersistFailed,
		PersistError:   result.PersistError,
		Truncated:      result.Truncated,
		OriginalSize:   result.OriginalSize,
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

// codexRolloutToolName 从 rollout payload 中提取工具名，MCP 工具会补齐 server 前缀。
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

// codexFunctionCallArgumentsPreview 提取函数调用参数预览，供 UI 和日志展示。
func codexFunctionCallArgumentsPreview(item map[string]any) string {
	for _, key := range []string{"arguments", "args"} {
		if value, ok := item[key]; ok && value != nil {
			if text, ok := value.(string); ok {
				return providershared.SafeToolArgumentsPreviewString(strings.TrimSpace(text))
			}
			return providershared.SafeToolArgumentsPreview(value)
		}
	}
	return ""
}

// codexMCPToolResultOutcome 解析 MCP 工具结果的成功状态和失败文本。
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

// codexMCPToolErrorText 从多种 MCP 错误字段中提取可读错误文本。
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

// captureCodexRolloutToolResult 捕获 rollout/replay 工具结果并保留持久化诊断。
// 运行时依赖缺失或捕获失败必须向 ToolCallEnd 返回显式错误。
func captureCodexRolloutToolResult(header shareddto.ToolCallHeader, timestamp time.Time, preview string) (providershared.ToolResultRecord, error) {
	return providershared.CaptureToolResult(providershared.ToolResultMeta{
		ThreadID:  header.ThreadID,
		TurnID:    header.TurnID,
		CallID:    header.CallID,
		ToolName:  header.ToolName,
		Timestamp: timestamp,
	}, preview)
}
