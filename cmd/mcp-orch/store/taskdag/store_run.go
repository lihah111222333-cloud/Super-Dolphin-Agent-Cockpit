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
		// 用 jsonb 空对象兜底，避免读路径 fromTaskDagRun 拿到 jsonb null 时与对象语义错位。
		metadata = json.RawMessage("{}")
	}
	return queryOne(func() (sqlc.TaskDagRun, error) {
		return s.q.CreateTaskDagRun(ctx, sqlc.CreateTaskDagRunParams{
			RunKey:             input.RunKey,
			DagKey:             input.DagKey,
			DagVersionSnapshot: input.DagVersionSnapshot,
			TriggerSource:      input.TriggerSource,
			Metadata:           metadata,
			BudgetLimit:        budgetLimitToInt8(input.BudgetLimit),
		})
	}, "create", "task_dag_run", fromTaskDagRun)
}

// GetRun 按 run_key 查 1 行；未找到返回 wrapTaskDAGError 包装的 database/sql
// ErrNoRows（统一域错误，service 层用 errors.Is 判断）。
func (s *store) GetRun(ctx context.Context, runKey string) (*Run, error) {
	return queryOne(func() (sqlc.TaskDagRun, error) {
		return s.q.GetTaskDagRun(ctx, sqlc.GetTaskDagRunParams{RunKey: runKey})
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
			DagKey:       filter.DagKey,
			StatusFilter: filter.Status,
			LimitCount:   int64(limit),
		})
	}, "list", "task_dag_run", fromTaskDagRun)
}

// PromoteRootNodesToReady 把 dag_key 下所有 depends_on=[] 且 status='pending'
// 的根节点提升为 'ready'。返回受影响行数（service 层用于断言至少一个根节点
// 被提升，否则视为 DAG 无可执行起点 → 报错 / 警告）。
func (s *store) CloneNodesForRun(ctx context.Context, dagKey string, runID int64) (int64, error) {
	return queryValue(func() (int64, error) {
		return s.q.CloneTaskDagNodesForRun(ctx, sqlc.CloneTaskDagNodesForRunParams{
			DagKey: dagKey,
			RunID:  int64Ptr(runID),
		})
	}, "clone_nodes_for_run", "task_dag_node")
}

func (s *store) PromoteRootNodesToReady(ctx context.Context, dagKey string, runID int64) (int64, error) {
	return queryValue(func() (int64, error) {
		return s.q.PromoteRootNodesToReady(ctx, sqlc.PromoteRootNodesToReadyParams{
			DagKey: dagKey,
			RunID:  int64Ptr(runID),
		})
	}, "promote_root_nodes_to_ready", "task_dag_node")
}

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

func terminateRunTx(ctx context.Context, txq *sqlc.Queries, input TerminateRunInput, reason string, event []byte) ([]string, error) {
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
	if _, err := txq.CancelTaskDagRun(ctx, sqlc.CancelTaskDagRunParams{
		Event:  event,
		DagKey: input.DagKey,
		RunID:  input.RunID,
		RunKey: input.RunKey,
	}); err != nil {
		return terminateRunAlreadyCancelled(ctx, txq, input, err)
	}
	return runSpawnedThreadIDs(ctx, txq, input.DagKey, input.RunID)
}

func terminateRunAlreadyCancelled(ctx context.Context, txq *sqlc.Queries, input TerminateRunInput, cancelErr error) ([]string, error) {
	if errors.Is(cancelErr, sql.ErrNoRows) {
		threadIDs, loadErr := cancelledRunSpawnedThreadIDs(ctx, txq, input)
		if loadErr == nil {
			return threadIDs, nil
		}
	}
	return nil, fmt.Errorf("cancel run: %w", cancelErr)
}

func cancelledRunSpawnedThreadIDs(ctx context.Context, q *sqlc.Queries, input TerminateRunInput) ([]string, error) {
	run, err := q.GetTaskDagRun(ctx, sqlc.GetTaskDagRunParams{RunKey: input.RunKey})
	if err != nil {
		return nil, err
	}
	if run.ID != input.RunID || run.DagKey != input.DagKey || run.Status != "cancelled" {
		return nil, sql.ErrNoRows
	}
	return runSpawnedThreadIDs(ctx, q, input.DagKey, input.RunID)
}

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

// fromTaskDagRun 把 sqlc 生成的行结构体转成 contract 层 Run。
// SQLite epoch milliseconds map to time.Time; nullable columns map to nil.
func fromTaskDagRun(row sqlc.TaskDagRun) Run {
	return Run{
		ID:                 row.ID,
		RunKey:             row.RunKey,
		DagKey:             row.DagKey,
		DagVersionSnapshot: row.DagVersionSnapshot,
		TriggerSource:      row.TriggerSource,
		Status:             row.Status,
		StartedAt:          timeValue(row.StartedAt),
		FinishedAt:         nullableTime(row.FinishedAt),
		Events:             json.RawMessage(row.Events),
		BudgetUsed:         row.BudgetUsed,
		BudgetLimit:        nullableInt64(row.BudgetLimit),
		Metadata:           json.RawMessage(row.Metadata),
		CreatedAt:          timeValue(row.CreatedAt),
		UpdatedAt:          timeValue(row.UpdatedAt),
	}
}

func budgetLimitToInt8(v *int64) *int64 {
	return v
}

func nullableTime(v *int64) *time.Time {
	return timestampPtr(v)
}

func nullableInt64(v *int64) *int64 {
	return v
}

// WithRunTx 起单一 PG 事务，fn 拿到的 tx RunStore 是
// 事务绑定的 *store 运行时实例，所有 RunStore 方法调用都在同事务内。
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

func (s *store) WithScheduledStartTx(ctx context.Context, fn func(tx ScheduledStartTxStore) error) error {
	return wrapTaskDAGError(sqlctx.WithTx(ctx, s.db, s.q, func(txq *sqlc.Queries, tx sqlc.DBTX) error {
		return fn(&store{db: tx, q: txq})
	}), "with_scheduled_start_tx", "task_dag_run")
}

func (s *store) UpdateScheduledDAGNextRun(ctx context.Context, dagKey string, dueAt, nextRunAt time.Time) (int64, error) {
	return s.q.UpdateTaskDagNextRun(ctx, sqlc.UpdateTaskDagNextRunParams{
		NextRunAt: sqlc.TimeValuePtr(&nextRunAt),
		DagKey:    dagKey,
		DueAt:     sqlc.TimeValuePtr(&dueAt),
	})
}
