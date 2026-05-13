package thread

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	sharedto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
	"github.com/kelindar/event"
)

func TestThreadSubscriptionsUpdateSessionUUIDFromAgentLaunched(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	bindings := &eventBindingStore{
		binding:  &bindingstore.Binding{AgentID: "agent-1", Provider: "codex", ProviderThreadID: "agent-1", SessionUUID: "fallback-agent-1", RolloutPath: writeExistingProviderHistoryFile(t)},
		updateCh: make(chan struct{}, 1),
	}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)
	svc.bindDispatcher(dispatcher)
	// P22 P2 thread S4: onAgentLaunched Enqueues into agentLaunchedWorker;
	// the worker must be started for the test to observe the downstream
	// UpdateSessionUUID write.
	svc.startBusWorkers()
	cancels := registerThreadSubscriptions(svc)
	defer func() {
		cancelThreadSubscriptions(cancels)
		svc.stopBusWorkers(context.Background())
	}()

	const realUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	event.Publish(dispatcher, newAgentLaunchedEvent("agent-1", "thread-1", realUUID))
	select {
	case <-bindings.updateCh:
	case <-time.After(time.Second):
		t.Fatal("expected session update")
	}

	sessionUpdates := bindings.SessionUpdates()
	if len(sessionUpdates) != 1 {
		t.Fatalf("len(sessionUpdates) = %d, want 1", len(sessionUpdates))
	}
	got := sessionUpdates[0]
	if got.AgentID != "agent-1" || got.SessionUUID != realUUID || got.UpdatedAt == 0 {
		t.Fatalf("session update = %#v", got)
	}
	if gotBinding := bindings.Binding(); gotBinding == nil || gotBinding.SessionUUID != realUUID {
		t.Fatalf("binding.SessionUUID = %q, want %s", bindingSessionUUID(gotBinding), realUUID)
	}
	providerUpdates := waitForProviderThreadUpdates(t, bindings, 1, time.Second)
	if len(providerUpdates) != 1 {
		t.Fatalf("len(providerUpdates) = %d, want 1", len(providerUpdates))
	}
	if got := providerUpdates[0]; got.AgentID != "agent-1" || got.ProviderThreadID != realUUID || got.UpdatedAt == 0 {
		t.Fatalf("provider thread update = %#v", got)
	}
	if gotBinding := bindings.Binding(); gotBinding == nil || gotBinding.ProviderThreadID != realUUID {
		t.Fatalf("binding.ProviderThreadID = %q, want %s", bindingProviderThreadID(gotBinding), realUUID)
	}
}

func TestAgentLaunchedDoesNotPromoteProviderThreadIDWithoutHistoryFile(t *testing.T) {
	t.Parallel()

	const realUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	bindings := &eventBindingStore{
		binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "agent-1",
			SessionUUID:      "fallback-agent-1",
		},
	}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)

	svc.processAgentLaunched(newAgentLaunchedEvent("agent-1", "thread-1", realUUID))

	if len(bindings.SessionUpdates()) != 1 {
		t.Fatalf("session updates = %d, want 1", len(bindings.SessionUpdates()))
	}
	if providerUpdates := bindings.ProviderThreadUpdates(); len(providerUpdates) != 0 {
		t.Fatalf("provider thread updates = %#v, want none without history file", providerUpdates)
	}
	if gotBinding := bindings.Binding(); gotBinding == nil || gotBinding.ProviderThreadID != "agent-1" {
		t.Fatalf("binding.ProviderThreadID = %q, want placeholder retained", bindingProviderThreadID(gotBinding))
	}
}

func TestOnAgentLaunchedSkipsUnchangedSessionUUID(t *testing.T) {
	t.Parallel()

	bindings := &eventBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1", SessionUUID: "session-uuid-1"}}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)
	// P22 P2 thread S4: Start + Stop the worker so the Enqueue lands and
	// drains synchronously before the assertion runs.
	svc.startBusWorkers()

	svc.onAgentLaunched(newAgentLaunchedEvent(" agent-1 ", "thread-1", " session-uuid-1 "))

	if err := svc.agentLaunchedWorker.Stop(context.Background()); err != nil {
		t.Fatalf("agent launched worker Stop: %v", err)
	}

	if sessionUpdates := bindings.SessionUpdates(); len(sessionUpdates) != 0 {
		t.Fatalf("session updates = %#v, want none", sessionUpdates)
	}
}

func TestOnAgentLaunchedUpdatesCWDAndInvalidatesWorktreePromptCache(t *testing.T) {
	t.Parallel()

	repoRoot, worktreeCWD := newPromptGitFixture(t)
	promptAssembly := &stubPromptAssemblyService{}
	bindings := &eventBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1", Cwd: repoRoot}}
	svc := NewServiceWithPromptAssembly(silentLogger(), nil, bindings, nil, nil, nil, nil, nil, promptAssembly, nil, nil).(*service)
	// P22 P2 thread S4: Start + Stop the worker so the Enqueue lands and
	// drains synchronously before the assertion runs.
	svc.startBusWorkers()

	ev := newAgentLaunchedEvent("agent-1", "thread-1", "")
	ev.CWD = worktreeCWD
	svc.onAgentLaunched(ev)

	if err := svc.agentLaunchedWorker.Stop(context.Background()); err != nil {
		t.Fatalf("agent launched worker Stop: %v", err)
	}

	cwdUpdates := bindings.CWDUpdates()
	if len(cwdUpdates) != 1 {
		t.Fatalf("cwd updates = %#v, want 1 update", cwdUpdates)
	}
	if got := cwdUpdates[0]; got.AgentID != "agent-1" || got.Cwd != worktreeCWD || got.UpdatedAt == 0 {
		t.Fatalf("cwd update = %#v", got)
	}
	if gotBinding := bindings.Binding(); gotBinding == nil || gotBinding.Cwd != worktreeCWD {
		t.Fatalf("binding.Cwd = %q, want %q", bindingCWD(gotBinding), worktreeCWD)
	}
	if got := promptAssembly.invalidated; len(got) != 1 || got[0] != contract.InvalidateWorktree {
		t.Fatalf("Invalidate calls = %#v, want [%q]", got, contract.InvalidateWorktree)
	}
	if sessionUpdates := bindings.SessionUpdates(); len(sessionUpdates) != 0 {
		t.Fatalf("session updates = %#v, want none", sessionUpdates)
	}
}

func newAgentLaunchedEvent(agentID, threadID, sessionID string) agentdto.AgentLaunched {
	return agentdto.AgentLaunched{
		AgentSessionHeader: sharedto.AgentSessionHeader{
			AgentHeader: sharedto.AgentHeader{
				ThreadHeader: sharedto.ThreadHeader{ThreadID: threadID},
				AgentID:      agentID,
			},
			SessionID: sessionID,
		},
	}
}

func cancelThreadSubscriptions(cancels []context.CancelFunc) {
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}

func waitForProviderThreadUpdates(t *testing.T, bindings *eventBindingStore, want int, d time.Duration) []bindingstore.UpdateProviderThreadIDParams {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		updates := bindings.ProviderThreadUpdates()
		if len(updates) >= want {
			return updates
		}
		time.Sleep(10 * time.Millisecond)
	}
	return bindings.ProviderThreadUpdates()
}

type eventBindingStore struct {
	mu              sync.RWMutex
	binding         *bindingstore.Binding
	sessionUpdates  []bindingstore.UpdateSessionUUIDParams
	providerUpdates []bindingstore.UpdateProviderThreadIDParams
	cwdUpdates      []bindingstore.UpdateAgentCwdParams
	updateCh        chan struct{}
}

func (s *eventBindingStore) GetByProviderThread(context.Context, string, string) (*bindingstore.Binding, error) {
	return nil, errors.New("not found")
}
func (s *eventBindingStore) Upsert(context.Context, bindingstore.UpsertParams) error { return nil }
func (s *eventBindingStore) DeleteByAgentID(context.Context, string) error           { return nil }
func (s *eventBindingStore) UpdateSessionUUID(_ context.Context, params bindingstore.UpdateSessionUUIDParams) error {
	s.mu.Lock()
	s.sessionUpdates = append(s.sessionUpdates, params)
	if s.binding != nil && s.binding.AgentID == params.AgentID {
		s.binding.SessionUUID = params.SessionUUID
	}
	updateCh := s.updateCh
	s.mu.Unlock()
	if updateCh != nil {
		select {
		case updateCh <- struct{}{}:
		default:
		}
	}
	return nil
}
func (s *eventBindingStore) SetArchived(context.Context, bindingstore.SetArchivedParams) error {
	return nil
}
func (s *eventBindingStore) UpdateProviderThreadID(_ context.Context, params bindingstore.UpdateProviderThreadIDParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providerUpdates = append(s.providerUpdates, params)
	if s.binding != nil && s.binding.AgentID == params.AgentID {
		s.binding.ProviderThreadID = params.ProviderThreadID
	}
	return nil
}
func (s *eventBindingStore) GetByAgentID(_ context.Context, agentID string) (*bindingstore.Binding, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.binding != nil && s.binding.AgentID == agentID {
		binding := *s.binding
		return &binding, nil
	}
	return nil, errors.New("not found")
}
func (s *eventBindingStore) BindAgentThread(context.Context, bindingstore.BindAgentThreadParams) error {
	return nil
}
func (s *eventBindingStore) UnbindAgentThread(context.Context, string) error { return nil }
func (s *eventBindingStore) ListAgentThreadBindings(context.Context) ([]bindingstore.Binding, error) {
	return nil, nil
}
func (s *eventBindingStore) GetThreadByAgent(context.Context, string) (string, error) {
	return "", errors.New("not found")
}
func (s *eventBindingStore) UpdateAgentCwd(_ context.Context, params bindingstore.UpdateAgentCwdParams) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cwdUpdates = append(s.cwdUpdates, params)
	if s.binding != nil && s.binding.AgentID == params.AgentID {
		s.binding.Cwd = params.Cwd
	}
	return nil
}

func (s *eventBindingStore) Rebind(context.Context, bindingstore.RebindParams) error { return nil }

func (s *eventBindingStore) ListProviderMap(context.Context) (map[string]string, error) {
	return nil, nil
}

func (s *eventBindingStore) ListCwdMap(context.Context) (map[string]string, error) {
	return nil, nil
}

func (s *eventBindingStore) SessionUpdates() []bindingstore.UpdateSessionUUIDParams {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]bindingstore.UpdateSessionUUIDParams, len(s.sessionUpdates))
	copy(out, s.sessionUpdates)
	return out
}

func (s *eventBindingStore) ProviderThreadUpdates() []bindingstore.UpdateProviderThreadIDParams {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]bindingstore.UpdateProviderThreadIDParams, len(s.providerUpdates))
	copy(out, s.providerUpdates)
	return out
}

func (s *eventBindingStore) CWDUpdates() []bindingstore.UpdateAgentCwdParams {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]bindingstore.UpdateAgentCwdParams, len(s.cwdUpdates))
	copy(out, s.cwdUpdates)
	return out
}

func (s *eventBindingStore) Binding() *bindingstore.Binding {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.binding == nil {
		return nil
	}
	binding := *s.binding
	return &binding
}

func bindingSessionUUID(binding *bindingstore.Binding) string {
	if binding == nil {
		return ""
	}
	return binding.SessionUUID
}

func bindingProviderThreadID(binding *bindingstore.Binding) string {
	if binding == nil {
		return ""
	}
	return binding.ProviderThreadID
}

func bindingCWD(binding *bindingstore.Binding) string {
	if binding == nil {
		return ""
	}
	return binding.Cwd
}
