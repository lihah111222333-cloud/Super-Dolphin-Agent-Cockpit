package tools

import (
	"context"
	"encoding/json"
)

// ToolHandler describes a tools API type.
type ToolHandler func(ctx context.Context, input json.RawMessage) (any, error)

// Schema describes a tools API type.
type Schema map[string]any

// ToolDefinition describes a tools API type.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema Schema      `json:"input_schema"`
	Handler     ToolHandler `json:"-"`
}

type listEnvelope[T any] struct {
	Data      []T    `json:"data"`
	Total     int    `json:"total"`
	Showing   int    `json:"showing"`
	Truncated bool   `json:"truncated"`
	Hint      string `json:"hint,omitempty"`
}

func newListEnvelope[T any](items []T, limit int, hint string) listEnvelope[T] {
	return listEnvelope[T]{
		Data:      items,
		Total:     len(items),
		Showing:   len(items),
		Truncated: limit > 0 && len(items) >= limit,
		Hint:      hint,
	}
}

func successResult(fields map[string]any) map[string]any {
	result := map[string]any{"success": true}
	for key, value := range fields {
		if value != nil {
			result[key] = value
		}
	}
	return result
}

// StringSchema 构建字符串参数 schema。
func StringSchema(description string) Schema {
	return scalarSchema("string", description)
}

// IntegerSchema 构建整数参数 schema。
func IntegerSchema(description string) Schema {
	return scalarSchema("integer", description)
}

// BooleanSchema 构建布尔参数 schema。
func BooleanSchema(description string) Schema {
	return scalarSchema("boolean", description)
}

// EnumStringSchema 构建枚举字符串参数 schema。
func EnumStringSchema(description string, values ...string) Schema {
	schema := StringSchema(description)
	schema["enum"] = append([]string(nil), values...)
	return schema
}

// EnumValues 从 Schema 反取 "enum" 字段（StringSchema enum 切片），
// 给 handler 层 requireEnum 做单源驱动：schema 和 handler 共用同一份枚举值，
// 避免「schema 写一份、handler 写一份」造成 drift。
//
// 仅识别 []string 与 []any（元素为 string）两种形状；其他类型直接返 nil，
// 调用方应保证 schema 用 EnumStringSchema 构造（已在单测覆盖）。
//
// EnumValues extracts the "enum" slice from a Schema so the handler layer
// (requireEnum) and the schema share one source of truth. Returns nil when
// the field is absent or has an unexpected shape; callers should pair it
// with a schema built via EnumStringSchema and cover the wiring in tests.
func EnumValues(s Schema) []string {
	if s == nil {
		return nil
	}
	raw, ok := s["enum"]
	if !ok || raw == nil {
		return nil
	}
	switch v := raw.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		out := make([]string, 0, len(v))
		for _, item := range v {
			str, ok := item.(string)
			if !ok {
				return nil
			}
			out = append(out, str)
		}
		return out
	default:
		return nil
	}
}

// ArraySchema 构建数组参数 schema。
func ArraySchema(items Schema, description string) Schema {
	schema := Schema{"type": "array", "items": map[string]any(items)}
	if description != "" {
		schema["description"] = description
	}
	return schema
}

// ObjectSchema 构建对象参数 schema。
func ObjectSchema(properties map[string]Schema, required ...string) Schema {
	schema := Schema{
		"type":                 "object",
		"properties":           schemaProperties(properties),
		"additionalProperties": false,
	}
	if len(required) > 0 {
		schema["required"] = append([]string(nil), required...)
	}
	return schema
}

// RawObjectSchema 处理原始objectschema。
func RawObjectSchema(description string) Schema {
	schema := Schema{"type": "object", "additionalProperties": true}
	if description != "" {
		schema["description"] = description
	}
	return schema
}

func scalarSchema(kind, description string) Schema {
	schema := Schema{"type": kind}
	if description != "" {
		schema["description"] = description
	}
	return schema
}

func schemaProperties(properties map[string]Schema) map[string]any {
	mapped := make(map[string]any, len(properties))
	for key, value := range properties {
		mapped[key] = map[string]any(value)
	}
	return mapped
}
