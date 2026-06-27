package shared

import (
	"encoding/json"
	"strings"

	mcp "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/util/clone"
	"github.com/anthropic-ai/super-agent-v3/internal/util/jsoninput"
	"github.com/anthropic-ai/super-agent-v3/internal/util/pathutil"
)

// DecodeInput 解码工具输入；空值和 null 按空对象处理，减少调用方重复分支。
func DecodeInput(input json.RawMessage, dst any) error {
	return jsoninput.DecodeStrictObject(input, dst)
}

// CloneSelector 深拷贝 Selector 中的可选 Scope，避免调用方共享指针。
func CloneSelector(selector mcp.Selector) mcp.Selector {
	cloned := selector
	if selector.Scope != nil {
		scope := *selector.Scope
		cloned.Scope = &scope
	}
	return cloned
}

// CloneHookPayload 深拷贝 hook payload 的 JSON context。
func CloneHookPayload(payload mcp.HookPayload) mcp.HookPayload {
	cloned := payload
	cloned.Context = CloneRawMessage(payload.Context)
	return cloned
}

// CloneStrings 复制字符串切片，nil 保持 nil。
func CloneStrings(input []string) []string { return clone.Strings(input) }

// CloneStringMap 复制字符串 map，nil 保持 nil。
func CloneStringMap(input map[string]string) map[string]string { return clone.StringMap(input) }

// FilterKeys 按白名单复制 payload 字段；keys 为空时保持原 payload。
func FilterKeys(payload map[string]any, keys []string) map[string]any {
	if len(keys) == 0 {
		return payload
	}
	filtered := make(map[string]any, len(keys))
	for _, key := range keys {
		if key = strings.TrimSpace(key); key == "" {
			continue
		}
		if value, ok := payload[key]; ok {
			filtered[key] = value
		}
	}
	return filtered
}

// CloneRawMessage 复制 JSON 原始字节，避免共享底层切片。
func CloneRawMessage(message json.RawMessage) json.RawMessage { return clone.RawMessage(message) }

// CloneJSONMap 深拷贝 JSON map，适用于运行时 payload 防御性复制。
func CloneJSONMap(input map[string]any) map[string]any { return clone.JSONMap(input) }

// CloneRuntimeConfigMap 深拷贝运行时配置 map，避免 provider 和模块互相改写。
func CloneRuntimeConfigMap(cfg map[string]any) map[string]any { return clone.RuntimeConfigMap(cfg) }

// NormalizeAbsolutePath 规范化绝对路径，保留 pathutil 的平台细节。
func NormalizeAbsolutePath(path string) (string, error) { return pathutil.NormalizeAbsolutePath(path) }
