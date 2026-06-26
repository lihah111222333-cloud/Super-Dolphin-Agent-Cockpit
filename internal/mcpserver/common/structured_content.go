package common

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

// StructuredContentFromRaw 将工具结果原始 JSON 规范化为 MCP structuredContent。
// 对象原样保留，数组包装为 items，标量包装为 value，空输入返回 nil。
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

// structuredArrayContent 将 JSON 数组包装为带 items/total 字段的对象。
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
