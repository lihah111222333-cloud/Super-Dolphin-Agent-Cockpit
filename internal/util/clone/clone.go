// Package clone provides deep-copy helpers for common Go types used across
// the module layer.
package clone

import (
	"encoding/json"
	"time"
)

// RawMessage returns a deep copy of a json.RawMessage.
// RawMessage 处理原始消息。
func RawMessage(message json.RawMessage) json.RawMessage {
	if len(message) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), message...)
}

// Strings returns a deep copy of a string slice.
// Strings 处理strings。
func Strings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	return append([]string(nil), input...)
}

// StringMap returns a deep copy of a string-to-string map.
// StringMap 处理stringmap。
func StringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

// JSONMap returns a deep copy of a JSON-like map.
// JSONMap 处理JSONmap。
func JSONMap(input map[string]any) map[string]any {
	if len(input) == 0 {
		return map[string]any{}
	}
	cloned := make(map[string]any, len(input))
	for key, value := range input {
		cloned[key] = cloneJSONValue(value)
	}
	return cloned
}

// RuntimeConfigMap returns a deep copy of a runtime configuration map.
// RuntimeConfigMap 处理运行时配置map。
func RuntimeConfigMap(cfg map[string]any) map[string]any {
	if len(cfg) == 0 {
		return nil
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return copyMapAny(cfg)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return copyMapAny(cfg)
	}
	return out
}

// Time returns a deep copy of a *time.Time pointer.
// Time 处理时间。
func Time(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneJSONValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return JSONMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index := range typed {
			cloned[index] = cloneJSONValue(typed[index])
		}
		return cloned
	default:
		return typed
	}
}

func copyMapAny(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
