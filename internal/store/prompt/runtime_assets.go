package prompt

import (
	"encoding/json"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// IsRuntimeAssetTemplate is kept as a compatibility helper for prompt runtime assets.
func IsRuntimeAssetTemplate(template PromptTemplate) bool {
	return contract.IsRuntimeAssetPromptTemplate(template)
}

// TemplateTags is kept as a compatibility helper for prompt template tags.
func TemplateTags(raw json.RawMessage) []string {
	return contract.PromptTemplateTags(raw)
}
