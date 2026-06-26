package retrieval

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// prefetch 状态常量描述单个 PrefetchHandle 从启动、可消费到终止的状态机。
const (
	PrefetchStatePending int32 = iota
	PrefetchStateReady
	PrefetchStateConsumed
	PrefetchStateDiscarded
)

// PrefetchHandle 代表一次 turn 相关记忆预取任务。
// generation 用来丢弃过期任务，done 只关闭一次，result/err 必须通过 mu 读写。
type PrefetchHandle struct {
	generation uint64             // 所属 manager generation，防止旧 goroutine 覆盖新查询。
	query      string             // 触发预取的规范化用户输入。
	turnID     string             // 调试用本地任务标识，不参与业务路由。
	cancel     context.CancelFunc // 取消底层查找任务。
	done       chan struct{}      // 任务 settle 后关闭，调用方可等待。
	once       sync.Once          // 保护 done 只关闭一次。

	state               atomic.Int32 // PrefetchState*，用 CAS 控制 ready 只能消费一次。
	settledAt           atomic.Int64 // UnixNano，便于后续观测任务完成时间。
	consumedOnIteration atomic.Int32 // 兼容迭代消费标记，-1 表示尚未消费。

	mu     sync.RWMutex // 保护 result 和 err。
	result []MemoryEntry
	err    error
}

// PrefetchManager 管理每个 thread 当前查询的相关记忆预取任务。
// 任一新查询都会递增 generation 并取消旧任务，已经 surfaced 的条目在同一 manager 内去重。
type PrefetchManager struct {
	memoryRoot string                // 记忆根目录；为空时预取直接 discard。
	finder     *RelevantMemoryFinder // 默认相关记忆查找器。
	builder    *ManifestBuilder      // 默认 manifest 构建器。

	// buildManifest 是测试可替换的 manifest 构建函数。
	buildManifest func(string) ([]MemoryEntry, error)
	findRelevant  func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error) // 测试可替换的检索函数。
	timeNow       func() time.Time                                                    // 测试可替换的时间源。

	mu              sync.Mutex
	generation      uint64              // 每次启动或 reset 递增，用于淘汰旧 handle。
	current         *PrefetchHandle     // 当前允许被消费的 handle。
	alreadySurfaced map[string]struct{} // 本 manager 已返回给用户的记忆去重键。
}

// NewPrefetchManager 创建相关记忆预取管理器，并默认用 surfaced snapshot 排除重复条目。
func NewPrefetchManager(memoryRoot string) *PrefetchManager {
	finder := NewRelevantMemoryFinder()
	builder := NewManifestBuilder()
	manager := &PrefetchManager{
		memoryRoot:      memoryRoot,
		finder:          finder,
		builder:         builder,
		buildManifest:   builder.BuildManifest,
		timeNow:         time.Now,
		alreadySurfaced: map[string]struct{}{},
	}
	manager.findRelevant = func(ctx context.Context, query string, manifest []MemoryEntry) ([]MemoryEntry, error) {
		return finder.FindRelevantMemoriesWithAlreadySurfaced(ctx, query, manifest, manager.surfacedSnapshot())
	}
	return manager
}

// SetBuildManifestFunc 替换 manifest 构建函数，主要供测试固定输入和错误路径。
func (m *PrefetchManager) SetBuildManifestFunc(fn func(string) ([]MemoryEntry, error)) {
	if m != nil {
		m.buildManifest = fn
	}
}

// SetFindRelevantFunc 替换相关记忆查找函数，主要供测试验证状态机。
func (m *PrefetchManager) SetFindRelevantFunc(fn func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error)) {
	if m != nil {
		m.findRelevant = fn
	}
}

// SetTimeNowFunc 替换时间源，避免测试依赖真实时钟。
func (m *PrefetchManager) SetTimeNowFunc(fn func() time.Time) {
	if m != nil {
		m.timeNow = fn
	}
}

// StartRelevantMemoryPrefetch 启动新的相关记忆预取任务。
// 新任务会取消旧 handle；空 query 或空 root 仍返回已 discard 的 handle，方便调用方统一等待。
func (m *PrefetchManager) StartRelevantMemoryPrefetch(ctx context.Context, query string) *PrefetchHandle {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	query = strings.TrimSpace(query)
	childCtx, cancel := context.WithCancel(ctx)

	m.mu.Lock()
	m.generation++
	generation := m.generation
	previous := m.current
	handle := newPrefetchHandle(generation, query, cancel)
	m.current = handle
	m.mu.Unlock()

	if previous != nil && previous.cancel != nil {
		previous.cancel()
	}
	if query == "" || strings.TrimSpace(m.memoryRoot) == "" {
		m.finishHandle(handle, PrefetchStateDiscarded, nil, nil)
		return handle
	}

	go m.runPrefetch(childCtx, handle)
	return handle
}

// ConsumeIfReady 只消费当前 generation 的 ready handle，并通过 CAS 保证结果最多返回一次。
// ready handle 若携带构建或检索错误，会 fail-closed 返回 ok=false；调用方可通过 handle.Err() 区分错误和未就绪。
func (m *PrefetchManager) ConsumeIfReady(handle *PrefetchHandle) ([]MemoryEntry, bool) {
	if m == nil || handle == nil || handle.state.Load() != PrefetchStateReady {
		return nil, false
	}
	if handle.Err() != nil {
		handle.state.CompareAndSwap(PrefetchStateReady, PrefetchStateDiscarded)
		return nil, false
	}
	if !m.isCurrentGeneration(handle.generation) {
		handle.state.CompareAndSwap(PrefetchStateReady, PrefetchStateDiscarded)
		return nil, false
	}
	if !handle.state.CompareAndSwap(PrefetchStateReady, PrefetchStateConsumed) {
		return nil, false
	}
	handle.consumedOnIteration.CompareAndSwap(-1, 0)
	return handle.snapshot(), true
}

// FilterAlreadySurfaced 过滤本 manager 已经展示过的记忆，避免连续 turn 重复注入。
func (m *PrefetchManager) FilterAlreadySurfaced(entries []MemoryEntry) []MemoryEntry {
	if m == nil || len(entries) == 0 {
		return entries
	}
	return filterAlreadySurfacedEntries(entries, m.surfacedSnapshot())
}

// MarkSurfaced 记录已经交给上层 prompt 的记忆条目，后续预取会据此去重。
func (m *PrefetchManager) MarkSurfaced(entries []MemoryEntry) {
	if m == nil || len(entries) == 0 {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.alreadySurfaced == nil {
		m.alreadySurfaced = map[string]struct{}{}
	}
	rememberSurfacedEntries(m.alreadySurfaced, entries)
}

// Reset 取消当前预取任务并清空 surfaced 去重集合；调用方用 reason 做审计但这里不保留。
func (m *PrefetchManager) Reset(reason string) {
	if m == nil {
		return
	}
	_ = strings.TrimSpace(reason)
	m.mu.Lock()
	m.generation++
	current := m.current
	m.current = nil
	m.alreadySurfaced = map[string]struct{}{}
	m.mu.Unlock()
	if current != nil && current.cancel != nil {
		current.cancel()
	}
}

// ResetSurfaced 仅清空 surfaced 去重集合，不取消当前预取任务。
func (m *PrefetchManager) ResetSurfaced(reason string) {
	if m == nil {
		return
	}
	_ = strings.TrimSpace(reason)
	m.mu.Lock()
	m.alreadySurfaced = map[string]struct{}{}
	m.mu.Unlock()
}

// runPrefetch 在后台构建 manifest 并查找相关记忆。
// 取消、过期 generation 或上下文结束都会转为 discarded。
func (m *PrefetchManager) runPrefetch(ctx context.Context, handle *PrefetchHandle) {
	manifest, err := m.buildManifestFn()(m.memoryRoot)
	if err != nil {
		m.finishHandle(handle, PrefetchStateReady, nil, err)
		return
	}
	entries, err := m.findRelevantFn()(ctx, handle.query, manifest)
	if isContextError(err) || isContextError(ctx.Err()) || !m.isCurrentGeneration(handle.generation) {
		m.finishHandle(handle, PrefetchStateDiscarded, nil, firstContextError(err, ctx.Err()))
		return
	}
	if err != nil {
		entries = nil
	}
	m.finishHandle(handle, PrefetchStateReady, entries, err)
}

// finishHandle 原子化发布预取结果并关闭 done，调用方必须在设置 result/err 后再更新 state。
func (m *PrefetchManager) finishHandle(handle *PrefetchHandle, state int32, entries []MemoryEntry, err error) {
	if handle == nil {
		return
	}
	handle.mu.Lock()
	handle.result = cloneEntries(entries)
	handle.err = err
	handle.mu.Unlock()
	handle.state.Store(state)
	handle.settledAt.Store(m.now().UnixNano())
	handle.once.Do(func() { close(handle.done) })
}

// now 返回可替换的时间源，nil manager 或未配置时回退到真实时间。
func (m *PrefetchManager) now() time.Time {
	if m != nil && m.timeNow != nil {
		return m.timeNow()
	}
	return time.Now()
}

// buildManifestFn 返回当前 manifest 构建函数，缺省时创建默认 builder 保持调用方无需判空。
func (m *PrefetchManager) buildManifestFn() func(string) ([]MemoryEntry, error) {
	if m != nil && m.buildManifest != nil {
		return m.buildManifest
	}
	return NewManifestBuilder().BuildManifest
}

// findRelevantFn 返回当前相关记忆查找函数，缺省时使用默认 finder。
func (m *PrefetchManager) findRelevantFn() func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error) {
	if m != nil && m.findRelevant != nil {
		return m.findRelevant
	}
	return NewRelevantMemoryFinder().FindRelevantMemories
}

// isCurrentGeneration 校验 handle 是否仍是当前可消费任务。
func (m *PrefetchManager) isCurrentGeneration(generation uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.generation == generation && m.current != nil && m.current.generation == generation
}

// surfacedSnapshot 克隆已展示集合，避免 finder 在无锁状态下看到可变 map。
func (m *PrefetchManager) surfacedSnapshot() map[string]struct{} {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneSurfacedSet(m.alreadySurfaced)
}

// newPrefetchHandle 初始化待执行 handle，并把 consumedOnIteration 置为未消费哨兵值。
func newPrefetchHandle(generation uint64, query string, cancel context.CancelFunc) *PrefetchHandle {
	handle := &PrefetchHandle{
		generation: generation,
		query:      query,
		turnID:     "prefetch-" + time.Now().Format("150405.000000000"),
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	handle.state.Store(PrefetchStatePending)
	handle.consumedOnIteration.Store(-1)
	return handle
}

// snapshot 在读锁内复制结果，防止调用方修改 manager 内部缓存。
func (h *PrefetchHandle) snapshot() []MemoryEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return cloneEntries(h.result)
}

// Query 返回触发本次预取的查询文本。
func (h *PrefetchHandle) Query() string {
	if h == nil {
		return ""
	}
	return h.query
}

// State 返回当前预取状态；nil handle 视为已丢弃，方便上层 fail-closed。
func (h *PrefetchHandle) State() int32 {
	if h == nil {
		return PrefetchStateDiscarded
	}
	return h.state.Load()
}

// Done 返回任务完成信号；nil handle 没有可等待的 channel。
func (h *PrefetchHandle) Done() <-chan struct{} {
	if h == nil {
		return nil
	}
	return h.done
}

// Cancel 取消当前预取任务，已完成的 handle 调用也安全。
func (h *PrefetchHandle) Cancel() {
	if h != nil && h.cancel != nil {
		h.cancel()
	}
}

// Err 返回后台查找错误，必须在 done 关闭后读取才有稳定语义。
func (h *PrefetchHandle) Err() error {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.err
}

// isContextError 判断错误是否来自上下文取消或超时。
func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

// firstContextError 返回候选错误中的第一个上下文错误。
func firstContextError(candidates ...error) error {
	for _, err := range candidates {
		if isContextError(err) {
			return err
		}
	}
	return nil
}
