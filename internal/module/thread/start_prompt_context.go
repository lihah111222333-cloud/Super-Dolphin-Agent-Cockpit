package thread

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	"github.com/anthropic-ai/super-agent-v3/internal/module/thread/titleextract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/kernel"
)

type promptGitContext struct {
	Root       string
	IsWorktree bool
}

type toolInstanceLister interface {
	ListInstances() []contract.ToolInstance
}

// buildStartCtx 构建起点ctx。
func buildStartCtx(req StartRequest, cfg *contract.Config, registry contract.ToolRegistry) contract.BuildCtx {
	cwd := resolvePromptCWD(req.CWD)
	outputStyleConfig := configOutputStyle(req.Config, "outputStyleConfig", "output_style_config")
	sessionFlags := firstNonEmptyFlags(req.SessionFlags, configBoolMap(req.Config, "sessionFlags", "session_flags"))
	sessionFlags = applyConfiguredSessionFlagDefaults(sessionFlags, cfg)
	enabledTools := applyPersistentSubagentToolPolicy(
		firstNonEmptyStrings(req.EnabledTools, kernel.ConfigStringSlice(req.Config, "enabledTools", "enabled_tools", "tools")),
		sessionFlags,
	)
	gitCtx := resolvePromptGitContext(
		cwd,
		kernel.FirstNonEmpty(req.GitRoot, kernel.ConfigString(req.Config, "gitRoot", "git_root")),
		cfg,
	)
	if req.IsWorktree || configBool(req.Config, "isWorktree", "is_worktree") {
		gitCtx.IsWorktree = true
	}
	return contract.BuildCtx{
		CWD:                          cwd,
		GitRoot:                      gitCtx.Root,
		IsWorktree:                   gitCtx.IsWorktree,
		Language:                     kernel.FirstNonEmpty(req.Language, kernel.ConfigString(req.Config, "language")),
		Provider:                     req.Provider,
		Model:                        req.Model,
		EnabledTools:                 enabledTools,
		AdditionalWorkingDirectories: firstNonEmptyStrings(req.AdditionalWorkingDirectories, kernel.ConfigStringSlice(req.Config, "additionalWorkingDirectories", "additional_working_directories")),
		ClaudeMdExcludes:             kernel.ConfigStringSlice(req.Config, "claudeMdExcludes", "claude_md_excludes"),
		MCPSnapshot:                  buildPromptMCPSnapshot(req.MCPSnapshot, configMCPSnapshot(req.Config), registryMCPSnapshot(registry)),
		SessionFlags:                 sessionFlags,
		Summary:                      kernel.FirstNonEmpty(req.Summary, kernel.ConfigString(req.Config, "summary")),
		OutputStyleConfig:            outputStyleConfig,
		ScratchpadDir:                configScratchpadDir(req.Config, "scratchpadDir", "scratchpad_dir"),
		FRCConfig:                    configFRCConfig(req.Config, "frcConfig", "frc_config"),
		KeepCodingInstructions:       firstNonNilBool(configOptionalBool(req.Config, "keepCodingInstructions", "keep_coding_instructions"), styleKeepCodingInstructions(outputStyleConfig)),
	}
}

func applyConfiguredSessionFlagDefaults(flags map[string]bool, cfg *contract.Config) map[string]bool {
	out := cloneFlags(flags)
	if cfg == nil || !cfg.Agent.PersistentSubagentDefault {
		return out
	}
	if hasConfiguredSessionFlag(out,
		"persistent_subagent_default",
		"persistentSubagentDefault",
		"managed_subagent_default",
		"managedSubagentDefault",
		"ui_persistent_subagent_default",
		"uiPersistentSubagentDefault",
	) {
		return out
	}
	if out == nil {
		out = map[string]bool{}
	}
	out["persistent_subagent_default"] = true
	return out
}

// hasConfiguredSessionFlag 判断configured会话flag是否可用。
func hasConfiguredSessionFlag(flags map[string]bool, names ...string) bool {
	if len(flags) == 0 || len(names) == 0 {
		return false
	}
	wanted := make(map[string]struct{}, len(names))
	for _, name := range names {
		if normalized := normalizeSessionFlagName(name); normalized != "" {
			wanted[normalized] = struct{}{}
		}
	}
	for name := range flags {
		if _, ok := wanted[normalizeSessionFlagName(name)]; ok {
			return true
		}
	}
	return false
}

func normalizeSessionFlagName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	replacer := strings.NewReplacer("_", "", "-", "", " ", "")
	return replacer.Replace(name)
}

func resolvePromptCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || cwd == "." {
		return ""
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return abs
	}
	return cwd
}

func resolvePromptGitContext(cwd, hintRoot string, cfg *contract.Config) promptGitContext {
	ctx := discoverPromptGitContext(cwd)
	if root := strings.TrimSpace(hintRoot); root != "" {
		ctx.Root = root
	}
	if ctx.Root != "" {
		return ctx
	}
	projectRoot := strings.TrimSpace(cfgProjectRoot(cfg))
	if projectRoot == "" {
		return promptGitContext{}
	}
	if discovered := discoverPromptGitContext(projectRoot); discovered.Root != "" {
		return discovered
	}
	return promptGitContext{Root: projectRoot}
}

// discoverPromptGitContext 处理discoverpromptgit上下文。
func discoverPromptGitContext(path string) promptGitContext {
	for dir := resolvePromptCWD(path); dir != ""; dir = filepath.Dir(dir) {
		gitPath := filepath.Join(dir, ".git")
		info, err := os.Stat(gitPath)
		if err == nil {
			if info.IsDir() {
				return promptGitContext{Root: dir}
			}
			return parsePromptGitFile(dir, gitPath)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return promptGitContext{}
}

// parsePromptGitFile 解析promptgit文件。
func parsePromptGitFile(dir, gitPath string) promptGitContext {
	raw, err := os.ReadFile(gitPath)
	if err != nil {
		return promptGitContext{Root: dir}
	}
	value := strings.TrimSpace(string(raw))
	if !strings.HasPrefix(strings.ToLower(value), "gitdir:") {
		return promptGitContext{Root: dir}
	}
	gitDir := strings.TrimSpace(value[len("gitdir:"):])
	if gitDir == "" {
		return promptGitContext{Root: dir}
	}
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Clean(filepath.Join(dir, gitDir))
	}
	if root := worktreeGitRoot(gitDir); root != "" {
		return promptGitContext{Root: root, IsWorktree: true}
	}
	if strings.HasSuffix(gitDir, string(filepath.Separator)+".git") {
		return promptGitContext{Root: filepath.Dir(gitDir)}
	}
	return promptGitContext{Root: dir}
}

func worktreeGitRoot(gitDir string) string {
	token := string(filepath.Separator) + filepath.Join(".git", "worktrees") + string(filepath.Separator)
	root, _, ok := strings.Cut(gitDir, token)
	if !ok {
		return ""
	}
	return filepath.Clean(root)
}

func cfgProjectRoot(cfg *contract.Config) string {
	if cfg == nil {
		return ""
	}
	return strings.TrimSpace(cfg.ProjectRoot)
}

func configBool(cfg map[string]any, keys ...string) bool {
	for _, key := range keys {
		value, ok := cfg[key].(bool)
		if ok {
			return value
		}
	}
	return false
}

func configOptionalBool(cfg map[string]any, keys ...string) *bool {
	for _, key := range keys {
		value, ok := cfg[key].(bool)
		if ok {
			cloned := value
			return &cloned
		}
	}
	return nil
}

func configBoolMap(cfg map[string]any, keys ...string) map[string]bool {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		if flags := normalizeBoolMap(value); len(flags) > 0 {
			return flags
		}
	}
	return nil
}

// normalizeBoolMap 规范化boolmap。
func normalizeBoolMap(value any) map[string]bool {
	switch typed := value.(type) {
	case map[string]bool:
		return cloneFlags(typed)
	case map[string]any:
		out := make(map[string]bool, len(typed))
		for key, raw := range typed {
			flag, ok := raw.(bool)
			if ok {
				key = strings.TrimSpace(key)
				if key != "" {
					out[key] = flag
				}
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func firstNonNilBool(primary, fallback *bool) *bool {
	if primary != nil {
		return primary
	}
	return fallback
}

func cloneOptionalBool(value *bool) *bool {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func configMCPSnapshot(cfg map[string]any) contract.MCPSnapshot {
	return contract.MCPSnapshot{
		Servers:                  kernel.ConfigStringSlice(cfg, "mcpServers", "mcp_servers"),
		Tools:                    kernel.ConfigStringSlice(cfg, "mcpTools", "mcp_tools"),
		Instructions:             configStringMap(cfg, "mcpInstructions", "mcp_instructions"),
		InstructionsDeltaEnabled: configBool(cfg, "mcpInstructionsDeltaEnabled", "mcp_instructions_delta_enabled"),
	}
}

func configStringMap(cfg map[string]any, keys ...string) map[string]string {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		if out := kernel.ConfigStringMap(value); len(out) > 0 {
			return out
		}
	}
	return nil
}

func registryMCPSnapshot(registry contract.ToolRegistry) contract.MCPSnapshot {
	lister, ok := registry.(toolInstanceLister)
	if !ok {
		return contract.MCPSnapshot{}
	}
	instances := lister.ListInstances()
	servers := make([]string, 0, len(instances))
	for _, instance := range instances {
		if !strings.EqualFold(strings.TrimSpace(instance.Status), mcpdto.StatusActive) {
			continue
		}
		if server := mcpServerName(instance); server != "" {
			servers = append(servers, server)
		}
	}
	return contract.MCPSnapshot{Servers: kernel.NormalizeConfigStringSlice(servers)}
}

func buildPromptMCPSnapshot(base, configured, live contract.MCPSnapshot) contract.MCPSnapshot {
	merged := mergeMCPSnapshot(base, configured)
	merged.Servers = append([]string(nil), live.Servers...)
	if len(merged.Servers) == 0 {
		merged.Servers = nil
	}
	return merged
}

func mcpServerName(instance contract.ToolInstance) string {
	if kind := strings.TrimSpace(instance.ClientKind); kind != "" && !strings.EqualFold(kind, "custom") {
		return kind
	}
	name := strings.TrimSpace(instance.BinaryName)
	return strings.TrimPrefix(name, "mcp-")
}

// mergeMCPSnapshot 合并MCP快照。
func mergeMCPSnapshot(base, extra contract.MCPSnapshot) contract.MCPSnapshot {
	out := contract.MCPSnapshot{
		Servers:                  uniquePromptStrings(base.Servers, extra.Servers),
		Tools:                    uniquePromptStrings(base.Tools, extra.Tools),
		ServerConfigs:            mergeMCPServerConfigMaps(base.ServerConfigs, extra.ServerConfigs),
		InstructionsDeltaEnabled: base.InstructionsDeltaEnabled || extra.InstructionsDeltaEnabled,
		InstructionAttachments:   append(append([]contract.MCPAttachmentRef(nil), base.InstructionAttachments...), extra.InstructionAttachments...),
	}
	if len(base.Instructions) > 0 || len(extra.Instructions) > 0 {
		out.Instructions = make(map[string]string, len(base.Instructions)+len(extra.Instructions))
		for key, value := range base.Instructions {
			out.Instructions[strings.TrimSpace(key)] = strings.TrimSpace(value)
		}
		for key, value := range extra.Instructions {
			key = strings.TrimSpace(key)
			value = strings.TrimSpace(value)
			if key != "" && value != "" {
				out.Instructions[key] = value
			}
		}
		if len(out.Instructions) == 0 {
			out.Instructions = nil
		}
	}
	return out
}

func firstNonEmptyStrings(primary, fallback []string) []string {
	if out := uniquePromptStrings(primary, nil); len(out) > 0 {
		return out
	}
	return uniquePromptStrings(fallback, nil)
}

func uniquePromptStrings(first, second []string) []string {
	combined := kernel.NormalizeConfigStringSlice(append(append([]string(nil), first...), second...))
	if len(combined) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(combined))
	out := make([]string, 0, len(combined))
	for _, value := range combined {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	return out
}

func firstNonEmptyFlags(primary, fallback map[string]bool) map[string]bool {
	if len(primary) > 0 {
		return cloneFlags(primary)
	}
	return cloneFlags(fallback)
}

func cloneFlags(flags map[string]bool) map[string]bool {
	if len(flags) == 0 {
		return nil
	}
	cloned := make(map[string]bool, len(flags))
	for key, value := range flags {
		key = strings.TrimSpace(key)
		if key != "" {
			cloned[key] = value
		}
	}
	if len(cloned) == 0 {
		return nil
	}
	return cloned
}

// ---- output-style helpers (formerly start_prompt_context_style.go) ----

func configOutputStyle(cfg map[string]any, keys ...string) *contract.OutputStyleConfig {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		if style := normalizeOutputStyleConfig(value); style != nil {
			return style
		}
	}
	return nil
}

// normalizeOutputStyleConfig 规范化outputstyle配置。
func normalizeOutputStyleConfig(value any) *contract.OutputStyleConfig {
	switch typed := value.(type) {
	case contract.OutputStyleConfig:
		return cloneOutputStyleConfig(typed)
	case *contract.OutputStyleConfig:
		if typed == nil {
			return nil
		}
		return cloneOutputStyleConfig(*typed)
	case map[string]any:
		style := contract.OutputStyleConfig{
			Name:        kernel.ConfigString(typed, "name"),
			Description: kernel.ConfigString(typed, "description"),
			Prompt:      kernel.ConfigString(typed, "prompt"),
			Source:      kernel.ConfigString(typed, "source"),
		}
		style.KeepCodingInstructions = configOptionalBool(typed, "keepCodingInstructions", "keep_coding_instructions")
		if strings.TrimSpace(style.Name) == "" &&
			strings.TrimSpace(style.Description) == "" &&
			strings.TrimSpace(style.Prompt) == "" &&
			strings.TrimSpace(style.Source) == "" &&
			style.KeepCodingInstructions == nil {
			return nil
		}
		return &style
	default:
		return nil
	}
}

// cloneOutputStyleConfig 复制outputstyle配置。
func cloneOutputStyleConfig(style contract.OutputStyleConfig) *contract.OutputStyleConfig {
	cloned := style
	cloned.KeepCodingInstructions = cloneOptionalBool(style.KeepCodingInstructions)
	if strings.TrimSpace(cloned.Name) == "" &&
		strings.TrimSpace(cloned.Description) == "" &&
		strings.TrimSpace(cloned.Prompt) == "" &&
		strings.TrimSpace(cloned.Source) == "" &&
		cloned.KeepCodingInstructions == nil {
		return nil
	}
	return &cloned
}

func styleKeepCodingInstructions(style *contract.OutputStyleConfig) *bool {
	if style == nil {
		return nil
	}
	return cloneOptionalBool(style.KeepCodingInstructions)
}

// ---- FRC config helpers (formerly frc_config.go) ----

func configFRCConfig(cfg map[string]any, keys ...string) *contract.FRCConfig {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		if frc := normalizeFRCConfig(value); frc != nil {
			return frc
		}
	}
	return nil
}

// normalizeFRCConfig 规范化frc配置。
func normalizeFRCConfig(value any) *contract.FRCConfig {
	switch typed := value.(type) {
	case contract.FRCConfig:
		return typed.Normalize()
	case *contract.FRCConfig:
		if typed == nil {
			return nil
		}
		return typed.Normalize()
	case map[string]any:
		cfg := contract.FRCConfig{
			Enabled:                      configBool(typed, "enabled"),
			SystemPromptSuggestSummaries: configBool(typed, "systemPromptSuggestSummaries", "system_prompt_suggest_summaries"),
			SupportedModels:              kernel.ConfigStringSlice(typed, "supportedModels", "supported_models"),
			KeepRecent:                   configInt(typed, "keepRecent", "keep_recent"),
		}
		if !cfg.Enabled && !cfg.SystemPromptSuggestSummaries && cfg.KeepRecent == 0 && len(cfg.SupportedModels) == 0 {
			return nil
		}
		return cfg.Normalize()
	default:
		return nil
	}
}

// configInt 处理配置int。
func configInt(cfg map[string]any, keys ...string) int {
	for _, key := range keys {
		switch value := cfg[key].(type) {
		case int:
			return value
		case int32:
			return int(value)
		case int64:
			return int(value)
		case float64:
			return int(value)
		case string:
			if parsed, err := strconv.Atoi(value); err == nil {
				return parsed
			}
		}
	}
	return 0
}

// ExtractTitle 提取 title。
func ExtractTitle(prompt string) string {
	return titleextract.Extract(prompt)
}

func resolveDisplayName(ctx context.Context, store contract.ThreadStore, agentID, _ string, currentName string) string {
	name := strings.TrimSpace(currentName)
	if name == defaultThreadName() {
		name = ""
	}
	if store != nil {
		existing, err := store.GetByThreadID(ctx, agentID)
		if err == nil && existing.ManuallyRenamed {
			return strings.TrimSpace(existing.Name)
		}
	}
	return name
}

func defaultThreadName() string {
	return "新对话"
}

func continuationName(parentName string) string {
	return titleextract.ContinuationName(parentName)
}
