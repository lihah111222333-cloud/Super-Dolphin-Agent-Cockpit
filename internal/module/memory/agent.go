package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	platformshared "github.com/anthropic-ai/super-agent-v3/internal/platform/shared"
)

const (
	agentMemoryDirName           = "agents"
	agentMemoryLocalDirName      = "local"
	defaultChildAgentMemoryScope = MemoryScopeProject
	emptyAgentMemoryPrompt       = "Your MEMORY.md is currently empty. Save only durable, agent-specific context here."
	unreadableAgentMemoryMsg     = "WARNING: MEMORY.md could not be read; continuing with empty agent memory."
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
	dir, err := m.GetAgentMemoryDir(agentType, scope)
	if err != nil {
		return "", err
	}
	_ = ensureAgentMemoryDir(dir)
	content, warning := loadAgentMemoryEntrypoint(memoryIndexPath(dir))
	rules := strings.TrimSpace(BuildMemoryLines(false, agentMemoryGuidelines(scope)))
	parts := nonEmpty([]string{rules, warning, "## MEMORY.md", content})
	return strings.Join(parts, "\n\n"), nil
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
	text, err := p.manager.LoadAgentMemoryPrompt(meta.agentType, meta.scope)
	if err != nil {
		if p.logger != nil {
			p.logger.Warn("agent memory prompt load failed",
				"agent_type", meta.agentType,
				"scope", string(meta.scope),
				"error", err,
			)
		}
		return nil, nil
	}
	if text = strings.TrimSpace(text); text == "" {
		return nil, nil
	}
	return &text, nil
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
		root, err := m.projectRootDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "memory", agentMemoryDirName), nil
	case MemoryScopeLocal:
		root, err := m.projectRootDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(root, "memory", agentMemoryLocalDirName), nil
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

func loadAgentMemoryEntrypoint(path string) (string, string) {
	parsed, err := ParseMemoryFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return emptyAgentMemoryPrompt, ""
		}
		return emptyAgentMemoryPrompt, unreadableAgentMemoryMsg
	}
	if strings.TrimSpace(parsed.Content) == "" {
		return emptyAgentMemoryPrompt, ""
	}
	return parsed.Content, ""
}

func resolveChildAgentStart(input prompt.SectionContext) (childAgentStart, bool) {
	if input.Start == nil || input.Turn != nil {
		return childAgentStart{}, false
	}
	if strings.TrimSpace(input.Start.ParentAgentID) == "" {
		return childAgentStart{}, false
	}
	agentType := strings.TrimSpace(platformshared.FirstNonEmpty(input.Start.AgentType, input.Start.Name))
	if agentType == "" {
		return childAgentStart{}, false
	}
	return childAgentStart{
		agentType: agentType,
		scope:     defaultChildAgentMemoryScope,
	}, true
}
