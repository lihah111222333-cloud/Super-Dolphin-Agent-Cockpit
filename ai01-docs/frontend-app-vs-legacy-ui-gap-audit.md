# frontend-app 与旧 agent-terminal UI 功能差距审计

日期：2026-06-03
审计范围：

- 新 UI 客户端：`/Users/ai/Desktop/Super-Dolphin/frontend-app`
- 旧 UI 客户端与桌面宿主：`/Users/ai/Desktop/Super-Dolphin/cmd/agent-terminal`

## 1. 审计口径

本次审计按代码静态扫描完成，没有启动桌面端做视觉回归。结论里的“已实现/未实现”以源码中是否存在页面入口、交互控件、状态流、后端 RPC 调用与测试线索为依据。

重要口径：

1. `cmd/agent-terminal` 本身是 Wails/HTTP/RPC 桌面宿主；旧页面主体在 `cmd/agent-terminal/frontend/vue-app`。
2. `frontend-app` 是当前 React/Vite 新 UI；`run-new-ui-desktop.sh` 通过 `VITE_DEV_URL` 让 `cmd/agent-terminal` 代理到 `frontend-app`。
3. 因此，Go/Wails 宿主层不算“旧 Vue 页面功能”，而是新旧 UI 共用的后端接入层。
4. 本文重点记录“旧 Vue 有而 React 新 UI 没有或未完整接上”的能力。

使用的推理约束：

- `karpathy-guidelines`：保持最小假设、逐文件证据、避免把宿主能力误判为页面能力。
- `superpowers:brainstorming`：用于整理对比维度与功能差距优先级；本任务是审计报告，不做实现变更。

## 2. 总体结论

React 新 UI 已经覆盖了核心桌面客户端的主路径：聊天、提示词、工作流、技能、记忆中心、共享文件、设置，并新增了链路追踪页。它不是空壳，很多核心后端 RPC 已经通过 `frontend-app/src/shared/api/backendApi.js` 做了集中封装与参数校验。

但从旧 Vue 客户端迁移完整度看，仍有明显缺口：

1. **任务/定时任务页未迁移**：旧 UI 有 `TasksPage` 与 `CronPanel`，支持任务工单、执行追踪、cron job 列表/创建/编辑/删除/启停/runOnce/run history；新 UI 没有对应页面，也没有 `cronjob/*` RPC facade。
2. **命令卡页未迁移**：旧 UI 有命令卡列表并可发送到当前会话；新 UI 没有命令卡页面，且 `runDashboardCommand()` 明确抛出“backend RPC is not registered”。
3. **聊天高级工作台未完全迁移**：新 Chat 主流程可用，但旧 UI 的 fork draft、路径选择、富 diff/preview/save/open/locate、cmd mode/card overview 等仍未完整复刻。
4. **提示词高级能力未完整迁移**：新 UI 有列表、编辑、创建向导、pending draft、强制启动偏好；但旧 UI 的 `prompt-intents/dry-run`、`SectionsEditor`、`match_when` 调试编辑、只读 fallback 到 `dashboard/prompts`、复制 prompt 内容等没有完整接到页面。
5. **工作流页缺少旧 Vue 的拓扑与共享文件分析面板**：新 UI 覆盖 DAG 列表、详情、运行、停止、删除、计划、AI 设计与节点编辑，但旧 UI 的 `DagTopologyPanel`、`DagSharedFilesPanel`、更完整 final output 读取面板没有迁移完全。
6. **设置页主体已迁移，但存在局部交互和 Provider 细节缺口**：新设置页覆盖 About、Turn Tracker、Context Usage、Provider、LSP Prompt、Builtin Tools、UI Log；但 UI Log 刷新按钮没有 onClick，Provider 缺少旧页的 personality、受限只读 readable roots、模型/effort 下拉和规范化逻辑。
7. **观测页是新 UI 新增能力，但路由/启动恢复不完整**：新 nav 有 `observability`，页面也存在；但路由表和 bootstrap page id 集合没有纳入 `observability`，深链/恢复能力不完整。

## 3. 路由与页面覆盖矩阵

| 旧 Vue 导航/页面 | 旧实现位置 | 新 React 页面 | 迁移状态 | 主要差距 |
|---|---|---|---|---|
| Chat | `cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.js` | `frontend-app/src/pages/chat/ChatPage.jsx` | 部分完成 | 主聊天可用；fork draft、旧 DiffPanel 交互、路径选择、cmd mode/overview 未完整迁移 |
| 提示词 | `pages/SystemPromptPage.js`、`pages/PromptIntentWizard.js`、`pages/SectionsEditor.js` | `frontend-app/src/features/prompts/PromptPageView.jsx` | 部分完成 | dry-run、sections、match_when、只读 fallback、复制内容缺失或未接页面 |
| 任务流程/DAG | `pages/DagsPage.js` | `frontend-app/src/pages/workflows/WorkflowPage.jsx` | 大部分完成 | 拓扑、共享文件分析、final output 读取面板不完整 |
| 任务 | `pages/TasksPage.js`、`pages/CronPanel.js` | 无独立页面；bootstrap alias 到 `workflows` | 未迁移 | task acks/traces、cron jobs 全缺 |
| 命令 | `pages/CommandsPage.js` | 无独立页面；bootstrap alias 到 `workflows` | 未迁移 | commandCards 列表与发送到会话缺失 |
| 技能 | `pages/SkillsPage.js` | `frontend-app/src/pages/skills/SkillsPage.jsx` | 大部分完成 | import summary drafts、冲突预览 diff/hash/手动步骤细节弱化 |
| 记忆中心 | `pages/MemoryCenterPage.js` | `frontend-app/src/pages/memory/MemoryPage.jsx` | 完成/增强 | 新 UI 使用异步整合 job 与轮询，整体不低于旧 UI |
| 共享文件 | `pages/SharedFilesPage.js` | `frontend-app/src/pages/files/FilesPage.jsx` | 完成 | 列表、分类、预览、复制、导出、删除保护、继续对话均有 |
| 设置 | `pages/SettingsPage.ts` 与 `pages/settings/*` | `frontend-app/src/pages/settings/SettingsPage.jsx` | 大部分完成 | Provider 细节、UI Log 刷新按钮、只读沙箱细节未完全一致 |
| 链路追踪 | 旧 UI 无独立同级页面 | `frontend-app/src/pages/observability/ObservabilityPage.jsx` | 新增 | 页面存在，但 app route/bootstrap 未完整注册 |

路由证据：

- 旧 Vue nav 包含 `chat/prompts/dags/tasks/skills/commands/memory-center/memory/settings`，见 `cmd/agent-terminal/frontend/vue-app/app.js`。
- 新 React nav 包含 `chat/prompts/workflows/skills/memory/observability/files/settings`，见 `frontend-app/src/App.jsx`。
- 新 store 的 bootstrap alias 把 `tasks`、`commands` 映射到 `workflows`，说明旧页面入口被折叠，而不是功能等价迁移。

## 4. 后端 RPC 对接覆盖矩阵

| 能力面 | 旧 Vue 后端调用 | 新 React 后端调用 | 状态 |
|---|---|---|---|
| Thread/Turn | `thread/start`、`turn/start`、`thread/recover`、`thread/config/*` 等 | `backendApi.js` 封装 `THREAD_*`、`TURN_*` | 已迁移 |
| Dashboard/DAG | `ui/dashboard/get page=dags`、`dashboard/dagDetail`、`dashboard/dagRuns`、`dashboard/dagStart`、`dashboard/dagApplyOps` | `listDags/getDagDetail/getDagRuns/startDag/applyDagOps` | 已迁移 |
| Cron job | `cronjob/list/get/create/update/delete/runOnce/setEnabled/listRuns` | 无 `cronjob/*` 方法 | 未迁移 |
| Command cards | 旧 dashboard 读 `commandCards`，页面发送 command template 到 chat | `runDashboardCommand()` 存在但直接抛错 | 未迁移 |
| Prompt assets | `prompt-assets/list`，失败时可只读 fallback 到 `dashboard/prompts` | `listPromptAssets()` | 部分迁移 |
| Prompt intent | `prompt-intents/draft/commit/discard/dry-run` | 页面只使用 draft/commit/discard；API facade 导出 dry-run | dry-run 页面未接 |
| Prompt sections | `prompt-sections/list/write/delete` via `SectionsEditor` | API facade 导出 list/write/delete；页面未用 | 页面未接 |
| Skills | `skills/local/*`、`skills/resolution_*` | `read/write/import/suggest/resolution_*` | 大部分迁移 |
| Memory | `ui/memory/*`、相似合并、auto-dream | `ui/memory/*`，并新增 consolidate-all/start/status | 完成/增强 |
| Shared files | `dashboard/sharedFiles`、`ui/memory/shared-file/get/delete`、保存文件 | 同等封装 | 完成 |
| Settings config | `ui/preferences/*`、`config/lspPromptHint/*`、`config/builtinTools/*` | 同等主体封装 | 大部分迁移 |
| Observability | 旧 UI 无独立页面 | `observability/recent/list`、`observability/trace/get` | 新增，但路由恢复不完整 |

## 5. P0/P1 级缺口

### 5.1 任务/定时任务未迁移

旧 Vue：

- `TasksPage` 有三个 sub-tab：任务工单、执行追踪、定时任务。
- `CronPanel` 作为定时任务 tab 渲染。
- `services/cron-api.js` 统一封装：
  - `cronjob/list`
  - `cronjob/get`
  - `cronjob/create`
  - `cronjob/update`
  - `cronjob/delete`
  - `cronjob/runOnce`
  - `cronjob/setEnabled`
  - `cronjob/listRuns`

新 React：

- `frontend-app/src/App.jsx` 没有 `tasks` nav/page。
- `frontend-app/src/entities/client/model/useClientStore.js` 仅把 bootstrap 里的 `tasks` alias 到 `workflows`。
- `frontend-app/src/shared/api/backendApi.js` 没有 `cronjob/*` RPC 方法。

影响：

- 用户无法在新 UI 查看 task ack / trace 列表。
- 用户无法创建、编辑、启停、立即运行、查看 cron job 历史。
- 旧 UI 的 `cron-panel.test.js`、`cron-api.behavior.test.js` 覆盖的行为在新 UI 没有对应测试。

建议优先级：P0。cron job 是独立业务能力，不是 DAG 工作流页的简单展示差异。

### 5.2 命令卡未迁移

旧 Vue：

- `CommandsPage` 展示 `commandCards`。
- 每张卡有“发送到当前会话”操作。
- 旧 app 从 dashboard payload 读取 `commandCards`。
- `runCommandCard` 取 `command_template` 后组装提示词发到当前或新会话。

新 React：

- 没有 `commands` 页面和 nav。
- bootstrap alias 把 `commands` 映射为 `workflows`。
- `backendApi.runDashboardCommand()` 直接抛出：`dashboard command execution backend RPC is not registered`。

影响：

- 旧 dashboard 命令卡无法在新 UI 中浏览。
- 命令卡到 chat 的启动链路缺失。
- 如果后端仍提供 `commandCards`，新 UI 当前没有消费面。

建议优先级：P0/P1，取决于命令卡是否仍是产品主流程。

### 5.3 Chat 高级工作台能力未完整迁移

新 React Chat 已实现：

- thread rail、active/archived 列表、pin/archive/unarchive/rename。
- new window、interrupt、recover、compact、thread config。
- composer、附件、拖拽、粘贴图片、provider/model/permission/tool surface。
- runtime panel、diff summary、activity stats/warnings。
- markdown、代码块、mermaid、图片等基础消息渲染。

旧 Vue 仍有但新 UI 未完全等价的能力：

1. **Fork/继承对话完整 UI**
   - 旧 UI 有 `useForkThread`、`ComposerForkDraftCard`，可从上下文 banner、composer、共享文件触发 fork draft。
   - 新 UI 只有 `continueWithSharedFile(path)`，会切到 chat 新草稿并附加文件；没有旧 UI 那种 fork draft 卡片、source thread、可选共享文件列表和提交状态。

2. **DiffPanel 富交互**
   - 旧 `DiffPanel` 支持 markdown preview、text preview、dirty state、save preview changes、citation/file-ref click。
   - 旧 chat 有 `PathChoiceModal`，并通过 `ui/code/locate`、`ui/code/open`、`ui/code/save` 做路径定位、打开、保存。
   - 新 React runtime diff 主要是只读 diff summary 和折叠文件展示；未发现 `ui/code/locate/open/save` 接入。

3. **Cmd mode/card overview**
   - 旧 Chat 模板里有 `CmdCardGrid`、`CmdOverviewPanel`、cmd layout/cols。
   - 新 Chat 没有相同的 cmd mode 工作区。

4. **timeline citation action 深度**
   - 旧 UI 有 `assistant-markdown-codex`、citation chips、task/automation/code-comment directive action 等较完整测试。
   - 新 UI 有基础 markdown/diff/mermaid/image 渲染，但旧 timeline 的 file-ref/citation action 链不完全等价。

建议优先级：P1。基础聊天可用，但这些能力影响“代码工作台”和“从结果继续工作”的高级体验。

### 5.4 Prompt 高级能力未完整迁移

新 React 已实现：

- `prompt-assets/list` 列表。
- `prompts/write/delete` 编辑与删除。
- `prompt-intents/draft/commit/discard` 创建向导和 pending draft。
- scope/status/tab 过滤。
- active prompt preference。

缺口：

1. **dry-run 未接页面**
   - `backendApi.js` 导出了 `dryRunPromptIntent()`。
   - `PromptPageView.jsx` 没有导入或调用 dry-run。
   - 旧 `PromptIntentWizard` 有“试问验证”面板，调用 `prompt-intents/dry-run`。

2. **Prompt sections 未接页面**
   - `backendApi.js` 导出了 `listPromptSections/writePromptSection/deletePromptSection`。
   - 旧 `SystemPromptPage` 注册 `SectionsEditor`，该组件调用 `prompt-sections/list/write/delete`。
   - 新 `PromptPageView.jsx` 未使用这些 API。

3. **match_when 高级编辑缺失**
   - 旧 `SystemPromptPage` 有 `serializeMatchWhenForEditor()` 和 `applyMatchWhenToPayload()`，可编辑并校验 `match_when` JSON。
   - 新 prompt form 未看到对应编辑入口。

4. **只读 fallback 不完整**
   - 旧页面在 `prompt-assets/list` 不可用时尝试 `dashboard/prompts` 只读旁路。
   - 新页面有 `fallbackMode` 状态和 UI 文案，但 `fetchPromptAssetsSurface()` 只返回 `listPromptAssets()` 结果，未实现旧的 `dashboard/prompts` 旁路。

5. **复制 prompt 内容缺失**
   - 旧卡片有“复制”按钮，必要时先读取完整 prompt 后写剪贴板。
   - 新卡片操作集中在编辑、删除、强制使用、pending draft；未发现复制内容操作。

建议优先级：P1。提示词主流程可用，但高级调试与只读降级能力未完整。

### 5.5 Workflow/DAG 高级面板缺失

新 React 已实现：

- 工作流列表、分类、状态与计划摘要。
- DAG detail、run history、selected run、start/terminate/delete。
- schedule modal、schedule toggle、`dashboard/dagApplyOps` 更新 cron。
- AI 设计和 agent node 编辑。

旧 Vue 独有或更完整的部分：

1. `DagTopologyPanel`：显示节点拓扑/依赖关系。
2. `DagSharedFilesPanel`：按节点分析共享文件输入输出。
3. `DagFinalOutputPanel`：可打开/读取 final output shared file。
4. node status bridge 事件记录更显式：旧 app 有 `routeDagBridgeEvent()` 与 `statusEvents` 传入 DagsPage。

新 React 的 `WorkflowPage.jsx` 有 final output 文本展示，但更接近 plain text；没有看到旧面板同等级的 topology/shared-files/final-output file 操作。

建议优先级：P1/P2。核心执行链已在，新缺的是诊断与可解释性面板。

## 6. P2 级差距与细节

### 6.1 Skills 页：导入摘要草稿与冲突预览细节弱化

新 React 已实现：

- skill dashboard 列表。
- 创建/编辑/删除。
- 子文件导航。
- import directories。
- summary suggest。
- resolution list/preview/apply。

差距：

1. 旧 `SkillsPage` 有 `visibleImportSummaryDrafts` 面板，可展示导入后生成的 summary draft，支持采用、编辑、跳过。
2. 旧冲突预览展示 `item.diff`、`source_hash`、`target_hash` 与多路径行。
3. 新 `SkillResolutionPreviewItem` 主要展示路径，diff/hash 技术细节未见渲染。
4. 旧页面有更详细的冲突 guide、action help、manual steps；新 UI 简化为按钮 title/help。

建议优先级：P2。技能主流程基本可用，但导入后整理与冲突审计体验下降。

### 6.2 Settings 页：主体迁移完成，但 Provider/UI Log 细节不足

新 React 已实现：

- About。
- Turn Tracker：`stallThresholdSec`。
- Context Usage Alert。
- Provider 基础设置。
- Summary / ApprovalPolicy。
- LSP Prompt read/write/reset/copy/show injected。
- Builtin tools read/write。
- UI Log 显示与日志级别切换。

差距：

1. **UI Log 刷新按钮没有绑定点击处理**
   - 新 `UILogCard` 渲染“刷新日志”按钮，但没有 `onClick`。
   - 旧 `SettingsPage.ts` 的按钮调用 `refreshLogPanel`。

2. **Provider 模型/effort 从下拉变成自由输入**
   - 旧 `ProviderSettings.ts` 使用 `MODEL_OPTIONS_BY_PROVIDER`、`EFFORT_MODES_BY_PROVIDER`，并处理 Claude Opus max effort、canonicalize model slug。
   - 新设置页是普通 input，容易保存无效模型/effort。

3. **Personality 缺失**
   - 旧 Provider 设置包含 `personality` 选项。
   - 新 React Provider form 未见 personality 字段。

4. **readOnly restricted readable roots 缺失**
   - 旧 readOnly mode 可选 `fullAccess` / `restricted`，restricted 时填写可读目录。
   - 新 `sandboxPreferenceValue()` 对 `readOnly` 只保存 `{ type: 'readOnly' }`。

5. **writable roots 校验弱化**
   - 旧 `validateAbsPaths()` 要求 workspaceWrite 的路径为绝对路径。
   - 新 UI 仅按行 split，没有看到绝对路径校验。

建议优先级：P2。不会阻断基础使用，但可能导致配置质量和旧设置兼容性下降。

### 6.3 Observability 页是新增能力，但 route/bootstrap 不完整

新 UI 新增：

- `ObservabilityPage.jsx` 支持 recent list、trace expansion、复制 Trace ID。
- `backendApi.js` 封装 `observability/recent/list`、`observability/trace/get` 等 RPC。

缺口：

- `frontend-app/src/App.jsx` 的 `PAGE_ROUTE_BY_ID` 没有 `observability`。
- `PAGE_ID_BY_ROUTE` 没有 `/observability`。
- `useClientStore.js` 的 `APP_PAGE_IDS` 也没有 `observability`。

影响：

- 点击 nav 可以在当前内存状态显示页面。
- 但 deep link、history route、window bootstrap 恢复到 observability 可能不完整或回落到 Chat。

建议优先级：P2。这是新 UI 自身补全项，不是旧 UI 迁移缺口。

## 7. 已完成或优于旧 UI 的部分

### 7.1 Memory

新 `MemoryPage.jsx` 覆盖旧 `MemoryCenterPage.js` 的核心能力：

- overview/health。
- private/team 或 preference/project 分类。
- 新建/编辑/删除 memory entry。
- auto-dream toggle。
- similar group merge/ignore。
- merge all。

新 UI 还使用 `ui/memory/similarity/consolidate-all/start` 与 status 轮询，避免旧 UI 同步 long-running RPC 卡住页面。此处可视为新 UI 强于旧 UI。

### 7.2 Shared Files

新 `FilesPage.jsx` 与旧 `SharedFilesPage.js` 能力基本等价：

- `dashboard/sharedFiles` 加载。
- final/work/all 分类。
- search/sort。
- `ui/memory/shared-file/get` 读取。
- saveTextFile 导出。
- delete with retention/final-output protection。
- 复制内容。
- 基于共享文件继续对话。

差异主要是视觉和实现方式，不是功能缺失。

### 7.3 Backend API facade

新 `backendApi.js` 明显更集中：

- RPC method name 常量化。
- 参数 payload 校验。
- thread、turn、dashboard、memory、prompt、dag、skills、observability、native bridge 统一出口。

这降低了页面直接拼 RPC payload 的风险。但也暴露出“facade 有方法、页面没接”或“facade 明确抛错”的迁移缺口，例如 prompt dry-run/sections 与 dashboard command。

## 8. 建议迁移优先级

### P0：先补业务入口缺失

1. 新建或恢复任务页：
   - task ack list。
   - task trace list。
   - cron job panel。
   - `cronjob/*` API facade 与页面测试。

2. 新建或恢复命令卡页：
   - dashboard command cards 列表。
   - command template 发送到当前/新 chat。
   - 明确是否仍需要 backend command execution RPC；如果不需要，就删除/改名 `runDashboardCommand()` 避免误导。

### P1：补主流程高级交互

1. Chat：
   - fork draft 卡片和 source thread 继承。
   - shared-file fork 而不是仅新草稿。
   - `ui/code/locate/open/save` 与 PathChoiceModal。
   - 旧 DiffPanel 的 preview/dirty/citation/file-ref 行为。

2. Prompt：
   - dry-run 面板。
   - SectionsEditor。
   - `match_when` advanced/debug editor。
   - `dashboard/prompts` readonly fallback。
   - 复制 prompt 内容。

3. Workflow：
   - topology panel。
   - shared files panel。
   - final output file open/read panel。

### P2：补一致性和可维护性

1. Skills import summary draft 面板。
2. Skills resolution preview diff/hash/manual steps。
3. Settings Provider personality、model/effort options、readOnly restricted readable roots、writable roots validation。
4. Settings UI Log refresh button onClick。
5. Observability route/bootstrap 注册。

## 9. 关键证据文件索引

旧 UI / 宿主：

- `cmd/agent-terminal/main.go`
- `cmd/agent-terminal/frontend.go`
- `cmd/agent-terminal/frontend/vue-app/app.js`
- `cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.js`
- `cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.template.js`
- `cmd/agent-terminal/frontend/vue-app/components/DiffPanel.js`
- `cmd/agent-terminal/frontend/vue-app/components/ComposerForkDraftCard.js`
- `cmd/agent-terminal/frontend/vue-app/components/PathChoiceModal.js`
- `cmd/agent-terminal/frontend/vue-app/pages/DagsPage.js`
- `cmd/agent-terminal/frontend/vue-app/components/dag/DagTopologyPanel.js`
- `cmd/agent-terminal/frontend/vue-app/components/dag/DagSharedFilesPanel.js`
- `cmd/agent-terminal/frontend/vue-app/components/dag/DagFinalOutputPanel.js`
- `cmd/agent-terminal/frontend/vue-app/pages/TasksPage.js`
- `cmd/agent-terminal/frontend/vue-app/pages/CronPanel.js`
- `cmd/agent-terminal/frontend/vue-app/services/cron-api.js`
- `cmd/agent-terminal/frontend/vue-app/pages/CommandsPage.js`
- `cmd/agent-terminal/frontend/vue-app/pages/SystemPromptPage.js`
- `cmd/agent-terminal/frontend/vue-app/pages/PromptIntentWizard.js`
- `cmd/agent-terminal/frontend/vue-app/pages/SectionsEditor.js`
- `cmd/agent-terminal/frontend/vue-app/pages/SkillsPage.js`
- `cmd/agent-terminal/frontend/vue-app/pages/MemoryCenterPage.js`
- `cmd/agent-terminal/frontend/vue-app/pages/SharedFilesPage.js`
- `cmd/agent-terminal/frontend/vue-app/pages/SettingsPage.ts`
- `cmd/agent-terminal/frontend/vue-app/pages/settings/ProviderSettings.ts`
- `cmd/agent-terminal/frontend/vue-app/pages/settings/LspPromptSettings.ts`
- `cmd/agent-terminal/frontend/vue-app/pages/settings/BuiltinToolsSettings.ts`

新 UI：

- `frontend-app/src/App.jsx`
- `frontend-app/src/entities/client/model/useClientStore.js`
- `frontend-app/src/shared/api/backendApi.js`
- `frontend-app/src/shared/api/wailsBridge.js`
- `frontend-app/src/pages/chat/ChatPage.jsx`
- `frontend-app/src/features/prompts/PromptPageView.jsx`
- `frontend-app/src/pages/workflows/WorkflowPage.jsx`
- `frontend-app/src/pages/skills/SkillsPage.jsx`
- `frontend-app/src/pages/memory/MemoryPage.jsx`
- `frontend-app/src/pages/files/FilesPage.jsx`
- `frontend-app/src/pages/settings/SettingsPage.jsx`
- `frontend-app/src/pages/observability/ObservabilityPage.jsx`

相关文档：

- `README.md`
- `docs/doc/codemap/01-terminal-ui-go.md`
- `docs/doc/codemap/01-terminal-ui-react.md`
- `docs/doc/codemap/01-terminal-ui-vue.md`

## 10. 动态测试补充（2026-06-03）

测试命令：

```bash
./run-new-ui-desktop.sh
```

启动结果：

- 本地 PostgreSQL 已在 `127.0.0.1:5432` 监听。
- 新 UI Vite 服务：`http://127.0.0.1:5175`。
- 桌面后端 / bridge：`http://127.0.0.1:4512`，`/metrics` 可访问。
- control rpc：`127.0.0.1:8092`。
- 后端日志：`.tmp/run-new-ui-desktop/backend.log`。

Playwright CLI 覆盖：

- `Chat`：首屏可渲染，项目选择、会话栏、composer、权限/模型控件存在；无会话时发送相关按钮禁用。
- `提示词`：可渲染为“AI 能力与资料”，显示当前项目、tabs、prompt card；未看到空白或控制台错误。
- `自动化`：可渲染 workflow 页面，能看到运行中 DAG、日程按钮、历史和步骤；动态确认仍未看到旧 UI 的 topology panel、shared files panel、final output open/read panel。
- `技能`：可渲染技能管理，显示 22 个项目共享技能，搜索、新建、导入入口存在。
- `记忆中心`：可渲染，显示 9 条记忆、健康度、自动沉淀开关、分类 tabs 和记忆卡片。
- `链路追踪`：点击侧栏后页面内容可渲染，`查询最新日志` 能返回 50 条 trace，说明观测查询后端路径可用。
- `共享文件`：可渲染，显示 `daily-life-plan.md` 文件卡，打开、导出、继续对话入口存在。
- `设置`：可渲染，构建信息刷新成功，Provider、Prompt、内置能力、UI LOG 等后端数据可读。
- Playwright console 检查：`console warning` 和 `console error` 均为 0；仅有 React DevTools info。

动态发现：

- `/observability` 深链有问题：点击侧栏“链路追踪”后 URL 停在 `/` 但内容切到观测页；直接访问 `http://127.0.0.1:5175/observability` 会回到 Chat 首屏。该问题会影响刷新、书签和外部链接打开。
- 观测页查询结果中存在早期启动阶段的历史 timeout trace，例如 `ui/state/get`、`ui/sidebar/get`、`ui/memory/get` 的 30s/120s timeout；本轮点击后的最新 trace 为 `ok`，因此这些错误更像历史记录而不是当前点击新产生的失败。
- Chrome 插件打开 `http://127.0.0.1:5175/` 后能渲染新 UI，并能进入 `/settings` 读取 ABOUT / Provider / Prompt / UI LOG 数据。Chrome 新标签页没有继承 Playwright 会话的本地项目选择状态，首屏显示“未选择项目”，这是浏览器存储隔离导致的测试环境差异。
- Computer Use 按 `agent-terminal` 名称绑定会命中 `/Users/ai/Desktop/go-agent-v2/bin/agent-terminal.app` 的旧 bundle；该证据不应计入本项目新 UI 验证。应以 `/Users/ai/Desktop/Super-Dolphin/run-new-ui-desktop.sh` 产生的进程、`127.0.0.1:4512`、`127.0.0.1:5175` 为准。

### 10.1 当前项目复测修正（2026-06-03 13:19-13:21）

用户指出上一轮 Computer Use 绑定到了旧项目 app。按当前项目重新启动：

```bash
cd /Users/ai/Desktop/Super-Dolphin
./run-new-ui-desktop.sh
```

本轮真实进程证据：

- 脚本进程：`bash ./run-new-ui-desktop.sh`。
- 后端启动命令：`go run ./cmd/agent-terminal`。
- 当前监听后端进程：`agent-terminal`，监听 `127.0.0.1:4512`。
- 当前 Vite 进程：`node /Users/ai/Desktop/Super-Dolphin/frontend-app/node_modules/.bin/vite`，监听 `127.0.0.1:5175`。
- 当前 `agent-terminal` 进程 CWD：`/Users/ai/Desktop/Super-Dolphin`。
- 当前 Wails 窗口标题：`Super Agent`，进程 bundle identifier 为 `missing value`，即 `go run` 裸进程，不是可由 Computer Use 通过 `.app` bundle 精准绑定的应用。

本轮动态结果：

- 13:19 首次复测时，Playwright CLI 与 Chrome 插件都看到 Vite error overlay；当时 `frontend-app/src/App.jsx` 文件头部存在 Git conflict marker。
- 当时 Vite 报错位置：

```text
frontend-app/src/App.jsx:3:1
冲突标记位于文件开头
```

- 13:21 重新核对时，`frontend-app/src/App.jsx` 已无 conflict marker，`git status --short -- frontend-app/src/App.jsx` 不再显示 `UU`。
- 13:21 再次按当前项目启动脚本后，Playwright CLI 打开 `http://127.0.0.1:5175/` 可正常渲染 Chat，页面显示 `Super-Dolphin`、8 个 Agent、会话列表、composer、发送权限与模型选择。
- Chrome 插件打开同一地址也正常渲染，`vite-error-overlay=false`，正文包含 `Super Agent`、`Super-Dolphin`、`Chat`、`提示词`、`自动化`、`技能`、`记忆中心`、`链路追踪`、`共享文件`、`设置`。

结论：上一轮旧 bundle 的 Computer Use 证据无效；按当前 `/Users/ai/Desktop/Super-Dolphin` 项目复测，最终当前状态的新 UI 可以正常渲染 Chat。Computer Use 仍无法通过 `.app` path/bundle 精准绑定 `go run` 裸 Wails 进程，只能通过进程 PID/CWD/端口证明当前窗口来源。

## 11. 风险与后续验证

1. 当前项目复测过程中曾短暂观察到 `frontend-app/src/App.jsx` conflict marker 导致的 Vite overlay；最终复测时该文件已无冲突标记，Chat 可正常渲染。若后续再做完整动态覆盖，应先确认 `rg -n '<<<<<<<|=======|>>>>>>>' frontend-app/src` 无输出。
2. 当前工作区已有大量未提交改动，尤其 `frontend-app` 与 thread/observability 相关文件；本报告按当前工作区内容审计，可能包含用户尚未提交的新代码状态。
3. 若下一步要按报告实现迁移，建议每个 P0/P1 项都先加 focused regression test，再补最小页面/RPC facade。
4. 对“旧功能是否仍是产品需求”的判断需要产品侧确认；如果命令卡或 cron 已被设计上废弃，应在文档和代码中删除 alias/死入口，而不是保留半迁移状态。
