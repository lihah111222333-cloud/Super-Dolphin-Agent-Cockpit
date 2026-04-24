package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	contract "github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	promptpkg "github.com/anthropic-ai/super-agent-v3/internal/module/prompt"
	skillpkg "github.com/anthropic-ai/super-agent-v3/internal/module/skill"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/pidregistry"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
	"go.uber.org/fx"
)

var Module = fx.Module("provider.codexapp",
	fx.Provide(
		NewServerManager,
		NewDriverFactory,
		fx.Annotate(provideContractDriverFactory, fx.ResultTags(`group:"drivers"`)),
		fx.Annotate(provideDreamExecutorProvider, fx.ResultTags(`group:"dream_executors"`)),
		// P20.1 Phase 10: codexapp 无原生 skill 机制，但仍注册空实现进聚合 group
		// ——保持 detector 成员不因 provider 组合误差而缺失。
		fx.Annotate(NewSkillInjectionPort, fx.ResultTags(promptpkg.SkillInjectionPortGroupTag)),
		// P21 Track B pool 基础设施：ServerPool + 周期 EvictIdle Runner。
		// ServerPool 目前是独立 fx 产物，不参与任何 driver／session／resolver
		// 当前路径；等下一期 PR 用真实 codex 二进制验证后再做 cutover。
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
func provideDefaultPeerSupervisor(mgr *ServerManager, logger *slog.Logger) platformrunner.Runner {
	return NewPeerSupervisor(mgr, logger)
}

func provideContractDriverFactory(factory *DriverFactory) contract.DriverFactory {
	if factory == nil {
		return contract.DriverFactory{}
	}
	return factory.DriverFactory
}

// ---------------------------------------------------------------------------
// ServerManager: shared codex app-server process (one process, N sessions)
// ---------------------------------------------------------------------------

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
func NewServerManager(p ServerManagerParams) *ServerManager {
	m := &ServerManager{pidRegistry: p.PIDRegistry}
	p.Lifecycle.Append(fx.Hook{
		OnStart: func(ctx context.Context) error { return m.start(ctx) },
		OnStop:  func(ctx context.Context) error { return m.stop(ctx) },
	})
	return m
}

// ServerURL returns the ws:// address of the shared app-server.
func (m *ServerManager) ServerURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.serverURL
}

// Running returns true if the shared app-server process is alive.
func (m *ServerManager) Running() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ready && m.process != nil && m.process.processRunning()
}

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

func (m *ServerManager) start(ctx context.Context) error {
	// Clean up processes registered by dead app instances via PID registry.
	// This is precise (only kills processes we actually spawned) and fast
	// (batch SIGTERM with a single grace period wait).
	if killed := pidregistry.CleanupStale(); killed > 0 {
		pkglogger.Info("server_manager: cleaned stale registry processes at startup",
			"killed", killed)
	}

	// Legacy fallback: also scan for orphaned processes that predate the
	// PID registry (e.g. from older builds). This can be removed once all
	// running builds include the PID registry.
	if killed := cleanOrphanedMCPProcesses(nil); killed > 0 {
		pkglogger.Info("server_manager: cleaned orphaned MCP processes at startup",
			"killed", killed)
	}
	if killed := cleanOrphanedAppServers(); killed > 0 {
		pkglogger.Info("server_manager: cleaned orphaned app-server processes at startup",
			"killed", killed)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

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
	if killed := cleanOrphanedMCPProcesses(nil); killed > 0 {
		pkglogger.Info("server_manager: cleaned residual MCP processes at shutdown",
			"killed", killed)
	}
	if killed := cleanOrphanedAppServers(); killed > 0 {
		pkglogger.Info("server_manager: cleaned residual app-server processes at shutdown",
			"killed", killed)
	}
}

// buildSkillPromptInput 按 P20.1 Phase 4 规格渲染 skill 注入块，并补 P20.2 §4
// 兜底：non-None skill 始终以 `skills:\n- name` 名单形式出现在模型上下文。
//
// 两段结构（与 claudecli buildSkillSection 对称）：
//  1. name-list：所有 Mode.Effective()!=None 的 skill 名都会列出，即便 render
//     因 Prompt/Summary 为空而返回 ok=false —— 避免手动选中的 skill 在
//     PrepareTurn 未完成 hydrate 时被 provider silent drop。
//  2. block-sections：按 SKILL_WRITER_FORMAT 渲染：
//     - legacy（默认） → [skill:name]\n<body> / [skill:name]\n摘要: ...\n使用方式: ...
//     - v1            → [skill:name::mode@v1]\n...\n[/skill:name::mode@v1]
//     - Mode=None / 非法值 → 跳过（与 name-list 一起剥离，对齐 claudecli buildSkillList）。
func buildSkillPromptInput(skills []dto.SkillRef) (turnInputItem, bool) {
	writerFormat := skillWriterFormat()
	listLines := make([]string, 0, len(skills)+1)
	listLines = append(listLines, "skills:")
	sections := make([]string, 0, len(skills))
	for _, skill := range skills {
		name := strings.TrimSpace(skill.Name)
		if name == "" {
			continue
		}
		// Mode.Effective()：None/invalid 既不进 name-list 也不进 block，
		// 保持与 claudecli session_turn.go:339 buildSkillList 语义一致。
		if skill.Mode.Effective() == dto.SkillModeNone {
			continue
		}
		listLines = append(listLines, "- "+name)
		if block, ok := renderSkillBlock(skill, writerFormat); ok {
			sections = append(sections, block)
		}
	}
	parts := make([]string, 0, 2)
	if len(listLines) > 1 {
		parts = append(parts, strings.Join(listLines, "\n"))
	}
	if len(sections) > 0 {
		parts = append(parts, strings.Join(sections, "\n\n"))
	}
	if len(parts) == 0 {
		return turnInputItem{}, false
	}
	return newTextTurnInput("text", strings.Join(parts, "\n\n")), true
}

func renderSkillBlock(skill dto.SkillRef, writerFormat string) (string, bool) {
	if writerFormat == "v1" {
		block := skillpkg.RenderSkillBlockV1(skill.Name, skill.Prompt, skill.Summary, string(skill.Mode), "1")
		return block, block != ""
	}
	return skillpkg.RenderLegacySkillBlock(skill.Name, skill.Prompt, skill.Summary, string(skill.Mode))
}

func skillWriterFormat() string {
	if strings.EqualFold(strings.TrimSpace(os.Getenv("SKILL_WRITER_FORMAT")), "v1") {
		return "v1"
	}
	return "legacy"
}

func resolveLocalTurnID(requested, fallback string) string {
	if requested = strings.TrimSpace(requested); requested != "" {
		return requested
	}
	return strings.TrimSpace(fallback)
}

// managedMCPBinaries are the singleton sidecar binaries that belong to the
// shared app-server lifecycle.
var managedMCPBinaries = map[string]struct{}{
	"mcp-orch": {},
	"mcp-lsp":  {},
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
func cleanOrphanedMCPProcesses(skipPIDs map[int]struct{}) int {
	allProcs, mcpProcs := discoverProcesses()
	if len(mcpProcs) == 0 {
		return 0
	}

	myTree := buildProcessTree(os.Getpid(), allProcs)

	killed := 0
	for _, proc := range mcpProcs {
		if proc.pid <= 1 {
			continue
		}
		if len(skipPIDs) > 0 {
			if _, skip := skipPIDs[proc.pid]; skip {
				continue
			}
		}
		if _, inTree := myTree[proc.pid]; inTree {
			continue
		}
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

// discoverProcesses runs a single `ps -eo pid,ppid,comm` and returns:
//   - allProcs: map[pid]ppid for the entire process table (used for tree building)
//   - mcpProcs: filtered list of mcp-orch/mcp-lsp entries
func discoverProcesses() (allProcs map[int]int, mcpProcs []mcpProcessInfo) {
	out, err := exec.Command("ps", "-eo", "pid,ppid,comm").Output()
	if err != nil {
		pkglogger.Warn("orphan cleanup: ps command failed", "error", err)
		return nil, nil
	}

	allProcs = make(map[int]int, 256)
	for _, line := range strings.Split(string(out), "\n") {
		pid, ppid, binary, ok := parseProcessLine(line)
		if !ok {
			continue
		}
		allProcs[pid] = ppid
		if binary != "" {
			mcpProcs = append(mcpProcs, mcpProcessInfo{pid: pid, ppid: ppid, binary: binary})
		}
	}
	return allProcs, mcpProcs
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

// buildProcessTree returns all PIDs that are descendants of rootPID
// (including rootPID itself). Uses a children-map + BFS for O(N) traversal.
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

// killMCPProcess terminates a process and its process group.
func killMCPProcess(pid int) error {
	if pid <= 1 {
		return errors.New("refusing to kill PID <= 1")
	}
	pgErr := syscall.Kill(-pid, syscall.SIGKILL)
	if pgErr == nil {
		return nil
	}
	err := syscall.Kill(pid, syscall.SIGKILL)
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}

// mcpOrphanCleanupGracePeriod is the delay after stopping the codex process
// before scanning for residual MCP sidecars. This gives the codex process
// time to propagate SIGTERM to its MCP children.
const mcpOrphanCleanupGracePeriod = 500 * time.Millisecond
