package taskdag

import (
	"context"

	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlc"
	"github.com/lihah111222333-cloud/super-dolphin-agent/cmd/mcp-orch/store/sqlctx"
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
// 但没有后续写操作，不需要 FOR UPDATE 的序列化代价。
func (s *store) GetDAGVersion(ctx context.Context, dagKey string) (int64, error) {
	version, err := s.q.GetTaskDagVersion(ctx, sqlc.GetTaskDagVersionParams{DagKey: dagKey})
	if err != nil {
		return 0, wrapTaskDAGError(err, "get_version", "task_dag")
	}
	return version, nil
}

// CountRunningRunsByDagKey 在模板变更事务内统计指定 DAG 的 running run 数。
// 调用方应已锁住 DAG 行；这里不是启动前预检，而是阻止运行中模板被改写的保护。
func (s *store) CountRunningRunsByDagKey(ctx context.Context, dagKey string) (int64, error) {
	return queryValue(func() (int64, error) {
		return s.q.CountActiveTaskDagRunsByKey(ctx, sqlc.CountActiveTaskDagRunsByKeyParams{DagKey: dagKey})
	}, "count_running", "task_dag_run")
}

// GetDAGSchedule 在模板变更事务中读取调度列。
// 调用方在同一事务内锁住 DAG 行后再读取，避免和计划更新并发错位。
func (s *store) GetDAGSchedule(ctx context.Context, dagKey string) (DAGSchedule, error) {
	row, err := s.q.GetTaskDagSchedule(ctx, sqlc.GetTaskDagScheduleParams{DagKey: dagKey})
	if err != nil {
		return DAGSchedule{}, wrapTaskDAGError(err, "get_schedule", "task_dag")
	}
	return DAGSchedule{Trigger: row.Trigger, CronExpr: row.CronExpr}, nil
}

// UpdateDAGPatch 在调用方 DAGOps 事务内更新允许变更的模板字段。
// nil 指针表示保留原列值，空字符串是显式写入值，不能被当成未提供。
func (s *store) UpdateDAGPatch(ctx context.Context, input UpdateDAGPatchInput) (int64, error) {
	var rows int64
	err := sqlctx.WithImmediateTxOrReuse(ctx, s.db, s.q, func(txq *sqlc.Queries, _ sqlc.DBTX) error {
		current, err := txq.GetTaskDag(ctx, sqlc.GetTaskDagParams{DagKey: input.DagKey})
		if err != nil {
			return err
		}
		rows, err = txq.UpdateTaskDagPatch(ctx, resolveDAGPatch(current, input))
		return requireSingleAffectedRow("update task_dag patch", rows, err)
	})
	return rows, wrapTaskDAGError(err, "update_patch", "task_dag")
}

// resolveDAGPatch 把 nil 保留语义解析成一组可直接写入的完整列值。
func resolveDAGPatch(current sqlc.GetTaskDagRow, input UpdateDAGPatchInput) sqlc.UpdateTaskDagPatchParams {
	trigger := valueOrCurrent(input.Trigger, current.Trigger)
	cronExpr := valueOrCurrent(input.CronExpr, current.CronExpr)
	return sqlc.UpdateTaskDagPatchParams{
		Title:             valueOrCurrent(input.Title, current.Title),
		Description:       valueOrCurrent(input.Description, current.Description),
		Trigger:           trigger,
		CronExpr:          cronExpr,
		OwnerID:           valueOrCurrent(input.OwnerID, current.OwnerID),
		ResolvedNextRunAt: resolveNextRunAt(current.NextRunAt, input, trigger, cronExpr),
		DagKey:            input.DagKey,
	}
}

// valueOrCurrent 在补丁字段缺失时保留数据库当前值。
func valueOrCurrent(value *string, current string) string {
	if value != nil {
		return *value
	}
	return current
}

// resolveNextRunAt 在 Go 层等价复现旧 SQL CASE：禁用或非有效计划时清空，
// 有新计划时间时覆盖，否则保留当前计划时间。
func resolveNextRunAt(current *int64, input UpdateDAGPatchInput, trigger, cronExpr string) *int64 {
	if input.ScheduleEnabled != nil && !*input.ScheduleEnabled {
		return nil
	}
	if trigger != "scheduled" || cronExpr == "" {
		return nil
	}
	if input.NextRunAt != nil {
		return sqlc.TimeValuePtr(input.NextRunAt)
	}
	return current
}

// BumpDAGVersion 把 task_dags.version 从 expectedVersion 推到 expectedVersion+1。
// 受影响行数 0 被翻成 IsNotFound，service 层据此返回 ErrVersionConflict；
// 单行更新成功后直接返回确定的 expectedVersion+1。
func (s *store) BumpDAGVersion(ctx context.Context, dagKey string, expectedVersion int64) (int64, error) {
	_, err := queryValueWrite(ctx, func() (int64, error) {
		rows, err := s.q.BumpTaskDagVersion(ctx, sqlc.BumpTaskDagVersionParams{
			DagKey:          dagKey,
			ExpectedVersion: expectedVersion,
		})
		return rows, requireSingleAffectedRow("bump task_dag version", rows, err)
	}, "bump_version", "task_dag")
	if err != nil {
		return 0, err
	}
	return expectedVersion + 1, nil
}

// WithDAGOpsTx 是 DAGOpsTxRunner 接口的 *store 实现。
// 它用同一个 SQLite IMMEDIATE 事务重绑 sqlc 查询集，让版本读取、节点写入和版本 bump 串行提交。
func (s *store) WithDAGOpsTx(ctx context.Context, fn func(tx DAGOpsStore) error) error {
	return wrapTaskDAGError(sqlctx.WithImmediateTx(ctx, s.db, s.q, func(txq *sqlc.Queries, tx sqlc.DBTX) error {
		return fn(&store{db: tx, q: txq})
	}), "with_dag_ops_tx", "task_dag")
}
