package claudecli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

const (
	defaultClaudeCLIBin = "claude"
	managedMCPPrefix    = "mcp-"
)

type cliLaunchConfig struct {
	ApprovalPolicy        string
	Sandbox               string
	Summary               string
	Effort                string
	Personality           string
	DeveloperInstructions string
	PromptSnapshot        contract.PromptAssemblySnapshot
}

var launchCLI = launchCLIWithManifest

func launchCLIWithManifest(
	binary string,
	cwd string,
	model string,
	instructions string,
	cfg cliLaunchConfig,
	manifest dto.MCPManifest,
	resumeID string,
) (*transport, func(), error) {
	mcpPath, cleanup, err := writeManifestConfig(manifest, cwd)
	if err != nil {
		return nil, nil, err
	}
	args := buildCLIArgs(model, instructions, mcpPath, cfg)
	logManifestLaunch(binary, cwd, model, mcpPath, manifest)
	args = appendFlagIfSet(args, "--resume", resumeID)
	tr, err := newTransport(binary, args, cwd, nil)
	if err != nil {
		return nil, nil, cleanupOnError(err, cleanup)
	}
	return tr, cleanup, nil
}

func logManifestLaunch(binary, cwd, model, mcpPath string, manifest dto.MCPManifest) {
	servers := make([]map[string]any, 0, len(manifest.Binaries))
	for _, bin := range manifest.Binaries {
		serverType := strings.TrimSpace(bin.Type)
		if serverType == "" {
			serverType = "stdio"
		}
		command := ""
		args := []string(nil)
		commandExists := false
		if len(bin.Command) > 0 {
			command = strings.TrimSpace(bin.Command[0])
			args = append(args, bin.Command[1:]...)
			if command != "" {
				_, statErr := os.Stat(command)
				commandExists = statErr == nil
			}
		}
		envKeys := make([]string, 0, len(bin.Env))
		for key := range bin.Env {
			envKeys = append(envKeys, key)
		}
		sort.Strings(envKeys)
		servers = append(servers, map[string]any{
			"name":           strings.TrimSpace(bin.Name),
			"type":           serverType,
			"command":        command,
			"args":           args,
			"command_exists": commandExists,
			"url":            strings.TrimSpace(bin.URL),
			"env_keys":       envKeys,
			"has_rpc_addr":   strings.TrimSpace(bin.Env["GO_AGENT_CTL_RPC_ADDR"]) != "",
			"has_bootstrap":  strings.TrimSpace(bin.Env["GO_AGENT_CTL_BOOTSTRAP_JSON"]) != "",
		})
	}
	pkglogger.Info("claudecli: launch mcp manifest",
		"binary", strings.TrimSpace(binary),
		"cwd", strings.TrimSpace(cwd),
		"model", strings.TrimSpace(model),
		"mcp_config_path", strings.TrimSpace(mcpPath),
		"server_count", len(servers),
		"servers", servers,
	)
}

func buildCLIArgs(model, instructions, mcpConfigPath string, cfg cliLaunchConfig) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	}
	args = appendFlagIfSet(args, "--model", model)
	args = appendFlagIfSet(args, "--system-prompt", composeLaunchSystemPrompt(instructions, cfg))
	args = appendFlagIfSet(args, "--permission-mode", resolvePermissionMode(cfg.ApprovalPolicy, cfg.Sandbox))
	args = appendFlagIfSet(args, "--effort", normalizeEffort(model, cfg.Effort))
	if mcpConfigPath = strings.TrimSpace(mcpConfigPath); mcpConfigPath != "" {
		args = appendFlagIfSet(args, "--mcp-config", mcpConfigPath)
		args = append(args, "--disallowedTools", "Read,Write,Edit,MultiEdit,Bash,Grep,Glob,LS")
		if !hasFlag(args, "--permission-mode") {
			args = appendFlagIfSet(args, "--permission-mode", "bypassPermissions")
		}
	}
	return args
}

func hasFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

func composeLaunchSystemPrompt(instructions string, cfg cliLaunchConfig) string {
	parts := nonEmptyStrings(
		promptBaseInstructions(instructions, cfg.PromptSnapshot),
		promptDeveloperInstructions(cfg),
	)
	meta := make([]string, 0, 5)
	for _, pair := range [][2]string{
		{"approval_policy", cfg.ApprovalPolicy},
		{"sandbox", cfg.Sandbox},
		{"summary", cfg.Summary},
		{"effort", cfg.Effort},
		{"personality", cfg.Personality},
	} {
		if value := strings.TrimSpace(pair[1]); value != "" {
			meta = append(meta, pair[0]+"="+value)
		}
	}
	if len(meta) > 0 {
		parts = append(parts, strings.Join(meta, "\n"))
	}
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func promptBaseInstructions(instructions string, snapshot contract.PromptAssemblySnapshot) string {
	if value := strings.TrimSpace(instructions); value != "" {
		return value
	}
	return strings.TrimSpace(snapshot.BaseInstructions)
}

func promptSnapshotBaseInstructions(snapshot contract.PromptAssemblySnapshot, fallback string) string {
	if value := strings.TrimSpace(snapshot.BaseInstructions); value != "" {
		return value
	}
	return strings.TrimSpace(fallback)
}

func promptDeveloperInstructions(cfg cliLaunchConfig) string {
	if value := strings.TrimSpace(cfg.PromptSnapshot.DeveloperInstructions); value != "" {
		return value
	}
	return strings.TrimSpace(cfg.DeveloperInstructions)
}

func promptSnapshotBlank(snapshot contract.PromptAssemblySnapshot) bool {
	return strings.TrimSpace(snapshot.DisplayName) == "" &&
		strings.TrimSpace(snapshot.BaseInstructions) == "" &&
		strings.TrimSpace(snapshot.DeveloperInstructions) == "" &&
		strings.TrimSpace(snapshot.Provider) == "" &&
		snapshot.Version == 0 &&
		strings.TrimSpace(snapshot.Hash) == "" &&
		snapshot.Generation == 0
}

func resolvePermissionMode(approvalPolicy, sandbox string) string {
	if mode := permissionModeFromSandbox(sandbox); mode != "" {
		return mode
	}
	switch strings.ToLower(strings.TrimSpace(approvalPolicy)) {
	case "", "never", "on-request", "always", "auto":
		return "bypassPermissions"
	case "on-failure", "untrusted":
		return "default"
	default:
		return "bypassPermissions"
	}
}

func permissionModeFromSandbox(sandbox string) string {
	raw := strings.TrimSpace(sandbox)
	if raw == "" {
		return ""
	}
	if strings.HasPrefix(raw, "{") {
		var payload struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err == nil {
			raw = payload.Type
		}
	}
	raw = strings.Trim(raw, "\"")
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(raw, "-", ""), "_", ""))
	switch normalized {
	case "dangerfullaccess":
		return "bypassPermissions"
	case "workspacewrite":
		return "acceptEdits"
	case "readonly":
		return "default"
	default:
		return ""
	}
}

func normalizeEffort(model, effort string) string {
	normalizedModel := strings.ToLower(strings.TrimSpace(model))
	if normalizedModel == "best" {
		normalizedModel = "opus"
	}
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "none":
		return ""
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high", "xhigh":
		return "high"
	case "max":
		if strings.Contains(normalizedModel, "opus") {
			return "max"
		}
		return "high"
	default:
		return strings.TrimSpace(effort)
	}
}

func writeManifestConfig(manifest dto.MCPManifest, cwd string) (string, func(), error) {
	servers := manifestServers(manifest, cwd)
	if len(servers) == 0 {
		return "", nil, nil
	}
	raw, err := json.Marshal(map[string]any{"mcpServers": servers})
	if err != nil {
		return "", nil, fmt.Errorf("marshal mcp config: %w", err)
	}
	file, err := os.CreateTemp("", "super-agent-claude-mcp-*.json")
	if err != nil {
		return "", nil, fmt.Errorf("create mcp config: %w", err)
	}
	path := file.Name()
	cleanup := func() { _ = os.Remove(path) }
	if _, err := file.Write(raw); err != nil {
		return "", nil, cleanupOnError(
			fmt.Errorf("write mcp config: %w", err),
			func() { _ = file.Close() },
			cleanup,
		)
	}
	if err := file.Close(); err != nil {
		return "", nil, cleanupOnError(fmt.Errorf("close mcp config: %w", err), cleanup)
	}
	return path, cleanup, nil
}

func manifestServers(manifest dto.MCPManifest, cwd string) map[string]any {
	cwd = strings.TrimSpace(cwd)
	servers := make(map[string]any, len(manifest.Binaries))
	for _, bin := range manifest.Binaries {
		name, server, ok := manifestServer(bin, cwd)
		if ok {
			servers[name] = server
		}
	}
	return servers
}

func manifestServer(bin dto.MCPBinary, cwd string) (string, map[string]any, bool) {
	name := strings.TrimSpace(bin.Name)
	if name == "" {
		return "", nil, false
	}

	// HTTP mode: peer process already running, just point Claude at the URL.
	if strings.TrimSpace(bin.Type) == "http" && strings.TrimSpace(bin.URL) != "" {
		server := map[string]any{
			"type": "http",
			"url":  strings.TrimSpace(bin.URL),
		}
		applyAutoApprove(server, bin.AutoApprove)
		return name, server, true
	}

	// stdio mode: validate command and ensure it's a managed MCP binary.
	server, ok := buildStdioServer(bin, cwd)
	if !ok {
		return "", nil, false
	}
	return name, server, true
}

func buildStdioServer(bin dto.MCPBinary, cwd string) (map[string]any, bool) {
	if len(bin.Command) == 0 {
		return nil, false
	}
	command := strings.TrimSpace(bin.Command[0])
	if command == "" {
		return nil, false
	}
	// Accept both short family names ("lsp", "orch") produced by
	// BuildManifest since P14, and legacy prefixed names ("mcp-lsp").
	// The binary on disk always keeps the "mcp-" prefix.
	if !strings.HasPrefix(filepath.Base(command), managedMCPPrefix) {
		return nil, false
	}
	server := map[string]any{"command": command}
	if len(bin.Command) > 1 {
		server["args"] = bin.Command[1:]
	}
	if len(bin.Env) > 0 {
		server["env"] = bin.Env
	}
	applyAutoApprove(server, bin.AutoApprove)
	if cwd != "" {
		server["cwd"] = cwd
	}
	return server, true
}

func applyAutoApprove(server map[string]any, autoApprove []string) {
	if len(autoApprove) > 0 {
		server["autoApprove"] = append([]string(nil), autoApprove...)
	}
}

func nonEmptyStrings(values ...string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}
