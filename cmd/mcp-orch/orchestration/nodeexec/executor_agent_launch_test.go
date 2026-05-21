package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestAgentExecutor_Execute_MissingCWDDoesNotCallLauncher(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{err: contract.ErrLaunchCWDRequired}
	exec := NewAgentExecutor(launcher)
	raw, err := json.Marshal(AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	out, err := exec.Execute(context.Background(), Node{NodeType: "agent", Title: "node", Config: raw}, RunContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("outcome = %#v, want validation failure", out)
	}
	if launcher.called != 0 {
		t.Fatalf("launcher called %d times, want 0 for missing cwd contract failure", launcher.called)
	}
	if strings.HasPrefix(out.ErrorSummary, "launch agent:") {
		t.Fatalf("ErrorSummary = %q, must not be retryable launch-agent validation", out.ErrorSummary)
	}
}

func TestAgentExecutor_ExecuteClassifiesLaunchCWDContractError(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{err: contract.ErrLaunchCWDRequired}
	exec := NewAgentExecutor(launcher)
	raw, err := json.Marshal(AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer", CWD: "/tmp/node-cwd"}})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	out, err := exec.Execute(context.Background(), Node{NodeType: "agent", Title: "node", Config: raw}, RunContext{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("outcome = %#v, want validation failure", out)
	}
	if launcher.called != 1 {
		t.Fatalf("launcher called %d times, want 1", launcher.called)
	}
	if strings.HasPrefix(out.ErrorSummary, "launch agent:") {
		t.Fatalf("ErrorSummary = %q, must not be retryable launch-agent validation", out.ErrorSummary)
	}
}

func TestAgentExecutor_Execute_InvalidConfig_BadJSON(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	node := Node{
		NodeType: "agent",
		Config:   json.RawMessage(`{"exec": "not-an-object"`), // 截断 + 类型错
	}
	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want classified validation outcome", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassValidation)
	}
	if launcher.called != 0 {
		t.Fatalf("LaunchAgent should not be called on invalid config, got %d", launcher.called)
	}
}

func TestAgentExecutor_Execute_InvalidConfig_MissingAgentKey(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{Provider: "claude"}, // 缺 agent_key
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want classified validation outcome", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassValidation)
	}
	if launcher.called != 0 {
		t.Fatalf("LaunchAgent should not be called, got %d", launcher.called)
	}
}

func TestAgentExecutor_Execute_InvalidProvider(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{
		Exec: AgentExecConfig{
			AgentKey: "implementer",
			Provider: "openai",
		},
	}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want classified provider validation outcome", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassValidation)
	}
	if !strings.Contains(out.ErrorSummary, "invalid provider") {
		t.Fatalf("ErrorSummary = %q, want invalid provider", out.ErrorSummary)
	}
	if launcher.called != 0 {
		t.Fatalf("LaunchAgent should not be called, got %d", launcher.called)
	}
}

func TestAgentExecutor_Execute_LaunchTransientErr(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{err: errors.New("connection refused: provider not up")}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)

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
	if out.ErrorSummary == "" {
		t.Fatalf("ErrorSummary should be populated on failure")
	}
}

func TestAgentExecutor_Execute_LaunchQuotaErr(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{err: errors.New("quota_exhausted: out of credits")}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassQuota {
		t.Fatalf("FailureClass = %q, want %q", out.FailureClass, FailureClassQuota)
	}
}

func TestAgentExecutor_Execute_LaunchPermanentErr(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{err: errors.New("401 unauthorized: invalid api key")}
	exec := NewAgentExecutor(launcher)

	cfg := AgentNodeConfig{Exec: AgentExecConfig{AgentKey: "implementer"}}
	node := makeAgentNode(t, cfg)

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v, want nil", err)
	}
	if out.Status != NodeStatusFailed {
		t.Fatalf("Status = %q, want %q", out.Status, NodeStatusFailed)
	}
	if out.FailureClass != FailureClassValidation {
		t.Fatalf("FailureClass = %q, want %q (permanent err maps to validation per F1.4 spec)",
			out.FailureClass, FailureClassValidation)
	}
}
