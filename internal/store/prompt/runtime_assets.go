package prompt

import (
	"encoding/json"
	"log/slog"
	"strings"
)

func IsRuntimeAssetTemplate(template PromptTemplate) bool {
	if strings.TrimSpace(template.AgentKey) == "default_rule" {
		return true
	}
	for _, tag := range TemplateTags(template.Tags) {
		switch strings.TrimSpace(tag) {
		case "intent:recall", "intent:default_rule":
			return true
		}
	}
	return false
}

func TemplateTags(raw json.RawMessage) []string {
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		slog.Warn("prompt: TemplateTags unmarshal failed, returning nil tag slice",
			slog.Int("raw_len", len(raw)),
			slog.String("error", err.Error()),
		)
		return nil
	}
	return tags
}
