// Package configutil provides pure utility functions for extracting typed
// values from map[string]any configuration maps. These helpers are
// intentionally dependency-free so that both provider and module layers can
// use them without introducing circular imports.
package configutil

import (
	"fmt"
	"strings"
)

// ConfigString returns the first non-empty string value found under any of
// the given keys. Values are sanitized via SanitizeConfigString.
// ConfigString 处理配置string。
func ConfigString(cfg map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := cfg[key].(string); ok {
			if value = SanitizeConfigString(value); value != "" {
				return value
			}
		}
	}
	return ""
}

// StrictString returns the trimmed string for the first present key and errors
// when that key exists with a non-string value.
// StrictString 处理strictstring。
func StrictString(cfg map[string]any, label string, keys ...string) (string, error) {
	value, key, ok := StrictValue(cfg, keys...)
	if !ok {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", fmt.Errorf("%s %q must be a string", label, key)
	}
	return strings.TrimSpace(text), nil
}

// StrictBool returns the bool for the first present key and errors when that
// key exists with a non-bool value.
// StrictBool 处理strictbool。
func StrictBool(cfg map[string]any, label string, keys ...string) (bool, error) {
	value, key, ok := StrictValue(cfg, keys...)
	if !ok {
		return false, nil
	}
	flag, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("%s %q must be a bool", label, key)
	}
	return flag, nil
}

// StrictValue 处理strict值。
func StrictValue(cfg map[string]any, keys ...string) (any, string, bool) {
	for _, key := range keys {
		if value, ok := cfg[key]; ok {
			return value, key, true
		}
	}
	return nil, "", false
}

// SanitizeConfigString trims whitespace and rejects common JS/JSON
// sentinel strings such as "undefined", "null", and "[object Object]".
// SanitizeConfigString 清理配置string。
func SanitizeConfigString(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "[object object]", "undefined", "null":
		return ""
	default:
		return value
	}
}

// StringMap converts a map[string]any (typically from JSON) to a
// map[string]string, keeping only entries where both key and value are
// non-empty trimmed strings.
// StringMap 处理stringmap。
func StringMap(raw any) map[string]string {
	input, _ := raw.(map[string]any)
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		text, ok := value.(string)
		if !ok {
			continue
		}
		if key = strings.TrimSpace(key); key == "" {
			continue
		}
		if text = strings.TrimSpace(text); text == "" {
			continue
		}
		out[key] = text
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// ConfigStringSlice returns the first non-empty string slice found under
// any of the given keys, normalizing the raw value via
// NormalizeConfigStringSlice.
// ConfigStringSlice 处理配置stringslice。
func ConfigStringSlice(cfg map[string]any, keys ...string) []string {
	for _, key := range keys {
		values, ok := cfg[key]
		if !ok {
			continue
		}
		if out := NormalizeConfigStringSlice(values); len(out) > 0 {
			return out
		}
	}
	return nil
}

// NormalizeConfigStringSlice coerces a value to []string. It accepts
// []string, []any (extracting string elements), and a single
// comma-separated string.
// NormalizeConfigStringSlice 规范化配置stringslice。
func NormalizeConfigStringSlice(values any) []string {
	switch typed := values.(type) {
	case []string:
		return TrimStrings(typed)
	case []any:
		return TrimConfigStringValues(typed)
	case string:
		return SplitConfigStringSlice(typed)
	default:
		return nil
	}
}

// TrimConfigStringValues extracts non-empty trimmed strings from a []any
// slice, discarding non-string elements.
// TrimConfigStringValues 处理裁剪配置string值。
func TrimConfigStringValues(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		if text = strings.TrimSpace(text); text != "" {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// SplitConfigStringSlice splits a comma-separated string into trimmed,
// non-empty segments.
// SplitConfigStringSlice 拆分配置stringslice。
func SplitConfigStringSlice(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return TrimStrings(strings.Split(value, ","))
}

// TrimStrings trims whitespace from each string and discards empty entries.
// TrimStrings 处理裁剪strings。
func TrimStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
