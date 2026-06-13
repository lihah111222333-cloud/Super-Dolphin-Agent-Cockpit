package taskdag

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlctx"
)

// DAGOpsStore 的 *store 实现 —— task_dag_apply_ops 业务的 OCC 版本号 helper。
// 底层 SQL 放在 cmd/mcp-orch/sql/queries/task_dag_dag.sql，对应方法由本地
// sqlc 包承载；store 层只做领域错误包装和参数映射。

// GetDAGVersionForUpdate 拿当前 task_dags.version + FOR UPDATE 锁行。
// 调用方应在事务内调用，让 OCC 序列化生效。dag_key 不存在返回 sql ErrNoRows
// 经 wrapTaskDAGError 翻成 platformdb.IsNotFound。
func (s *store) GetDAGVersionForUpdate(ctx context.Context, dagKey string) (int64, error) {
	version, err := s.q.GetTaskDagVersionForUpdate(ctx, sqlc.GetTaskDagVersionForUpdateParams{DagKey: dagKey})
	if err != nil {
		return 0, wrapTaskDAGError(err, "get_version_for_update", "task_dag")
	}
	return version, nil
}

// GetDAGVersion 是 GetDAGVersionForUpdate 的只读版本：不加任何锁，不需事务。
// 专为「空 ops 短路」场景设计：调用方面拿当前版本号判定 base_version 是否同庄，
// 但没有后续写操作，不需要 FOR UPDATE 的序列化代价（R3 P2 #3）。
func (s *store) GetDAGVersion(ctx context.Context, dagKey string) (int64, error) {
	version, err := s.q.GetTaskDagVersion(ctx, sqlc.GetTaskDagVersionParams{DagKey: dagKey})
	if err != nil {
		return 0, wrapTaskDAGError(err, "get_version", "task_dag")
	}
	return version, nil
}

// CountRunningRunsByDagKey is used by ApplyOps after GetDAGVersionForUpdate has
// locked the DAG row. This is not a StartDAG pre-check; it is part of the
// template mutation transaction that protects running executions.
func (s *store) CountRunningRunsByDagKey(ctx context.Context, dagKey string) (int64, error) {
	return queryValue(func() (int64, error) {
		return s.q.CountActiveTaskDagRunsByKey(ctx, sqlc.CountActiveTaskDagRunsByKeyParams{DagKey: dagKey})
	}, "count_running", "task_dag_run")
}

// GetDAGSchedule reads the scheduling columns. ApplyOps calls this after
// GetDAGVersionForUpdate has locked the row in the same transaction.
func (s *store) GetDAGSchedule(ctx context.Context, dagKey string) (DAGSchedule, error) {
	row, err := s.q.GetTaskDagSchedule(ctx, sqlc.GetTaskDagScheduleParams{DagKey: dagKey})
	if err != nil {
		return DAGSchedule{}, wrapTaskDAGError(err, "get_schedule", "task_dag")
	}
	return DAGSchedule{Trigger: row.Trigger, CronExpr: row.CronExpr}, nil
}

// UpdateDAGPatch applies the F4.4 update_dag metadata whitelist under the
// caller's DAGOps transaction. Nil pointer fields mean "leave the column as-is";
// empty strings are deliberate values and are written through.
func (s *store) UpdateDAGPatch(ctx context.Context, input UpdateDAGPatchInput) (int64, error) {
	rows, err := s.q.UpdateTaskDagPatch(ctx, sqlc.UpdateTaskDagPatchParams{
		DagKey:          input.DagKey,
		Title:           nullableTextArg(input.Title),
		Description:     nullableTextArg(input.Description),
		Trigger:         nullableTextArg(input.Trigger),
		CronExpr:        nullableTextArg(input.CronExpr),
		OwnerID:         nullableTextArg(input.OwnerID),
		ScheduleEnabled: nullableBoolArg(input.ScheduleEnabled),
		NextRunAt:       nullableTimeArg(input.NextRunAt),
	})
	if err != nil {
		return 0, wrapTaskDAGError(err, "update_patch", "task_dag")
	}
	return rows, nil
}

func nullableBoolArg(value *bool) interface{} {
	if value == nil {
		return nil
	}
	if *value {
		return int64(1)
	}
	return int64(0)
}

func nullableTextArg(value *string) *string {
	return value
}

func nullableTimeArg(value *time.Time) *int64 {
	return sqlc.TimeValuePtr(value)
}

// BumpDAGVersion 把 task_dags.version 从 expectedVersion 推到 expectedVersion+1。
// 受影响行数 0 → expected 与 actual 不匹配（OCC 冲突），返回 nil error +
// version=0 由上层用「row not found」语义判断（注：用 RETURNING 的 :one
// 在 0 行时返 ErrNoRows，上层解读成 OCC 失配）。
//
// expected/actual 不匹配 → sql ErrNoRows 包成 IsNotFound；service 层把
// 它翻成 ErrVersionConflict。
func (s *store) BumpDAGVersion(ctx context.Context, dagKey string, expectedVersion int64) (int64, error) {
	newVersion, err := s.q.BumpTaskDagVersion(ctx, sqlc.BumpTaskDagVersionParams{
		DagKey:          dagKey,
		ExpectedVersion: expectedVersion,
	})
	if err != nil {
		return 0, wrapTaskDAGError(err, "bump_version", "task_dag")
	}
	return newVersion, nil
}

// WithDAGOpsTx 是 DAGOpsTxRunner 接口的 *store 实现。复用事务 helper
// 起 PG 事务，把 fn 拿到的 store 跨上事务 *sqlc.Queries，让 fn 内调
// GetDAGVersionForUpdate / UpsertNode / BumpDAGVersion 同事务串起来。
func (s *store) WithDAGOpsTx(ctx context.Context, fn func(tx DAGOpsStore) error) error {
	return wrapTaskDAGError(sqlctx.WithTx(ctx, s.db, s.q, func(txq *sqlc.Queries, tx sqlc.DBTX) error {
		return fn(&store{db: tx, q: txq})
	}), "with_dag_ops_tx", "task_dag")
}
