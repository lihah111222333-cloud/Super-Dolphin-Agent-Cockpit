package contextlock

import (
	"context"
	"sync"

	"golang.org/x/sync/semaphore"
)

// maxReaders 是写锁需要一次性占用的读信号量总权重。
const maxReaders int64 = 1_000_000

// RWMutex 用 semaphore.Weighted 实现支持 context 的读锁。
// 标准 sync.RWMutex 不支持带 ctx 等待；这里用全量权重表示写锁，单权重表示读锁。
type RWMutex struct {
	once sync.Once
	sem  *semaphore.Weighted
}

// init 延迟创建 semaphore，保证零值 RWMutex 可用。
func (m *RWMutex) init() {
	m.once.Do(func() {
		m.sem = semaphore.NewWeighted(maxReaders)
	})
}

// Lock 获取写锁；等待期间使用不可取消上下文，不受调用方取消影响。
func (m *RWMutex) Lock() {
	_ = m.acquire(context.Background(), maxReaders)
}

// Unlock 释放写锁，必须与 Lock 成对调用。
func (m *RWMutex) Unlock() {
	m.init()
	m.sem.Release(maxReaders)
}

// RLock 获取读锁；等待期间使用不可取消上下文，不受调用方取消影响。
func (m *RWMutex) RLock() {
	_ = m.acquire(context.Background(), 1)
}

// RUnlock 释放读锁，必须与 RLock/RLockCtx 成对调用。
func (m *RWMutex) RUnlock() {
	m.init()
	m.sem.Release(1)
}

// RLockCtx 在上下文有效期内等待读锁，用于可能被取消的长等待路径。
func (m *RWMutex) RLockCtx(ctx context.Context) error {
	return m.acquire(ctx, 1)
}

// acquire 确保 semaphore 初始化后按指定权重等待。
func (m *RWMutex) acquire(ctx context.Context, weight int64) error {
	m.init()
	return m.sem.Acquire(ctx, weight)
}
