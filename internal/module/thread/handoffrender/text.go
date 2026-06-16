package handoffrender

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// ThreadStatus 处理线程状态。
func ThreadStatus(row *contract.Thread) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.Status)
}

// ThreadID 处理线程ID。
func ThreadID(row *contract.Thread) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.ThreadID)
}

// ThreadCWD 处理线程工作目录。
func ThreadCWD(row *contract.Thread) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.Cwd)
}

// NormalizeText 规范化文本。
func NormalizeText(raw string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n")), " ")
}

// TruncateText 截断文本。
func TruncateText(raw string, limit int) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || limit <= 0 {
		return ""
	}
	runes := []rune(raw)
	if len(runes) <= limit {
		return raw
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}
