package rpc

import (
	"context"
	"time"

	"github.com/creachadair/jrpc2"
)

const defaultApprovalCleanupInterval = time.Minute

// Cleanup 把超过 timeout 的 pending 审批标记为超时失败。
// lifecycleMu 防止定时清理、启动恢复和停止清理同时操作同一批 pending。
func (m *ApprovalManager) Cleanup(timeout time.Duration) {
	if m == nil || timeout <= 0 {
		return
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	cutoff := time.Now().Add(-timeout)
	for _, pending := range m.snapshotPending() {
		if pending.createdAt.After(cutoff) {
			continue
		}
		m.failPending(pending, ErrApprovalTimeout("approval timed out"))
	}
}

// 运行边界：审批清理 ticker 由 ApprovalCleanupRunner 托管到根 runners 聚合，
// 本文件只保留恢复、快照和超时清理动作。

// RestorePending 在启动或 UI 重连时重新派发尚未完成的 pending 审批。
// 成功重新派发的请求会刷新 TTL，避免刚恢复就被 cleanup 误判超时。
func (m *ApprovalManager) RestorePending(ctx context.Context, bridge *PushBridge, server *jrpc2.Server) error {
	if m == nil {
		return nil
	}
	m.lifecycleMu.Lock()
	defer m.lifecycleMu.Unlock()
	var firstErr error
	for _, pending := range m.snapshotPending() {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		started, err := m.ensureDispatch(bridge, server, pending)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if started {
			m.refreshPendingTTL(pending)
		}
	}
	return firstErr
}

// refreshPendingTTL 在持锁状态下刷新 pending 创建时间。
func (m *ApprovalManager) refreshPendingTTL(pending *pendingApproval) {
	if pending == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pending[pending.key] == pending {
		pending.createdAt = time.Now()
	}
}

// PendingSnapshot 返回当前 pending 审批的安全副本，供生命周期和诊断读取。
func (m *ApprovalManager) PendingSnapshot() []ApprovalRequest {
	pending := m.snapshotPending()
	out := make([]ApprovalRequest, 0, len(pending))
	for _, item := range pending {
		out = append(out, cloneApprovalRequest(item.request, item.requestID))
	}
	return out
}
