package mcpruntime

import (
	"bytes"
	"encoding/json"
)

// StructuredContentForToolResult 为工具结果生成structured内容。
func StructuredContentForToolResult(value any) (json.RawMessage, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return StructuredContentFromRaw(raw)
}

// StructuredContentFromRaw 从原始处理structured内容。
func StructuredContentFromRaw(raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return json.RawMessage(`{}`), nil
	}
	switch trimmed[0] {
	case '{':
		return append(json.RawMessage(nil), trimmed...), nil
	case '[':
		return structuredArrayContent(trimmed)
	default:
		return json.Marshal(map[string]json.RawMessage{"value": append(json.RawMessage(nil), trimmed...)})
	}
}

func structuredArrayContent(raw json.RawMessage) (json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{
		"items": append(json.RawMessage(nil), raw...),
		"total": len(items),
	})
}
