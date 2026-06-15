package codexapp

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/rpc"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/provider/unified"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"go.uber.org/fx"
)

var Module = fx.Module("provider.codexapp",
	fx.Provide(
		NewServerManager,
		provideDriverFactory,
		fx.Annotate(provideContractDriverFactory, fx.ResultTags(`group:"drivers"`)),
		fx.Annotate(provideDreamExecutorProvider, fx.ResultTags(`group:"dream_executors"`)),
		// P21 Track B pool 基础设施：ServerPool + 周期 EvictIdle Runner。
		// Codex sessions with an explicit identity route through this pool so
		// session shutdown owns the app-server process group and sidecars.
		provideServerPool,
		newPoolEvictRunner,
		fx.Annotate(poolEvictRunnerAsRunner, fx.ResultTags(`group:"runners"`)),
		// P22 P1a: PeerSupervisor is the single RunnerModule owner for mcp-orch /
		// mcp-lsp peer processes. See docs/plans/迁移/p22/P1a_CodexAppPeerSupervisor.md.
		// Replaces the pre-P1a fx.Invoke(spawnToolbridgePeers) + fire-and-forget
		// watchAndRestartPeer goroutines.
		fx.Annotate(provideDefaultPeerSupervisor, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(RegisterTranslators),
)

// provideDefaultPeerSupervisor is the production constructor for PeerSupervisor.
// Split out so the fx.Annotate above can type the result as platformrunner.Runner.
func provideDefaultPeerSupervisor(mgr *ServerManager, logger *slog.Logger, cfg *contract.Config) platformrunner.Runner {
	return NewPeerSupervisor(mgr, logger, WithPeerWorkspaceRoots(configuredPeerWorkspaceRoots(cfg)))
}

func configuredPeerWorkspaceRoots(cfg *contract.Config) func() []string {
	return func() []string {
		if cfg == nil {
			return nil
		}
		root := strings.TrimSpace(cfg.ProjectRoot)
		if root == "" || !filepath.IsAbs(root) {
			return nil
		}
		return []string{filepath.Clean(root)}
	}
}

// DriverFactoryParams holds the fx-injected dependencies for NewDriverFactory.
type DriverFactoryParams struct {
	fx.In

	Logger     *slog.Logger
	Dispatcher *unified.EventDispatcher
	Approvals  *rpc.ApprovalManager
	Reporter   contract.RuntimeReporter
	Manager    *ServerManager
	Pool       *ServerPool
	Mirror     contract.SkillMirrorReconciler
	Recovery   contract.SessionRecoveryReporter `optional:"true"`
}

func provideDriverFactory(p DriverFactoryParams) *DriverFactory {
	return NewDriverFactory(p.Logger, p.Dispatcher, p.Approvals, p.Reporter, p.Manager, p.Pool, p.Mirror, p.Recovery)
}

func provideContractDriverFactory(factory *DriverFactory) contract.DriverFactory {
	if factory == nil {
		return contract.DriverFactory{}
	}
	return factory.DriverFactory
}

// ServerManager owns a single codex app-server process. Each agent
// session creates its own independent WebSocket connection to
// ServerURL(), providing natural isolation: one broken WS only affects
// the owning session.
type ToolHandler func(context.Context, RawMessage) (any, error)

type ServerManager struct {
	mu          sync.Mutex
	process     *transport // owns the local process; sessions use ServerURL() to connect independently
	serverURL   string
	ready       bool
	err         error
	toolHandler ToolHandler
	pidRegistry *pidregistry.Registry
	cleanupOnce sync.Once
}

type Responder interface {
	RespondWithID(id json.RawMessage, result any, callErr error) error
}

// ServerManagerParams are the fx dependencies for NewServerManager.
type ServerManagerParams struct {
	fx.In
	Lifecycle   fx.Lifecycle
	PIDRegistry *pidregistry.Registry
}

// ServerPoolParams carries the fx dependencies for provideServerPool.
// The logger and pid registry are optional so downstream consumers can
// construct the pool in tests without wiring the full app graph.
type ServerPoolParams struct {
	fx.In

	Lifecycle   fx.Lifecycle
	Logger      *slog.Logger          `optional:"true"`
	PIDRegistry *pidregistry.Registry `optional:"true"`
}

// provideServerPool builds a production ServerPool using the
// transport-backed Spawner. The pool is closed on fx Stop so every
// remaining app-server child receives SIGTERM before the process tree
// tears down.
//
// The pool is intentionally provided independently of the legacy
// ServerManager. Consumers that opt into pool-backed spawning will
// take *ServerPool via fx; existing consumers keep talking to
// ServerManager unchanged. That split lets the cutover land in a
// follow-up PR once we have real codex-binary validation.
func provideServerPool(p ServerPoolParams) *ServerPool {
	logger := p.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	spawner := NewTransportSpawner(p.PIDRegistry, logger)
	pool := NewServerPool(logger, spawner, PoolConfig{})
	p.Lifecycle.Append(fx.Hook{
		OnStop: func(ctx context.Context) error { return pool.Close(ctx) },
	})
	return pool
}

// NewServerManager creates and registers a ServerManager with the fx lifecycle.
// NewServerManager 创建服务端manager。
func NewServerManager(p ServerManagerParams) *ServerManager {
	m := &ServerManager{pidRegistry: p.PIDRegistry}
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			m.cleanupStaleProcesses()
			return nil
		},
		OnStop: func(ctx context.Context) error { return m.stop(ctx) },
	})
	return m
}

// ServerURL returns the ws:// address of the shared app-server.
// ServerURL 处理服务端URL。
func (m *ServerManager) ServerURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.serverURL
}

// Running returns true if the shared app-server process is alive.
// Running 返回底层进程是否仍在运行。
func (m *ServerManager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ready && m.process != nil && m.process.processRunning()
}

// EnsureRunning 确保running。
func (m *ServerManager) EnsureRunning(ctx context.Context) error {
	if m == nil {
		return nil
	}
	m.cleanupStaleProcesses()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ready && m.process != nil && m.process.processRunning() {
		return nil
	}
	return m.startLocked(ctx)
}

// SetToolHandler 设置工具处理器。
func (m *ServerManager) SetToolHandler(h ToolHandler) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toolHandler = h
}

func (m *ServerManager) getToolHandler() ToolHandler {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.toolHandler
}

func (m *ServerManager) cleanupStaleProcesses() {
	if m == nil {
		return
	}
	m.cleanupOnce.Do(func() {
		m.cleanupStaleProcessesOnce()
	})
}

func (m *ServerManager) cleanupStaleProcessesOnce() {
	protected := currentRuntimeProtectionSet()
	// Clean up processes registered by dead app instances via PID registry.
	// This is precise (only kills processes we actually spawned) and fast
	// (batch SIGTERM with a single grace period wait).
	if killed := pidregistry.CleanupStaleWithProtectedPIDs(protected); killed > 0 {
		pkglogger.Info("server_manager: cleaned stale registry processes at startup",
			"killed", killed)
	}

	// Legacy fallback: also scan for orphaned processes that predate the
	// PID registry (e.g. from older builds). This can be removed once all
	// running builds include the PID registry.
	if killed := cleanOrphanedMCPProcesses(protected); killed > 0 {
		pkglogger.Info("server_manager: cleaned orphaned MCP processes at startup",
			"killed", killed)
	}
	if killed := cleanOrphanedAppServersWithProtectedPIDs(protected); killed > 0 {
		pkglogger.Info("server_manager: cleaned orphaned app-server processes at startup",
			"killed", killed)
	}
}

func (m *ServerManager) startLocked(ctx context.Context) error {
	startupCtx, cancel := withTimeout(ctx, transportReadyTimeout)
	defer cancel()
	t := &transport{}
	if err := t.spawnLocal(startupCtx); err != nil {
		m.err = err
		return err
	}
	// Register the app-server PID for crash-safe cleanup.
	if pid := t.localPID(); pid > 0 {
		m.pidRegistry.Register(pid, "codex-app-server", nil)
	}
	// Perform a single health-check connection+initialize to verify the
	// process started correctly. Sessions will each create their own WS.
	if err := t.establish(startupCtx); err != nil {
		_ = t.Kill()
		m.err = err
		return err
	}
	m.process = t
	m.serverURL = t.serverURL
	m.ready = true
	pkglogger.Info("server_manager: shared app-server ready", "server_url", m.serverURL)

	return nil
}

// stop 停止codexapp provider。
func (m *ServerManager) stop(ctx context.Context) error {
	// P22 P1a: peer stop/drain moved to PeerSupervisor.shutdown; ServerManager
	// only owns the shared app-server transport from this point onwards.
	m.mu.Lock()
	m.ready = false
	process := m.process
	registry := m.pidRegistry
	m.process = nil
	m.serverURL = ""
	m.mu.Unlock()

	if process == nil {
		if registry != nil {
			registry.Close()
		}
		return nil
	}
	pkglogger.Info("server_manager: stopping shared app-server")
	err := process.shutdownTransport(true)

	// Give MCP sidecar processes a moment to exit after codex receives SIGTERM.
	select {
	case <-time.After(mcpOrphanCleanupGracePeriod):
	case <-ctx.Done():
	}

	cleanResidualProcesses()

	// 修复：确保所有进程都已经妥善停止或被清理后，再从 /tmp 中删除 PID 注册表文件
	// 这样可以防止在长时间退出的过程中发生意外导致孤儿逃逸。
	if registry != nil {
		registry.Close()
	}
	return err
}

// cleanResidualProcesses sweeps any MCP or app-server orphans after shutdown.
func cleanResidualProcesses() {
	protected := currentRuntimeProtectionSet()
	if killed := cleanOrphanedMCPProcesses(protected); killed > 0 {
		pkglogger.Info("server_manager: cleaned residual MCP processes at shutdown",
			"killed", killed)
	}
	if killed := cleanOrphanedAppServersWithProtectedPIDs(protected); killed > 0 {
		pkglogger.Info("server_manager: cleaned residual app-server processes at shutdown",
			"killed", killed)
	}
}

func resolveLocalTurnID(requested, fallback string) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	return strings.TrimSpace(fallback)
}

// mcpProcessInfo holds PID and PPID for a discovered MCP sidecar process.
type mcpProcessInfo struct {
	pid    int
	ppid   int
	binary string
}

// cleanOrphanedMCPProcesses finds mcp-orch and mcp-lsp processes that are
// NOT part of the current application process tree. Such processes are
// orphans from a previous run (crash, SIGKILL, run-debug restart, etc.).
//
// Returns the number of processes killed.
func cleanOrphanedMCPProcesses(extraProtectedPIDs map[int]struct{}) int {
	allProcs, mcpProcs := discoverProcesses()
	if len(mcpProcs) == 0 {
		return 0
	}

	protected := mergeProtectedPIDs(buildCurrentRuntimeProtectionSet(allProcs), extraProtectedPIDs)
	orphans := filterOrphanMCPProcesses(mcpProcs, protected)

	killed := 0
	for _, proc := range orphans {
		if err := killMCPProcess(proc.pid); err != nil {
			pkglogger.Warn("orphan cleanup: kill failed",
				"binary", proc.binary, "pid", proc.pid, "ppid", proc.ppid,
				"error", err)
			continue
		}
		pkglogger.Info("orphan cleanup: killed orphaned MCP process",
			"binary", proc.binary, "pid", proc.pid, "ppid", proc.ppid)
		killed++
	}
	if killed > 0 {
		pkglogger.Info("orphan cleanup: summary", "total_killed", killed)
	}
	return killed
}

func filterOrphanMCPProcesses(procs []mcpProcessInfo, protectedPIDs map[int]struct{}) []mcpProcessInfo {
	orphans := make([]mcpProcessInfo, 0, len(procs))
	for _, proc := range procs {
		if proc.pid <= 1 {
			continue
		}
		// A process with a live non-init parent belongs to another running
		// application/tool runner, not to stale orphan cleanup.
		if proc.ppid > 1 {
			continue
		}
		if _, protected := protectedPIDs[proc.pid]; protected {
			continue
		}
		orphans = append(orphans, proc)
	}
	return orphans
}

// discoverProcesses returns:
//   - allProcs: map[pid]ppid for the entire process table (used for tree building)
//   - mcpProcs: filtered list of mcp-orch/mcp-lsp entries
//
// The implementation is platform-specific: Unix shells out to `ps`, while
// Windows currently returns nil (Phase 2 will replace it with EnumProcesses).
func discoverProcesses() (allProcs map[int]int, mcpProcs []mcpProcessInfo) {
	return discoverAllProcesses()
}

func parseProcessLine(line string) (pid, ppid int, binary string, ok bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return 0, 0, "", false
	}

	fields := strings.Fields(line)
	if len(fields) < 2 {
		return 0, 0, "", false
	}

	pid, ppid, ok = parseProcessIDs(fields)
	if !ok {
		return 0, 0, "", false
	}
	return pid, ppid, matchMCPBinary(fields), true
}

func parseProcessIDs(fields []string) (pid, ppid int, ok bool) {
	pid, err1 := strconv.Atoi(fields[0])
	ppid, err2 := strconv.Atoi(fields[1])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return pid, ppid, true
}

func matchMCPBinary(fields []string) string {
	if len(fields) < 3 {
		return ""
	}

	comm := fields[len(fields)-1]
	if idx := strings.LastIndex(comm, "/"); idx >= 0 {
		comm = comm[idx+1:]
	}
	if _, ok := managedMCPBinaries[comm]; ok {
		return comm
	}
	return ""
}

func currentRuntimeProtectionSet() map[int]struct{} {
	allProcs, _ := discoverProcesses()
	return buildCurrentRuntimeProtectionSet(allProcs)
}

func buildCurrentRuntimeProtectionSet(allProcs map[int]int) map[int]struct{} {
	return buildRuntimeProtectionSet(os.Getpid(), allProcs)
}

func buildRuntimeProtectionSet(rootPID int, allProcs map[int]int) map[int]struct{} {
	protected := buildProcessTree(rootPID, allProcs)
	for pid := range buildProcessAncestry(rootPID, allProcs) {
		protected[pid] = struct{}{}
	}
	return protected
}

func mergeProtectedPIDs(protected, extra map[int]struct{}) map[int]struct{} {
	if len(extra) == 0 {
		return protected
	}
	if protected == nil {
		protected = make(map[int]struct{}, len(extra))
	}
	for pid := range extra {
		protected[pid] = struct{}{}
	}
	return protected
}

// buildProcessTree returns all PIDs that are descendants of rootPID
// (including rootPID itself). Uses a children-map + BFS for O(N) traversal.
// buildProcessTree 构建进程tree。
func buildProcessTree(rootPID int, allProcs map[int]int) map[int]struct{} {
	children := make(map[int][]int, len(allProcs))
	for pid, ppid := range allProcs {
		children[ppid] = append(children[ppid], pid)
	}

	tree := make(map[int]struct{}, 16)
	tree[rootPID] = struct{}{}
	queue := []int{rootPID}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, child := range children[current] {
			if _, seen := tree[child]; !seen {
				tree[child] = struct{}{}
				queue = append(queue, child)
			}
		}
	}
	return tree
}

// buildProcessAncestry returns all PIDs on rootPID's parent chain (including
// rootPID itself, excluding PID 1). It is used by orphan cleanup to avoid
// killing an ancestor app-server when this binary is launched from an existing
// tool runner: killing that ancestor's descendants can kill the current process.
// buildProcessAncestry 构建进程ancestry。
func buildProcessAncestry(rootPID int, allProcs map[int]int) map[int]struct{} {
	ancestry := make(map[int]struct{}, 8)
	for pid := rootPID; pid > 1; {
		if _, seen := ancestry[pid]; seen {
			break
		}
		ancestry[pid] = struct{}{}

		ppid, ok := allProcs[pid]
		if !ok || ppid <= 1 || ppid == pid {
			break
		}
		pid = ppid
	}
	return ancestry
}

// killMCPProcess is implemented per-platform in process_unix.go /
// process_windows.go.

// mcpOrphanCleanupGracePeriod is the delay after stopping the codex process
// before scanning for residual MCP sidecars. This gives the codex process
// time to propagate SIGTERM to its MCP children.
const mcpOrphanCleanupGracePeriod = 500 * time.Millisecond
