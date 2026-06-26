package logger

import "fmt"

// DefaultTruncateLen 是非 debug 日志 payload 的默认截断长度。
const DefaultTruncateLen = 512

// TruncateForLog 在非 debug 模式下截断过长日志字符串，并追加原始长度。
// debug 日志开启或字符串未超限时保持原文，方便排障时查看完整内容。
func TruncateForLog(s string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = DefaultTruncateLen
	}
	if IsDebugEnabled() || len(s) <= maxLen {
		return s
	}
	truncated := s[:maxLen]
	return truncated + fmt.Sprintf("...[truncated, len=%d]", len(s))
}
