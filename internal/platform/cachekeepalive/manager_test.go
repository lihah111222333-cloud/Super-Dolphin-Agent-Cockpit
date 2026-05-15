package cachekeepalive

import (
	"context"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	threadstore "github.com/anthropic-ai/super-agent-v3/internal/store/thread"
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

type plainSession struct{}

func (plainSession) ThreadID() string                { return "" }
func (plainSession) RolloutPath() string             { return "" }
func (plainSession) Capabilities() dto.CapabilitySet { return dto.CapabilitySet{} }
func (plainSession) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, nil
}
func (plainSession) Interrupt(context.Context, dto.InterruptRequest) error         { return nil }
func (plainSession) ForceComplete(context.Context, dto.ForceCompleteRequest) error { return nil }
func (plainSession) ListThreads(context.Context) ([]dto.ThreadRef, error)          { return nil, nil }
func (plainSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}
func (plainSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) { return nil, nil }
func (plainSession) Configure(context.Context, dto.ThreadConfigPatch) error          { return nil }
func (plainSession) Close(context.Context) error                                     { return nil }
func (plainSession) ForceStop() error                                                { return nil }

type keepaliveSession struct{ plainSession }

func (*keepaliveSession) SendKeepalive(context.Context) error { return nil }

type bindingStoreStub struct {
	byAgent map[string]*bindingstore.Binding
}

func (*bindingStoreStub) GetByProviderThread(context.Context, string, string) (*bindingstore.Binding, error) {
	return nil, nil
}
func (*bindingStoreStub) Upsert(context.Context, bindingstore.UpsertParams) error { return nil }
func (*bindingStoreStub) DeleteByAgentID(context.Context, string) error           { return nil }
func (*bindingStoreStub) UpdateSessionUUID(context.Context, bindingstore.UpdateSessionUUIDParams) error {
	return nil
}
func (*bindingStoreStub) UpdateProviderThreadID(context.Context, bindingstore.UpdateProviderThreadIDParams) error {
	return nil
}
func (*bindingStoreStub) SetArchived(context.Context, bindingstore.SetArchivedParams) error {
	return nil
}
func (s *bindingStoreStub) GetByAgentID(_ context.Context, agentID string) (*bindingstore.Binding, error) {
	if s == nil || s.byAgent == nil {
		return nil, nil
	}
	return s.byAgent[agentID], nil
}
func (*bindingStoreStub) BindAgentThread(context.Context, bindingstore.BindAgentThreadParams) error {
	return nil
}
func (*bindingStoreStub) UnbindAgentThread(context.Context, string) error { return nil }
func (*bindingStoreStub) ListAgentThreadBindings(context.Context) ([]bindingstore.Binding, error) {
	return nil, nil
}
func (*bindingStoreStub) GetThreadByAgent(context.Context, string) (string, error) { return "", nil }
func (*bindingStoreStub) UpdateAgentCwd(context.Context, bindingstore.UpdateAgentCwdParams) error {
	return nil
}

func (*bindingStoreStub) Rebind(context.Context, bindingstore.RebindParams) error { return nil }

func (*bindingStoreStub) ListProviderMap(context.Context) (map[string]string, error) {
	return nil, nil
}

func (*bindingStoreStub) ListCwdMap(context.Context) (map[string]string, error) {
	return nil, nil
}

type threadStoreStub struct {
	byThread map[string]*threadstore.Thread
	lookups  []string
}

func (s *threadStoreStub) GetByThreadID(_ context.Context, threadID string) (*threadstore.Thread, error) {
	if s == nil {
		return nil, nil
	}
	s.lookups = append(s.lookups, threadID)
	if s.byThread == nil {
		return nil, nil
	}
	return s.byThread[threadID], nil
}
func (*threadStoreStub) GetByPort(context.Context, int32) (*threadstore.Thread, error) {
	return nil, nil
}
func (*threadStoreStub) ListAll(context.Context) ([]threadstore.Thread, error) { return nil, nil }
func (*threadStoreStub) ListConfigsByIDs(context.Context, []string) ([]threadstore.Thread, error) {
	return nil, nil
}
func (*threadStoreStub) ListRunning(context.Context) ([]threadstore.Thread, error) { return nil, nil }
func (*threadStoreStub) ListRecoverable(context.Context) ([]threadstore.Thread, error) {
	return nil, nil
}
func (*threadStoreStub) ListRunningAgents(context.Context) ([]threadstore.RunningAgent, error) {
	return nil, nil
}
func (*threadStoreStub) SavePromptSnapshot(context.Context, string, threadstore.PromptSnapshot) error {
	return nil
}
func (*threadStoreStub) LoadPromptSnapshot(context.Context, string) (*threadstore.PromptSnapshot, error) {
	return nil, nil
}
func (*threadStoreStub) Upsert(context.Context, threadstore.UpsertParams) error { return nil }
func (*threadStoreStub) UpdateStatus(context.Context, threadstore.UpdateStatusParams) error {
	return nil
}
func (*threadStoreStub) UpdateLaunchResult(context.Context, threadstore.UpdateLaunchResultParams) error {
	return nil
}
func (*threadStoreStub) DeleteByThreadID(context.Context, string) error { return nil }
func (*threadStoreStub) ResetRunning(context.Context) error             { return nil }
func (*threadStoreStub) ExpireStale(context.Context, threadstore.ExpireStaleParams) (int64, error) {
	return 0, nil
}
func (*threadStoreStub) RunningExists(context.Context, string) (bool, error)       { return false, nil }
func (*threadStoreStub) ListCwds(context.Context) ([]threadstore.ThreadCwd, error) { return nil, nil }
func (*threadStoreStub) ListCwdsByPrefix(context.Context, string) ([]threadstore.ThreadCwd, error) {
	return nil, nil
}
func (*threadStoreStub) CountChildren(context.Context, string) (int64, error) { return 0, nil }
func (*threadStoreStub) Exists(context.Context, string) (bool, error)         { return false, nil }
func (*threadStoreStub) CountAll(context.Context) (int64, error)              { return 0, nil }

func newTestManager(resolver contract.SessionResolver, bindings bindingstore.Store, threads threadstore.Store) *Manager {
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

// shutdownForTest wraps Shutdown with a short bounded ctx so existing tests
// can keep using `t.Cleanup(m.shutdownForTest)` style cleanups after the
// P22 P2 signature change.
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
	m := newTestManager(resolver, &bindingStoreStub{byAgent: map[string]*bindingstore.Binding{"agent-1": {AgentID: "agent-1", Archived: true}}}, nil)
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
	m := newTestManager(resolver, &bindingStoreStub{byAgent: map[string]*bindingstore.Binding{"agent-1": {AgentID: "agent-1"}}}, nil)
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
	threads := &threadStoreStub{byThread: map[string]*threadstore.Thread{"thread-1": {ThreadID: "thread-1", AgentID: "agent-2"}}}
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
