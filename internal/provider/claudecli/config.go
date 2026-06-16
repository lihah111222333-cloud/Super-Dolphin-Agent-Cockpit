package claudecli

import (
	"encoding/json"
	"maps"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/runtimeconfig"
)

// resolveAbsCWD ensures caller-provided CWD is absolute without inventing one.
// resolveAbsCWD 解析abs工作目录。
func resolveAbsCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || cwd == "." {
		return ""
	}
	if runtime.GOOS == "windows" && strings.HasPrefix(cwd, "/") && !strings.HasPrefix(cwd, "//") {
		return cwd
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
		ApprovalPolicy:              providershared.ConfigString(cfg, "approval_policy", "approvals"),
		Sandbox:                     providershared.ConfigString(cfg, "sandbox"),
		Summary:                     providershared.ConfigString(cfg, "summary"),
		Effort:                      providershared.ConfigString(cfg, "effort"),
		Personality:                 providershared.ConfigString(cfg, "personality"),
		DeveloperInstructions:       providershared.ConfigString(cfg, "developer_instructions", "developerInstructions"),
		BuiltinTools:                builtinToolsFromMap(cfg),
		DisallowedTools:             disallowedBuiltinToolsFromMap(cfg),
		AdditionalDisallowedTools:   additionalDisallowedToolsFromMap(cfg),
		DisableProviderNativeSkills: providerNativeSkillsDisabledFromMap(cfg),
	}
}

func builtinToolsFromMap(cfg map[string]any) []string {
	for _, key := range []string{"claude_builtin_tools", "claudeBuiltinTools", "builtin_tools", "builtinTools"} {
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

func additionalDisallowedToolsFromMap(cfg map[string]any) []string {
	for _, key := range []string{"additional_disallowed_tools", "additionalDisallowedTools", "extra_disallowed_tools", "extraDisallowedTools", "claude_additional_disallowed_tools", "claudeAdditionalDisallowedTools"} {
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

// providerNativeSkillsDisabledFromMap 从map处理providernativeskillsdisabled。
func providerNativeSkillsDisabledFromMap(cfg map[string]any) bool {
	for _, key := range []string{"providerNativeSkills", "provider_native_skills"} {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		enabled, ok := raw.(bool)
		return ok && !enabled
	}
	for _, key := range []string{"disableProviderNativeSkills", "disable_provider_native_skills"} {
		raw, ok := cfg[key]
		if !ok {
			continue
		}
		disabled, ok := raw.(bool)
		return ok && disabled
	}
	return false
}

func resolveStartAssembly(req dto.StartSessionRequest, cfg cliLaunchConfig, provider string) contract.StartAssembly {
	assembly := req.StartAssembly
	baseInstructions := promptSnapshotBaseInstructions(assembly.Snapshot, req.Instructions)
	if value := strings.TrimSpace(assembly.BaseInstructions); value != "" {
		baseInstructions = value
	}
	runtimeContext := contract.RenderStartRuntimeContext(assembly)
	baseInstructions = contract.AppendStartRuntimeContext(baseInstructions, assembly)
	developerInstructions := promptDeveloperInstructions(cliLaunchConfig{
		DeveloperInstructions: cfg.DeveloperInstructions,
		PromptSnapshot:        assembly.Snapshot,
	})
	if value := strings.TrimSpace(assembly.DeveloperInstructions); value != "" {
		developerInstructions = value
	}
	assembly.BaseInstructions = baseInstructions
	assembly.DeveloperInstructions = developerInstructions
	if assembly.Snapshot.Boundary == nil && assembly.Boundary != nil {
		boundary := *assembly.Boundary
		assembly.Snapshot.Boundary = &boundary
	}
	assembly.Snapshot = normalizePromptSnapshot(assembly.Snapshot, baseInstructions, developerInstructions, provider)
	assembly.Snapshot = appendRuntimeContextToSnapshotBoundary(assembly.Snapshot, runtimeContext)
	return assembly
}

func appendRuntimeContextToSnapshotBoundary(
	snapshot contract.PromptAssemblySnapshot,
	runtimeContext string,
) contract.PromptAssemblySnapshot {
	runtimeContext = strings.TrimSpace(runtimeContext)
	if runtimeContext == "" || snapshot.Boundary == nil {
		return snapshot
	}
	boundary := *snapshot.Boundary
	boundary.UncachedTail = appendPromptBlock(boundary.UncachedTail, runtimeContext)
	snapshot.Boundary = &boundary
	return snapshot
}

func appendPromptBlock(base, block string) string {
	base = strings.TrimSpace(base)
	block = strings.TrimSpace(block)
	if block == "" {
		return base
	}
	if base == "" {
		return block
	}
	if strings.Contains(base, block) {
		return base
	}
	return base + "\n\n" + block
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
	return ""
}
