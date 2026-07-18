package observability

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	argumentPreviewRawLimit    = 16 * 1024
	argumentPreviewOutputLimit = 512
	argumentPreviewTruncated   = "... [truncated]"
)

var argumentPreviewPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)--(?:api[_-]?key|private[_-]?key|token|access-token|secret|password|credential|cookie|session|certificate)(?:[=\s]+[^\s,;&"'}]+)?`),
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;&"'}]+`),
	regexp.MustCompile(`(?i)\b((?:api[_-]?key|private[_-]?key|secret[_-]?key|access[_-]?token|token|password|credential|cookie|session|certificate)\s*[:=]\s*)[^\s,;&"'}]+`),
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
	if len(raw) > argumentPreviewRawLimit {
		return safeOversizedToolArgumentsPreview([]byte(raw[:argumentPreviewRawLimit]))
	}
	return safeToolArgumentsPreviewBytes([]byte(raw))
}

// safeToolArgumentsPreviewBytes 对超限结构化输入 fail-closed，仅解析大小受限的完整输入。
func safeToolArgumentsPreviewBytes(raw []byte) string {
	limited, rawTruncated := limitArgumentPreviewRaw(raw)
	if rawTruncated {
		return safeOversizedToolArgumentsPreview(limited)
	}
	text := strings.TrimSpace(strings.ToValidUTF8(string(limited), ""))
	if text == "" {
		return ""
	}
	if preview, ok := safeToolArgumentsPreviewJSON(text); ok {
		return finishArgumentPreview(preview, false)
	}
	return finishArgumentPreview(sanitizeArgumentPreviewText(text), false)
}

// safeOversizedToolArgumentsPreview 仅处理已经限制为 16KiB 的超限输入前缀。
func safeOversizedToolArgumentsPreview(prefix []byte) string {
	if argumentPreviewPrefixLooksStructured(prefix) {
		return finishArgumentPreview(redacted, true)
	}
	text := strings.TrimSpace(strings.ToValidUTF8(string(prefix), ""))
	return finishArgumentPreview(sanitizeArgumentPreviewText(text), true)
}

// argumentPreviewPrefixLooksStructured 在有界字节前缀中查找首个有效非空白 rune。
func argumentPreviewPrefixLooksStructured(prefix []byte) bool {
	for len(prefix) > 0 {
		r, size := utf8.DecodeRune(prefix)
		if r == utf8.RuneError && size == 1 {
			prefix = prefix[1:]
			continue
		}
		if unicode.IsSpace(r) {
			prefix = prefix[size:]
			continue
		}
		return r == '{' || r == '['
	}
	return true
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
// 明确敏感词按分隔后的完整片段匹配，避免把 key、keyboard 等普通字段误判为私钥。
func sensitiveArgumentPreviewKey(key string) bool {
	key = strings.ToLower(argumentPreviewCamelBoundary.ReplaceAllString(strings.TrimSpace(key), "${1}_${2}"))
	key = strings.ReplaceAll(key, "-", "_")
	key = strings.ReplaceAll(key, " ", "_")
	return broadSensitiveArgumentPreviewKey(key) || explicitSensitiveArgumentPreviewKey(key)
}

// broadSensitiveArgumentPreviewKey 保留既有 token、环境和路径等宽匹配规则。
func broadSensitiveArgumentPreviewKey(key string) bool {
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

// explicitSensitiveArgumentPreviewKey 按完整字段片段识别凭据、会话、证书和私钥。
func explicitSensitiveArgumentPreviewKey(key string) bool {
	segments := strings.FieldsFunc(key, func(r rune) bool {
		return r == '_' || r == '.' || r == '/'
	})
	for i, segment := range segments {
		if sensitiveArgumentPreviewKeySegment(segment) {
			return true
		}
		if segment == "private" && i+1 < len(segments) && segments[i+1] == "key" {
			return true
		}
	}
	return false
}

var argumentPreviewCamelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

func sensitiveArgumentPreviewKeySegment(segment string) bool {
	switch segment {
	case "credential", "credentials", "cookie", "cookies", "session", "sessions", "certificate", "certificates":
		return true
	default:
		return false
	}
}

func sanitizeArgumentPreviewText(text string) string {
	if containsSensitivePEM(text) {
		return redacted
	}
	text = strings.NewReplacer("\r", " ", "\n", " ", "\t", " ").Replace(text)
	for _, pattern := range argumentPreviewPatterns {
		text = pattern.ReplaceAllString(text, redacted)
	}
	return strings.Join(strings.Fields(text), " ")
}

// containsSensitivePEM 检测私钥或证书 PEM 起始标签，命中后调用方整段脱敏。
func containsSensitivePEM(text string) bool {
	const beginMarker = "-----BEGIN "
	remaining := strings.ToUpper(text)
	for {
		begin := strings.Index(remaining, beginMarker)
		if begin < 0 {
			return false
		}
		remaining = remaining[begin+len(beginMarker):]
		end := strings.Index(remaining, "-----")
		if end < 0 {
			return false
		}
		label := strings.TrimSpace(remaining[:end])
		if strings.Contains(label, "PRIVATE KEY") || strings.Contains(label, "CERTIFICATE") {
			return true
		}
		remaining = remaining[end+len("-----"):]
	}
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
