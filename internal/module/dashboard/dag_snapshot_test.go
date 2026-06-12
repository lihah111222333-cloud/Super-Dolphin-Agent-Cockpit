package dashboard

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
)

func TestGetDashboardPageDAGsUsesSnapshotQueriesWithoutOrchPeer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	db := &stubDashboardQueryStore{responses: []stubDashboardQueryResponse{
		{contains: []string{"FROM task_dags"}, rows: []map[string]any{dashboardDAGRow(now)}},
		{contains: []string{"FROM task_dag_runs", "WHERE dag_key"}, rows: []map[string]any{dashboardRunRow(now)}},
	}}
	orchestration := &stubDashboardOrchestration{listDAGsErr: errDashboardStub, listRunsErr: errDashboardStub}
	svc := &service{dbQueries: db, dagRuntime: orchestration}

	got, err := svc.GetDashboardPage(context.Background(), "dags")
	if err != nil {
		t.Fatalf("GetDashboardPage(dags) error = %v", err)
	}
	if got == nil || len(got.DAGs) != 1 || got.DAGs[0].DagKey != "daily-brief" {
		t.Fatalf("GetDashboardPage(dags) = %#v", got)
	}
	if got.DAGs[0].LatestRun == nil || got.DAGs[0].LatestRun.RunKey != "daily-brief#run-1" {
		t.Fatalf("LatestRun = %#v", got.DAGs[0].LatestRun)
	}
	if orchestration.listDAGsFilter.Limit != 0 || len(orchestration.listRunsRequests) != 0 {
		t.Fatalf("orchestration should not be called, list filter=%#v runs=%#v", orchestration.listDAGsFilter, orchestration.listRunsRequests)
	}
}

func TestGetDashboardPageDAGsBatchesLatestRunsFromSnapshot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 4, 9, 0, 0, 0, time.UTC)
	db := &stubDashboardQueryStore{responses: []stubDashboardQueryResponse{
		{
			contains: []string{"FROM task_dags", "ORDER BY updated_at"},
			rows: []map[string]any{
				dashboardDAGRowWithKey(now, "dag-a"),
				dashboardDAGRowWithKey(now, "dag-b"),
				dashboardDAGRowWithKey(now, "dag-c"),
			},
		},
		{
			contains: []string{"FROM task_dag_runs", "ROW_NUMBER() OVER", "dag_key IN ($1, $2, $3)"},
			rows: []map[string]any{
				dashboardRunRowWithKey(now, "dag-b", "run-b", []byte(`{"final_output":{"kind":"file","path":"reports/b.md"}}`)),
				dashboardRunRowWithKey(now, "dag-a", "run-a", []byte(`{}`)),
			},
		},
	}}
	orchestration := &stubDashboardOrchestration{listDAGsErr: errDashboardStub, listRunsErr: errDashboardStub}
	svc := &service{dbQueries: db, dagRuntime: orchestration}

	got, err := svc.GetDashboardPage(context.Background(), "dags")
	if err != nil {
		t.Fatalf("GetDashboardPage(dags) error = %v", err)
	}
	requireSnapshotDashboardDAGOrder(t, got, []string{"dag-a", "dag-b", "dag-c"})
	requireSnapshotLatestRun(t, got.DAGs[0], "run-a", false)
	requireSnapshotLatestRun(t, got.DAGs[1], "run-b", true)
	if got.DAGs[2].LatestRun != nil || got.DAGs[2].HasFinalOutput {
		t.Fatalf("dag-c latest run = %#v final=%v, want no run and no final output", got.DAGs[2].LatestRun, got.DAGs[2].HasFinalOutput)
	}
	if len(db.calls) != 2 {
		t.Fatalf("snapshot query calls = %#v, want list DAGs + single batch latest-runs query", db.calls)
	}
	if strings.Join(anyStrings(db.calls[1].args[:3]), ",") != "dag-a,dag-b,dag-c" {
		t.Fatalf("latest-runs batch dag keys args = %#v, want dag-a,dag-b,dag-c", db.calls[1].args)
	}
	if len(orchestration.listRunsRequests) != 0 {
		t.Fatalf("orchestration ListRuns should not be called, got %#v", orchestration.listRunsRequests)
	}
}

func TestLatestRunsByDAGSnapshotQueryPassesDBQueryValidation(t *testing.T) {
	t.Parallel()

	db := newDashboardQuerySQLiteDB(t)
	svc := &service{dbQueries: dbquery.NewQueryStore(db, time.Second)}

	got, err := svc.listLatestDAGRunsByDAGFromSnapshot(context.Background(), []string{"dag-a", "dag-b"})
	if err != nil {
		t.Fatalf("listLatestDAGRunsByDAGFromSnapshot() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("listLatestDAGRunsByDAGFromSnapshot() = %#v, want no rows", got)
	}
}

func TestGetDAGDetailUsesSnapshotQueriesWithoutOrchPeer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	svc, orchestration := newDashboardSnapshotServiceForTest(now)

	detail, err := svc.GetDAGDetail(context.Background(), " daily-brief ")
	if err != nil {
		t.Fatalf("GetDAGDetail() error = %v", err)
	}
	if detail == nil || detail.DAG.DagKey != "daily-brief" || len(detail.Nodes) != 1 {
		t.Fatalf("GetDAGDetail() = %#v", detail)
	}
	if orchestration.getDAGKey != "" {
		t.Fatalf("orchestration GetDAG should not be called, got %q", orchestration.getDAGKey)
	}
}

func TestListDAGRunsUsesSnapshotQueriesWithoutOrchPeer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	svc, orchestration := newDashboardSnapshotServiceForTest(now)

	runs, err := svc.ListDAGRuns(context.Background(), "daily-brief", "running", 5)
	if err != nil {
		t.Fatalf("ListDAGRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].RunKey != "daily-brief#run-1" {
		t.Fatalf("ListDAGRuns() = %#v", runs)
	}
	if len(orchestration.listRunsRequests) != 0 {
		t.Fatalf("orchestration ListRuns should not be called, got %#v", orchestration.listRunsRequests)
	}
}

func TestGetDAGRunUsesSnapshotQueriesWithoutOrchPeer(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 5, 31, 10, 0, 0, 0, time.UTC)
	svc, orchestration := newDashboardSnapshotServiceForTest(now)

	run, err := svc.GetDAGRun(context.Background(), "daily-brief#run-1")
	if err != nil {
		t.Fatalf("GetDAGRun() error = %v", err)
	}
	if run.Run.RunKey != "daily-brief#run-1" || len(run.Nodes) != 1 {
		t.Fatalf("GetDAGRun() = %#v", run)
	}
	if orchestration.getRunRequest.RunKey != "" {
		t.Fatalf("orchestration GetRun should not be called, got %#v", orchestration.getRunRequest)
	}
}

func newDashboardSnapshotServiceForTest(now time.Time) (*service, *stubDashboardOrchestration) {
	db := &stubDashboardQueryStore{responses: []stubDashboardQueryResponse{
		{contains: []string{"FROM task_dags", "WHERE dag_key"}, rows: []map[string]any{dashboardDAGRow(now)}},
		{contains: []string{"FROM task_dag_nodes", "run_id IS NULL"}, rows: []map[string]any{dashboardNodeRow(now, nil)}},
		{contains: []string{"FROM task_dag_runs", "WHERE dag_key"}, rows: []map[string]any{dashboardRunRow(now)}},
		{contains: []string{"FROM task_dag_runs", "WHERE run_key"}, rows: []map[string]any{dashboardRunRow(now)}},
		{contains: []string{"FROM task_dag_nodes", "run_id = $2"}, rows: []map[string]any{dashboardNodeRow(now, int64PtrForDashboardTest(7))}},
	}}
	orchestration := &stubDashboardOrchestration{listDAGsErr: errDashboardStub, listRunsErr: errDashboardStub}
	return &service{dbQueries: db, dagRuntime: orchestration}, orchestration
}

type stubDashboardQueryStore struct {
	dbquery.Store
	mu        sync.Mutex
	responses []stubDashboardQueryResponse
	calls     []stubDashboardQueryCall
}

type stubDashboardQueryResponse struct {
	contains []string
	rows     []map[string]any
	err      error
}

type stubDashboardQueryCall struct {
	query string
	args  []any
}

func (s *stubDashboardQueryStore) Query(_ context.Context, query string, args ...any) ([]map[string]any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls = append(s.calls, stubDashboardQueryCall{query: query, args: append([]any(nil), args...)})
	for _, response := range s.responses {
		if dashboardQueryContainsAll(query, response.contains) {
			return cloneDashboardQueryRows(response.rows), response.err
		}
	}
	return nil, fmt.Errorf("unexpected dashboard query: %s", query)
}

func dashboardQueryContainsAll(query string, needles []string) bool {
	for _, needle := range needles {
		if !strings.Contains(query, needle) {
			return false
		}
	}
	return true
}

func cloneDashboardQueryRows(rows []map[string]any) []map[string]any {
	out := make([]map[string]any, len(rows))
	for i, row := range rows {
		clone := make(map[string]any, len(row))
		for key, value := range row {
			clone[key] = value
		}
		out[i] = clone
	}
	return out
}

func anyStrings(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, fmt.Sprint(value))
	}
	return out
}

func requireSnapshotDashboardDAGOrder(t *testing.T, got *DashboardPage, want []string) {
	t.Helper()
	if got == nil || len(got.DAGs) != len(want) {
		t.Fatalf("GetDashboardPage(dags).DAGs = %#v, want %d rows", got, len(want))
	}
	for index, wantKey := range want {
		if got.DAGs[index].DagKey != wantKey {
			t.Fatalf("DAG row %d key = %q, want %q in order %#v", index, got.DAGs[index].DagKey, wantKey, want)
		}
	}
}

func requireSnapshotLatestRun(t *testing.T, got DashboardDAG, wantRun string, wantFinal bool) {
	t.Helper()
	if got.LatestRun == nil || got.LatestRun.RunKey != wantRun {
		t.Fatalf("DAG %q latest run = %#v, want %q", got.DagKey, got.LatestRun, wantRun)
	}
	if got.HasFinalOutput != wantFinal {
		t.Fatalf("DAG %q HasFinalOutput = %v, want %v", got.DagKey, got.HasFinalOutput, wantFinal)
	}
}

func dashboardDAGRow(now time.Time) map[string]any {
	return map[string]any{
		"id":          int64(11),
		"dag_key":     "daily-brief",
		"version":     int64(3),
		"title":       "每日简报",
		"description": "自动整理日报",
		"status":      "ready",
		"created_by":  "user",
		"metadata":    []byte(`{"final_node_key":"report"}`),
		"trigger":     "scheduled",
		"cron_expr":   "0 9 * * *",
		"next_run_at": now.Add(time.Hour),
		"started_at":  nil,
		"finished_at": nil,
		"created_at":  now.Add(-time.Hour),
		"updated_at":  now,
	}
}

func dashboardDAGRowWithKey(now time.Time, dagKey string) map[string]any {
	row := dashboardDAGRow(now)
	row["dag_key"] = dagKey
	row["title"] = strings.ToUpper(dagKey)
	return row
}

func dashboardRunRow(now time.Time) map[string]any {
	return map[string]any{
		"id":                   int64(7),
		"run_key":              "daily-brief#run-1",
		"dag_key":              "daily-brief",
		"dag_version_snapshot": int64(3),
		"trigger_source":       "manual",
		"status":               "running",
		"started_at":           now.Add(-time.Minute),
		"finished_at":          nil,
		"events":               []byte(`[]`),
		"budget_used":          int64(0),
		"budget_limit":         nil,
		"metadata":             []byte(`{"final_output":{"kind":"file","path":"reports/daily.md"}}`),
		"created_at":           now.Add(-time.Minute),
		"updated_at":           now,
	}
}

func dashboardRunRowWithKey(now time.Time, dagKey, runKey string, metadata []byte) map[string]any {
	row := dashboardRunRow(now)
	row["run_key"] = runKey
	row["dag_key"] = dagKey
	row["metadata"] = metadata
	return row
}

func dashboardNodeRow(now time.Time, runID *int64) map[string]any {
	return map[string]any{
		"id":                 int64(21),
		"dag_key":            "daily-brief",
		"node_key":           "report",
		"title":              "输出简报",
		"node_type":          "agent",
		"assigned_to":        "codex",
		"depends_on":         []byte(`[]`),
		"status":             "running",
		"command_ref":        "",
		"config":             []byte(`{"model":"gpt-5"}`),
		"result":             nil,
		"run_id":             runID,
		"started_at":         now.Add(-time.Minute),
		"finished_at":        nil,
		"created_at":         now.Add(-time.Hour),
		"updated_at":         now,
		"active_turn_id":     nil,
		"active_wakeup_id":   nil,
		"last_event_at":      now,
		"spawning_thread_id": nil,
	}
}

func int64PtrForDashboardTest(value int64) *int64 {
	return &value
}

var _ dbquery.Store = (*stubDashboardQueryStore)(nil)
var _ contract.DAGRuntime = (*stubDashboardOrchestration)(nil)
