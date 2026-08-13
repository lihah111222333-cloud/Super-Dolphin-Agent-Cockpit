# ADR-012：taskdag 聚合 Store 接口 7/7 嵌入端口预算锁死

> 状态：✅ Accepted | 日期：2026-05-12 | 决策者：项目维护者 | 相关：`cmd/mcp-orch/store/taskdag/contract.go`、`internal/archtest/store_interface_isolation_test.go`、ADR-009（F1.5 NodeSpawnRecorderStore 作为窄端口独立挂的先例）

## 1. 背景

`cmd/mcp-orch/store/taskdag.Store` 是 DAG / Run / Wakeup / Worker lease 等持久化能力的**向后兼容聚合接口**。该接口当前嵌入 **7 个窄端口**，已顶到 archtest `InterfaceIsolation` 预算上限：

```go
type Store interface {
    OrchestrationStore
    DAGMutationStore
    DAGLockStore
    RunningNodeStore
    NodeFlowStore
    WakeupStore
    WorkerLeaseStore
}
```

archtest 预算（`internal/archtest/store_interface_isolation_test.go`）：聚合接口允许 `<=0 direct` 方法 + `<=7 embedded ports`。再加 1 个嵌入即破预算。

F1.5（commit `970cb5aa`）已用真实先例：`NodeSpawnRecorderStore` **没有嵌入** `Store`，而是作为独立窄接口由 `service` 单独持有 + 编译期 `var _ NodeSpawnRecorderStore = (*store)(nil)` 守护类型一致。同理 `RunStore`（commit eb341e54）、`DAGOpsStore`（F4.1 / F4.2）也是这条路径。

## 2. 决策

**禁止再向 `taskdag.Store` 聚合接口添加新 embedded port。**

新增 Store 端口必须满足三条：

1. **窄端口独立挂载**：新接口以独立类型声明，**不嵌入 `Store`** 也不嵌入其他已嵌入 `Store` 的 port（避免间接撑爆预算）。
2. **编译期 `var _` 断言**：在 `cmd/mcp-orch/store/taskdag/store_compile_assertions_test.go` 加 `var _ NewStore = (*store)(nil)`，确保同一 `*store` concrete 同时满足新接口与现有接口。
3. **Module 单独 `fx.Provide`**：在 `cmd/mcp-orch/store/taskdag/module.go` 用独立 `ProvideXxxStore` 把 `*store` type-assert 出新接口，让 service 层只依赖窄端口。

## 3. 替代方案与拒绝理由

- **方案 B**：把新端口嵌入 `DAGMutationStore` 等子聚合 → 拒绝。会让 `DAGMutationStore` 的 `embedded` 计数继续涨，把预算压力下放到子聚合层，迟早再爆。
- **方案 C**：放宽 archtest 预算（`<=7` → `<=8`） → 拒绝。预算放宽一次就再难收回；接口隔离原则被「再加一个不算多」蚕食。F1.5 follow-up `970cb5aa` 教训：预算紧才会把端口拆细，拆细才能让 mock / 测试边界清晰。
- **方案 D**：拆 `Store` 聚合接口 → 拒绝（暂时）。聚合接口被 module 装配与低层 store 测试依赖，拆分需要先列改用面。本 ADR 仅冻结新增；旧聚合保留向后兼容。

## 4. 适用范围

- 文件：`cmd/mcp-orch/store/taskdag/contract.go`
- 守卫：`internal/archtest/store_interface_isolation_test.go` 的 `TestInterfaceIsolationBudgets`
- 历史先例：`NodeSpawnRecorderStore`（F1.5）、`RunStore`（commit eb341e54）、`DAGOpsStore`（F4.1 / F4.2）、`DAGOpsTxRunner`（F4.2）

## 5. 触发再议条件

- 若发现 `Store` 聚合接口本身被 service 直接持有 ≥3 处 → 触发方案 D（拆聚合）评估。
- 若某个 embedded port 拆出后 mock / 测试桩复杂度反而失控 → 触发方案 B 与方案 D 的二次评估，但仍**不**走方案 C（放宽预算）。
