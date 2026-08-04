package multilsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const (
	defaultRecyclerInterval    = 30 * time.Second
	defaultGoRSSLimitBytes     = 512 * 1024 * 1024
	defaultGoplsHeapLimitBytes = 3584 * 1024 * 1024
	lspRSSLimitEnv             = "AGENT_LSP_RSS_LIMIT_MB"
	lspGoRSSLimitEnv           = "AGENT_LSP_GO_RSS_LIMIT_MB"
	lspGoplsHeapLimitEnv       = "AGENT_LSP_GOPLS_HEAP_LIMIT_MB"

	recyclerProbeDegradedThreshold = 3
)

type recyclerRSSProbe func(Client) (uint64, int, error)

type recyclerMemoryDecision struct {
	processExceeded bool
	processLimit    uint64
	activeLeases    int
	cohort          resourceCohortDecision
	cohortErr       error
}

type recyclerHealthSnapshot struct {
	ProbeFailuresTotal       int64
	ConsecutiveProbeFailures int64
	LastProbeError           string
	LastProbeAt              time.Time
	Degraded                 bool
}

// poolRecycler 周期性扫描池内 LSP 子进程的 RSS 和 workspace 闲置时间。
// 它只实现 platformrunner.Runner，不自行启动 goroutine；启动和停止都由根 runner 聚合器负责。
type poolRecycler struct {
	pool     *ManagerPool
	interval time.Duration
	rssProbe recyclerRSSProbe
	now      func() time.Time

	mu               sync.Mutex
	lastActive       map[int]time.Time
	health           recyclerHealthSnapshot
	scanCount        uint64
	lastScanAt       time.Time
	lastScanDuration time.Duration
	lastScanLag      time.Duration
}

// 编译期确认 poolRecycler 满足根 runner 聚合器消费的 Runner 合约。
var _ platformrunner.Runner = (*poolRecycler)(nil)

func newPoolRecycler(pool *ManagerPool) *poolRecycler {
	return &poolRecycler{
		pool:       pool,
		interval:   defaultRecyclerInterval,
		rssProbe:   clientRSSBytes,
		now:        time.Now,
		lastActive: map[int]time.Time{},
	}
}

func (r *poolRecycler) recyclerNow() time.Time {
	if r != nil && r.now != nil {
		return r.now()
	}
	return time.Now()
}

func (r *poolRecycler) activeLeases(workspace workspaceClient) int {
	return workspace.activeLeases
}

// TouchShard 记录指定 shard 的最近访问时间。
// recycler 用它避免刚被请求命中的 shard 立刻进入 RSS 扫描，降低活跃请求被回收打断的概率。
func (r *poolRecycler) TouchShard(index int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.lastActive[index] = time.Now()
	r.mu.Unlock()
}

// HealthSnapshot 返回 RSS 探测链路的线程安全健康快照。
func (r *poolRecycler) HealthSnapshot() recyclerHealthSnapshot {
	if r == nil {
		return recyclerHealthSnapshot{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.health
}

func (r *poolRecycler) recordProbeFailure(summary string, consecutive int) recyclerHealthSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.health.ProbeFailuresTotal++
	r.health.ConsecutiveProbeFailures = int64(consecutive)
	r.health.LastProbeError = summary
	r.health.LastProbeAt = time.Now()
	r.health.Degraded = consecutive >= recyclerProbeDegradedThreshold
	return r.health
}

func (r *poolRecycler) recordProbeSuccess() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.health.ConsecutiveProbeFailures = 0
	r.health.LastProbeError = ""
	r.health.LastProbeAt = time.Now()
	r.health.Degraded = false
}

// Run 按固定间隔执行回收检查，直到 ctx 取消。
// nil receiver 会等待 ctx 后返回，便于根 runner 聚合器统一调度而不需要额外判空分支。
func (r *poolRecycler) Run(ctx context.Context) error {
	if r == nil {
		<-ctx.Done()
		return nil
	}
	ticker := time.NewTicker(r.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			r.check()
		}
	}
}

func (r *poolRecycler) check() {
	if r == nil || r.pool == nil {
		return
	}
	r.pool.drainPendingReleases()
	for _, snapshot := range r.pool.snapshotManagers() {
		r.retryProvisionalCleanups(snapshot.manager)
		r.checkIdleWorkspaces(snapshot.manager, snapshot.resolvedScope)
		r.checkManager(snapshot.index, snapshot.manager, snapshot.resolvedScope)
	}
	r.evictIdleEmptyClones()
}

func (r *poolRecycler) retryProvisionalCleanups(mgr *manager) {
	if mgr == nil {
		return
	}
	mgr.ensureMu.Lock()
	defer mgr.ensureMu.Unlock()
	for _, key := range mgr.provisionalCleanupKeys() {
		if err := mgr.retryProvisionalClientCleanups(key); err != nil && mgr.logger != nil {
			args := []any{"workspace_hash", provisionalWorkspaceHash(key)}
			args = append(args, platformshared.SafePayloadLogFields("cleanup_error", err.Error())...)
			mgr.logger.Warn("LSP provisional cleanup retry pending", args...)
		}
	}
}

// evictIdleEmptyClones 摘除过期空 clone，并把关闭失败交回 pool receipt 等待重试。
func (r *poolRecycler) evictIdleEmptyClones() {
	cutoff := r.recyclerNow()
	if r.pool == nil || r.pool.primary == nil || r.pool.primary.idleTimeout <= 0 {
		return
	}
	cutoff = cutoff.Add(-r.pool.primary.idleTimeout)
	releases := r.pool.detachIdleEmptyClones(cutoff)
	if err := r.pool.closeDetachedPoolManagers(releases, "close idle LSP manager"); err != nil {
		for _, release := range releases {
			if release.manager != nil && release.manager.logger != nil {
				release.manager.logger.Warn("LSP idle clone close failed", "err", err)
			}
		}
	}
}

func (r *poolRecycler) checkManager(_ int, mgr *manager, scope ResolvedLSPToolScope) {
	if mgr == nil || managerIsClosed(mgr) {
		return
	}

	for _, workspace := range snapshotWorkspaceClients(mgr) {
		r.recycleIfNeeded(mgr, scope, workspace)
	}
}

// recycleIfNeeded 在单个 workspace client 超过 RSS 上限时尝试回收。
// 仍有活跃租约时只记录日志不关闭进程，避免正在执行的 LSP 请求被异步切断。
func (r *poolRecycler) recycleIfNeeded(mgr *manager, scope ResolvedLSPToolScope, workspace workspaceClient) {
	rssBytes, pid, ok, probeDegraded := r.probeWorkspace(mgr, scope, workspace)
	if !ok {
		r.failClosedAfterProbeDegradation(mgr, workspace, probeDegraded)
		return
	}
	decision := r.memoryDecision(workspace, rssBytes, pid)
	logResourceCohortHealth(mgr, scope, workspace, pid, rssBytes, decision.cohort, decision.cohortErr)
	if !decision.exceeded() {
		return
	}
	reason, limit := decision.reasonAndLimit()
	logRSSWindowExceeded(mgr, scope, workspace, pid, rssBytes, limit, decision.activeLeases, reason, decision.cohort)
	if decision.activeLeases > 0 {
		logRSSRecycleActiveLease(mgr, scope, workspace, pid, rssBytes, limit, decision.activeLeases, reason)
		return
	}
	if !r.managerIdleEligible(mgr, workspace, r.recyclerNow()) {
		if mgr.logger != nil {
			mgr.logger.Debug("LSP RSS pressure deferred until idle window", "workspace", workspace.key, "reason", reason)
		}
		return
	}
	recycled, err := executeMemoryRecycle(mgr, scope, workspace, decision.cohort.EvictSelf)
	logMemoryRecycleOutcome(mgr, scope, workspace, pid, rssBytes, limit, reason, decision.cohort, recycled, err)
}

// failClosedAfterProbeDegradation 在连续探测失败后关闭无租约 client，避免预算永久失效。
func (r *poolRecycler) failClosedAfterProbeDegradation(
	mgr *manager,
	workspace workspaceClient,
	probeDegraded bool,
) {
	if !probeDegraded {
		return
	}
	if r.activeLeases(workspace) > 0 || !r.managerIdleEligible(mgr, workspace, r.recyclerNow()) {
		return
	}
	_, _ = shutdownResourceCohortWorkspace(mgr, workspace)
}

// memoryDecision 合并创建期进程预算与跨 owner 总账；策略缺失时保持 fail-closed。
func (r *poolRecycler) memoryDecision(workspace workspaceClient, rssBytes uint64, pid int) recyclerMemoryDecision {
	activeLeases := 0
	activeLeases = r.activeLeases(workspace)
	processLimit := rssLimitBytesForLanguage(workspace.languageID)
	processExceeded := false
	policy, policyErr := resourceProcessPolicyForClient(workspace.client, workspace.languageID)
	cohortDecision := resourceCohortDecision{}
	var cohortErr error
	if policyErr == nil {
		processLimit = policy.rssLimitBytes
		processExceeded = rssBytes > processLimit
		cohortDecision, cohortErr = evaluateResourceCohort(
			workspace.client,
			workspace,
			policy,
			rssBytes,
			pid,
			activeLeases,
			r.recyclerNow(),
		)
	} else {
		// 创建期主次策略丢失或被篡改时 fail-closed；活跃请求租约只延迟关闭，不扩大预算。
		processExceeded = true
	}
	return recyclerMemoryDecision{
		processExceeded: processExceeded,
		processLimit:    processLimit,
		activeLeases:    activeLeases,
		cohort:          cohortDecision,
		cohortErr:       errors.Join(policyErr, cohortErr),
	}
}

func (d recyclerMemoryDecision) exceeded() bool {
	return d.processExceeded || d.cohort.EvictSelf
}

func (d recyclerMemoryDecision) reasonAndLimit() (string, uint64) {
	if d.cohort.EvictSelf {
		return "cohort_rss_limit", d.cohort.HardLimit
	}
	return "process_tree_rss_limit", d.processLimit
}

// logResourceCohortHealth 记录跨 worktree RSS 报告的陈旧或异常状态。
func logResourceCohortHealth(
	mgr *manager,
	scope ResolvedLSPToolScope,
	workspace workspaceClient,
	pid int,
	rssBytes uint64,
	cohort resourceCohortDecision,
	probeErr error,
) {
	if mgr == nil || mgr.logger == nil ||
		(probeErr == nil && cohort.StaleMembers == 0 && cohort.UnhealthyMembers == 0) {
		return
	}
	args := recyclerWorkspaceLogArgs(scope, workspace,
		"pid", pid,
		"rss_bytes", rssBytes,
		"reason", "resource_cohort_probe",
		"stale_members", cohort.StaleMembers,
		"unhealthy_members", cohort.UnhealthyMembers,
	)
	if probeErr != nil {
		args = append(args, platformshared.SafePayloadLogFields("probe_error", probeErr.Error())...)
	}
	mgr.logger.Warn("LSP resource cohort degraded", args...)
}

func logRSSRecycleActiveLease(
	mgr *manager,
	scope ResolvedLSPToolScope,
	workspace workspaceClient,
	pid int,
	rssBytes, limit uint64,
	activeLeases int,
	reason string,
) {
	if mgr == nil || mgr.logger == nil {
		return
	}
	args := recyclerWorkspaceLogArgs(scope, workspace,
		"pid", pid,
		"rss_bytes", rssBytes,
		"rss_limit_bytes", limit,
		"active_leases", activeLeases,
		"reason", reason,
	)
	mgr.logger.Info("LSP recycle skipped: active leases", args...)
}

func executeMemoryRecycle(mgr *manager, scope ResolvedLSPToolScope, workspace workspaceClient, cohortEviction bool) (bool, error) {
	if cohortEviction {
		return shutdownResourceCohortWorkspace(mgr, workspace)
	}
	return recycleWorkspaceClient(mgr, scope, workspace)
}

// logMemoryRecycleOutcome 统一记录本地重建或 cohort 懒回收的错误与最终结果。
func logMemoryRecycleOutcome(
	mgr *manager,
	scope ResolvedLSPToolScope,
	workspace workspaceClient,
	pid int,
	rssBytes, limit uint64,
	reason string,
	cohort resourceCohortDecision,
	recycled bool,
	recycleErr error,
) {
	if mgr == nil || mgr.logger == nil {
		return
	}
	if recycleErr != nil {
		args := recyclerWorkspaceLogArgs(scope, workspace,
			"pid", pid,
			"rss_bytes", rssBytes,
			"rss_limit_bytes", limit,
			"reason", reason,
		)
		args = append(args, platformshared.SafePayloadLogFields("recycle_error", recycleErr.Error())...)
		mgr.logger.Warn("LSP recycle failed", args...)
	}
	if recycled {
		args := recyclerWorkspaceLogArgs(scope, workspace,
			"pid", pid,
			"rss_bytes", rssBytes,
			"rss_limit_bytes", limit,
			"cohort_rss_bytes", cohort.AggregateRSS,
			"reason", reason,
		)
		mgr.logger.Warn("recycled LSP process", args...)
	}
}

// checkIdleWorkspaces 扫描生命周期 SSOT；候选必须经过扫描快照与 manager 锁内二次复核。
// scanner 不能把 lastActivity 或容量/RSS 事件当作第二个销毁资格来源。
func (r *poolRecycler) checkIdleWorkspaces(mgr *manager, scope ResolvedLSPToolScope) {
	if mgr == nil || managerIsClosed(mgr) {
		return
	}
	scanStarted := r.recyclerNow()
	for _, workspace := range snapshotWorkspaceClients(mgr) {
		if workspace.state == workspaceStateCleanupPending {
			r.retryCleanupPendingWorkspace(mgr, scope, workspace)
			continue
		}
		timeout := mgr.idleTimeout
		if timeout <= 0 {
			continue
		}
		if !idleEligible(&workspace, scanStarted, timeout) {
			continue
		}
		candidate, ok := r.managerIdleCandidate(mgr, workspace, scanStarted, timeout)
		if !ok {
			continue
		}
		idleDuration := scanStarted.Sub(candidate.idleSince)
		activeLeases := candidate.activeLeases
		r.logIdleWindowExceeded(mgr, scope, candidate, idleDuration, timeout, activeLeases)
		r.shutdownIdleWorkspace(mgr, scope, candidate)
	}
	r.recordScan(scanStarted)
}

func (r *poolRecycler) recordScan(started time.Time) {
	if r == nil {
		return
	}
	finished := r.recyclerNow()
	r.mu.Lock()
	r.scanCount++
	r.lastScanAt = finished
	r.lastScanDuration = finished.Sub(started)
	if !r.lastScanAt.IsZero() && !started.IsZero() {
		r.lastScanLag = finished.Sub(started)
	}
	r.mu.Unlock()
}

// shutdownIdleWorkspace 二次确认空闲状态后摘除 client，并完整关闭其进程树。
func (r *poolRecycler) shutdownIdleWorkspace(
	mgr *manager,
	scope ResolvedLSPToolScope,
	workspace workspaceClient,
) {
	detached, shutdownErr, closeErr := detachAndShutdownWorkspaceClient(mgr, workspace)
	if detached == nil || detached.client == nil {
		return
	}
	mgr.AdvanceDiagnosticGeneration()
	if cleanupErr := errors.Join(shutdownErr, closeErr); cleanupErr != nil && mgr.logger != nil {
		args := recyclerWorkspaceLogArgs(scope, workspace, "reason", "idle_timeout")
		args = append(args, platformshared.SafePayloadLogFields("cleanup_error", cleanupErr.Error())...)
		mgr.logger.Warn("LSP idle shutdown cleanup failed", args...)
	}

	if mgr.logger != nil {
		args := recyclerWorkspaceLogArgs(scope, workspace,
			"idle_duration", time.Since(workspace.lastActivity).String(),
			"reason", "idle_timeout",
		)
		mgr.logger.Info("LSP idle shutdown", args...)
	}
}

func (r *poolRecycler) logIdleWindowExceeded(
	mgr *manager,
	scope ResolvedLSPToolScope,
	workspace workspaceClient,
	idleDuration time.Duration,
	workspaceIdleTimeout time.Duration,
	activeLeases int,
) {
	if mgr == nil || mgr.logger == nil {
		return
	}
	action := "shutdown"
	if activeLeases > 0 {
		action = "skip_active_leases"
	}
	args := recyclerWorkspaceLogArgs(scope, workspace,
		"idle_duration", idleDuration.String(),
		"idle_timeout", workspaceIdleTimeout.String(),
		"active_leases", activeLeases,
		"action", action,
		"reason", "idle_timeout",
	)
	args = r.appendRecyclerRSSProbeArgs(args, mgr, scope, workspace)
	mgr.logger.Debug("LSP recycler idle window exceeded", args...)
}

// logRSSWindowExceeded 记录单进程树或跨 worktree cohort 的超限窗口。
func logRSSWindowExceeded(
	mgr *manager,
	scope ResolvedLSPToolScope,
	workspace workspaceClient,
	pid int,
	rssBytes, limit uint64,
	activeLeases int,
	reason string,
	cohort resourceCohortDecision,
) {
	if mgr == nil || mgr.logger == nil {
		return
	}
	action := "recycle"
	if activeLeases > 0 {
		action = "skip_active_leases"
	}
	args := recyclerWorkspaceLogArgs(scope, workspace,
		"pid", pid,
		"rss_bytes", rssBytes,
		"rss_limit_bytes", limit,
		"active_leases", activeLeases,
		"action", action,
		"reason", reason,
	)
	if cohort.Enabled {
		args = append(args,
			"cohort_rss_bytes", cohort.AggregateRSS,
			"cohort_hard_limit_bytes", cohort.HardLimit,
			"cohort_soft_limit_bytes", cohort.SoftLimit,
			"cohort_stale_members", cohort.StaleMembers,
			"cohort_unhealthy_members", cohort.UnhealthyMembers,
		)
	}
	mgr.logger.Debug("LSP recycler RSS threshold exceeded", args...)
}

func recyclerWorkspaceLogArgs(scope ResolvedLSPToolScope, workspace workspaceClient, extra ...any) []any {
	args := []any{
		"manager_key", scope.ManagerKey,
		"scope_key", scope.ScopeKey,
		"language", normalizeLanguageID(workspace.languageID),
	}
	args = append(args, platformshared.SafePathLogFields("workspace", workspace.key)...)
	return append(args, extra...)
}

func (r *poolRecycler) appendRecyclerRSSProbeArgs(args []any, mgr *manager, scope ResolvedLSPToolScope, workspace workspaceClient) []any {
	rssBytes, pid, ok, _ := r.probeWorkspace(mgr, scope, workspace)
	if !ok {
		return append(args, "rss_probe_failed", true)
	}
	return append(args, "pid", pid, "rss_bytes", rssBytes)
}

func (r *poolRecycler) probeWorkspace(
	mgr *manager,
	scope ResolvedLSPToolScope,
	workspace workspaceClient,
) (uint64, int, bool, bool) {
	rssBytes, pid, err := r.clientRSSBytes(workspace.client)
	summary := "rss_probe_error"
	if err == nil && pid <= 0 {
		err = fmt.Errorf("RSS probe returned invalid pid")
		summary = "rss_probe_invalid_pid"
	}
	if err != nil {
		fallback := int(r.HealthSnapshot().ConsecutiveProbeFailures) + 1
		consecutive := incrementWorkspaceProbeFailures(mgr, workspace, fallback)
		health := r.recordProbeFailure(summary, consecutive)
		r.logProbeFailure(mgr, scope, workspace, err, health)
		return 0, 0, false, consecutive >= recyclerProbeDegradedThreshold
	}
	resetWorkspaceProbeFailures(mgr, workspace)
	r.recordProbeSuccess()
	return rssBytes, pid, true, false
}

func incrementWorkspaceProbeFailures(mgr *manager, expected workspaceClient, fallback int) int {
	if mgr == nil {
		return fallback
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	current := mgr.workspaces[expected.key]
	if current == nil || current.client != expected.client {
		return fallback
	}
	current.rssProbeFailures++
	return current.rssProbeFailures
}

func resetWorkspaceProbeFailures(mgr *manager, expected workspaceClient) {
	if mgr == nil {
		return
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	current := mgr.workspaces[expected.key]
	if current != nil && current.client == expected.client {
		current.rssProbeFailures = 0
	}
}

func (r *poolRecycler) logProbeFailure(mgr *manager, scope ResolvedLSPToolScope, workspace workspaceClient, probeErr error, health recyclerHealthSnapshot) {
	if mgr == nil || mgr.logger == nil {
		return
	}
	args := recyclerWorkspaceLogArgs(scope, workspace,
		"probe_failures_total", health.ProbeFailuresTotal,
		"consecutive_probe_failures", health.ConsecutiveProbeFailures,
		"degraded", health.Degraded,
	)
	args = append(args, platformshared.SafePayloadLogFields("probe_error", probeErr.Error())...)
	mgr.logger.Warn("LSP recycler RSS probe failed", args...)
}

func (r *poolRecycler) clientRSSBytes(current Client) (uint64, int, error) {
	if r == nil || r.rssProbe == nil {
		return clientRSSBytes(current)
	}
	return r.rssProbe(current)
}

// recycleWorkspaceClient 从 manager 中摘除目标 workspace client 后重建同一 workspace。
// 摘除阶段会再次确认租约，关闭和重启错误合并返回，调用方据此记录一次完整回收结果。
func recycleWorkspaceClient(mgr *manager, scope ResolvedLSPToolScope, workspace workspaceClient) (bool, error) {
	detached, shutdownErr, closeErr := detachAndShutdownWorkspaceClient(mgr, workspace)
	if detached == nil || detached.client == nil {
		return false, nil
	}
	mgr.AdvanceDiagnosticGeneration()
	if closeErr != nil {
		return false, errors.Join(shutdownErr, closeErr)
	}

	languageID := workspace.languageID
	if languageID == "" {
		languageID = scope.LanguageID
	}
	languageID = normalizeLanguageID(languageID)
	if languageID == "" {
		return true, errors.Join(shutdownErr, closeErr, fmt.Errorf("recycle LSP client: workspace language is empty"))
	}
	cfg := workspaceConfig{
		key:              workspace.key,
		rootPath:         workspace.rootPath,
		rootURI:          workspace.rootURI,
		languageID:       languageID,
		env:              append([]string(nil), workspace.env...),
		workspaceFolders: cloneWorkspaceFolders(workspace.workspaceFolders),
	}
	restoreCtx := recycleRestoreContext(scope, cfg)
	_, ensureErr := mgr.ensureClient(restoreCtx, cfg)
	restoreErr := restoreBootstrappedWorkspace(restoreCtx, mgr, cfg)
	return true, errors.Join(shutdownErr, closeErr, ensureErr, restoreErr)
}

// shutdownResourceCohortWorkspace 在跨 worktree cohort 超限时只关闭当前 owner 的空闲 client。
// 它不立即重建；下一次真实请求会懒启动，从而让总账回到软水位而不跨进程 kill。
func shutdownResourceCohortWorkspace(mgr *manager, workspace workspaceClient) (bool, error) {
	detached, shutdownErr, closeErr := detachAndShutdownWorkspaceClient(mgr, workspace)
	if detached == nil || detached.client == nil {
		return false, nil
	}
	mgr.AdvanceDiagnosticGeneration()
	if closeErr != nil {
		return false, errors.Join(shutdownErr, closeErr)
	}
	return true, errors.Join(shutdownErr, closeErr)
}

// detachAndShutdownWorkspaceClient 串行化摘除与进程关闭；Close 失败时恢复 cleanup owner。
func detachAndShutdownWorkspaceClient(
	mgr *manager,
	workspace workspaceClient,
) (*workspaceClient, error, error) {
	if mgr == nil {
		return nil, nil, nil
	}
	mgr.ensureMu.Lock()
	defer mgr.ensureMu.Unlock()
	if managerIsClosed(mgr) {
		return nil, nil, nil
	}
	detached := detachWorkspaceClientGeneration(mgr, workspace.key, workspace.client, workspace.generation)
	if detached == nil || detached.client == nil {
		return nil, nil, nil
	}
	shutdownErr, closeErr := shutdownWorkspaceClient(detached.client)
	if closeErr != nil {
		restoreDetachedWorkspaceClient(mgr, detached)
	}
	return detached, shutdownErr, closeErr
}

func recycleRestoreContext(scope ResolvedLSPToolScope, cfg workspaceConfig) context.Context {
	ctx := context.Background()
	scope = recycleResolvedScope(scope, cfg)
	ctx = common.WithToolScope(ctx, recycleToolScope(scope))
	return WithResolvedLSPToolScope(ctx, scope)
}

// recycleResolvedScope 为回收后的重启补齐 ResolvedLSPToolScope。
// 优先复用已有 manager key；缺失时从 workspace 配置重建，失败则保留原 scope 以便日志仍可关联。
func recycleResolvedScope(scope ResolvedLSPToolScope, cfg workspaceConfig) ResolvedLSPToolScope {
	if scope.WorkspaceKey != "" || scope.ManagerKey != "" {
		return scope
	}
	if parsed, ok := lspScopeWorkspacePartsFromConfig(cfg); ok {
		if resolved, err := ResolveLSPToolScope(parsed); err == nil {
			return resolved
		}
	}
	resolved, err := ResolveLSPToolScope(LSPToolScope{
		LanguageID:            cfg.languageID,
		WorkspaceRoot:         cfg.rootPath,
		LanguageWorkspaceRoot: cfg.rootPath,
		ProjectRoot:           cfg.rootPath,
		RootKind:              "dir_fallback",
	})
	if err != nil {
		return scope
	}
	return resolved
}

func recycleToolScope(scope ResolvedLSPToolScope) common.ToolScope {
	return common.ToolScope{
		Family:   scope.Family,
		AgentID:  scope.AgentID,
		ThreadID: scope.ThreadID,
		TurnID:   scope.TurnID,
		CallID:   scope.CallID,
		CWD:      scope.CWD,
		WorkspaceRoots: append(
			[]string(nil),
			scope.WorkspaceRoots...,
		),
	}
}

func snapshotWorkspaceClients(mgr *manager) []workspaceClient {
	if mgr == nil {
		return nil
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()

	items := make([]workspaceClient, 0, len(mgr.workspaces))
	for _, workspace := range mgr.workspaces {
		if workspace == nil || workspace.client == nil {
			continue
		}
		items = append(items, *workspace)
	}
	return items
}

func managerIsClosed(mgr *manager) bool {
	if mgr == nil {
		return true
	}
	mgr.mu.RLock()
	defer mgr.mu.RUnlock()
	return mgr.closed
}

func clientRSSBytes(current Client) (uint64, int, error) {
	typed, ok := concreteClient(current)
	if !ok || typed.transport == nil || typed.transport.cmd == nil || typed.transport.cmd.Process == nil {
		return 0, 0, nil
	}
	pid := typed.transport.cmd.Process.Pid
	rss, err := typed.transport.processTreeRSSBytes()
	return rss, pid, err
}

// rssLimitBytesForLanguage 返回指定语言的单进程树紧急回收阈值。
// 非 gopls 默认不早于全局 15GiB 池触发；POSIX gopls forwarder 独立使用轻量阈值。
func rssLimitBytesForLanguage(languageID string) uint64 {
	return rssLimitBytesForLanguageOnOS(languageID, runtime.GOOS)
}

// rssLimitBytesForLanguageOnOS 按语言与平台选择单进程树的紧急回收阈值。
func rssLimitBytesForLanguageOnOS(languageID, goos string) uint64 {
	lang := normalizeLanguageID(languageID)
	if lang == "go" || lang == "gomod" || lang == "gosum" || lang == "gowork" {
		if value, ok := rssLimitBytesFromEnv(lspGoRSSLimitEnv); ok {
			return value
		}
		if goos == "windows" {
			return defaultGoplsCohortHardLimitBytes
		}
		return defaultGoRSSLimitBytes
	}
	if value, ok := rssLimitBytesFromEnv(lspRSSLimitEnv); ok {
		return value
	}
	return defaultCohortHardLimitBytes
}

// goplsHeapLimitBytes 返回共享 gopls daemon 的 Go heap 软上限。
// 该值与轻量 forwarder 的进程树 RSS 阈值分离，并为 4 GiB cohort RSS 回收高水位预留 native/非堆空间。
func goplsHeapLimitBytes() uint64 {
	if value, ok := rssLimitBytesFromEnv(lspGoplsHeapLimitEnv); ok {
		return value
	}
	return defaultGoplsHeapLimitBytes
}

func rssLimitBytesFromEnv(envKey string) (uint64, bool) {
	value, configured, err := strictRSSLimitBytesFromEnv(envKey)
	if err != nil {
		return 0, false
	}
	return value, configured
}

// ValidateResourceLimitEnvironment 在启动语言服务前严格校验 RSS/heap 配置及两层预算关系。
func ValidateResourceLimitEnvironment() error {
	if _, configured := os.LookupEnv(DeprecatedResourceCohortHardLimitMBEnv); configured {
		return fmt.Errorf(
			"%s is no longer supported; use %s",
			DeprecatedResourceCohortHardLimitMBEnv,
			ResourceCohortHardLimitMBEnv,
		)
	}
	for _, key := range []string{
		lspRSSLimitEnv,
		lspGoRSSLimitEnv,
		lspGoplsHeapLimitEnv,
		ResourceCohortHardLimitMBEnv,
		goplsCohortHardLimitEnv,
	} {
		if _, _, err := strictRSSLimitBytesFromEnv(key); err != nil {
			return err
		}
	}
	nonGoplsLocal := uint64(defaultCohortHardLimitBytes)
	if value, ok := rssLimitBytesFromEnv(lspRSSLimitEnv); ok {
		nonGoplsLocal = value
	}
	nonGoplsCohort := uint64(defaultCohortHardLimitBytes)
	if value, ok := rssLimitBytesFromEnv(ResourceCohortHardLimitMBEnv); ok {
		nonGoplsCohort = value
	}
	if nonGoplsLocal < nonGoplsCohort {
		return fmt.Errorf(
			"%s (%d bytes) must not be lower than %s (%d bytes)",
			lspRSSLimitEnv,
			nonGoplsLocal,
			ResourceCohortHardLimitMBEnv,
			nonGoplsCohort,
		)
	}
	goplsHeap := uint64(defaultGoplsHeapLimitBytes)
	if value, ok := rssLimitBytesFromEnv(lspGoplsHeapLimitEnv); ok {
		goplsHeap = value
	}
	goplsCohort := uint64(defaultGoplsCohortHardLimitBytes)
	if value, ok := rssLimitBytesFromEnv(goplsCohortHardLimitEnv); ok {
		goplsCohort = value
	}
	if goplsHeap >= goplsCohort {
		return fmt.Errorf(
			"%s (%d bytes) must be lower than %s (%d bytes)",
			lspGoplsHeapLimitEnv,
			goplsHeap,
			goplsCohortHardLimitEnv,
			goplsCohort,
		)
	}
	return nil
}

func strictRSSLimitBytesFromEnv(envKey string) (uint64, bool, error) {
	raw := strings.TrimSpace(os.Getenv(envKey))
	if raw == "" {
		return 0, false, nil
	}
	value, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || value == 0 {
		return 0, true, fmt.Errorf("%s must be a positive integer MiB value: %q", envKey, raw)
	}
	const mib = uint64(1024 * 1024)
	if value > ^uint64(0)/mib {
		return 0, true, fmt.Errorf("%s overflows bytes: %q MiB", envKey, raw)
	}
	return value * mib, true, nil
}
