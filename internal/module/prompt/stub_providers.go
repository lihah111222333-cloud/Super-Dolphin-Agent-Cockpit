package prompt

import (
	"os"
	"strings"
)

// promptFeatureEnabled 同时支持环境变量和 session flag 两种 feature gate。
func promptFeatureEnabled(flags map[string]bool, envKeys []string, flagNames ...string) bool {
	return promptEnvEnabled(envKeys...) || promptFlagEnabled(flags, flagNames...)
}

// promptEnvEnabled 判断任一环境变量是否按布尔规则开启。
func promptEnvEnabled(keys ...string) bool {
	for _, key := range keys {
		if parseBoolEnv(key, false) {
			return true
		}
	}
	return false
}

// promptFlagEnabled 判断任一规范化 session flag 是否开启。
func promptFlagEnabled(flags map[string]bool, names ...string) bool {
	if len(flags) == 0 || len(names) == 0 {
		return false
	}
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		if normalized := normalizePromptFlag(name); normalized != "" {
			wanted[normalized] = struct{}{}
		}
	}
	for name, enabled := range flags {
		if !enabled {
			continue
		}
		if _, ok := wanted[normalizePromptFlag(name)]; ok {
			return true
		}
	}
	return false
}

// promptUserType 读取当前用户类型，兼容大小写不同的环境变量名。
func promptUserType() string {
	for _, key := range []string{"USER_TYPE", "user_type"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

// normalizePromptFlag 去掉分隔符并转小写，允许 tokenBudget/token_budget/token-budget 等写法等价。
func normalizePromptFlag(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	return replacer.Replace(name)
}
