package handoffrender

import (
	"strings"

	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
)

func ThreadStatus(row *threadstore.Thread) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.Status)
}

func ThreadID(row *threadstore.Thread) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.ThreadID)
}

func ThreadCWD(row *threadstore.Thread) string {
	if row == nil {
		return ""
	}
	return strings.TrimSpace(row.Cwd)
}

func NormalizeText(raw string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(strings.TrimSpace(raw), "\r\n", "\n")), " ")
}

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
