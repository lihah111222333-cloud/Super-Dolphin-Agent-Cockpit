package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	agentMemoryDirName       = "agent-memory"
	agentMemoryLocalDirName  = "agent-memory-local"
	agentMemoryConfigDirName = ".claude"
	emptyAgentMemoryPrompt   = "Your MEMORY.md is currently empty. Save only durable, agent-specific context here."
	unreadableAgentMemoryMsg = "WARNING: MEMORY.md could not be read; continuing with empty agent memory."
)

var (
	ErrInvalidAgentType  = errors.New("invalid agent type")
	ErrInvalidAgentScope = errors.New("invalid agent memory scope")
	ErrInvalidProjectDir = errors.New("invalid agent project root")
)

var _ prompt.DynamicSectionProvider = (*AgentMemoryPromptProvider)(nil)

type AgentMemoryManager struct {
	cfg *Config
}

type AgentMemoryPromptProvider struct {
	cfg     *Config
	manager *AgentMemoryManager
	logger  *slog.Logger
}

type childAgentStart struct {
	agentType string
	scope     MemoryScope
}

type agentMemoryLoadResult struct {
	prompt           string
	contentLength    int
	lineCount        int
	wasTruncated     bool
	wasByteTruncated bool
	unreadable       bool
}

type agentEntrypointLoadResult struct {
	content          string
	warning          string
	contentLength    int
	lineCount        int
	wasTruncated     bool
	wasByteTruncated bool
	unreadable       bool
}

func NewAgentMemoryManager(cfg *Config) *AgentMemoryManager {
	return &AgentMemoryManager{cfg: memoryConfig(cfg)}
}

func NewAgentMemoryPromptProvider(cfg *Config, manager *AgentMemoryManager, logger *slog.Logger) *AgentMemoryPromptProvider {
	return &AgentMemoryPromptProvider{
		cfg:     memoryConfig(cfg),
		manager: manager,
		logger:  logger,
	}
}

func (m *AgentMemoryManager) GetAgentMemoryDir(agentType string, scope MemoryScope) (string, error) {
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

func (m *AgentMemoryManager) EnsureAgentMemoryDir(agentType string, scope MemoryScope) error {
	dir, err := m.GetAgentMemoryDir(agentType, scope)
	if err != nil {
		return err
	}
	return ensureAgentMemoryDir(dir)
}

func (m *AgentMemoryManager) LoadAgentMemoryPrompt(agentType string, scope MemoryScope) (string, error) {
	if err := m.EnsureAgentMemoryDir(agentType, scope); err != nil {
		return "", err
	}
	result, err := m.loadAgentMemoryPrompt(agentType, scope)
	if err != nil {
		return "", err
	}
	return result.prompt, nil
}

func loadAgentMemoryEntrypoint(path string) agentEntrypointLoadResult {
	raw, err := os.ReadFile(path)
	if err != nil {
		return agentEntrypointLoadResult{
			content:    emptyAgentMemoryPrompt,
			warning:    "> " + unreadableAgentMemoryMsg,
			unreadable: true,
		}
	}
	trimmed := strings.TrimSpace(stripUTF8BOM(string(raw)))
	if trimmed == "" {
		return agentEntrypointLoadResult{content: emptyAgentMemoryPrompt}
	}
	truncated := truncateAgentMemoryContent(trimmed)
	return agentEntrypointLoadResult{
		content:          truncated.content,
		contentLength:    len(trimmed),
		lineCount:        truncated.lineCount,
		wasTruncated:     truncated.wasLineTruncated || truncated.wasByteTruncated,
		wasByteTruncated: truncated.wasByteTruncated,
	}
}

func (r agentEntrypointLoadResult) contentBlock() string {
	return strings.TrimSpace(r.content)
}

func (r agentEntrypointLoadResult) warningBlock() string {
	return strings.TrimSpace(r.warning)
}

func (m *AgentMemoryManager) GetAgentMemoryEntrypoint(agentType string, scope MemoryScope) (string, error) {
	dir, err := m.GetAgentMemoryDir(agentType, scope)
	if err != nil {
		return "", err
	}
	return memoryIndexPath(dir), nil
}

func (m *AgentMemoryManager) IsAgentMemoryPath(path string) bool {
	candidate, err := cleanAbsolutePath(path)
	if err != nil {
		return false
	}
	for _, scope := range []MemoryScope{MemoryScopeUser, MemoryScopeProject, MemoryScopeLocal} {
		parent, err := m.scopeParentDir(scope)
		if err != nil {
			continue
		}
		root, err := cleanAbsolutePath(parent)
		if err == nil && platformshared.ContainsPath(root, candidate) {
			return true
		}
	}
	return false
}

func (m *AgentMemoryManager) loadAgentMemoryPrompt(agentType string, scope MemoryScope) (agentMemoryLoadResult, error) {
	entrypoint, err := m.GetAgentMemoryEntrypoint(agentType, scope)
	if err != nil {
		return agentMemoryLoadResult{}, err
	}
	entrypointResult := loadAgentMemoryEntrypoint(entrypoint)
	cfg := m.config()
	gate := ResolveMemoryGate(contract.BuildCtx{}, &cfg)
	rules := strings.TrimSpace(BuildMemoryLines(false, gate.SearchPastContextEnabled, append(
		agentMemoryGuidelines(scope),
		cloneStrings(m.config().ExtraGuidelines)...,
	)))
	parts := nonEmpty([]string{rules, "## MEMORY.md", entrypointResult.warningBlock(), entrypointResult.contentBlock()})
	promptText := strings.TrimSpace(strings.Join(parts, "\n\n"))
	return agentMemoryLoadResult{
		prompt:           promptText,
		contentLength:    entrypointResult.contentLength,
		lineCount:        entrypointResult.lineCount,
		wasTruncated:     entrypointResult.wasTruncated,
		wasByteTruncated: entrypointResult.wasByteTruncated,
		unreadable:       entrypointResult.unreadable,
	}, nil
}

func (p *AgentMemoryPromptProvider) SectionName() string {
	return prompt.DynamicSectionAgentMemory
}

func (p *AgentMemoryPromptProvider) Resolve(_ context.Context, input prompt.SectionContext) (*string, error) {
	if p == nil || p.manager == nil {
		return nil, nil
	}
	if !ResolveMemoryGate(input.BuildCtx, p.cfg).AutoEnabled {
		return nil, nil
	}
	meta, ok := resolveChildAgentStart(input)
	if !ok {
		return nil, nil
	}
	if !p.ensureAgentMemoryDir(meta) {
		return nil, nil
	}
	result, ok := p.loadAgentMemoryPrompt(meta)
	if !ok {
		return nil, nil
	}
	text := result.prompt
	if text = strings.TrimSpace(text); text == "" {
		return nil, nil
	}
	p.logAgentMemoryPromptLoaded(meta, result)
	return &text, nil
}

func (p *AgentMemoryPromptProvider) ensureAgentMemoryDir(meta childAgentStart) bool {
	if err := p.manager.EnsureAgentMemoryDir(meta.agentType, meta.scope); err != nil {
		p.logAgentMemoryPromptFailure("agent memory prompt preload failed", meta, err)
		return false
	}
	return true
}

func (p *AgentMemoryPromptProvider) loadAgentMemoryPrompt(meta childAgentStart) (agentMemoryLoadResult, bool) {
	result, err := p.manager.loadAgentMemoryPrompt(meta.agentType, meta.scope)
	if err != nil {
		p.logAgentMemoryPromptFailure("agent memory prompt load failed", meta, err)
		return agentMemoryLoadResult{}, false
	}
	return result, true
}

func (p *AgentMemoryPromptProvider) logAgentMemoryPromptFailure(message string, meta childAgentStart, err error) {
	if p == nil || p.logger == nil || err == nil {
		return
	}
	p.logger.Warn(message,
		"memory_type", "agent",
		"agent_type", meta.agentType,
		"scope", string(meta.scope),
		"scope_display", GetMemoryScopeDisplay(meta.scope),
		"status", agentMemoryFailureStatus(meta.scope, err),
		"error", err,
	)
}

func (p *AgentMemoryPromptProvider) logAgentMemoryPromptLoaded(meta childAgentStart, result agentMemoryLoadResult) {
	if p == nil || p.logger == nil {
		return
	}
	p.logger.Info("agent memory prompt loaded",
		"memory_type", "agent",
		"agent_type", meta.agentType,
		"scope", string(meta.scope),
		"scope_display", GetMemoryScopeDisplay(meta.scope),
		"content_length", result.contentLength,
		"line_count", result.lineCount,
		"was_truncated", result.wasTruncated,
		"was_byte_truncated", result.wasByteTruncated,
		"status", agentMemorySuccessStatus(result),
	)
}

func resolveChildAgentStart(input prompt.SectionContext) (childAgentStart, bool) {
	if input.Start == nil || input.Turn != nil || strings.TrimSpace(input.Start.ParentAgentID) == "" {
		return childAgentStart{}, false
	}
	scope, ok := parseAgentMemoryScope(input.Start.AgentMemoryScope)
	agentType := strings.TrimSpace(input.Start.AgentType)
	if !ok || agentType == "" {
		return childAgentStart{}, false
	}
	return childAgentStart{agentType: agentType, scope: scope}, true
}

func ensureAgentMemoryDir(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	entrypoint := memoryIndexPath(dir)
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

func (m *AgentMemoryManager) scopeParentDir(scope MemoryScope) (string, error) {
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

func (m *AgentMemoryManager) userScopeParentDir() (string, error) {
	root, err := ValidateMemoryRoot(strings.TrimSpace(m.config().RootDir))
	if err != nil {
		return "", err
	}
	if root == "" {
		return "", fmt.Errorf("%w: empty root", ErrInvalidMemoryRoot)
	}
	return filepath.Join(strings.TrimSuffix(root, string(os.PathSeparator)), agentMemoryDirName), nil
}

func (m *AgentMemoryManager) projectScopeParentDir() (string, error) {
	root, err := m.projectRootDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, agentMemoryConfigDirName, agentMemoryDirName), nil
}

func (m *AgentMemoryManager) localScopeParentDir() (string, error) {
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
	canonicalRoot, err := FindCanonicalGitRoot(context.Background(), root)
	if err != nil || strings.TrimSpace(canonicalRoot) == "" {
		canonicalRoot = root
	}
	return filepath.Join(remoteRoot, memoryProjectsDir, SanitizePath(canonicalRoot), agentMemoryLocalDirName), nil
}

func (m *AgentMemoryManager) localScopeRemoteRoot() (string, error) {
	remoteRoot := strings.TrimSpace(os.Getenv(envClaudeRemoteMemoryDir))
	if remoteRoot == "" {
		return "", nil
	}
	validatedRoot, err := ValidateMemoryRoot(remoteRoot)
	if err != nil {
		return "", err
	}
	if validatedRoot == "" {
		return "", fmt.Errorf("%w: empty root", ErrInvalidMemoryRoot)
	}
	return strings.TrimSuffix(validatedRoot, string(os.PathSeparator)), nil
}

func (m *AgentMemoryManager) projectRootDir() (string, error) {
	projectRoot := strings.TrimSpace(m.config().ProjectRoot)
	if projectRoot == "" {
		return "", ErrInvalidProjectDir
	}
	cleaned, err := cleanAbsolutePath(projectRoot)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidProjectDir, err)
	}
	return cleaned, nil
}

func (m *AgentMemoryManager) config() Config {
	if m == nil || m.cfg == nil {
		return Config{}
	}
	return *m.cfg
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
	want := CanonicalName(dirName)
	for _, entry := range entries {
		if entry.Name() != dirName && CanonicalName(entry.Name()) == want {
			return true
		}
	}
	return false
}

func agentMemoryGuidelines(scope MemoryScope) []string {
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
