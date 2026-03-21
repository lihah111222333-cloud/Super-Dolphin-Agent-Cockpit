package claudecli

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

func resolveBinaryPath() string {
	if bin := strings.TrimSpace(os.Getenv("CLAUDE_CLI_BIN")); bin != "" {
		return bin
	}
	return defaultClaudeCLIBin
}

func resolveBinaryDir(cwd string, cfg map[string]any) string {
	if dir := configString(cfg, "binary_dir", "binaryDir"); dir != "" {
		return dir
	}
	if exe, err := os.Executable(); err == nil {
		return filepath.Dir(exe)
	}
	if bin, err := exec.LookPath("go-agent-mcp-lsp"); err == nil {
		return filepath.Dir(bin)
	}
	return strings.TrimSpace(cwd)
}

func configFromMap(cfg map[string]any) cliLaunchConfig {
	return cliLaunchConfig{
		ApprovalPolicy:        configString(cfg, "approval_policy", "approvals"),
		Sandbox:               configString(cfg, "sandbox"),
		Summary:               configString(cfg, "summary"),
		Effort:                configString(cfg, "effort"),
		Personality:           configString(cfg, "personality"),
		DeveloperInstructions: configString(cfg, "developer_instructions", "developerInstructions"),
	}
}

func configString(cfg map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := cfg[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

func configStringSlice(cfg map[string]any, keys ...string) []string {
	for _, key := range keys {
		values, ok := cfg[key]
		if !ok {
			continue
		}
		if out := normalizeConfigStringSlice(values); len(out) > 0 {
			return out
		}
	}
	return nil
}

func normalizeConfigStringSlice(values any) []string {
	switch typed := values.(type) {
	case []string:
		return trimStrings(typed)
	case []any:
		return trimConfigStringValues(typed)
	case string:
		return splitConfigStringSlice(typed)
	default:
		return nil
	}
}

func trimConfigStringValues(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		text, ok := value.(string)
		if !ok {
			continue
		}
		if text = strings.TrimSpace(text); text != "" {
			out = append(out, text)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func splitConfigStringSlice(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return trimStrings(strings.Split(value, ","))
}

func stringMap(raw any) map[string]string {
	input, _ := raw.(map[string]any)
	if len(input) == 0 {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		text, ok := value.(string)
		if !ok {
			continue
		}
		if key = strings.TrimSpace(key); key == "" {
			continue
		}
		if text = strings.TrimSpace(text); text == "" {
			continue
		}
		out[key] = text
	}
	return out
}

func trimStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func copyCapabilities(in dto.CapabilitySet) dto.CapabilitySet {
	out := make(dto.CapabilitySet, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func fallbackThreadID(agentID, threadID string) string {
	if threadID = strings.TrimSpace(threadID); threadID != "" {
		return threadID
	}
	if agentID = strings.TrimSpace(agentID); agentID != "" {
		return agentID
	}
	return shared.NewID("claude")
}
