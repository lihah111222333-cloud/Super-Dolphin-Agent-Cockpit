package claudecli

import (
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillblocks"
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

// normalizeClaudeHistoryMessage 清理单条 Claude 历史消息中的注入噪声。
// 用户消息会移除开头的系统上下文块；非用户消息只做空内容过滤。
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
	if before, _, ok := strings.Cut(text, "\n已注入"); ok {
		return before
	}
	return text
}

// trimInjectedClaudeSkillBlock 委托公共纯函数裁剪 provider 注入的技能块。
// Claude 和 Codex 必须共用识别规则，否则同一线程跨 provider 恢复时历史会不一致。
func trimInjectedClaudeSkillBlock(text string) string {
	return skillblocks.TrimInjectedSkillBlocks(text)
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

// stripClaudeAgentsMDBlock 去掉 Claude 历史中注入到用户消息开头的 AGENTS.md 块。
// 优先按 instructions 结束标签裁剪，旧格式才退回空行分隔。
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
