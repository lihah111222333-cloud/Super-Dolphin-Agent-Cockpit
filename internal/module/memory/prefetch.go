package memory

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const (
	prefetchStatePending int32 = iota
	prefetchStateReady
	prefetchStateConsumed
	prefetchStateDiscarded
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

	mu         sync.Mutex
	generation uint64
	current    *PrefetchHandle
}

func NewPrefetchManager(memoryRoot string) *PrefetchManager {
	finder := NewRelevantMemoryFinder()
	builder := NewManifestBuilder()
	return &PrefetchManager{
		memoryRoot:    memoryRoot,
		finder:        finder,
		builder:       builder,
		buildManifest: builder.BuildManifest,
		findRelevant:  finder.FindRelevantMemories,
		timeNow:       time.Now,
		generation:    0,
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
		m.finishHandle(handle, prefetchStateDiscarded, nil, nil)
		return handle
	}

	go m.runPrefetch(childCtx, handle)
	return handle
}

func (m *PrefetchManager) ConsumeIfReady(handle *PrefetchHandle) ([]MemoryEntry, bool) {
	if m == nil || handle == nil || handle.state.Load() != prefetchStateReady {
		return nil, false
	}
	if !m.isCurrentGeneration(handle.generation) {
		handle.state.CompareAndSwap(prefetchStateReady, prefetchStateDiscarded)
		return nil, false
	}
	if !handle.state.CompareAndSwap(prefetchStateReady, prefetchStateConsumed) {
		return nil, false
	}
	handle.consumedOnIteration.CompareAndSwap(-1, 0)
	return handle.snapshot(), true
}

func (m *PrefetchManager) runPrefetch(ctx context.Context, handle *PrefetchHandle) {
	manifest, err := m.buildManifestFn()(m.memoryRoot)
	if err != nil {
		m.finishHandle(handle, prefetchStateReady, nil, err)
		return
	}
	entries, err := m.findRelevantFn()(ctx, handle.query, manifest)
	if isContextError(err) || isContextError(ctx.Err()) || !m.isCurrentGeneration(handle.generation) {
		m.finishHandle(handle, prefetchStateDiscarded, nil, firstContextError(err, ctx.Err()))
		return
	}
	if err != nil {
		entries = nil
	}
	m.finishHandle(handle, prefetchStateReady, entries, err)
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

func newPrefetchHandle(generation uint64, query string, cancel context.CancelFunc) *PrefetchHandle {
	handle := &PrefetchHandle{
		generation: generation,
		query:      query,
		turnID:     "prefetch-" + time.Now().Format("150405.000000000"),
		cancel:     cancel,
		done:       make(chan struct{}),
	}
	handle.state.Store(prefetchStatePending)
	handle.consumedOnIteration.Store(-1)
	return handle
}

func (h *PrefetchHandle) snapshot() []MemoryEntry {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return cloneEntries(h.result)
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
