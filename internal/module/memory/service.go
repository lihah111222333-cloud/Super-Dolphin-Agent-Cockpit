package memory

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	threaddto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/thread"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/dedup"
	shared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/memory/shared"
)

// Service 是 memory 模块对外暴露的根目录、整理和状态管理端口。
// UI/RPC 和生命周期装配只通过该接口触达持久化根，不直接操作 diskStore。
type Service interface {
	Config() Config
	RootDir() string
	EnsureRoot(ctx context.Context) error
	RunConsolidation(ctx context.Context) error
	GetDreamTaskStatus() DreamTaskSnapshot
	GetNestedIngestHealth() NestedIngestHealthSnapshot
	KillDreamTask() error
	MemoryCoordinator() *diskLockCoordinator
}

type service struct {
	cfg          *Config
	logger       *slog.Logger
	consolidator *AutoDreamConsolidator
	dreamHooks   *MemoryLifecycleHooks
}

// MemoryLifecycleHooks 连接 turn/thread 事件、provider memory tools 和磁盘写入闭环。
// 它持有同一个锁协调器，保证显式写入、自动抽取和整理不会绕过彼此的持久化边界。
type MemoryLifecycleHooks struct {
	cfg                 *Config
	team                *TeamMemoryManager
	enabled             bool
	extractOnStop       bool
	rootDir             string
	projectRoot         string
	autoMemPathOverride string
	consolidator        *AutoDreamConsolidator
	extractFn           ExtractFunc
	extractor           *MemoryExtractor
	manifestBuilder     *ManifestBuilder
	threads             historySource
	threadStore         threadMetadataStore
	sections            sectionInvalidator
	logger              *slog.Logger
	timeNow             func() time.Time
	feedbackTracker     *FeedbackTracker
	onFeedbackThreshold func(topicKey string, group []ExtractedMemory)

	// stateMu 串行保护下面六张 turn/extraction 追踪表。
	// 这里使用普通 map 加粗粒度锁；ExtractionState 自己再保护内部字段。调用方拿到
	// *ExtractionState 后可以释放 stateMu，但新增访问这些 map 的代码必须先持有 stateMu。
	stateMu           sync.Mutex
	states            map[string]*ExtractionState    // guarded by stateMu
	activeTurns       map[string]string              // guarded by stateMu
	callTurns         map[string]toolCallScope       // guarded by stateMu
	turnWrites        map[string]map[string]struct{} // guarded by stateMu
	turnInputs        map[string]string              // guarded by stateMu
	handledTurnInputs map[string]struct{}            // guarded by stateMu
	extractWG         sync.WaitGroup

	// drainMu 和 drainClosed 防止后台抽取 WaitGroup 被并发 Add/Wait 复用。
	// DrainPendingExtraction 设置关闭标志后，新 enqueue 会被拒绝；字段名刻意用
	// drainClosed，区别于本包其它组件中“清空队列”的 drainPending。
	drainMu     sync.Mutex
	drainClosed bool

	dreamMu     sync.Mutex
	dreamTask   *dreamTaskState
	dreamHealth autoDreamHealthSnapshot

	nestedIngestMu     sync.Mutex
	nestedIngestHealth NestedIngestHealthSnapshot

	dedupFilter *dedup.Filter

	// locks 是进程内共享的记忆协调器，负责 diskStore 锁和跨 scope 告警去重。
	// 构造函数必须显式注入；memoryCoordinator 只是 getter，不能懒加载，否则会和
	// module.go 里给整理器共享的实例竞争并覆盖。
	locks *diskLockCoordinator
}

var saveIntentPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)^\s*remember\s+that\s+(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*remember\s*[:：\-—,，]\s*(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:save|store)\s+(.+?)\s+(?:to|in)\s+memory\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:save|store)\s+to\s+memory\s*(?:[:：\-—,，]\s*|\s+)(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:请)?记住(?:这个|这点)?\s*(?:[:：\-—,，]\s*|\s+)(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:把\s+)?(.+?)\s*(?:记到记忆里|保存到记忆里|保存到记忆中)\s*$`),
}

var forgetIntentPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?is)^\s*forget\s+that\s+(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*forget\s*[:：\-—,，]\s*(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:remove|delete)\s+(.+?)\s+from\s+memory\s*$`),
	regexp.MustCompile(`(?is)^\s*(?:请)?(?:忘记|忘掉)(?:这个|这点|这条)?\s*(?:[:：\-—,，]\s*|\s+)(.+?)\s*$`),
	regexp.MustCompile(`(?is)^\s*把\s+(.+?)\s*(?:从记忆里删除|从记忆中删除|从记忆删除|从记忆里移除)\s*$`),
}

// NewService 创建记忆服务并绑定整理器与生命周期 hooks。
// cfg 或 consolidator 缺失时只补齐模块内可控默认值，真实根目录和抽取函数仍在调用阶段校验。
func NewService(cfg *Config, logger *slog.Logger, consolidator *AutoDreamConsolidator, hooks *MemoryLifecycleHooks) Service {
	return newServiceWithConsolidator(cfg, logger, consolidator, hooks)
}

// newServiceWithConsolidator 是测试和 fx provider 共用的构造入口。
// 它会把最新配置同步给 consolidator，确保手动整理和后台整理使用同一套根目录规则。
func newServiceWithConsolidator(cfg *Config, logger *slog.Logger, consolidator *AutoDreamConsolidator, hooks *MemoryLifecycleHooks) Service {
	if cfg == nil {
		cfg = &Config{}
	}
	if consolidator == nil {
		consolidator = NewAutoDreamConsolidator(nil)
	}
	consolidator.cfg = memoryConfig(cfg)
	return &service{cfg: cfg, logger: logger, consolidator: consolidator, dreamHooks: hooks}
}

// MemoryCoordinator 暴露生命周期 hooks 中共享的磁盘锁协调器。
// 调用方只读取该实例，不在服务层新建，避免不同写入路径拿到不同锁域。
func (s *service) MemoryCoordinator() *diskLockCoordinator {
	if s == nil || s.dreamHooks == nil {
		return nil
	}
	return s.dreamHooks.memoryCoordinator()
}

// Config 返回服务配置快照。
// 服务未初始化时返回零值配置，调用方仍需在具体读写入口做 fail-fast 校验。
func (s *service) Config() Config {
	if s == nil || s.cfg == nil {

		return Config{}
	}
	return *s.cfg
}

// RootDir 返回配置中的原始记忆根目录字符串。
// 该值未解析 project override；真正读写前必须走 resolvedStoreRoot。
func (s *service) RootDir() string {
	return strings.TrimSpace(s.Config().RootDir)
}

// EnsureRoot 创建当前私有和可选团队记忆根目录。
// 上下文取消、路径解析失败或 mkdir 失败都会立即返回错误，不用空目录兜底。
func (s *service) EnsureRoot(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	cfg := s.Config()
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		return err
	}
	if root == "" {
		return errors.New("memory root dir is empty")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if teamMemoryConfigured(cfg) {
		teamRoot, err := configuredTeamMemRoot(&cfg)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(teamRoot, 0o755); err != nil {
			return err
		}
	}
	if s.logger != nil {
		s.logger.Debug("memory root ready", "root_dir", filepath.Clean(root))
	}
	return nil
}

// RunConsolidation 启动一次手动记忆整理任务。
// 它先确保根目录存在，再让 consolidator 读取磁盘；成功后失效 prompt 区块，
// 让后续 turn 使用整理后的记忆索引。
func (s *service) RunConsolidation(ctx context.Context) error {
	if s == nil || s.consolidator == nil {
		return ErrConsolidationExtractFuncRequired
	}
	if err := s.EnsureRoot(ctx); err != nil {
		return err
	}
	cfg := s.Config()
	root, err := resolvedStoreRoot(cfg.RootDir, cfg.ProjectRoot, cfg.AutoMemPathOverride)
	if err != nil {
		return err
	}
	if err := s.consolidator.consolidateWithOptions(ctx, root, nil, s.consolidationRunOptions(ctx, root)); err != nil {
		return err
	}
	if s.dreamHooks != nil {
		s.dreamHooks.invalidateMemorySections()
	}
	return nil
}

// GetDreamTaskStatus 返回当前自动整理任务快照。
// hooks 缺失时返回零值，表示本进程没有正在管理的 dream task。
func (s *service) GetDreamTaskStatus() DreamTaskSnapshot {
	if s == nil || s.dreamHooks == nil {
		return DreamTaskSnapshot{}
	}
	return s.dreamHooks.GetDreamTaskStatus()
}

// NestedIngestHealthSnapshot 是 nested ingest 拒绝的唯一生产状态快照。
// 每次拒绝都保留累计数及最近一次的错误、线程和发生时间，供 UI health 消费。
type NestedIngestHealthSnapshot struct {
	RejectedTotal int64
	LastError     string
	LastAt        time.Time
	LastThreadID  string
}

// recordNestedIngestRejection 记录一次不能进入 nested ingest worker 的事件。
// 调用方必须传入真实错误；缺失 health owner 或错误属于装配/调用错误，立即终止而非静默丢弃。
func (h *MemoryLifecycleHooks) recordNestedIngestRejection(threadID string, err error) {
	if h == nil {
		panic("memory: nested ingest rejection health owner is required")
	}
	if err == nil {
		panic("memory: nested ingest rejection error is required")
	}
	h.nestedIngestMu.Lock()
	defer h.nestedIngestMu.Unlock()
	h.nestedIngestHealth.RejectedTotal++
	h.nestedIngestHealth.LastError = err.Error()
	h.nestedIngestHealth.LastAt = time.Now().UTC()
	h.nestedIngestHealth.LastThreadID = threadID
}

// GetNestedIngestHealth 返回 nested ingest 拒绝状态的一致快照。
func (h *MemoryLifecycleHooks) GetNestedIngestHealth() NestedIngestHealthSnapshot {
	if h == nil {
		return NestedIngestHealthSnapshot{}
	}
	h.nestedIngestMu.Lock()
	defer h.nestedIngestMu.Unlock()
	return h.nestedIngestHealth
}

// GetNestedIngestHealth 返回当前服务管理的 nested ingest 拒绝状态。
func (s *service) GetNestedIngestHealth() NestedIngestHealthSnapshot {
	if s == nil || s.dreamHooks == nil {
		return NestedIngestHealthSnapshot{}
	}
	return s.dreamHooks.GetNestedIngestHealth()
}

// KillDreamTask 请求终止正在运行的 dream 整理任务。
// 服务或 hooks 缺失时按“没有任务运行”返回，避免误报已停止。
func (s *service) KillDreamTask() error {
	if s == nil || s.dreamHooks == nil {
		return ErrDreamTaskNotRunning
	}
	return s.dreamHooks.KillDreamTask()
}

// consolidationRunOptions 组装手动整理的运行参数。
// runtimeContext 来源于上次整理 stamp 和会话统计，只影响提示信息，不改变读写根目录。
func (s *service) consolidationRunOptions(ctx context.Context, root string) consolidationRunOptions {
	return consolidationRunOptions{
		cfg:            s.cfg,
		runtimeContext: s.manualConsolidationRuntimeContext(ctx, root),
	}
}

// manualConsolidationRuntimeContext 构建手动整理任务的运行提示上下文。
// stamp 读取失败只影响上下文丰富度，不能阻断用户主动发起的整理。
func (s *service) manualConsolidationRuntimeContext(ctx context.Context, root string) string {
	if s == nil || s.dreamHooks == nil {
		return ""
	}
	stamp, err := loadConsolidationStamp(root)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) && s.logger != nil {
			s.logger.Warn("memory consolidation stamp load failed", "error", err)
		}
		return ""
	}
	sessions, err := s.dreamHooks.autoDreamSessionCount(ctx, "", stamp.lastSuccessTime())
	if err != nil {
		sessions = 0
	}
	return buildConsolidationRuntimeContext("manual consolidation request", sessions, stamp.lastSuccessTime(), "")
}

// onThreadStart 在 thread 启动时记录记忆 hook 已就绪。
// 该回调不做持久化工作，避免 thread start 事件被记忆模块阻塞。
func (h *MemoryLifecycleHooks) onThreadStart(_ context.Context, evt threaddto.Started) {
	if h == nil || !h.enabled || h.logger == nil {
		return
	}
	h.logger.Debug("memory thread hook ready", "thread_id", strings.TrimSpace(evt.ThreadID))
}

// onTurnEnd 处理 turn 完成后的显式记忆意图和运行时检测意图。
// 显式输入优先，避免同一轮既按 remember/forget 处理又被自动检测重复写入。
func (h *MemoryLifecycleHooks) onTurnEnd(ctx context.Context, evt turndto.TurnCompleted) {
	if h.shouldSkipTurnEnd(ctx, evt) {
		return
	}
	if h.handleTrackedTurnIntent(ctx, evt) {
		return
	}
	h.writeDetectedTurnIntent(ctx, evt)
}

// shouldSkipTurnEnd 决定这一轮结束后要不要碰 memory。
// 失败、取消或 memory 关闭时直接跳过，避免把不完整内容记住。
func (h *MemoryLifecycleHooks) shouldSkipTurnEnd(ctx context.Context, evt turndto.TurnCompleted) bool {
	if h == nil || !h.enabled || !evt.Success {
		return true
	}
	return contextErr(ctx) != nil
}

// handleTrackedTurnIntent 处理用户明确说 remember/forget 的输入。
// 明确输入优先，避免同一轮又被自动探测处理一次。
func (h *MemoryLifecycleHooks) handleTrackedTurnIntent(ctx context.Context, evt turndto.TurnCompleted) bool {
	key := turnTrackingKey(evt.ThreadID, evt.TurnID)
	if h.consumeHandledTurnInput(key) {
		h.clearTurnInput(key)
		return true
	}
	text, ok := h.consumeTurnInput(key)
	if !ok {
		return false
	}
	handled, action, err := h.handleExplicitUserMemoryIntent(ctx, evt, text)
	h.handleExplicitIntentError(evt, handled, action, err)
	return handled || err != nil
}

// writeDetectedTurnIntent 只写 runtime 明确识别出的保存意图。
// 普通 turn 不会因为文本像记忆就自动写入。
func (h *MemoryLifecycleHooks) writeDetectedTurnIntent(ctx context.Context, evt turndto.TurnCompleted) {
	intent := h.detectTurnIntent(ctx, evt)
	if !intent.Detected || strings.TrimSpace(intent.Content) == "" {
		return
	}
	if err := h.writeDetectedIntent(ctx, evt, intent); err != nil && h.logger != nil {
		h.logger.Warn("memory explicit save failed", "thread_id", strings.TrimSpace(evt.ThreadID), "error", err)
	}
}

func (h *MemoryLifecycleHooks) detectTurnIntent(ctx context.Context, evt turndto.TurnCompleted) SaveIntent {
	meta := h.resolveThreadRuntimeMetadata(ctx, strings.TrimSpace(evt.ThreadID))
	gate := ResolveMemoryGate(meta.buildCtx(), h.cfg)
	if gate.KairosActive {
		if intent := DetectKairosWriteIntent(evt); intent.Detected {
			return intent
		}
	}
	return SaveIntent{}
}

// writeIntent 把确认要保存的内容写进 private/team memory。
// 写完后要让 prompt 重新读，否则后续 start/turn 可能还看到旧内容。
func (h *MemoryLifecycleHooks) writeIntent(ctx context.Context, threadID string, intent SaveIntent) error {
	trackFeedbackIfApplicable(h, intent)

	entry := buildExplicitMemoryWrite(intent)
	options := h.writeOptions(ctx, threadID)
	primary, secondary, err := h.intentDiskStores(ctx, threadID, entry.Type)
	if err != nil {
		return err
	}
	store, err := selectExplicitWriteStore(entry.Name, primary, secondary)
	if err != nil {
		return err
	}
	primaryScope, secondaryScope := scopeNamesForIntentStores(entry.Type, secondary != nil)
	h.warnCrossScopeSameName(entry.Name, store, primary, secondary, primaryScope, secondaryScope)

	scope := primaryScope
	if store == secondary {
		scope = secondaryScope
	}
	handled, dedupErr := h.checkDedupAndHandle(entry, store, scope, options)
	if dedupErr != nil {
		return dedupErr
	}
	if handled {
		return nil
	}

	if writeErr := upsertStructuredMemory(store, entry, options); writeErr != nil {
		return writeErr
	}
	if err := h.maybeOverflowMerge(store, entry.Type, options); err != nil {
		h.invalidateMemorySections()
		return agentMemoryError("partial", err)
	}
	return nil
}

// checkDedupAndHandle 在写入前执行去重决策。
// 返回 true 表示本次写入已被 skip 或 merge 完成，调用方不能继续执行普通 upsert。
// 去重扫描失败会返回错误并阻断写入，避免重复记忆在告警失败时继续扩散。
func (h *MemoryLifecycleHooks) checkDedupAndHandle(entry MemoryWriteRequest, store memoryStructuredStore, scope string, options WriteOptions) (bool, error) {
	if h.dedupFilter == nil {
		return false, nil
	}
	candidate := dedup.EntrySnapshot{
		Name:        entry.Name,
		Type:        string(entry.Type),
		Description: entry.Description,
		Content:     entry.Body,
		Scope:       scope,
	}
	result, err := h.dedupFilter.Check(candidate)
	if err != nil {
		if h.logger != nil {
			h.logger.Warn("memory dedup check failed", "err", err, "scope", scope, "type", entry.Type)
		}
		return false, fmt.Errorf("memory dedup check: %w", err)
	}
	switch result.Action {
	case dedup.Skip:
		return true, nil
	case dedup.Merge:
		return h.tryDedupMerge(result, store, entry.Type, options)
	}
	return false, nil
}

// tryDedupMerge 将去重器给出的 merge 结果写回当前 store。
// 只有支持写入的 store 才执行 merge；不支持时交还调用方继续普通写入路径。
func (h *MemoryLifecycleHooks) tryDedupMerge(result dedup.CheckResult, store memoryStructuredStore, memType MemoryType, options WriteOptions) (bool, error) {
	ws, ok := store.(memoryWriteStore)
	if !ok {
		return false, nil
	}
	merged := snapshotToMemoryEntry(*result.MergedEntry)
	if mergeErr := mergeAndWriteMemory(ws, result.TargetPath, merged, options, h.memoryCoordinator()); mergeErr != nil {
		return false, mergeErr
	}
	if err := h.handleDedupOverflow(ws, memType, options); err != nil {
		return false, err
	}
	return true, nil
}

// maybeOverflowMerge 在普通写入成功后触发同 scope 溢出合并。
// 只对可写 store 生效，避免只读或测试替身误进入删除路径。
func (h *MemoryLifecycleHooks) maybeOverflowMerge(store memoryStructuredStore, memType MemoryType, options WriteOptions) error {
	if ws, ok := store.(memoryWriteStore); ok {
		return h.handleDedupOverflow(ws, memType, options)
	}
	return nil
}

// handleDedupOverflow 只在当前写入 store 内执行溢出合并。
// 全局去重可以跨 scope 检测重复，但溢出删除不能拿 private 的指令去改 team store，
// 也不能反向操作，避免越过记忆 scope 边界。
func (h *MemoryLifecycleHooks) handleDedupOverflow(store memoryWriteStore, memType MemoryType, options WriteOptions) error {
	if store == nil {
		return nil
	}
	filter := dedup.NewFilter(func(typeFilter string) ([]dedup.EntrySnapshot, error) {
		return scanEntriesAsSnapshots(store.Root(), typeFilter, storeScopeName(store, h))
	}, nil)
	instruction, err := filter.FindOverflowMerge(string(memType))
	if err != nil {
		if h != nil && h.logger != nil {
			h.logger.Warn("memory dedup overflow check failed", "err", err, "scope", storeScopeName(store, h), "type", memType)
		}
		return fmt.Errorf("%w: %v", ErrMemoryOverflowMergeFailed, err)
	}
	if instruction == nil {
		return nil
	}
	merged := snapshotToMemoryEntry(instruction.KeepEntry)
	if err := overflowMergeAndDelete(store, instruction.KeepEntry.Path, merged, instruction.DeletePath, options, h.memoryCoordinator()); err != nil {
		if h != nil && h.logger != nil {
			h.logger.Warn("memory dedup overflow merge failed", "err", err, "scope", storeScopeName(store, h), "type", memType)
		}
		return fmt.Errorf("%w: %v", ErrMemoryOverflowMergeFailed, err)
	}
	return nil
}

// storeScopeName 返回当前 store 对应的 scope 标签。
// 团队根目录与 store 根目录相同才标记为 team，其余写入路径默认视为 private。
func storeScopeName(store memoryWriteStore, h *MemoryLifecycleHooks) string {
	if store == nil {
		return ""
	}
	root := filepath.Clean(store.Root())
	if h != nil && h.team != nil {
		teamRoot := filepath.Clean(strings.TrimSpace(h.team.GetTeamMemPath()))
		if teamRoot != "." && root == teamRoot {
			return "team"
		}
	}
	return "private"
}

// warnCrossScopeSameName 在显式写入遇到跨 scope 同名条目时只告警一次。
// combined 模式下写入会选择一个 scope，但另一个 scope 的同名记忆仍可能被检索排序命中；
// 告警提醒运维者处理分歧，不阻断用户本次写入。
func (h *MemoryLifecycleHooks) warnCrossScopeSameName(name string, selected, primary, secondary memoryStructuredStore, primaryScope, secondaryScope string) {
	if h == nil || h.logger == nil {
		return
	}
	var (
		other              memoryStructuredStore
		selScope, oppScope string
	)
	switch selected {
	case primary:
		other, selScope, oppScope = secondary, primaryScope, secondaryScope
	case secondary:
		other, selScope, oppScope = primary, secondaryScope, primaryScope
	}
	if other == nil {
		return
	}
	if _, err := other.Read(name); err != nil {
		return
	}
	coordinator := h.memoryCoordinator()
	if coordinator == nil || !coordinator.markCrossScopeSameNameWarned(name) {
		return
	}

	h.logger.Warn("memory cross-scope same-name entry detected",
		"name", name,
		"selected_scope", selScope,
		"other_scope", oppScope,
		"note", "explicit write went to selected store; same-name entry exists in the other scope",
	)
}

// scopeNamesForIntentStores 返回 intentDiskStores 对应的 primary/secondary scope 标签。
// 没有团队 store 时 primary 固定为 private；有团队 store 时由记忆类型默认 scope 决定。
func scopeNamesForIntentStores(memoryType MemoryType, hasSecondary bool) (primaryScope, secondaryScope string) {
	if !hasSecondary {
		return "private", ""
	}
	if defaultTeamMemoryType(memoryType) {
		return "team", "private"
	}
	return "private", "team"
}

// deleteIntent 在 private/team 可见 store 中删除显式 forget 命中的记忆。
// 删除成功后立即失效 prompt 区块，避免下一轮仍注入已删除的索引内容。
func (h *MemoryLifecycleHooks) deleteIntent(ctx context.Context, threadID string, intent ForgetIntent) error {
	options := h.writeOptions(ctx, threadID)
	primary, secondary, err := h.intentDiskStores(ctx, threadID, MemoryTypeUnknown)
	if err != nil {
		return err
	}
	if err := deleteMemoryAcrossStores(intent.Query, options, primary, secondary); err != nil {
		return err
	}
	h.invalidateMemorySections()
	return nil
}

// memoryCoordinator 返回 hooks 持有的共享磁盘锁协调器。
// nil hooks 返回 nil，调用方必须按无协调器路径显式处理。
func (h *MemoryLifecycleHooks) memoryCoordinator() *diskLockCoordinator {
	if h == nil {
		return nil
	}
	return h.locks
}

// diskStore 根据 hooks 当前根目录配置创建可写磁盘 store。
// 路径解析失败会返回错误，禁止退回未解析根目录继续写。
func (h *MemoryLifecycleHooks) diskStore() (memoryWriteStore, error) {
	root, err := resolvedStoreRoot(h.rootDir, h.projectRoot, h.autoMemPathOverride)
	if err != nil {
		return nil, err
	}
	return newDiskStore(root, h.memoryCoordinator())
}

// ForgetIntent 表示 runtime 从用户文本中识别出的删除记忆意图。
// Query 只用于匹配可见 store，真正删除仍由 private/team 路由和路径校验控制。
type ForgetIntent struct {
	Detected bool
	Query    string
}

// handleExplicitUserMemoryIntent 识别并执行用户显式 remember/forget 指令。
// 未识别为记忆指令时返回 handled=false；识别后所有写删错误都原样返回给调用方记录。
func (h *MemoryLifecycleHooks) handleExplicitUserMemoryIntent(
	ctx context.Context,
	evt turndto.TurnCompleted,
	text string,
) (bool, string, error) {
	if forget := DetectForgetIntent(text); forget.Detected {
		return true, "forget", h.deleteIntent(ctx, evt.ThreadID, forget)
	}
	intent := DetectSaveIntent(text)
	if !intent.Detected {
		return false, "", nil
	}
	return true, "remember", h.writeDetectedIntent(ctx, evt, intent)
}

// handleExplicitIntentError 记录显式记忆指令失败。
// handled=false 表示普通文本，不应记录为记忆错误。
func (h *MemoryLifecycleHooks) handleExplicitIntentError(evt turndto.TurnCompleted, handled bool, action string, err error) {
	if !handled || err == nil {
		return
	}
	threadID := strings.TrimSpace(evt.ThreadID)
	if h != nil && h.logger != nil {
		h.logger.Warn("memory explicit intent failed", "thread_id", threadID, "turn_id", strings.TrimSpace(evt.TurnID), "action", strings.TrimSpace(action), "error", err)
	}
	publishMemoryIntentFailedDiagnostic(memoryIntentFailureDiagnostic{
		ThreadID: threadID,
		AgentID:  strings.TrimSpace(evt.AgentID),
		TurnID:   strings.TrimSpace(evt.TurnID),
		Action:   strings.TrimSpace(action),
		Error:    redactedMemoryIntentError(err),
	})
}

// resolvedStoreRoot 解析最终可写记忆根目录。
// 显式 override 必须通过 ValidateMemoryRoot；没有 override 时按 projectRoot 派生 AutoMem，
// 再退回配置根目录，任何空根目录都返回错误。
func resolvedStoreRoot(baseRoot, projectRoot, autoMemPathOverride string) (string, error) {
	if override := strings.TrimSpace(autoMemPathOverride); override != "" {
		validatedOverride, err := shared.ValidateMemoryRoot(override)
		if err != nil {
			return "", err
		}
		if validatedOverride == "" {
			return "", errors.New("memory path override is empty")
		}
		return strings.TrimSuffix(validatedOverride, string(os.PathSeparator)), nil
	}
	baseRoot = strings.TrimSpace(baseRoot)
	if baseRoot == "" {
		return "", errors.New("memory root dir is empty")
	}
	if projectRoot = strings.TrimSpace(projectRoot); projectRoot != "" {
		root, err := GetAutoMemPath(baseRoot, projectRoot)
		if err != nil {
			return "", err
		}
		if strings.TrimSpace(root) == "" {
			return "", errors.New("auto memory path is empty")
		}
		return root, nil
	}
	return baseRoot, nil
}
