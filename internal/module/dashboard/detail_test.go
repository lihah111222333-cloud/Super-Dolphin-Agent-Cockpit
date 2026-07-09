package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
)

func TestGetAgentDetailIncludesDerivedFields(t *testing.T) {
	t.Parallel()

	orchestration := &stubDashboardOrchestration{
		stubDashboardAgentDetailStore: stubDashboardAgentDetailStore{
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
	svc := &service{orchestration: orchestration, reports: orchestration}

	got, err := svc.GetAgentDetail(context.Background(), " agent-1 ")
	if err != nil {
		t.Fatalf("GetAgentDetail() error = %v", err)
	}
	if got == nil {
		t.Fatal("GetAgentDetail() = nil")
	}
	assertAgentDetailDerivedFields(t, got)
	assertAgentDetailReport(t, got)
	assertAgentDetailTurnHistory(t, got)
}

func assertAgentDetailDerivedFields(t *testing.T, got *AgentDetail) {
	t.Helper()
	if got.AgentID != "agent-1" || got.Name != "Agent One" || got.ThreadID != "thread-1" || got.Status != "running" {
		t.Fatalf("GetAgentDetail() derived fields = %#v", got)
	}
}

func assertAgentDetailReport(t *testing.T, got *AgentDetail) {
	t.Helper()
	if got.LastReport != "fresh report" || got.Snapshot.LastReport != "fresh report" {
		t.Fatalf("GetAgentDetail() report fields = %#v", got)
	}
}

func assertAgentDetailTurnHistory(t *testing.T, got *AgentDetail) {
	t.Helper()
	if len(got.TurnHistory) != 1 || got.TurnHistory[0].TurnID != "turn-1" || got.TurnHistory[0].Status != "running" {
		t.Fatalf("GetAgentDetail() turn history = %#v", got.TurnHistory)
	}
}

func TestGetAgentDetailSkipsEmptyTurnHistoryWithoutActiveTurn(t *testing.T) {
	t.Parallel()

	orchestration := &stubDashboardOrchestration{
		stubDashboardAgentDetailStore: stubDashboardAgentDetailStore{
			snapshot: contract.AgentSnapshot{
				ID:       "agent-2",
				Name:     "Agent Two",
				ThreadID: "thread-2",
				State:    "idle",
			},
		},
	}
	svc := &service{orchestration: orchestration, reports: orchestration}

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

func TestListDAGsUsesDAGRuntime(t *testing.T) {
	t.Parallel()

	stub := &stubDashboardOrchestration{
		listDAGsResult: []contract.DAGSummary{{DagKey: "dag-1", Title: "Dag One", Status: "running"}},
	}
	svc := &service{
		dagRuntime: stub,
	}

	got, err := svc.ListDAGs(context.Background(), contract.ListDAGsFilter{
		Keyword: " build ",
		Status:  " running ",
		Limit:   7,
	})
	if err != nil {
		t.Fatalf("ListDAGs() error = %v", err)
	}
	if stub.listDAGsFilter.Keyword != "build" || stub.listDAGsFilter.Status != "running" || stub.listDAGsFilter.Limit != 7 {
		t.Fatalf("ListDAGs() filter = %#v", stub.listDAGsFilter)
	}
	if len(got) != 1 || got[0].DagKey != "dag-1" {
		t.Fatalf("ListDAGs() = %#v", got)
	}
}

func TestGetDAGDetailUsesDAGRuntime(t *testing.T) {
	t.Parallel()

	stub := &stubDashboardOrchestration{
		dagDetail: contract.DAGDetail{
			DAG:   contract.DAGSummary{DagKey: "dag-1", Title: "Dag One"},
			Nodes: []contract.DAGNode{{NodeKey: "node-1", Title: "Node One"}},
		},
	}
	svc := &service{
		dagRuntime: stub,
	}

	got, err := svc.GetDAGDetail(context.Background(), " dag-1 ")
	if err != nil {
		t.Fatalf("GetDAGDetail() error = %v", err)
	}
	if stub.getDAGKey != "dag-1" {
		t.Fatalf("GetDAG() key = %q, want dag-1", stub.getDAGKey)
	}
	if got == nil || got.DAG.DagKey != "dag-1" || len(got.Nodes) != 1 || got.Nodes[0].NodeKey != "node-1" {
		t.Fatalf("GetDAGDetail() = %#v", got)
	}
}

func TestListDAGRunsUsesDAGRuntime(t *testing.T) {
	t.Parallel()

	stub := &stubDashboardOrchestration{
		listRunsResult: contract.ListRunsResponse{
			Runs: []contract.Run{{RunKey: "run-1", DagKey: "dag-1", Status: "succeeded"}},
		},
	}
	svc := &service{
		dagRuntime: stub,
	}

	got, err := svc.ListDAGRuns(context.Background(), " dag-1 ", " running ", 5)
	if err != nil {
		t.Fatalf("ListDAGRuns() error = %v", err)
	}
	if stub.listRunsRequest.DagKey != "dag-1" || stub.listRunsRequest.Status != "running" || stub.listRunsRequest.Limit != 5 {
		t.Fatalf("ListRuns() request = %#v", stub.listRunsRequest)
	}
	if len(got) != 1 || got[0].RunKey != "run-1" {
		t.Fatalf("ListDAGRuns() = %#v", got)
	}
}

func TestListDAGRunsDefaultLimitMatchesDAGRuntime(t *testing.T) {
	t.Parallel()

	stub := &stubDashboardOrchestration{
		listRunsResult: contract.ListRunsResponse{Runs: []contract.Run{}},
	}
	svc := &service{
		dagRuntime: stub,
	}

	if _, err := svc.ListDAGRuns(context.Background(), "dag-1", "", 0); err != nil {
		t.Fatalf("ListDAGRuns() error = %v", err)
	}
	if stub.listRunsRequest.Limit != 50 {
		t.Fatalf("ListRuns() request limit = %d, want orchestration default 50", stub.listRunsRequest.Limit)
	}
}

func TestListDAGRunsRequiresDAGKey(t *testing.T) {
	t.Parallel()

	svc := &service{}
	_, err := svc.ListDAGRuns(context.Background(), " ", "", 5)
	if err == nil {
		t.Fatal("ListDAGRuns() error = nil, want dag key required")
	}
}

func TestGetDAGRunUsesRuntimeNodes(t *testing.T) {
	t.Parallel()

	threadID := "thread-child"
	svc := &service{
		dagRuntime: &stubDashboardOrchestration{
			getRunResult: contract.GetRunResponse{
				Run: contract.Run{RunKey: "dag-1#run-1", DagKey: "dag-1", Status: "running"},
				Nodes: []contract.DAGNode{{
					NodeKey:          "node-1",
					Status:           "running",
					SpawningThreadID: &threadID,
				}},
			},
		},
	}

	got, err := svc.GetDAGRun(context.Background(), " dag-1#run-1 ")
	if err != nil {
		t.Fatalf("GetDAGRun() error = %v", err)
	}
	stub := svc.dagRuntime.(*stubDashboardOrchestration)
	if stub.getRunRequest.RunKey != "dag-1#run-1" {
		t.Fatalf("GetRun() request = %#v", stub.getRunRequest)
	}
	if got.Run.RunKey != "dag-1#run-1" {
		t.Fatalf("GetDAGRun().Run = %#v", got.Run)
	}
	if len(got.Nodes) != 1 || got.Nodes[0].SpawningThreadID == nil || *got.Nodes[0].SpawningThreadID != "thread-child" {
		t.Fatalf("GetDAGRun().Nodes = %#v", got.Nodes)
	}
}

func TestStartDAGUsesDAGRuntime(t *testing.T) {
	t.Parallel()

	stub := &stubDashboardOrchestration{
		startDAGResult: contract.StartDAGResponse{RunKey: "dag-1#run-ui", Version: 7},
	}
	svc := &service{
		dagRuntime: stub,
	}

	got, err := svc.StartDAG(context.Background(), " dag-1 ", " manual ", " ui-click-1 ")
	if err != nil {
		t.Fatalf("StartDAG() error = %v", err)
	}
	if got.RunKey != "dag-1#run-ui" || got.Version != 7 {
		t.Fatalf("StartDAG() = %#v", got)
	}
	if stub.startDAGRequest != (contract.StartDAGRequest{DagKey: "dag-1", TriggerSource: "manual", IdempotencyKey: "ui-click-1"}) {
		t.Fatalf("StartDAG request = %#v", stub.startDAGRequest)
	}
}

func TestStartDAGDefaultsManualTrigger(t *testing.T) {
	t.Parallel()

	stub := &stubDashboardOrchestration{
		startDAGResult: contract.StartDAGResponse{RunKey: "run-1", Version: 1},
	}
	svc := &service{
		dagRuntime: stub,
	}

	if _, err := svc.StartDAG(context.Background(), "dag-1", "", ""); err != nil {
		t.Fatalf("StartDAG() error = %v", err)
	}
	if stub.startDAGRequest.TriggerSource != "manual" {
		t.Fatalf("trigger source = %q, want manual", stub.startDAGRequest.TriggerSource)
	}
}

func TestTerminateDAGUsesRuntimeAndDefaultsReason(t *testing.T) {
	t.Parallel()

	stub := &stubDashboardOrchestration{}
	svc := &service{
		dagRuntime: stub,
	}

	err := svc.TerminateDAG(context.Background(), " dag-1 ", " run-1 ", " ")
	if err != nil {
		t.Fatalf("TerminateDAG() error = %v", err)
	}
	if stub.terminateDAGRequest != (contract.TerminateDAGRequest{DagKey: "dag-1", RunKey: "run-1", Reason: "user_requested"}) {
		t.Fatalf("TerminateDAG request = %#v", stub.terminateDAGRequest)
	}
}

func TestTerminateDAGRejectsMissingRunKey(t *testing.T) {
	t.Parallel()

	svc := &service{dagRuntime: &stubDashboardOrchestration{}}

	err := svc.TerminateDAG(context.Background(), "dag-1", " ", "")
	if err == nil || !strings.Contains(err.Error(), "run key") {
		t.Fatalf("TerminateDAG() error = %v, want run key required", err)
	}
}

func TestStartDAGRejectsNonManualTrigger(t *testing.T) {
	t.Parallel()

	svc := &service{dagRuntime: &stubDashboardOrchestration{}}
	_, err := svc.StartDAG(context.Background(), "dag-1", "scheduled", "")
	if err == nil || !strings.Contains(err.Error(), "manual") {
		t.Fatalf("StartDAG() error = %v, want manual trigger rejection", err)
	}
}

func TestStartDAGRequiresDAGKey(t *testing.T) {
	t.Parallel()

	svc := &service{}
	_, err := svc.StartDAG(context.Background(), " ", "manual", "")
	if err == nil {
		t.Fatal("StartDAG() error = nil, want dag key required")
	}
}

// TestCreateAndStartDAGRollsBackCreatedDAGWhenStartFails 确认立即启动失败不会留下半创建 DAG。
func TestCreateAndStartDAGRollsBackCreatedDAGWhenStartFails(t *testing.T) {
	t.Parallel()

	startErr := errors.New("start failed")
	orchestration := &stubDashboardOrchestration{startDAGErr: startErr}
	svc := &service{dagRuntime: orchestration}

	_, _, err := svc.CreateAndStartDAG(context.Background(), contract.CreateDAGRequest{
		DagKey:    " dag-rollback ",
		Title:     "Rollback DAG",
		CreatedBy: dashboardUICreatedBy,
		Nodes: []contract.CreateDAGNodeRequest{{
			NodeKey:    "draft",
			Title:      "Draft",
			NodeType:   "agent",
			AssignedTo: "codex-runner",
		}},
	}, "ui-create-1")
	if !errors.Is(err, startErr) {
		t.Fatalf("CreateAndStartDAG() error = %v, want start failure", err)
	}
	if orchestration.deleteDAGRequest != (contract.DeleteDAGRequest{DagKey: "dag-rollback"}) {
		t.Fatalf("DeleteDAG() request after failed start = %#v, want rollback", orchestration.deleteDAGRequest)
	}
}

func TestDispatchDAGNodeUsesDAGRuntime(t *testing.T) {
	t.Parallel()

	stub := &stubDashboardOrchestration{
		dispatchNodeResult: contract.DispatchNodeResponse{WakeupID: 99, Enqueued: true},
	}
	svc := &service{
		dagRuntime: stub,
	}

	got, err := svc.DispatchDAGNode(context.Background(), contract.DispatchNodeRequest{
		DagKey:     " dag-1 ",
		RunID:      88,
		NodeKey:    " draft ",
		AssignedTo: " codex-runner ",
	})
	if err != nil {
		t.Fatalf("DispatchDAGNode() error = %v", err)
	}
	if !got.Enqueued || got.WakeupID != 99 {
		t.Fatalf("DispatchDAGNode() = %#v", got)
	}
	if stub.dispatchNodeRequest != (contract.DispatchNodeRequest{DagKey: "dag-1", RunID: 88, NodeKey: "draft", AssignedTo: "codex-runner"}) {
		t.Fatalf("DispatchNode request = %#v", stub.dispatchNodeRequest)
	}
}

func TestApplyDAGOpsRejectsMissingOps(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ops  json.RawMessage
	}{
		{"missing", nil},
		{"null", json.RawMessage(`null`)},
		{"empty_array", json.RawMessage(`[]`)},
		{"blank", json.RawMessage(`   `)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			stub := &stubDashboardOrchestration{}
			svc := &service{dagRuntime: stub}

			_, err := svc.ApplyDAGOps(context.Background(), contract.ApplyOpsRequest{
				DagKey:      "dag-1",
				BaseVersion: 0,
				Ops:         tc.ops,
			})
			if err == nil || !strings.Contains(err.Error(), "ops") {
				t.Fatalf("ApplyDAGOps() error = %v, want ops required", err)
			}
			if len(stub.applyOpsRequest.Ops) != 0 {
				t.Fatalf("ApplyOps should not be called, got request %#v", stub.applyOpsRequest)
			}
		})
	}
}

type stubDashboardOrchestration struct {
	stubDashboardAgentDetailStore

	mu                  sync.Mutex
	listDAGsResult      []contract.DAGSummary
	listDAGsErr         error
	listDAGsFilter      contract.ListDAGsFilter
	dagDetail           contract.DAGDetail
	getDAGKey           string
	listRunsResult      contract.ListRunsResponse
	listRunsByDAG       map[string]contract.ListRunsResponse
	listRunsErr         error
	listRunsRequest     contract.ListRunsRequest
	listRunsRequests    []contract.ListRunsRequest
	getRunResult        contract.GetRunResponse
	getRunRequest       contract.GetRunRequest
	createDAGRequest    contract.CreateDAGRequest
	createDAGResult     contract.DAGDetail
	createDAGErr        error
	startDAGRequest     contract.StartDAGRequest
	startDAGResult      contract.StartDAGResponse
	startDAGErr         error
	terminateDAGRequest contract.TerminateDAGRequest
	terminateDAGErr     error
	deleteDAGRequest    contract.DeleteDAGRequest
	deleteDAGErr        error
	dispatchNodeRequest contract.DispatchNodeRequest
	dispatchNodeResult  contract.DispatchNodeResponse
	dispatchNodeErr     error
	applyOpsRequest     contract.ApplyOpsRequest
	applyOpsResult      contract.ApplyOpsResponse
	applyOpsErr         error
	applyOpsCalled      bool
}

type stubDashboardAgentDetailStore struct {
	snapshot contract.AgentSnapshot
	report   contract.AgentReportResult
}

var (
	_ OrchestrationReader             = (*stubDashboardOrchestration)(nil)
	_ contract.DAGRuntime             = (*stubDashboardOrchestration)(nil)
	_ contract.DAGCreateRuntime       = (*stubDashboardOrchestration)(nil)
	_ contract.DAGDeleteRuntime       = (*stubDashboardOrchestration)(nil)
	_ contract.DAGNodeDispatchRuntime = (*stubDashboardOrchestration)(nil)
)

func (stubDashboardAgentDetailStore) ListAgents(context.Context) ([]contract.AgentSnapshot, error) {
	return nil, nil
}

func (s *stubDashboardAgentDetailStore) Snapshot(context.Context, string) (contract.AgentSnapshot, error) {
	return s.snapshot, nil
}

func (s *stubDashboardAgentDetailStore) GetReport(context.Context, string) (contract.AgentReportResult, error) {
	return s.report, nil
}

func (s *stubDashboardOrchestration) CreateDAG(_ context.Context, req contract.CreateDAGRequest) (contract.DAGDetail, error) {
	s.createDAGRequest = req
	if s.createDAGErr != nil {
		return contract.DAGDetail{}, s.createDAGErr
	}
	return s.createDAGResult, nil
}

func (s *stubDashboardOrchestration) GetDAG(_ context.Context, dagKey string) (contract.DAGDetail, error) {
	s.getDAGKey = dagKey
	return s.dagDetail, nil
}

func (s *stubDashboardOrchestration) ListDAGs(_ context.Context, filter contract.ListDAGsFilter) ([]contract.DAGSummary, error) {
	s.listDAGsFilter = filter
	if s.listDAGsErr != nil {
		return nil, s.listDAGsErr
	}
	return s.listDAGsResult, nil
}

func (s *stubDashboardOrchestration) StartDAG(_ context.Context, req contract.StartDAGRequest) (contract.StartDAGResponse, error) {
	s.startDAGRequest = req
	if s.startDAGErr != nil {
		return contract.StartDAGResponse{}, s.startDAGErr
	}
	return s.startDAGResult, nil
}

func (s *stubDashboardOrchestration) TerminateDAG(_ context.Context, req contract.TerminateDAGRequest) error {
	s.terminateDAGRequest = req
	return s.terminateDAGErr
}

func (s *stubDashboardOrchestration) DeleteDAG(_ context.Context, req contract.DeleteDAGRequest) error {
	s.deleteDAGRequest = req
	return s.deleteDAGErr
}

func (s *stubDashboardOrchestration) GetRun(_ context.Context, req contract.GetRunRequest) (contract.GetRunResponse, error) {
	s.getRunRequest = req
	return s.getRunResult, nil
}

func (s *stubDashboardOrchestration) ApplyOps(_ context.Context, req contract.ApplyOpsRequest) (contract.ApplyOpsResponse, error) {
	s.applyOpsCalled = true
	s.applyOpsRequest = req
	if s.applyOpsErr != nil {
		return contract.ApplyOpsResponse{}, s.applyOpsErr
	}
	return s.applyOpsResult, nil
}

func (s *stubDashboardOrchestration) ListRuns(_ context.Context, req contract.ListRunsRequest) (contract.ListRunsResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.listRunsRequest = req
	s.listRunsRequests = append(s.listRunsRequests, req)
	if s.listRunsErr != nil {
		return contract.ListRunsResponse{}, s.listRunsErr
	}
	if s.listRunsByDAG != nil {
		return s.listRunsByDAG[req.DagKey], nil
	}
	return s.listRunsResult, nil
}

var errDashboardStub = errors.New("dashboard stub error")

func (s *stubDashboardOrchestration) DispatchNode(_ context.Context, req contract.DispatchNodeRequest) (contract.DispatchNodeResponse, error) {
	s.dispatchNodeRequest = req
	if s.dispatchNodeErr != nil {
		return contract.DispatchNodeResponse{}, s.dispatchNodeErr
	}
	return s.dispatchNodeResult, nil
}
