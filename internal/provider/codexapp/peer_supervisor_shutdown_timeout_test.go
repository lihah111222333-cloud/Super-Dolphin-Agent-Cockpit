package codexapp

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestPeerSupervisorShutdownReturnsErrorWhenPeerIgnoresForcedSignal(t *testing.T) {
	stuck := &stuckPeerHandle{name: "stuck-peer", pid: 99998, done: make(chan struct{})}
	s := NewPeerSupervisorWithOptions(nil, nil,
		WithPeerLauncher(&singleHandleLauncher{h: stuck, launchCh: make(chan struct{}, 1)}),
		WithPeerPIDTracker(newFakePIDTracker()),
		WithPeerNames([]string{"test-peer"}),
		WithPeerRestartBackoff(10*time.Millisecond),
		WithPeerStopGrace(10*time.Millisecond, 10*time.Millisecond),
		WithPeerControlProbe("127.0.0.1:0", 1*time.Millisecond, 0),
		WithPeerCleanupHook(func() {}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := runSupervisor(ctx, s)
	waitUntil(t, time.Second, stuck.registered, "stuck peer never tracked")

	cancel()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "shutdown timeout") {
			t.Fatalf("Run err = %v, want shutdown timeout", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("Run did not return after peer ignored EOF, SIGTERM, and SIGKILL")
	}
	if !stuck.hasSignal(sigTerminate) {
		t.Fatal("stuck peer did not receive SIGTERM before shutdown timeout")
	}
	if !stuck.hasSignal(sigForceKill) {
		t.Fatal("stuck peer did not receive SIGKILL before shutdown timeout")
	}
}

func TestPeerSupervisorShutdownTimeoutKeepsDiscoveryCleanupAndPIDRegistry(t *testing.T) {
	stuck := &stuckPeerHandle{name: "stuck-peer", pid: 99997, done: make(chan struct{})}
	tracker := newFakePIDTracker()
	var cleanupCalled atomic.Bool
	s := NewPeerSupervisorWithOptions(nil, nil,
		WithPeerLauncher(&singleHandleLauncher{h: stuck, launchCh: make(chan struct{}, 1)}),
		WithPeerPIDTracker(tracker),
		WithPeerNames([]string{"test-peer"}),
		WithPeerRestartBackoff(10*time.Millisecond),
		WithPeerStopGrace(10*time.Millisecond, 10*time.Millisecond),
		WithPeerControlProbe("127.0.0.1:0", 1*time.Millisecond, 0),
		WithPeerCleanupHook(func() { cleanupCalled.Store(true) }),
	)
	ctx, cancel := context.WithCancel(context.Background())
	done := runSupervisor(ctx, s)
	waitUntil(t, time.Second, stuck.registered, "stuck peer never tracked")
	if !tracker.has(stuck.PID()) {
		t.Fatalf("pid %d should be registered before shutdown", stuck.PID())
	}

	cancel()

	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "shutdown timeout") {
			t.Fatalf("Run err = %v, want shutdown timeout", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after peer ignored EOF, SIGTERM, and SIGKILL")
	}
	if !tracker.has(stuck.PID()) {
		t.Fatalf("pid %d should remain registered after unconfirmed shutdown", stuck.PID())
	}
	if cleanupCalled.Load() {
		t.Fatal("cleanup hook ran after unconfirmed shutdown; discovery state must be preserved")
	}
}

func TestPeerSupervisorSuperviseOneCancelDoesNotWaitForeverOnStuckWait(t *testing.T) {
	stuck := &neverReturningWaitPeerHandle{
		name:        "stuck-peer",
		pid:         99996,
		waitStarted: make(chan struct{}),
	}
	s := NewPeerSupervisorWithOptions(nil, nil,
		WithPeerNames([]string{"test-peer"}),
		WithPeerRestartBackoff(10*time.Millisecond),
		WithPeerStopGrace(10*time.Millisecond, 10*time.Millisecond),
		WithPeerControlProbe("127.0.0.1:0", 1*time.Millisecond, 0),
		WithPeerCleanupHook(func() {}),
	)
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	wgDone := make(chan struct{})
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() {
		wg.Wait()
		close(wgDone)
	})
	goroutines.Go(func() { s.superviseOne(ctx, "test-peer", stuck, &wg) })

	waitUntil(t, time.Second, stuck.started, "stuck peer Wait never started")
	cancel()

	select {
	case <-wgDone:
	case <-time.After(time.Second):
		t.Fatal("superviseOne did not release WaitGroup after ctx cancel and stuck peer Wait")
	}
}

type neverReturningWaitPeerHandle struct {
	name        string
	pid         int
	waitStarted chan struct{}
	once        sync.Once
}

func (h *neverReturningWaitPeerHandle) Name() string { return h.name }
func (h *neverReturningWaitPeerHandle) PID() int     { return h.pid }

func (h *neverReturningWaitPeerHandle) Wait() error {
	h.once.Do(func() { close(h.waitStarted) })
	select {}
}

func (h *neverReturningWaitPeerHandle) ClosePipe() error { return nil }
func (h *neverReturningWaitPeerHandle) Signal(processSig) error {
	return nil
}

func (h *neverReturningWaitPeerHandle) started() bool {
	select {
	case <-h.waitStarted:
		return true
	default:
		return false
	}
}
