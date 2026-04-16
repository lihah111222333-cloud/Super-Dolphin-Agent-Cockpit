package retrieval

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	PrefetchStatePending int32 = iota
	PrefetchStateReady
	PrefetchStateConsumed
	PrefetchStateDiscarded
)

type PrefetchHandle struct {
	generation uint64
	query      string
	turnID     string
	cancel     context.CancelFunc
	done       chan struct{}
	once       sync.Once

	state               atomic.Int32
	settledAt           atomic.Int64
	consumedOnIteration atomic.Int32

	mu     sync.RWMutex
	result []MemoryEntry
	err    error
}

type PrefetchManager struct {
	memoryRoot string
	finder     *RelevantMemoryFinder
	builder    *ManifestBuilder

	buildManifest func(string) ([]MemoryEntry, error)
	findRelevant  func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error)
	timeNow       func() time.Time

	mu              sync.Mutex
	generation      uint64
	current         *PrefetchHandle
	alreadySurfaced map[string]struct{}
}

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

func (m *PrefetchManager) SetBuildManifestFunc(fn func(string) ([]MemoryEntry, error)) {
	if m != nil {
		m.buildManifest = fn
	}
}

func (m *PrefetchManager) SetFindRelevantFunc(fn func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error)) {
	if m != nil {
		m.findRelevant = fn
	}
}

func (m *PrefetchManager) SetTimeNowFunc(fn func() time.Time) {
	if m != nil {
		m.timeNow = fn
	}
}

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

func (m *PrefetchManager) ConsumeIfReady(handle *PrefetchHandle) ([]MemoryEntry, bool) {
	if m == nil || handle == nil || handle.state.Load() != PrefetchStateReady {
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

func (m *PrefetchManager) FilterAlreadySurfaced(entries []MemoryEntry) []MemoryEntry {
	if m == nil || len(entries) == 0 {
		return entries
	}
	return filterAlreadySurfacedEntries(entries, m.surfacedSnapshot())
}

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

func (m *PrefetchManager) ResetSurfaced(reason string) {
	if m == nil {
		return
	}
	_ = strings.TrimSpace(reason)
	m.mu.Lock()
	m.alreadySurfaced = map[string]struct{}{}
	m.mu.Unlock()
}

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

func (m *PrefetchManager) now() time.Time {
	if m != nil && m.timeNow != nil {
		return m.timeNow()
	}
	return time.Now()
}

func (m *PrefetchManager) buildManifestFn() func(string) ([]MemoryEntry, error) {
	if m != nil && m.buildManifest != nil {
		return m.buildManifest
	}
	return NewManifestBuilder().BuildManifest
}

func (m *PrefetchManager) findRelevantFn() func(context.Context, string, []MemoryEntry) ([]MemoryEntry, error) {
	if m != nil && m.findRelevant != nil {
		return m.findRelevant
	}
	return NewRelevantMemoryFinder().FindRelevantMemories
}

func (m *PrefetchManager) isCurrentGeneration(generation uint64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.generation == generation && m.current != nil && m.current.generation == generation
}

func (m *PrefetchManager) surfacedSnapshot() map[string]struct{} {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return cloneSurfacedSet(m.alreadySurfaced)
}

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

func (h *PrefetchHandle) snapshot() []MemoryEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return cloneEntries(h.result)
}

func (h *PrefetchHandle) Query() string {
	if h == nil {
		return ""
	}
	return h.query
}

func (h *PrefetchHandle) State() int32 {
	if h == nil {
		return PrefetchStateDiscarded
	}
	return h.state.Load()
}

func (h *PrefetchHandle) Done() <-chan struct{} {
	if h == nil {
		return nil
	}
	return h.done
}

func (h *PrefetchHandle) Cancel() {
	if h != nil && h.cancel != nil {
		h.cancel()
	}
}

func (h *PrefetchHandle) Err() error {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.err
}

func isContextError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func firstContextError(candidates ...error) error {
	for _, err := range candidates {
		if isContextError(err) {
			return err
		}
	}
	return nil
}
