# mcp-orch 模块边界收敛设计

Date: 2026-07-10
Status: implemented; verified
Scope: `cmd/mcp-orch` 的 Fx 组合、orchestration contract 消费边界，以及对应 archtest 与启动测试。

## 1. 背景与问题

当前 `cmd/mcp-orch` 已是独立 MCP sidecar，且 `internal/archtest` 禁止它依赖 `internal/app`、`internal/module`、`internal/provider` 和 desktop host。这条依赖方向正确，必须保留。

仍有三处可维护性债务：

1. `contract.AgentLifecyclePort` 聚合 Launch、Read、Stop、Interrupt、Recover 等 7 个操作；部分 tool 与 adapter 只需要其中一小部分能力。
2. `cmd/mcp-orch/fx.go:buildOrchestrationOptions` 同时绑定 service 的 19 个角色接口、生命周期订阅、DAG runner、node executor 和本地 launcher，成为单一组合热点。
3. `TestParentFxStartup` 镜像生产的 `fx.As(...)` 绑定；生产与测试组合容易在后续增减接口时漂移。

## 2. 目标与非目标

目标：

- 消费端只能获得完成其 workflow 所需的窄 contract port。
- 保持 `run()` 为 sidecar 唯一 composition root；它仍负责进程级 config、store module、transport 和退出生命周期。
- 将 orchestration 的装配拆成可单独理解、可单独启动验证的 option groups。
- 让启动测试直接复用生产 option groups，消除手抄的 interface binding。
- 用可执行 archtest 防止宽接口再次泄露给普通 consumer。

非目标：

- 不把 `cmd/mcp-orch/orchestration` 迁入 `internal/module`。
- 不改变 MCP tool schema、JSON-RPC envelope、runner 启停顺序、SQLite schema、agent/DAG 业务语义或错误语义。
- 不新建全局 service locator、默认值或兼容兜底。

## 3. 目标结构

### 3.1 按 workflow 拆窄 lifecycle port

在 `internal/contract/orchestration.go` 中以实际消费工作流而非“每个方法一个接口”为准拆分：

- `AgentLaunchPort`：launch workflow 所需的 `ListAgents`、`LaunchAgent` 与 `Snapshot`。
- `AgentStateReader`：`ListAgents`、`Snapshot`、`GetState`。
- `AgentStopPort`：仅 `StopAgent`；`AgentStopWaitPort` 组合停止与等待终态所需的同源快照读取。
- `AgentInterruptPort`：仅 `InterruptAgent`。
- `AgentRecoveryPort`：`Snapshot` 与 `Recover`。

`AgentLifecyclePort` 不再作为普通 tool、adapter、dashboard consumer 或 facade 的依赖；它已从 production contract 删除，并由 archtest 阻止重新声明。

每个 consumer 的 constructor 参数改为它所需的 workflow port。Fx 继续由同一个 orchestration `service` 实现这些接口，因而不改变运行时对象身份、状态一致性或调用顺序。

### 3.2 在入口包内拆分 Fx option groups

保留 `cmd/mcp-orch` 对应用装配的所有权，不向 `cmd/mcp-orch/orchestration` 导出 `fx.Module`。在同一入口 package 中新增职责明确的文件：

- `fx_orchestration_lifecycle.go`：service 与 lifecycle/report/hook bindings。
- `fx_orchestration_dag.go`：DAG store adapter、turn-completed subscriber、wakeup/cron runners。
- `fx_orchestration_execution.go`：launcher、node executor、command-card 与 shared-file adapters。
- `fx_transport.go`：stdio、HTTP、bootstrap 的 runner 组合；仅移动现有 adapter，不改协议行为。

`buildOrchestrationOptions(remoteAddr)` 收敛为只组合命名 option groups：

```go
return []fx.Option{
	orchestrationLifecycleOptions(),
	orchestrationDAGOptions(),
	orchestrationExecutionOptions(remoteAddr),
}
```

`run()` 继续持有进程级 modules、`bindRuntime`、`app.Start`、`app.Wait` 与 `app.Stop`。`orchestrationTransportOptions()` 由完整 app 构造加入；没有业务规则、存储事务或 provider 行为迁入 root。

### 3.3 启动测试复用生产装配

`TestParentFxStartup` 使用 `orchestrationLifecycleOptions()` 作为被测对象，并以 fake store、launcher、event bus 和 runner dependency 补齐测试图。测试不再重新书写 `fx.Module("orchestration")` 和 `fx.As(...)` 列表。

`TestNewMCPOrchAppBuildsCompleteGraph` 以 `newMCPOrchApp(remoteAddr).Err()` 解析完整 production graph，覆盖本地与远端 launcher 分支。该测试显式提供 test bootstrap、临时 SQLite 路径与 stdio 输出前置条件，但不执行 `Start`、stdio loop、HTTP listener 或远端连接。

### 3.4 防回归守卫

现有 `TestOrchestrationServiceConsumersUseNarrowPorts` 持续阻止生产 consumer 使用完整 orchestration service。相邻 archtest 还检查：

- `AgentLifecyclePort` 不得在 production contract 中重新声明；
- 普通 tool、adapter、dashboard consumer 使用对应 workflow port；
- `WakeupDispatchStore` 与 `WakeupReclaimStore` 不得嵌入宽 `WakeupStore`，且只能声明各自 consumer 的直接方法集；
- 未登记的宽接口、额外方法或 Fx binding 漂移均会失败。

守卫以 AST/type/SSA 的现有 helper 为基础，不为 Fx 文件排版写脆弱的文本匹配规则。

## 4. 迁移步骤与门禁

1. 先添加 lifecycle-port 生产 consumer 守卫与 fixture，确认旧的宽 consumer 使测试失败。
2. 新增窄 port，逐个修改 consumer constructor 和 Fx binding，保持公共 tool/RPC 接口不变。
3. 将 Fx binding 移入四个 option-group 文件；每次只移动一个 group，不同时调整业务实现。
4. 让启动测试直接消费生产 option groups，删除镜像 binding。
5. 删除无消费者的宽 lifecycle 依赖；不得通过 fallback provider、noop 替代或放宽 archtest 让迁移继续。

任一步出现启动图差异、runner 缺失、port 未提供或行为测试失败时立即停止并修正根因。

## 5. 验证与验收

每个 Go 文件变更后先运行对应 package 的受控测试；阶段完成后运行：

```bash
./scripts/test_with_guard.sh ./internal/archtest -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch -count=1
make guard
```

验收条件：

- `cmd/mcp-orch` 仍不依赖 `internal/app`、`internal/module`、`internal/provider` 或其他 `cmd` host。
- tool/RPC schema、MCP 错误 envelope、agent/DAG 状态迁移和 runner 数量不变。
- `TestParentFxStartup` 不再镜像生产 `fx.As(...)` 列表。
- archtest 能拒绝宽 lifecycle port consumer 和宽 wakeup store port 回归。
- 完整 Fx 图在本地和远端 launcher 分支均可解析，且 `taskdag.NodeFlowStore` 只有一个未命名 binding。
- LSP diagnostics 与上述验证命令均无问题。

## 6. 回滚

本设计只移动装配代码并收窄编译期接口，不引入数据迁移。任一阶段可回滚该阶段的文件移动和 consumer 签名改动，恢复原来的 Fx binding；不需要数据库回滚或运行时双写。

## 7. 实施范围

生产与测试文件为：

- `internal/contract/orchestration.go`
- `cmd/mcp-orch/fx.go` 与新增 `fx_*` option-group 文件
- `cmd/mcp-orch/fx_test.go`
- 受影响的 `cmd/mcp-orch/tools/**`、`cmd/mcp-orch/orchestration/**`、`internal/app/dashboard_adapter.go`
- `internal/archtest` 的 lifecycle、dispatcher 与 wakeup port 守卫

未触碰 frontend、provider、SQLite migration、tool schema 或 `internal/module/**`。

## 8. 实施证据

- legacy contract guard 先 RED 后 GREEN；`AgentLifecyclePort` 不再出现在 production contract。
- `taskdag.Store` 的本次 consumer 边被收窄，priority SSA 守卫自动删除 15 条已消失的冻结记录，未新增冻结项。
- P2 follow-up 先以 RED archtest 锁定 wakeup 端口，再把 dispatcher 收窄为五个直接方法、reclaimer 收窄为两个直接方法；可选 smart-retry 与运行节点读取仍经显式断言处理。
- 完整 Fx smoke 在提取 `newMCPOrchApp` 后发现并消除了重复的 `taskdag.NodeFlowStore` binding，同时删除遗留 identity provider：`taskdag.Module` 现在是唯一的绑定所有者。
- 已通过 `cmd/mcp-orch` 全包、taskdag/orchestration 包、全 archtest、全改动 Go 文件 LSP diagnostics、`go build ./cmd/mcp-orch`、`make guard` 与 `git diff --check`。
