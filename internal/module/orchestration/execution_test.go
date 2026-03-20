package orchestration

import (
	"context"
	"io"
	"log/slog"
	"os/exec"
	"testing"

	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
)

func TestClaimTurnWorkStartsQueuedSubmission(t *testing.T) {
	t.Parallel()

	starter := &stubTurnStarter{returnTurnID: "thread-1-turn-1"}
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, starter, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.cmd = &exec.Cmd{}
	agent.state = agentdto.StateIdle
	svc.agents[agent.id] = agent

	if err := svc.SubmitTurn(context.Background(), TurnSubmission{
		AgentID:  "agent-1",
		ThreadID: "thread-1",
		Inputs:   []shareddto.InputItem{{Type: "text", Content: "hello"}},
	}); err != nil {
		t.Fatalf("SubmitTurn() error = %v", err)
	}

	work := svc.claimTurnWork(context.Background())
	if len(work) != 1 {
		t.Fatalf("len(work) = %d, want 1", len(work))
	}
	if work[0].submission.ExpectedTurnID != "thread-1-turn-1" {
		t.Fatalf("ExpectedTurnID = %q, want thread-1-turn-1", work[0].submission.ExpectedTurnID)
	}

	svc.startTurnExecution(context.Background(), work[0])

	if starter.submission.AgentID != "agent-1" {
		t.Fatalf("starter submission agent = %q, want agent-1", starter.submission.AgentID)
	}
	if agent.state != agentdto.StateTurnRunning {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateTurnRunning)
	}
	if agent.activeTurnID != "thread-1-turn-1" {
		t.Fatalf("activeTurnID = %q, want thread-1-turn-1", agent.activeTurnID)
	}
}

type stubTurnStarter struct {
	submission   TurnSubmission
	returnTurnID string
}

func (s *stubTurnStarter) StartTurn(_ context.Context, submission TurnSubmission) (string, error) {
	s.submission = submission
	return s.returnTurnID, nil
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
