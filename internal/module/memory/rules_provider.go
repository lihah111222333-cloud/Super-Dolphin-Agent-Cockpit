package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"go.uber.org/fx"
)

var (
	_ prompt.DynamicSectionProvider = (*MemoryRulesProvider)(nil)
	_ prompt.DynamicSectionProvider = (*MemoryContextProvider)(nil)
)

type MemoryRulesProvider struct {
	engine          *MemoryRuleEngine
	autoEnabled     bool
	skipIndex       bool
	extraGuidelines []string
	features        MemoryFeatureFlags
}

type promptProviderParams struct {
	fx.In

	Registry        prompt.PromptRegistry      `optional:"true"`
	Provider        *MemoryRulesProvider       `optional:"true"`
	AgentProvider   *AgentMemoryPromptProvider `optional:"true"`
	ContextProvider *MemoryContextProvider     `optional:"true"`
}

func NewRulesProvider(cfg *Config, engine *MemoryRuleEngine) *MemoryRulesProvider {
	autoEnabled := cfg != nil && cfg.IsMemoryEnabled()
	skipIndex := cfg != nil && cfg.SkipIndex
	features := MemoryFeatureFlags{}
	var extraGuidelines []string
	if cfg != nil {
		extraGuidelines = cloneStrings(cfg.ExtraGuidelines)
		features = cfg.Features
	}
	if engine == nil {
		engine = NewMemoryRuleEngine()
	}
	return &MemoryRulesProvider{
		engine:          engine,
		autoEnabled:     autoEnabled,
		skipIndex:       skipIndex,
		extraGuidelines: extraGuidelines,
		features:        features,
	}
}

func (p *MemoryRulesProvider) SectionName() string {
	return prompt.DynamicSectionMemory
}

func (p *MemoryRulesProvider) Resolve(_ context.Context, input prompt.SectionContext) (*string, error) {
	if p == nil || input.Start == nil || input.Turn != nil {
		return nil, nil
	}
	if _, ok := resolveChildAgentStart(input); ok {
		return nil, nil
	}
	text := p.engine.LoadMemoryPrompt(MemoryModeStandard, p.autoEnabled, MemoryRuleOptions{
		SkipIndex:       p.skipIndex,
		ExtraGuidelines: p.resolvedExtraGuidelines(),
	})
	if text == nil || strings.TrimSpace(*text) == "" {
		return nil, nil
	}
	wrapped := "## " + prompt.DynamicSectionMemory + "\n\n" + strings.TrimSpace(*text)
	return &wrapped, nil
}

func (p *MemoryRulesProvider) resolvedExtraGuidelines() []string {
	if p == nil {
		return nil
	}
	guidelines := cloneStrings(p.extraGuidelines)
	if p.features.Kairos {
		guidelines = append(guidelines, "Feature flag `kairos` is enabled, but Kairos-specific memory prompts are not implemented yet; continue using the standard memory rules until that mode is wired.")
	}
	if p.features.TeamMemory {
		guidelines = append(guidelines, "Feature flag `teammem` is enabled, but team-memory prompt composition is not implemented yet; continue using the standard memory rules until that mode is wired.")
	}
	if p.features.SearchPastContext {
		guidelines = append(guidelines, "Feature flag `search_past_context` is enabled, but prompt-side search instructions still defer to runtime retrieval; keep relying on surfaced memory until that retrieval path is wired.")
	}
	if len(guidelines) == 0 {
		return nil
	}
	return guidelines
}

type MemoryContextProvider struct {
	enabled    bool
	memoryRoot string

	mu    sync.Mutex
	turns map[string]*prefetchTurnState
}

type prefetchTurnState struct {
	query   string
	turnID  string
	manager *PrefetchManager
	handle  *PrefetchHandle
}

func NewContextProvider(cfg *Config) *MemoryContextProvider {
	if cfg == nil {
		cfg = &Config{}
	}
	root, _ := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	return &MemoryContextProvider{
		enabled:    cfg.IsMemoryEnabled(),
		memoryRoot: strings.TrimSpace(root),
		turns:      map[string]*prefetchTurnState{},
	}
}

func (p *MemoryContextProvider) SectionName() string {
	return prompt.DynamicSectionMemoryContext
}

func (p *MemoryContextProvider) Resolve(_ context.Context, input prompt.SectionContext) (*string, error) {
	if p == nil || !p.enabled || input.Turn == nil {
		return nil, nil
	}
	p.rememberTurnQuery(input.Turn.ThreadID, input.Turn.UserText)
	if text, ok := p.consumePrefetchText(input.Turn.ThreadID); ok {
		return &text, nil
	}
	return p.loadMemoryIndexFallback()
}

func (p *MemoryContextProvider) rememberTurnQuery(threadID, query string) {
	threadID = strings.TrimSpace(threadID)
	query = strings.TrimSpace(query)
	if p == nil || !p.enabled || threadID == "" || query == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.turnStateLocked(threadID)
	if strings.TrimSpace(state.turnID) != "" {
		return
	}
	state.query = query
}

func (p *MemoryContextProvider) onTurnStarted(ctx context.Context, evt turndto.TurnStarted) {
	threadID := strings.TrimSpace(evt.ThreadID)
	turnID := strings.TrimSpace(evt.TurnID)
	if p == nil || !p.enabled || threadID == "" || turnID == "" || p.memoryRoot == "" {
		return
	}
	p.mu.Lock()
	state := p.turnStateLocked(threadID)
	state.turnID = turnID
	if state.manager == nil {
		state.manager = NewPrefetchManager(p.memoryRoot)
	}
	query := state.query
	manager := state.manager
	p.mu.Unlock()
	if manager == nil || query == "" {
		return
	}
	handle := manager.StartRelevantMemoryPrefetch(ctx, query)
	p.mu.Lock()
	if state := p.turnStateLocked(threadID); strings.EqualFold(strings.TrimSpace(state.turnID), turnID) {
		state.handle = handle
	}
	p.mu.Unlock()
}

func (p *MemoryContextProvider) onTurnTerminated(threadID, turnID string) {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if p == nil || threadID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.turns[threadID]
	if !ok {
		return
	}
	if turnID != "" && strings.TrimSpace(state.turnID) != "" && !strings.EqualFold(strings.TrimSpace(state.turnID), turnID) {
		return
	}
	if state.handle != nil && state.handle.cancel != nil {
		state.handle.cancel()
	}
	state.query = ""
	state.turnID = ""
	state.handle = nil
}

func (p *MemoryContextProvider) consumePrefetchText(threadID string) (string, bool) {
	entries, ok := p.consumePrefetchEntries(threadID)
	if !ok {
		return "", false
	}
	text := renderRelevantMemoryContext(entries)
	return text, strings.TrimSpace(text) != ""
}

func (p *MemoryContextProvider) consumePrefetchEntries(threadID string) ([]MemoryEntry, bool) {
	threadID = strings.TrimSpace(threadID)
	if p == nil || threadID == "" {
		return nil, false
	}
	p.mu.Lock()
	state, ok := p.turns[threadID]
	if !ok || state.manager == nil || state.handle == nil {
		p.mu.Unlock()
		return nil, false
	}
	manager := state.manager
	handle := state.handle
	p.mu.Unlock()
	entries, ok := manager.ConsumeIfReady(handle)
	if !ok || len(entries) == 0 {
		return nil, false
	}
	p.mu.Lock()
	if state := p.turnStateLocked(threadID); state.handle == handle {
		state.handle = nil
	}
	p.mu.Unlock()
	return entries, true
}

func (p *MemoryContextProvider) loadMemoryIndexFallback() (*string, error) {
	if p == nil || strings.TrimSpace(p.memoryRoot) == "" {
		return nil, nil
	}
	content, err := os.ReadFile(memoryIndexPath(p.memoryRoot))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, nil
	}
	trimmed := strings.TrimSpace(stripUTF8BOM(string(content)))
	if trimmed == "" {
		return nil, nil
	}
	text := "# MEMORY.md\nContents of MEMORY.md:\n" + truncateAgentMemoryContent(trimmed).content
	return &text, nil
}

func (p *MemoryContextProvider) turnStateLocked(threadID string) *prefetchTurnState {
	if p.turns == nil {
		p.turns = map[string]*prefetchTurnState{}
	}
	state, ok := p.turns[threadID]
	if !ok {
		state = &prefetchTurnState{}
		p.turns[threadID] = state
	}
	return state
}

func renderRelevantMemoryContext(entries []MemoryEntry) string {
	sections := []string{
		"# Relevant memory hints",
		"Use these retrieved memory notes if they help with the current turn.",
	}
	for _, entry := range entries {
		if section := formatMemoryHint(entry); section != "" {
			sections = append(sections, section)
		}
	}
	if len(sections) == 2 {
		return ""
	}
	return strings.Join(sections, "\n\n")
}

func formatMemoryHint(entry MemoryEntry) string {
	snippet := memoryHintSnippet(entry)
	if snippet == "" {
		return ""
	}
	lines := []string{"## " + memoryHintTitle(entry)}
	if path := strings.TrimSpace(entry.FilePath); path != "" {
		lines = append(lines, "Path: "+filepath.ToSlash(path))
	}
	lines = append(lines, snippet)
	return strings.Join(lines, "\n")
}

func memoryHintTitle(entry MemoryEntry) string {
	if name := strings.TrimSpace(entry.Frontmatter.Name); name != "" {
		return name
	}
	if base := strings.TrimSpace(strings.TrimSuffix(filepath.Base(entry.FilePath), filepath.Ext(entry.FilePath))); base != "" {
		return base
	}
	return "memory note"
}

func memoryHintSnippet(entry MemoryEntry) string {
	text := strings.TrimSpace(entry.Content)
	if text == "" {
		text = strings.TrimSpace(entry.Frontmatter.Description)
	}
	if text == "" {
		return ""
	}
	const limit = 360
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return strings.TrimSpace(string(runes[:limit])) + "…"
}

func registerPromptProviders(p promptProviderParams) error {
	if p.Registry == nil {
		return nil
	}
	providers := []prompt.DynamicSectionProvider{p.Provider, p.AgentProvider, p.ContextProvider}
	for _, provider := range providers {
		if provider == nil {
			continue
		}
		if err := p.Registry.RegisterDynamicProvider(provider); err != nil {
			return err
		}
	}
	return nil
}
