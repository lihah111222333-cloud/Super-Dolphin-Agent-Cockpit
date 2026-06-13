package taskdag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlctx"
)

// CreateRun 是 RunStore.CreateRun 的实现：StartDAG 阶段插入新 run 记录。
// 入参 input.RunKey 必须由调用方保证 UNIQUE（service 层根据 dag_key +
// 时间戳 [+ idempotency_key] 生成）；UNIQUE 冲突会被 wrapTaskDAGError
// 包成 task_dag_run 域错误向上传。
func (s *store) CreateRun(ctx context.Context, input CreateRunInput) (*Run, error) {
	metadata := input.Metadata
	if metadata == nil {
		// 与 migration 0077 task_dag_runs.metadata NOT NULL DEFAULT '{}'::jsonb 对齐：
		// 用 jsonb 空对象兜底，避免读路径 fromTaskDagRun 拿到 jsonb null 时与对象规则错位。
		metadata = json.RawMessage("{}")
	}
	return queryOne(func() (sqlc.TaskDagRun, error) {
		return s.q.CreateTaskDagRun(ctx, sqlc.CreateTaskDagRunParams{
			RunKey:             input.RunKey,
			DagKey:             input.DagKey,
			DagVersionSnapshot: input.DagVersionSnapshot,
			TriggerSource:      input.TriggerSource,
			Column5:            metadata,
			BudgetLimit:        budgetLimitToInt8(input.BudgetLimit),
		})
	}, "create", "task_dag_run", fromTaskDagRun)
}

// GetRun 按 run_key 查 1 行；未找到返回 wrapTaskDAGError 包装的 pgx
// ErrNoRows（统一域错误，service 层用 errors.Is 判断）。
func (s *store) GetRun(ctx context.Context, runKey string) (*Run, error) {
	return queryOne(func() (sqlc.TaskDagRun, error) {
		return s.q.GetTaskDagRun(ctx, runKey)
	}, "get", "task_dag_run", fromTaskDagRun)
}

// ListRuns 按 dag_key 列出所有 run；可选 status 过滤；ORDER BY started_at DESC。
// filter.Limit=0 时使用默认上限 50，避免无界返回。
func (s *store) ListRuns(ctx context.Context, filter ListRunsFilter) ([]Run, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 50
	}
	return queryMany(func() ([]sqlc.TaskDagRun, error) {
		return s.q.ListTaskDagRunsByKey(ctx, sqlc.ListTaskDagRunsByKeyParams{
			DagKey:  filter.DagKey,
			Column2: filter.Status,
			Limit:   limit,
		})
	}, "list", "task_dag_run", fromTaskDagRun)
}

// CloneNodesForRun 把模板节点复制成 run-scoped runtime nodes。后续所有
// task_update_node / dispatcher 写入都必须带 run_id 命中这些副本，模板节点
// 只由 create/apply_ops 路径维护。
func (s *store) CloneNodesForRun(ctx context.Context, dagKey string, runID int64) (int64, error) {
	return queryValue(func() (int64, error) {
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
	return queryValue(func() (int64, error) {
		return s.q.PromoteRootNodesToReady(ctx, sqlc.PromoteRootNodesToReadyParams{
			DagKey:  dagKey,
			Column2: runID,
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
	err = sqlctx.WithTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
		threadIDs, err := terminateRunTx(ctx, txq, input, reason, event)
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

// terminateRunTx 的顺序不能随意调整：先停节点，再停 wakeup，最后翻 run
// status。这样即使最后一步发现 run 已被其它路径取消，也可以安全读取已有
// spawned_thread_id 返回给上层做 agent lifecycle 清理。
func terminateRunTx(ctx context.Context, txq *sqlc.Queries, input TerminateRunInput, reason string, event []byte) ([]string, error) {
	if _, err := txq.CancelTaskDagRunNodes(ctx, sqlc.CancelTaskDagRunNodesParams{
		DagKey:  input.DagKey,
		Column2: input.RunID,
		Column3: reason,
	}); err != nil {
		return nil, fmt.Errorf("cancel run nodes: %w", err)
	}
	if _, err := txq.CancelTaskDagRunWakeups(ctx, sqlc.CancelTaskDagRunWakeupsParams{
		DagKey:    input.DagKey,
		RunID:     int64Ptr(input.RunID),
		LastError: "run_cancelled: " + reason,
	}); err != nil {
		return nil, fmt.Errorf("cancel run wakeups: %w", err)
	}
	if _, err := txq.CancelTaskDagRun(ctx, sqlc.CancelTaskDagRunParams{
		DagKey:  input.DagKey,
		ID:      input.RunID,
		RunKey:  input.RunKey,
		Column4: event,
	}); err != nil {
		return terminateRunAlreadyCancelled(ctx, txq, input, err)
	}
	return runSpawnedThreadIDs(ctx, txq, input.DagKey, input.RunID)
}

func terminateRunAlreadyCancelled(ctx context.Context, txq *sqlc.Queries, input TerminateRunInput, cancelErr error) ([]string, error) {
	if errors.Is(cancelErr, pgx.ErrNoRows) {
		threadIDs, loadErr := cancelledRunSpawnedThreadIDs(ctx, txq, input)
		if loadErr == nil {
			return threadIDs, nil
		}
	}
	return nil, fmt.Errorf("cancel run: %w", cancelErr)
}

func cancelledRunSpawnedThreadIDs(ctx context.Context, q *sqlc.Queries, input TerminateRunInput) ([]string, error) {
	run, err := q.GetTaskDagRun(ctx, input.RunKey)
	if err != nil {
		return nil, err
	}
	if run.ID != input.RunID || run.DagKey != input.DagKey || run.Status != "cancelled" {
		return nil, pgx.ErrNoRows
	}
	return runSpawnedThreadIDs(ctx, q, input.DagKey, input.RunID)
}

func runSpawnedThreadIDs(ctx context.Context, q *sqlc.Queries, dagKey string, runID int64) ([]string, error) {
	nodes, err := q.ListTaskDagRunNodes(ctx, sqlc.ListTaskDagRunNodesParams{DagKey: dagKey, RunID: int64Ptr(runID)})
	if err != nil {
		return nil, err
	}
	values := make([]pgtype.Text, 0, len(nodes))
	for _, node := range nodes {
		values = append(values, node.SpawningThreadID)
	}
	return nonEmptyTextValues(values), nil
}

func nonEmptyTextValues(values []pgtype.Text) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if !value.Valid {
			continue
		}
		if text := strings.TrimSpace(value.String); text != "" {
			out = append(out, text)
		}
	}
	sort.Strings(out)
	return out
}

// fromTaskDagRun 把 sqlc 生成的行结构体转成 contract 层 Run。
// pgtype.Timestamptz → time.Time：直接取 .Time（PG 列 NOT NULL DEFAULT NOW()
// 保证有效）；FinishedAt 列 nullable，无值时返回 nil。
// pgtype.Int8 → *int64：用 nullableInt64 helper。
func fromTaskDagRun(row sqlc.TaskDagRun) Run {
	return Run{
		ID:                 row.ID,
		RunKey:             row.RunKey,
		DagKey:             row.DagKey,
		DagVersionSnapshot: row.DagVersionSnapshot,
		TriggerSource:      row.TriggerSource,
		Status:             row.Status,
		StartedAt:          row.StartedAt.Time,
		FinishedAt:         nullableTime(row.FinishedAt),
		Events:             json.RawMessage(row.Events),
		BudgetUsed:         row.BudgetUsed,
		BudgetLimit:        nullableInt64(row.BudgetLimit),
		Metadata:           json.RawMessage(row.Metadata),
		CreatedAt:          row.CreatedAt.Time,
		UpdatedAt:          row.UpdatedAt.Time,
	}
}

func budgetLimitToInt8(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

func nullableTime(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time
	return &t
}

func nullableInt64(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// WithRunTx 起单一 PG 事务，fn 拿到的 tx RunStore 是
// 事务绑定的 *store runtime实例，所有 RunStore 方法调用都在同事务内。
//
// 主要调用点：service.StartDAG 使用 WithRunTx 原子化 CreateRun +
// PromoteRootNodesToReady，任一失败都会回滚事务、避免“run 已建却
// 根节点未 ready”脱状态。 PG 事务跨 task_dag_runs / task_dag_nodes 两
// 表不是问题。
func (s *store) WithRunTx(ctx context.Context, fn func(tx RunStore) error) error {
	return wrapTaskDAGError(sqlctx.WithTx(ctx, s.db, s.q, func(txq *sqlc.Queries, tx sqlc.DBTX) error {
		return fn(&store{db: tx, q: txq})
	}), "with_run_tx", "task_dag_run")
}

// WithScheduledStartTx 在事务中绑定计划启动上下文。
func (s *store) WithScheduledStartTx(ctx context.Context, fn func(tx ScheduledStartTxStore) error) error {
	return wrapTaskDAGError(sqlctx.WithTx(ctx, s.db, s.q, func(txq *sqlc.Queries, tx sqlc.DBTX) error {
		return fn(&store{db: tx, q: txq})
	}), "with_scheduled_start_tx", "task_dag_run")
}

// UpdateScheduledDAGNextRun 更新计划 DAG 的下一次运行时间。
func (s *store) UpdateScheduledDAGNextRun(ctx context.Context, dagKey string, dueAt, nextRunAt time.Time) (int64, error) {
	return s.q.UpdateTaskDagNextRun(ctx, sqlc.UpdateTaskDagNextRunParams{
		NextRunAt: pgtype.Timestamptz{Time: nextRunAt, Valid: true},
		DagKey:    dagKey,
		DueAt:     pgtype.Timestamptz{Time: dueAt, Valid: true},
	})
}
