package rpc

import (
	"context"
	"log/slog"
	"time"

	"github.com/creachadair/jrpc2"
)

var approvalCleanupInterval = time.Minute

func (m *ApprovalManager) Cleanup(timeout time.Duration) {
	if timeout <= 0 {
		return
	}
	cutoff := time.Now().Add(-timeout)
	for _, pending := range m.snapshotPending() {
		if pending.createdAt.After(cutoff) {
			continue
		}
		m.failPending(pending, ErrApprovalTimeout("approval timed out"))
	}
}

func startApprovalCleanupLoop(ctx context.Context, approvals *ApprovalManager, interval, timeout time.Duration, logger *slog.Logger) {
	if approvals == nil || interval <= 0 || timeout <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			before := len(approvals.PendingSnapshot())
			approvals.Cleanup(timeout)
			if logger != nil {
				if after := len(approvals.PendingSnapshot()); after < before {
					logger.Warn("rpc: cleaned expired pending approvals", "removed", before-after, "timeout", timeout.String())
				}
			}
		}
	}
}

func (m *ApprovalManager) RestorePending(ctx context.Context, bridge *PushBridge, server *jrpc2.Server) error {
	var firstErr error
	for _, pending := range m.snapshotPending() {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if err := m.ensureDispatch(bridge, server, pending); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (m *ApprovalManager) PendingSnapshot() []ApprovalRequest {
	pending := m.snapshotPending()
	out := make([]ApprovalRequest, 0, len(pending))
	for _, item := range pending {
		out = append(out, cloneApprovalRequest(item.request, item.requestID))
	}
	return out
}
