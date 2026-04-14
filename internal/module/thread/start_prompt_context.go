package thread

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	mcpdto "github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
	providershared "github.com/anthropic-ai/super-agent-v3/internal/provider/shared"
)

type promptGitContext struct {
	Root       string
	IsWorktree bool
}

type toolInstanceLister interface {
	ListInstances() []contract.ToolInstance
}

func buildStartCtx(req StartRequest, cfg *platformconfig.Config, registry contract.ToolRegistry) contract.BuildCtx {
	cwd := resolvePromptCWD(req.CWD)
	outputStyleConfig := configOutputStyle(req.Config, "outputStyleConfig", "output_style_config")
	gitCtx := resolvePromptGitContext(
		cwd,
		shared.FirstNonEmpty(req.GitRoot, providershared.ConfigString(req.Config, "gitRoot", "git_root")),
		cfg,
	)
	if req.IsWorktree || configBool(req.Config, "isWorktree", "is_worktree") {
		gitCtx.IsWorktree = true
	}
	return contract.BuildCtx{
		CWD:                          cwd,
		GitRoot:                      gitCtx.Root,
		IsWorktree:                   gitCtx.IsWorktree,
		Language:                     shared.FirstNonEmpty(req.Language, providershared.ConfigString(req.Config, "language")),
		Provider:                     req.Provider,
		Model:                        req.Model,
		EnabledTools:                 firstNonEmptyStrings(req.EnabledTools, providershared.ConfigStringSlice(req.Config, "enabledTools", "enabled_tools", "tools")),
		AdditionalWorkingDirectories: firstNonEmptyStrings(req.AdditionalWorkingDirectories, providershared.ConfigStringSlice(req.Config, "additionalWorkingDirectories", "additional_working_directories")),
		MCPSnapshot: mergeMCPSnapshot(
			mergeMCPSnapshot(req.MCPSnapshot, configMCPSnapshot(req.Config)),
			registryMCPSnapshot(registry),
		),
		SessionFlags:           firstNonEmptyFlags(req.SessionFlags, configBoolMap(req.Config, "sessionFlags", "session_flags")),
		OutputStyleConfig:      outputStyleConfig,
		KeepCodingInstructions: firstNonNilBool(configOptionalBool(req.Config, "keepCodingInstructions", "keep_coding_instructions"), styleKeepCodingInstructions(outputStyleConfig)),
	}
}

func resolvePromptCWD(cwd string) string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" || cwd == "." {
		if wd, err := os.Getwd(); err == nil {
			cwd = strings.TrimSpace(wd)
		}
	}
	if cwd == "" {
		return "."
	}
	if abs, err := filepath.Abs(cwd); err == nil {
		return abs
	}
	return cwd
}

func resolvePromptGitContext(cwd, hintRoot string, cfg *platformconfig.Config) promptGitContext {
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

func cfgProjectRoot(cfg *platformconfig.Config) string {
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
			Name:        providershared.ConfigString(typed, "name"),
			Description: providershared.ConfigString(typed, "description"),
			Prompt:      providershared.ConfigString(typed, "prompt"),
			Source:      providershared.ConfigString(typed, "source"),
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

func configMCPSnapshot(cfg map[string]any) contract.MCPSnapshot {
	return contract.MCPSnapshot{
		Servers:      providershared.ConfigStringSlice(cfg, "mcpServers", "mcp_servers"),
		Tools:        providershared.ConfigStringSlice(cfg, "mcpTools", "mcp_tools"),
		Instructions: configStringMap(cfg, "mcpInstructions", "mcp_instructions"),
	}
}

func configStringMap(cfg map[string]any, keys ...string) map[string]string {
	for _, key := range keys {
		value, ok := cfg[key]
		if !ok {
			continue
		}
		if out := providershared.StringMap(value); len(out) > 0 {
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
	return contract.MCPSnapshot{Servers: providershared.NormalizeConfigStringSlice(servers)}
}

func mcpServerName(instance contract.ToolInstance) string {
	if kind := strings.TrimSpace(instance.ClientKind); kind != "" && !strings.EqualFold(kind, "custom") {
		return kind
	}
	name := strings.TrimSpace(instance.BinaryName)
	return strings.TrimPrefix(name, "mcp-")
}

func mergeMCPSnapshot(base, extra contract.MCPSnapshot) contract.MCPSnapshot {
	out := contract.MCPSnapshot{
		Servers:                  uniquePromptStrings(base.Servers, extra.Servers),
		Tools:                    uniquePromptStrings(base.Tools, extra.Tools),
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
	combined := providershared.NormalizeConfigStringSlice(append(append([]string(nil), first...), second...))
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
