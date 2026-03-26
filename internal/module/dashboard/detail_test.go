package dashboard

import (
	"context"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestGetAgentDetailIncludesDerivedFields(t *testing.T) {
	t.Parallel()

	svc := &service{
		orchestration: &stubDashboardOrchestration{
			snapshot: contract.AgentSnapshot{
				ID:           "agent-1",
				Name:         "Agent One",
				ThreadID:     "thread-1",
				ActiveTurnID: "turn-1",
				State:        "running",
				LastReport:   "stale",
			},
			report: contract.AgentReportResult{
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
			snapshot: contract.AgentSnapshot{
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

func TestListDAGsUsesOrchestration(t *testing.T) {
	t.Parallel()

	svc := &service{
		orchestration: &stubDashboardOrchestration{
			listDAGsResult: []contract.DAGSummary{{DagKey: "dag-1", Title: "Dag One", Status: "running"}},
		},
	}

	got, err := svc.ListDAGs(context.Background(), contract.ListDAGsFilter{
		Keyword: " build ",
		Status:  " running ",
		Limit:   7,
	})
	if err != nil {
		t.Fatalf("ListDAGs() error = %v", err)
	}
	stub := svc.orchestration.(*stubDashboardOrchestration)
	if stub.listDAGsFilter.Keyword != "build" || stub.listDAGsFilter.Status != "running" || stub.listDAGsFilter.Limit != 7 {
		t.Fatalf("ListDAGs() filter = %#v", stub.listDAGsFilter)
	}
	if len(got) != 1 || got[0].DagKey != "dag-1" {
		t.Fatalf("ListDAGs() = %#v", got)
	}
}

func TestGetDAGDetailUsesOrchestration(t *testing.T) {
	t.Parallel()

	svc := &service{
		orchestration: &stubDashboardOrchestration{
			dagDetail: contract.DAGDetail{
				DAG:   contract.DAGSummary{DagKey: "dag-1", Title: "Dag One"},
				Nodes: []contract.DAGNode{{NodeKey: "node-1", Title: "Node One"}},
			},
		},
	}

	got, err := svc.GetDAGDetail(context.Background(), " dag-1 ")
	if err != nil {
		t.Fatalf("GetDAGDetail() error = %v", err)
	}
	stub := svc.orchestration.(*stubDashboardOrchestration)
	if stub.getDAGKey != "dag-1" {
		t.Fatalf("GetDAG() key = %q, want dag-1", stub.getDAGKey)
	}
	if got == nil || got.DAG.DagKey != "dag-1" || len(got.Nodes) != 1 || got.Nodes[0].NodeKey != "node-1" {
		t.Fatalf("GetDAGDetail() = %#v", got)
	}
}

type stubDashboardOrchestration struct {
	snapshot       contract.AgentSnapshot
	report         contract.AgentReportResult
	listDAGsResult []contract.DAGSummary
	listDAGsFilter contract.ListDAGsFilter
	dagDetail      contract.DAGDetail
	getDAGKey      string
}

func (s *stubDashboardOrchestration) LaunchAgent(context.Context, contract.LaunchRequest) error {
	return nil
}

func (s *stubDashboardOrchestration) ListAgents(context.Context) ([]contract.AgentSnapshot, error) {
	return nil, nil
}

func (s *stubDashboardOrchestration) StopAgent(context.Context, string) error { return nil }

func (s *stubDashboardOrchestration) SubmitTurn(context.Context, contract.TurnSubmission) error {
	return nil
}

func (s *stubDashboardOrchestration) CompleteTurn(context.Context, string, string, bool, string) error {
	return nil
}

func (s *stubDashboardOrchestration) Recover(context.Context, string) error { return nil }

func (s *stubDashboardOrchestration) BindSessionGeneration(context.Context, string, uint64) error {
	return nil
}

func (s *stubDashboardOrchestration) Snapshot(context.Context, string) (contract.AgentSnapshot, error) {
	return s.snapshot, nil
}

func (s *stubDashboardOrchestration) UpdateRuntime(context.Context, contract.RuntimeReport) error {
	return nil
}

func (s *stubDashboardOrchestration) GetState(context.Context, string) (contract.AgentStateResult, error) {
	return contract.AgentStateResult{}, nil
}

func (s *stubDashboardOrchestration) GetReport(context.Context, string) (contract.AgentReportResult, error) {
	return s.report, nil
}

func (s *stubDashboardOrchestration) RememberReportRequest(context.Context, contract.RememberReportRequest) (contract.RememberReportRequestResult, error) {
	return contract.RememberReportRequestResult{}, nil
}

func (s *stubDashboardOrchestration) HandleReportEvent(context.Context, contract.ReportEvent) (contract.ReportEventResult, error) {
	return contract.ReportEventResult{}, nil
}

func (s *stubDashboardOrchestration) CreateDAG(context.Context, contract.CreateDAGRequest) (contract.DAGDetail, error) {
	return contract.DAGDetail{}, nil
}

func (s *stubDashboardOrchestration) GetDAG(_ context.Context, dagKey string) (contract.DAGDetail, error) {
	s.getDAGKey = dagKey
	return s.dagDetail, nil
}

func (s *stubDashboardOrchestration) ListDAGs(_ context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	s.listDAGsFilter = filter
	return s.listDAGsResult, nil
}

func (s *stubDashboardOrchestration) UpdateNodeStatus(context.Context, contract.UpdateNodeStatusRequest) (contract.DAGNode, error) {
	return contract.DAGNode{}, nil
}
