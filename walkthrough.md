# Stitch UI Redesign Walkthrough

工作树：`.worktrees/stitch-ui-redesign-20260720`（分支 `codex/stitch-ui-redesign-20260720`，基线 `origin/main@9debcd39`）。
设计来源：Stitch 项目 `Codex Style Main Console`（ID `16556859161700396548`，DESKTOP，设计系统 `Luminous Minimalist`），本地落盘于 `design/stitch/`（`README.md`、`DESIGN_SYSTEM.md`、18 张 PNG）。实施前已逐一查看全部 8 张正式页面图与 10 张 `reference-*.png`（reference 图为当前应用真实截图，仅作结构参考，未当作新路由）。

## 1. Stitch 页面 ↔ 实际路由映射

| Stitch 设计图（本地路径） | 主题 | 应用路由 | 页面 |
| --- | --- | --- | --- |
| `design/stitch/screens/01-chat-vibe-light.png` | Light | `/` | 聊天页 |
| `design/stitch/screens/02-chat-dark.png` | Dark | `/` | 聊天页 |
| `design/stitch/screens/03-plugins-vibe-light.png` | Light | `/skills` | 插件与技能（MCP工具/技能库/数据源） |
| `design/stitch/screens/04-plugins-dark.png` | Dark | `/skills` | 同上 |
| `design/stitch/screens/05-memory-vibe-light.png` | Light | `/memory` | 记忆中心 |
| `design/stitch/screens/06-memory-dark.png` | Dark | `/memory` | 同上 |
| `design/stitch/screens/07-roles-vibe-light.png` | Light | `/prompts` | 个性化角色 |
| `design/stitch/screens/08-roles-dark.png` | Dark | `/prompts` | 同上 |
| 无独立设计图 | — | `/files`、`/dags`、`/observability`、`/settings` | 仅同步外壳/令牌/排版/按钮/输入框/卡片语言，未重排信息架构 |

## 2. 设计令牌落入现有 CSS 的方式

没有新建平行体系，全部并入 `frontend-app/src/styles.css` 已有的 `--suiyuan-*` / 主题变量块：

- 新增（`:root` 浅色共享规则，暗色自动继承）：`--suiyuan-primary-container:#c84d05`、`--suiyuan-inverse-surface:#30312c`（暗色下反转为 `#f2f1ea`）、`--suiyuan-inverse-on-surface`、`--suiyuan-page-margin:32px`、`--suiyuan-mobile-padding:16px`、`--suiyuan-radius-card:16px`、`--suiyuan-radius-input:24px`、`--suiyuan-radius-control:8px`、排版字阶（`--suiyuan-text-display-*` 40/52、`--suiyuan-text-headline-*` 24/32、`--suiyuan-text-body-large-*` 18/28、`--suiyuan-text-body-*` 14/22、`--suiyuan-text-label-*` 12/16、`--suiyuan-text-label-xs-*` 11/14）、`--suiyuan-mobile-nav-height:64px`。
- 既有令牌已与设计一致，未改值：`#fbf9f3/#ffffff/#f5f4ed/#f0eee7/#eae8e1`、`#1b1c18/#584238`、`#a03b00/#792b00/#ffdbcd/#ffb597`、`#8b7268/#e0c0b3`、`#ba1a1a`、侧边栏 280px、内容 1100px、gutter 24px、卡片/输入阴影。
- 深色背景说明：`DESIGN_SYSTEM.md` 的 `Inverse surface #30312c` 是 M3 反色表面令牌，四张正式 Dark 设计图的实际页面底色为近黑暖色；按“正式设计图优先”原则，暗色页面底保持现有 `#131411/#1b1c18/#1e1f1b/#242620` 体系（与 02/04/06/08 截图一致），`#30312c` 作为 `--suiyuan-inverse-surface` 令牌使用。
- fusion 渐变清除：`--suiyuan-fusion-bg` 由 `linear-gradient(135deg,#a03b00,#ffb597)` 改为 `var(--surface)`（暗色 `var(--surface-2)`），`.fusion-surface/.fusion-surface-glass/.fusion-toolbar` 变为中性暖色卡片，`.suiyuan-btn-fusion` 变为主色胶囊、`.suiyuan-btn-fusion-ghost` 变为中性描边胶囊；`MemoryPage.css` 与 `PromptPageView.css` 中的渐变 `!important` 覆盖块整体删除。设计稿明令删除的 `Skill Fusion` 横幅未恢复（`SkillsPage.test.jsx:290` 仍断言其不存在）。

## 3. 逐页实际修改

### 外壳（`src/App.jsx`、`src/AppShell.css`、`src/styles.css`、i18n）

- 侧边栏 280px 固定 + 居中主画布保持不变；修复浅色 `新对话` 按钮 `opacity:0.8` 导致的褪色（实色 `#a03b00`，hover `#c84d05`）；导航激活态背景由 10% 提至 55% `--suiyuan-primary-fixed`，保留 4px 左侧指示条；底部保持 Settings/Help，无用户卡片。
- 新增移动端底部导航 `.suiyuan-mobile-nav`（`SuiyuanMobileNav`，`App.jsx:532-557`）：聊天/插件/定制角色/记忆/设置 5 项，仅 `≤920px` 显示（`AppShell.css:531-598`），抽屉侧栏打开时隐藏；主画布底部留白与浮动输入框 `bottom` 同步抬升，不遮挡内容；新增短标签 i18n 键 `nav.chatShort/memoryShort`、`workbench.mobileNavAriaLabel`（中英双份）。
- 顶栏保留真实功能入口（通知/历史/主题/语言），未添加设计稿中的 `Overview/Usage/Limits/Upgrade Plan` 营销链接（`styles.test.js:1531-1539` 本就禁止其进入外壳）。
- `.sa-window` 增加 `data-brand="suiyuan"`：激活既有的品牌作用域规则（聊天 hero 的 logo tile 与副标题此前因无人设置该属性而永久 `display:none`）。

### 聊天页（`src/pages/chat/ChatPage.css`、`composer/ComposerDock.css`、`ChatPageWorkbench.css`）— 对应 01/02

- hero：标题 display 字阶 40px/600/52px、`-0.02em`，`燧元` 主色高亮保留；副标题 18/28、`--suiyuan-on-surface-variant`(#584238)；logo tile 与副标题经 `data-brand` 修复后实际显示。
- 建议卡：白底（暗色 `--suiyuan-surface-low`）、1px `--suiyuan-outline-variant`、16px 圆角、静止无阴影、hover `--suiyuan-card-shadow` + `translateY(-2px)`；图标浅色 tile（暗色为纯主色图标）；标题/描述显式颜色。
- 浮动输入卡：圆角 20→24px（`--suiyuan-radius-input`），白底 + `--suiyuan-input-shadow`；工具行保留 添加文件/Add image/项目选择/模型选择/发送 全部真实交互；暗色发送键改为 `--suiyuan-primary-container`(#c84d05) + 近白箭头（对齐 02 设计）；免责声明修复为 `--suiyuan-on-surface-variant`（原 `#c6c7c4` 在浅底上约 1.6:1 不可读）。
- 暂缓：设计稿的方形发送键（保持产品现有圆形）；暗色 hero logo tile 保留。

### 插件与技能（`src/pages/skills/SkillsPageHub.css`、`SkillsPage.css`、`DatasourcePage.css`）— 对应 03/04

- 三个子栏目共享统一骨架：白容器卡（`min(1080px,100%-48px)`、16px 圆角、`--suiyuan-card-shadow`）+ 卡内标题（32px/760）+ 说明 + tab 行（激活 tab 暖橙下划线、semibold；非激活 muted）。
- 技能库：单列列表改为 `repeat(auto-fill,minmax(280px,1fr))` 卡片网格；`.skill-card-redesign` 全套样式落地（此前零样式）：暖底 `--surface-2`、16px 圆角、图标 tile、显式标题/路径/描述色、tag chips、scope 胶囊、底部分隔线 + 操作按钮（编辑详情 outline、删除 danger）；统计行 4 张暖底统计卡；工具栏搜索 24px pill + 分段控件 + 批量导入/新建技能（主色胶囊）。
- MCP 工具卡：保留深色 `--mcp-card-*` 卡片契约（对比度见第 7 节），仅圆角 8→16px、min-height 54→60；`注册技能工具` 卡由深色虚线改为中性虚线卡（1.5px dashed `--border-strong`、`--surface-2` 底）。
- 技能冲突面板：中性 warning 表面（白卡 + 3px `--accent` 左边条 + 6% tint 头部），推荐/次要按钮分级（主色胶囊/中性描边），冲突处理、筛选、导入、MCP 开关逻辑未动。
- 数据源：导入区改中性虚线（upload 图标 + 主色导入胶囊）、搜索 pill、紧凑中性空态（保持 `min-height:auto` 契约）；`.datasource-card` 全套新样式。
- 删除 grep 验证零引用的死 CSS 约 260 行（`.connected-*`、`.recommended-*`、旧 `.datasource-table*`、重复 subtabs 块等）。
- 暂缓：技能库网格中额外的 "Add New Skill" 占位卡（产品 DOM 无此元素，新建入口是工具栏按钮）；tab 的 `role="tab"`（保持 class 方案）。

### 记忆中心（`src/pages/memory/MemoryPage.jsx`、`MemoryPage.css`）— 对应 05/06

- 新增可见 `.memory-hero` 页头（标题 24px/500/32 + 副标题），替代被外壳隐藏的共享 `PageHeader`；标题与副标题均通过 `copy.title` / `copy.subtitle` 从现有 memory 文案契约渲染（zh：`管理并观察 AI 的上下文记忆留存。`；en：`Manage and observe your AI's contextual retention.`），无硬编码、无 `||` fallback、无默认值兜底，缺失文案将直接暴露并被测试捕获。
- 三张统计卡（总览/健康度/自动沉淀）：白底（暗色抬升面）、16px 圆角、细边、软阴影、显式文字色；健康度 meter 轨道 `--surface-3`；自动沉淀卡开关右置为真 switch（保留未生效 fail-fast 反馈与 pendingRestart 提示）。
- 工具栏中性白卡：搜索 24px pill + 新建主色胶囊（保留在工具栏内——`App.test.jsx:6057` 钉住该位置，与设计“页头右侧”不同，属有意保留差异）；`.memory-tabs` 偏好/项目/全部 保持。
- 空态改为 340px 高中性面板（白卡、66px 图标圆、加粗标题、限宽描述），无橙色背景。
- 删除渐变 `!important` 覆盖块与全部死 CSS（`.memory-stats-card*`、`.bias-*`、`.suiyuan-memory-empty-canvas*` 等），746→~470 行；修复 `.memory-similar-list` 负 margin；`code` 预览不再裁剪。

### 个性化角色（`src/features/prompts/Personalization.css`、`PromptPagePolish.css`、`PromptPageView.css`、`PromptPageView.jsx`）— 对应 07/08

- 渐变 hero 改为平静页头： eyebrow + 24px 标题 + 副标题在左，4 张白色统计卡（定制角色/知识/默认规则/待确认）在右，白卡深字（暗色为抬升面浅字）；`.personalization-overview` 背景 `var(--surface)`→`transparent`，让标题与统计卡直接落在画布上。
- 个人资料卡：白 16px 卡、图标+标题+状态 chip、昵称/职业并排、textareas 全宽、输入 `--surface-2` + 细边 + 显式文字色、行内校验错误保留；`保存个人资料` 改左对齐主色胶囊（原全宽）。
- 导入记忆卡：上传图标在内容上方（`--suiyuan-primary-fixed` tile）、标题、描述、`导入记忆` 胶囊在卡片底部居中；点击仍打开 `参考资料` tab 的向导。
- 提示词卡：中性 16px 卡、徽章中性 pill、操作区分隔线；弹窗表面去米色化到令牌（顺带修复暗色下米色弹窗的错色）；删除渐变 `!important` 覆盖块与未使用的 drag-zone CSS。

### 其余页面（阶段 7，无独立设计图）

- `/files`：统计条由橙渐变改为白卡 + 深色文字 tiles；`FilesPageWorkbench.css` 中原生 hex/rgba 全部令牌化（全仓“无原生色字面量”契约恢复通过）；搜索 pill + focus-within 环、行卡片 hover 阴影。
- `/observability`：过滤表单白卡、输入统一 8px 控件圆角、提交按钮主色胶囊 + 新增 `:disabled` 态。
- `/dags`：删除最后一个渐变（`.enterprise-design-progress`）；面板/模板卡 16px 圆角；装饰性橙色 accent 收敛为中性；空态图标圈中性化、预设按钮真 999px outline 胶囊。
- `/settings`：卡片 16px 圆角、输入由 invisible surface-2-on-surface-2 改为 `--surface`、修复失效的 `--shadow-sm` 引用、移除负 margin hack。

## 4. 保留的业务逻辑（未改动）

- 聊天：草稿/发送/中断、附件与图片选择、项目选择（react-aria 菜单 + RPC）、模型选择（线程覆盖/继承）、slash 命令、拖拽附件、approval inert 门控。
- 技能：MCP `mcpServer/list` + sqlite/playwright start/stop、技能工具注册（防重模型）、技能冲突 resolution preview/apply、批量导入与摘要草稿、范围筛选与搜索、数据源 V2 全部 CRUD 与分块分页。
- 记忆：`ui/memory/get` 仪表盘、新建偏好/项目（自动命名 + 校验）、编辑/删除、自动沉淀 intent 切换（含“未生效”fail-fast 反馈）、相似记忆整合/忽略/一键整合轮询。
- 个性化：资料加载/保存（字段长度校验 + stale-save 防护）、导入记忆向导（draft/dry-run/commit + 风险确认）、提示词卡片操作、只读 fallback。
- 全部 fail-fast 错误传播、RPC 契约、store 行为均未触碰；未添加任何 mock/兜底。

## 5. 新增或更新的测试

- `src/App.test.jsx`：新增 `renders the mobile bottom navigation with core destinations and active state`（5 项顺序、图标、`aria-current`、点击导航到 `/memory`）；现为 217 项。
- `src/styles.test.js`：新增 `renders the mobile bottom navigation as a fixed bar and keeps content clear of it`（桌面 `display:none`、≤920px fixed + token z-index、抽屉打开隐藏、active 主色、主画布与浮动输入框让位）；同步更新 12 处既有契约断言以匹配新设计（composer 24px 圆角×2、暗色 intro 卡底色、intro 标题字重 600、卡描边 outline-variant、描述色/字重/行高、memory stats 3 列/面板 16px/新建 999px、personalization overview transparent、免责声明色、z-index 计数 41→42）；现为 91 项。
- `src/pages/memory/MemoryPage.test.jsx`：新增 `MemoryPage hero copy` 两项语言回归——zh 模式断言 hero 标题 `记忆中心` 与副标题 `管理并观察 AI 的上下文记忆留存。`；en 模式断言标题 `Memory` 与副标题 `Manage and observe your AI's contextual retention.` 且页面不出现中文副标题。副标题经 `copy.subtitle` 从文案契约渲染，无 `||` fallback；缺失文案会直接暴露并被该测试捕获。
- 各页面既有测试（chat 281、skills 42、memory 40、prompts 58、files/observability/workflows/settings 177、SettingsPage 15）全部保持绿色，页面测试未因视觉改动而删除任何行为断言。

## 6. 浏览器验收（真实前后端：`run-new-ui-desktop.sh`，Vite `127.0.0.1:5175` + Go 后端 `127.0.0.1:4512`）

工具：Playwright（Chromium），脚本与证据位于 `.tmp/stitch-acceptance/`。**注意：`.tmp/` 已被 `.gitignore` 忽略（`/.tmp/` 规则），这些脚本、截图与 JSON 报告只是本地临时验收证据，不是、也不能描述成提交后可复核的仓库产物**；本节验收结论以文字记录为准。

### 视口 × 主题矩阵（10 个路由变体 × 3 视口 × 2 主题 = 精确 60 条记录）

完整运行（无 filter）`acceptance.mjs` 生成 `shots/report.json`。每条记录真实断言：无横向溢出、DOM `data-theme` 与预期一致、刷新后主题保持、`console error = 0`、`pageerror = 0`、截图文件真实存在。脚本硬性断言 `report.length === 60`、`issues === 0`、`missingFields === 0`，任一不满足以非零退出；局部 filter 运行写入 `report-<filter>.json`，不会覆盖完整报告。

最终真实输出：`{ "report": "report.json", "total": 60, "issues": 0, "missingFields": 0 }` + `FULL MATRIX PASS (60 records)`，退出码 0。

| 视口 | 结果 |
| --- | --- |
| 1318×1244 | 全部路由：无横向溢出、主题正确应用、刷新后主题保持、控制台 0 错误 |
| 900×900 | 同上；≤920px 进入移动布局（汉堡 + 底部导航），工具栏自然换行 |
| 390×844 | 同上；底部导航可见可点、浮动输入框不被遮挡、无横向溢出 |

### 逐页核对（与对应设计图并排）

- `/`（01/02）：hero 标题字阶、logo tile、副标题、3 张建议卡（白/暗底 + 1px 描边 + 16px + hover 抬升）、24px 浮动输入卡、发送键（亮 `#a03b00` / 暗 `#c84d05`）、免责声明——一致。截图：`chat-intro-1318x1244-{light,dark}.png`。
- `/skills` 三栏目（03/04）：统一白卡骨架、标题/tab 行、统计卡、卡片网格、冲突面板、MCP 深卡、中性虚线 add 卡、数据源导入/搜索/空态——一致。截图：`skills-1318x1244-*`、`skills-library-1318x1244-*`、`skills-datasource-1318x1244-*`。
- `/memory`（05/06）：hero、3 统计卡、搜索+新建工具栏、tabs、340px 中性空态面板——一致（新建按钮位置见第 3 节说明）。截图：`memory-settled-1318x1244-{light,dark}.png`。
- `/prompts`（07/08）：页头 + 4 白统计卡深字、资料卡表单、导入卡（图标在上、按钮底部居中）——一致。截图：`prompts-1318x1244-{light,dark}.png`。
- `/files`、`/dags`、`/observability`、`/settings`：统一令牌语言，信息架构未动。截图：对应 `*-1318x1244-{light,dark}.png`。
- 移动端：底部导航 5 项 + 激活态、抽屉打开时隐藏、`/memory` 导航——一致。截图：`mobile-nav-390-light.png`、`mobile-memory-390-light.png`、`skills-library-900x900-light.png`。

### 交互验收（脚本 `interactions.mjs`，18 项全部来自真实断言，退出码 0）

每个 PASS 均来自 DOM/URL/可访问状态/计算样式/业务状态断言；脚本约定任何操作或断言失败都会记录 FAIL 并以非零退出，无无条件通过项。产物 `interactions.json` 在全部断言（含全局 console/pageerror 检查）完成后写盘，并回读校验：脚本硬性断言 `results.length === 18`、每条含 `page/check/ok`、第 18 条必须是 global 检查；回读确认 JSON 长度精确 18、`failed = 0`、`global` 记录恰 1 条，任一不满足以非零退出。本轮真实产物：`count=18, failed=0, global=1`。

1. 建议卡 hover 350ms 后计算样式含 `20px 40px -10px` 卡片阴影（非过渡中间态）。
2. 建议卡点击后 `#composer-input` 草稿非空。
3. 空输入发送 disabled、添加文件/选择模型 enabled。
4. Tab 后焦点落在按钮上且 outline 为 `solid 2px`。
5-7. 三个技能子栏目：每次点击后断言 tab 带 `active` 类，且对应内容（`.mcp-tool-card` / `.skills-toolbar` / `datasource-import-zone`）实际可见。
8. MCP 开关可见且 enabled。
9. 技能搜索：过滤前 9 张 → 精确名称查询 `产品评分` 断言收敛为 1 张且文本含查询词 → 多字段查询 `MCP` 断言列表收窄 → 清空后断言恢复 9 张。
10. 记忆 hero 副标题可见且非空（文案契约渲染结果）。
11. 新建菜单打开且 `新建偏好`/`新建项目` 两项可见。
12. 自动沉淀开关（点击前/后因果断言）：点击前快照开关业务状态（`.active` class、状态点、状态文案三指示器）、pending-restart 文案、以及**操作专属**通知集合（仅 `.memory-notice` 中含“自动沉淀”的节点，禁止整页泛化错误词搜索）；点击后用 `waitForFunction` 轮询等待本次点击产生的收敛（busy 结束且状态文案/pending/通知集合之一变化）；只接受三种结果之一——A 状态真实翻转且三指示器一致、B pending-restart 从无到有、C 操作专属错误通知新增。本轮实际命中分支 C：`beforeChecked=false afterChecked=false stateChanged=false pendingAdded=false operationErrorAdded=true`，新增错误文案 `自动沉淀切换未生效：记忆服务的自动梦境配置存在读写路径不一致（后端已知问题）…`（后端读写路径不一致时 UI 如实 fail-fast，且该错误被证明由本次点击新产生）。
13. 导入记忆打开向导且 `参考资料` tab 可见。
14. 保存个人资料可见且状态明确（enabled 或 aria-disabled）。
15-17. 390px：底部导航可见且 5 项、点击记忆导航到 `/memory` 且激活项正确、无横向溢出。
18. 全程 console/pageerror 为 0（global 记录）。

### 主题验收（脚本 `theme-toggle.mjs`）

light → 点击切换 → dark（DOM + localStorage）→ 刷新保持 dark → 再切回 light：PASS；60 张矩阵截图亦全部通过“预置主题 → 刷新保持”。

## 7. 对比度抽测（WCAG 相对亮度比）

白卡正文 17.13:1、页面正文 16.27:1、次级 8.86:1、弱化 4.46:1（大字号/次要文本达标，AA 4.5 边缘）、主色 6.39:1、主按钮白字 6.73:1、hover 白字 4.65:1；暗色正文 14.28:1、次级 10.06:1、弱化 5.83:1、主色 10.84:1；MCP 卡 12.25/13.87:1；暗发送键 4.1:1（≥3:1 组件级）；错误 6.46:1。免责声明色经修复后 8.86:1。

## 8. 自动化验证真实输出

- `npm run lint`：通过，无 warning/error 输出。
- `npm test`（含 guard:critical-skip、typecheck:contracts、audit:rpc-contracts）：**Test Files 162 passed (162)，Tests 2493 passed (2493)**。
- `npm run build`：**✓ 5597 modules transformed，built in 1.01s**，成功。
- `npx react-doctor@latest --verbose --scope changed --base origin/main`：扫描 6 个变更 JSX/测试文件，`No issues found!`。
- `git diff --check`：无输出。
- diff 统计（修复轮最终命令真实输出，执行于本文件本次更新前一刻，本文件自身行数可能有个位数漂移）：
  - 完整工作树 `git diff --shortstat`：`29 files changed, 1647 insertions(+), 1474 deletions(-)`
  - 仅前端 `git diff --stat -- frontend-app`：`28 files changed, 1448 insertions(+), 1432 deletions(-)`

## 9. 未验证项与 Blocker

- 聊天页在有历史会话时先显示“正在同步会话历史”，intro 需点击“新对话”进入（数据驱动，非缺陷）；验收 intro 截图均通过真实点击获得。
- hover 阴影已按 350ms 过渡结束后断言计算样式；focus-visible 以 outline 计算样式断言，未逐帧截图。
- 旧 walkthrough 提到的后端集成问题 `ui/windowBootstrap/get response snapshot must be an object`：本轮 60 条矩阵记录 + 18 项交互中未复现，console/pageerror 均为 0；未做长时间会话压测。
- 自动沉淀开关在实测中如实呈现 fail-fast 错误反馈（后端读写路径不一致），属既有后端行为而非 UI 缺陷；交互验收按真实产品契约断言了该反馈可见。
- 技能库 “Add New Skill” 网格占位卡、设计稿顶部营销链接（Overview/Usage/Limits/Upgrade Plan）、设计稿用户卡片：按任务约束（无真实功能不添加/不恢复）未实现，属有意差异。
- 无阻塞性 Blocker；未发现需要 mock 或吞错才能展示的后端异常。

## 10. `git status --short` 原样输出

```
 M frontend-app/src/App.jsx
 M frontend-app/src/App.test.jsx
 M frontend-app/src/AppShell.css
 M frontend-app/src/features/prompts/Personalization.css
 M frontend-app/src/features/prompts/PromptPagePolish.css
 M frontend-app/src/features/prompts/PromptPageView.css
 M frontend-app/src/features/prompts/PromptPageView.jsx
 M frontend-app/src/pages/chat/ChatPage.css
 M frontend-app/src/pages/chat/ChatPageWorkbench.css
 M frontend-app/src/pages/chat/composer/ComposerDock.css
 M frontend-app/src/pages/files/FilesPage.css
 M frontend-app/src/pages/files/FilesPageWorkbench.css
 M frontend-app/src/pages/memory/MemoryPage.css
 M frontend-app/src/pages/memory/MemoryPage.jsx
 M frontend-app/src/pages/memory/MemoryPage.test.jsx
 M frontend-app/src/pages/observability/ObservabilityPage.css
 M frontend-app/src/pages/settings/SettingsPage.css
 M frontend-app/src/pages/settings/components/SettingsPageComponents.css
 M frontend-app/src/pages/skills/DatasourcePage.css
 M frontend-app/src/pages/skills/SkillsPage.css
 M frontend-app/src/pages/skills/SkillsPageHub.css
 M frontend-app/src/pages/workflows/WorkflowEmptyState.css
 M frontend-app/src/pages/workflows/WorkflowPage.css
 M frontend-app/src/pages/workflows/WorkflowPolish.css
 M frontend-app/src/shared/i18n/appI18n.en.json
 M frontend-app/src/shared/i18n/appI18n.zh.json
 M frontend-app/src/styles.css
 M frontend-app/src/styles.test.js
 M walkthrough.md
?? design/
```

`design/` 为本次交付的设计资产（Stitch 落盘资料），按任务要求保留，不作为临时文件清理。
