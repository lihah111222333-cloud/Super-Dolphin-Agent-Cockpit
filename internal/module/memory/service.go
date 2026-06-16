package memory

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	threaddto "github.com/anthropic-ai/super-agent-v3/internal/dto/thread"
	turndto "github.com/anthropic-ai/super-agent-v3/internal/dto/turn"
	"github.com/anthropic-ai/super-agent-v3/internal/module/memory/dedup"
	shared "github.com/anthropic-ai/super-agent-v3/internal/module/memory/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

type Service interface {
	Config() Config
	RootDir() string
	EnsureRoot(ctx context.Context) error
	RunConsolidation(ctx context.Context) error
	GetDreamTaskStatus() DreamTaskSnapshot
	KillDreamTask() error
	MemoryCoordinator() *diskLockCoordinator
}

type service struct {
	cfg          *Config
	logger       *pkglogger.Logger
	consolidator *AutoDreamConsolidator
	dreamHooks   *MemoryLifecycleHooks
}

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
	logger              *pkglogger.Logger
	timeNow             func() time.Time
	feedbackTracker     *FeedbackTracker
	onFeedbackThreshold func(topicKey string, group []ExtractedMemory)

	// stateMu serialises all reads and writes of the six maps below.
	// The plain map choice (vs sync.Map) is intentional: this is a
	// coarse-grained mutex over the whole turn/extraction bookkeeping
	// surface, and ExtractionState values carry their own mu for
	// finer-grained field protection once a *ExtractionState reference
	// has been resolved under stateMu (the maps never delete entries
	// that other goroutines may still hold a reference to, so the
	// reference itself stays valid after stateMu is released).
	// New callers MUST hold stateMu while touching any of these maps.
	stateMu           sync.Mutex
	states            map[string]*ExtractionState    // guarded by stateMu
	activeTurns       map[string]string              // guarded by stateMu
	callTurns         map[string]toolCallScope       // guarded by stateMu
	turnWrites        map[string]map[string]struct{} // guarded by stateMu
	turnInputs        map[string]string              // guarded by stateMu
	handledTurnInputs map[string]struct{}            // guarded by stateMu
	extractWG         sync.WaitGroup

	// drainMu + drainClosed guard the extractWG against the classic
	// sync.WaitGroup race: once DrainPendingExtraction calls Wait(),
	// concurrent Add(1) from a new enqueueBackgroundExtraction can panic
	// with "WaitGroup is reused before previous Wait has returned".
	// drainClosed is set monotonically in Drain (close-path semantics);
	// new enqueues entering after Drain are dropped. Field is named
	// `drainClosed` (not `drainPending`) to avoid confusion with the
	// unrelated `drainPending()` method on nestedIngestWorker /
	// teamSyncCoordinator in the same package, which empty a queue
	// rather than flag a close.
	drainMu     sync.Mutex
	drainClosed bool

	dreamMu   sync.Mutex
	dreamTask *dreamTaskState

	dedupFilter *dedup.Filter

	// locks is the process-scoped memory coordinator shared across all
	// diskStore instances and cross-scope warning dedupe for this lifecycle.
	// Required field — constructors (provideMemoryLifecycleHooks, newTestHooks)
	// MUST set it. memoryCoordinator() is a pure getter; lazy-init is forbidden
	// because it would race callers and silently overwrite the
	// consolidator-shared instance wired in module.go.
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

// NewService 创建模块服务并注入存储和运行依赖。
func NewService(cfg *Config, logger *pkglogger.Logger, consolidator *AutoDreamConsolidator, hooks *MemoryLifecycleHooks) Service {
	return newServiceWithConsolidator(cfg, logger, consolidator, hooks)
}

func newServiceWithConsolidator(cfg *Config, logger *pkglogger.Logger, consolidator *AutoDreamConsolidator, hooks *MemoryLifecycleHooks) Service {
	if cfg == nil {
		cfg = &Config{}
	}
	if consolidator == nil {
		consolidator = NewAutoDreamConsolidator(nil)
	}
	consolidator.cfg = memoryConfig(cfg)
	return &service{cfg: cfg, logger: logger, consolidator: consolidator, dreamHooks: hooks}
}

// MemoryCoordinator 返回底层记忆协调器。
func (s *service) MemoryCoordinator() *diskLockCoordinator {
	if s == nil || s.dreamHooks == nil {
		return nil
	}
	return s.dreamHooks.memoryCoordinator()
}

// Config 返回服务当前配置。
func (s *service) Config() Config {
	if s == nil || s.cfg == nil {

		return Config{}
	}
	return *s.cfg
}

// RootDir 返回当前服务使用的根目录。
func (s *service) RootDir() string {
	return strings.TrimSpace(s.Config().RootDir)
}

// EnsureRoot 确保记忆根目录存在且可用。
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

// GetDreamTaskStatus 读取 dream 整理任务状态。
func (s *service) GetDreamTaskStatus() DreamTaskSnapshot {
	if s == nil || s.dreamHooks == nil {
		return DreamTaskSnapshot{}
	}
	return s.dreamHooks.GetDreamTaskStatus()
}

// KillDreamTask 终止正在运行的 dream 整理任务。
func (s *service) KillDreamTask() error {
	if s == nil || s.dreamHooks == nil {
		return ErrDreamTaskNotRunning
	}
	return s.dreamHooks.KillDreamTask()
}

func (s *service) consolidationRunOptions(ctx context.Context, root string) consolidationRunOptions {
	return consolidationRunOptions{
		cfg:            s.cfg,
		runtimeContext: s.manualConsolidationRuntimeContext(ctx, root),
	}
}

// manualConsolidationRuntimeContext 构建手动整理任务所需的 runtime 上下文。
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

func (h *MemoryLifecycleHooks) onThreadStart(_ context.Context, evt threaddto.Started) {
	if h == nil || !h.enabled || h.logger == nil {
		return
	}
	h.logger.Debug("memory thread hook ready", "thread_id", strings.TrimSpace(evt.ThreadID))
}

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
	handled, err := h.handleExplicitUserMemoryIntent(ctx, evt, text)
	h.handleExplicitIntentError(evt.ThreadID, handled, err)
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
	h.maybeOverflowMerge(store, entry.Type, options)
	return nil
}

// checkDedupAndHandle runs the dedup filter check and handles Skip/Merge.
// Returns true if the write was fully handled (caller should not proceed).
// checkDedupAndHandle 检查记忆去重并执行对应处理。
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

func (h *MemoryLifecycleHooks) tryDedupMerge(result dedup.CheckResult, store memoryStructuredStore, memType MemoryType, options WriteOptions) (bool, error) {
	ws, ok := store.(memoryWriteStore)
	if !ok {
		return false, nil
	}
	merged := snapshotToMemoryEntry(*result.MergedEntry)
	if mergeErr := mergeAndWriteMemory(ws, result.TargetPath, merged, options, h.memoryCoordinator()); mergeErr != nil {
		return false, mergeErr
	}
	h.handleDedupOverflow(ws, memType, options)
	return true, nil
}

func (h *MemoryLifecycleHooks) maybeOverflowMerge(store memoryStructuredStore, memType MemoryType, options WriteOptions) {
	if ws, ok := store.(memoryWriteStore); ok {
		h.handleDedupOverflow(ws, memType, options)
	}
}

// handleDedupOverflow checks and executes overflow merges within the current
// write store only. The global dedup filter scans cross-scope for duplicate
// detection, but overflow control must never use private-scope instructions
// to mutate a team store or vice versa.
// handleDedupOverflow 处理去重队列超过上限的情况。
func (h *MemoryLifecycleHooks) handleDedupOverflow(store memoryWriteStore, memType MemoryType, options WriteOptions) {
	if store == nil {
		return
	}
	filter := dedup.NewFilter(func(typeFilter string) ([]dedup.EntrySnapshot, error) {
		return scanEntriesAsSnapshots(store.Root(), typeFilter, storeScopeName(store, h))
	}, nil)
	instruction, err := filter.FindOverflowMerge(string(memType))
	if err != nil || instruction == nil {
		if err != nil && h != nil && h.logger != nil {
			h.logger.Warn("memory dedup overflow check failed", "err", err, "scope", storeScopeName(store, h), "type", memType)
		}
		return
	}
	merged := snapshotToMemoryEntry(instruction.KeepEntry)
	if err := overflowMergeAndDelete(store, instruction.KeepEntry.Path, merged, instruction.DeletePath, options, h.memoryCoordinator()); err != nil && h != nil && h.logger != nil {

		h.logger.Warn("memory dedup overflow merge failed", "err", err, "scope", storeScopeName(store, h), "type", memType)
	}
}

// storeScopeName 返回记忆存储 scope 的显示名称。
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

// warnCrossScopeSameName logs a single warn (dedup'd by name) when the
// same-name entry exists in BOTH the selected store and the other store
// of the (primary, secondary) pair. Combined mode invariant: explicit
// writes pick one scope, but if the entry already exists in the other
// scope under the same name, future retrieval may surface either copy
// depending on ranking. The warn signals this divergence to operators
// without blocking the write. Phase 4.1a 子项 3.3.
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

// scopeNamesForIntentStores returns the scope tag corresponding to the
// (primary, secondary) pair returned by intentDiskStores. Mirrors the
// routing logic at intentDiskStores: when no team store is available,
// primary is always private; otherwise primary depends on
// defaultTeamMemoryType. Phase 4.1a 子项 3.3.
func scopeNamesForIntentStores(memoryType MemoryType, hasSecondary bool) (primaryScope, secondaryScope string) {
	if !hasSecondary {
		return "private", ""
	}
	if defaultTeamMemoryType(memoryType) {
		return "team", "private"
	}
	return "private", "team"
}

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

func (h *MemoryLifecycleHooks) memoryCoordinator() *diskLockCoordinator {
	if h == nil {
		return nil
	}
	return h.locks
}

func (h *MemoryLifecycleHooks) diskStore() (memoryWriteStore, error) {
	root, err := resolvedStoreRoot(h.rootDir, h.projectRoot, h.autoMemPathOverride)
	if err != nil {
		return nil, err
	}
	return newDiskStore(root, h.memoryCoordinator())
}

type ForgetIntent struct {
	Detected bool
	Query    string
}

func (h *MemoryLifecycleHooks) handleExplicitUserMemoryIntent(
	ctx context.Context,
	evt turndto.TurnCompleted,
	text string,
) (bool, error) {
	if forget := DetectForgetIntent(text); forget.Detected {
		return true, h.deleteIntent(ctx, evt.ThreadID, forget)
	}
	intent := DetectSaveIntent(text)
	if !intent.Detected {
		return false, nil
	}
	return true, h.writeDetectedIntent(ctx, evt, intent)
}

func (h *MemoryLifecycleHooks) handleExplicitIntentError(threadID string, handled bool, err error) {
	if !handled || err == nil || h == nil || h.logger == nil {
		return
	}
	h.logger.Warn("memory explicit intent failed", "thread_id", strings.TrimSpace(threadID), "error", err)
}

// resolvedStoreRoot 解析当前记忆存储根目录。
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
		if root, err := GetAutoMemPath(baseRoot, projectRoot); err == nil && strings.TrimSpace(root) != "" {
			return root, nil
		}
	}
	return baseRoot, nil
}
