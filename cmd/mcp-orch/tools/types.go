package tools

import (
	"bytes"
	"context"
	"encoding/json"
)

type ToolHandler func(ctx context.Context, input json.RawMessage) (any, error)

type Schema map[string]any

type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema Schema      `json:"input_schema"`
	Handler     ToolHandler `json:"-"`
}

func decodeInput(input json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	return json.Unmarshal(trimmed, dst)
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

func StringSchema(description string) Schema {
	return scalarSchema("string", description)
}

func IntegerSchema(description string) Schema {
	return scalarSchema("integer", description)
}

func NumberSchema(description string) Schema {
	return scalarSchema("number", description)
}

func BooleanSchema(description string) Schema {
	return scalarSchema("boolean", description)
}

func EnumStringSchema(description string, values ...string) Schema {
	schema := StringSchema(description)
	schema["enum"] = append([]string(nil), values...)
	return schema
}

func ArraySchema(items Schema, description string) Schema {
	schema := Schema{"type": "array", "items": map[string]any(items)}
	if description != "" {
		schema["description"] = description
	}
	return schema
}

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
