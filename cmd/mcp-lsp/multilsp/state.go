package multilsp

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// bootstrapInFlightTTL 定义正在启动的条目最长存活时间，超时后视为僵尸重置。
const bootstrapInFlightTTL = 30 * time.Second

// bootstrapStatus 表示文档的启动状态。
type bootstrapStatus string

const (
	bootstrapPending       bootstrapStatus = "pending"
	bootstrapBootstrapping bootstrapStatus = "bootstrapping"
	bootstrapReady         bootstrapStatus = "ready"
	bootstrapStale         bootstrapStatus = "stale"
	bootstrapError         bootstrapStatus = "error"
)

// bootstrapAction 表示 prepare 返回的决策类型。
type bootstrapAction uint8

const (
	bootstrapActionSkip bootstrapAction = iota
	bootstrapActionWait
	bootstrapActionRun
)

// bootstrapDecision 保存 prepare 的完整决策，包含后续需要等待的句柄。
type bootstrapDecision struct {
	action   bootstrapAction
	previous bootstrapStatus
	wait     *bootstrapWait
}

// bootstrapResult 保存启动完成后的错误结果，通过 wait channel 传递。
type bootstrapResult struct {
	err error
}

// bootstrapWait 提供等待某次启动完成的同步原语。
type bootstrapWait struct {
	done   chan struct{}
	result bootstrapResult
}

// bootstrapKey 唯一标识一个 workspace + URI 组合的启动状态记录。
type bootstrapKey struct {
	workspace string
	uri       string
}

// bootstrapEntry 保存单个文档的启动状态及相关元数据。
type bootstrapEntry struct {
	status      bootstrapStatus
	fingerprint string
	version     int
	err         error
	updatedAt   time.Time
	wait        *bootstrapWait
}

// bootstrapStateStore 管理所有文档的启动状态，并发访问通过 mu 保护。
type bootstrapStateStore struct {
	mu      sync.Mutex
	entries map[bootstrapKey]*bootstrapEntry
}

// newBootstrapStateStore 创建空的启动状态存储。
func newBootstrapStateStore() *bootstrapStateStore {
	return &bootstrapStateStore{entries: map[bootstrapKey]*bootstrapEntry{}}
}

// restore 将已知 URI 设置为 pending 状态，用于工作区恢复时重新触发启动。
func (s *bootstrapStateStore) restore(workspace string, uris []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, uri := range uris {
		entry := s.entryLocked(bootstrapKey{workspace: workspace, uri: uri})
		if entry.status == bootstrapReady || entry.status == bootstrapStale || entry.status == bootstrapBootstrapping {
			continue
		}
		entry.status = bootstrapPending
		entry.updatedAt = now
	}
}

// reset 强制将 URI 重置为 pending，忽略正在进行中的条目。
func (s *bootstrapStateStore) reset(workspace string, uris []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now()
	for _, uri := range uris {
		entry := s.entryLocked(bootstrapKey{workspace: workspace, uri: uri})
		if entry.status == bootstrapBootstrapping {
			continue
		}
		entry.status = bootstrapPending
		entry.fingerprint = ""
		entry.version = 0
		entry.err = nil
		entry.updatedAt = now
	}
}

// prepare 准备LSP。
func (s *bootstrapStateStore) prepare(workspace, uri, fingerprint string) bootstrapDecision {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := bootstrapKey{workspace: workspace, uri: uri}
	entry := s.entryLocked(key)
	previous := entry.status

	switch {
	case entry.status == bootstrapBootstrapping && entry.wait != nil:
		if time.Since(entry.updatedAt) <= bootstrapInFlightTTL {
			return bootstrapDecision{action: bootstrapActionWait, previous: previous, wait: entry.wait}
		}
		entry.finishWaitLocked(fmt.Errorf("stale bootstrap for %s in %s", uri, workspace))
		entry.wait = nil
	case entry.status == bootstrapReady && entry.fingerprint == fingerprint:
		return bootstrapDecision{action: bootstrapActionSkip, previous: previous}
	case entry.status == bootstrapReady && entry.fingerprint != fingerprint:
		entry.status = bootstrapStale
		previous = bootstrapStale
	}

	entry.status = bootstrapBootstrapping
	entry.err = nil
	entry.updatedAt = time.Now()
	entry.wait = newBootstrapWait()
	return bootstrapDecision{action: bootstrapActionRun, previous: previous, wait: entry.wait}
}

// complete 标记文档启动成功，记录指纹和版本。
func (s *bootstrapStateStore) complete(workspace, uri, fingerprint string, version int) {
	s.finish(workspace, uri, func(entry *bootstrapEntry) {
		entry.status = bootstrapReady
		entry.fingerprint = fingerprint
		entry.version = version
		entry.err = nil
		entry.updatedAt = time.Now()
	})
}

// fail 标记文档启动失败并记录错误。
func (s *bootstrapStateStore) fail(workspace, uri string, err error) {
	s.finish(workspace, uri, func(entry *bootstrapEntry) {
		entry.status = bootstrapError
		entry.err = err
		entry.updatedAt = time.Now()
	})
}

// waitFor 阻塞直到指定 wait 完成或 ctx 取消。
func (s *bootstrapStateStore) waitFor(ctx context.Context, workspace, uri string, wait *bootstrapWait) error {
	if wait == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-wait.done:
		return wait.result.err
	}
}

// status 返回指定 URI 当前的启动状态。
func (s *bootstrapStateStore) status(workspace, uri string) bootstrapStatus {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry := s.entries[bootstrapKey{workspace: workspace, uri: uri}]
	if entry == nil {
		return bootstrapPending
	}
	return entry.status
}

// finish 应用 apply 函数更新条目并通知等待方。
func (s *bootstrapStateStore) finish(workspace, uri string, apply func(*bootstrapEntry)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := bootstrapKey{workspace: workspace, uri: uri}
	entry := s.entryLocked(key)
	apply(entry)
	if entry.wait != nil {
		entry.finishWaitLocked(entry.err)
		entry.wait = nil
	}
}

// entryLocked 返回 key 对应的条目，不存在时创建初始 pending 条目。
func (s *bootstrapStateStore) entryLocked(key bootstrapKey) *bootstrapEntry {
	if entry := s.entries[key]; entry != nil {
		return entry
	}
	entry := &bootstrapEntry{status: bootstrapPending}
	s.entries[key] = entry
	return entry
}

// newBootstrapWait 创建新的等待句柄。
func newBootstrapWait() *bootstrapWait {
	return &bootstrapWait{done: make(chan struct{})}
}

// finishWaitLocked 关闭 wait channel 并写入结果，调用方必须持有锁。
func (e *bootstrapEntry) finishWaitLocked(err error) {
	if e.wait == nil {
		return
	}
	e.wait.result = bootstrapResult{err: err}
	close(e.wait.done)
}
