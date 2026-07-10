# mcp-orch 模块边界收敛设计

Date: 2026-07-10
Status: implemented; verified; pending user review
Scope: `cmd/mcp-orch` 的 Fx 组合、orchestration contract 消费边界，以及对应 archtest 与启动测试。

## 1. 背景与问题

`cmd/mcp-orch` 已是独立 MCP sidecar，且 `internal/archtest` 禁止它依赖 `internal/app`、`internal/module`、`internal/provider` 和 desktop host。这条依赖方向正确，必须保留。

仍有三处可维护性债务：

1. `contract.AgentLifecyclePort` 聚合 Launch、Read、Stop、Interrupt、Recover 等 7 个操作；部分 tool 与 adapter 只需要其中一小部分能力。
2. `cmd/mcp-orch/fx.go:buildOrchestrationOptions` 同时绑定 service 的角色接口、生命周期订阅、DAG runner、node executor 和本地 launcher，成为单一组合热点。
3. `TestParentFxStartup` 镜像生产的 `fx.As(...)` 绑定；生产与测试组合容易在后续增减接口时漂移。

## 2. 目标与非目标

目标：消费端只获得完成其 workflow 所需的窄 contract port；保持 `run()` 为 sidecar 唯一 composition root；将装配拆为可独立验证的 option groups；让启动测试复用生产 option groups；用 archtest 防止宽接口重新泄露。

非目标：不把 `cmd/mcp-orch/orchestration` 迁入 `internal/module`；不改变 MCP tool schema、JSON-RPC envelope、runner 启停顺序、SQLite schema、agent/DAG 业务语义或错误语义；不增加默认值、兼容兜底或全局 service locator。

## 3. 目标结构

### 3.1 Workflow contract ports

以现有 consumer 的真实方法集定义 `AgentLaunchPort`、`AgentStateReader`、`AgentStopPort`、`AgentStopWaitPort`、`AgentInterruptPort` 与 `AgentRecoveryPort`。`AgentStopWaitPort` 包含 `ListAgents`，因为等待终态必须读取 `list_agents` 同源快照。`AgentLifecyclePort` 被删除；同一个 orchestration service 继续实现所有新接口，故不改变对象身份或状态一致性。

### 3.2 Fx option groups

入口 package 保留装配所有权，并以 `orchestrationLifecycleOptions`、`orchestrationDAGOptions`、`orchestrationExecutionOptions` 和 `orchestrationTransportOptions` 分组。`run()` 仍负责进程级 module、开始、等待和停止；业务包不导出 `fx.Module`。

### 3.3 测试与守卫

启动测试复用生产 lifecycle option group，并只以 fake 补齐外部依赖。`newMCPOrchApp(remoteAddr)` 让测试在不执行 `Start` 的前提下解析完整生产 Fx 图，覆盖本地和远端 launcher 分支。archtest 同时禁止重新声明 `AgentLifecyclePort`，并锁定 wakeup dispatcher/reclaimer 端口不再嵌入宽 `WakeupStore`。

## 4. 验收

- `cmd/mcp-orch` 不依赖 `internal/app`、`internal/module`、`internal/provider` 或其他 `cmd` host。
- tool/RPC schema、MCP 错误 envelope、agent/DAG 状态迁移和 runner 行为不变。
- 启动测试不再手写生产 lifecycle bindings。
- `AgentLifecyclePort` 不能在 production contract 中重新出现。
- `WakeupDispatchStore` 与 `WakeupReclaimStore` 只能暴露各自 consumer 的直接方法集。
- 完整 Fx 图在本地和远端 launcher 分支均可解析，且 `taskdag.NodeFlowStore` 只有一个未命名 binding。
- `./scripts/test_with_guard.sh ./internal/archtest -count=1`、`./scripts/test_with_guard.sh ./cmd/mcp-orch -count=1` 和 `make guard` 均通过。

## 5. 实施证据

- legacy contract guard 先 RED 后 GREEN；`AgentLifecyclePort` 不再出现在生产 contract。
- `taskdag.Store` 的本次 consumer 边被收窄，priority SSA 守卫自动删除 15 条已消失的冻结记录，未新增冻结项。
- P2 follow-up 先以 RED archtest 锁定 wakeup 端口，再把 dispatcher 收窄为五个直接方法、reclaimer 收窄为两个直接方法；可选 smart-retry 与运行节点读取仍经显式断言处理。
- 完整 Fx smoke 在提取 `newMCPOrchApp` 后发现并消除了重复的 `taskdag.NodeFlowStore` binding，同时删除遗留 identity provider：`taskdag.Module` 现在是唯一的绑定所有者。
- 已通过 `cmd/mcp-orch` 全包、taskdag/orchestration 包、全 archtest、全改动 Go 文件 LSP diagnostics、`go build ./cmd/mcp-orch`、`make guard` 与 `git diff --check`。
