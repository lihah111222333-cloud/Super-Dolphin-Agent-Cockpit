# Task 16: Dashboard DAG 快照 int64 时间戳修复

> **For agentic workers:** REQUIRED SUB-SKILL: use `superpowers:subagent-driven-development` or repo-local `子代理驱动开发` to implement this task in one dedicated task worktree. Steps use checkbox syntax for tracking. Do not implement unrelated SQLite cleanup.

**Goal:** 修复 SQLite 切换后自动化页加载失败：`dashboard: map dag row 0: created_at has unsupported type int64`，并锁定所有 Dashboard DAG snapshot 时间字段的 SQLite INTEGER 毫秒兼容性。

**Architecture:** Dashboard 在配置了 `dbquery.Store` 时会绕过 orchestration runtime，直接用受限 `dbquery` 查询 SQLite `task_dags/task_dag_runs/task_dag_nodes`，再把 `[]map[string]any` 映射成 contract 类型。修复应放在 Dashboard snapshot mapper 的时间字段解析边界，而不是修改 `dbquery` 的原始查询返回语义，也不是改 SQLite schema。

**Tech Stack:** Go, `database/sql`, modernc SQLite driver, Dashboard RPC, existing `internal/store/dbquery`, existing `internal/sidecar/orch/store/sqlc.TimeValue/TimePtr` Unix-millis convention.

---

## Agent Prompt

你负责修复集成分支 `codex/sqlite-switch-integration` 上的 Dashboard DAG snapshot 时间戳回归。当前现象是前端自动化页显示“同步失败/加载自动化失败”，后端日志报：

```text
dashboard: map dag row 0: created_at has unsupported type int64
```

你必须先写失败回归测试，再改 mapper。不要只修 `created_at` 一个字段；必须覆盖 DAG summary、DAG detail nodes、DAG runs/latest run 的所有时间字段。不要修改 generated sqlc 文件，不要把 `dbquery` 的 `rowValues` 做按列名猜测的隐式时间转换。

## Scope

依赖：Task 08、Task 11、Task 15 已合入集成分支。

建议实现 worktree：`.worktrees/sqlite-switch-task-16-dashboard-dag-snapshot-time-int64`。

本任务是 post-integration bugfix，不属于原 01-15 主计划扩范围。它只修 Dashboard DAG snapshot 的 SQLite INTEGER 时间戳读路径。

## 源码追溯

用户可见入口：

- `frontend-app/src/shared/api/backendApi.js:59` 定义 `UI_DASHBOARD_GET: 'ui/dashboard/get'`。
- `frontend-app/src/shared/api/backendApi.js:823` `getDashboardPage` 调用 `ui/dashboard/get`。
- `internal/module/dashboard/rpc.go:245` 注册 `ui/dashboard/get`。
- `internal/module/dashboard/rpc.go:247` 调 `svc.GetDashboardPage(ctx, p.Page)`。
- `internal/module/dashboard/ui_page.go:96` `page == "dags"` 时加载 `populateDashboardDAGs`。
- `internal/module/dashboard/ui_page.go:133` `populateDashboardDAGs` 调 `ListDAGs`。
- `internal/module/dashboard/detail.go:53` 只要 `s.hasDAGSnapshotQueries()` 为真，就走 `listDAGsFromSnapshot`，不走 orchestration runtime。

故障点：

- `internal/module/dashboard/dag_snapshot.go:114` 调 `s.dbQueries.Query` 查询 `task_dags`。
- `internal/module/dashboard/dag_snapshot.go:238` 将 mapper 错误包装为 `dashboard: map dag row %d: ...`。
- `internal/module/dashboard/dag_snapshot.go:258` `dashboardDAGSummaryFromRow` 解析 `created_at`。
- `internal/module/dashboard/dag_snapshot.go:572` `dashboardRowTimePtr` 当前只支持 `time.Time`、`*time.Time`、RFC3339 string。
- `internal/module/dashboard/dag_snapshot.go:592` 遇到 SQLite driver 返回的 `int64` 后报 `created_at has unsupported type int64`。

SQLite schema 与 typed store 对照：

- `internal/platform/db/sqlite/migrations/001_baseline.sql:525` 起定义 `task_dags`。
- `internal/platform/db/sqlite/migrations/001_baseline.sql:533-540` `started_at/finished_at/created_at/updated_at/next_run_at` 都是 `INTEGER`。
- `internal/platform/db/sqlite/migrations/001_baseline.sql:544-558` `task_dag_runs.started_at/finished_at/created_at/updated_at` 都是 `INTEGER`。
- `internal/platform/db/sqlite/migrations/001_baseline.sql:561-579` `task_dag_nodes.started_at/finished_at/created_at/updated_at/last_event_at` 都是 `INTEGER`。
- `internal/sidecar/orch/store/sqlc/types_ext.go:10` `TimeValue(int64)` 使用 `time.UnixMilli(value).UTC()`。
- `internal/sidecar/orch/store/sqlc/types_ext.go:17` `TimePtr(*int64)` 对 `nil` 和 `0` 返回 `nil`，否则 `time.UnixMilli(*value).UTC()`。

实际运行库证据：

```text
task_dags row id=1 created_at=integer/int64/1781446235000 updated_at=integer/int64/1781446250000 next_run_at=integer/int64/1781481600000
task_dag_nodes row id=1 created_at=integer/int64/1781446235000 updated_at=integer/int64/1781446235000
task_dag_runs count=0
```

## 上层防护判定

这是一个真实阻塞，不是误报：

- 前端只能展示上次成功数据并提示同步失败，不能修复后端 mapper 错误。
- `ui/dashboard/get(page=dags)` 在 `populateDashboardDAGs` 第一阶段就失败，后续 latest run/detail 逻辑没有机会执行。
- orchestration runtime typed mapper 已有 `int64 -> time.Time` 转换，但 `detail.go:53` 在有 `dbQueries` 时主动绕过该路径。
- `dbquery` 是原始受限查询接口，`internal/store/dbquery/executor.go:73-81` 扫描到 `[]any` 后交给 `rowValues` 原样返回；它不知道哪些 `INTEGER` 是时间，哪些是 id/count/budget/version。不要在这里按列名后缀做隐式转换，否则会改变 `dashboard/query` 的原始返回语义。

## 受影响范围

必须覆盖这些 Dashboard snapshot mapper 字段：

- DAG summary:
  - required: `created_at`, `updated_at`
  - optional: `next_run_at`, `started_at`, `finished_at`
- DAG node:
  - required: `created_at`, `updated_at`
  - optional: `started_at`, `finished_at`, `last_event_at`
- DAG run:
  - required: `started_at`, `created_at`, `updated_at`
  - optional: `finished_at`

当前第一个暴露点是 `task_dags.created_at`。只修它会让后续字段继续在 detail/runs/latest-run 路径失败或被静默丢失。

## 修改点

- Modify:
  - `internal/module/dashboard/dag_snapshot.go`
- Test:
  - `internal/module/dashboard/dag_snapshot_test.go`

不要修改：

- `internal/store/dbquery/executor.go`
- `internal/store/dbquery/executor_parser.go`
- `internal/platform/db/sqlite/migrations/001_baseline.sql`
- `internal/sidecar/orch/store/sqlc/**` generated files
- `internal/store/sqlc/**` generated files

## Task Steps

### Task 16.1: 写失败回归测试

**Files:**

- Modify: `internal/module/dashboard/dag_snapshot_test.go`

- [ ] **Step 1: 更新测试文件 imports**

`dag_snapshot_test.go` 需要新增真实 SQLite/dbquery 集成测试，因此 imports 应包含 `database/sql`、`path/filepath` 和 modernc SQLite driver。

```go
import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anthropic-ai/super-agent-v3/internal/contract"
	"github.com/anthropic-ai/super-agent-v3/internal/store/dbquery"
	_ "modernc.org/sqlite"
)
```

- [ ] **Step 2: 添加 SQLite INTEGER 毫秒行 fixture**

在 `dashboardDAGRow`/`dashboardRunRow`/`dashboardNodeRow` helper 附近添加以下 helper。它们把现有 `time.Time` stub 行转换成真实 SQLite driver 会返回的 `int64` 毫秒形态。

```go
func dashboardDAGRowSQLiteMillis(now time.Time) map[string]any {
	row := dashboardDAGRow(now)
	row["next_run_at"] = now.Add(time.Hour).UnixMilli()
	row["started_at"] = now.Add(-30 * time.Minute).UnixMilli()
	row["finished_at"] = nil
	row["created_at"] = now.Add(-time.Hour).UnixMilli()
	row["updated_at"] = now.UnixMilli()
	return row
}

func dashboardRunRowSQLiteMillis(now time.Time) map[string]any {
	row := dashboardRunRow(now)
	row["started_at"] = now.Add(-time.Minute).UnixMilli()
	row["finished_at"] = nil
	row["created_at"] = now.Add(-time.Minute).UnixMilli()
	row["updated_at"] = now.UnixMilli()
	return row
}

func dashboardNodeRowSQLiteMillis(now time.Time, runID *int64) map[string]any {
	row := dashboardNodeRow(now, runID)
	row["started_at"] = now.Add(-time.Minute).UnixMilli()
	row["finished_at"] = nil
	row["created_at"] = now.Add(-time.Hour).UnixMilli()
	row["updated_at"] = now.UnixMilli()
	row["last_event_at"] = now.UnixMilli()
	return row
}
```

- [ ] **Step 3: 添加 stub mapper 回归测试**

添加一个测试覆盖 `GetDashboardPage(dags)`、DAG detail、DAG runs、DAG run detail 全链路。该测试在当前代码上必须失败，失败信息应包含 `created_at has unsupported type int64` 或后续时间字段 unsupported type。

```go
func TestDashboardDAGSnapshotParsesSQLiteIntegerTimestamps(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 14, 14, 30, 50, 123_000_000, time.UTC)
	db := &stubDashboardQueryStore{responses: []stubDashboardQueryResponse{
		{contains: []string{"FROM task_dags", "ORDER BY updated_at"}, rows: []map[string]any{dashboardDAGRowSQLiteMillis(now)}},
		{contains: []string{"FROM task_dag_runs", "ROW_NUMBER() OVER"}, rows: []map[string]any{dashboardRunRowSQLiteMillis(now)}},
		{contains: []string{"FROM task_dags", "WHERE dag_key"}, rows: []map[string]any{dashboardDAGRowSQLiteMillis(now)}},
		{contains: []string{"FROM task_dag_nodes", "run_id IS NULL"}, rows: []map[string]any{dashboardNodeRowSQLiteMillis(now, nil)}},
		{contains: []string{"FROM task_dag_runs", "WHERE dag_key"}, rows: []map[string]any{dashboardRunRowSQLiteMillis(now)}},
		{contains: []string{"FROM task_dag_runs", "WHERE run_key"}, rows: []map[string]any{dashboardRunRowSQLiteMillis(now)}},
		{contains: []string{"FROM task_dag_nodes", "run_id = $2"}, rows: []map[string]any{dashboardNodeRowSQLiteMillis(now, int64PtrForDashboardTest(7))}},
	}}
	svc := &service{
		dbQueries:  db,
		dagRuntime: &stubDashboardOrchestration{listDAGsErr: errDashboardStub, listRunsErr: errDashboardStub},
	}

	page, err := svc.GetDashboardPage(context.Background(), "dags")
	if err != nil {
		t.Fatalf("GetDashboardPage(dags) error = %v", err)
	}
	if len(page.DAGs) != 1 {
		t.Fatalf("GetDashboardPage(dags).DAGs len = %d, want 1", len(page.DAGs))
	}
	if got, want := page.DAGs[0].CreatedAt, now.Add(-time.Hour); !got.Equal(want) {
		t.Fatalf("DAG CreatedAt = %s, want %s", got, want)
	}
	if page.DAGs[0].NextRunAt == nil || !page.DAGs[0].NextRunAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("DAG NextRunAt = %#v, want %s", page.DAGs[0].NextRunAt, now.Add(time.Hour))
	}
	if page.DAGs[0].StartedAt == nil || !page.DAGs[0].StartedAt.Equal(now.Add(-30*time.Minute)) {
		t.Fatalf("DAG StartedAt = %#v, want %s", page.DAGs[0].StartedAt, now.Add(-30*time.Minute))
	}
	requireSnapshotLatestRun(t, page.DAGs[0], "daily-brief#run-1", true)

	detail, err := svc.GetDAGDetail(context.Background(), "daily-brief")
	if err != nil {
		t.Fatalf("GetDAGDetail() error = %v", err)
	}
	if len(detail.Nodes) != 1 || !detail.Nodes[0].CreatedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("GetDAGDetail().Nodes = %#v, want SQLite millis node timestamps", detail.Nodes)
	}
	if detail.Nodes[0].LastEventAt == nil || !detail.Nodes[0].LastEventAt.Equal(now) {
		t.Fatalf("Node LastEventAt = %#v, want %s", detail.Nodes[0].LastEventAt, now)
	}

	runs, err := svc.ListDAGRuns(context.Background(), "daily-brief", "", 5)
	if err != nil {
		t.Fatalf("ListDAGRuns() error = %v", err)
	}
	if len(runs) != 1 || !runs[0].StartedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("ListDAGRuns() = %#v, want SQLite millis run timestamp", runs)
	}

	run, err := svc.GetDAGRun(context.Background(), "daily-brief#run-1")
	if err != nil {
		t.Fatalf("GetDAGRun() error = %v", err)
	}
	if !run.Run.CreatedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("GetDAGRun().Run.CreatedAt = %s, want %s", run.Run.CreatedAt, now.Add(-time.Minute))
	}
	if len(run.Nodes) != 1 || run.Nodes[0].RunID == nil || *run.Nodes[0].RunID != 7 {
		t.Fatalf("GetDAGRun().Nodes = %#v, want run node with run_id=7", run.Nodes)
	}
}
```

- [ ] **Step 4: 添加真实 SQLite/dbquery 集成测试**

stub 测试可以锁 mapper，但不能证明 `database/sql -> dbquery -> []map[string]any` 这条真实链路。再添加一个真实 SQLite fixture：schema 时间列必须是 `INTEGER`，并通过 `dbquery.NewQueryStore` 进入 Dashboard snapshot mapper。

```go
func TestDashboardDAGSnapshotReadsSQLiteIntegerTimestampsThroughDBQuery(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 6, 14, 14, 30, 50, 123_000_000, time.UTC)
	db := newDashboardSnapshotSQLiteIntegerDB(t, now)
	svc := &service{dbQueries: dbquery.NewQueryStore(db, time.Second)}

	page, err := svc.GetDashboardPage(context.Background(), "dags")
	if err != nil {
		t.Fatalf("GetDashboardPage(dags) error = %v", err)
	}
	if len(page.DAGs) != 1 {
		t.Fatalf("GetDashboardPage(dags).DAGs len = %d, want 1", len(page.DAGs))
	}
	if got, want := page.DAGs[0].CreatedAt, now.Add(-time.Hour); !got.Equal(want) {
		t.Fatalf("DAG CreatedAt = %s, want %s", got, want)
	}
	if page.DAGs[0].NextRunAt == nil || !page.DAGs[0].NextRunAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("DAG NextRunAt = %#v, want %s", page.DAGs[0].NextRunAt, now.Add(time.Hour))
	}
	requireSnapshotLatestRun(t, page.DAGs[0], "daily-brief#run-1", true)

	detail, err := svc.GetDAGDetail(context.Background(), "daily-brief")
	if err != nil {
		t.Fatalf("GetDAGDetail() error = %v", err)
	}
	if len(detail.Nodes) != 1 || !detail.Nodes[0].CreatedAt.Equal(now.Add(-time.Hour)) {
		t.Fatalf("GetDAGDetail().Nodes = %#v, want SQLite integer node timestamps", detail.Nodes)
	}

	runs, err := svc.ListDAGRuns(context.Background(), "daily-brief", "", 5)
	if err != nil {
		t.Fatalf("ListDAGRuns() error = %v", err)
	}
	if len(runs) != 1 || !runs[0].StartedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("ListDAGRuns() = %#v, want SQLite integer run timestamp", runs)
	}

	run, err := svc.GetDAGRun(context.Background(), "daily-brief#run-1")
	if err != nil {
		t.Fatalf("GetDAGRun() error = %v", err)
	}
	if !run.Run.CreatedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("GetDAGRun().Run.CreatedAt = %s, want %s", run.Run.CreatedAt, now.Add(-time.Minute))
	}
	if len(run.Nodes) != 1 || run.Nodes[0].RunID == nil || *run.Nodes[0].RunID != 7 {
		t.Fatalf("GetDAGRun().Nodes = %#v, want run node with run_id=7", run.Nodes)
	}
}

func newDashboardSnapshotSQLiteIntegerDB(t *testing.T, now time.Time) *sql.DB {
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

	nowMS := now.UnixMilli()
	if _, err := db.Exec(`INSERT INTO task_dags
		(id, dag_key, version, title, description, status, created_by, metadata, trigger, cron_expr, next_run_at, started_at, finished_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		11, "daily-brief", 3, "每日简报", "自动整理日报", "ready", "user", `{"final_node_key":"report"}`,
		"scheduled", "0 9 * * *", now.Add(time.Hour).UnixMilli(), now.Add(-30*time.Minute).UnixMilli(), nil,
		now.Add(-time.Hour).UnixMilli(), nowMS,
	); err != nil {
		t.Fatalf("insert task_dags: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO task_dag_runs
		(id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, events, budget_used, budget_limit, metadata, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		7, "daily-brief#run-1", "daily-brief", 3, "manual", "running", now.Add(-time.Minute).UnixMilli(), nil,
		`[]`, 0, nil, `{"final_output":{"kind":"file","path":"reports/daily.md"}}`, now.Add(-time.Minute).UnixMilli(), nowMS,
	); err != nil {
		t.Fatalf("insert task_dag_runs: %v", err)
	}
	for _, runID := range []any{nil, int64(7)} {
		if _, err := db.Exec(`INSERT INTO task_dag_nodes
			(dag_key, node_key, title, node_type, assigned_to, depends_on, status, command_ref, config, result, run_id, started_at, finished_at, created_at, updated_at, active_turn_id, active_wakeup_id, last_event_at, spawning_thread_id)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"daily-brief", "report", "输出简报", "agent", "codex", `[]`, "running", "", `{"model":"gpt-5"}`, nil, runID,
			now.Add(-time.Minute).UnixMilli(), nil, now.Add(-time.Hour).UnixMilli(), nowMS, nil, nil, nowMS, nil,
		); err != nil {
			t.Fatalf("insert task_dag_nodes runID=%v: %v", runID, err)
		}
	}
	return db
}
```

- [ ] **Step 5: 运行失败测试确认真阳性**

PowerShell:

```powershell
go test ./internal/module/dashboard -run "TestDashboardDAGSnapshot(ParsesSQLiteIntegerTimestamps|ReadsSQLiteIntegerTimestampsThroughDBQuery)" -count=1
```

Expected before implementation:

```text
FAIL
... created_at has unsupported type int64
```

如果当前失败不是时间戳 unsupported type，停止并追溯新失败原因，不要继续改实现。

### Task 16.2: 修复 Dashboard snapshot 时间解析边界

**Files:**

- Modify: `internal/module/dashboard/dag_snapshot.go`

- [ ] **Step 1: 修改 optional time helper 为 fail-fast**

当前 `dashboardOptionalTime` 在解析失败时 `slog.Warn` 后返回 `nil`，这会让 `next_run_at/started_at/last_event_at` 这类 SQLite `INTEGER` 字段被静默丢掉。将它改成返回 `(*time.Time, error)`，并让所有 mapper 显式传播错误。

```go
func dashboardOptionalTime(row map[string]any, key string) (*time.Time, error) {
	return dashboardRowTimePtr(row, key, false)
}
```

更新 `dashboardDAGSummaryFromRow`：

```go
	nextRunAt, err := dashboardOptionalTime(row, "next_run_at")
	if err != nil {
		return contract.DAGSummary{}, err
	}
	startedAt, err := dashboardOptionalTime(row, "started_at")
	if err != nil {
		return contract.DAGSummary{}, err
	}
	finishedAt, err := dashboardOptionalTime(row, "finished_at")
	if err != nil {
		return contract.DAGSummary{}, err
	}
```

并在返回结构里使用 `nextRunAt/startedAt/finishedAt`，不要继续直接调用会吞错的 helper。

同样更新 `dashboardDAGNodeFromRow` 的 `started_at/finished_at/last_event_at`，以及 `dashboardRunFromRow` 的 `finished_at`。每个字段解析失败都应返回错误，保留外层 `dashboard: map dag node row %d: ...` / `dashboard: map dag run row %d: ...` 包装。

- [ ] **Step 2: 让 `dashboardRowTimePtr` 支持 SQLite Unix millis**

在 `dashboardRowTimePtr` 的 type switch 中新增整数毫秒支持，语义对齐 `internal/sidecar/orch/store/sqlc.TimeValue/TimePtr`。

```go
func dashboardRowTimePtr(row map[string]any, key string, required bool) (*time.Time, error) {
	value, ok := row[key]
	if !ok || value == nil {
		if required {
			return nil, fmt.Errorf("%s is required", key)
		}
		return nil, nil
	}
	switch typed := value.(type) {
	case time.Time:
		return &typed, nil
	case *time.Time:
		return typed, nil
	case int64:
		return dashboardUnixMillisTimePtr(typed, required), nil
	case *int64:
		if typed == nil {
			if required {
				return nil, fmt.Errorf("%s is required", key)
			}
			return nil, nil
		}
		return dashboardUnixMillisTimePtr(*typed, required), nil
	case int:
		return dashboardUnixMillisTimePtr(int64(typed), required), nil
	case int32:
		return dashboardUnixMillisTimePtr(int64(typed), required), nil
	case json.Number:
		parsed, err := typed.Int64()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		return dashboardUnixMillisTimePtr(parsed, required), nil
	case float64:
		if math.Trunc(typed) != typed {
			return nil, fmt.Errorf("%s must be an integer millisecond timestamp", key)
		}
		return dashboardUnixMillisTimePtr(int64(typed), required), nil
	case string:
		parsed, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(typed))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", key, err)
		}
		return &parsed, nil
	default:
		return nil, fmt.Errorf("%s has unsupported type %T", key, value)
	}
}

func dashboardUnixMillisTimePtr(value int64, required bool) *time.Time {
	if value == 0 {
		if !required {
			return nil
		}
		zero := time.Time{}
		return &zero
	}
	parsed := time.UnixMilli(value).UTC()
	return &parsed
}
```

Notes:

- Required timestamp `0` returns zero `time.Time`, matching `sqlc.TimeValue(0)`.
- Optional timestamp `0` returns `nil`, matching `sqlc.TimePtr`.
- Do not parse numeric strings as milliseconds in this task. Existing string support is RFC3339 only; changing numeric string semantics is unnecessary.

- [ ] **Step 3: 移除不再使用的 import**

如果 `log/slog` 只被旧 `dashboardOptionalTime` 使用，删除该 import。保留 `encoding/json`、`math`、`strconv`、`strings`、`time` 中实际仍使用的 import。

- [ ] **Step 4: 单文件守卫**

Windows PowerShell：

```powershell
if (Test-Path .\scripts\test_with_guard.ps1) {
  pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal/module/dashboard/dag_snapshot.go
  pwsh -NoProfile -ExecutionPolicy Bypass -File .\scripts\test_with_guard.ps1 internal/module/dashboard/dag_snapshot_test.go
} else {
  Write-Host "scripts/test_with_guard.ps1 not found; running gofmt and focused package tests instead"
  gofmt -w internal/module/dashboard/dag_snapshot.go internal/module/dashboard/dag_snapshot_test.go
  go test ./internal/module/dashboard -run "TestDashboardDAGSnapshot(ParsesSQLiteIntegerTimestamps|ReadsSQLiteIntegerTimestampsThroughDBQuery)" -count=1
}
```

Expected after implementation:

```text
PASS
```

### Task 16.3: 验收与回归覆盖

**Files:**

- Verify only.

- [ ] **Step 1: 运行 dashboard focused tests**

```powershell
go test ./internal/module/dashboard -count=1
```

Expected:

```text
ok  	github.com/anthropic-ai/super-agent-v3/internal/module/dashboard	...
```

- [ ] **Step 2: 运行 dbquery + dashboard 联合测试**

```powershell
go test ./internal/store/dbquery ./internal/module/dashboard -count=1
```

Expected:

```text
ok  	github.com/anthropic-ai/super-agent-v3/internal/store/dbquery	...
ok  	github.com/anthropic-ai/super-agent-v3/internal/module/dashboard	...
```

- [ ] **Step 3: 运行 diff check**

```powershell
git diff --check
```

Expected: no output, exit 0.

- [ ] **Step 4: 运行应用级手工验收**

在集成分支或修复 worktree 构建/启动应用，使用包含至少一条 DAG 的 SQLite 数据库。当前仓库推荐入口：

Git Bash/WSL：

```bash
./run-new-ui-desktop.sh
```

Windows PowerShell 如果没有可执行的 `.ps1` 启动包装，必须明确记录 skipped reason，并用当前已启动的 `agent-terminal.exe` 或手工启动的 `cmd/agent-terminal` 进程验证同一组 RPC。验证前先确认监听端口：

```powershell
Get-NetTCPConnection -LocalPort 4512,8092,5175 -ErrorAction SilentlyContinue |
  Select-Object LocalAddress,LocalPort,State,OwningProcess
```

验证项：

```text
ui/dashboard/get page=dags returns success
dashboard/dags returns success
dashboard/dagDetail dagKey=<existing dag> returns success
dashboard/dagRuns dagKey=<existing dag> returns success, or empty list when no runs exist
```

前端自动化页不再显示：

```text
dashboard: map dag row 0: created_at has unsupported type int64
```

如果应用里已有 run/node 数据，确认页面中的 DAG 时间、next run、node last event、run started/finished 时间没有丢失。

## Review Checklist

两个 reviewagent 必须分别给出源码依据，不允许只说“看起来通过”。

Review 必须确认：

- `dashboardRowTimePtr` 支持 SQLite driver 返回的 `int64` 毫秒时间戳。
- DAG summary、DAG node、DAG run 的所有时间字段都覆盖，不是只修 `created_at`。
- optional 时间字段解析失败不会被静默吞掉。
- 没有修改 `dbquery` 原始返回语义。
- 没有手改 generated sqlc 文件。
- 新测试在旧实现下失败，在新实现下通过。
- focused tests 与 `git diff --check` 输出已记录。
- 运行态验收证明 `ui/dashboard/get(page=dags)` 不再失败。

## 不允许改

- 不要把 `task_dags/task_dag_runs/task_dag_nodes` 时间列改成 TEXT。
- 不要在 `dbquery.rowValues` 里按列名把所有 `*_at` 转成 `time.Time`。
- 不要增加前端缓存兜底来掩盖后端错误。
- 不要绕回 orchestration runtime 来避开 snapshot mapper。
- 不要扩大到 wakeup/lease/cron/UI 状态其它时间字段，除非源码追溯证明同一个 Dashboard snapshot mapper 会读取它们。
- 不要提交无法解释的 `internal/archtest/baseline.json` diff。

## Commit 与合并

实现任务的 codeagent 完成验收并双 reviewagent 通过后，使用中文 commit message：

```bash
git add internal/module/dashboard/dag_snapshot.go internal/module/dashboard/dag_snapshot_test.go
git commit -m "修复Dashboard DAG快照SQLite时间戳解析"
```

再合并回 `codex/sqlite-switch-integration`，并在集成分支至少重跑：

```powershell
go test ./internal/store/dbquery ./internal/module/dashboard -count=1
git diff --check
```
