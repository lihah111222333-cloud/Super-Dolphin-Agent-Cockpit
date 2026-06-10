# frontend-app 与旧 agent-terminal UI 功能差距审计

日期：2026-06-03
审计范围：

- 新 UI 客户端：`/Users/ai/Desktop/Super-Dolphin/frontend-app`
- 旧 UI 客户端与桌面宿主：`/Users/ai/Desktop/Super-Dolphin/cmd/agent-terminal`

## 1. 审计口径

本审计初版按代码静态扫描完成；后续已按 P0/P1 缺口补齐部分新 UI 能力，并用 `frontend-app` 单元/组件测试验证。结论里的“已实现/未实现”以源码中是否存在页面入口、交互控件、状态流、后端 RPC 调用与测试线索为依据。

重要口径：

1. `cmd/agent-terminal` 本身是 Wails/HTTP/RPC 桌面宿主；旧页面主体在 `cmd/agent-terminal/frontend/vue-app`。
2. `frontend-app` 是当前 React/Vite 新 UI；`run-new-ui-desktop.sh` 通过 `VITE_DEV_URL` 让 `cmd/agent-terminal` 代理到 `frontend-app`。
3. 因此，Go/Wails 宿主层不算“旧 Vue 页面功能”，而是新旧 UI 共用的后端接入层。
4. 本文重点记录“旧 Vue 有而 React 新 UI 没有或未完整接上”的能力。

使用的推理约束：

- `karpathy-guidelines`：保持最小假设、逐文件证据、避免把宿主能力误判为页面能力。
- `superpowers:brainstorming`：用于整理对比维度与功能差距优先级；后续实现只补明确缺口，不做无关重构。

## 2. 总体结论

React 新 UI 已经覆盖了核心桌面客户端的主路径：聊天、提示词、工作流、技能、记忆中心、共享文件、设置，并新增了链路追踪页。它不是空壳，很多核心后端 RPC 已经通过 `frontend-app/src/shared/api/backendApi.js` 做了集中封装与参数校验。

但从旧 Vue 客户端迁移完整度看，仍有缺口与已补项需要区分：

1. **任务/定时任务页已补 React MVP**：新 UI 已恢复 `TasksPage`、三段 tab、task ack/trace 展示、cron job 列表/创建/编辑/删除/启停/runOnce/run history，并补 `cronjob/*` API facade 与测试。
2. **命令卡页已补 React MVP**：新 UI 已恢复 `CommandsPage` 与 nav，读取 dashboard `commandCards`，并通过当前/新 chat turn 链路发送 `command_template`。
3. **聊天高级工作台已补 P1 主链路**：fork draft MVP、共享文件页触发继承对话草稿、runtime diff `ui/code/locate/open/save` 基础链路、`PathChoiceModal` 多候选基础选择、markdown/image preview、dirty close guard、cmd mode/card overview MVP、timeline file-ref/citation action MVP 已补；剩余主要是旧 UI 视觉密度和少量 directive 样式边界的 P2/P3 深度等价。
4. **提示词高级能力已补主迁移**：新 UI 已覆盖列表、编辑、创建向导、pending draft、强制启动偏好、`prompt-intents/dry-run`、`SectionsEditor`、`match_when` 调试编辑、只读 fallback 到 `dashboard/prompts`、复制完整 prompt 内容。
5. **工作流页基础诊断面已补，旧面板深度仍有差距**：新 UI 覆盖 DAG 列表、详情、运行、停止、删除、计划、AI 设计、节点编辑、拓扑、共享文件分析与 final output 文件读取；node status bridge 事件已补自动刷新与 malformed payload fail-fast 校验，旧 UI 更细粒度面板状态仍未完全等价。
6. **技能与设置页迁移细节继续补齐**：技能页已补导入摘要草稿、重复导入 failures、同名冲突草稿、冲突 guide/preview 文案、筛选计数、搜索空态、preview markdown link 打开子文件、skill-file citation link 打开目标技能、conversation citation 明确提示，项目级新建技能已改走后端 `skills/create`，个人技能新建仍锁定 `skills/local/write` + `personal_type=user`，并新增页面级测试锁住 dashboard + `skills/local/*` + `skills/create` 链路；设置页已补 Provider personality、受限只读 readable roots、模型/effort 下拉、继承默认值保护、writable roots fail-fast 校验、UI Log 后端刷新、scoped/global preference fallback、tombstone、Claude model canonicalize、非 Opus `max` effort 限制、隐藏内部 Codex model provider、Codex identity 清空 tombstone、旧 Vue JSON-string sandbox preference 解析、active provider 选择后立即 `ui/preferences/set` 持久化、非法 active provider fail-fast、active provider 与 Provider properties 异步加载过期保护、Summary/ApprovalPolicy scoped/global fallback 与 tombstone，以及 `config/builtinTools/*` 页面级读写测试。
7. **观测页是新 UI 新增能力，路由/启动恢复已补到页面集合**：新 nav 有 `observability`，页面与 route/bootstrap page id 均已纳入；此项从旧 UI 迁移角度不再是缺口。

## 3. 路由与页面覆盖矩阵

| 旧 Vue 导航/页面 | 旧实现位置 | 新 React 页面 | 迁移状态 | 主要差距 |
|---|---|---|---|---|
| Chat | `cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.js` | `frontend-app/src/pages/chat/ChatPage.jsx` | P1 主链路已补 | 主聊天、fork draft MVP、共享文件页触发 fork draft、runtime diff locate/open/save、路径多候选基础选择、markdown/image preview、dirty close guard、cmd mode/overview MVP、timeline file-ref/citation action MVP 已补；剩余旧 UI 视觉密度与 directive 样式边界归入 P2/P3 |
| 提示词 | `pages/SystemPromptPage.js`、`pages/PromptIntentWizard.js`、`pages/SectionsEditor.js` | `frontend-app/src/features/prompts/PromptPageView.jsx` | 主流程已补齐 | dry-run、sections、match_when、只读 fallback、复制完整内容已补；剩余主要是旧 Vue 视觉和全部 guide 文案细节 |
| 任务流程/DAG | `pages/DagsPage.js` | `frontend-app/src/pages/workflows/WorkflowPage.jsx` | 大部分完成 | 拓扑、共享文件分析、final output 文件读取基础面板已补；node status bridge 自动刷新与 malformed payload fail-fast 已补，旧面板细节仍未完全等价 |
| 任务 | `pages/TasksPage.js`、`pages/CronPanel.js` | `frontend-app/src/pages/tasks/TasksPage.jsx` | 已补 MVP | task acks/traces 与 cron jobs 主流程已恢复；旧 CronPanel 视觉/边界行为仍需继续对齐 |
| 命令 | `pages/CommandsPage.js` | `frontend-app/src/pages/commands/CommandsPage.jsx` | 已补 MVP | commandCards 列表与发送到会话已恢复；旧 Chat cmd mode/overview 已在 Chat 侧补 MVP，剩余视觉与细分状态 |
| 技能 | `pages/SkillsPage.js` | `frontend-app/src/pages/skills/SkillsPage.jsx` | 主流程已补齐 | import summary drafts、重复导入 failures、同名冲突草稿、冲突 guide、preview diff/hash/path label/手动步骤、preview markdown link 子文件打开、dashboard + `skills/local/*` + `skills/create` 页面级测试已补；项目新建走 `skills/create`，个人新建走 `skills/local/write` + `personal_type=user`；剩余主要是视觉文案细差 |
| 记忆中心 | `pages/MemoryCenterPage.js` | `frontend-app/src/pages/memory/MemoryPage.jsx` | 完成/增强 | 新 UI 使用异步整合 job 与轮询，整体不低于旧 UI |
| 共享文件 | `pages/SharedFilesPage.js` | `frontend-app/src/pages/files/FilesPage.jsx` | 完成 | 列表、分类、预览、复制、导出、删除保护、继续对话均有 |
| 设置 | `pages/SettingsPage.ts` 与 `pages/settings/*` | `frontend-app/src/pages/settings/SettingsPage.jsx` | 主流程已补齐 | Provider personality/model/effort/readOnly restricted/UI Log refresh、scoped/global fallback、tombstone、Claude model canonicalize/max effort、继承默认值保护、writable roots 空值 fail-fast、active provider 与 Provider properties 过期加载保护、Summary/ApprovalPolicy scoped/global fallback、隐藏内部 Codex model provider、Codex identity 清空 tombstone、builtin tools 页面级读写测试已补；剩余主要是布局组织差异 |
| 链路追踪 | 旧 UI 无独立同级页面 | `frontend-app/src/pages/observability/ObservabilityPage.jsx` | 新增/已注册 | 页面、route、bootstrap page id 已纳入新 UI |

路由证据：

- 旧 Vue nav 包含 `chat/prompts/dags/tasks/skills/commands/memory-center/memory/settings`，见 `cmd/agent-terminal/frontend/vue-app/app.js`。
- 新 React nav 包含 `chat/prompts/workflows/tasks/commands/skills/memory/observability/files/settings`，见 `frontend-app/src/App.jsx`。
- 新 store 的 `APP_PAGE_IDS` 已包含 `tasks`、`commands`、`observability`；`TASKS` alias 已不再把旧页面入口折叠到 `workflows`。

## 4. 后端 RPC 对接覆盖矩阵

| 能力面 | 旧 Vue 后端调用 | 新 React 后端调用 | 状态 |
|---|---|---|---|
| Thread/Turn | `thread/start`、`turn/start`、`thread/recover`、`thread/config/*` 等 | `backendApi.js` 封装 `THREAD_*`、`TURN_*` | 已迁移 |
| Dashboard/DAG | `ui/dashboard/get page=dags`、`dashboard/dagDetail`、`dashboard/dagRuns`、`dashboard/dagStart`、`dashboard/dagApplyOps` | `listDags/getDagDetail/getDagRuns/startDag/applyDagOps` | 已迁移 |
| Cron job | `cronjob/list/get/create/update/delete/runOnce/setEnabled/listRuns` | `listCronJobs/getCronJob/createCronJob/updateCronJob/deleteCronJob/runCronJobOnce/setCronJobEnabled/listCronJobRuns` | 已补 MVP |
| Command cards | 旧 dashboard 读 `commandCards`，页面发送 command template 到 chat | `CommandsPage` 读 `commandCards`，`runDashboardCommand()` 通过 thread/turn 链路发送 | 已补 MVP |
| Prompt assets | `prompt-assets/list`，失败时可只读 fallback 到 `dashboard/prompts` | `listPromptAssets()`，失败时 `getDashboardPrompts()` 只读 fallback | 已迁移 |
| Prompt intent | `prompt-intents/draft/commit/discard/dry-run` | draft/commit/discard/dry-run 均已接页面 | 已迁移 |
| Prompt sections | `prompt-sections/list/write/delete` via `SectionsEditor` | `PromptSectionsPanel` 接 `list/write/delete` | 已迁移 |
| Skills | `skills/local/*`、`skills/create`、`skills/resolution_*` | `create/read/write/import/suggest/resolution_*` | 已迁移；项目级新建走 `skills/create`，导入摘要、failures、冲突 guide/preview 已补 |
| Memory | `ui/memory/*`、相似合并、auto-dream | `ui/memory/*`，并新增 consolidate-all/start/status | 完成/增强 |
| Shared files | `dashboard/sharedFiles`、`ui/memory/shared-file/get/delete`、保存文件 | 同等封装 | 完成 |
| Settings config | `ui/preferences/*`、`config/lspPromptHint/*`、`config/builtinTools/*`、`dashboard/logs` | 同等主体封装；UI Log 刷新使用 `backendApi.listDashboardLogs()` | 已迁移；Provider 运行时字段与 Summary/ApprovalPolicy scoped/global preference 解析、tombstone 和 settings config facade 已补 |
| Observability | 旧 UI 无独立页面 | `observability/recent/list`、`observability/trace/get` | 新增/已注册 |

后端 `internal` 对接证据：

- Skills RPC 注册在 `internal/module/skill/rpc.go`：`skills/local/read`、`skills/local/listFiles`、`skills/local/write`、`skills/local/importDir`、`skills/local/delete`、`skills/create`、`skills/summary/suggest`、`skills/resolution_list`、`skills/resolution_preview`、`skills/resolution_apply`。
- Settings preference/config RPC 注册在 `internal/module/uistate/rpc.go` 与 `internal/module/uistate/config_rpc.go`：`ui/preferences/get/set/getAll`、`config/lspPromptHint/read/write`、`config/builtinTools/read/write`。
- UI Log 后端刷新 RPC 注册在 `internal/module/dashboard/rpc.go`：`dashboard/logs`。

## 5. P0/P1 级缺口

### 5.1 任务/定时任务已补 React MVP

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

新 React 已补：

- `frontend-app/src/App.jsx` 增加 `tasks` nav、`/tasks` route 与页面渲染。
- `frontend-app/src/pages/tasks/TasksPage.jsx` 增加三个 sub-tab：任务工单、执行追踪、定时任务。
- `frontend-app/src/shared/api/backendApi.js` 增加 `cronjob/list/get/create/update/delete/runOnce/setEnabled/listRuns` facade。
- `CronPanel` 主流程已覆盖：列表、创建、编辑、删除确认、启停、立即运行、查看 run history。
- `frontend-app/src/App.test.jsx` 覆盖页面渲染、tab 切换、runOnce、setEnabled、listRuns、create。
- `frontend-app/src/shared/api/backendApi.test.js` 覆盖 `cronjob/*` payload。

剩余差距：

- 新 `TasksPage` 是 React MVP，旧 `CronPanel` 的全部视觉细节和历史测试边界尚未一比一迁移。
- cron 表达式使用 `cron-parser` 计算 `next_run_at`，需要继续用真实后端数据做桌面端回归。

当前优先级：P0 已处理，剩余为 P2/P3 验证与细节对齐。

### 5.2 命令卡已补 React MVP

旧 Vue：

- `CommandsPage` 展示 `commandCards`。
- 每张卡有“发送到当前会话”操作。
- 旧 app 从 dashboard payload 读取 `commandCards`。
- `runCommandCard` 取 `command_template` 后组装提示词发到当前或新会话。

新 React 已补：

- `frontend-app/src/App.jsx` 增加 `commands` nav、`/commands` route 与页面渲染。
- `frontend-app/src/pages/commands/CommandsPage.jsx` 读取 dashboard payload 的 `commandCards`。
- `frontend-app/src/entities/client/model/useClientStore.js` 的 `runDashboardCommand()` 不再抛占位错误，已组装 `command_template` 并发送到当前会话；没有 active thread 时会先启动 chat session。
- `frontend-app/src/App.test.jsx` 覆盖命令卡列表和“发送到当前会话”链路。
- `frontend-app/src/entities/client/model/useClientStore.test.js` 覆盖当前会话、无 active thread、空模板 fail-fast 三条路径。

剩余差距：

- 旧 Chat 模板里的 cmd mode/card overview 工作区已在 Chat 侧补 MVP；这不是命令卡独立页面本身，剩余是 Chat 内部高级工作台的视觉和细分状态等价。
- 如果产品仍需要专门的 backend command execution RPC，需要另行确认；当前实现按旧 UI 的“把模板发给 chat”路径恢复。

当前优先级：P0/P1 已处理，剩余归入 5.3 的 Chat cmd mode P2/P3 深度等价。

### 5.3 Chat 高级工作台能力已补 P1 主链路，剩余为深度等价

新 React Chat 已实现：

- thread rail、active/archived 列表、pin/archive/unarchive/rename。
- new window、interrupt、recover、compact、thread config。
- composer、附件、拖拽、粘贴图片、provider/model/permission/tool surface。
- runtime panel、diff summary、activity stats/warnings。
- markdown、代码块、mermaid、图片等基础消息渲染。
- fork draft MVP：composer 可打开继承对话草稿，展示 source thread、可选 shared files、提交状态，并通过 thread/start + turn/start 创建继承会话。
- 上下文使用率 banner 触发 fork draft：`usedPercent >= 90` 时显示 `context-usage-banner`，点击“新建继承会话”会调用 `openForkDraft({ origin: 'context-usage' })`。
- 共享文件页触发 fork draft：点击“用此文件继续对话”会切到 Chat，保持当前 source thread，打开继承对话草稿并预选该 shared file；没有 source thread 时保留普通新草稿 fallback。
- runtime diff file action MVP：文件组支持 `ui/code/locate`、`ui/code/open`、`ui/code/save`，可打开文本预览、编辑并保存。
- cmd mode/card overview MVP：聊天页新增 `ChatModeToolbar` 与 `CommandWorkspace`，可在对话工作区和命令工作区间切换；命令工作区展示 thread cards、状态、指标、diff/timeline 摘要，支持紧凑视图、2/3 列、打开 Agent、同步历史、停止 Agent。
- timeline file-ref/citation action MVP：`:codex-file-citation` 会通过 `ui/code/locate` + `ui/code/open` 打开文件预览；`:task-stub` 会把任务 prompt 写入 composer；`:automation-update` 会按旧 `citation-action-utils` 语义写入 composer；`:code-comment` 会打开对应文件并写入评论草稿；`agent://thread-id` citation chip 会切换到目标 thread。

旧 Vue 仍有但新 UI 未完全等价的能力：

1. **Fork/继承对话完整 UI**
   - 旧 UI 有 `useForkThread`、`ComposerForkDraftCard`，可从上下文 banner、composer、共享文件触发 fork draft。
   - 新 UI 已补 `threadFork.js`、store fork draft action 与 `ComposerForkDraftCard` 等价 MVP；composer 按钮与共享文件页“用此文件继续对话”均可触发 fork draft。
   - 当前状态：上下文 banner、composer、共享文件页三处触发链路均已恢复并有回归测试覆盖。

2. **DiffPanel 富交互**
   - 旧 `DiffPanel` 支持 markdown preview、text preview、dirty state、save preview changes、citation/file-ref click。
   - 旧 chat 有 `PathChoiceModal`，并通过 `ui/code/locate`、`ui/code/open`、`ui/code/save` 做路径定位、打开、保存。
   - 新 React 已补基础 file action：runtime diff 文件头有定位、打开按钮；打开后显示可编辑文本预览；保存调用 `ui/code/save`；`ui/code/locate` 返回多个候选时会打开基础路径选择弹窗，再按选中路径调用 `ui/code/open`。
   - 新 React 已补 `ui/code/open` 的 markdown/image preview：markdown 默认渲染后可编辑，image 走只读图片预览；未保存的编辑关闭时会阻断并提示先保存或放弃。
   - 剩余差距：旧 DiffPanel 的全部 preview 视觉细节、citation 来源样式和文件 ref 边界用例仍未逐项复刻；这些归入 P2/P3 深度等价，不再是 P1 主链路缺口。

3. **Cmd mode/card overview**
   - 旧 Chat 模板里有 `CmdCardGrid`、`CmdOverviewPanel`、cmd layout/cols。
   - 新 Chat 已补 `CommandWorkspace` MVP：按 thread card 展示 agent 状态、运行指标、diff/timeline 摘要和操作按钮。
   - 剩余差距：旧 UI 的全部 cmd layout 视觉、卡片密度、概览面板细分状态和指令入口仍未做一比一复刻；这些归入 P2/P3 深度等价，不再是 P1 主链路缺口。

4. **timeline citation action 深度**
   - 旧 UI 有 `assistant-markdown-codex`、citation chips、task/automation/code-comment directive action 等较完整测试。
   - 新 UI 已补 file citation、task stub、automation update、code-comment、agent thread link 的主链路测试；automation/code-comment 的 composer 写入语义已与旧 `citation-action-utils` 主链路一致。
   - 剩余差距：旧 `assistant-markdown-codex` 的所有 citation 样式和边界测试尚未完整搬迁；自动化创建/更新如果未来有独立后端 RPC，需要另做产品确认，旧 UI 当前也是 composer action 主链路。这些归入 P2/P3 深度等价。

建议优先级：P1 主链路已处理；剩余为 P2/P3 的旧 UI 深度视觉、样式和 directive 边界测试迁移。

### 5.4 Prompt 高级能力已补主迁移

新 React 已实现：

- `prompt-assets/list` 列表。
- `prompt-assets/list` 不可用时 fallback 到 `dashboard/prompts` 只读列表。
- `prompts/write/delete` 编辑与删除。
- `prompt-intents/draft/commit/discard` 创建向导和 pending draft。
- `prompt-intents/dry-run` 试问验证。
- `prompt-sections/list/write/delete` 分段编辑器。
- `match_when` 高级调试 JSON 编辑与保存前校验。
- prompt card “复制”操作：先读取完整 prompt 内容，再写入剪贴板。
- scope/status/tab 过滤。
- active prompt preference。

当前剩余：

1. 旧 Vue 的全部视觉排版、帮助文案和 guide 细节未做像素级一比一复刻。
2. 高级调试入口沿用本地开关 `super-dolphin.promptDebug` / `window.__SUPER_DOLPHIN_PROMPT_DEBUG__`，默认不展示；这是迁移后的显式调试面，不是普通用户主路径。

建议优先级：P1 主迁移已处理，剩余为 P3 视觉/文案一致性与产品默认开关决策。

### 5.5 Workflow/DAG 高级面板已补基础诊断面

新 React 已实现：

- 工作流列表、分类、状态与计划摘要。
- DAG detail、run history、selected run、start/terminate/delete。
- schedule modal、schedule toggle、`dashboard/dagApplyOps` 更新 cron。
- AI 设计和 agent node 编辑。
- `WorkflowTopologyPanel`：展示节点与依赖边。
- `WorkflowSharedFilesPanel`：按节点展示 shared file 读取/写入。
- `WorkflowFinalOutputPanel`：识别 final output shared file，并可调用 shared-file RPC 读取内容。

旧 Vue 仍更完整的部分：

1. 旧面板的视觉细节、异常状态与更细粒度的文件分析仍未一比一复刻。

当前已补：

1. React store 对 `task/node/statusChanged` bridge event 会驱动 `workflowRevision` 刷新 Workflow/Tasks/Commands 页面。
2. 该事件 payload 已按旧 Vue `useDagStatusEventBridge` 语义做 fail-fast 校验：必须包含 `dag_key`、`node_key`、`new_status`，且必须有 `run_key` 或有效 `run_id`；malformed payload 不再触发 workflow 刷新。

建议优先级：P2。核心执行链和基础诊断面已在，剩余主要是旧面板细节等价与事件深度。

## 6. P2 级差距与细节

### 6.1 Skills 页：导入摘要草稿、冲突预览与 preview link 已补

新 React 已实现：

- skill dashboard 列表。
- 创建/编辑/删除。
- 子文件导航。
- import directories。
- summary suggest。
- resolution list/preview/apply。
- 导入后 summary draft 面板：展示导入技能、简介建议、采用并编辑、编辑简介、跳过、收起。
- `skills/local/importDir` 返回的 `failures`：重复导入会显示“项目共享/私人使用里已存在”，非重复失败会显示失败来源。
- 导入后同名冲突：读取导入技能时遇到 same-name conflict 会生成 `conflict` 草稿，提示到冲突面板选择版本。
- 冲突预览技术信息：展示 source/target path、source_hash/target_hash 短 hash、diff。
- 冲突 guide/preview 文案：恢复旧 UI 的冲突说明、view action 预览说明，以及外部版本/本项目版本/保存位置等 path label。
- 同名冲突无自动动作时展示手动处理步骤。
- 旧 UI 等价列表反馈：筛选计数、当前搜索无结果空态、搜索/范围过滤后的总数提示。
- 技能正文 preview 中的 markdown link 可打开当前技能文件树内的子文件；相对路径按当前预览文件目录解析，不做任意路径扩展。
- 技能正文 preview 中的 skill-file citation link 可按 `skill_file` / `dir/SKILL.md` / 技能名称匹配目标技能，并通过 `skills/local/read` + `skills/local/listFiles` 打开目标技能。
- 技能正文 preview 中的 conversation citation link 不误触技能后端，页面显示“暂不支持会话跳转”提示。
- 项目级新建技能使用 `internal/module/skill/rpc.go` 注册的 `skills/create`，避免把新建主技能误走 `skills/local/write` 的第二条项目写入路径；编辑已有技能、关联文件和个人技能仍保留 `skills/local/write`。

本轮补齐：

1. `frontend-app/src/pages/skills/SkillsPage.jsx` 新增 `importSummaryDrafts` 状态和 `SkillImportSummaryPanel`。
2. `confirmImportScope()` 在 `skills/local/importDir` 后读取导入的 `SKILL.md`，缺简介时调用 `skills/summary/suggest` 生成草稿。
3. `SkillResolutionPreviewItem` 渲染 `diff`、`source_hash`、`target_hash`。
4. 同名冲突缺少 `keep_selected`/`rename_personal` 动作时展示旧 UI 等价 manual steps。
5. `SkillGrid` 增加筛选计数和“没有匹配技能”空态，避免搜索无结果时误显示“暂无技能”。
6. `SkillMarkdownPreview` 增加安全的 `[label](path)` 内联渲染和点击打开，只允许打开 `skills/local/listFiles` 返回的当前技能文件。

本轮继续补齐（2026-06-03 17:50）：

1. `frontend-app/src/pages/skills/SkillsPage.test.jsx` 从 export-only 测试升级为页面级后端迁移测试。
2. 测试覆盖 `getDashboardPage({ cwd, page: 'skills' })` 加载技能列表、`listSkillResolutions({ cwd })` 加载冲突状态。
3. 测试覆盖编辑技能时调用 `readSkill({ cwd, path })` 与 `listSkillFiles({ cwd, dir })`，并确认关联文件列表可见。
4. 测试覆盖保存已有技能时通过 `writeSkill({ cwd, path, content, scope, personal_type })` 写回主 `SKILL.md`。
5. 测试覆盖新建项目技能时通过 `createSkill({ cwd, name, content })` 调用 `skills/create`，不再用 `skills/local/write` 写项目主技能。
5. 对接的后端 RPC 仍来自 `internal/module/skill/rpc.go`：`skills/local/read`、`skills/local/listFiles`、`skills/local/write`、`skills/resolution_list` 等。

本轮继续补齐（2026-06-03 18:10）：

1. `normalizeSkill()` 保留后端返回的 `skill_file` / `skillFile`，缺省时才回退到 `<dir>/SKILL.md`。
2. `SkillMarkdownPreview` 对 `app://`、`agent://` 与 `SKILL.md` 链接做 citation 分类，传出 link target 和 label。
3. `openSkillCitation()` 支持通过精确 `skill_file`、派生 `dir/SKILL.md` 或技能名称/title/id 匹配目标技能，然后复用 `openEditSkill()` 打开目标 `SKILL.md` 与文件树。
4. conversation citation link 显示明确提示，不调用 `skills/local/read`，避免把会话链接误判成技能路径。
5. 新增测试覆盖从当前技能 preview 点击 `[Docs Skill](/repo/app/.agent/skills/docs/SKILL.md)` 后调用 `readSkill({ cwd, path })` 与 `listSkillFiles({ cwd, dir })` 打开目标技能。

本轮继续补齐（2026-06-03 18:55）：

1. 新增页面级测试覆盖新建个人技能：选择“私人使用”后保存必须调用 `writeSkill({ cwd, path, content, scope: 'personal', personal_type: 'user' })`。
2. 该测试确认项目级新建技能与个人技能新建的后端路径分离：项目共享走 `skills/create`，个人技能仍走 `skills/local/write`，与旧 Vue `useSkillEditor` 的 personal target payload 语义一致。

当前剩余：

1. 旧 Vue 的全部视觉排版和每条 guide/help 文案未做像素级一比一复刻。
2. 后续如果新增 resolution kind/action，仍需要按后端返回补充对应文案测试。

建议优先级：已完成主迁移，剩余为 P3 视觉/文案一致性。

### 6.2 Settings 页：Provider/UI Log、继承默认值与 Provider 规范化已补

新 React 已实现：

- About。
- Turn Tracker：`stallThresholdSec`。
- Context Usage Alert。
- Provider 基础设置。
- Summary / ApprovalPolicy。
- LSP Prompt read/write/reset/copy/show injected。
- Builtin tools read/write。
- UI Log 显示与日志级别切换。
- UI Log 后端刷新：点击“刷新日志”调用 `backendApi.listDashboardLogs({ limit: 14 })`，由 `dashboard/logs` RPC 渲染返回日志。
- Provider model/effort 下拉选项。
- Provider personality 设置。
- readOnly restricted readable roots 保存。
- workspaceWrite writable roots 绝对路径校验。
- Provider preference scoped/global fallback：先读 cwd scope，缺省时读 global。
- Provider tombstone：`{ cleared: true }` 会阻断 global fallback，并回到默认值。
- Provider model/effort 继承默认值保护：未显式配置且本次未触碰时不写入 `settings.provider.<provider>.model/effort`，避免把 inherited default 固化成 override。
- Claude model canonicalize：`claude-opus-4-7` 等长 slug 会折回 `opus` / `opus[1m]` 等旧 UI 下拉短值。
- Claude effort 规范化：非 Opus 模型不显示也不保存 `max`，保存时会落到 `high`。
- Provider Summary / ApprovalPolicy 按当前 active provider 读取和保存：Claude 页面不再误读/误写 `settings.provider.codex.*`。
- Provider Summary / ApprovalPolicy 也按旧 Vue 的 scoped/global preference 语义读取：cwd scope 缺省时回退 global，遇到 `{ cleared: true }` tombstone 时不回退。
- Active Provider 下拉选择后立即通过 `ui/preferences/set` 写入 `settings.provider.active`，并重新加载目标 provider 的 scoped/global preferences。
- Active Provider 切换时有本地请求序号保护，旧的 `ui/preferences/get` 加载结果不会覆盖用户刚切换成功的 provider。
- Provider Properties 卡片也有本地请求序号保护，旧 Summary/ApprovalPolicy 请求不会覆盖切换后的目标 provider 值。
- `settings.provider.active` 如果返回非法 provider 值会 fail-fast，页面显示加载错误，不再静默回退到 Codex。
- 旧 Vue JSON-string sandbox preference 可被新 UI 解析为受限/工作区/全访问 sandbox 表单；非法 JSON 或非法 payload 会显示“加载 Sandbox 失败”。
- 技能关联文件保存体验补齐：编辑 `SKILL.md` 关联文件时按钮显示“保存文件”，保存成功后提示具体文件名，RPC 仍走 `skills/local/write` 的目标文件路径。

本轮补齐：

1. `UILogCard` 的刷新按钮已绑定 `onClick`，通过 `backendApi.listDashboardLogs({ limit: 14 })` 拉取日志。
2. `ProviderSettingsForm` 中 Provider Model、Provider Effort、Personality 改为下拉框，并保留当前值追加到 options。
3. `settings.provider.<provider>.personality` 已纳入读取和保存。
4. `readOnly` sandbox 支持 `fullAccess` / `restricted`；restricted 保存 `{ type: 'readOnly', access: { type: 'restricted', readableRoots, includePlatformDefaults: true } }`。
5. `workspaceWrite` 保存前校验 writable roots 必须非空且每项都是绝对路径，失败时阻断写入并显示 alert。
6. `frontend-app/src/shared/api/backendApi.js` 已集中封装 `config/lspPromptHint/read`、`config/lspPromptHint/write`、`config/builtinTools/read`、`config/builtinTools/write`、`dashboard/logs`，并通过 payload 测试锁住 `internal/module/uistate` 与 `internal/module/dashboard` RPC 名称。
7. Provider model/effort 保存沿用旧 Vue 的 explicit/touched 语义：只有已有显式 preference 或用户本次修改后才写入，默认继承状态保持不落库。

本轮继续补齐（2026-06-03 17:25）：

1. `ProviderPropertiesCard` 的 Summary / ApprovalPolicy 已跟随 `runtime.form.activeProvider`，通过 `ui/preferences/get/set` 读写 `settings.provider.<provider>.summary` 与 `settings.provider.<provider>.approvalPolicy`。
2. 新增测试覆盖 Claude active provider 场景，确认不会再写入 `settings.provider.codex.summary` / `settings.provider.codex.approvalPolicy`。
3. `SkillEditorModal` 对关联文件显示“保存文件”，保存成功提示 `文件已保存：<filename>`；`skills/local/write` payload 保持 `{ cwd, path, content, scope, personal_type }`，与 `internal/module/skill/rpc.go` 的 `skills/local/write` 后端接口一致。
4. 新增测试覆盖 markdown preview 打开关联文件后编辑保存，确认保存目标是关联文件路径而不是主 `SKILL.md`。

本轮继续补齐（2026-06-03 17:50）：

1. 设置页不再渲染旧内部调试字段 `Model Provider`，并停止读写 `settings.provider.codex.codexModelProvider`。
2. 清空 `Codex Home` 或 `Instance Key` 时保存 `{ cleared: true }` tombstone，避免 scoped preference 被 global fallback 重新填回。
3. Codex provider 在继承默认 model/effort 且用户未触碰时，不写入 `settings.provider.codex.model` / `settings.provider.codex.effort`。
4. `frontend-app/src/pages/settings/SettingsPage.test.jsx` 新增页面级测试，覆盖隐藏内部 Codex model provider、identity tombstone、model/effort 不落库。
5. `frontend-app/src/pages/settings/SettingsPage.test.jsx` 新增 builtin tools 页面级测试，覆盖 `readBuiltinTools({ cwd })` 分组渲染与 `writeBuiltinTool({ cwd, id, enabled })` 切换。
6. 对接的后端 RPC 仍来自 `internal/module/uistate/rpc.go` 与 `internal/module/uistate/config_rpc.go`：`ui/preferences/get`、`ui/preferences/set`、`config/builtinTools/read`、`config/builtinTools/write`。

本轮继续补齐（2026-06-03 18:10）：

1. `providerNameFromPreference()` 对 `settings.provider.active` 做严格校验，非法 provider 会阻断加载并显示错误。
2. `sandboxPreferenceFromRaw()` 支持旧 Vue 存下的 JSON-string sandbox preference，并规范化 `workspace-write`、`read-only`、`danger-full-access`。
3. `Active Provider` 下拉从本地表单修改改为调用 `changeActiveProviderPreference()`，立即写入 `settings.provider.active`，再读取目标 provider 的运行时 preferences。
4. “保存 Provider 设置”不再重复写 active provider，避免 active provider 与 provider 具体配置保存语义混在一起。
5. 新增测试覆盖 legacy JSON sandbox preference、非法 active provider fail-fast、active provider 立即 `ui/preferences/set`。

本轮继续补齐（2026-06-03 18:42）：

1. `frontend-app/src/pages/settings/SettingsPage.jsx` 给 runtime preference 加载和 active provider 切换加本地请求序号；过期的加载成功/失败回调都会被忽略，避免旧 `settings.provider.active=codex` 在用户切到 Claude 后回写表单。
2. `frontend-app/src/pages/settings/SettingsPage.test.jsx` 新增竞态测试，覆盖 stale active provider promise 后完成时仍保持 `Active Provider=claude`。

本轮继续补齐（2026-06-03 18:55）：

1. `useProviderPreferences()` 读取 Summary / ApprovalPolicy 时复用 `readScopedPreference()`，补齐 cwd scope 优先、global fallback 和 tombstone 阻断语义。
2. `useProviderPreferences()` 增加请求序号；切换 active provider 后，旧 Summary/ApprovalPolicy 请求完成也不会覆盖新 provider 的 Properties 卡片。
3. `frontend-app/src/pages/settings/SettingsPage.test.jsx` 新增两条页面级回归：scoped/global fallback + tombstone，以及 stale provider properties load 防覆盖。

当前剩余：

1. 新 UI 仍把 Summary / ApprovalPolicy 放在独立 Properties card，旧 Provider 设置是同一卡片内组合；这是信息架构差异，不是后端功能缺失。
2. Codex identity 的默认值与旧 Vue 常量不完全一致，当前按新 UI 已有默认值保留，未在本轮改动。

建议优先级：已完成主迁移，剩余为 P3 布局与默认值产品决策。

### 6.3 Observability 页是新 UI 新增能力，route/bootstrap 已补

新 UI 新增：

- `ObservabilityPage.jsx` 支持 recent list、trace expansion、复制 Trace ID。
- `backendApi.js` 封装 `observability/recent/list`、`observability/trace/get` 等 RPC。
- `frontend-app/src/App.jsx` 已包含 `observability` route。
- `frontend-app/src/entities/client/model/useClientStore.js` 的 `APP_PAGE_IDS` 已包含 `observability`。

剩余注意事项：

- 这是新 UI 自身增强，不是旧 Vue 迁移缺口。
- 后续应继续以 trace 数据规模、慢查询/错误列表 UX 做回归，而不是作为 P0 迁移项处理。

建议优先级：已处理路由/恢复基础项，剩余为 P2 体验验证。

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

### P0：业务入口缺失已处理

1. 任务页：
   - task ack list 已补。
   - task trace list 已补。
   - cron job panel 已补。
   - `cronjob/*` API facade 与页面测试已补。

2. 命令卡页：
   - dashboard command cards 列表已补。
   - command template 发送到当前/新 chat 已补。
   - 是否仍需要专门 backend command execution RPC 仍是产品决策项；当前实现按旧 UI 的 chat 启动链路恢复。

### P1：主流程高级交互已处理

1. Chat：
   - fork draft 卡片、source thread 继承、shared-file fork 触发点已补。
   - `ui/code/locate/open/save` 基础动作已补；PathChoiceModal 多候选基础选择已补。
   - 旧 DiffPanel 的 markdown/image preview 与 dirty close guard 已补；runtime file actions 已接 `ui/code/*`。
   - Chat cmd mode/card overview 工作区已补 MVP，剩余旧视觉和概览细分状态归入 P2/P3。
   - timeline file-ref、task-stub、automation-update、code-comment、agent thread link 已补主链路，剩余旧 citation 样式/边界测试归入 P2/P3。

2. Prompt：
   - dry-run 面板已补。
   - SectionsEditor 已补为 `PromptSectionsPanel`。
   - `match_when` advanced/debug editor 已补。
   - `dashboard/prompts` readonly fallback 已补。
   - 复制 prompt 内容已补。

3. Workflow：
   - topology panel。
   - shared files panel。
   - final output file open/read panel。

### P2：补一致性和可维护性

1. Skills import summary draft、重复导入、同名冲突、失败文案等边界已补。
2. Skills resolution preview diff/hash/manual steps、旧 guide/help、path label、preview markdown link 子文件打开、dashboard + `skills/local/*` 页面级测试已补。
3. Settings Provider personality、model/effort options、model/effort inherited default、readOnly restricted readable roots、writable roots fail-fast validation、Claude canonicalize/max effort、scoped/global fallback、tombstone、active provider 与 Provider properties 过期加载保护、隐藏内部 Codex model provider、Codex identity tombstone 已补。
4. Settings UI Log refresh button onClick 已补，调用 `dashboard/logs`；builtin tools 页面级读写测试已补。
5. Workflow node status bridge 自动刷新和 malformed payload fail-fast 已补；旧面板视觉细节仍归入 P3 深度等价。
6. Observability 数据量与错误/慢查询体验回归。

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
- `frontend-app/src/entities/client/model/threadFork.js`
- `frontend-app/src/shared/api/backendApi.js`
- `frontend-app/src/shared/api/wailsBridge.js`
- `frontend-app/src/pages/chat/ChatPage.jsx`
- `frontend-app/src/pages/tasks/TasksPage.jsx`
- `frontend-app/src/pages/commands/CommandsPage.jsx`
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

动态发现（历史记录，部分已被后续实现修正）：

- 早期 `/observability` 深链有问题：点击侧栏“链路追踪”后 URL 停在 `/` 但内容切到观测页；直接访问 `http://127.0.0.1:5175/observability` 会回到 Chat 首屏。当前代码已在 `App.jsx` 与 `useClientStore.js` 注册 `observability`，该问题按源码状态已修正，仍建议动态复测刷新/书签路径。
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
- Chrome 插件打开同一地址也正常渲染，`vite-error-overlay=false`，正文包含 `Super Agent`、`Super-Dolphin`、`Chat`、`提示词`、`自动化`、`技能`、`记忆中心`、`链路追踪`、`共享文件`、`设置`。后续当前代码新增的 `任务`、`命令` nav 已在 11 节复测。

结论：上一轮旧 bundle 的 Computer Use 证据无效；按当前 `/Users/ai/Desktop/Super-Dolphin` 项目复测，最终当前状态的新 UI 可以正常渲染 Chat。Computer Use 仍无法通过 `.app` path/bundle 精准绑定 `go run` 裸 Wails 进程，只能通过进程 PID/CWD/端口证明当前窗口来源。

## 11. 本轮实现更新（2026-06-03）

已补能力：

1. `frontend-app/src/pages/tasks/TasksPage.jsx`：新增任务页、任务工单/执行追踪/定时任务 tabs、cron job CRUD/启停/立即运行/历史。
2. `frontend-app/src/pages/commands/CommandsPage.jsx`：新增命令卡页，读取 `commandCards` 并发送到 chat。
3. `frontend-app/src/entities/client/model/useClientStore.js` 与 `frontend-app/src/entities/client/model/threadFork.js`：新增 fork draft MVP、source thread、shared file 选择和继承会话提交链路。
4. `frontend-app/src/pages/chat/ChatPage.jsx`：runtime diff 文件组新增定位/打开按钮，预览弹窗可编辑并通过 `ui/code/save` 保存。
5. `frontend-app/src/shared/api/backendApi.js`：新增 `cronjob/*` 与 `ui/code/locate/open/save` facade。
6. `frontend-app/src/App.jsx` 与 `frontend-app/src/entities/client/model/useClientStore.js`：新增 `tasks`、`commands`、`observability` 页面注册和路由恢复。
7. `frontend-app/src/pages/skills/SkillsPage.jsx`：新增导入摘要草稿面板、采用并编辑/编辑/跳过/收起动作、冲突预览 diff/hash 渲染、同名冲突手动处理步骤。
8. `frontend-app/src/pages/settings/SettingsPage.jsx`：新增 Provider model/effort/personality 下拉，readOnly restricted readable roots，workspaceWrite 绝对路径校验，UI Log `dashboard/logs` 刷新。

已补测试：

- `frontend-app/src/App.test.jsx`：覆盖 tasks/cron 页面、commands 页面、fork draft、runtime diff locate/open/save。
- `frontend-app/src/App.test.jsx`：覆盖 Skills 导入摘要草稿、采用并编辑、跳过，以及 resolution diff/hash/manual steps。
- `frontend-app/src/SettingsPage.test.jsx`：覆盖 Provider model/effort/personality/readOnly restricted 保存、writable roots 校验、UI Log 后端刷新。
- `frontend-app/src/entities/client/model/useClientStore.test.js`：覆盖 fork 和 command card store 链路。
- `frontend-app/src/shared/api/backendApi.test.js`：覆盖 `cronjob/*` 与 `ui/code/*` RPC payload。

本轮验证（2026-06-03 15:37-15:58，当前工作区 fresh run）：

- `cd frontend-app && npm run lint`：通过。
- `cd frontend-app && npm test -- src/shared/api/backendApi.test.js src/App.test.jsx src/entities/client/model/useClientStore.test.js`：通过，3 files / 305 tests。
- `cd frontend-app && npm test -- src/SettingsPage.test.jsx src/App.test.jsx`：通过，2 files / 191 tests；本轮新增 Skills/Settings 回归覆盖在此命令内。
- `cd frontend-app && npm test`：通过，19 files / 407 tests。
- `cd frontend-app && npm run build`：通过；仅 Vite chunk size warning。
- `git diff --check`：通过。
- Playwright CLI 打开 `http://127.0.0.1:5175/`：首屏来自当前项目，nav 可见 `任务`、`命令`、`链路追踪`。
- Playwright CLI 打开 `/skills`：技能管理页渲染正常，可见 `批量导入技能目录`、`新建技能`、筛选按钮和 22 个项目共享技能。
- Playwright CLI 打开 `/settings`：设置页渲染正常，可见 Provider Model/Effort/Personality 下拉、Writable Roots、UI LOG；点击 `刷新日志` 后日志列表刷新到新的后端 RPC 时间。
- Playwright CLI 打开 `/tasks`：任务页渲染，`任务工单`、`执行追踪`、`定时任务` 三个 tab 可见；点击 `定时任务` 后可见 `新建定时任务`、`刷新` 和空态。

- Playwright CLI 打开 `/commands`：命令卡页渲染，可见 `命令卡`、`刷新` 与空态。
- Playwright CLI 打开 `/observability`：深链直接渲染 `链路追踪` 页面，不再回落 Chat，可见 Trace ID / Thread ID / Agent ID / Method / Limit 筛选项。
- Playwright console：仅 React DevTools info，无 warning/error。
- Chrome 插件只读打开 `/commands`：可见 `Super Agent`、`任务`、`链路追踪`、`命令卡`、`暂无命令卡`，Chrome warning/error console 为 0。
- Computer Use：当前只暴露按键能力；按 `Super Agent` 标题绑定返回 `Invalid app: Super Agent`。按 `agent-terminal` 会命中旧 `/Users/ai/Desktop/go-agent-v2/bin/agent-terminal.app`，该证据继续判定为无效，不计入当前项目验证。

### 11.1 Skills/Settings 补充迁移（2026-06-03 16:09-16:16）

本轮继续补齐：

1. `frontend-app/src/pages/skills/SkillsPage.jsx`：
   - `skills/local/importDir` 返回 `failures` 时展示重复导入与失败来源。
   - 导入后读取技能遇到 same-name conflict 时生成 `conflict` 草稿，提示用户到冲突面板选择版本。
   - 冲突卡片显示旧 UI 等价 guide 文案。
   - resolution preview 显示 view-action intro、外部版本/本项目版本/保存位置等 path label。
2. `frontend-app/src/pages/settings/SettingsPage.jsx`：
   - Provider preference 读取改为 cwd scope 优先，缺省时 fallback 到 global。
   - `{ cleared: true }` tombstone 会阻断 global fallback。
   - Claude long slug 规范化为下拉短值，例如 `claude-opus-4-7` -> `opus`。
   - Claude 非 Opus 模型不显示/不保存 `max` effort，保存时规范化为 `high`。
   - Claude active provider 下隐藏 Codex identity 输入，保存时不写 Codex identity preferences。

本轮新增/更新测试：

- `frontend-app/src/App.test.jsx`：覆盖 Skills duplicate import failures、same-name conflict draft、resolution guide 与 preview intro/path label。
- `frontend-app/src/SettingsPage.test.jsx`：覆盖 scoped/global provider fallback、tombstone、Claude canonicalize、非 Opus `max` effort 限制和保存 payload。

本轮验证（当前工作区 fresh run）：

- `cd frontend-app && npm test -- src/SettingsPage.test.jsx src/App.test.jsx`：通过，2 files / 195 tests。
- `cd frontend-app && npm run lint`：通过。
- `cd frontend-app && npm test`：通过，19 files / 411 tests。
- `cd frontend-app && npm run build`：通过；仅 Vite chunk size warning。
- `git diff --check`：通过。
- 当前服务确认：`agent-terminal` PID 17158 的 CWD 为 `/Users/ai/Desktop/Super-Dolphin`；Vite PID 17236 来自 `/Users/ai/Desktop/Super-Dolphin/frontend-app/node_modules/.bin/vite`；后端 `127.0.0.1:4512/metrics` 和 Vite `127.0.0.1:5175` 均可访问。
- Playwright CLI 打开 `/skills`：技能管理页渲染正常，可见 22 个项目共享技能、`批量导入技能目录`、`新建技能`、编辑/删除入口。
- Playwright CLI 打开 `/settings`：设置页渲染正常，可见 Provider Model/Effort/Personality、Codex identity、Prompt、Builtin Tools、UI LOG；点击 `刷新日志` 后日志列表刷新到 16:16 的后端 RPC 记录。
- Playwright console：仅 React DevTools info，无 warning/error。

### 11.2 Skills/Settings facade 与列表反馈补充（2026-06-03 16:39-16:50）

本轮继续补齐：

1. `frontend-app/src/shared/api/backendApi.js`：
   - 新增并导出 settings config facade：`readLspPromptHint()`、`writeLspPromptHint()`、`readBuiltinTools()`、`writeBuiltinTool()`、`listDashboardLogs()`。
   - RPC method 与后端 `internal/module/uistate/config_rpc.go`、`internal/module/dashboard/rpc.go` 保持一致：`config/lspPromptHint/read`、`config/lspPromptHint/write`、`config/builtinTools/read`、`config/builtinTools/write`、`dashboard/logs`。
   - payload 校验保持 fail-fast：缺 `cwd`、缺 `hint`、缺 `id`、`enabled` 非 boolean、`limit <= 0` 均直接抛错。
2. `frontend-app/src/pages/settings/SettingsPage.jsx`：
   - Prompt、Builtin Tools、UI Log 已从裸 RPC/浏览器剪贴板调用迁移到 `backendApi` 门面和 `copyTextToClipboard()`。
3. `frontend-app/src/pages/skills/SkillsPage.jsx`：
   - 补齐旧 UI 的技能筛选计数。
   - 搜索或范围过滤无结果时显示“没有匹配技能”说明，不再误用全局“暂无技能”空态。
4. `frontend-app/src/SettingsPage.test.jsx`：
   - 清理测试里旧 `navigator.clipboard` 与 `callBackend` mock 残留，测试口径改为验证新门面。

本轮新增/更新测试：

- `frontend-app/src/shared/api/backendApi.test.js`：覆盖 settings config facade RPC 名称和 fail-fast payload。
- `frontend-app/src/App.test.jsx`：覆盖 Skills 筛选计数、私人使用过滤、搜索无结果空态。
- `frontend-app/src/SettingsPage.test.jsx`：继续覆盖 LSP Prompt、Builtin Tools、UI Log 后端刷新，且不再依赖裸 `callBackend` mock。

本轮验证（当前工作区 fresh run）：

- `cd frontend-app && npm test -- src/App.test.jsx -t "shows skills filter counts"`：先失败，缺少 `共 2 个技能`；实现后通过，1 test。
- `cd frontend-app && npm test -- src/SettingsPage.test.jsx src/shared/api/backendApi.test.js`：通过，2 files / 37 tests。
- `cd frontend-app && npm test -- src/App.test.jsx -t "skill"`：通过，1 file / 16 tests。
- `cd frontend-app && npm test -- src/App.test.jsx -t "renders workflow topology"`：通过，1 test；同步处理 stop-gate 中暴露的 Workflow 拓扑测试异步等待与多重文件名匹配问题。
- `cd frontend-app && npm run lint`：通过。
- `cd frontend-app && npm test`：通过，19 files / 419 tests。
- `cd frontend-app && npm run build`：通过；仅 Vite chunk size warning。
- `git diff --check`：通过。

本轮动态验证：

- 当前服务复用 `/Users/ai/Desktop/Super-Dolphin/run-new-ui-desktop.sh` 启动进程；后端 `agent-terminal` PID 17158 CWD 为 `/Users/ai/Desktop/Super-Dolphin`，Vite PID 17236 CWD 为 `/Users/ai/Desktop/Super-Dolphin/frontend-app`。
- `127.0.0.1:4512/metrics` 与 `127.0.0.1:5175` 均可访问。
- Playwright CLI 打开 `/skills`：页面显示 `技能管理`、22 个项目共享技能、`共 22 个技能`；输入不存在关键词后显示 `没有匹配技能` 与 `当前没有匹配技能，共 22 个`。
- Playwright CLI 打开 `/settings`：页面显示 Provider、Prompt、Builtin Tools、UI LOG；点击 `刷新日志` 后 UI LOG 出现新的 16:51:28 RPC 记录。
- Playwright console：仅 React DevTools info，无 warning/error。
- Chrome 插件打开 `/settings` 只读检查：`hasSettings/hasProvider/hasPrompt/hasBuiltinTools/hasUiLog` 均为 true，warn/error console 为 0。
- Computer Use 只读应用列表仍显示 `agent-terminal` 指向 `/Users/ai/Desktop/go-agent-v2/bin/agent-terminal.app`；该旧 app 未用于本轮当前项目验证。

### 11.3 Prompt 高级能力补齐确认（2026-06-03 16:55）

当前工作区已补齐 Prompt P1 主迁移：

1. `frontend-app/src/features/prompts/PromptPageView.jsx`：
   - `fetchPromptAssetsSurface()` 先调用 `listPromptAssets({ cwd })`，遇到 method-not-found / not-registered / unimplemented 等旧后端兼容错误时，切到 `getDashboardPrompts({ cwd })` 只读 fallback。
   - card “复制”按钮通过 `getPrompt({ cwd, id })` 读取完整内容，再调用 `copyTextToClipboard()`。
   - 编辑器保留 `match_when JSON` 高级调试入口，保存前用 JSON object 校验阻断非法输入。
   - `PromptSectionsPanel` 接入 `listPromptSections()`、`writePromptSection()`、`deletePromptSection()`。
   - 创建向导的“试问验证”调用 `dryRunPromptIntent()`，页面只展示可读匹配结论，不暴露后端 reasons 内部细节。
2. `frontend-app/src/shared/api/backendApi.js`：
   - 已导出 `getDashboardPrompts()`、`dryRunPromptIntent()`、`listPromptSections()`、`writePromptSection()`、`deletePromptSection()` 和 `copyTextToClipboard()`。
   - `writePrompt()` payload 支持 `match_when` / `matchWhen`。

本轮 focused 验证：

- `cd frontend-app && npm test -- src/features/prompts/PromptPageView.test.jsx src/shared/api/backendApi.test.js`：通过，2 files / 34 tests。

剩余主差距：

1. 旧 DiffPanel 的 citation/file-ref click 主链路已在后续 11.6 补齐；automation-update 主链路与 shared-file fork 触发点已在 11.8 补齐，剩余为 directive 样式和边界测试深度等价。
2. Chat cmd mode/card overview 工作区已在后续 11.6 补 MVP，剩余为旧 UI 视觉和细分状态等价。
3. Workflow topology/shared-files/final-output 基础诊断面板已补；旧 UI 的事件深度和面板细节仍未完整等价。

### 11.4 Skills/Settings 与 PathChoice 补充确认（2026-06-03 17:05）

本轮继续补齐：

1. `frontend-app/src/pages/settings/SettingsPage.jsx`：
   - Provider model/effort 保留旧 Vue 的 explicit/touched 语义：默认继承值只显示，不在未触碰时写入 preference。
   - `workspaceWrite` 的 writable roots 为空时 fail-fast，提示 `请至少填写一个绝对路径`，不再保存空 root 集合。
2. `frontend-app/src/pages/skills/SkillsPage.jsx`：
   - 技能正文 preview 支持 `[label](path)` 内联链接。
   - 链接目标只允许命中当前技能 `skills/local/listFiles` 返回的文件；相对路径按当前预览文件所在目录解析。
   - 点击链接后通过既有 `skills/local/read` 打开子文件内容，恢复旧 UI 的 preview link-to-subfile 工作流。
3. `frontend-app/src/pages/chat/ChatPage.jsx`：
   - `ui/code/locate` 返回多个 `paths` / `matches` 时打开基础路径选择弹窗。
   - 选择候选后调用 `ui/code/open` 进入已有文件预览/保存链路。
   - 这补齐的是旧 `PathChoiceModal` 的基础路径选择能力；当时 markdown/image preview、dirty close guard、citation/file-ref 深度交互尚未完成，后续 11.5/11.6 已继续补主链路，当前剩余转入 P2/P3 深度等价。

本轮 focused 验证：

- `cd frontend-app && npm test -- src/SettingsPage.test.jsx -t "keeps default provider model|blocks workspaceWrite"`：通过，2 tests。
- `cd frontend-app && npm test -- src/SettingsPage.test.jsx`：通过，13 tests。
- `cd frontend-app && npm test -- src/App.test.jsx -t "opens a linked skill subfile"`：通过，1 test。
- `cd frontend-app && npm test -- src/App.test.jsx -t "opens a path choice dialog"`：通过，1 test。
- `cd frontend-app && npm test -- src/App.test.jsx -t "runtime diff|path choice|locates, previews and saves"`：通过，2 tests。
- `cd frontend-app && npm test -- src/SettingsPage.test.jsx src/App.test.jsx -t "SettingsPage|linked skill subfile|opens a path choice dialog|locates, previews and saves"`：通过，2 files / 16 tests，185 skipped。

本轮收尾验证：

- `cd frontend-app && npm run lint`：通过。
- `cd frontend-app && npm test`：通过，19 files / 423 tests；输出中仍有既有 React `act(...)` warning 和测试刻意模拟的 bridge failure stderr，但 exit code 为 0。
- `cd frontend-app && npm run build`：通过；仅 Vite chunk size warning。
- `git diff --check`：通过。

本轮动态验证：

- 当前服务确认仍来自 `/Users/ai/Desktop/Super-Dolphin/run-new-ui-desktop.sh`：`bash ./run-new-ui-desktop.sh`、`go run ./cmd/agent-terminal`、`frontend-app/node_modules/.bin/vite`；后端监听 `127.0.0.1:4512`，Vite 监听 `127.0.0.1:5175`。
- Playwright CLI 打开 `http://127.0.0.1:5175/skills`：页面显示 `技能管理`、`批量导入技能目录`、`新建技能`、`项目共享 22`、`共 22 个技能`。
- Playwright CLI 打开 `http://127.0.0.1:5175/settings`：页面显示 `Provider Model`、`Provider Effort`、`Personality`、`Writable Roots`、`UI LOG`。
- Playwright CLI 点击 `刷新日志`：UI LOG 追加新的 `api.rpc.start` / `api.rpc.done` / `bridge.call.*` 记录，确认 `dashboard/logs` 刷新链路可用。
- Playwright console：仅 React DevTools info，无 warning/error。
- Chrome/Computer Use 备注：本轮未用 `agent-terminal` 名称做 Computer Use 绑定，避免命中旧 `/Users/ai/Desktop/go-agent-v2/bin/agent-terminal.app`；当前可用 Computer Use 只暴露按键/点击能力，无法可靠证明 `go run` 裸 Wails 进程窗口来源。

### 11.5 Chat DiffPanel markdown/image preview 补充确认（2026-06-03 17:18）

本轮继续补齐：

1. `frontend-app/src/pages/chat/ChatPage.jsx`：
   - `ui/code/open` 结果现在会消费 `image`、`mediaType`、`previewURL`、`thumbnailURL`、`previewKind`、`language`、`snippet`、line range 等后端字段。
   - markdown 文件默认显示渲染预览，点击 `编辑预览` 后进入 textarea；保存后回到 markdown 预览。
   - image 文件显示只读图片预览、媒体类型和大小，不再落到空 textarea。
   - dirty close guard 已补：编辑后直接点关闭或 Escape 会阻断关闭并提示 `请先保存或放弃预览更改`。
2. `frontend-app/src/styles.css`：
   - 新增 markdown/text/image code preview 的 modal 样式与元信息样式。
3. `frontend-app/src/App.test.jsx`：
   - 新增 markdown preview + dirty close guard 回归。
   - 新增 image preview 回归。

本轮 focused 验证：

- `cd frontend-app && npm test -- src/App.test.jsx -t "markdown runtime diff previews|image runtime diff previews"`：先失败，现有弹窗只渲染 textarea；实现后通过，2 tests。
- `cd frontend-app && npm test -- src/App.test.jsx -t "runtime diff|path choice|locates, previews and saves|markdown runtime diff previews|image runtime diff previews"`：通过，4 tests。
- `cd frontend-app && npm run lint`：通过。
- `cd frontend-app && npm test`：通过，19 files / 425 tests；输出中仍有既有 React `act(...)` warning 和测试刻意模拟的 bridge failure stderr，但 exit code 为 0。
- `cd frontend-app && npm run build`：通过；仅 Vite chunk size warning。
- `git diff --check`：通过。

### 11.6 Skills/Settings 与 Chat citation 补充确认（2026-06-03 17:50-17:55）

本轮继续补齐：

1. `frontend-app/src/pages/chat/ChatPage.jsx`：
   - timeline file citation 的可访问名称改为完整文件路径，保证 `:codex-file-citation[]{path="src/main.go" ...}` 能按旧 UI 语义触发文件引用打开链路。
   - Chat 已接入 `ChatModeToolbar`、`CommandWorkspace`、timeline `onFileRef/onCitation` actions，审计状态从“cmd/citation 缺失”更新为“P1 主链路已补，深度交互转入 P2/P3”。
2. `frontend-app/src/pages/skills/SkillsPage.test.jsx`：
   - 新增页面级迁移测试，覆盖 dashboard skills、`skills/resolution_list`、`skills/local/read`、`skills/local/listFiles`、`skills/local/write`。
3. `frontend-app/src/pages/settings/SettingsPage.test.jsx`：
   - 新增页面级迁移测试，覆盖隐藏内部 Codex `Model Provider`、Codex Home/Instance Key 清空 tombstone、继承默认 model/effort 不落库、builtin tools `config/builtinTools/read/write`。
4. 本文档同步更新 Skills、Settings、Chat 迁移状态与 `internal/module/skill`、`internal/module/uistate` 后端 RPC 对接证据。

本轮验证：

- `cd frontend-app && npm test -- src/pages/skills/SkillsPage.test.jsx src/pages/settings/SettingsPage.test.jsx src/pages/chat/ChatPage.test.jsx`：通过，3 files / 15 tests。
- `cd frontend-app && npm run lint`：通过。
- `cd frontend-app && npm test`：通过，19 files / 431 tests；存在测试用例自身的 React `act(...)` warning 和预期 bridge failure 日志，最终 exit code 为 0。
- `cd frontend-app && npm run build`：通过；仅 Vite chunk size warning。
- `git diff --check`：通过。

本轮动态验证：

- 当前监听进程来自本项目脚本：`bash ./run-new-ui-desktop.sh`、`go run ./cmd/agent-terminal`、`node /Users/ai/Desktop/Super-Dolphin/frontend-app/node_modules/.bin/vite`。
- 当前端口：Vite `127.0.0.1:5175`，后端/bridge `127.0.0.1:4512`；`http://127.0.0.1:4512/metrics` 可返回 metrics。
- Playwright CLI 打开 `http://127.0.0.1:5175/skills`：技能管理页加载 22 个项目共享技能；点击“编辑详情”可打开“编辑技能”弹窗，并显示技能正文预览与“保存技能”入口。
- Playwright CLI 打开 `http://127.0.0.1:5175/settings`：设置页显示当前项目 `/Users/ai/Desktop/Super-Dolphin`，Provider Model/Effort/Personality、Codex Home、Instance Key、Writable Roots、builtin tools 分组、UI LOG 均可见；页面没有旧内部 `Model Provider` 字段。
- Playwright CLI 打开 `http://127.0.0.1:5175/`：Chat 首屏、nav、项目名 `Super-Dolphin` 可正常渲染；该 profile 无 active thread，因此命令工作区动态按钮未出现，相关链路以组件测试覆盖。
- Playwright console 检查：本轮 `/skills`、`/settings`、`/` 三次打开没有 `error/warn` 级 console 记录。
- Chrome + Computer Use 复核：Chrome 真实窗口 URL 为 `127.0.0.1:5175/settings`，页面标题 `Super Agent Frontend App`，设置页当前项目显示 `/Users/ai/Desktop/Super-Dolphin`；未绑定旧 `/Users/ai/Desktop/go-agent-v2/bin/agent-terminal.app`。

### 11.7 Skills/Settings 迁移补强（2026-06-03 18:10）

本轮继续补齐：

1. `frontend-app/src/pages/skills/SkillsPage.jsx`：
   - `normalizeSkill()` 保留后端 `skill_file` / `skillFile`，用于精确定位技能主文件。
   - 技能正文 preview 的 citation-like markdown link 可区分 `app://` skill citation、`agent://` conversation citation 和 `SKILL.md` 文件引用。
   - skill citation 通过 `skill_file`、派生 `dir/SKILL.md`、技能名称/title/id 匹配目标技能，并复用 `skills/local/read` + `skills/local/listFiles` 打开目标技能。
   - conversation citation 显示“暂不支持会话跳转”提示，不误调用技能后端 RPC。
2. `frontend-app/src/pages/settings/SettingsPage.jsx`：
   - Active Provider 下拉选择后立即写入 `settings.provider.active`，再重新加载目标 provider 的 scoped/global preferences。
   - `settings.provider.active` 非法值会 fail-fast，页面显示加载错误，不再静默回退到 Codex。
   - Sandbox preference 支持旧 Vue JSON-string payload，并规范化旧命名 `workspace-write`、`read-only`、`danger-full-access`。
   - “保存 Provider 设置”只保存当前 provider 的运行时字段，不再重复写 active provider。

本轮新增/更新测试：

- `frontend-app/src/pages/skills/SkillsPage.test.jsx`：覆盖 preview 中点击 conversation citation 显示提示、点击 skill-file citation 后调用 `readSkill({ cwd, path })` 与 `listSkillFiles({ cwd, dir })` 打开目标技能。
- `frontend-app/src/pages/settings/SettingsPage.test.jsx`：覆盖 legacy JSON sandbox preference、非法 active provider fail-fast、active provider 切换立即调用 `ui/preferences/set`。

本轮 focused 验证：

- `cd frontend-app && npm test -- src/pages/skills/SkillsPage.test.jsx src/pages/settings/SettingsPage.test.jsx`：通过，2 files / 9 tests。

本轮收尾验证（当前工作区 fresh run）：

- `cd frontend-app && npm run lint`：首次发现 `sandboxPreferenceFromRaw()` 重新抛错缺少 `cause`，已补 `new Error(..., { cause })`；复跑通过。
- `cd frontend-app && npm test`：通过，19 files / 435 tests；输出中仍有既有 localStorage ExperimentalWarning、React `act(...)` warning 与测试刻意模拟的 bridge failure 日志，最终 exit code 为 0。
- `cd frontend-app && npm run build`：通过；仅 Vite chunk size warning。
- `git diff --check`：通过。

本轮动态验证：

- 当前监听进程来自本项目脚本：`bash ./run-new-ui-desktop.sh`、`go run ./cmd/agent-terminal`、`node /Users/ai/Desktop/Super-Dolphin/frontend-app/node_modules/.bin/vite`。
- 当前端口：Vite `127.0.0.1:5175`，后端/bridge `127.0.0.1:4512`；`http://127.0.0.1:4512/metrics` 可返回 metrics。
- Playwright CLI 打开 `http://127.0.0.1:5175/skills`：技能管理页加载完成，可见 `技能管理`、`批量导入技能目录`、`新建技能`、`项目共享 22`、`全部 22`、技能卡片编辑/删除入口和 `共 22 个技能`。
- Playwright CLI 打开 `http://127.0.0.1:5175/settings`：设置页加载完成，可见 `Active Provider`、`Provider Model`、`Provider Effort`、`Personality`、`Sandbox Policy`、`Writable Roots`、Builtin Tools 分组、UI LOG。
- Playwright CLI 点击 `刷新日志`：UI LOG 出现新的 `api.rpc.start` / `api.rpc.done` / `bridge.call.*` 记录，且 ABOUT 当前项目刷新为 `/Users/ai/Desktop/Super-Dolphin`。
- Computer Use 只读列应用再次确认：`agent-terminal` 仍指向旧 `/Users/ai/Desktop/go-agent-v2/bin/agent-terminal.app`，本轮未绑定该旧 app。
- Computer Use 只读抓取 Chrome 当前窗口：Google Chrome URL 为 `127.0.0.1:5175/settings`，标题 `Super Agent Frontend App`，页面可见当前项目 `/Users/ai/Desktop/Super-Dolphin` 与 Provider/Sandbox/Builtin Tools/UI LOG。

### 11.8 Shared-file fork 触发点补齐（2026-06-03 18:28）

本轮继续补齐：

1. `frontend-app/src/entities/client/model/useClientStore.js`：
   - `continueWithSharedFile(path)` 现在先检查当前 active thread 是否能解析为后端 thread。
   - 有 source thread 时，动作切到 Chat、保留 source thread、调用 `openForkDraft({ origin: 'shared-files', sharedFilePath })`，并预选该 shared file。
   - 没有 source thread 时保留原普通新草稿 fallback，避免无源会话场景被阻断。
   - `openForkDraft(options)` 支持 `sharedFilePath` / `seedSharedFilePath` seed，并在刷新 `listSharedFiles()` 后保留预选路径。
2. `frontend-app/src/App.test.jsx`：
   - 共享文件页“用此文件继续对话”页面测试从断言 composer 普通草稿，改为断言 Chat 中出现 `fork-draft-card`，source title 为 `继承自会话：后端线程`，且 `reports/final.md` checkbox 已选中。
3. 本轮同时核对旧 Vue 证据：
   - `cmd/agent-terminal/frontend/vue-app/pages/SharedFilesPage.js` 通过 `start-inherited-chat` 发出 `sharedFilePath`。
   - `cmd/agent-terminal/frontend/vue-app/pages/UnifiedChatPage.js` 消费 payload 后调用 `composer.openForkDraft({ origin: 'shared-files', sharedFilePath })`。
   - `cmd/agent-terminal/frontend/vue-app/stores/composer.js` 会把 seed path 写入 `forkDraft.sharedFilePaths`。
4. automation directive 状态修正：
   - 旧 `cmd/agent-terminal/frontend/vue-app/utils/citation-action-utils.js` 对 `automation-update` 的主链路是写入 composer，不是直接调用自动化后端 RPC。
   - 新 React `composerTextFromCitation()` 与 `handleTimelineCitationAction()` 已同样把 `automation-update` 写入 composer；因此此项从 P1 剩余移到已补主链路。

本轮 RED/GREEN 验证：

- RED：`cd frontend-app && npm test -- src/entities/client/model/useClientStore.test.js -t "shared file continuation"` 先失败，现有实现把 `activeThreadId` 清空为普通新草稿。
- GREEN：`cd frontend-app && npm test -- src/entities/client/model/useClientStore.test.js -t "shared file continuation"` 通过，1 test。
- `cd frontend-app && npm test -- src/entities/client/model/useClientStore.test.js -t "shared file"`：通过，3 tests。
- `cd frontend-app && npm test -- src/App.test.jsx -t "loads shared files from the shared-files RPC"`：通过，1 test。

本轮收尾验证：

- `cd frontend-app && npm run lint`：通过。
- `cd frontend-app && npm test`：通过，19 files / 436 tests；输出中仍有既有 localStorage ExperimentalWarning、React `act(...)` warning 与测试刻意模拟的 bridge failure 日志，最终 exit code 为 0。
- `cd frontend-app && npm run build`：通过；仅 Vite chunk size warning。
- `git diff --check`：通过。

本轮动态验证：

- 当前监听进程来自本项目脚本：`bash ./run-new-ui-desktop.sh`、`go run ./cmd/agent-terminal`、`node /Users/ai/Desktop/Super-Dolphin/frontend-app/node_modules/.bin/vite`。
- 当前端口：Vite `127.0.0.1:5175`，后端/bridge `127.0.0.1:4512`。
- Playwright CLI 打开 `http://127.0.0.1:5175/files`：共享文件页加载完成，可见 `daily-life-plan.md`、`reports/daily-life-plan.md` 和“用此文件继续对话”。
- Playwright CLI 点击“用此文件继续对话”：页面切到 Chat，出现 `继承对话草稿`，source title 为 `继承自会话：你好。`，`reports/daily-life-plan.md` checkbox 已选中。

### 11.9 Skills create 与 Settings provider 竞态补齐（2026-06-03 18:42）

本轮继续补齐：

1. `frontend-app/src/shared/api/backendApi.js`：
   - 新增 `RPC_METHODS.SKILLS_CREATE = 'skills/create'`。
   - 新增并导出 `createSkill({ cwd, name, content })` facade，payload 与 `internal/module/skill/rpc.go` / `internal/module/skill/rpc_skill_types.go` 的 `createSkillParams` 对齐。
   - facade 对 `cwd`、`name`、`content` 做 fail-fast 校验。
2. `frontend-app/src/pages/skills/SkillsPage.jsx`：
   - 新建项目共享主技能时调用 `createSkill({ cwd, name, content })`。
   - 编辑已有主技能、编辑关联文件、个人技能写入仍使用 `writeSkill({ cwd, path, content, scope, personal_type })`，保持旧 `skills/local/write` 编辑语义。
3. `frontend-app/src/pages/settings/SettingsPage.jsx`：
   - `useSettingsRuntime()` 为 preference load / active provider switch 加本地请求序号。
   - 过期的 `ui/preferences/get` 成功或失败结果不会再覆盖用户新选择的 active provider。
4. `frontend-app/src/pages/chat/ChatPage.jsx`：
   - 恢复高上下文使用率 banner，`usedPercent >= 90` 时显示 `context-usage-banner`。
   - banner 的“新建继承会话”按钮复用现有 `openForkDraft({ origin: 'context-usage' })`，补回旧 Chat 从上下文警告继续 fork draft 的入口。

本轮 RED/GREEN 验证：

- RED：`cd frontend-app && npm test -- --run src/shared/api/backendApi.test.js src/pages/skills/SkillsPage.test.jsx src/pages/settings/SettingsPage.test.jsx` 先失败，失败点为 `api.createSkill is not a function`、技能页新建未调用 `createSkill`、stale provider load 把 Claude 覆盖回 Codex。
- GREEN：同一命令复跑通过，3 files / 38 tests。
- RED：`cd frontend-app && npm test -- --run src/App.test.jsx -t "opens a fork draft from the context usage warning banner"` 先失败，当前实现没有 `context-usage-banner`。
- GREEN：同一命令复跑通过，1 test。

本轮收尾验证：

- `cd frontend-app && npm run lint`：通过。
- `cd frontend-app && npm test`：通过，19 files / 440 tests；输出中仍有既有 localStorage ExperimentalWarning、React `act(...)` warning 与测试刻意模拟的 bridge failure 日志，最终 exit code 为 0。
- `cd frontend-app && npm run build`：通过；仅 Vite chunk size warning。

本轮审计状态更新：

- Skills RPC 矩阵新增 `skills/create`，项目级新建技能不再归入 `skills/local/write`。
- Settings 章节新增 active provider 过期加载保护，后端接口仍是 `internal/module/uistate/rpc.go` 注册的 `ui/preferences/get` / `ui/preferences/set`。
- Chat 章节维持 fork draft 主链路已补状态，并恢复上下文使用率警告触发 fork draft 的入口。

### 11.10 Skills/Settings Provider properties 补强（2026-06-03 18:55）

本轮继续补齐：

1. `frontend-app/src/pages/settings/SettingsPage.jsx`：
   - `useProviderPreferences()` 读取 Summary / ApprovalPolicy 时复用 `readScopedPreference()`。
   - 补齐 cwd scoped preference 优先、scoped 缺省回退 global、`{ cleared: true }` tombstone 阻断 global fallback 的旧 Vue 等价语义。
   - Provider Properties 卡片新增请求序号保护；切换 active provider 后，旧 Summary/ApprovalPolicy 异步请求完成也不会覆盖新 provider 的 Properties 表单。
2. `frontend-app/src/pages/settings/SettingsPage.test.jsx`：
   - 新增 scoped/global fallback + tombstone 页面级回归。
   - 新增 stale provider properties load 回归，覆盖从 Codex 切到 Claude 后旧 Codex Summary 请求晚到的场景。
3. `frontend-app/src/pages/skills/SkillsPage.test.jsx`：
   - 新增新建个人技能回归，确认“私人使用”保存仍走 `writeSkill({ scope: 'personal', personal_type: 'user' })`。
   - 该测试锁住项目级新建 `skills/create` 与个人技能 `skills/local/write` 的分流边界。

本轮 RED/GREEN 验证：

- RED：`cd frontend-app && npm test -- --run src/pages/settings/SettingsPage.test.jsx -t "provider summary|stale provider properties"` 先失败，失败点为 Summary 未从 global fallback 到 `concise`，以及旧 Codex Summary 请求晚到后覆盖 Claude 的 `auto`。
- GREEN：同一命令复跑通过，2 tests。
- `cd frontend-app && npm test -- --run src/pages/skills/SkillsPage.test.jsx -t "new personal skills"`：通过，1 test；确认现有技能页实现已保持个人技能写入语义，无需生产代码改动。
- `cd frontend-app && npm test -- --run src/pages/settings/SettingsPage.test.jsx src/pages/skills/SkillsPage.test.jsx src/shared/api/backendApi.test.js`：通过，3 files / 41 tests。

本轮收尾验证：

- `cd frontend-app && npm run lint`：通过。
- `cd frontend-app && npm test`：通过，19 files / 443 tests；输出中仍有既有 localStorage ExperimentalWarning、React `act(...)` warning 与测试刻意模拟的 bridge failure 日志，最终 exit code 为 0。
- `cd frontend-app && npm run build`：通过；仅 Vite chunk size warning。
- `git diff --check`：通过。

本轮动态验证：

- 当前监听进程来自本项目脚本：`bash ./run-new-ui-desktop.sh`、`go run ./cmd/agent-terminal`、`node /Users/ai/Desktop/Super-Dolphin/frontend-app/node_modules/.bin/vite`。
- Playwright CLI 打开 `http://127.0.0.1:5175/settings`：设置页加载完成，可见当前项目 `/Users/ai/Desktop/Super-Dolphin`、Provider、PROPERTIES、Prompt、Builtin Tools、UI LOG；控制台只有 React DevTools info。
- Playwright CLI 打开 `http://127.0.0.1:5175/skills`：技能页加载完成，可见 `项目共享 22`、`全部 22`、`共 22 个技能`、`批量导入技能目录`、`新建技能`、编辑/删除入口；控制台只有 React DevTools info。
- Chrome 进程存在；尝试用 Computer Use 读取 `Google Chrome` / `com.google.Chrome` 窗口均返回 `cgWindowNotFound`，AppleScript 查询 Chrome 活动 tab 卡住并已终止。因此本轮 Chrome/Computer Use 只确认到进程存在，可靠页面动态证据以 Playwright CLI 为准。

### 11.11 Workflow bridge payload fail-fast 补齐（2026-06-03 20:02）

本轮继续补齐：

1. `frontend-app/src/entities/client/model/useClientStore.js`：
   - `task/node/statusChanged` bridge event 在触发 `workflowRevision` 之前先校验 payload。
   - payload 必须包含 `dag_key`、`node_key`、`new_status`，并且必须包含 `run_key` 或有效正数 `run_id`。
   - malformed payload 会 fail-fast，不再刷新 Workflow/Tasks/Commands 页面数据。
2. `frontend-app/src/entities/client/model/useClientStore.test.js`：
   - 新增 malformed `task/node/statusChanged` 回归，确认缺 run identity 时抛出 `dag status event run identity is required` 且 `workflowRevision` 保持不变。
   - 合法 node status event 测试补上 `run_key`，与旧 Vue `requireDagNodeStatusPayload()` 语义一致。
3. `frontend-app/src/App.test.jsx`：
   - Workflow bridge 自动刷新页面级测试补上 `run_key`，确认合法事件仍能触发页面刷新与背景同步错误提示。

本轮 RED/GREEN 验证：

- RED：`cd frontend-app && npm test -- --run src/entities/client/model/useClientStore.test.js -t "malformed task node status"` 先失败，失败点为 malformed node status event 没有抛错。
- GREEN：`cd frontend-app && npm test -- --run src/entities/client/model/useClientStore.test.js -t "workflow revision|malformed task node status"` 通过，2 tests。
- `cd frontend-app && npm test -- --run src/App.test.jsx -t "auto-updates workflow page|background sync fails"` 通过，6 tests。
- `cd frontend-app && npm test -- --run src/pages/observability/ObservabilityPage.test.jsx src/shared/api/backendApi.test.js` 通过，34 tests；确认 Observability P2 的 error/slow、trace 展开、自动刷新去重与 API payload 覆盖仍有效。

### 11.12 Settings Provider identity 测试稳定性修正（2026-06-03 20:06）

本轮全量验证暴露：

- `cd frontend-app && npm test` 首次失败在 `frontend-app/src/pages/settings/SettingsPage.test.jsx` 的 Codex identity tombstone 回归，单独跑同一测试可通过。
- 根因是测试只等待 `Codex Home` input 出现，但该 input 初始就带默认值 `~/.codex`；全量高负载时 preference 异步加载尚未完成，断言抢跑。

本轮修正：

1. `frontend-app/src/pages/settings/SettingsPage.test.jsx` 增加 Testing Library `cleanup()`，避免同文件多次 render 残留 DOM。
2. Codex identity 回归改为 `waitFor()` 等待 `Codex Home=/Users/test/.codex` 与 `Instance Key=desktop-main` 真正加载进表单后再继续断言。

本轮验证：

- `cd frontend-app && npm test -- --run src/pages/settings/SettingsPage.test.jsx` 通过，9 tests。
- `cd frontend-app && npm test` 通过，19 files / 444 tests；仍有既有 React `act(...)` warning 和测试刻意模拟的 bridge failure stderr，但 exit code 为 0。

### 11.13 Baseline UI 全图扫描与可访问控件修正（2026-06-03 20:31）

本轮使用 baseline-ui + karpathy-guidelines 对新 React UI 的按钮、卡片、文本框、对话框和主题 token 做全图扫描。处理原则：

- 只修高置信 baseline 问题：可访问名称、真实控件语义、显式按钮类型、主题 token 不漂移。
- 旧 UI 像素级深度等价、卡片密度、dialog focus model、线程名重命名交互结构等需要产品/交互确认的项不做主观复刻；其中可由 baseline UI contract 验证的交互语义项已在 11.14 收敛。

本轮已修：

1. `frontend-app/src/pages/chat/ChatPage.jsx`
   - Markdown task list checkbox 增加任务文本可访问名称。
   - Composer textarea 增加稳定可访问名称 `输入给 Agent 的内容`，不再只依赖 placeholder。
   - Runtime stat 从可点击 `span role=listitem` 改为语义化 `ul/li/button`，保留 `aria-expanded` / `aria-haspopup` 在真实 button 上。
   - Runtime warning/result log 行从可点击 `<p>` 改为 `<button type="button">`，键盘与屏幕阅读器语义与点击行为一致。
2. `frontend-app/src/pages/settings/SettingsPage.jsx`
   - Provider Properties、Prompt、UI LOG action row 的按钮补齐 `type="button"`。
3. `frontend-app/src/styles.css`
   - 对 runtime stat button、runtime log button 做 token 化默认样式重置，避免浏览器默认 button 背景/边框破坏现有主题。
   - 保持现有卡片、输入框、按钮颜色走 CSS token；未新增 raw color。
4. `frontend-app/src/App.test.jsx`
   - 用角色查询锁住 task checkbox、composer textbox、runtime stat button、runtime log button 的 baseline UI contract。

本轮扫描结论：

- JSX button 显式类型扫描通过：所有 `frontend-app/src/**/*.jsx` 的 `<button>` 均有 `type`。
- `styles.test.js` 的 raw color / theme token 合约通过，未发现新增主题外颜色。
- React Doctor diff：从前次记录的 81/100 提升到 84/100；本轮复扫为 84/100，Accessibility warnings 从 24 降到 20，总 issues 从 149 降到 145。原先 ChatPage task checkbox、composer textarea、runtime stat unsupported ARIA 的目标项已消失。
- 11.13 当时剩余 ChatPage 可访问性诊断主要是需要产品/交互确认的项：可调 splitter 是否保留 `role=separator`、模型下拉/路径弹窗是否迁移原生 `<dialog>`、线程名内嵌重命名是否拆成独立控件、runtime panel click-away 是否换事件结构、`role=status` 是否换 `<output>`。这些可落地项已在 11.14 处理。

本轮验证：

- RED：`cd frontend-app && npm test -- App.test.jsx -t "runtime tool details|chat composer textarea|assistant markdown"` 先失败于 runtime stat 不是 button、composer textarea 无可访问名称。
- GREEN：同一类 focused tests 通过，覆盖 runtime stat、composer、task checkbox。
- RED：`cd frontend-app && npm test -- App.test.jsx -t "tool return entries|warning log entries"` 先失败于 runtime log 行不是 button。
- GREEN：`cd frontend-app && npm test -- App.test.jsx -t "tool return entries|warning log entries|runtime tool details|chat composer textarea|assistant markdown"` 通过，5 tests。
- `cd frontend-app && npm test -- styles.test.js` 通过，43 tests。
- `cd frontend-app && npx react-doctor@latest --verbose --diff` 通过，84/100；React Doctor 未安装到项目，仅临时 npx 运行。
- Playwright 动态检查 `http://127.0.0.1:5175/`、`/settings`、`/dags`：页面均无 Vite overlay；Chat/Settings/Workflow 的按钮、卡片、输入框样式均来自现有主题；runtime stat 为透明无边框 button。
- `cd frontend-app && npm run lint` 通过。
- `git diff --check` 通过。
- `cd frontend-app && npm test` 通过，19 files / 444 tests；仍有既有 localStorage ExperimentalWarning、React `act(...)` warning 与测试刻意模拟的 bridge failure stderr，最终 exit code 为 0。
- `cd frontend-app && npm run build` 通过；仅 Vite chunk size warning。

### 11.14 Baseline UI 剩余可访问性与交互决策项收敛（2026-06-03 21:13）

本轮继续使用 baseline-ui + karpathy-guidelines，针对 11.13 留下的“需要产品/交互确认但可由代码验证”的项做最小实现；不做旧 Vue 像素级一比一复刻式重设计。

本轮已修：

1. `frontend-app/src/shared/ui/FocusTrapDialog.jsx`
   - 共享 FocusTrap 弹窗由 `section role="dialog"` 改为原生 `<dialog open>`。
   - overlay click-away 从非交互 `div onClick` 改为独立 backdrop button；键盘 Escape/Tab trap 仍由 dialog 内事件监听负责。
2. `frontend-app/src/pages/chat/ChatPage.jsx`
   - 模型配置弹层与图片 lightbox 改为原生 `<dialog>`。
   - 线程名重命名从可点击 `span` 拆成独立 `button`，输入框去掉 JSX `autoFocus`，改用 effect 聚焦与选中文本。
   - 会话栏、右侧栏、runtime activity 可调分隔条改为真实 `button type="button"`，保留 `role="separator"`、方向和值属性，键盘与拖拽行为不变。
   - `role=status` 状态反馈改为 `<output>`：上下文使用率 banner、diff action notice、代码预览保存状态、titlebar feedback。
   - runtime activity panel 的 click-away 从非交互 `section` handler 改为 document-level dismiss 判断。
   - `MessageAvatar role="assistant"` prop 改名为 `messageRole`，避免被识别为非法 ARIA role。
3. `frontend-app/src/pages/observability/ObservabilityPage.jsx`
   - 最近日志从 `div role="table/row"` 改为原生 `table/thead/tbody/tr/th/td`。
   - trace event 详情从伪 table row 改为原生 `ol/li` 事件列表，展开区域用原生 `section`。
4. `frontend-app/src/pages/skills/SkillsPage.jsx` 与 `frontend-app/src/features/prompts/PromptPageView.jsx`
   - 使用范围 / 草稿范围 segmented control 从 `div role="group"` 改为 `fieldset/legend`。
5. `frontend-app/src/pages/tasks/TasksPage.jsx`、`frontend-app/src/pages/commands/CommandsPage.jsx`、`frontend-app/src/pages/settings/SettingsPage.jsx`、`frontend-app/src/pages/observability/ObservabilityPage.jsx`
   - 同类状态提示统一从 `role="status"` 改为 `<output>`；cron 删除确认改为原生 `<dialog>`。
6. `frontend-app/src/styles.css`
   - 为原生 `dialog`、backdrop、fieldset、resizer button、Observability table/list 补必要 reset，保持现有主题 token，不新增 raw color。

本轮产品/视觉决策口径：

- “旧 UI 像素级深度等价”不再作为 P0/P1/P2 阻塞项处理；没有指定旧 UI 截图、selector 或交互验收前，不做主观复刻。
- 已落地的基线验收口径是：真实控件语义、键盘可达、状态输出语义、主题 token 一致、浏览器页面无 overlay。
- 剩余 React Doctor 的 Bugs/Performance/Maintainability 项属于状态架构、key 稳定性、数组迭代和渲染器维护性问题；本轮不混入 UI 视觉修复。

本轮扫描结论：

- React Doctor diff 从 84/100 提升到 86/100。
- Accessibility 分类已清零：本轮最终 `npx react-doctor@latest --verbose --diff` 不再输出 Accessibility 分组。
- 总 issues 从 145 降到 128；剩余为 Bugs/Performance/Maintainability。

本轮验证：

- RED：`cd frontend-app && npm test -- App.test.jsx FocusTrapDialog.test.jsx -t "idle status|Mermaid diagrams|lightbox|context usage|keyboard resizing|model chip|FocusTrapDialog"` 先失败，覆盖 native dialog、output、resizer、rename button、model dropdown。
- GREEN：同类 focused tests 通过；Observability 语义替换后追加 `src/pages/observability/ObservabilityPage.test.jsx` focused 验证通过。
- `cd frontend-app && npm run lint` 通过。
- `cd frontend-app && npm test` 通过，19 files / 447 tests；仍有既有 localStorage ExperimentalWarning、React `act(...)` warning 与测试刻意模拟的 bridge failure stderr，最终 exit code 为 0。
- `cd frontend-app && npm run build` 通过；仅 Vite chunk size warning。
- `cd frontend-app && npx react-doctor@latest --verbose --diff` 通过，86/100，无 Accessibility 分类；React Doctor 未安装到项目，仅临时 npx 运行。
- `git diff --check` 通过。

本轮动态验证：

- Vite `http://127.0.0.1:5175/` 可访问，Chat 页面无 Vite overlay；composer textbox、thread count output、resizer separator control 可由 Playwright snapshot 识别。
- `http://127.0.0.1:5175/settings` 可访问，设置页输入框、select、status output、按钮、Builtin Tools、UI LOG 渲染正常。
- `http://127.0.0.1:5175/observability` 可访问；点击“查询最新日志”后最近日志渲染为原生 table，行内“复制 Trace ID / 打开 Trace”按钮可见。
- Playwright console 日志仅有 React DevTools info，无运行时错误。

### 11.15 React Doctor 状态/渲染稳定性收敛（2026-06-03 21:40）

本轮继续使用 baseline-ui + karpathy-guidelines，在 11.14 已清零 Accessibility 后，处理剩余 React Doctor 中可安全落地、能用测试验证的状态和渲染稳定性问题；不把旧 UI 像素级深度等价作为自动修改目标。

本轮已修：

1. `frontend-app/src/App.jsx`
   - 页面入口改为直接导入具体页面，避免 barrel import 扩大首屏加载面。
   - 路由 explicit marker 和 memory badge 本地计数改为惰性/keyed 状态，避免无效派生同步。
2. `frontend-app/src/shared/ui/FocusTrapDialog.jsx`
   - keydown listener 改为稳定订阅，通过 ref 读取最新 handler，避免每次 close handler 改变都重新订阅。
3. `frontend-app/src/pages/chat/ChatPage.jsx`
   - Markdown 渲染器的 heading/table/list/blockquote/preview 改为命名组件路径，避免 JSX 内联 render function 重挂载。
   - Markdown 预览、拖拽/剪贴板文件、计划解析、日志/config 判断、项目选项等纯数据清洗改为单次遍历。
   - 模型选择器关闭态使用 store 快照派生，打开态才保留本地草稿。
   - Mermaid 图按 source key 重建，本地状态只保存异步渲染结果；timeline materialization 用 key + 派生 count 避免 effect 链。
   - Runtime popup document listener 改为稳定订阅。
4. `frontend-app/src/features/prompts/PromptPageView.jsx`
   - tags/issues/list/word list 规范化改为单次遍历。
   - Prompt wizard 和 sections panel 使用 key reset 处理 `initialDraft` / prompt scope 变化。
   - 页面 notice 按 cwd keyed state 管理，项目切换时旧 notice 立即失效。
5. `frontend-app/src/pages/skills/SkillsPage.jsx`
   - Markdown preview 使用来源行号/内容 key，内联渲染改为组件路径。
   - import summary draft 并发生成，文件列表/冲突动作/导入提示清洗改为单次遍历。
   - editor body preview 使用 activeSkillPath key reset；page notice/error 与 resolution preview/name prompt 使用 keyed state 管理。
   - Skills dashboard 超时保留 8 秒级体验，但本页请求提前 250ms 进入错误态，避免 loading 与 blocking alert 同屏。
   - 技能搜索框补充 `aria-label`，保持图标搜索视觉不变，同时让图标-only 输入获得稳定可访问名称。
6. `frontend-app/src/pages/workflows/WorkflowPage.jsx`
   - DAG category、selected DAG、selected run、node editor form、final output panel 改为派生/key reset，切换任务后 action notice 真正清空。
   - 步骤下拉框补充 `aria-label`，避免数据驱动路径下自动化扫描误判为未命名控件。
7. `frontend-app/src/pages/settings/SettingsPage.jsx`
   - preference 加载/切换 provider 的 stale request guard 前移，并把 await 后早退改为条件写 UI。

本轮扫描结论：

- React Doctor diff 从 86/100 提升到 97/100。
- Accessibility 仍为 0；最终只剩 17 个 warning，无 error。
- 剩余 17 个 warning 属于架构/产品取舍项，本轮保留：
  - `App.jsx` route popstate sync 与 memory badge warning 上报是跨 store / history 副作用，不适合为清分强行搬到事件入口。
  - `ChatPage.jsx` thread rail/right panel 宽度同步依赖 ref 与外部 store，按 React Compiler lint 保留 effect 版本。
  - `PromptPageView.jsx` active prompt cache 校正是 query cache 一致性逻辑；sections panel / wizard 的 `prefer-useReducer` 属于可读性重构建议。
  - `ObservabilityPage.jsx` active recent params 驱动轮询 effect，不能直接换 ref；`prefer-useReducer` 属于后续架构重构。
  - `SkillsPage.jsx` refreshKey 自动刷新是外部事件桥接，不作为 UI 视觉/控件基线阻塞项。

本轮验证：

- `cd frontend-app && npm run lint` 通过。
- `cd frontend-app && npm test` 通过，19 files / 447 tests；仍有既有 localStorage ExperimentalWarning、React `act(...)` warning 与测试刻意模拟的 bridge failure stderr，最终 exit code 为 0。
- `cd frontend-app && npm run build` 通过；仅 Vite chunk size warning。
- `cd frontend-app && npx react-doctor@latest --verbose --diff` 通过，97/100，17 warnings，0 errors，无 Accessibility 分类；React Doctor 未安装到项目，仅临时 npx 运行。
- Playwright 浏览器复扫 `http://127.0.0.1:5175/`、`/prompts`、`/dags`、`/skills`、`/observability`、`/settings`：无 Vite overlay、无空白页、无 console/page error、无水平溢出、无 icon-only 未命名按钮、无未命名可见输入控件。
- `git diff --check` 通过。

### 11.16 React Doctor 安全项继续清理（2026-06-03 22:00）

本轮在 11.15 的 17 个 React Doctor warning 基础上继续清理，但仍遵守产品/交互决策边界：旧 UI 像素级密度复刻、模型下拉/路径弹窗是否迁移原生 `<dialog>`、线程名内嵌重命名是否拆分为独立控件、可调 splitter 语义、runtime panel click-away 事件结构、`role=status` 是否改 `<output>`，都不作为自动修改项。

本轮已修：

1. `frontend-app/src/pages/observability/ObservabilityPage.jsx`
   - 将 recent log 轮询参数从仅供 interval 使用的 state 改为 ref，避免无意义重渲染。
   - 将页面级 `recentResult`、`copiedTraceId`、`notice`、`loading` 合并为 reducer；自动刷新仍保持 2 秒间隔、跳过重叠请求、查询成功后才启用轮询参数。
2. `frontend-app/src/features/prompts/PromptPageView.jsx`
   - active prompt id 改为 query state 派生：当当前 prompt 列表快照中不存在可强制启动的 active prompt 时，渲染阶段直接视为未选中，不再用 effect 事后回写 query cache。
   - `PromptSectionsPanel` 和 `PromptIntentWizardModal` 的相关状态合并为 reducer，保留原来的保存、删除、dry-run、commit 后端调用语义。
3. `frontend-app/src/pages/skills/SkillsPage.jsx`
   - skills dashboard 刷新从 effect 监听 `skillRevision` 改为 query key 携带 revision；成功数据同时写入稳定 base cache，revision 刷新失败时仍显示上次成功快照和“同步失败，显示的是上次成功的数据”提示。
4. `frontend-app/src/App.jsx`
   - memory badge 查询失败 warning 移入 query fetch 失败路径，移除单独监听 query error 的 effect。
5. `frontend-app/src/styles.css`
   - `.runtime-stat` 仍保持 `button` 元素语义，同时 CSS 为 `border: 0`、`background: transparent`，满足 runtime stat 透明无边框按钮口径。

本轮扫描结论：

- React Doctor diff 从 97/100、17 warnings 降到 98/100、7 warnings。
- Accessibility 仍为 0；无 error。
- 剩余 7 个 warning 保留为需要产品/交互或路由架构确认的项：
  - `App.jsx` route popstate / explicit route sync 是浏览器 history 与全局 store 的同步边界，不能为了清分改成 render 阶段 set state 或绕过 bootstrap page 优先级。
  - `ChatPage.jsx` thread rail 和 runtime right panel 宽度同步属于可调 splitter/runtime panel 外部状态语义；目标已明确这类为产品/交互决策项，未做自动改造。

本轮 focused 验证：

- `cd frontend-app && npm test -- --run src/pages/observability/ObservabilityPage.test.jsx src/features/prompts/PromptPageView.test.jsx` 通过，15 tests。
- `cd frontend-app && npm test -- --run src/pages/skills/SkillsPage.test.jsx src/App.test.jsx -t "skills loading state|技能|skill"` 通过，22 passed / 175 skipped。
- `cd frontend-app && npm test -- --run src/features/prompts/PromptPageView.test.jsx src/App.test.jsx -t "prompt|提示词|wizard|分段|dry-run|Sections"` 通过，24 passed / 176 skipped。
- `cd frontend-app && npm test -- --run src/App.test.jsx -t "memory center|similar memories|memory badge|记忆中心"` 通过，7 passed / 185 skipped。
- 每批修改后 `cd frontend-app && npm run lint` 均通过。
- 每批修改后 `cd frontend-app && npx react-doctor@latest --verbose --diff` 均通过，最终为 98/100、7 warnings、0 errors、无 Accessibility 分类；React Doctor 仍仅通过临时 `npx` 运行，未安装进项目依赖。
- 最终完整验证：
  - `cd frontend-app && npm run lint` 通过。
  - `cd frontend-app && npm test` 通过，19 files / 447 tests；仍有既有 React `act(...)` warning、localStorage/bridge 测试日志和刻意模拟的 backend unavailable stderr，最终 exit code 为 0。
  - `cd frontend-app && npm run build` 通过；仅 Vite chunk size warning。
  - `git diff --check` 通过。
- Playwright 浏览器复扫 `http://127.0.0.1:5175/`、`/settings`、`/dags`、`/prompts`、`/skills`、`/observability`：无 Vite overlay、无空白页、无 console/page error、无水平溢出、无 icon-only 未命名按钮、无未命名可见输入控件。默认数据下未出现可见 runtime stat button；该项以 `frontend-app/src/styles.css` 的 `.runtime-stat` 样式作为源码证据确认。

## 12. 风险与后续验证

1. 当前项目复测过程中曾短暂观察到 `frontend-app/src/App.jsx` conflict marker 导致的 Vite overlay；最终复测时该文件已无冲突标记，Chat 可正常渲染。若后续再做完整动态覆盖，应先确认 `rg -n '<<<<<<<|=======|>>>>>>>' frontend-app/src` 无输出。
2. 当前工作区已有大量未提交改动，尤其 `frontend-app` 与 thread/observability 相关文件；本报告按当前工作区内容审计，可能包含用户尚未提交的新代码状态。
3. 若下一步继续实现剩余 P2/P3 深度等价项，建议每项都先加 focused regression test，再补最小页面/RPC facade。
4. 对“旧功能是否仍是产品需求”的判断需要产品侧确认；如果命令卡或 cron 已被设计上废弃，应在文档和代码中删除 alias/死入口，而不是保留半迁移状态。
