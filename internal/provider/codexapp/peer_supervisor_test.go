package codexapp

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/platform/discovery"
)

// ---------------------------------------------------------------------------
// Fakes
// ---------------------------------------------------------------------------

// fakePeerLauncher is the in-process launcher used by every PeerSupervisor
// test. It serialises launches behind a mutex so tests can safely inspect
// per-peer handle history on the main goroutine.
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
	out := make([]*fakePeerHandle, len(f.handles[name]))
	copy(out, f.handles[name])
	return out
}

// waitLaunch blocks until Launch fires for expected name, or t.Fatal on timeout.
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

// triggerExit simulates the peer process exiting with err. Idempotent.
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

// ClosePipe mimics sending EOF on stdin — the real peer binary exits on EOF,
// so the fake follows suit by triggering a clean exit.
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

// fakePIDTracker observes Register/Unregister calls so the atomic-swap test
// can assert the exact sequence the supervisor issues.
type fakePIDTracker struct {
	mu    sync.Mutex
	live  map[int]string // pid -> kind
	order []string       // human-readable audit trail, e.g. "register:10001:mcp-orch"
}

func newFakePIDTracker() *fakePIDTracker {
	return &fakePIDTracker{live: map[int]string{}}
}

func (f *fakePIDTracker) Register(pid int, kind string, _ map[string]string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.live[pid] = kind
	f.order = append(f.order, "register:"+itoaSimple(pid)+":"+kind)
}

func (f *fakePIDTracker) Unregister(pid int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.live, pid)
	f.order = append(f.order, "unregister:"+itoaSimple(pid))
}

func (f *fakePIDTracker) has(pid int) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	_, ok := f.live[pid]
	return ok
}

func itoaSimple(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var buf [20]byte
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// newTestSupervisor wires a supervisor with fakes + tiny timings so tests stay
// fast and deterministic. peerNames defaults to a single "test-peer" to keep
// assertions simple; callers override via WithPeerNames when multiple peers
// are needed.
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
		// Disable the control-plane probe: zero attempts means probe returns
		// immediately without any dial, keeping tests independent of the host
		// network and not dependent on timing.
		WithPeerControlProbe("127.0.0.1:0", 1*time.Millisecond, 0),
	}
	s := NewPeerSupervisorWithOptions(nil, nil, append(base, opts...)...)
	return s, launcher, tracker
}

// runSupervisor starts Run in a goroutine and returns a done channel that
// closes when Run returns. Callers are responsible for cancelling the ctx.
func runSupervisor(ctx context.Context, s *PeerSupervisor) <-chan error {
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()
	return done
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestPeerSupervisorStartsPeers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s, launcher, _ := newTestSupervisor(t,
		WithPeerNames([]string{"mcp-orch", "mcp-lsp"}),
	)
	done := runSupervisor(ctx, s)

	launcher.waitLaunch(t, "mcp-orch", time.Second)
	launcher.waitLaunch(t, "mcp-lsp", time.Second)

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after ctx cancel")
	}

	if got := launcher.snapshotCount("mcp-orch"); got != 1 {
		t.Errorf("mcp-orch launch count = %d, want 1", got)
	}
	if got := launcher.snapshotCount("mcp-lsp"); got != 1 {
		t.Errorf("mcp-lsp launch count = %d, want 1", got)
	}
}

func TestPeerProcessEnvInjectsConfiguredMcpLSPWorkspaceRoots(t *testing.T) {
	root := t.TempDir()
	rawRoots, err := json.Marshal([]string{root})
	if err != nil {
		t.Fatalf("Marshal roots: %v", err)
	}

	env, err := peerProcessEnv("mcp-lsp", []string{"PATH=/bin"}, []string{root})
	if err != nil {
		t.Fatalf("peerProcessEnv() error = %v", err)
	}
	requireEnvValue(t, env, "GO_AGENT_LSP_ROOT", root)
	requireEnvValue(t, env, "GO_AGENT_LSP_ROOTS", string(rawRoots))
}

func TestPeerProcessEnvRejectsExplicitInvalidMcpLSPRoots(t *testing.T) {
	_, err := peerProcessEnv("mcp-lsp", []string{"GO_AGENT_LSP_ROOTS=not-json"}, nil)
	if err == nil || !strings.Contains(err.Error(), "GO_AGENT_LSP_ROOTS") {
		t.Fatalf("peerProcessEnv() error = %v, want explicit roots parse failure", err)
	}
}

func TestExecPeerLauncherFailsWhenNoRootSourceExists(t *testing.T) {
	launcher := newExecPeerLauncher(nil)
	launcher.workspaceRoots = func() []string { return nil }
	_, err := launcher.peerEnvForTest("mcp-lsp", []string{"PATH=/bin"})
	if err == nil || !strings.Contains(err.Error(), "workspace root") {
		t.Fatalf("peer env error = %v, want visible missing root failure", err)
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
	env, err := launcher.peerEnvForTest("mcp-lsp", []string{"PATH=/bin"})
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
	_, err := launcher.peerEnvForTest("mcp-lsp", []string{"PATH=/bin"})
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
	// Simulate abrupt peer exit.
	handles[0].triggerExit(errors.New("boom"))

	// Supervisor should relaunch after the 10ms backoff.
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
		// Long backoff so if shutdown didn't suppress, a stray restart would
		// be observable as an additional Launch within the test window.
		WithPeerRestartBackoff(500*time.Millisecond),
	)
	done := runSupervisor(ctx, s)
	launcher.waitLaunch(t, "test-peer", time.Second)

	// Cancel first: shutdown closes the pipe, triggering a graceful exit on
	// the fake handle. superviseOne must NOT loop into a restart.
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

	// Force a restart.
	first.triggerExit(errors.New("boom"))
	launcher.waitLaunch(t, "test-peer", time.Second)

	// Give the supervisor a tiny window to process replacePeer after Launch.
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

	// Final state: both PIDs unregistered (shutdown phase).
	if tracker.has(second.PID()) {
		t.Errorf("pid %d should be unregistered after shutdown", second.PID())
	}
}

func TestPeerSupervisorBackoffCancelledByStop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	s, launcher, _ := newTestSupervisor(t,
		// Long backoff: the test must prove the timer is cancelled rather than
		// waited out. Any spurious restart would require waiting this long,
		// which would trip the outer 1s deadline below.
		WithPeerRestartBackoff(2*time.Second),
	)
	done := runSupervisor(ctx, s)
	launcher.waitLaunch(t, "test-peer", time.Second)

	// Simulate peer exit so supervisor enters the restart backoff.
	launcher.snapshotHandles("test-peer")[0].triggerExit(errors.New("boom"))

	// Give the supervisor a moment to hit the backoff timer, then cancel.
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

	// Seed a discovery file for this process's PID so the real cleanup hook
	// has something concrete to remove. Use a unique per-test peer name so a
	// parallel test run cannot race on the same /tmp path.
	testPeerName := "test-peer-discovery"
	if err := discovery.WriteDiscoveryFile(testPeerName, os.Getpid(), "127.0.0.1:65432"); err != nil {
		t.Fatalf("seed discovery file: %v", err)
	}

	// Sanity: the file exists before supervisor runs.
	addr, err := discovery.ReadDiscoveryAddr(testPeerName, os.Getpid())
	if err != nil || addr != "127.0.0.1:65432" {
		t.Fatalf("seeded discovery file missing: addr=%q err=%v", addr, err)
	}

	s, launcher, _ := newTestSupervisor(t,
		// Install a cleanup hook that removes the seeded file AND flags it ran.
		// The real cleanPeerDiscoveryFiles only clears the canonical peer names
		// (mcp-orch, mcp-lsp), so we install a test-specific hook to cover the
		// seam without mutating production defaults.
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

// TestPeerSupervisorShutdownEscalatesToSIGTERM exercises the grace-period
// escalation path. Not one of the six mandatory P1a tests, but included
// because §Step 4 pins the stop sequence contract (EOF -> SIGTERM -> SIGKILL)
// and the shape must not silently regress.
func TestPeerSupervisorShutdownEscalatesToSIGTERM(t *testing.T) {
	// Build a handle that ignores ClosePipe (simulates a peer that does not
	// exit on EOF), so shutdown is forced into the SIGTERM / SIGKILL path.
	stuck := &stuckPeerHandle{name: "stuck-peer", pid: 99999, done: make(chan struct{})}
	launcher := newFakePeerLauncher()
	launcher.mu.Lock()
	launcher.handles["test-peer"] = []*fakePeerHandle{}
	launcher.mu.Unlock()

	// Custom launcher that returns the stuck handle.
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

	// Wait until Launch returned (stuck handle tracked).
	waitUntil(t, time.Second, func() bool {
		return stuck.registered()
	}, "stuck peer never tracked")

	cancel()
	// Supervisor should SIGTERM (and then SIGKILL) to unblock the stuck peer.
	waitUntil(t, time.Second, func() bool {
		return stuck.hasSignal(sigTerminate)
	}, "stuck peer never received SIGTERM")

	// Unblock so Run can return — simulate SIGKILL taking effect.
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
	// Intentionally do NOT trigger exit — this is the "stuck peer" seam.
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

// Regression: on Windows the peer binaries are .exe but defaultPeerNames
// returns "mcp-orch" / "mcp-lsp" without a suffix. findPeerBinary used to
// stat the literal name only, so on Windows it always missed the file
// and toolbridge had no peers — sessions failed with
// "toolbridge: no active peer".
func TestFindPeerBinary_WindowsAddsExeSuffix(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		exe := filepath.Join(dir, "mcp-orch.exe")
		if err := os.WriteFile(exe, []byte("stub"), 0o755); err != nil {
			t.Fatalf("seed exe: %v", err)
		}
		got, ok := findPeerBinary([]string{dir}, "mcp-orch")
		if !ok || got != exe {
			t.Fatalf("want %q, got %q ok=%v", exe, got, ok)
		}
		// And: caller already passing the .exe must still work.
		got, ok = findPeerBinary([]string{dir}, "mcp-orch.exe")
		if !ok || got != exe {
			t.Fatalf("explicit .exe: want %q, got %q ok=%v", exe, got, ok)
		}
	} else {
		bin := filepath.Join(dir, "mcp-orch")
		if err := os.WriteFile(bin, []byte("stub"), 0o755); err != nil {
			t.Fatalf("seed bin: %v", err)
		}
		got, ok := findPeerBinary([]string{dir}, "mcp-orch")
		if !ok || got != bin {
			t.Fatalf("want %q, got %q ok=%v", bin, got, ok)
		}
	}
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
