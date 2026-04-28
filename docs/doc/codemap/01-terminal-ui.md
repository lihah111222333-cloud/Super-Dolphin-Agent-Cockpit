# super-agent-v3 代码地图：终端入口与 UI 层

> 本卷已按 `docs/契约/modularity-convention.md` §2.4 拆分：原文件 1021 行，超出单文件 ≤600 行约束。

## 分卷索引

- [01-terminal-ui-go.md](01-terminal-ui-go.md) — Go 桌面端 / Wails 运行时 / 内置 RPC / 多窗口 / 代码预览
- [01-terminal-ui-vue.md](01-terminal-ui-vue.md) — super-agent-v3 代码地图：终端入口与 UI 层（Vue 前端）

## 阅读顺序

1. 先读 [01-terminal-ui-go.md](01-terminal-ui-go.md) 理解桌面入口、Wails 壳、事件桥、RPC 与多窗口。
2. 再读 [01-terminal-ui-vue.md](01-terminal-ui-vue.md) 衔接前端页面、store、快捷键与组件编排。

## 边界

- 本轮 A01a 只深化 Go/Wails 后端，不展开 Vue 前端细节。
- 旧版混合文档中的 Vue 章节已迁出到独立分卷，由 A01b 接手。

## 拆卷映射表

| 卷 | 重点章节 | 讲什么 |
|---|---|---|
| [01-terminal-ui-go.md](01-terminal-ui-go.md) | §2、§3、§4.1-§4.8、§5、§6 | 桌面入口、Wails 壳、EventBridge、内置 RPC、项目作用域、代码预览、多窗口、HTTP runner |
| [01-terminal-ui-vue.md](01-terminal-ui-vue.md) | §1、§2、§3、§4、§5.1、§5.2、§6、§7 | Vue 入口、五层职责、store/composable、blank-thread 首发、LaunchSkillPicker、feature flag、现状差异 |

## 阅读顺序补充

1. 先读 [01-terminal-ui-go.md](01-terminal-ui-go.md) §2、§3，拿到桌面启动总链。
2. 再按问题切到 Go 分卷细节：RPC/原生能力看 §4.5，代码预览/CWD 看 §4.6，多窗口看 §4.7。
3. 需要页面编排、技能选择、blank-thread 首发时，再读 [01-terminal-ui-vue.md](01-terminal-ui-vue.md) §2-§5.2。
4. 若问题继续追到后端写链，不要停在 01；直接跳 [07-module-write.md](07-module-write.md) §5，再回看 §2.4 / §3.4。

## 跨卷跳转锚点

- 看 dashboard cwd 去 [07-module-read.md](07-module-read.md) §2.4。
- 看 blank-thread `startThread -> sendMessage` 去 [07-module-write.md](07-module-write.md) §5。
- 看 thread/start、turn/start 后端落点去 [07-module-write.md](07-module-write.md) §2.4、§3.4。
- 看 memory / prompt snapshot 如何并入启动链，去 [11-memory.md](11-memory.md) §5.5 B；prompt 入口位仍看 [11-prompt-thread.md](11-prompt-thread.md)。

## 最近一次重大变更摘要

- **2026-04-17**：01 从原单卷拆成 Go/Wails 与 Vue 两卷，本页改为稳定索引，旧外链继续落在这里。
- **2026-04-20 补记**：把 blank-thread 首发、LaunchSkillPicker、dashboard/prompts 作用域跳转关系补回索引口径。

## 常见误导

- `01-terminal-ui.md` 只有十几行，**不代表内容少**；正文已经拆进 `01-terminal-ui-go.md` / `01-terminal-ui-vue.md`。
- Go 分卷不讲页面/store 细节；Vue 分卷也不重复 Wails runtime 壳。
- `dashboard/prompts` 的 readonly fallback 虽然从前端触发，但真实过滤逻辑不在 01，而在 `07-module-read.md` §2.4。

## 新增符号入口

| 符号 / 主题 | 去哪看 |
|---|---|
| `LaunchSkillPicker.js` / `useLaunchSkillSelection.js` / `services/skills-api.js` | [01-terminal-ui-vue.md](01-terminal-ui-vue.md) §5.1、§5.2、§2 |
| `isReadonlyFallbackListError` / `SystemPromptPage` readonly fallback | 先看 [01-terminal-ui-vue.md](01-terminal-ui-vue.md) §7，再跳 [07-module-read.md](07-module-read.md) §2.4 |
| `requestScopeRoots` / `resolveSaveTarget` / `findScopedFiles` | [01-terminal-ui-go.md](01-terminal-ui-go.md) §4.6 |
| `openNewWindow` / `ao_ui_bootstrap` / `ao_window_cwd` | [01-terminal-ui-go.md](01-terminal-ui-go.md) §4.7 |
