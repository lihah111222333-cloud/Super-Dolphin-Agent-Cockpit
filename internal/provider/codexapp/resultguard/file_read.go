package resultguard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// EmptyFileReadFromRaw 从原始 tool 参数中识别空文件读取结果。
// 该入口服务 Codex MCP 原始事件，解析失败时返回 ok=false，让上游保留原结果。
func EmptyFileReadFromRaw(toolName string, params json.RawMessage, result any) (string, bool) {
	return EmptyFileRead(toolName, ArgsFromRaw(params), result)
}

// ApplyEmptyFileReadFromRaw 在成功工具调用里把空文件读取改写为显式失败。
// 只有确认是文件读取且结果无可读文本时才覆盖 success/error/preview。
func ApplyEmptyFileReadFromRaw(success bool, errorText, preview, toolName string, params json.RawMessage, result any) (bool, string, string) {
	if !success {
		return success, errorText, preview
	}
	if text, ok := EmptyFileReadFromRaw(toolName, params, result); ok {
		return false, text, text
	}
	return success, errorText, preview
}

// ApplyCodexMCPPreview 为 Codex MCP tool_result 失败事件补上空文件读取说明。
// 仅在原事件已失败且可识别为空读取时替换 preview，避免改写正常工具输出。
func ApplyCodexMCPPreview(success bool, errorText, preview, toolName string, item map[string]any) string {
	if text, ok := EmptyCodexMCPFileRead(toolName, item); !success && errorText != "" && ok {
		return text
	}
	return preview
}

// SuccessUnlessEmptyCodexMCPFileRead 判断 Codex MCP tool_result 是否应由成功降为失败。
// 空文件读取对用户是可行动失败，不能以空成功结果静默吞掉。
func SuccessUnlessEmptyCodexMCPFileRead(toolName string, item map[string]any) (bool, string) {
	if text, ok := EmptyCodexMCPFileRead(toolName, item); ok {
		return false, text
	}
	return true, ""
}

// EmptyCodexMCPFileRead 从 Codex MCP rollout item 中识别空文件读取。
// 兼容 result.Ok/result.ok 包装，无法识别为文件读取时不改动。
func EmptyCodexMCPFileRead(toolName string, item map[string]any) (string, bool) {
	result, _ := item["result"].(map[string]any)
	if len(result) == 0 {
		return "", false
	}
	return EmptyFileRead(toolName, ArgsFromPayload(item), resultPayload(result))
}

// EmptyFileRead 在文件读取工具返回空内容时生成面向用户的失败说明。
// 它同时兼容单路径和多路径参数，缺目标时仍返回通用 requested file 文案。
func EmptyFileRead(toolName string, args map[string]any, result any) (string, bool) {
	if !isFileRead(toolName, args) || resultHasReadableText(result) {
		return "", false
	}
	target := stringValue(args, "file_path", "filePath", "path", "pos")
	if target == "" {
		target = stringListValue(args, "file_paths", "filePaths", "paths")
	}
	if target == "" {
		target = "requested file"
	}
	return fmt.Sprintf("file read returned no content for %q; the path does not exist or is outside workspace", target), true
}

// ArgsFromRaw 从 JSON 原始参数中提取工具 arguments。
// 解析失败返回 nil，避免 result guard 因非标准 payload 误判。
func ArgsFromRaw(raw json.RawMessage) map[string]any {
	var payload map[string]any
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	return ArgsFromPayload(payload)
}

// ArgsFromPayload 从不同 provider payload 形态中提取实际工具参数。
// 支持 invocation.arguments、顶层 arguments/args 和 action 直写形态。
func ArgsFromPayload(payload map[string]any) map[string]any {
	if invocation, _ := payload["invocation"].(map[string]any); len(invocation) > 0 {
		if args := argumentObject(invocation); len(args) > 0 {
			return args
		}
		if stringValue(invocation, "action", "operation", "op") != "" {
			return invocation
		}
	}
	if args := argumentObject(payload); len(args) > 0 {
		return args
	}
	return payload
}

func resultPayload(result map[string]any) map[string]any {
	if okPayload, _ := result["Ok"].(map[string]any); len(okPayload) > 0 {
		return okPayload
	}
	if okPayload, _ := result["ok"].(map[string]any); len(okPayload) > 0 {
		return okPayload
	}
	return result
}

// argumentObject 返回 payload 中的 arguments/args 对象。
// 字符串形态会按 JSON 再解析一层，以兼容 MCP 工具把参数序列化进字符串的情况。
func argumentObject(payload map[string]any) map[string]any {
	for _, key := range []string{"arguments", "args"} {
		switch value := payload[key].(type) {
		case map[string]any:
			return value
		case string:
			if args := ArgsFromRaw(json.RawMessage(strings.TrimSpace(value))); len(args) > 0 {
				return args
			}
		}
	}
	return nil
}

func isFileRead(toolName string, args map[string]any) bool {
	tool := strings.ToLower(strings.TrimSpace(toolName))
	if tool != "file" && tool != "lsp_file" && !strings.HasSuffix(tool, "__file") {
		return false
	}
	switch strings.ToLower(stringValue(args, "action", "operation", "op")) {
	case "read", "read_file", "readfile", "open", "open_file":
		return true
	default:
		return false
	}
}

// resultHasReadableText 判断工具结果是否包含可展示文本。
// marshal 或非对象结果按保守策略处理，避免把未知结构误判为空读取。
func resultHasReadableText(result any) bool {
	raw, err := json.Marshal(result)
	if err != nil {
		return true
	}
	var payload map[string]any
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &payload) != nil {
		return strings.TrimSpace(string(raw)) != "" && string(bytes.TrimSpace(raw)) != "null"
	}
	return itemsHaveText(payload["contentItems"]) ||
		itemsHaveText(payload["content"]) ||
		structuredText(payload["structuredContent"]) != ""
}

func itemsHaveText(value any) bool {
	items, _ := value.([]any)
	for _, raw := range items {
		item, _ := raw.(map[string]any)
		if stringValue(item, "text") != "" {
			return true
		}
	}
	return false
}

func structuredText(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		return stringValue(typed, "value", "text", "content", "message", "preview", "result")
	default:
		return ""
	}
}

func stringValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if text, ok := payload[key].(string); ok && strings.TrimSpace(text) != "" {
			return strings.TrimSpace(text)
		}
	}
	return ""
}

// stringListValue 从字符串数组参数中提取可读路径列表。
// 空项会被忽略，返回值用于错误文案而不是执行路径。
func stringListValue(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		items, _ := payload[key].([]any)
		values := make([]string, 0, len(items))
		for _, item := range items {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				values = append(values, strings.TrimSpace(text))
			}
		}
		if len(values) > 0 {
			return strings.Join(values, ", ")
		}
	}
	return ""
}
