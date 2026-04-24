package mcpcontrol

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestSweeperRunnerBlocksUntilContextDone pins the P22 P1b contract that the
// runner is a blocking actor: Run returns only when ctx is cancelled, and
// returns ctx.Err() so the run.Group sees a clean cancellation.
func TestSweeperRunnerBlocksUntilContextDone(t *testing.T) {
	t.Parallel()
	sweeper := NewSweeperWithOptions(NewRegistry(), nil, SweeperOptions{
		Tick:   10 * time.Millisecond,
		Jitter: time.Millisecond,
	})
	runner := NewSweeperRunner(sweeper)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	// Let the sweep fire at least once so we are exercising the live loop,
	// not a cold start / early return path.
	time.Sleep(25 * time.Millisecond)
	select {
	case err := <-done:
		t.Fatalf("Run returned before ctx cancel: err=%v", err)
	default:
	}

	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return within 1s after ctx cancel")
	}
}

// TestSweeperRunnerPreservesJitterAndStaleTransitions checks that the
// runner's outer wrapping does not change sweeper cadence / stale behaviour:
// we assert the jitter+tick constants are still visible on the underlying
// Sweeper (the runner must NOT recreate a sweeper with different options).
func TestSweeperRunnerPreservesJitterAndStaleTransitions(t *testing.T) {
	t.Parallel()
	opts := SweeperOptions{
		Tick:       7 * time.Millisecond,
		Jitter:     2 * time.Millisecond,
		Timeout:    defaultHeartbeatTTL, // 30s
		StaleGrace: defaultStaleGraceTime,
	}
	sweeper := NewSweeperWithOptions(NewRegistry(), nil, opts)
	r, ok := NewSweeperRunner(sweeper).(*SweeperRunner)
	if !ok {
		t.Fatalf("NewSweeperRunner type = %T, want *SweeperRunner", r)
	}
	if r.sweeper != sweeper {
		t.Fatalf("runner wraps a different sweeper instance: got %p, want %p", r.sweeper, sweeper)
	}
	// The P1b contract pins these defaults as authoritative; drift must be
	// paired with a doc update.
	if defaultHeartbeatTTL != 30*time.Second {
		t.Fatalf("defaultHeartbeatTTL drifted to %v, want 30s", defaultHeartbeatTTL)
	}
	if defaultSweepTick != 5*time.Second {
		t.Fatalf("defaultSweepTick drifted to %v, want 5s", defaultSweepTick)
	}
	if defaultStaleGraceTime != 5*time.Second {
		t.Fatalf("defaultStaleGraceTime drifted to %v, want 5s", defaultStaleGraceTime)
	}
}

// TestSweeperHeartbeatUsesCodeTruthTTL30s freezes the P1b-required heartbeat
// TTL constant so the runner (and any future documentation) cannot silently
// downgrade it. This matches the P1b §TDD mandated test name.
func TestSweeperHeartbeatUsesCodeTruthTTL30s(t *testing.T) {
	t.Parallel()
	if defaultHeartbeatTTL != 30*time.Second {
		t.Fatalf("defaultHeartbeatTTL = %v, want 30s", defaultHeartbeatTTL)
	}
}

// TestRunnerStopDrainsBeforeFinalCleanup exercises the stop ordering: when
// the runner's ctx is cancelled, Run returns promptly; the registry's
// OnStop (shutdownActiveLeases) is a separate concern and keeps working
// after Run has unwound.
func TestRunnerStopDrainsBeforeFinalCleanup(t *testing.T) {
	t.Parallel()
	reg := NewRegistry()
	sweeper := NewSweeperWithOptions(reg, nil, SweeperOptions{
		Tick:   5 * time.Millisecond,
		Jitter: time.Millisecond,
	})
	runner := NewSweeperRunner(sweeper)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()

	time.Sleep(20 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return after cancel")
	}

	// After Run has returned we can still shut active leases; that is the
	// final cleanup path owned by registerRegistryLifecycle.OnStop. The
	// call should succeed on an empty registry.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer stopCancel()
	if err := reg.shutdownActiveLeases(stopCtx); err != nil {
		t.Fatalf("shutdownActiveLeases() after Run drain = %v", err)
	}
}

// TestSweeperRunnerNilSweeperBlocksUntilDone covers the defensive path where
// the runner is given a nil sweeper: it must not panic and must still honor
// ctx cancellation.
func TestSweeperRunnerNilSweeperBlocksUntilDone(t *testing.T) {
	t.Parallel()
	runner := NewSweeperRunner(nil)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runner.Run(ctx) }()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run err = %v, want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("Run did not return with nil sweeper")
	}
}
