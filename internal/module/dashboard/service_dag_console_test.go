package dashboard

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
)

func TestGetDashboardPageDAGsIncludeLatestRunAndFinalOutputMarker(t *testing.T) {
	t.Parallel()

	orchestration := newDAGConsoleDashboardStub()
	svc := &service{dagRuntime: orchestration}

	got, err := svc.GetDashboardPage(context.Background(), "dags")
	if err != nil {
		t.Fatalf("GetDashboardPage(dags) error = %v", err)
	}
	dag := requireSingleDashboardDAG(t, got)
	requireDAGConsoleSummary(t, dag)
	requireDAGConsoleLatestRun(t, dag)
	requireDAGConsoleRunLookup(t, orchestration)
}

func TestGetDashboardPageDAGsKeepOrderWithPerDAGLatestRuns(t *testing.T) {
	t.Parallel()

	orchestration := newMultiDAGConsoleDashboardStub()
	svc := &service{dagRuntime: orchestration}

	got, err := svc.GetDashboardPage(context.Background(), "dags")
	if err != nil {
		t.Fatalf("GetDashboardPage(dags) error = %v", err)
	}
	requireDAGConsoleOrder(t, got, []string{"dag-a", "dag-b"}, []string{"run-a", "run-b"})
	requireDAGConsoleLookupCount(t, orchestration, 2)
}

func newDAGConsoleDashboardStub() *stubDashboardOrchestration {
	return &stubDashboardOrchestration{
		listDAGsResult: []contract.DAGSummary{{
			DagKey:   "dag-1",
			Title:    "Dag One",
			Status:   "running",
			Trigger:  "scheduled",
			CronExpr: "0 8 * * *",
		}},
		listRunsResult: contract.ListRunsResponse{Runs: []contract.Run{{
			RunKey:   "run-1",
			DagKey:   "dag-1",
			Status:   "succeeded",
			Metadata: json.RawMessage(`{"final_output":{"kind":"text","text":"ready"}}`),
		}}},
	}
}

func newMultiDAGConsoleDashboardStub() *stubDashboardOrchestration {
	return &stubDashboardOrchestration{
		listDAGsResult: []contract.DAGSummary{
			{DagKey: "dag-a", Title: "Dag A"},
			{DagKey: "dag-b", Title: "Dag B"},
		},
		listRunsByDAG: map[string]contract.ListRunsResponse{
			"dag-a": {Runs: []contract.Run{{RunKey: "run-a", DagKey: "dag-a", Status: "succeeded"}}},
			"dag-b": {Runs: []contract.Run{{RunKey: "run-b", DagKey: "dag-b", Status: "failed"}}},
		},
	}
}

func requireSingleDashboardDAG(t *testing.T, got *DashboardPage) DashboardDAG {
	t.Helper()
	if got == nil || len(got.DAGs) != 1 {
		t.Fatalf("GetDashboardPage(dags) = %#v", got)
	}
	return got.DAGs[0]
}

func requireDAGConsoleOrder(t *testing.T, got *DashboardPage, wantDAGs, wantRuns []string) {
	t.Helper()
	if got == nil || len(got.DAGs) != len(wantDAGs) {
		t.Fatalf("GetDashboardPage(dags).DAGs = %#v, want %d rows", got, len(wantDAGs))
	}
	for index, wantDAG := range wantDAGs {
		requireDAGConsoleRow(t, got.DAGs[index], wantDAG, wantRuns[index])
	}
}

func requireDAGConsoleRow(t *testing.T, got DashboardDAG, wantDAG, wantRun string) {
	t.Helper()
	if got.DagKey != wantDAG || got.LatestRun == nil || got.LatestRun.RunKey != wantRun {
		t.Fatalf("DAG row = %#v, want %s/%s", got, wantDAG, wantRun)
	}
}

func requireDAGConsoleLookupCount(t *testing.T, orchestration *stubDashboardOrchestration, want int) {
	t.Helper()
	if len(orchestration.listRunsRequests) != want {
		t.Fatalf("ListRuns requests = %#v, want %d latest-run lookups", orchestration.listRunsRequests, want)
	}
}

func requireDAGConsoleSummary(t *testing.T, dag DashboardDAG) {
	t.Helper()
	if dag.Trigger != "scheduled" || dag.CronExpr != "0 8 * * *" {
		t.Fatalf("DAG schedule = trigger %q cron %q, want scheduled cron", dag.Trigger, dag.CronExpr)
	}
}

func requireDAGConsoleLatestRun(t *testing.T, dag DashboardDAG) {
	t.Helper()
	if dag.LatestRun == nil || dag.LatestRun.RunKey != "run-1" || dag.LatestRun.Status != "succeeded" {
		t.Fatalf("DAG latest run = %#v, want run-1 succeeded", dag.LatestRun)
	}
	if !dag.HasFinalOutput {
		t.Fatalf("DAG HasFinalOutput = false, want true")
	}
}

func requireDAGConsoleRunLookup(t *testing.T, orchestration *stubDashboardOrchestration) {
	t.Helper()
	if orchestration.listRunsRequest.DagKey != "dag-1" || orchestration.listRunsRequest.Limit != 1 {
		t.Fatalf("ListRuns request = %#v", orchestration.listRunsRequest)
	}
}

func TestDashboardDAGDetailRowsPreserveInvalidJSONColumnsAsStrings(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 2, 3, 0, 0, 0, time.UTC)
	dag, err := dashboardDAGSummaryFromRow(map[string]any{
		"id":         int64(1),
		"dag_key":    "dag-1",
		"version":    int64(1),
		"created_at": now,
		"updated_at": now,
		"metadata":   "Running",
	})
	if err != nil {
		t.Fatalf("dashboardDAGSummaryFromRow() error = %v", err)
	}
	node, err := dashboardDAGNodeFromRow(map[string]any{
		"id":         int64(2),
		"dag_key":    "dag-1",
		"node_key":   "node-1",
		"created_at": now,
		"updated_at": now,
		"config":     "Running",
		"result":     "Running",
	})
	if err != nil {
		t.Fatalf("dashboardDAGNodeFromRow() error = %v", err)
	}
	if string(dag.Metadata) != `"Running"` || string(node.Config) != `"Running"` || string(node.Result) != `"Running"` {
		t.Fatalf("invalid detail JSON = metadata %q config %q result %q, want quoted raw string", dag.Metadata, node.Config, node.Result)
	}
	if _, err := json.Marshal(contract.DAGDetail{DAG: dag, Nodes: []contract.DAGNode{node}}); err != nil {
		t.Fatalf("marshal DAG detail with invalid source JSON = %v", err)
	}
}

func TestDashboardRunFromRowPreservesInvalidJSONColumnsAsStrings(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 2, 3, 0, 0, 0, time.UTC)
	run, err := dashboardRunFromRow(map[string]any{
		"id":                   int64(1),
		"dag_version_snapshot": int64(1),
		"budget_used":          int64(0),
		"run_key":              "run-1",
		"dag_key":              "dag-1",
		"started_at":           now,
		"created_at":           now,
		"updated_at":           now,
		"events":               "Running",
		"metadata":             "Running",
	})
	if err != nil {
		t.Fatalf("dashboardRunFromRow() error = %v", err)
	}
	if string(run.Events) != `"Running"` || string(run.Metadata) != `"Running"` {
		t.Fatalf("dashboardRunFromRow invalid JSON = events %q metadata %q, want quoted raw string", run.Events, run.Metadata)
	}
	if !json.Valid(run.Events) || !json.Valid(run.Metadata) {
		t.Fatalf("dashboardRunFromRow returned invalid JSON: events=%q metadata=%q", run.Events, run.Metadata)
	}
}

func TestRunMetadataHasFinalOutputRejectsEmptyValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		raw  json.RawMessage
		want bool
	}{
		{name: "missing", raw: json.RawMessage(`{}`), want: false},
		{name: "null", raw: json.RawMessage(`{"final_output":null}`), want: false},
		{name: "empty object", raw: json.RawMessage(`{"final_output":{}}`), want: false},
		{name: "empty array", raw: json.RawMessage(`{"final_output":[]}`), want: false},
		{name: "empty string", raw: json.RawMessage(`{"final_output":""}`), want: false},
		{name: "file output", raw: json.RawMessage(`{"final_output":{"kind":"file","path":"reports/daily.pptx"}}`), want: true},
		{name: "text output", raw: json.RawMessage(`{"final_output":{"kind":"text","text":"ready"}}`), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := runMetadataHasFinalOutput(tt.raw); got != tt.want {
				t.Fatalf("runMetadataHasFinalOutput(%s) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}
