// Package identifier 提供 thread/session ID 的形状判断。
// LooksLikeUUID 用于宽松过滤 provider ID；IsClaudeCLISessionUUID 用于 Claude --resume 的严格门禁。
package identifier

import (
	"regexp"
	"strings"
)

// LooksLikeUUID 判断字符串是否像 provider UUID：只允许十六进制和短横线，且至少 32 个十六进制字符。
// 它会拒绝 agent_ 前缀这类内部占位 ID，避免被误送入 provider resume 流程。
func LooksLikeUUID(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 32 {
		return false
	}
	hex := 0
	for _, c := range s {
		switch {
		case (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F'):
			hex++
		case c == '-':
			// 短横线只用于 UUID 分隔，不计入十六进制长度。
		default:
			return false
		}
	}
	return hex >= 32
}

// claudeCLIUUIDRE 只接受 Claude CLI --resume 明确支持的标准 UUID 形状。
// CLI 也接受标题字符串，但它们无法和内部 thread ID 安全区分，所以这里不放行。
var claudeCLIUUIDRE = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// IsClaudeCLISessionUUID 判断字符串是否可作为 Claude CLI --resume 的标准 UUID 参数。
func IsClaudeCLISessionUUID(s string) bool {
	return claudeCLIUUIDRE.MatchString(strings.TrimSpace(s))
}
