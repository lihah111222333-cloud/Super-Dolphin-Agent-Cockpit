package standalone

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/standalone/claudewire"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	turndto "github.com/lihah111222333-cloud/super-dolphin-agent/internal/dto/turn"
)

func TestClaudeWaitForSessionReadyInitBeforeSubmit(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Providers[0].Name = "claude"
	cfg.Providers[0].Executable = claudeReadinessHelper(t, `
session=""
previous=""
for arg in "$@"; do
  if [ "$previous" = "--session-id" ]; then session="$arg"; fi
  previous="$arg"
done
printf '{"type":"system","subtype":"init","session_id":"%s"}\n' "$session"
while IFS= read -r line; do :; done
`)
	e := NewExecution(cfg)
	e.BindTerminalSink(func(context.Context, turndto.TurnTerminalV2) {})
	provider := cfg.Providers[0]
	session, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent", Cwd: cfg.WorkspaceRoot}, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer e.launches.Done()
	if _, err := e.launchClaude(context.Background(), session, contract.LaunchRequest{AgentID: "agent", Cwd: cfg.WorkspaceRoot}, provider, "test-model"); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(context.Background(), "agent")
	if err := e.WaitForSessionReady(context.Background(), "agent", time.Second); err != nil {
		t.Fatalf("WaitForSessionReady() = %v", err)
	}
	if !session.claudeInitialized {
		t.Fatal("matching system/init did not activate Claude session")
	}
	if _, err := e.SubmitTurn(context.Background(), contract.TurnSubmission{AgentID: "agent", Inputs: []turndto.InputItem{{Type: "text", Content: "hello"}}}); err != nil {
		t.Fatalf("SubmitTurn after init = %v", err)
	}
}

func TestClaudeWaitForSessionReadyRejectsInitializedWithoutRunningProcess(t *testing.T) {
	cases := []struct {
		name    string
		process *claudewire.Process
	}{
		{name: "nil process"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validTestConfig(t)
			cfg.Providers[0].Name = "claude"
			e := NewExecution(cfg)
			e.BindTerminalSink(func(context.Context, turndto.TurnTerminalV2) {})
			session, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent", Cwd: cfg.WorkspaceRoot}, cfg.Providers[0])
			if err != nil {
				t.Fatal(err)
			}
			defer e.launches.Done()
			session.mu.Lock()
			session.claudeInitialized = true
			session.claude = tc.process
			session.readyCh = nil
			session.mu.Unlock()
			err = e.WaitForSessionReady(context.Background(), "agent", time.Second)
			if err == nil || !strings.Contains(err.Error(), "exited before system/init") {
				t.Fatalf("WaitForSessionReady() = %v, want non-running Claude failure", err)
			}
		})
	}
}

func TestClaudeWaitForSessionReadyRejectsInitializedWithExitedProcess(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Providers[0].Name = "claude"
	process, err := claudewire.StartWithExitContext(context.Background(), claudeReadinessHelper(t, "exit 0"), cfg.WorkspaceRoot,
		"test-model", "session", "", os.Environ(), func(claudewire.Event) {}, contract.CLIProcessExitBinding{})
	if err != nil {
		t.Fatal(err)
	}
	defer process.Close()
	select {
	case <-process.Done():
	case <-time.After(time.Second):
		t.Fatal("helper process did not exit")
	}
	e := NewExecution(cfg)
	e.BindTerminalSink(func(context.Context, turndto.TurnTerminalV2) {})
	session, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent", Cwd: cfg.WorkspaceRoot}, cfg.Providers[0])
	if err != nil {
		t.Fatal(err)
	}
	defer e.launches.Done()
	session.mu.Lock()
	session.claudeInitialized = true
	session.claude = process
	session.readyCh = nil
	session.mu.Unlock()
	err = e.WaitForSessionReady(context.Background(), "agent", time.Second)
	if err == nil || !strings.Contains(err.Error(), "exited before system/init") {
		t.Fatalf("WaitForSessionReady() = %v, want exited Claude failure", err)
	}
}

func TestClaudeLaunchHonorsCancelledContext(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Providers[0].Name = "claude"
	cfg.Providers[0].Executable = claudeReadinessHelper(t, "sleep 2")
	e := NewExecution(cfg)
	e.BindTerminalSink(func(context.Context, turndto.TurnTerminalV2) {})
	provider := cfg.Providers[0]
	session, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent", Cwd: cfg.WorkspaceRoot}, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer e.launches.Done()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = e.launchClaude(ctx, session, contract.LaunchRequest{AgentID: "agent", Cwd: cfg.WorkspaceRoot}, provider, "test-model")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("launchClaude() = %v, want context cancellation", err)
	}
	session.mu.Lock()
	process := session.claude
	session.mu.Unlock()
	if process != nil {
		t.Fatal("cancelled Claude launch retained a process")
	}
}

func TestClaudeWaitForSessionReadyTimeout(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Providers[0].Name = "claude"
	cfg.Providers[0].Executable = claudeReadinessHelper(t, "sleep 2")
	e := NewExecution(cfg)
	e.BindTerminalSink(func(context.Context, turndto.TurnTerminalV2) {})
	provider := cfg.Providers[0]
	session, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent", Cwd: cfg.WorkspaceRoot}, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer e.launches.Done()
	if _, err := e.launchClaude(context.Background(), session, contract.LaunchRequest{AgentID: "agent", Cwd: cfg.WorkspaceRoot}, provider, "test-model"); err != nil {
		t.Fatal(err)
	}
	defer e.Stop(context.Background(), "agent")
	err = e.WaitForSessionReady(context.Background(), "agent", 20*time.Millisecond)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("WaitForSessionReady() = %v, want deadline exceeded", err)
	}
}

func TestClaudeWaitForSessionReadyExitFailFast(t *testing.T) {
	cfg := validTestConfig(t)
	cfg.Providers[0].Name = "claude"
	cfg.Providers[0].Executable = claudeReadinessHelper(t, "exit 0")
	e := NewExecution(cfg)
	e.BindTerminalSink(func(context.Context, turndto.TurnTerminalV2) {})
	provider := cfg.Providers[0]
	session, err := e.reserveSession(contract.LaunchRequest{AgentID: "agent", Cwd: cfg.WorkspaceRoot}, provider)
	if err != nil {
		t.Fatal(err)
	}
	defer e.launches.Done()
	if _, err := e.launchClaude(context.Background(), session, contract.LaunchRequest{AgentID: "agent", Cwd: cfg.WorkspaceRoot}, provider, "test-model"); err != nil {
		t.Fatal(err)
	}
	err = e.WaitForSessionReady(context.Background(), "agent", time.Second)
	if err == nil || !strings.Contains(err.Error(), "exited before system/init") {
		t.Fatalf("WaitForSessionReady() = %v, want fail-fast process exit", err)
	}
}

func claudeReadinessHelper(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "claude-helper.sh")
	script := fmt.Sprintf("#!/bin/sh\nset -eu\n%s\n", body)
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	return path
}
