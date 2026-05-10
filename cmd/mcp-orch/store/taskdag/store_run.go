package taskdag

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// CreateRun 是 RunStore.CreateRun 的实现：StartDAG 阶段插入新 run 记录。
// 入参 input.RunKey 必须由调用方保证 UNIQUE（service 层根据 dag_key +
// 时间戳 [+ idempotency_key] 生成）；UNIQUE 冲突会被 wrapTaskDAGError
// 包成 task_dag_run 域错误向上传。
func (s *store) CreateRun(ctx context.Context, input CreateRunInput) (*Run, error) {
	metadata := input.Metadata
	if metadata == nil {
		metadata = json.RawMessage("null")
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

// CountActiveRunsByDagKey 用于 StartDAG 多 run 并发 reject（T1.2-mid 限制）。
// 返回 status='running' 的 run 数。F6.5 升级 multi-run 后此方法不再被
// StartDAG 调用，但保留作为运维 / 监控用途。
func (s *store) CountActiveRunsByDagKey(ctx context.Context, dagKey string) (int64, error) {
	return queryValue(func() (int64, error) {
		return s.q.CountActiveTaskDagRunsByKey(ctx, dagKey)
	}, "count_active", "task_dag_run")
}

// PromoteRootNodesToReady 把 dag_key 下所有 depends_on=[] 且 status='pending'
// 的根节点提升为 'ready'。返回受影响行数（service 层用于断言至少一个根节点
// 被提升，否则视为 DAG 无可执行起点 → 报错 / 警告）。
func (s *store) PromoteRootNodesToReady(ctx context.Context, dagKey string) (int64, error) {
	return queryValue(func() (int64, error) {
		return s.q.PromoteRootNodesToReady(ctx, dagKey)
	}, "promote_root_nodes_to_ready", "task_dag_node")
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
