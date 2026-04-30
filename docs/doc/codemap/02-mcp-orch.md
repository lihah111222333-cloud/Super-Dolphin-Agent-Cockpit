# mcp-orch 代码地图

> 2026-04-24 debt banner / authoritative pointer：本卷描述 `cmd/mcp-orch` 的稳定职责与装配结构，不再是 orchestration 依赖方向 / hidden contract 的权威记录。orchestration `Module` / `handler.Map` / hidden side-channel contract、`OrchestrationTurnStarter` 与 `OrchestrationSessionCleaner` 的双树同构、`BootstrapHookAfterHandler` 等契约的 authoritative 入口是 [`docs/plans/迁移/p22/README.md`](../../plans/迁移/p22/README.md)、[`docs/plans/迁移/p22/P3_OrchestrationWaiterAlignment.md`](../../plans/迁移/p22/P3_OrchestrationWaiterAlignment.md) 与 [`docs/plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md`](../../plans/迁移/p22/P4_DependencyDirectionAndHiddenContracts.md)。若本卷与 P22/P3/P4 冲突，以后者为准。

## 1. 模块概述

`cmd/mcp-orch` 是 `super-agent-v3` 的编排侧车 / peer 服务，核心职责可归纳为 5 类：

1. **Agent 编排**：维护 agent 生命周期、状态机、turn 队列、停止 / 恢复、report 请求者等内存态。
2. **MCP 工具出口**：把 orchestration / task DAG / workspace / prompt / command card / shared file / memory read 能力注册进 `tools.Registry`，再通过 stdio / HTTP MCP 与 bootstrap `OnToolsList` / `OnToolsCall` 暴露给 Claude 或主控代理。
3. **包级 JSON-RPC handler 映射**：定义 `agent.*`、`task/dag/*`、`workspace/run/*` 等 handler map；它们不是 Claude 侧 MCP tools，是否暴露取决于外层 Fx / RPC 组合。
4. **持久化层**：维护 DAG / wakeup / worker lease / workspace run / shared file / prompt / command card 等 PostgreSQL 数据。
5. **peer / bootstrap 桥接**：在 peer 模式下注册到主控、订阅 hooks，并把远端 thread/state/turn/runtime 事件回灌到本地编排内存态；`tools/list` / `tools/call` 也会经 bootstrap 回调代理到同一份 registry。

### 作为 MCP server 暴露给 Claude 的方式

- `main.go`
  - 保存原始 `stdout` 到 `mcpStdout`。
  - 把普通 `os.Stdout` 重定向到 `stderr`，防止日志污染 MCP JSON-RPC 流。
  - 把 `GOMAXPROCS` 上限压到 2，避免轻量 sidecar 空转占 CPU。
- `fx.go`
  - 组装 Fx app。
  - 注入 store / workspace / orchestration / registry / bootstrap / stdio runner / http runner。
  - 根据 `GO_AGENT_CTL_RPC_ADDR` 选择 `localLauncher` 或 `remoteLauncher`。
- `runtime.go`
  - `newLogger(cfg *platformconfig.Config)`：默认写 `/tmp/mcp-orch-<pid>.log`。
  - `newPool(cfg *platformconfig.Config)` / `newQueries(pool *pgxpool.Pool)`：初始化 pgx pool 与 sqlc 查询器。
  - `newRegistry(orchestration, ws, prompt, command, sharedFile, memory)`：构造运行时 `tools.Registry`；`tools.NewRegistry()` 会把 `memory_read` 一并挂入，因此 registry **定义数**是 20；`memory` 依赖也已注入，读侧是否真正返回内容再由 `memory.Config` 的开关决定。
  - `registryToolProvider`：把 `tools.Registry` 适配为 `common.ToolProvider`；stdio MCP、HTTP MCP、bootstrap `OnToolsList` / `OnToolsCall` 都复用这层 `ListTools` / `CallTool` 出口。
  - `newStdioRunner()`：基于 `common.NewServer("mcp-orch", "dev", transport, provider)` 启动 stdio MCP。
  - `newBootstrapRunner(cfg bootstrap.Config, client *bootstrap.Client)` / `bootstrapRunner.Run(ctx)`：runner 总是被加入 runner group，但只有在 `GO_AGENT_PEER_MODE=1` 且 `GO_AGENT_CTL_RPC_ADDR` 非空时才真正向控制面注册；否则只是阻塞等待 ctx 结束。
  - `bindRuntime()`：把 stdio / HTTP / bootstrap runner 注入同一个 `platformrunner.RunGroup` 生命周期。
  - 还包含 `newNoopSessionCleaner()` / `newNoopTurnStarter()`，作为 standalone / 测试兜底实现。
- `cmd/mcp-orch/memory/`
  - `config.go`：解析 `ENABLE_MEMORY_SYSTEM`、`ENABLE_MEMORY_TOOLS`、`MULTI_AGENT_MEMORY_DIR`，生成 memory 根目录 / 项目根 / 机器标识配置。
  - `service.go`：实现 `contract.MemoryService.Read()`；只读读取 memory entry，并回传 `denyReason` / `degraded` / `source` 元数据。
  - `path.go`：负责 scope root 解析、路径 sanitize / resolve / authorize，以及 project/user/local 三种 scope 的根目录派生。
  - 当前 `fx.go/run()` 已通过 `memory.NewConfig()` / `memory.NewService()` 接入启动图；因此 wiring 已注入完成，运行时是否允许读取取决于 enable flags 与 scope 授权结果。
- `http_runner.go`
  - 仅在 `GO_AGENT_PEER_MODE=1` 时启用 `common.NewHTTPServer`。
  - 监听 `127.0.0.1:0`，并把地址写入 peer discovery file。
  - 非 peer 模式返回 `blockRunner`，仅阻塞等待上下文结束。
- `buildBootstrapConfig()`
  - 给控制面注册 `OnToolsList` / `OnToolsCall`，使主控可代理调用本服务工具；底层直接复用 `registryToolProvider`。
  - 声明能力：`tools/orchestration`、`tools/task`、`tools/workspace`、`tools/prompt`、`tools/command`、`tools/shared_file`、`tools/memory`。
  - 配置 `OnShutdown`、`OnConfigChanged`、`FinalReport`、`Hooks.OnAfter`。

### 与第 11 卷的边界

- 本卷聚焦 `cmd/mcp-orch` 的编排侧车、registry、bootstrap 与 tool 暴露；`memory / prompt / thread snapshot` 的跨模块语义请结合 `11-memory-prompt-thread.md` 阅读。
- `cmd/mcp-orch` 当前**没有直接 import** `internal/module/{memory,prompt,thread}`；它消费的是 `internal/contract`、`store/*`、`internal/mcpserver/common/bootstrap` 这些边界层，因此这里描述的是“暴露 / 注入 / 存储读取”，不是第 11 卷那种“语义组装 / 生命周期编排”。
- `prompt_list` / `prompt_get` 读取的是 `cmd/mcp-orch/store/prompt` 里的 prompt template 资源，不等于 `internal/module/prompt` 的 system prompt assembly。
- `memory_read` 走的是 `cmd/mcp-orch/memory.(*service).Read` 这条只读链路，不覆盖第 11 卷里的 durable memory 保存、rules 注入、manifest/retrieval 逻辑。
- `UpdateRuntime()` 作为编排内存态的收口点仍在本卷说明；但 provider 何时上报 runtime、thread/session 如何消费这些元数据，属于第 11 卷与 provider/runtime 侧共同关注的链路。

### 运行组合关系

`mcp-orch` 的“传输方式”和“agent 启动后端”是两条正交维度，不能简单合并成一个模式表：

| 维度 | 条件 | 实现 |
|---|---|---|
| MCP 传输 | 默认 | stdio MCP |
| MCP 传输 | `GO_AGENT_PEER_MODE=1` | stdio MCP + HTTP MCP |
| bootstrap 注册 | `GO_AGENT_PEER_MODE=1` 且 `GO_AGENT_CTL_RPC_ADDR` 非空 | 向主控注册并订阅 hooks |
| agent 启动后端 | `GO_AGENT_CTL_RPC_ADDR` 为空 | `localLauncher`，本地拉起进程 |
| agent 启动后端 | `GO_AGENT_CTL_RPC_ADDR` 非空 | `remoteLauncher`，经主控 RPC 调 `thread/start` / `turn/start` / `thread/stop` |

> 也就是说：**即便不是 peer 模式，只要设置了 `GO_AGENT_CTL_RPC_ADDR`，stdio sidecar 也会改用 `remoteLauncher`**；只是 bootstrap / HTTP runner 不会启动。

---

## 2. 目录结构

### 顶层

- `cmd/mcp-orch/main.go`：保护 stdout 的 MCP 通道，限制 `GOMAXPROCS`。
- `cmd/mcp-orch/fx.go`：Fx 装配入口；拼装 store、workspace、orchestration、launcher、bootstrap、runner。
- `cmd/mcp-orch/runtime.go`：logger / pgx pool / sqlc queries / registry / stdio runner / bootstrap runner / runtime 绑定。
- `cmd/mcp-orch/memory/`：memory 只读能力实现；覆盖 env 配置、scope root / 路径授权，以及 `contract.MemoryService.Read()`。
- `cmd/mcp-orch/http_runner.go`：peer 模式 HTTP MCP server、peer discovery file、非 peer 模式阻塞 runner。
- `cmd/mcp-orch/hook_subscription.go`：hook topic 常量与 `subscribeOrchestrationHooks()` 辅助函数；主启动路径未直接调用，主要被测试覆盖。
- `cmd/mcp-orch/sqlc.yaml`：`sql/queries/*` 到 `store/sqlc/*` 的 sqlc 生成配置。
- 顶层测试：`fx_test.go`、`runtime_test.go` 覆盖 Fx 装配与 bootstrap runner 行为。

### `orchestration/`

- `service.go`：模块导出、`service` 主体、Fx provider、turn / approval 生命周期订阅。
- `launcher.go`：`AgentLauncher` 接口；`localLauncher` 与 `remoteLauncher` 两种实现。
- `launch_helpers.go`：launch 请求归一化、端口 / provider 推断、`SubmissionQueue`。
- `service_launcher_bridge.go`：launch / stop / submit 在 service 与 launcher 之间的桥接层。
- `process_lifecycle.go`：`runnerActor`、进程退出监控、session 清理、stall 检测与恢复入口。
- `helpers.go`：状态机辅助、`BindActiveTurnID`、session-ready 等待、本地进程启动、快照列举。
- `turn_lifecycle.go`：turn 完成 / 中断 / 用户输入审批的状态流转与兜底修复。
- `hook_consumer.go`：bootstrap `after` hooks 消费器；同步 thread/state/turn/item/process 事件。
- `runtime.go`：runtime port/provider 上报、provider 归一化、snapshot 组装。
- `report.go`：agent report 聚合、report requester 跟踪、终态事件判定与 drain。
- `dag.go`：DAG create/get/list/update 的 service 层映射，兼容旧 JSON 字段别名。
- `dag_retry_policy.go`：Phase 3.5 helper —— 把 DAG metadata `schedule.{default_retry, fail_fast}` + node `config.execution.retry` 解析为 `RetryPolicy{MaxAttempts, FailFast}`，给 dispatcher 决定 retry vs fail 用。
- `rpc.go`：编排 JSON-RPC handler 映射；把 RPC 参数转成 `contract` 请求。
- `rpc_types.go`：RPC 入参结构与旧字段兼容（如 `agentId` / `dagKey` / `selectedSkills` / `outputSchema`）。
- `events.go`：封装 state / launched / stopped / recovering / failed / runtime / stalled / resumed 事件发布。
- `factory.go`：事件 DTO 封装、agent 读写锁辅助、状态切换底层工具、legacy alias 解码。
- `recover.go`：agent 恢复与基于 DAG wakeup 的 turn replay。
- 测试文件：`execution_test.go`、`hook_consumer_test.go`、`launcher_test.go`、`recover_test.go`、`rpc_golden_test.go`、`runtime_report_test.go`、`runtime_test.go`、`stop_test.go`、`submission_test.go`、`turn_lifecycle_test.go`、`user_input_test.go`；`testdata/golden/turn-agent/rpc_samples.golden.json` 是 RPC golden fixture。

### `tools/`

- `types.go`：`ToolDefinition`、`Schema`、常用 JSON Schema 构造器。
- `factory.go`：通用 handler 工厂、依赖检查、raw JSON 编码、list limit 规范化、not-found 翻译。
- `registry.go`：把各类 tool definition 汇总到 `Registry`。
- `orchestration_tools.go`：agent 启动 / 发消息 / 停止 / 枚举 / report 工具。
- `task_tools.go`：DAG 工具；`schedule` / `execution` 仍编码进 DAG metadata / node config JSON。
- `workspace_tools.go`：workspace create/get/list/merge/abort 工具。
- `workspace_tool_compat.go`：把 workspace service 输出适配成 MCP 兼容 DTO，补 `workspace_root` / `files_merged` / `finished_at` 等字段。
- `prompt_tools.go`：prompt template list/get 工具；走通用 resource tool builder。
- `command_tools.go`：command card list/get 工具；带固定 list limit（50）。
- `shared_file_tools.go`：shared file read/write 工具；附带 path 归一化与 10 MiB 内容上限。
- `memory_tools.go`：唯一的 memory 工具出口 `memory_read`；schema 暴露 `name/path/scope/type`，handler 调 `contract.MemoryService.Read()`。
- 测试文件：`handler_regression_test.go`、`orchestration_tools_test.go`、`parity_v2_test.go`、`workspace_tools_compat_test.go`。

### `workspace/`

- `contract.go`：workspace service 接口、请求 / 结果类型、legacy JSON 兼容。
- `event.go`：workspace 领域事件定义。
- `factory.go`：文件复制、原子写回、symlink 防护、merge 评估、RPC 校验辅助。
- `module.go`：Fx module，可导出 workspace service 与 workspace RPC handlers；`cmd/mcp-orch/fx.go` 当前是手动 `fx.Provide` workspace service，没有直接引入这个 module。
- `rpc.go`：workspace JSON-RPC 方法映射。
- `rpc_types.go`：workspace RPC 参数类型与旧字段兼容。
- `service.go`：workspace run 生命周期主逻辑。
- `service_delete_removed.go`：`delete_removed=true` 时的删除候选构建。
- `service_dry_run.go`：dry-run merge 路径。
- `service_helpers.go`：相对路径校验、hash、事件发送、merge 统计、持久化与回滚。
- `service_merge.go`：真正的文件写回 / 删除、失败收敛、最终状态迁移。

### `store/`

- `commandcard/contract.go`：command card 读写契约与 version 结构。
- `commandcard/module.go`：Fx provider。
- `commandcard/store.go`：`command_cards` / `command_card_versions` 读写与映射。
- `prompt/contract.go`：prompt template 读写契约与 version 结构。
- `prompt/module.go`：Fx provider。
- `prompt/store.go`：`prompt_templates` / `prompt_template_versions` 读写，支持事务 `WithTx()`。
- `sharedfile/contract.go`：shared file 读写契约。
- `sharedfile/module.go`：Fx provider。
- `sharedfile/store.go`：`shared_files` CRUD。
- `taskdag/contract.go`：DAG / node / wakeup / worker lease 的完整持久化接口。
- `taskdag/factory.go`：通用 query helper、interval 解析、wakeup fencing 辅助。
- `taskdag/module.go`：Fx provider。
- `taskdag/store.go`：DAG / node CRUD、running node 绑定、灵活状态更新、事务封装。
- `taskdag/store_lease.go`：worker lease 抢占 / 续约 / 释放。
- `taskdag/store_wakeup.go`：wakeup 入队 / claim / sent / retry / fail / bind turn / reclaim / 查询。
- `taskdag/*_test.go`：`scan_helpers_test.go`、`store_fencing_test.go`、`test_helpers_test.go`，覆盖 wakeup fencing、running node turn 绑定与 fake DB 扫描。
- `workspace/contract.go`：workspace run / file 的持久化契约。
- `workspace/module.go`：Fx provider。
- `workspace/store.go`：`workspace_runs` / `workspace_run_files` 的 upsert / list / get / CAS 状态迁移。
- `workspace/*_test.go`：`store_test.go`、`test_helpers_test.go`，覆盖 workspace store 的 query 参数和扫描。
- `sqlc/`：sqlc 生成层。
  - `db.go`：`Queries` 与 `DBTX`。
  - `db_ext.go`：事务辅助 `WithTx()` / `WithTxOrReuse()`。
  - `types_ext.go`：pgtype/time/string/int64 转换。
  - `models.go`：生成模型。
  - `command_card.sql.go`、`prompt_template.sql.go`、`shared_file.sql.go`、`workspace_run.sql.go`：资源 / workspace SQL 的 Go 封装。
  - `task_dag_dag.sql.go`、`task_dag_node_read.sql.go`、`task_dag_node_write.sql.go`、`task_dag_node_runtime.sql.go`、`task_dag_wakeup_dispatch.sql.go`、`task_dag_wakeup_query.sql.go`、`task_dag_worker_lease.sql.go`：DAG / wakeup / lease SQL 的 Go 封装。
  - `task_ack.sql.go`：已生成但尚未接入手写 store / tool 包装。

### `sql/queries/`

- `README.md`：说明哪些 SQL 与仓库根目录同名，需要双边同步。
- `command_card.sql`：command card 查询与 version 历史。
- `prompt_template.sql`：prompt template 查询与 version 历史。
- `shared_file.sql`：shared file 查询。
- `workspace_run.sql`：workspace run / file 查询与状态迁移。
- `task_dag_dag.sql`：DAG 主表 CRUD / `FOR UPDATE`。
- `task_dag_node_read.sql`：node 读取、按 assignee 查 running node、`FOR UPDATE` 读取。
- `task_dag_node_write.sql`：node upsert 与通用状态更新。
- `task_dag_node_runtime.sql`：running node turn 绑定、事件 touch，以及带状态前置约束的运行时更新（`pending` gated update、`running` gated update、`running|awaiting_verify` complete）。
- `task_dag_wakeup_dispatch.sql`：wakeup 入队、claim、mark sent、bind turn、retry、fail。
- `task_dag_wakeup_query.sql`：stale reclaim、sent-unbound / pending-or-dispatching 查询、单条读取。
- `task_dag_worker_lease.sql`：worker lease 抢占、续约、释放。
- `task_ack.sql`：task ack SQL；当前已生成 sqlc，但还没有对应手写 store/tool 包装。

---

## 3. 核心类型 / 接口

| 类型 / 接口 | 位置 | 职责 |
|---|---|---|
| `type service struct` | `orchestration/service.go` | 编排核心；持有 `agents`、`dagStore`、`launcher`、事件总线、状态机配置。 |
| `type agentRuntime struct` | `orchestration/service.go` | 单个 agent 的内存态：state、thread/turn、runtime port/provider、queue、`exec.Cmd`、报告与错误信息。 |
| `type AgentLauncher interface` | `orchestration/launcher.go` | 抽象 agent 的 `Launch/Stop/SubmitTurn/IsRunning`。 |
| `type localLauncher` | `orchestration/launcher.go` | 本地进程模式，直接 `exec.Command` 启动命令。 |
| `type remoteLauncher` | `orchestration/launcher.go` | 远程模式，调用主控 RPC：`thread/start`、`thread/stop`、`turn/start`。 |
| `type SubmissionQueue` | `orchestration/launch_helpers.go` | 本地 turn 队列；支持 `Enqueue/Prepend/Dequeue/Peek/Len/Clear`。 |
| `type HookConsumer interface` | `orchestration/hook_consumer.go` | bootstrap hooks 的 `After` 处理入口。 |
| `type runnerActor` | `orchestration/process_lifecycle.go` | 本地模式 runner；处理进程 wait、队列消费、stall 恢复。 |
| `type StallDetector` | `orchestration/recover.go` | `turn_running` 超时检测，触发恢复。 |
| `type Registry struct` | `tools/registry.go` | 汇总 20 个 MCP tool definition（含 `memory_read`），并支持 `List/Lookup`。 |
| `type ToolDefinition` | `tools/types.go` | MCP tool 元信息：名字、描述、输入 schema、handler。 |
| `type workspace.Service interface` | `workspace/contract.go` | workspace run 的 create/get/list/merge/abort/file 查询能力。 |
| `type workspace.service struct` | `workspace/service.go` | workspace 领域实现，负责路径校验、bootstrap copy、merge、事件发送。 |
| `type taskdag.Store interface` | `store/taskdag/contract.go` | DAG/node/wakeup/worker lease 的完整持久化接口。 |
| `type workspace.Store interface` | `store/workspace/contract.go` | `workspace_runs` / `workspace_run_files` 的持久化接口。 |
| `type prompt.Store` / `commandcard.Store` / `sharedfile.Store` | `store/*/contract.go` | prompt / command / shared_file 资源查询与写入。 |
| `type contract.MemoryService` | `internal/contract/memory.go` | `memory_read` 背后的只读契约；返回 `entry/sourcePath/indexHit/denyReason/degraded/source`。 |
| `type memory.Config` | `memory/config.go` | memory root / project root / machine id / 功能开关配置；决定 `memory_read` 是否真正可用。 |
| `type sqlc.Queries` | `store/sqlc/db.go` | 所有 SQL 的底层执行器。 |

### `agentRuntime` 里最关键的字段

- 标识：`id`、`name`、`parentID`
- 启动参数：`cwd`、`command`、`env`
- 进程 / 线程：`cmd`、`threadID`、`remoteThreadID`、`remoteAgentID`
- turn：`activeTurnID`、`queue`
- 运行时：`port` / `runtimePort`、`provider` / `runtimeProvider`、`portSource` / `providerSource`
- 状态：`state`、`sm`、`lastError`、`lastReport`、`reportRequesters`
- 生命周期：`startedAt`、`updatedAt`、`exitedAt`、`launchSeq`、`lastExitedSeq`、`monitoredSeq`、`sessionGeneration`
- 停止 / 恢复：`stopRequested`、`stopReason`

### 状态机来源

`orchestration` 不自定义状态常量，而是复用 `internal/dto/agent`：

- state：`provisioning`、`idle`、`turn_queued`、`turn_starting`、`turn_running`、`awaiting_user_input`、`recovering`、`stopping`、`stopped`、`failed`
- trigger：`launch_succeeded`、`launch_failed`、`turn_enqueued`、`turn_accepted`、`turn_completed`、`turn_aborted`、`user_input_requested`、`user_input_resolved`、`recover_requested`、`stop_requested`、`process_exited`

`internal/dto/agent/state.go` 中的实际 transition 是：

| From | Trigger | To |
|---|---|---|
| `provisioning` | `launch_succeeded` | `idle` |
| `provisioning` | `launch_failed` | `failed` |
| `idle` | `turn_enqueued` | `turn_queued` |
| `idle` | `recover_requested` | `recovering` |
| `idle` | `stop_requested` | `stopping` |
| `idle` | `process_exited` | `failed` |
| `turn_queued` | `turn_accepted` | `turn_starting` |
| `turn_queued` | `recover_requested` | `recovering` |
| `turn_queued` | `stop_requested` | `stopping` |
| `turn_queued` | `process_exited` | `failed` |
| `turn_starting` | `turn_completed` | `idle` |
| `turn_starting` | `turn_accepted` | `turn_running` |
| `turn_starting` | `recover_requested` | `recovering` |
| `turn_starting` | `stop_requested` | `stopping` |
| `turn_starting` | `process_exited` | `failed` |
| `turn_running` | `turn_completed` | `idle` |
| `turn_running` | `turn_aborted` | `idle` |
| `turn_running` | `user_input_requested` | `awaiting_user_input` |
| `turn_running` | `recover_requested` | `recovering` |
| `turn_running` | `process_exited` | `failed` |
| `turn_running` | `stop_requested` | `stopping` |
| `awaiting_user_input` | `user_input_resolved` | `turn_running` |
| `awaiting_user_input` | `turn_aborted` | `idle` |
| `awaiting_user_input` | `recover_requested` | `recovering` |
| `awaiting_user_input` | `process_exited` | `failed` |
| `awaiting_user_input` | `stop_requested` | `stopping` |
| `recovering` | `launch_succeeded` | `idle` |
| `recovering` | `launch_failed` | `failed` |
| `stopping` | `process_exited` | `stopped` |
| `stopped` | `recover_requested` | `recovering` |
| `stopped` | `launch_succeeded` | `idle` |
| `failed` | `recover_requested` | `recovering` |
| `failed` | `stop_requested` | `stopping` |

### 其他关键状态值

- workspace run：`active`、`merging`、`merged`、`failed`、`aborted`
- workspace run file：`synced`、`tracked`、`merged`、`removed`、`conflict`、`error`、`unchanged`
- DAG 默认状态：`draft`
- wakeup：`pending`、`dispatching`、`sent`、`failed`
- task DAG node 运行时 SQL 体现出的常见状态：`pending`、`running`、`awaiting_verify`，以及终态如 `done` / `failed`；其中 `UpdateRunningTaskDagNodeStatus` 实际只匹配 `status IN ('pending')`（通常用于把 pending 节点推进到 running），`UpdateAwaitingVerifyTaskDagNodeStatus` 只匹配 `running`，`CompleteTaskDagNode` 只匹配 `running` / `awaiting_verify`

---

## 4. RPC / 工具清单

### 4.1 对外暴露的 MCP tools

当前 `tools.Registry` 注册了 **20 个** MCP tools；数量拆分为 `5 orchestration + 3 task + 5 workspace + 2 prompt + 2 command + 2 shared_file + 1 memory`。其中 `tools.NewRegistry()` 会把 `memory_read` 一并加入列表，因此 **registry/list 视角是 20 个**；`contract.MemoryService` 也已通过 Fx 注入，`memory_read` 是否返回内容取决于 `ENABLE_MEMORY_SYSTEM` / `ENABLE_MEMORY_TOOLS` 与 scope 授权结果。

> `buildBootstrapConfig()` 里的 `cfg.Capabilities` 当前显式声明 7 个能力名（`tools/orchestration|tools/task|tools/workspace|tools/prompt|tools/command|tools/shared_file|tools/memory`），与 registry 暴露的 tool family 保持一致。
>
> bootstrap 元数据声明与实际工具暴露链仍共用同一份 `registryToolProvider`：`ListTools()` 暴露 20 个 tool definition，`CallTool()` 也会把调用路由到同一份 registry；`memory_read` 的运行结果由 memory enable flags、路径解析与授权共同决定。

#### 编排类

| Tool | 功能 | 备注 |
|---|---|---|
| `orchestration_launch_agent` | 异步启动一个受编排管理的 agent。`name` 目前直接作为 `agent_id`，因此应使用简短、面向用户的任务名（如“排查登录回调 500”），不要用路径、内部 slug 或泛化角色名。 | 仅允许 `provider=codex/claude`；底层命令固定为当前 `mcp-orch` 可执行文件。 |
| `orchestration_send_message` | 给指定 agent 追加一条文本 turn。 | 自动把消息包装为 `[{type:"text", content: message}]`。 |
| `orchestration_stop_agent` | 停止指定 agent。 | 远程 agent 走 `thread/stop`，本地 agent 走进程 kill + 等待退出。 |
| `orchestration_list_agents` | 返回当前所有 agent snapshot。 | 包含 thread / runtime / report / state 等快照。 |
| `orchestration_get_agent_report` | 读取指定 agent 的最后 report。 | 返回 `report + state + requester metadata`。 |

#### DAG / task 类

| Tool | 功能 | 备注 |
|---|---|---|
| `task_create_dag` | 创建或 upsert DAG 与节点。 | `agent_id` 必填；`schedule` 与 node `execution` 先编码进 JSON。 |
| `task_get_dag` | 获取 DAG 详情和节点列表。 | 返回 `dag + nodes`。 |
| `task_update_node` | 更新节点运行状态。 | MCP schema 把状态枚举收敛为 `pending/running/done/failed`。 |

> 当前**没有**对 Claude 暴露 `task_list_dags`；但包级 JSON-RPC handler 已有 `task/dag/list`。

#### workspace 类

| Tool | 功能 | 备注 |
|---|---|---|
| `workspace_create_run` | 创建虚拟 workspace run，并可把指定文件从 source root 拷入 workspace。 | `source_root` 必填。 |
| `workspace_get_run` | 读取单个 run 详情。 | 兼容返回 `workspace_root` 字段。 |
| `workspace_list_runs` | 按状态 / DAG 过滤 run 列表。 | tool 层默认 limit=200，最大 5000。 |
| `workspace_merge_run` | 把 workspace 变更回写 source root。 | 支持 `dry_run`、`delete_removed`；兼容返回 `files_merged`。 |
| `workspace_abort_run` | 将 run 标记为 aborted。 | 直接更新状态并读取最新 run 返回。 |

#### prompt / command / shared file / memory 类

| Tool | 功能 | 备注 |
|---|---|---|
| `prompt_list` | 列出 prompt template。 | 通过 `resourceToolDefinitions()` 暴露的 list 端点，固定 keyword 检索，limit=50；读取的是 template store，不是 system prompt assembly。由于 `fx.go` 已加载 `promptstore.Module`，它在默认 wiring 下是可用的。 |
| `prompt_get` | 读取指定 prompt template。 | 使用 `prompt_key`；底层走 `promptstore.Store.Get()`，不进入 `internal/module/prompt` 的 section 组装链。 |
| `command_list` | 列出 command card。 | 固定 keyword 检索，limit=50。 |
| `command_get` | 读取指定 command card。 | 使用 `card_key`。 |
| `shared_file_read` | 读取 shared file。 | 路径会先把 `\` 归一化成 `/`，再做 `path.Clean` 和前导 `/` 清理。 |
| `shared_file_write` | 写入 shared file。 | 内容大小限制 10 MiB。 |
| `memory_read` | 只读读取 memory entry。 | schema 暴露 `name/path/scope/type`；handler 调 `contract.MemoryService.Read()`，内部会做 sanitize → resolve → authorize，并可能返回 `denyReason/degraded/source`。它只覆盖 read-side 检索，不覆盖第 11 卷里的 memory 保存 / index 更新 / rules 注入。当前 `cmd/mcp-orch` 已通过 Fx 把 `memory.NewConfig()` / `memory.NewService()` 注入 `newRegistry()`；tool 是否真正返回内容取决于 memory enable flags 与 scope 授权。 |

> `prompt_*` / `command_*` 走 `resourceToolDefinitions()` 成对导出 list/get；`memory_read` 是单独 schema/handler，不走资源型工具 builder。`prompt_*` 面向模板资源库，`memory_read` 面向只读 memory 文件视图，它们都不是第 11 卷里的运行时 prompt assembly 主链。

### 4.2 包级 JSON-RPC handlers（非 Claude MCP tools）

这些名称来自 `orchestration.ProvideRPCFacade()` 与 `workspace.NewWorkspaceHandlers()` 的 handler map。它们不是 `tools.Registry` 注册的 MCP tools；`cmd/mcp-orch` 运行时的 Claude-facing 出口仍是 stdio / HTTP MCP，远程 launcher 调用的是控制面 `thread/start` / `turn/start` / `thread/stop`。

#### orchestration RPC

- `agent.launch`
- `agent.submit`
- `agent.submitPrompt`
- `agent.stop`
- `agent.list`
- `agent.snapshot`
- `orchestration.reportRuntime`
- `agent.getState`
- `agent.getReport`
- `agent.rememberReportRequest`
- `agent.reportEvent`
- `task/dag/create`
- `task/dag/get`
- `task/dag/list`
- `task/node/update`
- `orchestration/report`

#### workspace RPC

- `workspace/run/create`
- `workspace/run/get`
- `workspace/run/list`
- `workspace/run/merge`
- `workspace/run/abort`
- `workspace/run/files/list`
- `workspace/run/file/get`

> `orchestration/rpc_types.go` 兼容旧字段别名，如 `agentId`、`agent_id`、`dagKey`、`selectedSkills`、`manualSkillSelection`、`outputSchema` 等；`workspace/rpc_types.go` 则兼容 `runKey`、`dagKey`、`updatedBy`、`dryRun`、`deleteRemoved` 等 legacy / camelCase 字段。
>
> `orchestration.reportRuntime` 的 JSON 解码走 `decodeStrictRuntimeReportJSON()`，会显式拒绝未知字段。

---

## 5. 关键函数 / 方法

### 启动与传输层

| 签名 | 作用 |
|---|---|
| `func run() error` | 创建 Fx app，启动所有 runner；当前 provider 列表已包含 `memory.NewConfig()` / `memory.NewService()`。 |
| `func buildBootstrapConfig(shutdowner fx.Shutdowner, hookAfter contract.BootstrapHookAfterHandler, registry tools.Registry) bootstrap.Config` | 配置 bootstrap 注册、工具代理、能力声明、hook 回调；`OnToolsList` / `OnToolsCall` 直接复用 `registryToolProvider`，其中 `OnToolsCall` 会把 tool 返回值再包装成 text content。P22 P4 §278：bootstrap 的 after-hook 入口已退成 `contract.BootstrapHookAfterHandler` 函数型，不再 import `orchestration.HookConsumer`。 |
| `func buildOrchestrationOptions(remoteAddr string) []fx.Option` | 根据是否存在 `GO_AGENT_CTL_RPC_ADDR` 选择 launch backend，并在本地模式注入 `runnerActor`。 |
| `func buildLauncher(lc fx.Lifecycle, turnStarter orchestration.TurnStarter, logger *slog.Logger, remoteAddr string) orchestration.AgentLauncher` | 选择 `localLauncher` 或 `remoteLauncher`。 |
| `func newRegistry(orchestration contract.OrchestrationService, ws workspace.Service, prompt promptstore.Store, command commandcardstore.Store, sharedFile sharedfilestore.Store, memory contract.MemoryService) tools.Registry` | 构造运行时 registry；当前会把 `Dependencies.Memory` 一并传给 `tools.NewRegistry()`，因此 `memory_read` 与其余 tool 同时完成 wiring。 |
| `func newStdioRunner(registry tools.Registry) platformrunner.Runner` | 用 stdio 启动 MCP server。 |
| `func newHTTPRunner(registry tools.Registry) platformrunner.Runner` | peer 模式启 HTTP MCP；否则返回阻塞 runner。 |
| `func (p registryToolProvider) ListTools(context.Context) ([]common.MCPTool, error)` | 把 registry definitions 编码成 MCP `tools/list` 响应；stdio/HTTP/bootstrap 共用。 |
| `func (p registryToolProvider) CallTool(ctx context.Context, name string, args json.RawMessage) (any, error)` | 把 `tools/call` 请求转给 `handleToolCall()`；与 `ListTools()` 共享同一份 registry。 |
| `func (r bootstrapRunner) Run(ctx context.Context) error` | runner 始终存在，但只有 peer mode + RPC addr 时才真正向主控注册；成功后还会订阅 `agent.session.start` / `agent.turn.*` / `agent.state.change` / `agent.process.exit` hooks。 |
| `func handleToolCall(ctx context.Context, registry tools.Registry, name string, args json.RawMessage) (any, error)` | `tools/call` 的统一查表与 handler 调度入口。 |
| `func bindRuntime(lc fx.Lifecycle, params runtimeParams)` | 把所有 runner 注入同一个 `platformrunner.RunGroup` 生命周期统一管理。 |
| `func subscribeOrchestrationHooks(ctx context.Context, client hookSubscriber) error` | 旧式 hook 订阅辅助函数；主路径未直接使用。 |

### 编排核心

| 签名 | 作用 |
|---|---|
| `func NewService(logger *slog.Logger, eventBus *event.Dispatcher, launcher AgentLauncher, sessionCleaner SessionCleaner, turnStarter TurnStarter, dagStore taskdag.Store) *service` | 创建编排核心服务，初始化状态机配置和 agent map。 |
| `func (s *service) LaunchAgent(ctx context.Context, req LaunchRequest) error` | 统一入口：发起 agent 启动。 |
| `func (s *service) SubmitTurn(ctx context.Context, req TurnSubmission) error` | 统一入口：提交 turn。 |
| `func (s *service) StopAgent(ctx context.Context, agentID string) error` | 统一入口：停止 agent。 |
| `func (s *service) StopAllAgents()` | 进程退出时批量停止全部 agent。 |
| `func (s *service) Snapshot(ctx context.Context, agentID string) (AgentSnapshot, error)` | 读取单个 agent snapshot。 |
| `func (s *service) UpdateRuntime(ctx context.Context, report RuntimeReport) error` | 更新 runtime port/provider，并在快照变化时发布 runtime reported 事件；这是 runtime report 最终收口点。 |
| `func (r runtimeReporter) ReportRuntime(ctx context.Context, report contract.RuntimeReport) error` | `contract.RuntimeReporter` 的本地适配器；本地 provider driver 可直接通过它把 runtime report 转发到 `UpdateRuntime()`。 |
| `func (s *service) GetState(ctx context.Context, agentID string) (AgentStateResult, error)` | 返回 agent 当前状态。 |
| `func (s *service) GetReport(ctx context.Context, agentID string) (AgentReportResult, error)` | 返回最后 report 和 metadata。 |
| `func (s *service) RememberReportRequest(ctx context.Context, req RememberReportRequest) (RememberReportRequestResult, error)` | 记录谁在等待某 agent 的最终报告。 |
| `func (s *service) HandleReportEvent(ctx context.Context, event ReportEvent) (ReportEventResult, error)` | 吸收 report 事件并在终态时 drain requester。 |
| `func (s *service) CreateDAG(ctx context.Context, req CreateDAGRequest) (DAGDetail, error)` | 事务性 upsert DAG 与 node。 |
| `func (s *service) GetDAG(ctx context.Context, dagKey string) (DAGDetail, error)` | 读取 DAG 详情。 |
| `func (s *service) ListDAGs(ctx context.Context, filter ListDAGsFilter) ([]DAGSummary, error)` | 按 status/keyword/limit 列出 DAG。 |
| `func (s *service) UpdateNodeStatus(ctx context.Context, req UpdateNodeStatusRequest) (DAGNode, error)` | 更新 DAG node 状态。 |
| `func (s *service) Recover(ctx context.Context, agentID string) error` | 手动触发恢复。 |
| `func (s *service) BindSessionGeneration(ctx context.Context, agentID string, generation uint64) error` | 记录 generation-aware session cleaner 所需 session 代次。 |
| `func (a *runnerActor) Run(ctx context.Context) error` | 轮询队列、监控进程退出、做 stall 检测。 |
| `func (c *hookConsumer) After(ctx context.Context, payload mcp.HookPayload) (mcp.AfterDecision, error)` | 消费控制面 hooks，把外部线程/turn/state/process 事件同步回 orchestration 内存态。 |

### launch / turn / recover 细节

| 签名 | 作用 |
|---|---|
| `func (s *service) launchAgentViaLauncher(ctx context.Context, req LaunchRequest) error` | launch 的主桥接逻辑。 |
| `func (s *service) prepareLauncherLaunch(ctx context.Context, req LaunchRequest) (launcherLaunchAttempt, bool, error)` | 校验请求、创建/重置 `agentRuntime`、判断是否 launch 中。 |
| `func (s *service) finishLauncherLaunch(ctx context.Context, attempt launcherLaunchAttempt, result LaunchResult, launchErr error) error` | 处理 launch 成功 / 失败 / 过期结果。 |
| `func (s *service) stopAgentViaLauncher(ctx context.Context, agentID, reason string) error` | 停止远程或本地 agent。 |
| `func (s *service) submitTurnViaLauncher(ctx context.Context, req TurnSubmission) error` | 优先走 remote submit，否则入本地队列。 |
| `func (s *service) trySubmitRemoteTurn(ctx context.Context, agentID string, req TurnSubmission) (bool, error)` | 远程 agent 提交 turn，并驱动本地状态机到 `turn_starting`。 |
| `func (s *service) claimTurnWork(ctx context.Context) []turnWork` | 本地模式从队列取出待执行 turn。 |
| `func (s *service) startTurnExecution(ctx context.Context, work turnWork)` | 真正启动本地 turn；必要时等待 session ready。 |
| `func (s *service) handleProcessExit(ctx context.Context, agentID string, launchSeq uint64, err error)` | 处理进程退出，清理 runtime/session 并推进状态机。 |
| `func handleTurnCompletedEvent(svc *service, logger *slog.Logger, ev turndto.TurnCompleted)` / `func handleTurnInterruptedEvent(svc *service, logger *slog.Logger, ev turndto.TurnInterrupted)` | 收敛 turn 完成 / 中断事件，并在必要时强制修复回 idle。 |
| `func handleToolApprovalRequestedEvent(svc *service, logger *slog.Logger, ev tooldto.ToolApprovalRequested)` / `func handleToolApprovalResolvedEvent(svc *service, logger *slog.Logger, ev tooldto.ToolApprovalResolved)` | 把审批类事件转换为 `awaiting_user_input` 状态流转。 |
| `func recoverAgent(ctx context.Context, s *service, agent *agentRuntime) (bool, error)` | 停掉旧进程、重置状态、重启、必要时回放 DAG wakeup。 |
| `func loadRecoveredTurnSubmission(ctx context.Context, s *service, agent *agentRuntime) (TurnSubmission, bool, error)` | 从 `task_dag_wakeups.prompt_payload` 恢复 turn payload。 |

### 工具与 workspace

| 签名 | 作用 |
|---|---|
| `func NewRegistry(deps Dependencies) Registry` | 汇总全部 MCP tools；当前把 prompt/command/shared_file 和 `memory_read` 一并 append 到 registry。 |
| `func makeHandler[T any, R any](dependency any, dependencyName string, exec func(context.Context, T) (R, error)) ToolHandler` | 通用 handler 工厂：依赖检查 + 输入解码。 |
| `func resourceToolDefinitions(spec resourceToolSpec) []ToolDefinition` | 构造 prompt / command 这类 list/get 资源型工具。 |
| `func promptToolDefinitions(store promptstore.Store) []ToolDefinition` | 通过 `resourceToolDefinitions()` 暴露 `prompt_list` / `prompt_get` 两个出口。 |
| `func memoryToolDefinitions(svc contract.MemoryService) []ToolDefinition` | 注册唯一的 memory tool `memory_read`。 |
| `func HandleMemoryRead(svc contract.MemoryService) ToolHandler` | `memory_read` handler；把 `name/path/scope/type` 转成 `MemoryReadRequest` 后调用 `svc.Read()`。 |
| `func HandleLaunchAgent(svc contract.OrchestrationService) ToolHandler` | `orchestration_launch_agent` 实现；异步 launch。 |
| `func createDAGRequestFromInput(in CreateDAGInput) (contract.CreateDAGRequest, error)` | 把 tool 输入转成 service contract。 |
| `func createWorkspaceRun(ctx context.Context, svc workspace.Service, input WorkspaceCreateRunRequest) (*workspaceRunDTO, error)` | `workspace_create_run` 工具实现。 |
| `func workspaceRunDTOFromRun(ctx context.Context, svc workspace.Service, run *workspace.Run) (*workspaceRunDTO, error)` | 把 workspace service 输出补齐兼容字段与文件列表。 |
| `func (s *service) CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error)` | 创建 workspace run，并 bootstrap 文件。 |
| `func (s *service) dryRunMerge(ctx context.Context, run *Run, req MergeRunRequest, updatedBy string) (*MergeRunResult, error)` | dry-run merge：`active -> merging -> active`。 |
| `func (s *service) executeMerge(ctx context.Context, run *Run, req MergeRunRequest, updatedBy string) (*MergeRunResult, error)` | 真正执行写回与状态迁移。 |
| `func (s *service) buildMergePlan(run *Run, files []RunFile, req MergeRunRequest) (*MergeRunResult, []storeworkspace.WorkspaceRunFile, error)` | 计算 tracked / removed / conflict / unchanged / error。 |
| `func (s *service) transitionMergeRun(ctx context.Context, run *Run, fromStatus, toStatus string, req MergeRunRequest, updatedBy string, result *MergeRunResult, message string) (*Run, error)` | 基于 CAS 状态迁移 workspace run。 |
| `func validateRelativePath(raw string) (string, error)` | 防止 workspace 文件路径逃逸 source root。 |

### 存储层

| 签名 | 作用 |
|---|---|
| `func (s *store) WithTx(ctx context.Context, fn func(txStore Store) error) error` | DAG / workspace / prompt 等 store 的事务封装。 |
| `func sqlc.WithTx(ctx context.Context, q *Queries, fn func(txq *Queries) error) error` | sqlc 查询集的 pool-backed 事务封装。 |
| `func sqlc.WithTxOrReuse(ctx context.Context, q *Queries, fn func(txq *Queries) error) error` | 已在事务中则复用，否则新开事务。 |
| `func (s *store) UpsertDAG(ctx context.Context, dag DAG) (*DAG, error)` | DAG 主记录 upsert。 |
| `func (s *store) UpsertNode(ctx context.Context, node Node) (*Node, error)` | DAG node upsert。 |
| `func (s *store) BindRunningNodeTurn(ctx context.Context, input BindRunningNodeTurnInput) (*Node, error)` | 把 running node 和 wakeup 绑定到 turn。 |
| `func (s *store) TouchRunningNodeEvent(ctx context.Context, input TouchRunningNodeEventInput) (*Node, error)` | 更新运行中节点的 `last_event_at`。 |
| `func (s *store) UpdateRunningNodeStatus(ctx context.Context, input RunningNodeStatusUpdate) (*Node, error)` | 只匹配 `pending` 节点，设置新状态 / result / active wakeup，常用于 `pending -> running`。 |
| `func (s *store) UpdateAwaitingVerifyNodeStatus(ctx context.Context, input AwaitingVerifyNodeStatusUpdate) (*Node, error)` | 只匹配 `running` 节点，设置新状态 / result 并清空 active turn / wakeup，常用于 `running -> awaiting_verify`。 |
| `func (s *store) CompleteNode(ctx context.Context, input CompleteNodeInput) (*Node, error)` | 把节点推进到终态，并清空 active turn/wakeup 绑定。 |
| `func (s *store) CompleteNodeAndScheduleDownstream(ctx context.Context, input CompleteNodeInput) (*CompleteNodeWithDownstreamResult, error)` | Phase 3.4：在同事务内完成节点并对所有 ready 下游入队 wakeup（idempotency_key=`dag/<dagKey>/<nodeKey>/start`）。 |
| `func (s *store) FailNodeAndCancelDownstream(ctx context.Context, input FailNodeInput) (*FailNodeResult, error)` | Phase 3.5：把节点标 failed；FailFast=true 时同事务内对所有 transitively-pending 下游 cascade 标 failed（已 running/done 节点不动）。 |
| `func (s *store) UpdateNodeStatusFlexible(ctx context.Context, input FlexibleNodeStatusUpdate) (*Node, error)` | 无状态前置约束的通用节点状态更新。 |
| `func (s *store) EnqueueWakeup(ctx context.Context, input EnqueueWakeupInput) (int64, error)` | 入队 wakeup。 |
| `func (s *store) ClaimDueWakeups(ctx context.Context, input ClaimDueWakeupsInput) ([]Wakeup, error)` | 抢占可发送 wakeup。 |
| `func (s *store) MarkWakeupSent(ctx context.Context, input MarkWakeupSentInput) (int64, error)` | 把 wakeup 从 `dispatching` 标记成 `sent`。 |
| `func (s *store) BindWakeupTurn(ctx context.Context, input BindWakeupTurnInput) (int64, error)` | 对 `sent` 且未绑定的 wakeup 写入 turn id。 |
| `func (s *store) RetryWakeup(ctx context.Context, input RetryWakeupInput) (int64, error)` | 派发失败后重回 `pending`。 |
| `func (s *store) FailWakeup(ctx context.Context, input FailWakeupInput) (int64, error)` | 派发失败并进入 `failed`。 |
| `func (s *store) ReclaimStaleDispatchingWakeups(ctx context.Context) (int64, error)` | 把 lease 过期的 `dispatching` wakeup 回收为 `pending`。 |
| `func (s *store) ListSentUnboundWakeups(ctx context.Context, targetAgentID string) ([]Wakeup, error)` | 查询指定 agent 的 `sent` 且未绑定 turn 的 wakeup。 |
| `func (s *store) ListPendingOrDispatchingWakeups(ctx context.Context) ([]Wakeup, error)` | 查询仍待派发或派发中的 wakeup。 |
| `func (s *store) GetWakeup(ctx context.Context, id int64) (*Wakeup, error)` | 按 id 读取 wakeup。 |
| `func (s *store) AcquireWorkerLease(ctx context.Context, input AcquireWorkerLeaseInput) (int64, error)` | 抢占 worker lease。 |
| `func (s *store) RenewWorkerLease(ctx context.Context, input RenewWorkerLeaseInput) (int64, error)` | 续约 worker lease。 |
| `func (s *store) ReleaseWorkerLease(ctx context.Context, input ReleaseWorkerLeaseInput) error` | 释放 worker lease。 |
| `func (s *store) TransitionRunStatus(ctx context.Context, input TransitionRunStatusInput) (*WorkspaceRun, error)` | workspace run CAS 状态切换。 |
| `func (s *store) UpsertFile(ctx context.Context, file WorkspaceRunFile) (*WorkspaceRunFile, error)` | 持久化 workspace tracked file 状态。 |

---

## 6. 依赖关系

### 6.1 关键 internal 包依赖

| 依赖 | 用途 |
|---|---|
| `internal/contract` | 编排服务、workspace 对外契约；`orchestration.Service` 直接 alias 这些类型。 |
| `internal/dto/agent` | agent 状态机状态、trigger、事件 DTO。 |
| `internal/dto/turn` | turn started/completed/interrupted/stalled/resumed/item completed 事件。 |
| `internal/dto/tool` | tool approval 请求/解决事件。 |
| `internal/dto/thread` | thread started/stopped hook 事件。 |
| `internal/dto/shared` | 输入项、事件时间透传、header 结构。 |
| `internal/dto/mcp` | bootstrap hook / report / shutdown / selector DTO。 |
| `internal/mcpserver/common` | stdio/HTTP MCP server、tool provider 协议、peer discovery。 |
| `internal/mcpserver/common/bootstrap` | 向控制面注册、订阅配置与 hooks、转发 `tools/list` / `tools/call`。 |
| `internal/platform/config` | RPC timeout、async launch timeout、runner stop timeout 等。 |
| `internal/platform/runner` | runner group 管理。 |
| `internal/platform/bus` | resilient 事件订阅。 |
| `internal/platform/statemachine` | 基于 `dto/agent` 构建状态机。 |
| `internal/platform/shared` | 输入解码、路径标准化、raw JSON/time clone、payload 取值。 |
| `internal/platform/db` | pgx pool 创建、事务、store 错误包装。 |
| `internal/platform/rpc` | JSON-RPC handler map 注册类型。 |
| `internal/store` / `internal/store/{prompt,commandcard,sharedfile}` | prompt/command/sharedfile 的共享 reader 模型与 Fx module。 |
| `pkg/logger` | 统一日志输出；`mcp-orch` 默认写 `/tmp/mcp-orch-<pid>.log`。 |

> 反过来说，`cmd/mcp-orch` 对 `internal/module/memory`、`internal/module/prompt`、`internal/module/thread` 没有直接包依赖；这也是本卷与第 11 卷的源码边界。

### 6.2 分层关系

```text
main/fx/runtime
  -> tools.Registry
  -> orchestration.Service
  -> workspace.Service
  -> store.{taskdag,workspace,prompt,commandcard,sharedfile}
  -> sqlc.Queries -> PostgreSQL

transport
  -> stdio runner (always)
  -> http runner (peer mode only)
  -> bootstrap runner (peer mode + RPC addr)

orchestration.Service
  -> internal/contract
  -> internal/dto/{agent,turn,tool,thread,shared,mcp}
  -> internal/platform/{bus,statemachine,shared,config}
  -> store/taskdag
  -> AgentLauncher(local|remote)

workspace.Service
  -> store/workspace
  -> internal/platform/{shared,db}

tools.Registry
  -> orchestration.Service
  -> workspace.Service
  -> store/{prompt,commandcard,sharedfile}
  -> contract.MemoryService
```

> 当前 source tree 已有 `cmd/mcp-orch/memory/*` 与 `memory_read` tool definition，且 `run()` 的 Fx provider 已 `fx.Provide(memory.NewConfig, memory.NewService)` 并经 `newRegistry(..., memory)` 注入 `contract.MemoryService`；`memory_read` 的可读结果因此受 memory 开关、路径解析与 scope 授权控制，不再归因于 wiring 缺失。

- `run()`：Fx 装配总入口（`cmd/mcp-orch/fx.go:30`）。
- `buildBootstrapConfig()`：注册 `OnToolsList` / `OnToolsCall` / `Hooks.OnAfter` / capabilities（`cmd/mcp-orch/fx.go:77`）。
- `newRegistry()`：把 orchestration / task / workspace / prompt / command / shared_file / memory 组装成统一 registry（`cmd/mcp-orch/runtime.go:110`）。
- `bootstrapRunner.Run()`：仅在 peer mode + RPC addr 下真正 register + subscribe hooks（`cmd/mcp-orch/runtime.go:184`）。
- `newHTTPRunner()`：peer mode 下启动 HTTP MCP，并写 discovery file（`cmd/mcp-orch/http_runner.go:23`、`internal/mcpserver/common/discovery.go:62`）。

## 3. Agent 生命周期（launch / list / report / send_message / stop）

### 3.1 完整时序图

```mermaid
sequenceDiagram
    autonumber
    participant U as Claude/主控
    participant T as tools/orchestration_tools
    participant S as orchestration.service
    participant L as localLauncher/remoteLauncher
    participant R as runnerActor/bootstrap hooks
    participant Core as 主控 thread/turn/session
    participant Rep as report.go

    U->>T: orchestration_launch_agent(name,prompt,...)
    T-->>U: {agent_id,status:"launching"}
    T->>S: LaunchAgent(req)（异步 goroutine）
    S->>S: prepareLauncherLaunch()
    alt localLauncher
        S->>L: Launch()
        L->>L: exec.Command().Start()
        R->>S: startWaiters()
    else remoteLauncher
        S->>L: Launch()
        L->>Core: thread/start RPC
        Core-->>L: thread_id / remote_agent_id
    end
    S->>S: finishLauncherLaunch()
    S-->>R: state_changed + agent_launched/agent_failed

    U->>T: orchestration_list_agents()
    T->>S: ListAgents()
    S-->>U: []AgentSnapshot

    U->>T: orchestration_send_message(agent_id,message)
    T->>S: SubmitTurn(submission)
    alt remote agent
        S->>L: SubmitTurn()
        L->>Core: turn/start RPC
        Core-->>S: turn_id
    else local agent
        S->>S: enqueueLocalTurnSubmission()
        R->>Core: TurnStarter.StartTurn()
    end
    R-->>S: TurnStarted/Completed/Interrupted or hook.after
    S->>Rep: HandleReportEvent()/setReportLocked()

```mermaid
sequenceDiagram
  participant C as Claude
  participant T as orchestration_send_message
  participant S as orchestration.Service
  participant L as AgentLauncher
  participant Q as SubmissionQueue
  participant A as agent/provider
  participant H as hooks or local events
  C->>T: send message
  T->>S: SubmitTurn(TurnSubmission)
  alt remoteLauncher
    S->>L: SubmitTurn -> turn/start
    L->>A: start remote turn
    A-->>H: turn/state/process hooks
    H-->>S: handleTurnCompleted / Interrupted / After
  else localLauncher
    S->>Q: enqueueLocalTurnSubmission
    Q-->>S: claimTurnWork
    S->>A: TurnStarter.StartTurn()
    A-->>S: TurnStarted / Completed / Interrupted
  end
  S-->>C: state + report updates
```

1. Claude 调用 `orchestration_send_message`。
2. `submissionFromMessage()` 生成 `TurnSubmission`：
    - `Inputs = [{type:"text", content: message}]`
   - `ThreadID` 优先取现有 snapshot 的 `thread_id`，否则回退到 `agent_id`
3. `service.SubmitTurn()` 进入 `submitTurnViaLauncher()`。
4. 分支：
   - **远程 agent**：
     - `remoteLauncher.SubmitTurn()` 调主控 `turn/start`
     - 本地状态机先走 `turn_enqueued -> turn_accepted`，把状态推进到 `turn_starting`
     - 真正进入 `turn_running/idle/stopped/failed` 依赖后续 hook / state change 同步
   - **本地 agent**：
     - `enqueueLocalTurnSubmission()` 先确认 agent 正在运行，必要时等 session ready
     - turn 入 `SubmissionQueue`
     - 若当前 `idle`，立即触发 `turn_enqueued`，状态进入 `turn_queued`
     - `runnerActor.claimTurnWork()` 取出 queued work，并触发第一次 `turn_accepted`，状态进入 `turn_starting`
     - `startTurnExecution()` 调 `TurnStarter.StartTurn()`
     - 若 provider 返回了新的 turn id，则 `BindActiveTurnID()` 回填；随后再次触发 `turn_accepted`，状态进入 `turn_running`
5. turn 生命周期事件来源：
   - 本地总线：`turndto.TurnStarted / TurnCompleted / TurnInterrupted`
   - peer hooks：`turn.completed` / `turn.interrupted` / `turn.item_completed`
   - 审批：`tooldto.ToolApprovalRequested / Resolved`
6. 状态推进：
   - `idle -> turn_queued -> turn_starting -> turn_running`
   - 审批阻塞时：`turn_running -> awaiting_user_input`
   - 审批恢复：`awaiting_user_input -> turn_running`
   - 完成：`turn_running -> idle`，且状态机也允许 `turn_starting --turn_completed--> idle`
   - 中断 / abort：`turn_running -> idle` 或 `awaiting_user_input -> idle`
7. report 路径：
   - `hookConsumer.handleTurnCompleted()` 把 `result/summary/message` 写入 `lastReport`
   - `handleItemCompleted()` 遇到 final answer item 也会更新 report
   - `HandleReportEvent()` 在终态事件时 drain `reportRequesters`
8. 异常与恢复：
    - `runnerActor` 每 200ms tick 一次
    - 若 `turn_running` 且 `updatedAt` 超过 30s 未刷新，`StallDetector` 触发 `recoverWithReason(ctx, agentID, "stall_detected")`
    - `Recover()` 会重启 agent；若 DAG store 中存在与 `activeTurnID` 精确绑定、且 wakeup 仍是 `sent` 的记录，则把原 prompt 回放回队列

    U->>T: orchestration_stop_agent(agent_id)
    T->>S: StopAgent()
    alt remote agent
        S->>L: Stop()
        L->>Core: thread/stop RPC
        R-->>S: hook thread.stopped / state.change
    else local agent
        S->>L: Stop()
        L->>L: kill process
        R-->>S: handleProcessExit()
    end
    S->>S: removeSession()
    S-->>R: state_changed + agent_stopped/agent_failed
```

### 3.2 生命周期要点

- `orchestration_launch_agent` 是**立即返回**的异步工具；真正 launch 在后台跑，避免 MCP tool-call 超时（`cmd/mcp-orch/tools/orchestration_tools.go:36`、`cmd/mcp-orch/orchestration/service.go:273`、`cmd/mcp-orch/orchestration/service_launcher_bridge.go:53`）。
- launch 先经过 `prepareLauncherLaunch()` 归一化 `agentRuntime`，再由 `localLauncher` 执行本地进程或由 `remoteLauncher` 发 `thread/start` RPC（`cmd/mcp-orch/orchestration/launcher.go:141`）。
- `orchestration_list_agents` 纯读 `agents` map 并返回 snapshot，不做副作用（`cmd/mcp-orch/orchestration/service.go:301`）。
- 报告读取与报告写入分离：`GetReport()` 只读；真正写 `lastReport` 的入口是 `HandleReportEvent()`、`hookConsumer.handleTurnCompleted()` 与 final-answer item 镜像（`cmd/mcp-orch/orchestration/report.go:49`、`cmd/mcp-orch/orchestration/report.go:81`、`cmd/mcp-orch/orchestration/hook_consumer.go:53`）。
- stop 统一收口到 `stopAgentViaLauncher()`；本地进程退出走 `handleProcessExit()`，远端线程退出主要靠 hook 镜像回灌（`cmd/mcp-orch/orchestration/service.go:277`、`cmd/mcp-orch/orchestration/service_launcher_bridge.go:180`、`cmd/mcp-orch/orchestration/process_lifecycle.go:82`）。
- `sessionGeneration` 在 thread 启动/恢复后从 `SessionManager` 回写给 mcp-orch；退出时优先调用 generation-aware cleaner，避免误删新 session（`internal/module/thread/lifecycle.go:62`、`cmd/mcp-orch/orchestration/process_lifecycle.go:21`、`internal/provider/unified/session_adapter.go:54`）。

## 4. 三条核心数据流

### 4.1 Registry → handler → service/store

```mermaid
flowchart LR
    A[Fx newRegistry] --> B[tools.Registry]
    B --> C[registryToolProvider]
    C --> D1[stdio tools/list tools/call]
    C --> D2[HTTP peer tools/list tools/call]
    C --> D3[bootstrap OnToolsList / OnToolsCall]
    D1 --> E[tools/* handler]
    D2 --> E
    D3 --> E
    E --> F1[orchestration.service]
    E --> F2[workspace.service]
    E --> F3[memory.service]
    E --> F4[prompt/command/sharedfile stores]
    E --> F5[taskdag/workspace stores]
    F4 --> G[(PostgreSQL)]
    F5 --> G
```

1. **恢复 replay**：`recover.go` 根据 `assigned_to + active_turn_id + active_wakeup_id` 找回原 prompt payload。
2. **未来 watcher / dispatcher 扩展**：store + SQL 已齐备，但尚未全部暴露成 MCP tools。
3. **包级 RPC 扩展面**：已存在 `task/dag/list`，但尚无 Claude 侧直接工具入口。

## 8. 测试入口 + archtest freeze 映射

本卷涉及的 `cmd/mcp-orch` 子树当前有明确测试入口；`freeze` 列统一写 `—`，因为 `internal/archtest/freeze_registry.go` 当前只对 `internal/module/{memory,prompt}` 维护显式 freeze。

| 包 | 测试文件 | 核心 Test* | freeze |
|---|---|---|---|
| `mcp-orch` | `runtime_memory_test.go` | `TestNewRegistryIncludesMemoryTool` | — |
| `memory` | `memory/service_test.go` | `TestServiceReadByNameReturnsEntryContentAndMetadata` | — |
| `orchestration` | `orchestration/submission_test.go` | `TestEnqueueDequeue` | — |
| `store/taskdag` | `store/taskdag/store_fencing_test.go` | `TestClaimDueWakeupsSkipsAlreadyClaimedWakeups` | — |
| `store/workspace` | `store/workspace/store_test.go` | `TestCreateWorkspaceRun` | — |
| `tools` | `tools/parity_v2_test.go` | `TestHandleWorkspaceGetRunNilMapsToNotFound` | — |

## 9. 常见改动 how-to

| 场景 | 触发 | 步骤 | 锚点 | 验证 |
|---|---|---|---|---|
| MCP tool | 暴露新的 Claude-facing 工具能力 | 1. 在 `tools/*_tools.go` 增 schema / handler。<br>2. 在 `tools.NewRegistry()` 挂入 definition。<br>3. 回看 `newRegistry()` 与 `buildBootstrapConfig()`，同步依赖注入和 capability。 | `tools.NewRegistry()` / `newRegistry()` / `buildBootstrapConfig()` | `tools/parity_v2_test.go`、`runtime_memory_test.go` |
| orch RPC | 新增 agent / task / report 类 JSON-RPC | 1. 先补 `orchestration/rpc_types.go` 参数结构与 legacy alias。<br>2. 在 `ProvideRPCFacade()` 注册 handler。<br>3. 复用 `service` / helper 做 contract 映射与严格解码。 | `ProvideRPCFacade()` | `orchestration/rpc_golden_test.go` |
| workspace op | 补 run / query / merge / abort 等 workspace 能力 | 1. 先补 `workspace/contract.go` / `workspace/rpc_types.go`。<br>2. 在 `workspace/service*.go` 实现校验、状态迁移与 merge 计划。<br>3. 经 `NewWorkspaceHandlers()` 与 `createWorkspaceRun()` / `workspaceRunDTOFromRun()` 同步暴露到 RPC / MCP。 | `NewWorkspaceHandlers()` / `createWorkspaceRun()` / `workspaceRunDTOFromRun()` | `tools/workspace_tools_compat_test.go`、`store/workspace/store_test.go` |
