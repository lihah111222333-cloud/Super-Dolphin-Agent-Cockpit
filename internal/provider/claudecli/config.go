package claudecli

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
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
		DisallowedTools:       disallowedBuiltinToolsFromMap(cfg),
	}
}

// disallowedBuiltinToolsFromMap preserves nil when no override key is present
// so callers fall back to the legacy default disallow list, while an explicit
// empty array in the config map yields a non-nil empty slice meaning "enable
// every upstream built-in tool".
func disallowedBuiltinToolsFromMap(cfg map[string]any) []string {
	for _, key := range []string{"disallowed_tools", "disallowedTools", "disallowed_builtin_tools", "disallowedBuiltinTools"} {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		ids := providershared.NormalizeConfigStringSlice(raw)
		if ids == nil {
			return []string{}
		}
		return ids
	}
	return nil
}

func resolveStartAssembly(req dto.StartSessionRequest, cfg cliLaunchConfig, provider string) contract.StartAssembly {
	assembly := req.StartAssembly
	baseInstructions := promptSnapshotBaseInstructions(assembly.Snapshot, req.Instructions)
	if value := strings.TrimSpace(assembly.BaseInstructions); value != "" {
		baseInstructions = value
	}
	developerInstructions := promptDeveloperInstructions(cliLaunchConfig{
		DeveloperInstructions: cfg.DeveloperInstructions,
		PromptSnapshot:        assembly.Snapshot,
	})
	if value := strings.TrimSpace(assembly.DeveloperInstructions); value != "" {
		developerInstructions = value
	}
	assembly.BaseInstructions = baseInstructions
	assembly.DeveloperInstructions = developerInstructions
	assembly.Snapshot = normalizePromptSnapshot(assembly.Snapshot, baseInstructions, developerInstructions, provider)
	return assembly
}

func normalizePromptSnapshot(
	snapshot contract.PromptAssemblySnapshot,
	baseInstructions string,
	developerInstructions string,
	provider string,
) contract.PromptAssemblySnapshot {
	if strings.TrimSpace(snapshot.BaseInstructions) == "" {
		snapshot.BaseInstructions = strings.TrimSpace(baseInstructions)
	}
	if strings.TrimSpace(snapshot.DeveloperInstructions) == "" {
		snapshot.DeveloperInstructions = strings.TrimSpace(developerInstructions)
	}
	if strings.TrimSpace(snapshot.Provider) == "" {
		snapshot.Provider = strings.TrimSpace(provider)
	}
	if snapshot.Version == 0 {
		snapshot.Version = contract.PromptAssemblySnapshotVersion
	}
	return snapshot
}

func copyCapabilities(in dto.CapabilitySet) dto.CapabilitySet {
	out := make(dto.CapabilitySet, len(in))
	maps.Copy(out, in)
	return out
}

func cloneConfigMap(cfg map[string]any) map[string]any {
	if len(cfg) == 0 {
		return nil
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		out := make(map[string]any, len(cfg))
		maps.Copy(out, cfg)
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		out = make(map[string]any, len(cfg))
		maps.Copy(out, cfg)
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
