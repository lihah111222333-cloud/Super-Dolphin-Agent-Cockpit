package turn

import (
	"strings"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/util/configutil"
)

// configOutputStyle 从运行时配置候选键中读取 output style 配置。
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

// normalizeOutputStyleConfig 接受强类型或动态 map，并丢弃完全空的 output style。
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
			Name:        configutil.ConfigString(typed, "name"),
			Description: configutil.ConfigString(typed, "description"),
			Prompt:      configutil.ConfigString(typed, "prompt"),
			Source:      configutil.ConfigString(typed, "source"),
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

// cloneOutputStyleConfig 深拷贝 output style，空配置返回 nil，避免把空对象写进 PrepareInput。
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

// cloneOutputStyleConfigValue 复制指针形式的 output style，nil 输入保持 nil。
func cloneOutputStyleConfigValue(style *contract.OutputStyleConfig) *contract.OutputStyleConfig {
	if style == nil {
		return nil
	}
	return cloneOutputStyleConfig(*style)
}

// firstNonNilOutputStyle 优先返回 primary 的副本，否则返回 fallback 的副本。
func firstNonNilOutputStyle(primary, fallback *contract.OutputStyleConfig) *contract.OutputStyleConfig {
	if primary != nil {
		return cloneOutputStyleConfigValue(primary)
	}
	return cloneOutputStyleConfigValue(fallback)
}

// configScratchpadDir 从运行时配置中读取第一个非空 scratchpad 目录。
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

// configOptionalBool 读取可选 bool，并返回独立指针以避免共享调用方变量。
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

// clonePrepareOptionalBool 复制 bool 指针，用于 PrepareInput 中的可选配置字段。
func clonePrepareOptionalBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
