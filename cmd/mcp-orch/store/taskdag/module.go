package taskdag

import (
	"database/sql"

	"go.uber.org/fx"
)

var Module = fx.Module("store.taskdag",
	fx.Provide(
		NewStoreFromDB,
		ProvideOrchestrationStore,
		ProvideDAGDetailStore,
		ProvideNodeFlowStore,
		ProvideWakeupStore,
		ProvideWakeupDispatchStore,
		ProvideWakeupReclaimStore,
		// ProvideRunStore 把同一 *store 实例并行登记为 RunStore binding，
		// 修复 RunStore wiring bug：消费方 (orchestration.serviceParams) 可命中此 provider。
		// 编译期由 store_compile_assertions_test.go 的 var _ RunStore = (*store)(nil) 守住。
		ProvideRunStore,
		ProvideScheduledStartStore,
		// ProvideDispatchNodeStore 把同一 *store 作为 DispatchNodeStore (task_dispatch_node)。
		// 编译期同样由 store_compile_assertions_test.go 守住。
		ProvideDispatchNodeStore,
		// ProvideNodeSpawnRecorderStore 把同一 *store 作为节点 spawn 写回窄端口。
		// NodeExecutorRouter 只依赖该接口写回 spawning_thread_id，编译期断言守住接口实现。
		ProvideNodeSpawnRecorderStore,
		// ProvideNodeSpawningThreadLookup 把同一 *store 作为 spawning_thread_id 反查窄端口。
		// turn.completed 订阅和 thread.stopped 兜底路径共用该接口，编译期断言守住实现。
		ProvideNodeSpawningThreadLookup,
	),
)

// NewStoreFromDB 从 SQLite 连接创建 DAG 任务存储。
func NewStoreFromDB(db *sql.DB) Store {
	return NewStore(db)
}

// ProvideNodeSpawnRecorderStore 从聚合 Store 中取出节点 spawn 写回窄接口。
// nodeexec.AgentExecutor 只需要写 spawning_thread_id；接口收窄由编译期断言守住。
func ProvideNodeSpawnRecorderStore(store Store) NodeSpawnRecorderStore {
	return store.(NodeSpawnRecorderStore)
}

// ProvideNodeSpawningThreadLookup 从聚合 Store 中取出节点 spawning 线程反查窄接口。
// turn.completed 订阅和 thread.stopped 兜底路径依赖该接口。
// 断言安全性由 store_compile_assertions_test.go 静态守住。
func ProvideNodeSpawningThreadLookup(store Store) NodeSpawningThreadLookup {
	return store.(NodeSpawningThreadLookup)
}

// ProvideOrchestrationStore 提供 orchestration DAG 存储窄接口。
func ProvideOrchestrationStore(store Store) OrchestrationStore { return store }

// ProvideDAGDetailStore 提供节点路由器读取 DAG 元数据所需的窄接口。
func ProvideDAGDetailStore(store Store) DAGDetailStore { return store }

// ProvideNodeFlowStore 提供 DAG 完成/失败下游推进所需的窄接口。
func ProvideNodeFlowStore(store Store) NodeFlowStore { return store }

// ProvideWakeupStore 提供 wakeup dispatcher 所需的窄接口。
func ProvideWakeupStore(store Store) WakeupStore { return store }

// ProvideWakeupDispatchStore 提供 dispatcher 投递和 smart retry 所需的组合窄接口。
func ProvideWakeupDispatchStore(store Store) WakeupDispatchStore { return store }

// ProvideWakeupReclaimStore 提供 wakeup 周期回收与半写节点恢复所需的组合窄接口。
// *store 的实现关系由 store_compile_assertions_test.go 守住。
func ProvideWakeupReclaimStore(store Store) WakeupReclaimStore {
	return store.(WakeupReclaimStore)
}

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
