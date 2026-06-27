package cachekeepalive

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

type resolverStub struct {
	session   contract.Session
	err       error
	threadIDs []string
}

func (s *resolverStub) ResolveSession(_ context.Context, threadID string) (contract.Session, error) {
	s.threadIDs = append(s.threadIDs, threadID)
	return s.session, s.err
}

type plainSession struct{ contract.Session }

type keepaliveSession struct{ plainSession }

func (*keepaliveSession) SendKeepalive(context.Context) error { return nil }

type failingKeepaliveSession struct{ plainSession }

func (*failingKeepaliveSession) SendKeepalive(context.Context) error {
	return errors.New("keepalive ping failed")
}

type bindingStoreStub struct {
	byAgent map[string]*contract.CacheKeepaliveBinding
}

func (s *bindingStoreStub) GetCacheKeepaliveBindingByAgentID(_ context.Context, agentID string) (*contract.CacheKeepaliveBinding, error) {
	if s == nil || s.byAgent == nil {
		return nil, nil
	}
	return s.byAgent[agentID], nil
}

type threadStoreStub struct {
	byThread map[string]*contract.CacheKeepaliveThreadRef
	lookups  []string
}

func (s *threadStoreStub) GetCacheKeepaliveThreadByID(_ context.Context, threadID string) (*contract.CacheKeepaliveThreadRef, error) {
	if s == nil {
		return nil, nil
	}
	s.lookups = append(s.lookups, threadID)
	if s.byThread == nil {
		return nil, nil
	}
	return s.byThread[threadID], nil
}

func newTestManager(resolver contract.SessionResolver, bindings contract.CacheKeepaliveBindingLookup, threads contract.CacheKeepaliveThreadLookup) *Manager {
	pingCtx, pingCancel := context.WithCancel(context.Background())
	return &Manager{
		resolver:     resolver,
		bindingStore: bindings,
		threadStore:  threads,
		timers:       make(map[string]*agentTimer),
		pingCtx:      pingCtx,
		pingCancel:   pingCancel,
		drainClosed:  make(chan struct{}),
	}
}

// shutdownForTest 用短超时 ctx 包装 Shutdown，供测试在 t.Cleanup 中幂等收尾。
// 这样每个用例都走真实关闭路径，同时避免失败时长期阻塞。
func (m *Manager) shutdownForTest() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = m.Shutdown(ctx)
}

func TestRegisterSchedulesInitialTimer(t *testing.T) {
	t.Parallel()
	m := newTestManager(nil, nil, nil)
	t.Cleanup(m.shutdownForTest)
	m.register("session-1", "agent-1", "thread-1")
	first := m.snapshotTimer("session-1", nil)
	if first == nil || first.timer == nil {
		t.Fatalf("register() should schedule initial timer; got %#v", first)
	}
}

func TestResetTimer(t *testing.T) {
	t.Parallel()
	m := newTestManager(nil, nil, nil)
	t.Cleanup(m.shutdownForTest)
	m.register("session-1", "agent-1", "thread-1")
	m.ResetTimerByAgent("agent-1")
	first := requireAgentTimer(t, m.snapshotTimer("session-1", nil), "first")
	m.ResetTimerByAgent("agent-1")
	second := requireAgentTimer(t, m.snapshotTimer("session-1", nil), "second")
	if keepaliveInterval != 55*time.Minute {
		t.Fatalf("keepaliveInterval = %s, want 55m", keepaliveInterval)
	}
	if first.timer == second.timer {
		t.Fatalf("reset timer reused timer: first=%v second=%v", first, second)
	}
	if first.timer.Stop() {
		t.Fatalf("old timer remained active after reset: %#v", first)
	}
}

func TestArchivedExcluded(t *testing.T) {
	t.Parallel()
	resolver := &resolverStub{session: &keepaliveSession{}}
	m := newTestManager(resolver, &bindingStoreStub{byAgent: map[string]*contract.CacheKeepaliveBinding{"agent-1": {AgentID: "agent-1", Archived: true}}}, nil)
	if m.canPing(context.Background(), &agentTimer{agentID: "agent-1", threadID: "thread-1"}) {
		t.Fatal("canPing() = true, want false for archived binding")
	}
	if len(resolver.threadIDs) != 0 {
		t.Fatalf("ResolveSession() calls = %d, want 0", len(resolver.threadIDs))
	}
}

func TestActiveTurnSkipped(t *testing.T) {
	t.Parallel()
	resolver := &resolverStub{session: plainSession{}}
	m := newTestManager(resolver, &bindingStoreStub{byAgent: map[string]*contract.CacheKeepaliveBinding{"agent-1": {AgentID: "agent-1"}}}, nil)
	if m.canPing(context.Background(), &agentTimer{agentID: "agent-1", threadID: "thread-1"}) {
		t.Fatal("canPing() = true, want false when session is not KeepaliveCapable")
	}
	if len(resolver.threadIDs) != 1 || resolver.threadIDs[0] != "thread-1" {
		t.Fatalf("ResolveSession() threads = %v, want [thread-1]", resolver.threadIDs)
	}
}

func TestShutdownClearsTimers(t *testing.T) {
	t.Parallel()
	m := newTestManager(nil, nil, nil)
	m.register("session-1", "agent-1", "thread-1")
	m.register("session-2", "agent-2", "thread-2")
	m.ResetTimerByAgent("agent-1")
	m.ResetTimerByAgent("agent-2")
	first := requireAgentTimer(t, m.snapshotTimer("session-1", nil), "first")
	second := requireAgentTimer(t, m.snapshotTimer("session-2", nil), "second")
	if err := m.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown() error = %v", err)
	}
	if len(m.timers) != 0 {
		t.Fatalf("shutdown timers=%d, want 0", len(m.timers))
	}
	if first.timer.Stop() {
		t.Fatalf("first timer remained active after shutdown: %#v", first)
	}
	if second.timer.Stop() {
		t.Fatalf("second timer remained active after shutdown: %#v", second)
	}
}

func requireAgentTimer(t *testing.T, got *agentTimer, label string) *agentTimer {
	t.Helper()
	if got == nil {
		t.Fatalf("%s timer = nil", label)
	}
	if got.timer == nil {
		t.Fatalf("%s timer.timer = nil: %#v", label, got)
	}
	return got
}

func TestHandleAgentLaunchedFallback(t *testing.T) {
	t.Parallel()
	threads := &threadStoreStub{byThread: map[string]*contract.CacheKeepaliveThreadRef{"thread-1": {ThreadID: "thread-1", AgentID: "agent-2"}}}
	m := newTestManager(nil, &bindingStoreStub{}, threads)
	t.Cleanup(m.shutdownForTest)
	ev := agentdto.AgentLaunched{}
	ev.AgentID, ev.ThreadID, ev.SessionID = "missing-agent", "thread-1", "session-1"
	m.HandleAgentLaunched(ev)
	got := m.timers["session-1"]
	if got == nil || got.agentID != "agent-2" || got.threadID != "thread-1" {
		t.Fatalf("fallback register = %#v, want agent-2/thread-1", got)
	}
	if len(threads.lookups) != 1 || threads.lookups[0] != "thread-1" {
		t.Fatalf("GetByThreadID() lookups = %v, want [thread-1]", threads.lookups)
	}
}

func TestStopTimerByAgent(t *testing.T) {
	t.Parallel()
	m := newTestManager(nil, nil, nil)
	t.Cleanup(m.shutdownForTest)
	m.register("session-1", "agent-1", "thread-1")
	m.register("session-2", "agent-2", "thread-2")
	m.ResetTimerByAgent("agent-1")
	m.ResetTimerByAgent("agent-2")
	first := m.snapshotTimer("session-1", nil)
	m.StopTimerByAgent("agent-1")
	if _, ok := m.timers["session-1"]; ok {
		t.Fatal("session-1 timer still present after stop")
	}
	if _, ok := m.timers["session-2"]; !ok {
		t.Fatal("session-2 timer removed unexpectedly")
	}
	if first == nil || first.timer == nil || first.timer.Stop() {
		t.Fatalf("session-1 timer stop mismatch: %#v", first)
	}
}

func TestDeliverPingReschedulesOnFailure(t *testing.T) {
	t.Parallel()
	m := newTestManager(nil, nil, nil)
	t.Cleanup(m.shutdownForTest)
	m.register("session-1", "agent-1", "thread-1")
	before := requireAgentTimer(t, m.snapshotTimer("session-1", nil), "before")

	// keepalive ping 失败后仍必须重新布置 timer：静默 turn 不会派发 TurnCompleted。
	// deliverPing 是失败路径维持 keepalive 循环的唯一入口。
	timerRef := &agentTimer{sessionUUID: "session-1", agentID: "agent-1", threadID: "thread-1"}
	m.deliverPing(context.Background(), "session-1", timerRef, &failingKeepaliveSession{})

	after := requireAgentTimer(t, m.snapshotTimer("session-1", nil), "after")
	if before.timer == after.timer {
		t.Fatal("deliverPing did not reschedule the keepalive timer after a failed ping")
	}
}
