# V2↔V3 1:1 对齐：Store 层

## 范围与方法

- V2 基线按 `go-agent-v2/internal/store/doc.go:6-27` 的 20 个 store 口径。
- V3 基线按 `internal/store/module.go:28-49` 的 19 个 repo 口径。
- 读取方式只用 LSP：`read_file`、`document_symbol`、`text_search`；未使用 `grep/find/cat/sed/awk`。
- sqlc 基线以 `internal/store/sqlc/querier.go:8-110` 为总表，并抽读对应 `query_*.go`。

## 总结

- 总体结论：`⚠️ 不满足严格 1:1`
- 逐项结论：`12 个 ✅ / 5 个 ⚠️ / 3 个 ❌`
- 明确缺失项不是“模糊感受”，而是三层同时缺：
  - V3 `internal/store` 没有独立 `threadbinding` repo。
  - V3 `internal/store/sqlc/querier.go:8-110` 没有任何 `ThreadBinding` 生成方法。
  - V2 `go-agent-v2/internal/store/agent_thread_binding.go:28-479` 仍是完整独立 store 面。
- `WrapStoreError` 覆盖面在当前 V3 repo 层已经是 `19/19`，不是局部试点。
- 显式事务只在 `taskdag` 和 `workspace` 两个 repo 暴露；其余 repo 主要依赖单 SQL 原子性。
- 生成层漂移最明显的 4 个点：
  - `AgentThreadBindingStore` 整块没有进入 V3/sqlc。
  - `DBQueryStore` 退化成 placeholder。
  - `AILogStore` 不再是独立 AI 日志视图，而是挂在 `system_log` 生成层上的原始日志列表。
  - `prompt/commandcard/uipreference/cwdlock/systemlog` 都有不同程度的 repo/sqlc 能力收缩或协议变化。

## 逐项对比

| V2 store | V3 repo / sqlc | 方法覆盖度（V2 -> V3 sqlc） | 事务使用 | WrapStoreError | sqlc 生成层漂移 | 结论 |
| --- | --- | --- | --- | --- | --- | --- |
| `AgentStatusStore` | `agentstatus` | `Upsert/Get/List` 1:1 对齐 | 无显式 tx | 是 | 无明显漂移 | ✅ |
| `AgentThreadStore` | `thread` | V2 12 个方法全部有对应；主要是重命名与拆分：`FindByPort -> GetByPort`，`ListRunning -> ListRunningAgents`，`ListRunningFull -> ListRunning`，`Delete -> DeleteByThreadID`，`ResetRunningToCreated -> ResetRunning`，`ExpireStaleAgents -> ExpireStale`，`ExistsRunning -> RunningExists`，`ListCwdMap -> ListCwds`，`ListCwdMapByCwd -> ListCwdsByPrefix`；V3 另增 `GetByThreadID/ListAll` | 无显式 tx | 是 | 无明显漂移 | ✅ |
| `AgentThreadBindingStore` | 无独立 repo；仅 `binding + thread` 局部覆盖 | 只有 `UpdateSessionUUID/SetArchived/FindBindingByAgentID` 等片段能勉强映射；`Bind/BindWithProvider/Unbind/UpdateCwd/ListAll/ListProviderMap/ListCwdMap/Rebind` 无独立对等面 | 无独立 tx；V2 有 `runRebindTx` | 不适用 | V3 无 `threadbinding` repo/contract/query | ❌ |
| `AgentProviderBindingStore` | `binding` | `Upsert/DeleteByAgentID/UpdateSessionUUID/FindByAgentID` 对齐；V3 另增 `GetByProviderThread/SetArchived` | 无显式 tx | 是 | `binding` repo 同时承接部分 thread-binding 残余字段，但 sqlc 仍只生成 provider binding 面 | ✅ |
| `InteractionStore` | `interaction` | `Create/Get/List/Review` 1:1 对齐 | 无显式 tx | 是 | 无明显漂移 | ✅ |
| `TaskTraceStore` | `tasktrace` | `Create -> Insert`，`List` 对齐 | 无显式 tx | 是 | 无明显漂移 | ✅ |
| `TaskDAGStore` | `taskdag` | V2 core + phase1 的 DAG/node/wakeup/lease/tx 能力都能映射到 V3；V3 只是把状态更新拆得更细 | `WithTx` | 是 | 生成层拆成 `query_task_dag.go` + `query_task_dag_wakeup.go`，属扩展非缺失 | ✅ |
| `TaskAckStore` | `taskack` | `Save -> Upsert`，`List` 对齐 | 无显式 tx | 是 | 无明显漂移 | ✅ |
| `CommandCardStore` | `commandcard` | `Save -> Upsert + InsertVersion`，`Get/List/Delete` 对齐；缺独立 `SetEnabled` | 无显式 tx | 是 | `query_command_card.go` 没有单独 `SetEnabled` query，改成整卡 `Upsert` | ⚠️ |
| `PromptTemplateStore` | `prompt` | `Save -> Upsert + InsertVersion`，`Get/List` 对齐；缺 `SetEnabled/Delete` | 无显式 tx | 是 | `query_prompt.go` 只有 `Get/InsertVersion/Upsert/List` | ⚠️ |
| `AuditLogStore` | `auditlog` | `Append -> Insert`，`List` 对齐 | 无显式 tx | 是 | 无明显漂移 | ✅ |
| `SystemLogStore` | `systemlog` | `Append -> Insert`，`ListV2 -> List` 基本对齐；缺 `ListFilterValues` | 无显式 tx | 是 | `query_system_log.go` 无 distinct-filter helper | ⚠️ |
| `AILogStore` | `ailog` | V2 `Query(category, keyword, limit)` 带分类/HTTP/status/model 派生；V3 仅 `List(keyword, limit)` 返回原始 `system_log` 映射，能力不等价 | 无显式 tx | 是 | 无独立 `query_ai_log.go`；生成方法是 `query_system_log.go` 里的 `ListAILogSystemLogs` | ❌ |
| `BusLogStore` | `buslog` | `List` 1:1 对齐 | 无显式 tx | 是 | 无明显漂移 | ✅ |
| `SharedFileStore` | `sharedfile` | `Write/Read/List/Delete -> Upsert/Get/List/Delete` | 无显式 tx | 是 | 无明显漂移 | ✅ |
| `TopologyApprovalStore` | `topologyapproval` | `Create/Approve/Reject/GetPending -> Create/Approve/Reject/ListPending` | 无显式 tx | 是 | 无明显漂移 | ✅ |
| `DBQueryStore` | `dbquery` | V2 `Query` 是只读 SQL 执行器；V3 只剩 `Placeholder` | 无显式 tx | 是 | `query_db_query.go` 仅生成 `PlaceholderDBQuery` | ❌ |
| `CwdLockStore` | `cwdlock` | V2 `Acquire` 封装“抢锁/查 holder/死进程强占/冲突错误”；V3 拆成 `Acquire + ForceAcquire + GetHolder`，API 协议变化明显 | 无显式 tx | 是 | `query_cwd_lock.go` 只暴露 primitive ops，没有 V2 `CwdLockedError` 级别协议 | ⚠️ |
| `UIPreferenceStore` | `uipreference` | 核心存取还在；但 V2 `Get` 带 context-scope/global fallback，`GetAll` 带全局+项目合并；V3 改成显式 `cwd` + raw JSON `GetValue/List` | 无显式 tx | 是 | `query_ui_preference.go` 仅保留原子读写，没有 scope merge helper | ⚠️ |
| `WorkspaceRunStore` | `workspace` | `SaveRun/GetRun/ListRuns/UpdateRunStatus/TryTransitionRunStatus/SaveFile/GetFile/ListFiles` 全覆盖；V3 另增 `WithTx` | `WithTx` | 是 | 无明显漂移 | ✅ |

## 红项

### 1. `AgentThreadBindingStore` 没有以独立 repo/sqlc 落地

- V2 独立 store 证据：`go-agent-v2/internal/store/agent_thread_binding.go:28-479`
- V3 repo 总表只有 19 个 repo：`internal/store/module.go:28-49`
- `internal/store` 下搜索不到 `threadbinding`
- `internal/store/sqlc/querier.go:8-110` 无任何 `ThreadBinding` 相关方法
- 当前 V3 最接近的是：
  - `internal/store/binding/contract.go:7-14`
  - `internal/store/thread/contract.go:7-21`
- 但这两者拼起来，仍然缺 V2 独立兼容层上的 `Bind/BindWithProvider/Unbind/UpdateCwd/ListAll/ListProviderMap/ListCwdMap/Rebind`

结论：这不是“换名迁移”，而是 store 面收缩。

### 2. `AILogStore` 从“派生视图”退化成“原始 system_log 列表”

- V2 `Query` 会按 message 计算 `Category/Method/URL/Endpoint/StatusCode/StatusText/Model`，并支持 `category` 过滤：
  - `go-agent-v2/internal/store/ai_log.go:12-16`
  - `go-agent-v2/internal/store/ai_log.go:18-48`
  - `go-agent-v2/internal/store/ai_log.go:50-85`
- V3 contract 只保留 `List(keyword, limit)`，返回字段变成原始日志视图：
  - `internal/store/ailog/contract.go:9-34`
  - `internal/store/ailog/store.go:18-31`
- sqlc 也没有独立 AI log 生成层，而是挂在 `system_log` 上：
  - `internal/store/sqlc/query_system_log.go:25-27`

结论：这是能力和结果形态双重退化，不是简单重命名。

### 3. `DBQueryStore` 仍是 placeholder

- V2 是只读 SQL 查询能力：
  - `go-agent-v2/internal/store/db_query.go:14-31`
- V3 contract/store 明确只剩 placeholder：
  - `internal/store/dbquery/contract.go:5-11`
  - `internal/store/dbquery/store.go:16-26`
- sqlc 生成层同样只有 placeholder：
  - `internal/store/sqlc/query_db_query.go:15-17`

结论：这一块还没有实际迁移。

## 黄项

### 1. `prompt` 和 `commandcard` 没做到严格 1:1

- V2 `CommandCardStore` 有 `Save/Get/List/SetEnabled/Delete`：
  - `go-agent-v2/internal/store/command_card.go:15-75`
- V3 `commandcard` 变成 `Upsert/Get/List/Delete + InsertVersion/ListVersions`，没有独立 `SetEnabled`：
  - `internal/store/commandcard/contract.go:9-16`
  - `internal/store/sqlc/query_command_card.go:32-54`
- V2 `PromptTemplateStore` 有 `Save/Get/List/SetEnabled/Delete`：
  - `go-agent-v2/internal/store/prompt_template.go:15-73`
- V3 `prompt` 只有 `Get/InsertVersion/Upsert/List`：
  - `internal/store/prompt/contract.go:9-14`
  - `internal/store/sqlc/query_prompt.go:18-32`

结论：版本归档能力反而更明确了，但 `Save` 的隐式归档副作用被拆成显式两步调用，而且 direct toggle/delete API 收缩了。

### 2. `systemlog` 少了 filter-values helper

- V2 有 `ListV2` 和 `ListFilterValues`：
  - `go-agent-v2/internal/store/system_log.go:20-43`
- V3 只有 `List/Insert`：
  - `internal/store/systemlog/contract.go:9-12`
  - `internal/store/sqlc/query_system_log.go:17-27`

结论：主查询还在，但辅助筛选能力没迁。

### 3. `cwdlock` 改成了更底层的协议

- V2 `Acquire` 自带“先原子抢锁，再看 holder 是否已死，再 force acquire，否则返回 `CwdLockedError`”：
  - `go-agent-v2/internal/store/cwd_lock.go:45-132`
- V3 把它拆成 `Acquire/ForceAcquire/GetHolder` 三个原子调用：
  - `internal/store/cwdlock/contract.go:8-15`
  - `internal/store/sqlc/query_cwd_lock.go:20-42`

结论：能力还能组合出来，但不再是 V2 的一站式 store 协议。

### 4. `uipreference` 从“上下文作用域 API”改成了“显式 cwd/raw json API”

- V2 依赖 context scope，并有全局 fallback/项目覆盖合并：
  - `go-agent-v2/internal/store/preference_scope.go:10-33`
  - `go-agent-v2/internal/store/ui_preference.go:37-116`
- V3 contract 变成显式 `cwd` 参数，返回 `json.RawMessage`：
  - `internal/store/uipreference/contract.go:9-26`
  - `internal/store/sqlc/query_ui_preference.go:20-30`

结论：底层数据模型还在，但 V2 调用约定没有被 1:1 迁过来。

### 5. `binding` 没有显式 tx，无法替代 V2 thread-binding 的兼容事务面

- V3 显式事务只在：
  - `internal/store/taskdag/store.go:16-20`
  - `internal/store/workspace/store.go:16-20`
- 二者都依赖统一的 `sqlc.Queries.WithTx`：
  - `internal/store/sqlc/db.go:51-58`
- `binding.Upsert` 是“写入失败后再查一次”的两步逻辑，不在 tx 里：
  - `internal/store/binding/store.go:30-58`
- V2 thread-binding 侧明确存在 rebind 事务面：
  - `go-agent-v2/internal/store/agent_thread_binding.go:429-479`

结论：V3 provider-binding 的单 repo 语义还行，但替代不了 V2 thread-binding 的事务兼容层。

## 全局结论

### 1. `WrapStoreError` 覆盖面：`✅ 19/19`

- 定义：`internal/platform/db/errors.go:47-61`
- LSP `text_search` 命中了全部 19 个 V3 repo 的 `store.go`
- 包括最弱项 `dbquery` 也直接包了：
  - `internal/store/dbquery/store.go:16-19`

结论：当前 repo 层不存在“只有少数 store 使用统一错误包装”的问题。

### 2. 显式事务使用：`⚠️ 只有 taskdag/workspace`

- `taskdag` 和 `workspace` 都把 tx 往 repo contract 暴露了：
  - `internal/store/taskdag/contract.go:9-39`
  - `internal/store/workspace/contract.go:9-19`
- 其它 repo 没有 tx API，默认就是单语句原子性
- 这对普通 CRUD 没问题，但对 V2 `AgentThreadBindingStore` 这类跨步骤兼容逻辑是不够的

### 3. sqlc 生成层漂移：`⚠️ 不是全量一比一照搬`

- 漂移最小：
  - `agentstatus/thread/interaction/tasktrace/taskack/taskdag/buslog/sharedfile/topologyapproval/workspace`
- 漂移中等：
  - `commandcard/prompt/systemlog/cwdlock/uipreference`
- 漂移严重：
  - `threadbinding(缺失)`、`ailog(挂到 systemlog)`、`dbquery(placeholder)`

最终判断：

- 如果目标是“V2 Store 层 1:1 对齐”，当前状态不能打 `✅`
- 如果目标是“V3 repo 层已形成稳定 sqlc + 统一错误包装底座”，这个底座本身是成立的
- 真正还没对齐的不是通用基建，而是少数几个语义面：`AgentThreadBindingStore`、`AILogStore`、`DBQueryStore`，以及 `prompt/commandcard/systemlog/cwdlock/uipreference` 的协议收缩
