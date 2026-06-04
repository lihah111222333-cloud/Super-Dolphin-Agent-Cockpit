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
3. Trace 证明在 `frontend callAPI/runtime shim -> /wails/ws -> backend dispatch` 之间存在 pre-dispatch wait：大量 6–10 秒级 `ui/sidebar/get` 会挤压其他 nav 页 RPC（`prompt-assets/list`、`ui/preferences/get`、`ui/dashboard/get`、`dashboard/dagRuns` 等），使它们在前端看到 20–30 秒等待甚至 30 秒 timeout，即使真正进入后端 dispatch 后只花几毫秒到几十毫秒。源码只能证明 dev runtime 通过 `frontend-app/public/wails/runtime.js` 的 `/wails/ws` 路径进入 `internal/platform/rpc/transport_ws.go` 的 WebSocket RPC dispatch；具体等待点未被 trace 直接定位。

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

所以高频事件不会只更新本地 patch，而是放大成大量 pending/in-flight 的 `ui/sidebar/get`；这些请求随后沿 dev runtime 的 `/wails/ws` 传输路径进入后端 dispatch，trace 只能定位到 dispatch 前存在等待，不能直接定位具体等待点。

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
- `ui/sidebar/get` 后端耗时分两种口径：
  - 全量 backend done（`method == "ui/sidebar/get" && kind == "backend.rpc.dispatch.done"`，n=1008）：min 2659ms，median 8242.5ms，p90≈10552ms，max 13119ms。
  - frontend-paired subset（可与 frontend trace 配对的 backend done 子集，n=377）：min 2659ms，median 7453ms，p90≈9003ms，max 11935ms。
- 复算说明：全量 backend done 只按 backend dispatch done 过滤；frontend-paired subset 只用于解释可配对前端样本，不作为 1008 条全量 backend 基线。
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
| `a3aac825` | `turn/start` | 前端 30s timeout | 30040ms | 18:39:24.549，271ms | 普通对话也受同一 pre-dispatch wait 现象影响 |
| `2f172169` | `ui/sidebar/get` | 前端 30s timeout | 30059ms | 18:39:58.794，9828ms | sidebar 本身又慢，继续加压 |

聚合统计也支持这个判断：

- `prompt-assets/list` 后端耗时 p50 17ms，但前端耗时 p50 22611ms。
- `ui/preferences/get` 后端耗时 p50 4ms，但前端耗时 p50 22469ms。
- `ui/dashboard/get` 后端耗时 p50 57ms，但前端耗时 p50 16600ms。

这些数据说明“nav 页红框”不是这些 nav handler 的主因；它们大多是在 `ui/sidebar/get` 风暴中出现 pre-dispatch wait，或被 dev runtime 30 秒短 timeout 击穿。这里的直接证据是 frontend/runtime 耗时远大于 backend dispatch 耗时；源码只能把传输路径追到 `frontend-app/public/wails/runtime.js` 的 `/wails/ws` JSON-RPC send 与 `internal/platform/rpc/transport_ws.go` 的 `WSHandler` / `Dispatch`，尚不能证明等待发生在某个具体队列或串行执行点。

## 为什么重试也可能失败

`withTimeout()` 与 dev runtime timeout 都只是让前端 promise reject：

- page-level `withTimeout()` 没有 AbortSignal，超时不会取消 Wails/RPC。
- `frontend-app/src/shared/api/wailsBridge.js` 的 `callAPI()` 只是 await `runtime.Call.ByID(...)`，没有传递 abort（行 602-624）。
- dev runtime shim 只有 `pendingCalls` map + `setTimeout`；timeout 后删除 pending 并 reject，但底层请求可能之后才到后端或才返回（`frontend-app/public/wails/runtime.js:20,297-318,322-343`）。后端接入点是 `internal/platform/rpc/transport_ws.go:25-45,67-78`。

所以点击“重试同步”会再加一个请求，不会清掉旧的 sidebar 请求或旧的 nav 请求。若 sidebar 风暴还在，重试仍可能继续落入 pre-dispatch wait 并超时。

## 历史自动化页专项文档（非证据背景索引）

已有文档 `docs/cc/workflow-sync-root-cause-20260604.md` 仅作为历史背景索引：本文不继承其中任何结论作为当前证据，也不把旧报告的专项归因纳入本轮 finding。

当前全局 nav RPC 主结论仍以本文 trace/source 证据链为准：即使切到提示词、技能、记忆、共享文件等页面，只要运行中的 thread/run 触发大量 UI 事件，运行中 UI 事件会放大出大量 `ui/sidebar/changed`，前端进一步触发全量 `ui/sidebar/get` 风暴，叠加 sidebar 后端读路径过重和桥接层 timeout，挤压其他 nav RPC 在 `frontend callAPI/runtime shim -> /wails/ws -> backend dispatch` 之间的等待窗口，导致这些 nav 页面的请求排队/超时/红框；具体等待点仍需额外 in-flight/concurrency telemetry 证明。自动化页自身问题若需要纳入，必须另用当前 trace/source 重新列证据。

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
4. **为 nav 页 timeout 增加更明确的 pre-dispatch wait / 后台刷新语义。**
   - 不建议简单吞掉 `CanceledError` 或 timeout 文案；应先减少请求风暴。
   - 若要增加 AbortSignal，需要贯通 `callAPI`、runtime shim/Wails、后端 ctx，而不是只在页面层 `Promise.race`。

### Implementation acceptance / 验收清单（F8，未实施）

本节只补跨层实现验收口径；本文仍是 docs-only root-cause 文档，未实施任何代码修复。F1/F3/F9 的调查性结论以各自章节为准，F8 仅定义实现验收口径。

实际修复进入合入前，至少应满足：

1. **Frontend sidebar single-flight + trailing refresh。**
   - 同一 `cwd` 的 burst `ui/sidebar/changed` 期间，最多允许 1 个 `ui/sidebar/get` in-flight；in-flight 期间的新事件只能标记 dirty / pending-after-current。
   - 当前 in-flight 完成后，如 dirty 被置位，必须再触发 1 次 trailing refresh，确保最终 sidebar snapshot 反映最后一次事件，不能做 reuse-only 后丢 fresh。
   - 用 `frontend-app` 侧 scoped Vitest 覆盖 burst coalesce、trailing refresh、旧响应不覆盖新状态。
2. **SQLC batch 查询与生成校验。**
   - `ListAgentThreadConfigsByIDs` 必须支持多个 thread ID，在 pgx 下不再出现 `IN ($1)` + `[]string` 作为单个 text 参数的编码失败。
   - 增加或保留 SQL/store 防回归测试，证明多 ID batch 可用。
   - 运行并通过 `make sqlc-verify`，同时运行受影响 Go package 测试。
3. **Uistate fallback / warn 风暴关闭。**
   - `ReadRuntimeConfigs failed` 不应再触发无界逐 thread `ReadRuntimeConfig` fallback 与 warn 风暴。
   - 若 sidebar 不需要 runtime config，应移除无用读取；若仍需要读取，错误路径必须可诊断且受控，不得静默吞错或用隐式兜底掩盖 fail-fast 问题。
   - 在 SQLC batch 修复路径下，复测日志中 `ReadRuntimeConfigs failed` 应为 0；如仍有 missing session / binding 等独立业务错误，必须聚合或显式报错，逐线程 `ReadRuntimeConfig failed` 不得形成风暴。
4. **Legacy compatibility tests 仍是硬边界。**
   - 事件面降噪只能收窄有证据的二次放大路径，不能删除 direct legacy refresh contract。
   - 保留并通过 `internal/platform/eventsurface/legacy_test.go` 与 `internal/ui/wails/bridge_test.go` 相关兼容测试。
5. **Manual trace/log 闭环复测。**
   - 在 turn/run 进行中切换 prompts / skills / dashboard / shared files 等 nav 页，页面不再出现 8s 红框，底层 runtime trace 不再出现 30s timeout。
   - 采集修复前后 trace/log，对比 `ui/sidebar/get` 数量、backend dispatch duration、pre-dispatch wait、uistate warn 数量，证明“事件风暴 -> sidebar get 数量 -> batch/fallback -> nav RPC timeout”链路被切断。

## 当前未做的事

- 未修改业务代码。
- 未启动桌面复现新的 turn/run；本次使用 2026-06-04 已有 trace/log 做源码对应分析。
- 未运行测试；本次是 docs-only 调查落盘。
