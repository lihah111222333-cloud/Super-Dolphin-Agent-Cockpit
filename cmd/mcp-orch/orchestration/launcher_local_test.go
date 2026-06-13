package orchestration

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestLocalLauncher_LaunchStop(t *testing.T) {
	agent := &agentRuntime{id: "agent-1", command: []string{os.Args[0], "-test.run=^TestLauncherHelperProcess$"}, env: []string{"GO_WANT_LAUNCHER_HELPER=1"}}
	launcher := NewLocalLauncher(nil, silentLogger()).(*localLauncher)
	if _, err := launcher.Launch(context.Background(), agent, LaunchRequest{}); err != nil || agent.cmd == nil || agent.cmd.Process == nil {
		t.Fatalf("Launch() err=%v cmd=%#v", err, agent.cmd)
	}
	if err := launcher.Stop(context.Background(), agent); err != nil {
		t.Fatalf("Stop() error = %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- agent.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Wait() timed out after Stop()")
	}
}
