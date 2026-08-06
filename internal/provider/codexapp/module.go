package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	contract "github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/pidregistry"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/rpc"
	platformrunner "github.com/lihah111222333-cloud/super-dolphin-agent/internal/platform/runner"
	providershared "github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/shared"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/provider/unified"
	pkglogger "github.com/lihah111222333-cloud/super-dolphin-agent/pkg/logger"
	"github.com/lihah111222333-cloud/super-dolphin-agent/pkg/skillmetrics"
	"go.uber.org/fx"
)

var Module = fx.Module("provider.codexapp",
	fx.Provide(
		newCodexInstaller,
		NewServerManager,
		provideDriverFactory,
		fx.Annotate(provideContractDriverFactory, fx.ResultTags(`group:"drivers"`)),
		fx.Annotate(provideDreamExecutorProvider, fx.ResultTags(`group:"dream_executors"`)),
		// 显式 Codex identity 的会话通过 ServerPool 获取独立 app-server。
		// 这样 session shutdown 才能拥有对应进程组和 sidecar 的释放边界。
		provideServerPool,
		newPoolEvictRunner,
		fx.Annotate(poolEvictRunnerAsRunner, fx.ResultTags(`group:"runners"`)),
		// PeerSupervisor 是 mcp-orch/mcp-lsp singleton peer 的唯一 Runner owner。
		// peer 启动、重启和退出收敛到这里，避免散落 goroutine 绕过生命周期管理。
		fx.Annotate(provideDefaultPeerSupervisor, fx.ResultTags(`group:"runners"`)),
	),
	fx.Invoke(registerTranslatorsWithRuntimeHooks),
)

// registerTranslatorsWithRuntimeHooks 将同一 runtime owner 显式注入 Codex 事件翻译链。
func registerTranslatorsWithRuntimeHooks(dispatcher *unified.EventDispatcher, hooks providershared.RuntimeHooks) error {
	return RegisterTranslators(dispatcher, hooks)
}

// provideDefaultPeerSupervisor 构造生产使用的 PeerSupervisor runner。
// 独立函数让 fx.Annotate 可以把具体类型收窄为 platformrunner.Runner。
func provideDefaultPeerSupervisor(
	mgr *ServerManager,
	logger *slog.Logger,
	cfg *contract.Config,
	issuer contract.ManagedAuthorityIssuer,
) platformrunner.Runner {
	return NewPeerSupervisor(
		mgr,
		logger,
		WithPeerWorkspaceRoots(configuredPeerWorkspaceRoots(cfg)),
		WithPeerManagedAuthorityIssuer(issuer),
	)
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

// DriverFactoryParams 收集 NewDriverFactory 的 fx 依赖。
// Pool 和 Mirror 参与 provider home 隔离；缺失时会在会话准备阶段 fail-fast。
type DriverFactoryParams struct {
	fx.In

	Logger       *slog.Logger
	LogRuntime   *pkglogger.Runtime
	Dispatcher   *unified.EventDispatcher
	Approvals    *rpc.ApprovalManager
	Reporter     contract.RuntimeReporter
	Dependency   contract.DependencyConfig `optional:"true"`
	Config       *contract.Config          `optional:"true"`
	Manager      *ServerManager
	Pool         *ServerPool
	Mirror       contract.SkillMirrorReconciler
	Recovery     contract.SessionRecoveryReporter `optional:"true"`
	RuntimeHooks providershared.RuntimeHooks
	Metrics      *skillmetrics.Registry
}

func provideDriverFactory(p DriverFactoryParams) (*DriverFactory, error) {
	if err := requireApprovalManager(p.Approvals); err != nil {
		return nil, err
	}
	reporter, err := newModeAwareRuntimeReporter(p.Reporter, p.Dependency, p.Config, p.Logger, "codex")
	if err != nil {
		return nil, err
	}
	if p.Metrics == nil {
		return nil, fmt.Errorf("codexapp skill metrics registry is required")
	}
	if p.LogRuntime == nil {
		return nil, fmt.Errorf("codexapp logger runtime is required")
	}
	factory, err := NewDriverFactory(p.Logger, p.Dispatcher, p.Approvals, reporter, p.Manager, p.Pool, p.Mirror, p.Recovery, p.RuntimeHooks)
	if err != nil {
		return nil, err
	}
	factory.SetSkillMetrics(p.Metrics)
	factory.SetLogRuntime(p.LogRuntime)
	return factory, nil
}

func provideContractDriverFactory(factory *DriverFactory) contract.DriverFactory {
	if factory == nil {
		return contract.DriverFactory{}
	}
	return factory.DriverFactory
}

type runtimeReportDeferredStatus struct {
	AgentID    string
	Provider   string
	Port       int
	Dependency string
	Profile    contract.DependencyProfile
	Status     string
}

type modeAwareRuntimeReporter struct {
	base     contract.RuntimeReporter
	logger   *slog.Logger
	profile  contract.DependencyProfile
	provider string

	mu       sync.Mutex
	deferred map[string]runtimeReportDeferredStatus
}

func newModeAwareRuntimeReporter(base contract.RuntimeReporter, dependency contract.DependencyConfig, cfg *contract.Config, logger *slog.Logger, provider string) (*modeAwareRuntimeReporter, error) {
	if base == nil {
		return nil, fmt.Errorf("%s runtime reporter is required", strings.TrimSpace(provider))
	}
	profile, err := dependencyProfileFromFactoryParams(dependency, cfg, provider)
	if err != nil {
		return nil, err
	}
	return &modeAwareRuntimeReporter{
		base:     base,
		logger:   logger,
		profile:  profile,
		provider: strings.TrimSpace(provider),
		deferred: map[string]runtimeReportDeferredStatus{},
	}, nil
}

func dependencyProfileFromFactoryParams(dependency contract.DependencyConfig, cfg *contract.Config, provider string) (contract.DependencyProfile, error) {
	profile := dependency.Profile
	if strings.TrimSpace(string(profile)) == "" && cfg != nil {
		profile = cfg.Dependency.Profile
	}
	switch profile {
	case contract.DependencyProfileDesktopHost, contract.DependencyProfileProduction, contract.DependencyProfileTest:
		return profile, nil
	case "":
		return "", fmt.Errorf("%s dependency profile is required", strings.TrimSpace(provider))
	default:
		return "", fmt.Errorf("%s dependency profile %q is not supported", strings.TrimSpace(provider), profile)
	}
}

// ReportRuntime 在 Codex start/resume 收尾阶段执行 mode-aware runtime 上报。
// production 和 generic error 都必须阻断，desktop/test 只接受 typed deferred 并记录状态。
func (r *modeAwareRuntimeReporter) ReportRuntime(ctx context.Context, report contract.RuntimeReport) error {
	if r == nil || r.base == nil {
		return fmt.Errorf("%s runtime reporter is required", r.providerName())
	}
	err := r.base.ReportRuntime(ctx, report)
	if err == nil {
		return nil
	}
	if !errors.Is(err, contract.ErrDependencyDeferred) || !r.allowDeferred() {
		return fmt.Errorf("%s runtime report failed: %w", r.providerName(), err)
	}
	return r.acceptDeferredRuntimeReport(report)
}

func (r *modeAwareRuntimeReporter) allowDeferred() bool {
	return r.profile == contract.DependencyProfileDesktopHost || r.profile == contract.DependencyProfileTest
}

func (r *modeAwareRuntimeReporter) recordDeferred(report contract.RuntimeReport) {
	status := runtimeReportDeferredStatus{
		AgentID:    strings.TrimSpace(report.AgentID),
		Provider:   strings.TrimSpace(report.Provider),
		Port:       report.Port,
		Dependency: "runtime_reporter.orchestration_service",
		Profile:    r.profile,
		Status:     "deferred",
	}
	r.mu.Lock()
	r.deferred[status.AgentID] = status
	r.mu.Unlock()
	if r.logger != nil {
		r.logger.Info("runtime report deferred", "agent_id", status.AgentID, "provider", status.Provider, "dependency", status.Dependency, "profile", status.Profile, "status", status.Status)
	}
}

func (r *modeAwareRuntimeReporter) acceptDeferredRuntimeReport(report contract.RuntimeReport) error {
	r.recordDeferred(report)
	return nil
}

func (r *modeAwareRuntimeReporter) runtimeReportDeferredForTest(agentID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	status, ok := r.deferred[strings.TrimSpace(agentID)]
	return ok && status.Dependency == "runtime_reporter.orchestration_service" && status.Profile == r.profile && status.Status == "deferred"
}

func (r *modeAwareRuntimeReporter) providerName() string {
	if r == nil || strings.TrimSpace(r.provider) == "" {
		return "provider"
	}
	return r.provider
}

// ToolHandler 是 app-server 回调到 toolbridge 的最小处理函数。
// 它接收原始 JSON-RPC 消息，返回结果或错误供 transport 写回 provider。
type ToolHandler func(context.Context, RawMessage) (any, error)

// ServerManager 管理 legacy 共享 Codex app-server 进程。
// 每个 session 仍独立建立 WebSocket，单条连接损坏不会直接拖垮其他 session。
type ServerManager struct {
	mu          sync.Mutex
	process     *transport // owns the local process; sessions use ServerURL() to connect independently
	serverURL   string
	ready       bool
	err         error
	toolHandler ToolHandler
	pidRegistry *pidregistry.Registry
	installer   *codexInstaller
	cleanupOnce sync.Once
}

// Responder 抽象 JSON-RPC 响应写回能力。
// toolbridge 回调只依赖该窄接口，避免暴露完整 transport 生命周期。
type Responder interface {
	RespondWithID(id json.RawMessage, result any, callErr error) error
}

// ServerManagerParams 是 NewServerManager 需要的 fx 依赖。
// PIDRegistry 用于启动和关闭时回收遗留子进程。
type ServerManagerParams struct {
	fx.In
	Lifecycle   fx.Lifecycle
	PIDRegistry *pidregistry.Registry
	Installer   *codexInstaller
}

// ServerPoolParams 承载 provideServerPool 的 fx 依赖。
// logger 和 PID registry 可选，便于测试只构造 pool 而不接入完整应用图。
type ServerPoolParams struct {
	fx.In

	Lifecycle   fx.Lifecycle
	Logger      *slog.Logger          `optional:"true"`
	PIDRegistry *pidregistry.Registry `optional:"true"`
	Installer   *codexInstaller
}

// provideServerPool 用 transport-backed spawner 构造生产 ServerPool。
// fx Stop 会关闭 pool，确保仍被占用的 app-server 子进程先收到 SIGTERM 再释放进程树。
// 它与 legacy ServerManager 并行提供，调用方只有显式选择 pool 时才获得独立 app-server。
func provideServerPool(p ServerPoolParams) (*ServerPool, error) {
	if p.Installer == nil {
		return nil, errors.New("codexapp installer is required")
	}
	logger := p.Logger
	if logger == nil {
		logger = pkglogger.Get()
	}
	spawner, err := NewTransportSpawner(p.PIDRegistry, logger, p.Installer)
	if err != nil {
		return nil, err
	}
	pool := NewServerPool(logger, spawner, PoolConfig{})
	p.Lifecycle.Append(fx.Hook{
		OnStop: func(ctx context.Context) error { return pool.Close(ctx) },
	})
	return pool, nil
}

// NewServerManager 创建共享 app-server 管理器并注册 fx 生命周期钩子。
// 启动时只做一次遗留进程清理，停止时负责关闭共享 transport 和 PID registry。
func NewServerManager(p ServerManagerParams) (*ServerManager, error) {
	if p.Installer == nil {
		return nil, errors.New("codexapp installer is required")
	}
	m := &ServerManager{pidRegistry: p.PIDRegistry, installer: p.Installer}
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error {
			m.cleanupStaleProcesses()
			return nil
		},
		OnStop: func(ctx context.Context) error { return m.stop(ctx) },
	})
	return m, nil
}

// ServerURL 返回共享 app-server 的 ws:// 地址。
// 调用方只读这个快照，真正的连接有效性仍由 Running 或会话连接阶段校验。
func (m *ServerManager) ServerURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.serverURL
}

// Running 判断共享 app-server 是否处于 ready 且底层进程仍存活。
// 这里在锁内读取状态，避免 stop/start 与 UI 轮询看到不一致快照。
func (m *ServerManager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ready && m.process != nil && m.process.processRunning()
}

// EnsureRunning 确保共享 app-server 已启动并通过一次健康连接。
// 进程不存在或不可用时会重新启动，启动失败直接返回错误而不伪造 serverURL。
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

// SetToolHandler 替换共享 app-server 回调到宿主的工具处理器。
// handler 在锁内切换，避免会话启动期间读到半更新状态。
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

// cleanupStaleProcessesOnce 按稳定进程身份回收一次历史 runtime，并记录拒绝原因。
func (m *ServerManager) cleanupStaleProcessesOnce() {
	protected := currentRuntimeProtectionSet()
	// 先清理 PID registry 记录的死实例进程，只终止确认为本应用创建过的子进程。
	cleanup := pidregistry.CleanupStaleDetailedWithProtectedPIDs(protected)
	if cleanup.Killed > 0 || cleanup.IdentityMismatch > 0 ||
		cleanup.IdentityReadFailure > 0 || cleanup.MissingIdentity > 0 {
		pkglogger.Info("server_manager: cleaned stale registry processes at startup",
			"killed", cleanup.Killed,
			"identity_mismatch", cleanup.IdentityMismatch,
			"identity_read_failure", cleanup.IdentityReadFailure,
			"missing_identity", cleanup.MissingIdentity)
	}

	// 再扫描早于 PID registry 的遗留孤儿进程，覆盖旧版本崩溃后留下的 sidecar。
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
	if m.installer == nil {
		return errors.New("codexapp installer is required")
	}
	if err := m.installer.ensureCLIAvailable(startupCtx); err != nil {
		m.err = err
		return err
	}
	t := &transport{}
	if err := t.spawnLocal(startupCtx); err != nil {
		m.err = err
		return err
	}
	// 注册 app-server PID，宿主崩溃后的下一次启动可精确回收。
	if pid := t.localPID(); pid > 0 {
		if err := m.pidRegistry.RegisterChecked(pid, "codex-app-server", nil); err != nil {
			_ = t.Kill()
			m.err = err
			return err
		}
	}
	// 先做一次健康连接和 initialize，后续 session 会各自建立独立 WebSocket。
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

// stop 停止共享 app-server transport 并清理残留 sidecar。
// peer 进程由 PeerSupervisor 拥有，这里只处理 ServerManager 自己启动的 app-server。
func (m *ServerManager) stop(ctx context.Context) error {
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

	// 给 Codex 收到 SIGTERM 后退出 MCP sidecar 的时间，再做残留扫描。
	select {
	case <-time.After(mcpOrphanCleanupGracePeriod):
	case <-ctx.Done():
	}

	cleanResidualProcesses()

	// 确认进程停止或清理完成后再关闭 PID registry，避免退出中途失去崩溃恢复线索。
	if registry != nil {
		registry.Close()
	}
	return err
}

// cleanResidualProcesses 在关闭后扫描并回收残留 MCP 或 app-server 孤儿。
// 保护集会排除当前宿主进程树和调用方传入的活跃 server，避免误杀仍被使用的进程。
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

// mcpProcessInfo 描述一次进程表扫描发现的 MCP sidecar。
// PID/PPID 用于判断是否已脱离当前运行中的应用树。
type mcpProcessInfo struct {
	pid    int
	ppid   int
	binary string
}

// cleanOrphanedMCPProcesses 清理不属于当前应用进程树的 mcp-orch/mcp-lsp。
// 返回实际 kill 成功的数量；受保护 PID 会跳过，防止误杀当前或上游工具进程。
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
		// 仍有非 init 父进程的 MCP 属于其他运行中应用或工具，不按孤儿清理。
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

// discoverProcesses 返回完整 PID 父子关系和已过滤的 MCP sidecar 列表。
// 平台差异封装在 discoverAllProcesses 中，调用方只依赖这里的抽象结果。
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

// buildProcessTree 返回 rootPID 及其所有 descendant PID。
// 先建 children map 再 BFS，避免在进程表上反复线性扫描。
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

// buildProcessAncestry 返回 rootPID 到父链上的所有 PID，不包含 PID 1。
// 孤儿清理用它保护上游工具或宿主，避免杀祖先进程时连带终止当前进程。
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

// killMCPProcess 由 process_unix.go 和 process_windows.go 分别实现。
// 调用方只关心 PID 终止语义，平台细节不泄漏到清理流程。

// mcpOrphanCleanupGracePeriod 是停止 Codex 后等待 MCP sidecar 自行退出的宽限期。
// 宽限期结束后才扫描残留，减少正常退出路径上的误报和多余强杀。
const mcpOrphanCleanupGracePeriod = 500 * time.Millisecond
