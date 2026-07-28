package thread

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	sharedto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/shared"
	bindingstore "github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/binding"
)

func TestThreadSubscriptionsUpdateSessionUUIDFromAgentLaunched(t *testing.T) {
	t.Parallel()

	dispatcher := event.NewDispatcher()
	defer func() { _ = dispatcher.Close() }()

	bindings := &eventBindingStore{
		binding:  &BindingRecord{AgentID: "agent-1", Provider: "codex", ProviderThreadID: "agent-1", SessionUUID: "fallback-agent-1", RolloutPath: writeExistingProviderHistoryFile(t)},
		updateCh: make(chan struct{}, 1),
	}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)
	svc.bindDispatcher(dispatcher)
	// onAgentLaunched 会把事件放入 agentLaunchedWorker；
	// 测试必须启动 worker，才能观察后续 UpdateSessionUUID 写入。
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
	assertSessionUUIDUpdate(t, sessionUpdates, "agent-1", realUUID)
	assertBindingSessionUUID(t, bindings.Binding(), realUUID)
	providerUpdates := waitForProviderThreadUpdates(t, bindings, 1, time.Second)
	assertProviderThreadUpdate(t, providerUpdates, "agent-1", realUUID)
	assertBindingProviderThreadID(t, bindings.Binding(), realUUID)
}

func TestAgentLaunchedPromotesAuthoritativeProviderThreadIDBeforeHistoryFileExists(t *testing.T) {
	t.Parallel()

	const realUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	bindings := &eventBindingStore{
		binding: &BindingRecord{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "agent-1",
			SessionUUID:      "fallback-agent-1",
		},
	}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)

	if err := svc.processAgentLaunched(newAgentLaunchedEvent("agent-1", "thread-1", realUUID)); err != nil {
		t.Fatalf("processAgentLaunched: %v", err)
	}

	if len(bindings.SessionUpdates()) != 1 {
		t.Fatalf("session updates = %d, want 1", len(bindings.SessionUpdates()))
	}
	assertProviderThreadUpdate(t, bindings.ProviderThreadUpdates(), "agent-1", realUUID)
	assertBindingProviderThreadID(t, bindings.Binding(), realUUID)
}

func TestAgentLaunchedKeepsSessionAndProviderThreadIdentitiesDistinct(t *testing.T) {
	t.Parallel()

	const sessionUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	const providerThreadID = "019fa810-3386-7b20-a93a-eb5c04fa8b19"
	bindings := &eventBindingStore{binding: &BindingRecord{AgentID: "agent-parent", Provider: "codex"}}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)
	ev := newAgentLaunchedEvent("agent-parent", "agent-parent", sessionUUID)
	ev.ProviderThreadID = providerThreadID

	if err := svc.processAgentLaunched(ev); err != nil {
		t.Fatalf("processAgentLaunched: %v", err)
	}

	assertSessionUUIDUpdate(t, bindings.SessionUpdates(), "agent-parent", sessionUUID)
	assertProviderThreadUpdate(t, bindings.ProviderThreadUpdates(), "agent-parent", providerThreadID)
	assertBindingProviderThreadID(t, bindings.Binding(), providerThreadID)
}

func TestProcessAgentLaunchedReturnsBindingStoreError(t *testing.T) {
	wantErr := errors.New("binding store unavailable")
	bindings := &eventBindingStore{getByAgentErr: wantErr}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)

	err := svc.processAgentLaunched(newAgentLaunchedEvent("agent-1", "", ""))
	if !errors.Is(err, wantErr) {
		t.Fatalf("processAgentLaunched error = %v, want %v", err, wantErr)
	}
}

func TestOnAgentLaunchedSkipsUnchangedSessionUUID(t *testing.T) {
	t.Parallel()

	bindings := &eventBindingStore{binding: &BindingRecord{AgentID: "agent-1", SessionUUID: "session-uuid-1"}}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)
	// 启动再停止 worker，让入队事件在断言前同步 drain 完成。
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

	_, worktreeCWD := newPromptGitFixture(t)
	promptAssembly := &stubPromptAssemblyService{}
	bindings := &eventBindingStore{binding: &BindingRecord{AgentID: "agent-1"}}
	svc := NewServiceWithPromptAssembly(silentLogger(), nil, bindings, nil, nil, nil, nil, nil, promptAssembly, nil, nil).(*service)
	// 启动再停止 worker，让入队事件在断言前同步 drain 完成。
	svc.startBusWorkers()

	ev := newAgentLaunchedEvent("agent-1", "thread-1", "")
	ev.CWD = worktreeCWD
	svc.onAgentLaunched(ev)

	if err := svc.agentLaunchedWorker.Stop(context.Background()); err != nil {
		t.Fatalf("agent launched worker Stop: %v", err)
	}

	cwdUpdates := bindings.CWDUpdates()
	assertCWDUpdate(t, cwdUpdates, "agent-1", worktreeCWD)
	assertBindingCWD(t, bindings.Binding(), worktreeCWD)
	if got := promptAssembly.invalidated; len(got) != 1 || got[0] != contract.InvalidateWorktree {
		t.Fatalf("Invalidate calls = %#v, want [%q]", got, contract.InvalidateWorktree)
	}
	if sessionUpdates := bindings.SessionUpdates(); len(sessionUpdates) != 0 {
		t.Fatalf("session updates = %#v, want none", sessionUpdates)
	}
}

func TestOnAgentLaunchedRejectsMismatchedCWDFromExistingBinding(t *testing.T) {
	t.Parallel()

	bindings := &eventBindingStore{binding: &BindingRecord{AgentID: "agent-1", Cwd: "/repo/stored"}}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)
	svc.startBusWorkers()

	ev := newAgentLaunchedEvent("agent-1", "thread-1", "")
	ev.CWD = "/repo/active-window"
	svc.onAgentLaunched(ev)

	if err := svc.agentLaunchedWorker.Stop(context.Background()); err != nil {
		t.Fatalf("agent launched worker Stop: %v", err)
	}

	if updates := bindings.CWDUpdates(); len(updates) != 0 {
		t.Fatalf("cwd updates = %#v, want none on mismatch", updates)
	}
	assertBindingCWD(t, bindings.Binding(), "/repo/stored")
}

func assertSessionUUIDUpdate(t *testing.T, updates []BindingSessionUUIDUpdate, wantAgentID, wantUUID string) {
	t.Helper()
	if len(updates) != 1 {
		t.Fatalf("len(sessionUpdates) = %d, want 1", len(updates))
	}
	got := updates[0]
	if got.AgentID != wantAgentID {
		t.Fatalf("session update AgentID = %q, want %s; update=%#v", got.AgentID, wantAgentID, got)
	}
	if got.SessionUUID != wantUUID {
		t.Fatalf("session update UUID = %q, want %s; update=%#v", got.SessionUUID, wantUUID, got)
	}
	if got.UpdatedAt == 0 {
		t.Fatalf("session update UpdatedAt = 0; update=%#v", got)
	}
}

func assertBindingSessionUUID(t *testing.T, got *BindingRecord, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("binding = nil, want SessionUUID %s", want)
	}
	if got.SessionUUID != want {
		t.Fatalf("binding.SessionUUID = %q, want %s", got.SessionUUID, want)
	}
}

func assertProviderThreadUpdate(t *testing.T, updates []BindingProviderThreadIDUpdate, wantAgentID, wantThreadID string) {
	t.Helper()
	if len(updates) != 1 {
		t.Fatalf("len(providerUpdates) = %d, want 1", len(updates))
	}
	got := updates[0]
	if got.AgentID != wantAgentID {
		t.Fatalf("provider update AgentID = %q, want %s; update=%#v", got.AgentID, wantAgentID, got)
	}
	if got.ProviderThreadID != wantThreadID {
		t.Fatalf("provider update ThreadID = %q, want %s; update=%#v", got.ProviderThreadID, wantThreadID, got)
	}
	if got.UpdatedAt == 0 {
		t.Fatalf("provider update UpdatedAt = 0; update=%#v", got)
	}
}

func assertBindingProviderThreadID(t *testing.T, got *BindingRecord, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("binding = nil, want ProviderThreadID %s", want)
	}
	if got.ProviderThreadID != want {
		t.Fatalf("binding.ProviderThreadID = %q, want %s", got.ProviderThreadID, want)
	}
}

func assertCWDUpdate(t *testing.T, updates []BindingCWDUpdate, wantAgentID, wantCWD string) {
	t.Helper()
	if len(updates) != 1 {
		t.Fatalf("cwd updates = %#v, want 1 update", updates)
	}
	got := updates[0]
	if got.AgentID != wantAgentID {
		t.Fatalf("cwd update AgentID = %q, want %s; update=%#v", got.AgentID, wantAgentID, got)
	}
	if got.Cwd != wantCWD {
		t.Fatalf("cwd update Cwd = %q, want %s; update=%#v", got.Cwd, wantCWD, got)
	}
	if got.UpdatedAt == 0 {
		t.Fatalf("cwd update UpdatedAt = 0; update=%#v", got)
	}
}

func assertBindingCWD(t *testing.T, got *BindingRecord, want string) {
	t.Helper()
	if got == nil {
		t.Fatalf("binding = nil, want Cwd %s", want)
	}
	if got.Cwd != want {
		t.Fatalf("binding.Cwd = %q, want %q", got.Cwd, want)
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
		ProviderThreadID: sessionID,
	}
}

func cancelThreadSubscriptions(cancels []context.CancelFunc) {
	for _, cancel := range cancels {
		if cancel != nil {
			cancel()
		}
	}
}

func waitForProviderThreadUpdates(t *testing.T, bindings *eventBindingStore, want int, d time.Duration) []BindingProviderThreadIDUpdate {
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
	eventBindingLookupNoopStore
	eventBindingMapNoopStore

	mu              sync.RWMutex
	binding         *BindingRecord
	sessionUpdates  []BindingSessionUUIDUpdate
	providerUpdates []BindingProviderThreadIDUpdate
	cwdUpdates      []BindingCWDUpdate
	updateCh        chan struct{}
	getByAgentErr   error
}

type eventBindingLookupNoopStore struct{}

func (eventBindingLookupNoopStore) GetByProviderThread(context.Context, string, string) (*BindingRecord, error) {
	return nil, errors.New("not found")
}
func (eventBindingLookupNoopStore) Upsert(context.Context, BindingUpsert) error {
	return nil
}
func (eventBindingLookupNoopStore) DeleteByAgentID(context.Context, string) error { return nil }
func (s *eventBindingStore) UpdateSessionUUID(_ context.Context, params BindingSessionUUIDUpdate) error {
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
func (s *eventBindingStore) UpdateProviderThreadID(_ context.Context, params BindingProviderThreadIDUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.providerUpdates = append(s.providerUpdates, params)
	if s.binding != nil && s.binding.AgentID == params.AgentID {
		s.binding.ProviderThreadID = params.ProviderThreadID
	}
	return nil
}
func (s *eventBindingStore) GetByAgentID(_ context.Context, agentID string) (*BindingRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.getByAgentErr != nil {
		return nil, s.getByAgentErr
	}
	if s.binding != nil && s.binding.AgentID == agentID {
		binding := *s.binding
		return &binding, nil
	}
	return nil, contract.ErrNotFound
}
func (eventBindingLookupNoopStore) BindAgentThread(context.Context, bindingstore.BindAgentThreadParams) error {
	return nil
}
func (eventBindingLookupNoopStore) UnbindAgentThread(context.Context, string) error { return nil }
func (eventBindingLookupNoopStore) ListAgentThreadBindings(context.Context) ([]BindingRecord, error) {
	return nil, nil
}
func (eventBindingLookupNoopStore) GetThreadByAgent(context.Context, string) (string, error) {
	return "", errors.New("not found")
}
func (s *eventBindingStore) UpdateAgentCwd(_ context.Context, params BindingCWDUpdate) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cwdUpdates = append(s.cwdUpdates, params)
	if s.binding != nil && s.binding.AgentID == params.AgentID {
		s.binding.Cwd = params.Cwd
	}
	return nil
}

type eventBindingMapNoopStore struct{}

func (eventBindingMapNoopStore) Rebind(context.Context, bindingstore.RebindParams) error { return nil }

func (eventBindingMapNoopStore) ListProviderMap(context.Context) (map[string]string, error) {
	return nil, nil
}

func (eventBindingMapNoopStore) ListCwdMap(context.Context) (map[string]string, error) {
	return nil, nil
}

func (s *eventBindingStore) SessionUpdates() []BindingSessionUUIDUpdate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]BindingSessionUUIDUpdate, len(s.sessionUpdates))
	copy(out, s.sessionUpdates)
	return out
}

func (s *eventBindingStore) ProviderThreadUpdates() []BindingProviderThreadIDUpdate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]BindingProviderThreadIDUpdate, len(s.providerUpdates))
	copy(out, s.providerUpdates)
	return out
}

func (s *eventBindingStore) CWDUpdates() []BindingCWDUpdate {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]BindingCWDUpdate, len(s.cwdUpdates))
	copy(out, s.cwdUpdates)
	return out
}

func (s *eventBindingStore) Binding() *BindingRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.binding == nil {
		return nil
	}
	binding := *s.binding
	return &binding
}

func bindingProviderThreadID(binding *BindingRecord) string {
	if binding == nil {
		return ""
	}
	return binding.ProviderThreadID
}
