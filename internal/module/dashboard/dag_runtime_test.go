package dashboard

import (
	"context"
	"testing"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestGetDashboardPageLoadsDAGsFromDAGRuntimeWithoutOrchestration(t *testing.T) {
	t.Parallel()

	dagRuntime := newDAGRuntimeOnlyStub()
	svc := &service{dagRuntime: dagRuntime}

	got, err := svc.GetDashboardPage(context.Background(), "dags")
	if err != nil {
		t.Fatalf("GetDashboardPage(dags) error = %v", err)
	}
	assertDashboardPageDAG(t, got, "dag-1")
	assertDAGRuntimeListFilter(t, dagRuntime, contract.ListDAGsFilter{Limit: dashboardPageDefaultLimit})
}

func TestDAGRuntimeOnlyListDAGs(t *testing.T) {
	t.Parallel()

	dagRuntime := newDAGRuntimeOnlyStub()
	svc := &service{dagRuntime: dagRuntime}

	dags, err := svc.ListDAGs(context.Background(), contract.ListDAGsFilter{Keyword: " one ", Status: " running ", Limit: 7})
	if err != nil {
		t.Fatalf("ListDAGs() error = %v", err)
	}
	assertDAGList(t, dags, "dag-1")
	assertDAGRuntimeListFilter(t, dagRuntime, contract.ListDAGsFilter{Keyword: "one", Status: "running", Limit: 7})
}

func TestDAGRuntimeOnlyGetDAGDetail(t *testing.T) {
	t.Parallel()

	dagRuntime := newDAGRuntimeOnlyStub()
	svc := &service{dagRuntime: dagRuntime}

	detail, err := svc.GetDAGDetail(context.Background(), " dag-1 ")
	if err != nil {
		t.Fatalf("GetDAGDetail() error = %v", err)
	}
	if detail.DAG.DagKey != "dag-1" || len(detail.Nodes) != 1 {
		t.Fatalf("GetDAGDetail() = %#v", detail)
	}
}

func TestDAGRuntimeOnlyListDAGRuns(t *testing.T) {
	t.Parallel()

	dagRuntime := newDAGRuntimeOnlyStub()
	svc := &service{dagRuntime: dagRuntime}

	runs, err := svc.ListDAGRuns(context.Background(), " dag-1 ", " running ", 3)
	if err != nil {
		t.Fatalf("ListDAGRuns() error = %v", err)
	}
	if dagRuntime.listRunsRequest.Status != "running" {
		t.Fatalf("ListDAGRuns() status = %#v, want running", dagRuntime.listRunsRequest)
	}
	if len(runs) != 1 || runs[0].RunKey != "run-1" {
		t.Fatalf("ListDAGRuns() = %#v", runs)
	}
}

func TestDAGRuntimeOnlyStartDAG(t *testing.T) {
	t.Parallel()

	dagRuntime := newDAGRuntimeOnlyStub()
	svc := &service{dagRuntime: dagRuntime}

	started, err := svc.StartDAG(context.Background(), " dag-1 ", "", " ui-1 ")
	if err != nil {
		t.Fatalf("StartDAG() error = %v", err)
	}
	if started.RunKey != "run-2" || started.Version != 8 {
		t.Fatalf("StartDAG() = %#v", started)
	}
	want := contract.StartDAGRequest{DagKey: "dag-1", TriggerSource: "manual", IdempotencyKey: "ui-1"}
	if dagRuntime.startDAGRequest != want {
		t.Fatalf("StartDAG() request = %#v", dagRuntime.startDAGRequest)
	}
}

func TestDAGRuntimeOnlyDeleteDAG(t *testing.T) {
	t.Parallel()

	dagRuntime := newDAGRuntimeOnlyStub()
	svc := &service{dagRuntime: dagRuntime}

	if err := svc.DeleteDAG(context.Background(), " dag-1 "); err != nil {
		t.Fatalf("DeleteDAG() error = %v", err)
	}
	if dagRuntime.deleteDAGRequest != (contract.DeleteDAGRequest{DagKey: "dag-1"}) {
		t.Fatalf("DeleteDAG() request = %#v", dagRuntime.deleteDAGRequest)
	}
}

func newDAGRuntimeOnlyStub() *stubDashboardOrchestration {
	return &stubDashboardOrchestration{
		listDAGsResult: []contract.DAGSummary{{DagKey: "dag-1", Title: "Dag One", Status: "running"}},
		dagDetail: contract.DAGDetail{
			DAG:   contract.DAGSummary{DagKey: "dag-1", Title: "Dag One", Status: "running"},
			Nodes: []contract.DAGNode{{DagKey: "dag-1", NodeKey: "n1", Status: "done"}},
		},
		listRunsResult: contract.ListRunsResponse{
			Runs: []contract.Run{{RunKey: "run-1", DagKey: "dag-1", Status: "succeeded"}},
		},
		startDAGResult: contract.StartDAGResponse{RunKey: "run-2", Version: 8},
	}
}

func assertDashboardPageDAG(t *testing.T, got *DashboardPage, wantKey string) {
	t.Helper()
	if got == nil || len(got.DAGs) != 1 || got.DAGs[0].DagKey != wantKey {
		t.Fatalf("GetDashboardPage(dags) = %#v", got)
	}
}

func assertDAGList(t *testing.T, dags []contract.DAGSummary, wantKey string) {
	t.Helper()
	if len(dags) != 1 || dags[0].DagKey != wantKey {
		t.Fatalf("ListDAGs() = %#v", dags)
	}
}

func assertDAGRuntimeListFilter(t *testing.T, runtime *stubDashboardOrchestration, want contract.ListDAGsFilter) {
	t.Helper()
	if runtime.listDAGsFilter != want {
		t.Fatalf("ListDAGs() filter = %#v", runtime.listDAGsFilter)
	}
}
