package thread

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/module/turn"
	bindingstore "github.com/anthropic-ai/super-agent-v3/internal/store/binding"
)

func TestStopInterruptsTurnAndCleansThreadState(t *testing.T) {
	t.Parallel()

	turns := &stubTurnService{}
	orch := &stubThreadOrchestration{}
	svc := &service{
		bindingStore: &stubThreadBindingStore{binding: &bindingstore.Binding{
			AgentID:          "agent-1",
			Provider:         "codex",
			ProviderThreadID: "thread-1",
		}},
		sessions: &stubThreadSessions{
			agentID: "agent-1",
			session: &stubThreadSession{threadID: "thread-1"},
		},
		turns:         turns,
		orchestration: orch,
	}
	if err := svc.Stop(context.Background(), "agent-1"); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	if !reflect.DeepEqual(turns.interruptCalls, []string{"thread-1:thread_stopped"}) {
		t.Fatalf("interrupt calls = %#v", turns.interruptCalls)
	}
	if orch.stoppedAgentID != "agent-1" {
		t.Fatalf("stopped agent = %q, want %q", orch.stoppedAgentID, "agent-1")
	}
	wantCleanup := map[string]struct{}{
		"agent-1:thread_stopped":  {},
		"thread-1:thread_stopped": {},
	}
	if len(turns.cleanupCalls) != len(wantCleanup) {
		t.Fatalf("cleanup calls = %#v", turns.cleanupCalls)
	}
	for _, call := range turns.cleanupCalls {
		if _, ok := wantCleanup[call]; !ok {
			t.Fatalf("unexpected cleanup call %q", call)
		}
	}
}

type stubThreadBindingStore struct {
	binding *bindingstore.Binding
}

func (s *stubThreadBindingStore) GetByProviderThread(context.Context, string, string) (*bindingstore.Binding, error) {
	return nil, errors.New("not found")
}
func (s *stubThreadBindingStore) Upsert(context.Context, bindingstore.UpsertParams) error { return nil }
func (s *stubThreadBindingStore) DeleteByAgentID(context.Context, string) error           { return nil }
func (s *stubThreadBindingStore) UpdateSessionUUID(context.Context, bindingstore.UpdateSessionUUIDParams) error {
	return nil
}
func (s *stubThreadBindingStore) SetArchived(context.Context, bindingstore.SetArchivedParams) error {
	return nil
}
func (s *stubThreadBindingStore) GetByAgentID(_ context.Context, agentID string) (*bindingstore.Binding, error) {
	if s.binding != nil && s.binding.AgentID == agentID {
		return s.binding, nil
	}
	return nil, errors.New("not found")
}

type stubThreadSessions struct {
	agentID string
	session contract.Session
}

func (s *stubThreadSessions) GetSession(agentID string) (contract.Session, error) {
	if s.session != nil && agentID == s.agentID {
		return s.session, nil
	}
	return nil, errors.New("not found")
}
func (s *stubThreadSessions) RemoveSession(string) {}

type stubThreadSession struct {
	threadID string
}

func (s *stubThreadSession) ThreadID() string { return s.threadID }
func (s *stubThreadSession) Capabilities() dto.CapabilitySet {
	return nil
}
func (s *stubThreadSession) StartTurn(context.Context, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, nil
}
func (s *stubThreadSession) Interrupt(context.Context, dto.InterruptRequest) error { return nil }
func (s *stubThreadSession) ForceComplete(context.Context, dto.ForceCompleteRequest) error {
	return nil
}
func (s *stubThreadSession) ListThreads(context.Context) ([]dto.ThreadRef, error) {
	return nil, nil
}
func (s *stubThreadSession) ForkThread(context.Context, dto.ForkRequest) (dto.ForkResult, error) {
	return dto.ForkResult{}, nil
}
func (s *stubThreadSession) ReadHistory(context.Context, string, int) ([]dto.Message, error) {
	return nil, nil
}
func (s *stubThreadSession) Configure(context.Context, dto.ThreadConfigPatch) error { return nil }
func (s *stubThreadSession) Close(context.Context) error                            { return nil }
func (s *stubThreadSession) ForceStop() error                                       { return nil }

type stubTurnService struct {
	interruptCalls []string
	cleanupCalls   []string
}

func (s *stubTurnService) PrepareTurn(context.Context, contract.Session, turn.PrepareInput) (dto.TurnRequest, error) {
	return dto.TurnRequest{}, nil
}
func (s *stubTurnService) StartTurn(context.Context, contract.Session, dto.TurnRequest) (contract.TurnHandle, error) {
	return nil, nil
}
func (s *stubTurnService) SteerTurn(context.Context, contract.Session, string) (contract.TurnHandle, error) {
	return nil, nil
}
func (s *stubTurnService) InterruptTurn(context.Context, contract.Session, string) error {
	return nil
}
func (s *stubTurnService) InterruptActiveTurn(_ context.Context, session contract.Session, source string) error {
	s.interruptCalls = append(s.interruptCalls, session.ThreadID()+":"+source)
	return nil
}
func (s *stubTurnService) ForceCompleteTurn(context.Context, contract.Session) error { return nil }
func (s *stubTurnService) CleanupThread(_ context.Context, threadID, reason string) error {
	s.cleanupCalls = append(s.cleanupCalls, threadID+":"+reason)
	return nil
}
func (s *stubTurnService) TrackTurn(context.Context, string) (turn.TurnStatus, error) {
	return turn.TurnStatus{}, nil
}

type stubThreadOrchestration struct {
	stoppedAgentID string
}

func (s *stubThreadOrchestration) LaunchAgent(context.Context, LaunchAgentRequest) error { return nil }
func (s *stubThreadOrchestration) StopAgent(_ context.Context, agentID string) error {
	s.stoppedAgentID = agentID
	return nil
}
func (s *stubThreadOrchestration) Recover(context.Context, string) error { return nil }
func (s *stubThreadOrchestration) BindSessionGeneration(context.Context, string, uint64) error {
	return nil
}
