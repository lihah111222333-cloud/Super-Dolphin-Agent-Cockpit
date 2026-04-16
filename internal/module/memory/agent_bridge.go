// Package memory compatibility bridge for the agent memory subpackage migration.
// Owned by the agent memory subpackage split; keep here until root callers move
// to direct memory/agent imports, then delete.
package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	memagent "github.com/anthropic-ai/super-agent-v3/internal/module/memory/agent"
)

var (
	_ contract.DynamicSectionProvider = (*AgentMemoryPromptProvider)(nil)

	ErrInvalidAgentType  = memagent.ErrInvalidAgentType
	ErrInvalidAgentScope = memagent.ErrInvalidAgentScope
	ErrInvalidProjectDir = memagent.ErrInvalidProjectDir
)

type AgentMemoryManager struct {
	inner *memagent.Manager
}

type AgentMemoryPromptProvider struct {
	inner *memagent.PromptProvider
}

type childAgentStart struct {
	agentType string
	scope     MemoryScope
}

func NewAgentMemoryManager(cfg *Config) *AgentMemoryManager {
	return wrapAgentMemoryManager(memagent.NewManager(
		provideAgentMemoryConfig(cfg),
		provideAgentMemoryPathHelper(),
		provideAgentMemoryPromptBuilder(cfg),
	))
}

func NewAgentMemoryPromptProvider(cfg *Config, manager *AgentMemoryManager, logger *slog.Logger) *AgentMemoryPromptProvider {
	var inner *memagent.Manager
	if manager != nil {
		inner = manager.inner
	}
	return wrapAgentMemoryPromptProvider(memagent.NewPromptProvider(
		provideAgentMemoryConfig(cfg),
		inner,
		provideAgentMemoryGateResolver(cfg),
		logger,
	))
}

func wrapAgentMemoryManager(inner *memagent.Manager) *AgentMemoryManager {
	if inner == nil {
		return nil
	}
	return &AgentMemoryManager{inner: inner}
}

func wrapAgentMemoryPromptProvider(inner *memagent.PromptProvider) *AgentMemoryPromptProvider {
	if inner == nil {
		return nil
	}
	return &AgentMemoryPromptProvider{inner: inner}
}

func (m *AgentMemoryManager) GetAgentMemoryDir(agentType string, scope MemoryScope) (string, error) {
	if m == nil || m.inner == nil {
		return "", errors.New("agent memory manager is nil")
	}
	return m.inner.GetAgentMemoryDir(agentType, memagent.MemoryScope(scope))
}

func (m *AgentMemoryManager) EnsureAgentMemoryDir(agentType string, scope MemoryScope) error {
	if m == nil || m.inner == nil {
		return errors.New("agent memory manager is nil")
	}
	return m.inner.EnsureAgentMemoryDir(agentType, memagent.MemoryScope(scope))
}

func (m *AgentMemoryManager) LoadAgentMemoryPrompt(agentType string, scope MemoryScope) (string, error) {
	if m == nil || m.inner == nil {
		return "", errors.New("agent memory manager is nil")
	}
	return m.inner.LoadAgentMemoryPrompt(agentType, memagent.MemoryScope(scope))
}

func (m *AgentMemoryManager) GetAgentMemoryEntrypoint(agentType string, scope MemoryScope) (string, error) {
	if m == nil || m.inner == nil {
		return "", errors.New("agent memory manager is nil")
	}
	return m.inner.GetAgentMemoryEntrypoint(agentType, memagent.MemoryScope(scope))
}

func (m *AgentMemoryManager) IsAgentMemoryPath(path string) bool {
	return m != nil && m.inner != nil && m.inner.IsAgentMemoryPath(path)
}

func (p *AgentMemoryPromptProvider) SectionName() string {
	if p == nil || p.inner == nil {
		return contract.DynamicSectionAgentMemory
	}
	return p.inner.SectionName()
}

func (p *AgentMemoryPromptProvider) Resolve(ctx context.Context, input contract.SectionContext) (*string, error) {
	if p == nil || p.inner == nil {
		return nil, nil
	}
	return p.inner.Resolve(ctx, input)
}

func GetMemoryScopeDisplay(scope MemoryScope) string {
	return memagent.ScopeDisplay(memagent.MemoryScope(scope))
}

func resolveChildAgentStart(input contract.SectionContext) (childAgentStart, bool) {
	meta, ok := memagent.ResolveChildAgentStart(input)
	if !ok {
		return childAgentStart{}, false
	}
	return childAgentStart{
		agentType: meta.AgentType,
		scope:     MemoryScope(meta.Scope),
	}, true
}

func provideAgentMemoryConfig(cfg *Config) *memagent.Config {
	cfg = memoryConfig(cfg)
	return &memagent.Config{
		RootDir:         cfg.RootDir,
		ProjectRoot:     cfg.ProjectRoot,
		ExtraGuidelines: cloneStrings(cfg.ExtraGuidelines),
	}
}

func provideAgentMemoryPathHelper() memagent.PathHelper {
	return agentPathHelper{}
}

func provideAgentMemoryPromptBuilder(cfg *Config) memagent.PromptBuilder {
	return agentPromptBuilder{cfg: memoryConfig(cfg)}
}

func provideAgentMemoryGateResolver(cfg *Config) memagent.GateResolver {
	return agentGateResolver{cfg: memoryConfig(cfg)}
}

type agentPathHelper struct{}

func (agentPathHelper) ValidateRoot(raw string) (string, error) {
	validated, err := ValidateMemoryRoot(raw)
	if errors.Is(err, ErrInvalidMemoryRoot) {
		return "", fmt.Errorf("%w: %v", memagent.ErrInvalidMemoryRoot, err)
	}
	return validated, err
}

func (agentPathHelper) CleanAbsolute(raw string) (string, error) {
	return cleanAbsolutePath(raw)
}

func (agentPathHelper) CanonicalGitRoot(ctx context.Context, projectRoot string) (string, error) {
	return FindCanonicalGitRoot(ctx, projectRoot)
}

func (agentPathHelper) SanitizePath(raw string) string {
	return SanitizePath(raw)
}

func (agentPathHelper) MemoryIndexPath(root string) string {
	return memoryIndexPath(root)
}

type agentPromptBuilder struct {
	cfg *Config
}

func (b agentPromptBuilder) BuildPrompt(_ memagent.MemoryScope, extraGuidelines []string) string {
	gate := ResolveMemoryGate(contract.BuildCtx{}, memoryConfig(b.cfg))
	return BuildMemoryLines(false, gate.SearchPastContextEnabled, extraGuidelines)
}

type agentGateResolver struct {
	cfg *Config
}

func (r agentGateResolver) AutoEnabled(buildCtx contract.BuildCtx) bool {
	return ResolveMemoryGate(buildCtx, memoryConfig(r.cfg)).AutoEnabled
}
