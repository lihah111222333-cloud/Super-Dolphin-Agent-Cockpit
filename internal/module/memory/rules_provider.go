package memory

import (
	"context"
	"errors"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	"github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	"go.uber.org/fx"
)

var (
	_ prompt.DynamicSectionProvider    = (*MemoryRulesProvider)(nil)
	_ prompt.DynamicSectionProvider    = (*MemoryContextProvider)(nil)
	_ prompt.InvalidationAwareProvider = (*MemoryContextProvider)(nil)
)

type MemoryRulesProvider struct {
	cfg    *Config
	engine *MemoryRuleEngine
}

type promptProviderParams struct {
	fx.In

	Registry        prompt.PromptRegistry      `optional:"true"`
	Provider        *MemoryRulesProvider       `optional:"true"`
	AgentProvider   *AgentMemoryPromptProvider `optional:"true"`
	ContextProvider *MemoryContextProvider     `optional:"true"`
}

func NewRulesProvider(cfg *Config, engine *MemoryRuleEngine) *MemoryRulesProvider {
	cfg = memoryConfig(cfg)
	if engine == nil {
		engine = NewMemoryRuleEngine()
	}
	return &MemoryRulesProvider{cfg: cfg, engine: engine}
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
	gate := ResolveMemoryGate(input.BuildCtx, p.cfg)
	text := p.engine.LoadMemoryPrompt(gate.EffectiveMemoryMode, gate.AutoEnabled, MemoryRuleOptions{
		SkipIndex:       gate.SkipIndex,
		ExtraGuidelines: p.resolvedExtraGuidelines(),
	})
	if text == nil || strings.TrimSpace(*text) == "" {
		return nil, nil
	}
	wrapped := "## " + prompt.DynamicSectionMemory + "\n\n" + strings.TrimSpace(*text)
	return &wrapped, nil
}

func (p *MemoryRulesProvider) resolvedExtraGuidelines() []string {
	if p == nil || p.cfg == nil {
		return nil
	}
	guidelines := cloneStrings(p.cfg.ExtraGuidelines)
	if len(guidelines) == 0 {
		return nil
	}
	return guidelines
}

type MemoryContextProvider struct {
	cfg        *Config
	memoryRoot string
	timeNow    func() time.Time

	mu    sync.Mutex
	turns map[string]*prefetchTurnState
}

type prefetchTurnState struct {
	manager *PrefetchManager
	handle  *PrefetchHandle
	gate    MemoryGateSnapshot
}

func NewContextProvider(cfg *Config) *MemoryContextProvider {
	cfg = memoryConfig(cfg)
	root, _ := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	return &MemoryContextProvider{
		cfg:        cfg,
		memoryRoot: strings.TrimSpace(root),
		timeNow:    time.Now,
		turns:      map[string]*prefetchTurnState{},
	}
}

func (p *MemoryContextProvider) SectionName() string {
	return prompt.DynamicSectionMemoryContext
}

func (p *MemoryContextProvider) Resolve(_ context.Context, input prompt.SectionContext) (*string, error) {
	if p == nil || input.Turn == nil {
		return nil, nil
	}
	gate := ResolveMemoryGate(input.BuildCtx, p.cfg)
	p.rememberTurnGate(input.Turn.ThreadID, gate)
	if !gate.AutoEnabled && !gate.InjectMemoryIndex && !gate.EnableRelevantPrefetch {
		return nil, nil
	}
	if !gate.InjectMemoryIndex {
		return nil, nil
	}
	return p.loadMemoryIndexFallback()
}

func (p *MemoryContextProvider) PrepareTurnInputs(
	ctx context.Context,
	session contract.Session,
	buildCtx contract.BuildCtx,
	threadID, query string,
) []shareddto.InputItem {
	threadID = strings.TrimSpace(threadID)
	query = strings.TrimSpace(query)
	if p == nil || threadID == "" || query == "" {
		return nil
	}
	gate := ResolveMemoryGate(buildCtx, p.cfg)
	p.rememberTurnGate(threadID, gate)
	attemptPrefetch := strings.TrimSpace(p.memoryRoot) != "" &&
		ShouldStartRelevantMemoryPrefetch(gate, contract.TurnInput{UserText: query}, RelevantPrefetchSurfacedState{})
	entries, ready := p.consumePrefetchEntries(ctx, threadID, query, gate)
	memoryInputs := freezeRelevantMemoryInputs(entries, p.now())
	if attemptPrefetch && !ready {
		return memoryInputs
	}
	if !gate.SearchPastContextEnabled || !shouldSearchPastContextQuery(query) {
		return memoryInputs
	}
	if len(entries) > 0 && !memoryRetrievalLowConfidence(query, entries) {
		return memoryInputs
	}
	snippets := p.searchPastContext(ctx, session, threadID, query)
	if len(snippets) == 0 {
		return memoryInputs
	}
	return append(memoryInputs, freezeTranscriptInputs(snippets)...)
}

func (p *MemoryContextProvider) OnPromptInvalidate(reason prompt.InvalidateReason) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, state := range p.turns {
		if state == nil {
			continue
		}
		if state.handle != nil && state.handle.cancel != nil {
			state.handle.cancel()
		}
		state.handle = nil
		if state.manager != nil {
			state.manager.ResetSurfaced(string(reason))
		}
	}
}

func (p *MemoryContextProvider) rememberTurnGate(threadID string, gate MemoryGateSnapshot) {
	threadID = strings.TrimSpace(threadID)
	if p == nil || threadID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.turnStateLocked(threadID).gate = gate
}

func (p *MemoryContextProvider) onTurnTerminated(threadID, _ string) {
	threadID = strings.TrimSpace(threadID)
	if p == nil || threadID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	state, ok := p.turns[threadID]
	if !ok {
		return
	}
	if state.handle != nil && state.handle.cancel != nil {
		state.handle.cancel()
	}
	state.handle = nil
}

func (p *MemoryContextProvider) consumePrefetchEntries(
	ctx context.Context,
	threadID, query string,
	gate MemoryGateSnapshot,
) ([]MemoryEntry, bool) {
	manager, handle := p.startRelevantPrefetch(ctx, threadID, query, gate)
	if manager == nil || handle == nil {
		return nil, false
	}
	entries, ok := manager.ConsumeIfReady(handle)
	if !ok {
		return nil, false
	}
	filtered := manager.FilterAlreadySurfaced(entries)
	manager.MarkSurfaced(filtered)
	p.clearHandle(threadID, handle)
	return filtered, true
}

func (p *MemoryContextProvider) startRelevantPrefetch(
	ctx context.Context,
	threadID, query string,
	gate MemoryGateSnapshot,
) (*PrefetchManager, *PrefetchHandle) {
	if p == nil || strings.TrimSpace(p.memoryRoot) == "" {
		return nil, nil
	}
	if !ShouldStartRelevantMemoryPrefetch(gate, contract.TurnInput{UserText: query}, RelevantPrefetchSurfacedState{}) {
		return nil, nil
	}
	p.mu.Lock()
	state := p.turnStateLocked(threadID)
	state.gate = gate
	if state.manager == nil {
		state.manager = NewPrefetchManager(p.memoryRoot)
	}
	manager := state.manager
	p.mu.Unlock()
	handle := manager.StartRelevantMemoryPrefetch(ctx, query)
	p.mu.Lock()
	p.turnStateLocked(threadID).handle = handle
	p.mu.Unlock()
	return manager, handle
}

func (p *MemoryContextProvider) clearHandle(threadID string, handle *PrefetchHandle) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.turnStateLocked(strings.TrimSpace(threadID))
	if state.handle == handle {
		state.handle = nil
	}
}

func (p *MemoryContextProvider) searchPastContext(
	ctx context.Context,
	session contract.Session,
	threadID, query string,
) []transcriptSnippet {
	if p == nil || session == nil {
		return nil
	}
	messages, err := session.ReadHistory(ctx, strings.TrimSpace(threadID), 200)
	if err != nil {
		return nil
	}
	return searchTranscriptSnippets(query, messages, defaultRelevantMemoryBudgetBytes/2)
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
	text := "# MEMORY.md\nContents of MEMORY.md:\n" + TruncateEntrypointContent(trimmed).Content
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

func (p *MemoryContextProvider) now() time.Time {
	if p != nil && p.timeNow != nil {
		return p.timeNow()
	}
	return time.Now()
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
