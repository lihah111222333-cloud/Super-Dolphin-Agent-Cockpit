# Reasonix 前端架构机制吸收执行记录

> 本文件记录 `docs/plans/2026-07-11-reasonix-frontend-architecture-absorption.md` 的真实执行证据。不得用后续重跑覆盖失败证据；不得把计划目标写成已落地现状。

## 执行身份与冻结基线

| 项目 | 真实值 |
|---|---|
| agent id | `/root/serial_implementer` |
| 首次捕获开始时间 | `2026-07-11T19:53:18+0900 KST` |
| Task 0 证据完成时间 | `2026-07-11T19:58:09+0900 KST` |
| worktree | `/Users/mima0000/Desktop/wj/super-agent-v3/.worktrees/reasonix-frontend-absorption-20260711` |
| branch | `codex/reasonix-frontend-absorption-20260711` |
| BASE_SHA / Task 0 起始 HEAD | `0dd59a0599a98761f641db0f0abdd6504a9aaca0` |
| `origin/main` | `6b8f80ffe1b5d0d789b114dfc38f73a207322bb4` |
| 主工作区 HEAD | `d65361e722902721f1e50bc26936b574422f180a` |
| 主工作区状态 | `main...origin/main [ahead 3]`；仅 `M docs/plans/2026-07-11-reasonix-frontend-architecture-absorption.md` |
| 主工作区 dirty fingerprint | `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d` |
| 计划快照 commit | `0dd59a0599a98761f641db0f0abdd6504a9aaca0 docs(plan): 修订 Reasonix 前端吸收执行边界` |
| 计划文件 SHA-256 | `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061` |
| Reasonix 锁定 SHA | `1f5740a2129ea54bda7c86755ed58c88b84c16b4` |
| Reasonix 当前 SHA / 状态 | `1f5740a2129ea54bda7c86755ed58c88b84c16b4`；`main-v2...origin/main-v2`；clean |

`git ls-tree` 与按计划文件查询的 `git log -1` 均确认计划已进入 `0dd59a0` 执行基线。主工作区 dirty fingerprint 在 Task 0 前后保持一致。

### 根目录生产文件数

口径：目标目录的直接子文件，扩展名为 `js/jsx/ts/tsx`，排除 `.test.*` 与 `.spec.*`。

| 目录 | 生产文件数 |
|---|---:|
| `frontend-app/src` | 10 |
| `frontend-app/src/entities/client/model` | 15 |
| `frontend-app/src/pages/chat/thread` | 14 |
| `frontend-app/src/shared/ui` | 2 |
| `frontend-app/src/app` | 1 |

`npm test` 内的 production code-size guard 另行确认 `files=339, frozen=0` 且通过。

## 任务总览

| Task | STATE | DAG / commit |
|---|---|---|
| Task 0 冻结基线与恢复工具面 | `GREEN` | 前置计划快照 `0dd59a0599a98761f641db0f0abdd6504a9aaca0`；本记录所在 Task 0 提交由该提交的 `HEAD` 解析 |
| Task 1 thread-open intent | `GREEN` | Phase A RED 与 Phase B GREEN 均基于 `31c94421e82835560847723b29eb04bf70796543`；实现、验证与原子提交已完成，Task 1 提交由包含本记录的 Git `HEAD` 解析 |
| Task 2 scroll intent | `GREEN` | Phase A RED 与 Phase B GREEN 均基于 `af2c245409afa6f269b32250d256d7fa7162cbe8`；实现、验证与原子提交完成后由包含本记录的 Git `HEAD` 解析 |
| Task 3 recovery accepted | `GREEN` | Phase A RED 保留；Phase B 已实现并完成前端全量验证 |
| Task 4 crash containment | `GREEN` | Phase A RED 完整保留；Phase B 最小实现、全量门禁与原子提交完成后由包含本记录的 Git `HEAD` 解析 |
| Task 5 approval-only | `PHASE_A` | clean baseline 已锁定；正在做 LSP discovery 与 test-only RED，禁止生产实现 |
| Task 6 shell discovery | `TODO` | 依赖 Task 5 |
| Task 7 layer tokens | `TODO` | 依赖 Task 6 |
| Task 8 integration | `TODO` | 依赖 Task 1-7 |

## Task 0 — 冻结基线与恢复工具面

### STATE

`GREEN`

首次 UI MCP acceptance 因 Playwright 浏览器运行时缺失而失败；该失败完整保留。主代理裁决允许使用锁定的本地 CLI 补齐运行时，安装成功后同一 acceptance 命令重跑通过。未修改生产代码，未进入 Task 1。

### DAG

```text
plan snapshot 0dd59a0599a98761f641db0f0abdd6504a9aaca0
  -> Task 0 baseline/LSP evidence
  -> Task 0 execution-record commit (self = Git HEAD containing this file)
  -X-> Task 1 (not dispatched in this agent turn)
```

### RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 | 日志载体 |
|---:|---|---:|---|---|
| 1 | `go run ./cmd/codex-worktree-setup ready` | 0 | 当前 worktree 独立 `bin/mcp-lsp` 与 `.codex/config.toml`；列出七工具 | 当前 Codex task transcript |
| 2 | `go run ./cmd/codex-worktree-setup verify` | 0 | gopls、tsserver、typescript-language-server 与真实 Go/JS diagnostics 验证通过 | 当前 Codex task transcript |
| 3 | `codex mcp get lsp` | 0 | enabled；cwd、command 均指向当前 worktree | 当前 Codex task transcript |
| 4 | `codex mcp list` | 0 | `lsp` enabled，未引用其他 checkout | 当前 Codex task transcript |
| 5 | `cd frontend-app && npm ci` | 0 | added 448 packages；audited 449；0 vulnerabilities | 当前 Codex task transcript |
| 6 | `npm run lint` | 0 | `eslint .` 无输出错误 | 当前 Codex task transcript |
| 7 | `npm test` | 0 | 115 test files / 1394 tests passed；critical-skip、silent-async、contract/store、code-size、TS contract、RPC audit 全部通过 | 当前 Codex task transcript |
| 8 | `npm run build` | 0 | Vite transformed 5538 modules；同步 embed 后未产生 tracked diff | 当前 Codex task transcript |
| 9 | `npm run mcp:ui-test:acceptance`（首次） | 1 | 缺少 `/Users/mima0000/Library/Caches/ms-playwright/chromium_headless_shell-1223/chrome-headless-shell-mac-arm64/chrome-headless-shell` | 当前 Codex task transcript |
| 10 | `npx --no-install playwright install chromium` | 0 | 使用已锁定 CLI；下载 chromium v1223、ffmpeg v1011、headless-shell v1223；未更新 package/lockfile | 当前 Codex task transcript |
| 11 | `npm run mcp:ui-test:acceptance`（重跑） | 0 | `UI test MCP acceptance passed` | 当前 Codex task transcript |

首次 acceptance 失败不是产品测试回归，但属于真实基线事件，后续 GREEN 不覆盖该证据。仓库内未创建临时日志；Playwright 运行时落在用户 cache，而非 tracked worktree。

### EVIDENCE

#### LSP 七工具真实可见性

`ready`、`verify`、当前任务工具注册表同时确认以下工具：

```text
mcp__lsp.file
mcp__lsp.inspect
mcp__lsp.xref
mcp__lsp.grep
mcp__lsp.structure
mcp__lsp.patch_edit
mcp__lsp.completion
```

- 实际调用并成功：`file(read_file/diagnostics)`、`inspect(hover)`、`xref(references)`、`grep(text_search)`、`structure(document_symbol)`。
- `completion` 首次以 `App.jsx:512:60` 调用，得到明确 `position_out_of_range`；按返回提示收窄为 `App.jsx:512:50` 后成功返回 8/2702 candidates。
- `patch_edit` 在当前任务中真实可见，但 Task 0 禁止生产修改且执行记录按控制器要求使用 `apply_patch`，因此未制造无意义编辑来伪造调用证据。

#### 定位、理解、影响面与精读

| 链路 | 可复查 LSP 位置与判定 |
|---|---|
| thread-open 入口 | `WorkbenchSidebarProjectTree.jsx:329:9` 的 `selectThread` xref 到 `:355:29`；精读确认点击时 `beginOpeningThread` 仅返回 boolean `openingStarted`，随后进入异步 `selectProjectThreadAction`。 |
| thread-open 状态 | `threadSelectionActions.js:18:10` 的 `beginOpeningThread` hover 为 `(runtime, thread, deps) => boolean`，xref 到导出 action；精读确认它立即写入 `activeThreadId/pendingActiveThreadId`，现状尚无 opaque monotonic intent。 |
| sync 影响面 | `runtimeSlice.js:204:9` 的 `syncThreadState` hover 为 async boolean；精读确认 keyed generation 与 finally loading cleanup 已存在，但 error 分支直接发布全局 notice/warning，Task 1 必须保持 keyed 收敛并增加 intent gate。 |
| snapshot/bridge | `clientStoreSnapshotModel.js` structure 定位 `buildSnapshotState@392`、live-busy merge helpers；`clientStoreBridgeRuntime.js` structure 定位 `attachBridgeIdentityRuntime@62`、`attachBridgeEventRuntime@159`。二者纳入 diagnostics，Task 1 不应无证据改写。 |
| recovery | `threadLifecycleRuntime.js:50:17` 的 `attachActiveThreadRpcRuntime` xref 到 runtime core 与现有直接测试；精读确认通用 `activeThreadRPC` 当前返回 boolean，`clientStoreThreadActions.js` 的 recover 仍走该通用入口。 |
| approval | `ChatApprovalMessage.jsx:11:10` xref 覆盖自身测试与 `TimelineMessage.jsx:114`；`ComposerDock.jsx:51:10` xref 覆盖自身测试与 `Conversation.jsx:360`，证明现有 approval/render/composer 接线边界。 |
| layout | `useChatWorkbenchLayout.js` document structure 定位 width/drag/toggle helpers；纳入 diagnostics。Task 6 仍需针对具体字段另做 xref 决策，不在 Task 0 预判 GO。 |
| overlay/theme/root | `FocusTrapDialog.jsx:61:17` xref 至 chat/files/memory 等多个生产消费者；`App.jsx:102:10` 的 `useColorTheme` 仅 xref 至 `useAppShellState@512`；`main.jsx:68:10` 的 slow-render trace xref 至 Profiler callback，精读确认现状顺序为 `StrictMode -> Profiler -> App`。 |

#### 指定文件 diagnostics

以下 14 个计划指定文件以单次 `mcp__lsp.file(action=diagnostics, file_paths=[...])` 检查，结果为 `No diagnostics found`、`total=0`：

```text
frontend-app/src/WorkbenchSidebarProjectTree.jsx
frontend-app/src/entities/client/model/helpers/threadSelectionActions.js
frontend-app/src/entities/client/model/runtimeSlice.js
frontend-app/src/entities/client/model/helpers/a1/clientStoreSnapshotModel.js
frontend-app/src/entities/client/model/helpers/a1/clientStoreBridgeRuntime.js
frontend-app/src/entities/client/model/threadLifecycleRuntime.js
frontend-app/src/pages/chat/thread/ChatApprovalMessage.jsx
frontend-app/src/pages/chat/thread/TimelineMessage.jsx
frontend-app/src/pages/chat/thread/Conversation.jsx
frontend-app/src/pages/chat/composer/ComposerDock.jsx
frontend-app/src/pages/chat/hooks/useChatWorkbenchLayout.js
frontend-app/src/shared/ui/FocusTrapDialog.jsx
frontend-app/src/App.jsx
frontend-app/src/main.jsx
```

### NON_TARGET_DIFF

| 检查点 | worktree dirty fingerprint | 判定 |
|---|---|---|
| Task 0 开始 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | clean |
| `npm ci/lint/test/build/acceptance` 后、写本记录前 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | clean；build 未留下 tracked embed diff |
| 主工作区前后 | `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d` | 未变化；未触碰用户主工作区修改 |
| Reasonix 前后 | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` | clean；只读 |

提交前必须再次验证：排除本执行记录后的 worktree diff 仍为空，staged 只包含本文件。

### TRUTH_SOURCE_CHECK

- Task 0 未修改任何生产代码、测试、配置、package manifest、lockfile 或生成产物。
- 未新增 `sessionViewState`、recovery projection、generic decision/capability、overlay store 或第二 theme owner。
- 计划仍是实施约束；源码和测试仍是当前行为真相。
- Reasonix 只用于锁定 SHA 对照，本任务未读取或复制其实现源码。
- `activeThreadId/pendingActiveThreadId/threadStateLoadingByThread`、通用 boolean `activeThreadRPC`、现有 approval wire message、`useColorTheme` 和 `FocusTrapDialog` 的当前所有权未改变。

### CONCERNS / STOP CONDITIONS

- Playwright 浏览器运行时不是 `npm ci` 产物；全新执行机若无 cache，acceptance 会先失败。必须保留失败并显式安装锁定版本，禁止把缺失浏览器误写为产品 GREEN。
- `frontend-app/src/entities/client/model` 已有 15 个直接生产文件，正好位于目录上限；Task 1 新 coordinator 必须按计划落到 `thread-open/` 子目录，不能继续堆在 model 根目录。
- Task 0 agent turn 已按控制器要求停下；主代理复核 `31c94421e` 后，才在后续 turn 串行派发 Task 1 Phase A。

## Task 1 — 单一 thread-open intent coordinator

### STATE

`GREEN`（Phase A RED 已保留；Phase B 最小实现与验证完成）

Phase A 没有创建 `threadOpenCoordinator.js`，没有修改任何生产代码，也没有 stage/commit。RED 由缺失的 intent coordinator、token 贯穿、intent-aware cancel/invalidation 和 stale failure side-effect gate 触发，不是测试拼写、按钮查询、async fixture 或环境错误。主代理审核 Phase A 后才授权 Phase B；下方完整保留 Phase A 的失败证据，并追加 Phase B 的 GREEN 与中途结构门禁失败。

### DAG

```text
Task 0 commit 31c94421e82835560847723b29eb04bf70796543
  -> Task 1 Phase A LSP discovery
  -> Task 1 Phase A RED tests (uncommitted)
  -> 主代理审核 RED 语义与 dirty 边界
  -> Task 1 Phase B pure coordinator
  -> store/sidebar/runtime intent integration
  -> focused + repository-native gates
  -X-> Task 2
```

### RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 1 | LSP `grep/structure/inspect/xref/file(read_file)` | success | 锁定 sidebar → project switch → selection → sync → snapshot 链；见下方 EVIDENCE |
| 2 | `vitest run threadOpenCoordinator.test.js` | 1 | 唯一失败是计划中的 `./threadOpenCoordinator.js` 尚不存在；0 tests collected |
| 3 | Sidebar 测试首次运行 | 1 | 初始 fixture 未等待异步 cache refresh，找不到 Thread B；该结果不作为 RED，修正 test harness 后重跑 |
| 4 | Sidebar 测试修正后单独运行 | 1 | 2/2 因缺 `selectionIntent` 贯穿与缺 `cancelOpeningThread(intent)` 失败；无按钮/act/fixture 错误 |
| 5 | `vitest run useClientStore.test.js` | 1 | 204 tests：5 个新 intent 行为失败，199 passed |
| 6 | 定向 ESLint 三个测试文件 | 0 | 无 lint error/warning |
| 7 | LSP diagnostics 三个测试文件 | 0 | `No diagnostics found` |
| 8 | 计划指定三文件聚焦命令（最终） | 1 | 3 files failed；1 module-missing suite + 7 intended behavior failures；199 passed |

最终聚焦命令：

```bash
cd frontend-app
npx --no-install vitest run \
  src/WorkbenchSidebarProjectTree.test.jsx \
  src/entities/client/model/thread-open/threadOpenCoordinator.test.js \
  src/entities/client/model/useClientStore.test.js \
  --no-file-parallelism --maxWorkers=1
```

最终输出：`Test Files 3 failed (3)`；`Tests 7 failed | 199 passed (206)`；exit 1。

### EVIDENCE

#### LSP 定位、理解、影响面与精读

| 链路 | 证据与 Phase B 边界 |
|---|---|
| sidebar 点击 | `selectProjectThreadAction@WorkbenchSidebarProjectTree.jsx:133` xref 仅到 `:334`；`selectThread@329` xref 到 `:355`。当前只传 boolean `openingStarted`，project switch options 只有 `preserveActiveThreadId`。 |
| project switch | `createSetActiveProjectPathAction@projectSliceActions.js:56` xref 到 action set `:159`；精读确认它会 clear/refresh chat surface，并在失败时发布 project notice/warning。Task 1 不复制 project state。 |
| selection actions | `beginOpeningThread@threadSelectionActions.js:18` hover 当前返回 boolean；`setActiveThread@104` 不接收 options；`newThread@148` 与 `continueWithSharedFile@159` 均未失效 intent。 |
| sync gate | `syncThreadState@runtimeSlice.js:204` hover 为 async boolean；per-thread generation、keyed loading finally 和 stale generation cleanup 已存在，但 catch `:237-242` 无条件发布全局 notice/warning。 |
| snapshot projection | `applySnapshot@clientStoreRuntimeCore.js:305` xref 只到 runtime attach；`buildSnapshotState@clientStoreSnapshotModel.js:391` xref 只到 `applySnapshot`，且 `snapshotActiveThreadId` 已支持 `preserveActiveThreadId/preferredActiveThreadId`。 |

一次 `structure(workspace_symbol)` 对 `WorkbenchSidebarProjectTree.jsx` 返回 TypeScript `No Project`；按规则先 `file(open_file)`，再收窄为该文件的 `structure(document_symbol)`，重试成功（223 symbols，showing 50），随后 exact-pos hover/xref 成功。该异常不是 blocker。

#### `clientStoreSnapshotModel.js` 必要性判定

结论：**Phase B 不需要修改**。

- intent 是操作协调状态，应在纯 coordinator、thread selection action 和 runtime sync side-effect boundary 判断。
- snapshot model 已能在调用方传入 `preserveActiveThreadId` 时保留 active identity，并负责 keyed cache/timeline/status 真相投影。
- 把 selection predicate 注入 snapshot model 会把瞬时 UI intent 泄漏到通用 truth projection，增加第二判断面。
- 若 Phase B 产生与该结论冲突的新 LSP xref，必须先停下交由主代理裁决，不能顺手修改。

#### RED 行为矩阵

| 高风险场景 | 测试证据 | 当前结果 |
|---|---|---|
| coordinator 单调 token、重复 A 不按 target 判新旧 | `threadOpenCoordinator.test.js` | module missing RED；实现模块后将执行 2 个 pure unit tests |
| 跨项目 A → B → A | `WorkbenchSidebarProjectTree.test.jsx` | 缺 `selectionIntent` 传给 project switch；RED |
| project switch 失败 rollback | 同上第二测试 | 未调用 `cancelOpeningThread(intent)`，仍走旧空 thread selection；RED |
| A/B/C out-of-order | `useClientStore.test.js` | guard 通过，证明现有 active-changed 保护必须保留 |
| `newThread` 失效旧 intent | `useClientStore.test.js` | 迟到 `setActiveThread` 把 active 改回 A；RED |
| shared-file fork 失效旧 intent | `useClientStore.test.js` | 迟到 selection 返回 true；RED |
| stale sync failure 不污染全局 | `useClientStore.test.js` | keyed loading 已先断言清理，但 `actionNotice` 被 stale error 覆盖；RED |
| stale success 继续 keyed cache/loading 收敛 | `useClientStore.test.js` | guard 通过，A cache/timeline 更新且 active 保持 C |
| stale resolve canonical id | `useClientStore.test.js` | active 被改为 `canonical-a` 而非保持 C；RED |
| draft/trusted cache/bridge generation/sequence 回归 | 同一完整 `useClientStore.test.js` 文件 | 除 5 个新行为外其余 199 tests passed |

### NON_TARGET_DIFF

| 检查点 | fingerprint / status | 判定 |
|---|---|---|
| Phase A BASE | `31c94421e82835560847723b29eb04bf70796543`；clean | 与 Task 0 审核结果一致 |
| 写执行记录前 test-only fingerprint | `9e2ef9624784e494943d251b820af7a0131a6a2ce2349c1bd0634e2d9b214f38` | 仅 3 个允许的测试路径 |
| 主工作区 dirty fingerprint | `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d` | Task 0/Phase A 前后未变化 |

Phase A owned dirty paths：

```text
M  docs/plans/2026-07-11-reasonix-frontend-architecture-absorption-execution.md
M  frontend-app/src/entities/client/model/useClientStore.test.js
?? frontend-app/src/WorkbenchSidebarProjectTree.test.jsx
?? frontend-app/src/entities/client/model/thread-open/threadOpenCoordinator.test.js
```

### TRUTH_SOURCE_CHECK

- 没有生产文件变更；`threadOpenCoordinator.js` 仍不存在，这是预期 RED 原因。
- 没有新增 `sessionViewState` 或持久化 `phase/error/target` 字段。
- RED API 只描述瞬时 opaque intent：monotonic id、target、current/cancel/invalidate；不 import Zustand/backend API。
- `pendingActiveThreadId` 与 `threadStateLoadingByThread` 仍是既有 loading truth；测试要求 stale 请求继续 keyed 收敛。
- `clientStoreSnapshotModel.js` 未修改，也未把 intent predicate 下沉到 snapshot truth projection。
- 未 stage、未 commit、未 push；Task 2 未开始。

### CONCERNS / PHASE B CHECKPOINT

- Phase B 必须先创建纯 coordinator 让 module-missing 变为可执行 unit RED/GREEN，再接 store/sidebar，不能为了快速消除 import error 跳过 coordinator tests。
- `beginOpeningThread`、`setActiveThread`、`cancelOpeningThread`、`newThread`、`continueWithSharedFile` 必须共享同一 coordinator 实例，但不能把实例或其 current token持久化进 Zustand state。
- stale sync predicate 只能 gate global notice/warning 与 active-view commit；当前已通过的 keyed cache/loading tests 是不可回退护栏。
- 本 agent 在主代理检查 dirty tests 和 RED 语义前停在 Phase A，不自行进入生产实现。

### PHASE B RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 1 | coordinator pure unit test | 0 | 1 file / 2 tests passed；token 单调递增，重复 target 仍产生新 intent，cancel/invalidate 只按 identity 生效 |
| 2 | 三文件 focused Vitest（首轮 GREEN） | 0 | 3 files / 208 tests passed |
| 3 | production code-size guard（首次） | 1 | `activateExistingThread` 7 params、`runtimeSlice.js` nesting 5；属于真实结构门禁失败，未用 ignore/阈值绕过 |
| 4 | production code-size guard（结构修复后） | 0 | 参数收敛进 context，catch predicate 抽成 helper；`files=234, frozen=0` |
| 5 | strengthened same-thread in-flight sync test（首次） | 1 | `useClientStore.test.js`：204 tests 中 1 failed / 203 passed；迟到 A/B sync 已不改 active，但错误返回 `true`，预期 stale 返回 `false` |
| 6 | strengthened test 修复后 focused Vitest | 0 | current-intent 返回语义修复；3 files / 208 tests passed |
| 7 | resolve failure cleanup tests（首次 RED） | 1 | 1 file：2 failed / 204 skipped；stale 与 current resolve reject 后 A keyed loading 均仍为 `true` |
| 8 | resolve cleanup 初版定向测试 | 0 | 3 passed / 204 skipped；随后主代理发现它仍用 `pendingActiveThreadId` 猜 same-target ownership，不能作为最终 GREEN |
| 9 | strengthened same-target ownership RED | 1 | 2 files / 2 failed；A2 已进入 deferred sync、pending 已清空且 loading=true 后，A1 reject 错误清掉 A2 loading；coordinator 还没有 target release 判定 |
| 10 | coordinator ownership 修复后定向测试 | 0 | 2 files；4 passed / 205 skipped；由 opaque current identity + target ownership 判定 release，不再用 pending 猜 owner |
| 11 | 与第 9-10 步重叠启动的 `npm test` | 1 | 该进程在 coordinator 修复前已载入旧模块，最终为 1 failed / 1407 passed，失败是缺 `canReleaseTarget`；作为真实旧快照证据保留，不代表修复后 gate |
| 12 | `npm run lint`（最终） | 0 | `eslint .` 无 error/warning；在 ownership 修复后重跑 |
| 13 | `npm test`（最终） | 0 | 117 test files / 1408 tests passed；code-size `files=342, frozen=0`，critical-skip、silent-async、contract/store、TS contract、RPC audit 全部通过 |
| 14 | `npm run build`（最终） | 0 | Vite transformed 5539 modules；build 347ms；embed sync 未留下额外 tracked diff |

最终 focused 命令与 Phase A 相同：

```bash
cd frontend-app
npx --no-install vitest run \
  src/WorkbenchSidebarProjectTree.test.jsx \
  src/entities/client/model/thread-open/threadOpenCoordinator.test.js \
  src/entities/client/model/useClientStore.test.js \
  --no-file-parallelism --maxWorkers=1
```

最终输出：`Test Files 3 passed (3)`；`Tests 211 passed (211)`；exit 0。

### PHASE B EVIDENCE

#### 最小实现与所有权

- `thread-open/threadOpenCoordinator.js` 是纯闭包：只拥有单调 `selectionIntentId`、`targetThreadId` 与 current identity；不 import Zustand、backend API 或 Promise，不持久化 `phase/error/target`。
- `createThreadSelectionActions` 只创建一个 coordinator，`beginOpeningThread`、`openThreadById/openResolvedThread`、`setActiveThread`、`cancelOpeningThread`、`newThread`、`continueWithSharedFile` 共享该闭包；coordinator 不进入 store snapshot。
- Sidebar 在 click 起点取得一次 opaque token，同一 token 贯穿 project switch 与 thread selection；project switch 失败时按 identity cancel，旧 intent 无权清除新 intent。
- `syncThreadState` 保留既有 per-thread generation、keyed cache 与 finally loading cleanup，只用调用方 predicate 抑制 stale global notice/warning；stale success/failure 都返回非 current，不再把迟到结果报告为当前选择成功。
- `clientStoreSnapshotModel.js` 没有修改；snapshot truth projection 继续由原模型负责，没有新增第二个 session view state。

#### 行为收敛

| 场景 | Phase B 结果 |
|---|---|
| coordinator 单调 token、重复 A | 2 个 pure unit tests GREEN |
| 跨项目 A → B → A | 真实 Sidebar click 测试 GREEN；每次 click 使用独立 token |
| project switch 失败 rollback | 只 cancel 对应 token；不再用 `setActiveThread('')` 制造新选择 |
| A/B/C resolve 或 sync 乱序 | C 保持 active；迟到 A/B 返回 false |
| `newThread` / shared-file fork | invalidate 旧 intent；迟到 selection 无权恢复旧 active |
| stale failure | keyed loading 正常清理；不污染全局 notice/warning |
| stale success / canonical resolve | cache/timeline/status 仍按 thread key 收敛；active identity 不回退 |
| resolve reject/invalid | current 或已切到不同 target 时清理失败 target loading；stale 不发布 notice；A1→A2 同 target 即使 A2 已清 pending 并持有 deferred sync，仍依靠 coordinator 的 opaque identity/target ownership 保留 A2 loading |

#### LSP 链路复核

- `createThreadOpenCoordinator` hover/xref 确认 API 只暴露 `begin/cancel/invalidate/isCurrent/canReleaseTarget`，其中 release 判断封装 current identity + target ownership；生产引用只来自 `threadSelectionActions.js`。
- `createThreadSelectionActions` xref 只到 `useClientStore.js` 的 action assembly，证明 coordinator 是单 store 实例的 action closure，而不是跨 store singleton 或 snapshot state。
- `cancelOpeningThread`、`setActiveThread`、`syncThreadState` 与 Sidebar `selectThread` 均完成 hover/xref 精读；无 options 的既有调用方仍由 direct intent 兼容路径覆盖。
- Phase B 最终 diagnostics 覆盖 coordinator、selection/runtime/sidebar 生产文件及三个测试文件和本执行记录；结果为 `No diagnostics found`、total 0。

### PHASE B NON_TARGET_DIFF

- 生产修改严格限定为 `threadOpenCoordinator.js`、`threadSelectionActions.js`、`runtimeSlice.js`、`WorkbenchSidebarProjectTree.jsx`。
- 测试修改严格限定为 coordinator unit、Sidebar integration 与 `useClientStore.test.js`；执行记录是唯一文档修改。
- pre-commit 由规范生成器自动刷新 5 个 project-map 索引产物：`AI_PROJECT_DRIFT.md`、`AI_PROJECT_MANIFEST.json`、`AI_PROJECT_MAP.md`、`index/app-ui.tsv`、`index/docs-agent.tsv`；`codemap-check` 与 `project-map-check --strict-drift` 均通过。这些不是手工维护的第二真相源。
- 没有修改 `clientStoreSnapshotModel.js`、project slice、backend、Reasonix checkout、package manifest/lockfile 或门禁阈值。
- 主工作区 dirty fingerprint 在 Phase B 开始前仍为 `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`；提交前继续只读复核，禁止将其带入本 worktree。

### PHASE B TRUTH_SOURCE_CHECK / STOP

- 没有新增 `sessionViewState`、持久化 intent、`phase/error/target` 或第二 active/loading truth source。
- `pendingActiveThreadId` 与 `threadStateLoadingByThread` 仍是既有 store truth；intent 只决定迟到结果是否有权提交 active-view/global side effects。
- keyed cache/loading 收敛由原 runtime generation 机制继续负责；intent gate 不吞掉 cleanup，也不删除 stale thread cache。
- Task 1 仅在最终 diagnostics、focused gate、targeted lint、code-size、docs validator、diff check 与原子提交全部通过后结束；本 agent 随后停止，不进入 Task 2，也不 push。

## Task 2 — 单一 timeline scroll intent manager

### STATE

`GREEN`（Phase A RED 已完整保留；Phase B 最小实现与验证完成）

本阶段没有创建 `scrollIntentModel.js` 或 `useScrollIntentManager.js`，没有修改 `Conversation.jsx`、`timelineScroll.js` 或其他生产代码，也没有 stage/commit。RED 分别锁定 pure transition、Conversation 输入接线、旧双 truth owner 删除、可取消 rAF handle，以及 observer/listener 生命周期；失败原因是计划中的 model/hook 缺失和现有旧行为，不是 jsdom observer 缺失、错误 target/button、未执行 rAF callback 或源码路径错误。

### DAG

```text
Task 1 commit af2c245409afa6f269b32250d256d7fa7162cbe8
  -> Task 2 Phase A LSP discovery
  -> pure model module-missing RED
  -> Conversation interaction / legacy-owner RED
  -> hook lifecycle module-missing RED
  -> exact focused RED + diagnostics / dirty boundary
  -X-> Task 2 Phase B production implementation
  -X-> Task 3
```

### RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 1 | LSP `grep/structure/inspect/xref/file(read_file)` | success | 锁定两个旧 ref owner 的全部读写、DOM primitive xref、observer/listener/timer cleanup 与 ChatPage 测试接线 |
| 2 | pure model test 单独运行 | 1 | 唯一失败为 `./scrollIntentModel.js` 不存在；0 tests collected；5 个 pure behavior tests 已写入 |
| 3 | Core 测试首轮 | 1 | 暴露两个 test harness 问题：未执行 scheduled rAF 却断言 scrollTop、`import.meta.url` 不是 file URL；不计作产品 RED |
| 4 | timeline DOM primitive test | 1 | 3 tests 中 1 failed / 2 passed；`requestTimelineBottomScroll` 实际返回 `undefined`，预期返回 frame id `1` 供 hook cleanup |
| 5 | 四测试文件定向 ESLint（首次） | 1 | hook harness 整体访问 manager 被 `react-hooks/refs` 报 6 errors；改为直接解构理想 hook API 后消除，不计作产品 RED |
| 6 | Core harness 修正后单独运行 | 1 | 25 tests：5 expected failures / 20 passed；wheel up、touch upward、PageUp、Home 后仍请求 rAF，以及两个旧 refs 仍存在 |
| 7 | hook lifecycle test 单独运行（最终） | 1 | 唯一失败为 `./useScrollIntentManager.js` 不存在；0 tests collected；observer/rAF/load cleanup fixture 未执行、无环境错误 |
| 8 | 四测试文件定向 ESLint（最终） | 0 | 无 error/warning |
| 9 | 计划指定三文件 focused 命令（最终） | 1 | 3 files failed；1 module-missing suite + 6 intended behavior failures；22 passed |
| 10 | LSP diagnostics 四个测试文件 | 0 | `No diagnostics found`、total 0 |

最终 focused 命令：

```bash
cd frontend-app
npx --no-install vitest run \
  src/pages/chat/model/scrollIntentModel.test.js \
  src/pages/chat/hooks/timelineScroll.test.js \
  src/pages/chat/ChatPage.core.test.jsx \
  --no-file-parallelism --maxWorkers=1
```

最终输出：`Test Files 3 failed (3)`；`Tests 6 failed | 22 passed (28)`；exit 1。额外的 hook lifecycle test 按 Phase A 允许项单独运行，结果是 hook module-missing RED。

### EVIDENCE

#### LSP 定位、理解、影响面与精读

| 链路 | 可复查位置与 Phase B 判定 |
|---|---|
| controller owner | `useConversationScrollController@Conversation.jsx:87-185` hover/definition/xref 到 `Conversation@270`。`shouldStickToBottomRef@96` 初始 true；scroll 写 near-bottom；instant/smooth/send/thread-change 写 true；autoScrollKey、MutationObserver、capture-load 与 streaming callback 读取 gate。 |
| timeline owner | `ConversationTimeline@Conversation.jsx:416-542` hover/definition/xref 到 `Conversation@301`。`userScrolledRef@447` 初始 false；scroll 写 `!isTimelineNearBottom`；message effect读取；thread-change 写 false。 |
| controller cleanup | thread 初始化 50ms timer 在 effect cleanup `clearTimeout`；MutationObserver `disconnect@166-168`；capture-load listener 在 `178-181` 对称 remove。现有 rAF primitive 不返回 id，无法在 owner unmount 时 cancel；当前没有 ResizeObserver。 |
| timeline cleanup | `userScrolledRef` 没有独立 listener/observer；older-page request ref 的 async finally 只收敛 pagination ownership，与 scroll intent 无关。 |
| DOM primitives | `timelineScroll.js` 只有 threshold、`scrollTimelineElementToBottom`、`isTimelineNearBottom`、`requestTimelineBottomScroll`。三个 symbol hover/xref 只到 `Conversation.jsx` 与 `timelineScroll.test.js`，适合继续保持无状态。 |
| streaming call | `onScrollIfSticky` grep 覆盖 `Conversation` 传递链、`TimelineMessage.jsx:109-112` streaming effect、`TurnProcessGroup` 与相应测试；Phase B 应把 gate 回收到单一 manager，不复制第三份 intent。 |

#### 两个旧 truth owner 的完整读写清单

`shouldStickToBottomRef` 共 10 处：

```text
96 create=true
101 scroll position write
104 instant bottom write=true
108 smooth bottom write=true
112 sticky read
122 message send write=true
131 thread change write=true
154 autoScrollKey read
161 MutationObserver read
174 capture load read
```

`userScrolledRef` 共 4 处：

```text
447 create=false
482 scroll position write
493 message auto-scroll read
498 thread change write=false
```

结论：两个 ref 同时判断同一个 timeline 的 stickiness，但在不同 effect 中读写，是真实双 truth owner。Phase B 必须删除这 14 个旧 owner 读写，由 `useScrollIntentManager` 闭包唯一持有 intent；不能把其中一个 ref 改名后保留为第二判断面。

#### RED 行为矩阵

| 高风险场景 | 测试载体 | Phase A 结果 |
|---|---|---|
| 初次打开、send、thread change | `scrollIntentModel.test.js` | module-missing RED；理想 state 初始/重置为 sticky |
| wheel up、touch upward、PageUp、Home | pure model + `ChatPage.core.test.jsx` | Core 4/4 仍 schedule rAF，证明现有 Conversation 没有输入 intent 接线 |
| 离开/返回 near-bottom threshold | pure model + Core guard | pure model 待实现；Core 的既有 scroll-position 路径通过，作为不可回退 guard |
| End、bottom button | pure model + Core guard | pure model待实现；Core guard 当前通过，Phase B 必须接到同一 owner |
| editable ArrowUp | pure model + Core guard | target filter contract 已写；Core guard通过 |
| ctrl+wheel / horizontal wheel | pure model + Core guard | filter contract 已写；Core guard通过，不得被新 wheel handler 破坏 |
| streaming / load / mutation / resize | pure model + hook test | `shouldFollowTimeline` 四 source contract 与单 manager observer callbacks 已写；model/hook 均 module-missing |
| unmount cleanup | hook test | 要求取消 pending rAF、disconnect Mutation/Resize observers、移除 capture-load listener；hook module-missing RED |
| legacy owner deletion | Core source contract | `shouldStickToBottomRef`、`userScrolledRef` 仍存在且 `useScrollIntentManager` 不存在；expected RED |
| cancelable primitive | `timelineScroll.test.js` | 当前 rAF helper 返回 `undefined`；expected RED |

### NON_TARGET_DIFF

| 检查点 | status / fingerprint | 判定 |
|---|---|---|
| Phase A BASE | `af2c245409afa6f269b32250d256d7fa7162cbe8`；clean | 主代理独立复验后的 Task 1 commit |
| 主工作区 fingerprint | `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d` | Task 2 discovery/test-only 操作未触碰主工作区 |

Phase A 允许的 owned dirty paths：

```text
M  docs/plans/2026-07-11-reasonix-frontend-architecture-absorption-execution.md
M  frontend-app/src/pages/chat/ChatPage.core.test.jsx
M  frontend-app/src/pages/chat/hooks/timelineScroll.test.js
?? frontend-app/src/pages/chat/hooks/useScrollIntentManager.test.jsx
?? frontend-app/src/pages/chat/model/scrollIntentModel.test.js
```

### TRUTH_SOURCE_CHECK

- 没有生产代码变更；`scrollIntentModel.js` 与 `useScrollIntentManager.js` 仍不存在，这是两个 module-missing RED 的预期原因。
- `Conversation.jsx` 的 `shouldStickToBottomRef` 和 `userScrolledRef` 尚未删除；测试明确要求 Phase B 完全替代两者。
- 没有引入 GSAP、stream batching、第二 store、持久化 scroll state 或新 UI framework。
- pure model 只定义 `sticky/reading` transition contract；observer、listener、rAF 与 DOM coordination 只属于未来 hook；`timelineScroll.js` 仍保持无状态 DOM primitives。
- 没有 stage、commit、push；Task 3 未开始。

### CONCERNS / PHASE B CHECKPOINT

- Phase B 必须先创建 pure model，使 module-missing 变成可执行 transition RED/GREEN，再实现 hook；禁止把 transition 重新散落回 `Conversation.jsx` event handlers。
- 新 hook 应直接拥有 wheel/touch/key/scroll input、autoScrollKey、MutationObserver、ResizeObserver、capture-load listener、pending rAF 与 cleanup；Conversation 只做 wiring 和 send wrapper。
- `requestTimelineBottomScroll` 返回 frame id 只是无状态 primitive contract；取消与最新 frame ownership 必须留在 hook，不能在 module scope 建全局状态。
- touch upward 在 Core fixture 中定义为触点 `clientY: 120 -> 70`；Phase B 若采用不同坐标语义必须先与主代理裁决，不能让 model 与 DOM adapter 方向相反。
- 本 agent 停在 Phase A；主代理复核 test API、expected failures 与 dirty 边界前，不进入生产实现、GREEN、Task 3 或提交。

### PHASE B RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 1 | pure model unit | 0 | 1 file / 5 tests passed；model 只导出 create/reduce/follow 三个纯 API，无 React/DOM import |
| 2 | hook lifecycle unit | 0 | 1 file / 2 tests passed；同一 reading intent gate streaming/load/mutation/resize，End 恢复 sticky；unmount cleanup 全通过 |
| 3 | 首轮 4-file focused | 0 | model + hook + DOM primitive + Core，4 files / 35 tests passed |
| 4 | targeted lint / production size（首次） | 1 | lint 仅有删除旧 effect 后未用 `useEffect`；Conversation 151 行超过 150；删除 import并收敛解构后修复，未改阈值 |
| 5 | targeted lint / production size（修复后） | 0 | ESLint 无输出；`files=236, frozen=0` |
| 6 | streaming consumer 复核 | 0 | 主代理指出显式 streaming callback 必须消费同一 hook；恢复 `scrollIfSticky` 传递链后 4 files / 35 tests passed |
| 7 | `npm test`（首次） | 1 | Vitest 前 contract/store guard 阻断：`touches[0] || changedTouches[0]` 命中 compat fallback ratchet；未进入测试主体 |
| 8 | `npm test`（最终） | 0 | 119 test files / 1422 tests passed；contract/store 0/0、code-size `files=346, frozen=0`、TS contracts、RPC audit 全部通过 |
| 9 | `npm run build` | 0 | Vite transformed 5541 modules；build 403ms；embed sync 未留下额外手工产物 |
| 10 | 计划 exact focused（最终） | 0 | 3 files / 33 tests passed |
| 11 | hook lifecycle test（最终） | 0 | 1 file / 2 tests passed |
| 12 | targeted ESLint / production size（最终） | 0 | changed source/test lint clean；production `files=236, frozen=0` |
| 13 | `npm run lint`（最终） | 0 | `eslint .` 无 error/warning |
| 14 | LSP final hover/xref/read/diagnostics | 0 | hook API xref 只到 Conversation 与 hook test；9 个 changed source/test/doc diagnostics 为 `No diagnostics found`、total 0 |
| 15 | truth owner grep | 0 | Conversation 两个旧 ref 均 0；`intentRef` 5 个生产命中全部位于唯一 hook |
| 16 | docs validator / diff check | 0 | skill adaptation checks passed；`git diff --check` 无输出 |

最终计划命令输出：`Test Files 3 passed (3)`；`Tests 33 passed (33)`；exit 0。加入 hook test 的 4-file focused 输出：`Test Files 4 passed (4)`；`Tests 35 passed (35)`；exit 0。

### PHASE B EVIDENCE

#### 唯一 intent owner 与依赖方向

- `scrollIntentModel.js` 的 state 仅为冻结的 `{ mode: 'sticky' | 'reading', threadId }`；`createScrollIntentState`、`reduceScrollIntent`、`shouldFollowTimeline` 不 import React、DOM 或 store，未知 event/source fail-fast。
- `useScrollIntentManager.js` 的 `intentRef` 是唯一 stickiness truth。`touchStartYRef`、`frameIdRef`、`lastAutoScrollKeyRef`、blocked/initial refs 只保存坐标、资源句柄和生命周期细节，不表达第二个 sticky/reading 判断。
- manager 独占 wheel/touch/key/scroll-position transition、thread reset、message-sent、explicit-bottom、autoScrollKey、MutationObserver、ResizeObserver、capture-load listener、timer/rAF ownership与 cleanup。
- `timelineScroll.js` 继续只有无状态 DOM primitives；`requestTimelineBottomScroll` 只返回浏览器 frame id，不保存 id，也不负责取消。

#### Conversation truth-source migration

- 整个旧 `useConversationScrollController` 已删除；生产 `Conversation.jsx` 中 `shouldStickToBottomRef` 与 `userScrolledRef` grep 均为 0。
- `ConversationTimeline` 原先基于 `userScrolledRef` 的 message effect 与 thread reset effect 已删除；消息增长不再绕过 hook调用 `onScrollToBottom`。
- timeline element 直接接入 manager 的 wheel、touch-start/move、key、scroll handlers；分页 `handleScroll` 仍保留 local-hidden/backend-page threshold 与 `preserveScrollAfterOlderPage`。
- send wrapper 调用 `markMessageSent`，底部按钮调用 `scrollToBottom(true)`；near-bottom scroll 与 End 也回到同一 reducer 的 sticky state。
- `TimelineMessage` / `TurnProcessGroup` 的 streaming callback 通过 `onScrollIfSticky={scrollIfSticky}` 消费同一 manager；hook test证明 reading 时不滚、End 后 sticky 时滚。autoScrollKey、Mutation、Resize、load 也只读取同一 intent。

#### 输入、observer 与 cleanup 证据

| 行为 | GREEN 证据 |
|---|---|
| wheel up / touch upward / PageUp / Home | Core 4 个 integration 从 schedule rAF RED 转 GREEN；pure reducer同步 GREEN |
| ctrl+wheel / horizontal wheel / editable ArrowUp | Core integration guard 与 pure tests 均 GREEN |
| End / near-bottom / explicit button / send / thread change | pure transition GREEN；Core End/scroll/button与send路径 GREEN |
| streaming / load / mutation / resize | `shouldFollowTimeline` pure matrix GREEN；hook observer/callback test对reading/sticky均 GREEN |
| rAF | primitive 返回 id；manager 在新请求前与 unmount cancel，hook test断言 id `73` 被取消 |
| observer/listener | Mutation/Resize 均 observe timeline并在 cleanup disconnect；capture-load unmount 后不再触发 request |
| initial/thread lifecycle | thread transition重置 sticky、touch起点、autoScrollKey与initial标记；50ms timer由effect cleanup取消 |

### PHASE B NON_TARGET_DIFF

- 生产修改严格限定为计划内 `scrollIntentModel.js`、`useScrollIntentManager.js`、`timelineScroll.js`、`Conversation.jsx`。
- 测试严格限定为 model test、hook test、`timelineScroll.test.js` 与 `ChatPage.core.test.jsx`；本执行记录是唯一手工文档修改。
- 没有修改 TimelineMessage/TurnProcess 的 streaming batching、store/backend、CSS、package manifest/lockfile 或 guard 阈值；没有引入 GSAP 或新依赖。
- 主工作区 fingerprint 在 Phase B 验证后仍为 `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`；其 dirty 计划文档未进入本 worktree。
- pre-commit 若由单一生成器刷新 project-map/codemap 索引，只在 hook checks 通过后纳入同一原子提交，并在提交后核对 exact names 与 clean status。

### PHASE B TRUTH_SOURCE_CHECK / STOP

- sticky/reading 只有 manager 内 `intentRef` 一个 owner；生产 grep 不得重新出现两个旧 ref，也不得新增 store/session scroll state。
- autoScrollKey、stream callback、MutationObserver、ResizeObserver 与 load listener是同一 owner 的多个事件源，不是多个 truth source；每个 source 在滚动前都经过 `shouldFollowTimeline`。
- older-page scroll preservation 是分页 DOM offset 责任，不表达 sticky/reading，不迁入 reducer。
- Task 2 只在最终 LSP diagnostics、docs validator、diff checks、原子提交与 post-commit clean 全部通过后结束；随后停止，不进入 Task 3，也不 push。

## Task 3 — 严格恢复响应与 accepted 状态

### STATE

`GREEN`（Phase A RED 已完整保留；Phase B 最小实现与全量验证完成）

本阶段没有修改 validator、API factory、runtime、store、AppShell、header model/component 或其他生产代码，没有 stage、commit、push，也没有进入 Task 4。RED 锁定 recovery wire fail-closed、通用 RPC boolean 兼容、窄 recover result 投影、per-thread pending exactly-once、stale active notice gate，以及 requesting/accepted/failed 文案边界。

### DAG

```text
Task 2 commit 83534c85ffa5787bb8b008dce3e3e72ebae3c022
  -> Task 3 Phase A LSP discovery
  -> validator / callBackend integration RED
  -> runtime transport projection RED
  -> store pending / stale active RED
  -> AppShell / header selector and action RED
  -> exact focused RED + lint + diagnostics + dirty boundary
  -X-> Task 3 Phase B production implementation
  -X-> Task 4
```

### RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 1 | LSP `grep/structure/inspect/xref/file(read_file)` | success | 锁定 `createBackendResponseValidators -> createBackendCaller -> recoverThread -> activeThreadRPC -> store -> AppShell/header`；确认当前缺 THREAD_RECOVER validator、recover response 被 boolean 投影丢弃、header 无 pending projection |
| 2 | validator + API 两文件 RED | 1 | 2 files failed；10 failed / 90 passed；validator 未注册，非法 envelope 因此到达 consumer |
| 3 | 计划指定 exact 7-file RED | 1 | 7 files failed；19 failed / 314 passed（333 total）；失败均对应缺失的计划行为 |
| 4 | 7 个 changed tests 定向 ESLint | 0 | 无 error / warning；没有测试 harness 或 mock lint 问题 |
| 5 | LSP diagnostics 7 个 changed tests | 0 | `No diagnostics found`、total 0 |

最终 exact 命令：

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

最终输出：`Test Files 7 failed (7)`；`Tests 19 failed | 314 passed (333)`；exit 1。

### EVIDENCE

#### LSP owner 与调用时点

- recovery wire schema 的唯一 owner 是 `shared/api/backendResponseValidators.js:createBackendResponseValidators`；当前返回表未注册 `methods.THREAD_RECOVER`。
- `backendApiFactoryCore.js:createBackendCaller` 先 `await callAPI`，再按 rpc method 查 `BACKEND_RESPONSE_VALIDATORS`，验证完成后才向 facade/runtime consumer 返回；API RED 用 `.then(runtimeConsumer)` 直接证明非法响应当前仍会穿透，Phase B 注册 validator 后 consumer 必须保持 0 calls。
- `backendApiFactoryThread.js:recoverThread` 只负责严格 request payload（移除 cwd，发送 `{ threadId }`），不应新增第二套 response schema 或错误文案。
- `threadLifecycleRuntime.js:activeThreadRPC` 的生产调用仅 interrupt、force-complete、compact、recover 四处；前三者和既有 unit tests都要求 boolean 返回。Phase B 应由私有 transport runner保留 `{ ok, threadId, result }`，通用方法继续 boolean，窄 `recoverActiveThreadRPC` 只读取已验证 result 的 `recovered`。
- `appShellModel.js` 目前只投影 `actionNotice` / `recoverActiveThread` 等既有字段，没有 pending map；`chatHeaderFeedbackForStore` 目前只选择 bootstrap failure 或 `actionNotice`，`ChatPageHeader` 两个 recover trigger 只看 `hasActiveThreadActions`，尚不能表达 active thread requesting。

#### RED 行为矩阵

| 场景 | 测试载体 | Phase A 结果 |
|---|---|---|
| canonical recovery envelope | validator unit | validator 缺失 RED |
| 缺 thread/id/status、status 非 recovering、recovered 非 boolean、空 mode、body/thread unknown key | validator parameterized unit | 8 个 fail-closed case 均因 validator 缺失 RED；错误文案只在 schema owner test 定义 |
| 非法 API response 不到 runtime consumer | backend API integration | promise 当前 resolve 且 consumer 被调用，expected RED |
| generic boolean + narrow validated result | runtime unit | `recoverActiveThreadRPC` 尚不存在；既有 generic boolean guard 继续通过 |
| `recovered:false` typed failure | runtime + store | 当前 generic path 返回 true，expected RED；不得出现 accepted notice |
| 同线程重复点击 | store integration + deferred canonical response | 当前发起第二次 RPC，expected RED |
| active A 请求后切换 B | runtime + store deferred response | 当前没有 keyed pending；expected RED要求只清 A pending，不发布 B notice/warning |
| AppShell pending surface | app model unit | 白名单尚无 `threadRecoveryPendingByThread`，expected RED |
| active-thread requesting selector | header model unit | 当前返回 null，expected RED；旧线程 pending 对新 active 保持 null |
| requesting button | header component unit | 当前仍显示可点击“进程恢复”，expected RED要求“正在恢复”且 disabled |
| accepted 文案 | store + header model | 测试只允许“恢复请求已接受，正在恢复”，并显式排除“已恢复完成” |

### NON_TARGET_DIFF

Phase A dirty paths 仅为允许的 7 个测试和本执行记录：

```text
M  docs/plans/2026-07-11-reasonix-frontend-architecture-absorption-execution.md
M  frontend-app/src/app/appShellModel.test.js
M  frontend-app/src/entities/client/model/threadLifecycleRuntime.test.js
M  frontend-app/src/entities/client/model/useClientStore.test.js
M  frontend-app/src/pages/chat/components/ChatPageHeader.test.jsx
M  frontend-app/src/pages/chat/model/chatHeaderModel.test.js
M  frontend-app/src/shared/api/backendApi.test.js
M  frontend-app/src/shared/api/backendResponseValidators.test.js
```

- Phase A BASE / HEAD：`83534c85ffa5787bb8b008dce3e3e72ebae3c022`。
- 主工作区整棵 dirty diff fingerprint：`2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`。
- 主工作区计划文件内容 SHA-256：`6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`。
- 上述两个哈希口径不同，不互相比较；本阶段只读 main，未触碰其 dirty 计划文件。

### TRUTH_SOURCE_CHECK / PHASE B CHECKPOINT

- 没有生产代码变更；当前唯一 schema owner 仍未实现 recovery validator，这是预期 RED，而不是 PASS。
- 测试没有在 runtime/store 复制 thread/id/status/mode/unknown-key schema：runtime mock 只给 `{ recovered }`，用于证明该层只做业务投影；完整 canonical envelope 仅用于 API/store integration fixture。
- `threadRecoveryPendingByThread` 只作为未来最小 per-thread requesting map；测试没有要求持久化 accepted/failed enum、`recovered` UI 终态、`conflict` 或 `recoveryProjection`。
- accepted/failed 只要求一次性 `actionNotice`；后续 thread patch/snapshot 仍是业务状态真相。
- Phase B 必须保持 generic `activeThreadRPC` 纯 boolean；不得为 recover 引入 `boolean | object` 或 capability registry。
- 本 agent 停在 Phase A；主代理复核 owner、test API、19 个 expected failures 与 dirty 边界前，不进入生产实现、GREEN、提交或 Task 4。

### PHASE B RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 1 | validator + API focused | 0 | 2 files / 100 tests passed；非法 recovery envelope 在 `callBackend` 返回前被拒绝，runtime consumer 0 calls |
| 2 | runtime + store focused | 0 | 2 files / 217 tests passed；generic boolean、raw result、typed failure、exactly-once、stale active cleanup 全通过 |
| 3 | runtime 三个 fail-fast/identity 边界 RED | 1 | 10 tests 中 3 expected failures：缺 result 被静默投影 false、缺 pending map 被兜底、alias accepted notice 被误抑制 |
| 4 | runtime 边界修正后 | 0 | 1 file / 10 tests passed；direct result、direct map、normalized alias gate 均 GREEN |
| 5 | AppShell/header focused | 0 | 3 files / 16 tests passed；active pending selector 与 disabled “正在恢复”接线通过 |
| 6 | exact 7-file focused（最终） | 0 | 7 files / 336 tests passed |
| 7 | targeted ESLint + `npm run lint` | 0 | changed production/tests 与全量 `eslint .` 无 error/warning |
| 8 | production code-size（首次） | 1 | runtime nesting 6→5，仍超过上限 4；仅做等价 helper/单行 early return 收敛，不调阈值 |
| 9 | production code-size（最终） | 0 | `files=236, frozen=0` |
| 10 | `npm test` | 0 | critical-skip、silent async、contract/store、code-size、TS contracts、RPC audit 全通过；119 files / 1444 tests passed |
| 11 | `npm run build` | 0 | Vite transformed 5541 modules；built in 630ms；embed sync 未留下额外 dirty 产物 |
| 12 | LSP final grep/read/inspect/xref/diagnostics | 0 | JS hover/xref 对新 closure 无结果，grep/read 精确确认调用；15 个 changed source/test/doc diagnostics 为 `No diagnostics found`、total 0 |
| 13 | truth-source grep | 0 | `recoveryProjection` 0；生产“已恢复完成”0；runtime `outcome.result.*` 唯一命中为 `.recovered === true` |

### PHASE B EVIDENCE

#### 唯一 wire schema 与 API 边界

- `backendResponseValidators.js` 唯一保存 recovery body keys `{ thread, recovered, mode }` 与 thread keys `{ id, status }`，并强制注册 `methods.THREAD_RECOVER`。
- validator fail-closed 检查 object shape、non-empty id/mode、status 必须为 `recovering`、recovered 必须 boolean，以及 body/thread unknown key；错误文案只存在于该 schema owner 及其测试。
- `backendApiFactoryThread.js` 无修改；request 仍只发送 `{ threadId }`，response 继续由统一 `createBackendCaller` 验证。API integration 证明非法 response 的 `.then(runtimeConsumer)` 不执行。

#### runtime / store 责任边界

- 私有 `runActiveThreadRPC` 始终返回 `{ ok, threadId, result }`，成功时原样保留 API 已验证 result；generic `activeThreadRPC` 只投影 boolean，interrupt/force-complete/compact 的既有 notice/warning tests 全部保持 GREEN。
- 窄 `recoverActiveThreadRPC` 唯一读取的 recovery response 字段是 `outcome.result.recovered === true`；不读取或检查 thread/id/status/mode/unknown keys。缺 result 直接 TypeError，finally 仍清 pending，不静默变成 typed failure。
- `threadRecoveryPendingByThread` 只在 `clientStoreUtils.js` base state 初始化；runtime 直接读取，不用 optional/default fallback。相同 backend thread pending 时第二次调用立即 false且不发 RPC。
- settle 的 finally 总是删除 captured thread pending；notice gate 用 `backendThreadIdForState` 归一化当前 active identity。切换到不同 backend thread 后旧 success/failure/throw 均不污染新 notice/warning；active alias 仍映射同一 backend thread 时不会误抑制 accepted。
- `recovered:true` 仅发布 success `恢复请求已接受，正在恢复`；`recovered:false` 返回 false并发布 warning `恢复请求失败` + `thread.recover.failed`；transport throw 保持既有 `恢复连接失败：...` error/warning surface。

#### AppShell 与 header 投影

- AppShell 白名单只新增 `threadRecoveryPendingByThread`，没有订阅第二 recovery state 或 response envelope。
- `chatHeaderFeedbackForStore` 在 bootstrap failure 之后、actionNotice 之前，按 activeThreadId 从唯一 pending map 派生一次性 `{ recoveryRequesting: true, message: '正在恢复' }`；旧线程 pending 对新 active 返回 null。
- `ChatPageHeader` 只消费 store selector/action；legacy button 与 actions menu 在 requesting 时统一显示“正在恢复”并 disabled，不直接 import/call backend。
- accepted/failed 仍只存在于一次性 `actionNotice`；真实 thread patch/snapshot 继续拥有业务 status。

### PHASE B NON_TARGET_DIFF

- 生产修改严格限定为计划内 validator、runtime、store action/base state、AppShell、header model/component；`backendApiFactoryThread.js` 经证据确认无需修改。
- 测试严格限定为计划列出的 7 个文件；本执行记录是唯一手工文档修改。
- 没有新增 capability registry、`recoveryProjection`、长期 accepted/failed enum、`recovered` UI 终态、conflict、后端 event/error code、依赖或 guard 阈值。
- build sync 未留下 `web-dist`/`dist` tracked diff；提交 hook 若由单一生成器刷新 project-map/codemap 索引，只在 hook 通过后纳入同一原子提交并逐项核对。
- 主工作区整棵 dirty diff fingerprint 仍为 `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`；计划文件内容 SHA-256 仍为 `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`。两者口径不同且 main 始终只读。

### PHASE B TRUTH_SOURCE_CHECK / STOP

- wire truth 只有 `backendResponseValidators.js`；runtime `{ recovered }` unit mock故意省略其他 wire 字段，证明 runtime不复制 schema。
- requesting truth 只有 `threadRecoveryPendingByThread`；accepted/failed只映射 actionNotice，不持久化。
- runtime truth-source grep 只允许 `outcome.result.recovered === true`；缺 result/map fail-fast，不得恢复 optional fallback。
- Task 3 仅在原子提交 hook、post-commit clean 和主工作区 fingerprint 复核完成后结束；随后停止，不进入 Task 4，也不 push。

## Task 4 — 生产 ErrorBoundary 与隐私安全 crash diagnostics

### STATE

`GREEN`（Phase A RED 已完整保留；Phase B 最小实现与全量验证完成）

Phase A 没有创建 `AppErrorBoundary.jsx`、`frontendBreadcrumbs.js` 或 `frontendCrashReport.js`，没有修改 `main.jsx`、`App.jsx`、test setup、UI test harness 或其他生产代码，没有 stage、commit、push，也没有进入 Task 5。三个 focused suites 均因对应计划模块不存在而 module-missing RED；测试 lint 与 LSP diagnostics 为零，证明当前失败不是语法、mock、jsdom event 或源码 contract harness 问题。主代理审核 Phase A 后才授权 Phase B；下方完整保留 Phase A 失败证据，并追加 Phase B GREEN、时钟 ratchet 修正与全量门禁证据。

### DAG

```text
Task 3 commit a1c1b9ff17eb7aa5eda11150887ac9814bcc1530
  -> Task 4 Phase A LSP discovery
  -> breadcrumb ring / strict-field contract module-missing RED
  -> crash sanitize / reporter / global listener module-missing RED
  -> ErrorBoundary fallback / actions / main order module-missing RED
  -> exact focused RED + lint + diagnostics + dirty boundary
  -> Task 4 Phase B breadcrumb / crash diagnostics
  -> SystemDate clock guard correction
  -> ErrorBoundary / main composition
  -> exact GREEN + full frontend gates
  -> atomic Task 4 commit
  -X-> Task 5
```

### RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 1 | LSP `grep/inspect/xref/file(read_file)` | success with recorded JS gaps | 锁定 main render 顺序、safeLogFields 唯一定义与 19 个可导航引用、现有 trace reporter、dev UI harness collector；emit/Profiler JS hover/xref无结果，已用精确 grep/read补证 |
| 2 | breadcrumb test 单独运行 | 1 | 唯一失败为 `./frontendBreadcrumbs.js` 不存在；0 tests collected；ring与strict-field tests已写入 |
| 3 | crash report test 单独运行 | 1 | 唯一失败为 `./frontendCrashReport.js` 不存在；0 tests collected；privacy/reporter/listener fixtures未执行，无环境错误 |
| 4 | ErrorBoundary test 单独运行 | 1 | 唯一失败为 `./AppErrorBoundary.jsx` 不存在；0 tests collected；React fallback fixture未执行，无console/jsdom错误 |
| 5 | 三测试文件 targeted ESLint | 0 | 无 error/warning；测试 API 与 real-window spy fixture语法有效 |
| 6 | 计划指定 exact 3-file RED（最终） | 1 | 3 suites failed；三个均为对应 production module missing；0 tests collected |
| 7 | LSP diagnostics 三个 changed tests | 0 | `No diagnostics found`、total 0 |

最终 exact 命令：

```bash
cd frontend-app
npx --no-install vitest run \
  src/app/AppErrorBoundary.test.jsx \
  src/shared/diagnostics/frontendBreadcrumbs.test.js \
  src/shared/diagnostics/frontendCrashReport.test.js \
  --no-file-parallelism --maxWorkers=1
```

最终输出：`Test Files 3 failed (3)`；`Tests no tests`；exit 1。三个 suites 的唯一失败分别是 `AppErrorBoundary.jsx`、`frontendBreadcrumbs.js`、`frontendCrashReport.js` 不存在。

### EVIDENCE

#### LSP owner 与接入判定

- `main.jsx:83-93` 当前真实顺序为 `StrictMode -> Profiler -> App`；`Profiler` 使用既有 `APP_PROFILER_ID` 和 `emitSlowRenderTrace`。Phase B 只能插入 `ErrorBoundary` 得到 `StrictMode -> ErrorBoundary -> Profiler -> App`，不得删除或复制 performance monitor。
- `safeLogFields` 唯一定义在 `shared/diagnostics/safeLogFields.js:201-206`；LSP xref可导航到 UI test harness、bridge sanitizer及其测试。默认 forbidden keys已覆盖 token/API key/authorization/prompt/user message/text/content/tool result/memory/skill/stack/raw stack，字符串 sanitizer覆盖 secret assignment、Bearer/sk token、POSIX/Windows/UNC绝对路径。
- 现有 reporter surface 是 `backendApi.js` 导出的 `emitFrontendTraceEvent`，实现落在 `wailsBridgeTraceEvents.js:339-345`；`main.jsx` 已用于 slow render。Phase B 的 diagnostics模块必须接收注入 reporter，main 才注入该 surface；diagnostics不得 import backend/store。
- `test-setup.js` 只配置 Testing Library timeout 和 in-memory localStorage，没有 global error/rejection listener。
- 唯一既有 global collector 是 dev-only `uiTestHarness.js:captureUnhandledErrors@294-308`，由 `installUITestHarness` 的 window global做整体幂等，但两个匿名 listener没有独立cleanup。Task 4 production handlers必须维护自己的 per-window幂等与cleanup，不能重复安装/改写或依赖dev collector。

#### RED 行为矩阵

| 场景 | 测试载体 | Phase A contract |
|---|---|---|
| bounded ring + stable order | `frontendBreadcrumbs.test.js` | capacity 2保留最后两个条目，snapshot按时间/写入顺序稳定 |
| breadcrumb最小字段 | breadcrumb test + source contract | 只允许 actionCode/routeId/phase/timestamp；含prompt未知字段fail-fast且ring不写入；生产必须import/call `safeLogFields` |
| private crash serialization | `frontendCrashReport.test.js` | token、Authorization、POSIX绝对path、prompt、message、tool result、memory、skill、raw stack、component stack和breadcrumb secret均不得出现在JSON |
| reporter failure containment | crash report test | rejected reporter只返回false并调用一次最小 `console.error('[frontend-crash] reporter failed')`，不递归report也不输出原error |
| diagnostics依赖方向 | crash report source contract | 必须import/call同目录 `safeLogFields`；禁止 `useClientStore` / `entities/client` import |
| global install幂等 | real `window` add/remove spies | install两次总计只add `error`/`unhandledrejection`各一次；两个cleanup总计只remove各一次 |
| defaultPrevented | real `ErrorEvent` | 已preventDefault事件仍由预装dev collector看到一次，但production reporter保持0 |
| dev collector不重复计数 | preinstalled real listeners | normal error/rejection每个collector只计一次，production reporter各计一次；重复install不放大 |
| cleanup | real dispatch after cleanup | dev collector仍按自身owner计一次，production reporter不再增加 |
| render crash containment | `AppErrorBoundary.test.jsx` | 可访问 `role=alert` fallback、heading、重试界面与重新加载按钮，非空白窗口 |
| retry/reload | ErrorBoundary test | child停止抛错后retry恢复真实child；reload只调用注入动作 |
| boundary privacy | ErrorBoundary→reporter integration | reporter payload不得含render error message；raw/component stack由crash diagnostics负责脱敏 |
| main composition | ErrorBoundary test raw-source contract | 严格锁定 `StrictMode -> AppErrorBoundary -> Profiler -> App` |

### NON_TARGET_DIFF

Phase A dirty paths 仅为三个计划测试与本执行记录：

```text
M  docs/plans/2026-07-11-reasonix-frontend-architecture-absorption-execution.md
?? frontend-app/src/app/AppErrorBoundary.test.jsx
?? frontend-app/src/shared/diagnostics/frontendBreadcrumbs.test.js
?? frontend-app/src/shared/diagnostics/frontendCrashReport.test.js
```

- Phase A BASE / HEAD：`a1c1b9ff17eb7aa5eda11150887ac9814bcc1530`；开始时 clean。
- 主工作区整棵 dirty diff fingerprint：`2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`。
- 主工作区计划文件内容 SHA-256：`6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`。
- 上述两个哈希口径不同；本阶段只读 main，未触碰其 dirty 计划文件。

### TRUTH_SOURCE_CHECK / PHASE B CHECKPOINT

- 没有生产代码变更；三个生产模块仍不存在，这是预期 RED，不得写成 PASS。
- breadcrumb只拥有有界稳定 action history，不保存Error、message、stack或任意业务payload；未知字段fail-fast，不静默扩展。
- crash report只拥有 normalize/redact/report与global listener lifecycle；ErrorBoundary只拥有React containment/fallback。两者不得合并成store-aware全局单例。
- `safeLogFields` 是字段清洗真相；新模块只声明更窄allowed fields并调用它，不复制secret/path正则。
- global handlers必须忽略已 `defaultPrevented` 事件，但不得主动preventDefault或压制dev collector；幂等/cleanup以window为owner。
- Reporter通过参数注入；Phase B main可传现有 `emitFrontendTraceEvent`，diagnostics模块不得直接import backend/client store。
- 本 agent 停在 Phase A；主代理复核test API、module-missing原因、privacy fixture与4-path dirty边界前，不进入生产实现、GREEN、提交或Task 5。

### PHASE B RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 8 | breadcrumb + crash focused 首轮 | 1 | 2 files 中 1 passed / 1 failed，7/8 tests；唯一失败为显式 `now: null` 被 `??` 错当默认时钟，未改测试 |
| 9 | 收紧 clock fail-fast 后重跑两套 | 0 | 2 files / 8 tests passed；仅 `undefined` 使用默认时钟，`null` 保持 TypeError |
| 10 | `npm test -- --run` 计划 exact 三套 | 0 | critical/async/contract-store/code-size/typecheck/RPC audit 全通过；3 files / 11 tests passed |
| 11 | contract/store clock ratchet 修正 | 0 | 三个 production 文件统一采用 captured `SystemDate` + `currentTimestampISO`；`date-parse-order=0/0`，未放宽阈值 |
| 12 | targeted ESLint + `git diff --check` + privacy/dependency grep | 0 | 7 个 code/test/main 文件 lint 0；diagnostics生产模块无 backend/store import 或 forbidden payload key |
| 13 | LSP grep/definition/xref/read/diagnostics | success | grep定位7个 report调用/引用；xref确认global handler两个调用点；精读boundary delegate；7个 changed code/test文件 `No diagnostics found`、total 0 |
| 14 | `npm run lint` | 0 | 全前端 ESLint 无 error/warning |
| 15 | `npm test` | 0 | guards、contracts与RPC audit通过；122 files / 1455 tests passed |
| 16 | `npm run build` | 0 | Vite 5544 modules built；dist sync完成且未留下tracked `dist`/`web-dist` diff |
| 17 | production code-size guard | 0 | 已由 exact/full test hook各执行一次；`files=352, frozen=0` |
| 18 | 主审来源归因修正后 exact + lint + build + full test | 0 | trace component由固定boundary标签改为稳定 `report.actionCode`；source contract锁定；3 files / 11 tests、lint、5544-module build、122 files / 1455 tests均再次通过 |
| 19 | 原子提交 pre-commit / commit-msg hooks | 0 | generator refresh、AI maintenance、frontend embed、codemap/project-map check、full codebase guard、中文提交守卫全通过；hook内前端122 files / 1455 tests及5544-module build再次通过 |

### PHASE B EVIDENCE

#### containment 与诊断责任边界

- `AppErrorBoundary` 只拥有 React containment、无敏感内容的可访问 fallback、retry与注入reload；`componentDidCatch` 仅把原始Error/component stack委托给 `reportFrontendCrash`，reporter实际收到的窄投影不含二者。
- `frontendCrashReport` 的输出字段固定为 actionCode、routeId、phase、timestamp、breadcrumbs；breadcrumb entry再次只投影同四字段，并全部经过 `safeLogFields`。Error、raw/component stack、fields及业务payload不会进入report对象。
- reporter rejected只返回false并输出一次固定 `console.error('[frontend-crash] reporter failed')`；不输出原error、不重报、不递归。
- diagnostics只依赖同目录 `safeLogFields`；reporter、window、console、route与breadcrumbs均从外部注入，不import backend、client store或dev harness。

#### global listener 与入口接线

- global `error` / `unhandledrejection` 以window WeakMap为owner：重复install返回同一cleanup，不增加listener；cleanup幂等并删除两个listener及owner记录。
- 已 `defaultPrevented` 事件直接忽略；生产handler不调用preventDefault/stopPropagation。real-window测试证明预装dev collector对每个事件自然计数一次，production reporter不会因重复安装放大，cleanup后dev owner继续正常工作。
- `main.jsx` 只创建一个breadcrumb buffer，记录稳定 `app.bootstrap/app/start`，并把既有 `emitFrontendTraceEvent` 包成只映射稳定action/route/phase的reporter；trace component直接使用已脱敏 `report.actionCode` 区分render/global error/unhandled rejection，不用固定boundary标签错误归因，也没有复制performance monitor。
- render顺序严格为 `StrictMode -> AppErrorBoundary -> Profiler -> App`，既有 `APP_PROFILER_ID`、`emitSlowRenderTrace` 与UI test MCP props保持原位。

#### 时钟与隐私硬边界

- 首轮GREEN暴露 `now: null` 被空值合并吞掉；实现改为仅 `undefined` 使用默认clock，保留显式非法依赖fail-fast。
- contract/store guard随后发现直接 `new Date()` 会增加 `date-parse-order` ratchet；实现采用仓库既有captured `SystemDate` helper模式，最终两次guard均为0/0，没有改guard、baseline或测试。
- privacy fixture覆盖token、Authorization、POSIX绝对路径、prompt、message、tool result、memory、skill、raw stack、component stack及breadcrumb额外token；序列化report全部不含这些值。

### PHASE B NON_TARGET_DIFF / TRUTH_SOURCE_CHECK / STOP

- 生产变更只包含计划内三个模块和 `main.jsx`；没有修改 `App.jsx`、test setup、UI test harness、store、backend、trace sanitizer、依赖或guard阈值。
- 测试只包含计划内三个文件；本执行记录是唯一手工文档修改。build sync没有留下dist/web-dist tracked diff；commit hook按单一生成器职责刷新5个project-map/codemap索引文件并纳入同一原子提交，check显示drift=OK。
- breadcrumb ring是唯一action history owner；`safeLogFields`仍是清洗真相；crash report是唯一窄投影与listener lifecycle owner；boundary只做React containment。
- 主工作区整棵dirty diff fingerprint已在提交前只读复核为 `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`，计划文件内容SHA-256为 `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`；main仍只含该计划文件modified且相对origin ahead 3，两者哈希口径不同。
- Task 4 仅在文档diagnostics/validator、显式stage、原子提交hook、post-commit clean和主工作区fingerprint复核完成后结束；随后停止，不进入Task 5，也不push。

## Task 5 — Approval-only 决策面

### STATE

`GREEN`（聚焦、全量、lint、build、LSP、truth-source 与 dirty-boundary 均已独立复核；本记录所在原子提交完成 Task 5，未进入 Task 6，未 push）

Phase A BASE / HEAD 为 `765cc74ec413e0239b25a356af7d80e0f46894df`；分支 `codex/reasonix-frontend-absorption-20260711` 开始时 clean。主工作区保持 `main...origin/main [ahead 3]`，唯一 dirty 文件仍是 `docs/plans/2026-07-11-reasonix-frontend-architecture-absorption.md`；整棵 dirty diff fingerprint 为 `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`，计划文件内容 SHA-256 为 `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`。两种哈希口径不同，本 worktree 对 main 只读。

### DAG

```text
Task 4 commit 765cc74ec413e0239b25a356af7d80e0f46894df
  -> Task 5 Phase A clean baseline / main hash lock
  -> LSP approval wire + store exactly-once truth discovery
  -> LSP composer mounted/disabled + focus owner discovery
  -> plan-only approval model/shelf/integration RED tests
  -> exact 8-file RED + targeted lint + diagnostics
  -> Task 5 serial production slices / focused GREEN
  -> main agent App.test direct-integration migration / full gates
  -> serial resume final LSP + truth-source + boundary audit
  -> explicit Task 5 stage / this atomic commit
  -X-> Task 6 / push
```

### RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 1 | worktree `git status` / `git rev-parse HEAD` | 0 | branch clean；BASE / HEAD `765cc74ec413e0239b25a356af7d80e0f46894df` |
| 2 | main只读status / whole-diff SHA / plan-file SHA | 0 | ahead 3且仅计划文档dirty；两个既有哈希未变 |
| 3 | `approvalDecision.test.js` 单独运行 | 1 | 唯一失败为计划模块 `./approvalDecision.js` 不存在；0 tests collected，无fixture或环境错误 |
| 4 | `ApprovalDecisionShelf.test.jsx` 单独运行 | 1 | 唯一失败为计划模块 `./ApprovalDecisionShelf.jsx` 不存在；0 tests collected，无React/jsdom错误 |
| 5 | 两个A1测试 targeted ESLint | 0 | 无 error/warning；测试语法与API contract有效 |
| 6 | LSP diagnostics 两个A1测试与本执行记录 | 0 | `No diagnostics found`、total 0 |
| 7 | A1.5两个测试分别重跑 + targeted ESLint | expected RED / lint 0 | 两套仍分别只因对应production module missing而0 tests；布尔classifier与false-result retry合同未引入测试自身错误 |
| 8 | `ComposerDock.test.jsx` A2a单文件 | 1 | 7 tests中6 passed / 1 failed；唯一新增test产生8个soft contract failures：external inputRef pending/settled各1、inert、aria-disabled、发送/附件/模型/项目四控制；node identity、draft保留与settled属性移除断言已通过 |
| 9 | `ChatPage.core.test.jsx` A2b单文件最终RED | 1 | 30 tests中26 passed / 4 failed；同线程approval消失/terminal两例均仅缺focus；A→B仅缺composer inert；source contract仅缺唯一ref声明、inputRef传递与ref focus三项；thread switch no-focus及全部26个既有tests通过，无timeout/act warning |
| 10 | A2c exact三文件串行RED | 1 | 3 files failed；24 tests中14 passed / 10 failed。`TimelineMessage` 9/10、grouping 5/6，均仅新增“直接消费approvalDecision adapter”source contract失败，既有approval显示/分组行为保持green；`ChatApprovalMessage` 8/8均按预期因Shelf选择→显式确认、pending exactly-once、false/reject retry、terminal/invalid fail-closed及新adapter/legacy re-export契约尚未实现而失败 |
| 11 | A2c targeted ESLint + `git diff --check` + LSP diagnostics | 0 | 三个A2c测试lint 0；diff whitespace check 0；三个测试与本执行记录均 `No diagnostics found`、total 0 |
| 12 | A2c停止点主工作区只读复核 | 0 | 仍为 `main...origin/main [ahead 3]` 且仅原计划文档modified；whole-diff SHA与plan-file SHA分别保持 `2195780...16d` / `6bbe18c...061` |
| 13 | A3计划原样exact 8 files串行最终RED | 1 | 8 files中1 passed / 7 failed；271 tests中256 passed / 15 failed，另有2个missing-production suites各0 tests。`useClientStore.test.js` 整文件210 tests全green；失败只分布于计划新增approval model/Shelf、ChatApproval migration、Timeline/grouping adapter source contract、Composer pending inert/ref与ChatPage focus/inert/source contract |
| 14 | store exactly-once测试名定向复核 | 0 | `keeps approval RPC submission idempotent per request id while in flight`：1 passed / 209 skipped；断言同request第二次in-flight调用resolve false、backend RPC恰好1次、map保留首次`approved:true/inFlight:true`，首次完成后resolve true并清理map entry |
| 15 | 全部7个changed tests targeted ESLint + `git diff --check` | 0 | lint无error/warning；diff whitespace check 0 |
| 16 | 全部7个changed tests + 执行记录 LSP diagnostics | 0 | `No diagnostics found`、total 0 |
| 17 | A3 dirty / HEAD / main最终边界复核 | 0 | HEAD仍为Task4 commit `765cc74e...94df`，index empty；dirty严格为本执行记录和7个计划test，production无改动。main仍ahead3且仅原计划文档dirty，whole-diff与plan-file SHA完整值保持不变 |
| 18 | A4 Shelf success-gap 单文件RED | 1 | 唯一failed suite仍为计划production `ApprovalDecisionShelf.jsx` missing，0 tests collected；新增fixture未造成新的解析、React或环境错误 |
| 19 | A4 Shelf test ESLint + LSP diagnostics + `git diff --check` | 0 | targeted lint 0；Shelf test与执行记录 `No diagnostics found`、total 0；diff whitespace check 0 |
| 20 | B1.1 `approvalDecision.test.js` 单slice GREEN + lint/LSP | 0 | 1 file / 4 tests passed；production+test targeted ESLint 0，`git diff --check` 0，LSP structure可见仅test API三个export及internal validator，diagnostics `No diagnostics found`、total 0 |
| 21 | B1.2 `ApprovalDecisionShelf.test.jsx` 单slice GREEN + lint/LSP | 0 | 1 file / 8 expanded tests passed（含terminal approved/rejected两个case）；production+test targeted ESLint 0，`git diff --check` 0；LSP definition跳转到domain submission validator、xref仅Shelf test、diagnostics `No diagnostics found`、total 0 |
| 22 | B1.2 request-switch stale settlement RED→GREEN | 0 | 新增old request resolve/reject两个竞态case：首轮9/10，唯一RED为旧reject把error写入新request；token gate首版虽10/10但render期ref读写触发2个`react-hooks/refs` error，改为`useLayoutEffect`切换owner token且event捕获后，最终1 file / 10 tests、target lint、diff check均0，LSP diagnostics total 0 |
| 23 | B1.3 `ChatApprovalMessage.test.jsx` 计划停止点 | expected 1 | 8 tests中7 passed / 1 failed；唯一失败精确为主审要求暂留的legacy `chatApprovalModel.js` 仍含旧逻辑而非pure re-export。strict invalid、selection/confirm、terminal、pending exactly-once、false retry、reject/onError与15s timeout其余7项GREEN；target lint / diff check 0，LSP definition/xref确认adapter调用，diagnostics total 0 |
| 24 | B1.4 approval consumer/legacy 收敛 | 0 | ChatApproval + Timeline + grouping exact三套3 files / 24 tests GREEN；LSP xref定位adapter在Conversation/Timeline/grouping所有production consumer，legacy文本仅剩3个test的4条negative/source-contract证据，production refs 0且文件已删除；7个target code/test lint、truth grep、diff check均0，5个production diagnostics total 0 |
| 25 | B1.5 Composer + ProjectSelector 能力链 | 0 | Composer首轮6/7暴露React19 inert空串被移除及ProjectSelector断链；修正boolean inert后唯一RED仍为project button。主审授权扩边后ProjectSelector新增2 tests首轮3/5，随后正式`isDisabled`能力到4/5但open→disabled未关闭，layout effect owner close后5/5；Composer + ProjectSelector最终2 files / 12 tests，Composer单套7/7 GREEN。5个target files lint、diff check、LSP diagnostics均0；code-size `files=355,frozen=0`；fieldset/DOM query grep 0 |
| 26 | B1.6 Conversation approval focus/composer wiring | 0 | 先修既有global failure fixture为strict pending + Shelf选择/确认，并增加“A settle同时出现新terminal B不得focus”行为锁；implementation前ChatPage 27/31，4个既有focus/inert/source RED。接线后ChatPage + Composer 2 files / 38 tests GREEN。首轮code-size仅拦Conversation 157>150；抽top-level busy helper并补node truthy guard后Conversation恰150行，code-size `files=355,frozen=0`；4 target files lint/diff 0，5 files diagnostics total 0 |
| 27 | B1.7 计划exact 8 + ProjectSelector共9 files | 0 | 9 files / 291 tests passed；包含approval adapter 4、Shelf 10、ChatApproval 8、Timeline 10、grouping 6、Composer 7、store 210、ChatPage 31、ProjectSelector 5，0 skipped / 0 failed |
| 28 | store exactly-once测试名定向复核 | 0 | `keeps approval RPC submission idempotent per request id while in flight`：1 passed / 209 skipped；第二次同ID in-flight返回false、RPC一次、首次结果完成后map清理证据保持GREEN |
| 29 | 17个changed production/test targeted ESLint + contract/store/code-size guards | 0 | ESLint 0；contract/store全部9类ratchet 0/0；code-size `files=355,frozen=0`，Conversation结构证据仍为150行 |
| 30 | 全部changed code/test/doc LSP diagnostics + truth/focus/forbidden grep + diff check | 0 | 18个现存changed文件/文档 diagnostics `No diagnostics found`、total 0；legacy production refs 0且文件不存在；adapter仅shared ID helper与exact三status；Shelf store/backend 0；generic forbidden 0；Conversation显式focus唯一1处、DOM query 0；`git diff --check` 0 |
| 31 | B1.7 HEAD/index/dirty与main只读最终复核 | 0 | HEAD仍Task4 `765cc74e...94df`，index empty；worktree严格19条计划/授权扩边路径。main仍ahead3且仅原计划文档dirty，whole-diff SHA `2195780a...16d`、plan-file SHA `6bbe18cd...061`均完整值不变 |

### PHASE A CONTRACT INTERPRETATION

- “领域名是 approval，不出现 kind/capability/ask/plan”约束新领域文件、符号与抽象不得泛化为 DecisionKind/Capability/Ask/Plan；现有 wire truth 若以 `message.kind === 'approval'` 判别消息，strict adapter读取该 wire `kind` 是合法的，不会用全面源码正则阻断 wire contract。最终是否由 adapter读取kind或由已验证上游保证approval，须以本阶段LSP调用链证据裁决。
- 本阶段只创建/修改计划列出的测试文件和本执行记录；生产文件保持BASE不变。所有RED必须归因于计划生产契约缺失，而不是测试拼写、fixture、jsdom、lint、diagnostics或非目标既有失败。

### PHASE A1 RED EVIDENCE

- `approvalDecision.test.js` 锁定：必须import并调用既有 `shared/api/approvalRequestId.js:positiveApprovalRequestIdFromFields`，不得创建第三份Number/parse解析；只接受positive integer request id、wire `kind: approval` 与精确 `pending/approved/rejected` status；choice只允许 `approve/reject`，terminal request禁止再次提交。领域源码允许读取wire kind，但禁止新建DecisionKind/Capability/Ask/Plan泛化符号。
- `ApprovalDecisionShelf.test.jsx` 锁定：选择choice不会自动提交，必须显式“确认选择”；in-flight确认exactly-once并暴露aria-busy；失败后保留选择、显示alert并允许显式重试；组件通过props注入，不import client store/backend，也不建立泛化decision领域。
- A1.5补充：混合timeline consumer通过非抛错布尔 `isApprovalMessage` 分类，approval wire返回true，tool/null/array返回false；只有已判定approval进入strict adapter后，非法request id/status才throw fail-closed。Shelf `onConfirm` resolve false与reject同属未settled：busy解除、choice保留且必须允许再次显式确认，但不要求暴露raw error。
- A1 dirty路径严格为本执行记录和上述两个计划测试；没有创建两个production模块，没有修改既有测试或任何production文件，没有stage、commit、Task6或push。
- A2a采用计划窄props `inputRef` / `approvalPending`：`inputRef` 只允许Conversation绑定真实textarea，`approvalPending` 只投影composer pending状态，不引入store读取。ComposerDock rerender test锁定pending false→true→false全过程textarea node identity与draft不变、external ref始终指向该node；pending根节点必须有native inert与 `aria-disabled=true`，并通过既有 `canUseProjectActions` 链禁用fixture中实际渲染的发送、附件、模型和项目选择四个控制，settled后移除inert/disabled语义。ChatPage integration不在A2a范围。
- A2b只修改ChatPage core integration test：每个focus场景先把activeElement设为wrapper按钮并断言不是textarea；同一active thread的pending approval消失或变approved且没有新pending时，原textarea identity不变并获得focus；thread switch后的旧approval settle不得focus新thread composer；approval A变terminal但同时出现pending B时不得focus且composer继续inert。source contract只锁Conversation唯一 `composerInputRef` 声明、显式 `inputRef` prop和从该ref调用focus，并禁止 `document.querySelector/getElementById` DOM查找；不禁止既有reasoning `setTimeout`。
- A2c把ChatApprovalMessage integration收敛为Shelf choice→confirm→wire boolean：选择不提交、确认后actions exactly-once、in-flight重复确认不放大、false/reject保留choice可重试、terminal禁用、invalid strict adapter fail-closed；既有reject/timeout错误回归继续保留。ChatApprovalMessage、TimelineMessage与grouping source contract都要求直接消费新approvalDecision adapter；旧chatApprovalModel只允许删除或无逻辑唯一re-export。Store现有 `useClientStore.test.js:6331-6353` 已准确覆盖 `approvalSubmitByRequestId` in-flight exactly-once、RPC一次和finally清理，本阶段不为数量修改6981行大test。
- A2c exact命令只运行计划内三个integration/grouping测试：Timeline与grouping除各自新增source contract外全部既有行为通过；ChatApprovalMessage的8个RED都在旧的一键按钮/宽松解析/旧model import与计划Shelf交互之间，未出现fixture、环境或非目标测试错误。invalid approval采用strict adapter抛错实现fail-closed；terminal approval保留可见choice但全部disabled。生产代码仍未修改。
- A3 final RED matrix：`approvalDecision` 与 `ApprovalDecisionShelf` 因两个计划production module不存在，各为failed suite / 0 collected；`ChatApprovalMessage` 8/8 RED；`TimelineMessage` 9/10与grouping 5/6仅adapter source contract RED；`ComposerDock` 6/7，仅一个soft-assert contract test RED；`ChatPage.core` 26/30，两个同线程settle focus、一个pending replacement inert、一个唯一ref/source contract RED；`useClientStore` 210/210 GREEN。合计271个已收集tests为256 pass / 15 fail，所有失败均落在计划锁定的尚未实现生产契约。
- A3停止条件满足：未改`useClientStore.test.js`，其既有exactly-once测试提供真实store/RPC证据；未创建任何production module，未改既有production、guard、baseline、依赖或配置；未stage、commit、push，也未进入Task6。等待主审裁决后才可进入GREEN。
- A4补锁store map cleanup与wire patch之间的UI窗口：同一request的`onConfirm` resolve true后，Shelf必须保持本地resolved并禁用choice/confirm，继续点击不得增加调用；仅当`requestId`变化时清空choice/resolved，新的pending request才允许重新选择并确认。`approved/rejected` terminal request初始三项控制全部disabled且任何点击不得调用`onConfirm`。false/reject仍按A1.5保持retryable，不得与successful local resolved混同。

### PHASE B1.1 IMPLEMENTATION EVIDENCE

- 新增唯一domain adapter `features/approval/model/approvalDecision.js`：boolean predicate对tool/null/array返回false；strict adapter只接受approval、shared helper验证的positive integer ID及精确pending/approved/rejected；submission只接受approve/reject且拒绝terminal。未复制Number/parse ID逻辑，未增加generic decision词汇或额外对外API。
- 本slice只写adapter与本执行记录；Shelf、legacy/consumer、Conversation、Composer及store均未改，按主审检查点停止。

### PHASE B1.2 IMPLEMENTATION EVIDENCE

- 新增依赖注入Shelf，仅接收`request/onConfirm`；choice与显式确认分离，`approvalSubmissionFor`在调用transport前完成choice/terminal最终校验，没有复制request ID解析，也未import store/backend。
- busy、successful local resolved均以requestId隔离：pending重复确认锁定；resolve false/reject保留choice并可重试；resolve true后同request三项控制持续disabled；requestId变化同步清空choice/busy/resolved/error；初始approved/rejected全disabled；reject错误以`role=alert`显示。
- request owner另由每次requestId变化时在layout effect更新的opaque token隔离；async resolve、reject/error与finally busy cleanup都先核对当前token，因此旧request晚到的成功、错误或清理均不能污染新request，且不依赖render期ref读写。
- 本slice没有修改adapter、legacy/consumer、Conversation、Composer、store、CSS或其他生产文件；未stage、commit、Task6或push，按主审检查点停止。

### PHASE B1.3 IMPLEMENTATION EVIDENCE

- `ChatApprovalMessage` render入口直接调用strict `approvalRequestFromMessage`，非法approval在渲染前throw；确认回调再用`approvalSubmissionFor`把Shelf的approve/reject映射为wire boolean，不再持有submitting/resolved/choice/error React state。
- transport仍以15秒`Promise.race`负责timeout并在finally清timer；resolve false原样返回Shelf保持retryable；reject/timeout先调用`actions.onError('approval.failed', message)`，随后rethrow由Shelf显示`role=alert`并保留选择；resolve true交Shelf的request-scoped local resolved锁。缺少`actions.onApproval`时传undefined，Shelf全禁用。
- 本slice按要求未改legacy model，因此ChatApproval单suite仅source contract保留1个预期RED；Timeline/grouping/Conversation/Composer/store/CSS与其他生产均未动，未stage、commit、Task6或push。

### PHASE B1.4 IMPLEMENTATION EVIDENCE

- Timeline、grouping与Conversation的现有`isApprovalMessage` import均直接指向`features/approval/model/approvalDecision.js`；ChatApproval已直接使用同一adapter，四个production consumers不存在第二truth source。
- 删除`chatApprovalModel.js`而不保留compat逻辑。LSP legacy grep的4个结果全部位于source-contract tests，production scope的`rg`为0；新adapter xref明确返回Conversation、Timeline与grouping实际调用点。
- Conversation本slice只换import，没有新增pending selector、focus owner逻辑或Composer props；Composer/store/CSS/其他生产未动，未stage、commit、Task6或push，按主审检查点停止。

### PHASE B1.5 IMPLEMENTATION EVIDENCE

- `ComposerDock`新增窄`inputRef/approvalPending`；internal object ref继续唯一服务drop effect，`useImperativeHandle`把同一真实textarea同步给外部object/callback ref并由React在unmount清空，不增加focus owner。pending切换不改变textarea identity或draft。
- `effectiveCanUseProjectActions = canUseProjectActions && !approvalPending`统一派生canInterrupt/canSend/projectActionBlocked并传入ComposerMeta；pending root使用React19正确的boolean native inert与`aria-disabled=true`，send/attach/model/project四控制实际disabled，settle后属性与能力恢复。
- 计划原假设“既有canUseProjectActions链已贯穿项目选择”与源码不符：ComposerMeta此前把prop命名为unused `_canUseProjectActions`，ProjectSelector没有disabled API。主审基于该证据授权最小扩边ComposerMeta、ProjectSelector及direct test；实现由唯一`projectActionBlocked`透传React Aria正式`isDisabled`，不使用fieldset或DOM hack。
- ProjectSelector disabled时trigger/MenuTrigger均不可打开，select/add/remove callbacks均再次fail-closed；若菜单已open，layout effect同步关闭，恢复enabled不自动重开。WorkbenchSidebar/Header既有调用不传prop，保持默认enabled。Conversation/store/CSS/其他生产未动，未stage、commit、Task6或push。

### PHASE B1.6 IMPLEMENTATION EVIDENCE

- 同文件`approvalSnapshotFromMessages`只用`isApprovalMessage + approvalRequestFromMessage` strict遍历，输出当前latest pending与known request IDs；非法approval仍throw，不用try/catch或默认status。新增terminal B即使不是pending也会作为新identity阻止A settle抢焦点。
- 同文件小hook只在previous pending→无pending、同thread、真实`node`存在、current node与原node严格同一且current known IDs没有新approval identity时调用`composerInputRef.current.focus()`；A→B pending、A settle+terminal B、thread switch均不focus。ref未绑定/已卸载时显式`node` guard阻止null===null后调用focus。
- Conversation本体只有唯一`const composerInputRef = useRef(null)`，显式`inputRef={composerInputRef}`；没有DOM query/id或focus timer。既有唯一setTimeout仍只属于pending reasoning hint，不参与approval focus。approval snapshot/knownIds/focus逻辑均在top-level helper/hook，生产guard确认Conversation为150行。
- `approvalPending`传ComposerDock，同时以`effectiveCanUseProjectActions`喂给`useComposerInteractions`的canUse/projectActionBlocked，conversation-level drag/drop链与dock send/attach/model/project/paste/drop能力一致阻断；Composer节点始终沿既有intro/docked位置挂载，不因approval条件分支替换。
- ChatPage既有全局错误回归fixture按strict wire补`status: pending`并迁移到“同意→确认选择”，仍验证全局visible alert；未放宽adapter或保留旧按钮。未改store/CSS/其他生产，未跑全量/build，未stage、commit、Task6或push。

### PHASE B1.7 FINAL FOCUSED GREEN

- 计划exact 8 files原顺序加主审授权扩边`ProjectSelector.test.jsx`在同一串行Vitest进程获得9 files / 291 tests全GREEN；`useClientStore.test.js`整文件210/210包含真实store第二层，随后名称定向复核再次GREEN。
- contract/store guard与production code-size guard均直接执行并保留输出；没有运行`npm test`全量、`npm run build`、dist sync或任何commit hook，不把聚焦GREEN冒充全量/build证据。
- B1 dirty路径完整如下；`useClientStore.test.js`只运行未修改，未新增store/wire method/capability registry/overlay/generic decision，也未修改CSS、依赖、guard或baseline：

```text
M  docs/plans/2026-07-11-reasonix-frontend-architecture-absorption-execution.md
?? frontend-app/src/features/approval/model/approvalDecision.js
?? frontend-app/src/features/approval/model/approvalDecision.test.js
?? frontend-app/src/features/approval/ui/ApprovalDecisionShelf.jsx
?? frontend-app/src/features/approval/ui/ApprovalDecisionShelf.test.jsx
M  frontend-app/src/pages/chat/ChatPage.core.test.jsx
M  frontend-app/src/pages/chat/components/ProjectSelector.jsx
M  frontend-app/src/pages/chat/components/ProjectSelector.test.jsx
M  frontend-app/src/pages/chat/composer/ComposerDock.jsx
M  frontend-app/src/pages/chat/composer/ComposerDock.test.jsx
M  frontend-app/src/pages/chat/composer/ComposerMeta.jsx
M  frontend-app/src/pages/chat/thread/ChatApprovalMessage.jsx
M  frontend-app/src/pages/chat/thread/ChatApprovalMessage.test.jsx
M  frontend-app/src/pages/chat/thread/Conversation.jsx
M  frontend-app/src/pages/chat/thread/TimelineMessage.jsx
M  frontend-app/src/pages/chat/thread/TimelineMessage.test.jsx
D  frontend-app/src/pages/chat/thread/chatApprovalModel.js
M  frontend-app/src/pages/chat/thread/chatTurnGroupingModel.js
M  frontend-app/src/pages/chat/thread/chatTurnGroupingModel.test.js
```

- 当前STATE保持未stage/未commit；Phase B1.7完成后停止等待主审，不进入Task6或push。

### PHASE B2 FULL-GATE EVIDENCE

- 子 agent 首次 `npm test` 的前置 guards、typecheck 与 RPC audit 已明确通过，但最终 Vitest summary 因 exec session 未保留而不可判定；该轮不记为 PASS 或 FAIL，也没有据此修改文件。
- 主 agent 随后以保留 session 的同一 `npm test` 独立复跑，得到明确 exit 1：124 files 中 123 passed / 1 failed，1482 tests 中 1481 passed / 1 failed。唯一失败是 `App.test.jsx > submits timeline approval decisions from the React chat timeline` 仍查询旧的一键按钮 `同意审批 11`；生产 DOM 已正确呈现“同意 / 拒绝 / 确认选择”，不是生产逻辑或 adapter 失败。
- 原串行 agent 的模型通道随后连续返回外部 401，主 agent 未启用第二个 agent，只最小迁移上述直接集成测试：先选择“同意”并证明 backend 尚未调用，再点击“确认选择”，成功后锁定choice与confirm均disabled。定向复核为1 passed / 189 skipped，target ESLint exit 0，`App.test.jsx` LSP diagnostics 0。
- full `npm test`、build 与最终提交证据仍待 fresh 重跑；本节不得被解释为全量GREEN。
- 主 agent 在上述定向修正后以保留 session 的 fresh `npm test` 再次复核：exit 0，124 files / 1482 tests全部通过；critical-skip、silent-async、contract/store九类ratchet、code-size `files=355/frozen=0`、contracts typecheck与RPC audit均在同一命令中PASS。
- build 与最终提交证据仍待完成；只有本条可作为当前full-test GREEN证据。
- 主 agent 随后执行 `npm run build`：exit 0，Vite转换5545 modules并完成frontend dist sync；`git status`/name-only复核没有新增tracked `dist` 或 `web-dist` diff，`git diff --check` exit 0。
- 主 agent 在`App.test.jsx`最小迁移后重新执行全量 `npm run lint`：exit 0、无error/warning。
- 原串行 agent 的外部401仍待恢复；主 agent 因此仅完成上述旧集成测试迁移与独立全量门禁，没有启用第二个并行实现者。恢复后的唯一串行替代 agent 没有重做实现，只承担最终审计、记录、显式 stage 与原子提交。

```text
M  docs/plans/2026-07-11-reasonix-frontend-architecture-absorption-execution.md
M  frontend-app/src/App.test.jsx
M  frontend-app/src/pages/chat/ChatPage.core.test.jsx
M  frontend-app/src/pages/chat/components/ProjectSelector.jsx
M  frontend-app/src/pages/chat/components/ProjectSelector.test.jsx
M  frontend-app/src/pages/chat/composer/ComposerDock.jsx
M  frontend-app/src/pages/chat/composer/ComposerDock.test.jsx
M  frontend-app/src/pages/chat/composer/ComposerMeta.jsx
M  frontend-app/src/pages/chat/thread/ChatApprovalMessage.jsx
M  frontend-app/src/pages/chat/thread/ChatApprovalMessage.test.jsx
M  frontend-app/src/pages/chat/thread/Conversation.jsx
M  frontend-app/src/pages/chat/thread/TimelineMessage.jsx
M  frontend-app/src/pages/chat/thread/TimelineMessage.test.jsx
D  frontend-app/src/pages/chat/thread/chatApprovalModel.js
M  frontend-app/src/pages/chat/thread/chatTurnGroupingModel.js
M  frontend-app/src/pages/chat/thread/chatTurnGroupingModel.test.js
?? frontend-app/src/features/approval/model/approvalDecision.js
?? frontend-app/src/features/approval/model/approvalDecision.test.js
?? frontend-app/src/features/approval/ui/ApprovalDecisionShelf.jsx
?? frontend-app/src/features/approval/ui/ApprovalDecisionShelf.test.jsx
```

### PHASE B3 SERIAL RESUME FINAL AUDIT / RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 32 | 恢复后的唯一串行替代 agent：`git status --porcelain=v1 -uall` / `git diff --cached --name-status` | 0 | dirty 恰为上方20条 Task 5计划/主审授权路径；index empty；HEAD仍为Task4 `765cc74ec413e0239b25a356af7d80e0f46894df`，没有额外diff |
| 33 | LSP `grep` / `inspect(definition,hover)` / `xref(references)` / `file(read_file,diagnostics)` | 0 | shared request-id helper跳转到唯一实现；adapter references覆盖ChatApproval、Conversation、Timeline、grouping；19个现存changed code/test/doc diagnostics total 0；已删除legacy文件不伪造diagnostics |
| 34 | truth-source / focus / forbidden grep + `git diff --check` | 0 | legacy production refs 0且旧文件不存在；request-id helper定义1份；status精确为pending/approved/rejected；Shelf store/backend import 0；approval generic禁词0；Conversation唯一composer ref/唯一focus且DOM query 0；diff check 0 |
| 35 | 计划exact 8 files + 主审授权`ProjectSelector.test.jsx` | 0 | fresh 9 files / 291 tests passed，0 failed |
| 36 | `npm test` | 0 | fresh guards、contracts typecheck、RPC audit全部PASS；124 files / 1482 tests passed，0 failed |
| 37 | `npm run lint` | 0 | fresh ESLint全量无error/warning |
| 38 | `npm run build` | 0 | fresh Vite 5545 modules；dist sync完成；随后status/name-only确认没有新增tracked `dist` / `web-dist` diff |
| 39 | main只读status / whole-diff SHA / plan-file SHA | 0 | main仍ahead3且仅原计划文档dirty；whole-diff `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`；plan `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061` |
| 40 | 首次`git commit -m "feat(frontend): harden approval decision flow"` | 1 | pre-commit的codemap/project-map refresh、AI maintenance、两轮124/1482、build/embed、archtest/full code guard、lint全部PASS；commit-msg只因仓库中文标题守卫拒绝英文title。没有`--no-verify`，HEAD仍Task4 |
| 41 | 首次hook生成物diff / cached边界复核 | 0 | 唯一生成器`generate_ai_project_map.mjs --filesystem-scan`刷新5个project-map文件；4396→4399准确反映4个新增approval文件减1个legacy文件，app-ui索引同步source/test大小，docs-agent仅同步本记录大小；cached严格为Task5 20 paths + 5个生成物 |

### PHASE B3 NON_TARGET_DIFF / TRUTH_SOURCE_CHECK / STOP

- 原 agent 401 前的实现与RED/GREEN证据、主 agent 对旧`App.test.jsx`的最小迁移及fresh全量验证均原样保留；替代 agent 本轮没有改生产代码或测试，只修正本记录中B2的完整20-path边界、补充独立复核证据并执行原子提交。
- 生成物责任保持不变：fresh build没有制造tracked `frontend-app/dist`或根`web-dist`差异；commit hook若刷新项目地图，只允许纳入该hook明确生成且语义归属本提交的文件，否则提交停止。
- commit-msg失败后主 agent 明确批准按仓库规范将title收敛为`feat(frontend): 加固审批决策流程`；这是对仓库强制中文守卫的修正，不是绕过hook。首次hook产生的5个project-map文件属于同一生成器且与Task5文件增删/大小一一对应，因此随本提交纳入。
- Task 5只在显式stage这20条路径、cached diff复核、commit hook通过、post-commit clean以及main两种hash再次不变后结束。完成后停止，不进入Task 6，不push。
