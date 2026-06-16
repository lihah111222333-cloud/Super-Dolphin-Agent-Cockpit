package claudecli

import (
	"strings"

	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/runtimeconfig"
)

const claudeSystemNoiseTrimLeftCutset = "\ufeff \t\r\n"

var claudeSystemNoiseTagPairs = []struct {
	open  string
	close string
}{
	{open: "<environment_context>", close: "</environment_context>"},
	{open: "<instructions>", close: "</instructions>"},
	{open: "<permissions instructions>", close: "</permissions instructions>"},
}

func normalizeClaudeHistory(messages []Message) []Message {
	out := make([]Message, 0, len(messages))
	for _, msg := range messages {
		normalized, ok := normalizeClaudeHistoryMessage(msg)
		if ok {
			out = append(out, normalized)
		}
	}
	return out
}

// normalizeClaudeHistoryMessage 规范化claudehistory消息。
func normalizeClaudeHistoryMessage(msg Message) (Message, bool) {
	if !strings.EqualFold(strings.TrimSpace(msg.Role), "user") {
		msg.Content = strings.TrimSpace(msg.Content)
		if msg.Content == "" && len(msg.Metadata) == 0 {
			return Message{}, false
		}
		return msg, true
	}
	text := stripSystemNoise(msg.Content)
	trimmedText := strings.TrimSpace(text)
	if trimmedText == "" || isClaudeSystemNoiseText(text) {
		if !shouldKeepEmptyMessage(msg) {
			return Message{}, false
		}
		msg.Content = ""
		return msg, true
	}
	msg.Content = text
	return msg, true
}

func trimInjectedClaudeLSPHint(text string) string {
	if idx := strings.Index(text, "\n已注入"); idx >= 0 {
		return text[:idx]
	}
	return text
}

// trimInjectedClaudeSkillBlock 委托给共享包。P20 Phase 3 两家 provider 统一 trim 逻辑。
func trimInjectedClaudeSkillBlock(text string) string {
	return providershared.TrimInjectedSkillBlocks(text)
}

func isClaudeSystemNoiseText(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	if strings.HasPrefix(lower, "# agents.md") {
		return true
	}
	for _, pair := range claudeSystemNoiseTagPairs {
		if strings.HasPrefix(lower, pair.open) {
			return true
		}
	}
	return false
}

func stripLeadingClaudeSystemNoise(text string) string {
	for current := text; ; {
		next, stripped := stripOneLeadingClaudeSystemNoise(current)
		if !stripped {
			return current
		}
		current = next
		if strings.TrimSpace(current) == "" {
			return ""
		}
	}
}

func stripOneLeadingClaudeSystemNoise(text string) (string, bool) {
	trimmed := strings.TrimLeft(text, claudeSystemNoiseTrimLeftCutset)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "# agents.md") {
		return stripClaudeAgentsMDBlock(trimmed), true
	}
	for _, pair := range claudeSystemNoiseTagPairs {
		if strings.HasPrefix(lower, pair.open) {
			return stripClaudeTagBlock(trimmed, pair.close), true
		}
	}
	return text, false
}

func stripClaudeTagBlock(text, closeTag string) string {
	if idx := strings.Index(strings.ToLower(text), closeTag); idx >= 0 {
		return strings.TrimLeft(text[idx+len(closeTag):], claudeSystemNoiseTrimLeftCutset)
	}
	return ""
}

// stripClaudeAgentsMDBlock 处理stripclaude代理mdblock。
func stripClaudeAgentsMDBlock(text string) string {
	const closeInstructions = "</instructions>"
	lower := strings.ToLower(text)
	if idx := strings.Index(lower, closeInstructions); idx >= 0 {
		return strings.TrimLeft(text[idx+len(closeInstructions):], claudeSystemNoiseTrimLeftCutset)
	}
	idx, width := strings.Index(text, "\n\n"), 2
	if crlf := strings.Index(text, "\r\n\r\n"); idx < 0 || (crlf >= 0 && crlf < idx) {
		idx, width = crlf, 4
	}
	if idx >= 0 {
		return strings.TrimLeft(text[idx+width:], claudeSystemNoiseTrimLeftCutset)
	}
	return ""
}
