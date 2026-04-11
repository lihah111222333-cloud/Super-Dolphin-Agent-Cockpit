# mcp-orch 代码地图

## 1. 模块概述

`cmd/mcp-orch` 是 `super-agent-v3` 的编排侧车 / peer 服务，核心职责可归纳为 5 类：

1. **Agent 编排**：维护 agent 生命周期、状态机、turn 队列、停止 / 恢复、report 请求者等内存态。
2. **MCP 工具出口**：把 orchestration / task DAG / workspace / prompt / command card / shared file 能力暴露成 Claude 可调用的 MCP tools。
3. **内部 JSON-RPC 出口**：导出 `agent.*`、`task/dag/*`、`workspace/run/*` 等 handler，供主控或其他进程调用。
4. **持久化层**：维护 DAG / wakeup / worker lease / workspace run / shared file / prompt / command card 等 PostgreSQL 数据。
5. **peer / bootstrap 桥接**：在 peer 模式下注册到主控、订阅 hooks，并把远端 thread/state/turn/runtime 事件回灌到本地编排内存态。

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
  - `newLogger()`：默认写 `/tmp/mcp-orch-<pid>.log`。
  - `newPool()` / `newQueries()`：初始化 pgx pool 与 sqlc 查询器。
  - `newRegistry()`：汇总 19 个 MCP tools。
  - `registryToolProvider`：把 `tools.Registry` 适配为 `common.ToolProvider`，实现 `tools/list` 与 `tools/call`。
  - `newStdioRunner()`：基于 `common.NewServer("mcp-orch", "dev", transport, provider)` 启动 stdio MCP。
  - `newBootstrapRunner()` / `bootstrapRunner.Run()`：在 peer 模式下向控制面注册，并订阅编排 hooks。
  - `bindRuntime()`：用 `platformrunner.RunGroup` 统一托管所有 runner 生命周期。
  - 还包含 `newNoopSessionCleaner()` / `newNoopTurnStarter()`，作为 standalone / 测试兜底实现。
- `http_runner.go`
  - 仅在 `GO_AGENT_PEER_MODE=1` 时启用 `common.NewHTTPServer`。
  - 监听 `127.0.0.1:0`，并把地址写入 peer discovery file。
  - 非 peer 模式返回 `blockRunner`，仅阻塞等待上下文结束。
- `buildBootstrapConfig()`
  - 给控制面注册 `OnToolsList` / `OnToolsCall`，使主控可代理调用本服务工具。
  - 声明能力：`tools/orchestration`、`tools/task`、`tools/workspace`、`tools/prompt`、`tools/command`、`tools/shared_file`。
  - 配置 `OnShutdown`、`OnConfigChanged`、`FinalReport`、`Hooks.OnAfter`。

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
- `cmd/mcp-orch/http_runner.go`：peer 模式 HTTP MCP server、peer discovery file、非 peer 模式阻塞 runner。
- `cmd/mcp-orch/hook_subscription.go`：hook topic 常量与 `subscribeOrchestrationHooks()` 辅助函数；主启动路径未直接调用，主要被测试覆盖。
- `cmd/mcp-orch/sqlc.yaml`：`sql/queries/*` 到 `store/sqlc/*` 的 sqlc 生成配置。

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
- `rpc.go`：编排 JSON-RPC handler 映射；把 RPC 参数转成 `contract` 请求。
- `rpc_types.go`：RPC 入参结构与旧字段兼容（如 `agentId` / `dagKey` / `selectedSkills` / `outputSchema`）。
- `events.go`：封装 state / launched / stopped / recovering / failed / runtime / stalled / resumed 事件发布。
- `factory.go`：事件 DTO 封装、agent 读写锁辅助、状态切换底层工具、legacy alias 解码。
- `recover.go`：agent 恢复与基于 DAG wakeup 的 turn replay。

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

### `workspace/`

- `contract.go`：workspace service 接口、请求 / 结果类型、legacy JSON 兼容。
- `event.go`：workspace 领域事件定义。
- `factory.go`：文件复制、原子写回、symlink 防护、merge 评估、RPC 校验辅助。
- `module.go`：Fx 导出 workspace service 与 workspace RPC handlers。
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
- `workspace/contract.go`：workspace run / file 的持久化契约。
- `workspace/module.go`：Fx provider。
- `workspace/store.go`：`workspace_runs` / `workspace_run_files` 的 upsert / list / get / CAS 状态迁移。
- `sqlc/`：sqlc 生成层。
  - `db.go`：`Queries` 与 `DBTX`。
  - `db_ext.go`：事务辅助 `WithTx()` / `WithTxOrReuse()`。
  - `types_ext.go`：pgtype/time/string/int64 转换。
  - `models.go`：生成模型。
  - `*.sql.go`：各 SQL query 的 Go 封装，包括已生成但尚未接入手写 store 的 `task_ack.sql.go`。

### `sql/queries/`

- `README.md`：说明哪些 SQL 与仓库根目录同名，需要双边同步。
- `command_card.sql`：command card 查询与 version 历史。
- `prompt_template.sql`：prompt template 查询与 version 历史。
- `shared_file.sql`：shared file 查询。
- `workspace_run.sql`：workspace run / file 查询与状态迁移。
- `task_dag_dag.sql`：DAG 主表 CRUD / `FOR UPDATE`。
- `task_dag_node_read.sql`：node 读取、按 assignee 查 running node、`FOR UPDATE` 读取。
- `task_dag_node_write.sql`：node upsert 与通用状态更新。
- `task_dag_node_runtime.sql`：running node turn 绑定、事件 touch、`pending -> running`、`running -> awaiting_verify`、`running|awaiting_verify -> terminal`。
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
| `type SubmissionQueue` | `orchestration/launch_helpers.go` | 本地 turn 队列；支持 `Enqueue/Prepend/Dequeue/Peek/Clear`。 |
| `type HookConsumer interface` | `orchestration/hook_consumer.go` | bootstrap hooks 的 `After` 处理入口。 |
| `type runnerActor` | `orchestration/process_lifecycle.go` | 本地模式 runner；处理进程 wait、队列消费、stall 恢复。 |
| `type StallDetector` | `orchestration/recover.go` | `turn_running` 超时检测，触发恢复。 |
| `type Registry struct` | `tools/registry.go` | 汇总 19 个 MCP tool definition，并支持 `List/Lookup`。 |
| `type ToolDefinition` | `tools/types.go` | MCP tool 元信息：名字、描述、输入 schema、handler。 |
| `type workspace.Service interface` | `workspace/contract.go` | workspace run 的 create/get/list/merge/abort/file 查询能力。 |
| `type workspace.service struct` | `workspace/service.go` | workspace 领域实现，负责路径校验、bootstrap copy、merge、事件发送。 |
| `type taskdag.Store interface` | `store/taskdag/contract.go` | DAG/node/wakeup/worker lease 的完整持久化接口。 |
| `type workspace.Store interface` | `store/workspace/contract.go` | `workspace_runs` / `workspace_run_files` 的持久化接口。 |
| `type prompt.Store` / `commandcard.Store` / `sharedfile.Store` | `store/*/contract.go` | prompt / command / shared_file 资源查询与写入。 |
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

### 其他关键状态值

- workspace run：`active`、`merging`、`merged`、`failed`、`aborted`
- workspace run file：`synced`、`tracked`、`merged`、`removed`、`conflict`、`error`、`unchanged`
- DAG 默认状态：`draft`
- wakeup：`pending`、`dispatching`、`sent`、`failed`
- task DAG node 运行时 SQL 体现出的常见状态：`pending`、`running`、`awaiting_verify`，以及终态如 `done` / `failed`

---

## 4. RPC / 工具清单

### 4.1 对外暴露的 MCP tools

当前 `tools.Registry` 注册了 **19 个** MCP tools。

#### 编排类

| Tool | 功能 | 备注 |
|---|---|---|
| `orchestration_launch_agent` | 异步启动一个受编排管理的 agent。`name` 直接作为 `agent_id`。 | 仅允许 `provider=codex/claude`；底层命令固定为当前 `mcp-orch` 可执行文件。 |
| `orchestration_send_message` | 给指定 agent 追加一条文本 turn。 | 自动把消息包装为 `[{type:"text", content:...}]`。 |
| `orchestration_stop_agent` | 停止指定 agent。 | 远程 agent 走 `thread/stop`，本地 agent 走进程 kill + 等待退出。 |
| `orchestration_list_agents` | 返回当前所有 agent snapshot。 | 包含 thread / runtime / report / state 等快照。 |
| `orchestration_get_agent_report` | 读取指定 agent 的最后 report。 | 返回 `report + state + requester metadata`。 |

#### DAG / task 类

| Tool | 功能 | 备注 |
|---|---|---|
| `task_create_dag` | 创建或 upsert DAG 与节点。 | `agent_id` 必填；`schedule` 与 node `execution` 先编码进 JSON。 |
| `task_get_dag` | 获取 DAG 详情和节点列表。 | 返回 `dag + nodes`。 |
| `task_update_node` | 更新节点运行状态。 | MCP schema 把状态枚举收敛为 `pending/running/done/failed`。 |

> 当前**没有**对 Claude 暴露 `task_list_dags`；但内部 JSON-RPC 已有 `task/dag/list`。

#### workspace 类

| Tool | 功能 | 备注 |
|---|---|---|
| `workspace_create_run` | 创建虚拟 workspace run，并可把指定文件从 source root 拷入 workspace。 | `source_root` 必填。 |
| `workspace_get_run` | 读取单个 run 详情。 | 兼容返回 `workspace_root` 字段。 |
| `workspace_list_runs` | 按状态 / DAG 过滤 run 列表。 | tool 层默认 limit=200，最大 5000。 |
| `workspace_merge_run` | 把 workspace 变更回写 source root。 | 支持 `dry_run`、`delete_removed`；兼容返回 `files_merged`。 |
| `workspace_abort_run` | 将 run 标记为 aborted。 | 直接更新状态并读取最新 run 返回。 |

#### prompt / command / shared file 类

| Tool | 功能 | 备注 |
|---|---|---|
| `prompt_list` | 列出 prompt template。 | 固定 keyword 检索，limit=50。 |
| `prompt_get` | 读取指定 prompt template。 | 使用 `prompt_key`。 |
| `command_list` | 列出 command card。 | 固定 keyword 检索，limit=50。 |
| `command_get` | 读取指定 command card。 | 使用 `card_key`。 |
| `shared_file_read` | 读取 shared file。 | 路径会做 `path.Clean` 和前导 `/` 清理。 |
| `shared_file_write` | 写入 shared file。 | 内容大小限制 10 MiB。 |

### 4.2 内部 JSON-RPC handlers（非 Claude 直接调用，但同属对外接口）

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

> `orchestration/rpc_types.go` 与 `workspace/rpc_types.go` 普遍兼容旧字段别名，如 `agentId`、`dagKey`、`runKey`、`selectedSkills`、`manualSkillSelection`、`outputSchema`、camelCase 布尔字段等。
>
> `orchestration.reportRuntime` 的 JSON 解码走 `decodeStrictRuntimeReportJSON()`，会显式拒绝未知字段。

---

## 5. 关键函数 / 方法

### 启动与传输层

| 签名 | 作用 |
|---|---|
| `func run() error` | 创建 Fx app，启动所有 runner。 |
| `func buildBootstrapConfig(shutdowner fx.Shutdowner, hookConsumer orchestration.HookConsumer, registry tools.Registry) bootstrap.Config` | 配置 bootstrap 注册、工具代理、能力声明、hook 回调。 |
| `func buildOrchestrationOptions(remoteAddr string) []fx.Option` | 根据是否存在 `GO_AGENT_CTL_RPC_ADDR` 选择 launch backend，并在本地模式注入 `runnerActor`。 |
| `func buildLauncher(lc fx.Lifecycle, turnStarter orchestration.TurnStarter, logger *slog.Logger, remoteAddr string) orchestration.AgentLauncher` | 选择 `localLauncher` 或 `remoteLauncher`。 |
| `func newStdioRunner(registry tools.Registry) platformrunner.Runner` | 用 stdio 启动 MCP server。 |
| `func newHTTPRunner(registry tools.Registry) platformrunner.Runner` | peer 模式启 HTTP MCP；否则返回阻塞 runner。 |
| `func (r bootstrapRunner) Run(ctx context.Context) error` | peer 模式下向主控注册，并订阅 `agent.session.start` / `agent.turn.*` / `agent.state.change` / `agent.process.exit` hooks。 |
| `func handleToolCall(ctx context.Context, registry tools.Registry, name string, args json.RawMessage) (any, error)` | `tools/call` 的统一查表与 handler 调度入口。 |
| `func bindRuntime(lc fx.Lifecycle, params runtimeParams)` | 把所有 runner 放进 `platformrunner.RunGroup` 统一管理。 |
| `func subscribeOrchestrationHooks(ctx context.Context, client hookSubscriber) error` | 旧式 hook 订阅辅助函数；主路径未直接使用。 |

### 编排核心

| 签名 | 作用 |
|---|---|
| `func NewService(...) *service` | 创建编排核心服务，初始化状态机配置和 agent map。 |
| `func (s *service) LaunchAgent(ctx context.Context, req LaunchRequest) error` | 统一入口：发起 agent 启动。 |
| `func (s *service) SubmitTurn(ctx context.Context, req TurnSubmission) error` | 统一入口：提交 turn。 |
| `func (s *service) StopAgent(ctx context.Context, agentID string) error` | 统一入口：停止 agent。 |
| `func (s *service) StopAllAgents()` | 进程退出时批量停止全部 agent。 |
| `func (s *service) Snapshot(ctx context.Context, agentID string) (AgentSnapshot, error)` | 读取单个 agent snapshot。 |
| `func (s *service) UpdateRuntime(ctx context.Context, report RuntimeReport) error` | 更新 runtime port/provider，并在快照变化时发布 runtime reported 事件。 |
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
| `func handleTurnCompletedEvent(...)` / `handleTurnInterruptedEvent(...)` | 收敛 turn 完成 / 中断事件，并在必要时强制修复回 idle。 |
| `func handleToolApprovalRequestedEvent(...)` / `handleToolApprovalResolvedEvent(...)` | 把审批类事件转换为 `awaiting_user_input` 状态流转。 |
| `func recoverAgent(ctx context.Context, s *service, agent *agentRuntime) (bool, error)` | 停掉旧进程、重置状态、重启、必要时回放 DAG wakeup。 |
| `func loadRecoveredTurnSubmission(ctx context.Context, s *service, agent *agentRuntime) (TurnSubmission, bool, error)` | 从 `task_dag_wakeups.prompt_payload` 恢复 turn payload。 |

### 工具与 workspace

| 签名 | 作用 |
|---|---|
| `func NewRegistry(deps Dependencies) Registry` | 汇总全部 MCP tools。 |
| `func makeHandler[T any, R any](...) ToolHandler` | 通用 handler 工厂：依赖检查 + 输入解码。 |
| `func resourceToolDefinitions(spec resourceToolSpec) []ToolDefinition` | 构造 prompt / command 这类 list/get 资源型工具。 |
| `func HandleLaunchAgent(svc contract.OrchestrationService) ToolHandler` | `orchestration_launch_agent` 实现；异步 launch。 |
| `func createDAGRequestFromInput(in CreateDAGInput) (contract.CreateDAGRequest, error)` | 把 tool 输入转成 service contract。 |
| `func createWorkspaceRun(ctx context.Context, svc workspace.Service, input WorkspaceCreateRunRequest) (*workspaceRunDTO, error)` | `workspace_create_run` 工具实现。 |
| `func workspaceRunDTOFromRun(ctx context.Context, svc workspace.Service, run *workspace.Run) (*workspaceRunDTO, error)` | 把 workspace service 输出补齐兼容字段与文件列表。 |
| `func (s *service) CreateRun(ctx context.Context, req CreateRunRequest) (*Run, error)` | 创建 workspace run，并 bootstrap 文件。 |
| `func (s *service) dryRunMerge(ctx context.Context, run *Run, req MergeRunRequest, updatedBy string) (*MergeRunResult, error)` | dry-run merge：`active -> merging -> active`。 |
| `func (s *service) executeMerge(ctx context.Context, run *Run, req MergeRunRequest, updatedBy string) (*MergeRunResult, error)` | 真正执行写回与状态迁移。 |
| `func (s *service) buildMergePlan(run *Run, files []RunFile, req MergeRunRequest) (*MergeRunResult, []storeworkspace.WorkspaceRunFile, error)` | 计算 tracked / removed / conflict / unchanged / error。 |
| `func (s *service) transitionMergeRun(...) (*Run, error)` | 基于 CAS 状态迁移 workspace run。 |
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
| `func (s *store) UpdateRunningNodeStatus(ctx context.Context, input RunningNodeStatusUpdate) (*Node, error)` | 节点运行时状态推进。 |
| `func (s *store) CompleteNode(ctx context.Context, input CompleteNodeInput) (*Node, error)` | 把节点推进到终态，并清空 active turn/wakeup 绑定。 |
| `func (s *store) EnqueueWakeup(ctx context.Context, input EnqueueWakeupInput) (int64, error)` | 入队 wakeup。 |
| `func (s *store) ClaimDueWakeups(ctx context.Context, input ClaimDueWakeupsInput) ([]Wakeup, error)` | 抢占可发送 wakeup。 |
| `func (s *store) MarkWakeupSent(ctx context.Context, input MarkWakeupSentInput) (int64, error)` | 把 wakeup 从 `dispatching` 标记成 `sent`。 |
| `func (s *store) RetryWakeup(ctx context.Context, input RetryWakeupInput) (int64, error)` | 派发失败后重回 `pending`。 |
| `func (s *store) FailWakeup(ctx context.Context, input FailWakeupInput) (int64, error)` | 派发失败并进入 `failed`。 |
| `func (s *store) AcquireWorkerLease(ctx context.Context, input AcquireWorkerLeaseInput) (int64, error)` | 抢占 worker lease。 |
| `func (s *store) RenewWorkerLease(ctx context.Context, input RenewWorkerLeaseInput) (int64, error)` | 续约 worker lease。 |
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
```

### 6.3 外部库要点

- `go.uber.org/fx`：依赖注入与生命周期。
- `github.com/kelindar/event`：事件总线。
- `github.com/creachadair/jrpc2` / `channel`：remote launcher 与 bootstrap RPC。
- `github.com/jackc/pgx/v5`：PostgreSQL。
- `github.com/qmuntal/stateless`：agent 状态机。

---

## 7. 编排流程

### 7.1 agent 启动流程

1. Claude 调用 `orchestration_launch_agent`。
2. `tools.HandleLaunchAgent()` 构造 `LaunchRequest`：
   - `AgentID = Name`
   - `Command = 当前 mcp-orch 可执行文件`
   - `Env` 仅注入 `AGENT_PROVIDER`（若传了 provider）
3. `service.LaunchAgent()` 进入 `launchAgentViaLauncher()`。
4. `prepareLauncherLaunch()`：
   - 校验 `agent_id/command`
   - 创建或复用 `agentRuntime`
   - 清空旧 queue / runtime / error / stop 标记
   - 必要时把旧状态通过 `recover_requested` 归一化回可重启态
   - 判断是否已在 launch 中
5. 根据 launch backend 分支：
   - **localLauncher**：直接 `exec.Command()` 启动本地进程。
   - **remoteLauncher**：通过 `thread/start` RPC 请求主控创建远端 thread，并返回 `thread_id/remote_agent_id`；同时把 `provider/model/cwd/prompt/instructions` 传过去。
6. `finishLauncherLaunch()`：
   - 成功：绑定 launch result，触发 `launch_succeeded`，状态进入 `idle`，发布 `agent_launched`
   - 失败：记录 `lastError`，触发 `launch_failed`，状态进入 `failed`
7. 若是本地模式，`runnerActor.startWaiters()` 为每个新 `cmd` 启动 `Wait()` 监控；退出时进入 `handleProcessExit()`。
8. 若是 peer + bootstrap 模式，`bootstrapRunner.Run()` 会订阅：
   - `agent.session.start`
   - `agent.turn.after`
   - `agent.turn.failed`
   - `agent.turn.progress`
   - `agent.state.change`
   - `agent.process.exit`
   这些事件由 `hookConsumer.After()` 回灌到 orchestration 内存态。
9. runtime 元数据补齐：
   - provider 既可以来自 launch 推断，也可以来自 hook `thread.started`
   - port/provider 也可通过 `orchestration.reportRuntime` 主动上报覆盖

### 7.2 消息传递 / turn 流程

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
   - 完成 / 中断：`turn_running|awaiting_user_input -> idle`
7. report 路径：
   - `hookConsumer.handleTurnCompleted()` 把 `result/summary/message` 写入 `lastReport`
   - `handleItemCompleted()` 遇到 final answer item 也会更新 report
   - `HandleReportEvent()` 在终态事件时 drain `reportRequesters`
8. 异常与恢复：
   - `runnerActor` 每 200ms tick 一次
   - 若 `turn_running` 且 `updatedAt` 超过 30s 未刷新，`StallDetector` 触发 `recoverWithReason(..., "stall_detected")`
   - `Recover()` 会重启 agent；若 DAG store 中存在与 `activeTurnID` 精确绑定、且 wakeup 仍是 `sent` 的记录，则把原 prompt 回放回队列

### 7.3 workspace 生命周期

1. Claude 调用 `workspace_create_run`。
2. `workspace.Service.CreateRun()`：
   - 校验 `run_key`（`[A-Za-z0-9_-]+`，长度 <= 128）
   - 解析 `source_root` / `workspace_path` 为绝对路径
   - 若未指定 workspace 目录，则默认 `<cwd or source_root>/.workspace/<run_key>`
   - 要求 `workspace_path != source_root`
   - 对 `files` 做相对路径去重与逃逸校验
   - 把 bootstrap 文件从 source root 复制到 workspace
   - 持久化 `workspace_runs` 与 `workspace_run_files`
   - 发布 `WorkspaceRunCreated`
3. 外部 agent 在 `workspace_path` 内进行文件编辑。
4. Claude 调用 `workspace_merge_run`。
5. `MergeRun()`：
   - 只允许 `status=active` 的 run 进入 merge
   - `dry_run=true`：`active -> merging -> active`，只计算结果，不写文件
   - 普通 merge：`active -> merging`
6. merge 计划构建：
   - 对每个 tracked file 读取 `baseline_sha256 / workspace_sha256 / source_sha256_before`
   - 生成 `merged / removed / unchanged / conflict / error`
   - 若 `delete_removed=true`，仅把**已跟踪且在 workspace 中消失**的文件视为删除候选
7. 实际写回：
   - `writeMergedSourceFile()` 原子拷贝 workspace 文件回 source，并拒绝 symlink 目标
   - `removeMergedSourceFile()` 删除 source 文件
   - 持久化 `workspace_run_files`
8. 收敛：
   - 若仅是业务冲突 / 评估错误：`merging -> failed`，发送 `WorkspaceRunMergeError`
   - 若中途写盘 / 持久化失败：先尝试恢复 file state，再 `merging -> failed`
   - 若全部成功：`merging -> merged`，发送 `WorkspaceRunMerged`
9. 中止：
   - `workspace_abort_run` 走 `UpdateRunStatus()`，直接把 run 状态改为 `aborted`，并发送 `WorkspaceRunAborted`

### 7.4 DAG / wakeup / lease / 恢复相关存储流

虽然当前 MCP tool 只暴露了 DAG 的 create/get/update，但 `taskdag.Store` 已经实现了更完整的运行时存储：

- `task_dags`：DAG 主记录，支持 list/get/for-update
- `task_dag_nodes`：
  - 主数据 CRUD
  - `active_turn_id / active_wakeup_id / last_event_at`
  - runtime 状态推进（`pending -> running -> awaiting_verify -> done/failed` 等）
- `task_dag_wakeups`：
  - prompt 派发
  - `pending -> dispatching -> sent`
  - retry / fail / reclaim stale dispatching
  - turn 绑定 fencing
- `task_dag_worker_leases`：worker 抢占 / 续约 / 释放

当前这些能力主要用于：

1. **恢复 replay**：`recover.go` 根据 `assigned_to + active_turn_id + active_wakeup_id` 找回原 prompt payload。
2. **未来 watcher / dispatcher 扩展**：store + SQL 已齐备，但尚未全部暴露成 MCP tools。
3. **内部 RPC 扩展面**：已存在 `task/dag/list`，但尚无 Claude 侧直接工具入口。

## 补充观察

- `hook_subscription.go` 的 `subscribeOrchestrationHooks()` 是辅助函数，且使用订阅模式 `sync`；真正生产路径在 `bootstrapRunner.Run()` 中直接调用 `SubscribeHooks(..., "after")`。
- `remoteLauncher.IsRunning()` 只看 `remoteThreadID != ""`；远程实际运行态仍要靠 hooks / state sync 纠偏。
- `hookConsumer.handleThreadStarted()` 只从 hook 同步 provider / thread id；runtime port 仍需 `orchestration.reportRuntime` 主动上报。
- `runtime provider` 若不是 `claude/codex`，会被打上 `runtime-unverified` 的 source，但不会拒绝写入 snapshot。
- `workspace_tool_compat.go` 继续兼容旧 DTO：`workspace_root` 是 `workspace_path` 别名，`files_merged` 是 `merged` 别名。
- `shared_file_tools.go` 会把 `\` 转 `/`、做 `path.Clean()`，再去掉前导 `/`，因此 shared file path 实际是逻辑路径而不是磁盘绝对路径。
- `prompt_list` / `command_list` 都是基于通用 `resourceToolDefinitions()` 生成的 list/get 资源型工具，list limit 固定 50。
- `task_ack.sql` 已有 sqlc 生成产物，但 `cmd/mcp-orch/store/` 尚无手写 store/module，也没有工具暴露。

## 审查补遗

本次按要求逐项核对了：

- `cmd/mcp-orch/` 顶层 Go 文件
- `cmd/mcp-orch/orchestration/` 全部 Go 文件
- `cmd/mcp-orch/tools/` 全部 Go 文件
- `cmd/mcp-orch/workspace/` 全部 Go 文件
- `cmd/mcp-orch/store/` 各子目录与 Go 文件
- `cmd/mcp-orch/sql/queries/` 全部 SQL 文件

审查后直接修正 / 补充的重点有：

1. **修正运行模式描述**：`GO_AGENT_CTL_RPC_ADDR` 控制的是 launcher 后端，和 `GO_AGENT_PEER_MODE` 是否启 HTTP / bootstrap 是正交关系。
2. **补全 store / SQL 明细**：把 `store.go`、`store_lease.go`、`store_wakeup.go`、`sql/queries/README.md` 等实际文件职责写清了。
3. **补全遗漏的内部能力**：补上 `task/dag/list`、workspace file RPC、runtime 上报、report requester、wakeup / lease / recovery 细节。
4. **补全工具兼容与约束**：补上 `workspace_root` / `files_merged` 兼容字段、`shared_file_write` 10 MiB 限制、prompt/command list limit=50。
5. **修正若干流程细节**：尤其是本地 / 远程 turn 启动差异、workspace dry-run 状态回滚、删除候选的真实判定条件。

结论：当前文档已覆盖 `mcp-orch` 生产代码主路径；测试文件本次也作为核对依据使用，但文档主体仍以**生产代码地图**为主，不逐个展开 `*_test.go`。
