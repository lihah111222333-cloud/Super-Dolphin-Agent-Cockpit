package logger

import "fmt"

// DefaultTruncateLen is the default max length for non-debug log payloads.
const DefaultTruncateLen = 512

// TruncateForLog truncates s to maxLen, appending a length indicator.
// Returns s unchanged if within limit or if debug logging is enabled.
func TruncateForLog(s string, maxLen int) string {
	if maxLen <= 0 {
		maxLen = DefaultTruncateLen
	}
	if len(s) <= maxLen || IsDebugEnabled() {
		return s
	}
	return s[:maxLen] + fmt.Sprintf("...[truncated, len=%d]", len(s))
}
