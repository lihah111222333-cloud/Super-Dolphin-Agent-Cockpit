package contract

import (
	"sort"
	"strings"
)

const defaultFRCKeepRecent = 2

type FRCConfig struct {
	Enabled                      bool
	SystemPromptSuggestSummaries bool
	SupportedModels              []string
	KeepRecent                   int
}

// Normalize 规范化跨模块契约。
func (c *FRCConfig) Normalize() *FRCConfig {
	if c == nil {
		return nil
	}
	out := &FRCConfig{
		Enabled:                      c.Enabled,
		SystemPromptSuggestSummaries: c.SystemPromptSuggestSummaries,
		KeepRecent:                   c.KeepRecent,
	}
	if out.KeepRecent <= 0 {
		out.KeepRecent = defaultFRCKeepRecent
	}
	seen := make(map[string]struct{}, len(c.SupportedModels))
	for _, model := range c.SupportedModels {
		normalized := strings.ToLower(strings.TrimSpace(model))
		if normalized == "" {
			continue
		}
		if _, ok := seen[normalized]; ok {
			continue
		}
		seen[normalized] = struct{}{}
		out.SupportedModels = append(out.SupportedModels, normalized)
	}
	if len(out.SupportedModels) > 1 {
		sort.Strings(out.SupportedModels)
	}
	return out
}

// EnabledForModel 为模型判断跨模块契约。
func (c *FRCConfig) EnabledForModel(model string) bool {
	normalized := c.Normalize()
	if normalized == nil || !normalized.Enabled {
		return false
	}
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" || len(normalized.SupportedModels) == 0 {
		return false
	}
	index := sort.SearchStrings(normalized.SupportedModels, model)
	return index < len(normalized.SupportedModels) && normalized.SupportedModels[index] == model
}

// KeepRecentCount 处理keeprecentcount。
func (c *FRCConfig) KeepRecentCount() int {
	normalized := c.Normalize()
	if normalized == nil {
		return defaultFRCKeepRecent
	}
	return normalized.KeepRecent
}
