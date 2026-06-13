package logger

import "fmt"

// DefaultTruncateLen is the default max length for non-debug log payloads.
const DefaultTruncateLen = 512

// TruncateForLog truncates s to maxLen, appending a length indicator.
// Returns s unchanged if within limit or if debug logging is enabled.
// TruncateForLog 为日志截断日志。
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
