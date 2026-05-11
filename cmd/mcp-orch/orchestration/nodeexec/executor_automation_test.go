package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

type stubAutomationGetter struct {
	called  int
	lastKey string
	card    AutomationCommandCard
	err     error
}

func (s *stubAutomationGetter) GetCommandCard(_ context.Context, cardKey string) (AutomationCommandCard, error) {
	s.called++
	s.lastKey = cardKey
	return s.card, s.err
}

type stubAutomationRunner struct {
	called   int
	lastCard AutomationCommandCard
	lastArgs json.RawMessage
	result   AutomationCommandResult
	err      error
}

func (s *stubAutomationRunner) RunCommandCard(_ context.Context, card AutomationCommandCard, args json.RawMessage) (AutomationCommandResult, error) {
	s.called++
	s.lastCard = card
	s.lastArgs = append(json.RawMessage(nil), args...)
	return s.result, s.err
}

func makeAutomationNode(t *testing.T, cfg AutomationNodeConfig) Node {
	t.Helper()
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal AutomationNodeConfig: %v", err)
	}
	return Node{
		DagKey:   "dag-x",
		NodeKey:  "node-auto",
		NodeType: "automation",
		Title:    "automation node",
		Config:   raw,
	}
}

func TestAutomationExecutor_Happy(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{
		CardKey:         "build_app",
		CommandTemplate: "printf 'hello %s' '{{.name}}'",
		Enabled:         true,
	}}
	exec := NewAutomationExecutor(getter, NewShellCommandRunner())
	node := makeAutomationNode(t, AutomationNodeConfig{Exec: AutomationExecConfig{
		Kind:       AutomationKindCommandCard,
		CommandRef: " build_app ",
		Args:       json.RawMessage(`{"name":"dolphin"}`),
	}})

	out, err := exec.Execute(context.Background(), node, RunContext{DagKey: "dag-x", NodeKey: "node-auto", RunID: 7})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusDone)
	}
	if out.FailureClass != "" {
		t.Fatalf("FailureClass = %q, want empty on success", out.FailureClass)
	}
	if getter.called != 1 || getter.lastKey != "build_app" {
		t.Fatalf("command_get called (%d, %q), want (1, build_app)", getter.called, getter.lastKey)
	}
	var result AutomationCommandResult
	if err := json.Unmarshal(out.Result, &result); err != nil {
		t.Fatalf("unmarshal Result: %v; payload=%s", err, out.Result)
	}
	if result.CardKey != "build_app" || result.ExitCode != 0 || result.Stdout != "hello dolphin" {
		t.Fatalf("Result = %#v, want card_key build_app exit 0 stdout hello dolphin", result)
	}
}

func TestAutomationExecutor_UnsupportedKind(t *testing.T) {
	getter := &stubAutomationGetter{}
	runner := &stubAutomationRunner{}
	exec := NewAutomationExecutor(getter, runner)
	node := Node{NodeType: "automation", Config: json.RawMessage(`{"exec":{"kind":"webhook","command_ref":"x"}}`)}

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassValidation)
	}
	if !strings.Contains(out.ErrorSummary, "unsupported automation.kind") {
		t.Fatalf("ErrorSummary = %q, want unsupported automation.kind", out.ErrorSummary)
	}
	if getter.called != 0 || runner.called != 0 {
		t.Fatalf("getter/runner called on unsupported kind: getter=%d runner=%d", getter.called, runner.called)
	}
}

func TestAutomationExecutor_CommandNotFound(t *testing.T) {
	getter := &stubAutomationGetter{err: errors.New("command missing not found")}
	runner := &stubAutomationRunner{}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{Exec: AutomationExecConfig{CommandRef: "missing"}})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassHard {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassHard)
	}
	if runner.called != 0 {
		t.Fatalf("runner called on command_get failure: %d", runner.called)
	}
}

func TestAutomationExecutor_Timeout(t *testing.T) {
	getter := &stubAutomationGetter{card: AutomationCommandCard{CardKey: "slow", CommandTemplate: "sleep 10", Enabled: true}}
	runner := &stubAutomationRunner{err: context.DeadlineExceeded}
	exec := NewAutomationExecutor(getter, runner)
	node := makeAutomationNode(t, AutomationNodeConfig{Exec: AutomationExecConfig{CommandRef: "slow"}})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassTransient {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassTransient)
	}
}

func TestAutomationExecutor_NilLauncher(t *testing.T) {
	exec := NewAutomationExecutor(nil, &stubAutomationRunner{})
	node := makeAutomationNode(t, AutomationNodeConfig{Exec: AutomationExecConfig{CommandRef: "build_app"}})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q on nil command_get client", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassValidation)
	}
	if !strings.Contains(out.ErrorSummary, "command_get client not wired") {
		t.Fatalf("ErrorSummary = %q, want command_get client not wired", out.ErrorSummary)
	}
}

func TestAutomationExecutor_ImplementsNodeExecutor(t *testing.T) {
	var _ NodeExecutor = (*AutomationExecutor)(nil)
}

func TestClassifyAutomationError(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want FailureClass
	}{
		{"not_found_hard", errors.New("command missing not found"), FailureClassHard},
		{"timeout_transient", errors.New("i/o timeout"), FailureClassTransient},
		{"network_transient", errors.New("connection refused"), FailureClassTransient},
		{"infra", errors.New("postgres service unavailable"), FailureClassInfrastructure},
		{"parse_validation", errors.New("parse command args: invalid json"), FailureClassValidation},
		{"nonzero_hard", CommandExitError{ExitCode: 2, Err: errors.New("exit status 2")}, FailureClassHard},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyAutomationError(tc.err); got != tc.want {
				t.Fatalf("classifyAutomationError(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}
