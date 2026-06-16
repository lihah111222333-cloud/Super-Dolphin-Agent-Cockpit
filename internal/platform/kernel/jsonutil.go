package kernel

import (
	"bytes"
	"encoding/json"
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
)

// DecodeInput 解码input。
func DecodeInput(input json.RawMessage, dst any) error {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		trimmed = []byte("{}")
	}
	return json.Unmarshal(trimmed, dst)
}

// CloneSelector 复制selector。
func CloneSelector(selector mcp.Selector) mcp.Selector {
	cloned := selector
	if selector.Scope != nil {
		scope := *selector.Scope
		cloned.Scope = &scope
	}
	return cloned
}

// CloneHookPayload 复制hook载荷。
func CloneHookPayload(payload mcp.HookPayload) mcp.HookPayload {
	cloned := payload
	cloned.Context = CloneRawMessage(payload.Context)
	return cloned
}

// CloneStrings delegates to clone.Strings.
// CloneStrings 复制strings。
func CloneStrings(input []string) []string { return clone.Strings(input) }

// CloneStringMap delegates to clone.StringMap.
// CloneStringMap 复制stringmap。
func CloneStringMap(input map[string]string) map[string]string { return clone.StringMap(input) }

// FilterKeys 处理过滤条件键。
func FilterKeys(payload map[string]any, keys []string) map[string]any {
	if len(keys) == 0 {
		return payload
	}
	filtered := make(map[string]any, len(keys))
	for _, key := range keys {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if value, ok := payload[key]; ok {
			filtered[key] = value
		}
	}
	return filtered
}

// CloneRawMessage delegates to clone.RawMessage.
// CloneRawMessage 复制原始消息。
func CloneRawMessage(message json.RawMessage) json.RawMessage { return clone.RawMessage(message) }

// CloneJSONMap delegates to clone.JSONMap.
// CloneJSONMap 复制JSONmap。
func CloneJSONMap(input map[string]any) map[string]any { return clone.JSONMap(input) }

// CloneRuntimeConfigMap delegates to clone.RuntimeConfigMap.
// CloneRuntimeConfigMap 复制运行时配置map。
func CloneRuntimeConfigMap(cfg map[string]any) map[string]any { return clone.RuntimeConfigMap(cfg) }

// NormalizeAbsolutePath 规范化absolute路径。
func NormalizeAbsolutePath(path string) (string, error) {
	return pathutil.NormalizeAbsolutePath(path)
}
