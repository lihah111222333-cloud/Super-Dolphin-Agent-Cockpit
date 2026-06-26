package contract

import (
	"sort"
	"strings"
)

const defaultFRCKeepRecent = 2

// FRCConfig 是 prompt FRC 功能的跨模块配置。
// SupportedModels 经过 Normalize 后转为小写、去重、排序，便于快速二分匹配。
type FRCConfig struct {
	Enabled                      bool
	SystemPromptSuggestSummaries bool
	SupportedModels              []string
	KeepRecent                   int
}

// Normalize 复制并规范化 FRC 配置。
// nil receiver 返回 nil；KeepRecent 非正数时使用受控默认值，避免调用方自行兜底。
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

// EnabledForModel 判断 FRC 是否对指定模型启用。
// 空模型或空 SupportedModels 都视为未启用，防止配置缺失时扩大生效范围。
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

// KeepRecentCount 返回 FRC 保留最近消息数量。
// nil 配置使用受控默认值，与 Normalize 的默认策略保持一致。
func (c *FRCConfig) KeepRecentCount() int {
	normalized := c.Normalize()
	if normalized == nil {
		return defaultFRCKeepRecent
	}
	return normalized.KeepRecent
}
