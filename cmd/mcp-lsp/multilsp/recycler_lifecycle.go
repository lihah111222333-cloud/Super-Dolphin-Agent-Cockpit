package multilsp

import (
	"errors"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-lsp/internal/hiddenexec"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

// recyclerCleanupErrorFields 将进程树清理失败归类为非敏感结构化字段。
func recyclerCleanupErrorFields(cleanupErr error) []any {
	return []any{
		"cleanup_pending", errors.Is(cleanupErr, hiddenexec.ErrProcessTreeCleanupPending),
		"owner_missing", errors.Is(cleanupErr, hiddenexec.ErrProcessTreeOwnerMissing),
		"members_remaining", errors.Is(cleanupErr, hiddenexec.ErrProcessTreeRemaining),
		"cleanup_error_class", recyclerCleanupErrorClass(cleanupErr),
	}
}

// recyclerCleanupErrorClass 根据可判定的进程树哨兵返回清理失败类别。
func recyclerCleanupErrorClass(cleanupErr error) string {
	switch {
	case errors.Is(cleanupErr, hiddenexec.ErrProcessTreeRemaining):
		return "members_remaining"
	case errors.Is(cleanupErr, hiddenexec.ErrProcessTreeOwnerMissing):
		return "owner_missing"
	case errors.Is(cleanupErr, hiddenexec.ErrProcessTreeCleanupPending):
		return "cleanup_pending"
	default:
		return "shutdown_or_close"
	}
}

// recyclerCleanupLogFields 组合脱敏工作区标识、错误摘要和进程树清理类别。
func recyclerCleanupLogFields(workspace workspaceClient, cleanupErr error) []any {
	args := platformshared.SafePathLogFields("workspace_key", workspace.key)
	args = append(args, platformshared.SafePayloadLogFields("cleanup_error", cleanupErr.Error())...)
	return append(args, recyclerCleanupErrorFields(cleanupErr)...)
}

// retryCleanupPendingWorkspace 重试 Close 失败的唯一 cleanup owner；它不重新进入 idle 倒计时。
func (r *poolRecycler) retryCleanupPendingWorkspace(mgr *manager, scope ResolvedLSPToolScope, snapshot workspaceClient) {
	if mgr == nil || snapshot.client == nil {
		return
	}
	mgr.ensureMu.Lock()
	defer mgr.ensureMu.Unlock()
	mgr.mu.Lock()
	current := mgr.workspaces[snapshot.key]
	if !sameWorkspaceGeneration(current, &snapshot) || current.state != workspaceStateCleanupPending {
		mgr.mu.Unlock()
		return
	}
	current.state = workspaceStateClosing
	delete(mgr.workspaces, snapshot.key)
	mgr.mu.Unlock()
	shutdownErr, closeErr := shutdownWorkspaceClient(snapshot.client)
	if closeErr != nil {
		restoreCleanupPendingWorkspace(mgr, snapshot)
	} else {
		mgr.AdvanceDiagnosticGeneration()
	}
	if mgr.logger != nil {
		if cleanupErr := errors.Join(shutdownErr, closeErr); cleanupErr != nil {
			args := recyclerWorkspaceLogArgs(scope, snapshot, "reason", "cleanup_pending_retry")
			args = append(args, platformshared.SafePayloadLogFields("cleanup_error", cleanupErr.Error())...)
			mgr.logger.Warn("LSP cleanup pending retry failed", args...)
		}
	}
}

func sameWorkspaceGeneration(current *workspaceClient, snapshot *workspaceClient) bool {
	return current != nil && snapshot != nil && current.client == snapshot.client && current.generation == snapshot.generation
}

func restoreCleanupPendingWorkspace(mgr *manager, snapshot workspaceClient) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if mgr.workspaces[snapshot.key] == nil {
		snapshot.state = workspaceStateCleanupPending
		snapshot.idleSince = time.Time{}
		mgr.workspaces[snapshot.key] = &snapshot
	}
}

// managerIdleEligible 在 manager 锁下取得当前 key/client/generation 的精确资格。
func (r *poolRecycler) managerIdleEligible(mgr *manager, snapshot workspaceClient, now time.Time) bool {
	if mgr == nil {
		return false
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	current := mgr.workspaces[snapshot.key]
	return sameWorkspaceGeneration(current, &snapshot) && idleEligible(current, now, mgr.idleTimeout)
}

func (r *poolRecycler) managerIdleCandidate(mgr *manager, snapshot workspaceClient, now time.Time, timeout time.Duration) (workspaceClient, bool) {
	if mgr == nil {
		return workspaceClient{}, false
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	current := mgr.workspaces[snapshot.key]
	if !sameWorkspaceGeneration(current, &snapshot) || !idleEligible(current, now, timeout) {
		return workspaceClient{}, false
	}
	current.state = workspaceStateRecheck
	return *current, true
}

// detachWorkspaceClientGeneration 在 manager 锁内复核代际、状态、租约与完整 idle window。
func detachWorkspaceClientGeneration(mgr *manager, key string, expected Client, generation uint64) *workspaceClient {
	if mgr == nil || generation == 0 {
		return nil
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	workspace := mgr.workspaces[key]
	if !matchesDetachWorkspace(workspace, expected, generation) {
		return nil
	}
	if !detachWorkspaceEligible(mgr, workspace) {
		return nil
	}
	workspace.state = workspaceStateClosing
	delete(mgr.workspaces, key)
	return workspace
}

func matchesDetachWorkspace(workspace *workspaceClient, expected Client, generation uint64) bool {
	if workspace == nil || workspace.client == nil {
		return false
	}
	if expected != nil && workspace.client != expected {
		return false
	}
	return workspace.generation == generation
}

func detachWorkspaceEligible(mgr *manager, workspace *workspaceClient) bool {
	if workspace.state == workspaceStateCleanupPending {
		return true
	}
	return idleEligible(workspace, mgr.managerNow(), mgr.idleTimeout)
}
