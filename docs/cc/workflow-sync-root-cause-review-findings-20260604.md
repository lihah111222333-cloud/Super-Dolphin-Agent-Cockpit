# 自动化页同步根因文档审查 Findings

- 日期：2026-06-04
- 审查对象：`docs/cc/workflow-sync-root-cause-20260604.md`
- 审查方式：在同名隔离 worktree `.worktrees/integration/workflow-sync-root-cause-20260604` 启动 5 个 orch 审查 agent，分别从生产就绪、性能、风险、准确性、维护性维度审查。
- 本轮动作：对高票和边界 finding 逐项追源码，确认风险是否真实可达、是否已有上层防护，并同步修正根因文档。
- 过滤规则：只保留当前 worktree 源码可复核、有明确 `file:line` 证据、能指向文档具体位置的 finding；剔除旧报告、历史编号、无证据结论和审查 agent 错读。

## Findings 汇总（复核后）

| 严重级别 | 数量 | 复核处理 |
|---|---:|---|
| P1 | 1 | 1 个成立：上一版文档默认路径归因错误，已修正为 DB snapshot 优先、toolbridge fallback |
| P2 | 4 | 3 个真实可达，1 个证据不足需降级表述 |
| P3 | 2 | 2 个成立：证据可维护性和修复顺序已修正 |
| 合计 | 7 | 全部逐项复核并写回目标文档 |

## 已剔除项

- 剔除“文档路径写成 `frontend-app/src/pages/workflow/WorkflowPage.jsx`”相关 finding。复核目标文档仅写 `WorkflowPage.jsx:52-58` 等短路径，没有写 `pages/workflow/`；该 finding 属于审查 agent 错读，无证据。

## 逐项源码复核结论

| Finding | 复核结论 | 是否真实可达 | 已有上层防护 | 文档处理 |
|---|---|---|---|---|
| F-01 后端主读路径归因 | 成立，但性质是“文档过度归因” | 默认 toolbridge 主路径不成立；DB snapshot N+1 真实可达 | `hasDAGSnapshotQueries()` + 默认 app/store/dbquery 装配使 snapshot 优先 | 根因文档改为 DB snapshot 优先，toolbridge 仅 fallback |
| F-02 detail/run 查询负载 | 成立 | 有选中 DAG/run 时真实可达 | 仅 selected key 为空时跳过；列表失败不会阻断 detail 刷新 | 根因链路加入 list + detail + run 扇出 |
| F-03 刷新触发面遗漏 | 成立 | focus/visibility、action refresh、retry 均可达；activePage effect 属边界入口 | WorkflowPage 挂载、document visible、action 业务条件等局部限制；activePage 受条件挂载限制；无统一合流 | 新增触发矩阵和修复覆盖要求 |
| F-04 `workflowSyncFailure` 陈旧告警 | 成立 | refreshDags 失败后，focus/visibility 成功查询可能绕过清理 | 手动/action 的 `refreshDags()` 成功会清空；直接 invalidate 成功不会 | 加入双状态竞争和清理建议 |
| F-05 `CanceledError` / `CancelledError` 来源 | 边界项：原归因过度确定 | “错误会被展示”真实可达；“一定来自 TanStack”证据不足 | 无类型识别；仅 `error.message` 透传 | 降级为可能来源，禁止按文案粗暴过滤 |
| F-06 node_modules 证据 | 成立 | 文档证据不可维护风险真实 | lockfile 可作为稳定版本证据；node_modules 被 gitignore | 删除本地 node_modules 行号依赖，改用 package-lock + 项目调用点 |
| F-07 修复方向过宽 | 成立 | 非运行时风险；属于落地风险 | 现有测试只覆盖 immediate reject + retry success | 增加最小测试/止血/后端优化顺序 |

## Accepted Findings（带复核细节）

### F-01 / P1：后端主读路径被误写成 mcp-orch/toolbridge，当前装配优先走 DB snapshot

- 原文档位置：`docs/cc/workflow-sync-root-cause-20260604.md` 结论、后端读路径、修复方向章节
- 复核结论：**成立；上一版文档需要修正。**
- 可达性：默认 app 下 `mcp-orch/toolbridge` 不是 dashboard DAG 读主路径；默认 DB snapshot 路径真实可达，且存在 latest-run N+1。
- 上层防护：默认装配 `DBQueries` 后，`hasDAGSnapshotQueries()` 会让 `ListDAGs()` / `ListDAGRuns()` 走 snapshot SQL，避免 fallback 到 mcp-orch/toolbridge。

证据：

- app 同时装配 `store.Module` 与 `dashboard.Module`：`internal/app/modules.go:47-62`
- `store.Module` 包含 `dbquery.Module`：`internal/store/module.go:35-47`
- `dbquery.Module` 提供 store：`internal/store/dbquery/module.go:10-15`
- dashboard service 注入并传入 `DBQueries`：`internal/module/dashboard/module.go:18-29`, `48-63`
- snapshot 可用判定：`internal/module/dashboard/dag_snapshot.go:65-67`
- `ListDAGs()` / `ListDAGRuns()` 优先走 snapshot：`internal/module/dashboard/detail.go:49-60`, `82-100`
- fallback DAGRuntime/toolbridge 仍存在：`internal/app/modules.go:90-93`, `internal/app/orchestration_dag_runtime_adapter.go:37-45`, `97-107`

目标文档已修正：把后端路径拆成“默认 DB snapshot 路径”和“无 snapshot fallback 到 mcp-orch/toolbridge 路径”；当前可证性能风险表述为 snapshot 路径仍存在 `ListDAGs + 每 DAG 一次 ListDAGRuns` 的 latest-run N+1。

### F-02 / P2：刷新/重试路径低估了 detail/run 查询负载

- 复核结论：**成立。**
- 可达性：当页面已有选中 DAG 或 selected run 时真实可达。
- 上层防护：只有 `selectedDagKey` / `effectiveSelectedRunKey` 为空时跳过；`refreshDags()` catch 后返回缓存，不会阻断后续 detail/run 刷新。

证据：

- `refreshDags()` catch 后仍返回 query cache：`frontend-app/src/pages/workflows/WorkflowPage.jsx:651-660`
- `fetchWorkflowDagDetail()` 并发调用 `getDagDetail` + 两次 `getDagRuns`：`WorkflowPage.jsx:707-713`
- detail query enabled by selected DAG：`WorkflowPage.jsx:736-740`
- run detail query/fetchQuery：`WorkflowPage.jsx:767-777`
- `refreshWorkflowSurface()` 串起 list 与 detail/run：`WorkflowPage.jsx:807-811`
- 后端对应 RPC：`internal/module/dashboard/rpc.go:333-352`

目标文档已修正：把“重试仍失败”的链路改为 list + selected DAG detail/run 多请求链路，并要求合流覆盖 `dags`、`dag-detail`、`dag-run` 三类 query key。

### F-03 / P2：刷新风暴触发面遗漏 focus/visibility 和 action refresh

- 复核结论：**成立。**
- 可达性：focus/visibility、workflowRevision、action refresh、retry 都能到达 dashboard invalidate/refresh；`activePage` effect 有代码入口，但受条件挂载模型限制，本轮不作为主因。
- 上层防护：focus/visibility 只在 WorkflowPage 挂载且 document 非 hidden 时触发；actions 有各自业务条件；`activePage` effect 首次挂载会被 `mountedRef` 跳过；但核心刷新路径没有统一 single-flight/coalesce。

证据：

- WorkflowPage 调 `useDashboardFocusInvalidation(workflowCwd, 'dags')`：`frontend-app/src/pages/workflows/WorkflowPage.jsx:620`
- focus/visibility listener 直接 invalidate：`frontend-app/src/pages/shared/pageShared.js:114-123`
- activePage effect 边界入口：`WorkflowPage.jsx:623-627`；但 `frontend-app/src/App.jsx:258-270` 显示 WorkflowPage 只在 activePage 为 `workflows` 时挂载，首次 effect 会被 `mountedRef` 跳过
- workflowRevision effect：`WorkflowPage.jsx:796-816`
- start action 刷新 list/detail：`WorkflowPage.jsx:898-910`
- stop action finally 刷新 list/detail：`WorkflowPage.jsx:920-935`
- schedule action 并发刷新 list/detail：`WorkflowPage.jsx:972-988`
- delete action 成功后刷新 list：`WorkflowPage.jsx:940-965`
- retry 按钮：`WorkflowPage.jsx:1113-1129`

目标文档已修正：新增触发矩阵，并明确修复点必须在共享 refresh primitive 或 query-key 层合流，不能只包 `refreshWorkflowSurface()`。

### F-04 / P2：`workflowSyncFailure` 可能形成陈旧告警，文档只写了 React Query cached error

- 复核结论：**成立。**
- 可达性：`refreshDags()` 失败设置 `workflowSyncFailure` 后，focus/visibility 直接 invalidate 成功不会经过 `setWorkflowSyncFailure('')`，因此可能出现数据已更新但顶部仍显示旧同步失败。
- 上层防护：手动 retry 或 action 刷新若走 `refreshDags()` 并成功，会清空；直接 query success 没有清空防护。

证据：

- `workflowSyncFailure` 独立 state：`frontend-app/src/pages/workflows/WorkflowPage.jsx:641`
- 仅 `refreshDags()` 成功清空，catch 设置失败文案：`WorkflowPage.jsx:651-659`
- `syncError` 优先展示 `syncFailure`：`WorkflowPage.jsx:877-884`
- focus/visibility invalidate 不调用 `refreshDags()`：`frontend-app/src/pages/shared/pageShared.js:114-123`

目标文档已修正：补充“陈旧/误报告警”风险，并建议任意 `dags` query 成功后清空 `workflowSyncFailure`，或统一 `syncError` 来源。

### F-05 / P2：`CanceledError` / `CancelledError` 来源归因过度确定

- 复核结论：**边界项成立：上一版归因过度确定，但错误展示风险真实可达。**
- 可达性：前端会 catch 任意错误并展示 `error.message`，所以取消/超时/后端错误都可能进入 `workflow-sync-alert`。
- 上层防护：没有类型/来源识别；只有通用 `errorMessage()` 截断文案。
- 证据缺口：仓库源码不能证明截图中的 `CanceledError` 与 `CancelledError` 一定都来自 TanStack Query，也不能排除 Wails/RPC/后端取消错误。

证据：

- `refreshDags()` catch 任意错误后展示：`frontend-app/src/pages/workflows/WorkflowPage.jsx:655-659`
- cached sync error 拼接 query error message：`frontend-app/src/pages/shared/pageShared.js:106-110`
- `errorMessage()` 只读 `error.message` 或 `String(error)`：`pageShared.js:273-275`
- `callAPI()` catch 后重抛带 trace 的原错误：`frontend-app/src/shared/api/wailsBridge.js:609-617`

目标文档已修正：将“来自刷新重入/取消语义”降级为“与 TanStack Query cancellation 行为相符的可能来源”；修复建议改为“必须按可信类型/来源识别 cancellation，不能按 message/name 粗暴过滤 `CanceledError`”。

### F-06 / P3：React Query 内部源码证据引用不可维护

- 复核结论：**成立。**
- 可达性：生产审查中引用 ignored `node_modules` 行号不可复现。
- 上层防护：lockfile 可以证明版本；项目调用点可以证明未显式设置 `cancelRefetch`；但不能替代 repo-owned 复现测试。

证据：

- `node_modules` 被忽略：`.gitignore:22-23`
- React Query 依赖：`frontend-app/package.json:15-17`
- query-core 锁定版本：`frontend-app/package-lock.json:1866`
- 项目调用 `invalidateQueries`：`frontend-app/src/pages/workflows/WorkflowPage.jsx:651-660`

目标文档已修正：移除本地安装包源码行号，改用 package/lockfile + 项目调用点，并把上游取消语义标为“基于锁定版本/上游行为的推断”。

### F-07 / P3：修复方向过宽，缺少最小落地顺序和验收锚点

- 复核结论：**成立。**
- 可达性：这是落地风险，不是运行时 bug；若直接跨前端 single-flight、RPC abort、后端 batch、UI 数据模型多层改动，容易扩大 diff 且难以证明修复。
- 上层防护：现有测试只覆盖 immediate reject + retry success，不覆盖 pending/reentry/stale failure。

证据：

- 现有测试：`frontend-app/src/App.test.jsx:6443-6499`
- `refreshWorkflowSurface()` 当前链路：`frontend-app/src/pages/workflows/WorkflowPage.jsx:807-816`
- retry 按钮：`WorkflowPage.jsx:1113-1129`

目标文档已修正：增加最小落地顺序：先补 pending/reentry/stale alert 前端测试，再做共享 refresh 合流与 cancellation 类型识别；后端 batch latest-run 放到前端止血之后评估。

## 审查覆盖说明

- 已检查目标文档全文并重写根因链路中不准确部分。
- 已核对当前 React 新 UI 路径：`frontend-app/src/pages/workflows/WorkflowPage.jsx`、`frontend-app/src/pages/shared/pageShared.js`、`frontend-app/src/shared/api/wailsBridge.js`、`frontend-app/src/entities/client/model/useClientStore.js`、`frontend-app/src/App.jsx`、`frontend-app/src/App.test.jsx`。
- 已核对 dashboard 后端路径：`internal/module/dashboard/*`、`internal/app/modules.go`、`internal/app/orchestration_dag_runtime_adapter.go`、`internal/store/*`。
- 未引用旧报告、迁移计划、ai01-docs 或 rollout summary 作为 finding 证据。
- 未修改业务代码，未运行测试；本次产物是 docs-only 源码复核文档。
