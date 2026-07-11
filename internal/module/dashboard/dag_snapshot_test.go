package dashboard

import (
	"context"
	"database/sql"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/contract"
	"github.com/lihah111222333-cloud/super-dolphin-agent/internal/store/dbquery"
	_ "modernc.org/sqlite"
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

func TestDashboardDAGSnapshotListQueryCountDoesNotScaleWithPageSize(t *testing.T) {
	t.Parallel()

	for _, size := range []int{1, 25, dashboardPageDefaultLimit} {
		t.Run(fmt.Sprintf("page-%d", size), func(t *testing.T) {
			t.Parallel()

			now := time.Date(2026, 6, 7, 12, 0, 0, 0, time.UTC)
			rows := make([]map[string]any, 0, size)
			for i := range size {
				rows = append(rows, dashboardDAGRowWithKey(now, fmt.Sprintf("dag-%03d", i)))
			}
			db := &stubDashboardQueryStore{responses: []stubDashboardQueryResponse{
				{contains: []string{"FROM task_dags", "ORDER BY updated_at"}, rows: rows},
				{contains: []string{"FROM task_dag_runs", "ROW_NUMBER() OVER"}, rows: []map[string]any{}},
			}}
			svc := &service{
				dbQueries:  db,
				dagRuntime: &stubDashboardOrchestration{listDAGsErr: errDashboardStub, listRunsErr: errDashboardStub},
			}

			got, err := svc.GetDashboardPage(context.Background(), "dags")
			if err != nil {
				t.Fatalf("GetDashboardPage(dags) error = %v", err)
			}
			if got == nil || len(got.DAGs) != size {
				t.Fatalf("GetDashboardPage(dags).DAGs len = %d, want %d", len(got.DAGs), size)
			}
			if len(db.calls) != 2 {
				t.Fatalf("snapshot query calls for page size %d = %d (%#v), want exactly list + batched latest-runs", size, len(db.calls), db.calls)
			}
		})
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

func TestDashboardDAGSnapshotParsesSQLiteIntegerTimestamps(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 14, 14, 30, 50, 123_000_000, time.UTC)
	svc := newDashboardSnapshotSQLiteMillisService(now)

	page, err := svc.GetDashboardPage(context.Background(), "dags")
	requireNoDashboardSnapshotError(t, "GetDashboardPage(dags)", err)
	dag := requireSingleSnapshotPageDAG(t, page)
	requireSnapshotDAGSQLiteTimestamps(t, dag, now, true)

	detail, err := svc.GetDAGDetail(context.Background(), "daily-brief")
	requireNoDashboardSnapshotError(t, "GetDAGDetail()", err)
	requireSnapshotDetailNodeSQLiteTimestamps(t, detail, now, true, "SQLite millis node timestamps")

	runs, err := svc.ListDAGRuns(context.Background(), "daily-brief", "", 5)
	requireNoDashboardSnapshotError(t, "ListDAGRuns()", err)
	requireSnapshotListedRunSQLiteTimestamps(t, runs, now, "SQLite millis run timestamp")

	run, err := svc.GetDAGRun(context.Background(), "daily-brief#run-1")
	requireNoDashboardSnapshotError(t, "GetDAGRun()", err)
	requireSnapshotGetRunSQLiteTimestamps(t, run, now, "run node with SQLite millis timestamps")
}

func TestDashboardDAGSnapshotReadsSQLiteIntegerTimestampsThroughDBQuery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 14, 14, 30, 50, 123_000_000, time.UTC)
	db := newDashboardSnapshotSQLiteIntegerDB(t, now)
	svc := &service{dbQueries: dbquery.NewQueryStore(db, time.Second)}

	page, err := svc.GetDashboardPage(context.Background(), "dags")
	requireNoDashboardSnapshotError(t, "GetDashboardPage(dags)", err)
	dag := requireSingleSnapshotPageDAG(t, page)
	requireSnapshotDAGSQLiteTimestamps(t, dag, now, false)

	detail, err := svc.GetDAGDetail(context.Background(), "daily-brief")
	requireNoDashboardSnapshotError(t, "GetDAGDetail()", err)
	requireSnapshotDetailNodeSQLiteTimestamps(t, detail, now, false, "SQLite integer node timestamps")

	runs, err := svc.ListDAGRuns(context.Background(), "daily-brief", "", 5)
	requireNoDashboardSnapshotError(t, "ListDAGRuns()", err)
	requireSnapshotListedRunSQLiteTimestamps(t, runs, now, "SQLite integer run timestamp")

	run, err := svc.GetDAGRun(context.Background(), "daily-brief#run-1")
	requireNoDashboardSnapshotError(t, "GetDAGRun()", err)
	requireSnapshotGetRunSQLiteTimestamps(t, run, now, "run node with SQLite integer timestamps")
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
		maps.Copy(clone, row)
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

func newDashboardSnapshotSQLiteMillisService(now time.Time) *service {
	db := &stubDashboardQueryStore{responses: []stubDashboardQueryResponse{
		{contains: []string{"FROM task_dags", "ORDER BY updated_at"}, rows: []map[string]any{dashboardDAGRowSQLiteMillis(now)}},
		{contains: []string{"FROM task_dag_runs", "ROW_NUMBER() OVER"}, rows: []map[string]any{dashboardRunRowSQLiteMillis(now)}},
		{contains: []string{"FROM task_dags", "WHERE dag_key"}, rows: []map[string]any{dashboardDAGRowSQLiteMillis(now)}},
		{contains: []string{"FROM task_dag_nodes", "run_id IS NULL"}, rows: []map[string]any{dashboardNodeRowSQLiteMillis(now, nil)}},
		{contains: []string{"FROM task_dag_runs", "WHERE dag_key"}, rows: []map[string]any{dashboardRunRowSQLiteMillis(now)}},
		{contains: []string{"FROM task_dag_runs", "WHERE run_key"}, rows: []map[string]any{dashboardRunRowSQLiteMillis(now)}},
		{contains: []string{"FROM task_dag_nodes", "run_id = $2"}, rows: []map[string]any{dashboardNodeRowSQLiteMillis(now, int64PtrForDashboardTest(7))}},
	}}
	return &service{
		dbQueries:  db,
		dagRuntime: &stubDashboardOrchestration{listDAGsErr: errDashboardStub, listRunsErr: errDashboardStub},
	}
}

func requireNoDashboardSnapshotError(t *testing.T, op string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s error = %v", op, err)
	}
}

func requireSingleSnapshotPageDAG(t *testing.T, page *DashboardPage) DashboardDAG {
	t.Helper()
	if page == nil {
		t.Fatalf("GetDashboardPage(dags) = nil, want page with 1 DAG")
	}
	if len(page.DAGs) != 1 {
		t.Fatalf("GetDashboardPage(dags).DAGs len = %d, want 1", len(page.DAGs))
	}
	return page.DAGs[0]
}

func requireSnapshotDAGSQLiteTimestamps(t *testing.T, got DashboardDAG, now time.Time, wantStartedAt bool) {
	t.Helper()
	requireTimeEqual(t, "DAG CreatedAt", got.CreatedAt, now.Add(-time.Hour))
	requireTimePtrEqual(t, "DAG NextRunAt", got.NextRunAt, now.Add(time.Hour))
	if wantStartedAt {
		requireTimePtrEqual(t, "DAG StartedAt", got.StartedAt, now.Add(-30*time.Minute))
	}
	requireTimePtrEqual(t, "DAG FinishedAt", got.FinishedAt, now.Add(-10*time.Minute))
	requireSnapshotLatestRun(t, got, "daily-brief#run-1", true)
	requireTimePtrEqual(t, "LatestRun FinishedAt", got.LatestRun.FinishedAt, now.Add(-30*time.Second))
}

func requireSnapshotDetailNodeSQLiteTimestamps(
	t *testing.T,
	detail *contract.DAGDetail,
	now time.Time,
	wantLastEvent bool,
	timestampKind string,
) {
	t.Helper()
	node := requireSingleSnapshotDetailNode(t, detail, timestampKind)
	requireTimeEqual(t, "Node CreatedAt", node.CreatedAt, now.Add(-time.Hour))
	if wantLastEvent {
		requireTimePtrEqual(t, "Node LastEventAt", node.LastEventAt, now)
	}
	requireTimePtrEqual(t, "Node FinishedAt", node.FinishedAt, now.Add(-30*time.Second))
}

func requireSingleSnapshotDetailNode(t *testing.T, detail *contract.DAGDetail, timestampKind string) contract.DAGNode {
	t.Helper()
	if detail == nil {
		t.Fatalf("GetDAGDetail().Nodes = <nil>, want %s", timestampKind)
	}
	if len(detail.Nodes) != 1 {
		t.Fatalf("GetDAGDetail().Nodes = %#v, want %s", detail.Nodes, timestampKind)
	}
	return detail.Nodes[0]
}

func requireSnapshotListedRunSQLiteTimestamps(
	t *testing.T,
	runs []contract.Run,
	now time.Time,
	timestampKind string,
) {
	t.Helper()
	if len(runs) != 1 {
		t.Fatalf("ListDAGRuns() = %#v, want %s", runs, timestampKind)
	}
	requireTimeEqual(t, "ListDAGRuns()[0].StartedAt", runs[0].StartedAt, now.Add(-time.Minute))
	requireTimePtrEqual(t, "ListDAGRuns()[0].FinishedAt", runs[0].FinishedAt, now.Add(-30*time.Second))
}

func requireSnapshotGetRunSQLiteTimestamps(
	t *testing.T,
	run contract.GetRunResponse,
	now time.Time,
	nodeKind string,
) {
	t.Helper()
	requireTimeEqual(t, "GetDAGRun().Run.CreatedAt", run.Run.CreatedAt, now.Add(-time.Minute))
	requireTimePtrEqual(t, "GetDAGRun().Run.FinishedAt", run.Run.FinishedAt, now.Add(-30*time.Second))
	node := requireSingleSnapshotGetRunNode(t, run, nodeKind)
	requireTimePtrEqual(t, "GetDAGRun().Nodes[0].LastEventAt", node.LastEventAt, now)
	requireTimePtrEqual(t, "GetDAGRun().Nodes[0].FinishedAt", node.FinishedAt, now.Add(-30*time.Second))
}

func requireSingleSnapshotGetRunNode(t *testing.T, run contract.GetRunResponse, nodeKind string) contract.DAGNode {
	t.Helper()
	if len(run.Nodes) != 1 {
		t.Fatalf("GetDAGRun().Nodes = %#v, want %s", run.Nodes, nodeKind)
	}
	return run.Nodes[0]
}

func requireTimeEqual(t *testing.T, label string, got, want time.Time) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("%s = %s, want %s", label, got, want)
	}
}

func requireTimePtrEqual(t *testing.T, label string, got *time.Time, want time.Time) {
	t.Helper()
	if got == nil || !got.Equal(want) {
		t.Fatalf("%s = %#v, want %s", label, got, want)
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

func dashboardDAGRowSQLiteMillis(now time.Time) map[string]any {
	row := dashboardDAGRow(now)
	row["next_run_at"] = now.Add(time.Hour).UnixMilli()
	row["started_at"] = now.Add(-30 * time.Minute).UnixMilli()
	row["finished_at"] = now.Add(-10 * time.Minute).UnixMilli()
	row["created_at"] = now.Add(-time.Hour).UnixMilli()
	row["updated_at"] = now.UnixMilli()
	return row
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

func dashboardRunRowSQLiteMillis(now time.Time) map[string]any {
	row := dashboardRunRow(now)
	row["started_at"] = now.Add(-time.Minute).UnixMilli()
	row["finished_at"] = now.Add(-30 * time.Second).UnixMilli()
	row["created_at"] = now.Add(-time.Minute).UnixMilli()
	row["updated_at"] = now.UnixMilli()
	return row
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

func dashboardNodeRowSQLiteMillis(now time.Time, runID *int64) map[string]any {
	row := dashboardNodeRow(now, runID)
	row["started_at"] = now.Add(-time.Minute).UnixMilli()
	row["finished_at"] = now.Add(-30 * time.Second).UnixMilli()
	row["created_at"] = now.Add(-time.Hour).UnixMilli()
	row["updated_at"] = now.UnixMilli()
	row["last_event_at"] = now.UnixMilli()
	return row
}

func newDashboardSnapshotSQLiteIntegerDB(t *testing.T, now time.Time) *sql.DB {
	t.Helper()

	db := openDashboardSnapshotSQLiteIntegerDB(t)
	createDashboardSnapshotSQLiteIntegerSchema(t, db)
	seedDashboardSnapshotSQLiteIntegerDB(t, db, now)
	return db
}

func openDashboardSnapshotSQLiteIntegerDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "dashboard-snapshot.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Fatalf("close sqlite: %v", err)
		}
	})

	return db
}

func createDashboardSnapshotSQLiteIntegerSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`
CREATE TABLE task_dags (
	id INTEGER PRIMARY KEY,
	dag_key TEXT NOT NULL UNIQUE,
	version INTEGER NOT NULL DEFAULT 0,
	title TEXT NOT NULL DEFAULT '',
	description TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'draft',
	created_by TEXT NOT NULL DEFAULT '',
	metadata TEXT NOT NULL DEFAULT '{}',
	trigger TEXT NOT NULL DEFAULT 'manual',
	cron_expr TEXT NOT NULL DEFAULT '',
	next_run_at INTEGER,
	started_at INTEGER,
	finished_at INTEGER,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE task_dag_runs (
	id INTEGER PRIMARY KEY,
	run_key TEXT NOT NULL UNIQUE,
	dag_key TEXT NOT NULL,
	dag_version_snapshot INTEGER NOT NULL DEFAULT 0,
	trigger_source TEXT NOT NULL DEFAULT '',
	status TEXT NOT NULL DEFAULT 'running',
	started_at INTEGER NOT NULL,
	finished_at INTEGER,
	events TEXT NOT NULL DEFAULT '[]',
	budget_used INTEGER NOT NULL DEFAULT 0,
	budget_limit INTEGER,
	metadata TEXT NOT NULL DEFAULT '{}',
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE task_dag_nodes (
	id INTEGER PRIMARY KEY,
	dag_key TEXT NOT NULL,
	node_key TEXT NOT NULL,
	title TEXT NOT NULL DEFAULT '',
	node_type TEXT NOT NULL DEFAULT 'task',
	assigned_to TEXT NOT NULL DEFAULT '',
	depends_on TEXT NOT NULL DEFAULT '[]',
	status TEXT NOT NULL DEFAULT 'pending',
	command_ref TEXT NOT NULL DEFAULT '',
	config TEXT NOT NULL DEFAULT '{}',
	result TEXT,
	run_id INTEGER,
	started_at INTEGER,
	finished_at INTEGER,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL,
	active_turn_id TEXT,
	active_wakeup_id INTEGER,
	last_event_at INTEGER,
	spawning_thread_id TEXT
);
`); err != nil {
		t.Fatalf("create dashboard snapshot schema: %v", err)
	}
}

func seedDashboardSnapshotSQLiteIntegerDB(t *testing.T, db *sql.DB, now time.Time) {
	t.Helper()

	nowMS := now.UnixMilli()
	insertDashboardSnapshotSQLiteIntegerDAG(t, db, now, nowMS)
	insertDashboardSnapshotSQLiteIntegerRun(t, db, now, nowMS)
	insertDashboardSnapshotSQLiteIntegerNodes(t, db, now, nowMS)
}

func insertDashboardSnapshotSQLiteIntegerDAG(t *testing.T, db *sql.DB, now time.Time, nowMS int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO task_dags
		(id, dag_key, version, title, description, status, created_by, metadata, trigger, cron_expr, next_run_at, started_at, finished_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		11, "daily-brief", 3, "Daily Brief", "Automated daily report", "ready", "user", `{"final_node_key":"report"}`,
		"scheduled", "0 9 * * *", now.Add(time.Hour).UnixMilli(), now.Add(-30*time.Minute).UnixMilli(), now.Add(-10*time.Minute).UnixMilli(),
		now.Add(-time.Hour).UnixMilli(), nowMS,
	); err != nil {
		t.Fatalf("insert task_dags: %v", err)
	}
}

func insertDashboardSnapshotSQLiteIntegerRun(t *testing.T, db *sql.DB, now time.Time, nowMS int64) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO task_dag_runs
		(id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, events, budget_used, budget_limit, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		7, "daily-brief#run-1", "daily-brief", 3, "manual", "running", now.Add(-time.Minute).UnixMilli(), now.Add(-30*time.Second).UnixMilli(),
		`[]`, 0, nil, `{"final_output":{"kind":"file","path":"reports/daily.md"}}`, now.Add(-time.Minute).UnixMilli(), nowMS,
	); err != nil {
		t.Fatalf("insert task_dag_runs: %v", err)
	}
}

func insertDashboardSnapshotSQLiteIntegerNodes(t *testing.T, db *sql.DB, now time.Time, nowMS int64) {
	t.Helper()
	for _, runID := range []any{nil, int64(7)} {
		if _, err := db.Exec(`INSERT INTO task_dag_nodes
			(dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, run_id, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"daily-brief", "report", "Report Output", "agent", "codex", `[]`, "running", "", `{"model":"gpt-5"}`, nil, runID,
			now.Add(-time.Minute).UnixMilli(), now.Add(-30*time.Second).UnixMilli(), now.Add(-time.Hour).UnixMilli(), nowMS, nil, nil, nowMS, nil,
		); err != nil {
			t.Fatalf("insert task_dag_nodes runID=%v: %v", runID, err)
		}
	}
}

func int64PtrForDashboardTest(value int64) *int64 {
	return &value
}

var _ dbquery.Store = (*stubDashboardQueryStore)(nil)
var _ contract.DAGRuntime = (*stubDashboardOrchestration)(nil)
