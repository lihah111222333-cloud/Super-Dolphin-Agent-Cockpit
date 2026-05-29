# React + Zustand + Tailwind 前端架构重构方案 (Super-Dolphin Frontend Refactoring)


由于当前进入了纯重构期（不进行业务开发），我们将废弃原本的 Veaury 混跑策略，采取**彻底、直接的渐进式重构**，将 38,000+ 行 Vue 3 原生 ESM 代码整体迁移为标准的 **React 18 + Zustand + Tailwind CSS v4** 架构。

在重构过程中，必须保全 1394 个高价值单元测试（Vitest 运行），并严格遵守 [size-guard.cjs](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/scripts/size-guard.cjs) 体积守卫红线。

---

## 核心重构策略

### 1. 源码目录与构建重构
* **源目录命名规范**：将原 `vue-app` 目录重命名为 `src`，更符合 React 项目的主流规范。
* **工程编译流程**：引入 `@vitejs/plugin-react`。Vite 将原生支持 JSX 编译，生成高度优化的 React 应用包。
* **Tailwind CSS v4 整合**：移除冗余的 CSS 文件，利用 `@tailwindcss/vite` 或通过 `@import "tailwindcss";` 对 CSS 进行收拢，并在主体中直接消费统一的 Brutalist（粗野主义）设计变量（如粗黑描边和硬阴影）。

### 2. 状态管理迁移（Zustand 极简设计）
* **Zustand 全局 Store**：将 Vue 3 的 `reactive` 状态提取为 Zustand stores，位于 `src/stores/`：
  - `useThreadStore`：全局线程状态、timeline 事件以及 diff 同步。
  - `useProjectStore`：全局项目 active 作用域及 modal 控制。
  - `useComposerStore`：输入框状态、附件挂载、草稿管理。
  - `usePreferencesStore`：布局（splitRatio、cardCols）等云端偏好同步。
* **控制器 Hook 化（测试复用保障）**：
  由于原 Vue 单元测试大部分是通过调用页面的 `setup()` 函数返回响应式对象来进行无 DOM 的极速测试，在 React 中我们将维持这一结构——**将页面的业务核心逻辑抽离为 Custom Hook（如 `useUnifiedChatPage`）**，在 hook 中合并 Zustand 状态并处理系统异步调用。这能让 1390+ 单元测试用例直接通过 `@testing-library/react` 的 `renderHook()` 进行无缝迁移！

---

## 底层技术约束与安全护栏 (Underlying Technical Constraints & Safety Guardrails)

> [!IMPORTANT]
> 在重构 React 的全过程中，必须严格遵守以下底层技术约束和安全护栏，违背任何一项将导致体积守卫或集成验证失败：
>
> 1. **禁止兜底代码 (Fail-Fast Rule)**:
>    * 遇到异常、配置为空或关键数据缺失时，必须立即抛出异常并阻断流程（Fail-Fast）。
>    * 严禁使用静默降级、默认兜底配置、空 try-catch 吞错等隐式兜底逻辑。
> 2. **19位 BigInt 纳秒精度防截断**:
>    * 绝对禁止对 `agent_id`、`trace_id`、`timestamp`、`_ts` 等含有纳秒级的 19 位超长整数使用 `parseInt()` 或 `Number()` 进行类型强转。
>    * JavaScript 的 Double 精度上限是 2^53 - 1，超长 19 位整数强转会导致低位截断和精度丢失。必须使用原生 `BigInt()` 或保持 `string` 字符串进行字典序比较与传输。
> 3. **CWD 全局状态防逃逸**:
>    * 调用多项目敏感接口（例如 API 路由包含 `ui/(dashboard|memory|window)` 或 `threads/`）时，单行 Payload 参数必须显式携带 `cwd`。
>    * 严禁隐式读取全局变量或依赖未隔离的状态，防止在多项目并发切换时出现敏感数据溢出或读写串线。
> 4. **测试桩（data-testid）锁定与 0 单元测试回归**:
>    * 保证所有 HTML/JSX 结构中的 `data-testid` 锚点和核心事件回调契约（函数的参数和返回值类型）完全一致，决不能删除或重命名既有的 `data-testid`。
>    * 确保重构后 1394 个已有的 Vitest 单元测试 100% 成功通过。
> 5. **静态体积守卫与语法坏范式拦截**:
>    * `size-guard.cjs` 静态检查为硬红线。所有新编写的 JSX/JS 代码都必须规避以下坏范式：
>      - **嵌套三元表达式**：严禁嵌套三元表达式，如 `cond1 ? (cond2 ? a : b) : c`，一律抽取为独立变量或使用 `if-else`。
>      - **内联条件对象展开**：严禁采用 `...(cond ? { k: v } : {})` 等内联展开，一律使用外部 if 分支或显式属性赋值。
>      - **Map 伪 LRU 删除**：严禁使用 `cache.delete(cache.keys().next().value)` 进行缓存逐出，这会导致锁定状态或误删活跃数据。
> 6. **Wails 嵌入式编译缓存强制刷新**:
>    * 在前端 `npm run build` 构建并输出到 `dist/` 之后，必须轻微修改 Go 源码中的任意嵌入层文件（如 `frontend.go` 的空行或无意义注释），强制触发 Wails embed 的增量缓存重新打包。

---

## 避免拼装感与 UI 统一性约束 (Constraints to Avoid a Piecemeal Feel & Ensure UI Harmony)

> [!TIP]
> 重构期间必须彻底规避“组件堆砌拼装感”。前台界面必须呈现出高度连贯、严丝合缝、有张力的 Neo-Brutalist (新粗野主义) 现代极客纸质质感：
>
> 1. **全站设计系统收拢 (No Ad-hoc Styles)**:
>    * 严禁在页面或局部组件中随意定义硬编码色值（例如直接写 `bg-[#3b82f6]` 或 `border-red-500`）。
>    * 所有的布局和组件样式必须从 `src/styles.css` 或 Tailwind v4 主题中消费全局的 Brutalist 变量（如 `--color-mc-border`、`--shadow-brutalist`）。
> 2. **无条件使用原子交互组件 (Primitive Atoms)**:
>    * 凡是按钮点击交互，必须强制消费 `McButton`；凡是卡片外框，必须消费 `McCard`；凡是标签，必须消费 `McBadge`；凡是输入域必须遵循统一风格。
>    * 全站的边框粗细（2px border）、投影（4px hard offset shadow）以及 hover 态（整体 translate-y 上浮）、active 态（整体按下位移）必须完全一致，防止不同组件的触觉反馈发生割裂。
> 3. **网格与边缘严苛对齐 (Bento & Resizing Symmetry)**:
>    * 所有信息面板（如 Bento Grid） and Timeline 边缘必须具备严苛的网格对齐，禁止出现 1px/2px 的非对称边缘偏差。
>    * 主工作台的 Split Panel 缩放（左右拆分栏拖拽）时，必须通过 `useResizePanels` 维持平滑缩放，描边与分隔线绝不能产生重叠抖动或错位。
> 4. **微动效物理阻尼感**:
>    * 所有弹窗、悬浮卡片、下拉菜单的动效必须统一使用贝塞尔曲线（如 `cubic-bezier(0.175, 0.885, 0.32, 1.275)`），使动画呈现出具有惯性的物理阻尼回弹，从而极大提升质感。

---

## Open Questions (用户确认)

> [!IMPORTANT]
> 在执行重构方案前，请确认以下三个关键工程决策：

1. **测试运行环境与覆盖率要求**：
   重构后是否要在 CI/CD 中全面应用 `jsdom` 并结合 `@testing-library/react` 执行全量单测？由于之前的测试不依赖 DOM，我们可以通过 `renderHook()` 保证原本测试的 100% 对应翻译。
2. **源码目录重命名**：
   建议将 `vue-app` 重命名为 `src`，我们会同步修改 [size-guard.cjs](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/scripts/size-guard.cjs) 里的扫描路径 `SCAN_DIR` 和文件匹配后缀（加入 `.jsx` 和 `.tsx`）。是否同意此重命名？
3. **Go Embed 打包验证**：
   前端在编译输出至 `dist/` 后，Go 侧通过 `go:embed dist/*` 加载。我们将修改 Go 代码中的轻微注释（触发 Wails 的 embed 增量缓存更新）以确保 Wails 桌面端能够立即加载重构后的 React 界面。

---

## Proposed Changes (拟议变更)

### 1. 构建与工具链配置

#### [MODIFY] [package.json](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/package.json)
* 添加依赖：
  - `react`, `react-dom` (v18+)
  - `zustand` (v5+)
* 添加开发依赖：
  - `@vitejs/plugin-react`
  - `@tailwindcss/vite` (用于 Tailwind v4 编译)
  - `@testing-library/react` (用于 hook 及组件单元测试)
  - `@testing-library/jest-dom`

#### [MODIFY] [vite.config.js](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/vite.config.js)
* 替换别名 `@vue` 为正常的 react 相关映射。
* 引入并挂载 `@vitejs/plugin-react` 插件。
* 保持 Wails `/wails/runtime.js` 的 `external` 排除策略。

#### [MODIFY] [vitest.config.js](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/vitest.config.js)
* 配置测试环境为 `jsdom`。
* 确保 Vitest 启用 React 的 JSX/TSX 转译。

#### [MODIFY] [size-guard.cjs](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/scripts/size-guard.cjs)
* 将 `SCAN_DIR` 的 `vue-app` 指向新的 `src` 目录。
* 修改匹配后缀由仅限于 `.js` 扩展为支持 `.js`, `.jsx`, `.ts`, `.tsx`：
  ```javascript
  // regex update
  else if (entry.isFile() && /\.[jt]sx?$/.test(entry.name))
  ```

---

### 2. 设计系统与原子组件

#### [NEW] [index.css](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/src/index.css)
* 使用 Tailwind v4 的语法引入主样式与设计变量：
  ```css
  @import "tailwindcss";

  @theme {
    --color-mc-bg: var(--bg-base);
    --color-mc-card: var(--card);
    --color-mc-card-hover: var(--card-hover);
    --color-mc-border: var(--border);
    --animate-brutalist: 150ms cubic-bezier(0.2, 0.8, 0.2, 1);
  }
  ```

#### [NEW] Primitive UI Components
创建公共的基础原子组件以消除拼装感：
* [McButton.jsx](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/src/components/McButton.jsx)：收拢 2px 描边、4px 黑实体投影及 Hover/Active 物理偏移。
* [McCard.jsx](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/src/components/McCard.jsx)：收拢卡片容器物理微动效。
* [McBadge.jsx](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/src/components/McBadge.jsx)：定义极简纯色微标。

---

### 3. 全局 Store 迁移 (Zustand)

#### [NEW] Zustand Stores
* [useThreadStore.js](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/src/stores/useThreadStore.js)：接管全部 timeline、线程列表、diff 对账和 Wails 事件处理。
* [useProjectStore.js](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/src/stores/useProjectStore.js)：接管项目列表与 project modal 的开启状态。
* [useComposerStore.js](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/src/stores/useComposerStore.js)：接管输入状态与附件添加逻辑。

---

### 4. 业务页面与控制器 Hook

#### [NEW] Pages and Controllers (React)
每一个核心页面，对应重构为一个 Hook (逻辑控制器) + JSX Component：
* **统一工作台 UnifiedChatPage**：
  - [useUnifiedChat.js](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/src/pages/useUnifiedChat.js) (Hook)
  - [UnifiedChatPage.jsx](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/src/pages/UnifiedChatPage.jsx) (JSX View)
* **记忆中心 MemoryCenterPage**：
  - [useMemoryCenter.js](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/src/pages/useMemoryCenter.js) (Hook)
  - [MemoryCenterPage.jsx](file:///Users/mac/Desktop/agnet/Super-Dolphin/cmd/agent-terminal/frontend/src/pages/MemoryCenterPage.jsx) (JSX View)
* **其它页面**：包括 `SkillsPage`, `SharedFilesPage`, `DagsPage`, `SettingsPage`，均按此范式彻底拆包，完美适配体积守卫。

---

## 验证与测试保障 (Verification Plan)

### 1. 自动化测试迁移 (Vitest)
* 单元测试脚本在 `cmd/agent-terminal/frontend` 运行。
* 编写测试适配函数 `runHookTest`，利用 `@testing-library/react` 挂载 custom controller hooks，模拟 Vue 原单测中对 reactive 对象的测试流程。
* 执行测试命令确保 1394 个用例 100% 成功：
  ```bash
  npm run test
  ```

### 2. 静态红线守护
* 在重构过程中，随时调用体积与范式静态检查：
  ```bash
  npm run guard
  ```

### 3. Wails 物理集成测试
* 运行 frontend build 生成打包资产：
  ```bash
  npm run build
  ```
* 触发 Go Embed 刷新并编译桌面端：
  ```bash
  make build-agent-terminal-plain
  ```
* 运行桌面端，手动或通过 Playwright 测试用例进行真实操作，确认没有 BigInt 大数精度丢失或 UI 死锁。
