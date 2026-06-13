package taskdag

import (
	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"
)

var Module = fx.Module("store.taskdag",
	fx.Provide(
		NewStoreFromPool,
		ProvideOrchestrationStore,
		// ProvideRunStore 把同一 *store 实例并行登记为 RunStore binding，
		// 修复 RunStore wiring bug：消费方 (orchestration.serviceParams) 可命中此 provider。
		// 编译期由 store_compile_assertions_test.go 的 var _ RunStore = (*store)(nil) 守住。
		// ProvideRunStore registers the same *store instance as the RunStore binding,
		// fixing the RunStore wiring bug where the consumer (orchestration.serviceParams)
		// could not resolve the RunStore dependency. The type assertion is statically
		// guarded by store_compile_assertions_test.go's var _ RunStore = (*store)(nil).
		ProvideRunStore,
		ProvideScheduledStartStore,
		// ProvideDispatchNodeStore 报同一 *store 作为 DispatchNodeStore (task_dispatch_node)。
		// 编译期同样由 store_compile_assertions_test.go 守住。
		ProvideDispatchNodeStore,
		// ProvideNodeSpawnRecorderStore 报同一 *store 作为 NodeSpawnRecorderStore (F1.5)。
		// W1 fx wiring 依赖该 narrow port 构造 NodeExecutorRouter。编译期由
		// store_compile_assertions_test.go 中 var _ NodeSpawnRecorderStore = (*store)(nil) 守住。
		ProvideNodeSpawnRecorderStore,
		// ProvideNodeSpawningThreadLookup 报同一 *store 作为 NodeSpawningThreadLookup（ADR-017 §2.2），
		// DAG turn.completed subscriber + hookConsumer thread.stopped fallback 复用
		// 该窄端口反查 spawning_thread_id。编译期由 store_compile_assertions_test.go
		// 中 var _ NodeSpawningThreadLookup = (*store)(nil) 守住。
		ProvideNodeSpawningThreadLookup,
	),
)

// NewStoreFromPool 从pool创建存储。
func NewStoreFromPool(pool *pgxpool.Pool) Store {
	return NewStore(pool)
}

// ProvideNodeSpawnRecorderStore 从聚合 Store type-assert 出 F1.5 / ADR-009
// nodeexec.AgentExecutor 写回 spawning_thread_id 所需的窄端口 NodeSpawnRecorderStore。
//
// ProvideNodeSpawnRecorderStore narrows the aggregate Store to NodeSpawnRecorderStore
// (F1.5 / ADR-009, consumed by fxadapter.NewStoreNodeSpawnRecorder). Safety is
// statically guarded by store_compile_assertions_test.go.
func ProvideNodeSpawnRecorderStore(store Store) NodeSpawnRecorderStore {
	return store.(NodeSpawnRecorderStore)
}

// ProvideNodeSpawningThreadLookup narrows the aggregate Store to
// NodeSpawningThreadLookup (consumed by the DAG turn.completed subscriber /
// hookConsumer thread.stopped DAG fallback). Safety is statically guarded by
// store_compile_assertions_test.go.
// ProvideNodeSpawningThreadLookup 提供节点spawning线程lookup。
func ProvideNodeSpawningThreadLookup(store Store) NodeSpawningThreadLookup {
	return store.(NodeSpawningThreadLookup)
}

// ProvideOrchestrationStore 提供orchestration存储。
func ProvideOrchestrationStore(store Store) OrchestrationStore { return store }

// ProvideRunStore 通过 type-assertion 从聚合 Store 中取出 RunStore 窄接口。
// 断言安全性：*store 编译期实现 RunStore（参 store_compile_assertions_test.go），
// 这里仅是接口收窄，运行时不会 panic。
// ProvideRunStore narrows the aggregate Store down to RunStore via a type
// assertion. Safety is statically guaranteed by store_compile_assertions_test.go
// (var _ RunStore = (*store)(nil)); the assertion will not panic at runtime.
func ProvideRunStore(store Store) RunStore { return store.(RunStore) }

// ProvideScheduledStartStore 提供scheduled起点存储。
func ProvideScheduledStartStore(store Store) ScheduledStartStore {
	return store.(ScheduledStartStore)
}

// ProvideDispatchNodeStore 从聚合 Store 中 type-assert 出 task_dispatch_node
// MCP 工具需要的窄接口 DispatchNodeStore。断言安全性同样由
// store_compile_assertions_test.go 守住。
//
// ProvideDispatchNodeStore narrows the aggregate Store to DispatchNodeStore
// (consumed by service.DispatchNode). Safety is statically guarded.
func ProvideDispatchNodeStore(store Store) DispatchNodeStore { return store.(DispatchNodeStore) }
