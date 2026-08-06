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
	if cleanupErr != nil {
		args = append(args, platformshared.SafePayloadLogFields("cleanup_error", cleanupErr.Error())...)
		args = append(args, recyclerCleanupErrorFields(cleanupErr)...)
	}
	return args
}

// logIdleShutdownProtocolDegraded 区分协议退出失败和进程树回收失败，避免已 Close 的 client 被误报为 cleanup failed。
func logIdleShutdownProtocolDegraded(
	mgr *manager,
	scope ResolvedLSPToolScope,
	workspace workspaceClient,
	now time.Time,
	shutdownErr error,
) {
	if shutdownErr == nil || mgr == nil || mgr.logger == nil {
		return
	}
	args := recyclerWorkspaceLogArgs(scope, workspace,
		"generation", workspace.generation,
		"active_leases", workspace.activeLeases,
		"state", workspace.state,
		"idle_since", recyclerLifecycleTime(workspace.idleSince),
		"last_activity", recyclerLifecycleTime(workspace.lastActivity),
		"idle_duration", idleDurationSince(workspace.idleSince, now),
		"idle_timeout", mgr.idleTimeout.String(),
		"action", "shutdown",
		"action_result", "degraded",
		"reason", "idle_timeout",
	)
	args = append(args, platformshared.SafePayloadLogFields("shutdown_error", shutdownErr.Error())...)
	mgr.logger.Warn("LSP idle shutdown protocol degraded", args...)
}

func recyclerScopeLogArgs(scope ResolvedLSPToolScope) []any {
	args := platformshared.SafePathLogFields("manager_key", scope.ManagerKey)
	return append(args, platformshared.SafePathLogFields("scope_key", scope.ScopeKey)...)
}

func recyclerManagerLogArgs(scope ResolvedLSPToolScope, mgr *manager, workspaceCount int) []any {
	args := recyclerScopeLogArgs(scope)
	args = append(args, "workspace_count", workspaceCount)
	if mgr != nil {
		args = append(args, platformshared.SafePayloadLogFields("manager_instance", mgr.instanceID)...)
	}
	return args
}

// recyclerWorkspaceCleanupLogArgs 固化 workspace 级失败日志的生命周期快照，避免调用方遗漏租约和空闲窗口证据。
func recyclerWorkspaceCleanupLogArgs(
	mgr *manager,
	scope ResolvedLSPToolScope,
	workspace workspaceClient,
	now time.Time,
	action string,
	reason string,
	cleanupErr error,
) []any {
	idleDuration := ""
	if !workspace.idleSince.IsZero() && !now.IsZero() {
		idleDuration = now.Sub(workspace.idleSince).String()
	}
	idleTimeout := ""
	if mgr != nil && mgr.idleTimeout > 0 {
		idleTimeout = mgr.idleTimeout.String()
	}
	args := recyclerWorkspaceLogArgs(scope, workspace,
		"generation", workspace.generation,
		"active_leases", workspace.activeLeases,
		"state", workspace.state,
		"idle_since", recyclerLifecycleTime(workspace.idleSince),
		"last_activity", recyclerLifecycleTime(workspace.lastActivity),
		"idle_duration", idleDuration,
		"idle_timeout", idleTimeout,
		"action", action,
		"action_result", "failed",
		"reason", reason,
	)
	return append(args, recyclerCleanupLogFields(workspace, cleanupErr)...)
}

func recyclerManagerCleanupLogArgs(
	scope ResolvedLSPToolScope,
	mgr *manager,
	workspaceCount int,
	cleanupErr error,
	extra ...any,
) []any {
	args := recyclerManagerLogArgs(scope, mgr, workspaceCount)
	args = append(args, extra...)
	if cleanupErr != nil {
		args = append(args, platformshared.SafePayloadLogFields("cleanup_error", cleanupErr.Error())...)
		args = append(args, recyclerCleanupErrorFields(cleanupErr)...)
	}
	return args
}

func recyclerLifecycleTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func idleDurationSince(start, now time.Time) string {
	if start.IsZero() || now.IsZero() {
		return ""
	}
	return now.Sub(start).String()
}

// logRecyclerCleanupPending 在关闭所有权尚未收敛时记录 pending 而非猜测已释放，供后续 retry 关联。
func logRecyclerCleanupPending(
	mgr *manager,
	scope ResolvedLSPToolScope,
	workspaces []workspaceClient,
	now time.Time,
	action string,
	reason string,
	message string,
) {
	if mgr == nil || mgr.logger == nil {
		return
	}
	if len(workspaces) == 0 {
		args := recyclerManagerLogArgs(scope, mgr, 0)
		args = append(args,
			"action", action,
			"action_result", "pending",
			"reason", reason,
			"cleanup_incomplete", true,
		)
		mgr.logger.Warn(message, args...)
		return
	}
	for _, workspace := range workspaces {
		idleTimeout := ""
		if mgr.idleTimeout > 0 {
			idleTimeout = mgr.idleTimeout.String()
		}
		args := recyclerWorkspaceLogArgs(scope, workspace,
			"generation", workspace.generation,
			"active_leases", workspace.activeLeases,
			"state", workspace.state,
			"idle_since", recyclerLifecycleTime(workspace.idleSince),
			"last_activity", recyclerLifecycleTime(workspace.lastActivity),
			"idle_duration", idleDurationSince(workspace.idleSince, now),
			"idle_timeout", idleTimeout,
			"action", action,
			"action_result", "pending",
			"reason", reason,
			"cleanup_incomplete", true,
		)
		mgr.logger.Warn(message, args...)
	}
}

// logRecyclerCleanupFailure 为已知清理错误保留 workspace 级脱敏证据；没有 workspace 时退化为 manager 级事件。
func logRecyclerCleanupFailure(
	mgr *manager,
	scope ResolvedLSPToolScope,
	workspaces []workspaceClient,
	now time.Time,
	action string,
	reason string,
	cleanupErr error,
	message string,
) {
	if mgr == nil || mgr.logger == nil || cleanupErr == nil {
		return
	}
	if len(workspaces) == 0 {
		args := recyclerManagerCleanupLogArgs(scope, mgr, 0, cleanupErr,
			"action", action,
			"action_result", "failed",
			"reason", reason,
		)
		mgr.logger.Warn(message, args...)
		return
	}
	for _, workspace := range workspaces {
		args := recyclerWorkspaceCleanupLogArgs(mgr, scope, workspace, now, action, reason, cleanupErr)
		mgr.logger.Warn(message, args...)
	}
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
			args := recyclerWorkspaceCleanupLogArgs(mgr, scope, snapshot, r.recyclerNow(), "retry", "cleanup_pending_retry", cleanupErr)
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
