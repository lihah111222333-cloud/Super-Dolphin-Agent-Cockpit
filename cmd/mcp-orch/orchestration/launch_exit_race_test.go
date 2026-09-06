package orchestration

import (
	"context"
	"errors"
	"testing"

	"github.com/kelindar/event"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	agentdto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/agent"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
	"github.com/stretchr/testify/require"
)

// launchExitRaceBackend emits the provider exit synchronously from Launch,
// before orchestration has a chance to bind LaunchResult.ThreadID.
type launchExitRaceBackend struct {
	exitSink     func(contract.CLIProcessExit)
	launchResult LaunchResult
	emitExit     bool
	running      bool
	stopCalls    int
}

// BindTerminalSink intentionally ignores terminal events in this exit-race fixture.
func (b *launchExitRaceBackend) BindTerminalSink(func(context.Context, turndto.TurnTerminalV2)) {
	if b == nil {
		return
	}
}

func (b *launchExitRaceBackend) BindProcessExitSink(sink func(contract.CLIProcessExit)) {
	b.exitSink = sink
}

func (b *launchExitRaceBackend) Launch(ctx context.Context, req LaunchRequest) (LaunchResult, error) {
	b.running = true
	seq, ok := contract.LaunchSeqFromContext(ctx)
	if !ok {
		return LaunchResult{}, errors.New("launch sequence was not propagated")
	}
	if b.emitExit {
		if b.exitSink == nil {
			return LaunchResult{}, errors.New("process exit sink was not bound")
		}
		b.exitSink(contract.CLIProcessExit{
			AgentID: req.AgentID, SessionID: b.launchResult.ThreadID,
			LaunchSeq: seq, Err: errors.New("provider exited"), Unexpected: true,
		})
	}
	return b.launchResult, nil
}

func (b *launchExitRaceBackend) Fork(ctx context.Context, req string, launch LaunchRequest) (LaunchResult, error) {
	launch.AgentID = req
	return b.Launch(ctx, launch)
}

func (b *launchExitRaceBackend) Stop(context.Context, string) error {
	b.stopCalls++
	b.running = false
	return nil
}

func (*launchExitRaceBackend) Archive(context.Context, string) error { return nil }

func (*launchExitRaceBackend) Interrupt(context.Context, string, string) error { return nil }

func (*launchExitRaceBackend) SubmitTurn(context.Context, TurnSubmission) (string, error) {
	return "", nil
}

func (b *launchExitRaceBackend) IsRunning(context.Context, string) bool { return b.running }

func TestCLILaunchRejectsExitBeforeThreadBinding(t *testing.T) {
	backend := &launchExitRaceBackend{
		emitExit:     true,
		launchResult: LaunchResult{ThreadID: "provider-session", RemoteAgentID: "cli-agent"},
	}
	launcher, err := NewCLILauncher(backend)
	require.NoError(t, err)
	svc := NewService(silentLogger(), event.NewDispatcher(), launcher, nil, nil, nil)

	_, err = svc.LaunchAgentSnapshot(context.Background(), LaunchRequest{
		AgentID: "cli-agent", Name: "CLI", Cwd: t.TempDir(), Command: []string{"unused"},
	})
	require.ErrorContains(t, err, "process exited before launch activation")

	agent := svc.registry.agents["cli-agent"]
	require.NotNil(t, agent)
	require.Equal(t, agentdto.StateFailed, agent.state)
	require.Equal(t, uint64(1), agent.lastExitedSeq)
	require.Empty(t, agent.remoteThreadID)
	require.Equal(t, 1, backend.stopCalls)
}

func TestCLILaunchWithoutExitCompletesNormally(t *testing.T) {
	backend := &launchExitRaceBackend{
		launchResult: LaunchResult{ThreadID: "provider-session", RemoteAgentID: "cli-agent"},
	}
	launcher, err := NewCLILauncher(backend)
	require.NoError(t, err)
	svc := NewService(silentLogger(), event.NewDispatcher(), launcher, nil, nil, nil)

	_, err = svc.LaunchAgentSnapshot(context.Background(), LaunchRequest{
		AgentID: "cli-agent", Name: "CLI", Cwd: t.TempDir(), Command: []string{"unused"},
	})
	require.NoError(t, err)

	agent := svc.registry.agents["cli-agent"]
	require.NotNil(t, agent)
	require.Equal(t, agentdto.StateIdle, agent.state)
	require.Equal(t, "provider-session", agent.remoteThreadID)
	require.Zero(t, backend.stopCalls)
}

func TestCLIStaleExitGenerationCannotAffectReplacement(t *testing.T) {
	backend := &launchExitRaceBackend{
		launchResult: LaunchResult{ThreadID: "provider-session-1", RemoteAgentID: "cli-agent"},
	}
	launcher, err := NewCLILauncher(backend)
	require.NoError(t, err)
	svc := NewService(silentLogger(), event.NewDispatcher(), launcher, nil, nil, nil)
	request := LaunchRequest{AgentID: "cli-agent", Name: "CLI", Cwd: t.TempDir(), Command: []string{"unused"}}

	_, err = svc.LaunchAgentSnapshot(context.Background(), request)
	require.NoError(t, err)
	first := svc.registry.agents["cli-agent"]
	require.Equal(t, uint64(1), first.launchSeq)

	require.NoError(t, svc.StopAgent(context.Background(), "cli-agent"))
	backend.launchResult = LaunchResult{ThreadID: "provider-session-2", RemoteAgentID: "cli-agent"}
	_, err = svc.LaunchAgentSnapshot(context.Background(), request)
	require.NoError(t, err)
	current := svc.registry.agents["cli-agent"]
	require.Equal(t, uint64(2), current.launchSeq)

	backend.exitSink(contract.CLIProcessExit{
		AgentID: "cli-agent", SessionID: "provider-session-1",
		LaunchSeq: 1, Err: errors.New("stale provider exit"), Unexpected: true,
	})

	require.Equal(t, agentdto.StateIdle, current.state)
	require.Equal(t, "provider-session-2", current.remoteThreadID)
	require.Equal(t, uint64(1), current.lastExitedSeq)
	require.Equal(t, 1, backend.stopCalls)
}
