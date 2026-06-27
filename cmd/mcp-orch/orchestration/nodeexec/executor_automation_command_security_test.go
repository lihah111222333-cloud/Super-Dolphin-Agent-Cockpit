package nodeexec

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestShellCommandRunnerRejectsMissingCWDAndWorkspaceRoots(t *testing.T) {
	t.Parallel()
	runner := NewShellCommandRunner()

	_, err := runner.RunCommandCard(context.Background(), AutomationCommandCard{
		CardKey:         "cwd-required",
		CommandTemplate: "printf ok",
		RiskLevel:       "high",
		Enabled:         true,
	}, json.RawMessage(`{}`))

	if err == nil {
		t.Fatal("RunCommandCard() error = nil, want missing cwd/workspace roots rejected")
	}
	if !strings.Contains(err.Error(), "cwd") || !strings.Contains(err.Error(), "workspace") {
		t.Fatalf("RunCommandCard() error = %v, want cwd/workspace diagnostic", err)
	}
}

func TestShellCommandRunnerDoesNotInheritSensitiveParentEnv(t *testing.T) {
	runner := NewShellCommandRunner()
	root := t.TempDir()
	t.Setenv("AUTOMATION_COMMAND_SECRET_TOKEN", "leaked-secret")

	result, err := runner.RunCommandCard(context.Background(), AutomationCommandCard{
		CardKey:         "env-boundary",
		CommandTemplate: `printf '%s' "$AUTOMATION_COMMAND_SECRET_TOKEN"`,
		RiskLevel:       "high",
		Enabled:         true,
	}, json.RawMessage(`{}`), AutomationCommandRunOptions{
		CWD:            root,
		WorkspaceRoots: []string{root},
		Env:            map[string]string{"PATH": os.Getenv("PATH")},
	})

	if err != nil {
		t.Fatalf("RunCommandCard() error = %v, want nil", err)
	}
	if strings.Contains(result.Stdout, "leaked-secret") {
		t.Fatalf("Stdout leaked sensitive parent env: %q", result.Stdout)
	}
}

func TestAutomationExecutorPassesTrustedCommandRunOptions(t *testing.T) {
	t.Parallel()
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "k", CommandTemplate: "x", RiskLevel: "high", Enabled: true}}
	runner := &captureRunner{result: AutomationCommandResult{CardKey: "k", Stdout: "ok"}}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{
		Exec: AutomationExecConfig{
			CommandRef:     "k",
			CWD:            "/repo/project",
			WorkspaceRoots: []string{"/repo"},
			Env:            map[string]string{"PATH": "/bin"},
		},
	})

	out := executeAutomationNode(t, exec, node, RunContext{DagKey: "dag-x", NodeKey: "node-auto", RunID: 7})
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done; summary=%q", out.Status, out.ErrorSummary)
	}
	if len(runner.lastOpts) != 1 {
		t.Fatalf("RunCommandCard options count = %d, want 1", len(runner.lastOpts))
	}
	got := runner.lastOpts[0]
	if got.CWD != "/repo/project" || len(got.WorkspaceRoots) != 1 || got.WorkspaceRoots[0] != "/repo" || got.Env["PATH"] != "/bin" {
		t.Fatalf("RunCommandCard options = %+v, want cwd/root/env from automation exec config", got)
	}
}
