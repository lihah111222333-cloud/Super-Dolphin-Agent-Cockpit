package observability

import (
	"encoding/json"
	"math"
	"regexp"
	"strings"
	"unicode/utf8"
)

const redacted = "[REDACTED]"

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization\s*[:=]\s*bearer\s+)[^\s,;&]+`),
	regexp.MustCompile(`(?i)((?:api[_-]?key|secret[_-]?key|access[_-]?token|token|password)\s*[:=]\s*)[^\s,;&]+`),
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{8,}`),
}

type Sanitizer struct {
	stringMaxBytes   int
	metadataMaxBytes int
}

// NewSanitizer 创建sanitizer。
func NewSanitizer(cfg Config) Sanitizer {
	return Sanitizer{stringMaxBytes: cfg.StringMaxBytes, metadataMaxBytes: cfg.MetadataMaxBytes}
}

// SanitizeEvent 清理事件。
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

// String 返回字符串表示。
func (s Sanitizer) String(value string) string {
	value = normalizeMultiline(value)
	for _, pattern := range secretPatterns {
		value = pattern.ReplaceAllString(value, "$1"+redacted)
	}
	return truncateUTF8(value, s.stringMaxBytes)
}

// CodeAnchor 处理代码锚点。
func (s Sanitizer) CodeAnchor(anchor CodeAnchor) CodeAnchor {
	anchor.File = s.String(anchor.File)
	anchor.Function = s.String(anchor.Function)
	return anchor
}

// Stack 处理stack。
func (s Sanitizer) Stack(frames []StackFrame) []StackFrame {
	out := make([]StackFrame, 0, len(frames))
	for _, frame := range frames {
		out = append(out, StackFrame{File: s.String(frame.File), Function: s.String(frame.Function), Line: frame.Line})
	}
	return out
}

// SanitizeMetadata 清理元数据。
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

// metadataValueForKey 为键处理元数据值。
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

func finiteFloat(value float64) (any, bool) {
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return nil, false
	}
	return value, true
}

func (s Sanitizer) metadataString(key string, value string) string {
	if secretLikeKey(key) {
		return redacted
	}
	return s.String(value)
}

func (s Sanitizer) stringSliceForKey(key string, values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, s.metadataString(key, value))
	}
	return out
}

func (s Sanitizer) stringMap(values map[string]string) map[string]string {
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[s.String(key)] = s.metadataString(key, value)
	}
	return out
}

func secretLikeKey(key string) bool {
	key = strings.ToLower(key)
	normalized := strings.NewReplacer("-", "_", ".", "_", " ", "_").Replace(key)
	return strings.Contains(normalized, "token") || strings.Contains(normalized, "password") || strings.Contains(normalized, "secret") || strings.Contains(normalized, "authorization") || strings.Contains(normalized, "api_key")
}

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

func deleteOneMetadataKey(metadata map[string]any) {
	for key := range metadata {
		if key != "metadata_truncated" && key != "metadata_dropped" {
			delete(metadata, key)
			return
		}
	}
}

func metadataJSONSize(metadata map[string]any) int {
	data, err := json.Marshal(metadata)
	if err != nil {
		return math.MaxInt
	}
	return len(data)
}

// MarshalSanitizedJSON 编码sanitizedJSON。
func MarshalSanitizedJSON(event TraceEvent) ([]byte, error) {
	return json.Marshal(event)
}

func normalizeMultiline(value string) string {
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return strings.Join(strings.Fields(value), " ")
}

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
