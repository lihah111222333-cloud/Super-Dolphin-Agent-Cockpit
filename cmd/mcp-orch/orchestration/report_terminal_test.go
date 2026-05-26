package orchestration

import (
	"context"
	"errors"
	"strings"
	"testing"

	agentdto "github.com/anthropic-ai/super-agent-v3/internal/dto/agent"
)

func TestTerminalReportEventWithoutBodyWritesFallbackReport(t *testing.T) {
	svc := &service{agents: map[string]*agentRuntime{
		"agent-1": {id: "agent-1", state: agentdto.StateTurnRunning},
	}}

	if _, err := svc.HandleReportEvent(context.Background(), ReportEvent{AgentID: "agent-1", EventType: "completion"}); err != nil {
		t.Fatalf("HandleReportEvent() error = %v", err)
	}

	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if got.Report == "" || !strings.Contains(got.Report, "without producing") {
		t.Fatalf("GetReport().Report = %q, want explicit no-report fallback", got.Report)
	}
}

func TestProcessExitFailureWithoutReportWritesFallbackReport(t *testing.T) {
	svc := NewService(silentLogger(), nil, nil, nil, nil, nil)
	agent := svc.newAgentLocked("agent-1")
	agent.state = agentdto.StateTurnRunning
	agent.launchSeq = 1
	svc.agents[agent.id] = agent

	svc.handleProcessExit(context.Background(), agent.id, 1, errors.New("process crashed"))

	got, err := svc.GetReport(context.Background(), "agent-1")
	if err != nil {
		t.Fatalf("GetReport() error = %v", err)
	}
	if got.State != string(agentdto.StateFailed) || !strings.Contains(got.Report, "process crashed") {
		t.Fatalf("GetReport() = %#v, want failed-state fallback carrying exit error", got)
	}
}
