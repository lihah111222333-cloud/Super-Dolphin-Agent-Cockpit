# React + Zustand + Tailwind 前端架构重构方案

创建日期：2026-05-29
适用范围：`cmd/agent-terminal/frontend`

> **For agentic workers:** REQUIRED SUB-SKILL: Use `superpowers:executing-plans` to implement this plan task-by-task. 本方案是前端重构执行蓝图，不改变后端 RPC wire shape，不新增后端 API。

## 摘要

本次前端重构采用“渐进纵切”策略：先在新 `src/` 架构中重写 `UnifiedChat` 主链路，旧 `vue-app/` 在迁移期只作为对照、测试迁移参考和短期回滚依据。目标技术栈为 React 18、Zustand、Tailwind CSS v4，并使用 Feature-Sliced Design 约束目录和依赖方向。

重构的核心不是换皮或一次性搬目录，而是把当前页面级编排、桥接事件、runtime 状态、用户动作和样式状态拆成稳定边界：

- `shared/api` 统一封装 Wails bridge、RPC validation、runtime event 和 request tracing。
- `entities/*/model` 承载 Zustand store、selectors、reducers 和领域状态规约。
- `features/*` 承载用户动作，例如发送消息、恢复线程、修改线程配置。
- `widgets/*` 组合可见工作区，例如 thread rail、chat workspace、composer dock、activity panel。
- `pages/*` 只做页面级装配、路由参数和布局组织。

## 当前事实与约束

### 仓库现状

- 当前前端入口仍在 `cmd/agent-terminal/frontend/vue-app/main.js`，根装配在 `vue-app/app.js`。
- 主聊天工作台由 `vue-app/pages/UnifiedChatPage.js` 和 `UnifiedChatPage.template.js` 组装。
- 系统桥统一收口在 `vue-app/services/api.js`，前端日志在 `vue-app/services/log.js`。
- 全局状态由 `vue-app/stores/threads.js`、`projects.js`、`composer.js`、`thread-prefs.js` 等 Vue reactive store 承载。
- 当前测试和 size guard 默认扫描 `vue-app/**/*.test.js` 与 `vue-app/**/*.js`，迁移完成后必须扩展到 `src/**/*.{js,jsx}`。

### 后端契约约束

- RPC 方法保持 slash 风格，例如 `thread/start`、`turn/start`、`ui/state/get`、`ui/sidebar/get`、`ui/preferences/set`。
- RPC 参数必须是 object params，前端不得把 payload 改成位置参数或隐式全局状态。
- `thread/start` 支持 `deferSpawn`，空线程首发必须先创建 pending thread，再由第一条 `turn/start` 触发后端 `SpawnIfNeeded`。
- `turn/start` 会校验请求 `cwd` 与线程权威 cwd，一旦缺失或不一致必须 fail-fast。
- `ui/thread/patch`、timeline、token、diff、status、alert 等 runtime 更新继续通过 `bridge-event` / `agent-event` 投递。
- `ui/log` 是前端日志进入 Go logger 的唯一桌面日志通道。

### Fail-Fast 约束

- 禁止静默降级、吞错捕获、隐式默认配置和隐式状态补全。
- 前端 adapter、store action、controller hook 遇到缺失 `cwd`、非法 payload、未知 RPC response shape、后端错误时必须显式报错。
- UI 可以展示错误状态、warning log 或 toast，但不能把错误转换为空数组、空对象或默认 provider 继续执行。
- 所有敏感 RPC payload 必须显式携带 `cwd`，不得依赖“当前项目全局变量”兜底。

### 现有代码迁移映射

| 当前文件 / 目录 | 目标 slice | 迁移说明 |
| --- | --- | --- |
| `vue-app/services/api.js` | `src/shared/api/bridge` + `src/shared/api/rpc` | 拆成 runtime loader、`callAPI`、event subscription、request tracing、shape validation。禁止业务组件绕过该层直接调 Wails。 |
| `vue-app/services/log.js` | `src/entities/log/model` + `src/shared/api/tracing` | ring buffer、bridge queue、log level subscriber 迁移到 log entity；RPC/bridge/store 追踪放入 tracing API。 |
| `vue-app/stores/threads.js` | `src/entities/thread/model` | Vue reactive facade 改为 Zustand store；保留 snapshot、sidebar、live patch、optimistic state 和 selectors。 |
| `vue-app/stores/thread-actions-helpers.js` | `src/entities/thread/api` + `src/features/send-message/model` | 纯 RPC payload 构建留在 entity api；用户动作时序进入 feature controller。 |
| `vue-app/composables/useThreadActions.js` | `src/features/send-message`、`interrupt-turn`、`compact-thread`、`recover-thread` | 按动作拆分，避免继续形成大 hook；每个 feature 自带测试。 |
| `vue-app/stores/projects.js` | `src/entities/project/model` + `src/entities/project/api` | 保留项目列表、active cwd、modal、目录选择；新增 `requireActionCwd(reason)` 作为 fail-fast 入口。 |
| `vue-app/stores/composer.js` | `src/widgets/composer-dock/model` + `src/features/attach-files` | 输入框局部状态留在 composer dock；文件选择、粘贴图片、拖拽附件归入 attach-files feature。 |
| `vue-app/stores/thread-prefs.js` | `src/entities/preference/model` | 保留同 key 串行写队列；写入失败显式进入 `writeErrorsByKey` 和 warning log。 |
| `vue-app/pages/UnifiedChatPage.js` | `src/pages/unified-chat/UnifiedChatPage.jsx` | 页面只装配 widget，不承载发送、同步、diff、附件、日志追踪等业务细节。 |
| `vue-app/pages/UnifiedChatPage.template.js` | `src/widgets/*/ui` | 按 ThreadRail、ChatWorkspace、ComposerDock、ActivityPanel、DiffPanel、WarningLogPanel 拆 JSX。 |
| `vue-app/components/ChatTimeline.js` | `src/widgets/chat-workspace/ui` + `src/entities/thread/lib` | 渲染组件留在 widget；timeline item 归一化、排序、去重规则放入 entity lib。 |
| `vue-app/components/ComposerBar.js` | `src/widgets/composer-dock/ui` | UI 保留现有 `data-testid`；发送、附件、compact、thread config 通过 feature 注入。 |
| `vue-app/components/DiffPanel.js` | `src/widgets/diff-panel/ui` + `src/entities/thread/api` | 组件只渲染 diff；保存、打开、定位走 `shared/api/rpc` 并显式传 project cwd。 |
| `vue-app/styles/**` | `src/shared/styles/**` | token、theme、typography、layout 分层；业务局部样式留在 widget。 |

### 数据保真要求

- 保留所有当前高价值 `data-testid`，尤其是 `chat-page`、`chat-toolbar`、`thread-rail`、`thread-list`、`chat-empty-state`、`composer-bar`、`composer-input`、`composer-send-button`、`composer-compact-button`、`context-usage-banner`。
- 保留现有用户可见行为：空线程首发、pending launch、provider mode、thread config、activity stats、alerts、diff revision、file ref preview。
- 保留现有日志字段：`_aoClientKind`、`_aoClientRoute`、thread、agent、method、event、level。
- 保留现有 Wails 桥语义：文件系统、窗口、剪贴板、项目目录选择等桌面能力不得改成 browser-native fallback。

## 目标目录结构

```text
cmd/agent-terminal/frontend/
  src/
    app/
      main.jsx
      App.jsx
      providers/
        AppProviders.jsx
        ErrorBoundary.jsx
      routes/
        routeRegistry.js
      bridge/
        bootstrapRuntime.js
        runtimeEvents.js
      logging/
        frontendLogger.js

    pages/
      unified-chat/
        UnifiedChatPage.jsx
        index.js
      commands/
      dags/
      skills/
      memory/
      settings/

    widgets/
      app-shell/
        ui/
        index.js
      thread-rail/
        ui/
        model/
        index.js
      chat-workspace/
        ui/
        model/
        index.js
      composer-dock/
        ui/
        model/
        index.js
      activity-panel/
        ui/
        model/
        index.js
      diff-panel/
        ui/
        model/
        index.js
      warning-log-panel/
        ui/
        model/
        index.js

    features/
      send-message/
        model/
        ui/
        index.js
      interrupt-turn/
      compact-thread/
      recover-thread/
      rename-thread/
      configure-thread/
      select-project/
      attach-files/
      trace-event/

    entities/
      thread/
        api/
        model/
        lib/
        index.js
      turn/
        api/
        model/
        index.js
      project/
        api/
        model/
        index.js
      preference/
        api/
        model/
        index.js
      dashboard/
        api/
        model/
        index.js
      dag/
        api/
        model/
        index.js
      skill/
        api/
        model/
        index.js
      memory/
        api/
        model/
        index.js
      log/
        model/
        lib/
        index.js

    shared/
      api/
        bridge/
        rpc/
        events/
        tracing/
      ui/
        button/
        icon-button/
        tabs/
        dialog/
        tooltip/
        badge/
        toast/
        empty-state/
        log-row/
      layout/
        app-shell/
        panel/
        split-pane/
        toolbar/
        scroll-area/
      styles/
        reset.css
        tokens.css
        themes.css
        typography.css
        tailwind.css
      lib/
        assert/
        wire/
        date/
        format/
        dom/
      test/
        render.jsx
        fixtures/
```

依赖方向固定为：

```text
app -> pages -> widgets -> features -> entities -> shared
```

规则：

- `shared` 不依赖任何业务层。
- `entities` 只依赖 `shared`，不得依赖 `features`、`widgets` 或 `pages`。
- `features` 可以组合 `entities` 与 `shared`，但不依赖页面或 widget。
- `widgets` 可以组合多个 feature 与 entity，形成页面区域。
- `pages` 只负责页面装配、路由参数、布局组合和 provider 注入。
- 跨 slice 引用必须走 `index.js` 公共出口，禁止深层路径互相导入。

## 技术栈与工具链改造

### Package 与 Vite

- 新增 React 运行时依赖：`react`、`react-dom`、`zustand`。
- 新增构建插件：`@vitejs/plugin-react`。
- 引入 Tailwind CSS v4，使用 `@import "tailwindcss";` 或官方 Vite 插件，按实际版本验证后固定一种方式。
- Vite 入口从 `vue-app/main.js` 迁移到 `src/app/main.jsx`。
- `index.html` 最终只加载 `src/app/main.jsx` 和 `src/shared/styles/tailwind.css`。

### Vitest

- `vitest.config.js` include 扩展为：

```js
[
  "vue-app/**/*.test.js",
  "src/**/*.test.{js,jsx}",
]
```

- React 组件测试使用 `@testing-library/react`。
- Store 和 reducer 测试保持 node/jsdom 均可运行；涉及 DOM 的测试使用 jsdom。
- 迁移期保留 Vue 测试，确保旧行为可对照。

### Size Guard

- `scripts/size-guard.cjs` 扩展扫描目录和扩展名：
  - `vue-app/**/*.js`
  - `src/**/*.{js,jsx}`
- React 代码同样执行文件行数、函数行数、嵌套深度、nested ternary、inline conditional object spread 等 guard。
- 禁止通过更新 baseline 或放宽阈值绕过问题。

### 入口切换策略

迁移期间入口分三步走，避免一次切换导致不可回归：

1. **并行入口准备**：新增 `src/app/main.jsx`，但 `index.html` 仍指向 `vue-app/main.js`。此时只运行 React 单元测试和 isolated component tests。
2. **开发开关验证**：新增临时 query flag，例如 `?react=unified-chat`，仅在 dev server 下渲染 React UnifiedChat sandbox；该 flag 不进入生产默认路径。
3. **正式入口切换**：React UnifiedChat 的行为测试、size guard、build 通过后，把 `index.html` script 切到 `src/app/main.jsx`，由 React AppShell 装配已迁移页面；未迁移页面可以暂时显示明确的“迁移中不可用”阻断状态，不能静默落回旧 Vue。

正式切换后不允许在同一页面内长期混用 Vue/React。旧 Vue 文件只能作为回归对照，不能继续承载新功能。

### Tailwind 与 Token 落地

Tailwind 只作为 utility 编译器和设计 token 消费层，不替代设计系统边界：

- `src/shared/styles/tokens.css` 定义语义 token：背景、文本、边框、状态色、间距、阴影、半径、z-index。
- `src/shared/styles/themes.css` 定义 light/dark 或 runtime theme override；第一阶段只实现深色工作台。
- `src/shared/styles/tailwind.css` 引入 Tailwind，并把 token 映射给 utility。
- `shared/ui` 组件可以使用 Tailwind class，但业务组件优先组合 `shared/ui`，不直接重复按钮、badge、panel class。

建议 token：

| Token                 | 用途                          | 初始值方向                          |
| --------------------- | --------------------------- | ------------------------------ |
| `--sd-bg`             | 应用底色                        | 接近黑但避免纯黑大面积疲劳                  |
| `--sd-surface`        | 面板底色                        | 比底色略亮                          |
| `--sd-surface-raised` | 工具栏、浮层                      | 用于可交互区域                        |
| `--sd-border`         | 默认边框                        | 低对比细线                          |
| `--sd-border-strong`  | active / selected           | 高对比边框                          |
| `--sd-accent`         | 主操作                         | 冷色点缀，不铺满背景                     |
| `--sd-warning`        | warning log / token warning | 黄色语义色                          |
| `--sd-danger`         | error / destructive action  | 红色语义色                          |
| `--sd-success`        | completed / healthy         | 绿色语义色                          |
| `--sd-shadow-hard`    | 克制硬阴影                       | 只用于 active panel、floating dock |

### 架构导入守卫

第一阶段可先用 Vitest 静态测试守住依赖方向，后续再接入架构 linter：

- `shared/**` 禁止导入 `entities`、`features`、`widgets`、`pages`、`app`。
- `entities/**` 禁止导入 `features`、`widgets`、`pages`、`app`。
- `features/**` 禁止导入 `widgets`、`pages`、`app`。
- `widgets/**` 禁止导入 `pages`、`app`。
- 跨 slice 禁止深路径导入，只允许导入目标 slice 的 `index.js`。

测试文件建议命名：`src/shared/test/architecture-boundaries.test.js`。

## 后端业务逻辑映射

### 启动链路

React `app/bridge/bootstrapRuntime.js` 必须保持当前启动顺序：

```text
App mount
  -> subscribe bridge-event / agent-event / app-will-quit
  -> config/read
  -> ui/windowBootstrap/get
  -> ui/projects/get
  -> project active cwd hydrate
  -> ui/sidebar/get { cwd }
  -> active page bootstrap
```

要求：

- 事件订阅必须早于异步 bootstrap，避免启动时 runtime event 丢失。
- `ui/projects/get`、`ui/sidebar/get`、`ui/state/get`、`ui/preferences/*` 全部显式传 `cwd`。
- bootstrap 任一步失败都进入 fail-fast error boundary 和 warning log，不渲染“空状态假成功”。

### Chat 首发链路

空线程首发必须保持后端 pending launch 语义：

```text
Composer submit
  -> send-message feature validates cwd and provider readiness
  -> thread/start { cwd, prompt, deferSpawn: true, launchIntentId, ... }
  -> useThreadStore records pending thread and optimistic user item
  -> turn/start { cwd, threadId, input, manualSkillSelection: false }
  -> backend SpawnIfNeeded launches provider with first-turn input
  -> bridge-event ui/thread/patch updates timeline/status/runtime
```

续发路径：

```text
Composer submit with active thread
  -> turn/start { cwd, threadId, input, manualSkillSelection: false }
  -> optimistic user timeline item
  -> live patch / snapshot reconciliation
```

错误规则：

- 缺少 active project cwd 或 window cwd：阻断发送，写入 warning log，composer 保留草稿。
- `thread/start` 成功但 `turn/start` 失败：保留草稿和错误状态，不静默清空输入。
- selection stale：显式提示当前线程已变化，不把消息投递到未知线程。
- provider capability 不支持 `message_send`、`context_compact` 等动作时，按钮禁用并展示明确原因。

### Runtime 同步链路

Zustand `useThreadStore` 需要保留三类状态入口：

1. Snapshot：
   - `ui/state/get { cwd, threadId, includeDiff, knownDiffRevision }`
   - `ui/sidebar/get { cwd }`
2. Live patch：
   - `ui/thread/patch`
   - `ui/timeline/appended`
   - `ui/tokens/updated`
   - `ui/projection/updated`
3. Local optimistic state：
   - pending launch marker
   - optimistic user message
   - local selection
   - composer draft restore marker

Patch reducer 要求：

- 按 `threadId` 和 `sequence` 做顺序校验。
- 发现 sequence gap 时记录 `patch_gap` warning，并触发一次明确的 `ui/state/get` repair sync。
- remote timeline 到达后移除对应 optimistic item，避免重复消息。
- snapshot 不得覆盖 dirty local selection，除非后端明确返回 active thread 变化。

### Dashboard / DAG / Skill / Memory

- `dashboard/prompts` 和 `dashboard/skills` 必须带 `cwd`。
- `ui/dashboard/get { page, cwd }` 继续作为 dashboard 页面统一读取入口。
- DAG detail 继续使用 `dashboard/dagDetail`、`dashboard/dagRuns`、`dagStart`、`dagTerminate`、`dagDelete`、`dagApplyOps` 等现有方法。
- Skill 管理页不回到聊天内技能注入模式；聊天发送固定 `manualSkillSelection:false`。
- Memory 页面继续通过 `ui/memory/get` 与 shared-file 读取接口展示最终产物和共享文件。

### RPC 契约表

| 前端能力 | RPC 方法 | 必填参数 | 成功后写入 | 失败处理 |
| --- | --- | --- | --- | --- |
| 读取配置 | `config/read` | 无业务参数 | app runtime config | ErrorBoundary + warning log |
| 读取窗口启动快照 | `ui/windowBootstrap/get` | window context | project/window scope | ErrorBoundary + warning log |
| 项目列表 | `ui/projects/get` | `cwd` if scoped | `projectStore.projects` | 阻断依赖项目的页面 |
| 设置 active project | `ui/projects/setActive` | `cwd`, `path` | `projectStore.active` | modal 显示错误，不改 active |
| 读取 sidebar | `ui/sidebar/get` | `cwd` | `threadStore.applySidebar` | warning log + retry action |
| 读取 thread state | `ui/state/get` | `cwd`, `threadId` | `threadStore.applySnapshot` | timeline 显示 sync failure |
| 创建线程 | `thread/start` | `cwd`, provider/model config | pending thread / active thread | composer 保留草稿 |
| 发送回合 | `turn/start` | `cwd`, `threadId`, `input` | optimistic item / turn status | composer 保留草稿，thread 标记 send failed |
| 中断回合 | `turn/interrupt` | `cwd`, `threadId` | interrupt pending 状态 | button 恢复可点并显示错误 |
| 压缩上下文 | `thread/compact/start` | `cwd`, `threadId` | compact pending 状态 | warning log + toast |
| 恢复线程 | `thread/recover` | `cwd`, `threadId` | recovery status | thread card 显示恢复失败 |
| 保存偏好 | `ui/preferences/set` | `cwd`, `key`, `value` | preference store | `writeErrorsByKey` + warning log |
| Diff 保存 | `ui/code/save` | `cwd`, path/content payload | diff panel notice | diff panel error state |
| 打开新窗口 | `ui/openNewWindow` | `cwd`, thread route payload | none | warning log + toast |
| 前端日志 | `ui/log` | entries batch | backend logger | 本地 log sink error，停止递归 flush |

### Bridge Event 契约表

| Event | 来源 | Store 入口 | UI 影响 |
| --- | --- | --- | --- |
| `ui/thread/patch` | uistate projection | `threadStore.applyThreadPatch` | timeline/status/diff/token/activity 增量刷新 |
| `ui/timeline/appended` | runtime timeline | `threadStore.appendTimelineItems` | ChatTimeline 追加项 |
| `ui/tokens/updated` | token projection | `threadStore.applyTokenUsage` | ContextUsageBanner 更新 |
| `ui/projection/updated` | state projection | `threadStore.markSyncRequired` | 触发 sidebar/state sync |
| `skills/changed` | skill module | `skillStore.markRevisionChanged` | Skills 页面刷新，聊天页不注入技能 |
| `dag/node/status_changed` | DAG runtime | `dagStore.applyNodeStatus` | DAG 列表和详情刷新 |
| files dropped | Wails frontend event | `attachFilesFeature.handleDrop` | Composer 附件列表更新 |
| app will quit | Wails lifecycle | `appRuntime.flushBeforeQuit` | flush logs / persist drafts |

### 状态机：线程发送

```text
idle
  -> validating
  -> starting_thread        # only when no selected thread
  -> sending_turn
  -> waiting_runtime_patch
  -> streaming
  -> terminal
```

失败分支：

- `validating -> failed_missing_cwd`
- `starting_thread -> failed_thread_start`
- `sending_turn -> failed_turn_start`
- `waiting_runtime_patch -> failed_sync_timeout`

每个失败态都必须记录：

- `operationId`
- `threadId` if available
- `cwd`
- `method`
- root cause message
- composer draft preservation status

## Zustand Store 设计

### `entities/thread/model/useThreadStore`

状态字段：

- `threads`
- `statuses`
- `interruptibleByThread`
- `statusHeadersByThread`
- `statusDetailsByThread`
- `overlayTextByThread`
- `overlayTypeByThread`
- `overlayPriorityByThread`
- `timelinesByThread`
- `diffTextByThread`
- `diffRevisionByThread`
- `tokenUsageByThread`
- `agentMetaById`
- `agentRuntimeById`
- `activityStatsByThread`
- `alertsByThread`
- `activeThreadId`
- `activeCmdThreadId`
- `mainAgentId`
- `mainAgentState`
- `patchSequenceByThread`
- `syncStateByThread`

Actions：

- `applySnapshot(snapshot, options)`
- `applySidebar(sidebar)`
- `applyThreadPatch(patch)`
- `appendOptimisticUserMessage(threadId, item)`
- `removeOptimisticMessage(threadId, optimisticId)`
- `setActiveThread(mode, threadId)`
- `markSyncRequired(threadId, reason)`
- `recordThreadAlert(threadId, alert)`

Selectors：

- `selectActiveThread(mode)`
- `selectTimeline(threadId)`
- `selectThreadStatus(threadId)`
- `selectCanInterrupt(threadId)`
- `selectCanCompact(threadId)`
- `selectDiffState(threadId)`
- `selectActivity(threadId)`

### `entities/project/model/useProjectStore`

状态字段：

- `projects`
- `active`
- `windowCwd`
- `scopeCwd`
- `modal`
- `browsing`

Actions：

- `hydrateProjects(response)`
- `setActiveProject(cwd)`
- `openProjectModal()`
- `selectProjectDir()`
- `addProject(path)`
- `removeProject(path)`
- `requireActionCwd(reason)`

`requireActionCwd()` 是唯一允许 feature 获取 action cwd 的入口；缺失时直接 throw。

### `entities/preference/model/usePreferenceStore`

状态字段：

- `preferenceScopeCwd`
- `viewPrefsChat`
- `viewPrefsCmd`
- `writeQueueByKey`
- `writeErrorsByKey`

Actions：

- `setPreferenceScopeCwd(cwd)`
- `hydratePreferences(preferences)`
- `persistPreference(key, value)`
- `syncLayoutPreference(mode, patch)`

要求：

- `persistPreference()` 必须带 `cwd`。
- 同 key 写入串行执行。
- 写入失败进入 `writeErrorsByKey` 和 warning log，不能吞掉错误。

### `entities/log/model/useLogStore`

状态字段：

- `entries`
- `warnings`
- `errors`
- `traceEvents`
- `bridgeQueue`
- `level`
- `filters`

Actions：

- `log(level, event, fields)`
- `warn(event, fields)`
- `error(event, fields)`
- `trace(event, fields)`
- `flushBridgeQueue()`
- `setFilter(filterPatch)`
- `exportLogBundle()`

保留当前日志能力：

- ring buffer 默认 600 条。
- bridge queue 默认 240 条。
- batch size 默认 24 条。
- debug / warn / error 可以发送到后端 `ui/log`，info 默认只保留前端本地。

### Store 更新原则

- Store action 只接收已经 validation 过的 payload，validation 位于 `entities/*/api` 或 `shared/api/rpc`。
- Reducer 必须是同步、可测试的纯状态变更；RPC 调用放在 feature controller 或 entity api。
- 所有跨 store 动作由 feature 编排，不允许 store 之间互相 import。
- selector 使用稳定引用或 shallow compare，避免 timeline streaming 时整页重渲染。
- 大 map 更新必须按 thread 粒度替换，避免每个 token delta 都复制全局大对象。

### Store 与 UI 订阅关系

| UI 区域 | 订阅 selector | 说明 |
| --- | --- | --- |
| ThreadRail | `selectThreadCards(mode, cwd)` | 只订阅卡片摘要、active id、pin/archive/filter。 |
| ChatToolbar | `selectThreadStatus(threadId)` | 只订阅 status header、capabilities、provider runtime。 |
| ChatTimeline | `selectTimeline(threadId)` | timeline item append 要尽量保持 item 引用稳定。 |
| ComposerDock | composer local state + `selectCanSend(threadId)` | 输入内容不进入 thread store。 |
| DiffPanel | `selectDiffState(threadId)` | diff text 和 revision 独立订阅，避免 timeline 重绘。 |
| ActivityPanel | `selectActivity(threadId)` | activity stats、alerts、token usage。 |
| WarningLogPanel | `selectFilteredWarnings(filters)` | log store 独立，避免业务 store 更新带动日志列表重绘。 |

## Wire Data 规则

在 `shared/lib/wire/ids.js` 建立统一规则：

- `threadId`、`turnId`、`agent_id`、`trace_id`、`requestId`、`launchIntentId` 一律作为 string 保存。
- 纳秒时间戳、19 位及以上的数字字符串不得使用 `Number()` 或 `parseInt()`。
- 小整数才允许通过 `assertSafeInteger()` 转成 number，例如 UI column count、line number、token count、percent、patch sequence。
- wire adapter 对后端 response 做 shape validation；不认识的关键字段直接报错。

建议 helper：

```js
export function requireStringId(value, fieldName) {
  if (typeof value !== "string" || value.length === 0) {
    throw new Error(`${fieldName} must be a non-empty string`);
  }
  return value;
}

export function assertSafeInteger(value, fieldName) {
  if (!Number.isSafeInteger(value)) {
    throw new Error(`${fieldName} must be a safe integer`);
  }
  return value;
}
```

### Validation 分层

| 层 | 负责校验 | 不负责 |
| --- | --- | --- |
| `shared/api/rpc` | params 是 object、method 非空、requestId、RPC response envelope | 业务字段语义 |
| `entities/*/api` | response shape、必填字段、id 类型、cwd presence | UI 展示 |
| `features/*/model` | 用户动作前置条件，例如 selected thread、capability、draft 非空 | wire shape |
| `widgets/*/ui` | required props、可访问交互状态 | RPC 调用 |
| `pages/*` | route/page scope 是否齐全 | 业务动作细节 |

错误消息格式建议：

```text
<layer>.<domain>.<operation>: <field/reason>
```

例子：

- `feature.sendMessage.validate: cwd is required`
- `entity.thread.patch: sequence gap for thread th_123`
- `shared.rpc.callAPI: params for turn/start must be an object`

## UI / UX 方案

### 整体风格

采用“克制工作台”风格：

- 深色高密度开发工具布局。
- 使用硬边框、细阴影和语义色做状态点缀。
- 不做 landing page、hero、营销式大卡片布局。
- 不做卡片套卡片。
- 组件边角默认不超过 8px。
- 图标按钮优先使用 lucide-react，文本按钮只用于明确命令。
- 长文本必须有稳定宽度、截断或换行策略，不能挤压工具栏和状态栏。

### App Shell

布局：

```text
┌──────────────────────────────────────────────────────────────┐
│ Top toolbar: project, route tabs, runtime status, log level   │
├──────────────┬──────────────────────────────┬────────────────┤
│ Navigation   │ Main workspace               │ Inspector      │
│ Thread rail  │ Timeline / Diff / Composer   │ Activity/Logs  │
│ Filters      │                              │ Trace          │
└──────────────┴──────────────────────────────┴────────────────┘
```

要求：

- 左侧导航和 thread rail 可调整宽度，宽度写入 `ui/preferences/set { cwd }`。
- 右侧 inspector 可折叠，折叠状态写入 preference。
- 中央 composer dock 固定在工作区底部，timeline 可独立滚动。
- Activity、Warnings、Trace 使用 tabs 或 segmented control 切换。

### UnifiedChat 页面

保留并强化以下交互：

- 新线程首发。
- 续发。
- interrupt。
- compact。
- recover。
- fork。
- rename。
- archive / unarchive。
- open new window。
- thread config。
- drag and drop attachments。
- paste image attachments。
- citation / file ref preview。
- diff preview and save/open/locate actions。

关键 UI 状态：

- Pending launch：线程卡片和 toolbar 都显示等待 provider 启动。
- Provider capability：不可用动作禁用并有 tooltip 原因。
- Sync repair：patch gap 或 snapshot repair 时显示非阻断状态。
- Token usage：阈值化展示 normal / warning / danger。
- Diff stale：revision mismatch 时显示正在同步或同步失败。
- Send failure：composer 上方保留明确错误条，草稿不丢失。

### 关键区域尺寸与响应式规则

| 区域 | Desktop 默认 | 最小值 | 最大值 | 持久化 |
| --- | --- | --- | --- | --- |
| App nav | 56px | 56px | 72px | 不持久化 |
| Thread rail | 320px | 240px | 520px | `ui/preferences/set` |
| Inspector | 360px | 280px | 520px | `ui/preferences/set` |
| Composer dock | 内容自适应 | 96px 高 | 40vh | draft 本地状态 |
| Timeline | 填满剩余高度 | 320px | 无 | scroll anchor local |
| Diff panel | split ratio 默认 0.42 | 280px | 70% | `ui/preferences/set` |

响应式规则：

- 窄屏下 inspector 默认折叠，warning log 可从 toolbar icon 打开 drawer。
- Thread rail 在窄屏下变为 overlay panel，但 active thread 和 composer 不丢失。
- Toolbar 文案不足时优先保留图标、状态点和 tooltip，长 provider/model 文本截断。
- Composer buttons 使用稳定 36px 或 40px icon button，hover/loading 不改变布局尺寸。

### 主要 `data-testid` 保留清单

| 测试锚点 | 新组件归属 | 用途 |
| --- | --- | --- |
| `chat-page` | `pages/unified-chat` | 页面根节点。 |
| `chat-toolbar` | `widgets/chat-workspace` | 当前线程状态和动作入口。 |
| `provider-toggle` | `features/configure-thread` | provider/mode 控制。 |
| `thread-rail` | `widgets/thread-rail` | 左侧线程栏根节点。 |
| `thread-list` | `widgets/thread-rail` | thread cards 列表。 |
| `thread-empty-state` | `widgets/thread-rail` | 无线程状态。 |
| `chat-empty-state` | `widgets/chat-workspace` | 无 active thread 状态。 |
| `composer-bar` | `widgets/composer-dock` | composer 根节点。 |
| `composer-input` | `widgets/composer-dock` | 文本输入。 |
| `composer-attach-button` | `features/attach-files` | 附件选择。 |
| `composer-send-button` | `features/send-message` | 发送动作。 |
| `composer-compact-button` | `features/compact-thread` | 上下文压缩。 |
| `context-usage-banner` | `widgets/activity-panel` | token usage 提示。 |
| `thread-config-model-select` | `features/configure-thread` | model 设置。 |
| `thread-config-effort-select` | `features/configure-thread` | effort 设置。 |

### 可访问性要求

- Icon-only button 必须有 `aria-label` 和 tooltip。
- Dialog 和 drawer 必须支持 Escape 关闭、focus trap、初始 focus 和返回 focus。
- Warning/error 文本使用 `role="status"` 或 `role="alert"`，按严重性选择。
- Streaming timeline 更新不得抢占输入焦点。
- Keyboard shortcuts 只在 composer 未聚焦或明确组合键时生效，避免吞掉用户输入。

### Warning Log Panel

新增 `widgets/warning-log-panel`，作为右侧 inspector 的一部分。

显示事件：

- RPC failed。
- Missing cwd。
- Invalid payload。
- Patch sequence gap。
- Snapshot repair failed。
- Preference write failed。
- Bridge runtime load failed。
- Provider capability mismatch。
- Diff sync failed。
- Unexpected response shape。

过滤维度：

- level。
- route。
- RPC method。
- threadId。
- agentId。
- requestId。
- operationId。
- event type。

交互：

- Copy selected log。
- Export log bundle。
- Jump to thread。
- Clear local view filter。
- Toggle debug visibility。

高频事件策略：

- scroll、sidebar polling、routine sync heartbeat 默认 debug。
- repeated same warning 使用 count 聚合，不刷屏。
- warn/error 不自动消失，直到用户切换页面或清除过滤。

## 日志追踪机制

### Trace ID 模型

每个用户动作生成 `operationId`：

```text
user click/send
  -> operationId
  -> RPC requestId
  -> backend bridge event
  -> patch sequence
  -> UI state reducer
  -> render marker
```

RPC trace fields：

- `requestId`
- `operationId`
- `method`
- `cwd`
- `route`
- `startedAt`
- `durationMs`
- `status`
- `threadId`
- `turnId`

Bridge event trace fields：

- `eventType`
- `source`
- `threadId`
- `agentId`
- `sequence`
- `receivedAt`
- `operationId` if available

Store reducer trace fields：

- `store`
- `action`
- `threadId`
- `beforeRevision`
- `afterRevision`
- `patchSequence`
- `changedKeys`

### Logger API

前端统一使用 `shared/api/tracing` 与 `entities/log`：

```js
logger.info("thread.send.start", fields)
logger.warn("thread.patch.gap", fields)
logger.error("rpc.failed", fields)
logger.trace("store.thread.applyPatch", fields)
```

要求：

- 不允许组件直接 `console.warn()` 或 `console.error()` 作为业务日志。
- dev-only console mirror 可以保留，但必须由 logger 统一控制。
- 后端 `ui/log` sink failure 要进入本地 warning log，不能递归刷爆队列。

### 日志事件分类

| Event | Level | 必填字段 | 展示位置 |
| --- | --- | --- | --- |
| `app.bootstrap.start` | debug | `route`, `cwd` | trace only |
| `app.bootstrap.failed` | error | `route`, `cwd`, `cause` | WarningLog + ErrorBoundary |
| `rpc.start` | debug | `requestId`, `method`, `cwd` | trace only |
| `rpc.done` | debug | `requestId`, `method`, `durationMs` | trace only |
| `rpc.failed` | error | `requestId`, `method`, `cwd`, `cause` | WarningLog |
| `thread.send.start` | info | `operationId`, `threadId`, `cwd` | trace |
| `thread.send.failed` | error | `operationId`, `threadId`, `cwd`, `cause` | Composer notice + WarningLog |
| `thread.patch.gap` | warn | `threadId`, `expected`, `actual` | WarningLog + sync badge |
| `thread.snapshot.repair.failed` | error | `threadId`, `cwd`, `cause` | WarningLog |
| `preference.write.failed` | warn | `key`, `cwd`, `cause` | WarningLog + settings notice |
| `bridge.runtime.failed` | error | `route`, `cause` | ErrorBoundary |
| `log.flush.failed` | warn | `batchSize`, `cause` | local WarningLog only |

### Log Bundle 导出结构

`WarningLogPanel` 的 export 产物建议为 JSON：

```json
{
  "exported_at": "2026-05-29T00:00:00.000Z",
  "route": "chat",
  "cwd": "/abs/project",
  "active_thread_id": "th_123",
  "log_level": "debug",
  "entries": [],
  "trace_events": [],
  "runtime": {
    "client_kind": "wails",
    "client_route": "chat"
  }
}
```

导出动作只读取本地 ring buffer，不触发额外后端 RPC，避免故障期间导出也失败。

## 迁移计划

### Task 1: 写入工具链基础

**Files:**

- Modify: `cmd/agent-terminal/frontend/package.json`
- Modify: `cmd/agent-terminal/frontend/vite.config.js`
- Modify: `cmd/agent-terminal/frontend/vitest.config.js`
- Modify: `cmd/agent-terminal/frontend/jsconfig.json`
- Modify: `cmd/agent-terminal/frontend/scripts/size-guard.cjs`

**Steps:**

- [ ] 新增 React、React DOM、Zustand、Tailwind 和 Testing Library 依赖。
- [ ] 配置 React plugin。
- [ ] 扩展测试和 size guard 到 `src/**/*.{js,jsx}`。
- [ ] 保留 Vue 入口，直到 UnifiedChat React 纵切具备可运行入口。
- [ ] 验证旧 Vue 测试仍能运行。

### Task 2: 建立 `src/shared`

**Files:**

- Add: `src/shared/api/**`
- Add: `src/shared/ui/**`
- Add: `src/shared/layout/**`
- Add: `src/shared/styles/**`
- Add: `src/shared/lib/**`

**Steps:**

- [ ] 搬迁并重写 bridge adapter，保持 `callAPI(method, params)` object params 契约。
- [ ] 新增 request tracing wrapper。
- [ ] 新增 wire validation helpers。
- [ ] 建立基础 UI 组件和 layout primitives。
- [ ] 建立 tokens、themes、tailwind 入口。

### Task 3: 建立实体 store

**Files:**

- Add: `src/entities/thread/**`
- Add: `src/entities/project/**`
- Add: `src/entities/preference/**`
- Add: `src/entities/log/**`
- Add: `src/entities/turn/**`

**Steps:**

- [ ] 先用测试锁定 `thread/start -> turn/start` 首发语义。
- [ ] 实现 `useThreadStore` snapshot/live patch reducer。
- [ ] 实现 `useProjectStore.requireActionCwd()`。
- [ ] 实现 `usePreferenceStore.persistPreference()` 串行写入和显式错误状态。
- [ ] 实现 `useLogStore` warning/error/trace ring buffer。

### Task 4: 迁移 UnifiedChat 纵切

**Files:**

- Add: `src/pages/unified-chat/**`
- Add: `src/widgets/thread-rail/**`
- Add: `src/widgets/chat-workspace/**`
- Add: `src/widgets/composer-dock/**`
- Add: `src/widgets/activity-panel/**`
- Add: `src/widgets/diff-panel/**`
- Add: `src/widgets/warning-log-panel/**`
- Add: `src/features/send-message/**`
- Add: `src/features/interrupt-turn/**`
- Add: `src/features/compact-thread/**`
- Add: `src/features/recover-thread/**`

**Steps:**

- [ ] 页面只装配 `AppShell`、`ThreadRail`、`ChatWorkspace`、`ComposerDock`、`Inspector`。
- [ ] `send-message` feature 复刻现有 `performSend()` 的后端调用顺序。
- [ ] `ComposerDock` 保留附件、草稿、fork draft、send failure notice。
- [ ] `ThreadRail` 保留 pin、archive、filter、active selection。
- [ ] `ChatWorkspace` 保留 timeline、status header、file ref preview。
- [ ] `DiffPanel` 保留 diff revision sync、save/open/locate。
- [ ] `WarningLogPanel` 接入 log store 和 trace filters。

### Task 5: 迁移 dashboard 页面

**Files:**

- Add: `src/pages/commands/**`
- Add: `src/pages/dags/**`
- Add: `src/pages/skills/**`
- Add: `src/pages/memory/**`
- Add: `src/pages/settings/**`

**Steps:**

- [ ] Commands 读取 `ui/dashboard/get { page:"commands", cwd }`。
- [ ] Dags 读取和操作现有 dashboard DAG RPC。
- [ ] Skills 保持 provider-native mirror 管理，不回到聊天页技能选择器。
- [ ] Memory 保持 final output 与 shared files 读取链路。
- [ ] Settings 保持 provider、prompt、tool scope、preferences 语义。

### Task 6: 切换入口并清理 Vue

**Steps:**

- [ ] React UnifiedChat 测试和 build 通过后，把 `index.html` 入口切到 `src/app/main.jsx`。
- [ ] 确认 Wails dist build 正常。
- [ ] 逐页移除对应 Vue 页面和测试。
- [ ] 最后删除 Vue vendor alias 和 `vue-app` 入口。

## 详细执行批次

### Batch 0: 基线与保护网

**目标：** 在写 React 代码前确认旧行为可测，避免迁移期间不知道是旧问题还是新问题。

**Files:**

- Modify: `cmd/agent-terminal/frontend/vitest.config.js`
- Modify: `cmd/agent-terminal/frontend/scripts/size-guard.cjs`
- Add: `cmd/agent-terminal/frontend/src/shared/test/architecture-boundaries.test.js`

**Steps:**

- [ ] 运行 `cd cmd/agent-terminal/frontend && node scripts/size-guard.cjs`，记录迁移前 size guard 状态。
- [ ] 运行与 UnifiedChat 相关的 Vue 测试：

```bash
cd cmd/agent-terminal/frontend
npx vitest run \
  vue-app/use-thread-actions.test.js \
  vue-app/thread-store.runtime-thread-patch.test.js \
  vue-app/thread-store.runtime-sync.test.js \
  vue-app/composer-bar.behavior.test.js \
  vue-app/unified-chat-component.test.js \
  vue-app/diff-panel.test.js
```

- [ ] 扩展 Vitest include，使 `src/**/*.test.{js,jsx}` 可被发现。
- [ ] 扩展 size guard 扫描 `src/**/*.{js,jsx}`。
- [ ] 新增架构边界测试，先覆盖 `shared`、`entities`、`features`、`widgets`、`pages` 的禁止反向导入规则。

**验收：**

- 旧 UnifiedChat 相关测试可以独立运行。
- 新增空 `src` 目录不会破坏现有 build。
- 架构边界测试在没有违规导入时通过。

### Batch 1: Shared API 与 Wire Validation

**目标：** 先建立所有 React 业务代码必须走的 bridge 和 validation 层。

**Files:**

- Add: `src/shared/api/rpc/callAPI.js`
- Add: `src/shared/api/rpc/requestTrace.js`
- Add: `src/shared/api/events/runtimeEvents.js`
- Add: `src/shared/lib/wire/ids.js`
- Add: `src/shared/lib/assert/invariant.js`
- Add: `src/shared/api/rpc/callAPI.test.js`
- Add: `src/shared/lib/wire/ids.test.js`

**Steps:**

- [ ] 写 `ids.test.js`，覆盖 string id、19 位数字字符串、unsafe integer、empty id。
- [ ] 实现 `requireStringId()`、`assertSafeInteger()`、`requireObjectPayload()`。
- [ ] 写 `callAPI.test.js`，覆盖 params 非 object 时 fail-fast。
- [ ] 写 `callAPI.test.js`，覆盖 requestId 注入和 RPC failed 保留 root cause。
- [ ] 实现 `callAPI(method, params, traceContext)`，内部委托 Wails bridge。
- [ ] 实现 runtime event subscription wrapper，保留 `bridge-event` 与 `agent-event` 两条入口。

**验收：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/shared/lib/wire/ids.test.js src/shared/api/rpc/callAPI.test.js
```

### Batch 2: Log Store 与 Warning Log

**目标：** 先把 warning/error/trace 接住，为后续迁移提供可观测性。

**Files:**

- Add: `src/entities/log/model/useLogStore.js`
- Add: `src/entities/log/lib/logEventSchema.js`
- Add: `src/widgets/warning-log-panel/ui/WarningLogPanel.jsx`
- Add: `src/widgets/warning-log-panel/model/useWarningLogFilters.js`
- Add: `src/entities/log/model/useLogStore.test.js`
- Add: `src/widgets/warning-log-panel/ui/WarningLogPanel.test.jsx`

**Steps:**

- [ ] 写 log store 测试，验证 ring buffer 600 条裁剪。
- [ ] 写 bridge queue 测试，验证 queue limit 240 和 batch size 24。
- [ ] 写 warning filter 测试，覆盖 level、method、threadId、operationId。
- [ ] 实现 `useLogStore`。
- [ ] 实现 `WarningLogPanel`，保留 filter、copy、export、jump callback。
- [ ] 将 `shared/api/rpc` 的 failed event 接入 log store。

**验收：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/entities/log/model/useLogStore.test.js src/widgets/warning-log-panel/ui/WarningLogPanel.test.jsx
```

### Batch 3: Thread Entity Store

**目标：** 把 runtime snapshot、sidebar、patch、optimistic timeline 迁移为可测 reducer。

**Files:**

- Add: `src/entities/thread/model/useThreadStore.js`
- Add: `src/entities/thread/model/threadReducers.js`
- Add: `src/entities/thread/model/threadSelectors.js`
- Add: `src/entities/thread/api/threadApi.js`
- Add: `src/entities/thread/lib/timelineMerge.js`
- Add: `src/entities/thread/model/threadReducers.test.js`
- Add: `src/entities/thread/lib/timelineMerge.test.js`

**Steps:**

- [ ] 写 snapshot hydrate 测试，输入 `UIState` fixture，断言 threads、statuses、timeline、diff、tokens 写入正确。
- [ ] 写 patch apply 测试，输入 `UIThreadPatch` fixture，断言 sequence、status、timeline、alerts 更新。
- [ ] 写 patch gap 测试，sequence 从 1 跳到 3 时标记 repair required 并写 warning event。
- [ ] 写 optimistic merge 测试，remote user item 到达后移除 matching optimistic item。
- [ ] 实现纯 reducer。
- [ ] 实现 Zustand store facade。
- [ ] 实现 selectors，避免 UI 直接读取大 state。

**验收：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/entities/thread/model/threadReducers.test.js src/entities/thread/lib/timelineMerge.test.js
```

### Batch 4: Project / Preference / Composer 基础状态

**目标：** 完成发送链路依赖的 cwd、偏好和输入状态。

**Files:**

- Add: `src/entities/project/model/useProjectStore.js`
- Add: `src/entities/preference/model/usePreferenceStore.js`
- Add: `src/widgets/composer-dock/model/useComposerStore.js`
- Add: `src/features/attach-files/model/attachFiles.js`
- Add: corresponding tests under each package

**Steps:**

- [ ] 写 `requireActionCwd()` 测试，缺失 cwd 直接 throw，错误消息包含 action reason。
- [ ] 写 preference serial queue 测试，同 key 写入按顺序执行。
- [ ] 写 preference failure 测试，RPC 失败进入 `writeErrorsByKey` 和 warning log。
- [ ] 写 composer draft restore 测试，发送失败后 text 和 attachments 保留。
- [ ] 实现 project store。
- [ ] 实现 preference store。
- [ ] 实现 composer store 和 attach-files feature。

**验收：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run \
  src/entities/project/model/useProjectStore.test.js \
  src/entities/preference/model/usePreferenceStore.test.js \
  src/widgets/composer-dock/model/useComposerStore.test.js
```

### Batch 5: Send Message Feature

**目标：** 精确复刻当前 `performSend()` 的后端调用顺序。

**Files:**

- Add: `src/features/send-message/model/sendMessageController.js`
- Add: `src/features/send-message/model/sendMessageController.test.js`
- Add: `src/entities/turn/api/turnApi.js`

**Test cases:**

- [ ] 无 active thread、有 cwd、有文本：先 `thread/start`，再 `turn/start`。
- [ ] 无 active thread、空文本但有附件：允许创建 pending thread，再发送附件 input。
- [ ] 有 active thread：跳过 `thread/start`，直接 `turn/start`。
- [ ] missing cwd：不调用任何 RPC，composer draft 保留，warning log 有 `missing_cwd`。
- [ ] `thread/start` failed：不调用 `turn/start`。
- [ ] `turn/start` failed：保留 composer draft，记录 `thread.send.failed`。
- [ ] stale selected thread：阻断发送并提示 selection changed。
- [ ] payload 固定包含 `manualSkillSelection:false`。

**验收：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/features/send-message/model/sendMessageController.test.js
```

### Batch 6: UnifiedChat Widgets

**目标：** 用 React 组件替换 UnifiedChat 可见主链路。

**Files:**

- Add: `src/pages/unified-chat/UnifiedChatPage.jsx`
- Add: `src/widgets/thread-rail/ui/ThreadRail.jsx`
- Add: `src/widgets/chat-workspace/ui/ChatWorkspace.jsx`
- Add: `src/widgets/chat-workspace/ui/ChatTimeline.jsx`
- Add: `src/widgets/composer-dock/ui/ComposerDock.jsx`
- Add: `src/widgets/activity-panel/ui/ActivityPanel.jsx`
- Add: `src/widgets/diff-panel/ui/DiffPanel.jsx`
- Add: React component tests for each widget

**Steps:**

- [ ] 先写 `UnifiedChatPage.test.jsx`，断言关键 `data-testid` 都存在。
- [ ] 写 `ComposerDock.test.jsx`，覆盖输入、附件按钮、发送按钮 disabled/loading。
- [ ] 写 `ThreadRail.test.jsx`，覆盖 active、pin、archive、empty state。
- [ ] 写 `ChatTimeline.test.jsx`，覆盖 user/assistant/internal/status item。
- [ ] 写 `DiffPanel.test.jsx`，覆盖 stale revision 和 save failure。
- [ ] 实现组件，所有按钮使用 `shared/ui`。
- [ ] 接入 selectors 和 feature actions。

**验收：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run \
  src/pages/unified-chat/UnifiedChatPage.test.jsx \
  src/widgets/thread-rail/ui/ThreadRail.test.jsx \
  src/widgets/composer-dock/ui/ComposerDock.test.jsx \
  src/widgets/chat-workspace/ui/ChatTimeline.test.jsx \
  src/widgets/diff-panel/ui/DiffPanel.test.jsx
```

### Batch 7: App Shell 与 Bootstrap

**目标：** 让 React App 能以真实 backend contract 启动。

**Files:**

- Add: `src/app/main.jsx`
- Add: `src/app/App.jsx`
- Add: `src/app/providers/AppProviders.jsx`
- Add: `src/app/providers/ErrorBoundary.jsx`
- Add: `src/app/bridge/bootstrapRuntime.js`
- Add: `src/widgets/app-shell/ui/AppShell.jsx`

**Steps:**

- [ ] 写 bootstrap 测试，断言先订阅 runtime event，再调用 `config/read`。
- [ ] 写 bootstrap 测试，断言 `ui/sidebar/get` 带 cwd。
- [ ] 写 error boundary 测试，bootstrap failed 时显示错误并写 warning log。
- [ ] 实现 `bootstrapRuntime()`。
- [ ] 实现 `AppShell` 三栏布局。
- [ ] 接入 UnifiedChat page。

**验收：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/app/bridge/bootstrapRuntime.test.js src/app/providers/ErrorBoundary.test.jsx
```

### Batch 8: 入口切换和清理

**目标：** 在 React UnifiedChat 验证通过后切换默认入口。

**Files:**

- Modify: `cmd/agent-terminal/frontend/index.html`
- Modify: `cmd/agent-terminal/frontend/vite.config.js`
- Modify: `cmd/agent-terminal/frontend/jsconfig.json`

**Steps:**

- [ ] `index.html` script 切到 `/src/app/main.jsx`。
- [ ] 删除不再需要的 Vue alias 只在最后一批做。
- [ ] `jsconfig.json` include 加入 `src/**/*`。
- [ ] 运行完整前端验证。

**验收：**

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

## 测试计划

### Unit Tests

- `shared/api/rpc`：
  - object params validation。
  - requestId/operationId 注入。
  - RPC failed 写入 log store 并抛出原始错误。
- `shared/lib/wire`：
  - 19 位 id 保持 string。
  - unsafe integer 转换失败。
  - missing id fail-fast。
- `entities/thread`：
  - snapshot hydrate。
  - live patch apply。
  - patch gap repair marker。
  - optimistic user item remote 到达后去重。
- `features/send-message`：
  - 空线程先 `thread/start` 再 `turn/start`。
  - `deferSpawn:true` 保留。
  - missing cwd 阻断。
  - `turn/start` 失败保留 composer draft。
- `entities/preference`：
  - 同 key 写入串行。
  - missing cwd 阻断。
  - write failure 进入 warning log。

### Component Tests

- UnifiedChat page renders with existing `data-testid` anchors。
- Composer send / attach / paste / interrupt / compact buttons。
- Thread rail selection、pin、archive、filter。
- Timeline status、streaming、approval、citation actions。
- WarningLogPanel filter、copy、export、jump to thread。
- DiffPanel stale revision、sync failure、save/open/locate。

### E2E / Build Verification

迁移阶段每个纵切完成后运行：

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

涉及 Go/Wails embed 或后端 contract 时额外运行：

```bash
make guard
make build-plain
```

## Acceptance Criteria

- React `UnifiedChat` 能完成新线程首发、已有线程续发、interrupt、compact、recover、附件和 diff preview。
- 所有敏感 RPC payload 显式携带 `cwd`。
- `manualSkillSelection:false` 语义保留。
- Warning Log 能显示 RPC failed、missing cwd、patch gap、preference write failure。
- Trace 能从用户动作关联到 RPC、bridge event 和 store reducer。
- 旧 Vue 对应行为测试迁移到 React 后通过。
- `node scripts/size-guard.cjs`、`npx vitest run`、`npm run build` 通过。
- 不更新 size guard baseline，不放宽架构阈值。
- 不引入静默降级、默认 provider、空数组吞错或 browser-native 文件系统兜底。

## 明确不做

- 不改后端 RPC 方法名、参数结构或 event wire shape。
- 不在聊天页恢复手动技能选择器。
- 不全量引入 Mantine、Radix Themes 或外部重型 UI kit。
- 不一次性删除 `vue-app`。
- 不用 Tailwind class 堆满业务组件而绕过 `shared/ui` 和 tokens。
- 不为了通过测试而移除 `data-testid`。
- 不把 `agent_id`、`trace_id`、纳秒时间戳等 wire 字段转成 number。

## 后续视觉稿入口

本轮不产出 Figma 或 Canva 文件。若需要视觉稿，建议基于本方案单独建立：

- UnifiedChat desktop frame。
- Thread rail states。
- Composer dock states。
- Warning Log / Trace inspector。
- Dashboard page shared shell。

视觉稿必须遵守本方案的“克制工作台”风格，不做营销化首页或大面积装饰背景。
