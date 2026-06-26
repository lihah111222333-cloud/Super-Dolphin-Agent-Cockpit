// Package handoffrender 提供线程交接（handoff）场景下的文本规范化辅助函数。
package handoffrender

import (
	"strings"

	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

// ThreadStatus 从 thread store 行中取出交接展示用状态。
// nil 行返回空字符串，让渲染层可以统一跳过缺失字段。
func ThreadStatus(row *threadstore.Thread) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.Status)
}

// ThreadID 从 thread store 行中取出交接展示用 thread id。
func ThreadID(row *threadstore.Thread) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.ThreadID)
}

// ThreadCWD 从 thread store 行中取出交接展示用工作目录。
func ThreadCWD(row *threadstore.Thread) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.Cwd)
}

// NormalizeText 将多行和多空白压成单行文本，供 handoff 摘要稳定渲染。
func NormalizeText(raw string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n")), " ")
}

// TruncateText 按 rune 截断展示文本并补省略号，避免中文被按 byte 切坏。
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
