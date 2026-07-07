package observability

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"
)

const (
	argumentPreviewRawLimit    = 16 * 1024
	argumentPreviewOutputLimit = 512
	argumentPreviewTruncated   = "... [truncated]"
)

var argumentPreviewPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)--(?:api[_-]?key|token|access-token|secret|password)(?:[=\s]+[^\s,;&"'}]+)?`),
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;&"'}]+`),
	regexp.MustCompile(`(?i)\b((?:api[_-]?key|secret[_-]?key|access[_-]?token|token|password)\s*[:=]\s*)[^\s,;&"'}]+`),
	regexp.MustCompile(`(?i)\b([A-Z_]*(?:TOKEN|KEY|SECRET|PASSWORD)[A-Z_]*=)[^\s,;&"'}]+`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]+`),
	regexp.MustCompile(`(?i)(?:/Users|/home|/private|/tmp|/var|/etc|/Volumes)/[^\s,;&"'}]+`),
	regexp.MustCompile(`[A-Za-z]:\\[^\s,;&"'}]+`),
}

// SafeToolArgumentsPreview 将任意工具参数编码成短预览，并统一执行参数脱敏与长度上限。
// provider、toolbridge 和 UI 消费面都应从这里取 ArgumentsPreview，避免各自实现不同规则。
func SafeToolArgumentsPreview(raw any) string {
	if raw == nil {
		return ""
	}
	switch typed := raw.(type) {
	case string:
		return SafeToolArgumentsPreviewString(typed)
	case []byte:
		return safeToolArgumentsPreviewBytes(typed)
	case json.RawMessage:
		return safeToolArgumentsPreviewBytes(typed)
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return SafeToolArgumentsPreviewString(fmt.Sprint(raw))
	}
	return safeToolArgumentsPreviewBytes(encoded)
}

// SafeToolArgumentsPreviewString 处理已经是字符串形态的工具参数预览。
// 调用方传入 provider 原始 preview 时仍会走 JSON 感知脱敏、16KiB 输入上限和 512B 输出上限。
func SafeToolArgumentsPreviewString(raw string) string {
	return safeToolArgumentsPreviewBytes([]byte(raw))
}

func safeToolArgumentsPreviewBytes(raw []byte) string {
	limited, rawTruncated := limitArgumentPreviewRaw(raw)
	text := strings.TrimSpace(strings.ToValidUTF8(string(limited), ""))
	if text == "" {
		return finishArgumentPreview(text, rawTruncated)
	}
	if preview, ok := safeToolArgumentsPreviewJSON(text); ok {
		return finishArgumentPreview(preview, rawTruncated)
	}
	return finishArgumentPreview(sanitizeArgumentPreviewText(text), rawTruncated)
}

func limitArgumentPreviewRaw(raw []byte) ([]byte, bool) {
	if len(raw) <= argumentPreviewRawLimit {
		return raw, false
	}
	return raw[:argumentPreviewRawLimit], true
}

func safeToolArgumentsPreviewJSON(text string) (string, bool) {
	var decoded any
	if err := json.Unmarshal([]byte(text), &decoded); err != nil {
		return "", false
	}
	switch decoded.(type) {
	case map[string]any, []any:
	default:
		return "", false
	}
	encoded, err := json.Marshal(sanitizeArgumentPreviewValue(decoded, ""))
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

// sanitizeArgumentPreviewValue 递归复制 JSON 参数结构，并在敏感 key 下整体替换值。
// 普通字符串继续走文本正则，确保 command 里的 token/flag 也不会绕过 JSON key 脱敏。
func sanitizeArgumentPreviewValue(value any, key string) any {
	if key != "" && sensitiveArgumentPreviewKey(key) {
		return redacted
	}
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for childKey, childValue := range typed {
			out[sanitizeArgumentPreviewText(childKey)] = sanitizeArgumentPreviewValue(childValue, childKey)
		}
		return out
	case []any:
		out := make([]any, 0, len(typed))
		for _, childValue := range typed {
			out = append(out, sanitizeArgumentPreviewValue(childValue, key))
		}
		return out
	case string:
		return sanitizeArgumentPreviewStringValue(typed)
	default:
		return typed
	}
}

func sanitizeArgumentPreviewStringValue(value string) string {
	text := strings.TrimSpace(value)
	if preview, ok := safeToolArgumentsPreviewJSON(text); ok {
		return preview
	}
	return sanitizeArgumentPreviewText(value)
}

// sensitiveArgumentPreviewKey 识别参数对象中必须整值替换的敏感字段名。
// 这里保守覆盖 token、env、path 和工作区目录字段，避免路径或环境变量从结构化参数进入 UI。
func sensitiveArgumentPreviewKey(key string) bool {
	key = strings.ToLower(argumentPreviewCamelBoundary.ReplaceAllString(strings.TrimSpace(key), "${1}_${2}"))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, " ", "_")
	switch {
	case key == "env" || key == "environment" || key == "cwd":
		return true
	case strings.Contains(key, "token"), strings.Contains(key, "secret"), strings.Contains(key, "password"):
		return true
	case strings.Contains(key, "api_key") || strings.Contains(key, "apikey") || strings.Contains(key, "authorization"):
		return true
	case strings.Contains(key, "path") || strings.Contains(key, "workspace_root"):
		return true
	default:
		return false
	}
}

var argumentPreviewCamelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func sanitizeArgumentPreviewText(text string) string {
	text = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(text)
	for _, pattern := range argumentPreviewPatterns {
		text = pattern.ReplaceAllString(text, redacted)
	}
	return strings.Join(strings.Fields(text), " ")
}

func finishArgumentPreview(text string, truncated bool) string {
	text = strings.TrimSpace(text)
	if !truncated && len(text) <= argumentPreviewOutputLimit {
		return text
	}
	limit := max(argumentPreviewOutputLimit-len(argumentPreviewTruncated), 0)
	text = strings.TrimSpace(trimArgumentPreviewBytes(text, limit))
	if text == "" {
		return argumentPreviewTruncated
	}
	return text + argumentPreviewTruncated
}

func trimArgumentPreviewBytes(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	for limit > 0 && !utf8.ValidString(text[:limit]) {
		limit--
	}
	return text[:limit]
}
