package prompt

import (
	"encoding/json"
	"log/slog"
	"strings"
)

// IsRuntimeAssetTemplate 判断模板是否属于运行时资产模板。
// default_rule agent 或 intent 标签会进入运行时资产目录，普通提示词不会暴露给该列表。
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

// TemplateTags 解析模板 tags JSON。
// 解析失败时记录警告并返回 nil，调用方应把它视为没有可用标签。
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
