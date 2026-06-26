// Package configutil 提供从 map[string]any 中读取配置值的纯工具函数。
// 该包不依赖业务层，provider 和 module 都可以复用而不引入循环依赖。
package configutil

import (
	"fmt"
	"strings"
)

// ConfigString 按候选键读取宽松字符串配置，并过滤常见前端哨兵值。
// 仅用于兼容历史 UI 载荷；需要类型错误 fail-fast 的路径应使用 StrictString。
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

// StrictString 读取第一个存在的字符串键；键存在但类型错误时 fail-fast。
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

// StrictBool 读取第一个存在的 bool 键；键存在但类型错误时 fail-fast。
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

// StrictValue 返回第一个存在的原始配置值和命中的键名。
// 它只判断键是否存在，类型约束交给 StrictString/StrictBool 这类上层读取器。
func StrictValue(cfg map[string]any, keys ...string) (any, string, bool) {
	for _, key := range keys {
		if value, ok := cfg[key]; ok {
			return value, key, true
		}
	}
	return nil, "", false
}

// SanitizeConfigString 去掉空白，并过滤 "undefined"、"null" 等前端哨兵字符串。
func SanitizeConfigString(value string) string {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "", "[object object]", "undefined", "null":
		return ""
	default:
		return value
	}
}

// StringMap 将 JSON 风格 map 转为 string map，只保留非空字符串键值。
// 这是给松散前端 metadata 使用的兼容转换，严格配置字段不应依赖它吞掉类型错误。
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

// ConfigStringSlice 按候选键读取宽松字符串列表配置。
// 它兼容 []string、[]any 和逗号分隔字符串，严格配置读取应在调用方先校验类型。
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

// NormalizeConfigStringSlice 将 []string、[]any 或逗号分隔字符串规范化为 []string。
// 不支持的类型返回 nil，仅用于兼容路径，不能替代业务配置的显式校验。
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

// TrimConfigStringValues 从 []any 中提取非空字符串，忽略非字符串元素。
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

// SplitConfigStringSlice 将逗号分隔字符串拆成去空白后的非空片段。
func SplitConfigStringSlice(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return TrimStrings(strings.Split(value, ","))
}

// TrimStrings 去掉每个字符串的首尾空白并丢弃空项。
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
