package claudecli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
)

const defaultClaudeCLIBin = "claude"

type cliLaunchConfig struct {
	ApprovalPolicy        string
	Sandbox               string
	Summary               string
	Effort                string
	Personality           string
	DeveloperInstructions string
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
	if resumeID = strings.TrimSpace(resumeID); resumeID != "" {
		args = append(args, "--resume", resumeID)
	}
	tr, err := newTransport(binary, args, cwd, nil)
	if err != nil {
		if cleanup != nil {
			cleanup()
		}
		return nil, nil, err
	}
	return tr, cleanup, nil
}

func buildCLIArgs(model, instructions, mcpConfigPath string, cfg cliLaunchConfig) []string {
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	}
	if model = strings.TrimSpace(model); model != "" {
		args = append(args, "--model", model)
	}
	if prompt := composeLaunchSystemPrompt(instructions, cfg); prompt != "" {
		args = append(args, "--system-prompt", prompt)
	}
	if mode := resolvePermissionMode(cfg.ApprovalPolicy, cfg.Sandbox); mode != "" {
		args = append(args, "--permission-mode", mode)
	}
	if effort := normalizeEffort(cfg.Effort); effort != "" {
		args = append(args, "--effort", effort)
	}
	if mcpConfigPath = strings.TrimSpace(mcpConfigPath); mcpConfigPath != "" {
		args = append(args, "--mcp-config", mcpConfigPath)
		args = append(args, "--disallowedTools", "Read,Write,Edit,MultiEdit,Bash,Grep,Glob,LS")
		if !hasFlag(args, "--permission-mode") {
			args = append(args, "--permission-mode", "bypassPermissions")
		}
	}
	return args
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag {
			return true
		}
	}
	return false
}

func composeLaunchSystemPrompt(instructions string, cfg cliLaunchConfig) string {
	parts := nonEmptyStrings(strings.TrimSpace(instructions), strings.TrimSpace(cfg.DeveloperInstructions))
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

func normalizeEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "", "none":
		return ""
	case "minimal", "low":
		return "low"
	case "medium":
		return "medium"
	case "high", "xhigh":
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
		_ = file.Close()
		cleanup()
		return "", nil, fmt.Errorf("write mcp config: %w", err)
	}
	if err := file.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close mcp config: %w", err)
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
	if name == "" || len(bin.Command) == 0 {
		return "", nil, false
	}
	server := map[string]any{"command": strings.TrimSpace(bin.Command[0])}
	if len(bin.Command) > 1 {
		server["args"] = bin.Command[1:]
	}
	if len(bin.Env) > 0 {
		server["env"] = bin.Env
	}
	if len(bin.AutoApprove) > 0 {
		server["autoApprove"] = append([]string(nil), bin.AutoApprove...)
	}
	if cwd != "" {
		server["cwd"] = cwd
	}
	return name, server, true
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
