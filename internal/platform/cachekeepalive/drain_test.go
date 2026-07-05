package cachekeepalive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

// pingBlockingSession 在 SendKeepalive 内阻塞，直到 ctx 被取消。
// entered 用于让测试精确等待 ping 已进入执行路径，避免依赖 sleep。
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

// TestCacheKeepaliveDrainCancelsPendingPing 固定 keepalive 关闭路径的 drain 约束。
// 当 SendKeepalive 内的 ping 被卡住时，Shutdown 必须取消 Manager 持有的 pingCtx、
// 等待已进入的 goroutine 退出，并受调用方 shutdown ctx 的时间预算约束。
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
	bindings := &bindingStoreStub{byAgent: map[string]*contract.CacheKeepaliveBinding{
		"agent-1": {AgentID: "agent-1"},
	}}
	m := newTestManager(resolver, bindings, nil)
	m.register("session-1", "agent-1", "thread-1")
	return m, session
}

func fireKeepalivePing(t *testing.T, m *Manager) <-chan struct{} {
	t.Helper()
	// 直接触发 ping 路径，避免等待长周期定时器。
	// 这里保留生产闭包的 enterPing/pingInflight/executePing 顺序，覆盖 Shutdown 依赖的 drain 计数。
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
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = m.Shutdown(cleanupCtx)
		select {
		case <-pingDone:
		case <-time.After(time.Second):
			t.Fatal("keepalive ping goroutine did not stop")
		}
	})
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
	// Shutdown 返回前，已进入的 ping goroutine 必须退出。
	select {
	case <-pingDone:
	default:
		t.Fatal("Shutdown returned but ping goroutine is still alive; drain contract broken")
	}
}

func assertKeepaliveObservedCancellation(t *testing.T, session *pingBlockingSession) {
	t.Helper()
	// SendKeepalive 必须观察到 Manager 持有的 pingCtx 取消，而不是仅由 session 自身关闭带出。
	select {
	case err := <-session.exited:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("SendKeepalive err = %v, want context.Canceled", err)
		}
	case <-time.After(50 * time.Millisecond):
		t.Fatal("SendKeepalive did not publish its ctx.Err() after Shutdown")
	}
}

// TestCacheKeepaliveShutdownGatesNewPings 验证 Shutdown 关闭 drain gate 后会拒绝新 ping。
// 这样新的定时器回调不能再登记 in-flight 计数。
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
	// 重复 Shutdown 应为 no-op，不能死锁。
	if err := m.Shutdown(context.Background()); err != nil {
		t.Errorf("second Shutdown err = %v, want nil (idempotent)", err)
	}
}
