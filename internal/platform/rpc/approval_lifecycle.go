package rpc

import (
	"context"
	"time"

	"github.com/creachadair/jrpc2"
)

const defaultApprovalCleanupInterval = time.Minute

// Cleanup 处理cleanup。
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

// P22 P1b Finding 4: startApprovalCleanupLoop was deleted. The cleanup ticker
// is owned by ApprovalCleanupRunner (approval_cleanup_runner.go) and joined
// via the root `group:"runners"` aggregation.

// RestorePending 处理restore待处理。
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

// PendingSnapshot 处理待处理快照。
func (m *ApprovalManager) PendingSnapshot() []ApprovalRequest {
	pending := m.snapshotPending()
	out := make([]ApprovalRequest, 0, len(pending))
	for _, item := range pending {
		out = append(out, cloneApprovalRequest(item.request, item.requestID))
	}
	return out
}
