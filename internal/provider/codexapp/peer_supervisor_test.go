package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/discovery"
)

type fakePeerLauncher struct {
	mu sync.Mutex

	launchErr   map[string]error // per-peer launch error; consumed after first Launch unless sticky
	sticky      bool             // if true, launchErr is returned on every call
	launchCount map[string]int
	handles     map[string][]*fakePeerHandle
	pidSeed     int

	launchCh chan string // 1-per-launch notification; tests drain it to await launches
}

func newFakePeerLauncher() *fakePeerLauncher {
	return &fakePeerLauncher{
		launchErr:   map[string]error{},
		launchCount: map[string]int{},
		handles:     map[string][]*fakePeerHandle{},
		pidSeed:     10000,
		launchCh:    make(chan string, 16),
	}
}

func (f *fakePeerLauncher) Launch(_ context.Context, name string) (peerHandle, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.launchCount[name]++
	if err, ok := f.launchErr[name]; ok && err != nil {
		if !f.sticky {
			delete(f.launchErr, name)
		}
		return nil, err
	}
	f.pidSeed++
	h := &fakePeerHandle{name: name, pid: f.pidSeed, done: make(chan struct{})}
	f.handles[name] = append(f.handles[name], h)
	select {
	case f.launchCh <- name:
	default:
	}
	return h, nil
}

func (f *fakePeerLauncher) snapshotCount(name string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.launchCount[name]
}

func (f *fakePeerLauncher) snapshotHandles(name string) []*fakePeerHandle {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.handles[name]
}

func (f *fakePeerLauncher) waitLaunch(t *testing.T, expected string, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case got := <-f.launchCh:
			if got == expected {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for Launch(%q)", expected)
		}
	}
}

type fakePeerHandle struct {
	name string
	pid  int

	mu      sync.Mutex
	done    chan struct{}
	closed  bool
	exitErr error
	signals []processSig
}

func (h *fakePeerHandle) Name() string { return h.name }
func (h *fakePeerHandle) PID() int     { return h.pid }

func (h *fakePeerHandle) Wait() error {
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.exitErr
}

func (h *fakePeerHandle) triggerExit(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return
	}
	h.closed = true
	h.exitErr = err
	close(h.done)
}

func (h *fakePeerHandle) ClosePipe() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return os.ErrClosed
	}
	h.closed = true
	close(h.done)
	h.mu.Unlock()
	return nil
}

func (h *fakePeerHandle) Signal(sig processSig) error {
	h.mu.Lock()
	h.signals = append(h.signals, sig)
	h.mu.Unlock()
	return nil
}

func (h *fakePeerHandle) isClosed() bool { h.mu.Lock(); defer h.mu.Unlock(); return h.closed }

type fakePIDTracker struct {
	mu                                sync.Mutex
	live                              map[int]bool
	registerCalls, failOnRegisterCall int
	registerErr                       error
}

func newFakePIDTracker() *fakePIDTracker { return &fakePIDTracker{live: map[int]bool{}} }

func (f *fakePIDTracker) Register(pid int, _ string, _ map[string]string) {
	_ = f.RegisterChecked(pid, "", nil)
}

func (f *fakePIDTracker) RegisterChecked(pid int, _ string, _ map[string]string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.registerCalls++
	if f.registerErr != nil && f.registerCalls == f.failOnRegisterCall {
		return f.registerErr
	}
	f.live[pid] = true
	return nil
}

func (f *fakePIDTracker) Unregister(pid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, pid)
}

func (f *fakePIDTracker) has(pid int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.live[pid]
}

func newTestSupervisor(t *testing.T, opts ...PeerSupervisorOption) (*PeerSupervisor, *fakePeerLauncher, *fakePIDTracker) {
	t.Helper()
	launcher := newFakePeerLauncher()
	tracker := newFakePIDTracker()
	base := []PeerSupervisorOption{
		WithPeerLauncher(launcher),
		WithPeerPIDTracker(tracker),
		WithPeerNames([]string{"test-peer"}),
		WithPeerRestartBackoff(10 * time.Millisecond),
		WithPeerStopGrace(50*time.Millisecond, 20*time.Millisecond),
		WithPeerControlProbe("127.0.0.1:0", 1*time.Millisecond, 0),
	}
	s := NewPeerSupervisorWithOptions(nil, nil, append(base, opts...)...)
	return s, launcher, tracker
}

func testPeerParentEnv() []string {
	return []string{"PATH=/bin", "GO_AGENT_CTL_SESSION_TOKEN=test-peer-token", "SUPER_DOLPHIN_RUNTIME_MODE=dev", "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=/work/repo"}
}

func runSupervisor(ctx context.Context, s *PeerSupervisor) <-chan error {
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	return done
}

func TestPeerSupervisorStartsPeers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, launcher, _ := newTestSupervisor(t, WithPeerNames([]string{"mcp-orch", "mcp-lsp"}))
	done := runSupervisor(ctx, s)
	launcher.waitLaunch(t, "mcp-orch", time.Second)
	launcher.waitLaunch(t, "mcp-lsp", time.Second)
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run err = %v, want context.Canceled", err)
	}
	requireLaunchCount(t, launcher, "mcp-orch", 1)
	requireLaunchCount(t, launcher, "mcp-lsp", 1)
}

func TestPeerSupervisorPackagedInitialLaunchFailureIsFatal(t *testing.T) {
	t.Setenv(sidecarRuntimeModeEnv, "packaged")
	s, launcher, _ := newTestSupervisor(t, WithPeerNames([]string{"mcp-orch"}))
	setLaunchErr(launcher, "mcp-orch", errors.New("missing peer binary"))
	requireErrContains(t, s.Run(context.Background()), "packaged peer launch failed", "mcp-orch", "missing peer binary")
	requireLaunchCount(t, launcher, "mcp-orch", 1)
}

func TestPeerSupervisorDevInitialLaunchFailureStaysBestEffort(t *testing.T) {
	t.Setenv(sidecarRuntimeModeEnv, "dev")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, launcher, _ := newTestSupervisor(t, WithPeerNames([]string{"mcp-orch"}))
	setLaunchErr(launcher, "mcp-orch", errors.New("missing peer binary"))
	requireRunningBeforeCancel(t, runSupervisor(ctx, s), cancel)
	requireLaunchCount(t, launcher, "mcp-orch", 1)
}

func TestPeerSupervisorPackagedInitialLaunchFailureStopsAlreadyStartedPeers(t *testing.T) {
	t.Setenv(sidecarRuntimeModeEnv, "packaged")
	s, launcher, tracker := newTestSupervisor(t, WithPeerNames([]string{"mcp-orch", "mcp-lsp"}))
	setLaunchErr(launcher, "mcp-lsp", errors.New("lsp unavailable"))
	requireErrContains(t, s.Run(context.Background()), "packaged peer launch failed")
	handles := launcher.snapshotHandles("mcp-orch")
	if len(handles) != 1 || !handles[0].isClosed() || tracker.has(handles[0].PID()) {
		t.Fatalf("mcp-orch cleanup failed: handles=%d closed=%v live=%v", len(handles), len(handles) == 1 && handles[0].isClosed(), len(handles) == 1 && tracker.has(handles[0].PID()))
	}
	requireLaunchCount(t, launcher, "mcp-lsp", 1)
	requireLaunchCount(t, launcher, "mcp-orch", 1)
}

func setLaunchErr(launcher *fakePeerLauncher, name string, err error) {
	launcher.mu.Lock()
	launcher.launchErr[name] = err
	launcher.mu.Unlock()
}

func requireErrContains(t *testing.T, err error, wants ...string) {
	t.Helper()
	if err == nil {
		t.Fatal("Run err = nil, want failure")
	}
	for _, want := range wants {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Run err = %v, want contains %q", err, want)
		}
	}
}

func requireRunningBeforeCancel(t *testing.T, done <-chan error, cancel context.CancelFunc) {
	t.Helper()
	select {
	case err := <-done:
		t.Fatalf("Run returned before cancel: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}
}

func requireLaunchCount(t *testing.T, launcher *fakePeerLauncher, name string, want int) {
	t.Helper()
	if got := launcher.snapshotCount(name); got != want {
		t.Fatalf("%s launch count = %d, want %d", name, got, want)
	}
}
func TestPeerProcessEnvInjectsConfiguredMcpLSPWorkspaceRoots(t *testing.T) {
	root := t.TempDir()
	rawRoots, err := json.Marshal([]string{root})
	if err != nil {
		t.Fatalf("Marshal roots: %v", err)
	}

	env, err := peerProcessEnv("mcp-lsp", testPeerParentEnv(), []string{root})
	if err != nil {
		t.Fatalf("peerProcessEnv() error = %v", err)
	}
	requireEnvValue(t, env, "GO_AGENT_LSP_ROOT", root)
	requireEnvValue(t, env, "GO_AGENT_LSP_ROOTS", string(rawRoots))
}

func TestPeerProcessEnvRequiresSessionToken(t *testing.T) {
	_, err := peerProcessEnv("mcp-orch", []string{"PATH=/bin"}, nil)
	if err == nil || !strings.Contains(err.Error(), "GO_AGENT_CTL_SESSION_TOKEN") {
		t.Fatalf("peerProcessEnv() error = %v, want visible missing session token failure", err)
	}
}
func TestPeerProcessEnvCarriesLegacySessionTokenAsCanonical(t *testing.T) {
	env, err := peerProcessEnv("mcp-orch", []string{"PATH=/bin", "GO_AGENT_MCP_SESSION_TOKEN=legacy-token", "SUPER_DOLPHIN_RUNTIME_MODE=dev", "SUPER_DOLPHIN_RUNTIME_RESOURCES_DIR=/work/repo", "GO_AGENT_CTL_BINARY_NAME=super-agent-debug.exe", "GO_AGENT_CTL_CLIENT_KIND=custom"}, nil)
	if err != nil {
		t.Fatalf("peerProcessEnv() error = %v", err)
	}
	requireEnvValue(t, env, "GO_AGENT_CTL_SESSION_TOKEN", "legacy-token")
}
func TestPeerProcessEnvRejectsExplicitInvalidMcpLSPRoots(t *testing.T) {
	_, err := peerProcessEnv("mcp-lsp", append(testPeerParentEnv(), "GO_AGENT_LSP_ROOTS=not-json"), nil)
	if err == nil || !strings.Contains(err.Error(), "GO_AGENT_LSP_ROOTS") {
		t.Fatalf("peerProcessEnv() error = %v, want explicit roots parse failure", err)
	}
}

func TestProvideDefaultPeerSupervisorWiresProjectRootIntoMcpLSPLauncher(t *testing.T) {
	root := t.TempDir()
	runner := provideDefaultPeerSupervisor(nil, nil, &contract.Config{ProjectRoot: root})
	supervisor, ok := runner.(*PeerSupervisor)
	if !ok {
		t.Fatalf("runner type = %T, want *PeerSupervisor", runner)
	}
	launcher, ok := supervisor.launcher.(*execPeerLauncher)
	if !ok {
		t.Fatalf("launcher type = %T, want *execPeerLauncher", supervisor.launcher)
	}
	env, err := launcher.peerEnvForTest("mcp-lsp", testPeerParentEnv())
	if err != nil {
		t.Fatalf("peer env error = %v", err)
	}
	requireEnvValue(t, env, "GO_AGENT_LSP_ROOT", root)
	var roots []string
	if err := json.Unmarshal([]byte(requireEnvString(t, env, "GO_AGENT_LSP_ROOTS")), &roots); err != nil {
		t.Fatalf("decode GO_AGENT_LSP_ROOTS: %v", err)
	}
	if len(roots) != 1 || roots[0] != root {
		t.Fatalf("GO_AGENT_LSP_ROOTS = %#v, want [%q]", roots, root)
	}
}

func TestProvideDefaultPeerSupervisorRejectsMissingProjectRootForMcpLSP(t *testing.T) {
	runner := provideDefaultPeerSupervisor(nil, nil, &contract.Config{ProjectRoot: "relative"})
	supervisor := runner.(*PeerSupervisor)
	launcher := supervisor.launcher.(*execPeerLauncher)
	_, err := launcher.peerEnvForTest("mcp-lsp", testPeerParentEnv())
	if err == nil || !strings.Contains(err.Error(), "workspace root") {
		t.Fatalf("peer env error = %v, want visible workspace-root failure", err)
	}
}

func requireEnvValue(t *testing.T, env []string, key, want string) {
	t.Helper()
	if got := requireEnvString(t, env, key); got != want {
		t.Fatalf("%s = %q, want %q", key, got, want)
	}
}

func requireEnvString(t *testing.T, env []string, key string) string {
	t.Helper()
	prefix := key + "="
	for _, item := range env {
		if strings.HasPrefix(item, prefix) {
			return strings.TrimPrefix(item, prefix)
		}
	}
	t.Fatalf("%s missing from env %#v", key, env)
	return ""
}

func TestPeerSupervisorRestartsExitedPeer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, launcher, _ := newTestSupervisor(t)
	done := runSupervisor(ctx, s)

	launcher.waitLaunch(t, "test-peer", time.Second)
	handles := launcher.snapshotHandles("test-peer")
	if len(handles) != 1 {
		t.Fatalf("handles len = %d, want 1", len(handles))
	}
	handles[0].triggerExit(errors.New("boom"))

	launcher.waitLaunch(t, "test-peer", time.Second)

	cancel()
	<-done

	if got := launcher.snapshotCount("test-peer"); got != 2 {
		t.Errorf("launch count = %d, want 2", got)
	}
}

func TestPeerSupervisorClearsDiscoveryOnPeerExitBeforeRestart(t *testing.T) {
	const peerName = "test-peer"
	if err := discovery.WriteDiscoveryFile(peerName, os.Getpid(), "127.0.0.1:65535"); err != nil {
		t.Fatalf("WriteDiscoveryFile() error = %v", err)
	}
	t.Cleanup(func() { _ = discovery.CleanupDiscoveryFile(peerName, os.Getpid()) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, launcher, _ := newTestSupervisor(t)
	done := runSupervisor(ctx, s)

	launcher.waitLaunch(t, peerName, time.Second)
	first := launcher.snapshotHandles(peerName)[0]
	first.triggerExit(errors.New("peer crashed"))

	waitUntil(t, time.Second, func() bool {
		_, err := discovery.ReadDiscoveryAddr(peerName, os.Getpid())
		return os.IsNotExist(err)
	}, "discovery file removed before restart")
	launcher.waitLaunch(t, peerName, time.Second)
	cancel()
	<-done
}

func TestPeerSupervisorClearsDiscoveryOnRestartFailure(t *testing.T) {
	const peerName = "test-peer"
	if err := discovery.WriteDiscoveryFile(peerName, os.Getpid(), "127.0.0.1:65535"); err != nil {
		t.Fatalf("WriteDiscoveryFile() error = %v", err)
	}
	t.Cleanup(func() { _ = discovery.CleanupDiscoveryFile(peerName, os.Getpid()) })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s, launcher, _ := newTestSupervisor(t)
	done := runSupervisor(ctx, s)

	launcher.waitLaunch(t, peerName, time.Second)
	launcher.mu.Lock()
	launcher.launchErr[peerName] = errors.New("restart unavailable")
	launcher.mu.Unlock()
	first := launcher.snapshotHandles(peerName)[0]
	first.triggerExit(errors.New("peer crashed"))

	waitUntil(t, time.Second, func() bool {
		_, err := discovery.ReadDiscoveryAddr(peerName, os.Getpid())
		return os.IsNotExist(err)
	}, "discovery file removed after restart failure")
	cancel()
	<-done
}

func TestPeerSupervisorShutdownSuppressesRestart(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	s, launcher, _ := newTestSupervisor(t,
		WithPeerRestartBackoff(500*time.Millisecond),
	)
	done := runSupervisor(ctx, s)
	launcher.waitLaunch(t, "test-peer", time.Second)

	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}

	if got := launcher.snapshotCount("test-peer"); got != 1 {
		t.Errorf("launch count = %d, want 1 (no restart after shutdown)", got)
	}
}

func TestPeerSupervisorReplacePeerAtomicallySwapsBookkeeping(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, launcher, tracker := newTestSupervisor(t)
	done := runSupervisor(ctx, s)

	launcher.waitLaunch(t, "test-peer", time.Second)
	first := launcher.snapshotHandles("test-peer")[0]
	if !tracker.has(first.PID()) {
		t.Fatalf("pid %d should be registered after initial launch", first.PID())
	}

	first.triggerExit(errors.New("boom"))
	launcher.waitLaunch(t, "test-peer", time.Second)

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if !tracker.has(first.PID()) {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	second := launcher.snapshotHandles("test-peer")[1]
	if tracker.has(first.PID()) {
		t.Errorf("old pid %d should have been unregistered on replace", first.PID())
	}
	if !tracker.has(second.PID()) {
		t.Errorf("new pid %d should be registered after replace", second.PID())
	}

	cancel()
	<-done

	if tracker.has(second.PID()) {
		t.Errorf("pid %d should be unregistered after shutdown", second.PID())
	}
}

func TestPeerSupervisorBackoffCancelledByStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	s, launcher, _ := newTestSupervisor(t,
		WithPeerRestartBackoff(2*time.Second),
	)
	done := runSupervisor(ctx, s)
	launcher.waitLaunch(t, "test-peer", time.Second)

	launcher.snapshotHandles("test-peer")[0].triggerExit(errors.New("boom"))

	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Run did not return within 1s — backoff was not cancelled")
	}

	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("shutdown took %v; backoff should have been cancelled promptly", elapsed)
	}
	if got := launcher.snapshotCount("test-peer"); got != 1 {
		t.Errorf("launch count = %d, want 1 (no restart because backoff was cancelled)", got)
	}
}

func TestPeerSupervisorDiscoveryFilesRemovedOnStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	testPeerName := "test-peer-discovery"
	if err := discovery.WriteDiscoveryFile(testPeerName, os.Getpid(), "127.0.0.1:65432"); err != nil {
		t.Fatalf("seed discovery file: %v", err)
	}

	addr, err := discovery.ReadDiscoveryAddr(testPeerName, os.Getpid())
	if err != nil || addr != "127.0.0.1:65432" {
		t.Fatalf("seeded discovery file missing: addr=%q err=%v", addr, err)
	}

	s, launcher, _ := newTestSupervisor(t,
		WithPeerCleanupHook(func() {
			_ = discovery.CleanupDiscoveryFile(testPeerName, os.Getpid())
		}),
	)
	done := runSupervisor(ctx, s)
	launcher.waitLaunch(t, "test-peer", time.Second)

	cancel()
	<-done

	if _, err := discovery.ReadDiscoveryAddr(testPeerName, os.Getpid()); err == nil {
		t.Error("discovery file still exists after supervisor shutdown; cleanup hook did not run")
	}
}

func TestPeerSupervisorShutdownEscalatesToSIGTERM(t *testing.T) {
	stuck := &stuckPeerHandle{name: "stuck-peer", pid: 99999, done: make(chan struct{})}
	launcher := newFakePeerLauncher()
	launcher.mu.Lock()
	launcher.handles["test-peer"] = []*fakePeerHandle{}
	launcher.mu.Unlock()

	s := NewPeerSupervisorWithOptions(nil, nil,
		WithPeerLauncher(&singleHandleLauncher{h: stuck, launchCh: make(chan struct{}, 1)}),
		WithPeerPIDTracker(newFakePIDTracker()),
		WithPeerNames([]string{"test-peer"}),
		WithPeerRestartBackoff(10*time.Millisecond),
		WithPeerStopGrace(30*time.Millisecond, 20*time.Millisecond),
		WithPeerControlProbe("127.0.0.1:0", 1*time.Millisecond, 0),
		WithPeerCleanupHook(func() {}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := runSupervisor(ctx, s)

	waitUntil(t, time.Second, func() bool {
		return stuck.registered()
	}, "stuck peer never tracked")

	cancel()
	waitUntil(t, time.Second, func() bool {
		return stuck.hasSignal(sigTerminate)
	}, "stuck peer never received SIGTERM")

	stuck.triggerExit(nil)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after SIGKILL")
	}
}

type stuckPeerHandle struct {
	name    string
	pid     int
	mu      sync.Mutex
	done    chan struct{}
	reg     bool
	signals []processSig
}

func (s *stuckPeerHandle) Name() string { return s.name }
func (s *stuckPeerHandle) PID() int     { return s.pid }

func (s *stuckPeerHandle) Wait() error {
	<-s.done
	return nil
}

func (s *stuckPeerHandle) ClosePipe() error {
	return nil
}

func (s *stuckPeerHandle) Signal(sig processSig) error {
	s.mu.Lock()
	s.signals = append(s.signals, sig)
	s.mu.Unlock()
	return nil
}

func (s *stuckPeerHandle) hasSignal(want processSig) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, sig := range s.signals {
		if sig == want {
			return true
		}
	}
	return false
}

func (s *stuckPeerHandle) registered() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.reg
}

func (s *stuckPeerHandle) markRegistered() {
	s.mu.Lock()
	s.reg = true
	s.mu.Unlock()
}

func (s *stuckPeerHandle) triggerExit(_ error) {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
}

type singleHandleLauncher struct {
	h        *stuckPeerHandle
	launchCh chan struct{}
	once     sync.Once
}

func (l *singleHandleLauncher) Launch(_ context.Context, _ string) (peerHandle, error) {
	l.once.Do(func() { l.h.markRegistered() })
	return l.h, nil
}

func waitUntil(t *testing.T, timeout time.Duration, cond func() bool, msg string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met: %s", msg)
}
