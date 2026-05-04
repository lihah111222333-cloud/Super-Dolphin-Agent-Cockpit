package claudecli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// claudeSessionUUIDRE matches the canonical UUID shape the Claude CLI accepts
// for --resume. The CLI also accepts session titles, but those are arbitrary
// strings and we cannot safely distinguish them from internal thread IDs, so
// only UUIDs are allowed through.
var claudeSessionUUIDRE = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// sanitizeResumeID returns the resumeID only if it's a UUID that Claude CLI
// will accept. Passing a non-UUID (e.g. our synthetic "agent_<ts>" thread ID)
// makes the CLI exit with "not a UUID and does not match any session title"
// before it ever reads stdin — which surfaces to users as a silent empty
// "Claude API temporarily unavailable" result.
func sanitizeResumeID(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	if claudeSessionUUIDRE.MatchString(trimmed) {
		return trimmed
	}
	pkglogger.Warn("claudecli: dropping non-UUID resume id",
		"resume_id", trimmed)
	return ""
}

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
	// DisallowedTools overrides the upstream Claude CLI built-in tool disable
	// list. A nil slice keeps the legacy hardcoded default; a non-nil slice
	// (including empty) expresses an explicit user choice.
	DisallowedTools []string
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
	logSystemPromptArgs(args)
	args = appendFlagIfSet(args, "--resume", sanitizeResumeID(resumeID))
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

// logSystemPromptArgs dumps every --system-prompt block that we're about to
// hand to the Claude CLI. For each block we record length, head preview, tail
// preview, and also write the full content to a timestamped file under the
// OS temp dir so the operator can grep for known markers (e.g. to check
// whether a router-injected PromptTemplate actually made it through).
func logSystemPromptArgs(args []string) {
	blocks := make([]map[string]any, 0, 4)
	idx := 0
	for i := 0; i < len(args)-1; i++ {
		if args[i] != "--system-prompt" {
			continue
		}
		value := args[i+1]
		runes := []rune(value)
		const previewMax = 160
		head := value
		if len(runes) > previewMax {
			head = string(runes[:previewMax]) + "…"
		}
		tail := value
		if len(runes) > previewMax {
			tail = "…" + string(runes[len(runes)-previewMax:])
		}
		dumpPath := writeSystemPromptDump(idx, value)
		blocks = append(blocks, map[string]any{
			"index":     idx,
			"len":       len(value),
			"head":      head,
			"tail":      tail,
			"dump_path": dumpPath,
		})
		idx++
	}
	pkglogger.Info("claudecli: --system-prompt blocks handed to CLI",
		"count", len(blocks), "blocks", blocks)
}

func writeSystemPromptDump(index int, content string) string {
	dir := filepath.Join(os.TempDir(), "super-agent-systemprompt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		pkglogger.Warn("claudecli: systemprompt dump mkdir failed", "err", err)
		return ""
	}
	name := fmt.Sprintf("%s-block%d.txt", time.Now().UTC().Format("20060102T150405.000000000"), index)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		pkglogger.Warn("claudecli: systemprompt dump write failed", "err", err, "path", path)
		return ""
	}
	return path
}

func buildCLIArgs(model, instructions, mcpConfigPath string, cfg cliLaunchConfig) []string {
	model = sanitizeClaudeModel(model)
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	}
	args = appendFlagIfSet(args, "--model", model)
	args = appendSystemPromptFlags(args, instructions, cfg)
	args = appendFlagIfSet(args, "--permission-mode", resolvePermissionMode(cfg.ApprovalPolicy, cfg.Sandbox))
	args = appendFlagIfSet(args, "--effort", normalizeEffort(model, cfg.Effort))
	if mcpConfigPath = strings.TrimSpace(mcpConfigPath); mcpConfigPath != "" {
		args = appendFlagIfSet(args, "--mcp-config", mcpConfigPath)
		if disallowed := resolveDisallowedToolsFlag(cfg.DisallowedTools); disallowed != "" {
			args = append(args, "--disallowedTools", disallowed)
		}
		if !hasFlag(args, "--permission-mode") {
			args = appendFlagIfSet(args, "--permission-mode", "bypassPermissions")
		}
	}
	return args
}

func hasFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

// defaultDisallowedBuiltinTools mirrors the legacy hardcoded list kept for
// backwards compatibility when the caller has not provided an explicit
// DisallowedTools override.
var defaultDisallowedBuiltinTools = []string{"Read", "Write", "Edit", "MultiEdit", "Bash", "Grep", "Glob", "LS"}

// resolveDisallowedToolsFlag turns the configured list into the --disallowedTools
// flag value. nil → legacy default; non-nil empty → "" (caller skips the flag);
// otherwise the trimmed IDs are comma-joined.
func resolveDisallowedToolsFlag(override []string) string {
	source := override
	if source == nil {
		source = defaultDisallowedBuiltinTools
	}
	ids := make([]string, 0, len(source))
	for _, raw := range source {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	return strings.Join(ids, ",")
}

func appendSystemPromptFlags(args []string, instructions string, cfg cliLaunchConfig) []string {
	for _, block := range composeLaunchSystemPromptBlocks(instructions, cfg) {
		args = append(args, "--system-prompt", block)
	}
	return args
}

func composeLaunchSystemPrompt(instructions string, cfg cliLaunchConfig) string {
	parts := composeLaunchSystemPromptBlocks(instructions, cfg)
	return strings.TrimSpace(strings.Join(parts, "\n\n"))
}

func composeLaunchSystemPromptBlocks(instructions string, cfg cliLaunchConfig) []string {
	parts := promptBaseInstructionBlocks(instructions, cfg.PromptSnapshot)
	parts = append(parts, promptDeveloperInstructions(cfg))
	meta := make([]string, 0, 2)
	for _, pair := range [][2]string{
		{"summary", cfg.Summary},
		{"personality", cfg.Personality},
	} {
		if value := strings.TrimSpace(pair[1]); value != "" {
			meta = append(meta, pair[0]+"="+value)
		}
	}
	if len(meta) > 0 {
		parts = append(parts, strings.Join(meta, "\n"))
	}
	return nonEmptyStrings(parts...)
}

func promptBaseInstructionBlocks(instructions string, snapshot contract.PromptAssemblySnapshot) []string {
	if boundary := normalizePromptBoundary(snapshot.Boundary); boundary != nil {
		return nonEmptyStrings(boundary.CachedPrefix, boundary.UncachedTail)
	}
	return nonEmptyStrings(promptBaseInstructions(instructions, snapshot))
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
		promptBoundaryBlank(snapshot.Boundary) &&
		strings.TrimSpace(snapshot.DeveloperInstructions) == "" &&
		strings.TrimSpace(snapshot.Provider) == "" &&
		snapshot.Version == 0 &&
		strings.TrimSpace(snapshot.Hash) == "" &&
		len(snapshot.SectionSnapshot) == 0 &&
		snapshot.Generation == 0
}

func normalizePromptBoundary(boundary *dto.PromptAssemblyBoundary) *dto.PromptAssemblyBoundary {
	if boundary == nil {
		return nil
	}
	cloned := dto.PromptAssemblyBoundary{
		CachedPrefix: strings.TrimSpace(boundary.CachedPrefix),
		UncachedTail: strings.TrimSpace(boundary.UncachedTail),
	}
	if cloned.CachedPrefix == "" && cloned.UncachedTail == "" {
		return nil
	}
	return &cloned
}

func promptBoundaryBlank(boundary *dto.PromptAssemblyBoundary) bool {
	return normalizePromptBoundary(boundary) == nil
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

func cloneStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return map[string]string{}
	}
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
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
