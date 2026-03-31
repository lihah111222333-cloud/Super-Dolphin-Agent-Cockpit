package claudecli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

// resolveAbsCWD ensures CWD is an absolute path.
func resolveAbsCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		if wd, err := os.Getwd(); err == nil {
			return wd
		}
		return "."
	}
	if filepath.IsAbs(cwd) {
		return cwd
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return abs
	}
	return cwd
}

func resolveBinaryPath() string {
	if bin := strings.TrimSpace(os.Getenv("CLAUDE_CLI_BIN")); bin != "" {
		return bin
	}
	return defaultClaudeCLIBin
}

func configFromMap(cfg map[string]any) cliLaunchConfig {
	return cliLaunchConfig{
		ApprovalPolicy:        providershared.ConfigString(cfg, "approval_policy", "approvals"),
		Sandbox:               providershared.ConfigString(cfg, "sandbox"),
		Summary:               providershared.ConfigString(cfg, "summary"),
		Effort:                providershared.ConfigString(cfg, "effort"),
		Personality:           providershared.ConfigString(cfg, "personality"),
		DeveloperInstructions: providershared.ConfigString(cfg, "developer_instructions", "developerInstructions"),
	}
}

func copyCapabilities(in dto.CapabilitySet) dto.CapabilitySet {
	out := make(dto.CapabilitySet, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneConfigMap(cfg map[string]any) map[string]any {
	if len(cfg) == 0 {
		return nil
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		out := make(map[string]any, len(cfg))
		for key, value := range cfg {
			out[key] = value
		}
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		out = make(map[string]any, len(cfg))
		for key, value := range cfg {
			out[key] = value
		}
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
	return platformshared.NewID("claude")
}
