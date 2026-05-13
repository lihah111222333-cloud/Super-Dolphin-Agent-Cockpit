package nodeexec

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type stubAgentSharedFileWriter struct {
	writes []struct{ Path, Content string }
	err    error
}

func (s *stubAgentSharedFileWriter) WriteSharedFile(_ context.Context, path, content string) error {
	if s.err != nil {
		return s.err
	}
	s.writes = append(s.writes, struct{ Path, Content string }{path, content})
	return nil
}

func TestAgentExecutor_Outputs_DoesNotWriteSharedfileAtLaunch(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-output"}
	writer := &stubAgentSharedFileWriter{}
	exec := NewAgentExecutor(launcher)
	node := makeAgentNode(t, AgentNodeConfig{
		Exec: AgentExecConfig{AgentKey: "implementer"},
		Outputs: OutputsConfig{
			ToSharedfile: &SharedfileTarget{Path: "reports/agent.json", LockMode: "exclusive"},
		},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{SharedFileWriter: writer})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done; summary=%q", out.Status, out.ErrorSummary)
	}
	if len(writer.writes) != 0 {
		t.Fatalf("writer called %d times at launch, want 0; writes=%+v", len(writer.writes), writer.writes)
	}
	if out.Result != nil {
		t.Fatalf("Result = %s, want nil because agent outputs materialize from TurnCompleted", out.Result)
	}
}

func TestAgentExecutor_Outputs_DoesNotEmitLaunchMetadataToNodeResult(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-result"}
	exec := NewAgentExecutor(launcher)
	node := makeAgentNode(t, AgentNodeConfig{
		Exec:    AgentExecConfig{AgentKey: "reviewer"},
		Outputs: OutputsConfig{ToNodeResult: true},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusDone {
		t.Fatalf("Status = %q, want done", out.Status)
	}
	if out.Result != nil {
		t.Fatalf("Result = %s, want nil because launch metadata is not the agent output", out.Result)
	}
	if launcher.called != 1 {
		t.Fatalf("LaunchAgent called %d times, want 1", launcher.called)
	}
}

func TestAgentExecutor_Outputs_RejectsWebhookURL(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-should-not-launch"}
	exec := NewAgentExecutor(launcher)
	node := Node{
		NodeType: "agent",
		Config:   json.RawMessage(`{"exec":{"agent_key":"implementer"},"outputs":{"webhook_url":"x"}}`),
	}

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "webhook_url") {
		t.Fatalf("ErrorSummary = %q, want webhook_url", out.ErrorSummary)
	}
	if launcher.called != 0 {
		t.Fatalf("LaunchAgent called %d times, want 0 on invalid outputs", launcher.called)
	}
}

func TestAgentExecutor_Outputs_RejectsCommandRef(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-should-not-launch"}
	exec := NewAgentExecutor(launcher)
	node := Node{
		NodeType: "agent",
		Config:   json.RawMessage(`{"exec":{"agent_key":"implementer"},"outputs":{"command_ref":"build_app"}}`),
	}

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "command_ref") {
		t.Fatalf("ErrorSummary = %q, want command_ref", out.ErrorSummary)
	}
	if launcher.called != 0 {
		t.Fatalf("LaunchAgent called %d times, want 0 on invalid outputs", launcher.called)
	}
}
