package shared

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// SafePathLogFields 返回路径类日志值的不可逆摘要字段。
func SafePathLogFields(name string, value any) []any {
	return safeLogSummaryFields(name, value, "path")
}

// SafePayloadLogFields 返回 payload 类日志值的不可逆摘要字段。
func SafePayloadLogFields(name string, value any) []any {
	return safeLogSummaryFields(name, value, "payload")
}

// SafeRuntimeLogFields 将运行时敏感键值对转换为安全摘要日志字段。
func SafeRuntimeLogFields(fields ...any) []any {
	if len(fields)%2 != 0 {
		return []any{"safe_runtime_log_fields_error", "odd_field_count", "safe_runtime_log_fields_count", len(fields)}
	}
	out := make([]any, 0, len(fields)*2)
	for i := 0; i < len(fields); i += 2 {
		key, ok := fields[i].(string)
		if !ok || strings.TrimSpace(key) == "" {
			key = fmt.Sprintf("runtime_field_%d", i/2)
		}
		out = append(out, safeLogSummaryFields(key, fields[i+1], runtimeFieldKind(key))...)
	}
	return out
}

func safeLogSummaryFields(name string, value any, kind string) []any {
	raw, present := safeLogBytes(value)
	return []any{
		name + "_present", present,
		name + "_bytes", len(raw),
		name + "_sha256", safeLogHash(raw),
		name + "_display_class", safeLogDisplayClass(name, value, raw, present, kind),
	}
}

// safeLogBytes 将日志值序列化为仅用于长度和 hash 的字节串。
func safeLogBytes(value any) ([]byte, bool) {
	if value == nil {
		return nil, false
	}
	switch v := value.(type) {
	case string:
		return []byte(v), v != ""
	case []byte:
		return v, len(v) > 0
	case json.RawMessage:
		return []byte(v), len(v) > 0
	case []string:
		raw, err := json.Marshal(v)
		if err != nil {
			return fmt.Appendf(nil, "%T", value), true
		}
		return raw, len(v) > 0
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return fmt.Appendf(nil, "%T", value), true
		}
		return raw, string(raw) != "null"
	}
}

// runtimeFieldKind 按字段名选择路径、payload 或普通运行时摘要分类。
func runtimeFieldKind(name string) string {
	lower := strings.ToLower(strings.TrimSpace(name))
	if strings.Contains(lower, "cwd") || strings.Contains(lower, "root") || strings.Contains(lower, "path") || strings.Contains(lower, "home") {
		return "path"
	}
	switch lower {
	case "payload", "prompt", "config", "config_body", "instructions", "sandbox_policy":
		return "payload"
	default:
		return "runtime"
	}
}

func safeLogHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

func safeLogDisplayClass(name string, value any, raw []byte, present bool, kind string) string {
	if !present {
		return "empty"
	}
	if kind == "path" {
		return pathDisplayClass(value, raw)
	}
	if strings.EqualFold(strings.TrimSpace(name), "sandbox_policy") {
		return "policy_value"
	}
	return payloadDisplayClass(value, raw)
}

func pathDisplayClass(value any, raw []byte) string {
	if _, ok := value.([]string); ok {
		return "path_list"
	}
	text := string(raw)
	if filepath.IsAbs(text) {
		return "absolute_path"
	}
	if strings.HasPrefix(text, "~/") {
		return "home_relative_path"
	}
	if strings.ContainsAny(text, `/\`) {
		return "relative_path"
	}
	return "path_token"
}

// payloadDisplayClass 给 payload 摘要提供不含原文的展示分类。
func payloadDisplayClass(value any, raw []byte) string {
	if _, ok := value.([]string); ok {
		return "string_list"
	}
	trimmed := strings.TrimSpace(string(raw))
	if json.Valid(raw) {
		if strings.HasPrefix(trimmed, "{") {
			return "json_object"
		}
		if strings.HasPrefix(trimmed, "[") {
			return "json_array"
		}
		return "json_value"
	}
	if utf8.Valid(raw) {
		return "text"
	}
	return "binary"
}
