package claudecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

var claudeLatestLongModelAliases = map[string]string{
	"claude-opus-4-7":       "opus",
	"claude-opus-4-7[1m]":   "opus[1m]",
	"claude-sonnet-4-7":     "sonnet",
	"claude-sonnet-4-7[1m]": "sonnet[1m]",
	"claude-haiku-4-5":      "haiku",
}

func claudeContextWindow(runtimeWindow int, model string, history *historyBackend) int {
	if runtimeWindow > 0 {
		return runtimeWindow
	}
	return claudeModelContextWindow(claudeLaunchDisplayModel(model, history))
}

func claudeLaunchDisplayModel(model string, history *historyBackend) string {
	if model = sanitizeClaudeModel(model); model != "" {
		return model
	}
	return sanitizeClaudeModel(readClaudeSettingsModel(history))
}

func sanitizeClaudeModel(model string) string {
	model = strings.TrimSpace(model)
	switch strings.ToLower(model) {
	case "", "[object object]", "undefined", "null":
		return ""
	default:
		if alias := claudeLatestLongModelAliases[strings.ToLower(model)]; alias != "" {
			return alias
		}
		return model
	}
}

// claudeModelContextWindow 处理claude模型上下文window。
func claudeModelContextWindow(model string) int {
	normalized := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(normalized, "haiku"):
		return 400_000 - 64_000
	case strings.Contains(normalized, "opus") && strings.HasSuffix(normalized, "[1m]"):
		return 1_000_000 - 128_000
	case normalized == "best" || strings.Contains(normalized, "opus"):
		return 400_000 - 128_000
	case strings.HasSuffix(normalized, "[1m]"):
		return 1_000_000 - 64_000
	default:
		return 400_000 - 64_000
	}
}

func readClaudeSettingsModel(history *historyBackend) string {
	if history == nil {
		return ""
	}
	root, err := history.rootDir()
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		return ""
	}
	var payload struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ""
	}
	return strings.TrimSpace(payload.Model)
}
