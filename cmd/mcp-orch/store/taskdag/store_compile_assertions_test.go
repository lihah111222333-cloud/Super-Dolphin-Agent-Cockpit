package taskdag

// 编译期接口断言 — 守护 *store 实现各 narrow port 接口的契约。
//
// 这些 var 在编译期断言 *store 实现各对外接口；任何接口扩张但 *store 没
// 同步更新方法集会立即编译失败、错误信息原生定位到具体缺失方法（如
// "*store does not implement RunStore (missing method Foo)"），比 AST 文本扫
// 守卫更早期、更精确。
//
// L3 根治后取代 internal/archtest/dag_v2_runstore_pending_test.go：
//   - 旧守卫用 AST 扫 store_run.go 找 *store receiver 上的方法名列表，仅
//     检查方法存在不查签名一致性；
//   - 编译期 var 断言更严：方法名 + 参数类型 + 返回类型一致性都查；
//   - 同包 (_test.go 用 package taskdag 而非 taskdag_test) 让我们能引用
//     unexported *store。
//
// 维护：未来扩 RunStore / OrchestrationStore / ... 接口时，编译失败这里立
// 即指明 *store 缺啥方法。新加 narrow port 也加一行断言。
var (
	_ OrchestrationStore       = (*store)(nil)
	_ DAGMutationStore         = (*store)(nil)
	_ DAGOpsStore              = (*store)(nil)
	_ DAGOpsTxRunner           = (*store)(nil)
	_ DAGVersionReader         = (*store)(nil)
	_ DAGReadStore             = (*store)(nil)
	_ DAGDetailStore           = (*store)(nil)
	_ NodeStatusStore          = (*store)(nil)
	_ NodeConfigPatchStore     = (*store)(nil)
	_ SmartRetryConfigStore    = (*store)(nil)
	_ WakeupNodeFailureStore   = (*store)(nil)
	_ DAGLockStore             = (*store)(nil)
	_ RunStore                 = (*store)(nil)
	_ ScheduledStartTxStore    = (*store)(nil)
	_ ScheduledStartStore      = (*store)(nil)
	_ RunTerminationStore      = (*store)(nil)
	_ RunNodeReadStore         = (*store)(nil)
	_ RecoveryStore            = (*store)(nil)
	_ RunningNodeStore         = (*store)(nil)
	_ NodeFlowStore            = (*store)(nil)
	_ NodeSpawnRecorderStore   = (*store)(nil)
	_ NodeSpawningThreadLookup = (*store)(nil)
	_ DispatchNodeStore        = (*store)(nil)
	_ WakeupStore              = (*store)(nil)
	_ WakeupLeaseRenewer       = (*store)(nil)
	_ WorkerLeaseStore         = (*store)(nil)
	_ Store                    = (*store)(nil)
)
