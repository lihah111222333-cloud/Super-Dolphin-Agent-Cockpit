package turn

import (
	"context"
	"errors"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	dto "github.com/anthropic-ai/super-agent-v3/internal/dto/provider"
	"github.com/anthropic-ai/super-agent-v3/internal/module/orchestration"
)

func TestOrchestrationTurnStarterStartsQueuedTurn(t *testing.T) {
	t.Parallel()

	session := &stubSession{
		threadID: "thread-1",
		startTurn: func(_ context.Context, req dto.TurnRequest) (contract.TurnHandle, error) {
			if req.LocalID != "turn-1" {
				t.Fatalf("LocalID = %q, want turn-1", req.LocalID)
			}
			if req.ThreadID != "thread-1" {
				t.Fatalf("ThreadID = %q, want thread-1", req.ThreadID)
			}
			if len(req.Inputs) != 1 || req.Inputs[0].Content != "hello" {
				t.Fatalf("Inputs = %#v, want queued text input", req.Inputs)
			}
			if len(req.Skills) != 1 || req.Skills[0].Name != "debug" {
				t.Fatalf("Skills = %#v, want selected skill", req.Skills)
			}
			if !req.ManualSkillSelection {
				t.Fatal("ManualSkillSelection = false, want true")
			}
			if string(req.OutputSchema) != `{"type":"object"}` {
				t.Fatalf("OutputSchema = %s, want object schema", string(req.OutputSchema))
			}
			handle := newStubTurnHandle(req.LocalID, "provider-1")
			handle.complete(nil)
			return handle, nil
		},
	}
	starter := NewOrchestrationTurnStarter(
		NewService(silentLogger()),
		stubSessionProvider{session: session},
	)

	turnID, err := starter.StartTurn(context.Background(), orchestration.TurnSubmission{
		AgentID:              "agent-1",
		ThreadID:             "agent-1",
		ExpectedTurnID:       "turn-1",
		Inputs:               []InputItem{{Type: "text", Content: "hello"}},
		SelectedSkills:       []string{"debug"},
		ManualSkillSelection: true,
		OutputSchema:         []byte(`{"type":"object"}`),
	})
	if err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if turnID != "turn-1" {
		t.Fatalf("turnID = %q, want turn-1", turnID)
	}
}

type stubSessionProvider struct {
	session contract.Session
	err     error
}

func (p stubSessionProvider) GetSession(string) (contract.Session, error) {
	if p.err != nil {
		return nil, p.err
	}
	return p.session, nil
}

func TestOrchestrationTurnStarterReportsSessionNotReady(t *testing.T) {
	t.Parallel()

	starter := NewOrchestrationTurnStarter(
		NewService(silentLogger()),
		stubSessionProvider{err: contract.ErrSessionNotFound},
	)

	_, err := starter.StartTurn(context.Background(), orchestration.TurnSubmission{
		AgentID: "agent-1",
	})
	if err == nil {
		t.Fatal("StartTurn() error = nil, want session-not-ready error")
	}
	if got := err.Error(); got != "agent session not ready, ensure agent.launch completed" {
		t.Fatalf("StartTurn() error = %q, want session-not-ready error", got)
	}
}

func TestOrchestrationTurnStarterPreservesNonSessionLookupErrors(t *testing.T) {
	t.Parallel()

	want := errors.New("transport down")
	starter := NewOrchestrationTurnStarter(
		NewService(silentLogger()),
		stubSessionProvider{err: want},
	)

	_, err := starter.StartTurn(context.Background(), orchestration.TurnSubmission{
		AgentID: "agent-1",
	})
	if !errors.Is(err, want) {
		t.Fatalf("StartTurn() error = %v, want %v", err, want)
	}
}
