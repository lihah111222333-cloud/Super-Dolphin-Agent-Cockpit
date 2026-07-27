package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type commandExecutionAuthorizerStub struct {
	decision CommandExecutionAuthorization
	err      error
	calls    int
	request  CommandExecutionAuthorizationRequest
}

func (a *commandExecutionAuthorizerStub) AuthorizeCommandExecution(_ context.Context, request CommandExecutionAuthorizationRequest) (CommandExecutionAuthorization, error) {
	a.calls++
	a.request = request
	return a.decision, a.err
}

func TestShellCommandRunnerFailsClosedWithoutCommandExecutionAuthorizer(t *testing.T) {
	root := t.TempDir()
	runner := newShellCommandRunnerForTest(t)
	target := filepath.Join(root, "must-not-exist")

	_, err := runner.RunCommandCard(context.Background(), validAuthorizationTestCard("touch must-not-exist"), json.RawMessage(`{}`), authorizationTestRunOptions(root))
	if !errors.Is(err, ErrCommandExecutionAuthorizerUnavailable) {
		t.Fatalf("RunCommandCard() error = %v, want ErrCommandExecutionAuthorizerUnavailable", err)
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("command side effect exists or stat failed unexpectedly: %v", statErr)
	}
}

func TestShellCommandRunnerDeniesBeforeProcessSpawn(t *testing.T) {
	root := t.TempDir()
	authorizer := &commandExecutionAuthorizerStub{decision: CommandExecutionAuthorization{
		Subject: "agent-denied",
		Allowed: false,
	}}
	runner := newShellCommandRunnerForTest(t, WithCommandExecutionAuthorizer(authorizer))
	target := filepath.Join(root, "must-not-exist")

	_, err := runner.RunCommandCard(context.Background(), validAuthorizationTestCard("touch must-not-exist"), json.RawMessage(`{}`), authorizationTestRunOptions(root))
	if !errors.Is(err, ErrCommandExecutionDenied) {
		t.Fatalf("RunCommandCard() error = %v, want ErrCommandExecutionDenied", err)
	}
	if authorizer.calls != 1 {
		t.Fatalf("authorizer calls = %d, want 1", authorizer.calls)
	}
	if _, statErr := os.Stat(target); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("command side effect exists or stat failed unexpectedly: %v", statErr)
	}
}

func TestShellCommandRunnerRejectsAuthorizationWithoutTrustedPrincipal(t *testing.T) {
	root := t.TempDir()
	authorizer := &commandExecutionAuthorizerStub{decision: CommandExecutionAuthorization{Allowed: true}}
	runner := newShellCommandRunnerForTest(t, WithCommandExecutionAuthorizer(authorizer))

	_, err := runner.RunCommandCard(context.Background(), validAuthorizationTestCard("printf authorized"), json.RawMessage(`{}`), authorizationTestRunOptions(root))
	if !errors.Is(err, ErrCommandExecutionPrincipalMissing) {
		t.Fatalf("RunCommandCard() error = %v, want ErrCommandExecutionPrincipalMissing", err)
	}
}

func TestShellCommandRunnerExecutesAfterTrustedAuthorization(t *testing.T) {
	root := t.TempDir()
	authorizer := &commandExecutionAuthorizerStub{decision: CommandExecutionAuthorization{
		Subject: "agent-authorized",
		Allowed: true,
	}}
	runner := newShellCommandRunnerForTest(t, WithCommandExecutionAuthorizer(authorizer))

	result, err := runner.RunCommandCard(context.Background(), validAuthorizationTestCard("printf authorized"), json.RawMessage(`{}`), authorizationTestRunOptions(root))
	if err != nil {
		t.Fatalf("RunCommandCard() error = %v", err)
	}
	if result.Stdout != "authorized" {
		t.Fatalf("stdout = %q, want authorized", result.Stdout)
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("EvalSymlinks(%q): %v", root, err)
	}
	if authorizer.request.CardKey != "authorization-test" ||
		authorizer.request.CWD != canonicalRoot ||
		strings.TrimSpace(authorizer.request.Executable) == "" {
		t.Fatalf("authorization request = %+v, want card/cwd/executable metadata", authorizer.request)
	}
}

func validAuthorizationTestCard(command string) AutomationCommandCard {
	return AutomationCommandCard{
		CardKey:         "authorization-test",
		CommandTemplate: command,
		RiskLevel:       "high",
		Enabled:         true,
	}
}

func authorizationTestRunOptions(root string) AutomationCommandRunOptions {
	return AutomationCommandRunOptions{
		CWD:            root,
		WorkspaceRoots: []string{root},
		Env:            map[string]string{"PATH": os.Getenv("PATH")},
	}
}

type allowCommandExecutionAuthorizer struct{}

func (allowCommandExecutionAuthorizer) AuthorizeCommandExecution(context.Context, CommandExecutionAuthorizationRequest) (CommandExecutionAuthorization, error) {
	return CommandExecutionAuthorization{Subject: "test-authority", Allowed: true}, nil
}

func newShellCommandRunnerForTest(t testing.TB, opts ...ShellCommandRunnerOption) *ShellCommandRunner {
	t.Helper()
	runner, err := NewShellCommandRunner(opts...)
	if err != nil {
		t.Fatalf("NewShellCommandRunner() error = %v", err)
	}
	return runner
}

func newAuthorizedShellCommandRunnerForTest(t testing.TB) *ShellCommandRunner {
	t.Helper()
	return newShellCommandRunnerForTest(t, WithCommandExecutionAuthorizer(allowCommandExecutionAuthorizer{}))
}

func TestNewShellCommandRunnerRejectsNilOption(t *testing.T) {
	runner, err := NewShellCommandRunner(nil)
	if err == nil || !strings.Contains(err.Error(), "nil ShellCommandRunnerOption") {
		t.Fatalf("NewShellCommandRunner(nil) = (%v, %v), want explicit nil option error", runner, err)
	}
	if runner != nil {
		t.Fatalf("NewShellCommandRunner(nil) runner = %v, want nil", runner)
	}
}
