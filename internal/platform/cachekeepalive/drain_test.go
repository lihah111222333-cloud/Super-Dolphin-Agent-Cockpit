package cachekeepalive

import (
	"context"
	"errors"
	"testing"
	"time"

	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

// pingBlockingSession is a KeepaliveCapable that blocks inside SendKeepalive
// until its ctx is cancelled. It signals entry via `entered` so tests can
// synchronize on "ping is in-flight" without sleeping.
type pingBlockingSession struct {
	plainSession
	entered chan struct{}
	exited  chan error
}

func (s *pingBlockingSession) SendKeepalive(ctx context.Context) error {
	select {
	case <-s.entered:
	default:
		close(s.entered)
	}
	<-ctx.Done()
	err := ctx.Err()
	s.exited <- err
	return err
}

// TestCacheKeepaliveDrainCancelsPendingPing is the P22 P2 §TDD test named
// in docs/plans/迁移/p22/P2_BusRuntimeDecoupling.md:415.
//
// Contract: while a keepalive ping is stuck inside SendKeepalive,
// Shutdown(ctx) must
//
//  1. cancel the ambient pingCtx so the blocked session sees ctx.Err();
//  2. wait for the in-flight ping goroutine to unwind before returning;
//  3. return bounded by the caller's ctx (not hang past the shutdown
//     grace budget).
//
// Pre-P2 shape: executePing used context.Background() and was not
// tracked, so Shutdown() only cleared the timer map — the ping
// goroutine could outlive the module Lifecycle.OnStop entirely.
func TestCacheKeepaliveDrainCancelsPendingPing(t *testing.T) {
	t.Parallel()

	m, session := newDrainTestManager()
	pingDone := fireKeepalivePing(t, m)
	waitForKeepaliveEntry(t, session)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := m.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown() err = %v, want nil (drain must finish within ctx budget)", err)
	}
	elapsed := time.Since(start)
	if elapsed > 250*time.Millisecond {
		t.Errorf("Shutdown took %s, want <250ms — pingCtx cancellation should unblock SendKeepalive promptly", elapsed)
	}

	assertPingGoroutineDrained(t, pingDone)
	assertKeepaliveObservedCancellation(t, session)
}

func newDrainTestManager() (*Manager, *pingBlockingSession) {
	session := &pingBlockingSession{
		entered: make(chan struct{}),
		exited:  make(chan error, 1),
	}
	resolver := &resolverStub{session: session}
	bindings := &bindingStoreStub{byAgent: map[string]*bindingstore.Binding{
		"agent-1": {AgentID: "agent-1"},
	}}
	m := newTestManager(resolver, bindings, nil)
	m.register("session-1", "agent-1", "thread-1")
	return m, session
}

func fireKeepalivePing(t *testing.T, m *Manager) <-chan struct{} {
	t.Helper()
	// Fire the ping path directly instead of waiting keepaliveInterval
	// (55 minutes) for the scheduled AfterFunc. The test mirrors the
	// production closure: enterPing + pingInflight.Done + executePing,
	// so it goes through the same drain bookkeeping Shutdown depends on.
	timerRef := m.snapshotTimer("session-1", nil)
	if timerRef == nil || timerRef.timer == nil {
		t.Fatalf("register did not schedule timer")
	}
	pingDone := make(chan struct{})
	go func() {
		defer close(pingDone)
		if !m.enterPing() {
			return
		}
		defer m.pingInflight.Done()
		m.executePing("session-1", timerRef.timer)
	}()
	return pingDone
}

func waitForKeepaliveEntry(t *testing.T, session *pingBlockingSession) {
	t.Helper()
	select {
	case <-session.entered:
	case <-time.After(time.Second):
		t.Fatal("SendKeepalive did not enter within 1s; production path broken")
	}
}

func assertPingGoroutineDrained(t *testing.T, pingDone <-chan struct{}) {
	t.Helper()
	// Ping goroutine must have unwound before Shutdown returned.
	select {
	case <-pingDone:
	default:
		t.Fatal("Shutdown returned but ping goroutine is still alive; drain contract broken")
	}
}

func assertKeepaliveObservedCancellation(t *testing.T, session *pingBlockingSession) {
	t.Helper()
	// SendKeepalive must have observed ctx.Err() (i.e. the cancellation
	// came through the Manager-owned pingCtx, not via the session closing
	// itself).
	select {
	case err := <-session.exited:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("SendKeepalive err = %v, want context.Canceled", err)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("SendKeepalive did not publish its ctx.Err() after Shutdown")
	}
}

// TestCacheKeepaliveShutdownGatesNewPings verifies the enterPing gate:
// after Shutdown has closed the drain gate, a fresh AfterFunc-style
// invocation must be rejected before it registers another in-flight
// counter.
func TestCacheKeepaliveShutdownGatesNewPings(t *testing.T) {
	t.Parallel()

	m := newTestManager(nil, nil, nil)
	m.register("session-1", "agent-1", "thread-1")

	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() err = %v, want nil", err)
	}
	if m.enterPing() {
		t.Fatal("enterPing returned true after Shutdown; gate not closed")
	}
	// Redundant Shutdown must be a no-op and not deadlock.
	if err := m.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown err = %v, want nil (idempotent)", err)
	}
}
