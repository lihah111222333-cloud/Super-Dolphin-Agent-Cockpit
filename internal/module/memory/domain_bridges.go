// Package memory compatibility bridges for the agent-memory and team-memory
// subpackage migrations. Owned by those subpackage splits; keep here until
// root callers move to direct memory/agent and memory/team imports.
//
// This file consolidates the former agent_bridge.go + team_bridge.go to
// conserve the main-package file budget.
package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	memagent "github.com/anthropic-ai/super-agent-v3/internal/module/memory/agent"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	teampkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/team"
)

// ==== agent-memory bridge ====

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

func (m *AgentMemoryManager) GetAgentMemoryScopeRoot(scope MemoryScope) (string, error) {
	if m == nil || m.inner == nil {
		return "", errors.New("agent memory manager is nil")
	}
	return m.inner.GetAgentMemoryScopeRoot(memagent.MemoryScope(scope))
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
	validated, err := shared.ValidateMemoryRoot(raw)
	if errors.Is(err, ErrInvalidMemoryRoot) {
		return "", fmt.Errorf("%w: %v", memagent.ErrInvalidMemoryRoot, err)
	}
	return validated, err
}

func (agentPathHelper) CleanAbsolute(raw string) (string, error) {
	return shared.CleanAbsolutePath(raw)
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

// ==== team-memory bridge ====

type TeamMemoryManager = teampkg.TeamMemoryManager
type TeamMemoryGuard = teampkg.TeamMemoryGuard
type TeamMemSecretFinding = teampkg.TeamMemSecretFinding
type TeamMemSkippedFile = teampkg.TeamMemSkippedFile
type TeamMemPrePushScanResult = teampkg.TeamMemPrePushScanResult
type TeamMemSecretError = teampkg.TeamMemSecretError

const teamMemoryRootDirName = teampkg.RootDirName

var ErrTeamMemSecretDetected = teampkg.ErrTeamMemSecretDetected

func provideTeamConfig(cfg *Config) teampkg.Config {
	return teamConfigAdapter{cfg: memoryConfig(cfg)}
}

func NewTeamMemoryManager(cfg *Config) *TeamMemoryManager {
	return teampkg.NewTeamMemoryManager(provideTeamConfig(cfg))
}

func NewTeamMemoryGuard(manager *TeamMemoryManager) *TeamMemoryGuard {
	return teampkg.NewTeamMemoryGuard(manager)
}

func teamMemoryConfigured(cfg Config) bool {
	return cfg.Enabled && cfg.Features.TeamMemory
}

func configuredTeamMemRoot(cfg *Config, buildCtx ...contract.BuildCtx) (string, error) {
	return provideTeamConfig(cfg).TeamRoot(firstTeamBuildCtx(buildCtx))
}

func configuredTeamMemPath(m *TeamMemoryManager, buildCtx ...contract.BuildCtx) (string, error) {
	return teampkg.ConfiguredTeamMemPath(m, buildCtx...)
}

func teamMemPath(m *TeamMemoryManager, buildCtx contract.BuildCtx) string {
	if m == nil {
		return ""
	}
	return m.GetTeamMemPath(buildCtx)
}

func isTeamMemoryEnabled(m *TeamMemoryManager, buildCtx contract.BuildCtx) bool {
	if m == nil {
		return false
	}
	return m.IsTeamMemoryEnabled(buildCtx)
}

func validateTeamMemWriteRequest(m *TeamMemoryManager, raw string) error {
	if m == nil {
		return ErrTeamMemoryDisabled
	}
	return m.ValidateTeamMemWritePath(raw)
}

func validateTeamMemKeyRequest(m *TeamMemoryManager, key string) error {
	if m == nil {
		return ErrTeamMemoryDisabled
	}
	return m.ValidateTeamMemKey(key)
}

func firstTeamBuildCtx(buildCtx []contract.BuildCtx) contract.BuildCtx {
	if len(buildCtx) == 0 {
		return contract.BuildCtx{}
	}
	return buildCtx[0]
}

func setTeamMemoryRuntimeReady(ready bool) {
	teampkg.SetRuntimeReady(ready)
}

func ScanTeamMemContent(content string) []TeamMemSecretFinding {
	return teampkg.ScanTeamMemContent(content)
}

type teamConfigAdapter struct {
	cfg *Config
}

func (a teamConfigAdapter) Gate(buildCtx contract.BuildCtx) teampkg.GateSnapshot {
	gate := ResolveMemoryGate(buildCtx, a.cfg)
	return teampkg.GateSnapshot{
		AutoEnabled:    gate.AutoEnabled,
		TeamMemEnabled: gate.TeamMemEnabled,
		KairosActive:   gate.KairosActive,
	}
}

func (a teamConfigAdapter) TeamRoot(buildCtx contract.BuildCtx) (string, error) {
	cfg := memoryConfig(a.cfg)
	projectRoot := a.ProjectRoot(buildCtx)
	if projectRoot == "" && strings.TrimSpace(cfg.AutoMemPathOverride) == "" {
		return "", ErrInvalidProjectDir
	}
	root, err := resolvedStoreRoot(cfg.RootDir, projectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		return "", err
	}
	cleaned, err := shared.CleanAbsolutePath(root)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidMemoryRoot, err)
	}
	return filepath.Join(cleaned, teampkg.RootDirName), nil
}

func (a teamConfigAdapter) ProjectRoot(buildCtx contract.BuildCtx) string {
	if buildCtx.GitRoot != "" {
		return strings.TrimSpace(buildCtx.GitRoot)
	}
	if buildCtx.CWD != "" {
		return strings.TrimSpace(buildCtx.CWD)
	}
	if a.cfg == nil {
		return ""
	}
	return strings.TrimSpace(a.cfg.ProjectRoot)
}
