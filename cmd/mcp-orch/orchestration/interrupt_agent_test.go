package orchestration

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/creachadair/jrpc2/handler"
	"github.com/kelindar/event"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/launcherwire"
	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

func TestRemoteLauncherInterruptCallsTurnInterrupt(t *testing.T) {
	var interrupted map[string]any
	launcher := remoteLocalLauncher(t, handler.Map{
		launcherwire.MethodTurnInterrupt: handler.New(func(_ context.Context, req map[string]any) (struct{}, error) {
			interrupted = req
			return struct{}{}, nil
		}),
	})

	err := launcher.Interrupt(context.Background(), &agentRuntime{remoteThreadID: "thread-1"}, "parent_agent")
	if err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	if interrupted[launcherwire.ParamThreadID] != "thread-1" || interrupted[launcherwire.ParamSource] != "parent_agent" {
		t.Fatalf("turn/interrupt params = %#v", interrupted)
	}
}

func TestInterruptAgentRunningTurnSettlesIdle(t *testing.T) {
	launcher := &interruptAgentLauncher{}
	svc := newInterruptAgentService(launcher, agentdto.StateTurnRunning, "turn-1")
	launcher.afterInterrupt = func() {
		svc.handleRemoteTurnInterrupted(context.Background(), interruptedEventAt("agent-1", "thread-1", "turn-1", "parent_cancel", time.Now()))
	}

	result, err := svc.InterruptAgent(context.Background(), "agent-1", "parent_agent")
	if err != nil {
		t.Fatalf("InterruptAgent() error = %v", err)
	}
	if result.AgentID != "agent-1" || result.State != string(agentdto.StateIdle) {
		t.Fatalf("InterruptAgent() = %#v", result)
	}
	if launcher.interruptCalls != 1 || launcher.source != "parent_agent" {
		t.Fatalf("launcher interrupt calls=%d source=%q", launcher.interruptCalls, launcher.source)
	}
}

func TestInterruptAgentRejectsIdleAgent(t *testing.T) {
	launcher := &interruptAgentLauncher{}
	svc := newInterruptAgentService(launcher, agentdto.StateIdle, "")

	_, err := svc.InterruptAgent(context.Background(), "agent-1", "parent_agent")
	if err == nil || !strings.Contains(err.Error(), "idle") {
		t.Fatalf("InterruptAgent() error = %v, want idle rejection", err)
	}
	if launcher.interruptCalls != 0 {
		t.Fatalf("launcher interrupt calls = %d, want 0", launcher.interruptCalls)
	}
}

func TestInterruptAgentRejectsMissingActiveTurn(t *testing.T) {
	launcher := &interruptAgentLauncher{}
	svc := newInterruptAgentService(launcher, agentdto.StateTurnRunning, "")

	_, err := svc.InterruptAgent(context.Background(), "agent-1", "parent_agent")
	if err == nil || !strings.Contains(err.Error(), "active turn") {
		t.Fatalf("InterruptAgent() error = %v, want active turn rejection", err)
	}
	if launcher.interruptCalls != 0 {
		t.Fatalf("launcher interrupt calls = %d, want 0", launcher.interruptCalls)
	}
}

func TestInterruptAgentTimeoutMentionsActiveTurn(t *testing.T) {
	launcher := &interruptAgentLauncher{}
	svc := newInterruptAgentService(launcher, agentdto.StateTurnRunning, "turn-1")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	defer cancel()

	_, err := svc.InterruptAgent(ctx, "agent-1", "parent_agent")
	if err == nil || !strings.Contains(err.Error(), "agent-1") || !strings.Contains(err.Error(), "turn-1") {
		t.Fatalf("InterruptAgent() error = %v, want timeout with agent and turn", err)
	}
	if launcher.interruptCalls != 1 {
		t.Fatalf("launcher interrupt calls = %d, want 1", launcher.interruptCalls)
	}
}

func newInterruptAgentService(launcher *interruptAgentLauncher, state agentdto.AgentState, activeTurnID string) *service {
	svc := NewService(silentLogger(), event.NewDispatcher(), launcher, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = state
	agent.threadID = "thread-1"
	agent.remoteThreadID = "thread-1"
	agent.activeTurnID = activeTurnID
	svc.agents[agent.id] = agent
	return svc
}

type interruptAgentLauncher struct {
	interruptCalls int
	source         string
	afterInterrupt func()
}

func (l *interruptAgentLauncher) Launch(context.Context, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, nil
}

func (l *interruptAgentLauncher) Fork(context.Context, *agentRuntime, *agentRuntime, LaunchRequest) (LaunchResult, error) {
	return LaunchResult{}, nil
}

func (l *interruptAgentLauncher) Stop(context.Context, *agentRuntime) error    { return nil }
func (l *interruptAgentLauncher) Archive(context.Context, *agentRuntime) error { return nil }
func (l *interruptAgentLauncher) SubmitTurn(context.Context, *agentRuntime, TurnSubmission) (string, error) {
	return "", nil
}

func (l *interruptAgentLauncher) IsRunning(context.Context, *agentRuntime) bool { return true }

func (l *interruptAgentLauncher) Interrupt(_ context.Context, _ *agentRuntime, source string) error {
	l.interruptCalls++
	l.source = strings.TrimSpace(source)
	if l.afterInterrupt != nil {
		l.afterInterrupt()
	}
	return nil
}
