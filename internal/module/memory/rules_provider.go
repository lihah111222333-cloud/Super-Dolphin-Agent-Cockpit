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

// 编译期断言确保两个 provider 满足 prompt 动态 section 和 turn context 接口。
var (
	_ contract.DynamicSectionProvider    = (*MemoryRulesProvider)(nil)
	_ contract.DynamicSectionProvider    = (*MemoryContextProvider)(nil)
	_ contract.InvalidationAwareProvider = (*MemoryContextProvider)(nil)
	_ contract.TurnContextProvider       = (*MemoryContextProvider)(nil)
)

// retrieval 子包类型在 memory 根包下重新导出，保持旧调用方不必感知拆包。
type ManifestBuilder = retrievalpkg.ManifestBuilder
type RelevantMemoryFinder = retrievalpkg.RelevantMemoryFinder
type PrefetchManager = retrievalpkg.PrefetchManager
type PrefetchHandle = retrievalpkg.PrefetchHandle
type transcriptSnippet = retrievalpkg.TranscriptSnippet

// memory 检索默认值和 prefetch 状态常量。
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

// freezeRelevantMemoryAttachments 将相关记忆条目渲染为 provider attachment。
func freezeRelevantMemoryAttachments(entries []MemoryEntry, now time.Time) []dto.AttachmentEnvelope {
	return retrievalpkg.FreezeRelevantMemoryAttachments(entries, now)
}

// freezeTranscriptInputs 将历史片段渲染为本轮 turn input。
func freezeTranscriptInputs(snippets []transcriptSnippet) []shareddto.InputItem {
	return retrievalpkg.FreezeTranscriptInputs(snippets)
}

// memoryHeader 返回注入 prompt 时使用的记忆标题。
func memoryHeader(now time.Time, entry MemoryEntry) string {
	return retrievalpkg.MemoryHeader(now, entry)
}

// shouldSearchPastContextQuery 判断 query 是否值得触发历史上下文搜索。
func shouldSearchPastContextQuery(query string) bool {
	return retrievalpkg.ShouldSearchPastContextQuery(query)
}

// memoryRetrievalLowConfidence 判断相关记忆结果是否不足以覆盖 query，需要回退搜索历史。
func memoryRetrievalLowConfidence(query string, entries []MemoryEntry) bool {
	return retrievalpkg.MemoryRetrievalLowConfidence(query, entries)
}

// searchTranscriptSnippets 在历史消息里检索可作为补充上下文的片段。
func searchTranscriptSnippets(query string, messages []dto.Message, budget int) []transcriptSnippet {
	return retrievalpkg.SearchTranscriptSnippets(query, messages, budget)
}

// memoryRenderBody 渲染单条 memory 正文，用于预算和 attachment 内容。
func memoryRenderBody(entry MemoryEntry) string {
	return retrievalpkg.MemoryRenderBody(entry)
}

// searchTerms 将 query 规范化并拆成去重检索词。
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

// contextErr 非阻塞读取 ctx 错误，nil context 视为未取消。
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

// minInt 返回两个整数中的较小值。
func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

// MemoryRulesProvider 只负责 thread start 阶段的 memory 使用规则注入。
// 它不读取记忆正文，也不执行写入；turn 级相关记忆由 MemoryContextProvider 负责。
type MemoryRulesProvider struct {
	cfg    *Config            // memory 功能配置。
	engine *MemoryRuleEngine  // 规则文本渲染引擎。
	team   *TeamMemoryManager // combined 模式下的 team memory 根解析器。
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
	mode, err := p.promptMode(input.BuildCtx, gate, &opts)
	if err != nil {
		return nil, err
	}
	text := p.engine.LoadMemoryPrompt(mode, gate.AutoEnabled, opts)
	if text == nil || strings.TrimSpace(*text) == "" {
		return nil, nil
	}
	wrapped := "## " + contract.DynamicSectionMemory + "\n\n" + strings.TrimSpace(*text)
	return &wrapped, nil
}

// resolvedExtraGuidelines 复制配置里的额外规则，避免 prompt 渲染时共享可变 slice。
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
func (p *MemoryRulesProvider) promptMode(
	buildCtx contract.BuildCtx,
	gate MemoryGateSnapshot,
	opts *MemoryRuleOptions,
) (MemoryMode, error) {
	if !gate.AutoEnabled {
		return "", nil
	}
	autoDir, err := p.resolvedAutoMemPath(buildCtx)
	if err != nil {
		return "", err
	}
	if opts != nil {
		opts.AutoMemPath = autoDir
	}
	if gate.KairosActive {
		return MemoryModeKairos, nil
	}
	_, teamDir, ok, err := p.combinedMemoryPaths(buildCtx)
	if err != nil {
		return "", err
	}
	if !ok {
		return MemoryModeStandard, nil
	}
	if opts != nil {
		opts.TeamMemPath = teamDir
	}
	return MemoryModeCombined, nil
}

// resolvedAutoMemPath 解析当前 BuildCtx 下的 private auto memory 目录。
func (p *MemoryRulesProvider) resolvedAutoMemPath(buildCtx contract.BuildCtx) (string, error) {
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
		return "", err
	}
	return strings.TrimSpace(autoDir), nil
}

// combinedMemoryPaths 同时解析 private 和 team memory 目录；缺任一路径则退回 standard 模式。
func (p *MemoryRulesProvider) combinedMemoryPaths(buildCtx contract.BuildCtx) (string, string, bool, error) {
	if p == nil || p.team == nil {
		return "", "", false, nil
	}
	autoDir, err := p.resolvedAutoMemPath(buildCtx)
	if err != nil {
		return "", "", false, err
	}
	if autoDir == "" {
		return "", "", false, nil
	}
	teamDir := strings.TrimSpace(p.team.GetTeamMemPath(buildCtx))
	if teamDir == "" {
		return "", "", false, nil
	}
	return autoDir, teamDir, true, nil
}

// MemoryContextProvider 在每个 turn 准备相关记忆附件和历史片段输入。
// 预取状态按 threadID 存放，所有 turns map 访问都受 mu 保护。
type MemoryContextProvider struct {
	cfg        *Config          // memory 功能配置。
	memoryRoot string           // 当前用于相关记忆检索的根目录。
	timeNow    func() time.Time // 测试可替换时间源。

	mu    sync.Mutex
	turns map[string]*prefetchTurnState // threadID 到预取状态。
}

// prefetchTurnState 保存单个 thread 的相关记忆预取和 surfaced 预算。
type prefetchTurnState struct {
	manager       *PrefetchManager   // 当前 thread 的预取管理器。
	handle        *PrefetchHandle    // 当前 query 的预取任务。
	gate          MemoryGateSnapshot // 最近一次 turn 使用的 memory gate。
	lastDate      string             // 上次注入 Kairos 日期变化附件的日期。
	surfacedBytes int                // 本 thread 已注入相关记忆的累计字节数。
}

// TurnContextPayload 是 contract 层 turn context payload 在 memory 包内的别名。
type TurnContextPayload = contract.TurnContextPayload

// NewContextProvider 创建 turn 时读取 memory 的 provider。
// 这里只记住检索根；目录准备、权限和写入仍由 Service/Hooks 处理。
func NewContextProvider(cfg *Config) (*MemoryContextProvider, error) {
	cfg = memoryConfig(cfg)
	root := ""
	if shouldValidateMemoryRoot(cfg) {
		resolvedRoot, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, configuredAutoMemPathOverride(cfg))
		if err != nil {
			return nil, err
		}
		root = resolvedRoot
	}
	return &MemoryContextProvider{
		cfg:        cfg,
		memoryRoot: strings.TrimSpace(root),
		timeNow:    time.Now,
		turns:      map[string]*prefetchTurnState{},
	}, nil
}

// shouldValidateMemoryRoot 判断当前构造路径是否已经声明了可解析的 memory root。
// 仅启用历史搜索但未配置 root 的测试/运行态不需要触碰磁盘根目录。
func shouldValidateMemoryRoot(cfg *Config) bool {
	cfg = memoryConfig(cfg)
	return memoryProductEnabled(cfg) &&
		(strings.TrimSpace(cfg.RootDir) != "" || strings.TrimSpace(configuredAutoMemPathOverride(cfg)) != "")
}

// AsTurnContextProvider 把记忆规则提供器暴露为 turn 上下文提供器。
func AsTurnContextProvider(provider *MemoryContextProvider) contract.TurnContextProvider {
	return provider
}

// SectionName 返回该上下文提供器写入的 prompt section 名称。
func (p *MemoryContextProvider) SectionName() string {
	return contract.DynamicSectionMemoryContext
}

// Resolve 对 turn 级动态 section 保持空实现。
// MEMORY.md 入口由 MemoryEntrypointProvider 在 session start 注入；每轮相关记忆走 PrepareTurnContext。
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
	entries, ready, attemptPrefetch, prefetchErr := p.prepareTurnAttachments(ctx, threadID, query, gate, surfacedState)
	payload := TurnContextPayload{Attachments: freezeRelevantMemoryAttachments(entries, p.now())}
	payload.Attachments = p.appendKairosDateChangeAttachment(threadID, gate, payload.Attachments)
	if prefetchErr != nil {
		payload.Inputs = memoryPrefetchErrorInputs(prefetchErr)
		return payload
	}
	if attemptPrefetch && !ready {
		return payload
	}
	payload.Inputs = p.searchPastContextInputs(ctx, session, threadID, query, gate, entries)
	return payload
}

// prepareTurnAttachments 尝试消费上一轮预取结果，并判断本轮是否需要启动新的预取。
func (p *MemoryContextProvider) prepareTurnAttachments(
	ctx context.Context,
	threadID, query string,
	gate MemoryGateSnapshot,
	surfacedState RelevantPrefetchSurfacedState,
) ([]MemoryEntry, bool, bool, error) {
	attemptPrefetch := p.shouldAttemptTurnPrefetch(gate, query, surfacedState)
	entries, ready, err := p.consumePrefetchEntries(ctx, threadID, query, gate)
	return entries, ready, attemptPrefetch, err
}

// shouldAttemptTurnPrefetch 检查 feature gate、query 和 surfaced 预算是否允许启动预取。
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
	snippets, err := p.searchPastContext(ctx, session, threadID, query)
	if err != nil {
		return memoryHistorySearchErrorInputs(err)
	}
	if len(snippets) == 0 {
		return nil
	}
	return freezeTranscriptInputs(snippets)
}

// memoryHistorySearchErrorInputs 把历史检索失败显式传给本轮 turn，避免上层把错误误判成没有命中。
func memoryHistorySearchErrorInputs(err error) []shareddto.InputItem {
	if err == nil {
		return nil
	}
	return []shareddto.InputItem{{
		Type:    "filecontent",
		Name:    "Memory history search error",
		Content: "memory history search failed:\n" + err.Error(),
	}}
}

// memoryPrefetchErrorInputs 把相关记忆预取失败显式交给本轮 turn，避免错误被误判成无命中。
func memoryPrefetchErrorInputs(err error) []shareddto.InputItem {
	if err == nil {
		return nil
	}
	return []shareddto.InputItem{{
		Type:    "filecontent",
		Name:    "Memory prefetch error",
		Content: "memory prefetch failed:\n" + err.Error(),
	}}
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

// rememberTurnGate 保存 thread 最近一次 gate，供 Kairos/date-change 附件等后续步骤复用。
func (p *MemoryContextProvider) rememberTurnGate(threadID string, gate MemoryGateSnapshot) {
	threadID = strings.TrimSpace(threadID)
	if p == nil || threadID == "" {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.turnStateLocked(threadID).gate = gate
}

// onTurnTerminated 取消 thread 当前预取任务，但保留 manager surfaced 记录供后续 turn 去重。
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

// consumePrefetchEntries 消费当前 query 的 ready 预取结果，并更新 surfaced 去重和字节预算。
func (p *MemoryContextProvider) consumePrefetchEntries(
	ctx context.Context,
	threadID, query string,
	gate MemoryGateSnapshot,
) ([]MemoryEntry, bool, error) {
	manager, handle, startedNew := p.startRelevantPrefetch(ctx, threadID, query, gate)
	if manager == nil || handle == nil {
		return nil, false, nil
	}
	if startedNew {
		return nil, false, nil
	}
	if handle.State() == prefetchStateReady {
		if err := handle.Err(); err != nil {
			p.clearHandle(threadID, handle)
			return nil, true, err
		}
	}
	entries, ok := manager.ConsumeIfReady(handle)
	if !ok {
		return nil, false, nil
	}
	filtered := manager.FilterAlreadySurfaced(entries)
	p.markSurfacedEntries(threadID, manager, filtered)
	p.clearHandle(threadID, handle)
	return filtered, true, nil
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
	if !ShouldStartRelevantMemoryPrefetch(
		gate,
		contract.TurnInput{UserText: query},
		RelevantPrefetchSurfacedState{TotalBytes: state.surfacedBytes},
	) {
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

// clearHandle 只在 handle 仍是当前任务时清空，避免旧消费路径误删新查询。
func (p *MemoryContextProvider) clearHandle(threadID string, handle *PrefetchHandle) {
	p.mu.Lock()
	defer p.mu.Unlock()
	state := p.turnStateLocked(strings.TrimSpace(threadID))
	if state.handle == handle {
		state.handle = nil
	}
}

// surfacedState 返回 thread 已展示相关记忆的字节统计，用于预取 gate 预算判断。
func (p *MemoryContextProvider) surfacedState(threadID string) RelevantPrefetchSurfacedState {
	threadID = strings.TrimSpace(threadID)
	if p == nil || threadID == "" {
		return RelevantPrefetchSurfacedState{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return RelevantPrefetchSurfacedState{TotalBytes: p.turnStateLocked(threadID).surfacedBytes}
}

// markSurfacedEntries 同步更新 manager 去重集合和 provider 级字节预算。
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

// surfacedEntryBytes 计算本轮注入记忆正文占用的预算字节数。
func surfacedEntryBytes(entries []MemoryEntry) int {
	total := 0
	for _, entry := range entries {
		total += len(memoryRenderBody(entry))
	}
	return total
}

// searchPastContext 在记忆低置信或无结果时，从 thread 历史中检索可复用片段。
func (p *MemoryContextProvider) searchPastContext(
	ctx context.Context,
	session contract.Session,
	threadID, query string,
) ([]transcriptSnippet, error) {
	if p == nil || session == nil {
		return nil, nil
	}
	messages, err := session.ReadHistory(ctx, strings.TrimSpace(threadID), 200)
	if err != nil {
		return nil, err
	}
	return searchTranscriptSnippets(query, messages, defaultRelevantMemoryBudgetBytes/2), nil
}

// turnStateLocked 返回 thread 预取状态；调用方必须持有 p.mu。
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

// now 返回可替换时间源，未配置时使用真实时间。
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
