package bootstrap

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/dto/mcp"
)

// TestClientFireShutdownTracksCallbackWithWaitGroup asserts the P22
// P2 bootstrap-S1 invariant: OnShutdown is spawned through
// spawnCallback, which registers with callbackWG. Close() can then
// drain via drainCallbacks instead of racing a fire-and-forget
// goroutine.
func TestClientFireShutdownTracksCallbackWithWaitGroup(t *testing.T) {
	release := make(chan struct{})
	var fired atomic.Bool
	c := New(Config{
		BinaryName: "test-binary",
		InstanceID: "inst-1",
		OnShutdown: func(mcp.ShutdownRequest) {
			fired.Store(true)
			<-release
		},
	})

	c.fireShutdown(mcp.ShutdownRequest{})

	// drainCallbacks should block while the handler is still holding
	// on `release`; verify by racing with a short timeout.
	drainStart := time.Now()
	err := c.drainCallbacks(80 * time.Millisecond)
	if err == nil {
		t.Fatalf("drainCallbacks() returned nil before handler finished; WaitGroup may not be tracking the callback")
	}
	if time.Since(drainStart) < 60*time.Millisecond {
		t.Fatalf("drainCallbacks() returned too early: elapsed=%v", time.Since(drainStart))
	}

	close(release)
	if err := c.drainCallbacks(2 * time.Second); err != nil {
		t.Fatalf("drainCallbacks() after release error = %v", err)
	}
	if !fired.Load() {
		t.Fatalf("OnShutdown handler did not fire")
	}
}

// TestClientSpawnCallbackAfterCloseIsNoop asserts that once closed is
// set, spawnCallback refuses to launch a new goroutine, preventing
// late callbacks from extending the drain window indefinitely.
func TestClientSpawnCallbackAfterCloseIsNoop(t *testing.T) {
	var fired atomic.Bool
	c := New(Config{BinaryName: "test-binary", InstanceID: "inst-2"})
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()

	c.spawnCallback(func() { fired.Store(true) })

	if err := c.drainCallbacks(200 * time.Millisecond); err != nil {
		t.Fatalf("drainCallbacks() on empty group error = %v, want nil", err)
	}
	// Give any stray goroutine a moment to run; fired must stay
	// false because spawnCallback took the closed branch.
	time.Sleep(20 * time.Millisecond)
	if fired.Load() {
		t.Fatalf("spawnCallback ran after closed=true; expected no-op")
	}
}

// TestClientDrainCallbacksTimeoutSurfaced asserts that a stuck
// callback causes drainCallbacks to surface a timeout error, so
// Close() can log and proceed rather than hang indefinitely.
func TestClientDrainCallbacksTimeoutSurfaced(t *testing.T) {
	c := New(Config{BinaryName: "test-binary", InstanceID: "inst-3"})
	c.callbackWG.Add(1)
	defer c.callbackWG.Done()

	start := time.Now()
	err := c.drainCallbacks(40 * time.Millisecond)
	if err == nil {
		t.Fatalf("drainCallbacks() with stuck callback = nil err, want timeout error")
	}
	elapsed := time.Since(start)
	if elapsed < 30*time.Millisecond {
		t.Fatalf("drainCallbacks() returned too early: elapsed=%v", elapsed)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("drainCallbacks() returned too late: elapsed=%v", elapsed)
	}
}
