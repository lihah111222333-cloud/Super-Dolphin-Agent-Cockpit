# 接口隔离拆分方案：Skill / LSP / TaskDAG

> 创建时间：2026-04-29
> 状态：✅ 文档自审通过；`taskdag.Store`、`skill.Service`、`gopls.Manager`、`manager.Manager` 拆分护栏均已落地
> 关联上下文：用户反馈“接口膨胀违反 ISP”，上一轮核查确认 `SkillInjectionPort` 已收敛到 2 个方法，其余 `skill.Service`、`gopls.Manager`、`manager.Manager`、`taskdag.Store` 仍偏胖。

## 1. 上下文摘要

本轮从线程 `thread-1777393967546-24` / provider thread `019dd4ef-a2a0-7b43-a449-63aa85b8b421` 恢复的结论：

| 接口 | 旧报告 | 当前核查 | 处理策略 |
|---|---:|---:|---|
| `skill.Service` | 47 | 24 个直接方法 + `ApprovalSource` 2 个等效方法 | 分阶段拆 reader / writer / approval / expand/resource 端口，先加预算护栏防回长 |
| `gopls.Manager` | 42 | 28 个方法 | 后续按 LSP 能力面拆 navigation / structure / edit / lifecycle / diagnostics |
| `manager.Manager` | 33 | 26 个方法 | 与 `gopls.Manager` 同步拆，工具层只依赖所需能力 |
| `SkillInjectionPort` | 30 | 2 个方法 | 已不作为拆分目标，只纳入“不回归”观察 |
| `taskdag.Store` | 29 | 29 个方法 | 第一批执行：按 DAG / node / running / wakeup / lease / recovery 拆窄端口 |

## 2. 拆分原则

1. **先消费者隔离，再删除兼容聚合面**：对外仍可保留 legacy composite，但生产调用点必须改用窄端口。
2. **先护栏，后大拆**：每一批拆分都要有 archtest 或脚本预算，防止新方法继续堆回胖接口。
3. **低风险优先**：`taskdag.Store` 当前引用面小，且职责边界清楚，作为第一批。
4. **不制造新协议壳**：拆出来的接口必须由真实调用需求驱动，不允许为了“看起来拆了”新增无消费者 facade。
5. **保持 Fx 图 fail-closed**：如果改注入类型，必须提供显式 adapter，并跑相关 Fx / package 测试。

## 3. 第一批：`taskdag.Store`

### 3.1 目标

把 29 方法聚合接口拆为以下端口：

| 端口 | 职责 | 预期消费者 |
|---|---|---|
| `OrchestrationStore` | DAG 创建/读取/状态更新所需最小能力 | `internal/sidecar/orch/orchestration` |
| `UnitOfWorkStore` | transaction boundary | `OrchestrationStore` 内嵌 |
| `DAGMutationStore` | tx 内 DAG / node upsert + detail reload | `CreateDAG` tx callback |
| `DAGReadStore` / `DAGDetailStore` | DAG list/detail 读取 | DAG RPC flow |
| `NodeStatusStore` | node status update | DAG RPC flow |
| `RecoveryStore` | running node recovery + wakeup lookup | recover flow |
| `RunningNodeStore` | turn/runtime node 状态流 | worker/runtime flow |
| `WakeupStore` | wakeup claim/send/retry/fail/reclaim | scheduler/wakeup flow |
| `WorkerLeaseStore` | worker lease acquire/renew/release | scheduler lease flow |
| `Store` | 兼容聚合面 | store module / low-level tests only |

### 3.2 第一批验收

- `internal/sidecar/orch/orchestration.service.dagStore` 不再依赖 `taskdag.Store`，改为 `taskdag.OrchestrationStore`。
- `serviceParams.DAGStore` 不再请求胖 `taskdag.Store`，Fx 通过 `ProvideOrchestrationStore` 显式适配。
- `taskdag.Store` 不再声明 29 个直接方法，只嵌入窄端口。
- `WithTx` callback 不再暴露完整 `Store`，只给 `DAGMutationStore`。

## 4. 护栏设计

新增 `internal/archtest/interface_isolation_guard_test.go`：

1. **接口预算护栏**：
   - `taskdag.Store` 直接方法必须为 0，只允许嵌入有限数量窄端口。
   - `taskdag.OrchestrationStore` 直接方法必须为 0，只能组合 `UnitOfWorkStore` / `DAGReadStore` / `NodeStatusStore`。
   - `skill.Service`、`gopls.Manager`、`manager.Manager` 暂按当前直接方法数做“不回长”预算，后续每拆一批就下调预算。
2. **消费者隔离护栏**：
   - `internal/sidecar/orch/orchestration.service.dagStore` 必须是 `taskdag.OrchestrationStore`。
   - `serviceParams.DAGStore` 必须是 `taskdag.OrchestrationStore`。

## 5. 自审清单

- [x] 拆分目标来自当前调用点，不是凭空新增端口。
- [x] 第一批只碰 `taskdag.Store`，避免同时撕开 Skill / LSP 两条大链路。
- [x] legacy `Store` 暂留，避免低层 store tests / Fx provider 大面积破坏。
- [x] 新 guard 能防止 `taskdag.Store` 重新堆直接方法。
- [x] 后续 Skill / LSP 拆分已有预算 guard，不会继续回长。
- [x] 第二批 `skill.Service` 已把 dashboard / prompt / turn / toolbridge 的生产消费面改为窄端口。

## 6. 第一批执行记录（2026-04-29）

已完成：

- `taskdag.Store` 改为兼容聚合接口，只嵌入窄端口，不再声明 29 个直接方法。
- 新增 `OrchestrationStore` / `DAGMutationStore` / `DAGReadStore` / `DAGDetailStore` / `NodeStatusStore` / `RecoveryStore` / `RunningNodeStore` / `WakeupStore` / `WorkerLeaseStore`。
- `internal/sidecar/orch/orchestration` 的 `dagStore` 字段、`serviceParams.DAGStore` 与 `withDAGStore` callback 已改为 `taskdag.OrchestrationStore`。
- `WithTx` callback 已从完整 `Store` 收窄到 `DAGMutationStore`。
- `taskdag.Module` 新增 `ProvideOrchestrationStore`，Fx 图通过显式 adapter 提供窄端口。
- 新增 archtest：`TestInterfaceIsolationBudgets`、`TestTaskDAGStoreConsumersUseNarrowPort`。

已验证：

- `go test ./internal/sidecar/orch/store/taskdag ./internal/sidecar/orch/orchestration ./internal/archtest -run 'Test(ReclaimStaleDispatchingWakeupsAllowsFreshClaimAndBlocksStaleCommit|InterfaceIsolationBudgets|TaskDAGStoreConsumersUseNarrowPort)' -count=1`
- `go test ./cmd/mcp-orch/... ./internal/archtest`
- `go test ./...`
- `git diff --check`

## 7. 第二批执行记录（2026-04-29）

已完成：

- `skill.Service` 改为兼容聚合接口，直接方法预算从 24 下调为 0；具体能力拆成窄端口。
- 新增/明确窄端口：`SkillCommandExecutor`、`SkillLister`、`SkillCatalogSource`、`SkillHydrationSource`、`SkillHostToolReader`、`SkillRevisionSource`、`TrustRevisionSource`，并保留 package-private mutation / remote / config / preview / legacy expand / candidate reviewer 子接口。
- `skill.Module` 新增显式 adapter：`ProvideSkillLister`、`ProvideSkillCatalogSource`、`ProvideSkillHydrationSource`、`ProvideSkillHostToolReader`。
- `dashboard` 从 `skillmodule.Service` 收窄到 `skillmodule.SkillLister`。
- `prompt` Fx deps 从 `skillpkg.Service` 收窄到 `skillpkg.SkillCatalogSource`。
- `turn` 构造器从 `skillpkg.Service` 收窄到 `skillpkg.SkillHydrationSource`。
- `toolbridge` host-direct registry 从 `skillpkg.Service` 收窄到 `skillpkg.SkillHostToolReader`。
- `internal/archtest/interface_isolation_guard_test.go` 增加 `TestSkillServiceConsumersUseNarrowPorts`，防止上述消费面退回胖接口。

已验证：

- `go test ./internal/module/skill ./internal/module/prompt ./internal/module/turn ./internal/module/dashboard ./internal/platform/toolbridge ./internal/archtest -run 'Test(InterfaceIsolationBudgets|SkillServiceConsumersUseNarrowPorts|NewConfig|SkillProgressiveDisclosure|SkillCatalogProvider|RegisterSkillCatalogProvider|.*Skill.*|.*HostTool.*)' -count=1`

## 8. 第三批执行记录（2026-04-29）

已完成：

- `internal/sidecar/lsp/manager.Manager` 改为兼容聚合接口，直接方法预算从 26 下调为 0。
- 新增能力端口：`LifecycleManager`、`NavigationManager`、`XRefManager`、`StructureManager`、`CompletionManager`、`EditManager`、`DocumentLifecycleManager`、`DiagnosticsManager`。
- `cmd/mcp-lsp/gopls.Manager` 改为组合 `ClientEnsurer`、`lspmanager.Manager`、`BackgroundRunnerProvider`，直接方法预算从 28 下调为 0。
- `internal/archtest/interface_isolation_guard_test.go` 已同步下调 `gopls.Manager` / `manager.Manager` 预算，防止大接口重新堆直接方法。

已验证：

- `go test ./cmd/mcp-lsp/... ./internal/archtest -run 'Test(InterfaceIsolationBudgets|SkillServiceConsumersUseNarrowPorts|.*Manager.*|.*LSP.*|.*Structure.*|.*Inspect.*|.*Xref.*)' -count=1`

## 9. 后续建议

1. 后续如继续深拆，可把 `internal/sidecar/lsp/tools/*` 的 helper 参数逐步从 `manager.Manager` 收窄到 `NavigationManager` / `XRefManager` / `StructureManager` / `EditManager`。
2. 如果 `skill.Service` 后续删除兼容聚合面，需要先把 `internal/module/skill/rpc.go` 内部 handler 也拆成更细端口；当前保留聚合面是为了稳定 RPC surface。
3. 每批完成后继续下调 `interface_isolation_guard_test.go` 对应预算。
