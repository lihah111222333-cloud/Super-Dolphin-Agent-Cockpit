package prompt

import (
	"os"
	"strings"
)

func promptFeatureEnabled(flags map[string]bool, envKeys []string, flagNames ...string) bool {
	return promptEnvEnabled(envKeys...) || promptFlagEnabled(flags, flagNames...)
}

func promptEnvEnabled(keys ...string) bool {
	for _, key := range keys {
		if parseBoolEnv(key, false) {
			return true
		}
	}
	return false
}

// promptFlagEnabled 处理promptflagenabled。
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

func promptUserType() string {
	for _, key := range []string{"USER_TYPE", "user_type"} {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func normalizePromptFlag(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	return replacer.Replace(name)
}
