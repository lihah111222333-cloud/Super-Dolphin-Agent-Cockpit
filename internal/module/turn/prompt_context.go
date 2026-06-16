package turn

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

func configOutputStyle(cfg map[string]any, keys ...string) *contract.OutputStyleConfig {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		if style := normalizeOutputStyleConfig(value); style != nil {
			return style
		}
	}
	return nil
}

// normalizeOutputStyleConfig 规范化outputstyle配置。
func normalizeOutputStyleConfig(value any) *contract.OutputStyleConfig {
	switch typed := value.(type) {
	case contract.OutputStyleConfig:
		return cloneOutputStyleConfig(typed)
	case *contract.OutputStyleConfig:
		if typed == nil {
			return nil
		}
		return cloneOutputStyleConfig(*typed)
	case map[string]any:
		style := contract.OutputStyleConfig{
			Name:        kernel.ConfigString(typed, "name"),
			Description: kernel.ConfigString(typed, "description"),
			Prompt:      kernel.ConfigString(typed, "prompt"),
			Source:      kernel.ConfigString(typed, "source"),
		}
		style.KeepCodingInstructions = configOptionalBool(typed, "keepCodingInstructions", "keep_coding_instructions")
		if strings.TrimSpace(style.Name) == "" &&
			strings.TrimSpace(style.Description) == "" &&
			strings.TrimSpace(style.Prompt) == "" &&
			strings.TrimSpace(style.Source) == "" &&
			style.KeepCodingInstructions == nil {
			return nil
		}
		return &style
	default:
		return nil
	}
}

// cloneOutputStyleConfig 复制outputstyle配置。
func cloneOutputStyleConfig(style contract.OutputStyleConfig) *contract.OutputStyleConfig {
	cloned := style
	cloned.KeepCodingInstructions = clonePrepareOptionalBool(style.KeepCodingInstructions)
	if strings.TrimSpace(cloned.Name) == "" &&
		strings.TrimSpace(cloned.Description) == "" &&
		strings.TrimSpace(cloned.Prompt) == "" &&
		strings.TrimSpace(cloned.Source) == "" &&
		cloned.KeepCodingInstructions == nil {
		return nil
	}
	return &cloned
}

func cloneOutputStyleConfigValue(style *contract.OutputStyleConfig) *contract.OutputStyleConfig {
	if style == nil {
		return nil
	}
	return cloneOutputStyleConfig(*style)
}

func firstNonNilOutputStyle(primary, fallback *contract.OutputStyleConfig) *contract.OutputStyleConfig {
	if primary != nil {
		return cloneOutputStyleConfigValue(primary)
	}
	return cloneOutputStyleConfigValue(fallback)
}

func configScratchpadDir(cfg map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := cfg[key].(string)
		if !ok {
			continue
		}
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}

func configOptionalBool(cfg map[string]any, keys ...string) *bool {
	for _, key := range keys {
		value, ok := cfg[key].(bool)
		if ok {
			cloned := value
			return &cloned
		}
	}
	return nil
}

func clonePrepareOptionalBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
