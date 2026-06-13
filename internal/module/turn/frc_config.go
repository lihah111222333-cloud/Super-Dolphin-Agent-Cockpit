package turn

import (
	"strconv"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/util/configutil"
)

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

// normalizeFRCConfig 规范化frc配置。
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

// configInt 处理配置int。
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
