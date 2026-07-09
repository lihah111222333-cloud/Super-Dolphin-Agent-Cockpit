package orchestration

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration/exitmonitor"
	"github.com/kelindar/event"
)

const testInternalSQLitePathEnvKey = "SUPER_DOLPHIN_INTERNAL_SQLITE_PATH"

func TestLocalLauncherScrubsDatabaseEnvFromParentAndAgent(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://parent@localhost/super_dolphin")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@localhost/super_dolphin")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", t.TempDir()+"/parent.db")
	t.Setenv(testInternalSQLitePathEnvKey, t.TempDir()+"/parent-internal.db")
	t.Setenv("ORCH_SAFE_PARENT", "keep-parent")

	agent := &agentRuntime{
		id:      "agent-1",
		command: []string{os.Args[0], "-test.run=^TestLauncherHelperProcess$"},
		env: []string{
			"GO_WANT_LAUNCHER_HELPER=1",
			"ORCH_SAFE_AGENT=keep-agent",
			"DATABASE_URL=postgres://agent@localhost/super_dolphin",
			"POSTGRES_CONNECTION_STRING=postgres://agent-compat@localhost/super_dolphin",
			"SUPER_DOLPHIN_SQLITE_PATH=" + t.TempDir() + "/agent.db",
			testInternalSQLitePathEnvKey + "=" + t.TempDir() + "/agent-internal.db",
		},
	}
	launcher := NewLocalLauncher(nil, silentLogger()).(*localLauncher)
	launcher.exitMonitor = exitmonitor.New(silentLogger())
	if _, err := launcher.Launch(context.Background(), agent, LaunchRequest{}); err != nil {
		t.Fatalf("Launch() error = %v", err)
	}
	t.Cleanup(func() { stopAndDrainLocalLauncherTestAgent(t, launcher, agent) })

	requireDatabaseEnvAbsent(t, agent.cmd.Env)
	if got := envValue(agent.cmd.Env, "ORCH_SAFE_PARENT"); got != "keep-parent" {
		t.Fatalf("ORCH_SAFE_PARENT = %q, want keep-parent", got)
	}
	if got := envValue(agent.cmd.Env, "ORCH_SAFE_AGENT"); got != "keep-agent" {
		t.Fatalf("ORCH_SAFE_AGENT = %q, want keep-agent", got)
	}
}

func TestServiceStartProcessLockedScrubsDatabaseEnvFromParentAndAgent(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://parent@localhost/super_dolphin")
	t.Setenv("POSTGRES_CONNECTION_STRING", "postgres://compat@localhost/super_dolphin")
	t.Setenv("SUPER_DOLPHIN_SQLITE_PATH", t.TempDir()+"/parent.db")
	t.Setenv(testInternalSQLitePathEnvKey, t.TempDir()+"/parent-internal.db")
	t.Setenv("ORCH_SAFE_PARENT", "keep-parent")

	svc := NewService(silentLogger(), event.NewDispatcher(), nil, nil, nil, nil)
	req := LaunchRequest{
		AgentID: "agent-1",
		Cwd:     t.TempDir(),
		Command: []string{os.Args[0], "-test.run=^TestLauncherHelperProcess$"},
		Env: []string{
			"GO_WANT_LAUNCHER_HELPER=1",
			"ORCH_SAFE_AGENT=keep-agent",
			"DATABASE_URL=postgres://agent@localhost/super_dolphin",
			"POSTGRES_CONNECTION_STRING=postgres://agent-compat@localhost/super_dolphin",
			"SUPER_DOLPHIN_SQLITE_PATH=" + t.TempDir() + "/agent.db",
			testInternalSQLitePathEnvKey + "=" + t.TempDir() + "/agent-internal.db",
		},
	}
	if err := svc.LaunchAgent(context.Background(), req); err != nil {
		t.Fatalf("LaunchAgent() error = %v", err)
	}
	agent := svc.registry.agents["agent-1"]
	t.Cleanup(func() { stopAndDrainServiceTestAgent(t, svc, agent) })

	requireDatabaseEnvAbsent(t, agent.cmd.Env)
	if got := envValue(agent.cmd.Env, "ORCH_SAFE_PARENT"); got != "keep-parent" {
		t.Fatalf("ORCH_SAFE_PARENT = %q, want keep-parent", got)
	}
	if got := envValue(agent.cmd.Env, "ORCH_SAFE_AGENT"); got != "keep-agent" {
		t.Fatalf("ORCH_SAFE_AGENT = %q, want keep-agent", got)
	}
}

func requireDatabaseEnvAbsent(t *testing.T, env []string) {
	t.Helper()
	for _, key := range []string{"DATABASE_URL", "POSTGRES_CONNECTION_STRING", "SUPER_DOLPHIN_SQLITE_PATH", testInternalSQLitePathEnvKey} {
		prefix := key + "="
		for _, item := range env {
			if strings.HasPrefix(item, prefix) {
				t.Fatalf("%s leaked in env item %q within %#v", key, item, env)
			}
		}
	}
}

func stopAndDrainLocalLauncherTestAgent(t *testing.T, launcher *localLauncher, agent *agentRuntime) {
	t.Helper()
	if launcher == nil || launcher.exitMonitor == nil || agent == nil || agent.cmd == nil {
		return
	}
	_ = stopProcess(agent.cmd)
	drainCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := launcher.exitMonitor.Drain(drainCtx); err != nil {
		t.Fatalf("drain local launcher exit monitor: %v", err)
	}
}

func stopAndDrainServiceTestAgent(t *testing.T, svc *service, agent *agentRuntime) {
	t.Helper()
	if svc == nil || svc.exitMonitor == nil {
		return
	}
	if agent != nil && agent.cmd != nil {
		_ = stopProcess(agent.cmd)
	}
	drainCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := svc.exitMonitor.Drain(drainCtx); err != nil {
		t.Fatalf("drain exit monitor: %v", err)
	}
}
