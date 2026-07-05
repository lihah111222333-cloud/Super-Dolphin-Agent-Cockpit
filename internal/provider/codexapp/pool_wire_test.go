package codexapp

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"
)

// TestTransportServerWrapReflectsFields exercises the *transport →
// SpawnedServer adapter end-to-end on a non-live transport. The
// adapter should expose the URL written to transport.serverURL and
// report Alive=false while no process is attached. Close must
// idempotently short-circuit on an already-closed transport.
func TestTransportServerWrapReflectsFields(t *testing.T) {
	t.Parallel()

	tr := &transport{}
	tr.stateMu.Lock()
	tr.serverURL = "ws://127.0.0.1:12345"
	tr.stateMu.Unlock()

	wrapped := wrapTransport(tr)
	if got := wrapped.ServerURL(); got != "ws://127.0.0.1:12345" {
		t.Fatalf("ServerURL = %q, want ws://127.0.0.1:12345", got)
	}
	if wrapped.Alive() {
		t.Fatalf("Alive = true on transport without a live process")
	}
	if err := wrapped.Close(context.Background()); err != nil {
		t.Fatalf("first Close err = %v", err)
	}
	// Second Close is a no-op once the transport is already flagged
	// closed. The SpawnedServer contract allows a repeated Close; the
	// adapter must not panic or double-tear-down.
	if err := wrapped.Close(context.Background()); err != nil {
		t.Fatalf("second Close err = %v", err)
	}
}

// TestTransportServerNilSafe guards against the zero adapter blowing
// up the pool when an underlying spawner accidentally returns a
// partially-constructed transportServer. The pool only trusts the
// SpawnedServer contract, so the adapter MUST behave benignly.
func TestTransportServerNilSafe(t *testing.T) {
	t.Parallel()

	var zero *transportServer
	if url := zero.ServerURL(); url != "" {
		t.Fatalf("nil adapter ServerURL = %q, want empty", url)
	}
	if zero.Alive() {
		t.Fatalf("nil adapter Alive = true")
	}
	if err := zero.Close(context.Background()); err != nil {
		t.Fatalf("nil adapter Close err = %v", err)
	}
}

// TestNewTransportSpawnerSurfacesBuildError checks the spawner
// contract on the pure-Go path that BuildPoolSpawnCmd rejects: an
// empty codexHome must fail fast, before we try to launch anything.
// This verifies NewTransportSpawner threads build-time errors
// through to the pool's backoff slot without reaching exec.
func TestNewTransportSpawnerSurfacesBuildError(t *testing.T) {
	t.Parallel()

	spawner := NewTransportSpawner(nil, slog.Default())
	srv, err := spawner(context.Background(), "  ", "openai")
	if err == nil {
		t.Fatalf("expected error for empty home, got server %v", srv)
	}
}

// TestPoolEvictRunnerTicks drives the runner against a pool whose
// clock is advanced past IdleTimeout, and verifies that the runner's
// loop calls EvictIdle at least once before shutdown. A shorter
// interval is used so the test completes in a few ticks; the runner
// contract exits cleanly on ctx cancellation.
func TestPoolEvictRunnerTicks(t *testing.T) {
	t.Parallel()

	spawnErr := errors.New("port taken")
	spawner := func(context.Context, string, string) (SpawnedServer, error) {
		return nil, spawnErr
	}
	pool, clock := newPoolForTest(t, spawner, PoolConfig{
		IdleTimeout:  time.Millisecond,
		SpawnBackoff: time.Second,
	})
	defer pool.Close(context.Background())

	// A spawn failure leaves a refcount-free backoff slot for the idle runner
	// to clean up. Successful sessions are closed immediately on final release.
	_, _, err := pool.Acquire(context.Background(), identityFor(t, "glm"), "agent-1")
	if err == nil {
		t.Fatal("Acquire unexpectedly succeeded")
	}
	// Advance the pinned clock past IdleTimeout so EvictIdle finds
	// the entry expired.
	*clock = clock.Add(time.Hour)

	runner := newPoolEvictRunner(slog.Default(), pool)
	runner.interval = 5 * time.Millisecond

	cancel, done := startCodexRunnerForTest(t, "pool evict runner", runner.Run)

	// Wait for at least one tick to run; eviction is async so poll.
	deadline := time.Now().Add(2 * time.Second)
	for pool.Size() != 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if pool.Size() != 0 {
		cancel()
		<-done
		t.Fatalf("pool size = %d after eviction window, want 0", pool.Size())
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("runner Run err = %v, want context.Canceled", err)
	}
}

// TestPoolEvictRunnerNoPool checks the Run contract when the pool
// dependency is nil: the runner must park on ctx and exit with
// ctx.Err rather than panicking. This matters because fx lifecycle
// ordering can briefly surface nil dependencies during init.
func TestPoolEvictRunnerNoPool(t *testing.T) {
	t.Parallel()

	runner := newPoolEvictRunner(slog.Default(), nil)
	runner.interval = 5 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := runner.Run(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Run err = %v, want DeadlineExceeded", err)
	}
}
