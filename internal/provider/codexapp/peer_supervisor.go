package codexapp

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/platform/discovery"
	platformrunner "github.com/anthropic-ai/super-agent-v3/internal/platform/runner"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

// peerHandle abstracts one running peer process so PeerSupervisor can be
// exercised without real exec.Cmd spawns. Production uses execPeerHandle
// (exec.Cmd + stdin write-pipe); tests inject a fake via peerLauncher.
//
// Wait() is single-shot and must only be called by the owner goroutine
// (see PeerSupervisor.superviseOne). ClosePipe sends EOF to stdin, which
// every peer binary treats as a graceful shutdown cue. Signal is the
// escalation path used only by PeerSupervisor.shutdown.
type peerHandle interface {
	Name() string
	PID() int
	Wait() error
	ClosePipe() error
	Signal(sig processSig) error
}

// peerLauncher creates peerHandles for the supervisor. A real implementation
// resolves the peer binary via resolvePeerBinDirs + findPeerBinary and
// launches it; the test fake is a pure in-process channel-based stub.
type peerLauncher interface {
	Launch(ctx context.Context, name string) (peerHandle, error)
}

// peerPIDTracker is the narrow subset of pidregistry.Registry that the
// supervisor needs. Declared as an interface so tests can inject a fake
// tracker without a /tmp-backed registry file. *pidregistry.Registry
// satisfies this interface by virtue of its Register/Unregister methods.
type peerPIDTracker interface {
	Register(pid int, kind string, meta map[string]string)
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

// managedPeerNames is the single source of truth for singleton peer binaries
// owned by the codex app lifecycle. The supervisor launches them, orphan
// cleanup matches them, and Windows discovery accepts their .exe variants.
var managedPeerNames = []string{"mcp-orch", "mcp-lsp"}

var managedMCPBinaries = map[string]struct{}{"mcp-orch": {}, "mcp-lsp": {}}

// defaultPeerNames is kept as a function so tests can override via
// WithPeerNames without mutating the lifecycle-owned peer definition.
func defaultPeerNames() []string { return append([]string(nil), managedPeerNames...) }

// PeerSupervisor owns the lifecycle of bundled mcp-orch and mcp-lsp peer processes.
//
// Run keeps dev initial launch failures best-effort, but packaged runtime must
// fail fast when bundled peers cannot start.
type PeerSupervisor struct {
	pidRegistry peerPIDTracker
	logger      *pkglogger.Logger
	launcher    peerLauncher

	peerNames []string

	controlAddr          string
	controlProbeEvery    time.Duration
	controlProbeAttempts int

	restartBackoff time.Duration
	stopGrace      time.Duration
	killGrace      time.Duration

	// cleanupHook is the final shutdown step — discovery-file sweep. Exposed as
	// a field so tests can assert it ran without needing real /tmp side effects.
	cleanupHook func()

	workspaceRoots func() []string

	mu    sync.Mutex
	peers []peerHandle
}

// PeerSupervisorOption customises a PeerSupervisor. Production code uses the
// bare NewPeerSupervisor; tests inject overrides via NewPeerSupervisorWithOptions.
type PeerSupervisorOption func(*PeerSupervisor)

// WithPeerLauncher 设置peer启动器。
func WithPeerLauncher(l peerLauncher) PeerSupervisorOption {
	return func(s *PeerSupervisor) { s.launcher = l }
}

// WithPeerNames 设置peer名称。
func WithPeerNames(names []string) PeerSupervisorOption {
	return func(s *PeerSupervisor) {
		out := make([]string, 0, len(names))
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n != "" {
				out = append(out, n)
			}
		}
		s.peerNames = out
	}
}

// WithPeerRestartBackoff 设置peerrestartbackoff。
func WithPeerRestartBackoff(d time.Duration) PeerSupervisorOption {
	return func(s *PeerSupervisor) { s.restartBackoff = d }
}

// WithPeerStopGrace 设置peerstopgrace。
func WithPeerStopGrace(stop, kill time.Duration) PeerSupervisorOption {
	return func(s *PeerSupervisor) {
		s.stopGrace = stop
		s.killGrace = kill
	}
}

// WithPeerControlProbe 设置peercontrolprobe。
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

// WithPeerCleanupHook 设置peercleanuphook。
func WithPeerCleanupHook(fn func()) PeerSupervisorOption {
	return func(s *PeerSupervisor) { s.cleanupHook = fn }
}

// WithPeerPIDTracker replaces the default pid registry.
// WithPeerPIDTracker 设置peer进程 IDtracker。
func WithPeerPIDTracker(t peerPIDTracker) PeerSupervisorOption {
	return func(s *PeerSupervisor) { s.pidRegistry = t }
}

// WithPeerWorkspaceRoots 设置peer工作区根目录。
func WithPeerWorkspaceRoots(fn func() []string) PeerSupervisorOption {
	return func(s *PeerSupervisor) { s.workspaceRoots = fn }
}

// NewPeerSupervisor is the production constructor.
// NewPeerSupervisor 创建peersupervisor。
func NewPeerSupervisor(mgr *ServerManager, logger *pkglogger.Logger, opts ...PeerSupervisorOption) *PeerSupervisor {
	return NewPeerSupervisorWithOptions(mgr, logger, opts...)
}

// NewPeerSupervisorWithOptions is the test-friendly constructor. mgr may be nil.
// NewPeerSupervisorWithOptions 创建带选项的peersupervisor。
func NewPeerSupervisorWithOptions(mgr *ServerManager, logger *pkglogger.Logger, opts ...PeerSupervisorOption) *PeerSupervisor {
	var reg peerPIDTracker
	if mgr != nil && mgr.pidRegistry != nil {
		reg = mgr.pidRegistry
	}
	s := &PeerSupervisor{
		pidRegistry:          reg,
		logger:               logger,
		peerNames:            defaultPeerNames(),
		controlAddr:          peerControlAddrDefault,
		controlProbeEvery:    peerControlProbeInterval,
		controlProbeAttempts: peerControlProbeAttempts,
		restartBackoff:       peerRestartBackoffDefault,
		stopGrace:            peerStopGracePeriodDefault,
		killGrace:            peerKillGracePeriodDefault,
		cleanupHook:          cleanPeerDiscoveryFiles,
	}
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

// Run implements platformrunner.Runner.
// Run 启动codexapp provider后台流程。
func (s *PeerSupervisor) Run(ctx context.Context) error {
	s.probeControlPlane(ctx)

	superviseCtx, cancelSupervise := context.WithCancel(ctx)
	defer cancelSupervise()
	var wg sync.WaitGroup
	for _, name := range s.peerNames {
		s.clearPeerDiscovery(name)
		h, err := s.launcher.Launch(superviseCtx, name)
		if err != nil {
			if strings.TrimSpace(os.Getenv(sidecarRuntimeModeEnv)) == "packaged" {
				cancelSupervise()
				launchErr := fmt.Errorf("packaged peer launch failed: %s: %w", name, err)
				if sdErr := s.shutdown(&wg); sdErr != nil {
					return errors.Join(launchErr, sdErr)
				}
				return launchErr
			}
			s.logger.Warn("peer_supervisor: initial launch failed, peer skipped",
				"peer", name, "error", err)
			continue
		}
		s.trackPeer(h)
		wg.Add(1)
		go func() {
			defer func() {
				if rec := recover(); rec != nil {
					s.logger.Error("peer_supervisor: recovered superviseOne panic",
						"peer", name, "panic", rec)
					wg.Done()
				}
			}()
			s.superviseOne(superviseCtx, name, h, &wg)
		}()
	}

	<-ctx.Done()
	cancelSupervise()
	if err := s.shutdown(&wg); err != nil {
		return err
	}
	return ctx.Err()
}

// probeControlPlane dials the control RPC address up to controlProbeAttempts
// times, sleeping controlProbeEvery between attempts. It is best-effort by
// design: P1a §需冻结的兼容语义 writes down that a failed probe is NOT a hard
// startup gate; supervisor continues into peer launch regardless.
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

// superviseOne 处理superviseone。
func (s *PeerSupervisor) superviseOne(ctx context.Context, name string, initial peerHandle, wg *sync.WaitGroup) {
	defer wg.Done()
	current := initial
	for {
		waitCh := make(chan error, 1)
		go func(h peerHandle) {
			defer func() { _ = recover() }()
			waitCh <- h.Wait()
		}(current)

		select {
		case <-ctx.Done():
			s.closePeerPipe(current)
			s.waitForPeerAfterCancel(name, current, waitCh)
			return
		case waitErr := <-waitCh:
			s.logger.Warn("peer_supervisor: peer exited, scheduling restart",
				"peer", name, "pid", current.PID(), "error", waitErr)
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
			s.logger.Warn("peer_supervisor: restart failed, peer degraded until shutdown",
				"peer", name, "error", err)
			<-ctx.Done()
			return
		}
		s.replacePeer(current, next)
		current = next
	}
}

func (s *PeerSupervisor) clearPeerDiscovery(name string) {
	if err := discovery.CleanupDiscoveryFile(name, os.Getpid()); err != nil && !os.IsNotExist(err) {
		s.logger.Debug("peer_supervisor: discovery cleanup failed",
			"peer", name, "error", err)
	}
}

// trackPeer appends a new handle.
func (s *PeerSupervisor) trackPeer(h peerHandle) {
	s.mu.Lock()
	s.peers = append(s.peers, h)
	s.mu.Unlock()
	if s.pidRegistry != nil && h.PID() > 0 {
		s.pidRegistry.Register(h.PID(), h.Name(), nil)
	}
}

// replacePeer swaps the current handle.
// replacePeer 替换peer。
func (s *PeerSupervisor) replacePeer(old, next peerHandle) {
	s.mu.Lock()
	for i, h := range s.peers {
		if h == old {
			s.peers[i] = next
			break
		}
	}
	s.mu.Unlock()
	if s.pidRegistry != nil {
		if old != nil && old.PID() > 0 {
			s.pidRegistry.Unregister(old.PID())
		}
		if next != nil && next.PID() > 0 {
			s.pidRegistry.Register(next.PID(), next.Name(), nil)
		}
	}
}

// shutdown runs the stop/drain escalation: EOF -> SIGTERM -> SIGKILL. It
// joins the superviseOne goroutines via wg and never retries re-launches.
// Split into phase helpers so each function stays under the CC guard limit.
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

// closePeerPipes sends EOF to every peer.
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
		s.logger.Debug("peer_supervisor: close stdin pipe",
			"peer", h.Name(), "error", err)
	}
}

// drainOrEscalate sends EOF, SIGTERM, then SIGKILL, returning a timeout if peers still do not drain.
// drainOrEscalate 先等待正常退出，超时后升级终止。
func (s *PeerSupervisor) drainOrEscalate(peers []peerHandle, wg *sync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		defer func() { _ = recover() }()
		wg.Wait()
		close(done)
	}()
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

func (s *PeerSupervisor) signalAllPeers(peers []peerHandle, sig processSig) {
	for _, h := range peers {
		if err := h.Signal(sig); err != nil {
			s.logger.Debug("peer_supervisor: signal failed",
				"peer", h.Name(), "signal", sig, "error", err)
		}
	}
}

func (s *PeerSupervisor) unregisterPeerPIDs(peers []peerHandle) {
	if s.pidRegistry == nil {
		return
	}
	for _, h := range peers {
		if pid := h.PID(); pid > 0 {
			s.pidRegistry.Unregister(pid)
		}
	}
}

func (s *PeerSupervisor) snapshotPeers() []peerHandle {
	s.mu.Lock()
	out := make([]peerHandle, len(s.peers))
	copy(out, s.peers)
	s.mu.Unlock()
	return out
}

// execPeerLauncher is the production peerLauncher. It resolves the peer
// binary, starts it with GO_AGENT_PEER_MODE=1 in its own process group, and
// returns a handle wrapping the exec.Cmd + stdin write-pipe. The supervisor
// owns the pid-registry registration for every handle this launcher produces.
type execPeerLauncher struct {
	logger         *pkglogger.Logger
	workspaceRoots func() []string
}

func newExecPeerLauncher(logger *pkglogger.Logger) *execPeerLauncher {
	if logger == nil {
		logger = pkglogger.Get()
	}
	return &execPeerLauncher{logger: logger}
}

// Launch 启动codexapp provider。
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
	l.logger.Info("peer_supervisor: started",
		"peer", name, "pid", cmd.Process.Pid, "mode", "peer")
	return &execPeerHandle{name: name, cmd: cmd, guard: guard, stdin: stdinW}, nil
}

type peerBinaryMissingError struct {
	Name string
	Dirs []string
}

// Error 返回错误文本。
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

// Name 处理名称。
func (h *execPeerHandle) Name() string { return h.name }

// PID 处理进程 ID。
func (h *execPeerHandle) PID() int {
	if h.cmd != nil && h.cmd.Process != nil {
		return h.cmd.Process.Pid
	}
	return 0
}

// Wait 等待codexapp provider。
func (h *execPeerHandle) Wait() error {
	if h.cmd == nil {
		return errors.New("peer_supervisor: execPeerHandle with nil cmd")
	}
	err := h.cmd.Wait()
	h.guard.close()
	return err
}

// ClosePipe 关闭pipe。
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
