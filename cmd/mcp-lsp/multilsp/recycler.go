package multilsp

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/mcpserver/common"
	platformconfig "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/config"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	platformshared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/shared"
)

const (
	defaultRecyclerInterval = 30 * time.Second
	defaultRSSLimitBytes    = 768 * 1024 * 1024
	defaultGoRSSLimitBytes  = 384 * 1024 * 1024
	lspRSSLimitEnv          = "AGENT_LSP_RSS_LIMIT_MB"
	lspGoRSSLimitEnv        = "AGENT_LSP_GO_RSS_LIMIT_MB"

	idleTimeout                    = 10 * time.Minute
	recyclerProbeDegradedThreshold = 3
)

type recyclerRSSProbe func(Client) (uint64, int, error)

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

	mu         sync.Mutex
	lastActive map[int]time.Time
	health     recyclerHealthSnapshot
}

// 编译期确认 poolRecycler 满足根 runner 聚合器消费的 Runner 合约。
var _ platformrunner.Runner = (*poolRecycler)(nil)

func newPoolRecycler(pool *ManagerPool) *poolRecycler {
	return &poolRecycler{
		pool:       pool,
		interval:   defaultRecyclerInterval,
		rssProbe:   clientRSSBytes,
		lastActive: map[int]time.Time{},
	}
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

func (r *poolRecycler) recordProbeFailure(summary string) recyclerHealthSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.health.ProbeFailuresTotal++
	r.health.ConsecutiveProbeFailures++
	r.health.LastProbeError = summary
	r.health.LastProbeAt = time.Now()
	r.health.Degraded = r.health.ConsecutiveProbeFailures >= recyclerProbeDegradedThreshold
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
	for _, snapshot := range r.pool.snapshotManagers() {
		r.checkIdleWorkspaces(snapshot.manager, snapshot.resolvedScope)
		r.checkManager(snapshot.index, snapshot.manager, snapshot.resolvedScope)
	}
}

func (r *poolRecycler) checkManager(index int, mgr *manager, scope ResolvedLSPToolScope) {
	if mgr == nil || managerIsClosed(mgr) {
		return
	}
	if !r.shouldCheck(index) {
		return
	}

	for _, workspace := range snapshotWorkspaceClients(mgr) {
		r.recycleIfNeeded(mgr, scope, workspace)
	}
}

// recycleIfNeeded 在单个 workspace client 超过 RSS 上限时尝试回收。
// 仍有活跃租约时只记录日志不关闭进程，避免正在执行的 LSP 请求被异步切断。
func (r *poolRecycler) recycleIfNeeded(mgr *manager, scope ResolvedLSPToolScope, workspace workspaceClient) {
	rssBytes, pid, ok := r.probeWorkspace(mgr, scope, workspace)
	if !ok {
		return
	}
	limit := rssLimitBytesForLanguage(workspace.languageID)
	if rssBytes <= limit {
		return
	}
	activeLeases := r.pool.activeLeases(workspace.client)
	logRSSWindowExceeded(mgr, scope, workspace, pid, rssBytes, limit, activeLeases)
	if activeLeases > 0 {
		if mgr.logger != nil {
			mgr.logger.Info("LSP recycle skipped: active leases",
				"manager_key", scope.ManagerKey,
				"scope_key", scope.ScopeKey,
				"workspace", workspace.key,
				"language", normalizeLanguageID(workspace.languageID),
				"pid", pid,
				"rss_bytes", rssBytes,
				"rss_limit_bytes", limit,
				"active_leases", activeLeases,
				"reason", "rss_limit",
			)
		}
		return
	}
	recycled, err := recycleWorkspaceClient(mgr, scope, workspace)
	if err != nil && mgr.logger != nil {
		mgr.logger.Warn("LSP recycle failed",
			"manager_key", scope.ManagerKey,
			"scope_key", scope.ScopeKey,
			"workspace", workspace.key,
			"language", normalizeLanguageID(workspace.languageID),
			"pid", pid,
			"rss_bytes", rssBytes,
			"rss_limit_bytes", limit,
			"reason", "rss_limit",
			"err", err,
		)
	}
	if recycled && mgr.logger != nil {
		mgr.logger.Warn("recycled LSP process",
			"manager_key", scope.ManagerKey,
			"scope_key", scope.ScopeKey,
			"workspace", workspace.key,
			"language", normalizeLanguageID(workspace.languageID),
			"pid", pid,
			"rss_bytes", rssBytes,
			"rss_limit_bytes", limit,
			"reason", "rss_limit",
		)
	}
}

func (r *poolRecycler) shouldCheck(index int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	last := r.lastActive[index]
	return last.IsZero() || time.Since(last) >= r.interval/2
}

// checkIdleWorkspaces 关闭超过 idleTimeout 未收到请求的 workspace client。
// 有活跃租约的 client 会跳过，下一次请求会通过 ensureClient 懒启动新的 LSP 子进程。
func (r *poolRecycler) checkIdleWorkspaces(mgr *manager, scope ResolvedLSPToolScope) {
	if mgr == nil || managerIsClosed(mgr) {
		return
	}
	now := time.Now()
	for _, workspace := range snapshotWorkspaceClients(mgr) {
		if workspace.lastActivity.IsZero() {
			continue
		}
		idleDuration := now.Sub(workspace.lastActivity)
		if idleDuration < idleTimeout {
			continue
		}
		activeLeases := 0
		if r.pool != nil {
			activeLeases = r.pool.activeLeases(workspace.client)
		}
		r.logIdleWindowExceeded(mgr, scope, workspace, idleDuration, activeLeases)
		if activeLeases > 0 {
			continue
		}
		r.shutdownIdleWorkspace(mgr, scope, workspace)
	}
}

func (r *poolRecycler) shutdownIdleWorkspace(mgr *manager, scope ResolvedLSPToolScope, workspace workspaceClient) {
	detached := detachWorkspaceClientIfIdle(mgr, workspace.key, workspace.client)
	if detached == nil || detached.client == nil {
		return
	}
	mgr.AdvanceDiagnosticGeneration()

	ctx, cancel := platformconfig.WithTimeout(context.Background(), managerShutdownTimeout)
	_ = detached.client.Shutdown(ctx)
	cancel()
	_ = detached.client.Close()

	if mgr.logger != nil {
		mgr.logger.Info("LSP idle shutdown",
			"manager_key", scope.ManagerKey,
			"scope_key", scope.ScopeKey,
			"workspace", workspace.key,
			"language", normalizeLanguageID(workspace.languageID),
			"idle_duration", time.Since(workspace.lastActivity).String(),
			"reason", "idle_timeout",
		)
	}
}

func (r *poolRecycler) logIdleWindowExceeded(mgr *manager, scope ResolvedLSPToolScope, workspace workspaceClient, idleDuration time.Duration, activeLeases int) {
	if mgr == nil || mgr.logger == nil {
		return
	}
	action := "shutdown"
	if activeLeases > 0 {
		action = "skip_active_leases"
	}
	args := recyclerWorkspaceLogArgs(scope, workspace,
		"idle_duration", idleDuration.String(),
		"idle_timeout", idleTimeout.String(),
		"active_leases", activeLeases,
		"action", action,
		"reason", "idle_timeout",
	)
	args = r.appendRecyclerRSSProbeArgs(args, mgr, scope, workspace)
	mgr.logger.Debug("LSP recycler idle window exceeded", args...)
}

func logRSSWindowExceeded(mgr *manager, scope ResolvedLSPToolScope, workspace workspaceClient, pid int, rssBytes, limit uint64, activeLeases int) {
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
		"reason", "rss_limit",
	)
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
	rssBytes, pid, ok := r.probeWorkspace(mgr, scope, workspace)
	if !ok {
		return append(args, "rss_probe_failed", true)
	}
	return append(args, "pid", pid, "rss_bytes", rssBytes)
}

func (r *poolRecycler) probeWorkspace(mgr *manager, scope ResolvedLSPToolScope, workspace workspaceClient) (uint64, int, bool) {
	rssBytes, pid, err := r.clientRSSBytes(workspace.client)
	summary := "rss_probe_error"
	if err == nil && pid <= 0 {
		err = fmt.Errorf("RSS probe returned invalid pid")
		summary = "rss_probe_invalid_pid"
	}
	if err != nil {
		health := r.recordProbeFailure(summary)
		r.logProbeFailure(mgr, scope, workspace, err, health)
		return 0, 0, false
	}
	r.recordProbeSuccess()
	return rssBytes, pid, true
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
	detached := detachWorkspaceClientIfIdle(mgr, workspace.key, workspace.client)
	if detached == nil || detached.client == nil {
		return false, nil
	}
	mgr.AdvanceDiagnosticGeneration()

	ctx, cancel := platformconfig.WithTimeout(context.Background(), managerShutdownTimeout)
	shutdownErr := detached.client.Shutdown(ctx)
	cancel()
	closeErr := detached.client.Close()

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

// detachWorkspaceClientIfIdle 在持锁状态下摘除指定 workspace client。
// expected 用于防止快照过期误删新 client；存在活跃租约时直接返回 nil。
func detachWorkspaceClientIfIdle(mgr *manager, key string, expected Client) *workspaceClient {
	if mgr == nil {
		return nil
	}
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	workspace := mgr.workspaces[key]
	if workspace == nil || workspace.client == nil {
		return nil
	}
	if expected != nil && workspace.client != expected {
		return nil
	}
	if mgr.pool != nil && mgr.pool.activeLeases(workspace.client) > 0 {
		return nil
	}
	delete(mgr.workspaces, key)
	return workspace
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
	typed, ok := current.(*client)
	if !ok || typed.transport == nil || typed.transport.cmd == nil || typed.transport.cmd.Process == nil {
		return 0, 0, nil
	}
	pid := typed.transport.cmd.Process.Pid
	rss, err := processRSSBytes(pid)
	return rss, pid, err
}

func processRSSBytes(pid int) (uint64, error) {
	switch runtime.GOOS {
	case "linux":
		return linuxRSSBytes(pid)
	case "darwin":
		return psRSSBytes(pid)
	default:
		return 0, fmt.Errorf("unsupported platform: %s", runtime.GOOS)
	}
}

func linuxRSSBytes(pid int) (uint64, error) {
	payload, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", pid))
	if err != nil {
		return 0, err
	}
	fields := strings.Fields(string(payload))
	if len(fields) < 2 {
		return 0, fmt.Errorf("unexpected statm payload for pid %d", pid)
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, err
	}
	return pages * uint64(os.Getpagesize()), nil
}

func psRSSBytes(pid int) (uint64, error) {
	output, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, err
	}
	value := strings.TrimSpace(string(output))
	if value == "" {
		return 0, nil
	}
	kilobytes, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, err
	}
	return kilobytes * 1024, nil
}

// rssLimitBytesForLanguage 返回指定语言的 LSP RSS 回收阈值。
// Go 系列语言默认阈值更低且有独立环境变量，其余语言使用通用阈值。
func rssLimitBytesForLanguage(languageID string) uint64 {
	lang := normalizeLanguageID(languageID)
	if lang == "go" || lang == "gomod" || lang == "gosum" || lang == "gowork" {
		if value, ok := rssLimitBytesFromEnv(lspGoRSSLimitEnv); ok {
			return value
		}
		return defaultGoRSSLimitBytes
	}
	if value, ok := rssLimitBytesFromEnv(lspRSSLimitEnv); ok {
		return value
	}
	return defaultRSSLimitBytes
}

func rssLimitBytesFromEnv(envKey string) (uint64, bool) {
	value, err := strconv.ParseUint(strings.TrimSpace(os.Getenv(envKey)), 10, 64)
	if err != nil || value == 0 {
		return 0, false
	}
	return value * 1024 * 1024, true
}
