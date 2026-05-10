package taskdag

import "go.uber.org/fx"

var Module = fx.Module("store.taskdag",
	fx.Provide(
		NewStore,
		ProvideOrchestrationStore,
		// ProvideRunStore 把同一 *store 实例并行登记为 RunStore binding，
		// 修复 RunStore wiring bug：消费方 (orchestration.serviceParams) 可命中此 provider。
		// 编译期由 store_compile_assertions_test.go 的 var _ RunStore = (*store)(nil) 守住。
		// ProvideRunStore registers the same *store instance as the RunStore binding,
		// fixing the RunStore wiring bug where the consumer (orchestration.serviceParams)
		// could not resolve the RunStore dependency. The type assertion is statically
		// guarded by store_compile_assertions_test.go's var _ RunStore = (*store)(nil).
		ProvideRunStore,
	),
)

func ProvideOrchestrationStore(store Store) OrchestrationStore { return store }

// ProvideRunStore 通过 type-assertion 从聚合 Store 中取出 RunStore 窄接口。
// 断言安全性：*store 编译期实现 RunStore（参 store_compile_assertions_test.go），
// 这里仅是接口收窄，运行时不会 panic。
// ProvideRunStore narrows the aggregate Store down to RunStore via a type
// assertion. Safety is statically guaranteed by store_compile_assertions_test.go
// (var _ RunStore = (*store)(nil)); the assertion will not panic at runtime.
func ProvideRunStore(store Store) RunStore { return store.(RunStore) }
