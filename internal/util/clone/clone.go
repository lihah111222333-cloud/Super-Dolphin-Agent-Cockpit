// Package clone 提供模块层常用 Go 类型的深拷贝工具。
package clone

import (
	"encoding/json"
	"time"
)

// RawMessage 复制 JSON 原始字节，避免跨模块 DTO 共享可变底层切片。
// 空输入统一返回 nil，保持调用方现有的“未设置”判断。
func RawMessage(message json.RawMessage) json.RawMessage {
	if len(message) == 0 {
		return nil
	}
	return append(json.RawMessage(nil), message...)
}

// Strings 复制字符串切片，避免调用方修改共享配置列表。
// 空输入统一返回 nil，保持配置缺省和空列表的现有兼容行为。
func Strings(input []string) []string {
	if len(input) == 0 {
		return nil
	}
	return append([]string(nil), input...)
}

// StringMap 复制 string map 顶层，供跨模块传递标签、参数等只含标量的配置。
// 空输入统一返回 nil，避免把缺省配置误写成空对象。
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

// JSONMap 递归复制 JSON 风格 map 中的 map 和 slice，避免 runtime 配置被调用方改写。
// 空输入返回空 map，兼容需要继续写入字段的调用路径。
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

// RuntimeConfigMap 深拷贝 runtime 配置 map，JSON 编解码失败时退回浅层 map 拷贝。
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

// Time 复制 time.Time 指针，避免 DTO 调用方共享可修改的时间值。
// nil 输入保持 nil，表示对应时间字段没有被设置。
func Time(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// cloneJSONValue 递归复制 JSON 风格值中的 map 和 slice。
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

// copyMapAny 复制 map 顶层，作为 JSON 深拷贝失败时的保守退路。
func copyMapAny(input map[string]any) map[string]any {
	out := make(map[string]any, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
