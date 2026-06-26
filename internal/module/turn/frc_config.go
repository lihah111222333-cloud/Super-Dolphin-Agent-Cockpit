package turn

import (
	"strconv"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util/configutil"
)

// configFRCConfig 从运行时配置 map 的多个候选键读取 FRC 配置，返回第一份可规范化结果。
func configFRCConfig(cfg map[string]any, keys ...string) *contract.FRCConfig {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		if frc := normalizeFRCConfig(value); frc != nil {
			return frc
		}
	}
	return nil
}

// normalizeFRCConfig 接受强类型指针、值或旧版 map 配置，并统一交给 contract 归一化。
func normalizeFRCConfig(value any) *contract.FRCConfig {
	switch typed := value.(type) {
	case contract.FRCConfig:
		return typed.Normalize()
	case *contract.FRCConfig:
		if typed == nil {
			return nil
		}
		return typed.Normalize()
	case map[string]any:
		cfg := contract.FRCConfig{
			Enabled:                      configBool(typed, "enabled"),
			SystemPromptSuggestSummaries: configBool(typed, "systemPromptSuggestSummaries", "system_prompt_suggest_summaries"),
			SupportedModels:              configutil.ConfigStringSlice(typed, "supportedModels", "supported_models"),
			KeepRecent:                   configInt(typed, "keepRecent", "keep_recent"),
		}
		if !cfg.Enabled && !cfg.SystemPromptSuggestSummaries && cfg.KeepRecent == 0 && len(cfg.SupportedModels) == 0 {
			return nil
		}
		return cfg.Normalize()
	default:
		return nil
	}
}

// configInt 从动态配置中读取整数，兼容 JSON float64 和字符串形式的历史输入。
func configInt(cfg map[string]any, keys ...string) int {
	for _, key := range keys {
		switch value := cfg[key].(type) {
		case int:
			return value
		case int32:
			return int(value)
		case int64:
			return int(value)
		case float64:
			return int(value)
		case string:
			if parsed, err := strconv.Atoi(value); err == nil {
				return parsed
			}
		}
	}
	return 0
}
