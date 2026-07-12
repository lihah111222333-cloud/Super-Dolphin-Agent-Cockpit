# Reasonix Frontend Next Absorption Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking. 本文是下一轮实施约束，不是现状说明；不得把目标描述成已经实现。

**Goal:** 在保持 V3 后端真相源、严格 RPC、现有 UI 分层和 AI 可维护门禁的前提下，吸收 Reasonix 中尚未落地且适合个人 AI 开发的四项机制：统一命令/快捷键注册表与命令面板、后端权威的输入历史带、无 UI 侵入的性能压力监测、可复现的长历史合成基准。

**Architecture:** 命令描述是唯一静态真相，执行函数在 app composition 绑定；快捷键、命令面板、Settings 编辑器和帮助文案只消费同一投影。输入历史由 Go 后端分页扫描真实 thread message history，每个 canonical message page source 同时返回稳定 source revision，thread 层据此组合 snapshot nonce；前端控制器只持有当前浏览游标和未提交草稿。性能监测扩展现有 diagnostics/trace allowlist 与 sanitizer 契约，只上传有界稳定指标。长历史基准先建立结构性门禁与报告基线，不把机器抖动变成 flaky CI。

**Tech Stack:** React、Vitest、Testing Library、Vite、现有 Wails/JSON-RPC 适配层、Go、jrpc2、现有 `ui/preferences`、仓库内 MCP-LSP、前端 contract/size/z-index guards。

**Verification Surface:** `frontend-app` command/palette/composer/diagnostics tests、`internal/module/thread`、`internal/module/uistate`、RPC contract matrix、frontend lint/test/build、repository guard、codemap/project-map generated-state checks。

**Locked planning inputs:**

- V3: `/Users/mima0000/Desktop/wj/super-agent-v3@a7df089e32e4135a90f10a52f6ef10069cab8353`
- Reasonix: `/Users/mima0000/Desktop/wj/deepseek-reasonix@1f5740a2129ea54bda7c86755ed58c88b84c16b4`
- 上一轮计划: `docs/plans/2026-07-11-reasonix-frontend-architecture-absorption.md`

**Scope decision:** 四项能力各自形成可识别的原子 commit series、可按 lane 独立回退，但共同修改 app composition、shared API/diagnostics 和同一套全量前端门禁，因此保留一份总计划、三个独立实现 lane 与一个只做 cherry-pick/生成物/evidence 的 Integrator；禁止把任一 lane 的完成误报成整份计划完成。

---

## 0. 锁定事实、前提与历史残留

### 0.1 编写时仓库状态

本文编写时 V3 为干净的 `main...origin/main`。上面的 SHA 只锁定计划输入，不代替执行时基线。执行者必须重新记录：

```bash
git -C /Users/mima0000/Desktop/wj/super-agent-v3 status --short --branch
git -C /Users/mima0000/Desktop/wj/super-agent-v3 rev-parse HEAD
git -C /Users/mima0000/Desktop/wj/super-agent-v3 rev-parse origin/main
git -C /Users/mima0000/Desktop/wj/deepseek-reasonix rev-parse HEAD
```

若工作树不干净，不得清理、覆盖或顺带提交用户现场；从确认过的 `origin/main` 或用户指定提交建立隔离 worktree。

### 0.2 上一轮已经具备，禁止重复实现

- latest-intent-wins thread open coordinator。
- 单一 scroll intent manager 与资源变化后的滚动治理。
- `thread/recover` 严格响应消费和可见失败面。
- 根级 `AppErrorBoundary`、隐私安全 crash report 与 bounded breadcrumbs。
- approval-only decision shelf；当前唯一真实 wire decision 仍是 `approval/request`。
- Shell layout 最小 store。
- `LayerTokens.css`、`OverlayPortal` 与 z-index token guard。
- React Profiler 慢渲染 trace。
- timeline 最近 80 条 materialization 与已有消息分页。

因此本轮不得再新增第二套 thread state、scroll refs、bridge batching、timeline materialization、crash pipeline 或通用 decision framework。

### 0.3 LSP 是生产修改硬前置

2026-07-12 文档复核会话已经通过当前 `mcp__lsp.*` 对源码完成定位、inspect、xref、精确读取和 diagnostics，并据此修正 message revision、trace allowlist、Settings 写入路径与 worktree 拓扑；这只证明计划输入已复核，不代替执行 worktree 的新鲜 LSP 证据。执行仍必须先完成：

```bash
make codex-worktree-ready
go run ./cmd/codex-worktree-setup verify
codex mcp get lsp
```

并在新的执行任务中确认存在：

```text
mcp__lsp.file
mcp__lsp.inspect
mcp__lsp.xref
mcp__lsp.grep
mcp__lsp.structure
mcp__lsp.patch_edit
mcp__lsp.completion
```

每个生产任务至少保存定位、definition/hover、references/call hierarchy、精确读取和 diagnostics 五类证据。工具不可用时记录 blocker 并停止生产修改；不得用 `rg`、`gopls check` 或一次测试冒充 LSP。

### 0.4 已知历史命令残留

上一轮计划仍包含：

```bash
node scripts/frontend-z-index-token-guard.test.mjs
```

该文件是 Vitest 测试，不是可直接执行的 Node 脚本。后续统一使用：

```bash
cd frontend-app
npm exec -- vitest run scripts/frontend-z-index-token-guard.test.mjs --no-file-parallelism --maxWorkers=1
```

不得把前一条命令的失败解释为生产回归。

---

## 1. 剩余机制处置矩阵

| 机制 | 处置 | 优先级 | 判定 |
|---|---|---:|---|
| 全局 command registry | **ABSORB NOW** | P0 | 为快捷键、命令面板、帮助与冲突检测建立单一描述源 |
| Command Palette | **ABSORB NOW** | P0 | 对个人 AI 开发高频导航和动作有直接收益；必须做成 registry 投影 |
| 可配置快捷键 | **ADAPT** | P0 | 使用后端 `ui/preferences`，由 Settings 显式编辑、保存与重置；不复制 Reasonix localStorage/吞错策略 |
| backend-authoritative prompt history tape | **ABSORB NOW** | P1 | 跨 thread 输入复用价值高；后端历史是真相源 |
| long-task/event-loop/heap pressure monitor | **ADAPT** | P1 | 复用 V3 diagnostics；只报稳定、有界、无内容指标 |
| synthetic long-history benchmark | **ABSORB NOW** | P1 | 证明 80 条 materialization 在大历史下仍有界；初期不设 wall-clock 硬阈值 |
| Reasonix `AnchoredPopover` | **REJECT** | — | V3 已有 React Aria Popover、`OverlayPortal`、`LayerTokens.css` |
| Ask/Plan 通用 decision | **DEFER** | — | 等第二种真实 typed backend wire contract |
| 多 Tab 工作台 | **DEFER** | — | 属于产品模型变更，不是前端 primitive 吸收 |
| GSAP/大 Controller/大 Bridge | **REJECT** | — | 与现有依赖方向、复杂度和文件预算冲突 |
| raw stack/path/content performance payload | **REJECT** | — | 违反既有隐私安全 diagnostics contract |
| module-global prompt history cache | **REJECT** | — | 跨 cwd/线程污染，竞态与失效语义不明确 |

### 1.1 Reasonix 只作为行为参考

允许参考：

- `desktop/frontend/src/lib/keyboardShortcuts.ts` 的 action descriptor、平台默认键和冲突检测。
- `desktop/frontend/src/components/CommandPalette.tsx` 的 caller-supplied item、过滤、键盘导航与空态。
- `desktop/frontend/src/lib/composerHistory.ts` 的 backend canonical tape、cursor page 与 invalidation 思路。
- `desktop/frontend/src/lib/crash.ts` 的 visibility/focus/grace/cooldown 与 long-task/event-loop/可选 heap 指标。
- `desktop/frontend/src/__tests__/history-performance-benchmark.tsx` 的合成长历史案例。

禁止复制：

- 快捷键 localStorage 和 storage failure 静默吞错。
- prompt history 的模块级可变 cache/cursor。
- performance report 中 raw stack、绝对路径、prompt/tool 内容。
- Reasonix 组件组织、单体 controller 或 CSS 结构。

---

## 2. 不变量与目标数据流

### 2.1 命令系统不变量

```text
appCommandRegistry (descriptor only)
        ├── shortcut dispatcher projection
        ├── command palette projection
        ├── shortcut settings/help projection
        └── conflict/contract tests

App composition
        └── command id -> handler/canExecute/disabledReason
```

- registry 只包含稳定 `id`、i18n key、section、默认组合键、editable policy、是否允许重复触发和 capability key。
- registry 不得保存 handler，不得 import Zustand、backend API、router 或 page component。
- handler 只在 app composition 层绑定；binding 只能包含 exact allowlist 字段，不能覆盖 registry-owned descriptor；缺 handler、重复 id、未知 preference command id 均 fail fast。
- page-local Escape、dialog focus trap、textarea 内部按键保留局部所有权；全局 dispatcher 不抢占已 `defaultPrevented` 的事件。
- `Cmd`/`Ctrl` 差异由平台投影解决，不把平台判断散落在组件；冲突检测必须在平台投影与 override 合并后执行。
- palette 只能执行 registry 中已绑定且可执行的命令；disabled reason 必须可见，不静默无效。
- App composition 只创建一个 shortcut preference controller；runtime 与 Settings 快捷键卡片从它消费同一份 registry projection、validated overrides 与保存状态。controller 通过注入的现有 `getPreference/setPreference` 读写；保存、重置、未知 id、冲突和后端失败都必须有可见状态。

### 2.2 Prompt history 不变量

```text
thread message store/session JSONL (truth)
        ↓
thread/promptHistory RPC (strict cursor page)
        ↓
promptHistoryController (per composer instance)
        ↓
ComposerDock draft navigation
```

- 输入历史只来自已持久化的 `role=user` 消息；不得再维护 localStorage ring。
- 先用 canonical thread record 验证 `activeThreadId` 为空或精确属于规范化后的请求 cwd；不得用现有 prefix `ListByCWD` 代替 exact membership，cwd 不匹配时在读取任何消息前失败。
- active thread 优先，其余同一精确 cwd thread 按稳定的 `updatedAt DESC + threadId ASC` 顺序扫描；单次 snapshot 最多 100 个 thread，超过上限显式失败。
- 每个 canonical message page source 返回稳定、无路径无内容的 `sourceRevision`：JSONL 使用文件长度与最后完整记录摘要，支持分页的已加载 session 必须由 provider page reader 返回同一 source snapshot revision。thread 层将 ordered thread identity/lifecycle/sourceRevision 组合为 SHA-256 nonce；`agent_threads.updatedAt` 只参与排序和 lifecycle，不再冒充消息 freshness。只有 legacy `session.ReadHistory` 且无法给 revision 的来源对现有 UI history 保持兼容，但对 prompt-history 必须 typed fail-fast，不能从当前页内容猜 revision。
- cursor 对前端不透明、带版本和 snapshot nonce；未知字段、非法 cursor、过大 limit、缺失/空 source revision 必须显式失败。
- 前端控制器实例归 composer/cwd 所有，不能是 module singleton。
- 首次按 `ArrowUp` 前保存当前未提交 draft 作为 sentinel；回到最新位置时原样恢复。
- 只有 composer 为空、无 IME composition、无 selection range 且光标在边界时才触发历史导航。
- 重复 prompt 是合法历史，不按文本去重；顺序和 identity 由后端游标保证。
- cwd 变化、成功发送、thread create/delete/archive/rename 或 snapshot nonce 变化时显式 invalidate；同秒连续写入相同文本也必须产生不同 source revision/nonce。

### 2.3 性能监测不变量

- 监测器 headless；不直接创建 modal、toast 或 DOM overlay。
- reporter、clock、scheduler、visibility 与 observer 工厂必须可注入，测试不得依赖真实等待。
- 仅允许稳定 code 和数值 bucket：long-task count/total/max、event-loop lag bucket、可选 heap ratio bucket。
- 禁止 stack、绝对路径、prompt、message、tool input/output、DOM 文本和自由格式 reason。
- 页面不可见、窗口失焦、启动 grace、恢复 grace 内不报警；每类指标每 build/session 有 cooldown 和去重。
- WebView 不支持 `longtask` 或 `performance.memory` 是明确 capability absence，安全 no-op；不得伪造零值成功。
- 复用现有 `emitFrontendTraceEvent`/frontend observability ingest，不建立第二套上传协议；性能 phase、status 与 metadata keys 必须先进入 canonical allowlist/sanitizer，并由真实 bridge test 证明不会返回 `false` 或被过滤。

### 2.4 Benchmark 不变量

- fixture 只含合成、非敏感、确定性数据。
- 默认 CI 案例为 200/1000/5000 turns；10000 turns 只进入 extended/manual profile。
- 第一阶段硬门禁只约束结构：输出正确、materialized 节点有界、无重复 key、无未归档 tool payload 泄漏。
- duration、heap delta 先输出 JSON 报告，不设固定毫秒硬阈值。至少积累 5 次同环境结果后另立 ratchet。
- benchmark 不得为了“跑分快”绕过真实 timeline selector/materialization 入口。

---

## 3. 目标代码落点

### 3.1 Command/shortcut/palette

```text
frontend-app/src/app/commands/
  appCommandRegistry.js
  appCommandRegistry.test.js
  appCommandRuntime.js
  appCommandRuntime.test.js
  useAppCommandDispatcher.js
  useAppCommandDispatcher.test.jsx
frontend-app/src/shared/keyboard/
  shortcutModel.js
  shortcutModel.test.js
frontend-app/src/features/command-palette/
  model/commandPaletteModel.js
  model/commandPaletteModel.test.js
  ui/CommandPalette.jsx
  ui/CommandPalette.test.jsx
  ui/CommandPalette.css
frontend-app/src/features/shortcut-settings/
  model/shortcutSettingsModel.js
  model/shortcutSettingsModel.test.js
  hooks/useShortcutSettings.js
  hooks/useShortcutSettings.test.jsx
  ui/ShortcutSettingsCard.jsx
  ui/ShortcutSettingsCard.test.jsx
  ui/ShortcutSettingsCard.css
```

受控修改：

- `frontend-app/src/App.jsx`
- `frontend-app/src/pages/chat/ChatPage.jsx`
- `frontend-app/src/pages/settings/SettingsPage.jsx`
- `frontend-app/src/pages/settings/SettingsPage.test.jsx`
- `frontend-app/src/shared/i18n/appI18n.zh.json`
- `frontend-app/src/shared/i18n/appI18n.en.json`
- `internal/module/uistate/preferences.go`
- 对应 preference、command composition 与 integration tests

快捷键 preference key 固定为 `settings.shortcuts.bindings`。存储值是：

```json
{
  "command.palette.open": {"key":"k","meta":true,"ctrl":false,"alt":false,"shift":false}
}
```

约束：最多 64 个 override；command id/key 必须非空且有长度上限；每个 binding 只允许 `key/meta/ctrl/alt/shift`；修饰键必须为 boolean；未知字段、空键、重复有效组合显式失败。后端只验证通用 shape/size，command id 的唯一真相仍在前端 registry；前端加载时遇到未知 id 必须显示配置错误并阻止整份 override 生效，不能静默丢弃。Settings 保存使用整对象 replacement，保存成功后重新读取并重建 runtime；保存失败保留编辑草稿并显示错误，重置则写入空对象 `{}`，不得删除为 `null` 后依赖默认兜底。

### 3.2 Prompt history

```text
internal/module/thread/
  prompt_history_test.go
  history.go                    # 仅复用既有 message source，不复制读取链
  lifecycle_helpers.go          # 统一补齐 sourceRevision
  persistence_port.go           # exact cwd membership 仍由 canonical ThreadRecord 判断
  contract.go
  rpc.go
  rpc_types.go
  service_handlers_test.go
internal/module/thread/prompthistory/
  scanner.go                    # 纯 snapshot/cursor/nonce/scan 状态机
  scanner_test.go
internal/dto/provider/
  message.go                    # 仅给 MessagePageResult 增加 sourceRevision
  message_test.go
internal/dto/thread/
  prompt_history.go             # 聚合 RPC DTO owner，不污染 provider DTO
  prompt_history_test.go
internal/util/historyjsonl/
  page.go
  history_test.go
internal/provider/claudecli/
  session_history.go
  session_history_test.go
frontend-app/src/shared/api/backend/
  backendRpcMethods.js
  backendApiFactoryThread.js
frontend-app/src/shared/api/
  backendResponseValidators.js
  backendResponseValidators.test.js
  backendApi.contractMatrix.js
  backendApi.test.js
frontend-app/src/features/prompt-history/
  model/promptHistoryController.js
  model/promptHistoryController.test.js
  hooks/usePromptHistory.js
  hooks/usePromptHistory.test.jsx
frontend-app/src/pages/chat/composer/
  ComposerDock.jsx
  ComposerDock.test.jsx
```

RPC 名固定为 `thread/promptHistory`。

请求：

```json
{"cwd":"/repo","activeThreadId":"thread-1","cursor":"opaque","nonce":"opaque","limit":30}
```

响应：

```json
{
  "entries":[{"threadId":"thread-1","messageId":"m-1","text":"...","createdAt":"..."}],
  "nextCursor":"opaque",
  "hasMore":true,
  "nonce":"opaque"
}
```

契约要求：请求/响应 exact keys；`limit` 范围 1..50；cursor/nonce 最长 2048 bytes；entry 数量不得超过 limit；`threadId/messageId/text/createdAt` 类型严格；nonce 不匹配返回 typed stale error，前端清空页状态后只允许从首屏重试一次，第二次失败必须显式报错。服务端 nonce 固定为 exact cwd 下 ordered thread identity/lifecycle/sourceRevision 的 SHA-256；每个 source revision 必须来自 canonical page source，JSONL 不暴露路径或内容、loaded session 不依赖秒级 thread timestamp。RED 测试必须证明新增 user message（包括同秒、同文本）、创建/删除/归档 thread 会改变 nonce，并证明 active thread cwd 不匹配时在消息读取前失败。不得用当前时间、进程内计数器、`agent_threads.updatedAt` 或前端 generation 伪造 nonce。

### 3.3 Performance pressure

```text
frontend-app/src/shared/diagnostics/
  frontendPerformancePressure.js
  frontendPerformancePressure.test.js
frontend-app/src/main.jsx
frontend-app/src/app/AppErrorBoundary.test.jsx
frontend-app/src/shared/api/wails/wailsBridgeConstants.js
frontend-app/src/shared/api/wails/wailsBridgeTraceEvents.js
frontend-app/src/shared/api/wailsBridge.test.js
```

新增 canonical trace phase：

```text
frontend.performance.long_task_pressure
frontend.performance.event_loop_pressure
frontend.performance.heap_pressure
frontend.performance.capability_absent
```

事件 exact shape 使用既有 trace envelope：`phase` 为上面四个 code，`status` 只允许 `slow` 或 `ok`，`duration_ms` 只承载单值时长；其余 bounded metric 放入新增 allowlisted metadata keys：`count`、`total_ms`、`max_ms`、`lag_bucket`、`heap_ratio_bucket`、`build`、`capability`。sanitizer 继续生成 `ts`，调用方不得上传自由格式 `timestamp`、`code` 或未知 metadata。`shouldRemoteFlushFrontendTrace` 必须显式允许四个 phase，真实 `emitFrontendTraceEvent` contract test 必须断言返回 `true` 且 flush payload 只含 allowlist。

默认策略必须集中为不可变配置，不散落 magic number：启动 grace 15s、恢复 grace 5s、类别 cooldown 10min；单个 long task 阈值 200ms；event-loop lag 150ms 连续 3 次；heap ratio 85% 连续 3 次。调整阈值必须同时修改 fixture、测试和 ADR/计划证据，不得运行时静默漂移。

### 3.4 Long-history benchmark

```text
frontend-app/scripts/chat-history-benchmark.mjs
frontend-app/scripts/chat-history-benchmark.test.mjs
frontend-app/src/pages/chat/model/chatHistoryBenchmarkFixture.js
frontend-app/src/pages/chat/model/chatHistoryBenchmarkFixture.test.js
frontend-app/src/pages/chat/model/timelineMaterializationModel.js
frontend-app/src/pages/chat/model/timelineMaterializationModel.test.js
frontend-app/src/pages/chat/hooks/useTimelineMaterialization.js
frontend-app/package.json
```

新增脚本名固定为：

```json
"benchmark:chat-history": "node scripts/chat-history-benchmark.mjs"
```

脚本输出单个 JSON 文档，至少包含 `case`, `turns`, `toolsPerTurn`, `materializedCount`, `durationMs`, `heapDeltaBytes`, `node`, `commit`。输出不得包含合成 message 文本全文。

---

## 4. 执行拓扑与所有权

### 4.1 Integration base 与 lane worktrees

```bash
git -C /Users/mima0000/Desktop/wj/super-agent-v3 fetch origin
git -C /Users/mima0000/Desktop/wj/super-agent-v3 worktree add \
  /Users/mima0000/Desktop/wj/.worktrees/v3-reasonix-frontend-next \
  -b codex/reasonix-frontend-next origin/main
cd /Users/mima0000/Desktop/wj/.worktrees/v3-reasonix-frontend-next
make codex-worktree-ready
go run ./cmd/codex-worktree-setup verify
```

Task 0 只在 integration worktree 执行并提交 `00-baseline.md`。提交后记录唯一 lane base，再从该提交建立三个独立 worktree；禁止多个 lane 共享 worktree、Git index 或 HEAD：

```bash
cd /Users/mima0000/Desktop/wj/.worktrees/v3-reasonix-frontend-next
LANE_BASE=$(git rev-parse HEAD)
git worktree add /Users/mima0000/Desktop/wj/.worktrees/v3-reasonix-frontend-next-command \
  -b codex/reasonix-frontend-next-command "$LANE_BASE"
git worktree add /Users/mima0000/Desktop/wj/.worktrees/v3-reasonix-frontend-next-history \
  -b codex/reasonix-frontend-next-history "$LANE_BASE"
git worktree add /Users/mima0000/Desktop/wj/.worktrees/v3-reasonix-frontend-next-diagnostics \
  -b codex/reasonix-frontend-next-diagnostics "$LANE_BASE"
```

每个 lane 在自己的 worktree 重新运行 `make codex-worktree-ready`、setup verify 与 LSP discovery。若任一目标 worktree/branch 已存在，先检查 `git status`、HEAD 和归属；不得删除或复用未知现场。Integrator 只在 lane commits 全部完成后，按 Task 8 固定顺序 cherry-pick，不直接编辑 lane 独占纯模块。

### 4.2 可并行 lane

| Lane | 独立 worktree / 独占范围 | 可开始条件 | 禁止修改 |
|---|---|---|---|
| A Command | `...-command`；commands、keyboard、palette、shortcut-settings、App/ChatPage/Settings、i18n、uistate preference | Task 0 baseline commit | History、diagnostics、benchmark |
| B History | `...-history`；message revision、thread prompt-history、frontend RPC/controller/Composer | Task 0 baseline commit | Command、Settings、diagnostics、benchmark |
| C Diagnostics | `...-diagnostics`；trace allowlist、performance monitor、benchmark、timeline materialization、`main.jsx`、`package.json` | Task 0 baseline commit | Command、Settings、prompt history |
| Integrator | `...-next`；按固定顺序 cherry-pick、生成物与 evidence | 各 lane RED/GREEN 证据齐全 | 不重写 lane 已验证纯模块；冲突必须回到 owning lane 修复后重新提交 |

同一 lane 内的共享热点必须串行：A 的 `App.jsx`、`ChatPage.jsx`、Settings、`preferences.go` 与 i18n；B 的 `ComposerDock.jsx`、`backend/backendRpcMethods.js`、`backendApiFactoryThread.js`、`backendResponseValidators.js`、`contract.go`、`rpc.go`、`rpc_types.go`；C 的 trace constants/events、`main.jsx`、`package.json` 与 timeline hook。跨 lane 不允许声明同一个 owned file；生成 codemap/project-map/web-dist 只由 Integrator 刷新。

依赖 DAG：

```text
Task 0 baseline/LSP
  ├── Task 1 registry + shortcut pure model
  │      └── Task 2 command runtime + preference
  │             └── Task 3 command palette integration
  ├── Task 4 prompt-history backend RPC
  │      └── Task 5 composer history integration
  └── Task 6 performance monitor
         └── Task 7 long-history benchmark

Task 3 + Task 5 + Task 6 + Task 7
  └── Task 8 integration/full gates/generated artifacts
```

---

## 5. 逐任务执行清单

### Task 0: 基线、LSP 影响面与预算冻结

**Files:**

- Read: `frontend-app/src/App.jsx`
- Read: `frontend-app/src/pages/chat/ChatPage.jsx`
- Read: `frontend-app/src/pages/chat/composer/ComposerDock.jsx`
- Read: `frontend-app/src/shared/ui/FocusTrapDialog.jsx`
- Read: `frontend-app/src/pages/settings/SettingsPage.jsx`
- Read: `frontend-app/src/shared/api/backendApi.js`
- Read: `frontend-app/src/shared/api/wails/wailsBridgeConstants.js`
- Read: `frontend-app/src/shared/api/wails/wailsBridgeTraceEvents.js`
- Read: `frontend-app/src/shared/api/backend/backendApiFactoryThread.js`
- Read: `frontend-app/src/entities/client/model/threadMessagesRuntime.js`
- Read: `frontend-app/src/pages/chat/hooks/useTimelineMaterialization.js`
- Read: `frontend-app/src/main.jsx`
- Read: `internal/module/uistate/preferences.go`
- Read: `internal/module/thread/history.go`
- Read: `internal/module/thread/lifecycle_helpers.go`
- Read: `internal/util/historyjsonl/page.go`
- Read: `internal/module/thread/contract.go`
- Read: `internal/module/thread/rpc.go`
- Create: `docs/plans/evidence/reasonix-frontend-next/00-baseline.md`

- [ ] **Step 1: 保存 Git 与工具基线**

Run the four Git commands from §0.1, then `make codex-worktree-ready`, `go run ./cmd/codex-worktree-setup verify`, and `codex mcp get lsp`.

Expected: worktree ownership is known; all seven `mcp__lsp.*` tools are visible. Otherwise record `BLOCKED` and stop.

- [ ] **Step 2: 用 LSP 锁定调用面**

Run `grep/structure → inspect → xref → file(read_file) → file(diagnostics)` for global keydown, composer keydown, Settings preference calls, `ReadMessages`/`readMessagesPageSource`, JSONL page revision, exact cwd membership, trace sanitizer/remote flush, diagnostics bootstrap, and timeline materialization.

Expected: evidence names the owner of every shared seam and every retained local Escape handler. Evidence must confirm current `agent_threads.updatedAt` is lifecycle/status metadata rather than message revision, then freeze the §3.2 replacement contract: every JSONL/loaded-session page source returns non-empty stable `sourceRevision`, exact cwd membership is checked before message reads, and trace performance phases survive the canonical sanitizer/remote-flush path. Any provider source that cannot supply a deterministic revision is `BLOCKED_PLAN_REVISION`；不得回退到秒级 thread timestamp、当前时间或前端 generation。`internal/module/thread` root 当前已有 31 个生产 Go 文件，禁止新增 root production file；纯 scanner 必须进入 `internal/module/thread/prompthistory` 子包，root 只在现有 `history.go` 做窄适配。

- [ ] **Step 3: 跑未修改基线**

```bash
cd frontend-app
npm run guard:critical-skip
npm run typecheck:contracts
npm run audit:rpc-contracts
npm test
npm run build
cd ..
go test ./internal/module/thread ./internal/module/uistate -count=1
git status --short
```

Expected: PASS and no source/generated drift from the unmodified build, other than the owned plan/evidence documents already present in the worktree; otherwise preserve the first pre-existing failure/drift verbatim in `00-baseline.md` before stopping. The pre-commit hook is separately required to regenerate and stage the project-map index from the staged snapshot, so adding this plan and `00-baseline.md` is expected to update `AI_PROJECT_DRIFT.md`、`AI_PROJECT_MANIFEST.json`、`AI_PROJECT_MAP.md` and `index/docs-agent.tsv`. Mark `BLOCKED_BASELINE_DRIFT` only when generated changes do not correspond to the staged docs, a generated-state check fails, or additional generated paths appear. If the unmodified build itself changes `cmd/agent-terminal/web-dist`、codemap 或 project-map, record the exact paths and generator output and stop.

- [ ] **Step 4: 提交基线证据**

```bash
git add docs/plans/2026-07-12-reasonix-frontend-next-absorption.md
git add docs/plans/evidence/reasonix-frontend-next/00-baseline.md
git commit -m "docs(plan): 锁定 Reasonix 前端串行基线"
```

Expected: the repository pre-commit hook refreshes and stages the four project-map files named above from the staged plan/evidence snapshot, then all generated-state and guard checks pass. The resulting commit therefore contains the two owned docs plus those four hook-owned generated files. Do not hand-edit them or use `--no-verify`; unexpected paths or failed checks are blockers.

### Task 1: 单一 Command Registry 与纯 Shortcut Model

**Files:**

- Create: `frontend-app/src/app/commands/appCommandRegistry.js`
- Create: `frontend-app/src/app/commands/appCommandRegistry.test.js`
- Create: `frontend-app/src/shared/keyboard/shortcutModel.js`
- Create: `frontend-app/src/shared/keyboard/shortcutModel.test.js`

- [ ] **Step 1: 编写 registry 与 matcher 的失败测试**

```js
const descriptor = { id: 'app.newChat', labelKey: 'commands.newChat', section: 'chat', defaultShortcut: { key: 'n', mod: true } };
expect(() => defineAppCommandRegistry([descriptor, { ...descriptor }]))
  .toThrow('duplicate command id: app.newChat');
expect(resolveShortcut({ key: 'k', mod: true }, 'darwin')).toEqual({
  key: 'k', meta: true, ctrl: false, alt: false, shift: false,
});
expect(matchesShortcut(new KeyboardEvent('keydown', { key: 'k', metaKey: true }), {
  key: 'k', meta: true, ctrl: false, alt: false, shift: false,
})).toBe(true);
```

- [ ] **Step 2: 运行测试确认 RED**

```bash
cd frontend-app
npm exec -- vitest run src/app/commands/appCommandRegistry.test.js src/shared/keyboard/shortcutModel.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because both modules are absent.

- [ ] **Step 3: 实现 descriptor-only public surface**

```js
export const APP_COMMAND_IDS = Object.freeze({
  PALETTE_OPEN: 'command.palette.open',
  CHAT_NEW: 'chat.new',
  SETTINGS_OPEN: 'settings.open',
  SIDEBAR_TOGGLE: 'sidebar.toggle',
  TURN_INTERRUPT: 'turn.interrupt',
});

export function defineAppCommandRegistry(descriptors) {
  const ids = new Set();
  const result = descriptors.map((descriptor) => {
    assertExactCommandDescriptor(descriptor);
    if (!descriptor.id || !descriptor.labelKey || !descriptor.section) throw new Error('invalid command descriptor');
    if (ids.has(descriptor.id)) throw new Error(`duplicate command id: ${descriptor.id}`);
    ids.add(descriptor.id);
    return Object.freeze({
      id: descriptor.id,
      labelKey: descriptor.labelKey,
      helpKey: descriptor.helpKey,
      section: descriptor.section,
      defaultShortcut: Object.freeze({ ...descriptor.defaultShortcut }),
      editablePolicy: descriptor.editablePolicy,
      repeatable: descriptor.repeatable,
      capabilityKey: descriptor.capabilityKey,
    });
  });
  return Object.freeze(result);
}
```

`assertExactCommandDescriptor` rejects unknown fields and any `run`/handler field; optional fields keep exact types. `shortcutModel.js` must export `resolveShortcut`, `matchesShortcut`, `shortcutConflict`, and `isEditableShortcutTarget`; exact modifier matching must reject extra modifiers, IME composition, repeated events disallowed by descriptor, editable targets disallowed by descriptor, and `defaultPrevented` events.

- [ ] **Step 4: 运行 GREEN 与 diagnostics**

Run the Step 2 command, then LSP diagnostics on all four files.

Expected: tests PASS and diagnostics are empty.

- [ ] **Step 5: 原子提交**

```bash
git add frontend-app/src/app/commands frontend-app/src/shared/keyboard
git commit -m "feat(frontend): 新增命令与快捷键契约"
```

### Task 2: Command Runtime、全局 Dispatcher 与 Typed Preference

**Files:**

- Create: `frontend-app/src/app/commands/appCommandRuntime.js`
- Create: `frontend-app/src/app/commands/appCommandRuntime.test.js`
- Create: `frontend-app/src/app/commands/useAppCommandDispatcher.js`
- Create: `frontend-app/src/app/commands/useAppCommandDispatcher.test.jsx`
- Modify: `frontend-app/src/App.jsx`
- Modify: `frontend-app/src/App.test.jsx`
- Modify: `frontend-app/src/pages/chat/ChatPage.jsx`
- Modify: `internal/module/uistate/preferences.go`
- Modify: `internal/module/uistate/model_providers_test.go`

- [ ] **Step 1: 编写 runtime、listener 与 preference RED tests**

```js
expect(() => createAppCommandRuntime({ registry, bindings: {} }))
  .toThrow('missing command handler: command.palette.open');
expect(() => createAppCommandRuntime({ registry, bindings: { unknown: vi.fn() } }))
  .toThrow('unknown command binding: unknown');
expect(() => createAppCommandRuntime({ registry, bindings, overrides: { unknown: shortcut } }))
  .toThrow('unknown shortcut override: unknown');
expect(() => createAppCommandRuntime({ registry, bindings: { ...bindings, 'chat.new': { run, id: 'shadow' } } }))
  .toThrow('unknown command binding field: chat.new.id');
expect(eventTarget.addEventListener).toHaveBeenCalledOnce();
expect(eventTarget.removeEventListener).toHaveBeenCalledWith('keydown', expect.any(Function));
```

Add tests that reject platform-resolved duplicate effective combinations before listener installation. Add Go table cases that reject 65 overrides, blank ids/keys, non-boolean modifiers, and extra fields under `settings.shortcuts.bindings`.

- [ ] **Step 2: 运行测试确认 RED**

```bash
cd frontend-app
npm exec -- vitest run src/app/commands/appCommandRuntime.test.js src/app/commands/useAppCommandDispatcher.test.jsx src/App.test.jsx --no-file-parallelism --maxWorkers=1
cd ..
go test ./internal/module/uistate -run Shortcut -count=1
```

Expected: FAIL on missing runtime and missing preference validator.

- [ ] **Step 3: 实现 runtime contract**

```js
export function createAppCommandRuntime({ registry, bindings, overrides = {}, platform }) {
  const known = new Set(registry.map(({ id }) => id));
  Object.keys(bindings).forEach((id) => {
    if (!known.has(id)) throw new Error(`unknown command binding: ${id}`);
    assertExactCommandBinding(id, bindings[id]);
  });
  Object.keys(overrides).forEach((id) => {
    if (!known.has(id)) throw new Error(`unknown shortcut override: ${id}`);
  });
  const commands = registry.map((descriptor) => {
    const binding = bindings[descriptor.id];
    if (!binding) throw new Error(`missing command handler: ${descriptor.id}`);
    return Object.freeze({
      id: descriptor.id,
      labelKey: descriptor.labelKey,
      section: descriptor.section,
      editablePolicy: descriptor.editablePolicy,
      repeatable: descriptor.repeatable,
      capabilityKey: descriptor.capabilityKey,
      shortcut: resolveShortcut(overrides[descriptor.id] ?? descriptor.defaultShortcut, platform),
      run: binding.run,
      canExecute: binding.canExecute,
      disabledReason: binding.disabledReason,
    });
  });
  assertNoEffectiveShortcutConflicts(commands);
  const byId = new Map(commands.map((command) => [command.id, command]));
  return Object.freeze({ commands, execute: (id) => executeCommand(byId, id) });
}

function executeCommand(byId, id) {
  const command = byId.get(id);
  if (!command) throw new Error(`unknown command: ${id}`);
  if (command.canExecute && !command.canExecute()) return { executed: false, reason: command.disabledReason };
  command.run();
  return { executed: true, reason: '' };
}
```

`assertExactCommandBinding` 只允许 `run`、`canExecute`、`disabledReason`，并验证 `run`/`canExecute` 类型与 `disabledReason` 字符串 shape；不得让 binding 覆盖 registry descriptor。The hook must install exactly one `window` keydown listener and call `runtime.execute(id)` only after `matchesShortcut` accepts the event. Remove only the superseded app-global listener from `ChatPage.jsx`; retain dialog/preview/composer-local handlers.

- [ ] **Step 4: 实现 exact backend preference validation**

Add `preferenceShortcutBindings = "settings.shortcuts.bindings"` and a validator that accepts only an object of at most 64 entries. Each value must be an object with exactly `key`, `meta`, `ctrl`, `alt`, `shift`; `key` length is 1..32 and all modifiers are booleans. Extend `cloneJSONValue` coverage tests so callers cannot mutate stored bindings.

In `App.jsx`, bind all registry commands to real handlers in this task. `command.palette.open` owns real `paletteOpen` state even though Task 3 renders the palette; `chat.new`、`settings.open`、`sidebar.toggle`、`turn.interrupt` 必须绑定现有动作并由 `App.test.jsx` 证明。Task 2 只以空 override 启动默认 shortcut；禁止临时 noop handler。Task 3 接入唯一 preference controller 后再覆盖 runtime shortcuts。

- [ ] **Step 5: 运行 GREEN、LSP diagnostics 与原子提交**

Run Step 2 plus `npm run typecheck:contracts`; expected PASS. Then run diagnostics on every modified file.

```bash
git add frontend-app/src/app/commands/appCommandRuntime.js frontend-app/src/app/commands/appCommandRuntime.test.js frontend-app/src/app/commands/useAppCommandDispatcher.js frontend-app/src/app/commands/useAppCommandDispatcher.test.jsx frontend-app/src/App.jsx frontend-app/src/App.test.jsx frontend-app/src/pages/chat/ChatPage.jsx internal/module/uistate/preferences.go internal/module/uistate/model_providers_test.go
git commit -m "feat(frontend): 将全局命令绑定到类型化快捷键"
```

### Task 3: Command Palette UI 与可访问性

**Files:**

- Create: `frontend-app/src/features/command-palette/model/commandPaletteModel.js`
- Create: `frontend-app/src/features/command-palette/model/commandPaletteModel.test.js`
- Create: `frontend-app/src/features/command-palette/ui/CommandPalette.jsx`
- Create: `frontend-app/src/features/command-palette/ui/CommandPalette.test.jsx`
- Create: `frontend-app/src/features/command-palette/ui/CommandPalette.css`
- Create: `frontend-app/src/features/shortcut-settings/model/shortcutSettingsModel.js`
- Create: `frontend-app/src/features/shortcut-settings/model/shortcutSettingsModel.test.js`
- Create: `frontend-app/src/features/shortcut-settings/hooks/useShortcutSettings.js`
- Create: `frontend-app/src/features/shortcut-settings/hooks/useShortcutSettings.test.jsx`
- Create: `frontend-app/src/features/shortcut-settings/ui/ShortcutSettingsCard.jsx`
- Create: `frontend-app/src/features/shortcut-settings/ui/ShortcutSettingsCard.test.jsx`
- Create: `frontend-app/src/features/shortcut-settings/ui/ShortcutSettingsCard.css`
- Modify: `frontend-app/src/App.jsx`
- Modify: `frontend-app/src/App.test.jsx`
- Modify: `frontend-app/src/pages/settings/SettingsPage.jsx`
- Modify: `frontend-app/src/pages/settings/SettingsPage.test.jsx`
- Modify: `frontend-app/src/shared/i18n/appI18n.zh.json`
- Modify: `frontend-app/src/shared/i18n/appI18n.en.json`

- [ ] **Step 1: 编写 palette model 与 component RED tests**

```jsx
render(<CommandPalette open commands={commands} execute={execute} onClose={onClose} copy={copy} />);
expect(screen.getByRole('dialog', { name: copy.title })).toBeInTheDocument();
await user.type(screen.getByRole('searchbox'), 'sett');
expect(screen.getByRole('option', { name: /settings/i })).toBeInTheDocument();
await user.keyboard('{ArrowDown}{Enter}');
expect(execute).toHaveBeenCalledWith('settings.open');
```

Cover subsequence search, stable section order, Home/End, empty state, disabled reason, Escape, focus entry/restore, and both locales.

Add shortcut-settings tests for registry-projected labels/help/defaults, platform-resolved display, edit/save/reset, unknown preference id, duplicate effective combination, stale async load after cwd change, save failure preserving the draft, successful read-after-write rebuild, runtime rebind after save, and both locales. The hook receives `getPreference/setPreference` as injected dependencies from App composition and must reject missing dependencies; it must not import page services, store, router, or a second command descriptor list.

- [ ] **Step 2: 运行测试确认 RED**

```bash
cd frontend-app
npm exec -- vitest run src/features/command-palette/model/commandPaletteModel.test.js src/features/command-palette/ui/CommandPalette.test.jsx src/features/shortcut-settings --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because feature files do not exist.

- [ ] **Step 3: 实现 pure model 与 FocusTrapDialog UI**

```jsx
export function CommandPalette({ open, commands, execute, onClose, copy }) {
  if (!open) return null;
  return (
    <FocusTrapDialog ariaLabel={copy.title} className="command-palette" initialFocusSelector="[role=searchbox]" onClose={onClose}>
      <CommandPaletteBody commands={commands} execute={execute} onClose={onClose} copy={copy} />
    </FocusTrapDialog>
  );
}
```

`CommandPaletteBody` must keep only query/activeIndex UI state and call `filterCommandPaletteItems(commands, query)`. It must not import backend, store, or router. CSS must use existing layer variables and add no numeric production `z-index`.

`ShortcutSettingsCard` receives registry projection and controller state only; one App-level `useShortcutSettings` instance owns injected `getPreference/setPreference` calls, cwd generation, draft overrides, validation and read-after-write. The same validated overrides feed `createAppCommandRuntime`, and `SettingsPage` receives the controller through props rather than creating another hook. Reset writes `{}`. Unknown ids or conflicts block the whole override set and show a visible error; neither the card nor hook may copy a second command descriptor list.

- [ ] **Step 4: 接入 App composition 并运行 GREEN**

Render `CommandPalette` from the `paletteOpen` state and handler already bound in Task 2; pass only `runtime.commands` and `runtime.execute`. Create the single shortcut controller in App composition, rebuild runtime from its validated overrides, and pass that controller to `SettingsPageView` for `ShortcutSettingsCard`. Loading failure、unknown id 或 effective conflict 显示配置错误并阻止 dispatcher 安装；不得回退到默认 shortcut 后继续。Run Step 2, `App.test.jsx`, Settings tests, z-index Vitest, and the production z-index guard. Expected: PASS.

- [ ] **Step 5: 原子提交**

```bash
git add frontend-app/src/features/command-palette frontend-app/src/features/shortcut-settings frontend-app/src/App.jsx frontend-app/src/App.test.jsx frontend-app/src/pages/settings/SettingsPage.jsx frontend-app/src/pages/settings/SettingsPage.test.jsx frontend-app/src/shared/i18n
git commit -m "feat(frontend): 新增命令面板与快捷键设置"
```

### Task 4: 后端权威 Prompt History RPC

**Files:**

- Create: `internal/module/thread/prompt_history_test.go`
- Create: `internal/module/thread/prompthistory/scanner.go`
- Create: `internal/module/thread/prompthistory/scanner_test.go`
- Create: `internal/dto/thread/prompt_history.go`
- Create: `internal/dto/thread/prompt_history_test.go`
- Modify: `internal/module/thread/history.go`
- Modify: `internal/module/thread/lifecycle_helpers.go`
- Modify: `internal/module/thread/persistence_port.go`
- Modify: `internal/module/thread/contract.go`
- Modify: `internal/module/thread/rpc.go`
- Modify: `internal/module/thread/rpc_types.go`
- Modify: `internal/module/thread/service_handlers_test.go`
- Modify: `internal/dto/provider/message.go`
- Modify: `internal/dto/provider/message_test.go`
- Modify: `internal/util/historyjsonl/page.go`
- Modify: `internal/util/historyjsonl/history_test.go`
- Modify: `internal/provider/claudecli/session_history.go`
- Modify: `internal/provider/claudecli/session_history_test.go`

- [ ] **Step 1: 写 DTO、cursor 与 scan RED tests**

```go
func TestScanPromptHistoryActiveThreadFirstAndKeepsDuplicates(t *testing.T) {}
func TestScanPromptHistoryCursorCrossesThreadWithoutGap(t *testing.T) {}
func TestScanPromptHistoryRejectsStaleNonce(t *testing.T) {}
func TestScanPromptHistoryRejectsActiveThreadFromDifferentCWDBeforeRead(t *testing.T) {}
func TestPromptHistoryNonceChangesForSameSecondDuplicateUserMessage(t *testing.T) {}
func TestMessagePageSourceRevisionChangesWhenJSONLAppends(t *testing.T) {}
func TestMessagePageSourceRevisionDoesNotExposePathOrContent(t *testing.T) {}
func TestPromptHistoryParamsRejectUnknownField(t *testing.T) {}
func TestScanPromptHistoryStopsOnContextCancellation(t *testing.T) {}
```

Each test must use concrete user/assistant/tool fixtures and assert exact entries, cursor, `hasMore`, nonce, source revision, exact-cwd read count, and error code; test errors/logs/revisions must not contain fixture prompt text or cwd. Add provider DTO and JSONL tests proving `sourceRevision` is non-empty on every successful page, stable for the same source snapshot, changes after append even within the same second, and is identical across later-page reads of the same snapshot.

- [ ] **Step 2: 运行测试确认 RED**

```bash
go test ./internal/dto/provider ./internal/dto/thread ./internal/util/historyjsonl ./internal/provider/claudecli ./internal/module/thread/prompthistory ./internal/module/thread -run 'PromptHistory|SourceRevision' -count=1
```

Expected: FAIL because `ScanPromptHistory` and the RPC contract are absent.

- [ ] **Step 3: 添加 exact DTO 与 service surface**

```go
type PromptHistoryEntry struct {
	ThreadID string    `json:"threadId"`
	MessageID string   `json:"messageId"`
	Text      string    `json:"text"`
	CreatedAt time.Time `json:"createdAt"`
}

type PromptHistoryResult struct {
	Entries    []PromptHistoryEntry `json:"entries"`
	NextCursor string               `json:"nextCursor"`
	HasMore    bool                 `json:"hasMore"`
	Nonce      string               `json:"nonce"`
}
```

These aggregate wire types are owned by `internal/dto/thread/prompt_history.go`; `internal/dto/provider/message.go` only adds `SourceRevision string` to `MessagePageResult`. The new `prompthistory` subpackage owns pure snapshot ordering、cursor、nonce 与 scan state，依赖 caller-supplied thread snapshots/page-reader callback，不 import parent `thread` package。Add `ScanPromptHistory(ctx context.Context, req PromptHistoryRequest) (threaddto.PromptHistoryResult, error)` to `thread.Service`; its narrow adapter stays in existing `history.go`. `PromptHistoryRequest` has `CWD`, `ActiveThreadID`, `Cursor`, `Nonce`, and `Limit`.

- [ ] **Step 4: 实现 scanner、nonce 与 strict RPC**

Reuse `readMessagesPageSource` and propagate `SourceRevision` through JSONL and `messagePageReaderSession` paths. JSONL revision is SHA-256 over non-secret source facts including file length and the last complete record digest; it must not contain the path, raw line or timestamp-only freshness. A provider page reader must return the same revision for every page of one source snapshot. The legacy `session.ReadHistory` fallback may remain for existing `thread/messages`, but `ScanPromptHistory` must reject it with a typed revision-unavailable error unless the provider supplies a stable revision port; it must not hash a page-local window or array index to fabricate freshness. Missing revision is an error.

Normalize the request cwd with the thread module's canonical cwd comparison helper, list canonical `ThreadRecord` values, exact-filter to that cwd, enforce the 100-thread snapshot cap, and verify non-empty `activeThreadId` is in that set before calling any message reader. Scan only `role=user`; preserve duplicate text. Order active thread first, then exact-cwd threads by `updatedAt DESC, threadId ASC`. Compute nonce exactly as §3.2 from ordered identity/lifecycle/sourceRevision. Cursor JSON contains only version, nonce, thread index, and message `before`; base64url encode it, cap it at 2048 bytes, and reject unknown fields/version/nonce mismatch. Register `thread/promptHistory` with a strict typed handler.

- [ ] **Step 5: 运行 GREEN、diagnostics 与提交**

```bash
go test ./internal/module/thread -run 'PromptHistory|ThreadHandlers' -count=1
go test ./internal/dto/provider ./internal/dto/thread ./internal/util/historyjsonl ./internal/provider/claudecli ./internal/module/thread/prompthistory ./internal/module/thread -count=1
```

Expected: PASS. Then run LSP diagnostics on every Go file.

```bash
git add internal/module/thread/prompt_history_test.go internal/module/thread/prompthistory internal/module/thread/history.go internal/module/thread/lifecycle_helpers.go internal/module/thread/persistence_port.go internal/module/thread/contract.go internal/module/thread/rpc.go internal/module/thread/rpc_types.go internal/module/thread/service_handlers_test.go
git add internal/dto/provider/message.go internal/dto/provider/message_test.go internal/dto/thread/prompt_history.go internal/dto/thread/prompt_history_test.go internal/util/historyjsonl/page.go internal/util/historyjsonl/history_test.go internal/provider/claudecli/session_history.go internal/provider/claudecli/session_history_test.go
git commit -m "feat(thread): 暴露权威输入历史分页"
```

### Task 5: Frontend Prompt History Contract 与 Composer 导航

**Files:**

- Create: `frontend-app/src/features/prompt-history/model/promptHistoryController.js`
- Create: `frontend-app/src/features/prompt-history/model/promptHistoryController.test.js`
- Create: `frontend-app/src/features/prompt-history/hooks/usePromptHistory.js`
- Create: `frontend-app/src/features/prompt-history/hooks/usePromptHistory.test.jsx`
- Modify: `frontend-app/src/shared/api/backend/backendRpcMethods.js`
- Modify: `frontend-app/src/shared/api/backend/backendApiFactoryThread.js`
- Modify: `frontend-app/src/shared/api/backendResponseValidators.js`
- Modify: `frontend-app/src/shared/api/backendResponseValidators.test.js`
- Modify: `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- Modify: `frontend-app/src/shared/api/backendApi.contractMatrix.test.js`
- Modify: `frontend-app/src/shared/api/backendApi.test.js`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.jsx`
- Modify: `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx`

- [ ] **Step 1: 写 API/controller/composer RED tests**

```js
const controller = createPromptHistoryController({ fetchPage, cwd: '/repo', activeThreadId: 'thread-1' });
controller.captureDraft('unfinished');
await expect(controller.previous()).resolves.toBe('older prompt');
expect(controller.next()).toBe('unfinished');
```

Cover exact request/response fields, limit 1..50, active-thread cwd mismatch error, stale nonce one reset/retry, second stale error, pending dedupe, generation race, cwd invalidation, send success/failure, duplicate text, IME/selection/multiline caret boundaries, and no writes to thread timeline/store.

- [ ] **Step 2: 运行测试确认 RED**

```bash
cd frontend-app
npm exec -- vitest run src/shared/api/backendResponseValidators.test.js src/shared/api/backendApi.test.js src/shared/api/backendApi.contractMatrix.test.js src/features/prompt-history src/pages/chat/composer/ComposerDock.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL on missing RPC method and controller.

- [ ] **Step 3: 实现 API 与 controller public surface**

```js
export function createPromptHistoryController({ fetchPage, cwd, activeThreadId }) {
  if (typeof fetchPage !== 'function') throw new Error('fetchPage is required');
  if (!String(cwd || '').trim()) throw new Error('cwd is required');
  return createPromptHistoryStateMachine({ fetchPage, cwd, activeThreadId });
}
```

The state machine must expose `captureDraft`, async `previous`, `next`, `invalidate`, and `snapshot`. It owns entries/index/cursor/nonce/generation/pending/draft sentinel. It retries stale nonce exactly once and throws the second stale error. It is created per hook instance, never at module scope.

- [ ] **Step 4: 接入 ComposerDock boundary policy**

Add `shouldNavigatePromptHistory(event, textarea, direction)` as a tested pure helper. It returns true only when draft navigation is at the top/bottom boundary, selection is collapsed, IME is inactive, and the event is not already prevented. Selected history replaces draft but never calls send.

- [ ] **Step 5: 运行 GREEN、contracts、diagnostics 与提交**

Run Step 2, then `npm run typecheck:contracts` and `npm run audit:rpc-contracts`. Expected: PASS.

```bash
git add frontend-app/src/features/prompt-history frontend-app/src/shared/api/backend/backendRpcMethods.js frontend-app/src/shared/api/backend/backendApiFactoryThread.js frontend-app/src/shared/api/backendResponseValidators.js frontend-app/src/shared/api/backendResponseValidators.test.js frontend-app/src/shared/api/backendApi.contractMatrix.js frontend-app/src/shared/api/backendApi.contractMatrix.test.js frontend-app/src/shared/api/backendApi.test.js frontend-app/src/pages/chat/composer/ComposerDock.jsx frontend-app/src/pages/chat/composer/ComposerDock.test.jsx
git commit -m "feat(frontend): 导航权威输入历史"
```

### Task 6: Headless Performance Pressure Monitor

**Files:**

- Create: `frontend-app/src/shared/diagnostics/frontendPerformancePressure.js`
- Create: `frontend-app/src/shared/diagnostics/frontendPerformancePressure.test.js`
- Modify: `frontend-app/src/main.jsx`
- Modify: `frontend-app/src/app/AppErrorBoundary.test.jsx`
- Modify: `frontend-app/src/shared/api/wails/wailsBridgeConstants.js`
- Modify: `frontend-app/src/shared/api/wails/wailsBridgeTraceEvents.js`
- Modify: `frontend-app/src/shared/api/wailsBridge.test.js`

- [ ] **Step 1: 写 fake-clock/observer RED tests**

```js
const monitor = startFrontendPerformancePressure({ reporter, clock, scheduler, visibility, observerFactory });
clock.advance(15_000);
observerFactory.emit({ duration: 250 });
expect(reporter).toHaveBeenCalledWith(expect.objectContaining({
  phase: 'frontend.performance.long_task_pressure',
  status: 'slow',
  metadata: expect.objectContaining({ count: 1, max_ms: 250 }),
}));
monitor.stop();
expect(observerFactory.disconnect).toHaveBeenCalledOnce();
```

Cover startup/resume grace, hidden/unfocused, 10-minute cooldown, category dedupe, unsupported longtask/heap, reporter throw/Promise rejection/`false` return, payload allowlist, and StrictMode double mount/unmount. Add a real bridge test that calls the production `emitFrontendTraceEvent` for every new phase and asserts `true`, queued sanitized payload exact keys, forbidden content absence, and remote flush eligibility.

- [ ] **Step 2: 运行测试确认 RED**

```bash
cd frontend-app
npm exec -- vitest run src/shared/diagnostics/frontendPerformancePressure.test.js src/app/AppErrorBoundary.test.jsx src/shared/api/wailsBridge.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because the monitor module is absent.

- [ ] **Step 3: 实现 monitor exact surface**

```js
export const FRONTEND_PERFORMANCE_POLICY = Object.freeze({
  startupGraceMs: 15_000,
  resumeGraceMs: 5_000,
  cooldownMs: 600_000,
  longTaskMs: 200,
  eventLoopLagMs: 150,
  consecutiveSamples: 3,
  heapRatio: 0.85,
});

export function startFrontendPerformancePressure(deps) {
  const runtime = createPerformancePressureRuntime(deps, FRONTEND_PERFORMANCE_POLICY);
  runtime.start();
  return Object.freeze({ stop: () => runtime.stop(), capabilities: runtime.capabilities() });
}
```

Reports use the exact trace envelope from §3.3: top-level only `phase`, `status`, optional `duration_ms`, and allowlisted `metadata`; sanitizer owns `ts`. Extend `FRONTEND_TRACE_ALLOWED_PHASES`, `FRONTEND_TRACE_ALLOWED_METADATA_KEYS`, and `shouldRemoteFlushFrontendTrace` for the four performance phases before wiring the monitor. Reuse `emitFrontendTraceEvent`; no component imports and no localStorage. A reporter `false` return is a contract failure, not capability absence; surface it through the existing visible diagnostics/error path and never mark the sample reported.

- [ ] **Step 4: 接入 main bootstrap，运行 GREEN 与提交**

Start once beside existing crash/Profiler bootstrap and retain cleanup for test/HMR teardown. Run Step 2, `npm run typecheck:contracts`, `npm run audit:rpc-contracts`, and LSP diagnostics; expected PASS.

```bash
git add frontend-app/src/shared/diagnostics/frontendPerformancePressure.js frontend-app/src/shared/diagnostics/frontendPerformancePressure.test.js frontend-app/src/main.jsx frontend-app/src/app/AppErrorBoundary.test.jsx frontend-app/src/shared/api/wails/wailsBridgeConstants.js frontend-app/src/shared/api/wails/wailsBridgeTraceEvents.js frontend-app/src/shared/api/wailsBridge.test.js
git commit -m "feat(frontend): 上报有界性能压力"
```

### Task 7: 长历史合成 Benchmark 与非抖动门禁

**Files:**

- Create: `frontend-app/scripts/chat-history-benchmark.mjs`
- Create: `frontend-app/scripts/chat-history-benchmark.test.mjs`
- Create: `frontend-app/src/pages/chat/model/chatHistoryBenchmarkFixture.js`
- Create: `frontend-app/src/pages/chat/model/chatHistoryBenchmarkFixture.test.js`
- Create: `frontend-app/src/pages/chat/model/timelineMaterializationModel.js`
- Create: `frontend-app/src/pages/chat/model/timelineMaterializationModel.test.js`
- Modify: `frontend-app/src/pages/chat/hooks/useTimelineMaterialization.js`
- Modify: `frontend-app/package.json`

- [ ] **Step 1: 写 fixture/materialization/JSON RED tests**

```js
const history = buildChatHistoryFixture({ turns: 5000, toolsPerTurn: 3, archived: true, seed: 7 });
expect(history).toHaveLength(5000 * 5);
expect(selectMaterializedTimeline(history, 80)).toEqual(history.slice(-80));
expect(JSON.stringify(measureChatHistoryCase(history))).not.toContain('synthetic-message-body');
```

Default cases are 200/1000/5000 turns with 1/3 tools. The 10000-turn case exists only behind `--extended`. Timing and heap values are numbers but never compared with a machine-wide absolute threshold.

- [ ] **Step 2: 运行测试确认 RED**

```bash
cd frontend-app
npm exec -- vitest run scripts/chat-history-benchmark.test.mjs src/pages/chat/model/chatHistoryBenchmarkFixture.test.js src/pages/chat/model/timelineMaterializationModel.test.js --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because benchmark modules are absent.

- [ ] **Step 3: 提取真实 materialization pure model**

```js
export const TIMELINE_INITIAL_MATERIALIZED_MESSAGES = 80;
export const TIMELINE_MATERIALIZATION_INCREMENT = 80;

export function selectMaterializedTimeline(messages, count) {
  const materializedCount = Math.max(TIMELINE_INITIAL_MATERIALIZED_MESSAGES, count);
  return messages.slice(Math.max(0, messages.length - materializedCount));
}
```

Modify `useTimelineMaterialization.js` to consume these exports; delete its duplicate constants/slice calculation. Benchmark and production hook must import the same model.

- [ ] **Step 4: 实现 deterministic runner 与 package script**

The underlying Node script must print one JSON document with only `case`, `turns`, `toolsPerTurn`, `materializedCount`, `durationMs`, `heapDeltaBytes`, `node`, and `commit`. It must not print fixture content. Add exactly `"benchmark:chat-history": "node scripts/chat-history-benchmark.mjs"` to `package.json`; consumers that parse stdout must invoke it with `npm run --silent` because normal npm lifecycle headers are not JSON.

- [ ] **Step 5: 运行 GREEN、报告解析与提交**

```bash
cd frontend-app
npm exec -- vitest run scripts/chat-history-benchmark.test.mjs src/pages/chat/model/chatHistoryBenchmarkFixture.test.js src/pages/chat/model/timelineMaterializationModel.test.js --no-file-parallelism --maxWorkers=1
npm run --silent benchmark:chat-history > /tmp/v3-chat-history-benchmark.json
node -e 'JSON.parse(require("fs").readFileSync("/tmp/v3-chat-history-benchmark.json","utf8"))'
```

Expected: PASS and valid JSON. Do not commit `/tmp` output.

```bash
git add frontend-app/scripts/chat-history-benchmark.mjs frontend-app/scripts/chat-history-benchmark.test.mjs frontend-app/src/pages/chat/model frontend-app/src/pages/chat/hooks/useTimelineMaterialization.js frontend-app/package.json
git commit -m "test(frontend): 新增长历史确定性基准"
```

### Task 8: 集成、全量门禁、生成物与独立复核

**Files:**

- Create: `docs/plans/evidence/reasonix-frontend-next/01-command.md`
- Create: `docs/plans/evidence/reasonix-frontend-next/02-prompt-history.md`
- Create: `docs/plans/evidence/reasonix-frontend-next/03-performance.md`
- Create: `docs/plans/evidence/reasonix-frontend-next/04-benchmark.md`
- Create: `docs/plans/evidence/reasonix-frontend-next/05-full-gates.md`
- Create: `docs/plans/evidence/reasonix-frontend-next/06-independent-review.md`
- Modify: generated files reported by repository-owned generators only

- [ ] **Step 1: 串行 cherry-pick lane commits**

In the integration worktree, verify a clean index and cherry-pick complete lane commits in this fixed order: A command/shortcut/palette/settings → B message revision/prompt-history/composer → C trace contract/performance/benchmark. Record every commit SHA. If cherry-pick conflicts on an owned file, abort that cherry-pick and return the conflict to the owning lane for a new commit；Integrator 不得在冲突中即席重写 lane 逻辑。Each successful cherry-pick is followed immediately by that lane's focused GREEN command.

- [ ] **Step 2: 运行 targeted suite**

```bash
cd frontend-app
npm exec -- vitest run src/app/commands src/shared/keyboard src/features/command-palette src/features/shortcut-settings src/features/prompt-history src/pages/settings/SettingsPage.test.jsx src/pages/chat/composer/ComposerDock.test.jsx src/shared/diagnostics/frontendPerformancePressure.test.js src/shared/api/wailsBridge.test.js scripts/frontend-z-index-token-guard.test.mjs scripts/chat-history-benchmark.test.mjs --no-file-parallelism --maxWorkers=1
```

Expected: PASS with exact file/test counts recorded in `05-full-gates.md`.

- [ ] **Step 3: 运行 frontend/backend/repository full gates**

```bash
cd frontend-app
npm run lint
npm test
npm run build
cd ..
go test ./internal/dto/provider ./internal/dto/thread ./internal/util/historyjsonl ./internal/provider/claudecli ./internal/module/thread/prompthistory ./internal/module/thread ./internal/module/uistate -count=1
make guard
git diff --check
```

Expected: all exit 0. Preserve first failures separately from later green reruns.

- [ ] **Step 4: 刷新规范生成物**

Run only generators named by the failing generated-state guard. Re-run that guard and `git diff --check`. Do not hand-edit codemap, project-map, or `web-dist`.

- [ ] **Step 5: 三面独立复核**

Reviewer A reviews only command/shortcut/palette/settings; Reviewer B only message revision/prompt history/cwd isolation; Reviewer C only trace contract/performance/benchmark. Record every finding as `FIXED`, `REJECTED_WITH_EVIDENCE`, or `BLOCKED`, with reviewer identity/session and exact file/test evidence. A global `PASS` without per-finding disposition is invalid.

- [ ] **Step 6: 提交 evidence 与生成物**

```bash
git add docs/plans/evidence/reasonix-frontend-next
git add docs/doc/codemap cmd/agent-terminal/web-dist
git commit -m "docs(plan): 记录 Reasonix 前端后续证据"
```

Before the second `git add`, verify `git diff --name-only docs/doc/codemap cmd/agent-terminal/web-dist` contains only repository-generator output. If neither path changed, omit that command. If a generator reports a different path, stop and add that exact path to this plan before staging; do not use `git add -A`.

---

## 6. 验收证据格式

保存于：

```text
docs/plans/evidence/reasonix-frontend-next/
  00-baseline.md
  01-command.md
  02-prompt-history.md
  03-performance.md
  04-benchmark.md
  05-full-gates.md
  06-independent-review.md
```

每份至少包含：

```text
HEAD / origin/main / lane base / branch / worktree path
lane commit SHAs and Integrator cherry-pick order
owned files
LSP locate/inspect/xref/read/diagnostics evidence
RED command + expected failure + exit
GREEN command + pass count + exit
full gate command + exit
generated artifacts changed and generator
remaining blockers
reviewer identity/session and disposition
```

空日志、只写 `PASS`、最后一次绿色重跑、或 reviewer 自报 `DONE` 都不是完成证据。首次 deterministic failure、flaky/timeout、生成物 churn 与最终重跑必须分别保留。

---

## 7. 失败策略与停止条件

- LSP namespace 不可用：`BLOCKED`，停止生产修改。
- preference malformed、未知 command id 或平台投影后冲突：显示配置错误并阻止整份 override/dispatcher，不用默认值掩盖；Settings 草稿保留用于修复。
- prompt history nonce stale：最多一次显式 reset/retry；重复 stale 向用户显示错误。
- prompt history source revision 缺失/为空/不稳定，或 active thread 不属于 exact cwd：`BLOCKED`/typed error，在读取或返回任何 prompt 前停止；不得改用 `agent_threads.updatedAt`。
- history source 某 thread 读取失败：整页失败，不返回不完整成功。
- PerformanceObserver capability absent：通过 allowlisted capability phase 记录有界状态并 no-op；trace sanitizer/remote flush 返回 `false`、其他初始化或上报错误必须可见。
- benchmark timing 漂移：保留报告，不调整生产代码追逐一次机器结果。
- `internal/module/thread` root production file count 增加：`BLOCKED`，把纯逻辑留在 `prompthistory` 子包；不得扩大 package-count baseline。
- 任何新目录/文件触发 size/production guard：先重划职责或减少文件，不提升阈值、不扩大 baseline。
- 新功能要求第二种 backend decision、系统级 OS shortcut、多 tab truth 或遥测后端变更：停止并另立设计，不顺带扩 scope。

---

## 8. Definition of Done

只有同时满足以下条件才可称为完成：

- command registry 是快捷键、palette、Settings 和帮助元数据的唯一来源，binding 无法覆盖 descriptor，真实 command 均有 handler/capability 证据。
- shortcut override 通过 Settings 与 typed backend preference 读写、重置并 read-after-write，malformed/unknown/conflict/save failure 均可见且 fail fast。
- palette 具备完整键盘/focus/a11y 测试，并复用现有 overlay/layer primitives。
- prompt history 从后端真实 history 分页读取，canonical source revision、同秒重复消息、exact-cwd membership、cursor/nonce、race、draft sentinel 和跨 cwd invalidation 均有 RED/GREEN 证据。
- performance pressure 只发 canonical trace envelope 中 allowlisted bounded metrics，真实 `emitFrontendTraceEvent` 返回 true，observer/timer lifecycle 在 StrictMode 下无泄漏。
- long-history benchmark 使用真实 materialization 入口，默认 CI 无 wall-clock flaky threshold。
- LSP diagnostics 全部处理；无法处理项明确 blocker，不能写成 PASS。
- targeted、frontend lint/test/build、相关 Go tests、repository guard 全部有命令、exit 和输出摘要。
- generated artifacts 由规范生成器更新，`git diff --check` 通过，最终 diff 只包含本计划范围。
- 三个 lane 使用独立 worktree/branch，Integrator 只按记录的 SHA 串行 cherry-pick；三个独立审查面均有逐项 disposition，所有 P0/P1 问题关闭或明确阻塞。

完成后仍不代表吸收 Reasonix 的全部前端设计。`AnchoredPopover`、通用 Ask/Plan decision、多 Tab 工作台、OS 级全局快捷键继续保持 defer/reject，直到 V3 出现真实产品需求和 typed contract。
