package codexapp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/discovery"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	"github.com/anthropic-ai/super-agent-v3/internal/util/safego"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// peerHandle 抽象一个由 PeerSupervisor 拥有的 peer 进程。
// Wait 只能由 superviseOne 所在 goroutine 调用一次；ClosePipe 发送 EOF 触发优雅退出，Signal 只用于关闭升级。
type peerHandle interface {
	Name() string
	PID() int
	Wait() error
	ClosePipe() error
	Signal(sig processSig) error
}

// peerLauncher 为 supervisor 创建 peerHandle。
// 生产实现负责解析 peer 二进制并启动真实进程，测试可注入纯内存 fake 以覆盖重启和关闭路径。
type peerLauncher interface {
	Launch(ctx context.Context, name string) (peerHandle, error)
}

// peerPIDTracker 是 PeerSupervisor 需要的 PID registry 最小接口。
// 注册和注销由 supervisor 统一负责，测试可替换为 fake，避免依赖真实 /tmp registry 文件。
type peerPIDTracker interface {
	RegisterChecked(pid int, kind string, meta map[string]string) error
	Unregister(pid int)
}

const (
	peerControlProbeAttempts   = 30
	peerControlProbeInterval   = 200 * time.Millisecond
	peerRestartBackoffDefault  = 2 * time.Second
	peerStopGracePeriodDefault = 2 * time.Second
	peerKillGracePeriodDefault = 1 * time.Second
	peerControlAddrEnv         = "GO_AGENT_CTL_RPC_ADDR"
	peerControlAddrDefault     = "127.0.0.1:8090"
	peerBinDirEnv              = "GO_AGENT_PEER_BIN_DIR"
	peerModeEnv                = "GO_AGENT_PEER_MODE"
	peerBootstrapJSONEnv       = "GO_AGENT_CTL_BOOTSTRAP_JSON"
	peerBinaryNameEnv          = "GO_AGENT_CTL_BINARY_NAME"
	peerClientKindEnv          = "GO_AGENT_CTL_CLIENT_KIND"
)

// managedPeerNames 是 Codex app 生命周期拥有的 singleton peer 清单。
// supervisor 启动这些进程，孤儿清理也以同一清单识别残留二进制。
var managedPeerNames = []string{"mcp-orch", "mcp-lsp"}

var managedMCPBinaries = map[string]struct{}{"mcp-orch": {}, "mcp-lsp": {}}

// defaultPeerNames 返回 peer 清单副本。
// 测试通过 WithPeerNames 覆盖时不会改动生产使用的全局定义。
func defaultPeerNames() []string { return append([]string(nil), managedPeerNames...) }

// PeerSupervisor 监管 Codex app 依赖的 mcp-orch/mcp-lsp singleton peer。
// packaged runtime 初始启动失败会 fail-fast；运行中退出会尝试一次重启并在关闭时统一 drain。
type PeerSupervisor struct {
	pidRegistry peerPIDTracker
	logger      *slog.Logger
	launcher    peerLauncher

	peerNames []string

	controlAddr          string
	controlProbeEvery    time.Duration
	controlProbeAttempts int

	restartBackoff time.Duration
	stopGrace      time.Duration
	killGrace      time.Duration

	// cleanupHook 是关闭最后一步的 discovery 文件清理，测试可替换以避免真实 /tmp 副作用。
	cleanupHook func()

	workspaceRoots func() []string

	mu    sync.Mutex
	peers []peerHandle
}

// PeerSupervisorOption 调整 PeerSupervisor 的启动、探测和关闭策略。
// 生产路径通常使用默认值，测试通过 option 注入 launcher、tracker 和时间参数。
type PeerSupervisorOption func(*PeerSupervisor)

// WithPeerLauncher 替换 peer 启动器。
// 主要用于测试注入 fake launcher，生产路径默认使用 execPeerLauncher。
func WithPeerLauncher(l peerLauncher) PeerSupervisorOption {
	return func(s *PeerSupervisor) { s.launcher = l }
}

// WithPeerNames 覆盖需要监管的 peer 名称集合。
// 空白名称会被过滤，避免 launcher 收到无效二进制名。
func WithPeerNames(names []string) PeerSupervisorOption {
	return func(s *PeerSupervisor) {
		out := make([]string, 0, len(names))
		for _, n := range names {
			if n = strings.TrimSpace(n); n != "" {
				out = append(out, n)
			}
		}
		s.peerNames = out
	}
}

// WithPeerRestartBackoff 设置 superviseOne 重启失败后的等待时间。
func WithPeerRestartBackoff(d time.Duration) PeerSupervisorOption {
	return func(s *PeerSupervisor) { s.restartBackoff = d }
}

// WithPeerStopGrace 设置 peer 优雅停止与强杀之间的两个宽限期。
func WithPeerStopGrace(stop, kill time.Duration) PeerSupervisorOption {
	return func(s *PeerSupervisor) {
		s.stopGrace, s.killGrace = stop, kill
	}
}

// WithPeerControlProbe 设置控制面探测地址、间隔和次数。
// attempts 允许为 0，表示完全跳过 best-effort 探测。
func WithPeerControlProbe(addr string, every time.Duration, attempts int) PeerSupervisorOption {
	return func(s *PeerSupervisor) {
		if addr != "" {
			s.controlAddr = addr
		}
		if every > 0 {
			s.controlProbeEvery = every
		}
		if attempts >= 0 {
			s.controlProbeAttempts = attempts
		}
	}
}

// WithPeerCleanupHook 替换最终 discovery 文件清理步骤。
// 生产使用 cleanPeerDiscoveryFiles，测试用 hook 断言 shutdown 路径被调用。
func WithPeerCleanupHook(fn func()) PeerSupervisorOption {
	return func(s *PeerSupervisor) { s.cleanupHook = fn }
}

// WithPeerPIDTracker 替换默认 PID registry。
// supervisor 会在启动、替换和关闭时维护注册记录，外部不应重复登记同一 peer。
func WithPeerPIDTracker(t peerPIDTracker) PeerSupervisorOption {
	return func(s *PeerSupervisor) { s.pidRegistry = t }
}

// WithPeerWorkspaceRoots 为 execPeerLauncher 提供传给 LSP peer 的 workspace roots。
func WithPeerWorkspaceRoots(fn func() []string) PeerSupervisorOption {
	return func(s *PeerSupervisor) { s.workspaceRoots = fn }
}

// NewPeerSupervisor 构造生产使用的 peer supervisor。
// 实际初始化集中到 NewPeerSupervisorWithOptions，便于测试覆盖所有可变依赖。
func NewPeerSupervisor(mgr *ServerManager, logger *slog.Logger, opts ...PeerSupervisorOption) *PeerSupervisor {
	return NewPeerSupervisorWithOptions(mgr, logger, opts...)
}

// NewPeerSupervisorWithOptions 构造可注入依赖的 peer supervisor。
// mgr 可以为 nil；缺省 logger、launcher 和 cleanupHook 会在这里补齐。
func NewPeerSupervisorWithOptions(mgr *ServerManager, logger *slog.Logger, opts ...PeerSupervisorOption) *PeerSupervisor {
	var reg peerPIDTracker
	if mgr != nil && mgr.pidRegistry != nil {
		reg = mgr.pidRegistry
	}
	s := &PeerSupervisor{pidRegistry: reg, logger: logger, peerNames: defaultPeerNames(), controlAddr: peerControlAddrDefault, controlProbeEvery: peerControlProbeInterval, controlProbeAttempts: peerControlProbeAttempts, restartBackoff: peerRestartBackoffDefault, stopGrace: peerStopGracePeriodDefault, killGrace: peerKillGracePeriodDefault, cleanupHook: cleanPeerDiscoveryFiles}
	if env := strings.TrimSpace(os.Getenv(peerControlAddrEnv)); env != "" {
		s.controlAddr = env
	}
	for _, opt := range opts {
		opt(s)
	}
	if s.logger == nil {
		s.logger = pkglogger.Get()
	}
	if s.launcher == nil {
		s.launcher = newExecPeerLauncher(s.logger)
	}
	if launcher, ok := s.launcher.(*execPeerLauncher); ok {
		launcher.workspaceRoots = s.workspaceRoots
	}
	return s
}

var _ platformrunner.Runner = (*PeerSupervisor)(nil)

// Run 启动并监管 Codex app 依赖的 singleton peer 进程。
// packaged 模式下初始启动失败会 fail-fast；开发模式保留 best-effort 跳过语义。
func (s *PeerSupervisor) Run(ctx context.Context) error {
	s.probeControlPlane(ctx)

	superviseCtx, cancelSupervise := context.WithCancelCause(ctx)
	defer cancelSupervise(nil)
	var wg sync.WaitGroup
	for _, name := range s.peerNames {
		s.clearPeerDiscovery(name)
		h, err := s.launcher.Launch(superviseCtx, name)
		if err != nil {
			if strings.TrimSpace(os.Getenv(sidecarRuntimeModeEnv)) == "packaged" {
				launchErr := fmt.Errorf("packaged peer launch failed: %s: %w", name, err)
				return s.stopAfterPeerError(launchErr, cancelSupervise, &wg)
			}
			s.logger.Warn("peer_supervisor: initial launch failed, peer skipped", "peer", name, "error", err)
			continue
		}
		if err := s.trackPeer(h); err != nil {
			s.abortUnregisteredPeer(h)
			return s.stopAfterPeerError(err, cancelSupervise, &wg)
		}
		s.startSuperviseOne(superviseCtx, name, h, &wg, cancelSupervise)
	}

	<-superviseCtx.Done()
	if ctx.Err() == nil {
		cause := context.Cause(superviseCtx)
		if sdErr := s.shutdown(&wg); sdErr != nil {
			return errors.Join(cause, sdErr)
		}
		return cause
	}
	cancelSupervise(context.Canceled)
	if err := s.shutdown(&wg); err != nil {
		return err
	}
	return ctx.Err()
}

func (s *PeerSupervisor) stopAfterPeerError(err error, cancel context.CancelCauseFunc, wg *sync.WaitGroup) error {
	cancel(err)
	if sdErr := s.shutdown(wg); sdErr != nil {
		return errors.Join(err, sdErr)
	}
	return err
}

// probeControlPlane 按配置探测宿主控制面是否已可连接。
// 探测失败不阻断 peer 启动，peer 本身仍会通过 bootstrap 环境拿到控制面地址并自行重试。
func (s *PeerSupervisor) probeControlPlane(ctx context.Context) {
	if s.controlProbeAttempts == 0 {
		return
	}
	for i := 0; i < s.controlProbeAttempts; i++ {
		if ctx.Err() != nil {
			return
		}
		controlAddr := s.currentControlAddr()
		if strings.TrimSpace(controlAddr) == "" {
			continue
		}
		dialer := net.Dialer{Timeout: s.controlProbeEvery}
		conn, err := dialer.DialContext(ctx, "tcp", controlAddr)
		if err == nil {
			_ = conn.Close()
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(s.controlProbeEvery):
		}
	}
}

func (s *PeerSupervisor) currentControlAddr() string {
	if env := strings.TrimSpace(os.Getenv(peerControlAddrEnv)); env != "" {
		s.controlAddr = env
	}
	return strings.TrimSpace(s.controlAddr)
}

// superviseOne 监管单个 peer 的退出和重启。
// ctx 结束时只走关闭路径；运行中异常退出会清 discovery 文件并按退避重启一次。
func (s *PeerSupervisor) superviseOne(ctx context.Context, name string, initial peerHandle, wg *sync.WaitGroup) {
	s.superviseOneWithCancel(ctx, name, initial, wg, func(error) {})
}

func (s *PeerSupervisor) startSuperviseOne(ctx context.Context, name string, h peerHandle, wg *sync.WaitGroup, cancel context.CancelCauseFunc) {
	wg.Add(1)
	safego.Go(ctx, nil, "codexapp.peerSupervisor.superviseOne."+name, func(context.Context) {
		s.superviseOneWithCancel(ctx, name, h, wg, cancel)
	})
}

// waitPeerAsync 在独立 goroutine 里调用 h.Wait()，panic 时将错误写入 ch 并记录日志，保证 ch 必然收到值。
func (s *PeerSupervisor) waitPeerAsync(name string, h peerHandle, ch chan<- error) {
	safego.Go(context.Background(), nil, "codexapp.peerSupervisor.waitPeer."+name, func(context.Context) {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("peer_supervisor: panic in Wait goroutine", "peer", name, "panic", r)
				ch <- fmt.Errorf("panic in peer Wait: %v", r)
			}
		}()
		ch <- h.Wait()
	})
}

// superviseOneWithCancel 在重启注册失败时取消 supervisor，让 Run 统一关闭已托管 peer 并返回错误。
func (s *PeerSupervisor) superviseOneWithCancel(ctx context.Context, name string, initial peerHandle, wg *sync.WaitGroup, cancel context.CancelCauseFunc) {
	defer wg.Done()
	current := initial
	for {
		waitCh := make(chan error, 1)
		s.waitPeerAsync(name, current, waitCh)

		select {
		case <-ctx.Done():
			s.closePeerPipe(current)
			s.waitForPeerAfterCancel(name, current, waitCh)
			return
		case waitErr := <-waitCh:
			s.logger.Warn("peer_supervisor: peer exited, scheduling restart", "peer", name, "pid", current.PID(), "error", waitErr)
			s.clearPeerDiscovery(name)
		}

		timer := time.NewTimer(s.restartBackoff)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return
		case <-timer.C:
		}
		if ctx.Err() != nil {
			return
		}

		s.clearPeerDiscovery(name)
		next, err := s.launcher.Launch(ctx, name)
		if err != nil {
			s.logger.Warn("peer_supervisor: restart failed, peer degraded until shutdown", "peer", name, "error", err)
			<-ctx.Done()
			return
		}
		if err := s.replacePeer(current, next); err != nil {
			s.abortUnregisteredPeer(next)
			cancel(err)
			return
		}
		current = next
	}
}

func (s *PeerSupervisor) clearPeerDiscovery(name string) {
	if err := discovery.CleanupDiscoveryFile(name, os.Getpid()); err != nil && !os.IsNotExist(err) {
		s.logger.Debug("peer_supervisor: discovery cleanup failed", "peer", name, "error", err)
	}
}

// trackPeer 记录新启动的 peer 并登记 PID。
// PID registry 是崩溃后孤儿清理依据，因此只在 handle 有有效 PID 时写入。
func (s *PeerSupervisor) trackPeer(h peerHandle) error {
	if err := s.registerPeerPID(h); err != nil {
		return err
	}
	s.mu.Lock()
	s.peers = append(s.peers, h)
	s.mu.Unlock()
	return nil
}

// replacePeer 用重启后的 handle 替换旧 peer。
// 替换同时维护 PID registry，避免旧 PID 残留导致后续清理误判。
func (s *PeerSupervisor) replacePeer(old, next peerHandle) error {
	if err := s.registerPeerPID(next); err != nil {
		s.removePeer(old)
		s.unregisterPeerPIDs([]peerHandle{old})
		return err
	}
	s.mu.Lock()
	for i, h := range s.peers {
		if h == old {
			s.peers[i] = next
			break
		}
	}
	s.mu.Unlock()
	s.unregisterPeerPIDs([]peerHandle{old})
	return nil
}

func (s *PeerSupervisor) registerPeerPID(h peerHandle) error {
	if s.pidRegistry != nil && h != nil && h.PID() > 0 {
		return s.pidRegistry.RegisterChecked(h.PID(), h.Name(), nil)
	}
	return nil
}

func (s *PeerSupervisor) removePeer(target peerHandle) {
	s.mu.Lock()
	if i := slices.Index(s.peers, target); i >= 0 {
		s.peers = slices.Delete(s.peers, i, i+1)
	}
	s.mu.Unlock()
}

func (s *PeerSupervisor) abortUnregisteredPeer(h peerHandle) {
	if h == nil {
		return
	}
	_ = h.ClosePipe()
	_ = h.Signal(sigForceKill)
	safego.Go(context.Background(), nil, "codexapp.peerSupervisor.abortUnregisteredPeer", func(context.Context) { _ = h.Wait() })
}

// shutdown 执行 EOF、SIGTERM、SIGKILL 的 peer 关闭升级流程。
// 它等待 superviseOne goroutine 汇合，成功后注销 PID 并清理 discovery 文件，不再触发重启。
func (s *PeerSupervisor) shutdown(wg *sync.WaitGroup) error {
	peers := s.snapshotPeers()
	s.closePeerPipes(peers)
	err := s.drainOrEscalate(peers, wg)
	if err == nil {
		s.unregisterPeerPIDs(peers)
		if s.cleanupHook != nil {
			s.cleanupHook()
		}
	}
	return err
}

// closePeerPipes 向所有 peer 的 stdin 发送 EOF。
// peer 二进制把 EOF 视为优雅退出信号，失败只记录在单个 handle 的关闭路径中。
func (s *PeerSupervisor) closePeerPipes(peers []peerHandle) {
	for _, h := range peers {
		s.closePeerPipe(h)
	}
}

func (s *PeerSupervisor) closePeerPipe(h peerHandle) {
	if h == nil {
		return
	}
	if err := h.ClosePipe(); err != nil && !errors.Is(err, os.ErrClosed) {
		s.logger.Debug("peer_supervisor: close stdin pipe", "peer", h.Name(), "error", err)
	}
}

// drainOrEscalate 等待 peer 优雅退出，超时后依次升级 SIGTERM 和 SIGKILL。
// 三段等待后仍未汇合会返回 timeout，调用方据此保留 PID registry 供下次启动清理。
func (s *PeerSupervisor) drainOrEscalate(peers []peerHandle, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	s.waitSupervisorsAsync(wg, done)
	select {
	case <-done:
		return nil
	case <-time.After(s.stopGrace):
	}
	s.signalAllPeers(peers, sigTerminate)
	select {
	case <-done:
		return nil
	case <-time.After(s.killGrace):
	}
	s.signalAllPeers(peers, sigForceKill)
	select {
	case <-done:
		return nil
	case <-time.After(s.killGrace):
		return fmt.Errorf("peer_supervisor shutdown timeout: %d peer(s) did not exit after EOF, SIGTERM, and SIGKILL", len(peers))
	}
}

func (s *PeerSupervisor) waitSupervisorsAsync(wg *sync.WaitGroup, done chan<- struct{}) {
	safego.Go(context.Background(), nil, "codexapp.peerSupervisor.drain", func(context.Context) {
		defer func() {
			if r := recover(); r != nil {
				s.logger.Error("peer_supervisor: panic in drainOrEscalate goroutine", "panic", r)
				close(done)
			}
		}()
		wg.Wait()
		close(done)
	})
}

func (s *PeerSupervisor) signalAllPeers(peers []peerHandle, sig processSig) {
	for _, h := range peers {
		if err := h.Signal(sig); err != nil {
			s.logger.Debug("peer_supervisor: signal failed", "peer", h.Name(), "signal", sig, "error", err)
		}
	}
}

func (s *PeerSupervisor) unregisterPeerPIDs(peers []peerHandle) {
	if s.pidRegistry == nil {
		return
	}
	for _, h := range peers {
		if h == nil {
			continue
		}
		if pid := h.PID(); pid > 0 {
			s.pidRegistry.Unregister(pid)
		}
	}
}

func (s *PeerSupervisor) snapshotPeers() []peerHandle {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.peers)
}

// execPeerLauncher 是生产 peerLauncher。
// 它解析 peer 二进制、注入 peer mode 环境并独立启动进程；PID 注册仍由 PeerSupervisor 统一维护。
type execPeerLauncher struct {
	logger         *slog.Logger
	workspaceRoots func() []string
}

func newExecPeerLauncher(logger *slog.Logger) *execPeerLauncher {
	return &execPeerLauncher{logger: defaultCodexAppLogger(logger)}
}

// Launch 启动指定 peer 二进制并返回可监管 handle。
// stdin pipe 由 supervisor 持有用于优雅关闭，启动失败会关闭已创建的 pipe，避免文件描述符泄漏。
func (l *execPeerLauncher) Launch(_ context.Context, name string) (peerHandle, error) {
	binDirs, err := resolvePeerBinDirs()
	if err != nil {
		return nil, err
	}
	binPath, ok := findPeerBinary(binDirs, name)
	if !ok {
		return nil, &peerBinaryMissingError{Name: name, Dirs: binDirs}
	}
	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	env, err := l.peerEnvForTest(name, os.Environ())
	if err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return nil, err
	}
	cmd := exec.Command(binPath)
	cmd.Stdin = stdinR
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	cmd.Env = env
	setCodexProcessAttrs(cmd)
	if err := cmd.Start(); err != nil {
		_ = stdinR.Close()
		_ = stdinW.Close()
		return nil, err
	}
	_ = stdinR.Close()
	guard := attachProcessGuard(cmd)
	l.logger.Info("peer_supervisor: started", "peer", name, "pid", cmd.Process.Pid, "mode", "peer")
	return &execPeerHandle{name: name, cmd: cmd, guard: guard, stdin: stdinW}, nil
}

type peerBinaryMissingError struct {
	Name string
	Dirs []string
}

// Error 返回 peer 二进制缺失的诊断文本。
// 文本包含候选目录，便于 packaged 环境定位打包或 PATH 问题。
func (e *peerBinaryMissingError) Error() string {
	return "peer binary not found: " + e.Name + " in " + strings.Join(e.Dirs, string(os.PathListSeparator))
}

type execPeerHandle struct {
	name       string
	cmd        *exec.Cmd
	guard      *processGuard
	stdin      *os.File
	mu         sync.Mutex
	pipeClosed bool
}

// Name 返回该 handle 对应的 peer 名称。
func (h *execPeerHandle) Name() string { return h.name }

// PID 返回底层进程 ID。
// 进程尚未启动或 handle 已损坏时返回 0，调用方据此跳过 PID registry 操作。
func (h *execPeerHandle) PID() int {
	if h.cmd != nil && h.cmd.Process != nil {
		return h.cmd.Process.Pid
	}
	return 0
}

// Wait 等待 peer 进程退出并关闭进程守卫。
// 该方法必须单次调用；重复等待由 os/exec 语义决定为错误。
func (h *execPeerHandle) Wait() error {
	if h.cmd == nil {
		return errors.New("peer_supervisor: execPeerHandle with nil cmd")
	}
	err := h.cmd.Wait()
	h.guard.close()
	return err
}

// ClosePipe 关闭 peer stdin，向进程发送 EOF。
// 方法内部幂等，shutdown 与 supervise cancel 同时触发时不会重复关闭文件。
func (h *execPeerHandle) ClosePipe() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.pipeClosed || h.stdin == nil {
		return nil
	}
	h.pipeClosed = true
	return h.stdin.Close()
}

// Signal 向底层进程发送信号。
func (h *execPeerHandle) Signal(sig processSig) error {
	if h.cmd != nil && h.cmd.Process != nil {
		return signalCodexProcess(h.cmd, h.guard, sig)
	}
	return nil
}
