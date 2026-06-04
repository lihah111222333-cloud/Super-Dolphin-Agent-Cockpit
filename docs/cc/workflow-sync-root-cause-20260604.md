# 自动化页同步失败根因分析（frontend-app 新 UI）

- 日期：2026-06-04
- 分支：`integration/workflow-sync-root-cause-20260604`
- 范围：只定位根因并记录证据；未修改业务代码。
- 本轮修正：已对审查高票/边界项逐项追源码，区分“真实可达”“部分可达”“已有上层防护”“证据不足”。

## 用户可见现象

在 `frontend-app` 新 UI 中，一跑自动化任务，自动化页顶部出现：

- `同步失败，显示的是上次成功的数据：CanceledError` / `CancelledError`
- 或 `同步失败，显示的是上次成功的数据：自动化加载超时，请检查任务数据或后端状态。`

页面继续显示上次成功的数据；点击“重试同步”仍可能继续失败。

## 复核后结论

根因不是 DAG 数据本身损坏。当前源码能支撑的结论是：**自动化页存在多入口刷新重入/竞争风险，叠加 dashboard DAG 读路径扇出和前端 8 秒非中断式 timeout，导致刷新请求可能被取消、超时或排队；UI 又按缓存错误策略展示上次成功数据。**

需要修正上一版文档的一个过度归因：当前桌面 app 默认装配 `store.Module` 与 dashboard `DBQueries`，因此 `ListDAGs` / `ListDAGRuns` 默认优先走 DB snapshot；`mcp-orch/toolbridge` 是无 snapshot 时的 fallback，不应写成默认主路径。默认路径仍然存在可达的扇出问题：DAG 列表先查 DAG，再对每个 DAG 查一次 latest run；选中 DAG 后还会额外刷新 detail/run。

复核后的直接链路：

1. DAG / cron 事件递增 `workflowRevision`，激活 workflow 页刷新。
2. workflow 页还会被初始加载、focus/visibility、start/stop/schedule/delete action、手动“重试同步”触发刷新；`activePage` effect 也有刷新入口，但当前页面按 active page 条件挂载，本轮不把它作为主要可达触发。
3. 刷新入口没有共享 single-flight/coalesce；列表刷新使用 `invalidateQueries`，detail/run 也各自 invalidate 或 fetch。
4. `ui/dashboard/get?page=dags` 在默认 DB snapshot 路径下仍是 `ListDAGs + 每个 DAG 一次 ListDAGRuns(limit=1)`；无 snapshot 时才 fallback 到 mcp-orch toolbridge。
5. `withTimeout()` 只是 `Promise.race`，超时不会 abort Wails RPC；底层请求可能继续占用后端资源，手动重试会叠加新请求。
6. React Query 有缓存时，页面按设计显示“上次成功的数据”；另有 `workflowSyncFailure` 独立 state，某些成功刷新路径不会清空它，可能形成陈旧告警。

`CanceledError` / `CancelledError` 的来源需要谨慎表述：项目代码能证明的是前端会把任意 `error.message` 直接拼进同步失败文案；它与 TanStack Query 取消行为相符，但仓库源码不能证明截图里的两种名字一定都来自 TanStack，也不能排除 Wails/RPC/后端取消错误。

## 源码证据链

### 1. 自动化事件会驱动 workflow 刷新

`frontend-app/src/entities/client/model/useClientStore.js` 把 DAG / cron 事件映射到 `workflowRevision`：

- `task/node/statuschanged`
- `cron/job/runstatechanged`
- `task/dag/changed`
- `dags/changed`

证据：`useClientStore.js:96-100`。事件处理会将 event name 转小写后匹配，并递增对应 revision：`useClientStore.js:3489-3498`；`task/node/statuschanged` 还要求 payload 有 `dag_key`、`node_key`、`new_status` 和 run 身份：`useClientStore.js:175-193`。

`frontend-app/src/App.jsx:269-270` 将 `store.workflowRevision` 作为 `WorkflowPage` 的 `refreshKey`。

### 2. WorkflowPage 刷新入口不止自动事件和重试按钮

列表基础查询：

- `fetchDagsDashboard()` 调 `getDashboardPage({ cwd, page: 'dags' })`，并套 8 秒超时文案：`自动化加载超时，请检查任务数据或后端状态。`（`frontend-app/src/pages/workflows/WorkflowPage.jsx:52-58`）
- `useWorkflowListQuery.refreshDags()` 使用 `queryClient.invalidateQueries({ queryKey }, { throwOnError: true })`，失败后设置 `workflowSyncFailure`（`WorkflowPage.jsx:639-662`）

可达触发面：

| 触发面 | 源码证据 | 上层防护/限制 | 复核结论 |
|---|---|---|---|
| 初始加载 | `useQuery` enabled by `workflowCwd`（`WorkflowPage.jsx:642-646`） | 无 cwd 不加载 | 真实可达 |
| 自动化事件 | `refreshKey` effect 调 `refreshWorkflowSurface()`（`WorkflowPage.jsx:796-816`） | 同一 revision 只处理一次（`handledWorkflowRefreshRef`），但不同事件会递增 | 真实可达 |
| activePage effect | activePage effect 调 `list.refreshDags()`（`WorkflowPage.jsx:623-627`） | `ActivePageContent` 仅 activePage 为 `workflows` 时挂载 WorkflowPage；通常重新进入页面是重新挂载，effect 首次运行会被 `mountedRef` 跳过 | 边界入口，不作为主因 |
| focus / visibility | `useDashboardFocusInvalidation()`（`WorkflowPage.jsx:620`），内部监听 focus/visibilitychange 并 invalidate（`frontend-app/src/pages/shared/pageShared.js:114-123`） | 文档隐藏时跳过；WorkflowPage 未挂载时不触发 | 真实可达，但有可见性/挂载限制 |
| start action | 成功启动后 `list.refreshDags()` + `refresh.refreshDetail()`（`WorkflowPage.jsx:898-910`） | 受启动按钮业务条件限制 | 真实可达 |
| stop action | finally 中刷新 list/detail（`WorkflowPage.jsx:920-935`） | 需有 active run | 真实可达 |
| schedule action | 保存后并发刷新 list/detail（`WorkflowPage.jsx:972-988`） | 需 detail/version/root assignee 条件通过 | 真实可达 |
| delete action | 删除成功后 `list.refreshDags()`（`WorkflowPage.jsx:940-965`） | 有 active run 时阻断删除（`WorkflowPage.jsx:945-948`） | 真实可达，但有 active-run 防护 |
| 手动重试 | 顶部告警按钮调用 `refresh.refreshWorkflowSurface`（`WorkflowPage.jsx:1113-1129`） | 无 single-flight 防护 | 真实可达 |

因此修复如果只包一处 `refreshWorkflowSurface()`，会漏掉 focus invalidation 和 action 内直接调用的 `list.refreshDags()` / `refresh.refreshDetail()`。

### 3. 重试路径实际会叠加 detail/run 请求

`refreshWorkflowSurface()` 先 `refreshDags()`，再在有选中 DAG 时刷新 detail/run（`WorkflowPage.jsx:807-811`）。

选中 DAG detail 查询不是单请求：`fetchWorkflowDagDetail()` 并发调用：

- `dashboard/dagDetail`
- `dashboard/dagRuns` 最近运行列表，`limit=30`
- `dashboard/dagRuns` running 运行，`limit=1`

证据：`WorkflowPage.jsx:707-713`。

run detail 还会通过 `dashboard/dagRun` 查询当前 run，自动 query 证据为 `WorkflowPage.jsx:767-770`，手动/切换 run 会 `fetchQuery`（`WorkflowPage.jsx:772-777`）。后端 RPC 注册证据：`internal/module/dashboard/rpc.go:333-352`。

上层防护：只有 `selectedDagKey` / `effectiveSelectedRunKey` 为空时不触发（`WorkflowPage.jsx:736-740`, `767-770`）。当列表已有缓存并选中了 DAG 时，该风险真实可达。

### 4. UI “显示上次成功的数据”来自缓存错误策略和独立同步失败 state

React Query 缓存错误策略：

- `queryHasSnapshot(query)` 只要 `query.data !== undefined` 就认为有缓存（`frontend-app/src/pages/shared/pageShared.js:102-103`）
- 有错误且有缓存时，`dashboardQueryErrorState()` 返回 `同步失败，显示的是上次成功的数据：${message}`（`pageShared.js:106-110`）

WorkflowPage 还维护独立 state：

- `workflowSyncFailure` 定义于 `WorkflowPage.jsx:641`
- `refreshDags()` 成功才 `setWorkflowSyncFailure('')`，catch 时设置同步失败文案（`WorkflowPage.jsx:651-659`）
- 最终 `syncError` 优先使用 `syncFailure`，再用 list/detail cached error（`WorkflowPage.jsx:877-884`）

可达风险：如果某次 `refreshDags()` 失败设置了 `workflowSyncFailure`，后续 focus/visibility 直接 `invalidateQueries` 并成功，因为它没有经过 `refreshDags()` 的成功分支，`workflowSyncFailure` 可能不被清空，顶部仍显示旧同步失败。上层防护是手动重试或 action 走 `refreshDags()` 且成功时会清空；focus/visibility 成功路径没有同等防护。

### 5. 默认后端路径是 DB snapshot，但仍有 N+1 latest-run 查询

后端 RPC：

- `ui/dashboard/get` 进入 `svc.GetDashboardPage(ctx, p.Page)`（`internal/module/dashboard/rpc.go:211-214`）
- `page=dags` 调 `populateDashboardDAGs()`（`internal/module/dashboard/ui_page.go:88-97`）
- `populateDashboardDAGs()` 先 `ListDAGs(limit=100)`，再 `buildDashboardDAGs()`（`ui_page.go:126-142`）
- `buildDashboardDAGs()` 对每个 DAG 调一次 `ListDAGRuns(..., limit=1)`，并发上限为 `dashboardDAGLatestRunLookupLimit`（`ui_page.go:145-165`）

默认 DB snapshot 装配证据：

- app 装配包含 `store.Module` 和 `dashboard.Module`（`internal/app/modules.go:47-62`）
- `store.Module` 包含 `dbquery.Module`（`internal/store/module.go:35-47`）
- `dbquery.Module` 提供 `dbquerystore.Store`（`internal/store/dbquery/module.go:10-15`）
- dashboard service 接收 `DBQueries` 并传入 service（`internal/module/dashboard/module.go:18-29`, `48-63`）
- `hasDAGSnapshotQueries()` 只检查 `s.dbQueries != nil`（`internal/module/dashboard/dag_snapshot.go:65-67`）
- `ListDAGs()` / `ListDAGRuns()` 在 snapshot 可用时优先走 SQL（`internal/module/dashboard/detail.go:49-60`, `82-100`）

因此上一版“默认通过 mcp-orch/toolbridge”的表述不成立。真实可达的是 DB snapshot 路径上的 latest-run N+1：`dashboardListDAGsSnapshotQuery` 一次列 DAG（`dag_snapshot.go:16-27`），每个 DAG 再走 `dashboardListRunsSnapshotQuery`（`dag_snapshot.go:48-55`, `100-105`）。

无 snapshot fallback 仍存在，但不是默认主路径：`effectiveDAGRuntime()` 会返回 `DAGRuntime` / `OrchestrationService`（`internal/module/dashboard/detail.go:39-47`），app 中 `DAGRuntime` 由 `newMCPOrchDAGRuntime` 提供（`internal/app/modules.go:90-93`），其 `ListDAGs` / `ListRuns` 分别调用 `task_list_dags` / `task_list_runs`（`internal/app/orchestration_dag_runtime_adapter.go:37-45`, `97-107`）。

### 6. 前端 timeout 不会取消底层 Wails RPC

`withTimeout()` 只是 `Promise.race([promise, timeout])`，超时后 reject 并清 timer（`frontend-app/src/pages/shared/pageShared.js:14-21`）。

`callAPI()` / `invokeRuntimeByID()` 只 `await runtime.Call.ByID(...)`，没有接收或传递 `AbortSignal`（`frontend-app/src/shared/api/wailsBridge.js:402-425`, `598-620`）。

所以上层没有真正取消底层 RPC 的防护。“自动化加载超时”后，底层 `ui/dashboard/get` 仍可能继续执行；重试会叠加新的请求，而不是取消旧请求后重新来一次。

### 7. React Query 取消语义只能作为锁定版本推断，不能引用 node_modules 行号当生产证据

仓库稳定证据：

- `frontend-app/package.json:15-17` 依赖 `@tanstack/react-query` `^5.100.14`
- `frontend-app/package-lock.json:1866` 锁定 `@tanstack/query-core` `5.100.14`
- 项目代码调用 `queryClient.invalidateQueries({ queryKey: key }, { throwOnError: true })`，没有显式设置 `cancelRefetch`（`WorkflowPage.jsx:651-660`）
- `.gitignore:22-23` 忽略 `node_modules`

因此文档不再把本地 `node_modules` 内部源码行号作为证据。若要把“TanStack cancellation 是截图错误的唯一来源”升级为生产级结论，需要在项目内补最小复现测试或在代码中按类型识别 cancellation；当前只能说“与该版本上游取消行为相符”。

## 为什么重试仍可能失败

“重试同步”按钮走的是 `refresh.refreshWorkflowSurface()`（`WorkflowPage.jsx:1117-1129`），也就是同一套 list + selected detail/run 刷新路径。它没有 single-flight，也没有真正 abort 之前超时的 Wails RPC。

当自动化运行连续发事件、页面 focus/visibility 又触发 invalidate，或 action 成功后同时刷新 list/detail 时，手动重试会与后台刷新竞争。若列表或 detail/run 请求仍在 pending，新的 invalidate/fetch 可能取消、复用、排队或超时。UI 有缓存时会继续显示上次成功数据，并展示同步失败文案。

## 为什么现有测试没覆盖这个场景

已有测试覆盖“后台同步失败时保留缓存并允许重试成功”：`frontend-app/src/App.test.jsx:6443-6499`。但该测试的失败是一个立即 reject 的 `workflow backend offline`，重试时后端立即 resolve。

它没有覆盖：

- `ui/dashboard/get?page=dags` 长时间 pending；
- 连续 `workflowRevision` / focus / action refresh 重入；
- active refetch 期间再次 `invalidateQueries`；
- `withTimeout` 后底层 Wails RPC 仍未取消；
- 列表刷新失败后 detail/run 仍继续刷新；
- `workflowSyncFailure` 被 focus/visibility 成功查询绕过清理，形成陈旧告警。

## 建议最小修复顺序（未实施）

建议先做可验证的前端止血，再决定是否改后端读模型。

1. **先补前端回归测试。**
   - pending `getDashboardPage(page='dags')` + 连续 DAG 事件 + 手动重试：断言不会把可识别的查询取消错误展示成 `danger-text workflow-sync-alert`。
   - `refreshDags()` 失败后，再让 focus/visibility 触发的 dashboard query 成功：断言 `workflowSyncFailure` 不会留下陈旧告警。
   - 覆盖 selected DAG 时 list + detail + run 刷新重入，避免只测列表。
2. **做最小前端止血。**
   - 在共享 dashboard refresh primitive 或 query-key 层做 single-flight/coalesce，覆盖 `dags`、`dag-detail`、`dag-run`，不要只包 `refreshWorkflowSurface()`。
   - 对后台刷新和手动重试区分语义；如要改变 `cancelRefetch`，必须明确只影响目标 query key。
   - 取消错误只能按可信类型/来源识别后静默处理，不能粗暴按 `CanceledError` 文案过滤，避免吞掉真实后端取消/中断错误。
   - 任意 `dags` query 成功后应清空 `workflowSyncFailure`，或统一 `syncError` 来源，避免双状态竞争。
3. **再评估后端优化。**
   - 如果前端止血后 dashboard 仍慢，再把 latest-run N+1 改成 batch/snapshot 聚合查询，并补 dashboard DAG console 或批量查询测试。
   - mcp-orch/toolbridge fallback 只作为无 snapshot 配置下的兼容路径单独验证，不作为默认根因。

## 当前未做的事

- 未修改业务代码。
- 未运行完整前端/Go 测试；本次只做源码复核和文档落盘。
- 未消费真实自动化任务额度或启动桌面自动化流程。
