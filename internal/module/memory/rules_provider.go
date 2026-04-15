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
)

var (
	_ contract.DynamicSectionProvider    = (*MemoryRulesProvider)(nil)
	_ contract.DynamicSectionProvider    = (*MemoryContextProvider)(nil)
	_ contract.InvalidationAwareProvider = (*MemoryContextProvider)(nil)
	_ contract.TurnContextProvider       = (*MemoryContextProvider)(nil)
)

type MemoryRulesProvider struct {
	cfg    *Config
	engine *MemoryRuleEngine
	team   *TeamMemoryManager
}

func NewRulesProvider(cfg *Config, engine *MemoryRuleEngine, team *TeamMemoryManager) *MemoryRulesProvider {
	cfg = memoryConfig(cfg)
	if engine == nil {
		engine = NewMemoryRuleEngine()
	}
	return &MemoryRulesProvider{cfg: cfg, engine: engine, team: team}
}

func (p *MemoryRulesProvider) SectionName() string {
	return contract.DynamicSectionMemory
}

func (p *MemoryRulesProvider) Resolve(_ context.Context, input contract.SectionContext) (*string, error) {
	if p == nil || input.Start == nil || input.Turn != nil {
		return nil, nil
	}
	if _, ok := resolveChildAgentStart(input); ok {
		return nil, nil
	}
	if !memoryProductEnabled(p.cfg) {
		return nil, nil
	}
	gate := ResolveMemoryGate(input.BuildCtx, p.cfg)
	opts := MemoryRuleOptions{
		SkipIndex:                gate.SkipIndex,
		SearchPastContextEnabled: gate.SearchPastContextEnabled,
		ExtraGuidelines:          p.resolvedExtraGuidelines(),
	}
	text := p.engine.LoadMemoryPrompt(p.promptMode(input.BuildCtx, gate, &opts), gate.AutoEnabled, opts)
	if text == nil || strings.TrimSpace(*text) == "" {
		return nil, nil
	}
	wrapped := "## " + contract.DynamicSectionMemory + "\n\n" + strings.TrimSpace(*text)
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

func (p *MemoryRulesProvider) promptMode(buildCtx contract.BuildCtx, gate MemoryGateSnapshot, opts *MemoryRuleOptions) MemoryMode {
	if !gate.AutoEnabled {
		return ""
	}
	autoDir := p.resolvedAutoMemPath(buildCtx)
	if opts != nil {
		opts.AutoMemPath = autoDir
	}
	if gate.KairosActive {
		return MemoryModeKairos
	}
	_, teamDir, ok := p.combinedMemoryPaths(buildCtx)
	if !ok {
		return MemoryModeStandard
	}
	if opts != nil {
		opts.TeamMemPath = teamDir
	}
	return MemoryModeCombined
}

func (p *MemoryRulesProvider) resolvedAutoMemPath(buildCtx contract.BuildCtx) string {
	cfg := memoryConfig(p.cfg)
	projectRoot := strings.TrimSpace(buildCtx.GitRoot)
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(buildCtx.CWD)
	}
	if projectRoot == "" {
		projectRoot = strings.TrimSpace(cfg.ProjectRoot)
	}
	autoDir, err := resolvedStoreRoot(cfg.RootDir, projectRoot, configuredAutoMemPathOverride(cfg))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(autoDir)
}

func (p *MemoryRulesProvider) combinedMemoryPaths(buildCtx contract.BuildCtx) (string, string, bool) {
	if p == nil || p.team == nil {
		return "", "", false
	}
	autoDir := p.resolvedAutoMemPath(buildCtx)
	if autoDir == "" {
		return "", "", false
	}
	teamDir := providerTeamMemPath(p.team, buildCtx)
	if teamDir == "" {
		return "", "", false
	}
	return autoDir, teamDir, true
}

type MemoryContextProvider struct {
	cfg        *Config
	memoryRoot string
	timeNow    func() time.Time

	mu    sync.Mutex
	turns map[string]*prefetchTurnState
}

type prefetchTurnState struct {
	manager       *PrefetchManager
	handle        *PrefetchHandle
	gate          MemoryGateSnapshot
	lastDate      string
	surfacedBytes int
}

type TurnContextPayload = contract.TurnContextPayload

func NewContextProvider(cfg *Config) *MemoryContextProvider {
	cfg = memoryConfig(cfg)
	root, _ := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, configuredAutoMemPathOverride(cfg))
	return &MemoryContextProvider{
		cfg:        cfg,
		memoryRoot: strings.TrimSpace(root),
		timeNow:    time.Now,
		turns:      map[string]*prefetchTurnState{},
	}
}

func AsTurnContextProvider(provider *MemoryContextProvider) contract.TurnContextProvider {
	return provider
}

func (p *MemoryContextProvider) SectionName() string {
	return contract.DynamicSectionMemoryContext
}

func (p *MemoryContextProvider) Resolve(_ context.Context, input contract.SectionContext) (*string, error) {
	if p == nil || input.Turn == nil {
		return nil, nil
	}
	if !memoryProductEnabled(p.cfg) {
		return nil, nil
	}
	gate := ResolveMemoryGate(input.BuildCtx, p.cfg)
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
	return p.PrepareTurnContext(ctx, session, buildCtx, threadID, query).Inputs
}

func (p *MemoryContextProvider) PrepareTurnContext(
	ctx context.Context,
	session contract.Session,
	buildCtx contract.BuildCtx,
	threadID, query string,
) TurnContextPayload {
	threadID = strings.TrimSpace(threadID)
	query = strings.TrimSpace(query)
	if invalidTurnContextRequest(p, threadID, query) {
		return TurnContextPayload{}
	}
	if !memoryProductEnabled(p.cfg) {
		return TurnContextPayload{}
	}
	gate := ResolveMemoryGate(buildCtx, p.cfg)
	p.rememberTurnGate(threadID, gate)
	surfacedState := p.surfacedState(threadID)
	entries, ready, attemptPrefetch := p.prepareTurnAttachments(ctx, threadID, query, gate, surfacedState)
	payload := TurnContextPayload{Attachments: freezeRelevantMemoryAttachments(entries, p.now())}
	payload.Attachments = p.appendKairosDateChangeAttachment(threadID, gate, payload.Attachments)
	if attemptPrefetch && !ready {
		return payload
	}
	payload.Inputs = p.searchPastContextInputs(ctx, session, threadID, query, gate, entries)
	return payload
}

func (p *MemoryContextProvider) prepareTurnAttachments(
	ctx context.Context,
	threadID, query string,
	gate MemoryGateSnapshot,
	surfacedState RelevantPrefetchSurfacedState,
) ([]MemoryEntry, bool, bool) {
	attemptPrefetch := p.shouldAttemptTurnPrefetch(gate, query, surfacedState)
	entries, ready := p.consumePrefetchEntries(ctx, threadID, query, gate)
	return entries, ready, attemptPrefetch
}

func (p *MemoryContextProvider) shouldAttemptTurnPrefetch(
	gate MemoryGateSnapshot,
	query string,
	surfacedState RelevantPrefetchSurfacedState,
) bool {
	if strings.TrimSpace(p.memoryRoot) == "" {
		return false
	}
	return ShouldStartRelevantMemoryPrefetch(gate, contract.TurnInput{UserText: query}, surfacedState)
}

func (p *MemoryContextProvider) searchPastContextInputs(
	ctx context.Context,
	session contract.Session,
	threadID, query string,
	gate MemoryGateSnapshot,
	entries []MemoryEntry,
) []shareddto.InputItem {
	if !gate.SearchPastContextEnabled || !shouldSearchPastContextQuery(query) {
		return nil
	}
	if len(entries) > 0 && !memoryRetrievalLowConfidence(query, entries) {
		return nil
	}
	snippets := p.searchPastContext(ctx, session, threadID, query)
	if len(snippets) == 0 {
		return nil
	}
	return freezeTranscriptInputs(snippets)
}

func invalidTurnContextRequest(p *MemoryContextProvider, threadID, query string) bool {
	return p == nil || threadID == "" || query == ""
}

func (p *MemoryContextProvider) OnPromptInvalidate(reason contract.InvalidateReason) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, state := range p.turns {
		if state == nil {
			continue
		}
		if state.manager != nil {
			state.manager.Reset(string(reason))
		} else if state.handle != nil && state.handle.cancel != nil {
			state.handle.cancel()
		}
		state.handle = nil
		state.surfacedBytes = 0
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
	p.markSurfacedEntries(threadID, manager, filtered)
	p.clearHandle(threadID, handle)
	return filtered, true
}

func (p *MemoryContextProvider) startRelevantPrefetch(
	ctx context.Context,
	threadID, query string,
	gate MemoryGateSnapshot,
) (*PrefetchManager, *PrefetchHandle) {
	query = strings.TrimSpace(query)
	if p == nil || strings.TrimSpace(p.memoryRoot) == "" || !memoryProductEnabled(p.cfg) {
		return nil, nil
	}
	p.mu.Lock()
	state := p.turnStateLocked(threadID)
	state.gate = gate
	if !ShouldStartRelevantMemoryPrefetch(gate, contract.TurnInput{UserText: query}, RelevantPrefetchSurfacedState{TotalBytes: state.surfacedBytes}) {
		p.mu.Unlock()
		return nil, nil
	}
	if state.manager == nil {
		state.manager = NewPrefetchManager(p.memoryRoot)
	}
	manager := state.manager
	if state.handle != nil && state.handle.query == query {
		stateValue := state.handle.state.Load()
		if stateValue == prefetchStatePending || stateValue == prefetchStateReady {
			handle := state.handle
			p.mu.Unlock()
			return manager, handle
		}
	}
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

func (p *MemoryContextProvider) surfacedState(threadID string) RelevantPrefetchSurfacedState {
	threadID = strings.TrimSpace(threadID)
	if p == nil || threadID == "" {
		return RelevantPrefetchSurfacedState{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return RelevantPrefetchSurfacedState{TotalBytes: p.turnStateLocked(threadID).surfacedBytes}
}

func (p *MemoryContextProvider) markSurfacedEntries(threadID string, manager *PrefetchManager, entries []MemoryEntry) {
	if manager != nil {
		manager.MarkSurfaced(entries)
	}
	if p == nil || len(entries) == 0 {
		return
	}
	surfacedBytes := surfacedEntryBytes(entries)
	if surfacedBytes == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.turnStateLocked(strings.TrimSpace(threadID)).surfacedBytes += surfacedBytes
}

func surfacedEntryBytes(entries []MemoryEntry) int {
	total := 0
	for _, entry := range entries {
		total += len(memoryRenderBody(entry))
	}
	return total
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
	if p.ClaudeMdRegistrar == nil {
		if registrar, ok := p.Registry.(contract.ClaudeMdSourceProviderRegistrar); ok {
			p.ClaudeMdRegistrar = registrar
		}
	}
	if p.Registry != nil {
		providers := []contract.DynamicSectionProvider{p.Provider, p.AgentProvider, p.ContextProvider}
		for _, provider := range providers {
			if provider == nil {
				continue
			}
			if err := p.Registry.RegisterDynamicProvider(provider); err != nil {
				return err
			}
		}
	}
	if p.ClaudeMdRegistrar != nil && p.ClaudeMdProvider != nil {
		if err := p.ClaudeMdRegistrar.RegisterClaudeMdSourceProvider(p.ClaudeMdProvider); err != nil {
			return err
		}
	}
	return nil
}
