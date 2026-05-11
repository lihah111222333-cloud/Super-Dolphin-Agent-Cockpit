package sqlc

// QueriesDB 暴露 *Queries 持有的 DBTX，让同 module 内其它包能直接跑原生 SQL。
// sqlc 生成代码 (db.go) 不能改，本 file 是 hand-written 旁路。
//
// 用途：cmd/mcp-orch/store/taskdag/store_dag_ops.go 在 F4.1 / F4.x 阶段
// 需要查 task_dags.version 这类「sqlc 模型尚未重生成所以查不到」的字段，
// 走这个 helper 直接调 DBTX.QueryRow。F4.x 完整落地后做一次 sqlc regen
// 收敛掉这些 ad-hoc SQL，再删本 file。
//
// QueriesDB exposes the DBTX held by *Queries so hand-written SQL inside the
// same module can run alongside generated queries. Used by F4.x ApplyOps to
// hit columns (e.g. task_dags.version) that the current sqlc-generated models
// don't yet expose; cleaned up once sqlc is regenerated.
func QueriesDB(q *Queries) DBTX {
	if q == nil {
		return nil
	}
	return q.db
}
