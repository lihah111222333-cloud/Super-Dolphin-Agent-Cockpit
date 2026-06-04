# 运行中导航页 RPC 失败根因分析（turn/start、run 期间全局 nav 红框）

- 日期：2026-06-04
- 范围：源码追踪 + 既有 trace/log 复核；未修改业务代码。
- 现象入口：普通对话 `turn/start` 进行中，或自动化/run 进行中，切到其他 nav 页面（技能、提示词、自动化、记忆中心、共享文件等）会出现红色“同步失败/需要重试”提示；点击重试仍可能失败。
- 主要证据文件：`~/.multi-agent/log/workflow-sync-root-cause-20260604/traces/trace-2026-06-04.jsonl` 与同目录 `agent-terminal-2026-06-04-1.log`。

## 结论

这不是单个 nav 页面的 handler 慢，也不是 prompt / skills / dashboard 数据源普遍损坏。当前证据指向两段链路叠加：

1. **运行中事件被扩展成大量 `ui/sidebar/changed`，前端对每个事件都立即发起一次全量 `ui/sidebar/get`。**
   - `ui/sidebar/changed` 在 `useClientStore.js` 中没有 debounce、single-flight 或 abort；只用 `sidebarRefreshSeq` 忽略旧结果，但旧请求仍然真实发出。
2. **每个 `ui/sidebar/get` 在后端当前又很重。**
   - `GetSidebar()` 的耗时主要在 `enrichFromDB()`。
   - `enrichFromDB()` 先尝试 `ReadRuntimeConfigs(ctx, threadIDs)` 批量读取 runtime config，但当前 SQLC 生成的 `ListAgentThreadConfigsByIDs` 是 `WHERE thread_id IN ($1)`，pgx 无法把 `[]string` 编码成 text 参数；批量读取失败后，uistate 退回对每个 thread 调 `ReadRuntimeConfig()`，产生大量 DB/session 查询与 warn 日志。
3. 大量 6–10 秒级 `ui/sidebar/get` 堆积在同一个 dev Wails JSON-RPC/WebSocket 通道上；其他 nav 页 RPC（`prompt-assets/list`、`ui/preferences/get`、`ui/dashboard/get`、`dashboard/dagRuns` 等）在前端看到 20–30 秒等待甚至 30 秒 timeout，即使它们真正进入后端后只花几毫秒到几十毫秒。

因此，用户看到的“其他 navibar 功能一直红框、重试仍失败”，是 **sidebar 刷新风暴 + sidebar 后端读路径过重 + 前端/桥接层非中断式 timeout** 的组合结果。

## 用户可见红框来自哪里

### 提示词页

`frontend-app/src/features/prompts/PromptPageView.jsx`：

- `PROMPTS_REQUEST_TIMEOUT_MS = 8000`（行 21）。
- `withTimeout()` 是 `Promise.race([promise, timeout])`（行 446-454），不会取消底层 Wails RPC。
- `fetchPromptAssetsSurface()` 对 `prompt-assets/list` 套 8 秒超时（行 464-470）。
- `fetchActivePromptId()` 对 `ui/preferences/get` 套 8 秒超时（行 488-493）。
- 页面用 `PromptRetryNotice` 显示红色 retry（行 805-823）。

### 共享错误文案

`frontend-app/src/pages/shared/pageShared.js`：

- 共享 `withTimeout()` 同样只是 `Promise.race`（行 15-23）。
- `dashboardQueryErrorState()` 在已有缓存时拼出：`同步失败，显示的是上次成功的数据：...`（行 107-112）。

`frontend-app/src/pages/shared/pageComponents.jsx` 的 `RetryableSyncError` 负责通用红框 + “重试同步”按钮（行 12-18）。技能、共享文件、自动化等页都会复用这个模式或同类 timeout。

## 源码链路

### 1. 后端事件如何变成 `ui/sidebar/changed`

Wails 事件桥：`internal/ui/wails/bridge.go`

- `EventBridge.Start()` 订阅 `eventsurface.Bind(...)`（行 35-51）。
- `publish()` 对每个事件再次调用 `eventsurface.ExpandNotifications(method, payload)`，再发到前端 `bridge-event`（行 69-81）。

事件面：`internal/platform/eventsurface/bind.go` + `legacy.go`

- `bindCore()` 把 `turn/started`、`turn/completed`、`turn/interrupted`、`turn/resumed`、output delta 等发布到 UI 面（`bind.go:132-165`）。
- `bindTool()` 把 tool begin/end/approval 等发布到 UI 面（`bind.go:185-199`）。
- `bindUI()` 把 `UIThreadPatch` 和 `UIProjectionUpdated` 发布到 UI 面（`bind.go:202-228`）。
- `projectionUpdatedMethod()`：`Projection == "sidebar"` 映射为 `ui/sidebar/changed`，其他 projection 映射为 `ui/thread/changed`（`bind.go:451-457`）。
- `ExpandNotifications()` 会在原事件之外追加 legacy refresh（`legacy.go:20-43`）。只要 payload 里有 `threadId` / `agentId`，默认会追加 `ui/thread/changed` 和 `ui/sidebar/changed`（`legacy.go:46-64`）。
- 抑制列表只包含 `ui/thread/patch`、output delta、token usage 等（`legacy.go:71-83`），不包含 `ui/thread/changed` 本身。

这意味着：

- 许多 turn/tool 生命周期事件天然会附带一次 `ui/sidebar/changed`。
- timeline projection 先被映射成 `ui/thread/changed`；随后 Wails bridge 的二次 `ExpandNotifications()` 又可能因为 payload 带 `threadId` 追加 `ui/sidebar/changed`。这会把“线程/timeline 局部变化”扩大成“刷新整个 sidebar”。
- `workspace/run/*` 事件也被 legacy 规则显式视为 sidebar refresh（`legacy.go:50-68`）。

### 2. 前端收到 `ui/sidebar/changed` 后必拉全量 sidebar

`frontend-app/src/entities/client/model/useClientStore.js`：

- `handleBridgeEvent()` 中，`eventName === 'ui/sidebar/changed'` 时直接调用 `refreshActiveChatSidebarInBackground()`（行 3489-3502）。
- `refreshActiveChatSidebarInBackground()` 取当前 cwd 后调用 `refreshSidebarSnapshotForCwdInBackground(cwd, { preserveActiveThreadId: true })`（行 3119-3126）。
- `refreshSidebarSnapshotForCwdInBackground()` 每次都会执行 `getSidebarState({ cwd })`（行 3075-3089）。
- 这里只有 `seq !== runtime.sidebarRefreshSeq` 的结果丢弃逻辑（行 3091、3104），没有取消旧请求，也没有同 cwd single-flight/coalesce。

所以高频事件不会只更新本地 patch，而是放大成大量并发/排队的 `ui/sidebar/get`。

### 3. `ui/sidebar/get` 后端读路径本身很重

`internal/module/uistate/rpc.go`：

- `ui/sidebar/get` 直接进入 `svc.GetSidebar(withPreferenceScope(ctx, p.Cwd))`（行 51-53）。

`internal/module/uistate/service.go`：

- `GetSidebar()` 分三段：`GetPreferences()`、`sidebarSnapshot()`、`enrichFromDB()`，并记录 `ui.sidebar.get.duration`（行 213-233）。
- 本次日志显示慢点几乎都在 `enrich_db_ms`，不是 preference 或 snapshot。

`internal/module/uistate/module.go`：

- `enrichFromDB()` 调 `loadBatchConfigs(ctx, threads)`（行 126-132）。
- `loadBatchConfigs()` 如果 `ReadRuntimeConfigs()` 返回 error，只 warn，然后返回 nil（行 102-123）。
- batch 返回 nil 后，`enrichFromDB()` 会在每个 thread 上 fallback 到 `s.runtimeConfig.ReadRuntimeConfig(ctx, threadID)`（行 144-155）。

`internal/module/thread/history.go` + store/query：

- `ReadRuntimeConfigs()` 的 batch 入口会调用 `s.threadStore.ListConfigsByIDs(ctx, threadIDs)`（`history.go:177-197`, `216-224`）。
- store 调用 SQLC 方法 `ListAgentThreadConfigsByIDs`（`internal/store/thread/store.go:69-74`）。
- SQL 源是 `WHERE thread_id IN (sqlc.slice('thread_ids'))`（`sql/queries/agent_thread.sql:180-183`）。
- 当前生成代码实际是 `WHERE thread_id IN ($1)` 并把 `arg.ThreadIds []string` 作为单个参数传入（`internal/store/sqlc/agent_thread.sql.go:268-286`）。
- 日志中的错误：`failed to encode args[0]: unable to encode []string{...} into text format for text (OID 25): cannot find encode plan`。这证明 batch 查询没有生效。

结果是：每次 `ui/sidebar/get` 都尝试一次失败的 batch，然后对很多历史 thread 逐个 fallback 查询 runtime config；大量失败还会刷出 `ReadRuntimeConfig failed` warn。

## Trace / log 证据

### 1. `ui/sidebar/get` 数量和耗时异常

从 `trace-2026-06-04.jsonl` 统计：

- 全 trace 3903 行，时间范围约 2026-06-04 18:36:20–18:45:03（Asia/Shanghai）。
- `ui/sidebar/get` 是绝对主量：总出现 2413 行。
- 后端 dispatch start：`ui/sidebar/get` 1028 次；后端 done：1008 次，全部标记 slow。
- 前端 `ui/sidebar/get`：256 次 done，121 次 failed；失败多为 `runtime shim: rpc call timeout (30s) for ui/sidebar/get`。
- `ui/sidebar/get` 后端耗时：min 2659ms，p50 7453ms，p90 8985ms，max 11935ms。
- 前端看到的 `ui/sidebar/get` 耗时：min 2676ms，p50 17088ms，p90 30040ms，max 30179ms。

每分钟后端 `ui/sidebar/get` start 数：

| 时间 | sidebar start | 同分钟 frontend failed |
|---|---:|---:|
| 18:36 | 4 | 0 |
| 18:37 | 50 | 0 |
| 18:38 | 165 | 103 |
| 18:39 | 145 | 11 |
| 18:40 | 131 | 18 |
| 18:41 | 132 | 0 |
| 18:42 | 149 | 0 |
| 18:43 | 119 | 0 |
| 18:44 | 125 | 0 |
| 18:45 | 8 | 0 |

### 2. `ui/sidebar/get` 慢点在 `enrich_db_ms`

从 `agent-terminal-2026-06-04-1.log` 统计 `ui.sidebar.get.duration`：

- 记录 1018 条。
- `total_ms`：min 2656，p50 8221，p90 10536，max 13115。
- `get_prefs_ms`：p50 9，p90 33，max 81。
- `snapshot_ms`：p50 0，p90 0，max 32。
- `enrich_db_ms`：min 2655，p50 8203，p90 10528，max 13065。
- 同一日志中 `ReadRuntimeConfigs failed` 约 1030 次，`ReadRuntimeConfig failed` 约 199515 次。

典型日志形态：

```text
msg=ui.sidebar.get.duration total_ms=8280 get_prefs_ms=25 snapshot_ms=0 enrich_db_ms=8254
msg="uistate: ReadRuntimeConfigs failed" err="list_configs_by_ids thread: failed to encode args[0]: unable to encode []string{...} into text format for text (OID 25): cannot find encode plan"
msg="uistate: ReadRuntimeConfig failed" threadID=... err="session not found ..." / "get_by_agent_id binding: no rows in result set"
```

### 3. 其他 nav RPC 后端很快，但前端等待/超时

同一 trace 里可以看到：一些非 sidebar 请求前端耗时 23–30 秒，但后端真正 dispatch 只花几毫秒到几十毫秒。

| trace_id 前缀 | 方法 | 前端结果 | 前端耗时 | 后端 dispatch 时间/耗时 | 结论 |
|---|---|---:|---:|---:|---|
| `1190b29b` | `prompt-assets/list` | ok | 23186ms | 18:39:24.873，17ms | 提示词后端不慢；主要在进入后端前等待 |
| `819320ba` | `ui/preferences/get` | ok | 23122ms | 18:39:24.867，4ms | preference handler 不慢 |
| `8b61d034` | `ui/dashboard/get` | 前端 30s timeout | 30016ms | 18:39:24.796，65ms | dashboard handler 很快，但前端已超时 |
| `b66d9221` | `prompt-assets/list` | 前端 30s timeout | 30011ms | 18:39:24.666，44ms | 请求到后端时前端 pending 已被 timeout 删除 |
| `a3aac825` | `turn/start` | 前端 30s timeout | 30040ms | 18:39:24.549，271ms | 普通对话也会被同一通道/队列拖住 |
| `2f172169` | `ui/sidebar/get` | 前端 30s timeout | 30059ms | 18:39:58.794，9828ms | sidebar 本身又慢，继续加压 |

聚合统计也支持这个判断：

- `prompt-assets/list` 后端耗时 p50 17ms，但前端耗时 p50 22611ms。
- `ui/preferences/get` 后端耗时 p50 4ms，但前端耗时 p50 22469ms。
- `ui/dashboard/get` 后端耗时 p50 57ms，但前端耗时 p50 16600ms。

这些数据说明“nav 页红框”不是这些 nav handler 的主因；它们大多是在 `ui/sidebar/get` 风暴中排队或被 dev runtime 30 秒短 timeout 击穿。

## 为什么重试也可能失败

`withTimeout()` 与 dev runtime timeout 都只是让前端 promise reject：

- page-level `withTimeout()` 没有 AbortSignal，超时不会取消 Wails/RPC。
- `frontend-app/src/shared/api/wailsBridge.js` 的 `callAPI()` 只是 await `runtime.Call.ByID(...)`，没有传递 abort（行 602-624）。
- dev runtime shim 只有 `pendingCalls` map + `setTimeout`；timeout 后删除 pending 并 reject，但底层请求可能之后才到后端或才返回（`cmd/agent-terminal/frontend/wails/runtime.js:248-305`）。

所以点击“重试同步”会再加一个请求，不会清掉旧的 sidebar 请求或旧的 nav 请求。若 sidebar 风暴还在，重试仍排队/超时。

## 与自动化页专项文档的关系

已有文档 `docs/cc/workflow-sync-root-cause-20260604.md` 聚焦自动化页自身的刷新重入、DAG detail/run 扇出与 cached error 文案。那份结论仍解释“自动化页为什么会显示上次缓存数据”。

本文件补的是更全局的一层：即使切到提示词、技能、记忆、共享文件等页面，只要运行中的 thread/run 触发了大量 UI 事件，前端 sidebar refresh 会挤占同一 RPC 通道；于是这些 nav 页面自己的请求也会表现为超时/红框。

## 后续最小修复方向（未实施）

建议按“先止血、再降成本、最后清理语义”的顺序：

1. **前端先给 sidebar refresh 做同 cwd single-flight/coalesce。**
   - `ui/sidebar/changed` 到来时，如果同 cwd 的 `getSidebarState` 已在 flight，复用或标记 pending-after-current，不要每个事件都新开 RPC。
   - 至少加 debounce；更稳的是 single-flight + trailing refresh。
   - `sidebarRefreshSeq` 只能丢弃旧结果，不能减少请求数，需要补真正合流。
2. **后端修 `ListAgentThreadConfigsByIDs` batch 查询。**
   - 当前 `IN ($1)` + `[]string` 参数在 pgx 下不可用；需改成 PostgreSQL 可编码的数组查询（例如 `ANY($1::text[])`）或符合本项目 sqlc 配置的 slice 展开方式。
   - 修完应避免 `ReadRuntimeConfigs failed` 后逐线程 fallback 的 20 万级 warn。
3. **减少 legacy refresh 放大。**
   - 评估 `ui/thread/changed` / timeline projection 是否还应被 `ExpandNotifications()` 再扩成 `ui/sidebar/changed`。
   - 已有 `ui/thread/patch` 增量事件时，sidebar 全量刷新应只在 thread list/order/name/status 真正需要时触发。
4. **为 nav 页 timeout 增加更明确的排队/后台刷新语义。**
   - 不建议简单吞掉 `CanceledError` 或 timeout 文案；应先减少请求风暴。
   - 若要增加 AbortSignal，需要贯通 `callAPI`、runtime shim/Wails、后端 ctx，而不是只在页面层 `Promise.race`。

## 当前未做的事

- 未修改业务代码。
- 未启动桌面复现新的 turn/run；本次使用 2026-06-04 已有 trace/log 做源码对应分析。
- 未运行测试；本次是 docs-only 调查落盘。
