package prompt

import (
	"encoding/json"
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
		return nil
	}
	return tags
}
