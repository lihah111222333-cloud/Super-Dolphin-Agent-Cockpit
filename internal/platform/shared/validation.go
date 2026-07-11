package shared

import (
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util"
)

// FirstNonEmpty 返回第一个非空字符串，保持 shared 包旧入口兼容。
func FirstNonEmpty(values ...string) string { return util.FirstNonEmpty(values...) }

// FirstTrimmed 返回第一个 trim 后非空的字符串，保持 shared 包旧入口兼容。
func FirstTrimmed(values ...string) string { return util.FirstTrimmed(values...) }

// ClampLimit 把分页或列表 limit 限制在调用方给定范围内，保持 shared 包旧入口兼容。
func ClampLimit(val, min, max, defaultVal int) int { return util.ClampLimit(val, min, max, defaultVal) }

// FirstPayloadString 按 key 顺序从 payload 中提取第一个可用字符串。
func FirstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := payload[key]; ok {
			if text := payloadString(value); text != "" {
				return text
			}
		}
	}
	return ""
}

// payloadString 从字符串或嵌套消息对象中提取可展示文本。
func payloadString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case map[string]any:
		return FirstPayloadString(typed, "text", "summary", "message", "output", "result")
	default:
		return ""
	}
}
