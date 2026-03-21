package dashboard

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/orchestration"
)

func TestGetAgentDetailIncludesDerivedFields(t *testing.T) {
	t.Parallel()

	svc := &service{
		orchestration: &stubDashboardOrchestration{
			snapshot: orchestration.AgentSnapshot{
				ID:           "agent-1",
				Name:         "Agent One",
				ThreadID:     "thread-1",
				ActiveTurnID: "turn-1",
				State:        "running",
				LastReport:   "stale",
			},
			report: orchestration.AgentReportResult{
				AgentID: "agent-1",
				Report:  "fresh report",
				State:   "running",
			},
		},
	}

	got, err := svc.GetAgentDetail(context.Background(), " agent-1 ")
	if err != nil {
		t.Fatalf("GetAgentDetail() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetAgentDetail() = nil")
	}
	if got.AgentID != "agent-1" || got.Name != "Agent One" || got.ThreadID != "thread-1" || got.Status != "running" {
		t.Fatalf("GetAgentDetail() derived fields = %#v", got)
	}
	if got.LastReport != "fresh report" || got.Snapshot.LastReport != "fresh report" {
		t.Fatalf("GetAgentDetail() report fields = %#v", got)
	}
	if len(got.TurnHistory) != 1 || got.TurnHistory[0].TurnID != "turn-1" || got.TurnHistory[0].Status != "running" {
		t.Fatalf("GetAgentDetail() turn history = %#v", got.TurnHistory)
	}
}

func TestGetAgentDetailSkipsEmptyTurnHistoryWithoutActiveTurn(t *testing.T) {
	t.Parallel()

	svc := &service{
		orchestration: &stubDashboardOrchestration{
			snapshot: orchestration.AgentSnapshot{
				ID:       "agent-2",
				Name:     "Agent Two",
				ThreadID: "thread-2",
				State:    "idle",
			},
		},
	}

	got, err := svc.GetAgentDetail(context.Background(), "agent-2")
	if err != nil {
		t.Fatalf("GetAgentDetail() error = %v", err)
	}
	if len(got.TurnHistory) != 0 {
		t.Fatalf("GetAgentDetail() turn history = %#v, want empty", got.TurnHistory)
	}
	if got.Status != "idle" {
		t.Fatalf("GetAgentDetail() status = %q, want idle", got.Status)
	}
}

type stubDashboardOrchestration struct {
	snapshot orchestration.AgentSnapshot
	report   orchestration.AgentReportResult
}

func (s *stubDashboardOrchestration) LaunchAgent(context.Context, orchestration.LaunchRequest) error {
	return nil
}

func (s *stubDashboardOrchestration) ListAgents(context.Context) ([]orchestration.AgentSnapshot, error) {
	return nil, nil
}

func (s *stubDashboardOrchestration) StopAgent(context.Context, string) error { return nil }

func (s *stubDashboardOrchestration) SubmitTurn(context.Context, orchestration.TurnSubmission) error {
	return nil
}

func (s *stubDashboardOrchestration) CompleteTurn(context.Context, string, string, bool, string) error {
	return nil
}

func (s *stubDashboardOrchestration) Recover(context.Context, string) error { return nil }

func (s *stubDashboardOrchestration) BindSessionGeneration(context.Context, string, uint64) error {
	return nil
}

func (s *stubDashboardOrchestration) Snapshot(context.Context, string) (orchestration.AgentSnapshot, error) {
	return s.snapshot, nil
}

func (s *stubDashboardOrchestration) UpdateRuntime(context.Context, orchestration.RuntimeReport) error {
	return nil
}

func (s *stubDashboardOrchestration) SetReport(context.Context, string, string) error { return nil }

func (s *stubDashboardOrchestration) GetState(context.Context, string) (orchestration.AgentStateResult, error) {
	return orchestration.AgentStateResult{}, nil
}

func (s *stubDashboardOrchestration) GetReport(context.Context, string) (orchestration.AgentReportResult, error) {
	return s.report, nil
}

func (s *stubDashboardOrchestration) RememberReportRequest(context.Context, orchestration.RememberReportRequest) (orchestration.RememberReportRequestResult, error) {
	return orchestration.RememberReportRequestResult{}, nil
}

func (s *stubDashboardOrchestration) HandleReportEvent(context.Context, orchestration.ReportEvent) (orchestration.ReportEventResult, error) {
	return orchestration.ReportEventResult{}, nil
}

func (s *stubDashboardOrchestration) CreateDAG(context.Context, orchestration.CreateDAGRequest) (orchestration.DAGDetail, error) {
	return orchestration.DAGDetail{}, nil
}

func (s *stubDashboardOrchestration) GetDAG(context.Context, string) (orchestration.DAGDetail, error) {
	return orchestration.DAGDetail{}, nil
}

func (s *stubDashboardOrchestration) ListDAGs(context.Context, orchestration.ListDAGsFilter) ([]orchestration.DAGSummary, error) {
	return nil, nil
}

func (s *stubDashboardOrchestration) UpdateNodeStatus(context.Context, orchestration.UpdateNodeStatusRequest) (orchestration.DAGNode, error) {
	return orchestration.DAGNode{}, nil
}
