package thread

import (
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
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
			Name:        providershared.ConfigString(typed, "name"),
			Description: providershared.ConfigString(typed, "description"),
			Prompt:      providershared.ConfigString(typed, "prompt"),
			Source:      providershared.ConfigString(typed, "source"),
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

func cloneOutputStyleConfig(style contract.OutputStyleConfig) *contract.OutputStyleConfig {
	cloned := style
	cloned.KeepCodingInstructions = cloneOptionalBool(style.KeepCodingInstructions)
	if strings.TrimSpace(cloned.Name) == "" &&
		strings.TrimSpace(cloned.Description) == "" &&
		strings.TrimSpace(cloned.Prompt) == "" &&
		strings.TrimSpace(cloned.Source) == "" &&
		cloned.KeepCodingInstructions == nil {
		return nil
	}
	return &cloned
}

func styleKeepCodingInstructions(style *contract.OutputStyleConfig) *bool {
	if style == nil {
		return nil
	}
	return cloneOptionalBool(style.KeepCodingInstructions)
}
