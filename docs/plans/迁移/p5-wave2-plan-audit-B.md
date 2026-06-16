# P5 波次 2 审查 B

注：当前任务书与 `docs/plans/迁移/p5-execution-plan.md:96-114` 存在范围差异。主计划当前写的是 `module/skill/rpc.go` 15 个方法、`module/workspace/rpc.go` 5 个方法、`module/orchestration/rpc.go` 17 个方法；本审查按用户给定的 R3/R4/R5 维度展开，同时把这处 scope mismatch 作为结论项单列。

## 1. V2 复杂度

`go-agent-v2/internal/apiserver/methods_command.go`

- 按 handler 口径，最长方法应看 `commandExecTyped`(`:40-92`) 和 `skillsList`(`:217-239`)；`collectChangedSkillNames`(`:165-202`) 更长，但它是 helper，不是 RPC handler。
- `commandExecTyped` 不应留在 RPC handler。它包含命令黑名单、参数安全校验、env allowlist、超时、进程执行、日志、输出截断和 read-command hint 注入。这已经是可复用、可测试的业务执行策略，应该下沉到 `skill/command` service 或 facade。
- `skillsList` 可以保留为薄 handler，但更稳妥的是下沉到 service。它当前承担了 `processCwdSkillSvc` 与 `skillsManager` 的双来源选择，以及 DTO 组装；如果波次 2 新建 `module/skill`，这层分流更适合由 service 统一隐藏。

`go-agent-v2/internal/apiserver/workspace_methods.go`

- 最长两个 handler 是 `workspaceRunAbort`(`:138-167`) 和 `workspaceRunList`(`:85-113`)。
- 这两个方法的主要内容是取 `WorkspaceManager`、bind params、做简单默认值/必填校验、调用 manager、再做 UI sync 与 notify。
- 业务逻辑主体已经在 `WorkspaceManager`；这类逻辑可以继续留在 RPC handler 或一个很薄的 `module/workspace/rpc.go` 包装层，不需要再额外下沉。需要抽取的是 transport 级重复，不是再造一层 service 来包 service。

`go-agent-v2/internal/apiserver/methods_orchestration.go`

- 最长两个 handler 是 `agentLaunchTyped`(`:39-71`) 和 `agentReportEventTyped`(`:185-209`)。
- `agentLaunchTyped` 里 `LaunchWithConfig`/`Launch` 的适配与 fallback 不应留在 handler。它已经是 orchestrator 能力协商逻辑，应放到 `orchestration.Service`。
- `agentReportEventTyped` 仍然偏薄，只做 `agent_id/event_type` 校验、构造 `agentcore.Event`、桥接到 `AgentEventHandler`。这类逻辑可以继续留在 handler，除非后续事件类型校验、审计或持久化继续膨胀。

结论：波次 2 真正需要下沉到 service 的重逻辑集中在 `commandExecTyped` 和 `agentLaunchTyped`；workspace V2 handler 反而已经是薄壳。

## 2. 工厂模式

- 波次 1 的源码参照物不是文档里的抽象名，而是 `internal/module/thread/rpc.go:96-114` 的 `newThreadCommandHandler` / `newCapabilityThreadCommandHandler`。
- 这个工厂解决的是“注册与 wrapper 重复”，但也把多条 V2 异构 RPC 契约压成了统一 `commandParams.Args`；波次 1 审查已经证明，这会引入外部 schema 回归。
- 因此，波次 2 不能复刻一个更大的 `cmd()/capCmd()` 版本。只能抽 transport 共性，不能抽业务 schema。

R3 `command/card`

- 不建议做重型 `cardHandler` 工厂。
- 可以接受的轻量复用是 `withCommandCardService`、`cardKeyParams`、`successOnly` 之类 helper。
- 原因是 `list`、`get`、`upsert`、`delete`、`version`、`run/execute` 的入参与返回值差异明显；如果强行工厂化，极容易重复波次 1 的“为了复用而扁平化 schema”问题。

R4 `workspace/run`

- 不建议做重型 `workspaceHandler` 工厂。
- 可以接受的轻量复用是 `withWorkspaceService`、`runKeyParams`、`notifyWorkspaceRun(eventName, payload)` 之类 helper。
- `get`、`list`、`create`、`merge`、`abort` 共享的是 manager/service 注入、`runKey` 校验和 notify 模板，不共享业务 payload。工厂价值有限，helper 足够。

R5 `agent/task`

- 不建议把 agent 与 task/dag 混成一个工厂。
- agent 生命周期、event 注入、sub-agent 绑定、DAG/node 状态更新不是同一类请求。
- 最多拆成几个小 helper：`agentIDParams`、`dagKeyParams`、`dagNodeParams`、`withOrchestrationService`。

结论：波次 2 需要的是“小 helper + 明确 param struct”，不是波次 1 风格的统一 handler 工厂。

## 3. store 支撑

`internal/store/commandcard/contract.go`

- 当前 `Store` 只有 `Get`、`InsertVersion`、`Upsert`、`List` 4 个方法。
- `Upsert` 可以覆盖 create/update，但缺 `Delete`。
- 缺版本读取面：没有 `GetVersion`、`ListVersions` 一类接口。
- `CommandCardRun` 类型虽然已定义在 contract 里，但 `Store` 完全没有 run 相关方法；`internal/store/sqlc` 也只有 model，没有 `InsertCommandCardRun`、`ListCommandCardRuns` 等 query 命中。
- 结论：如果 R3 真包含 command/card CRUD + run/execute 语义，当前 store 明显不完整，属于 blocker。

`internal/store/workspace/contract.go`

- 当前 `Store` 有 `UpsertRun`、`GetRun`、`ListRuns`、`UpdateRunStatus`、`TransitionRunStatus`、`UpsertFile`、`GetFile`、`ListFiles` 8 个方法。
- `internal/store/workspace/store.go` 已完整实现这 8 个方法。
- 对 `workspace/run/create|get|list|merge|abort` 这组方法来说，现有 store 基本够用：create 走 `UpsertRun/UpsertFile`，merge/abort 走状态更新与 file upsert。
- 唯一明显缺口是没有 `WithTx`；如果后续要求 merge 的多文件状态更新具备强事务性，需要补事务边界。
- 结论：workspace store 对 R4 不是 blocker。

`internal/store/taskdag/contract.go`

- DAG、node、wakeup、worker lease 相关方法都已在 contract 中定义。
- `internal/store/taskdag/store.go` 已把整个 contract 落地，不是空壳。
- 对 DAG/task RPC 所需的底层持久化来说，当前 store 足够，甚至超出“只做 DAG/node 读写”的最低要求。
- 结论：taskdag store 对 R5 不是 blocker；真正缺的是 module/service 映射层。

## 4. 参数类型

按当前已知范围，不应把 24 个方法做成 24 个独立 param struct，但也不应为了“工厂化”压缩到 3-5 个 mega struct。合理区间约为 15-17 个。

R3 `command/card`

- `cardKeyParams` 值得共用，适合 key-only 方法，如 `get`、`delete`。
- 仍然需要独立的 `listCardsParams`、`upsertCardParams`、`cardVersionParams`、`cardRunParams` 或 `executeCardParams`。
- 预计 4-5 个 param struct。

R4 `workspace/run`

- `runKeyParams` 值得共用，但只适合纯 key 路由，例如 `get`。
- `merge` 和 `abort` 应该在 `runKeyParams` 基础上扩展字段，不能硬共用同一个 struct，因为它们分别还需要 `updatedBy/dryRun/deleteRemoved` 与 `updatedBy/reason`。
- `createRunParams` 与 `listRunsParams` 也必须独立。
- 预计 4-5 个 param struct。

R5 `agent/task`

- 参数差异大，不能指望一个通用 struct。
- 可复用的只有 `agentIDParams`，适合 `stop`、`getState`、`getReport` 一类 key-only 调用。
- `launch`、`submit`、`reportEvent`、`rememberReportRequest`、`save/delete/persist sub-agent`、`task/dag get`、`task/node status update` 都应独立建模。
- 预计 6-7 个 param struct。

结论：`cardKeyParams` 和 `runKeyParams` 都值得存在，但只能做局部复用；agent/task 参数异构度最高，不能靠工厂抹平。

## 5. workspace 现状

- `internal/module/workspace/` 当前不存在；LSP 直接返回 `path not found`，并且在 `internal/module` 下搜索 `package workspace` 无命中。
- 当前代码树里与 workspace 直接相关的只有 `internal/store/workspace/{contract.go,module.go,store.go}`。
- 在当前 `internal` 树内，也找不到 `CreateRun(ctx`、`MergeRun(ctx`、`AbortRun(ctx` 这类 workspace service 方法实现。
- 结论：R4 不是“只建一个 `rpc.go`”的问题，而是要从零补一个完整 module，至少包括 `contract.go`、`service.go`、`module.go`、`rpc.go`。否则 `rpc.go` 只能直接碰 store 和文件系统，分层会失真。

## 6. orchestration 映射

`internal/sidecar/orch/orchestration/contract.go`

- `agent.launch` -> `LaunchAgent` 存在。
- 但它只是“方法名存在”，不是“语义已对齐”。当前 `LaunchRequest` 字段是 `AgentID/Name/ParentID/Cwd/Command/Env`；V2 `agentLaunchParams` 字段是 `id/name/prompt/cwd/instructions/dynamic_tools/config`。这不是直接映射，contract 需要重设计或至少加 adapter DTO。
- `agent.stop` -> `StopAgent` 存在，形状直接可用。
- `agent.list` -> 没有 `ListAgents`。当前只有 `internal/sidecar/orch/orchestration/helpers.go:123-137` 的私有 `listAgents() []agentRuntime`，RPC 不能把它当稳定 contract。
- `agent.snapshot` -> `Snapshot` 存在。
- `agent.getState` 可以部分复用 `Snapshot.State`，但 contract 没有独立 `GetState` 方法。
- `agent.getReport` 没有对应方法，也没有任何实现命中。
- `task/dag/*` 完全没有对应方法。`internal/sidecar/orch/orchestration/service.go` 的 `service` 结构当前只有 logger、event bus、session cleaner、state machine、agents map，没有 `taskdag.Store` 或其他 DAG 依赖。
- 另外，V2 兼容面里的 `rememberReportRequest`、`reportEvent`、`saveSubAgent`、`deleteSubAgent`、`persistSubAgentBinding` 也都不在 contract 上。
- `SubmitTurn` 虽然存在，但它的 DTO `TurnSubmission` 当前字段是 `agentId/threadId/input/...`，也不直接匹配 V2 `agent.submit` 的 `agent_id/prompt/images/files` 形状。

结论：R5 当前不是“补一个 `rpc.go`”的工作量，而是要先扩 `orchestration.Service` contract，且至少补 agent list/report/state/sub-agent/task-dag 其中一部分方法。否则 handler 没有合法落点。

## 结论（Blocker / Improvement）

Blocker

- 任务书与 `p5-execution-plan.md` 的 R3/R4/R5 方法面不一致。当前一个写 8/8/8，一个写 15/5/17；不先冻结 scope，后续 factory、param struct、contract 设计都会反复返工。
- R3 若按 command/card 实现，`store/commandcard` 目前不够：缺 delete、缺版本查询、缺 run 持久化，属于直接 blocker。
- R4 的 store 已就绪，但 `internal/module/workspace` 整个模块不存在，属于 module 级 blocker。
- R5 的 blocker 最大：`orchestration.Service` 只覆盖 `LaunchAgent/StopAgent/SubmitTurn/CompleteTurn/Recover/Snapshot`，且 `LaunchRequest`、`TurnSubmission` 都与 V2 兼容面不对齐；`agent.list/getState/getReport/sub-agent/task/dag` 都缺正式 contract。

Improvement

- 波次 2 只应引入轻量 helper，不应再引入波次 1 那种会扁平化外部 schema 的大工厂。
- 推荐以“局部共享参数”替代“统一 mega params”：`cardKeyParams`、`runKeyParams`、`agentIDParams`、`dagKeyParams`、`dagNodeParams`。
- 如果 R5 确认要纳入 DAG/task 写面，更干净的做法是新增独立 taskdag facade，而不是继续把 `orchestration.Service` 扩成 lifecycle + task engine 的混合 God service。
