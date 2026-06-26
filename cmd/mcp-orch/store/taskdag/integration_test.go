package taskdag

import (
	"os"
	"testing"
)

// integration_test.go 保留 taskdag PostgreSQL 集成测试的待接线骨架。
//
// 当前所有 taskdag 测试都依赖 fake DB（不上 PG，pq 行锁 / partial index /
// CTE NULL / `ON CONFLICT DO UPDATE` / 并发 UPDATE 真实语义都未验）。本文件
// 给出三组关键 case 的占位骨架：默认 `t.Skip` 不跑；当
// 环境变量 `TASKDAG_INTEGRATION=1` 显式打开时再真起 PG。
//
// 长期方案：补 testcontainers-go (`github.com/testcontainers/testcontainers-go`)
// + 起 PG 容器 → 应用 schema → 跑 SQL → 断言。本仓库还没引入该依赖，
// 所以这里只挂占位 + 「打开开关」说明，避免无声忘记。
//
// 三组关键 case 覆盖：
//
//  1) task_dag_node_spawning_thread.sql CTE 首次写入与重试追加事件。
//     fake DB 测不到 NULL 分支、同事务 append events 以及无 running run 时的真实 SQL 行为。
//
//  2) CompleteNode 同事务 promote pending→ready 的并发行锁。
//     两个 goroutine 完成同一节点的不同上游分支时，行锁应让 promote 串行；fake DB 无行锁语义。
//
//  3) GetDAGVersionForUpdate + BumpDAGVersion 的 OCC 行为。
//     两个事务同时 SELECT FOR UPDATE 时，后到的 bump 应因 version 改变返回冲突。
//
// 打开方式：
//
//	TASKDAG_INTEGRATION=1 go test ./cmd/mcp-orch/store/taskdag/... -run TestTaskDagPGIntegration
//
// 实际执行 testcontainer PG 容器需补依赖与 schema apply helper。本占位
// 文件优先存在的目的：让 reviewer 在 PR 期间能扫到「集成测试还没补」的
// flag，避免与 fake DB 测试混淆视为已覆盖。
//
// CI gate：CI 暂不设 TASKDAG_INTEGRATION=1，等 testcontainers-go 依赖落地
// + schema apply helper 完成后再切。

func requireIntegrationEnv(t *testing.T) {
	t.Helper()
	if os.Getenv("TASKDAG_INTEGRATION") != "1" {
		t.Skip("requires PG; set TASKDAG_INTEGRATION=1 to enable (also needs testcontainers-go support, not yet wired)")
	}
}

// 该占位测试覆盖 CTE 首次写入与重试追加事件的真实 SQL 语义。
// task_dag_node_spawning_thread.sql CTE 首次/重试/无 running run 三 case。
//
// 真实 case 待 testcontainers-go 引入后填：
//   - 首次：spawning_thread_id 旧值 NULL → 写值、不 append event
//   - 重试：spawning_thread_id 旧值非 NULL → 同事务 append 一条
//     `{kind: "node_spawn", prev_thread_id, thread_id}` 到 task_dag_runs.events
//   - 无 running run：retry 路径下 task_dag_runs 不存在或非 running → 静默
//     吞 0 行，不报错
func TestTaskDagPGIntegrationF15CTEFirstRecord(t *testing.T) {
	requireIntegrationEnv(t)
	t.Fatalf("not implemented: testcontainer PG harness 未引入；详 file-top 注释 / R2 P1")
}

// 该占位测试覆盖同事务下游推进的并发行锁语义。
//
// 真实 case 待 testcontainers-go 引入后填：
//   - 起一个 PG 容器，应用包含 PromoteSingleNodePendingToReady 的 schema
//   - 两个 goroutine 各自 begin TX 并完成同一 DAG 不同 sibling 节点
//   - 断言下游 pending 节点最终 status=ready，没出现 race-cond 重复 promote
func TestTaskDagPGIntegrationF63PromoteSameTxRace(t *testing.T) {
	requireIntegrationEnv(t)
	t.Fatalf("not implemented: testcontainer PG harness 未引入；详 file-top 注释 / R2 P1")
}

// 该占位测试覆盖 DAG 版本 CAS 与 SELECT FOR UPDATE 的串行化。
//
// 真实 case 待 testcontainers-go 引入后填：
//   - 两个 TX 同 SELECT FOR UPDATE 锁同一 task_dags 行
//   - 其中一个 BumpDAGVersion 成功 (version += 1)，另一个 BumpDAGVersion
//     应返 sql.ErrNoRows → wrapped 成 platformdb.IsNotFound → service
//     层 ErrVersionConflict
func TestTaskDagPGIntegrationF41OCCSelectForUpdate(t *testing.T) {
	requireIntegrationEnv(t)
	t.Fatalf("not implemented: testcontainer PG harness 未引入；详 file-top 注释 / R2 P1")
}
