package resultguard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// EmptyFileReadFromRaw 从原始处理empty文件read。
func EmptyFileReadFromRaw(toolName string, params json.RawMessage, result any) (string, bool) {
	return EmptyFileRead(toolName, ArgsFromRaw(params), result)
}

// ApplyEmptyFileReadFromRaw 从原始应用empty文件read。
func ApplyEmptyFileReadFromRaw(success bool, errorText, preview, toolName string, params json.RawMessage, result any) (bool, string, string) {
	if !success {
		return success, errorText, preview
	}
	if text, ok := EmptyFileReadFromRaw(toolName, params, result); ok {
		return false, text, text
	}
	return success, errorText, preview
}

// ApplyCodexMCPPreview 应用codexMCPpreview。
func ApplyCodexMCPPreview(success bool, errorText, preview, toolName string, item map[string]any) string {
	if text, ok := EmptyCodexMCPFileRead(toolName, item); !success && errorText != "" && ok {
		return text
	}
	return preview
}

// SuccessUnlessEmptyCodexMCPFileRead 处理successunlessemptycodexMCP文件read。
func SuccessUnlessEmptyCodexMCPFileRead(toolName string, item map[string]any) (bool, string) {
	if text, ok := EmptyCodexMCPFileRead(toolName, item); ok {
		return false, text
	}
	return true, ""
}

// EmptyCodexMCPFileRead 处理emptycodexMCP文件read。
func EmptyCodexMCPFileRead(toolName string, item map[string]any) (string, bool) {
	result, _ := item["result"].(map[string]any)
	if len(result) == 0 {
		return "", false
	}
	return EmptyFileRead(toolName, ArgsFromPayload(item), resultPayload(result))
}

// EmptyFileRead 处理empty文件read。
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

// ArgsFromRaw 从原始处理args。
func ArgsFromRaw(raw json.RawMessage) map[string]any {
	var payload map[string]any
	if len(bytes.TrimSpace(raw)) == 0 || json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	return ArgsFromPayload(payload)
}

// ArgsFromPayload 从载荷处理args。
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

// argumentObject 处理argumentobject。
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

// resultHasReadableText 处理结果hasreadable文本。
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

// stringListValue 处理stringlist值。
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
