# Store 层总审查（19 repo + sqlc）

审查范围：

- `internal/store/module.go`
- `internal/store/sqlc/`
- `internal/store/` 下 19 个 repo 子包
- 对照范围补充：`internal/app/modules.go`、`sql/queries/`、`migrations/`、`go-agent-v2/internal/store/`

审查方式：

- 按要求以 LSP 为主取证：`text_search`、`workspace_symbol`、`references(compact)`、`call_hierarchy`、`read_file`、`document_symbol`
- 只用只读脚本做计数、目录盘点、import 汇总、SQL 名称比对；未修改业务代码

## 结论摘要

- 结构面：V3 `internal/store/` 目前确实有 19 个 repo，且 19 个 repo 全部具备 `module.go + contract.go + store.go` 三件套。
- 聚合面：`internal/store/module.go:28-49` 已聚合 `sqlc.New` 和全部 19 个 repo；`internal/app/modules.go:20,31` 只通过 `store.Module` 引入，无 app 侧绕过。
- 生成层面：`internal/store/sqlc/querier.go:8-110` 当前有 101 个方法；但 `sql/queries/*.sql` 只有 100 个 `-- name:`，多出来的是 `UpdateAgentProviderBindingArchived`。这说明生成层与 SQL 源已经漂移。
- 事务面：store 层只有两个显式事务入口：`taskdag.Store.WithTx` 与 `workspace.Store.WithTx`。`workspace.persistRun` 在 service 层，确实通过 `store.WithTx` 包住 `UpsertRun + UpsertFile`。
- 对照面：按 V2“20 个 store”口径，V3 少掉的独立 store 是 `AgentThreadBindingStore`；另外 `dbquery` 虽然保留了 repo 名，但当前只剩 placeholder，不是 V2 `DBQueryStore.Query` 的功能等价迁移。

## 主要发现

1. `sqlc` 生成结果与 SQL 源不一致。
`sqlc.yaml:4-12` 明确声明生成输入来自 `sql/queries/` 和 `migrations/`；但当前 `Querier` 比 SQL 源多 1 个方法：`UpdateAgentProviderBindingArchived`。对应实现存在于 `internal/store/sqlc/query_agent_binding.go:10,36-38`，但 `sql/queries/agent_provider_binding.sql:1-31` 没有对应 `-- name:`。如果重新生成，`binding/store.go:66-72` 这条调用链有失配风险。

2. V2 -> V3 的 store 数量差异，本质上是 `AgentThreadBindingStore` 没有以独立 repo 落到 V3。
V2 文档同时列出了 `AgentThreadBindingStore` 和 `AgentProviderBindingStore`，见 `go-agent-v2/internal/store/doc.go:6-27`；V2 代码里二者也确实是两个独立 store，见 `go-agent-v2/internal/store/agent_thread_binding.go:28-479` 与 `go-agent-v2/internal/store/agent_provider_binding.go:27-140`。V3 只有 `binding` 与 `thread` 两个 repo，没有 `threadbinding` repo。

3. 错误处理没有形成统一包装层。
`internal/store/` 下未搜到 `%w` 和 `fmt.Errorf(...)` 包装；绝大多数方法是原样 `return err` / `return nil, err`。唯一例外是 `binding/store.go:30-52` 的唯一键冲突幂等处理，以及 `sqlc/db.go:51-58` 的事务前置错误。调试上下文偏薄。

## 1. repo 清单

包名与目录名一致，文件数如下：

| repo | 文件数 |
| --- | ---: |
| `agentstatus` | 3 |
| `ailog` | 3 |
| `auditlog` | 3 |
| `binding` | 3 |
| `buslog` | 3 |
| `commandcard` | 3 |
| `cwdlock` | 3 |
| `dbquery` | 3 |
| `interaction` | 3 |
| `prompt` | 3 |
| `sharedfile` | 3 |
| `systemlog` | 3 |
| `taskack` | 3 |
| `taskdag` | 3 |
| `tasktrace` | 3 |
| `thread` | 3 |
| `topologyapproval` | 3 |
| `uipreference` | 3 |
| `workspace` | 3 |

结论：19 个 repo 均存在，且全部是 3 文件结构。

## 2. 三件套完整性

- 19/19 repo 全部具备 `module.go + contract.go + store.go`。
- 19/19 repo 的 `module.go` 都是 `fx.Module(..., fx.Provide(NewStore))` 形态。
- `workspace_symbol` 结果显示 `internal/store/*/contract.go` 下共 19 个 `Store` interface，和 repo 数一致。

结论：三件套完整性为 `19/19`，无缺项。

## 3. `store/module.go` 聚合

- `internal/store/module.go:28-49` 聚合内容为：`fx.Provide(sqlc.New)` + 19 个 repo 的 `Module`。
- 目录盘点出的 19 个 repo 名，与 `store.Module` 中枚举的 19 个模块完全一致；无 missing / extra。
- `references(compact)` 结果显示 `store.Module` 只在 `internal/app/modules.go:31` 被应用层引用。

结论：S4 后的顶层聚合已到位，确实覆盖全部 19 个 repo。

## 4. sqlc 方法统计

- `internal/store/sqlc/querier.go:8-110` 的 `Querier` 接口共有 `101` 个方法。
- `sql/queries/*.sql` 中共提取出 `100` 个 `-- name:`。
- 差集只有一个：`UpdateAgentProviderBindingArchived`。

结论：

- 当前编译面上的 `Querier` 方法数是 `101`。
- 当前 SQL 源查询数是 `100`。
- 两者不完全一致。

## 5. contract vs sqlc 对齐（抽查 3 个 repo）

### 5.1 `binding`

证据：

- contract：`internal/store/binding/contract.go:7-14`
- store：`internal/store/binding/store.go:18-81`
- sqlc 生成：`internal/store/sqlc/query_agent_binding.go:20-42`
- SQL 源：`sql/queries/agent_provider_binding.sql:1-31`

对应关系：

- `GetByProviderThread` -> `GetAgentProviderBindingByProviderThread`
- `Upsert` -> `UpsertAgentProviderBinding`
- `DeleteByAgentID` -> `DeleteAgentProviderBindingByAgentID`
- `UpdateSessionUUID` -> `UpdateAgentProviderBindingSessionUUID`
- `SetArchived` -> `UpdateAgentProviderBindingArchived`
- `GetByAgentID` -> `GetAgentProviderBindingByAgentID`

结论：

- contract 和生成层 `sqlc` 方法表面是对齐的。
- 但 `SetArchived` 对应的 `UpdateAgentProviderBindingArchived` 只存在于生成层，不存在于 SQL 源；这是本次审查发现的唯一一处 source/generated 漂移。

### 5.2 `thread`

证据：

- contract：`internal/store/thread/contract.go:7-22`
- store：`internal/store/thread/store.go:17-134`
- SQL 源：`sql/queries/agent_thread.sql:1-150`

对应关系：

- `GetByThreadID` -> `GetAgentThreadByID`
- `GetByPort` -> `GetAgentThreadByPort`
- `ListAll` -> `ListAgentThreads`
- `ListRunning` -> `ListRunningAgentThreads`
- `ListRecoverable` -> `ListRecoverableAgentThreads`
- `ListRunningAgents` -> `ListRunningAgents`
- `Upsert` -> `UpsertAgentThread`
- `UpdateStatus` -> `UpdateAgentThreadStatus`
- `DeleteByThreadID` -> `DeleteAgentThreadByID`
- `ResetRunning` -> `ResetRunningAgentThreads`
- `ExpireStale` -> `ExpireStaleAgentThreads`
- `RunningExists` -> `AgentThreadRunningExists`
- `ListCwds` -> `ListAgentThreadCwds`
- `ListCwdsByPrefix` -> `ListAgentThreadCwdsByPrefix`

结论：抽查结果完全对齐。

### 5.3 `workspace`

证据：

- contract：`internal/store/workspace/contract.go:9-19`
- store：`internal/store/workspace/store.go:15-116`
- SQL 源：`sql/queries/workspace_run.sql:1-84`
- 事务底座：`internal/store/sqlc/db.go:51-58`

对应关系：

- `WithTx` -> `sqlc.Queries.WithTx`
- `UpsertRun` -> `UpsertWorkspaceRun`
- `GetRun` -> `GetWorkspaceRun`
- `ListRuns` -> `ListWorkspaceRuns`
- `UpdateRunStatus` -> `UpdateWorkspaceRunStatus`
- `TransitionRunStatus` -> `TransitionWorkspaceRunStatus`
- `UpsertFile` -> `UpsertWorkspaceRunFile`
- `GetFile` -> `GetWorkspaceRunFile`
- `ListFiles` -> `ListWorkspaceRunFiles`

结论：抽查结果完全对齐。

## 6. store 实现质量（抽查 3 个 repo）

### 6.1 `binding/store.go`

证据：

- `internal/store/binding/store.go:18-96`
- `internal/store/sqlc/models.go:35-46`
- `internal/store/binding/contract.go:39-50`

检查点：

- `Binding` 与 `sqlc.AgentProviderBinding` 字段和类型一致：`string/bool/int64` 全部直接对位。
- `mapBinding` 是纯字段搬运，没有错误的零值覆盖或类型截断。
- `Upsert` 的唯一键冲突分支通过 `platformdb.IsUniqueViolation` + 回查现有记录实现幂等容忍，逻辑合理。

结论：类型映射正确；唯一风险不是类型问题，而是 `SetArchived` 对应 SQL 源缺失。

### 6.2 `taskdag/store.go`

证据：

- `internal/store/taskdag/store.go:15-255`
- `internal/store/taskdag/contract.go:9-214`
- `internal/store/sqlc/models_extra.go:62-117`

检查点：

- `DAG`、`Node`、`Wakeup` 的字段类型与 `sqlc.TaskDag` / `sqlc.TaskDagNode` / `sqlc.TaskDagWakeup` 对齐。
- `json.RawMessage`、`*time.Time`、`*string`、`*int64`、`int32` 都是同型直传，没有看到手写转换错误。
- `fromDAG` / `fromNode` / `fromWakeup` 是明确的 field-to-field 映射。

结论：抽查范围内未发现类型转换错误。

### 6.3 `workspace/store.go`

证据：

- `internal/store/workspace/store.go:15-149`
- `internal/store/workspace/contract.go:9-75`
- `internal/store/sqlc/models_extra.go:179-205`
- `internal/store/sqlc/query_types_extra.go:8-60`

检查点：

- `WorkspaceRun` / `WorkspaceRunFile` 与 sqlc 模型同型：`json.RawMessage`、`time.Time`、`*time.Time` 全部直传。
- `WithTx` 会把事务内的 `*sqlc.Queries` 重新包装成 `&store{q: txq}`，事务边界清晰。
- `ListRuns` / `ListFiles` 的 slice 构造容量正确，`fromSQLCRun` / `fromSQLCFile` 无字段遗漏。

结论：抽查范围内未发现类型转换错误。

## 7. 依赖方向（全量 import 扫描）

结论先行：

- 如果只看 repo 的 `store.go` 实现层，依赖方向基本干净：只依赖 `sqlc` + 标准库，唯一额外例外是 `binding/store.go` 依赖 `internal/platform/db`。
- 如果看整个 `internal/store` 树，则不能说“只依赖 sqlc + 标准库 + platform/db”，因为还包含 `go.uber.org/fx`、顶层 `module.go` 对兄弟 repo 的聚合，以及 `sqlc/db.go` 对 `pgx` 的依赖。

分层结果：

- `contract.go`：只见标准库依赖，主要是 `context` / `encoding/json` / `time`
- repo `module.go`：统一依赖 `go.uber.org/fx`
- repo `store.go`：统一依赖 `internal/store/sqlc`
- 额外例外：`internal/store/binding/store.go:6-7` 同时依赖 `internal/platform/db`
- 顶层聚合：`internal/store/module.go:3-25` 依赖 `fx + 19 个兄弟 repo + sqlc`
- 生成层：`internal/store/sqlc/db.go:3-10` 依赖 `internal/platform/db + pgx`

未发现的问题：

- 未发现 repo store 反向依赖 `internal/module/*`
- 未发现 repo store 直接依赖 provider / rpc / orchestration 等上层模块

## 8. 事务使用

store 层的事务入口只有两处：

- `internal/store/taskdag/store.go:15-19` -> `taskdag.Store.WithTx`
- `internal/store/workspace/store.go:15-19` -> `workspace.Store.WithTx`

二者都下沉到：

- `internal/store/sqlc/db.go:51-58` -> `sqlc.Queries.WithTx`
- `sqlc.Queries.WithTx` 再调用 `platformdb.WithTx`

实际调用点只有两处：

- `internal/sidecar/orch/orchestration/dag.go:20-34`
- `internal/module/workspace/service_helpers.go:168-178`

`workspace.persistRun` 结论：

- `internal/module/workspace/service_helpers.go:166-180` 的 `persistRun` 不在 store 层，而在 service 层。
- 它确实通过 `s.store.WithTx(...)` 把 `txStore.UpsertRun(...)` 与 `upsertRunFilesWithStore(...)` 包在同一事务里。
- `call_hierarchy` 显示它的调用方只有 `internal/module/workspace/service.go:55-73` 的 `CreateRun`。

补充：

- `internal/sidecar/orch/orchestration/dag.go:14-39` 的 `CreateDAG` 也是通过 `taskdag.Store.WithTx`，把 DAG 主记录、节点和明细装载放在同一事务内。

## 9. 错误处理

扫描结果：

- `internal/store/` 下未搜到 `%w`
- `internal/store/` 下未搜到 `fmt.Errorf(`
- `errors.New(...)` 只在 `internal/store/sqlc/db.go:53` 出现一次：`sqlc queries requires pool-backed store for transactions`
- 大量 repo store 采用直接透传：`return err` / `return nil, err`

唯一显式特殊处理：

- `internal/store/binding/store.go:30-52` 会捕获唯一键冲突。
- 如果现有记录与待写记录是同一逻辑绑定，则把冲突视为幂等成功。

结论：

- 目前没有统一错误包装层，也没有统一 sentinel/error taxonomy。
- 对上层来说，store 层错误上下文较薄，定位多依赖调用栈而不是错误消息本身。

## 10. fx 注册

证据：

- `internal/app/modules.go:20` 只 import 顶层 `internal/store`
- `internal/app/modules.go:31` 只把 `store.Module` 放进 `fx.Options(...)`
- `references(compact)` 结果显示 `store.Module` 的应用侧引用只有这里

结论：app 侧是通过 `store.Module` 单点引入，没有绕过顶层聚合直接塞 repo module。

## 11. schema 一致性

### 11.1 查询表与 migrations 是否对齐

对 `sql/queries/*.sql` 做表级提取后，当前查询触达的表有：

- `agent_interactions`
- `agent_provider_binding`
- `agent_status`
- `agent_threads`
- `audit_events`
- `bus_exception_logs`
- `command_card_runs`
- `command_card_versions`
- `command_cards`
- `cwd_instance_locks`
- `prompt_templates`
- `prompt_versions`
- `shared_files`
- `system_logs`
- `task_acks`
- `task_dag_nodes`
- `task_dag_wakeups`
- `task_dag_worker_leases`
- `task_dags`
- `task_traces`
- `topology_approvals`
- `ui_preferences`
- `workspace_run_files`
- `workspace_runs`

这些表在 `migrations/` 中都能找到建表或后续变更来源，未发现“查询命中了不存在表”的情况。

代表性证据：

- `agent_provider_binding`：`migrations/0021_agent_provider_binding.sql:9-23`，`migrations/0022_binding_session_uuid.sql:7`
- `workspace_runs`：`migrations/0006_workspace_runs.sql:6-25`
- `task_dags`：`migrations/0004_ack_dag.sql:33-47`

### 11.2 migrations 中有、当前 sqlc 没有触达的表

当前未被 `sql/queries/*.sql` 命中的 migration 表有：

- `agent_codex_binding`
- `prompt_template_versions`
- `prompts`
- `schema_migrations`
- `topology_approval_archives`

解释：

- `prompt_template_versions` 是历史名；`migrations/0003_task_trace_prompt_versions.sql:1-36` 已把它迁到 `prompt_versions`
- `schema_migrations` 是迁移基础设施表，不属于业务 store
- `agent_codex_binding` 是旧兼容表，见 `migrations/0013_agent_codex_binding.sql:15-42`
- `topology_approval_archives` 与 `prompts` 仍在 schema 中，但当前 V3 store/sqlc 未覆盖

### 11.3 生成层与 SQL 源的真实不一致

这是本次 schema/sqlc 审查里最重要的点：

- `sqlc.yaml:4-12` 配置说明生成输入应来自 `sql/queries/`
- `sql/queries/agent_provider_binding.sql:1-31` 只有 5 个 `-- name:`
- `internal/store/sqlc/query_agent_binding.go:5-11,36-38` 却多出 `updateAgentProviderBindingArchivedSQL` 与 `UpdateAgentProviderBindingArchived(...)`
- `internal/store/binding/store.go:66-72` 已经依赖这个生成方法

结论：

- 表结构本身和 queries 触达表是对齐的
- 但 SQL 源与生成代码并不完全一致，`binding` 是明确漂移点

## 12. V2 对照

### 12.1 V3 19 个 repo，少了哪个

按 V2 “20 个 store” 口径，V3 少掉的独立 store 是：

- `AgentThreadBindingStore`

依据：

- V2 文档同时列出 `AgentThreadBindingStore` 与 `AgentProviderBindingStore`：`go-agent-v2/internal/store/doc.go:6-27`
- V2 的 `AgentThreadBindingStore` 是完整独立 store：`go-agent-v2/internal/store/agent_thread_binding.go:28-479`
- V2 的 `AgentProviderBindingStore` 也是独立 store：`go-agent-v2/internal/store/agent_provider_binding.go:27-140`
- V3 只有 `internal/store/binding/` 与 `internal/store/thread/`，没有 `internal/store/threadbinding/`

### 12.2 为什么不是 `binding` 缺失

V3 的 `binding` 更像 V2 的 `AgentProviderBindingStore`，因为它的表面方法是：

- `GetByProviderThread`
- `Upsert`
- `DeleteByAgentID`
- `UpdateSessionUUID`
- `SetArchived`
- `GetByAgentID`

这与 V2 provider-binding 面更接近，而不是 V2 thread-binding 那个兼容层的厚接口。

### 12.3 额外 parity 说明：`dbquery` 仍未等价迁移

虽然 V3 有 `internal/store/dbquery/` repo，但它当前只是 placeholder：

- V3 contract：`internal/store/dbquery/contract.go:5-11`
- V3 store：`internal/store/dbquery/store.go:15-24`
- V3 SQL：`sql/queries/db_query.sql:1-8`
- V3 sqlc：`internal/store/sqlc/query_db_query.go:5-17`

而 V2 的 `DBQueryStore` 支持运行时只读 SQL：

- `go-agent-v2/internal/store/db_query.go:14-30`

结论：

- “19 vs 20” 的缺失 store 名称是 `AgentThreadBindingStore`
- 但除数量差异外，`dbquery` 也存在明显功能降级，不应视为 V2 parity 已完成

## 总评

- 结构完整性：通过
- 顶层聚合：通过
- fx 引入路径：通过
- import 依赖方向：基本通过
- 事务边界：清晰，但只覆盖 `taskdag/workspace`
- store 类型映射质量：抽查通过
- 主要风险 1：`sqlc` 生成层与 SQL 源漂移，`binding` 已出现实锤
- 主要风险 2：V2 `AgentThreadBindingStore` 未以独立 repo 落地
- 主要风险 3：`dbquery` 仍是 placeholder，V2 parity 未达成
- 次要风险：错误处理没有统一包装，诊断上下文偏少
