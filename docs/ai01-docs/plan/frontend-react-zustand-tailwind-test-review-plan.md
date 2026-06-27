# React + Zustand + Tailwind 前端重构多 Agent 测试审查方案

创建日期：2026-05-29
适用范围：`cmd/agent-terminal/frontend`
依赖方案：`docs/ai01-docs/plan/frontend-react-zustand-tailwind-refactor.md`

> **For agentic workers:** 本方案用于审查和测试 React + Zustand + Tailwind 前端重构实施结果。可直接使用平台原生子代理；只有需要持久 DAG、重试、租约或结构化交接记录时，才可选使用 `mcp-go-agent-orchestration`。所有 agent 只在被分配的文件和测试域内工作，禁止跨域重构。

## 摘要

本方案把前端重构后的测试与代码审查拆成多个可并行 agent 域，目标是在不互相干扰的前提下覆盖：

- 后端 RPC 契约是否保持不变。
- Zustand store 是否正确承接 snapshot、sidebar、live patch、optimistic state。
- UnifiedChat 交互是否保留旧 Vue 行为。
- Warning Log 和 trace 是否能定位问题，而不是吞错。
- Tailwind / UI / layout 是否符合“克制工作台”风格。
- 迁移期间是否存在隐式兜底、browser-native fallback、19 位 ID 精度损坏、`cwd` 丢失等高风险问题。

多 agent 审查采用“两层并行”：

1. **测试 Agent**：按领域补测试、跑测试、记录行为证据。
2. **审查 Agent**：不写实现代码，按契约、状态、UI、日志、安全、架构边界审查 diff 和测试覆盖。

最终由 Orchestrator 汇总所有报告，形成一个 go / no-go 结论。

## 总体原则

### 审查目标

- 发现行为回归，而不是只看代码是否“像 React”。
- 优先审查契约、状态同步、错误边界和用户关键路径。
- 每个 agent 给出可复现证据：文件路径、测试命令、失败输出、风险等级。
- 所有失败必须回到 coding 方案中的批次和验收标准定位。

### 禁止事项

- 禁止为了通过测试修改后端 RPC wire shape。
- 禁止删除或改名既有高价值 `data-testid`。
- 禁止把 `agent_id`、`trace_id`、thread id、turn id、纳秒时间戳转成 number。
- 禁止在前端添加静默 fallback，例如空数组、默认 provider、默认 cwd、browser-native 文件系统替代 Wails。
- 禁止更新 size guard baseline 或放宽 guard 阈值。
- 禁止跨 agent 同时编辑同一文件。
- 禁止只给“看起来没问题”的主观结论，必须提供命令和证据。

### 分级标准

| 等级 | 含义 | 示例 | 处理 |
| --- | --- | --- | --- |
| P0 | 阻断发布 | `turn/start` 缺 cwd、ID 精度损坏、发送消息投错线程、build 失败 | 必须修复并重跑全量门禁 |
| P1 | 阻断合并 | Warning Log 吞掉 RPC failed、patch gap 不触发 repair、关键 `data-testid` 丢失 | 必须修复并重跑相关域测试 |
| P2 | 合并前应修 | UI 状态不清晰、日志字段不完整、边界测试缺口 | 修复或登记明确后续 issue |
| P3 | 建议优化 | 组件命名、局部样式重复、非关键文案 | 可后续处理 |

## Orchestration DAG

### DAG 总览

```text
N0 baseline-and-diff-scan
  -> N1 contract-review
  -> N2 shared-api-wire-tests
  -> N3 state-store-tests
  -> N4 send-message-flow-tests
  -> N5 unified-chat-ui-tests
  -> N6 warning-log-trace-tests
  -> N7 architecture-style-accessibility-review
  -> N8 migration-build-guard
  -> N9 integration-synthesis
```

依赖说明：

- `N0` 必须先跑，负责确认变更范围和基线状态。
- `N1` 到 `N8` 可并行，但每个节点只能处理自己的文件域。
- `N9` 必须等待所有 agent 报告完成后执行。

### DAG 创建建议

Orchestrator 开始时创建 DAG：

```text
task_create_dag:
  title: frontend-react-zustand-tailwind-test-review
  description: Review and test React/Zustand/Tailwind frontend refactor
  nodes:
    - N0 baseline-and-diff-scan
    - N1 contract-review
    - N2 shared-api-wire-tests
    - N3 state-store-tests
    - N4 send-message-flow-tests
    - N5 unified-chat-ui-tests
    - N6 warning-log-trace-tests
    - N7 architecture-style-accessibility-review
    - N8 migration-build-guard
    - N9 integration-synthesis
```

如果本轮选择 mcp-orch，每个 agent 启动时写入对应运行状态；未选择 mcp-orch 时，用报告和计划状态记录：

```text
task_start_dag / task_dispatch_node:
  node_id: <node-id>
  status: in_progress
```

结束时写入：

```text
task_update_node:
  node_id: <node-id>
  status: completed | blocked | failed
  summary: <findings and verification evidence>
```

## Agent 角色与职责

### A0 Orchestrator / 测试总控

**目标：** 管理 DAG、分发任务、合并报告、做最终 go / no-go 判断。

**输入：**

- `docs/ai01-docs/plan/frontend-react-zustand-tailwind-refactor.md`
- 本测试审查方案。
- 当前实现分支 diff。
- `cmd/agent-terminal/frontend/package.json`
- `cmd/agent-terminal/frontend/vitest.config.js`
- `cmd/agent-terminal/frontend/scripts/size-guard.cjs`

**职责：**

- 运行基线命令并保存输出。
- 确认每个 agent 的文件所有权，避免冲突。
- 收集所有 agent 报告。
- 统一复跑最终验证命令。
- 生成最终测试审查报告。

**禁止：**

- 不直接修实现代码，除非只是修报告路径或合并测试报告。
- 不忽略任何 P0/P1。

**输出：**

- `docs/ai01-docs/test/frontend-react-zustand-tailwind-review-summary.md`

### A1 Contract Review Agent / 后端契约审查

**目标：** 审查 React 前端是否仍遵守 Go 后端 RPC、Wails bridge、event wire shape。

**重点文件：**

- `src/shared/api/**`
- `src/entities/*/api/**`
- `src/app/bridge/**`
- `src/features/send-message/**`
- `src/entities/thread/model/**`

**必查契约：**

- `callAPI(method, params)` 的 `params` 必须是 object。
- `thread/start` payload 保留 `cwd`、provider/model config、`deferSpawn`、`launchIntentId`。
- `turn/start` payload 保留 `cwd`、`threadId`、`input`、`manualSkillSelection:false`。
- `ui/state/get`、`ui/sidebar/get`、`ui/preferences/set` 必须显式传 `cwd`。
- `bridge-event` 和 `agent-event` 都被订阅。
- `ui/thread/patch` 不被改名或改结构。
- `ui/log` 继续作为前端日志回传通道。

**建议命令：**

```bash
cd cmd/agent-terminal/frontend
rg -n "callAPI\\(|thread/start|turn/start|ui/state/get|ui/sidebar/get|ui/preferences/set|ui/log" src
rg -n "manualSkillSelection|manual_skill_selection|deferSpawn|defer_spawn|launchIntentId|launch_intent_id" src
rg -n "onBridgeEvent|onAgentEvent|bridge-event|agent-event" src
```

**审查输出：**

- 逐条列出 RPC 方法、调用文件、payload 字段。
- 标记缺失 `cwd` 或 wire shape 改动。
- 标记任何 browser-native fallback。

### A2 Shared API / Wire Test Agent

**目标：** 用单元测试锁住 bridge、RPC wrapper、wire id 和 validation 规则。

**文件域：**

- `src/shared/api/**`
- `src/shared/lib/wire/**`
- `src/shared/lib/assert/**`

**必须覆盖测试：**

- `callAPI()` 拒绝非 object params。
- `callAPI()` 给每次请求生成 `requestId`。
- RPC failed 保留 root cause 并写入 log store。
- `requireStringId()` 接受 19 位数字字符串但不转 number。
- `assertSafeInteger()` 拒绝 unsafe integer。
- unknown response shape fail-fast。

**建议测试命令：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run \
  src/shared/api/rpc/callAPI.test.js \
  src/shared/lib/wire/ids.test.js
```

**审查点：**

- 是否出现 `Number(agent_id)`、`parseInt(trace_id)`、`Number(timestamp)`。
- 是否存在 `catch (err) { console.warn(...); return [] }`。
- 是否存在无上下文的 `throw new Error("failed")`。

### A3 State Store Test Agent

**目标：** 验证 Zustand store 与旧 Vue thread store 的关键行为一致。

**文件域：**

- `src/entities/thread/model/**`
- `src/entities/thread/lib/**`
- `src/entities/project/model/**`
- `src/entities/preference/model/**`
- `src/widgets/composer-dock/model/**`

**必须覆盖测试：**

- `applySnapshot()` 写入 threads、statuses、timelines、diff、token、agent runtime。
- `applySidebar()` 不覆盖 dirty local selection。
- `applyThreadPatch()` 按 sequence 更新。
- patch sequence gap 记录 warning 并标记 repair required。
- optimistic user message 在 remote item 到达后去重。
- preference 同 key 串行写入。
- preference 写失败进入 `writeErrorsByKey`。
- missing cwd 时 `requireActionCwd()` throw。
- composer send failure 后 draft 和 attachments 保留。

**建议测试命令：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run \
  src/entities/thread/model/threadReducers.test.js \
  src/entities/thread/lib/timelineMerge.test.js \
  src/entities/project/model/useProjectStore.test.js \
  src/entities/preference/model/usePreferenceStore.test.js \
  src/widgets/composer-dock/model/useComposerStore.test.js
```

**高风险审查：**

- Store 之间互相 import。
- selector 返回全局大对象导致 streaming 时整页重渲染。
- reducer 内部执行 RPC。
- snapshot 盲目覆盖 active thread。
- patch gap 只 log 不 repair。

### A4 Send Message Flow Test Agent

**目标：** 专门审查发送消息链路，确保没有破坏后端 pending launch 语义。

**文件域：**

- `src/features/send-message/**`
- `src/entities/thread/api/**`
- `src/entities/turn/api/**`
- `src/widgets/composer-dock/**`

**必须覆盖测试：**

- 无 active thread、有 cwd、有文本：先 `thread/start` 再 `turn/start`。
- 无 active thread、空文本但有附件：允许创建 pending thread，再发送附件 input。
- 有 active thread：跳过 `thread/start`。
- `thread/start` payload 带 `deferSpawn:true`。
- `turn/start` payload 带 `manualSkillSelection:false`。
- missing cwd：不调用 RPC，composer draft 保留。
- `thread/start` failed：不调用 `turn/start`。
- `turn/start` failed：composer draft 保留，warning log 记录 `thread.send.failed`。
- stale selected thread：阻断发送，不投递到错误 thread。

**建议测试命令：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/features/send-message/model/sendMessageController.test.js
```

**审查输出格式：**

| 场景 | 期望 RPC 顺序 | 实际 RPC 顺序 | 结论 |
| --- | --- | --- | --- |
| blank thread send | `thread/start -> turn/start` | ... | pass/fail |

### A5 UnifiedChat UI Test Agent

**目标：** 审查用户可见行为、`data-testid`、组件组合、关键交互是否迁移完整。

**文件域：**

- `src/pages/unified-chat/**`
- `src/widgets/thread-rail/**`
- `src/widgets/chat-workspace/**`
- `src/widgets/composer-dock/**`
- `src/widgets/activity-panel/**`
- `src/widgets/diff-panel/**`
- `src/features/*/ui/**`

**必须检查 `data-testid`：**

- `chat-page`
- `chat-toolbar`
- `provider-toggle`
- `thread-rail`
- `thread-list`
- `thread-empty-state`
- `chat-empty-state`
- `composer-bar`
- `composer-input`
- `composer-attach-button`
- `composer-send-button`
- `composer-compact-button`
- `context-usage-banner`
- `thread-config-model-select`
- `thread-config-effort-select`

**必须覆盖交互：**

- 新线程首发。
- 已有线程续发。
- interrupt。
- compact。
- recover。
- rename。
- archive / unarchive。
- open new window。
- thread config。
- drag/drop attachments。
- paste image attachments。
- citation / file ref preview。
- diff save/open/locate failure state。

**建议测试命令：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run \
  src/pages/unified-chat/UnifiedChatPage.test.jsx \
  src/widgets/thread-rail/ui/ThreadRail.test.jsx \
  src/widgets/composer-dock/ui/ComposerDock.test.jsx \
  src/widgets/chat-workspace/ui/ChatTimeline.test.jsx \
  src/widgets/diff-panel/ui/DiffPanel.test.jsx
```

**审查重点：**

- 页面是否只装配 widget，没有重新堆积业务逻辑。
- 组件是否直接调用 `callAPI`，如果是则标记违规。
- loading/disabled/error 状态是否改变布局尺寸。
- streaming timeline 是否抢占 composer focus。

### A6 Warning Log / Trace Test Agent

**目标：** 确认日志追踪机制真的能定位前端和后端交界问题。

**文件域：**

- `src/entities/log/**`
- `src/shared/api/tracing/**`
- `src/widgets/warning-log-panel/**`
- `src/shared/api/rpc/**`
- `src/app/logging/**`

**必须覆盖测试：**

- ring buffer 默认 600 条裁剪。
- bridge queue 默认 240 条上限。
- flush batch size 默认 24。
- `rpc.failed` 进入 warning log。
- `thread.patch.gap` 进入 warning log。
- `preference.write.failed` 进入 warning log。
- log sink failure 不递归刷爆队列。
- export log bundle 不触发后端 RPC。
- filter 支持 level、method、threadId、agentId、requestId、operationId。

**建议测试命令：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run \
  src/entities/log/model/useLogStore.test.js \
  src/widgets/warning-log-panel/ui/WarningLogPanel.test.jsx
```

**审查重点：**

- 是否仍有业务代码直接 `console.warn()` / `console.error()`。
- info 是否默认不刷后端。
- debug 高频事件是否不会污染 warning 主列表。
- operationId 是否能串联 user action、RPC、bridge event、store reducer。

### A7 Architecture / Style / Accessibility Review Agent

**目标：** 审查 FSD 边界、Tailwind/token 使用、布局风格、可访问性。

**文件域：**

- `src/app/**`
- `src/pages/**`
- `src/widgets/**`
- `src/features/**`
- `src/entities/**`
- `src/shared/ui/**`
- `src/shared/layout/**`
- `src/shared/styles/**`

**架构检查：**

- `shared` 不依赖业务层。
- `entities` 不依赖 `features/widgets/pages/app`。
- `features` 不依赖 `widgets/pages/app`。
- `widgets` 不依赖 `pages/app`。
- 跨 slice 引用只走 `index.js`。

**样式检查：**

- Tailwind class 不应绕过 `shared/ui` 重复造 button、badge、panel。
- token 必须来自 `src/shared/styles/tokens.css`。
- 不出现营销式 hero、卡片套卡片、大面积单色主题。
- 图标按钮必须有 tooltip 和 `aria-label`。
- 组件半径默认不超过 8px。

**建议命令：**

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/shared/test/architecture-boundaries.test.js
rg -n "from ['\\\"]\\.\\./\\.\\./|from ['\\\"]src/" src
rg -n "console\\.(warn|error)|localStorage|getItem\\(|setItem\\(" src
```

**审查输出：**

- 架构违规按 P1 处理。
- 视觉偏差按 P2/P3 处理。
- 可访问性缺口按 P1/P2 处理，取决于是否阻断核心操作。

### A8 Migration / Build / Guard Agent

**目标：** 审查工具链、入口切换、build、size guard 和旧测试保留策略。

**文件域：**

- `cmd/agent-terminal/frontend/package.json`
- `cmd/agent-terminal/frontend/vite.config.js`
- `cmd/agent-terminal/frontend/vitest.config.js`
- `cmd/agent-terminal/frontend/jsconfig.json`
- `cmd/agent-terminal/frontend/index.html`
- `cmd/agent-terminal/frontend/scripts/size-guard.cjs`

**必须检查：**

- React plugin 配置正确。
- Vitest include 覆盖 `src/**/*.test.{js,jsx}`。
- size guard 扫描 `src/**/*.{js,jsx}`。
- `index.html` 入口切换时机符合 coding 方案。
- 没有删除未迁移页面的旧测试保护网。
- `dist` 不作为源真相提交。

**验证命令：**

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

如涉及 Go/Wails embed：

```bash
make guard
make build-plain
```

**审查重点：**

- 是否为了迁移跳过旧 Vue 关键测试。
- 是否新增依赖但 package-lock 未更新。
- 是否 build 成功但 runtime 入口仍指向旧文件。
- 是否隐藏 size guard 违规。

### A9 Integration Synthesis Agent / 汇总审查

**目标：** 读取所有 agent 报告，形成最终审查结论。

**输入：**

- A1-A8 agent 报告。
- Orchestrator 最终命令输出。
- 当前 git diff。

**输出：**

- 总体结论：go / no-go。
- P0/P1 阻断清单。
- P2/P3 后续清单。
- 测试命令矩阵。
- 覆盖缺口。
- 推荐下一步。

## 并行执行策略

### 第一轮：只读审查 + 测试缺口识别

所有 agent 先不改代码，只做：

- 读 coding 方案。
- 读自己的文件域。
- 跑自己的查询命令。
- 标记缺失测试和高风险代码。

输出：

- `docs/ai01-docs/test/frontend-react-review/Ax-*-round1.md`

### 第二轮：测试补强

只有测试 agent 可以写测试：

- A2 写 shared API / wire tests。
- A3 写 store tests。
- A4 写 send message tests。
- A5 写 UI component tests。
- A6 写 log / trace tests。
- A8 写 build/guard 配置测试或架构扫描测试。

审查 agent A1/A7 仍保持只读。

输出：

- 新增或修改的测试文件。
- 每个测试的 red/green 证据。

### 第三轮：实现审查

实现 agent 完成 coding 后，各审查 agent 重新跑：

- 自己的 targeted tests。
- 自己的 `rg` 审查命令。
- 对应 diff review。

输出：

- P0/P1/P2/P3 findings。
- 可复现命令。
- 建议修复方向。

### 第四轮：集成门禁

Orchestrator 统一执行：

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

如果涉及 Go/Wails：

```bash
make guard
make build-plain
```

通过后才允许进入 PR 或合并流程。

## Agent 报告模板

每个 agent 输出 Markdown：

```markdown
# A<id> <Agent Name> Report

## Scope

- Files reviewed:
- Tests run:
- Commands run:

## Summary

- Result: pass | blocked | failed
- Highest severity: P0 | P1 | P2 | P3 | none

## Findings

### P1: <title>

- File: `path/to/file.js:123`
- Evidence:
- Expected:
- Actual:
- Reproduction:
- Suggested fix:

## Test Evidence

```bash
<command>
```

Result:

```text
<important output>
```

## Coverage Gaps

- Gap:
- Risk:
- Proposed test:

## Handoff

- Files changed:
- Follow-up owner:
```
```

## 最终汇总报告模板

```markdown
# Frontend React Refactor Multi-Agent Review Summary

## Verdict

- Decision: go | no-go
- Reason:

## Command Matrix

| Command | Result | Notes |
| --- | --- | --- |
| `node scripts/size-guard.cjs` | pass/fail | |
| `npx vitest run` | pass/fail | |
| `npm run build` | pass/fail | |
| `make guard` | pass/fail/skipped | |
| `make build-plain` | pass/fail/skipped | |

## Blocking Findings

| Severity | Agent | File | Finding | Owner |
| --- | --- | --- | --- | --- |

## Coverage Map

| Requirement | Agent | Test / Evidence | Status |
| --- | --- | --- | --- |
| `thread/start -> turn/start` order | A4 | `sendMessageController.test.js` | pass/fail |
| Explicit `cwd` | A1/A4 | contract review + tests | pass/fail |
| 19-digit IDs stay string | A2 | `ids.test.js` | pass/fail |
| Patch gap repair | A3/A6 | reducer + warning tests | pass/fail |
| Warning Log | A6 | `WarningLogPanel.test.jsx` | pass/fail |
| FSD boundaries | A7 | `architecture-boundaries.test.js` | pass/fail |

## Residual Risks

- Risk:
- Mitigation:

## Recommended Next Step

- Merge / fix blockers / rerun targeted agent / rerun full gate
```

## 测试矩阵

### Baseline Matrix

| 阶段 | 命令 | 负责人 | 通过条件 |
| --- | --- | --- | --- |
| Baseline | `git status --short` | A0 | 只包含预期改动 |
| Frontend guard | `node scripts/size-guard.cjs` | A8 | exit 0 |
| Targeted Vue reference | selected `vue-app/*.test.js` | A0 | 旧关键行为可跑 |
| Shared API | `npx vitest run src/shared/**` | A2 | exit 0 |
| Store | `npx vitest run src/entities/**` | A3 | exit 0 |
| Send flow | `npx vitest run src/features/send-message/**` | A4 | exit 0 |
| UI | `npx vitest run src/pages src/widgets` | A5 | exit 0 |
| Logs | `npx vitest run src/entities/log src/widgets/warning-log-panel` | A6 | exit 0 |
| Full frontend | `npx vitest run && npm run build` | A8/A0 | exit 0 |

### Manual UX Review Matrix

| 场景 | 操作 | 期望 |
| --- | --- | --- |
| 空线程首发 | 选择项目后在 composer 输入并发送 | 创建 pending thread，随后 turn 启动，草稿清空 |
| 发送失败 | mock `turn/start` 失败 | 草稿保留，composer 显示错误，Warning Log 出现 `thread.send.failed` |
| Patch gap | mock sequence 跳号 | Warning Log 出现 patch gap，状态显示 sync repair |
| Diff stale | mock diff revision mismatch | DiffPanel 显示同步中或失败状态 |
| 缺 cwd | 清空 project/window cwd 后发送 | 不调用 RPC，显示 fail-fast 错误 |
| 日志导出 | 打开 Warning Log export | 得到本地 JSON，不触发后端 RPC |

## 风险矩阵

| 风险 | 严重性 | 负责 Agent | 检测方式 |
| --- | --- | --- | --- |
| 发送消息顺序错误 | P0 | A4 | RPC order test |
| `cwd` 丢失 | P0 | A1/A4 | payload review + missing cwd test |
| ID 精度损坏 | P0 | A2 | wire id test + `rg Number/parseInt` |
| Patch gap 被吞 | P1 | A3/A6 | reducer test + warning test |
| Warning Log 不可用 | P1 | A6 | log store + panel test |
| `data-testid` 丢失 | P1 | A5 | component tests |
| FSD 反向依赖 | P1 | A7 | architecture boundary test |
| build/guard 未覆盖 `src` | P1 | A8 | config review + guard |
| 视觉过度卡片化 | P2 | A7 | UI review |
| 可访问性缺口 | P1/P2 | A7 | keyboard/focus review |

## Agent Prompt 模板

### 通用前缀

```markdown
你是 Super-Dolphin 前端 React 重构测试审查 agent。

必须阅读：
- `docs/ai01-docs/plan/frontend-react-zustand-tailwind-refactor.md`
- `docs/ai01-docs/plan/frontend-react-zustand-tailwind-test-review-plan.md`

约束：
- 只处理你的分配文件域。
- 不改后端 RPC wire shape。
- 不新增静默 fallback。
- 不更新 size guard baseline。
- 保留所有关键 `data-testid`。
- 所有发现必须提供文件路径、命令、证据和严重性。
- 如果需要记录任务状态，使用 `mcp-go-agent-orchestration` 的 DAG node 更新。
```

### A4 Send Message Agent Prompt

```markdown
你的任务是审查并测试 React send-message feature。

范围：
- `cmd/agent-terminal/frontend/src/features/send-message/**`
- `cmd/agent-terminal/frontend/src/entities/thread/api/**`
- `cmd/agent-terminal/frontend/src/entities/turn/api/**`
- `cmd/agent-terminal/frontend/src/widgets/composer-dock/**`

必须验证：
1. blank thread send 的 RPC 顺序是 `thread/start -> turn/start`。
2. `thread/start` 带 `deferSpawn:true`。
3. `turn/start` 带 `manualSkillSelection:false`。
4. missing cwd 不调用任何 RPC。
5. `turn/start` 失败后 composer draft 和 attachments 保留。
6. stale selected thread 不会发送到错误 thread。

运行：
```bash
cd cmd/agent-terminal/frontend
npx vitest run src/features/send-message/model/sendMessageController.test.js
```

输出报告到：
`docs/ai01-docs/test/frontend-react-review/A4-send-message-flow.md`
```

### A7 Architecture Agent Prompt

```markdown
你的任务是审查 React 重构是否遵守 FSD、Tailwind/token、可访问性和布局风格。

范围：
- `cmd/agent-terminal/frontend/src/**`

必须验证：
1. `shared` 不依赖业务层。
2. `entities` 不依赖 `features/widgets/pages/app`。
3. `features` 不依赖 `widgets/pages/app`。
4. `widgets` 不依赖 `pages/app`。
5. 跨 slice 只走 `index.js`。
6. 图标按钮有 `aria-label` 和 tooltip。
7. 不存在卡片套卡片、营销式 hero、大面积单色主题。

运行：
```bash
cd cmd/agent-terminal/frontend
npx vitest run src/shared/test/architecture-boundaries.test.js
rg -n "console\\.(warn|error)" src
```

输出报告到：
`docs/ai01-docs/test/frontend-react-review/A7-architecture-style-accessibility.md`
```

## Go / No-Go 规则

### No-Go

出现任一情况必须 no-go：

- 任意 P0 未修复。
- 任意 P1 未修复且无明确 owner。
- `node scripts/size-guard.cjs` 失败。
- `npx vitest run` 失败。
- `npm run build` 失败。
- `thread/start -> turn/start` 顺序无测试证据。
- `cwd` 显式传递无测试或审查证据。
- Warning Log 无法展示 RPC failed。

### Go

同时满足：

- P0/P1 为零。
- 前端完整验证命令通过。
- A1-A8 全部 completed。
- A9 汇总报告给出 go。
- 残余 P2/P3 已登记 owner 和后续处理路径。

## 保存路径建议

审查报告统一放在：

```text
docs/ai01-docs/test/frontend-react-review/
  A0-baseline.md
  A1-contract-review.md
  A2-shared-api-wire.md
  A3-state-store.md
  A4-send-message-flow.md
  A5-unified-chat-ui.md
  A6-warning-log-trace.md
  A7-architecture-style-accessibility.md
  A8-migration-build-guard.md
  A9-summary.md
```

如果后续要进入 GitHub PR，A9 summary 可以直接转换为 PR review checklist。
