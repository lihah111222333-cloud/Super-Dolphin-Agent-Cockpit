package orchestration

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/launcherrors"
	"github.com/kelindar/event"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
	shareddto "github.com/anthropic-ai/super-agent-v3/internal/dto/shared"
	pkglogger "github.com/anthropic-ai/super-agent-v3/pkg/logger"
)

func TestClaimTurnWorkStartsQueuedSubmission(t *testing.T) {
	t.Parallel()

	starter := &stubTurnStarter{returnTurnID: "thread-1-turn-1"}
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, starter, nil)
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
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, starter, nil)
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

	want := errors.New("agent session not ready, ensure agent/launch completed")
	starter := &stubTurnStarter{waitErr: want}
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, starter, nil)
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
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, starter, nil)
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

func TestStartTurnExecutionWaitsForSessionReadyAfterBusySubmit(t *testing.T) {
	t.Parallel()

	starter := &stubTurnStarter{}
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, starter, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.cmd = &exec.Cmd{}
	agent.state = agentdto.StateTurnRunning
	agent.activeTurnID = "turn-active"
	svc.agents[agent.id] = agent

	if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: "agent-1"}); err != nil {
		t.Fatalf("SubmitTurn() error = %v", err)
	}
	if starter.waitCalls != 0 {
		t.Fatalf("waitCalls after busy SubmitTurn = %d, want 0", starter.waitCalls)
	}

	agent.state = agentdto.StateIdle
	agent.activeTurnID = ""
	work := svc.claimTurnWork(context.Background())
	if len(work) != 1 {
		t.Fatalf("len(work) = %d, want 1", len(work))
	}

	svc.startTurnExecution(context.Background(), work[0])

	if starter.waitCalls != 1 {
		t.Fatalf("waitCalls after startTurnExecution = %d, want 1", starter.waitCalls)
	}
	if starter.startCalls != 1 {
		t.Fatalf("startCalls = %d, want 1", starter.startCalls)
	}
	if agent.state != agentdto.StateTurnRunning {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateTurnRunning)
	}
}

func TestStartTurnExecutionReturnsSessionWaitErrorAfterSubmitWait(t *testing.T) {
	t.Parallel()

	want := errors.New("agent session not ready, ensure agent/launch completed")
	waitCalls := 0
	starter := &stubTurnStarter{
		waitFunc: func(_ context.Context, _ string, _ time.Duration) error {
			waitCalls++
			if waitCalls == 2 {
				return want
			}
			return nil
		},
	}
	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, starter, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.cmd = &exec.Cmd{}
	agent.state = agentdto.StateIdle
	svc.agents[agent.id] = agent

	if err := svc.SubmitTurn(context.Background(), TurnSubmission{AgentID: "agent-1"}); err != nil {
		t.Fatalf("SubmitTurn() error = %v", err)
	}

	work := svc.claimTurnWork(context.Background())
	if len(work) != 1 {
		t.Fatalf("len(work) = %d, want 1", len(work))
	}
	svc.startTurnExecution(context.Background(), work[0])

	if starter.waitCalls != 2 {
		t.Fatalf("waitCalls = %d, want 2 across submit and execute", starter.waitCalls)
	}
	if starter.startCalls != 0 {
		t.Fatalf("startCalls = %d, want 0 after wait failure", starter.startCalls)
	}
	if agent.state != agentdto.StateIdle {
		t.Fatalf("agent.state = %q, want %q", agent.state, agentdto.StateIdle)
	}
	if agent.activeTurnID != "" {
		t.Fatalf("activeTurnID = %q, want empty after wait failure", agent.activeTurnID)
	}
	if agent.lastError != want.Error() {
		t.Fatalf("lastError = %q, want %q", agent.lastError, want.Error())
	}
}

func TestWaitForSubmitSessionReadyLogsCompletion(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	starter := &stubTurnStarter{}
	svc := NewService(logger, event.NewDispatcher(), nil, nil, starter, nil)

	if err := svc.waitForSubmitSessionReady(context.Background(), "agent-1"); err != nil {
		t.Fatalf("waitForSubmitSessionReady() error = %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "waiting for submit session ready") {
		t.Fatalf("log output = %q, want wait start log", output)
	}
	if !strings.Contains(output, "submit session ready wait completed") {
		t.Fatalf("log output = %q, want wait completion log", output)
	}
}

func TestWaitRetryBackoffLogsCompletion(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	// 该测试会临时替换包级 logger。保持串行执行，避免其他并行测试写入同一个 bytes.Buffer。
	pkglogger.SetForTest(logger)
	t.Cleanup(func() { pkglogger.SetForTest(silentLogger()) })

	if err := launcherrors.WaitRetryBackoff(context.Background(), 0, "agent-1", errors.New("transient")); err != nil {
		t.Fatalf("waitRetryBackoff() error = %v", err)
	}
	output := buf.String()
	if !strings.Contains(output, "retrying launch") {
		t.Fatalf("log output = %q, want retry start log", output)
	}
	if !strings.Contains(output, "launch retry backoff completed") && !strings.Contains(output, "launch retry backoff slow") {
		t.Fatalf("log output = %q, want retry completion log", output)
	}
}

type stubTurnStarter struct {
	submission   TurnSubmission
	returnTurnID string
	startCalls   int
	startErr     error
	waitCalls    int
	waitAgentID  string
	waitTimeout  time.Duration
	waitErr      error
	waitFunc     func(context.Context, string, time.Duration) error
}

func (s *stubTurnStarter) StartTurn(_ context.Context, submission TurnSubmission) (string, error) {
	s.startCalls++
	s.submission = submission
	if s.startErr != nil {
		return "", s.startErr
	}
	return s.returnTurnID, nil
}

func (s *stubTurnStarter) WaitForSessionReady(ctx context.Context, agentID string, timeout time.Duration) error {
	s.waitCalls++
	s.waitAgentID = agentID
	s.waitTimeout = timeout
	if s.waitFunc != nil {
		return s.waitFunc(ctx, agentID, timeout)
	}
	return s.waitErr
}

func silentLogger() *slog.Logger {
	return pkglogger.New(pkglogger.NewTextHandler(io.Discard, nil))
}
