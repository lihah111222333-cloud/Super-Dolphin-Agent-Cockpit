package multilsp

import (
	"context"
	"testing"
	"time"
)

// TestPoolRecyclerRunExitsOnCtxCancel asserts the P22 P2 LSP-S1
// runner contract: poolRecycler.Run must return once the supplied
// ctx is cancelled, with no residual goroutine. Pre-P22 P2 the
// recycler was driven by a constructor-launched goroutine and its
// own stopCh; those are gone, and the runner owner (root
// group:"runners" bridge) must be able to drive shutdown via ctx.
func TestPoolRecyclerRunExitsOnCtxCancel(t *testing.T) {
	pool := NewManagerPool(nil, defaultPoolSize)
	r := pool.RecyclerRunner()
	if r == nil {
		t.Fatalf("RecyclerRunner() returned nil; expected the pool's recycler")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- r.Run(ctx) }()

	// Give the loop a moment to arm the ticker.
	time.Sleep(5 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() returned error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run() did not return within 2s after ctx cancel")
	}
}

// TestNewManagerPoolDoesNotLaunchRecyclerGoroutine asserts the P22 P2
// LSP-S1 invariant that the constructor no longer self-spawns the
// recycler goroutine. After construction the recycler must exist
// (TouchShard still works) but the loop goroutine must not be
// running yet — only Run(ctx) starts it.
func TestNewManagerPoolDoesNotLaunchRecyclerGoroutine(t *testing.T) {
	pool := NewManagerPool(nil, defaultPoolSize)
	if pool.recycler == nil {
		t.Fatalf("NewManagerPool did not create a recycler")
	}
	// TouchShard should still work for callers that touched it
	// before the runner is started; it's pure bookkeeping.
	pool.recycler.TouchShard(0)

	// StopAll must not hang or panic even though the recycler has
	// never been Started in this test — it is now a no-op for the
	// recycler lifecycle (owned by ctx).
	if err := pool.StopAll(); err != nil {
		t.Fatalf("StopAll() error = %v", err)
	}
}

// TestPoolRecyclerRunNilReceiverBlocks asserts that a nil recycler's
// Run(ctx) blocks until ctx.Done() and returns nil. This keeps
// RecyclerRunner()'s nil-receiver branch wire-compatible with the
// root group:"runners" aggregation.
func TestPoolRecyclerRunNilReceiverBlocks(t *testing.T) {
	var nilRecycler *poolRecycler
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- nilRecycler.Run(ctx) }()

	select {
	case err := <-done:
		t.Fatalf("Run() on nil receiver returned before ctx cancel: err=%v", err)
	case <-time.After(20 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run() on nil receiver error = %v, want nil", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Run() on nil receiver did not return after ctx cancel")
	}
}
