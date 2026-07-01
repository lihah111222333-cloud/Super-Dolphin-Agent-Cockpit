package logger

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	FieldTraceparent      = "traceparent"
	FieldTraceparentSnake = "trace_parent"
	FieldAOTraceparent    = "_aoTraceparent"
	FieldAOTraceID        = "_aoTraceId"
	FieldAOSpanID         = "_aoSpanId"
)

// TraceCarrierFields 是日志、RPC 和 MCP 边界共享的 trace/span 标识集合。
type TraceCarrierFields struct {
	TraceID      string
	SpanID       string
	ParentSpanID string
}

// TraceFieldAliases 描述从松散字段 map 中提取 trace/span 的候选键。
type TraceFieldAliases struct {
	TraceIDKeys      []string
	SpanIDKeys       []string
	ParentSpanIDKeys []string
	TraceparentKeys  []string
}

// DefaultTraceFieldAliases 返回 agent-v3 当前接受的 trace 字段别名集合。
func DefaultTraceFieldAliases() TraceFieldAliases {
	return TraceFieldAliases{
		TraceIDKeys: []string{
			FieldTraceID,
			FieldECSTraceID,
			"traceId",
			FieldAOTraceID,
		},
		SpanIDKeys: []string{
			FieldSpanID,
			FieldECSSpanID,
			"spanId",
			FieldAOSpanID,
		},
		ParentSpanIDKeys: []string{
			FieldParentSpanID,
			FieldECSParentSpanID,
			"parent.span_id",
			"parentSpanId",
		},
		TraceparentKeys: []string{
			FieldTraceparent,
			FieldTraceparentSnake,
			FieldAOTraceparent,
		},
	}
}

// ExtractTraceCarrierFields 从边界字段中提取 trace/span；traceparent 与显式字段冲突时直接报错。
func ExtractTraceCarrierFields(fields map[string]any, aliases TraceFieldAliases) (TraceCarrierFields, error) {
	aliases = aliases.withDefaults()
	trace := TraceCarrierFields{
		TraceID:      firstCarrierString(fields, aliases.TraceIDKeys...),
		SpanID:       firstCarrierString(fields, aliases.SpanIDKeys...),
		ParentSpanID: firstCarrierString(fields, aliases.ParentSpanIDKeys...),
	}
	traceparent := firstCarrierString(fields, aliases.TraceparentKeys...)
	if traceparent != "" {
		parsed, err := ParseTraceparent(traceparent)
		if err != nil {
			return TraceCarrierFields{}, err
		}
		if trace.TraceID != "" && trace.TraceID != parsed.TraceID {
			return TraceCarrierFields{}, fmt.Errorf("%s does not match traceparent", FieldTraceID)
		}
		if trace.SpanID != "" && trace.SpanID != parsed.SpanID {
			return TraceCarrierFields{}, fmt.Errorf("%s does not match traceparent", FieldSpanID)
		}
		trace.TraceID = firstNonEmpty(trace.TraceID, parsed.TraceID)
		trace.SpanID = firstNonEmpty(trace.SpanID, parsed.SpanID)
	}
	if err := ValidateTraceToken(FieldTraceID, trace.TraceID); err != nil {
		return TraceCarrierFields{}, err
	}
	if err := ValidateTraceToken(FieldSpanID, trace.SpanID); err != nil {
		return TraceCarrierFields{}, err
	}
	if err := ValidateTraceToken(FieldParentSpanID, trace.ParentSpanID); err != nil {
		return TraceCarrierFields{}, err
	}
	return trace, nil
}

// ExtractAOTraceCarrierJSON 从前端 JSON metadata 中提取 traceparent，并校验冗余 trace/span 字段一致。
func ExtractAOTraceCarrierJSON(obj map[string]json.RawMessage) (TraceCarrierFields, bool, error) {
	traceparent, ok, err := carrierJSONString(obj, FieldAOTraceparent)
	if err != nil {
		return TraceCarrierFields{}, false, err
	}
	if !ok {
		return TraceCarrierFields{}, false, nil
	}
	trace, err := ParseTraceparent(traceparent)
	if err != nil {
		return TraceCarrierFields{}, true, fmt.Errorf("invalid %s: %w", FieldAOTraceparent, err)
	}
	if metadataTraceID, ok, err := carrierJSONString(obj, FieldAOTraceID); err != nil {
		return TraceCarrierFields{}, true, err
	} else if ok && metadataTraceID != trace.TraceID {
		return TraceCarrierFields{}, true, fmt.Errorf("mismatched %s", FieldAOTraceID)
	}
	if metadataSpanID, ok, err := carrierJSONString(obj, FieldAOSpanID); err != nil {
		return TraceCarrierFields{}, true, err
	} else if ok && metadataSpanID != trace.SpanID {
		return TraceCarrierFields{}, true, fmt.Errorf("mismatched %s", FieldAOSpanID)
	}
	return trace, true, nil
}

// ParseTraceparent 校验 W3C traceparent 并返回其中的 trace/span 标识。
func ParseTraceparent(value string) (TraceCarrierFields, error) {
	parts := strings.Split(strings.TrimSpace(value), "-")
	if len(parts) != 4 {
		return TraceCarrierFields{}, fmt.Errorf("traceparent must have 4 dash-separated fields")
	}
	if parts[0] != "00" {
		return TraceCarrierFields{}, fmt.Errorf("unsupported traceparent version %q", parts[0])
	}
	if !ValidTraceID(parts[1]) {
		return TraceCarrierFields{}, fmt.Errorf("invalid trace id")
	}
	if !ValidSpanID(parts[2]) {
		return TraceCarrierFields{}, fmt.Errorf("invalid span id")
	}
	if !ValidTraceFlags(parts[3]) {
		return TraceCarrierFields{}, fmt.Errorf("invalid trace flags")
	}
	return TraceCarrierFields{TraceID: parts[1], SpanID: parts[2]}, nil
}

// ValidTraceID 判断 trace id 是否符合 W3C traceparent 的非零小写十六进制 16 字节格式。
func ValidTraceID(value string) bool {
	return len(value) == 32 && isLowerHex(value) && !allZeroHex(value)
}

// ValidSpanID 判断 span id 是否符合 W3C traceparent 的非零小写十六进制 8 字节格式。
func ValidSpanID(value string) bool {
	return len(value) == 16 && isLowerHex(value) && !allZeroHex(value)
}

// ValidTraceFlags 判断 trace flags 是否为 W3C traceparent 允许的小写十六进制单字节值。
func ValidTraceFlags(value string) bool {
	return len(value) == 2 && isLowerHex(value)
}

// ValidateTraceToken 防止 trace/span 查询键被控制字符、路径符或超长值污染。
func ValidateTraceToken(name, value string) error {
	if value == "" {
		return nil
	}
	if len(value) > 128 {
		return fmt.Errorf("%s is too long", name)
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f || r == '/' || r == '\\' {
			return fmt.Errorf("%s contains unsafe characters", name)
		}
	}
	return nil
}

func (a TraceFieldAliases) withDefaults() TraceFieldAliases {
	if len(a.TraceIDKeys) == 0 && len(a.SpanIDKeys) == 0 && len(a.ParentSpanIDKeys) == 0 && len(a.TraceparentKeys) == 0 {
		return DefaultTraceFieldAliases()
	}
	return a
}

func firstCarrierString(fields map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := carrierString(fields[key]); value != "" {
			return value
		}
	}
	return ""
}

func carrierString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func carrierJSONString(obj map[string]json.RawMessage, key string) (string, bool, error) {
	raw, ok := obj[key]
	if !ok {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", true, fmt.Errorf("%s must be a string", key)
	}
	return value, true, nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// isLowerHex 判断 traceparent 片段是否只包含小写十六进制字符。
func isLowerHex(value string) bool {
	for _, r := range value {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

// allZeroHex 判断 trace/span id 是否为 W3C traceparent 禁止的全零值。
func allZeroHex(value string) bool {
	for _, r := range value {
		if r != '0' {
			return false
		}
	}
	return true
}
