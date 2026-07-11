package taskdag

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
)

// CreateRun 是 RunStore.CreateRun 的实现：StartDAG 阶段插入新 run 记录。
// 入参 input.RunKey 必须由调用方保证 UNIQUE（service 层根据 dag_key +
// 时间戳 [+ idempotency_key] 生成）；UNIQUE 冲突会被 wrapTaskDAGError
// 包成 task_dag_run 域错误向上传。
func (s *store) CreateRun(ctx context.Context, input CreateRunInput) (*Run, error) {
	metadata := input.Metadata
	if metadata == nil {
		// 与 task_dag_runs.metadata NOT NULL DEFAULT '{}' 对齐：
		// 用 JSON 空对象兜底，避免读路径拿到 null 时与对象语义错位。
		metadata = json.RawMessage("{}")
	}
	return queryOneWrite(ctx, func() (taskDagRunRow, error) {
		return scanTaskDagRunRow(s.db.QueryRowContext(ctx, createTaskDagRunSQL,
			input.RunKey,
			input.DagKey,
			input.DagVersionSnapshot,
			input.TriggerSource,
			metadata,
			budgetLimitToInt8(input.BudgetLimit),
		))
	}, "create", "task_dag_run", fromTaskDagRunRow)
}

// GetRun 按 run_key 查 1 行；未找到返回 wrapTaskDAGError 包装的 database/sql
// ErrNoRows（统一域错误，service 层用 errors.Is 判断）。
func (s *store) GetRun(ctx context.Context, runKey string) (*Run, error) {
	return queryOne(func() (taskDagRunRow, error) {
		return getTaskDagRunRow(ctx, s.db, runKey)
	}, "get", "task_dag_run", fromTaskDagRunRow)
}

// ListRuns 按 dag_key 列出所有 run；可选 status 过滤；ORDER BY started_at DESC。
// filter.Limit=0 时使用默认上限 50，避免无界返回。
func (s *store) ListRuns(ctx context.Context, filter ListRunsFilter) ([]Run, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.QueryContext(ctx, listTaskDagRunsByKeySQL, filter.DagKey, filter.Status, int64(limit))
	if err != nil {
		return nil, wrapTaskDAGError(err, "list", "task_dag_run")
	}
	defer rows.Close()
	runs, err := scanTaskDagRunListRows(rows)
	if err != nil {
		return nil, wrapTaskDAGError(err, "list", "task_dag_run")
	}
	return mapRows(runs, fromTaskDagRunListRow), nil
}

// CloneNodesForRun 把模板节点复制成 run-scoped runtime nodes。后续所有
// task_update_node / dispatcher 写入都必须带 run_id 命中这些副本，模板节点
// 只由 create/apply_ops 路径维护。
func (s *store) CloneNodesForRun(ctx context.Context, dagKey string, runID int64) (int64, error) {
	return queryValueWrite(ctx, func() (int64, error) {
		return s.q.CloneTaskDagNodesForRun(ctx, sqlc.CloneTaskDagNodesForRunParams{
			DagKey: dagKey,
			RunID:  int64Ptr(runID),
		})
	}, "clone_nodes_for_run", "task_dag_node")
}

// PromoteRootNodesToReady 把当前 run 下 depends_on=[] 且 status='pending'
// 的根 runtime node 提升为 ready；它只做状态流程推进，不负责入队 wakeup，
// root dispatch 路由由 ScheduleRootWakeups 再按 assigned_to/config 决定。
func (s *store) PromoteRootNodesToReady(ctx context.Context, dagKey string, runID int64) (int64, error) {
	return queryValueWrite(ctx, func() (int64, error) {
		return s.q.PromoteRootNodesToReady(ctx, sqlc.PromoteRootNodesToReadyParams{
			DagKey: dagKey,
			RunID:  int64Ptr(runID),
		})
	}, "promote_root_nodes_to_ready", "task_dag_node")
}

// TerminateRun 同事务取消 running run、非终态 runtime nodes 与未完成 wakeups，
// 并收集本 run 已 spawn 的 child thread ids 供 service 停止外部 agent。
// 已取消 run 再次终止会走幂等读取分支返回同一组 thread ids。
func (s *store) TerminateRun(ctx context.Context, input TerminateRunInput) (TerminateRunResult, error) {
	if err := requireRuntimeRunID("terminate_run", input.RunID); err != nil {
		return TerminateRunResult{}, err
	}
	reason := strings.TrimSpace(input.Reason)
	if reason == "" {
		reason = "user_requested"
	}
	event, err := json.Marshal(map[string]any{
		"kind":   "run_cancelled",
		"reason": reason,
	})
	if err != nil {
		return TerminateRunResult{}, fmt.Errorf("marshal run cancel event: %w", err)
	}
	var result TerminateRunResult
	err = sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, txdb sqlc.DBTX) error {
		threadIDs, err := terminateRunTx(ctx, txq, txdb, input, reason, event)
		if err != nil {
			return err
		}
		result.SpawnedThreadIDs = threadIDs
		return nil
	})
	if err != nil {
		return TerminateRunResult{}, wrapTaskDAGError(err, "terminate_run", "task_dag_run")
	}
	return result, nil
}

// terminateRunTx 是 TerminateRun 的事务体：先取消节点，再取消 wakeup，最后取消 run 行。
// 顺序不能颠倒，这样即使最后一步幂等重入也能正确收集 spawned thread ids。
func terminateRunTx(ctx context.Context, txq *sqlc.Queries, txdb sqlc.DBTX, input TerminateRunInput, reason string, event []byte) ([]string, error) {
	if _, err := txq.CancelTaskDagRunNodes(ctx, sqlc.CancelTaskDagRunNodesParams{
		Reason: reason,
		DagKey: input.DagKey,
		RunID:  int64Ptr(input.RunID),
	}); err != nil {
		return nil, fmt.Errorf("cancel run nodes: %w", err)
	}
	if _, err := txq.CancelTaskDagRunWakeups(ctx, sqlc.CancelTaskDagRunWakeupsParams{
		Reason: "run_cancelled: " + reason,
		DagKey: input.DagKey,
		RunID:  int64Ptr(input.RunID),
	}); err != nil {
		return nil, fmt.Errorf("cancel run wakeups: %w", err)
	}
	if err := cancelTaskDagRunRow(ctx, txdb, input, event); err != nil {
		return terminateRunAlreadyCancelled(ctx, txq, txdb, input, err)
	}
	return runSpawnedThreadIDs(ctx, txq, input.DagKey, input.RunID)
}

// terminateRunAlreadyCancelled 处理幂等路径：run 已被取消时读取其 spawned thread ids 返回，
// 仅在 run 确认为 cancelled 且匹配 input 时认可幂等，否则透传原错误。
func terminateRunAlreadyCancelled(ctx context.Context, txq *sqlc.Queries, txdb sqlc.DBTX, input TerminateRunInput, cancelErr error) ([]string, error) {
	if errors.Is(cancelErr, sql.ErrNoRows) {
		threadIDs, loadErr := cancelledRunSpawnedThreadIDs(ctx, txq, txdb, input)
		if loadErr == nil {
			return threadIDs, nil
		}
	}
	return nil, fmt.Errorf("cancel run: %w", cancelErr)
}

// cancelledRunSpawnedThreadIDs 验证已取消 run 的幂等性，通过后读取 spawned thread ids。
func cancelledRunSpawnedThreadIDs(ctx context.Context, q *sqlc.Queries, db sqlc.DBTX, input TerminateRunInput) ([]string, error) {
	run, err := getTaskDagRunRow(ctx, db, input.RunKey)
	if err != nil {
		return nil, err
	}
	if run.ID != input.RunID || run.DagKey != input.DagKey || run.Status != "cancelled" {
		return nil, sql.ErrNoRows
	}
	return runSpawnedThreadIDs(ctx, q, input.DagKey, input.RunID)
}

// runSpawnedThreadIDs 从 run 下所有节点的 spawning_thread_id 中收集非空值并排序去重。
func runSpawnedThreadIDs(ctx context.Context, q *sqlc.Queries, dagKey string, runID int64) ([]string, error) {
	nodes, err := q.ListTaskDagRunNodes(ctx, sqlc.ListTaskDagRunNodesParams{DagKey: dagKey, RunID: int64Ptr(runID)})
	if err != nil {
		return nil, err
	}
	values := make([]*string, 0, len(nodes))
	for _, node := range nodes {
		values = append(values, node.SpawningThreadID)
	}
	return nonEmptyTextValues(values), nil
}

// nonEmptyTextValues 过滤 nil 和空字符串指针，返回排序后的非空值切片。
func nonEmptyTextValues(values []*string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == nil {
			continue
		}
		if text := strings.TrimSpace(*value); text != "" {
			out = append(out, text)
		}
	}
	sort.Strings(out)
	return out
}

const createTaskDagRunSQL = `
INSERT INTO task_dag_runs (
    run_key, dag_key, dag_version_snapshot, trigger_source, status,
    started_at, metadata, budget_limit, created_at, updated_at
)
VALUES (
    ?, ?, ?, ?, 'running',
    (CAST(strftime('%s','now') AS INTEGER) * 1000),
    ?, ?,
    (CAST(strftime('%s','now') AS INTEGER) * 1000),
    (CAST(strftime('%s','now') AS INTEGER) * 1000)
)
RETURNING id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, events, budget_used, budget_limit, metadata, created_at, updated_at`

const getTaskDagRunSQL = `
SELECT id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, events, budget_used, budget_limit, metadata, created_at, updated_at
FROM task_dag_runs
WHERE run_key = ?`

const listTaskDagRunsByKeySQL = `
SELECT id, run_key, dag_key, dag_version_snapshot, trigger_source, status, started_at, finished_at, budget_used, budget_limit, created_at, updated_at
FROM task_dag_runs
WHERE dag_key = ?
  AND (?2 = '' OR status = ?2)
ORDER BY started_at DESC, id DESC
LIMIT ?3`

const loadTaskDagRunEventsForCancelSQL = `
SELECT CAST(events AS BLOB)
FROM task_dag_runs
WHERE dag_key = ?1
  AND id = ?2
  AND run_key = ?3
  AND status = 'running'`

const cancelTaskDagRunSQL = `
UPDATE task_dag_runs
SET status = 'cancelled',
    finished_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    updated_at = (CAST(strftime('%s','now') AS INTEGER) * 1000),
    events = ?1
WHERE dag_key = ?2
  AND id = ?3
  AND run_key = ?4
  AND status = 'running'
RETURNING id`

// taskDagRunScanner 是 sql.Row 和 sql.Rows 的公共扫描接口，让 scanTaskDagRunRow 兼容两者。
type taskDagRunScanner interface {
	Scan(dest ...any) error
}

// taskDagRunRow 是 task_dag_runs 全量列的扫描结构体，用于 RETURNING 和 GetRun 路径。
type taskDagRunRow struct {
	ID                 int64
	RunKey             string
	DagKey             string
	DagVersionSnapshot int64
	TriggerSource      string
	Status             string
	StartedAt          int64
	FinishedAt         *int64
	Events             []byte
	BudgetUsed         int64
	BudgetLimit        *int64
	Metadata           []byte
	CreatedAt          int64
	UpdatedAt          int64
}

// taskDagRunListRow 是 ListRuns 查询的轻量投影（不含 events / metadata 大字段）。
type taskDagRunListRow struct {
	ID                 int64
	RunKey             string
	DagKey             string
	DagVersionSnapshot int64
	TriggerSource      string
	Status             string
	StartedAt          int64
	FinishedAt         *int64
	BudgetUsed         int64
	BudgetLimit        *int64
	CreatedAt          int64
	UpdatedAt          int64
}

// getTaskDagRunRow 按 run_key 读取单行 task_dag_runs。
func getTaskDagRunRow(ctx context.Context, db sqlc.DBTX, runKey string) (taskDagRunRow, error) {
	return scanTaskDagRunRow(db.QueryRowContext(ctx, getTaskDagRunSQL, runKey))
}

// cancelTaskDagRunRow 先读取当前 events 数组，再追加 cancel 事件，最后 UPDATE + RETURNING id。
// sql.ErrNoRows 表示 run 不存在或不在 running 状态（幂等路径由 terminateRunAlreadyCancelled 处理）。
func cancelTaskDagRunRow(ctx context.Context, db sqlc.DBTX, input TerminateRunInput, event []byte) error {
	var currentEvents json.RawMessage
	if err := db.QueryRowContext(ctx, loadTaskDagRunEventsForCancelSQL, input.DagKey, input.RunID, input.RunKey).Scan(&currentEvents); err != nil {
		return err
	}
	nextEvents, err := appendRunEventJSON(currentEvents, event)
	if err != nil {
		return err
	}
	var id int64
	return db.QueryRowContext(ctx, cancelTaskDagRunSQL, nextEvents, input.DagKey, input.RunID, input.RunKey).Scan(&id)
}

// scanTaskDagRunRows 迭代 sql.Rows 扫描多行 taskDagRunRow，遇错立即返回。
func scanTaskDagRunRows(rows *sql.Rows) ([]taskDagRunRow, error) {
	out := make([]taskDagRunRow, 0)
	for rows.Next() {
		row, err := scanTaskDagRunRow(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// scanTaskDagRunListRows 迭代 sql.Rows 扫描多行 taskDagRunListRow（轻量投影）。
func scanTaskDagRunListRows(rows *sql.Rows) ([]taskDagRunListRow, error) {
	out := make([]taskDagRunListRow, 0)
	for rows.Next() {
		var row taskDagRunListRow
		if err := rows.Scan(
			&row.ID,
			&row.RunKey,
			&row.DagKey,
			&row.DagVersionSnapshot,
			&row.TriggerSource,
			&row.Status,
			&row.StartedAt,
			&row.FinishedAt,
			&row.BudgetUsed,
			&row.BudgetLimit,
			&row.CreatedAt,
			&row.UpdatedAt,
		); err != nil {
			return nil, err
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// scanTaskDagRunRow 从 scanner 中按列顺序扫描 taskDagRunRow。
func scanTaskDagRunRow(scanner taskDagRunScanner) (taskDagRunRow, error) {
	var row taskDagRunRow
	err := scanner.Scan(
		&row.ID,
		&row.RunKey,
		&row.DagKey,
		&row.DagVersionSnapshot,
		&row.TriggerSource,
		&row.Status,
		&row.StartedAt,
		&row.FinishedAt,
		&row.Events,
		&row.BudgetUsed,
		&row.BudgetLimit,
		&row.Metadata,
		&row.CreatedAt,
		&row.UpdatedAt,
	)
	return row, err
}

// fromTaskDagRun 把 sqlc 生成的行结构体转成 contract 层 Run。
// SQLite epoch 毫秒会统一转为 time.Time，可空列保持 nil。
func fromTaskDagRun(row sqlc.TaskDagRun) Run {
	return fromTaskDagRunRaw(row.ID, row.RunKey, row.DagKey, row.DagVersionSnapshot, row.TriggerSource, row.Status, row.StartedAt, row.FinishedAt, row.Events, row.BudgetUsed, row.BudgetLimit, row.Metadata, row.CreatedAt, row.UpdatedAt)
}

// fromTaskDagRunRow 把手写扫描结构体转成 contract Run；Events 可能为 nil（list 路径）。
func fromTaskDagRunRow(row taskDagRunRow) Run {
	return fromTaskDagRunRaw(row.ID, row.RunKey, row.DagKey, row.DagVersionSnapshot, row.TriggerSource, row.Status, row.StartedAt, row.FinishedAt, row.Events, row.BudgetUsed, row.BudgetLimit, row.Metadata, row.CreatedAt, row.UpdatedAt)
}

// fromTaskDagRunListRow 把列表行投影成 contract Run，events/metadata 置 nil。
func fromTaskDagRunListRow(row taskDagRunListRow) Run {
	return fromTaskDagRunRaw(row.ID, row.RunKey, row.DagKey, row.DagVersionSnapshot, row.TriggerSource, row.Status, row.StartedAt, row.FinishedAt, nil, row.BudgetUsed, row.BudgetLimit, nil, row.CreatedAt, row.UpdatedAt)
}

// fromTaskDagRunRaw 是所有 run 行映射路径的公共构造函数，把原始列值转为 contract Run。
func fromTaskDagRunRaw(id int64, runKey, dagKey string, dagVersionSnapshot int64, triggerSource, status string, startedAt int64, finishedAt *int64, events []byte, budgetUsed int64, budgetLimit *int64, metadata []byte, createdAt, updatedAt int64) Run {
	return Run{
		ID:                 id,
		RunKey:             runKey,
		DagKey:             dagKey,
		DagVersionSnapshot: dagVersionSnapshot,
		TriggerSource:      triggerSource,
		Status:             status,
		StartedAt:          timeValue(startedAt),
		FinishedAt:         nullableTime(finishedAt),
		Events:             json.RawMessage(events),
		BudgetUsed:         budgetUsed,
		BudgetLimit:        nullableInt64(budgetLimit),
		Metadata:           json.RawMessage(metadata),
		CreatedAt:          timeValue(createdAt),
		UpdatedAt:          timeValue(updatedAt),
	}
}

// budgetLimitToInt8 透传可空的预算上限指针（占位函数，便于未来类型收窄）。
func budgetLimitToInt8(v *int64) *int64 {
	return v
}

// nullableTime 把可空的 epoch 毫秒指针转为 *time.Time，nil 输入返回 nil。
func nullableTime(v *int64) *time.Time {
	return timestampPtr(v)
}

// nullableInt64 透传可空 int64 指针（占位函数，便于未来拦截特殊值）。
func nullableInt64(v *int64) *int64 {
	return v
}

// WithRunTx 起单一 SQLite BEGIN IMMEDIATE 事务，fn 拿到的 tx RunStore 是
// 事务绑定的 *store 运行时实例，所有 RunStore 方法调用都在同事务内。
//
// 主要调用点：service.StartDAG 使用 WithRunTx 原子化 CreateRun +
// PromoteRootNodesToReady，任一失败都会回滚事务、避免“run 已建却
// 根节点未 ready”脱状态。
func (s *store) WithRunTx(ctx context.Context, fn func(tx RunStore) error) error {
	return wrapTaskDAGError(sqlctx.WithImmediateTx(ctx, s.db, s.q, func(txq *sqlc.Queries, tx sqlc.DBTX) error {
		return fn(&store{db: tx, q: txq})
	}), "with_run_tx", "task_dag_run")
}

// WithScheduledStartTx 在事务中绑定计划启动上下文。
func (s *store) WithScheduledStartTx(ctx context.Context, fn func(tx ScheduledStartTxStore) error) error {
	return wrapTaskDAGError(sqlctx.WithImmediateTx(ctx, s.db, s.q, func(txq *sqlc.Queries, tx sqlc.DBTX) error {
		return fn(&store{db: tx, q: txq})
	}), "with_scheduled_start_tx", "task_dag_run")
}

// UpdateScheduledDAGNextRun 更新计划 DAG 的下一次运行时间。
func (s *store) UpdateScheduledDAGNextRun(ctx context.Context, dagKey string, dueAt, nextRunAt time.Time) (int64, error) {
	return queryValueWrite(ctx, func() (int64, error) {
		return s.q.UpdateTaskDagNextRun(ctx, sqlc.UpdateTaskDagNextRunParams{
			NextRunAt: sqlc.TimeValuePtr(&nextRunAt),
			DagKey:    dagKey,
			DueAt:     sqlc.TimeValuePtr(&dueAt),
		})
	}, "update_next_run", "task_dag")
}
