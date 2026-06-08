package contextlock

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

const maxReaders int64 = 1_000_000

type RWMutex struct {
	once sync.Once
	sem  *semaphore.Weighted
}

func (m *RWMutex) init() {
	m.once.Do(func() {
		m.sem = semaphore.NewWeighted(maxReaders)
	})
}

func (m *RWMutex) Lock() {
	_ = m.acquire(context.Background(), maxReaders)
}

func (m *RWMutex) Unlock() {
	m.init()
	m.sem.Release(maxReaders)
}

func (m *RWMutex) RLock() {
	_ = m.acquire(context.Background(), 1)
}

func (m *RWMutex) RUnlock() {
	m.init()
	m.sem.Release(1)
}

func (m *RWMutex) RLockCtx(ctx context.Context) error {
	return m.acquire(ctx, 1)
}

func (m *RWMutex) acquire(ctx context.Context, weight int64) error {
	m.init()
	return m.sem.Acquire(ctx, weight)
}
