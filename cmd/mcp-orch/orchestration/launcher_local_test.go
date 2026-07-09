package orchestration

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/kelindar/event"
)

func TestLocalLauncher_LaunchStop(t *testing.T) {
	agent := &agentRuntime{id: "agent-1", command: []string{os.Args[0], "-test.run=^TestLauncherHelperProcess$"}, env: []string{"GO_WANT_LAUNCHER_HELPER=1"}}
	launcher := NewLocalLauncher(nil, silentLogger()).(*localLauncher)
	monitor := exitmonitor.New(silentLogger())
	launcher.exitMonitor = monitor
	if _, err := launcher.Launch(context.Background(), agent, LaunchRequest{}); err != nil || agent.cmd == nil || agent.cmd.Process == nil {
		t.Fatalf("Launch() err=%v cmd=%#v", err, agent.cmd)
	}
	if err := launcher.Stop(context.Background(), agent); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	select {
	case ev := <-monitor.ExitEvents():
		if ev.AgentID != agent.id || ev.LaunchSeq != agent.launchSeq {
			t.Fatalf("exit event = %+v, want %s/%d", ev, agent.id, agent.launchSeq)
		}
	case <-time.After(time.Second):
		t.Fatal("exit monitor timed out after Stop()")
	}
}

func TestServiceLocalLauncherStopReapsViaExitEventStream(t *testing.T) {
	svc := NewService(silentLogger(), event.NewDispatcher(), NewLocalLauncher(nil, silentLogger()), nil, nil, nil)
	svc.processExitWaitTimeout = 25 * time.Millisecond
	req := LaunchRequest{
		AgentID: "agent-local-stream",
		Cwd:     t.TempDir(),
		Command: []string{os.Args[0], "-test.run=^TestLauncherHelperProcess$"},
		Env:     []string{"GO_WANT_LAUNCHER_HELPER=1"},
	}
	if err := svc.LaunchAgent(context.Background(), req); err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	agent := svc.registry.agents["agent-local-stream"]
	if agent == nil || agent.cmd == nil {
		t.Fatalf("launched agent = %#v, want live local process", agent)
	}
	launchSeq := agent.launchSeq
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	runDone := make(chan error, 1)
	goroutines := newTestGoroutineGroup(t)
	goroutines.Go(func() { runDone <- NewRunnerActor(silentLogger(), svc).Run(ctx) })

	if err := svc.StopAgent(context.Background(), agent.id); err != nil {
		t.Fatalf("StopAgent() error = %v", err)
	}
	requireLocalLauncherReaped(t, svc, agent.id, launchSeq)
	cancel()
	requireRunnerStoppedAfterCancel(t, runDone)
}

func requireLocalLauncherReaped(t *testing.T, svc *service, agentID string, launchSeq uint64) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		svc.registry.mu.RLock()
		agent := svc.registry.agents[agentID]
		reaped := agent != nil && agent.lastExitedSeq >= launchSeq && agent.cmd == nil
		svc.registry.mu.RUnlock()
		if reaped {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("local launcher process was not reaped through exit event stream for %s seq=%d", agentID, launchSeq)
		case <-ticker.C:
		}
	}
}
