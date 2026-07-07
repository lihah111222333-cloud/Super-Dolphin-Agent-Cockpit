package observability

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

// redacted 是所有敏感字段统一替换后的展示值。
const redacted = "[REDACTED]"

// secretPatterns 覆盖常见 token、密钥和 Authorization 文本形态。
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;&]+`),
	regexp.MustCompile(`(?i)((?:api[_-]?key|secret[_-]?key|access[_-]?token|token|password)\s*[:=]\s*)[^\s,;&]+`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`),
}

var metadataCamelBoundaryPattern = regexp.MustCompile(`([a-z0-9])([A-Z])`)

var sensitiveMetadataKeyTokens = map[string]struct{}{
	"auth":        {},
	"body":        {},
	"content":     {},
	"credential":  {},
	"credentials": {},
	"cwd":         {},
	"env":         {},
	"input":       {},
	"output":      {},
	"params":      {},
	"password":    {},
	"path":        {},
	"paths":       {},
	"profile":     {},
	"prompt":      {},
	"raw":         {},
	"secret":      {},
	"stack":       {},
	"text":        {},
	"token":       {},
}

var sensitiveMetadataKeys = map[string]struct{}{
	"access_token":    {},
	"api_key":         {},
	"auth_token":      {},
	"authorization":   {},
	"file_content":    {},
	"file_contents":   {},
	"file_path":       {},
	"id_token":        {},
	"message_text":    {},
	"output_tail":     {},
	"raw_input":       {},
	"raw_output":      {},
	"raw_params":      {},
	"raw_stack":       {},
	"refresh_token":   {},
	"request_params":  {},
	"result_preview":  {},
	"stack_trace":     {},
	"stacktrace":      {},
	"tool_result":     {},
	"tool_results":    {},
	"user_message":    {},
	"user_prompt":     {},
	"workspace_root":  {},
	"workspace_roots": {},
}

// Sanitizer 负责把 trace event 中的字符串、栈和 metadata 约束到可落盘形态。
type Sanitizer struct {
	stringMaxBytes   int
	metadataMaxBytes int
}

// NewSanitizer 根据配置创建 trace 脱敏器。
func NewSanitizer(cfg Config) Sanitizer {
	return Sanitizer{stringMaxBytes: cfg.StringMaxBytes, metadataMaxBytes: cfg.MetadataMaxBytes}
}

// SanitizeEvent 统一设置 schema version，并脱敏事件所有可变文本字段。
func (s Sanitizer) SanitizeEvent(event TraceEvent) TraceEvent {
	event.SchemaVersion = SchemaVersion
	event.TraceID = s.String(event.TraceID)
	event.SpanID = s.String(event.SpanID)
	event.ParentSpanID = s.String(event.ParentSpanID)
	event.Kind = s.String(event.Kind)
	event.Phase = s.String(event.Phase)
	event.Method = s.String(event.Method)
	event.ThreadID = s.String(event.ThreadID)
	event.AgentID = s.String(event.AgentID)
	event.TurnID = s.String(event.TurnID)
	event.CallID = s.String(event.CallID)
	event.ToolName = s.String(event.ToolName)
	event.ClientKind = s.String(event.ClientKind)
	event.ClientRoute = s.String(event.ClientRoute)
	event.Error = s.String(event.Error)
	event.Code = s.CodeAnchor(event.Code)
	event.Stack = s.Stack(event.Stack)
	event.Metadata = s.SanitizeMetadata(event.Metadata)
	return event
}

// String 规范化多行文本、替换敏感片段，并按 UTF-8 边界截断。
func (s Sanitizer) String(value string) string {
	value = normalizeMultiline(value)
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllString(value, "$1"+redacted)
	}
	return truncateUTF8(value, s.stringMaxBytes)
}

// CodeAnchor 脱敏代码锚点里的文件名和函数名。
func (s Sanitizer) CodeAnchor(anchor CodeAnchor) CodeAnchor {
	anchor.File = s.String(anchor.File)
	anchor.Function = s.String(anchor.Function)
	return anchor
}

// Stack 脱敏调用栈帧，只保留文件、函数和行号。
func (s Sanitizer) Stack(frames []StackFrame) []StackFrame {
	out := make([]StackFrame, 0, len(frames))
	for _, frame := range frames {
		out = append(out, StackFrame{File: s.String(frame.File), Function: s.String(frame.Function), Line: frame.Line})
	}
	return out
}

// SanitizeMetadata 复制并脱敏 metadata，只保留可 JSON 编码的安全类型。
func (s Sanitizer) SanitizeMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]any, len(metadata))
	dropped := false
	for key, value := range metadata {
		safeKey := s.String(key)
		safeValue, ok := s.metadataValueForKey(key, value)
		if !ok {
			dropped = true
			continue
		}
		out[safeKey] = safeValue
	}
	return s.enforceMetadataLimit(out, dropped)
}

// metadataValueForKey 根据 metadata 键名和值类型决定如何脱敏或丢弃。
func (s Sanitizer) metadataValueForKey(key string, value any) (any, bool) {
	switch typed := value.(type) {
	case string:
		return s.metadataString(key, typed), true
	case bool:
		return typed, true
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case float64:
		return finiteFloat(typed)
	case float32:
		return finiteFloat(float64(typed))
	case []string:
		return s.stringSliceForKey(key, typed), true
	case []int64:
		return typed, true
	case map[string]string:
		return s.stringMap(typed), true
	default:
		return nil, false
	}
}

// finiteFloat 拒绝 JSON 无法安全表达的 NaN 和 Inf。
func finiteFloat(value float64) (any, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, false
	}
	return value, true
}

// metadataString 对敏感键直接整体隐藏，否则走通用字符串脱敏。
func (s Sanitizer) metadataString(key string, value string) string {
	if secretLikeKey(key) {
		return redacted
	}
	return s.String(value)
}

// stringSliceForKey 对字符串切片逐项应用 metadata 字符串规则。
func (s Sanitizer) stringSliceForKey(key string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, s.metadataString(key, value))
	}
	return out
}

// stringMap 脱敏 map 的键和值，避免嵌套字符串绕过限制。
func (s Sanitizer) stringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[s.String(key)] = s.metadataString(key, value)
	}
	return out
}

// secretLikeKey 用键名判断字段是否应整体隐藏。
func secretLikeKey(key string) bool {
	normalized := normalizeMetadataKey(key)
	if normalized == "" {
		return false
	}
	if _, ok := sensitiveMetadataKeys[normalized]; ok {
		return true
	}
	for part := range strings.SplitSeq(normalized, "_") {
		if _, ok := sensitiveMetadataKeyTokens[part]; ok {
			return true
		}
	}
	compact := strings.ReplaceAll(normalized, "_", "")
	return strings.Contains(compact, "token") ||
		strings.Contains(compact, "password") ||
		strings.Contains(compact, "secret") ||
		strings.Contains(compact, "authorization") ||
		strings.Contains(compact, "apikey")
}

func normalizeMetadataKey(key string) string {
	normalized := metadataCamelBoundaryPattern.ReplaceAllString(strings.TrimSpace(key), "${1}_${2}")
	normalized = strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(normalized)
	return strings.ToLower(normalized)
}

// enforceMetadataLimit 在超出字节上限时逐项删除 metadata 并写入截断标记。
func (s Sanitizer) enforceMetadataLimit(metadata map[string]any, dropped bool) map[string]any {
	if dropped {
		metadata["metadata_dropped"] = true
	}
	for metadataJSONSize(metadata) > s.metadataMaxBytes && len(metadata) > 0 {
		deleteOneMetadataKey(metadata)
		metadata["metadata_truncated"] = true
	}
	return metadata
}

// deleteOneMetadataKey 删除一个普通 metadata 键，保留诊断标记。
func deleteOneMetadataKey(metadata map[string]any) {
	for key := range metadata {
		if key != "metadata_truncated" && key != "metadata_dropped" {
			delete(metadata, key)
			return
		}
	}
}

// metadataJSONSize 返回 metadata 编码后的字节数，编码失败时视为超限。
func metadataJSONSize(metadata map[string]any) int {
	data, err := json.Marshal(metadata)
	if err != nil {
		return math.MaxInt
	}
	return len(data)
}

// MarshalSanitizedJSON 编码已脱敏的 trace event。
func MarshalSanitizedJSON(event TraceEvent) ([]byte, error) {
	return json.Marshal(event)
}

// normalizeMultiline 把多行文本压成单行，避免 JSONL 预览和日志展示错位。
func normalizeMultiline(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.Join(strings.Fields(value), " ")
}

// truncateUTF8 按字节上限截断字符串，同时不切断 Unicode 字符。
func truncateUTF8(value string, maxBytes int) string {
	if maxBytes <= 0 || len(value) <= maxBytes {
		return value
	}
	for len(value) > maxBytes && len(value) > 0 {
		_, size := utf8.DecodeLastRuneInString(value)
		value = value[:len(value)-size]
	}
	return value
}
