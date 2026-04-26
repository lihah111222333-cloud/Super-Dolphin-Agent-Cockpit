package agent

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	parse "github.com/anthropic-ai/super-agent-v3/internal/module/memory/parse"
	memshared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	agentMemoryDirName       = "agent-memory"
	agentMemoryLocalDirName  = "agent-memory-local"
	agentMemoryConfigDirName = ".claude"
	emptyAgentMemoryPrompt   = "Your MEMORY.md is currently empty. Save only durable, agent-specific context here."
	unreadableAgentMemoryMsg = "WARNING: MEMORY.md could not be read; continuing with empty agent memory."
	envClaudeRemoteMemoryDir = "CLAUDE_CODE_REMOTE_MEMORY_DIR"
)

var (
	ErrInvalidAgentType  = errors.New("invalid agent type")
	ErrInvalidAgentScope = errors.New("invalid agent memory scope")
	ErrInvalidProjectDir = errors.New("invalid agent project root")
	ErrInvalidMemoryRoot = errors.New("invalid memory root")
)

type MemoryScope = contract.MemoryScope

const (
	MemoryScopeUser    = contract.MemoryScopeUser
	MemoryScopeProject = contract.MemoryScopeProject
	MemoryScopeLocal   = contract.MemoryScopeLocal
)

// Config carries the inputs the agent-memory subsystem needs to locate and
// render per-agent MEMORY.md.
//
// Note: this Config intentionally does NOT carry a Harness field, unlike the
// parent memory.Config (see config.go::Harness). Harness-keyed overlay
// suppression for agent-memory injection IS enforced — but it is sourced
// from the parent memory.Config.Harness via the GateResolver interface
// (see PromptProvider.Resolve calling gates.SuppressForOverlay), not
// duplicated here. Storing it twice would risk drift between the parent
// snapshot and the agent-scoped copy. If a future feature needs an
// agent-only gate independent of the parent snapshot, add the field here
// AND treat it as immutable post-construction (mirroring the contract on
// memory.Config.Harness) so a mid-run os.Setenv cannot flip the decision.
type Config struct {
	RootDir         string
	ProjectRoot     string
	ExtraGuidelines []string
}

type PathHelper interface {
	ValidateRoot(raw string) (string, error)
	CleanAbsolute(raw string) (string, error)
	CanonicalGitRoot(ctx context.Context, projectRoot string) (string, error)
	SanitizePath(raw string) string
	MemoryIndexPath(root string) string
}

type PromptBuilder interface {
	BuildPrompt(scope MemoryScope, extraGuidelines []string) string
}

type GateResolver interface {
	AutoEnabled(buildCtx contract.BuildCtx) bool
	// SuppressForOverlay reports whether the underlying CLI harness already
	// runs its own complete memory pipeline and our providers should step
	// aside. Mirrors MemoryGateSnapshot.SuppressForOverlay so child-agent
	// MEMORY.md injection respects the same overlay gating as the root
	// MemoryEntrypointProvider; otherwise claude_code overlay would suppress
	// the parent prompt entrypoint while still injecting agent-scope
	// MEMORY.md into spawned children.
	SuppressForOverlay(buildCtx contract.BuildCtx) bool
}

type Manager struct {
	cfg     *Config
	paths   PathHelper
	builder PromptBuilder
}

type PromptProvider struct {
	cfg     *Config
	manager *Manager
	gates   GateResolver
	logger  *slog.Logger
}

type ChildStart struct {
	AgentType string
	Scope     MemoryScope
}

type loadResult struct {
	prompt           string
	contentLength    int
	lineCount        int
	wasTruncated     bool
	wasByteTruncated bool
	unreadable       bool
}

type entrypointLoadResult struct {
	content          string
	warning          string
	contentLength    int
	lineCount        int
	wasTruncated     bool
	wasByteTruncated bool
	unreadable       bool
}

var _ contract.DynamicSectionProvider = (*PromptProvider)(nil)

func NewManager(cfg *Config, paths PathHelper, builder PromptBuilder) *Manager {
	return &Manager{
		cfg:     agentConfig(cfg),
		paths:   paths,
		builder: builder,
	}
}

func NewPromptProvider(cfg *Config, manager *Manager, gates GateResolver, logger *slog.Logger) *PromptProvider {
	return &PromptProvider{
		cfg:     agentConfig(cfg),
		manager: manager,
		gates:   gates,
		logger:  logger,
	}
}

func (m *Manager) GetAgentMemoryDir(agentType string, scope MemoryScope) (string, error) {
	parent, err := m.scopeParentDir(scope)
	if err != nil {
		return "", err
	}
	dirName, err := resolveAgentDirName(parent, agentType)
	if err != nil {
		return "", err
	}
	return filepath.Join(parent, dirName), nil
}

func (m *Manager) GetAgentMemoryScopeRoot(scope MemoryScope) (string, error) {
	return m.scopeParentDir(scope)
}

func (m *Manager) EnsureAgentMemoryDir(agentType string, scope MemoryScope) error {
	dir, err := m.GetAgentMemoryDir(agentType, scope)
	if err != nil {
		return err
	}
	return ensureAgentMemoryDir(dir, m.pathHelper().MemoryIndexPath(dir))
}

func (m *Manager) LoadAgentMemoryPrompt(agentType string, scope MemoryScope) (string, error) {
	if err := m.EnsureAgentMemoryDir(agentType, scope); err != nil {
		return "", err
	}
	result, err := m.loadAgentMemoryPrompt(agentType, scope)
	if err != nil {
		return "", err
	}
	return result.prompt, nil
}

// loadAgentMemoryEntrypoint reads an agent's MEMORY.md under the supplied
// scope root. It uses memshared.SafeReadEntrypoint so a symlinked or
// path-traversal-escaped entrypoint cannot redirect the reader to a file
// outside scopeRoot. Any failure (missing file, broken symlink, containment
// violation) collapses into the existing "unreadable" branch.
func loadAgentMemoryEntrypoint(scopeRoot, path string) entrypointLoadResult {
	raw, _, err := memshared.SafeReadEntrypoint(scopeRoot, path)
	if err != nil {
		return entrypointLoadResult{
			content:    emptyAgentMemoryPrompt,
			warning:    "> " + unreadableAgentMemoryMsg,
			unreadable: true,
		}
	}
	trimmed := strings.TrimSpace(parse.StripUTF8BOM(string(raw)))
	if trimmed == "" {
		return entrypointLoadResult{content: emptyAgentMemoryPrompt}
	}
	truncated := truncateAgentMemoryContent(trimmed)
	return entrypointLoadResult{
		content:          truncated.content,
		contentLength:    len(trimmed),
		lineCount:        truncated.lineCount,
		wasTruncated:     truncated.wasLineTruncated || truncated.wasByteTruncated,
		wasByteTruncated: truncated.wasByteTruncated,
	}
}

func (r entrypointLoadResult) contentBlock() string {
	return strings.TrimSpace(r.content)
}

func (r entrypointLoadResult) warningBlock() string {
	return strings.TrimSpace(r.warning)
}

func (m *Manager) GetAgentMemoryEntrypoint(agentType string, scope MemoryScope) (string, error) {
	dir, err := m.GetAgentMemoryDir(agentType, scope)
	if err != nil {
		return "", err
	}
	return m.pathHelper().MemoryIndexPath(dir), nil
}

func (m *Manager) IsAgentMemoryPath(path string) bool {
	candidate, err := m.pathHelper().CleanAbsolute(path)
	if err != nil {
		return false
	}
	for _, scope := range []MemoryScope{MemoryScopeUser, MemoryScopeProject, MemoryScopeLocal} {
		parent, err := m.scopeParentDir(scope)
		if err != nil {
			continue
		}
		root, err := m.pathHelper().CleanAbsolute(parent)
		if err == nil && platformshared.ContainsPath(root, candidate) {
			return true
		}
	}
	return false
}

func (m *Manager) loadAgentMemoryPrompt(agentType string, scope MemoryScope) (loadResult, error) {
	entrypoint, err := m.GetAgentMemoryEntrypoint(agentType, scope)
	if err != nil {
		return loadResult{}, err
	}
	scopeRoot, err := m.GetAgentMemoryScopeRoot(scope)
	if err != nil {
		return loadResult{}, err
	}
	entrypointResult := loadAgentMemoryEntrypoint(scopeRoot, entrypoint)
	extraGuidelines := append(scopeGuidelines(scope), cloneStrings(m.config().ExtraGuidelines)...)
	rules := ""
	if m.builder != nil {
		rules = strings.TrimSpace(m.builder.BuildPrompt(scope, extraGuidelines))
	}
	parts := nonEmpty([]string{rules, "## MEMORY.md", entrypointResult.warningBlock(), entrypointResult.contentBlock()})
	promptText := strings.TrimSpace(strings.Join(parts, "\n\n"))
	return loadResult{
		prompt:           promptText,
		contentLength:    entrypointResult.contentLength,
		lineCount:        entrypointResult.lineCount,
		wasTruncated:     entrypointResult.wasTruncated,
		wasByteTruncated: entrypointResult.wasByteTruncated,
		unreadable:       entrypointResult.unreadable,
	}, nil
}

func (p *PromptProvider) SectionName() string {
	return contract.DynamicSectionAgentMemory
}

func (p *PromptProvider) unavailable() bool {
	return p == nil || p.manager == nil || p.gates == nil
}

func (p *PromptProvider) Resolve(_ context.Context, input contract.SectionContext) (*string, error) {
	if p.unavailable() {
		return nil, nil
	}
	if !p.gates.AutoEnabled(input.BuildCtx) {
		return nil, nil
	}
	if p.gates.SuppressForOverlay(input.BuildCtx) {
		return nil, nil
	}
	meta, ok := ResolveChildAgentStart(input)
	if !ok {
		return nil, nil
	}
	if err := p.manager.EnsureAgentMemoryDir(meta.AgentType, meta.Scope); err != nil {
		if p.logger != nil {
			p.logger.Warn("agent memory prompt preload failed",
				"memory_type", "agent",
				"agent_type", meta.AgentType,
				"scope", string(meta.Scope),
				"scope_display", ScopeDisplay(meta.Scope),
				"status", agentMemoryFailureStatus(meta.Scope, err),
				"error", err,
			)
		}
		return nil, nil
	}
	result, err := p.manager.loadAgentMemoryPrompt(meta.AgentType, meta.Scope)
	if err != nil {
		if p.logger != nil {
			p.logger.Warn("agent memory prompt load failed",
				"memory_type", "agent",
				"agent_type", meta.AgentType,
				"scope", string(meta.Scope),
				"scope_display", ScopeDisplay(meta.Scope),
				"status", agentMemoryFailureStatus(meta.Scope, err),
				"error", err,
			)
		}
		return nil, nil
	}
	text := strings.TrimSpace(result.prompt)
	if text == "" {
		return nil, nil
	}
	if p.logger != nil {
		p.logger.Info("agent memory prompt loaded",
			"memory_type", "agent",
			"agent_type", meta.AgentType,
			"scope", string(meta.Scope),
			"scope_display", ScopeDisplay(meta.Scope),
			"content_length", result.contentLength,
			"line_count", result.lineCount,
			"was_truncated", result.wasTruncated,
			"was_byte_truncated", result.wasByteTruncated,
			"status", agentMemorySuccessStatus(result),
		)
	}
	return &text, nil
}

func ensureAgentMemoryDir(dir, entrypoint string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	info, err := os.Stat(entrypoint)
	if err == nil {
		if info.IsDir() {
			return fmt.Errorf("agent memory entrypoint is a directory: %s", entrypoint)
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	file, err := os.OpenFile(entrypoint, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		if errors.Is(err, os.ErrExist) {
			return nil
		}
		return err
	}
	return file.Close()
}

func (m *Manager) scopeParentDir(scope MemoryScope) (string, error) {
	switch scope {
	case MemoryScopeUser:
		return m.userScopeParentDir()
	case MemoryScopeProject:
		return m.projectScopeParentDir()
	case MemoryScopeLocal:
		return m.localScopeParentDir()
	default:
		return "", fmt.Errorf("%w: %q", ErrInvalidAgentScope, scope)
	}
}

func (m *Manager) userScopeParentDir() (string, error) {
	root, err := m.pathHelper().ValidateRoot(strings.TrimSpace(m.config().RootDir))
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", fmt.Errorf("%w: empty root", ErrInvalidMemoryRoot)
	}
	return filepath.Join(strings.TrimSuffix(root, string(os.PathSeparator)), agentMemoryDirName), nil
}

func (m *Manager) projectScopeParentDir() (string, error) {
	root, err := m.projectRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, agentMemoryConfigDirName, agentMemoryDirName), nil
}

func (m *Manager) localScopeParentDir() (string, error) {
	root, err := m.projectRootDir()
	if err != nil {
		return "", err
	}
	remoteRoot, err := m.localScopeRemoteRoot()
	if err != nil {
		return "", err
	}
	if remoteRoot == "" {
		return filepath.Join(root, agentMemoryConfigDirName, agentMemoryLocalDirName), nil
	}
	canonicalRoot, err := m.pathHelper().CanonicalGitRoot(context.Background(), root)
	if err != nil || strings.TrimSpace(canonicalRoot) == "" {
		canonicalRoot = root
	}
	return filepath.Join(remoteRoot, "projects", m.pathHelper().SanitizePath(canonicalRoot), agentMemoryLocalDirName), nil
}

func (m *Manager) localScopeRemoteRoot() (string, error) {
	remoteRoot := strings.TrimSpace(os.Getenv(envClaudeRemoteMemoryDir))
	if remoteRoot == "" {
		return "", nil
	}
	validatedRoot, err := m.pathHelper().ValidateRoot(remoteRoot)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", fmt.Errorf("%w: empty root", ErrInvalidMemoryRoot)
	}
	return strings.TrimSuffix(validatedRoot, string(os.PathSeparator)), nil
}

func (m *Manager) projectRootDir() (string, error) {
	projectRoot := strings.TrimSpace(m.config().ProjectRoot)
	if projectRoot == "" {
		return "", ErrInvalidProjectDir
	}
	cleaned, err := m.pathHelper().CleanAbsolute(projectRoot)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidProjectDir, err)
	}
	return cleaned, nil
}

func (m *Manager) pathHelper() PathHelper {
	if m != nil && m.paths != nil {
		return m.paths
	}
	return defaultPathHelper{}
}

func (m *Manager) config() Config {
	if m == nil || m.cfg == nil {
		return Config{}
	}
	return *m.cfg
}

func ResolveChildAgentStart(input contract.SectionContext) (ChildStart, bool) {
	if input.Start == nil || input.Turn != nil || strings.TrimSpace(input.Start.ParentAgentID) == "" {
		return ChildStart{}, false
	}
	scope, ok := parseAgentMemoryScope(input.Start.AgentMemoryScope)
	agentType := strings.TrimSpace(input.Start.AgentType)
	if !ok || agentType == "" {
		return ChildStart{}, false
	}
	return ChildStart{AgentType: agentType, Scope: scope}, true
}

func ScopeDisplay(scope MemoryScope) string {
	switch scope {
	case MemoryScopeUser:
		return "user-scoped agent memory"
	case MemoryScopeLocal:
		return "local-scoped agent memory"
	default:
		return "project-scoped agent memory"
	}
}

func scopeGuidelines(scope MemoryScope) []string {
	guidelines := []string{
		"This prompt is for agent-specific memory. Keep it isolated from the main thread and other agent types.",
		"Searching past context: consult the MEMORY.md section below before assuming nothing has been remembered for this agent.",
	}
	switch scope {
	case MemoryScopeUser:
		return append(guidelines, "This user-scoped agent memory is shared across projects for the same agent type.")
	case MemoryScopeProject:
		return append(guidelines, "This project-scoped agent memory is isolated to the current project and can be shared with the repo.")
	case MemoryScopeLocal:
		return append(guidelines, "This local agent memory is isolated to the current project on this machine only.")
	default:
		return guidelines
	}
}

func agentMemoryFailureStatus(scope MemoryScope, err error) string {
	if err == nil {
		return "loaded"
	}
	if errors.Is(err, ErrInvalidProjectDir) && scope == MemoryScopeLocal {
		return "local_unavailable"
	}
	if errors.Is(err, ErrInvalidProjectDir) || errors.Is(err, ErrInvalidAgentScope) || errors.Is(err, ErrInvalidMemoryRoot) {
		return "deny"
	}
	return "ensure_dir_failed"
}

func agentMemorySuccessStatus(result loadResult) string {
	if result.unreadable {
		return "unreadable"
	}
	if result.wasTruncated || result.wasByteTruncated {
		return "truncated"
	}
	return "loaded"
}

func agentConfig(cfg *Config) *Config {
	if cfg == nil {
		return &Config{}
	}
	cloned := *cfg
	cloned.ExtraGuidelines = cloneStrings(cfg.ExtraGuidelines)
	return &cloned
}

func parseAgentMemoryScope(raw string) (MemoryScope, bool) {
	switch MemoryScope(strings.ToLower(strings.TrimSpace(raw))) {
	case MemoryScopeUser:
		return MemoryScopeUser, true
	case MemoryScopeLocal:
		return MemoryScopeLocal, true
	case MemoryScopeProject:
		return MemoryScopeProject, true
	default:
		return "", false
	}
}

func resolveAgentDirName(parent, agentType string) (string, error) {
	sanitized := SanitizeAgentType(agentType)
	if sanitized == "" {
		return "", fmt.Errorf("%w: %q", ErrInvalidAgentType, strings.TrimSpace(agentType))
	}
	if hasAgentDirConflict(parent, sanitized) {
		return fallbackAgentTypeName(normalizeAgentType(agentType)), nil
	}
	return sanitized, nil
}

func hasAgentDirConflict(parent, dirName string) bool {
	entries, err := os.ReadDir(parent)
	if err != nil {
		return false
	}
	want := canonicalName(dirName)
	for _, entry := range entries {
		if entry.Name() != dirName && canonicalName(entry.Name()) == want {
			return true
		}
	}
	return false
}
