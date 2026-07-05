package claudecli

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/observability"
	"github.com/anthropic-ai/super-agent-v3/internal/util/identifier"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// sanitizeResumeID 只把 Claude CLI 能接受的 UUID resumeID 传给子进程。
// 本地合成的 thread ID 会让 CLI 在读取 stdin 前退出，因此这里丢弃非法值并记录告警。
func sanitizeResumeID(id string) string {
	trimmed := strings.TrimSpace(id)
	if trimmed == "" {
		return ""
	}
	if identifier.IsClaudeCLISessionUUID(trimmed) {
		return trimmed
	}
	pkglogger.Warn("claudecli: dropping non-UUID resume id",
		"resume_id", trimmed)
	return ""
}

const (
	defaultClaudeCLIBin = "claude"
	systemPromptDumpEnv = "SUPER_DOLPHIN_CLAUDE_SYSTEM_PROMPT_DUMP"
)

type cliLaunchConfig struct {
	ApprovalPolicy        string
	Sandbox               string
	Summary               string
	Effort                string
	Personality           string
	DeveloperInstructions string
	ClaudeHome            string
	PromptSnapshot        contract.PromptAssemblySnapshot
	// BuiltinTools 对应传给 Claude CLI --tools 的显式 allowlist。
	// nil 保留 disallow-list 路径；非 nil 空切片表示本轮明确不暴露 native tool。
	BuiltinTools                []string
	DisallowedTools             []string
	AdditionalDisallowedTools   []string
	DisableProviderNativeSkills bool
}

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
	args, err := buildCLIArgs(model, instructions, mcpPath, cfg)
	if err != nil {
		return nil, nil, cleanupOnError(err, cleanup)
	}
	logManifestLaunch(binary, cwd, model, mcpPath, manifest)
	logSystemPromptArgs(args)
	args = appendFlagIfSet(args, "--resume", sanitizeResumeID(resumeID))
	tr, err := newTransport(binary, args, cwd, claudeLaunchEnv(cfg))
	if err != nil {
		return nil, nil, cleanupOnError(err, cleanup)
	}
	return tr, cleanup, nil
}

func claudeLaunchEnv(cfg cliLaunchConfig) []string {
	home := strings.TrimSpace(cfg.ClaudeHome)
	if home == "" {
		return nil
	}
	return []string{"CLAUDE_CONFIG_DIR=" + home}
}

// logManifestLaunch 记录本轮写给 Claude CLI 的 MCP manifest 摘要。
// 日志只暴露路径存在性、命令 basename、参数摘要和 env key，不输出 cwd、配置路径、args 或 URL secret。
func logManifestLaunch(binary, cwd, model, mcpPath string, manifest dto.MCPManifest) {
	servers := observability.SafeMCPServerSummaries(manifest)
	pkglogger.Info("claudecli: launch mcp manifest",
		"binary", safeCLICommandName(binary),
		"cwd_present", strings.TrimSpace(cwd) != "",
		"model", strings.TrimSpace(model),
		"mcp_config_present", strings.TrimSpace(mcpPath) != "",
		"server_count", len(servers),
		"servers", servers,
	)
}

func safeCLICommandName(command string) string {
	command = strings.TrimSpace(command)
	if command == "" {
		return ""
	}
	command = strings.ReplaceAll(command, "\\", "/")
	return filepath.Base(filepath.FromSlash(command))
}

// logSystemPromptArgs 记录即将交给 Claude CLI 的每段 --system-prompt。
// 默认只记录长度和 hash；只有显式打开 debug dump 时才把完整内容写入私有临时目录。
func logSystemPromptArgs(args []string) {
	blocks := make([]map[string]any, 0, 4)
	idx := 0
	dumpEnabled := systemPromptDumpEnabled()
	for i := 0; i < len(args)-1; i++ {
		if args[i] != "--system-prompt" {
			continue
		}
		value := args[i+1]
		sum := sha256.Sum256([]byte(value))
		block := map[string]any{
			"index":  idx,
			"len":    len(value),
			"sha256": fmt.Sprintf("%x", sum[:]),
		}
		if dumpEnabled {
			block["dump_path"] = writeSystemPromptDump(idx, value)
		}
		blocks = append(blocks, block)
		idx++
	}
	pkglogger.Info("claudecli: --system-prompt blocks handed to CLI",
		"count", len(blocks), "blocks", blocks)
}

func systemPromptDumpEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(systemPromptDumpEnv))) {
	case "1", "true", "yes", "debug":
		return true
	default:
		return false
	}
}

func writeSystemPromptDump(index int, content string) string {
	dir := filepath.Join(os.TempDir(), "super-agent-systemprompt")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		pkglogger.Warn("claudecli: systemprompt dump mkdir failed", "err", err)
		return ""
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		pkglogger.Warn("claudecli: systemprompt dump chmod failed", "err", err, "path", dir)
		return ""
	}
	name := fmt.Sprintf("%s-block%d.txt", time.Now().UTC().Format("20060102T150405.000000000"), index)
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		pkglogger.Warn("claudecli: systemprompt dump write failed", "err", err, "path", path)
		return ""
	}
	return path
}

// buildCLIArgs 汇总模型、prompt、权限和工具策略并生成 Claude CLI 参数。
// native tool allowlist 优先于 disallow list；带 MCP 配置时必须补权限模式，避免 CLI 交互式阻塞。
func buildCLIArgs(model, instructions, mcpConfigPath string, cfg cliLaunchConfig) ([]string, error) {
	model = sanitizeClaudeModel(model)
	args := []string{
		"-p",
		"--input-format", "stream-json",
		"--output-format", "stream-json",
		"--verbose",
	}
	args = appendFlagIfSet(args, "--model", model)
	args = appendSystemPromptFlags(args, instructions, cfg)
	permissionMode, err := resolvePermissionMode(cfg.ApprovalPolicy, cfg.Sandbox)
	if err != nil {
		return nil, err
	}
	args = appendFlagIfSet(args, "--permission-mode", permissionMode)
	args = appendFlagIfSet(args, "--effort", normalizeEffort(model, cfg.Effort))
	builtinTools := cfg.BuiltinTools
	if cfg.DisableProviderNativeSkills && builtinTools != nil {
		builtinTools = withoutToolID(builtinTools, "Skill")
	}
	if builtinTools != nil {
		args = append(args, "--tools", resolveToolsFlag(builtinTools))
	} else if disallowed := resolveDisallowedToolsFlag(cfg.DisallowedTools, additionalDisallowedTools(cfg)); disallowed != "" {
		args = append(args, "--disallowedTools", disallowed)
	}
	if mcpConfigPath = strings.TrimSpace(mcpConfigPath); mcpConfigPath != "" {
		args = appendFlagIfSet(args, "--mcp-config", mcpConfigPath)
		if !hasFlag(args, "--permission-mode") {
			args = appendFlagIfSet(args, "--permission-mode", "bypassPermissions")
		}
	}

	return args, nil
}

func hasFlag(args []string, flag string) bool {
	return slices.Contains(args, flag)
}

// defaultDisallowedBuiltinTools 读取 provider registry 里的默认禁用 native tool。
// 调用方未显式覆盖时使用这份清单，确保启动参数与 UI/配置层的默认治理一致。
func defaultDisallowedBuiltinTools() []string {
	factory := NewDriverFactory(driverFactoryParams{})
	return defaultDisabledLaunchToolIDs(factory.NativeTools)
}

// defaultDisabledLaunchToolIDs 过滤 Claude hard-disable native tool ID。
// 只有属于 Claude provider 且标记为启动期强禁用的工具才会写入 disallow 参数。
func defaultDisabledLaunchToolIDs(tools []contract.NativeToolDescriptor) []string {
	ids := make([]string, 0, len(tools))
	for _, tool := range tools {
		id := strings.TrimSpace(tool.ID)
		if id == "" || !tool.DefaultDisabled {
			continue
		}
		if strings.TrimSpace(tool.Provider) != "claude" || tool.FilterMode != contract.NativeToolFilterModeHard {
			continue
		}
		ids = append(ids, id)
	}
	return ids
}

func resolveToolsFlag(allowlist []string) string {
	ids := make([]string, 0, len(allowlist))
	for _, raw := range allowlist {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		ids = append(ids, id)
	}
	return strings.Join(ids, ",")
}

func withoutToolID(values []string, blocked string) []string {
	out := make([]string, 0, len(values))
	for _, raw := range values {
		id := strings.TrimSpace(raw)
		if id == "" || strings.EqualFold(id, strings.TrimSpace(blocked)) {
			continue
		}
		out = append(out, id)
	}
	return out
}

func additionalDisallowedTools(cfg cliLaunchConfig) []string {
	if !cfg.DisableProviderNativeSkills {
		return cfg.AdditionalDisallowedTools
	}
	ids := append([]string(nil), cfg.AdditionalDisallowedTools...)
	return append(ids, "Skill")
}

// resolveDisallowedToolsFlag 把覆盖值和追加值合并成 --disallowedTools 参数。
// nil 覆盖值沿用默认禁用清单；非 nil 空切片表示调用方明确跳过默认值。
func resolveDisallowedToolsFlag(override []string, additional ...[]string) string {
	source := override
	if source == nil {
		source = defaultDisallowedBuiltinTools()
	}
	ids := make([]string, 0, len(source))
	seen := map[string]struct{}{}
	appendIDs := func(values []string) {
		for _, raw := range values {
			id := strings.TrimSpace(raw)
			if id == "" {
				continue
			}
			if _, exists := seen[id]; exists {
				continue
			}
			seen[id] = struct{}{}
			ids = append(ids, id)
		}
	}
	appendIDs(source)
	for _, values := range additional {
		appendIDs(values)
	}
	return strings.Join(ids, ",")
}

func appendSystemPromptFlags(args []string, instructions string, cfg cliLaunchConfig) []string {
	blocks := composeLaunchSystemPromptBlocks(instructions, cfg)
	for _, block := range blocks {
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

func promptLaunchBaseBlank(instructions string, cfg cliLaunchConfig) bool {
	return len(promptBaseInstructionBlocks(instructions, cfg.PromptSnapshot)) == 0
}

func promptSnapshotBaseInstructions(snapshot contract.PromptAssemblySnapshot, fallback string) string {
	if boundary := normalizePromptBoundary(snapshot.Boundary); boundary != nil {
		return strings.TrimSpace(strings.Join(nonEmptyStrings(boundary.CachedPrefix, boundary.UncachedTail), "\n\n"))
	}
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

// promptSnapshotBlank 判断 prompt snapshot 是否完全没有可复用内容。
// 这是兼容旧调用方的空值探测，不能把 Generation 或 Hash 等持久化字段漏掉。
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

func resolvePermissionMode(approvalPolicy, sandbox string) (string, error) {
	if mode, err := permissionModeFromSandbox(sandbox); err != nil {
		return "", err
	} else if mode != "" {
		return mode, nil
	}
	switch strings.ToLower(strings.TrimSpace(approvalPolicy)) {
	case "", "never", "on-request", "always", "auto":
		return "bypassPermissions", nil
	case "on-failure", "untrusted":
		return "default", nil
	default:
		return "", fmt.Errorf("invalid approval policy %q", approvalPolicy)
	}
}

// permissionModeFromSandbox 将运行时 sandbox 形态映射到 Claude CLI 权限模式。
// sandbox 可能来自 JSON wire 或纯字符串，未知 sandbox 必须报错，避免落到提权模式。
func permissionModeFromSandbox(sandbox string) (string, error) {
	raw := strings.TrimSpace(sandbox)
	if raw == "" {
		return "", nil
	}
	if strings.HasPrefix(raw, "{") {
		var payload struct {
			Type *string `json:"type"`
		}
		if err := json.Unmarshal([]byte(raw), &payload); err != nil {
			return "", fmt.Errorf("invalid sandbox object: %w", err)
		}
		if payload.Type == nil {
			return "", errors.New("invalid sandbox object: type is required")
		}
		raw = *payload.Type
	}
	raw = strings.Trim(raw, "\"")
	normalized := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(raw, "-", ""), "_", ""))
	switch normalized {
	case "":
		return "", nil
	case "dangerfullaccess":
		return "bypassPermissions", nil
	case "workspacewrite":
		return "acceptEdits", nil
	case "readonly":
		return "default", nil
	default:
		return "", fmt.Errorf("invalid sandbox type %q", raw)
	}
}

// normalizeEffort 将前端 effort 选项转换为 Claude CLI 支持的枚举。
// max 仅对 opus 保留，其他模型降到 high，未知值原样返回让 CLI 自行 fail-fast。
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

// writeManifestConfig 将 MCP manifest 写成 Claude CLI 可读取的临时配置文件。
// 空 manifest 返回空路径；只要有 server 被拒绝就返回错误，避免静默少挂 MCP 能力。
func writeManifestConfig(manifest dto.MCPManifest, cwd string) (string, func(), error) {
	servers, err := manifestServers(manifest, cwd)
	if err != nil {
		return "", nil, err
	}
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

func manifestServers(manifest dto.MCPManifest, cwd string) (map[string]any, error) {
	cwd = strings.TrimSpace(cwd)
	servers := make(map[string]any, len(manifest.Binaries))
	for _, bin := range manifest.Binaries {
		name, server, err := manifestServer(bin, cwd)
		if err != nil {
			return nil, err
		}
		servers[name] = server
	}
	return servers, nil
}

func manifestServer(bin dto.MCPBinary, cwd string) (string, map[string]any, error) {
	name := strings.TrimSpace(bin.Name)
	if name == "" {
		return "", nil, fmt.Errorf("claudecli: rejected mcp manifest server: missing name")
	}

	// HTTP 模式只把已运行 peer 的 URL 写给 Claude，不在 provider 配置里启动新进程。
	if strings.TrimSpace(bin.Type) == "http" && strings.TrimSpace(bin.URL) != "" {
		server := map[string]any{
			"type": "http",
			"url":  strings.TrimSpace(bin.URL),
		}
		applyHeaders(server, bin.Headers)
		applyAutoApprove(server, bin.AutoApprove)
		return name, server, nil
	}

	// stdio 模式必须先校验命令来源，只允许托管 sidecar 或显式允许的 MCP 包。
	server, err := buildStdioServer(bin, cwd)
	if err != nil {
		return "", nil, fmt.Errorf("claudecli: rejected mcp manifest server %q: %w", name, err)
	}
	return name, server, nil
}

// buildStdioServer 将单个 stdio MCP 二进制转换为 Claude 配置项。
// 命令为空或不在 allowlist 内会直接报错，避免 provider 缺能力却继续运行。
func buildStdioServer(bin dto.MCPBinary, cwd string) (map[string]any, error) {
	if len(bin.Command) == 0 {
		return nil, errors.New("missing stdio command")
	}
	command := strings.TrimSpace(bin.Command[0])
	if command == "" {
		return nil, errors.New("empty stdio command")
	}
	if !allowedStdioMCPCommand(bin.Name, command, bin.Command[1:]) {
		return nil, fmt.Errorf("stdio command %q is not allowed", command)
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
	return server, nil
}

// allowedStdioMCPCommand 控制 Claude MCP 配置里可被拉起的 stdio 命令范围。
// 托管 sidecar 必须绑定内置 server name；npx 只允许明确列出的 MCP 包，避免任意 npm 包被配置启动。
func allowedStdioMCPCommand(serverName, command string, args []string) bool {
	base := strings.ToLower(strings.TrimSpace(filepath.Base(command)))
	base = strings.TrimSuffix(strings.TrimSuffix(base, ".exe"), ".cmd")
	switch base {
	case "mcp-lsp":
		return strings.TrimSpace(serverName) == string(dto.FamilyLSP)
	case "mcp-orch":
		return strings.TrimSpace(serverName) == string(dto.FamilyOrch)
	case "mcp-server-postgres":
		return true
	case "npx":
	default:
		return false
	}
	for _, arg := range args {
		switch strings.TrimSpace(arg) {
		case "@modelcontextprotocol/server-postgres", "@bytebase/dbhub", "@playwright/mcp@latest":
			return true
		}
	}
	return false
}

func applyAutoApprove(server map[string]any, autoApprove []string) {
	if len(autoApprove) > 0 {
		server["autoApprove"] = append([]string(nil), autoApprove...)
	}
}

// applyHeaders 将非空 HTTP header 写入 MCP server 配置。
// 空 key/value 会被丢弃，避免 provider 侧收到无效 header 后才失败。
func applyHeaders(server map[string]any, headers map[string]string) {
	if len(headers) == 0 {
		return
	}
	out := make(map[string]string, len(headers))
	for key, value := range headers {
		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		if key == "" || value == "" {
			continue
		}
		out[key] = value
	}
	if len(out) > 0 {
		server["headers"] = out
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
