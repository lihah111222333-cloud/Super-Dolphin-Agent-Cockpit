package thread

import (
	"context"
	"errors"
	"testing"
	"time"

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
		binding:  &bindingstore.Binding{AgentID: "agent-1", SessionUUID: "fallback-agent-1"},
		updateCh: make(chan struct{}, 1),
	}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)
	svc.bindDispatcher(dispatcher)
	cancels := registerThreadSubscriptions(svc)
	defer cancelThreadSubscriptions(cancels)

	const realUUID = "019d5f6b-fb3c-7760-9d6f-54005553f5b3"
	event.Publish(dispatcher, newAgentLaunchedEvent("agent-1", "thread-1", realUUID))
	select {
	case <-bindings.updateCh:
	case <-time.After(time.Second):
		t.Fatal("expected session update")
	}

	if len(bindings.sessionUpdates) != 1 {
		t.Fatalf("len(sessionUpdates) = %d, want 1", len(bindings.sessionUpdates))
	}
	got := bindings.sessionUpdates[0]
	if got.AgentID != "agent-1" || got.SessionUUID != realUUID || got.UpdatedAt == 0 {
		t.Fatalf("session update = %#v", got)
	}
	if bindings.binding.SessionUUID != realUUID {
		t.Fatalf("binding.SessionUUID = %q, want %s", bindings.binding.SessionUUID, realUUID)
	}
}

func TestOnAgentLaunchedSkipsUnchangedSessionUUID(t *testing.T) {
	t.Parallel()

	bindings := &eventBindingStore{binding: &bindingstore.Binding{AgentID: "agent-1", SessionUUID: "session-uuid-1"}}
	svc := NewService(silentLogger(), nil, bindings, nil, nil, nil, nil, nil).(*service)

	svc.onAgentLaunched(newAgentLaunchedEvent(" agent-1 ", "thread-1", " session-uuid-1 "))

	if len(bindings.sessionUpdates) != 0 {
		t.Fatalf("session updates = %#v, want none", bindings.sessionUpdates)
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

type eventBindingStore struct {
	binding        *bindingstore.Binding
	sessionUpdates []bindingstore.UpdateSessionUUIDParams
	updateCh       chan struct{}
}

func (s *eventBindingStore) GetByProviderThread(context.Context, string, string) (*bindingstore.Binding, error) {
	return nil, errors.New("not found")
}
func (s *eventBindingStore) Upsert(context.Context, bindingstore.UpsertParams) error { return nil }
func (s *eventBindingStore) DeleteByAgentID(context.Context, string) error           { return nil }
func (s *eventBindingStore) UpdateSessionUUID(_ context.Context, params bindingstore.UpdateSessionUUIDParams) error {
	s.sessionUpdates = append(s.sessionUpdates, params)
	if s.binding != nil && s.binding.AgentID == params.AgentID {
		s.binding.SessionUUID = params.SessionUUID
	}
	if s.updateCh != nil {
		select {
		case s.updateCh <- struct{}{}:
		default:
		}
	}
	return nil
}
func (s *eventBindingStore) SetArchived(context.Context, bindingstore.SetArchivedParams) error {
	return nil
}
func (s *eventBindingStore) GetByAgentID(_ context.Context, agentID string) (*bindingstore.Binding, error) {
	if s.binding != nil && s.binding.AgentID == agentID {
		return s.binding, nil
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
func (s *eventBindingStore) UpdateAgentCwd(context.Context, bindingstore.UpdateAgentCwdParams) error {
	return nil
}
