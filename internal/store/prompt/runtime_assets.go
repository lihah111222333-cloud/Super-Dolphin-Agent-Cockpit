package prompt

import (
	"encoding/json"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"strings"
)

// IsRuntimeAssetTemplate 判断运行时assettemplate是否可用。
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

// TemplateTags 处理templatetags。
func TemplateTags(raw json.RawMessage) []string {
	var tags []string
	if err := json.Unmarshal(raw, &tags); err != nil {
		pkglogger.Warn("prompt: TemplateTags unmarshal failed, returning nil tag slice",
			pkglogger.Int("raw_len", len(raw)),
			pkglogger.String("error", err.Error()),
		)
		return nil
	}
	return tags
}
