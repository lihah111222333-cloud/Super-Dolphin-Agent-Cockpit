package nodeexec

import (
	"context"
	"encoding/json"
	"errors"
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

func TestAgentExecutor_Outputs_WritesSharedfile(t *testing.T) {
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
	if len(writer.writes) != 1 {
		t.Fatalf("writer called %d times, want 1", len(writer.writes))
	}
	if writer.writes[0].Path != "reports/agent.json" {
		t.Fatalf("write path = %q, want reports/agent.json", writer.writes[0].Path)
	}
	var got agentLaunchResult
	if err := json.Unmarshal([]byte(writer.writes[0].Content), &got); err != nil {
		t.Fatalf("unmarshal sharedfile payload: %v; raw=%s", err, writer.writes[0].Content)
	}
	if got.ThreadID != "thread-output" || got.AgentKey != "implementer" {
		t.Fatalf("sharedfile payload = %+v, want thread-output/implementer", got)
	}
	if out.Result != nil {
		t.Fatalf("Result should be nil when to_sharedfile set and to_node_result unset; got %s", out.Result)
	}
}

func TestAgentExecutor_Outputs_ToNodeResult(t *testing.T) {
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
	if out.Result == nil {
		t.Fatalf("Result nil, want agent launch metadata")
	}
	var got agentLaunchResult
	if err := json.Unmarshal(out.Result, &got); err != nil {
		t.Fatalf("unmarshal Result: %v; raw=%s", err, out.Result)
	}
	if got.ThreadID != "thread-result" || got.AgentKey != "reviewer" {
		t.Fatalf("Result = %+v, want thread-result/reviewer", got)
	}
}

func TestAgentExecutor_Outputs_OversizeNodeResultRejected(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-oversize"}
	exec := NewAgentExecutor(launcher)
	node := makeAgentNode(t, AgentNodeConfig{
		Exec:    AgentExecConfig{AgentKey: strings.Repeat("x", NodeResultSizeCapBytes)},
		Outputs: OutputsConfig{ToNodeResult: true},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassValidation {
		t.Fatalf("got status=%q class=%q, want failed/validation", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "4KB") ||
		!strings.Contains(out.ErrorSummary, "size cap") ||
		!strings.Contains(out.ErrorSummary, "ADR-006") {
		t.Fatalf("ErrorSummary = %q, want 4KB size cap ADR-006 context", out.ErrorSummary)
	}
	if launcher.called != 1 {
		t.Fatalf("LaunchAgent called %d times, want 1", launcher.called)
	}
}

func TestAgentExecutor_Outputs_ToSharedfileWithoutWriterInfrastructure(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-no-writer"}
	exec := NewAgentExecutor(launcher)
	node := makeAgentNode(t, AgentNodeConfig{
		Exec: AgentExecConfig{AgentKey: "implementer"},
		Outputs: OutputsConfig{
			ToSharedfile: &SharedfileTarget{Path: "reports/agent.json", LockMode: "exclusive"},
		},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassInfrastructure {
		t.Fatalf("got status=%q class=%q, want failed/infrastructure", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "SharedFileWriter not wired") {
		t.Fatalf("ErrorSummary = %q, want SharedFileWriter not wired", out.ErrorSummary)
	}
	if launcher.called != 1 {
		t.Fatalf("LaunchAgent called %d times, want 1", launcher.called)
	}
}

func TestAgentExecutor_Outputs_ToSharedfileWriteErrorInfrastructure(t *testing.T) {
	t.Parallel()
	launcher := &stubAgentLauncher{threadID: "thread-write-error"}
	writer := &stubAgentSharedFileWriter{err: errors.New("disk full")}
	exec := NewAgentExecutor(launcher)
	node := makeAgentNode(t, AgentNodeConfig{
		Exec: AgentExecConfig{AgentKey: "implementer"},
		Outputs: OutputsConfig{
			ToSharedfile: &SharedfileTarget{Path: "reports/fail.json", LockMode: "exclusive"},
		},
	})

	out, err := exec.Execute(context.Background(), node, RunContext{SharedFileWriter: writer})
	if err != nil {
		t.Fatalf("Execute() framework error = %v", err)
	}
	if out.Status != NodeStatusFailed || out.FailureClass != FailureClassInfrastructure {
		t.Fatalf("got status=%q class=%q, want failed/infrastructure", out.Status, out.FailureClass)
	}
	if !strings.Contains(out.ErrorSummary, "reports/fail.json") || !strings.Contains(out.ErrorSummary, "disk full") {
		t.Fatalf("ErrorSummary = %q, want path and disk full", out.ErrorSummary)
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
