package taskdag

import (
	"context"
	"time"

	"github.com/anthropic-ai/super-agent-v3/cmd/mcp-orch/store/sqlc"
)

// DAGOpsStore 的 *store 实现 —— task_dag_apply_ops 业务的 OCC 版本号 helper。
//
// 走原生 q.db SQL 而非 sqlc generate：task_dags.version 列已存在
// （migration 0072），但 sqlc 1.30 生成出的 TaskDag 模型还没包含此列
// （等待团队整体 sqlc 重生成）。F4.1 不引入 sqlc realignment，避免触发
// 跨模块改动；改在 store 内部 SQL 直查 version 列规避。F4.x 完整落地后
// 跑一次 sqlc generate 把这些 ad-hoc SQL 收敛回 sqlc query 即可。
//
// DAGOpsStore implementation for *store. Uses raw q.db SQL instead of sqlc
// queries: column `version` exists since migration 0072 but sqlc-generated
// models haven't been re-aligned yet, so F4.1 keeps the schema sync deferred
// to the dedicated regen task.

// GetDAGVersionForUpdate 拿当前 task_dags.version + FOR UPDATE 锁行。
// 调用方应在事务内调用，让 OCC 序列化生效。dag_key 不存在返回 sql ErrNoRows
// 经 wrapTaskDAGError 翻成 platformdb.IsNotFound。
func (s *store) GetDAGVersionForUpdate(ctx context.Context, dagKey string) (int64, error) {
	const q = `SELECT version FROM task_dags WHERE dag_key = $1 FOR UPDATE`
	row := sqlcDB(s.q).QueryRow(ctx, q, dagKey)
	var version int64
	if err := row.Scan(&version); err != nil {
		return 0, wrapTaskDAGError(err, "get_version_for_update", "task_dag")
	}
	return version, nil
}

// GetDAGVersion 是 GetDAGVersionForUpdate 的只读版本：不加任何锁，不需事务。
// 专为「空 ops 短路」场景设计：调用方面拿当前版本号判定 base_version 是否同庄，
// 但没有后续写操作，不需要 FOR UPDATE 的序列化代价（R3 P2 #3）。
func (s *store) GetDAGVersion(ctx context.Context, dagKey string) (int64, error) {
	const q = `SELECT version FROM task_dags WHERE dag_key = $1`
	row := sqlcDB(s.q).QueryRow(ctx, q, dagKey)
	var version int64
	if err := row.Scan(&version); err != nil {
		return 0, wrapTaskDAGError(err, "get_version", "task_dag")
	}
	return version, nil
}

// CountRunningRunsByDagKey is used by ApplyOps after GetDAGVersionForUpdate has
// locked the DAG row. This is not a StartDAG pre-check; it is part of the
// template mutation transaction that protects running executions.
func (s *store) CountRunningRunsByDagKey(ctx context.Context, dagKey string) (int64, error) {
	return queryValue(func() (int64, error) {
		return s.q.CountActiveTaskDagRunsByKey(ctx, dagKey)
	}, "count_running", "task_dag_run")
}

// GetDAGSchedule reads the scheduling columns omitted by the current sqlc
// TaskDag model. ApplyOps calls this after GetDAGVersionForUpdate has locked
// the row in the same transaction.
func (s *store) GetDAGSchedule(ctx context.Context, dagKey string) (DAGSchedule, error) {
	const q = `SELECT trigger, cron_expr FROM task_dags WHERE dag_key = $1`
	row := sqlcDB(s.q).QueryRow(ctx, q, dagKey)
	var schedule DAGSchedule
	if err := row.Scan(&schedule.Trigger, &schedule.CronExpr); err != nil {
		return DAGSchedule{}, wrapTaskDAGError(err, "get_schedule", "task_dag")
	}
	return schedule, nil
}

// UpdateDAGPatch applies the F4.4 update_dag metadata whitelist under the
// caller's DAGOps transaction. Nil pointer fields mean "leave the column as-is";
// empty strings are deliberate values and are written through.
func (s *store) UpdateDAGPatch(ctx context.Context, input UpdateDAGPatchInput) (int64, error) {
	const q = `
-- name: UpdateTaskDagPatch :execrows
UPDATE task_dags
SET title = COALESCE($2, title),
    description = COALESCE($3, description),
    trigger = COALESCE($4, trigger),
    cron_expr = COALESCE($5, cron_expr),
    owner_id = COALESCE($6, owner_id),
	    next_run_at = CASE
	      WHEN $8::boolean IS NOT NULL AND $8::boolean = FALSE THEN NULL
	      WHEN COALESCE($4, trigger) = 'scheduled'
	        AND COALESCE($5, cron_expr) <> ''
	      THEN CASE
	        WHEN $8::boolean IS NOT NULL AND $8::boolean = TRUE THEN COALESCE($7, next_run_at)
	        WHEN $4 IS NOT NULL OR $5 IS NOT NULL OR next_run_at IS NULL THEN COALESCE($7, next_run_at)
	        ELSE next_run_at
	      END
	      ELSE NULL
    END,
    updated_at = NOW()
WHERE dag_key = $1`
	tag, err := sqlcDB(s.q).Exec(ctx, q,
		input.DagKey,
		nullableStringArg(input.Title),
		nullableStringArg(input.Description),
		nullableStringArg(input.Trigger),
		nullableStringArg(input.CronExpr),
		nullableStringArg(input.OwnerID),
		nullableTimeArg(input.NextRunAt),
		nullableBoolArg(input.ScheduleEnabled),
	)
	if err != nil {
		return 0, wrapTaskDAGError(err, "update_patch", "task_dag")
	}
	return tag.RowsAffected(), nil
}

func nullableBoolArg(value *bool) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableStringArg(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableTimeArg(value *time.Time) any {
	if value == nil {
		return nil
	}
	return *value
}

// BumpDAGVersion 把 task_dags.version 从 expectedVersion 推到 expectedVersion+1。
// 受影响行数 0 → expected 与 actual 不匹配（OCC 冲突），返回 nil error +
// version=0 由上层用「row not found」语义判断（注：用 RETURNING 的 :one
// 在 0 行时返 ErrNoRows，上层解读成 OCC 失配）。
//
// expected/actual 不匹配 → sql ErrNoRows 包成 IsNotFound；service 层把
// 它翻成 ErrVersionConflict。
func (s *store) BumpDAGVersion(ctx context.Context, dagKey string, expectedVersion int64) (int64, error) {
	const q = `
UPDATE task_dags
SET version = version + 1,
    updated_at = NOW()
WHERE dag_key = $1 AND version = $2
RETURNING version`
	row := sqlcDB(s.q).QueryRow(ctx, q, dagKey, expectedVersion)
	var newVersion int64
	if err := row.Scan(&newVersion); err != nil {
		return 0, wrapTaskDAGError(err, "bump_version", "task_dag")
	}
	return newVersion, nil
}

// sqlcDB 暴露 *sqlc.Queries 内部 db 字段供本文件做原生 SQL 调用。
// 同包访问 unexported 字段，无需破坏 sqlc 生成代码（generated db.go 不动）。
// F4.x 完整完成后随 sqlc regen 一并干掉。
func sqlcDB(q *sqlc.Queries) sqlc.DBTX {
	return sqlc.QueriesDB(q)
}

// WithDAGOpsTx 是 DAGOpsTxRunner 接口的 *store 实现。复用 sqlc.WithTx
// 起 PG 事务，把 fn 拿到的 store 跨上事务 *sqlc.Queries，让 fn 内调
// GetDAGVersionForUpdate / UpsertNode / BumpDAGVersion 同事务串起来。
func (s *store) WithDAGOpsTx(ctx context.Context, fn func(tx DAGOpsStore) error) error {
	return wrapTaskDAGError(sqlc.WithTx(ctx, s.q, func(txq *sqlc.Queries) error {
		return fn(&store{q: txq})
	}), "with_dag_ops_tx", "task_dag")
}
