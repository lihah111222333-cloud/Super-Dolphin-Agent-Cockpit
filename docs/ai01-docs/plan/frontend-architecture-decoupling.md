# 前端架构解耦与组件化调研

创建日期：2026-05-29
适用范围：`cmd/agent-terminal/frontend`

## 摘要

当前前端最需要解决的不是换一个 UI 库，而是先建立稳定的模块边界：页面只负责装配，业务动作沉到 `features`，领域数据沉到 `entities`，通用 UI、布局、样式变量沉到 `shared`。建议采用 `Feature-Sliced Design` 作为分层规则，参考 `bulletproof-react` 的 React/Vite 工程实践，用 `shadcn/ui` 的“源码内置组件库”思想建设本地 `shared/ui`，暂不全量迁移到 Mantine 或其他完整组件库。

## 当前问题判断

从当前仓库结构看，前端已经处在迁移期：`cmd/agent-terminal/frontend/src` 与旧 `vue-app` 的文件迁移并存，且页面、组件、composable、store、样式之间的职责边界不够清晰。

主要症状：

- 页面级文件承载过多职责：页面装配、桥接事件、数据派生、用户动作、样式状态混在一起。
- `components`、`composables`、`stores` 按技术类型堆放，业务归属不明显。
- 统一聊天相关逻辑跨度大，`UnifiedChatPage`、线程列表、聊天区域、工具栏、composer、活动状态、thread store 之间耦合明显。
- 样式文件按页面和历史模块散落，设计 token 与布局 primitive 没有形成强约束。
- 旧 Vue 兼容层、React 组件、Zustand store 同时存在，迁移期更需要边界和导入规则。

## 调研资源

| 资源 | 价值 | 在本项目中的用法 | 注意事项 |
| --- | --- | --- | --- |
| [bulletproof-react](https://github.com/alan2207/bulletproof-react) | React 生产级目录结构、API 层、状态、测试、错误处理范式 | 参考其 feature-first 组织方式、项目标准和可维护性约束 | 不要照搬模板，取其边界和约定 |
| [Feature-Sliced Design](https://fsd.how/) | 面向大型前端的分层架构方法，目标是让代码结构清晰稳定 | 作为本项目主架构：`app/pages/widgets/features/entities/shared` | 需要结合现有迁移状态渐进落地 |
| [Steiger](https://github.com/feature-sliced/steiger) | FSD 架构 linter，可检查目录和导入规则 | 后续可加入 CI，防止跨层乱导入和公共 API 破坏 | 当前处于 beta，先试运行再纳入强校验 |
| [shadcn/ui](https://github.com/shadcn-ui/ui) | 可复制、可改造、源码内置的组件体系 | 借鉴“组件源码归仓库所有”的方式建设 `shared/ui` | 不建议直接全量引入 Tailwind 化重写 |
| [Radix Themes](https://github.com/radix-ui/themes) | 可访问性和主题化较强的 React 组件库 | 作为可访问性、主题 token、基础组件状态设计参考 | 若引入会改变现有 CSS 体系，需单独评估 |
| [Mantine](https://github.com/mantinedev/mantine) | 成熟完整的 React 组件库，包含 hooks、表单、通知、命令面板等 | 可参考复杂组件能力和 hooks 设计 | 不建议作为当前第一阶段迁移目标，成本过高 |
| [Vercel AI Elements](https://github.com/vercel/ai-elements) | 面向 AI 聊天产品的组件范式，基于 shadcn/ui registry | 参考 Conversation、Message、CodeBlock 等 AI 原生组件拆分 | 其默认依赖 Next.js、Tailwind、AI SDK，不能直接套用 |
| [frontend-clean-architecture](https://github.com/bespoyasov/frontend-clean-architecture) | 前端 Clean Architecture 示例，强调 use case、adapter、domain 分离 | 参考复杂业务动作如何从 hook 中拆出可测试 use case | 对 UI 密集型桌面应用只应局部采用 |

## 推荐方向

### 架构结论

采用组合方案：

1. 用 Feature-Sliced Design 定义目录和依赖方向。
2. 用 bulletproof-react 补充 React/Vite 项目标准。
3. 用 shadcn/ui 思路建设本地可控的 `shared/ui`，而不是直接依赖外部组件黑盒。
4. 保留现有 CSS 变量和视觉资产，先治理 token、layout、组件边界，再谈整体换肤。

不建议第一阶段做的事：

- 不做全量重写。
- 不直接把 Mantine 或 Radix Themes 铺满全项目。
- 不把所有组件一次性迁到 TypeScript。
- 不为了目录漂亮而移动所有文件，必须按业务纵切迁移。
- 不引入新的静默兜底逻辑，所有数据缺失和配置异常继续 fail-fast。

## 目标目录结构

建议目标结构如下：

```text
cmd/agent-terminal/frontend/src/
  app/
    App.jsx
    main.jsx
    providers/
    bridge/

  pages/
    UnifiedChatPage.jsx
    CommandsPage.jsx
    DagsPage.jsx
    SettingsPage.jsx

  widgets/
    thread-rail/
      ui/
      model/
      index.js
    chat-workspace/
      ui/
      model/
      index.js
    activity-panel/
      ui/
      model/
      index.js
    composer-dock/
      ui/
      model/
      index.js

  features/
    send-message/
      model/
      ui/
      api/
      index.js
    rename-thread/
      model/
      ui/
      index.js
    thread-config/
      model/
      ui/
      index.js
    file-preview/
      model/
      ui/
      index.js
    select-project/
      model/
      ui/
      index.js

  entities/
    thread/
      api/
      model/
      lib/
      index.js
    project/
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

  shared/
    api/
      bridge/
      http/
    ui/
      button/
      card/
      dialog/
      tabs/
      tooltip/
      badge/
      empty-state/
    layout/
      app-shell/
      panel/
      split-pane/
      toolbar/
    styles/
      reset.css
      tokens.css
      themes.css
      typography.css
      layout.css
    lib/
      date/
      format/
      assert/
      dom/
```

## 依赖方向

推荐只允许从上层依赖下层：

```text
app -> pages -> widgets -> features -> entities -> shared
```

规则：

- `shared` 不依赖任何业务层。
- `entities` 可以依赖 `shared`，但不能依赖 `features` 或 `widgets`。
- `features` 可以组合一个业务动作，例如发送消息、重命名线程、修改线程配置。
- `widgets` 组合多个 features 和 entities，形成页面区域，例如线程栏、聊天工作区。
- `pages` 只做页面级装配、路由参数和整体布局。
- 跨 slice 引用必须走 `index.js` 公共出口，禁止深层路径互相乱导。

## 当前代码迁移映射

| 当前归类 | 建议归类 | 示例 |
| --- | --- | --- |
| `src/pages/UnifiedChatPage.jsx` | `pages/UnifiedChatPage.jsx` 只保留页面装配 | 把派生状态、动作控制器和大块 JSX 拆出 |
| `src/components/unified-chat/*` | `widgets/thread-rail`、`widgets/chat-workspace` | `ThreadRailSidePanel`、`WorkspaceChatPanel` |
| `src/components/ComposerBar.jsx` | `widgets/composer-dock` + `features/send-message` | 输入框 UI 和发送用例分离 |
| `src/composables/useThreadActions.js` | `features/*/model` 或 `entities/thread/model` | 按动作拆分，不继续堆大 hook |
| `src/stores/threads.js` | `entities/thread/model` + `entities/thread/api` | store facade 和领域派生选择器分离 |
| `src/services/api.js` | `shared/api/http` 或 `shared/api/bridge` | 桥接和 HTTP 调用集中 |
| `src/styles/tokens.css` | `shared/styles/tokens.css` | 作为全局设计 token 唯一入口 |
| 页面专属 CSS | `shared/layout` 或 widget 自有 CSS | 可复用布局进入 shared，业务样式留在 widget |

## 组件化策略

### shared/ui

`shared/ui` 只放无业务语义的基础组件：

- `Button`
- `IconButton`
- `Card`
- `Dialog`
- `Tabs`
- `Tooltip`
- `Badge`
- `Input`
- `Textarea`
- `EmptyState`
- `Spinner`

要求：

- 组件 API 稳定，不接受 thread、dag、skill 等业务对象。
- 使用 `variant`、`size`、`tone` 等有限枚举控制视觉。
- 样式变量来自 `shared/styles/tokens.css`。
- 所有交互态包含 hover、focus-visible、disabled、loading。
- 复杂组件必须有同包测试或至少有使用方回归测试。

### shared/layout

`shared/layout` 放应用级布局 primitive：

- `AppShell`
- `Panel`
- `SplitPane`
- `Toolbar`
- `Sidebar`
- `ScrollArea`

这些组件解决一致的布局约束，不解决业务问题。

### features

`features` 以用户动作命名，而不是以 UI 形状命名：

- `send-message`
- `rename-thread`
- `fork-thread`
- `thread-config`
- `file-preview`
- `select-project`
- `schedule-dag`

每个 feature 建议包含：

```text
feature-name/
  model/      # hook, controller, state transition
  ui/         # 该动作需要的控件或弹层
  api/        # 可选，动作专属 API adapter
  index.js    # 公共出口
```

### entities

`entities` 放领域模型和可复用选择器：

- thread
- project
- dag
- skill
- memory
- provider

每个 entity 可以包含：

```text
entity-name/
  model/      # store slice, selector, view model
  api/        # entity 相关 API adapter
  lib/        # 纯函数和格式化
  index.js
```

## 样式治理策略

### 样式分层

建议把样式分成四层：

1. `reset.css`：基础 reset，不含业务色彩。
2. `tokens.css`：颜色、间距、圆角、阴影、字体、z-index、动画时长。
3. `themes.css`：主题变量覆盖，例如 dark/light、高对比度。
4. 局部 CSS：widget 或 feature 内部样式，只消费 token。

### token 原则

所有颜色和布局常量应该先进入 token：

```css
:root {
  --color-bg-app: #0f1115;
  --color-bg-panel: #171a21;
  --color-border-subtle: rgba(255, 255, 255, 0.08);
  --space-2: 0.5rem;
  --radius-sm: 6px;
  --duration-fast: 120ms;
}
```

使用规则：

- 业务组件不直接写散落十六进制颜色，除非是一次性可视化图表色板。
- 页面布局不要重复定义 panel、toolbar、sidebar 的基础样式。
- 组件内部不使用全局页面 class 作为样式依赖。
- 避免单一色系支配整个界面，工作台类应用应该克制、密集、可扫描。

## 统一聊天页优先拆分方案

第一阶段建议只针对 `UnifiedChatPage` 做纵切，不碰所有页面。

目标：

- `UnifiedChatPage` 只保留页面装配。
- 线程列表成为 `widgets/thread-rail`。
- 聊天主体成为 `widgets/chat-workspace`。
- 输入区成为 `widgets/composer-dock`。
- 发送、重命名、线程配置、文件预览拆成 `features`。
- thread 相关 view model、selector、store facade 进入 `entities/thread`。

建议拆分后的页面形态：

```jsx
export default function UnifiedChatPage() {
  return (
    <ChatPageShell>
      <ThreadRail />
      <ChatWorkspace />
      <ActivityPanel />
      <ComposerDock />
    </ChatPageShell>
  );
}
```

页面不应该直接知道：

- 线程卡片如何排序。
- 消息如何流式 patch。
- composer 如何处理拖拽文件。
- thread config 如何保存。
- 活动统计如何聚合。

## 渐进迁移计划

### 第 0 步：建立边界文档和命名约定

输出：

- 本文档。
- `src/shared`、`src/entities`、`src/features`、`src/widgets` 空目录或首批目录。
- 一份导入规则说明。

验收：

- 不改变运行行为。
- 不触碰旧迁移文件。

### 第 1 步：抽出 shared/styles 和 shared/layout

输出：

- `shared/styles/tokens.css`
- `shared/styles/layout.css`
- `shared/layout/Panel`
- `shared/layout/Toolbar`
- `shared/layout/SplitPane`

验收：

- 现有页面视觉不回退。
- CSS 重复减少。
- size guard、vitest、build 通过。

### 第 2 步：抽出 shared/ui 基础组件

优先抽：

- Button
- IconButton
- Badge
- Dialog
- Tabs
- Tooltip
- EmptyState

验收：

- 只替换高复用、低业务语义组件。
- 不引入新 UI 库大面积改造。

### 第 3 步：UnifiedChatPage 纵切拆分

输出：

- `widgets/thread-rail`
- `widgets/chat-workspace`
- `widgets/composer-dock`
- `features/send-message`
- `features/rename-thread`
- `features/thread-config`
- `entities/thread`

验收：

- 原有聊天、流式更新、切换线程、发送、重命名、配置保存行为不变。
- 相关回归测试覆盖。
- 浏览器验证不再出现 Vue compat infinite loop 误报。

### 第 4 步：引入架构守卫

输出：

- 先以非阻塞方式试运行 Steiger 或自定义 import guard。
- 观察现有违规点。
- 分阶段把规则纳入 CI。

验收：

- 新代码不能继续制造跨层乱导入。
- 旧代码迁移期间允许白名单，但白名单必须逐步减少。

## 架构守卫建议

可以先写轻量规则：

- 禁止 `shared` 导入 `entities/features/widgets/pages/app`。
- 禁止 `entities` 导入 `features/widgets/pages/app`。
- 禁止跨 feature 深导入内部文件。
- 禁止页面直接导入 `stores/*`，必须通过 widget、feature 或 entity facade。
- 禁止新增大于阈值的页面文件。

后续再评估 Steiger：

```bash
npm i -D steiger @feature-sliced/steiger-plugin
npx steiger ./src
```

当前建议先试运行，不要立即作为硬门禁。

## 验证策略

每次前端重构必须至少跑：

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

如果只改文档，可不跑前端测试，但最终报告要明确说明。

关键回归点：

- 线程选择和刷新。
- 流式消息 patch。
- token usage 推送。
- composer 发送、停止、文件拖拽。
- thread config 保存。
- 线程重命名。
- ActivityPanel 和 alerts/stats 派生数据。
- Vue compat 桥接高频更新。

## 风险与应对

| 风险 | 表现 | 应对 |
| --- | --- | --- |
| 全量重写失控 | 改动巨大，测试无法锁住行为 | 按页面纵切，每次只迁一个业务区域 |
| 目录漂亮但逻辑更散 | 文件移动后职责仍混乱 | 先按 feature/use case 拆，不按组件形状拆 |
| UI 库迁移成本过高 | CSS 冲突、bundle 增大、视觉断层 | 第一阶段只建设本地 `shared/ui` |
| Store 继续膨胀 | Zustand store 成为全局上帝对象 | 通过 entity facade 和 selector 分层 |
| 兼容层误伤 | Vue compat 与 React bridge 高频触发 | 高频路径用测试锁住，不引入同步深 watch 回退 |
| 架构守卫过早变硬 | 大量旧代码违规导致 CI 失效 | 先 advisory，再逐步收紧 |

## 推荐第一张任务卡

任务名：统一聊天页第一阶段解耦

范围：

- 只处理 `UnifiedChatPage` 相关 UI 装配。
- 不改后端。
- 不引入新 UI 库。
- 不改变用户可见行为。

交付：

- 新增 `widgets/thread-rail`、`widgets/chat-workspace`、`widgets/composer-dock`。
- 新增 `features/send-message` 和 `features/rename-thread` 的第一版 facade。
- 页面文件减少直接业务逻辑。
- 保持现有测试通过，并补充至少一个页面装配回归测试。

完成标准：

- `UnifiedChatPage` 文件显著变薄。
- 线程列表、聊天工作区、输入区可以独立读懂和测试。
- 公共 UI 和布局开始收敛到 `shared`。
- 没有扩大现有迁移噪音。

## 一句话结论

先用 FSD 建边界，再用本地 shared UI 统一样式，最后用架构守卫防止代码重新变乱。
