package claudecli

import "strings"

const claudeSystemNoiseTrimLeftCutset = "\ufeff \t\r\n"

var claudeSystemNoiseTagPairs = []struct {
	open  string
	close string
}{
	{open: "<environment_context>", close: "</environment_context>"},
	{open: "<instructions>", close: "</instructions>"},
	{open: "<permissions instructions>", close: "</permissions instructions>"},
}

var claudeInjectedSkillMarkers = []struct {
	label         string
	allowContains bool
}{
	{label: "摘要:", allowContains: true},
	{label: "使用方式: ", allowContains: false},
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

func trimInjectedClaudeSkillBlock(text string) string {
	lines := strings.Split(text, "\n")
	for i, raw := range lines {
		line := strings.TrimSpace(raw)
		if strings.HasPrefix(line, "[skill:") && strings.Contains(line, "]") && looksLikeInjectedClaudeSkillBlock(lines, i) {
			return strings.TrimRight(strings.Join(lines[:i], "\n"), "\n")
		}
	}
	return text
}

func looksLikeInjectedClaudeSkillBlock(lines []string, start int) bool {
	if start < 0 || start >= len(lines) {
		return false
	}
	const lookahead = 8
	matched := map[string]bool{}
	markInjectedSkillMarkers(strings.TrimSpace(lines[start]), matched)
	for i := start + 1; i < len(lines) && i <= start+lookahead; i++ {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[skill:") {
			break
		}
		markInjectedSkillMarkers(line, matched)
		if len(matched) == len(claudeInjectedSkillMarkers) {
			return true
		}
	}
	return len(matched) == len(claudeInjectedSkillMarkers)
}

func markInjectedSkillMarkers(line string, matched map[string]bool) {
	for _, marker := range claudeInjectedSkillMarkers {
		if matchClaudeInjectedSkillMarker(line, marker.label, marker.allowContains) {
			matched[marker.label] = true
		}
	}
}

func matchClaudeInjectedSkillMarker(line, marker string, allowContains bool) bool {
	if allowContains {
		return strings.Contains(line, marker)
	}
	return strings.HasPrefix(line, marker)
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
