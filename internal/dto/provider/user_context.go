package provider

import (
	"sort"
	"strings"
)

var preferredUserContextKeys = []string{
	"claudeMd",
	"currentDate",
	"workerToolsContext",
	"terminalFocus",
	"runtimeExtras",
}

func FormatUserContextText(payload map[string]string) string {
	normalized := normalizeUserContext(payload)
	if len(normalized) == 0 {
		return ""
	}
	blocks := make([]string, 0, len(normalized))
	for _, key := range orderedUserContextKeys(normalized) {
		if block := renderUserContextSection(key, normalized[key]); block != "" {
			blocks = append(blocks, block)
		}
	}
	return strings.TrimSpace(strings.Join(blocks, "\n\n"))
}

func (a TurnAssembly) RenderUserContextMessage() string {
	if text := FormatUserContextText(a.UserContext); text != "" {
		return wrapSystemReminder(text)
	}
	return wrapSystemReminder(a.UserContextText)
}

func orderedUserContextKeys(payload map[string]string) []string {
	seen := make(map[string]struct{}, len(payload))
	ordered := make([]string, 0, len(payload))
	for _, key := range preferredUserContextKeys {
		if _, ok := payload[key]; ok {
			ordered = append(ordered, key)
			seen[key] = struct{}{}
		}
	}
	extra := make([]string, 0, len(payload))
	for key := range payload {
		if _, ok := seen[key]; ok {
			continue
		}
		extra = append(extra, key)
	}
	sort.Strings(extra)
	return append(ordered, extra...)
}

func normalizeUserContext(payload map[string]string) map[string]string {
	if len(payload) == 0 {
		return nil
	}
	normalized := make(map[string]string, len(payload))
	for key, value := range payload {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		normalized[key] = value
	}
	if len(normalized) == 0 {
		return nil
	}
	return normalized
}

func renderUserContextSection(key, body string) string {
	key = strings.TrimSpace(key)
	body = strings.TrimSpace(body)
	if key == "" || body == "" {
		return ""
	}
	return "# " + key + "\n" + body
}

func wrapSystemReminder(text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	if strings.HasPrefix(text, "<system-reminder>") {
		return text
	}
	return strings.Join([]string{"<system-reminder>", text, "</system-reminder>"}, "\n\n")
}
