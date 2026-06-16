# Store 层容错与数据一致性审查

## 范围与方法

- 审查范围：`internal/platform/db`、`internal/store/*`、`internal/module/{workspace,thread,orchestration}`、`migrations/*`、`go-agent-v2/internal/store/*`
- 审查方式：只读；仅使用 LSP `text_search` / `workspace_symbol` / `references(compact)` / `call_hierarchy` / `read_file`

## 结论总览

| # | 维度 | 判定 | 结论 |
|---|---|---|---|
| 1 | 连接池容错 | 部分通过 | 代码只依赖 `pgxpool` 默认行为；没有仓内重连/退避/查询超时策略，`DBQueryTimeout` 也未接入 store 层。 |
| 2 | 事务使用 | 部分通过 | `workspace`、`taskdag` 有事务入口；`thread + binding` 的多步写仍未事务化。 |
| 3 | 统一错误包装 | 不通过 | `WrapStoreError` 只覆盖 `binding/thread/workspace` 3 个 repo，不是全量修复。 |
| 4 | 幂等性 | 部分通过 | `UpsertRun/UpsertFile` 结构上可重放，但本质仍是 last-write-wins；`TransitionRunStatus` 不是重放幂等。 |
| 5 | 并发写入 | 部分通过 | `TransitionRunStatus` 做了 SQL 级 CAS；大量其他更新仍无版本号/CAS。 |
| 6 | sqlc 生成层漂移 | 通过 | `UpdateAgentProviderBindingArchived` 的 interface + generated query + store 调用链已对齐。 |
| 7 | `dbquery` placeholder | 不通过 | 仍是空实现，返回空结果集。 |
| 8 | 级联删除 | 不通过 | schema 没有 FK / `ON DELETE CASCADE`；清理主要靠手工代码，且并不完整。 |
| 9 | 大数据量 | 部分通过 | `ListRuns/ListFiles` 自身有 `LIMIT`，无 limit 不会无限拉取；但 `queryMany` 是整批入内存，其他 repo 仍有无上限 list。 |
| 10 | V2 store 等价性 | 不通过 | V3 仍缺独立 `AgentThreadBindingStore`，V2 的多项能力面没有等价迁移。 |

## 1. 连接池容错

判定：部分通过。

证据：

- `internal/platform/db/module.go:19-25` 的 `NewPool` 只做了 `pgxpool.ParseConfig(cfg.DatabaseURL)`、`poolCfg.MaxConns = 4`、`pgxpool.NewWithConfig(...)`。
- `internal/platform/db/module.go:28-39` 生命周期里只有启动时 `pool.Ping(ctx)` 和停止时 `pool.Close()`。
- `internal/platform/config/timeouts.go:8-20` 定义了 `DBQueryTimeout = 10s`，但 LSP 搜索显示仓内没有把它接到 store / sqlc / db 模块。
- 仓内没有 `AcquireTimeout`、store 层 `context.WithTimeout(...)`、重试/退避逻辑的命中。

结论：

- 断连、重连、坏连接淘汰，代码侧完全交给 `pgxpool` 默认行为处理；本仓没有显式恢复策略。
- 查询超时依赖上层传入的 `ctx`；store 层本身没有统一超时保护。
- 发生网络抖动时，仓内能保证的只有“错误会上抛”；不能保证自动重试或统一降级。

风险：

- 小连接池上限 `MaxConns = 4` 会放大阻塞与慢查询影响。
- 只有 `binding/thread/workspace` 这 3 个 repo 会把 timeout 分类成 `ErrTimeout`；其余 repo 多数直接漏出原始 pgx/pgconn 错误。

## 2. 事务使用

判定：部分通过。

已使用事务的路径：

- `internal/store/workspace/store.go:16-20` 暴露 `WithTx`。
- `internal/module/workspace/service_helpers.go:166-179` 的 `persistRun` 把 `UpsertRun + UpsertFile*` 放进一个事务。
- `internal/store/taskdag/store.go:15-19` 暴露 `WithTx`。
- `internal/sidecar/orch/orchestration/dag.go:20-33` 的 `CreateDAG` 把 `upsertDAG + upsertDAGNodes + loadDAGDetail` 放进一个事务。
- `internal/store/sqlc/query_task_dag.go:13-19` 还有 `FOR UPDATE`/条件更新，说明 taskdag 对并发控制是有意识设计的。

缺事务的多步写路径：

- `internal/module/thread/lifecycle.go:238-270` 的 `persistThreadState` 先 `threadStore.Upsert(...)`，再 `bindingStore.Upsert(...)`；中间无事务，后一步失败会留下孤立 thread 记录。
- `internal/module/thread/service.go:102-119` 的 `Delete` 先删 binding，再删 thread；中间无事务，且 `forgetThreadAgent(id)` 在真正删 thread 前就执行。
- `internal/module/workspace/service_merge.go:17-55` / `internal/module/workspace/service_helpers.go:141-163` 合并阶段逐条 `UpsertFile`，失败后靠 `rollbackMergeState(...)` 做补偿，不是数据库原子提交。

结论：

- 当前只有 `workspace` 和 `taskdag` 两个 store 面向上层提供了事务能力。
- `thread + binding` 这一组最典型的跨表一致性路径仍靠顺序写，失败时会留下中间态。

## 3. 统一错误包装

判定：不通过。

证据：

- `internal/platform/db/errors.go:47-61` 定义 `WrapStoreError`。
- 对该符号做 `references(compact)`，只有 3 个调用点：
  - `internal/store/binding/store.go:104-106`
  - `internal/store/thread/store.go:145-147`
  - `internal/store/workspace/store.go:152-154`
- `internal/store/module.go:28-49` 注册了 19 个 store module。
- 未覆盖 repo 的代表性证据：
  - `internal/store/taskdag/store.go:21-24` / `30-34` / `38-44` 等大量路径直接 `return err`
  - `internal/store/agentstatus/store.go:27-29` / `36-38`
  - `internal/store/sharedfile/store.go:23-25` / `32-34` / `44-46`
  - `internal/store/dbquery/store.go:16-19`

结论：

- “统一错误包装”并未全仓落地，当前覆盖面是 `3 / 19`。
- `ErrNotFound` / `ErrConflict` / `ErrTimeout` 这一套分类能力，目前只在 `binding/thread/workspace` 可稳定使用。
- 如果 D5 的目标是“store 统一错误语义”，那现在只能算局部修复，不是完成态。

## 4. 幂等性

判定：部分通过。

证据与判断：

- `internal/store/sqlc/query_workspace.go:6-13`：
  - `UpsertWorkspaceRun` 基于 `ON CONFLICT (run_key) DO UPDATE`
  - `UpsertWorkspaceRunFile` 基于 `ON CONFLICT (run_key, relative_path) DO UPDATE`
- `internal/store/workspace/store.go:22-39`、`80-96` 只是薄封装，没有额外副作用。
- `internal/store/sqlc/query_agent_thread.go:12-16` 的 `UpsertAgentThread` 也是 `ON CONFLICT (thread_id) DO UPDATE`。
- `internal/store/binding/store.go:30-58` 的 `Upsert` 对 `(provider, provider_thread_id)` 唯一键冲突做了补救读取：如果查回来的 `agent_id` 相同，则当作成功。
- `migrations/0021_agent_provider_binding.sql:26-49` 用 trigger 禁止更新 `agent_id/provider/provider_thread_id`，所以“同 agent 改绑到新 thread/provider”不是可重放 upsert，而是受限操作。

结论：

- `UpsertRun`、`UpsertFile`、`UpsertAgentThread` 对“同 key、同 payload 重复调用”基本安全，但会刷新 `updated_at`，且没有版本控制，本质仍是 last-write-wins。
- `binding.Upsert` 对“同 provider-thread、同 agent”的重复写是幂等的；对“同 agent 改绑新 provider-thread”则会被 trigger 阻止，不属于可幂等重放。
- `TransitionRunStatus` 不是重放幂等：第一次成功后，再用旧 `FromStatus` 重放会失败。

## 5. 并发写入

判定：部分通过。

正向证据：

- `internal/store/sqlc/query_workspace.go:9-10`：
  - `UpdateWorkspaceRunStatus` 是普通覆盖更新。
  - `TransitionWorkspaceRunStatus` 带 `WHERE run_key = $4 AND status = $5`，是 SQL 级 CAS。
- `internal/module/workspace/service.go:289-312` 说明 store 层 CAS miss 会先表现为 `ErrNoRows/NotFound`，service 再二次 `GetRun(...)` 区分“真不存在”和“状态不匹配”。
- `internal/store/sqlc/query_task_dag.go:13-19` 的 `FOR UPDATE` 和条件 `UPDATE ... WHERE status IN (...)` 说明 taskdag 也有并发保护面。

负向证据：

- `internal/store/sqlc/query_workspace.go:6-13` 的 run/file upsert 都没有版本列。
- `internal/store/sqlc/query_agent_thread.go:12-19` 的 thread upsert / update 也没有 CAS。
- `internal/store/binding/store.go:30-58` 依赖 PK/UNIQUE/trigger 保约束，不是显式版本控制。

结论：

- `TransitionRunStatus` 是当前最明确的并发安全点，适合状态机流转。
- 但大量写路径仍是“最后一次写覆盖前一次写”，包括 run upsert、file upsert、thread upsert、普通 status update。
- store 层对 CAS 冲突没有单独的 `ErrConflict` 语义；workspace 是在 service 层做了二次解释。

## 6. sqlc 生成层漂移

判定：通过。

证据：

- `internal/store/sqlc/querier.go:26` 声明了 `UpdateAgentProviderBindingArchived(...) error`
- `internal/store/sqlc/query_agent_binding.go:10,36-38` 有对应 SQL 常量和 generated method
- `internal/store/binding/store.go:72-78` 直接调用该 generated method
- 对 `internal/store/sqlc/query_agent_binding.go:36` 做 `references(compact)`，只有 generated declaration 本身和 `binding.Store.SetArchived(...)` 调用点

结论：

- `UpdateAgentProviderBindingArchived` 的 sqlc 漂移问题已经修复，当前 interface / generated layer / store 调用链一致。

## 7. `dbquery` placeholder

判定：不通过。

证据：

- `internal/store/dbquery/contract.go:5-11` 整个接口只剩 `Placeholder(ctx)`。
- `internal/store/dbquery/store.go:15-25` 只是转调 `PlaceholderDBQuery(...)`。
- `internal/store/sqlc/query_db_query.go:6-16` 的 SQL 是：
  - `SELECT NULL::text AS placeholder WHERE FALSE;`

结论：

- `dbquery` 仍然是空壳，不具备 V2 `DBQueryStore` 的只读 SQL 查询能力。
- 这不是“能力受限”，而是“功能未实现”。

## 8. 级联删除

判定：不通过。

证据：

- `migrations/0006_workspace_runs.sql:29-42` 里 `workspace_run_files.run_key` 只是普通 `TEXT`，没有 FK 到 `workspace_runs.run_key`。
- `migrations/0012_agent_threads.sql:6-19` 的 `agent_threads` 也没有 FK。
- `migrations/0021_agent_provider_binding.sql:9-24` 的 `agent_provider_binding` 也没有 FK。
- 对 migrations 做 `text_search`：
  - `ON DELETE CASCADE` 无命中
  - `REFERENCES workspace_runs` 无命中
  - `REFERENCES agent_threads` 无命中
  - `REFERENCES agent_provider_binding` 无命中
- `internal/module/thread/service.go:102-119` 删除 thread 时，只是手动删 binding 再删 thread。
- `internal/store/workspace/contract.go:9-19` 根本没有 delete run/file API。
- `migrations/001_baseline.sql:62-70` 的 `agent_threads.workspace_run_key` 只是文本列；删除 workspace run 不会自动清理 thread。

结论：

- 数据库层没有级联删除保障。
- thread 删除目前只有“删 binding + 删 thread”这条手工路径，而且无事务。
- workspace run 连基本 delete API 都没有；一旦通过 SQL 或未来代码直接删除主记录，`workspace_run_files` 很容易变孤儿。
- 以 `agent_id/thread_id/workspace_run_key` 作为普通文本列引用的其他表，也不会被自动清理。

## 9. 大数据量

判定：部分通过。

`ListRuns/ListFiles` 本身：

- `internal/store/sqlc/query_workspace.go:8,13` 两条 SQL 都带 `LIMIT $3`。
- `internal/module/workspace/service.go:193-201` 对 `ListRuns` 做了默认 `limit = 200`。
- `internal/module/workspace/service.go:259-264` 的 `ListRunFiles` 固定 `limit = 200`。
- `internal/module/workspace/service.go:325-329` / `internal/module/workspace/service_merge.go:17-20` 合并场景固定 `limit = 5000`。

关键细节：

- 如果直接调用 store 且 `Limit = 0`，PostgreSQL 会返回 0 行，不是“无限制”。
- 因此 `ListRuns/ListFiles` 的“无 limit”不会 OOM，但可能 silently 返回空结果。

残余风险：

- `internal/store/sqlc/db.go:77-91` 的 `queryMany(...)` 会把结果整批 append 到内存切片。
- 其他 repo 仍有无上限 list，例如：
  - `internal/store/sqlc/query_agent_thread.go:8-11,48-62` 的 `ListAgentThreads/ListRunningAgentThreads/ListRecoverableAgentThreads`
  - `internal/store/sqlc/query_task_dag.go:11-12,55-60` 的 `ListTaskDagNodes/ListRunningTaskDagNodesByAssignee`

结论：

- 针对题目点名的 `ListRuns/ListFiles`，当前不存在“漏传 limit 就拉全表”的 OOM 问题。
- 但 store 基础设施是 eager materialization；其他未加 `LIMIT` 的 list 仍存在大表内存风险。

## 10. V2 store 等价性

判定：不通过。

V2 证据：

- `go-agent-v2/internal/store/doc.go:6-27` 明确把 `AgentThreadBindingStore` 和 `AgentProviderBindingStore` 作为独立 store 列出。
- `go-agent-v2/internal/store/agent_thread_binding.go` 至少包含这些公开能力：
  - `Bind`：`187-189`
  - `UpdateSessionUUID`：`192-208`
  - `BindWithProvider`：`212-245`
  - `SetArchived`：`251-255`
  - `Unbind`：`258-280`
  - `UpdateCwd`：`282-312`
  - `FindByAgentID`：`314-320`
  - `FindBindingByAgentID`：`322-343`
  - `ListAll`：`345-350`
  - `ListCwdMap` / `ListCwdMapByCwd`：`354-399`
  - `ListProviderMap` / `ListProviderMapByCwd`：`402-427`
  - `Rebind`：`468-479`

V3 现状：

- `internal/store/binding/contract.go:7-14` 只有 6 个方法：`GetByProviderThread / Upsert / DeleteByAgentID / UpdateSessionUUID / SetArchived / GetByAgentID`
- `internal/store/thread/contract.go:7-21` 提供 thread 元数据面，但不提供 thread-binding 兼容层能力
- 对 V3 `internal/store` 做 LSP 搜索：
  - `UpdateCwd(` 无命中
  - `Rebind(` 无命中
  - `Unbind(` 无命中
  - `FindBindingByAgentID` 无命中

结论：

- V3 不是把 `AgentThreadBindingStore` “换个名字迁过来”，而是拆成了较窄的 `binding + thread`，且缺失一整组 V2 兼容层方法。
- 缺失的不只是 repo 名称，还包括行为面：解绑、重绑、cwd 统一更新、provider/cwd map 视图、兼容式 binding 读取等。
- 因此“V2 store 等价性”这一项当前不能判通过。

## 优先级建议

P0：

- 把 `WrapStoreError` 扩到所有 store repo，至少统一 `not found / conflict / timeout` 语义。
- 为 `thread + binding` 的创建/删除/重绑路径补事务。
- 明确 `dbquery` 是要补全、删除，还是继续作为 placeholder；现在不应再按“已迁移”口径计入 parity。
- 明确 `AgentThreadBindingStore` 的迁移策略：补独立 repo，或正式声明 V2 能力收缩并逐个替换调用面。

P1：

- 给 schema 补 FK/级联策略，或至少补齐显式 delete API 与补偿路径。
- 为未限流的 list 接口加 `LIMIT`/分页，避免 `queryMany(...)` 在大表上整批入内存。
- 把 `DBQueryTimeout` 真正接进 store/sqlc 层，而不是只停留在配置常量。

## 最终判断

Store 层当前最强的一块是 `workspace` 状态机 CAS 和 `taskdag` 的事务/锁语义；最弱的几块是统一错误语义、跨 repo 原子性、schema 级引用完整性，以及 V2 `AgentThreadBindingStore` / `DBQueryStore` 的能力缺口。就“容错 + 数据一致性”口径看，当前还不能算收口完成。

## 互审

### 对 `docs/plans/迁移/cap-workspace-ops.md` 的批判

1. `ListRunFiles` 一节有事实错误。该报告把“`runKey` 非空”约束归到 service，但 service 实际只是透传：`internal/module/workspace/service.go:259-265` 直接把 `strings.TrimSpace(runKey)` 传给 store，没有校验。真正的非空约束在 RPC 层：`internal/module/workspace/rpc.go:113-123`。而 store/SQL 其实支持空 `runKey` 的跨 run 查询：`sql/queries/workspace_run.sql:78-84`。这说明报告把 `RPC surface` 和 `service/store` 能力混成了一层。
2. 报告开头把默认 workspace 路径写成 `sourceRoot/.workspace/<runKey>`，表述不够准确。真实逻辑是先取 `req.CWD` 作为 base，只有 `CWD == ""` 时才回退到 `sourceRoot`：`internal/module/workspace/service.go:143-153`。因此路径隔离分析不能只盯 `sourceRoot/.workspace/...`，还要覆盖“调用方通过 `CWD` 改写默认 base”的情况。
3. 报告对 B3 风险排序偏焦。它在摘要里强调 `dryRun` 旁路 `merging` CAS，但更直接的状态机旁路是公开 RPC `workspace/run/status/update`：`internal/module/workspace/rpc.go:13-23,62-73`。该路径走的是 `service.UpdateRunStatus(...)`：`internal/module/workspace/service.go:204-218`，底层 SQL 也是不带 `from_status` 条件的普通更新：`sql/queries/workspace_run.sql:30-42`。报告后文提到了这一点，但没有放进“最关键 6 点”，优先级判断偏低。

### 对 `docs/plans/迁移/cap-fx-lifecycle.md` 的批判

1. 报告把 `RunDesktop()` 里 `app.Start()` 成功后 `wailsApp == nil` / `lifecycle == nil` 的早退当成显式缺口，证据链不够硬。`RunDesktop()` 用的是 `fx.Populate(&wailsApp, &lifecycle)`：`internal/app/app.go:31-45`。而 Fx 本地源码里 `Populate` 本质是 `Invoke`：`/Users/mima0000/go/pkg/mod/go.uber.org/fx@v1.24.0/populate.go:64-115`。在当前 `uiwails.Module` 已提供 `NewWailsLifecycle` 与 `NewWailsApplication` 的前提下：`internal/ui/wails/module.go:17-28,72-98`，如果这些值无法解析，启动阶段就应该报错，而不是成功启动后留下 nil 指针。因此这里更像不可达保护分支，不该当作 live lifecycle 缺口。
2. 报告把大量未消费 store provider 列成 `R1 高风险发现`，严重性偏高。被点名的 store module 只是纯 `fx.Provide(NewStore)`，例如 `internal/store/agentstatus/module.go:5-7`、`internal/store/dbquery/module.go:5-7`、`internal/store/uipreference/module.go:5-7`。对 `internal/store` 做 LSP 搜索，没有 `fx.Invoke(...)` 或 `lc.Append(...)` 命中。这说明这些 unused provider 是图收敛/死代码问题，但它们并不参与 `OnStart/OnStop`，很难支撑“生命周期高风险”这个分级。
3. “V3 真实运行入口只有 desktop”这个表述过满。`internal/app.Run()` 确实没有 caller，LSP `references` 对 `internal/app/app.go:25` 为 0；`RunDesktop()` 则由 `cmd/agent-terminal/main.go:10-15` 调用。但仓内仍有真实的非 desktop binary 入口：`cmd/mcp-lsp/main.go:8-13`、`cmd/mcp-ida/main.go:8-13`、`cmd/mcp-orch/main.go:8-13`。更准确的说法应是“只有 `internal/app` 这条应用壳入口接到了 desktop，headless `internal/app.Run()` 尚无 caller”，而不是“整个 V3 真实入口只有 desktop”。
4. 同样因为 `internal/app.Run()` 没有 caller，报告把 headless 双 signal 入口放进 `R4 高风险发现` 也有失衡之处。`Run()` 无引用是当前事实；而 signal 双入口依赖的正是 `runApp()` + `BindRuntime()` 这条未接发布入口的 headless 路径：`internal/app/app.go:25-27,60-65`、`internal/app/runner.go:32-69`、`internal/platform/runner/group.go:49-64`。这更像 latent design debt，而不是当前主路径上的高风险。

### 对 `docs/plans/迁移/cap-wails-desktop.md` 的批判

1. 报告把 `ShouldQuit` / 双向 shutdown 判成“通过”，在产品层面偏乐观。当前 `frontendReady` 不是前端 JS 握手，而是绑定在 Wails `ApplicationStarted` 事件上：`internal/ui/wails/module.go:93-95`。与此同时，live 资产 `internal/ui/wails/frontend/index.html:1-53` 没有任何 `<script>`、没有 `CallAPI`、没有 `bridge-event` / `app-will-quit` 订阅。也就是说，当前闭合的是“backend 进程退出 wiring”，不是“前端可观察的 shutdown UX 闭环”。
2. 报告对前端资产的审查漏掉了一条会漂移的死代码路径。`internal/ui/wails/window.go:35-58` 里有 `bootstrapWindowHTML()`，内嵌了 `/wails/runtime.js` 和 `/wails/transport.js`，但对这个函数做 `references(compact)` 结果为 0。真正 live 的路径是 `CreateMainWindow()` 把窗口 URL 指向 `/`：`internal/ui/wails/window.go:15-32`，再由 `AssetHandler()` 提供 `frontend/index.html`：`internal/ui/wails/module.go:79-81`、`internal/ui/wails/assets.go:14-24`。报告只说“shipped frontend 是占位页”还不够，应把这条无人使用的第二套 bootstrap 策略也点出来。
3. 报告把 `GetGroup()` 作为 V2 parity 的显著缺口之一，但这个点当前信号很低。V3 的 `GetGroup()` 的确固定返回空串：`internal/ui/wails/binding.go:14,45-47`；但对该方法做 `references(compact)` 结果为 0，说明它在当前 V3 根本没有消费方。与之相比，`OpenNewWindow` 缺失、`SelectProjectDirs` 缺失、无真实前端、无事件消费方，都是更靠近 live product surface 的缺口。把 `GetGroup` 放进同一优先级，会稀释更真实的阻塞项。
