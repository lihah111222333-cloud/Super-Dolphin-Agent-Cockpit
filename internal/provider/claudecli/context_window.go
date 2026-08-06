package claudecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// claudeLatestLongModelAliases 返回前端最新长模型名到 Claude CLI 短别名的映射。
func claudeLatestLongModelAliases() map[string]string {
	return map[string]string{
		"claude-opus-4-7":       "opus",
		"claude-opus-4-7[1m]":   "opus[1m]",
		"claude-sonnet-4-7":     "sonnet",
		"claude-sonnet-4-7[1m]": "sonnet[1m]",
		"claude-haiku-4-5":      "haiku",
	}
}

// claudeContextWindow 优先使用 runtime 上报窗口，缺省时按启动模型估算。
func claudeContextWindow(runtimeWindow int, model string, history *historyBackend) int {
	if runtimeWindow > 0 {
		return runtimeWindow
	}
	return claudeModelContextWindow(claudeLaunchDisplayModel(model, history))
}

// claudeLaunchDisplayModel 返回用于展示和窗口估算的规范模型名。
func claudeLaunchDisplayModel(model string, history *historyBackend) string {
	if model = sanitizeClaudeModel(model); model != "" {
		return model
	}
	return sanitizeClaudeModel(readClaudeSettingsModel(history))
}

// sanitizeClaudeModel 清理 UI 可能传入的空对象字符串，并归一化最新模型别名。
func sanitizeClaudeModel(model string) string {
	model = strings.TrimSpace(model)
	switch strings.ToLower(model) {
	case "", "[object object]", "undefined", "null":
		return ""
	default:
		if alias := claudeLatestLongModelAliases()[strings.ToLower(model)]; alias != "" {
			return alias
		}
		return model
	}
}

// claudeModelContextWindow 按 Claude 模型族估算可用上下文窗口，并预留输出余量。
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

// readClaudeSettingsModel 从 Claude home 的 settings.json 中读取默认模型。
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
