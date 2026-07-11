# Reasonix 前端架构机制吸收执行计划

> For agentic workers: 本文是实施约束，不是现状文档。2026-07-11 经两个独立 reviewer 复核后修订。执行前必须锁定基线、建立隔离 worktree、恢复 LSP 工具面，并按 RED → GREEN → 全量门禁保存证据。不得把目标结构描述成已经落地。

**Goal:** 在不削弱 V3 现有契约、守卫和模块边界的前提下，吸收 Reasonix 中对个人 AI 开发有明确收益的前端机制：最新用户意图优先、可靠滚动意图、严格恢复响应、生产崩溃隔离、approval-only 决策体验和可治理的视觉层级。

**Architecture:** 保留 V3 的 `app / entities / features / pages / shared` 分层与后端真相源。新增协调器只保存最小操作状态，不复制 thread、timeline、loading 或 error 真相；只有第二种真实 wire decision 出现后，才从 approval 抽象通用 decision；只有 LSP xref 证明 Shell 状态确实跨消费者后，才建立 Shell store。

**Tech Stack:** React、Zustand、Vitest、Testing Library、Vite、ESLint、现有 Wails/RPC 适配层、仓库内 MCP-LSP、前端契约守卫与代码体积守卫。

**Verification Surface:** 聚焦 RED/GREEN 单测、前端全量 lint/test/build、当前真实支持的 UI MCP acceptance、LSP diagnostics、仓库级 guard/codemap/project-map。当前 UI MCP 不支持线程切换、timeline 滚动、审批或恢复动作，因此这些行为由明确的 Vitest integration/component tests 证明，不用不存在的 UI MCP 场景冒充验收。

---

## 0. 锁定事实与执行前提

### 0.1 文档编写基线

| 项目 | 路径 | 锁定版本 |
|---|---|---|
| V3 主仓 | `/Users/mima0000/Desktop/wj/super-agent-v3` | `5dbee863f2dcd118fd3cf94c6299f44818526f10` |
| Reasonix 参考仓 | `/Users/mima0000/Desktop/wj/deepseek-reasonix` | `1f5740a2129ea54bda7c86755ed58c88b84c16b4` |

以上 SHA 只表示本文修订时的锁定输入。实际执行者必须重新记录：

```bash
git -C /Users/mima0000/Desktop/wj/super-agent-v3 status --short --branch
git -C /Users/mima0000/Desktop/wj/super-agent-v3 rev-parse HEAD
git -C /Users/mima0000/Desktop/wj/super-agent-v3 rev-parse origin/main
git -C /Users/mima0000/Desktop/wj/deepseek-reasonix rev-parse HEAD
```

若 V3 执行基线不再包含本文列出的既有能力，先修订本文，不得边猜边改。Reasonix 只按上表 SHA 复现设计输入；除非用户明确要求，不在执行中追逐其新提交。

### 0.2 编写时工作树指纹

修订本文期间，另一条用户工作流提交了 `docs(plan): 编写 App adapter 分包执行文档`，因此当前 V3 `main` 为 `5dbee863f`，比 `origin/main@6b8f80ffe` ahead 1。该提交及其生成 project-map 属于用户现场，本计划不得改写或回退。当前仍存在以下与本计划无关的未跟踪文件：

```text
docs/plans/2026-07-10-generated-artifacts-launchd-refresh.md
docs/superpowers/specs/2026-07-10-capcontract-launchd-refresh-design.md
scripts/generated_artifacts_auto_refresh.sh
scripts/generated_artifacts_auto_refresh_guard_test.go
```

这些文件属于用户现场。不得删除、覆盖、暂存或带入本计划提交。本文自身必须先经审阅并提交，执行 worktree 才能从选定基线看到它。

### 0.3 LSP 是生产修改硬前置

本文编写和两次独立复核会话均未暴露 `mcp__lsp.*`，因此不声称取得 LSP PASS。执行必须先完成：

```bash
make codex-worktree-ready
go run ./cmd/codex-worktree-setup verify
codex mcp get lsp
```

随后新建 Codex 任务并确认真实出现：

```text
mcp__lsp.file
mcp__lsp.inspect
mcp__lsp.xref
mcp__lsp.grep
mcp__lsp.structure
mcp__lsp.patch_edit
mcp__lsp.completion
```

若未出现，停止生产代码修改并记录 blocker；不得用 `rg` 冒充 LSP structure/xref/diagnostics。

### 0.4 必须保留的 V3 既有能力

- bridge generation/sequence、防陈旧 patch、delta batching 和 completion 前 flush。
- 按 `cwd + thread` 隔离的 composer draft。
- `activeThreadId / pendingActiveThreadId / threadStateLoadingByThread / threadTimelineReadyByThread`。
- `thread/recover` RPC 和现有统一 `actionNotice` 错误/成功表面。
- `approval/request`、request-id 校验、UI 和 store 双层提交锁、超时与错误展示。
- `backendResponseValidators.js`、RPC contract audit、contract-store guard。
- 800 行文件、150 行函数、4 层嵌套、5 参数、20 导出、单目录 15 个生产文件的 code-size guard。
- `FocusTrapDialog`、现有可访问性语义。
- timeline 分批 materialization、React slow-render trace、RuntimeDiff TanStack Virtual。
- generated artifacts 单一生成入口。

### 0.5 明确非目标

- 不复制 Reasonix 的巨型 `App.tsx`、`useController.ts`、`Composer.tsx`、`SettingsPanel.tsx`、`bridge.ts` 或单体 CSS。
- 不新增第二套 stream batching、bridge revision、draft persistence 或 recovery RPC。
- 不新增持久化的 `sessionViewState` 去复制现有 loading/pending/error。
- 不虚构 `ask_request`、plan approval、stop decision、runtime rebuilt 或 recovery conflict。
- 不为只有一个真实 approval adapter 提前建设通用 capability framework。
- 不为没有多个真实消费者的 component-local overlay 建立全局 Zustand store。
- 不引入 GSAP；不照搬 Reasonix 静默吞掉 localStorage 错误的策略。
- 不在本计划内实现 Command Palette、快捷键自定义、长历史 benchmark 或新的 UI MCP 动作协议。
- 不降低守卫阈值、不扩大 baseline、不用 allowlist 隐藏新债务。
- 不直接复制 Reasonix 源码；只翻译行为与测试案例，并按 V3 边界重新实现。

---

## 1. Reasonix 机制完整处置矩阵

| 机制 | 处置 | 原因 |
|---|---|---|
| latest-intent-wins 导航 | **ABSORB NOW** | V3 现有请求缺少 active-intent commit gate |
| 显式 hydrate reason/phase | **ADAPT** | 只保留瞬时 intent reason；phase 从现有 keyed loading 派生 |
| 用户滚动意图 | **ABSORB NOW** | 当前存在两套 stickiness ref，资源变化与键盘/触摸语义不完整 |
| recovery 可见性 | **ADAPT** | 严格消费 `{thread,recovered,mode}`；只展示 requesting/accepted/failed |
| 生产 ErrorBoundary/crash reporting | **ABSORB NOW** | V3 只有 Profiler，生产根节点缺少 crash containment |
| 统一决策面 | **ADAPT** | 当前只做 approval-only；第二种 wire decision 出现后再泛化 |
| Shell 持久/瞬时状态分层 | **DISCOVERY** | 允许合法结论 `NO_CHANGE`，避免空壳 store |
| z-index token guard | **ABSORB NOW** | 所有生产 z-index 进入语义 token；不使用数字阈值猜语义 |
| rAF stream batching | **ALREADY HAVE** | 保留现有 delta batching/flush，不重复实现 |
| composer draft key | **ALREADY HAVE** | V3 已按 cwd/thread 隔离 |
| timeline 热/冷 materialization | **ALREADY HAVE** | V3 已只 materialize 最近窗口 |
| diff virtualization | **ALREADY HAVE** | V3 已用 TanStack Virtual |
| slow render 观测 | **ALREADY HAVE** | V3 根入口已有 React Profiler trace |
| command registry/shortcut/palette | **DEFER** | 产品价值高，但应独立计划，避免扩大本轮状态治理 |
| synthetic long-history benchmark | **DEFER** | 先设计非抖动指标；不以 wall-clock flaky test 阻塞本轮 |
| AnchoredPopover | **DEFER** | 至少出现两个共享消费者后再抽 shared primitive |
| Ask/Plan 通用 decision | **DEFER** | 等真实后端 contract |
| recovery 完成/冲突事件 | **DEFER** | 等 typed backend event/error contract |
| 巨型 Controller/Bridge/CSS、GSAP | **REJECT** | 与 V3 小文件、明确契约和现有依赖方向冲突 |

### 1.1 状态所有权

| 状态 | 唯一所有者 | 本计划规则 |
|---|---|---|
| thread/timeline/runtime 真相 | Go backend + client entity projection | 不复制 |
| thread loading/ready | 现有 keyed client-store maps | 新 selector 只能派生 |
| 导航 intent | thread-open coordinator 的单调序号 | 瞬时；不持久化业务对象 |
| recovery 请求中 | 最小 per-thread pending map | RPC 结束即清理；不声称恢复完成 |
| approval 请求 | 现有 wire message + approval feature adapter | 不复制 request/status |
| 滚动意图 | 单个 chat scroll hook/controller | 删除现有平行 refs |
| Shell layout | discovery 后确定 | 不能同时留在 App、client store 和新 store |
| crash breadcrumbs | bounded diagnostics ring | 仅 action code/route id/timestamp；禁止内容、路径、stack、token |

### 1.2 修订后的目标落点

```text
frontend-app/src/
  WorkbenchSidebarProjectTree.test.jsx
  entities/client/model/thread-open/
    threadOpenCoordinator.js
    threadOpenCoordinator.test.js
  features/approval/
    model/approvalDecision.js
    model/approvalDecision.test.js
    ui/ApprovalDecisionShelf.jsx
    ui/ApprovalDecisionShelf.test.jsx
  pages/chat/model/
    scrollIntentModel.js
    scrollIntentModel.test.js
  pages/chat/hooks/
    useScrollIntentManager.js
  app/
    AppErrorBoundary.jsx
    AppErrorBoundary.test.jsx
  shared/ui/
    OverlayPortal.jsx
    OverlayPortal.test.jsx
  shared/diagnostics/
    frontendBreadcrumbs.js
    frontendBreadcrumbs.test.js
    frontendCrashReport.js
    frontendCrashReport.test.js
  shared/styles/
    LayerTokens.css
frontend-app/scripts/
  frontend-z-index-token-guard.mjs
  frontend-z-index-token-guard.test.mjs
```

`app/shell/model/**` 不在无条件 Create 清单中。Task 6 discovery 只有证明迁移收益后才允许创建。

---

## 2. 执行拓扑与文件所有权

### 2.1 Worktree 幂等 preflight

本文审阅并提交后执行：

```bash
set -euo pipefail
REPO=/Users/mima0000/Desktop/wj/super-agent-v3
BASE_SHA="$(git -C "$REPO" rev-parse HEAD)"
BATCH="$(date +%Y%m%d-%H%M%S)"
BRANCH="codex/reasonix-frontend-$BATCH"
WT="/Users/mima0000/Desktop/wj/worktrees/super-agent-v3-reasonix-frontend-$BATCH"

git -C "$REPO" worktree list --porcelain
git -C "$REPO" show-ref --verify --quiet "refs/heads/$BRANCH" && {
  echo "branch already exists: $BRANCH" >&2
  exit 1
}
test ! -e "$WT" || {
  echo "worktree path already exists: $WT" >&2
  exit 1
}
git -C "$REPO" worktree add "$WT" -b "$BRANCH" "$BASE_SHA"
```

执行记录必须保存 `BASE_SHA`、`origin/main`、dirty fingerprint、worktree、branch、worker/agent id 和开始时间。

### 2.2 推荐串行；并行时只并行新文件

跨层接线很多，默认由一个集成 worktree 串行执行。控制器确需并行时，只允许下表新文件 lane 并行；所有现有文件由 Integrator 独占：

| Owner | 可写路径 |
|---|---|
| Lane A | `entities/client/model/thread-open/**` |
| Lane B | `pages/chat/model/scrollIntentModel*`、`features/approval/**` |
| Lane C | `app/AppErrorBoundary*`、新增 `shared/ui/OverlayPortal*`、新增 `shared/diagnostics/**`、`shared/styles/LayerTokens.css`、新增 z-index guard/test |
| Integrator | 所有既有文件、`WorkbenchSidebarProjectTree.test.jsx`、CSS 迁移、package scripts、生成产物、Task 6 discovery |

Lane commit 只能包含新文件。Integrator 在 lane commit 通过聚焦测试后完成接线，避免 `App.jsx`、`Conversation.jsx`、store、validators、`main.jsx` 和 CSS 冲突。

### 2.3 每任务交付格式

每个任务记录：

1. `STATE`: `TODO / RED / GREEN / NO_CHANGE / BLOCKED`。
2. `DAG`: 前置任务与实际 commit SHA。
3. `RESULT_GATES`: 命令、exit code、日志路径。
4. `EVIDENCE`: RED、GREEN、LSP diagnostics、变更文件。
5. `NON_TARGET_DIFF`: 执行前后 dirty fingerprint。
6. `TRUTH_SOURCE_CHECK`: 旧状态是否删除或转为派生。

---

## 3. Task 0 — 冻结基线与恢复工具面

**Files:** 只更新执行记录，不改生产代码。

- [ ] 记录 V3/Reasonix SHA、status、diff stat、根目录生产文件数。
- [ ] 确认本文已进入执行基线。
- [ ] 完成 worktree preflight。
- [ ] 运行 worktree readiness 并在新任务确认 LSP 七工具。
- [ ] 用 LSP structure/xref 复核：
  - `WorkbenchSidebarProjectTree.jsx`
  - `helpers/threadSelectionActions.js`
  - `runtimeSlice.js`
  - `helpers/a1/clientStoreSnapshotModel.js`
  - `helpers/a1/clientStoreBridgeRuntime.js`
  - `threadLifecycleRuntime.js`
  - `ChatApprovalMessage.jsx`
  - `TimelineMessage.jsx`
  - `Conversation.jsx`
  - `pages/chat/composer/ComposerDock.jsx`
  - `useChatWorkbenchLayout.js`
  - `shared/ui/FocusTrapDialog.jsx`
  - `App.jsx`
  - `main.jsx`
- [ ] 保存 baseline：

```bash
cd frontend-app
npm ci
npm run lint
npm test
npm run build
npm run mcp:ui-test:acceptance
```

**Stop condition:** baseline 失败或 LSP 不可用时，不进入生产任务。

---

## 4. Task 1 — 单一 thread-open intent coordinator

**Intent:** 快速选择 A → B → C 时只允许 C 改变 active view，同时继续允许旧只读请求安全完成。

**Create:**

- `frontend-app/src/entities/client/model/thread-open/threadOpenCoordinator.js`
- `frontend-app/src/entities/client/model/thread-open/threadOpenCoordinator.test.js`

**Integrator creates/modifies:**

- `frontend-app/src/WorkbenchSidebarProjectTree.jsx`
- `frontend-app/src/WorkbenchSidebarProjectTree.test.jsx`（新增真实 sidebar integration test）
- `frontend-app/src/entities/client/model/helpers/threadSelectionActions.js`
- `frontend-app/src/entities/client/model/runtimeSlice.js`
- `frontend-app/src/entities/client/model/useClientStore.test.js`
- 只有 LSP xref 证明必要时：`helpers/a1/clientStoreSnapshotModel.js`

### Contract

- 使用单调 `selectionIntentId`，不建立持久化 `sessionViewState`。intent 必须在用户点击入口创建，而不是在异步 continuation 最后调用 `setActiveThread` 时重新签发。
- `WorkbenchSidebarProjectTree.selectThread` 在点击时调用 `beginOpeningThread(thread)` 创建并取得 opaque intent context，再把同一 context 贯穿 `selectProjectThreadAction → setActiveProjectPath → setActiveThread`；project switch 完成后的 continuation 只能消费原 context，不能创建新 intent。
- `beginOpeningThread` 不再只返回无法区分 A→B→A 的 boolean；成功时返回包含单调 id/target 的 opaque intent context，失败时返回 null。project switch 失败时使用 intent-aware cancel/rollback，禁止通过 `setActiveThread('')` 创建一个更新 intent。
- `setActiveThread(threadId, { selectionIntent })` 在收到 context 时只消费/校验该 context；只有不存在异步前置步骤的直接同步入口才允许不传 context 并现场创建新 intent。
- V3 的 resolve/sync 是只读获取，允许并行；本计划不复制 Reasonix 的串行 latest-pending scheduler。
- 每个 active-view commit 与全局 error notice/warning 发布前检查 intent；`runtimeSlice.syncThreadState` 的错误副作用也必须接受同一 intent predicate，不能只在 `threadSelectionActions.js` 外层判断。
- stale 请求可以合并其目标 thread 的 keyed cache，并且必须按既有 per-thread sync generation 清理自身 `threadStateLoadingByThread[target]`；它不能修改 `activeThreadId`、当前 draft、当前 thread 的 loading/error 或全局 notice/warning。
- `syncThreadState` 的普通调用方不传 selection predicate 时保持现有行为；thread-open 调用方传入的 predicate 只控制用户可见失败副作用，不得阻止目标线程 cache、message page 或 keyed loading 的正常收敛。
- `pendingActiveThreadId` 和 `threadStateLoadingByThread` 继续是加载真相；hydrate reason 只作为请求上下文/诊断字段。
- `newThread`、`continueWithSharedFile` 和其他离开当前 thread-open 流程的用户动作必须推进/失效当前 intent；迟到的 project switch、resolve 或 sync 不得重新激活旧选择。
- 不实现“相同 target 所有调用共享同一 promise”，避免吞掉用户显式刷新。

### RED

- [ ] A/B/C 并行，B 最后返回，active view 仍是 C。
- [ ] 跨项目 A→B→A：第一次 A 的 project-switch continuation 最晚返回时，不能覆盖第三次 A 的 intent；不能只按 target id 判新旧。
- [ ] 点击跨项目线程后立即 `newThread`，迟到 continuation 不得离开新草稿。
- [ ] 点击跨项目线程后立即 `continueWithSharedFile`，迟到 continuation 不得覆盖 fork/shared-file draft。
- [ ] stale resolve 返回不同 canonical id，不能把 active id 改回旧线程。
- [ ] stale sync 失败，不能覆盖 C 的 notice/error。
- [ ] stale 请求仍可更新其目标线程 keyed cache。
- [ ] stale 请求完成或失败后，其目标线程 keyed loading 会清理；不能因为 intent 已过期而永久保持 loading。
- [ ] draft、trusted cache、bridge generation/sequence 回归继续通过。

```bash
cd frontend-app
npx --no-install vitest run \
  src/WorkbenchSidebarProjectTree.test.jsx \
  src/entities/client/model/thread-open/threadOpenCoordinator.test.js \
  src/entities/client/model/useClientStore.test.js \
  --no-file-parallelism --maxWorkers=1
```

### GREEN

- [ ] Coordinator 不 import Zustand/backend API，只生成和判断 intent。
- [ ] 用户点击入口只签发一个 intent；sidebar/project-switch/store continuation 全程携带同一 opaque token。
- [ ] `runtimeSlice.syncThreadState` 仅在 predicate 仍有效时发布全局失败 notice/warning；keyed cache/loading 继续由现有 per-thread generation 管理。
- [ ] 没有新增 `phase/error/target` 平行 store 字段。
- [ ] LSP diagnostics 为零，目录生产文件数守卫通过。

**Acceptance:** 快速选择遵循最后用户意图；旧异步结果不反杀当前页面。

---

## 5. Task 2 — 单一滚动意图控制器

**Intent:** streaming、图片/代码块加载或 resize 不抢走用户阅读位置，并删除当前两套 stickiness 真相。

**Create:**

- `frontend-app/src/pages/chat/model/scrollIntentModel.js`
- `frontend-app/src/pages/chat/model/scrollIntentModel.test.js`
- `frontend-app/src/pages/chat/hooks/useScrollIntentManager.js`

**Integrator modifies:**

- `frontend-app/src/pages/chat/hooks/timelineScroll.js`
- `frontend-app/src/pages/chat/hooks/timelineScroll.test.js`
- `frontend-app/src/pages/chat/thread/Conversation.jsx`
- `frontend-app/src/pages/chat/ChatPage.core.test.jsx`

### Truth-source migration

- 删除或完全替代 `useConversationScrollController` 中的 `shouldStickToBottomRef`。
- 删除或完全替代 `ConversationTimeline` 中的 `userScrolledRef`。
- 唯一意图状态由 `useScrollIntentManager` 持有；`timelineScroll.js` 只保留无状态 DOM primitives。

### RED

- [ ] 初次打开、发送新消息、显式回到底部进入 sticky。
- [ ] wheel up、touch 向上、PageUp/Home、滚离阈值退出 sticky。
- [ ] End、向下回到底部、回到底部按钮重新 sticky。
- [ ] editable target 中方向键不改变 timeline 意图。
- [ ] ctrl+wheel/横向 wheel 不被误判成纵向阅读意图。
- [ ] streaming、load、MutationObserver、ResizeObserver 只在 sticky 时滚底。
- [ ] 切 thread 重置意图；卸载取消 rAF/observer/listener。

```bash
cd frontend-app
npx --no-install vitest run \
  src/pages/chat/model/scrollIntentModel.test.js \
  src/pages/chat/hooks/timelineScroll.test.js \
  src/pages/chat/ChatPage.core.test.jsx \
  --no-file-parallelism --maxWorkers=1
```

### GREEN

- [ ] Pure transitions 在 `pages/chat/model`，React/DOM coordination 在 hooks。
- [ ] 不引入 GSAP，不新增 stream batching。
- [ ] 两个旧 refs 已删除或只作为新 hook 内部同一状态的实现细节。
- [ ] LSP diagnostics 为零。

**Acceptance:** 用户上翻时不被新 token 拉走；回到底部后恢复贴底。

---

## 6. Task 3 — 严格恢复响应与 accepted 状态

**Intent:** 正确表达“恢复请求已接受”，不虚构完成或冲突终态。

**Modify:**

- `frontend-app/src/shared/api/backendResponseValidators.js`
- `frontend-app/src/shared/api/backendResponseValidators.test.js`
- `frontend-app/src/shared/api/backend/backendApiFactoryThread.js`（仅在 validator 接入方式需要显式 normalize 时）
- `frontend-app/src/shared/api/backendApi.test.js`
- `frontend-app/src/entities/client/model/threadLifecycleRuntime.js`
- `frontend-app/src/entities/client/model/threadLifecycleRuntime.test.js`
- `frontend-app/src/entities/client/model/helpers/a1/clientStoreThreadActions.js`
- `frontend-app/src/entities/client/model/helpers/a1/clientStoreUtils.js`
- `frontend-app/src/entities/client/model/useClientStore.test.js`
- `frontend-app/src/app/appShellModel.js`
- `frontend-app/src/app/appShellModel.test.js`
- `frontend-app/src/pages/chat/model/chatHeaderModel.js`
- `frontend-app/src/pages/chat/model/chatHeaderModel.test.js`
- `frontend-app/src/pages/chat/components/ChatPageHeader.jsx`
- `frontend-app/src/pages/chat/components/ChatPageHeader.test.jsx`

### Contract

真实响应固定为：

```text
{
  thread: { id: non-empty, status: "recovering" },
  recovered: boolean,
  mode: non-empty
}
```

前端 UI 状态只允许：

```text
idle → requesting → accepted | failed
```

- store 只保存 `requesting` 所需的 per-thread pending；`accepted/failed` 是映射到 `actionNotice` 的一次性结果，不新增长期 enum/projection。
- 只有合法 envelope 且 `recovered === true` 才进入 `accepted`；`recovered === false` 是 typed failure。
- `accepted` 表示 RPC 已接受，不表示线程已恢复完成。
- 后续真实 thread patch/snapshot 继续决定业务状态。
- 不增加 `recovered` UI 终态和 `conflict`。
- 仅新增最小 `threadRecoveryPendingByThread` 以禁用重复请求；RPC settle 后清理。
- 结果提示复用 `actionNotice`，不建立长期 `recoveryProjection`。
- `backendResponseValidators.js` 是 recovery wire schema、字段集合、unknown-key 和 shape 错误文案的唯一 owner；所有 `recoverThread` 成功响应在进入 runtime 前已经由统一 `callBackend` validator 验证。
- `threadLifecycleRuntime` 抽出一个私有 transport runner 返回 `{ ok, threadId, result }`；现有 `activeThreadRPC` 继续把它投影为 boolean，保持 interrupt/force-complete/compact 调用方契约不变。
- recover 使用窄的 `recoverActiveThreadRPC` 保留并传递已验证 result；runtime 只做 `recovered === true/false` 业务投影、pending 清理和 active-thread notice gate，禁止复制 thread/id/status/mode/unknown-key 等 wire schema 校验或第二套错误文案。
- 不得让通用 `activeThreadRPC` 出现 `boolean | object` 混合返回，也不得为单个 recover 建 capability registry。

### RED

- [ ] API validator 对缺 thread/id/status、错误 recovered 类型、空 mode、body 或 thread 内额外未知字段全部 fail closed，且 runtime handler 未被调用。
- [ ] `recovered:false` 进入 failed/notice，绝不能显示 accepted。
- [ ] 同一线程 requesting 中重复点击只调用一次 RPC。
- [ ] 切换 active thread 后旧响应只完成旧线程 pending 清理，不污染新 header/notice。
- [ ] accepted 文案是“恢复请求已接受/正在恢复”，不得写“已恢复完成”。
- [ ] 错误继续进入现有 warning/action notice 表面。

```bash
cd frontend-app
npx --no-install vitest run \
  src/shared/api/backendResponseValidators.test.js \
  src/shared/api/backendApi.test.js \
  src/entities/client/model/threadLifecycleRuntime.test.js \
  src/entities/client/model/useClientStore.test.js \
  src/app/appShellModel.test.js \
  src/pages/chat/model/chatHeaderModel.test.js \
  src/pages/chat/components/ChatPageHeader.test.jsx \
  --no-file-parallelism --maxWorkers=1
```

### GREEN

- [ ] `THREAD_RECOVER` validator 强制注册，不是“若需要”。
- [ ] 私有 runner 保留 API 层已验证的 recover envelope，窄 `recoverActiveThreadRPC` 只做业务投影；`threadLifecycleRuntime.test.js` 直接证明 runtime 没有复制 wire validator，通用 `activeThreadRPC` 仍只返回 boolean，且 interrupt/force-complete/compact 的既有 notice 和 warning 语义不变。
- [ ] Header 只消费 selector/action，不直接调用 backend。
- [ ] 没有新增后端 event 或错误码。

**Acceptance:** 用户能区分 requesting、accepted、failed；不能从当前 contract 推断恢复完成。

---

## 7. Task 4 — 生产 ErrorBoundary 与隐私安全 crash diagnostics

**Intent:** React render crash 不再导致空白窗口；global error/rejection 有安全、有限、可观察的诊断。

**Create:**

- `frontend-app/src/app/AppErrorBoundary.jsx`
- `frontend-app/src/app/AppErrorBoundary.test.jsx`
- `frontend-app/src/shared/diagnostics/frontendBreadcrumbs.js`
- `frontend-app/src/shared/diagnostics/frontendBreadcrumbs.test.js`
- `frontend-app/src/shared/diagnostics/frontendCrashReport.js`
- `frontend-app/src/shared/diagnostics/frontendCrashReport.test.js`

**Integrator modifies:**

- `frontend-app/src/main.jsx`
- 必要时 `frontend-app/src/App.test.jsx`

### Privacy contract

- breadcrumbs 只允许 stable action code、route id、phase、timestamp。
- 禁止 prompt、message、tool result、memory、skill、token、authorization、cwd、绝对路径、raw stack、component stack。
- 所有字段经过现有 `safeLogFields`；禁止绕过。
- crash report 通过注入的 reporter/现有 frontend trace surface 发送，不在 diagnostics 模块直接 import client store。
- global `error` / `unhandledrejection` handler 可重复安装但只能生效一次，并忽略已 `defaultPrevented` 事件。

### RED

- [ ] React child render throw 时显示可访问 fallback，不是空白。
- [ ] fallback 提供“重试界面”和“重新加载”明确动作。
- [ ] reporter 自身失败不会递归 crash；失败通过最小 console error 表面出现。
- [ ] breadcrumb ring 有容量上限和稳定顺序。
- [ ] secret/path/stack/message content fixture 不出现在序列化结果。
- [ ] global listener 安装幂等、卸载干净。
- [ ] UI test harness 的 dev-only unhandled collector 不被生产 handler 重复计数。

```bash
cd frontend-app
npx --no-install vitest run \
  src/app/AppErrorBoundary.test.jsx \
  src/shared/diagnostics/frontendBreadcrumbs.test.js \
  src/shared/diagnostics/frontendCrashReport.test.js \
  --no-file-parallelism --maxWorkers=1
```

### GREEN

- [ ] `main.jsx` 顺序为 StrictMode → ErrorBoundary → Profiler → App。
- [ ] ErrorBoundary 只负责 containment/fallback；diagnostics 负责 normalize/redact/report。
- [ ] 不复制 Reasonix 的完整 performance monitor；V3 现有 Profiler 保留。
- [ ] LSP diagnostics 为零。

**Acceptance:** 生产 render crash 有安全 fallback 和脱敏证据，不泄漏用户代码或会话内容。

---

## 8. Task 5 — Approval-only 决策面

**Intent:** 吸收 Reasonix 的“选择、确认、pending、焦点恢复”机制，但不提前建设 Ask/Plan 通用框架。

**Create:**

- `frontend-app/src/features/approval/model/approvalDecision.js`
- `frontend-app/src/features/approval/model/approvalDecision.test.js`
- `frontend-app/src/features/approval/ui/ApprovalDecisionShelf.jsx`
- `frontend-app/src/features/approval/ui/ApprovalDecisionShelf.test.jsx`

**Integrator modifies:**

- `frontend-app/src/pages/chat/thread/ChatApprovalMessage.jsx`
- `frontend-app/src/pages/chat/thread/ChatApprovalMessage.test.jsx`
- `frontend-app/src/pages/chat/thread/chatApprovalModel.js`
- `frontend-app/src/pages/chat/thread/TimelineMessage.jsx`
- `frontend-app/src/pages/chat/thread/TimelineMessage.test.jsx`
- `frontend-app/src/pages/chat/thread/chatTurnGroupingModel.js`
- `frontend-app/src/pages/chat/thread/chatTurnGroupingModel.test.js`
- `frontend-app/src/pages/chat/thread/Conversation.jsx`
- `frontend-app/src/pages/chat/composer/ComposerDock.jsx`
- `frontend-app/src/pages/chat/composer/ComposerDock.test.jsx`
- `frontend-app/src/entities/client/model/useClientStore.test.js`（既有 store exactly-once 回归）
- `frontend-app/src/pages/chat/ChatPage.core.test.jsx`

### Contract

- 领域名是 `approval`，不出现 `kind/capability/ask/plan`。
- 现有 wire message 是唯一 request/status 真相。
- `approvalDecision.js` 只做 strict adapter/selector；迁移后删除旧 `chatApprovalModel`，或把它降为唯一 re-export，不得保留两套判断。
- Composer 保持 mounted，draft 不丢失；pending 时仅禁用/inert，不卸载。
- `Conversation` 持有唯一 `composerInputRef`，通过 `ComposerDock` 的窄 `inputRef` prop 绑定真实 textarea；禁止用 `document.querySelector`、DOM id 或延时猜测恢复焦点。
- approval pending 时 `ComposerDock` 根节点设置 `inert`/`aria-disabled`，并通过既有 `canUseProjectActions` 链禁用发送、附件、模型和项目动作；不得只做视觉置灰。
- 决策结束后，只有 active thread 未变化、没有后续 approval 且原 composer 仍 mounted 时，才通过 `composerInputRef` 恢复 focus。
- 当前没有全局 overlay store，因此不为“关闭 overlay”新增 store。

### RED

- [ ] 无效 request id、未知 choice、terminal 重复提交 fail closed。
- [ ] 选择与确认分离；pending 禁止重复提交；失败保留选择并允许重试。
- [ ] store 与 UI 双层 exactly-once 回归继续通过。
- [ ] composer 节点 identity/draft 在 approval 前后保持。
- [ ] approval 期间 composer inert/disabled 且可访问性语义明确。
- [ ] thread 切换或新 approval 到达时不恢复错误焦点。
- [ ] 焦点恢复只走显式 `composerInputRef`；测试锁定不存在全局 DOM 查询或第二个 focus owner。

```bash
cd frontend-app
npx --no-install vitest run \
  src/features/approval/model/approvalDecision.test.js \
  src/features/approval/ui/ApprovalDecisionShelf.test.jsx \
  src/pages/chat/thread/ChatApprovalMessage.test.jsx \
  src/pages/chat/thread/TimelineMessage.test.jsx \
  src/pages/chat/thread/chatTurnGroupingModel.test.js \
  src/pages/chat/composer/ComposerDock.test.jsx \
  src/entities/client/model/useClientStore.test.js \
  src/pages/chat/ChatPage.core.test.jsx \
  --no-file-parallelism --maxWorkers=1
```

### GREEN

- [ ] Approval Shelf 不 import client store/backend API。
- [ ] 所有 approval consumer 指向同一 adapter。
- [ ] 不新增 wire method 或 capability registry。
- [ ] 第二种真实 decision 出现前，不改名为 generic decision。

**Acceptance:** 当前 approval 体验更稳定，但架构复杂度与真实能力数量一致。

---

## 9. Task 6 — Shell layout discovery，允许 NO_CHANGE

**Intent:** 判断 layout 状态是否值得从 client store/App/local hook 迁移；不是强制创建新 store。

**Discovery set:**

- `frontend-app/src/App.jsx`
- `frontend-app/src/app/appShellModel.js`
- `frontend-app/src/pages/chat/ChatPage.jsx`
- `frontend-app/src/pages/chat/hooks/useChatWorkbenchLayout.js`
- `frontend-app/src/pages/chat/model/chatWorkbenchLayoutModel.js`
- `frontend-app/src/entities/client/model/helpers/a1/clientStoreUtils.js`
- `frontend-app/src/entities/client/model/helpers/a1/clientStorePageActions.js`

### Decision gate

用 LSP xref 为 `rightPanelWidth`、`rightPanelOpen`、`threadRailWidth` 分别记录：

- 生产消费者数量。
- 是否跨 page/app boundary。
- 是否需要刷新后持久化。
- 当前唯一 writer。
- 迁移是否能删除 client business store 中的 UI-only 字段。

若没有字段同时满足“跨边界或多个消费者”与“迁移能删除旧真相”，Task 状态标记 `NO_CHANGE`，不创建任何文件。

### 只有 GO 时允许创建

```text
frontend-app/src/app/shell/model/useShellLayoutStore.js
frontend-app/src/app/shell/model/useShellLayoutStore.test.js
frontend-app/src/app/shell/model/shellLayoutSchema.js
frontend-app/src/app/shell/model/shellLayoutSchema.test.js
```

### GO 后规则

- 必须锁定确切迁移字段及旧字段删除文件。
- component-local overlay 继续局部维护；不创建 `overlayState.js`。
- persistence 使用带 `get/set/remove` 的注入 storage port 和严格 scalar schema，不直接 `JSON.parse`。
- storage 不可用或 write 失败：抛出并交 ErrorBoundary/diagnostics。
- storage 中不存在 key 是显式 first-run 状态，可使用 schema 声明的初始 layout 值；这不是读取失败后的 fallback。
- storage 中已存在但 persisted scalar 无效：报告 typed validation error 并阻断 Shell store 初始化；不得自动删除、改写或退回初始值。
- 若产品需要恢复入口，只允许由错误表面提供显式“重置布局偏好”动作；用户确认后调用 `storage.remove`，下一次初始化再按 first-run 处理。GO 决策记录必须同时锁定该 UI action 的 owner/修改文件；若本轮不实现 reset，非法值继续保持 BLOCKED。不得在读取路径静默自愈。
- 迁移 commit 必须同时删除 App/client-store/hook 中的平行副本。

### GO 后 RED

- [ ] key 缺失按 first-run 初始化，并持久化后可严格 roundtrip。
- [ ] key 已存在但类型、范围或格式非法时初始化失败，原值保持不变，diagnostics 可见。
- [ ] storage read/write/remove 失败均阻断；没有空 catch、自动删除或默认值兜底。
- [ ] 只有显式 reset 动作成功后，下一次初始化才允许进入 first-run。

**Acceptance:** `NO_CHANGE` 是合法成功；GO 时只有一个 layout truth source。

---

## 10. Task 7 — 全量 z-index token 与守卫

**Intent:** 所有生产 z-index 使用语义 token，并明确局部 stacking context 与全局 overlay 的区别。

**Create:**

- `frontend-app/src/shared/styles/LayerTokens.css`
- `frontend-app/src/shared/ui/OverlayPortal.jsx`
- `frontend-app/src/shared/ui/OverlayPortal.test.jsx`
- `frontend-app/scripts/frontend-z-index-token-guard.mjs`
- `frontend-app/scripts/frontend-z-index-token-guard.test.mjs`

**Integrator modifies:**

- `frontend-app/index.html`
- `frontend-app/src/App.jsx`
- `frontend-app/src/App.test.jsx`
- `frontend-app/src/main.jsx`
- `frontend-app/src/styles.test.js`
- `frontend-app/src/shared/ui/FocusTrapDialog.jsx`
- `frontend-app/src/shared/ui/FocusTrapDialog.test.jsx`
- `frontend-app/src/shared/styles/PagePrimitivesPolish.css`
- `frontend-app/package.json`
- `frontend-app/src/AppChrome.css`
- `frontend-app/src/AppShell.css`
- `frontend-app/src/AppShellSidebarThreadActions.css`
- `frontend-app/src/AppShellWorkbench.css`
- `frontend-app/src/pages/chat/ChatMessages.css`
- `frontend-app/src/pages/chat/ChatPage.css`
- `frontend-app/src/pages/chat/ChatPageWorkbench.css`
- `frontend-app/src/pages/chat/composer/ComposerDock.css`
- `frontend-app/src/pages/chat/runtime/RuntimePanel.css`
- `frontend-app/src/pages/memory/MemoryPage.css`
- `frontend-app/src/pages/skills/SkillsPage.css`

### Token contract

```text
--z-local-behind
--z-local-raised
--z-local-handle
--z-local-sticky
--z-shell-control
--z-overlay-popover
--z-overlay-dialog
--z-overlay-lightbox
--z-overlay-critical
```

实际数值只在 `LayerTokens.css` 定义。局部 token 表示只在其 stacking context 内比较；overlay token 只用于 `frontend-app/index.html` 中唯一的 `#overlay-root`。`OverlayPortal` 必须通过 `createPortal` 写入该 host；host 缺失时 fail-fast，禁止回退到 `document.body` 或原地渲染。`FocusTrapDialog` 继续拥有焦点/ARIA 语义，但 DOM 挂载统一委托给 `OverlayPortal`。Token 数量可按真实 selector 增加，但必须保持语义命名和单一真相。

### Theme projection contract

- theme 唯一 owner 仍是 `App.jsx` 的现有 `useColorTheme`；`#overlay-root[data-theme]` 只是由同一值驱动的只读 DOM projection，不增加 persistence、setter 或第二个 theme store。
- `App.jsx` 在 theme 变化时同步更新唯一 `#overlay-root` 的 `data-theme`；host 缺失或重复时 fail-fast，卸载时只清理自己写入的 projection。
- portal 后所有依赖 `.sa-window[data-theme]` 祖先的 dialog/lightbox selector 必须迁移为显式 overlay-host selector，或改为从 `:root`/host 继承的 theme token；禁止复制两套可独立漂移的颜色值。
- `PagePrimitivesPolish.css` 中 `.sa-window[data-theme="light"] .skills-editor-modal...` 等现有 selector 必须纳入迁移与测试，不能让 light theme dialog 在 portal 后退回 dark/default 样式。

### RED

- [ ] 任意生产 CSS 裸 `z-index` 数字，包括负数、0 和低值，守卫失败。
- [ ] `z-index: var(--z-*)` 通过；未知 token 失败。
- [ ] 重复定义、未使用 token、非严格全局 overlay 顺序失败。
- [ ] fixture 覆盖局部与全局 token。
- [ ] style tests 证明 `LayerTokens.css` 在其他生产 CSS 之前由 `main.jsx` 导入。
- [ ] `OverlayPortal.test.jsx` 证明 dialog DOM 挂到 `#overlay-root` 而不是调用方祖先，host 缺失立即报错，卸载后 portal 内容清理。
- [ ] `index.html`/style tests 证明 `#overlay-root` 与 `#root` 同级，且 `html/body/#overlay-root` 没有 transform、opacity、filter、perspective、contain 或 isolation 创建的意外 stacking context。
- [ ] `FocusTrapDialog.test.jsx` 在 portal 后继续证明初始焦点、Tab trap、Escape、overlay click 和焦点恢复。
- [ ] `App.test.jsx` 证明 light/dark 切换同步更新 shell 与 `#overlay-root`，projection 不接受独立写入，卸载/重挂载不遗留 stale theme。
- [ ] style tests 枚举所有带 `.sa-window[data-theme]` 的 overlay/dialog selector；迁移漏项时失败。

### GREEN

- [ ] 迁移上述全部 11 个当前含 z-index 的 CSS 文件。
- [ ] guard 扫描所有生产 CSS，不使用 `>=8` 阈值和 baseline/allowlist。
- [ ] guard 接入 `guard:critical-skip`，fixture test 由 `npm test` 覆盖。
- [ ] 全局 overlay token 只允许在统一 host 的 overlay selector 使用；普通 page/local popover 只能使用 local token。
- [ ] `FocusTrapDialog` 不自行创建第二 host，不保留原地渲染分支。

```bash
cd frontend-app
node scripts/frontend-z-index-token-guard.test.mjs
npm run guard:critical-skip
npx --no-install vitest run \
  src/styles.test.js \
  src/App.test.jsx \
  src/shared/ui/OverlayPortal.test.jsx \
  src/shared/ui/FocusTrapDialog.test.jsx \
  --no-file-parallelism --maxWorkers=1
```

**Acceptance:** 所有 z-index 都有语义 token；全局 overlay 通过唯一 portal host 脱离 page stacking context，并保持与唯一 App theme owner 同步；守卫不再把数值大小误当成架构边界。

---

## 11. Task 8 — 集成、真实验收与生成产物

### 11.1 合并顺序

```text
Task 1 navigation
→ Task 2 scroll
→ Task 3 recovery
→ Task 4 crash containment
→ Task 5 approval
→ Task 6 shell discovery
→ Task 7 layer tokens
```

- [ ] lane 新文件 commit 先通过聚焦测试。
- [ ] Integrator 再完成既有文件接线。
- [ ] 每个任务独立 commit，可单独回滚。
- [ ] 所有改动源码逐文件 LSP diagnostics 为零。
- [ ] LSP xref 证明旧状态/adapter/ref 已删除或变为唯一派生。
- [ ] `git diff --check` 为零。

### 11.2 前端全量门禁

```bash
cd frontend-app
npm run lint
npm test
npm run build
npm run mcp:ui-test:acceptance
npm run mcp:ui-test:scenario
```

随后回到仓库根目录验证正式 embed manifest；`npm run build` 的同步成功不能替代逐文件 SHA-256 一致性检查：

```bash
cd "$(git rev-parse --show-toplevel)"
make frontend-embed-verify
```

执行记录必须保存 `frontend-embed-verify` exit code 与 smoke hash；失败时不得把 Vite build PASS 写成可发布。

`mcp:ui-test:acceptance` 只证明当前 MCP contract、composer isolated submit 和 diagnostics；默认 scenario 只证明现有 `frontend_navigation_probe`。它们不是 thread race、scroll、approval 或 recovery 的证据。

### 11.3 行为证据矩阵

| 行为 | 强制证据 |
|---|---|
| A/B/C 快速线程选择 | `threadOpenCoordinator.test.js` + `useClientStore.test.js` |
| streaming 上翻不抢滚动 | `scrollIntentModel.test.js` + ChatPage integration test |
| recover accepted/failed | response validator + store/header integration tests |
| React render crash | ErrorBoundary component test + diagnostics privacy fixtures |
| approval exactly-once/focus | approval feature test + ChatPage integration test |
| z-index 层级 | guard fixtures + styles tests + build |

若未来必须从真实浏览器操纵上述动作，另立 UI MCP contract 扩展计划，明确修改：

```text
src/devtools/uiTestContract.js
src/devtools/uiTestContract.test.js
src/devtools/uiTestHarness.js
src/devtools/uiTestHarness.test.js
scripts/ui-test-mcp-server.mjs
scripts/ui-test-mcp-server.test.mjs
scripts/ui-test-mcp-scenario.mjs
```

本计划不偷偷扩展 dev/test attack surface。

### 11.4 生成产物与仓库门禁

```bash
ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"
scripts/refresh_generated_artifacts.sh codemap --check
scripts/refresh_generated_artifacts.sh project-map --check
scripts/refresh_generated_artifacts.sh capcontract --check
```

check 失败时仅由 Integrator 通过同一脚本执行对应 refresh，并审阅生成 diff。随后：

```bash
scripts/refresh_generated_artifacts.sh codemap --check
scripts/refresh_generated_artifacts.sh project-map --check
scripts/refresh_generated_artifacts.sh capcontract --check
make codemap-check
make project-map-check
make capcontract-check
make guard
git diff --check
```

refresh 后三项 `--check` 必须全部重新 exit 0；不得用 `make guard` 代替 capability-contract 复检。

### 11.5 完成定义

只有同时满足以下条件才可标记 `DONE`：

- 每个生产任务都有真实 RED 与 GREEN 证据。
- Task 6 明确记录 `NO_CHANGE` 或 GO 证据，不能悬空。
- 聚焦测试、前端全量门禁、当前 UI MCP acceptance、仓库门禁全部 exit 0。
- `make frontend-embed-verify` 逐文件 SHA-256 manifest 一致且 exit 0。
- 所有改动源码 LSP diagnostics 为零。
- 没有新增平行 session view、recovery projection、generic decision 或 overlay store。
- code-size、contract-store、RPC contract、critical-skip 守卫未放宽。
- Reasonix 仅作为 clean-room 设计参考。
- 非目标 dirty 文件保持原样。
- 受影响生成产物由生成器刷新并通过 check。

---

## 12. 后续独立计划，不进入本轮实现

### 12.1 Command registry + shortcut + Command Palette

建议未来落点：

```text
frontend-app/src/app/commands/appCommandRegistry.js
frontend-app/src/shared/keyboard/shortcutModel.js
frontend-app/src/features/command-palette/
```

必须先盘点现有 page-level keydown、命令冲突、editable target、平台修饰键和权限边界。

### 12.2 隐私安全长历史 benchmark

V3 已有 timeline materialization，不重做 Reasonix hot/warm/cold UI。后续只建立 synthetic 200/1000/5000-turn 数据与稳定指标；在证明 wall-clock 阈值跨 CI 环境稳定前，不把易抖动耗时直接设为硬 gate。

### 12.3 第二种 decision 与 recovery 完成事件

- 第二种真实 wire decision 到来后，再从 `features/approval` 抽 `features/decision`。
- 后端提供 typed recovered/failed/conflict event/error contract 后，再设计长期 recovery projection。

---

## 13. 风险与停止条件

| 风险 | 触发条件 | 动作 |
|---|---|---|
| 平行真相源 | 新状态重复 loading/error/request/status | 停止，改为 intent 或派生 selector |
| 虚构恢复终态 | UI 从 accepted 推断 recovered/conflict | 删除推断，等待后端 contract |
| 预抽象 | 只有一个 adapter/consumer 却增加 generic store/framework | 收窄到 approval/local state 或 NO_CHANGE |
| 大文件回潮 | 触发 code-size guard | 按真实领域拆子目录，不调阈值 |
| 静默容错 | storage/parser/async 空 catch | typed error + 可观察表面 |
| UI MCP 假验收 | 当前 allowlist 不支持目标动作 | 使用明确 Vitest 证据或另立 contract 计划 |
| CSS 假层级 | 只比较数字，不检查 stacking context | token + host contract + style test |
| 工具证据缺失 | LSP 不可用 | BLOCKED，不修改生产代码 |
| 现场污染 | 非目标 fingerprint 变化 | 停止提交，定位所有权，不 destructive reset |

---

## 14. 最终预期

完成后，V3 保留自身更强的契约、守卫、API validator、bridge 时序和小文件边界，同时获得：

- 快速线程操作遵循最后用户意图。
- streaming 尊重用户阅读位置。
- recovery 只表达后端真实承诺。
- React crash 有安全 fallback 和脱敏证据。
- approval 决策体验完整，但不过早泛化。
- Shell 状态只有证明收益才迁移。
- 所有视觉层级由语义 token 与守卫维护。

这才是本轮 Reasonix 前端架构吸收的口径：吸收已赚到收益的状态机、诊断和守卫，保留 V3 的单一真相与 AI 可维护边界，并把高价值但独立的能力明确推迟，而不是一次性堆进一个大重构。
