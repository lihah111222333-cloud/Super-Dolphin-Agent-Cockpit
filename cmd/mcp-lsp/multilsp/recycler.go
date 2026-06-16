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

	common "github.com/anthropic-ai/super-agent-v3/internal/mcpserver/runtime"
	platformconfig "github.com/anthropic-ai/super-agent-v3/internal/platform/config"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
)

const (
	defaultRecyclerInterval = 30 * time.Second
	defaultRSSLimitBytes    = 768 * 1024 * 1024
	defaultGoRSSLimitBytes  = 384 * 1024 * 1024
	lspRSSLimitEnv          = "AGENT_LSP_RSS_LIMIT_MB"
	lspGoRSSLimitEnv        = "AGENT_LSP_GO_RSS_LIMIT_MB"

	idleTimeout       = 10 * time.Minute
	idleCheckInterval = 30 * time.Second
)

var clientRSSBytesForRecycler = clientRSSBytes

// poolRecycler is the background loop that periodically scans managed
// LSP server processes and recycles ones whose RSS exceeds the configured
// limit. Pre-P22 P2 it spun itself up from NewManagerPool via a
// self-owned goroutine + stopCh pair; it is now a plain
// platformrunner.Runner owned by the root `group:"runners"` bridge.
// See docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md §480-494.
type poolRecycler struct {
	pool     *ManagerPool
	interval time.Duration

	mu         sync.Mutex
	lastActive map[int]time.Time
}

// Compile-time assertion: poolRecycler satisfies the Runner contract
// consumed by the `group:"runners"` aggregation.
var _ platformrunner.Runner = (*poolRecycler)(nil)

func newPoolRecycler(pool *ManagerPool) *poolRecycler {
	return &poolRecycler{
		pool:       pool,
		interval:   defaultRecyclerInterval,
		lastActive: map[int]time.Time{},
	}
}

// TouchShard 处理touchshard。
func (r *poolRecycler) TouchShard(index int) {
	if r == nil {
		return
	}
	r.mu.Lock()
	r.lastActive[index] = time.Now()
	r.mu.Unlock()
}

// Run drives the recycler check loop until ctx is cancelled. A nil
// receiver is accepted as a no-op so callers that rely on
// ManagerPool.RecyclerRunner() without a pool still wire cleanly.
// Run 启动LSP后台流程。
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

// recycleIfNeeded 处理recycleifneeded。
func (r *poolRecycler) recycleIfNeeded(mgr *manager, scope ResolvedLSPToolScope, workspace workspaceClient) {
	rssBytes, pid, err := clientRSSBytesForRecycler(workspace.client)
	if err != nil || pid <= 0 {
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

// checkIdleWorkspaces shuts down workspace clients that have not received
// any request within idleTimeout. The next request will trigger a lazy
// restart via ensureClient.
// checkIdleWorkspaces 处理checkidleworkspaces。
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
		logIdleWindowExceeded(mgr, scope, workspace, idleDuration, activeLeases)
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

func logIdleWindowExceeded(mgr *manager, scope ResolvedLSPToolScope, workspace workspaceClient, idleDuration time.Duration, activeLeases int) {
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
	args = appendRecyclerRSSProbeArgs(args, workspace.client)
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
		"workspace", workspace.key,
		"language", normalizeLanguageID(workspace.languageID),
	}
	return append(args, extra...)
}

func appendRecyclerRSSProbeArgs(args []any, client Client) []any {
	rssBytes, pid, err := clientRSSBytesForRecycler(client)
	if err != nil {
		return append(args, "rss_error", err.Error())
	}
	return append(args, "pid", pid, "rss_bytes", rssBytes)
}

// recycleWorkspaceClient 处理recycle工作区客户端。
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

// recycleResolvedScope 处理recycle已解析作用域。
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

// detachWorkspaceClientIfIdle 处理detach工作区客户端ifidle。
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

// rssLimitBytesForLanguage 为语言处理rsslimitbytes。
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
