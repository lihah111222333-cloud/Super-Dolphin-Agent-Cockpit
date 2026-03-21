package orchestration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"testing"
	"time"

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

func TestSubmitTurnWaitsForSessionReadyWhenIdle(t *testing.T) {
	t.Parallel()

	starter := &stubTurnStarter{}
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, starter, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.cmd = &exec.Cmd{}
	agent.state = agentdto.StateIdle
	svc.agents[agent.id] = agent

	if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: "agent-1"}); err != nil {
		t.Fatalf("SubmitTurn() error = %v", err)
	}
	if starter.waitCalls != 1 {
		t.Fatalf("waitCalls = %d, want 1", starter.waitCalls)
	}
	if starter.waitAgentID != "agent-1" {
		t.Fatalf("waitAgentID = %q, want agent-1", starter.waitAgentID)
	}
	if starter.waitTimeout != submitSessionReadyTimeout {
		t.Fatalf("waitTimeout = %s, want %s", starter.waitTimeout, submitSessionReadyTimeout)
	}
	if got := agent.queue.Len(); got != 1 {
		t.Fatalf("queue len = %d, want 1", got)
	}
}

func TestSubmitTurnReturnsSessionWaitError(t *testing.T) {
	t.Parallel()

	want := errors.New("agent session not ready, ensure agent.launch completed")
	starter := &stubTurnStarter{waitErr: want}
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, starter, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.cmd = &exec.Cmd{}
	agent.state = agentdto.StateIdle
	svc.agents[agent.id] = agent

	err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: "agent-1"})
	if !errors.Is(err, want) {
		t.Fatalf("SubmitTurn() error = %v, want %v", err, want)
	}
	if got := agent.queue.Len(); got != 0 {
		t.Fatalf("queue len = %d, want 0 after wait failure", got)
	}
}

func TestSubmitTurnSkipsSessionWaitWhenBusy(t *testing.T) {
	t.Parallel()

	starter := &stubTurnStarter{}
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, starter, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.cmd = &exec.Cmd{}
	agent.state = agentdto.StateTurnRunning
	agent.activeTurnID = "turn-1"
	svc.agents[agent.id] = agent

	if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: "agent-1"}); err != nil {
		t.Fatalf("SubmitTurn() error = %v", err)
	}
	if starter.waitCalls != 0 {
		t.Fatalf("waitCalls = %d, want 0 while agent is busy", starter.waitCalls)
	}
	if got := agent.queue.Len(); got != 1 {
		t.Fatalf("queue len = %d, want 1", got)
	}
}

type stubTurnStarter struct {
	submission   TurnSubmission
	returnTurnID string
	waitCalls    int
	waitAgentID  string
	waitTimeout  time.Duration
	waitErr      error
}

func (s *stubTurnStarter) StartTurn(_ context.Context, submission TurnSubmission) (string, error) {
	s.submission = submission
	return s.returnTurnID, nil
}

func (s *stubTurnStarter) WaitForSessionReady(_ context.Context, agentID string, timeout time.Duration) error {
	s.waitCalls++
	s.waitAgentID = agentID
	s.waitTimeout = timeout
	return s.waitErr
}

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
