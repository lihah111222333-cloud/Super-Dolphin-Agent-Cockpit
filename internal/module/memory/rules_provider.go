package memory

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	retrievalpkg "github.com/anthropic-ai/super-agent-v3/internal/module/memory/retrieval"
)

var (
	_ contract.DynamicSectionProvider    = (*MemoryRulesProvider)(nil)
	_ contract.DynamicSectionProvider    = (*MemoryContextProvider)(nil)
	_ contract.InvalidationAwareProvider = (*MemoryContextProvider)(nil)
	_ contract.TurnContextProvider       = (*MemoryContextProvider)(nil)
)

type ManifestBuilder = retrievalpkg.ManifestBuilder
type RelevantMemoryFinder = retrievalpkg.RelevantMemoryFinder
type PrefetchManager = retrievalpkg.PrefetchManager
type PrefetchHandle = retrievalpkg.PrefetchHandle
type transcriptSnippet = retrievalpkg.TranscriptSnippet

const (
	defaultManifestFileLimit         = retrievalpkg.DefaultManifestFileLimit
	defaultRelevantMemoryBudgetBytes = retrievalpkg.DefaultRelevantMemoryBudgetBytes
	defaultRelevantMemoryLimit       = retrievalpkg.DefaultRelevantMemoryLimit
	defaultRelevantMemoryCandidates  = retrievalpkg.DefaultRelevantMemoryCandidates
	prefetchStatePending             = retrievalpkg.PrefetchStatePending
	prefetchStateReady               = retrievalpkg.PrefetchStateReady
	prefetchStateConsumed            = retrievalpkg.PrefetchStateConsumed
	prefetchStateDiscarded           = retrievalpkg.PrefetchStateDiscarded
)

// NewManifestBuilder 创建记忆规则 manifest 构建器。
func NewManifestBuilder() *ManifestBuilder {
	return retrievalpkg.NewManifestBuilder()
}

// ScanHeadersSafe 安全扫描记忆文件头部信息。
func ScanHeadersSafe(memoryRoot string) ([]MemoryEntry, error) {
	return retrievalpkg.ScanHeadersSafe(memoryRoot)
}

// NewPrefetchManager 创建记忆预取管理器。
func NewPrefetchManager(memoryRoot string) *PrefetchManager {
	return retrievalpkg.NewPrefetchManager(memoryRoot)
}

func freezeRelevantMemoryAttachments(entries []MemoryEntry, now time.Time) []dto.AttachmentEnvelope {
	return retrievalpkg.FreezeRelevantMemoryAttachments(entries, now)
}

func freezeTranscriptInputs(snippets []transcriptSnippet) []shareddto.InputItem {
	return retrievalpkg.FreezeTranscriptInputs(snippets)
}

func memoryHeader(now time.Time, entry MemoryEntry) string {
	return retrievalpkg.MemoryHeader(now, entry)
}

func shouldSearchPastContextQuery(query string) bool {
	return retrievalpkg.ShouldSearchPastContextQuery(query)
}

func memoryRetrievalLowConfidence(query string, entries []MemoryEntry) bool {
	return retrievalpkg.MemoryRetrievalLowConfidence(query, entries)
}

func searchTranscriptSnippets(query string, messages []dto.Message, budget int) []transcriptSnippet {
	return retrievalpkg.SearchTranscriptSnippets(query, messages, budget)
}

func memoryRenderBody(entry MemoryEntry) string {
	return retrievalpkg.MemoryRenderBody(entry)
}

func searchTerms(query string) (string, []string) {
	normalized := CanonicalName(query)
	if normalized == "" {
		return "", nil
	}
	seen := map[string]struct{}{normalized: {}}
	terms := []string{normalized}
	for _, part := range strings.Fields(normalized) {
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		terms = append(terms, part)
	}
	return normalized, terms
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

type MemoryRulesProvider struct {
	cfg    *Config
	engine *MemoryRuleEngine
	team   *TeamMemoryManager
}

// NewRulesProvider 创建 start 时用的 memory 规则 provider。
// 它只告诉 AI 怎么用 memory，不读取 topic 正文，也不写文件。
func NewRulesProvider(cfg *Config, engine *MemoryRuleEngine, team *TeamMemoryManager) *MemoryRulesProvider {
	cfg = memoryConfig(cfg)
	if engine == nil {
		engine = NewMemoryRuleEngine()
	}
	return &MemoryRulesProvider{cfg: cfg, engine: engine, team: team}
}

// SectionName 返回该上下文提供器写入的 prompt section 名称。
func (p *MemoryRulesProvider) SectionName() string {
	return contract.DynamicSectionMemory
}

// Resolve 只在 thread/start 时产出 memory 规则。
// 每轮相关记忆、历史片段和真实写入都不在这里做。
func (p *MemoryRulesProvider) Resolve(_ context.Context, input contract.SectionContext) (*string, error) {
	if p == nil || input.Start == nil || input.Turn != nil {
		return nil, nil
	}
	if !memoryProductEnabled(p.cfg) {
		return nil, nil
	}
	gate := ResolveMemoryGate(input.BuildCtx, p.cfg)
	if gate.SuppressForOverlay() {
		return nil, nil
	}
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

// promptMode 解析当前 prompt 的记忆加载模式。
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
	teamDir := strings.TrimSpace(p.team.GetTeamMemPath(buildCtx))
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

// NewContextProvider 创建 turn 时读取 memory 的 provider。
// 这里只记住检索根；目录准备、权限和写入仍由 Service/Hooks 处理。
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

// AsTurnContextProvider 把记忆规则提供器暴露为 turn 上下文提供器。
func AsTurnContextProvider(provider *MemoryContextProvider) contract.TurnContextProvider {
	return provider
}

// SectionName 返回该上下文提供器写入的 prompt section 名称。
func (p *MemoryContextProvider) SectionName() string {
	return contract.DynamicSectionMemoryContext
}

// Resolve is intentionally a no-op since Phase 1.5: the durable MEMORY.md
// entrypoint is now injected exclusively by MemoryEntrypointProvider at
// session start. Per-turn relevant memory and search-past-context attachments
// are surfaced via PrepareTurnContext, not the dynamic-section pipeline. The
// provider is still registered so future per-turn dynamic sections can attach
// here without re-plumbing the section list.
// Resolve 解析当前请求需要注入的 prompt 内容。
func (p *MemoryContextProvider) Resolve(_ context.Context, _ contract.SectionContext) (*string, error) {
	return nil, nil
}

// PrepareTurnInputs 为本轮 turn 准备记忆相关输入。
func (p *MemoryContextProvider) PrepareTurnInputs(
	ctx context.Context,
	session contract.Session,
	buildCtx contract.BuildCtx,
	threadID, query string,
) []shareddto.InputItem {
	return p.PrepareTurnContext(ctx, session, buildCtx, threadID, query).Inputs
}

// PrepareTurnContext 给本轮 turn 准备 memory 附件和历史片段。
// 它只返回上下文给 turn，不改 memory 文件，也不直接改 prompt 缓存。
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

// searchPastContextInputs 搜索可复用的历史上下文输入。
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

// OnPromptInvalidate 在 prompt 失效时清理相关缓存。
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
		} else if state.handle != nil {
			state.handle.Cancel()
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
	if state.handle != nil {
		state.handle.Cancel()
	}
	state.handle = nil
}

func (p *MemoryContextProvider) consumePrefetchEntries(
	ctx context.Context,
	threadID, query string,
	gate MemoryGateSnapshot,
) ([]MemoryEntry, bool) {
	manager, handle, startedNew := p.startRelevantPrefetch(ctx, threadID, query, gate)
	if manager == nil || handle == nil {
		return nil, false
	}
	if startedNew {
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

// startRelevantPrefetch 记住每个 thread 当前 query 的预取任务。
// 这个 map 只是短期缓存，不代表 memory 或 prompt snapshot 的长期状态。
func (p *MemoryContextProvider) startRelevantPrefetch(
	ctx context.Context,
	threadID, query string,
	gate MemoryGateSnapshot,
) (*PrefetchManager, *PrefetchHandle, bool) {
	query = strings.TrimSpace(query)
	if p == nil || strings.TrimSpace(p.memoryRoot) == "" || !memoryProductEnabled(p.cfg) {
		return nil, nil, false
	}
	p.mu.Lock()
	state := p.turnStateLocked(threadID)
	state.gate = gate
	if !ShouldStartRelevantMemoryPrefetch(gate, contract.TurnInput{UserText: query}, RelevantPrefetchSurfacedState{TotalBytes: state.surfacedBytes}) {
		p.mu.Unlock()
		return nil, nil, false
	}
	if state.manager == nil {
		state.manager = NewPrefetchManager(p.memoryRoot)
	}
	manager := state.manager
	if state.handle != nil && state.handle.Query() == query {
		stateValue := state.handle.State()
		if stateValue == prefetchStatePending || stateValue == prefetchStateReady {
			handle := state.handle
			p.mu.Unlock()
			return manager, handle, false
		}
	}
	p.mu.Unlock()
	handle := manager.StartRelevantMemoryPrefetch(ctx, query)
	p.mu.Lock()
	p.turnStateLocked(threadID).handle = handle
	p.mu.Unlock()
	return manager, handle, true
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

// registerPromptProviders 把 memory 的几种提示内容注册到 prompt。
// 新的 memory 可见内容优先从这里接入，不要让 prompt 直接读 memory 内部实现。
func registerPromptProviders(p promptProviderParams) error {
	if p.ClaudeMdRegistrar == nil {
		if registrar, ok := p.Registry.(contract.ClaudeMdSourceProviderRegistrar); ok {
			p.ClaudeMdRegistrar = registrar
		}
	}
	if p.Registry != nil {
		providers := []contract.DynamicSectionProvider{p.Provider, p.EntrypointProvider, p.ContextProvider}
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
