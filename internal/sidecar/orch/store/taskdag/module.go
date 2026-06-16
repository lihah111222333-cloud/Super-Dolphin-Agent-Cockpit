package taskdag

import (
	"database/sql"

	"go.uber.org/fx"
)

// Module is part of the taskdag package API.
var Module = fx.Module("store.taskdag",
	fx.Provide(
		NewStoreFromDB,
		ProvideOrchestrationStore,
		// ProvideRunStore 把同一 *store 实例并行登记为 RunStore binding，
		// 修复 RunStore wiring bug：消费方 (orchestration.serviceParams) 可命中此 provider。
		// 编译期由 store_compile_assertions_test.go 的 var _ RunStore = (*store)(nil) 守住。
		ProvideRunStore,
		ProvideScheduledStartStore,
		// ProvideDispatchNodeStore 把同一 *store 作为 DispatchNodeStore (task_dispatch_node)。
		// 编译期同样由 store_compile_assertions_test.go 守住。
		ProvideDispatchNodeStore,
		// ProvideNodeSpawnRecorderStore 把同一 *store 作为 NodeSpawnRecorderStore (F1.5)。
		// W1 fx wiring 依赖该 narrow port 构造 NodeExecutorRouter。编译期由
		// store_compile_assertions_test.go 中 var _ NodeSpawnRecorderStore = (*store)(nil) 守住。
		ProvideNodeSpawnRecorderStore,
		// ProvideNodeSpawningThreadLookup 把同一 *store 作为 NodeSpawningThreadLookup（ADR-017 §2.2），
		// DAG turn.completed subscriber + hookConsumer thread.stopped fallback 复用
		// 该窄端口反查 spawning_thread_id。编译期由 store_compile_assertions_test.go
		// 中 var _ NodeSpawningThreadLookup = (*store)(nil) 守住。
		ProvideNodeSpawningThreadLookup,
	),
)

// NewStoreFromDB 从 SQLite 连接创建 DAG 任务存储。
func NewStoreFromDB(db *sql.DB) Store {
	return NewStore(db)
}

// ProvideNodeSpawnRecorderStore 从聚合 Store type-assert 出 F1.5 / ADR-009
// nodeexec.AgentExecutor 写回 spawning_thread_id 所需的窄端口 NodeSpawnRecorderStore。
// 断言安全性由 store_compile_assertions_test.go 静态守住。
func ProvideNodeSpawnRecorderStore(store Store) NodeSpawnRecorderStore {
	return store.(NodeSpawnRecorderStore)
}

// ProvideNodeSpawningThreadLookup 从聚合 Store 中取出节点 spawning 线程反查窄接口。
// DAG turn.completed subscriber 和 hookConsumer thread.stopped fallback 依赖该接口。
// 断言安全性由 store_compile_assertions_test.go 静态守住。
func ProvideNodeSpawningThreadLookup(store Store) NodeSpawningThreadLookup {
	return store.(NodeSpawningThreadLookup)
}

// ProvideOrchestrationStore 提供 orchestration DAG 存储窄接口。
func ProvideOrchestrationStore(store Store) OrchestrationStore { return store }

// ProvideRunStore 通过 type-assertion 从聚合 Store 中取出 RunStore 窄接口。
// 断言安全性：*store 编译期实现 RunStore（参 store_compile_assertions_test.go），
// 这里仅是接口收窄，运行时不会 panic。
func ProvideRunStore(store Store) RunStore { return store.(RunStore) }

// ProvideScheduledStartStore 提供 scheduled start DAG 存储窄接口。
func ProvideScheduledStartStore(store Store) ScheduledStartStore {
	return store.(ScheduledStartStore)
}

// ProvideDispatchNodeStore 从聚合 Store 中 type-assert 出 task_dispatch_node
// MCP 工具需要的窄接口 DispatchNodeStore。断言安全性同样由
// store_compile_assertions_test.go 守住。
func ProvideDispatchNodeStore(store Store) DispatchNodeStore { return store.(DispatchNodeStore) }
