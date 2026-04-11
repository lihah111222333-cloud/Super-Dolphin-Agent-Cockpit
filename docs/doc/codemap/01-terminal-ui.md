# super-agent-v3 代码地图：终端入口与 UI 层

## 1. 模块概述

这一层现在可以明确拆成三块：

- **桌面入口层**：`cmd/agent-terminal/` 负责把前端构建产物 embed 进 Go 二进制，并调用 `internal/app.RunDesktop(...)` 启动桌面应用。
- **Wails UI 运行时层**：`internal/ui/wails/` 负责拼装 Wails `application.App`、前端资源服务、原生对话框/剪贴板、RPC 绑定、事件桥、多窗口、代码预览与退出生命周期。
- **Vue 交互外壳层**：`cmd/agent-terminal/frontend/vue-app/` 负责聊天工作台、Diff 预览、技能/提示词/设置/LSP IDE 等界面与状态管理。

前端访问系统能力有两条路径：

1. **主 RPC 路径**：`services/api.js -> runtime.Call.ByID(METHOD_IDS.CALL_API) -> Go App.CallAPI -> rpc.Server.Dispatch`
2. **少量直连绑定路径**：`services/api.js -> runtime.Call.ByID(...) -> Go 绑定方法`，目前用于 `GetBuildInfo`、`SelectProjectDir`、`SelectFiles`、`SaveClipboardImage` 等高频原生能力

后端事件则通过：

```text
backend event dispatcher
  -> EventBridge.publish()
  -> WailsLifecycle.EmitEvent('bridge-event' / 'agent-event' / 'files-dropped' / 'app-will-quit')
  -> services/api.js 订阅器
  -> thread store / page state / composer 响应式更新
```

这份代码地图重点覆盖三类事实：

- 入口与 Wails 壳如何装配
- Vue 前端新增的组件/composable/store 是否已被纳入描述
- 真实数据流是否已经从“发送 / 增量 patch / 历史回填 / Diff lazy sync / 预览保存 / 多窗口”角度闭环

---

## 2. 目录结构

### 2.1 Go 入口 / Wails UI 壳

```text
cmd/agent-terminal/
├── main.go                      # 桌面入口；调用 app.RunDesktop(frontendDistFS())
└── frontend.go                  # embed frontend/dist，并导出 frontendDistFS()

internal/ui/wails/
├── assets.go                    # FrontendFS 注入、Vite dev proxy、placeholder 回退
├── binding.go                   # Wails App 绑定：CallAPI / 旧版 LaunchAgent / BuildInfo / LSP diag / 多窗口组
├── binding_native.go            # 原生能力：目录/文件选择、复制文本、剪贴板图片保存
├── bridge.go                    # eventsurface -> bridge-event / agent-event 兼容事件桥
├── code_preview.go              # ui/code/save|locate|open 的读写/预览/打开编辑器实现
├── code_scope.go                # 作用域根匹配、后缀搜索、深度限制、symlink 安全校验
├── http_server.go               # 浏览器调试模式的 HTTP 资源服务 + /wails/ws RPC
├── lifecycle.go                 # 退出拦截、pending quit、backend shutdown、硬超时强退
├── module.go                    # Fx 装配：App / Service / Lifecycle / EventBridge / HTTP runner
├── rpc.go                       # 注册 ui/code/*、ui/log、ui/select*、ui/openNewWindow 等 UI RPC
├── runner.go                    # application.App -> platformrunner.Runner 适配（桌面路径当前未注入 Module）
├── scope_catalog.go             # 从 config + uistate 构建“已注册项目根目录”目录册
├── window.go                    # 主窗口/新窗口创建、文件拖拽事件、URL query 参数拼装
├── window_state.go              # bootstrap snapshot 编解码、按窗口名消费、窗口分组状态
└── frontend/index.html          # 前端未构建时的占位页
```

补充：同目录还包含 `*_test.go`，覆盖 `binding`、`bridge`、`code_preview`、`code_scope`、`lifecycle`、`window` 等关键行为与兼容性。

### 2.2 Vue 前端应用

```text
cmd/agent-terminal/frontend/vue-app/
├── main.js                      # 挂载 Vue 应用
├── app.js                       # AppRoot：页面切换、全局 bootstrap、dashboard 刷新、事件订阅
├── provider-config-options.js   # Provider 的 model / effort 选项源
├── lib/echarts-custom.js        # JsonRenderer 图表用的精简 ECharts bundle
│
├── components/
│   ├── ChatTimeline.js          # 主时间线；渲染 dialog/process/approval/plan/json-render/citation
│   ├── ComposerBar.js           # 输入、附件、技能建议、线程配置、发送/停止/压缩
│   ├── DiffPanel.js             # Diff / 文本 / Markdown / 图片预览与保存
│   ├── DiffPanel.template.js    # DiffPanel 的模板拆分文件
│   ├── ActivityPanel.js         # 活动统计、告警、过程事件面板
│   ├── ProjectSelect.js         # 顶部项目选择器
│   ├── ProjectModal.js          # 添加项目模态框
│   ├── PathChoiceModal.js       # 多路径 locate 结果选择框
│   ├── JsonRenderer.js          # json-render 递归渲染入口
│   ├── JsonRenderWidgets.js     # Card/Metric/Chart/Tabs/Timeline/... 组件注册表
│   ├── json-render-markdown-action-key.js # JsonRenderer 内 markdown 行为键常量
│   ├── SidebarNav.js            # 左侧导航
│   ├── timeline/
│   │   ├── AttachmentPreview.ts
│   │   ├── ToolTickerBar.ts
│   │   ├── useApprovalActions.js
│   │   ├── useAssistantBodyActions.js
│   │   ├── useAttachmentPreviewState.ts
│   │   ├── useCommandHelpers.js
│   │   ├── usePresencePopover.js
│   │   ├── useTimelineHelpers.js
│   │   ├── useTimelineItems.js
│   │   └── timeline-markdown-helpers.js
│   └── unified-chat/
│       ├── ChatToolbar.js
│       ├── ThreadRailSidePanel.js
│       ├── WorkspaceChatPanel.js
│       ├── CmdCardGrid.js
│       └── CmdOverviewPanel.js
│
├── composables/
│   ├── scroll-helpers.js        # useAutoScroll 的纯函数辅助
│   ├── useAutoScroll.js         # 自动滚底、DOM rebuild 恢复、snapshot guard
│   ├── useResizePanels.js       # 中区 / thread rail / 活动面板尺寸拖拽
│   ├── useThreadActions.js      # 页面级 launch/send/interrupt/recover/openNewWindow 封装
│   ├── useThreadStatus.js       # 状态文案、耗时、token、compact、alerts 聚合
│   ├── useThreadCards.js        # 会话卡 / cmd card / 概览指标
│   ├── useThreadCards.pinned-plan.js # 聊天页 pinned plan 状态与 dismiss 持久化
│   ├── useThreadSelection.js    # 线程切换 freshness / 历史回填 / 强制滚动
│   ├── useFileRefPreview.js     # file-ref / image citation -> DiffPanel 预览调度
│   ├── useFileRefPreview.helpers.js # dirty preview、locate/open fallback、PathChoiceModal 协调
│   ├── useSkillPreview.js       # skills/match/preview 防抖与 force/manual 选择合并
│   ├── useProviderMode.js       # Claude/Codex provider 切换
│   ├── useCopyThreadInfo.js     # 复制 thread/provider/runtime 信息到剪贴板
│   ├── usePageLifecycle.js      # UnifiedChatPage 生命周期副作用总线
│   ├── useKeyboardShortcuts.js  # Esc 中断快捷键
│   ├── useInlineRename.js       # thread rail 内联改名
│   ├── useMermaidRenderer.js    # Mermaid 懒渲染调度
│   ├── useComposerDragDrop.js   # ComposerBar 拖拽与原生投递
│   ├── useComposerInterrupt.js  # 发送/停止主按钮与确认状态机
│   ├── useComposerTextarea.js   # textarea 高度自适应 + IME 状态
│   ├── useComposerThreadConfig.js # ComposerBar 线程级 model/effort 下拉
│   ├── useFileDrop.js           # 全局 files-dropped -> composer.attachByPaths
│   ├── useDiffPreview.js        # timeline/diff/fallback preview 聚合
│   ├── useDiffPanelPreview.js   # DiffPanel 的 diff/text/markdown/image 视图模型
│   ├── useDiffPanelInteractions.js # DiffPanel 聚焦、折叠、复制路径、滚动定位
│   ├── useSkillEditor.js        # SkillsPage 编辑器/导入/保存逻辑
│   ├── useSkillFileNavigation.js # SkillsPage 子文件跳转与技能引用跳转
│   └── useThreadConfigController.js # 当前线程配置的加载/保存/恢复继承
│
├── stores/
│   ├── threads.js               # 线程 store 门面：组装 state / prefs / sync / actions / selectors
│   ├── thread-actions.js        # 动作管理器组装层
│   ├── thread-actions-helpers.js# thread/start|turn/start|interrupt|recover|archive 等动作实现
│   ├── thread-sync.js           # 同步管理器组装层
│   ├── thread-sync-helpers.js   # ui/state/get、ui/sidebar/get、event-driven sync 主逻辑
│   ├── thread-sync-selectors.js # timeline / diff / status / token / alerts selector
│   ├── thread-snapshot.js       # runtime snapshot patch、optimistic 合并、selection 保护
│   ├── thread-snapshot-utils.js # map merge / timeline freeze / runtime normalize 工具
│   ├── thread-live-patch.js     # ui/thread/patch 增量 patch、sequence gap 恢复
│   ├── thread-history-ui.js     # thread/messages -> timeline 立即投影
│   ├── thread-diff-sync.js      # diff revision 跟踪与 includeDiff lazy sync
│   ├── thread-compact.js        # compact waiter/result/success-hide 状态
│   ├── bridge-event-parser.js   # bridge-event method/type/threadId 解析工具
│   ├── thread-prefs.js          # UI 偏好读写、cwd scope 注入、串行化持久化
│   ├── thread-preference.model.js # 线程偏好 key 与 viewPref 归一化模型
│   ├── thread-state-whitelist.js # store 根状态白名单与断言
│   ├── thread-time-utils.js     # thread 时间戳解析与稳定排序索引
│   ├── thread-ui-normalize.js   # thread/status/split/cardCols/threadRailWidth 归一化
│   ├── thread-view.model.js     # chat/cmd 布局缺省与 thread 列表派生模型
│   ├── thread-store-view.js     # displayName / getThreadsByMode / 当前选中 thread
│   ├── thread-optimistic.js     # optimistic thread / preference taint 临时缓存
│   ├── composer.js              # 输入文本、附件、粘贴/拖放/文件选择
│   └── projects.js              # 项目列表、active project、项目模态框
│
├── pages/
│   ├── UnifiedChatPage.js       # 主聊天工作台
│   ├── UnifiedChatPage.template.js
│   ├── UnifiedChatPage.helpers.js
│   ├── LspIdePage.js            # 四栏 LSP IDE
│   ├── SkillsPage.js            # 技能管理页
│   ├── SystemPromptPage.js      # system prompt CRUD
│   ├── SettingsPage.ts          # About / Turn Tracker / Provider / LSP Prompt / UI Log
│   ├── settings/
│   │   ├── ProviderSettings.ts
│   │   ├── LspPromptSettings.ts
│   │   └── useSettingsScope.ts
│   ├── CommandsPage.js          # Command cards / prompts 发送页
│   ├── TasksPage.js             # task ack / trace 页
│   └── DataPage.js              # DAG / Memory 通用数据卡片页
│
├── services/
│   ├── api.js                   # 前端唯一系统桥；runtime.Call.ByID + callAPI + runtime event 订阅
│   ├── log.js                   # 本地 ring buffer + 批量桥接 ui/log
│   ├── lsp-api.js               # lsp/gui_* RPC 薄封装
│   ├── diff.js                  # unified diff / apply_patch diff 解析器
│   ├── pretext-layout.js        # streaming markdown 尾部高度测量与容器观察
│   ├── json-render-engine.js    # json-render spec block 提取与解析
│   └── status.js                # 后端线程状态标准化
│
└── utils/
    ├── assistant-markdown.js    # Markdown 渲染主入口、file ref 检测、reasoning leak 规整
    ├── assistant-markdown-click.js # 从 DOM 事件解析 file-ref / citation action
    ├── assistant-markdown-codex.js # Codex directive / citation / task-stub 预处理与后处理
    ├── assistant-markdown-codex-ui.js # Codex link / skill / automation / code-comment 徽章渲染
    ├── assistant-markdown-streaming.js # streaming markdown 帧调度与高度缓存
    ├── preview-utils.js         # code_open -> text/markdown/image/synthetic diff preview
    ├── diff-utils.js            # diff 路径归一化、选中文件与行号聚焦
    ├── citation-action-utils.js # task/skill/conversation/image citation 点击动作
    ├── citation-preview-utils.js # image citation -> active timeline 预览解析
    ├── plan-utils.js            # plan item -> pinned plan / json-render spec
    ├── skill-match-utils.js     # skill match 去重、force/manual 合并、签名
    ├── skill-parser.js          # SKILL.md frontmatter 解析与生成
    ├── code-highlight.js        # LSP IDE 代码高亮与语言推断
    ├── composer-textarea-height.js # Composer textarea 高度计算
    ├── mermaid-renderer.js      # Mermaid 懒加载与 SVG 安全清洗
    ├── thread-copy-utils.js     # thread 信息复制时的 log path / 时间格式工具
    ├── thread-page-types.d.ts   # thread page 共享 TS 类型声明
    ├── thread-page-utils.js     # selection freshness / 历史回填 / card 列表构建
    ├── translate-dict.js        # 英中思考摘要/状态短语翻译；读取本地词典
    └── format-utils.js          # 时间/token/elapsed/活动输出格式化
```

补充：`vue-app/` 根目录下还包含大量 `*.test.js` 行为/回归测试，覆盖 `api bridge`、`ChatTimeline`、`ComposerBar`、`DiffPanel`、`thread store`、`UnifiedChatPage`、`Skills/Settings/LSP` 等场景；本地图以运行时代码为主，不逐一展开。

---

## 3. 核心类型 / 接口 / 组件

### 3.1 Go / Wails

| 类型 / 模型 | 位置 | 职责 |
|---|---|---|
| `FrontendFS` | `assets.go` | 由入口层注入的前端文件系统包装器。 |
| `App` | `binding.go` | Wails 暴露对象；既负责 `CallAPI`，也持有多窗口 bootstrap / group 状态。 |
| `EventBridge` | `bridge.go` | 把后端事件标准化为 `bridge-event`，并额外发 `agent-event` 兼容通道。 |
| `WailsLifecycle` | `lifecycle.go` | 退出拦截、frontend ready、backend shutdown、pending quit、硬超时。 |
| `ActiveAgentCounter` / `ActiveAgentCounterFunc` | `lifecycle.go` | 退出时统计活跃 agent 数的抽象与函数适配器。 |
| `scopeCatalog` | `scope_catalog.go` | “默认 project root + 已注册 roots”的目录册。 |
| `scopedPath` | `code_scope.go` | `Root / Abs / Relative` 三元组；所有 `ui/code/*` 都依赖它保证 scope 安全。 |
| `codeSaveResult` | `code_preview.go` | `ui/code/save` 返回模型。 |
| `codeLocateMatch` / `codeLocateResult` | `code_preview.go` | `ui/code/locate` 的匹配列表、截断标记与元数据。 |
| `codeSnippetLine` / `codeOpenResult` | `code_preview.go` | `ui/code/open` 的 snippet / full-text / image preview 返回模型。 |
| `clientMetaParams` / `scopeParams` / `code*Params` / `openNewWindowParams` | `rpc.go` | UI RPC 的严格参数模型；兼容 `_aoClientKind/_aoClientRoute` 元字段。 |
| `httpAssetServer` | `http_server.go` | 浏览器模式下的 HTTP 资源服务与 WebSocket JRPC 容器。 |

### 3.2 Vue 组件 / Store / Composable

#### 核心组件

| 名称 | 位置 | 职责 |
|---|---|---|
| `AppRoot` | `app.js` | 根组件；页面切换、dashboard 刷新、build info、运行时配置、全局事件订阅。 |
| `UnifiedChatPage` | `pages/UnifiedChatPage.js` | 聊天主工作台：toolbar、thread rail、timeline、diff、composer、activity 的总装点。 |
| `ChatToolbar` | `components/unified-chat/ChatToolbar.js` | 顶栏：项目切换、provider 切换、停止/恢复、复制 thread info、窗口 CWD 徽章。 |
| `ThreadRailSidePanel` | `components/unified-chat/ThreadRailSidePanel.js` | 左侧 thread rail；支持 pin/archive/inline rename/new window。 |
| `CmdCardGrid` / `CmdOverviewPanel` | `components/unified-chat/*` | cmd 模式卡片视图与总览指标。 |
| `WorkspaceChatPanel` | `components/unified-chat/WorkspaceChatPanel.js` | 中央聊天容器，包裹 `ChatTimeline` 与 pinned plan 区。 |
| `ChatTimeline` | `components/ChatTimeline.js` | 渲染 dialog、thinking、tool、command、file、approval、plan、json-render、citation。 |
| `DiffPanel` | `components/DiffPanel.js` | 右侧 diff / image / markdown / text preview；支持原地编辑和 `ui/code/save`。 |
| `ComposerBar` | `components/ComposerBar.js` | 输入区；组合附件、技能提示、线程配置、发送/中断/压缩逻辑。 |
| `ActivityPanel` | `components/ActivityPanel.js` | 聚合 LSP/tool/command/file 活动统计、过程事件与 alerts。 |
| `JsonRenderer` / `JsonRenderWidgets` | `components/*` | 渲染 json-render spec，并把 markdown action 继续转交到外层。 |
| `ProjectSelect` / `ProjectModal` | `components/*` | 项目作用域切换与新增。 |
| `PathChoiceModal` | `components/PathChoiceModal.js` | `ui/code/locate` 命中多路径时的人机确认层。 |
| `SkillsPage` | `pages/SkillsPage.js` | 技能目录导入、SKILL.md / 子文件编辑、Markdown 预览跳转。 |
| `SystemPromptPage` | `pages/SystemPromptPage.js` | 主/子 Agent 的 system prompt CRUD。 |
| `SettingsPage` | `pages/SettingsPage.ts` | About、统一超时阈值、Provider、LSP Prompt、UI 日志。 |
| `LspIdePage` | `pages/LspIdePage.js` | 文件读取、高亮、symbol tree、hover、references、search、diagnostics。 |
| `CommandsPage` / `TasksPage` / `DataPage` | `pages/*` | Dashboard 类业务页：命令卡/任务/DAG/记忆。 |

#### Store / Manager

| 名称 | 位置 | 职责 |
|---|---|---|
| `useThreadStore()` | `stores/threads.js` | 主线程 store 门面；组合 runtime state、偏好、同步器、动作管理器、selectors。 |
| `createPreferenceManager()` | `stores/thread-prefs.js` | UI 偏好持久化与 `cwd` scope 注入；串行化 `ui/preferences/set` 写入。 |
| `createSyncManager()` | `stores/thread-sync.js` + `thread-sync-helpers.js` | `ui/state/get` / `ui/sidebar/get` / `thread/messages` / event-driven sync 协调器。 |
| `createThreadActions()` | `stores/thread-actions.js` + `thread-actions-helpers.js` | thread/start、turn/start、interrupt、recover、compact、rename、archive。 |
| `createThreadViewHelpers()` | `stores/thread-store-view.js` | `displayName`、模式过滤、当前选中 thread。 |
| `thread-live-patch.js` | `stores/thread-live-patch.js` | 优先消费 `ui/thread/patch`，做 timeline delta、diff/status patch 与 sequence gap 恢复。 |
| `thread-snapshot.js` | `stores/thread-snapshot.js` | full snapshot patch、optimistic item 合并、本地 active selection 保护、payload pruning。 |
| `thread-diff-sync.js` | `stores/thread-diff-sync.js` | diffRevision 跟踪与 includeDiff lazy load。 |
| `thread-history-ui.js` | `stores/thread-history-ui.js` | `thread/messages` 结果即时投影成 timeline。 |
| `thread-state-whitelist.js` | `stores/thread-state-whitelist.js` | 限制 JS store 根节点只放本地 UI 键，runtime 状态通过 accessor 暴露。 |
| `useProjectStore()` | `stores/projects.js` | 项目列表、active project、项目模态框、目录浏览。 |
| `useComposerStore()` | `stores/composer.js` | 输入文本、文件选择、粘贴图片、拖放附件。 |

#### 关键 composables

| 名称 | 职责 |
|---|---|
| `useThreadActions` | 把 threadStore 动作组装为页面/组件可直接绑定的 UI handler。 |
| `useThreadStatus` | 状态头、详情、token usage、compact result、活动告警聚合。 |
| `useThreadCards` | thread rail / cmd cards / recent threads / pinned plan 视图模型。 |
| `createPinnedPlanState` | 根据最新 `plan` item 生成 pinned plan，并用 `sessionStorage` 记录 dismiss。 |
| `useThreadSelection` | 线程切换 freshness、历史加载、scroll reset 协调。 |
| `useAutoScroll` + `scroll-helpers` | 自动滚底、drag/wheel/key 检测、DOM rebuild 恢复、snapshot guard。 |
| `useFileRefPreview` + `useFileRefPreview.helpers` | file-ref/citation -> diff focus / locate / open / dirty preview guard / path choice。 |
| `useSkillPreview` | `skills/match/preview` 防抖；合并 force match 与手动技能选择。 |
| `useResizePanels` | 拖拽调整 workspace split、thread rail 宽度、activity panel 高度。 |
| `useComposerDragDrop` / `useFileDrop` | 浏览器拖放 + Wails `files-dropped` 原生投递接入。 |
| `useComposerInterrupt` / `useKeyboardShortcuts` | 发送/停止主按钮与 Esc 中断状态机。 |
| `useComposerTextarea` / `useComposerThreadConfig` | 输入框高度自适应与线程级 model/effort 配置下拉。 |
| `useDiffPreview` / `useDiffPanelPreview` / `useDiffPanelInteractions` | 统一 preview state、DiffPanel 视图模型、文件聚焦/折叠/复制。 |
| `useSkillEditor` / `useSkillFileNavigation` | SkillsPage 的导入、读写、子文件导航与引用跳转。 |
| `useCopyThreadInfo` | 聚合 provider/runtime/thread config 后复制 thread 身份信息。 |
| `useMermaidRenderer` | Markdown 中 Mermaid 图表的懒渲染调度。 |
| `createThreadConfigController` | 当前线程配置的加载、草稿、保存、恢复继承。 |

---

## 4. 关键函数 / 方法

### 4.1 Go 入口与 Wails 壳

```go
func main()
```
- 桌面入口；调用 `app.RunDesktop(frontendDistFS())`，失败时向 `stderr` 打印并退出。

```go
func frontendDistFS() fs.FS
```
- 从 `frontend/dist` embed 子目录导出可注入的前端资源 FS。

```go
func AssetHandlerFrom(injected FrontendFS) http.Handler
```
- 资源入口：优先 `VITE_DEV_URL` 反代，其次使用注入前端 FS，再回退 placeholder。

```go
func NewApp(server *rpc.Server, cfg *config.Config) *App
func NewService(app *App) application.Service
func NewWailsApplication(p applicationParams) *application.App
```
- 分别创建 `App` 绑定、Wails `Service`、完整 `application.App`；同时绑定 `ShouldQuit` / `OnShutdown` / runtime event emitter 并创建主窗口。

```go
func NewActiveAgentCounter(p activeAgentCounterParams) ActiveAgentCounter
func NewWailsLifecycle(counter ActiveAgentCounter, slogLogger *slog.Logger) *WailsLifecycle
func NewEventBridge(dispatcher *event.Dispatcher, lifecycle *WailsLifecycle, slogLogger *slog.Logger) *EventBridge
```
- 装配退出前活跃 agent 计数、生命周期对象和事件桥。

```go
func (a *App) CallAPI(method string, params json.RawMessage) (any, error)
```
- 前端通用 RPC 入口；会剥离 `_ao*` 元字段后转发到 `rpc.Server.Dispatch`。

```go
func (a *App) LaunchAgent(name, prompt, cwd string) (any, error)
func (a *App) StopAgent(threadID string) error
func (a *App) ListAgents() (any, error)
```
- 旧版桌面绑定兼容层；内部分别代理到 `thread/start`、`thread/stop`、`agent.list`。

```go
func (a *App) GetBuildInfo() map[string]string
func (a *App) GetLSPDiagnostics(filePath string) (string, error)
func (a *App) GetLSPStatus() (any, error)
```
- 提供直连绑定能力：读取 build info、查询 `lsp/gui_file diagnostics`、返回 LSP 状态占位值。

```go
func NewRPCHandlers(app *App, cfg *config.Config, uiState uistate.Service) rpc.HandlerMapResult
```
- 注册 UI 专属 RPC：`ui/code/*`、`ui/log`、`ui/selectProjectDir`、`ui/selectProjectDirs`、`ui/selectFiles`、`ui/openNewWindow`、`ui/windowBootstrap/get`。

```go
func handleCodeSave(...)
func handleCodeLocate(...)
func handleCodeOpen(...)
```
- `ui/code/*` 的入口；先根据 project scope 求根目录，再调用 scoped 文件能力。

```go
func requestScopeRoots(ctx context.Context, cfg *config.Config, state projectStateReader, project string, projects []string) ([]string, error)
func resolveScopeRoots(project string, projects []string, catalog scopeCatalog) ([]string, error)
```
- 从 config + UI 项目状态计算本次访问允许的根目录集合。

```go
func saveScopedFile(rawPath, content string, roots []string, createNew bool) (codeSaveResult, error)
func locateScopedFile(ctx context.Context, rawPath string, roots []string, limit int) (codeLocateResult, error)
func openScopedFile(ctx context.Context, rawPath string, line, column int, roots []string) (codeOpenResult, error)
```
- 分别完成 scoped save / locate / open；`openScopedFile` 最终会尝试调用 VS Code 或系统默认程序打开目标文件。

```go
func resolveOpenTarget(ctx context.Context, raw string, roots []string) (scopedPath, error)
func findScopedFiles(ctx context.Context, raw string, roots []string, limit int) ([]scopedPath, bool, error)
func secureRelativeToRoot(root, candidate string) (string, error)
```
- 负责相对路径命中、后缀搜索、深度/时间限制、symlink 安全校验与相对路径生成。

```go
func (b *EventBridge) Start()
func (b *EventBridge) publish(method string, payload any)
```
- 订阅后端事件面，展开通知后统一发成 `bridge-event`；若 payload 中能提取 `threadId/agent_id`，再发 `agent-event` 兼容事件。

```go
func (l *WailsLifecycle) ShouldQuit() bool
func (l *WailsLifecycle) requestBackendShutdown()
func (l *WailsLifecycle) NotifyBackendFailed()
func ShutdownHardDeadline() time.Duration
```
- 退出拦截：若仍有活跃 agent，则先发 `app-will-quit` overlay 并延时请求 backend 关闭；若超时或 backend 异常，则放行强退。

```go
func CreateMainWindow(app *application.App, title string, debug bool)
func createWindow(app *application.App, title string, debug bool, name, uiBootstrap, cwd string) *application.WebviewWindow
func buildFilesDroppedPayload(files []string, details *application.DropTargetDetails) (map[string]any, bool)
func windowURL(uiBootstrap, cwd string) string
```
- 创建主窗口/新窗口，向 URL 注入 `ao_ui_bootstrap`、`ao_window_cwd`，并把文件拖拽事件转成 `files-dropped`。

```go
func encodeWindowBootstrapSnapshot(snapshot map[string]any) (string, error)
func decodeWindowBootstrapSnapshot(raw string) (map[string]any, error)
func (a *App) consumeWindowBootstrapSnapshot() map[string]any
```
- 多窗口 bootstrap snapshot 编解码与“一次性消费”能力。

```go
func handleUIWindowBootstrapGet(app *App) map[string]map[string]any
func handleUIOpenNewWindow(app *App, p openNewWindowParams) (map[string]any, error)
func resolveUIBootstrap(raw string, snapshot map[string]any) (string, error)
```
- `ui/windowBootstrap/get` 与 `ui/openNewWindow` 的 RPC 入口；支持传入 snapshot 或预编码 bootstrap 字符串。

```go
func NewHTTPAssetServer(p httpAssetServerParams) httpAssetRunnerResult
```
- 提供 `127.0.0.1:4511` 的 HTTP 前端服务与 `/wails/ws` WebSocket RPC，便于浏览器调试模式。

### 4.2 前端核心函数

```js
async function bootstrap()
```
- `main.js` 启动函数；挂载 `AppRoot`。

```js
export async function callAPI(method, params = {})
function subscribeRuntimeEvent(eventName, callback, options = {})
```
- `services/api.js` 的核心桥：前者统一走 `METHOD_IDS.CALL_API`；后者封装 `bridge-event` / `agent-event` / `files-dropped` / `app-will-quit` 订阅。

```js
async function callByID(methodID, ...args)
export async function getBuildInfo()
export async function selectProjectDir(defaultPath)
export async function selectProjectDirs()
export async function selectFiles()
export async function saveClipboardImage(base64Payload)
```
- API 桥中的直连绑定路径；`selectProjectDir/selectFiles` 在 by-ID 形状不符合预期时还会回退到 `ui/select*` RPC。

```js
export async function refreshChatPageData(threadStore)
export async function ensureThreadSelectionFresh(threadStore, threadId, options = {})
export async function requestHistoryLoad(threadStore, threadId, loadOptions)
```
- `app.js` 与 `thread-page-utils.js` 中的页面 freshness 管线；负责 page-enter / selection / bootstrap 时的 thread sync 与历史回填。

```js
export function useThreadStore()
```
- 暴露整个聊天状态机：sidebar refresh、snapshot patch、live patch、message load、send/stop/recover/compact、selectors 与 preference API。

```js
export function applyRuntimeSnapshot(state, snapshot, options = {})
export function applyRuntimeThreadPatch(ctx, evt, threadId, options = {})
export function handleBridgeEvent(ctx, evt)
```
- 分别处理全量 snapshot、`ui/thread/patch` 增量事件、普通 bridge-event；共同构成 store 的“全量+增量+修复”同步策略。

```js
export async function syncRuntimeState(ctx)
export async function syncThreadState(ctx, threadId)
export function createSyncThreadDiffState(deps)
export async function loadMessages(ctx, threadId, limit = 300, options = {})
```
- 分别负责全局快照、单线程快照、Diff lazy sync、消息历史加载。

```js
export async function sendMessage(ctx, threadId, prompt, attachments = [], options = {})
export async function stopThread(ctx, threadId, options = {})
export async function compactThread(ctx, threadId)
export async function forceCompleteThread(ctx, threadId)
```
- thread action 主路径：发送 turn、停止当前 turn、上下文压缩、强制完成。

```js
export function useThreadActions(props, deps)
```
- 页面动作整合层：`launchOne/send/interruptCurrent/recoverSelected/openNewWindow/...`。

```js
export function useAutoScroll(workspaceRef)
```
- 利用 MutationObserver、snapshot guard、拖拽/滚轮/键盘检测与 DOM rebuild 恢复保持聊天滚动稳定。

```js
export function useFileRefPreview(props, deps)
export async function openSelectionFallbackPreview(options)
```
- file-ref 点击后优先用当前 diff 聚焦；否则走 `ui/code/locate`、`PathChoiceModal`、`ui/code/open`，最终把结果投影到 `DiffPanel`。

```js
export function useSkillPreview(opts)
```
- 防抖调用 `skills/match/preview`，把 force match 和手动选择合并成最终 `selectedSkills`。

```js
export function createThreadConfigController({ threadStore, threadActions, selectedThreadId, isCmd })
```
- 当前线程 model/effort 配置的加载、草稿、保存与恢复继承。

```js
export function renderAssistantMarkdown(rawText)
export function resolveRenderedMarkdownAction(event)
```
- Markdown 渲染主入口与 DOM action 解析入口；支持 file ref、image citation、Codex directive、复制代码、展开代码块等交互。

```js
export function buildTextPreviewFromCodeOpen(codeOpenResult)
export function buildMarkdownPreviewFromCodeOpen(codeOpenResult)
export function buildImagePreviewFromCodeOpen(codeOpenResult)
export function buildSyntheticDiffFromCodeOpen(codeOpenResult)
```
- 把 `ui/code/open` 的返回结果转换成 `DiffPanel` 可消费的 preview state。

```js
export function extractSpecBlocks(text)
export async function renderMermaidInContainer(root = document)
export function translateThinkingBody(text)
```
- 分别服务于 json-render block 拆分、Mermaid 懒渲染与 thinking/status 文案翻译。

---

## 5. 依赖关系

### 5.1 Go 层依赖什么

#### `cmd/agent-terminal/`
- 直接依赖：`internal/app`
- 标准库关键依赖：`embed`、`io/fs`
- 职责：只负责提供 embed 资源并启动桌面 app，不承载业务逻辑。

#### `internal/ui/wails/`
- 直接依赖的 internal 包：
  - `internal/platform/rpc`：统一 RPC 分发与 `WSHandler`
  - `internal/platform/config`：启动/关闭超时、project root、debug/log 配置
  - `internal/platform/runner`：HTTP asset server 的 runner 接口输出
  - `internal/platform/shared`：安全 goroutine 与忽略错误日志
  - `internal/platform/eventsurface`：后端事件绑定与通知展开
  - `internal/module/uistate`：项目列表/活动项目等 UI 状态
  - `internal/contract`：`OrchestrationService`，用于活跃 agent 计数
  - `internal/dto/agent`：agent state 常量
- 额外依赖：
  - `wails/v3/pkg/application` / `wails/v3/pkg/events`
  - `go.uber.org/fx`
  - `github.com/creachadair/jrpc2/handler`
  - `github.com/kelindar/event`
  - `pkg/logger`

### 5.2 谁依赖它

- `internal/app/app.go`
  - `RunDesktop(frontendFS)` 在桌面模式下把 `FrontendFS` 注入 `uiwails.Module`
  - 桌面路径直接 `Populate(&wailsApp, &lifecycle)`，然后手动执行 `wailsApp.Run()`
- `internal/app/runner.go`
  - `BindRuntime` 启动 grouped runners（当前主要是 `NewHTTPAssetServer`），并在 runner 异常退出时通过 `WailsLifecycle.NotifyBackendFailed()` 兜底
- `cmd/agent-terminal/frontend/vue-app/services/api.js`
  - 运行时动态加载 `/wails/runtime.js`，把前端所有系统能力收口到 Wails bridge

### 5.3 前端逻辑依赖什么

前端没有直接 import `internal/...`，但**逻辑上强依赖**以下 RPC / 绑定面：

- **状态快照与 dashboard**
  - `ui/state/get`
  - `ui/sidebar/get`
  - `ui/dashboard/get`
  - `ui/windowBootstrap/get`（路由已存在，前端当前尚未消费）

- **线程生命周期 / 聊天动作**
  - `thread/start`
  - `thread/messages`
  - `turn/start`
  - `turn/interrupt`
  - `turn/forceComplete`
  - `thread/compact/start`
  - `thread/recover`
  - `thread/name/set`
  - `thread/archive` / `thread/unarchive`
  - `thread/config/get` / `thread/config/set`
  - `thread/resolve`

- **偏好 / 配置 / 作用域**
  - `ui/preferences/get|set`
  - `config/read`
  - `config/lspPromptHint/read|write`

- **项目与原生能力**
  - `ui/projects/get|add|remove|setActive`
  - `ui/selectProjectDir`
  - `ui/selectProjectDirs`
  - `ui/selectFiles`
  - `ui/copyText`
  - `ui/openNewWindow`
  - 以及直连绑定：`GetBuildInfo` / `SelectProjectDir` / `SelectFiles` / `SaveClipboardImage`

- **代码预览**
  - `ui/code/locate`
  - `ui/code/open`
  - `ui/code/save`

- **业务页 RPC**
  - `skills/match/preview`
  - `skills/local/read|write|delete|importDir|listFiles`
  - `skills/config/write`
  - `prompts/list|write|delete`
  - `approval/respond`

- **LSP**
  - `lsp/gui_file`
  - `lsp/gui_grep`
  - `lsp/gui_structure`
  - `lsp/gui_inspect`
  - `lsp/gui_xref`

### 5.4 前端内部的中心依赖点

- **`services/api.js`**：前端唯一系统桥，同时负责 runtime event 订阅和 direct binding fallback。
- **`services/log.js`**：几乎所有页面/组件依赖它做 ring buffer、本地诊断和 `ui/log` 回传。
- **`stores/threads.js`**：统一聊天状态中心。
- **`stores/thread-snapshot.js` + `stores/thread-live-patch.js` + `stores/thread-sync-helpers.js`**：前端状态正确性的三条主链。
- **`utils/thread-page-utils.js`**：页面层 freshness / 历史加载 / 卡片列表的公共逻辑。
- **`pages/UnifiedChatPage.js`**：聊天工作台总装点。
- **`composables/useFileRefPreview.helpers.js`**：右侧预览与多路径选择、dirty preview、code open fallback 的关键调度点。

---

## 6. 数据流

### 6.1 启动链路

```text
cmd/agent-terminal/main.go
  -> frontendDistFS()
  -> internal/app.RunDesktop(frontendFS)
  -> Fx 启动 Module + uiwails.Module
  -> NewHTTPAssetServer 作为 grouped runner 启动
  -> NewWailsApplication + AssetHandlerFrom
  -> CreateMainWindow(...)
  -> main.js bootstrap()
  -> AppRoot.setup()/onMounted()
  -> 先订阅 onAgentEvent/onBridgeEvent/onAppWillQuit
  -> 并发 refreshBuildInfo() + config/read + projectStore.reloadProjects()
  -> threadStore.setPreferenceScopeCwd(threadScopeCwd)
  -> threadStore.refreshSidebarState()
  -> ensureThreadSelectionFresh(activeThreadId)
```

关键修正：`AppRoot.bootstrap()` 会**先注册事件订阅，再做异步初始化**，避免初始化过程丢事件。

### 6.2 直连绑定路径（BuildInfo / 原生对话框 / 剪贴板）

```text
services/api.js
  -> callByID(METHOD_IDS.*)
  -> /wails/runtime.js
  -> Go binding method
     - GetBuildInfo
     - SelectProjectDir / SelectFiles
     - SaveClipboardImage
  -> 若 selectProjectDir/selectFiles 的 by-ID 返回形状不符
     -> 回退 callAPI('ui/selectProjectDir' / 'ui/selectFiles')
```

补充：`copyTextToClipboard()` 默认优先 `ui/copyText`；仅在 debug shim 或原生桥失败时才降级浏览器 Clipboard API。

### 6.3 用户输入 -> 后端 -> Provider / Orchestration

```text
ComposerBar
  -> useComposerInterrupt.onPrimaryAction()
  -> useThreadActions.send()
  -> composer store 读取 text/attachments
  -> 若当前无 threadId：threadStore.startThread(activeProject, { focusMode })
  -> useSkillPreview.resolveComposerSkillSelectionForSend()
  -> threadStore.sendMessage()
  -> stores/thread-actions-helpers.sendMessage()
  -> callAPI('turn/start', { threadId, input, cwd, selectedSkills, manualSkillSelection })
  -> Go App.CallAPI -> rpc.Server.Dispatch
  -> 后端 turn/start handler
  -> provider / orchestration runtime
```

`sendMessage()` 还会把附件转换为：

- `text`
- `localImage`
- `image`
- `mention`

并在本地 timeline 中**乐观插入 user item**，避免等待后端 JSONL/history 回写后才显示。

### 6.4 后端事件回推 -> 前端状态更新

```text
backend event dispatcher
  -> eventsurface.Bind(...)
  -> EventBridge.publish()
  -> WailsLifecycle.EmitEvent('bridge-event' / 'agent-event')
  -> services/api.onBridgeEvent()/onAgentEvent()
  -> threadStore.handleBridgeEvent()
     -> skills/changed => state.skillRevision++
     -> thread/tokenusage/updated => 直接 push tokenUsageByThread
     -> ui/thread/patch => applyRuntimeThreadPatch()
     -> sidebar changed => refreshSidebarState()
     -> streaming delta => syncThreadHistoryAtomic() 节流 + trailing debounce
     -> turn completed / history page => syncThreadHistoryAtomic() / loadMessages()
  -> applyRuntimeSnapshot() / applyImmediateTimelineFromMessages()
  -> Vue reactive state 更新
  -> ChatTimeline / ActivityPanel / ChatToolbar / ThreadRail 重渲染
```

这里的优化点比原文更完整：

- `thread-live-patch.js` 会优先消费 `ui/thread/patch`，并用 `sequence` 检测 gap / stale event。
- `thread-sync-helpers.js` 会把 `tokenUsage` 更新直接写入 store，避免为 token push 再 round-trip `ui/state/get`。
- `thread-history-ui.js` 会在 `thread/messages` 返回后**立即**把消息投影成 timeline，再与现有 optimistic / runtime items 合并。

### 6.5 Diff revision 的 lazy sync

```text
ui/state/get(includeDiff=false)
  -> applyRuntimeSnapshot(...)
  -> 发现 activeThread 的 diffRevision != loadedDiffRevision
  -> syncThreadDiffState(threadId, { force?: false })
  -> callAPI('ui/state/get', { threadId, includeDiff: true, knownDiffRevision })
  -> 仅在 revision 变化时回传 diffTextByThread
  -> applyRuntimeSnapshot(... loadedRevisionByThread ...)
  -> DiffPanel / cmd cards 更新
```

也就是说：**主 snapshot 默认不携带完整 diff**，只有 revision 变化或强制拉取时才补齐 `diffText`。

### 6.6 File ref / Citation -> 右侧预览

```text
ChatTimeline / WorkspaceChatPanel
  -> resolveRenderedMarkdownAction()
  -> file-ref-click / citation-click
  -> useFileRefPreview()
  -> confirmAbandonDirtyPreview()?（有未保存编辑时先确认）
  -> restoreCurrentThreadSelection()
     -> syncThreadState()
     -> syncThreadDiffState(force)
     -> buildFocusedDiffSelection()
  -> 若当前 diff 中可聚焦：只更新 focusedDiffPath/focusedDiffLine
  -> 否则 callAPI('ui/code/locate')
     -> PathChoiceModal（多路径命中时让用户选）
     -> callAPI('ui/code/open')
  -> preview-utils.js 构造 Text / Markdown / Image / SyntheticDiff preview
  -> DiffPanel 展示；文本/Markdown 可编辑并调用 callAPI('ui/code/save')
```

补充：`citation-preview-utils.js` 会把 image citation 直接解析成图片预览，不一定走 `ui/code/open`。

### 6.7 技能预览 / 自动技能选择

```text
composer.state.text 或 selectedThreadId 或 skillRevision 变化
  -> useSkillPreview.scheduleComposerSkillPreview()
  -> debounce 240ms
  -> callAPI('skills/match/preview', { threadId, text })
  -> normalizeSkillPreviewMatches()
  -> force matched skills 自动加入
  -> manual selected skills 保留
  -> sendMessage() 时把两者合并成 selectedSkills
```

其中 `skillRevision` 来自 `skills/changed` bridge event，因此技能目录变更后 composer 建议会自动刷新。

### 6.8 项目作用域切换

```text
ProjectSelect / ProjectModal
  -> useProjectStore.setActive/addProject/removeProject
  -> callAPI('ui/projects/*')
  -> projectStore.state.active 变化
  -> AppRoot.watch(threadScopeCwd)
  -> threadStore.setPreferenceScopeCwd(next)
  -> threadStore.refreshSidebarState()
  -> 后续 ui/state/get / ui/preferences/* / code preview / settings page / prompts / skills import
     自动带上 scoped cwd 或 project/projects
```

### 6.9 新窗口 / bootstrap snapshot

```text
ThreadRailSidePanel new-window button
  -> useThreadActions.openNewWindow()
  -> callAPI('ui/selectProjectDir', { defaultPath })
  -> callAPI('ui/openNewWindow', { cwd })
  -> Go handleUIOpenNewWindow()
  -> resolveUIBootstrap(snapshot/raw)
  -> App.openNewWindow()
  -> createWindow(..., uiBootstrap, cwd)
  -> registerWindowState(name, group, snapshot)
  -> window URL 带 ao_ui_bootstrap / ao_window_cwd
```

现状修正：**后端已经支持 snapshot 编码与 `ui/windowBootstrap/get`**，但前端源码中仍未消费 `ao_ui_bootstrap/ao_window_cwd` 查询参数，也未读取 `ui/windowBootstrap/get`。

### 6.10 退出链路

```text
用户关闭窗口
  -> WailsLifecycle.ShouldQuit()
  -> ActiveAgentCounter.ActiveAgentCount()
  -> 若 >0: emit 'app-will-quit' overlay + 延迟 requestBackendShutdown()
  -> backend shutdown / hard deadline
  -> NotifyBackendFailed() 或正常 quit
  -> 前端 onAppWillQuit() -> AppRoot.isExiting = true
  -> 显示退出遮罩
```

---

## 7. 前端架构

### 7.1 组件层次

```text
AppRoot
├── SidebarNav
├── UnifiedChatPage
│   ├── ChatToolbar
│   │   └── ProjectSelect
│   ├── ThreadRailSidePanel
│   ├── CmdOverviewPanel / CmdCardGrid（cmd 模式）
│   ├── WorkspaceChatPanel
│   │   └── ChatTimeline
│   │       ├── AttachmentPreview
│   │       ├── ToolTickerBar
│   │       └── JsonRenderer
│   │           └── JsonRenderWidgets (Card/Metric/Markdown/Chart/Tabs/Timeline/...)
│   ├── DiffPanel
│   ├── ComposerBar
│   ├── ActivityPanel
│   └── PathChoiceModal
├── ProjectModal
├── SystemPromptPage
├── SkillsPage
├── LspIdePage
├── SettingsPage
│   ├── ProviderSettings
│   └── LspPromptSettings
└── DataPage / TasksPage / CommandsPage
```

### 7.2 状态管理

#### 全局 store

1. **`useProjectStore()`**
   - 状态：`projects`, `active`, `showModal`, `modalPath`, `browsing`
   - 负责：项目列表加载、活动项目切换、项目增删、目录选择器模态框。

2. **`useComposerStore()`**
   - 状态：`text`, `attachments`, `attaching`
   - 负责：附件去重、图片粘贴、拖放、文件选择、清空 composer。

3. **`useThreadStore()`**
   - UI 本地态：`activeThreadId`, `activeCmdThreadId`, `pinnedThreadAtById`, `archivedThreadAtById`
   - runtime 态：`threads`, `statuses`, `interruptibleByThread`, `viewPrefsChat`, `viewPrefsCmd`, `timelinesByThread`, `diffTextByThread`, `diffRevisionByThread`, `tokenUsageByThread`, `agentMetaById`, `agentRuntimeById`, `mainAgentId`, `mainAgentState`, `activityStatsByThread`, `alertsByThread`, `skillRevision` 等
   - 特点：store 根节点只允许 UI 本地键，runtime 状态通过 accessor 映射到 `runtimeRootState`
   - 内部层次：
     - `thread-prefs.js` + `thread-preference.model.js` + `thread-ui-normalize.js`
     - `thread-sync.js` + `thread-sync-helpers.js` + `thread-sync-selectors.js`
     - `thread-actions.js` + `thread-actions-helpers.js`
     - `thread-snapshot.js` + `thread-snapshot-utils.js`
     - `thread-live-patch.js` + `thread-history-ui.js` + `thread-diff-sync.js`
     - `thread-store-view.js` + `thread-view.model.js` + `thread-time-utils.js`
     - `thread-compact.js` + `thread-optimistic.js`

### 7.3 composables 分层

#### A. 页面编排层
- `useThreadActions`
- `useThreadStatus`
- `useThreadCards`
- `useThreadSelection`
- `usePageLifecycle`
- `createThreadConfigController`

#### B. 交互 / 布局层
- `useAutoScroll`
- `scroll-helpers`
- `useResizePanels`
- `useKeyboardShortcuts`
- `useInlineRename`
- `useCopyThreadInfo`

#### C. 输入增强层
- `useComposerTextarea`
- `useComposerDragDrop`
- `useComposerInterrupt`
- `useComposerThreadConfig`
- `useSkillPreview`
- `useFileDrop`

#### D. 预览 / 渲染层
- `useFileRefPreview`
- `useFileRefPreview.helpers`
- `useDiffPreview`
- `useDiffPanelPreview`
- `useDiffPanelInteractions`
- `useMermaidRenderer`
- `useAssistantBodyActions`
- `useAttachmentPreviewState`
- `usePresencePopover`

#### E. 技能 / 配置工具页层
- `useSkillEditor`
- `useSkillFileNavigation`
- `useSettingsScope`

### 7.4 设计上的关键特点

1. **前端把系统能力全部收口到 `services/api.js`**
   - 主能力走 `callAPI()`；少数高频原生能力走 `callByID()`。
   - 所有 RPC 自动附带 `_aoClientKind/_aoClientRoute`，Go 侧再 strip 掉，兼顾日志与 strict handler。

2. **聊天状态不是“只拉快照”，而是“事件驱动 + live patch + history hydrate + diff lazy sync”**
   - `thread-live-patch.js` 优先吃 `ui/thread/patch`
   - `thread-sync-helpers.js` 按事件类型决定 patch / syncThreadState / refreshSidebar / loadMessages
   - `thread-diff-sync.js` 只在 revision 变化时拉完整 diff

3. **store 根状态白名单是显式设计，不是偶然实现**
   - `thread-state-whitelist.js` 保证根节点只放少量 UI local state
   - runtime payload 统一通过 snapshot/live patch 更新，降低响应式污染

4. **滚动保护是单独一条复杂子系统**
   - `useAutoScroll.js` 用 MutationObserver、snapshot guard、重建恢复、drag/wheel/key 判断保证 streaming 场景不跳动

5. **右侧预览面板是独立预览管线**
   - diff 命中优先
   - 否则走 `ui/code/locate/open`
   - 再退到 text/markdown/image/synthetic diff preview
   - Markdown / Text 预览可原地编辑并通过 `ui/code/save` 回写

6. **Markdown 渲染不是单文件逻辑，而是一个子系统**
   - `assistant-markdown.js`：file-ref / 图片 / reasoning leak / code highlight
   - `assistant-markdown-codex*.js`：Codex directive、skill/conversation/task/automation/comment citation
   - `assistant-markdown-click.js`：DOM action 解析
   - `assistant-markdown-streaming.js` + `pretext-layout.js`：流式文本高度与刷新调度

7. **工具页普遍遵循“页面 + composable + services/api + cwd scope”模式**
   - `SettingsPage`、`SystemPromptPage`、`SkillsPage` 都会把活动项目 `cwd` 作为作用域带给后端

---

## 8. 关键观察 / 备注

- `internal/ui/wails/binding.go` 中 `LaunchAgent/StopAgent/ListAgents` 仍是**旧版桌面绑定兼容接口**，真实执行已切到 `thread/*` RPC。
- `services/api.js` 现在不仅有 `callAPI()`，还有 `callByID()`：`GetBuildInfo`、`SelectProjectDir`、`SelectFiles`、`SaveClipboardImage` 走的是直连绑定路径。
- `internal/ui/wails/rpc.go` 的 `ui/code/save` 虽然保留 `CreateNew` 字段，但 `resolveSaveTarget(..., _ bool)` 仍要求文件已存在，**新建文件尚未真正打通**。
- 多窗口后端路径已经完整：`ui/openNewWindow`、snapshot codec、`ao_ui_bootstrap/ao_window_cwd` URL 参数、`ui/windowBootstrap/get` 全都存在；**缺口在前端消费**。
- `internal/ui/wails/GetLSPStatus()` 目前仍是 stub，固定返回空数组。
- `internal/ui/wails/runner.go` 提供了 `application.App -> platformrunner.Runner` 适配，但当前 `uiwails.Module` 没有 `fx.Provide(NewRunner)`；桌面路径仍由 `internal/app.RunDesktop()` 直接 `wailsApp.Run()`。
- `thread-live-patch.js` 已经加入 `sequence` 去重、gap 检测、diff clear 忽略和 fallback recovery；数据流比“纯全量 sync”复杂得多。
- `thread-sync-helpers.js` 对 `thread/tokenusage/updated` 做了**直接 push**，对 `skills/changed` 做了 `skillRevision++`，都不再强依赖整包 snapshot。
- `ChatTimeline.js` 现在额外依赖 `translate-dict.js`、`assistant-markdown-streaming.js`、`useMermaidRenderer.js`，说明 thinking/markdown 渲染已经演进成独立优化子系统。
- `SkillsPage` 不只是单文件 CRUD：`useSkillEditor.js` + `useSkillFileNavigation.js` 已支持多目录导入、子文件列表、Markdown 引用跳转、技能引用跳转。
- `services/log.js` 会把 **debug/warn/error** 批量回传后端 `ui/log`，高频 `info` 更多留在本地 ring buffer 里。

---

## 9. 总结

这个区域的真正“中轴线”可以归纳为六个点：

1. **Go 入口**：`cmd/agent-terminal/main.go` / `frontend.go`
2. **Wails 壳装配**：`internal/ui/wails/module.go` / `binding.go` / `window.go`
3. **Wails 生命周期与事件桥**：`internal/ui/wails/lifecycle.go` / `bridge.go`
4. **前端系统桥与状态中心**：`services/api.js` + `stores/threads.js`
5. **前端状态正确性主链**：`thread-sync-helpers.js` + `thread-snapshot.js` + `thread-live-patch.js`
6. **主工作台**：`pages/UnifiedChatPage.js`

如果后续要继续读代码，建议按下面顺序深入：

```text
cmd/agent-terminal/main.go
-> internal/app.RunDesktop
-> internal/ui/wails/module.go / binding.go / rpc.go / bridge.go / window.go / window_state.go
-> frontend/vue-app/services/api.js
-> frontend/vue-app/app.js
-> frontend/vue-app/stores/threads.js
-> frontend/vue-app/stores/thread-sync-helpers.js / thread-snapshot.js / thread-live-patch.js / thread-diff-sync.js
-> frontend/vue-app/pages/UnifiedChatPage.js
-> frontend/vue-app/composables/useFileRefPreview.helpers.js
-> frontend/vue-app/components/ChatTimeline.js / DiffPanel.js / ComposerBar.js
```

---

## 审查补遗

本次对照源码后，已在正文中补充/修正以下内容：

1. **补齐遗漏文件覆盖**
   - 前端新增覆盖了原文未展开的文件：
     - `components/DiffPanel.template.js`
     - `components/json-render-markdown-action-key.js`
     - `composables/scroll-helpers.js`
     - `composables/useFileRefPreview.helpers.js`
     - `composables/useSkillFileNavigation.js`
     - `composables/useThreadCards.pinned-plan.js`
     - `lib/echarts-custom.js`
     - `stores/thread-optimistic.js`
     - `stores/thread-preference.model.js`
     - `stores/thread-snapshot-utils.js`
     - `stores/thread-state-whitelist.js`
     - `stores/thread-time-utils.js`
     - `stores/thread-ui-normalize.js`
     - `stores/thread-view.model.js`
     - `utils/assistant-markdown-click.js`
     - `utils/assistant-markdown-codex.js`
     - `utils/assistant-markdown-codex-ui.js`
     - `utils/assistant-markdown-streaming.js`
     - `utils/citation-action-utils.js`
     - `utils/citation-preview-utils.js`
     - `utils/code-highlight.js`
     - `utils/composer-textarea-height.js`
     - `utils/mermaid-renderer.js`
     - `utils/skill-match-utils.js`
     - `utils/skill-parser.js`
     - `utils/thread-copy-utils.js`
     - `utils/thread-page-types.d.ts`
     - `utils/translate-dict.js`
   - 同时把 `stores/thread-actions.js`、`stores/thread-sync.js` 及其 helper/model 文件补回目录结构与架构说明。

2. **补齐 Wails 侧遗漏能力**
   - 补记了 `binding.go` 中的 `GetBuildInfo`、`GetLSPDiagnostics`、`GetLSPStatus`、`OpenNewWindow`。
   - 补记了 `rpc.go` 中 `ui/selectProjectDirs`、`ui/windowBootstrap/get`、`ui/openNewWindow`、`ui/log`。
   - 补记了 `window_state.go` 的 snapshot codec 与按窗口消费逻辑。

3. **修正系统桥描述**
   - 原文把前端系统桥近似成单一路径 `callAPI -> App.CallAPI`；现已修正为“**主 RPC 路径 + 少量直连绑定路径**”双通道结构。

4. **修正 thread store 架构描述**
   - 原文未充分体现 `thread-state-whitelist`、`thread-optimistic`、`thread-preference.model`、`thread-time-utils`、`thread-ui-normalize` 等基础层。
   - 现已把 `thread-snapshot`、`thread-live-patch`、`thread-diff-sync`、`thread-history-ui` 的职责拆清。

5. **补全数据流**
   - 新增了 **Diff lazy sync**（`diffRevision` -> `syncThreadDiffState(includeDiff=true)`）链路。
   - 新增了 **技能建议**（`skills/match/preview`）链路。
   - 新增了 **新窗口 / bootstrap snapshot** 链路。
   - 细化了 **file-ref/citation -> preview** 中的 dirty preview guard、`PathChoiceModal` 与 fallback open 流程。

6. **修正事件桥说明**
   - 明确写出 `thread/tokenusage/updated` 现在会直接 push 到 store。
   - 明确写出 `skills/changed` 会驱动 `skillRevision`，从而刷新 composer 技能建议。
   - 明确写出 `ui/thread/patch` 会优先走 live patch，并做 `sequence` 去重与 gap 恢复。

7. **修正多窗口现状判断**
   - 原文只提到后端支持多窗口 bootstrap；现补充说明：**后端链路已完整，前端消费 query/bootstrap snapshot 仍是 TODO**。

8. **补充非运行时代码说明**
   - 目标目录中还包含大量 `*_test.go` 与 `*.test.js`，本地图已在目录说明中明确：测试文件存在且覆盖面广，但不在主树逐一展开。
