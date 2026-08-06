package orchestration

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kelindar/event"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/orchestration/launcherwire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	providerdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/provider"
	turnmodule "github.com/lihah111222333-cloud/super-dolphin-agent/internal/module/turn"
)

func TestRemoteLauncherInterruptUsesRealTurnInterruptContract(t *testing.T) {
	launcher, session := newRealTurnInterruptLauncher(t, "turn-1")

	err := launcher.Interrupt(context.Background(), &agentRuntime{remoteThreadID: "thread-1", activeTurnID: "turn-1"}, "parent_agent")
	if err != nil {
		t.Fatalf("Interrupt() error = %v", err)
	}
	if session.interruptCalls != 1 || session.lastInterrupt.ThreadID != "thread-1" || session.lastInterrupt.TurnID != "provider-turn-1" {
		t.Fatalf("real turn/interrupt request calls=%d request=%#v", session.interruptCalls, session.lastInterrupt)
	}
	if session.lastInterrupt.RequestID == "" || session.lastInterrupt.Source != "parent_agent" {
		t.Fatalf("real turn/interrupt stop identity = %#v", session.lastInterrupt)
	}
}

func TestRemoteLauncherInterruptRejectsTargetChangedFromRealHandler(t *testing.T) {
	launcher, session := newRealTurnInterruptLauncher(t, "turn-current")

	err := launcher.Interrupt(context.Background(), &agentRuntime{remoteThreadID: "thread-1", activeTurnID: "turn-stale"}, "parent_agent")
	if err == nil || !strings.Contains(err.Error(), "TARGET_CHANGED") {
		t.Fatalf("Interrupt() error = %v, want TARGET_CHANGED", err)
	}
	if session.interruptCalls != 0 {
		t.Fatalf("provider interrupt calls = %d, want 0 after target change", session.interruptCalls)
	}
}

func TestRemoteLauncherInterruptRealHandlerRejectsMissingStopIdentity(t *testing.T) {
	launcher, _ := newRealTurnInterruptLauncher(t, "turn-1")
	tests := []struct {
		name string
		req  launcherwire.TurnInterruptRequest
		want string
	}{
		{name: "expected turn", req: launcherwire.TurnInterruptRequest{ThreadID: "thread-1", RequestID: "request-1"}, want: "invalid parameters"},
		{name: "request id", req: launcherwire.TurnInterruptRequest{ThreadID: "thread-1", ExpectedTurnID: "turn-1"}, want: "invalid parameters"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := rpcCall[launcherwire.TurnInterruptResponse](context.Background(), launcher, launcherwire.MethodTurnInterrupt, tc.req)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("turn/interrupt error = %v, want %q", err, tc.want)
			}
		})
	}
}

type realTurnInterruptSessionResolver struct{ session contract.Session }

func (r realTurnInterruptSessionResolver) ResolveSession(_ context.Context, threadID string) (contract.Session, error) {
	if r.session == nil || strings.TrimSpace(threadID) != r.session.ThreadID() {
		return nil, fmt.Errorf("unknown test thread %q", threadID)
	}
	return r.session, nil
}

type realTurnInterruptSession struct {
	contract.Session
	threadID       string
	handle         *realTurnInterruptHandle
	interruptCalls int
	lastInterrupt  providerdto.InterruptRequest
}

func (s *realTurnInterruptSession) ThreadID() string { return s.threadID }

func (s *realTurnInterruptSession) StartTurn(context.Context, providerdto.TurnRequest) (contract.TurnHandle, error) {
	return s.handle, nil
}

func (s *realTurnInterruptSession) Interrupt(_ context.Context, request providerdto.InterruptRequest) error {
	s.interruptCalls++
	s.lastInterrupt = request
	time.AfterFunc(time.Millisecond, func() { s.handle.complete(fmt.Errorf("interrupted")) })
	return nil
}

type realTurnInterruptHandle struct {
	localID, providerID string
	done                chan struct{}
	once                sync.Once
	mu                  sync.RWMutex
	err                 error
}

func newRealTurnInterruptHandle(localID string) *realTurnInterruptHandle {
	return &realTurnInterruptHandle{localID: localID, providerID: "provider-" + localID, done: make(chan struct{})}
}

func (h *realTurnInterruptHandle) LocalID() string       { return h.localID }
func (h *realTurnInterruptHandle) ProviderID() string    { return h.providerID }
func (h *realTurnInterruptHandle) Done() <-chan struct{} { return h.done }

func (h *realTurnInterruptHandle) Err() error {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.err
}

func (h *realTurnInterruptHandle) complete(err error) {
	h.mu.Lock()
	h.err = err
	h.mu.Unlock()
	h.once.Do(func() { close(h.done) })
}

func newRealTurnInterruptLauncher(t *testing.T, activeTurnID string) (*remoteLauncher, *realTurnInterruptSession) {
	t.Helper()
	handle := newRealTurnInterruptHandle(activeTurnID)
	session := &realTurnInterruptSession{threadID: "thread-1", handle: handle}
	svc := turnmodule.NewService(nil, turnmodule.NewToolResultRuntime())
	if _, err := svc.StartTurn(context.Background(), session, providerdto.TurnRequest{
		LocalID: activeTurnID, ThreadID: session.threadID, Inputs: []providerdto.InputItem{{Type: "text", Content: "interrupt contract"}},
	}); err != nil {
		t.Fatalf("StartTurn() error = %v", err)
	}
	if shutdowner, ok := svc.(interface{ Shutdown() }); ok {
		t.Cleanup(shutdowner.Shutdown)
	}
	handlers := turnmodule.NewTurnHandlers(svc, realTurnInterruptSessionResolver{session: session}, nil, nil, nil, nil)
	return remoteLocalLauncher(t, handlers.Handlers), session
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

func TestInterruptAgentReturnsWhenReplacementOwnsActiveTurn(t *testing.T) {
	launcher := &interruptAgentLauncher{}
	svc := newInterruptAgentService(launcher, agentdto.StateTurnRunning, "turn-old")
	launcher.afterInterrupt = func() {
		if err := svc.registry.withAgentLocked("agent-1", func(agent *agentRuntime) error {
			agent.remoteThreadID = "thread-replacement"
			agent.activeTurnID = "turn-replacement"
			agent.state = agentdto.StateTurnRunning
			return nil
		}); err != nil {
			t.Fatalf("replace interrupted turn: %v", err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	result, err := svc.InterruptAgent(ctx, "agent-1", "parent_agent")

	if err != nil {
		t.Fatalf("InterruptAgent() error = %v", err)
	}
	if result.State != string(agentdto.StateTurnRunning) {
		t.Fatalf("InterruptAgent() state = %q, want replacement running", result.State)
	}
	if launcher.interruptCalls != 1 {
		t.Fatalf("launcher interrupt calls = %d, want 1", launcher.interruptCalls)
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
	svc.registry.agents[agent.id] = agent
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
