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
| Task 5 approval-only | `GREEN` | `952ba9ea988a5478152533e1b6744fc1256f40f6`；实现、全量门禁、生成物刷新与原子提交完成 |
| Task 6 shell discovery | `RED` | GO只锁`rightPanelWidth`；Phase A两套plan-only tests分别因对应production module missing而RED，等待主审授权GREEN |
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

## Task 6 — Shell layout discovery

### STATE

`GREEN`（唯一迁移候选`rightPanelWidth`已完成strict schema、可注入vanilla StoreApi、App/route/page显式接线、旧business truth删除及B4.2 fresh full gates；`rightPanelOpen`与`threadRailWidth`未迁移。不进入Task 7、不push）

BASE / HEAD 为Task5提交`952ba9ea988a5478152533e1b6744fc1256f40f6`，开始时worktree与index均clean。主工作区仍为`main...origin/main [ahead 3]`且仅原计划文档dirty；whole-diff SHA为`2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`，plan-file SHA为`6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`。

### DAG

```text
Task 5 commit 952ba9ea988a5478152533e1b6744fc1256f40f6
  -> Task 6 clean baseline / main hash lock
  -> 7-file discovery set structure + precise read
  -> rightPanelWidth / rightPanelOpen / threadRailWidth LSP grep + inspect + xref
  -> storage / tests / ADR evidence
  -> literal decision-gate evaluation
  -> GO(rightPanelWidth only)
  -X-> create Shell files / modify frontend source or tests
  -X-> stage / commit / Task 7 / push
```

### RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 1 | worktree `git status` / `git rev-parse HEAD` | 0 | branch clean；BASE / HEAD为`952ba9ea988a5478152533e1b6744fc1256f40f6` |
| 2 | main只读status / whole-diff SHA / plan-file SHA | 0 | ahead3且仅计划文档dirty；两个既有hash完整值不变 |
| 3 | LSP `grep(text_search)`三字段 | 0 | `rightPanelWidth`18条含6 test；`rightPanelOpen`35条含test与未被引用的`AppWindowFrame`；`threadRailWidth`4条。文本命中只用于定位，不直接冒充consumer count |
| 4 | LSP `structure(document_symbol)` discovery set | 0 | 定位App shell owner、ChatPage、两个layout hooks、layout model、baseState与resource-page action；大文件结果收窄到精确函数读取 |
| 5 | LSP `inspect(hover/definition)` + `xref(references)` local symbols | 0 | ChatPage local `rightPanelWidth` alias为声明+2 use；App local `rightPanelOpen`、ChatPage prop、hook open/setOpen均有可复查xref；`threadRailWidth`为声明+1 read，setter为声明+3 writes |
| 6 | LSP dynamic Zustand property retry | capability blocker | 从`store.rightPanelWidth`与`store.setRightPanelWidth`精确usage做definition/xref均返回0；从baseState property xref只返回声明与tests，从action property xref只返回声明。已收窄到单文件/单位置并重试，故raw store consumer数由LSP grep定位后逐函数`file(read_file)`裁决，不把失败的xref写成PASS |
| 7 | storage / persistence discovery | 0 | client store为裸`create(createClientStore)`，无Zustand persist；layout三字段没有storage key；全前端localStorage命中仅theme/log/debug/i18n等其他语义；sessionStorage 0；当前layout tests只锁几何、键盘和in-memory drag commit |
| 8 | ADR直接相关搜索 | 0 | 当前`docs/adr`中`rightPanel`/`layout`均0；持久化命中仅workflow/MCP其他领域，没有Shell layout已接受决策 |
| 9 | 15个discovery/consumer/test文件LSP diagnostics | 0 | `No diagnostics found`，total 0 |

### FIELD EVIDENCE / DECISION TABLE

| 字段 | 定义与唯一writer | 生产消费者（排除tests/死文件） | 边界 | 刷新后持久化证据 | 能否删除client business store旧真相 | 裁决 |
|---|---|---|---|---|---|---|
| `rightPanelWidth` | truth定义为`clientStoreUtils.js`的`baseState.rightPanelWidth: 380`；唯一状态写入口是`clientStorePageActions.js:createResourcePageCacheActions.setRightPanelWidth -> runtime.set`。生产调用全部位于`useChatWorkbenchLayout.js` | raw truth有3个函数级consumer：`useThreadRailLayout`、`useRuntimeSidePanelLayout`、`useRuntimePanelWidthSync`；同文件keyboard/toggle/drag/sync共7个setter call site。ChatPage的派生alias另有grid columns与`RuntimePanelSlot.width`两个实际use，不计作第二truth | 是：entities/client business store → app selector surface → chat page hook；`APP_SHELL_STORE_KEYS`还订阅field和setter，虽AppWindow语义上实际把独立`routeStore`传page | 无。reload回到base 380，panel reopen还会按viewport default重设；没有storage key、roundtrip test或ADR | 是：迁移可同时删除base field、唯一action及AppShell selector中的field/action，并让layout hook改读唯一Shell owner | **GO**，唯一迁移候选 |
| `rightPanelOpen` | App `useAppShellState`的`useState(false)`是唯一truth/writer owner；setter经props传给ChatPage/Header/runtime hook | 4个语义surface：ChatPage layout/slot、ChatPageHeader/ChatActionsMenu labels+actions、thread-rail drag projection、runtime side-panel sync/toggle/resize。`AppRoutes`只是prop transport；无引用的`AppWindowFrame`不计consumer | 是：App → ActivePageContent/route → ChatPage | 无；每次App mount初始化false，没有storage key/test/ADR | 否：只能删除App local state/prop drilling，不能删除client store中的UI-only field | **不迁移** |
| `threadRailWidth` | `useThreadRailLayout`内部`useState(threadRailTargetWidth)`；唯一setter同hook内由viewport effect、pointer finish、keyboard三处调用 | 原symbol xref只有1个read用于`clampWidth`；返回的generic `rail.width`只在ChatPage用于right-panel max与resizer ARIA/status，仍在同一page/hook边界 | 否：component/page-local responsive state | 无且不应默认新增：viewport变化会重新计算/夹紧，当前意图是responsive session state | 否：client store从未拥有该字段 | **不迁移** |

### EVIDENCE / TRUTH_SOURCE_CHECK

- `rightPanelWidth`满足计划的两个硬条件：“多个生产consumer”与“迁移能删除client business store旧truth”，因此Task 6不能标`NO_CHANGE`。GO范围严格只有该字段；不能顺带迁移`rightPanelOpen`、`threadRailWidth`、overlay或theme。
- 计划假设的“刷新后持久化”在当前产品没有事实依据。若主审授权后续实现，新增persistence属于显式产品语义：必须使用注入`get/set/remove` storage port与strict scalar schema；不存在key才是first-run；非法已有值/read/write失败都阻断。当前没有产品reset入口，本轮锁定为“不实现reset，非法值保持BLOCKED”，不得静默自愈。
- 若进入实现，旧truth删除清单锁定为：`clientStoreUtils.js`的`rightPanelWidth`、`clientStorePageActions.js`的`setRightPanelWidth`、`appShellModel.js`的两个selector keys，以及`useChatWorkbenchLayout.js`所有`store.rightPanelWidth/setRightPanelWidth`访问；真实consumer改接唯一Shell owner。不得保留compat re-export或双写。
- `rightPanelOpen`虽然跨app/page且有多个consumer，但不满足“删除client business store字段”；`threadRailWidth`既不跨边界也没有旧store truth。二者不因名字相似或作为width/open派生输入而被并入GO。

### NON_TARGET_DIFF / STOP

- Task 6开始前worktree clean；本轮唯一允许diff是本执行记录。四个计划Shell文件均未创建，frontend production/test、依赖、guard、baseline、codemap/project-map均未修改。
- 当前不stage、不commit、不push；主代理复核三字段证据表、dynamic JS xref blocker与GO范围前，不进入Task 7。

### PHASE A RED STATE

`RED`（主代理批准GO后只创建两套计划测试；两个production modules仍不存在，禁止GREEN实现、既有test迁移、stage/commit、Task 7与push）

### PHASE A RED RESULT_GATES

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 10 | `shellLayoutSchema.test.js`单文件 | expected 1 | 1 suite / 0 tests collected；唯一错误为计划production`./shellLayoutSchema.js`无法解析，没有fixture、Vitest或环境错误 |
| 11 | `useShellLayoutStore.test.js`单文件 | expected 1 | import顺序锁定factory module后，1 suite / 0 tests collected；唯一错误为计划production`./useShellLayoutStore.js`无法解析，没有被schema missing遮蔽 |
| 12 | 两个计划test串行同进程 | expected 1 | 2 suites failed / 0 tests；各自只报告对应production module missing，RED归因清晰 |
| 13 | 两个计划test targeted ESLint | 0 | 无error/warning；测试语法、Vitest API与mock contract有效 |
| 14 | 两个计划test LSP diagnostics | 0 | `No diagnostics found`，total 0 |

### PHASE A RED CONTRACT

- `shellLayoutSchema.test.js`锁定唯一`rightPanelWidthSchema`：key精确为`super-dolphin.shell.right-panel-width`，schema显式initial value为现有base 380。persisted scalar是canonical字符串；合法范围为0到`Number.MAX_SAFE_INTEGER`的有限非负数并保留有限小数，拒绝null/undefined/number输入、空白、前导零、正负号、NaN/Infinity、指数、单位与越界值。
- schema range只保证scalar可无损持久化，不替代viewport geometry clamp；`Number.MAX_SAFE_INTEGER`在schema层合法不表示UI可直接展示该宽度，实际展示仍必须经现有`rightPanelMaxWidth/clampWidth`。
- parse/serialize非法值必须抛稳定typed error：`name=ShellLayoutValidationError`、`code=shell_layout.invalid_right_panel_width`。两个production module的raw source contract均禁止`JSON.parse`；factory source还禁止直接`window.localStorage`，只允许注入port。
- `useShellLayoutStore.test.js`只锁可注入`createShellLayoutStore({ storage })` factory，不预先锁全局singleton、React hook消费方式或App接线。port必须在任何`get`前同步验证`get/set/remove`均为function；缺失或非函数立即fail-fast。
- `storage.get(key) === null`是唯一first-run：factory必须先把schema initial value串行写入成功，再暴露initial state。合法existing scalar直接parse；`setRightPanelWidth`必须先serialize+storage.set成功，再更新state，write失败时state与原storage均不变；第二个factory可从同一port strict roundtrip。
- invalid existing scalar或get失败都阻断初始化；invalid path必须证明原值不变、`storage.set`与`storage.remove`均未调用。当前没有产品reset owner，本轮明确不实现`resetShellLayout`、remove action或错误表面；测试锁定state无reset API。计划的“remove failure/显式reset后first-run”是no-reset决策下不可执行的条件分支，不伪造PASS；非法值持续BLOCKED。

### PROPOSED PHASE B EXISTING-TEST MIGRATION（未修改）

- `frontend-app/src/App.test.jsx`：现有6处直接断言`useClientStore.getState().rightPanelWidth`，GREEN迁移时必须改为新Shell owner或等价可观察layout状态，同时保留“drag move只改DOM、pointer release才提交truth”的行为锁。
- `frontend-app/src/pages/chat/ChatPage.core.test.jsx`：现有fake store的`setRightPanelWidth`断言将在删除client-store action后失效；需改为注入Shell writer并保留resize调用证据。
- `frontend-app/src/app/appShellModel.test.js`：GREEN删除`APP_SHELL_STORE_KEYS`中的`rightPanelWidth/setRightPanelWidth`时，应补negative selector contract，防止旧truth被重新订阅。
- 本Phase A没有修改上述既有测试；`useChatWorkbenchLayout.test.js`只测geometry model且无需因truth owner迁移改写。主代理若不批准这三处精确迁移，Phase B不得碰它们。

### PHASE A NON_TARGET_DIFF / TRUTH_SOURCE_CHECK / STOP

- 当前dirty严格为本执行记录、`shellLayoutSchema.test.js`与`useShellLayoutStore.test.js`三条；index empty。`shellLayoutSchema.js`、`useShellLayoutStore.js`及其他frontend production/test仍未创建或修改。
- 两套source tests已把“no JSON.parse / no direct window.localStorage”升级为未来GREEN可执行truth gate；当前module-missing RED没有引入第二种失败类别。
- Phase A到此停止；不实现production、不迁移既有tests、不stage/commit、不进入Task 7、不push，等待主代理审核。

### PHASE B0 WIRING DESIGN（已批准，未改接线文件）

- 唯一truth owner采用`createShellLayoutStore({ storage })`创建的vanilla Zustand StoreApi；App后续显式持有实例并沿`App -> AppShell -> AppWindow -> ActivePageContent -> ChatPageRoute -> ChatPage`传递。单一ChatPage消费者不新增Provider/context，不创建module singleton。
- storage adapter后续由`App.jsx`显式owner注入；factory module不在module load访问`window`或global localStorage。factory初始化发生在现有`AppErrorBoundary`覆盖下；read/first-write/validation错误直接抛出，不catch、不默认兜底。`main.jsx`的`StrictMode -> AppErrorBoundary -> Profiler -> App`顺序保持不变。
- StrictMode lazy initializer可能执行两次；同步共享port下第一次`get(null)`写入`380`，第二次读取已存在scalar，first-run `set`总数应精确为1，后续由App集成测试锁定。
- `rightPanelOpen`继续由App local state持有，`threadRailWidth`继续由layout hook local responsive state持有。未引用的`AppWindowFrame.jsx`不修改；真实`AppRoutes.jsx`只做显式prop transport。
- 真实后续路径锁定为`frontend-app/src/app/appShellModel.js`、`frontend-app/src/pages/chat/hooks/useChatWorkbenchLayout.js`、`frontend-app/src/entities/client/model/helpers/a1/clientStorePageActions.js`与`frontend-app/src/pages/chat/__tests__/chatPageTestSupport.js`；禁止在相似错误目录创建同名文件。

### PHASE B1 STRICT FACTORY GREEN

`GREEN`（仅两份计划production module与两套新unit tests；未修改App/AppRoutes/ChatPage/layout hook/旧store/既有tests，未stage/commit/Task 7/push）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 15 | 两套focused unit初次GREEN | 0 | `shellLayoutSchema.test.js`与`useShellLayoutStore.test.js`共2 files / 45 tests passed；guard、contract typecheck与RPC audit前置门禁均通过 |
| 16 | diagnostics privacy追加RED | expected 1 | 新增恶意persisted scalar不得进入`error.message`契约；单schema suite 29 tests中唯一1 failed，received message精确回显`<script>leak-storage-value</script>`，归因到validation error message |
| 17 | 固定安全error message后两套focused | 0 | 2 files / 46 tests passed；typed error继续保留稳定`name=ShellLayoutValidationError`与`code=shell_layout.invalid_right_panel_width`，message不再拼接persisted/runtime value |
| 18 | 2 production + 2 test targeted ESLint | 0 | 无error/warning |
| 19 | LSP locate / inspect / xref / read / diagnostics | 0 | grep定位error class与所有throw；definition从factory test跳转`useShellLayoutStore.js:17-41`，hover识别error class，factory xref为声明+9个test references；逐文件精读确认先persist后set；4 files diagnostics为`No diagnostics found`、total 0 |
| 20 | `git diff --check` + forbidden/truth grep | 0 | diff-check无输出；production对`JSON.parse`、`window.localStorage`、`resetShellLayout`、`removeShellLayout`均0命中；唯一`createStore`来自`zustand/vanilla`，storage只调用get/set，remove仅参与完整port shape验证 |

### PHASE B1 CONTRACT / STOP

- `shellLayoutSchema.js`用canonical非负十进制scalar直接parse/serialize，范围为0到`Number.MAX_SAFE_INTEGER`且必须有限；拒绝空白、前导零、符号、指数、单位、非字符串persisted值与无法由同一schema roundtrip的runtime数值。schema range不替代viewport clamp。
- `useShellLayoutStore.js`完整验证注入port的`get/set/remove`三函数后才首次`get`。只有`get(key) === null`进入first-run，并在store暴露前成功写入initial `380`；existing非法值、read失败与first-write失败全部阻断。
- `setRightPanelWidth`严格执行serialize、`storage.set`、内存`set`顺序；后续写失败时内存和原storage保持不变。module仅导出factory与接受显式StoreApi的窄React hook，无singleton、global storage、reset或remove action。
- 当前owned dirty严格为本执行记录，以及`frontend-app/src/app/shell/model/`下两份新production与两份新test；index empty。停止等待主代理Phase B2授权，不进入任何接线、旧truth删除、既有test迁移、stage/commit、Task 7或push。

### PHASE B2 TEST-ONLY INTEGRATION RED

`RED`（只修改四个已批准test/support文件与本执行记录；App/route/page/hook/旧store等production保持Phase B1前状态，未stage/commit/Task 7/push）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 21 | `App.test.jsx`首次执行 | expected 1 | npm参数未把name filter传给Vitest，因而实际完整执行195 tests：186 passed / 9 target failed。无关App行为全部GREEN；9个失败严格为source wiring 1、StrictMode first-run 1、persisted layout 1、ErrorBoundary read/first-write 2、storage commit resize 4 |
| 22 | App direct Vitest精确filter复跑 | expected 1 | `shell layout|right sidebar`命中14个tests：5 passed / 9 failed / 181 skipped；9个失败与上表完全一致，4个resize失败均精确显示observable scalar仍为`380`而非`751/480/150/0`，没有timeout或fixture失败 |
| 23 | `ChatPage.core.test.jsx`完整suite | expected 1 | 31 tests：30 passed / 1 failed；business fake store的setter已为undefined，唯一失败为注入Shell storage writer 0 calls，证明当前ChatPage尚未消费显式`shellLayoutStore` |
| 24 | `appShellModel.test.js`完整suite | expected 1 | 3 tests：2 passed / 1 failed；唯一失败为`APP_SHELL_STORE_KEYS`仍包含`rightPanelWidth`，第二个`setRightPanelWidth`negative assertion被首个失败遮蔽，无其他model回归 |
| 25 | 四个test/support targeted ESLint | 0 | 无error/warning |
| 26 | LSP grep / structure / inspect / xref / read / diagnostics | 0 | grep定位9个shellLayoutStore test/support接点；factory definition跳到`useShellLayoutStore.js:17-41`，harness import definition跳到support `107-113`，hover给出memory port+StoreApi形状，xref为声明、default wrapper、core call与export共5处；精读App contract、support、core writer与selector negative；4 files diagnostics `No diagnostics found`、total 0。首次从core call做definition返回0，收窄到import与factory call后重试成功 |
| 27 | diff / truth / main只读边界 | 0 | `git diff --check`无输出，index empty；App test旧`useClientStore.getState().rightPanelWidth`为0，chat support旧business width/setter为0；main仍ahead3且仅原计划文档dirty，whole-diff SHA `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`、plan SHA `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`不变 |

### PHASE B2 TEST CONTRACT / FAILURE CLASSIFICATION

- `App.test.jsx`新增可观察`get/set/remove/value` fake scalar port，不窥视未导出的shell store。StrictMode contract锁first-run `set(key, '380')`总数精确为1；合法existing `480.5`必须在真实chat layout打开时形成`480.5px`列且不被默认宽度覆盖。
- storage read与first-write失败分别注入现有`AppErrorBoundary`；GREEN必须显示安全fallback、不得渲染chat layout、不得remove/创建fallback state，reporter payload不得泄漏private storage error。当前两项RED均因App忽略注入port而没有触发边界。
- 旧6处client store width读取已全部删除。四个resize tests各自注入独立storage：pointer move只改变DOM且scalar保持`380`，pointer release、buttons=0隐式finish与close才应提交`751/480/150/0`。
- source contract不锁字符串路径或出现次数：结构性要求App创建/持有store，`ChatPageRoute`参数与`ChatPage`/`ActivePageContent`两段显式prop transport，ChatPage通过窄hook消费，layout hook production source不再出现`store.rightPanelWidth/setRightPanelWidth`。
- `chatPageTestSupport.js`已从business fake store删除width/setter，默认每个wrapper用lazy initializer创建独立memory port + `createShellLayoutStore`，也允许core显式注入StoreApi。Core唯一writer assertion转向Shell storage与StoreApi state。
- `appShellModel.test.js`锁selector surface不再包含`rightPanelWidth/setRightPanelWidth`，防止迁移后重新订阅business store。

### PHASE B2 NON-TARGET DIFF / STOP

- B2新增dirty仅为`frontend-app/src/App.test.jsx`、`frontend-app/src/pages/chat/ChatPage.core.test.jsx`、`frontend-app/src/pages/chat/__tests__/chatPageTestSupport.js`、`frontend-app/src/app/appShellModel.test.js`与本执行记录；`frontend-app/src/app/shell/model/`四个untracked文件为Phase A/B1既有owned diff。
- B2没有修改任何production、新unit production/tests、依赖、guard、baseline或生成物；index empty。停止等待主代理Phase B3生产接线授权，不stage/commit、不进入Task 7、不push。

### PHASE B3 PRODUCTION INTEGRATION GREEN

`GREEN`（7个批准production完成唯一Shell truth接线与旧business truth删除；focused matrix、target lint、guards、LSP与truth边界通过，尚未stage/commit/Task 7/push）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 28 | 首轮B1 unit命令的前置code-size guard | 1 | Vitest未运行；仅2个参数数违规：`ChatPageRoute`与`ChatPage`新增第6个解构参数超过上限5。没有绕过guard，主代理批准后改为单一`props`形参并在函数体显式解构，prop transport语义不变 |
| 29 | 单跑frontend code-size guard | 0 | `files=359, frozen=0`，两项参数违规清零 |
| 30 | B1 strict schema/store units | 0 | 2 files / 46 tests passed；critical-skip、contract/store、code-size、typecheck contracts与RPC audit前置门禁均通过 |
| 31 | App exact 14首次production复跑 | 1 | 14个命中中11 passed / 3 failed：source regex被默认callback brace截断1；saved initial `380`经1024 viewport clamp后旧test仍期待viewport default `189` 1；旧close drag从恢复宽度出发未越过0阈值导致timeout 1。失败均为新语义的test表达，不是production exception |
| 32 | App test语义按批准设计对齐后复跑 | 0 | 14 passed / 181 skipped；source contract改锁函数体显式解构；恢复宽度通过`Math.min(rightPanelWidthSchema.initialValue, rightPanelMaxWidth(window.innerWidth, threadRailTargetWidth(window.innerWidth)))`表达persisted initial经viewport clamp，不写魔法默认；close pointer延长到1500以明确越过0阈值 |
| 33 | ChatPage Core + appShellModel | 0 | 2 files / 34 tests passed（Core 31、app shell model 3）；Shell writer被真实消费，business setter保持undefined，selector两个旧keys均删除 |
| 34 | theme storage fail-fast优先级单测 | 0 | 1 passed / 194 skipped；既有`fails fast when required browser storage is unavailable`仍首先得到theme storage错误，证明Shell factory没有在App顶层、QueryClient initializer或module load抢先运行 |
| 35 | 15个target production/test ESLint | 0 | 无error/warning |
| 36 | explicit frontend contract/store guard | 0 | 9类guard结果全部`0/0` |
| 37 | explicit frontend code-size guard | 0 | `files=359, frozen=0` |
| 38 | LSP locate / structure / inspect / xref / read / diagnostics | 0 | grep定位factory唯一production owner为`AppShell`；factory xref覆盖App、unit tests与chat test harness共14处；`useRuntimeSidePanelLayout` xref仅ChatPage import/call与hook声明/export4处；窄`useShellLayoutStore` xref仅ChatPage两个selector调用；精读storage/AppShell/route/ChatPage/hook/base/action/selector；15 code/test files diagnostics `No diagnostics found`、total 0。首次definition位置与hook xref返回0，收窄到精确call/selection后重试成功 |
| 39 | diff / truth / main边界 | 0 | `git diff --check`无输出，index empty；三个business truth文件对width/setter 0；production对`store.rightPanelWidth/setRightPanelWidth` 0，命中仅negative tests；`rightPanelOpen`仍App local state，`threadRailWidth`仍hook local state；diff不含`main.jsx`或`AppWindowFrame.jsx`；main两个既有SHA完整值不变 |

### PHASE B3 OWNERSHIP / BEHAVIOR EVIDENCE

- `requiredAppStoragePort`现在严格验证并映射`getItem/setItem/removeItem`到`get/set/remove`。`AppShell`先执行包含theme required storage的`useAppShellState`，再用lazy `useState`创建`createShellLayoutStore`；default storage label为`shell layout storage`，只有`undefined`走browser port，显式`null`直接进入factory端口校验且不fallback。整个初始化仍位于main既有`AppErrorBoundary`下，未修改main顺序。
- StrictMode下第一次initializer看到missing key并成功写`380`，第二次读取已有scalar，因此first-run storage set总数精确为1。read/first-write异常无catch，沿render抛给ErrorBoundary；App integration tests证明安全fallback、无chat layout/remove/fallback state与无private error泄漏。
- Shell StoreApi显式沿`AppShell -> AppWindow -> ActivePageContent -> ChatPageRoute -> ChatPage`传递；没有Provider/context或module singleton。`ChatPage`用两个窄selector分别订阅width与setter，再显式传给layout hooks；business `store`只保留diff sync等原职责。
- 打开面板时，saved width大于0优先恢复并经`rightPanelMaxWidth` clamp；saved width为0才使用`rightPanelDefaultWidth`并先持久化。viewport收窄时sync把clamped truth写回；不存在`resizedRef=false`无条件覆盖持久值。drag move仍只改DOM，finish/keyboard/toggle才写唯一Shell truth。
- 同一slice已删除`clientStoreUtils.js`的base width、`clientStorePageActions.js`的writer action与`APP_SHELL_STORE_KEYS`的width/setter；没有double write、compat re-export或另一个session view layer。`rightPanelOpen`与`threadRailWidth`未迁移。

### PHASE B3 NON-TARGET DIFF / STOP

- 当前owned diff共16个文件：本执行记录；7个批准production；4个批准test/support；Phase A/B1的2个Shell production与2个Shell unit tests。index empty。
- `main.jsx`、`AppWindowFrame.jsx`、依赖、guard、baseline、CSS、其他tests与生成物均未修改。main仍`main...origin/main [ahead 3]`且仅原计划文档dirty；whole-diff SHA `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`、plan SHA `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`不变。
- 按主代理停止点不运行full `npm test`/build，不stage/commit，不进入Task 7或push；等待独立复核与后续授权。

### PHASE B3.1 COMPLETE FIVE-FILE MATRIX CORRECTION

`GREEN`（主代理完整矩阵发现focused name filter漏项后，仅修正`App.test.jsx`两个旧语义contract；production保持不动，计划5 files最终275/275）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 40 | 主代理独立完整5-file矩阵 | 1 | 5 files / 275 tests：273 passed / 2 failed。失败均在App旧语义：keyboard test仍期待打开width `264`但新truth恢复`380`；viewport test仍期待首次`189`并在放宽时回到比例`380`。因此此前App 14/14只证明name-filter子集，不能冒充完整相关GREEN |
| 41 | 两个漏项test定向修正后复跑 | 0 | 2 passed / 193 skipped；keyboard覆盖restored initial、Arrow commit、Home=0 close、reopen default commit、End=max、hide/reopen保持；viewport覆盖1400容纳480.5无写、缩到1024按最终rail geometry clamp并exactly-once持久化、增到1980不回弹 |
| 42 | 计划完整5 files串行矩阵 | 0 | `shellLayoutSchema.test.js`、`useShellLayoutStore.test.js`、完整`App.test.jsx`、`ChatPage.core.test.jsx`、`appShellModel.test.js`全部执行；5 files / 275 tests passed，0 failed / 0 skipped。前置critical skip、silent async、contract/store、code-size、typecheck contracts与RPC audit全部通过 |
| 43 | App test targeted ESLint | 0 | 无error/warning |
| 44 | LSP locate / inspect / xref / read / diagnostics | 0 | grep定位两个test；definition从viewport geometry跳到`rightPanelMaxWidth`实现、schema usage跳到`rightPanelWidthSchema`；storage helper xref覆盖声明与9个App integration consumers；精读两个完整test；App test diagnostics `No diagnostics found`、total 0。首次inspect/xref使用漂移行号返回position out of range，按提示收窄到当前精确行列后重试成功 |
| 45 | diff / main边界 | 0 | `git diff --check`无输出，index empty；旧viewport test名与右栏`aria 264`断言均0（剩余264仅thread rail初始值）；B3.1未改任何production；main两个既有SHA完整值不变 |

### PHASE B3.1 BEHAVIOR EVIDENCE / STOP

- Keyboard test现在注入existing scalar `380`并直接观察storage。1400 viewport、rail keyboard commit为248时，右栏max由`rightPanelMaxWidth`推导，打开恢复`min(schema initial, max)`；Arrow后的ARIA数值必须同步持久化。Home写0并关闭；再次打开仅因saved=0使用`rightPanelDefaultWidth`并写入；End写geometry max；hide/reopen保持max且整个序列恰4次write。
- Viewport test改名为persisted clamp contract：1400 viewport能容纳480.5，打开不得写；缩到1024后，期望值由`rightPanelMaxWidth(window.innerWidth, threadRailTargetWidth(window.innerWidth))`计算，DOM与storage必须达到同一最终geometry max且exactly once write；放宽到1980后保持已提交clamp，write count仍1。实际结果未出现旧rail宽导致的过度clamp竞态。
- B3.1仅修改`frontend-app/src/App.test.jsx`与本执行记录；production、其他tests、Shell modules、main/AppWindowFrame、依赖、guard与生成物未改。main whole-diff SHA仍为`2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`，plan SHA仍为`6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`。
- 当前不stage/commit，不进入Task 7或push；等待主代理完整矩阵复核。

### PHASE B4 FULL-GATE FAILURE / B4.1 TEST-HARNESS CORRECTION

`GREEN_AT_TARGETED_SCOPE`（fresh full `npm test`暴露3个遗漏的直接`ChatPage`测试夹具；B4.1只扩展已批准test support与3个失败suite，生产代码保持B3.1不动。3-suite最终33/33，完整相关8-file矩阵308/308；按主代理停止点不重启full test、不stage/commit/Task 7/push）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 46 | fresh `npm test` B4首次full gate | 1 | 126 files中123 passed / 3 failed；1533 tests中1507 passed / 26 failed。失败严格集中于`ChatPage.preview.test.jsx` 8、`ChatPage.timeline.test.jsx` 15、`ChatPage.scroll.test.jsx` 3；统一根因为这些suite直接渲染production `ChatPage`却未传新必需`StoreApi`，Zustand `useStore`在`useShellLayoutStore.js:44`读取`undefined.subscribe`。门禁在test失败处停止，没有继续lint/build/commit，也未修改文件伪造full GREEN |
| 47 | B4.1 harness与三suite最小迁移 | 0 | support新增无额外DOM的`TestChatPage`，用React lazy state为每个mount创建独立`createShellLayoutStore` harness，显式`shellLayoutStore`仍可覆盖；既有Wrapper复用该组件且只拥有`rightPanelOpen`。preview 10、timeline 21、scroll 4个direct render/rerender全部保持同一`TestChatPage` component type；Wrapper既有用法不变，production没有新增undefined/default fallback |
| 48 | 3个原失败suite首次修复复跑 | 0 | critical-skip、silent-async、contract/store、code-size、contracts typecheck与RPC audit均PASS；3 files / 33 tests passed，0 failed |
| 49 | 完整相关8-file串行矩阵 | 0 | schema/store units、完整App、ChatPage core、app shell model以及preview/timeline/scroll全部执行；8 files / 308 tests passed，0 failed。前置guards、contracts typecheck与RPC audit全部PASS |
| 50 | 机械替换缩进清理后的3-suite复跑 | 0 | 仅恢复timeline 6处与scroll 1处原有缩进，不改语义；3 files / 33 tests passed，0 failed，前置全部门禁再次PASS |
| 51 | 四个B4.1文件target lint / LSP五类证据 | 0 | 主控独立target ESLint为exit 0；LSP grep/structure定位`TestChatPage`及全部usage，preview import definition跳到support `144-153`，import binding references覆盖support/export及三suite共41处，精读support与3个代表性rerender，4 files diagnostics `No diagnostics found`、total 0。首次从support声明做xref返回0，收窄到suite import binding后重试成功 |
| 52 | truth / diff / main只读边界 | 0 | `git diff --check`无输出，index empty；3 suites的direct production `ChatPage` import与JSX均0；B4.1只改support和3个失败suite及本记录，没有修改B3 production。main仍ahead3且仅原计划文档dirty，whole-diff SHA `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`、plan SHA `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`不变 |

### PHASE B4.1 OWNERSHIP / FAILURE CLASSIFICATION / STOP

- B4失败不是Shell production退化：此前计划5-file矩阵只覆盖App/Core/model与新units，遗漏的3个suite仍直接render `ChatPage`。`shellLayoutStore`是显式必需依赖，因此测试夹具必须跟随真实route接线传入StoreApi；禁止在production `ChatPage`或`useShellLayoutStore`里为undefined偷偷创建默认store。
- `TestChatPage`不创建wrapper节点，不改变query/scroll/layout DOM；lazy initializer保证同一次Testing Library rerender继续使用同一Shell store。每个独立mount默认拿到独立memory port，显式override仍支持core/writer场景。`TestChatPageWrapper`保留唯一额外button/div与open-state职责。
- 当前owned diff扩展为19个文件：本记录、7个B3 production、4个Shell module/unit、原4个B2/B3 test-support以及3个B4.1 suite；index empty。没有修改`main.jsx`、`AppWindowFrame.jsx`、依赖、guard、baseline、CSS或生成物。
- 由于fresh full gate仍保留上述26-failure历史证据且B4.1后未获授权重启full `npm test`，不得声称full suite GREEN，也不得进入lint/build/commit。当前只可声称定向3-suite 33/33与完整相关8-file 308/308 GREEN；到此立即STOP等待主代理下一检查点。

### PHASE B4.2 FINAL GATES / TASK 6 GREEN

`GREEN`（在保留B4首次26-failure与B4.1定向修复历史的基础上，fresh full test、lint、build、LSP、truth与main边界全部通过；提交边界锁定为19个owned paths，不进入Task 7、不push）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 53 | fresh `npm test` | 0 | critical-skip、silent async、contract/store、code-size、contracts typecheck与RPC audit全部PASS；126 files / 1533 tests passed，0 failed，Duration 202.59s。该次full覆盖B4首次失败的preview/timeline/scroll以及此前8-file 308/308相关矩阵 |
| 54 | fresh `npm run lint` | 0 | ESLint全量无error/warning |
| 55 | fresh `npm run build` | 0 | Vite 5547 modules，build与dist sync完成；随后`frontend-app/dist`与根`web-dist`的tracked/untracked差异均0 |
| 56 | final LSP locate / structure / inspect / xref / read / diagnostics | 0 | factory locate共16处，definition从App import跳到`useShellLayoutStore.js:17-41`，hover为`StoreApi`；factory xref覆盖App、units与chat harness共14处；窄hook locate仅定义、ChatPage import及两个selector call共4处；精读AppShell lazy owner、persist-before-set factory、ChatPage窄selector和saved-width sync；18个code/test文件diagnostics `No diagnostics found`、total 0 |
| 57 | final truth / diff / main边界 | 0 | worktree恰19个owned paths，index empty，`git diff --check`无输出；旧business三文件width/setter命中0，ChatPage/layout hook旧`store.rightPanelWidth/store.setRightPanelWidth`命中0，Shell production禁用API命中0；`rightPanelOpen`仍App local、`threadRailWidth`仍hook local；`main.jsx`、`AppWindowFrame.jsx`、dist/web-dist差异0；main仍ahead3且仅原计划文档dirty，whole-diff SHA `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`、plan SHA `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`不变 |

### PHASE B4.2 COMMIT BOUNDARY

- B4两次full历程均保留：首次full为3 suites / 26 tests失败并在test gate停止；B4.1补齐真实必需依赖后，B4.2 fresh full为126 files / 1533 tests全部通过。后一次GREEN不抹除前一次失败证据。
- 原子提交边界精确为19个owned paths：本执行记录；7个production接线/旧truth删除文件；4个Shell production/unit文件；App/Core/model/support 4个既有test/support；preview/timeline/scroll 3个B4.1 suite。stage前后均不得混入生成物或非目标文件。
- 最终动作只允许中文提交`feat(frontend): 统一壳层布局状态`。若首次commit hook刷新生成物并导致需要二次stage/commit，必须先报告精确diff并等待批准，不得自行重试；提交完成后要求worktree clean且main两个SHA不变。不进入Task 7、不push。

## TASK 7 A0 — LAYER / PORTAL DISCOVERY LOCK

### STATE

`A0_COMPLETE / A1_AWAITING_MAIN_APPROVAL`（Task 7只冻结既有discovery、RED文件边界与分片顺序；计划外显式portal override已由主控裁决并锁定最小扩面；未创建或修改production/test/guard/package，未stage/commit/push）

- Task 6已在隔离worktree提交，Task 7 BASE / HEAD为`6791cb72039db21517719db384d69a4c75c4ea48`，分支`codex/reasonix-frontend-absorption-20260711`，A0开始时clean。
- production CSS当前共有39条裸`z-index`规则，分布在计划锁定的11个CSS文件；Task 7必须一次性迁移全部规则，禁止按数值阈值、baseline或allowlist留下例外。
- `FocusTrapDialog`当前有7个production import、16个production实例；既有LSP证据在收窄到import binding后得到43个references。焦点、ARIA、Escape、overlay click与焦点恢复仍由`FocusTrapDialog`负责，DOM host职责不得复制到调用方。
- 本地`react-aria` / `react-aria-components`依赖支持`UNSAFE_PortalProvider`；当前未显式提供全局container的`ModalOverlay`/`Popover`默认portal到`document.body`。计划组合锁定为App层provider统一React Aria overlays到唯一`#overlay-root`，自有`FocusTrapDialog`则委托`OverlayPortal`的`createPortal`。
- 主工作区继续只读，但其现状已被外部并发工作改变：97个status entries，cached diff SHA-256为`d5587d263f0e57270e5a792f2b462d0329b2f533133203adfcb49d5889f90bc6`；本任务基线的unstaged whole-diff SHA-256仍为`2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`，计划文件SHA-256仍为`6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`。外部staged内容不属于本任务，禁止读取后吸收、unstage、改写或提交。

### DAG / STOP GATES

```text
Task 6 commit 6791cb72039db21517719db384d69a4c75c4ea48
  -> Task 7 A0 discovery lock (this section)
  -> [RESOLVED: ProjectSelector explicit portal container enters minimal migration]
  -> A1 guard/style RED
  -> main review / approval
  -> A2 portal/focus/theme RED
  -> main review / approval
  -> B1 LayerTokens + z-index guard GREEN
  -> main review / approval
  -> B2 unique host + PortalProvider + OverlayPortal + theme projection GREEN
  -> focused matrix / full gates / atomic commit
  -X-> Task 8 / push until every preceding gate is independently approved
```

| Result gate | 当前结果 | 继续条件 |
|---|---|---|
| 39条规则 / 11文件完整枚举 | `PASS` | A1 guard必须证明全量扫描且无baseline/allowlist |
| 9个token按语义context映射 | `PASS_AT_DISCOVERY` | B1逐selector迁移；不得从旧数值反推owner |
| 唯一global host与theme owner | `PASS_AT_DISCOVERY` | `index.html`唯一sibling host；`App.jsx`仍是theme唯一writer |
| React Aria与自有dialog portal策略 | `PASS / SCOPE_EXPANDED` | 删除`ProjectSelector`局部container ownership并以test锁定统一host |
| theme selector迁移清单 | `PASS / CORRECTED_IN_A1.2` | 下方6条、两个CSS文件必须全部进入RED与GREEN |
| test host策略 | `PASS_WITH_AUTHORIZATION_REQUIRED` | RED后由主控批准`test-setup.js`最小扩面，当前不得编辑 |
| LSP证据 | `PARTIAL / BLOCKER_RECORDED` | JS import binding已有43 refs；CSS/HTML literal无法用xref证明，保留精确文本/AST guard证据 |
| A1执行授权 | `READY / AWAITING_MAIN_COMMAND` | 主控逐片下达A1.1；`test-setup.js`留到RED后单独批准 |

### 39-RULE SEMANTIC TOKEN MAP

数值只允许在`frontend-app/src/shared/styles/LayerTokens.css`定义。下表按stacking context语义锁定分类，不记录或沿用旧数值；B1必须将39条现有规则逐条归类并由guard证明没有遗漏、未知token、重复定义或未使用token。

| Token | Scope | 语义context / owner |
|---|---|---|
| `--z-local-behind` | local | 组件自身stacking context内、位于内容之后的装饰/背景层；owner仍是最近CSS selector |
| `--z-local-raised` | local | 同一组件/页面context内需要高于普通兄弟元素的内容、控件或状态层 |
| `--z-local-handle` | local | resize、drag或splitter hit target，只在所属panel/context内竞争 |
| `--z-local-sticky` | local | scroll container内的sticky header/footer/fade，不得越过page stacking context |
| `--z-shell-control` | shell-local | App shell/workbench内跨局部内容的rail、toggle或shell control，仍低于全局overlay host |
| `--z-overlay-popover` | global overlay | 统一host内的React Aria menu/popover/tooltip类浮层 |
| `--z-overlay-dialog` | global overlay | 统一host内的modal overlay与dialog内容；`FocusTrapDialog`只保留交互语义 |
| `--z-overlay-lightbox` | global overlay | 统一host内的image/code preview等沉浸式查看层 |
| `--z-overlay-critical` | global overlay | 统一host内必须高于其他overlay的终止性/关键阻断层，不得被普通page selector消费 |

39条规则的文件owner精确锁定为：`AppChrome.css`、`AppShell.css`、`AppShellSidebarThreadActions.css`、`AppShellWorkbench.css`、`ChatMessages.css`、`ChatPage.css`、`ChatPageWorkbench.css`、`ComposerDock.css`、`RuntimePanel.css`、`MemoryPage.css`、`SkillsPage.css`。局部token继续由上述最近selector拥有；全局overlay token只能在`#overlay-root`承载的selector中使用。

### GLOBAL / LOCAL OWNERSHIP AND PORTAL CONTRACT

- `frontend-app/index.html`当前body只有`<div id="root"></div>`后接main script；GREEN必须新增且只新增一个与`#root`同级的`#overlay-root`。host、`html`、`body`均不得通过`transform`、`opacity`、`filter`、`perspective`、`contain`或`isolation`建立意外stacking context。
- `App.jsx`继续通过现有`useColorTheme`拥有唯一theme truth。它必须fail-fast取得恰好一个`#overlay-root`，向App层`UNSAFE_PortalProvider`提供该node，并从同一theme值投影`data-theme`；禁止增加第二个theme store、setter或persistence。missing/duplicate host均阻断；unmount只清理自己写入的projection。
- React Aria `ModalOverlay`/`Popover`统一消费App provider；`OverlayPortal.jsx`只负责验证唯一host并以`createPortal`挂载自有overlay，host缺失/重复立即抛错，禁止回退`document.body`或原地render。
- `FocusTrapDialog.jsx`继续拥有focus/ARIA/Escape/backdrop click/focus restore，DOM挂载唯一委托`OverlayPortal`；不得自行创建host或保留非portal分支。
- `ProjectSelector.jsx:11`当前局部`portalContainer` state初始为`null`，trigger callback在`:16-17`正常挂载时必从`.sa-window`取得`themeShell`，`:71`再通过`UNSTABLE_portalContainer={portalContainer || undefined}`覆盖App provider；它不是恒空分支。主控已批准将`ProjectSelector.jsx`与`ProjectSelector.test.jsx`纳入最小迁移：删除container state/callback/显式prop，使Popover继承统一provider；保留`triggerRef`、close-focus与disabled行为，并用test证明Popover挂到`#overlay-root`而不是`.sa-window`。该popover仍只能消费local/popover语义token，不得建立第二套global顺序。

### THEME SELECTOR MIGRATION LOCK

portal后下列6条selector失去`.sa-window`祖先，必须全部改为由唯一overlay host/theme projection驱动，并在`styles.test.js`中逐条枚举；禁止复制可独立漂移的light颜色值。前三条来自`ThemePolish.css`，后三条来自`PagePrimitivesPolish.css`：

1. `.sa-window[data-theme="light"] .runtime-stat-tooltip`
2. `.sa-window[data-theme="light"] .warning-log-popover`
3. `.sa-window[data-theme="light"] .warning-log-popover code`
4. `.sa-window[data-theme="light"] .skills-editor-modal button`
5. `.sa-window[data-theme="light"] .skills-editor-modal button:hover:not(:disabled)`
6. `.sa-window[data-theme="light"] .skills-editor-modal button.ghost`

### TEST HOST / SETUP DECISION

- `frontend-app/src/test-setup.js`当前只配置Testing Library与memory `localStorage`，没有`#overlay-root`或其他DOM host初始化；Vitest也不会加载`index.html`。
- `FocusTrapDialog.test.jsx`是当前direct custom-overlay test；其他App/page/Markdown集成仍依赖默认body/测试DOM。GREEN若将所有overlay改为required host而不补测试环境，会让非目标suite因基础设施缺失失败，无法区分真实行为退化。
- 因此把`frontend-app/src/test-setup.js`标记为“计划外但GREEN很可能必需的最小test-environment expansion”：统一创建与`index.html`同构的唯一`#overlay-root`；missing/duplicate用例在各自suite显式移除/制造重复并在结束后恢复。该扩面必须先由RED证据证明并经主控批准；A0/A1不得预先修改setup来掩盖预期失败。

### EXACT RED FILES / SERIAL SHARDS

所有RED只写测试，不创建production module，不改CSS、HTML、App runtime、package scripts或test setup；每片失败证据与dirty boundary经主控复核后才进入下一片。

| 分片 | Create | Modify | 必须先看到的失败 |
|---|---|---|---|
| A1.1 guard contract RED | `frontend-app/scripts/frontend-z-index-token-guard.test.mjs` | 无 | 裸负数/0/低值均失败；已知token通过；未知、重复、未使用、overlay顺序错误失败；local/global fixture分开 |
| A1.2 style/index RED | 无 | `frontend-app/src/styles.test.js` | `LayerTokens.css`不存在/未在其他production CSS前导入；39条裸值仍存在；`#overlay-root`缺失或非root sibling；stacking-context禁用属性与6条theme selector漏迁移均失败 |
| A2.1 portal/focus RED | `frontend-app/src/shared/ui/OverlayPortal.test.jsx` | `frontend-app/src/shared/ui/FocusTrapDialog.test.jsx`、`frontend-app/src/pages/chat/components/ProjectSelector.test.jsx` | 内容未挂到唯一host、missing/duplicate未fail-fast、unmount未清理；FocusTrapDialog portal后focus/Tab/Escape/click/restore契约尚未满足；ProjectSelector Popover仍挂到`.sa-window`且局部container ownership未删除 |
| A2.2 App provider/theme RED | 无 | `frontend-app/src/App.test.jsx` | App未向React Aria提供唯一host、light/dark未同步shell与host、独立写入/卸载/重挂载会留下stale projection |

RED预期dirty边界严格为6个文件：新建2个test，修改4个既有test。`frontend-app/src/test-setup.js`只保留为GREEN候选，不得混入RED；`package.json`与guard production也不得在RED提前接线。

### GREEN FILE BOUNDARY RESERVED BY PLAN（NOT AUTHORIZED IN A0）

- Create：`frontend-app/src/shared/styles/LayerTokens.css`、`frontend-app/src/shared/ui/OverlayPortal.jsx`、`frontend-app/scripts/frontend-z-index-token-guard.mjs`。
- Modify：`frontend-app/index.html`、`frontend-app/src/App.jsx`、`frontend-app/src/main.jsx`、`frontend-app/src/shared/ui/FocusTrapDialog.jsx`、`frontend-app/src/shared/styles/ThemePolish.css`、`frontend-app/src/shared/styles/PagePrimitivesPolish.css`、`frontend-app/src/pages/chat/components/ProjectSelector.jsx`、`frontend-app/package.json`以及上方11个z-index CSS文件。
- Resolved scope expansion：`ProjectSelector.jsx/.test.jsx`已由主控批准纳入统一provider迁移；不再保留局部portal container。
- Pending GREEN-only scope decision：`frontend-app/src/test-setup.js`（test environment最小扩面）。RED证明前不是owned edit path。

### LSP EVIDENCE GAP / STOP

- 既有JS证据通过收窄到`FocusTrapDialog` import binding获得43个references，支持7个production imports / 16 instances的影响面结论。
- LSP `xref(references)`不为CSS声明值、selector或HTML id literal建立可复查的symbol edge；即使把`work_dir`收窄到本worktree并把目标收窄到单个CSS/HTML文件，仍不能用definition/xref证明39条`z-index`或`#overlay-root`结构。因此这两类证据必须由精确文本枚举、HTML/CSS AST style tests与全量guard共同闭环，不能把缺少LSP xref写成PASS。
- A0到此停止：只允许本执行记录dirty；不进入A1、不创建测试、不改production/package/setup、不卡位stage/commit、不进入Task 8、不push。等待主控逐片下达A1继续指令；`test-setup.js`仍须在RED证据后单独批准。

## TASK 7 A1.1 — Z-INDEX GUARD CONTRACT RED

### STATE

`RED`（只创建`frontend-z-index-token-guard.test.mjs`并更新本执行记录；production guard module不存在，单文件Vitest唯一因静态import missing而在collection阶段失败；未进入A1.2/GREEN）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 1 | 既有Node guard fixture与LSP定位/精读 | 0 | 既有scripts tests使用Vitest静态import production pure exports并直接喂内存source/Map fixtures；A1.1沿用该模式，不shell真实仓库、不创建临时baseline |
| 2 | create `scripts/frontend-z-index-token-guard.test.mjs` | 0 | 176行test-only；静态import`validateZIndexContract`，锁定纯函数输入`{ tokenSource, cssSources }`与稳定coded violation objects |
| 3 | `node --check scripts/frontend-z-index-token-guard.test.mjs` | 0 | test module语法有效 |
| 4 | `npx --no-install eslint scripts/frontend-z-index-token-guard.test.mjs` | 0 | repo-native target ESLint无error/warning；无需修改package或全局lint配置 |
| 5 | `npx --no-install vitest run scripts/frontend-z-index-token-guard.test.mjs --no-file-parallelism --maxWorkers=1` | 1（expected RED） | 1 failed suite / 0 tests；唯一错误为line 2无法resolve`./frontend-z-index-token-guard.mjs`，production module不存在；无fixture parse、assertion或环境噪声 |
| 6 | LSP structure / inspect / xref / read / diagnostics | 0 | structure完整识别19个展开后contract cases及helpers；import binding definition只能回到test import自身，xref仅import与`validate`helper call两处，符合target module尚不存在；test diagnostics `No diagnostics found`、total 0 |
| 7 | worktree diff-check / status | 0 | `git diff --check`无输出；owned dirty恰为本执行记录与新guard test，index empty；没有guard production、LayerTokens、CSS、package、其他tests或setup diff |
| 8 | main只读snapshot | 0 | main当前1个status entry且cached diff empty（SHA-256 `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`）；unstaged whole-diff仍`2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`，计划SHA仍`6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`。A0观察到的外部97-entry staged状态已由外部自行撤离，本任务未触碰main |

### CONTRACT LOCK

- exact token set固定为9个：5个local/shell token与`popover < dialog < lightbox < critical`4个global overlay token；所有定义必须唯一且只存在于注入的LayerTokens source。
- fixtures锁定裸`z-index`的`-1`、`0`、`1`、`9999`全部产生`z-index-bare-number`，不存在阈值豁免；只有exact known`var(--z-*)`通过，unknown和fallback forms分别产生稳定coded violations。
- duplicate definition、production CSS外部定义、未使用token均失败；global顺序的重复值、逆序值和非数值均产生`overlay-order-invalid`。
- global token selector必须显式位于`#overlay-root`下；普通page selector消费global token失败。local token允许普通selector，`ProjectSelector`类local popover明确不被强迫使用global token。
- 同一CSS fixture同时包含global/local selectors；comment-only裸值不误报，带注释/换行的active裸负数仍必须命中。`baseline`、`allowlist`、`threshold`输入一律产生`policy-bypass-option`，禁止后续CLI悄悄引入ratchet/阈值语义。

### STOP

- 当前RED是预期功能缺失而不是测试语法错误：production module存在并导出`validateZIndexContract`后，19个contract cases才会实际执行；A1.1没有为获得更深失败而创建stub或临时production。
- 到此立即停止：不进入A1.2，不创建guard production/LayerTokens，不改CSS/package/其他tests/setup，不stage/commit，不进入Task 8，不push。等待主控复核并下达下一分片。

## TASK 7 A1.2 — STYLE / INDEX CONTRACT RED

### STATE

`RED`（只修改`styles.test.js`与本执行记录；7个新contract cases完整collect，精确暴露当前LayerTokens/import、39条numeric z-index、overlay host与6条theme selector缺口；未改任何production）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 9 | `styles.test.js` source/parser最小扩展 | 0 | 新增optional LayerTokens source slot，missing只返回空source供单一断言失败，不在module top-level抛ENOENT；继续复用既有PostCSS root/source contract，未复制A1.1 guard parser |
| 10 | `node --check src/styles.test.js` | 0 | test module语法有效 |
| 11 | `npx --no-install eslint src/styles.test.js` | 0 | target ESLint无error/warning |
| 12 | exact new describe Vitest | 1（expected RED） | 84 tests中5 failed / 2 passed / 77 skipped。五类失败精确为LayerTokens source空、main import缺失、39/39 active z-index仍为numeric、真实index缺`#overlay-root`、6条旧theme selector全保留且6条host selector全缺失 |
| 13 | full `src/styles.test.js` Vitest | 1（expected RED） | 84 tests中6 failed / 78 passed；除上述5类外，唯一额外失败是既有cascade list已预先加入LayerTokens而main尚未import。fixture missing/duplicate/nested/misordered host分类与host stacking-context负面检查2/2通过；其余既有style tests无新增失败 |
| 14 | LSP grep / structure / inspect / xref / read / diagnostics | 0 | grep定位唯一new describe；structure识别helpers/tests；调用点definition跳到`activeZIndexDeclarations@137-152`，xref严格为声明与contract call两处；精读7 cases；`styles.test.js` diagnostics `No diagnostics found`、total 0。首次definition使用漂移列号返回0，收窄到current call `285:27`后重试成功 |
| 15 | final diagnostics / diff-check / main只读snapshot | 0 | 三个owned文件diagnostics total 0；`git diff --check`无输出，index empty且status恰为本记录、styles test与A1.1 guard test。main被外部并发改为10个status entries，cached仍empty SHA `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`，unstaged whole-diff变为`7eab22f74996b3953b0e92c1a5e28c75838a4e04d420cd81b0e6ff352dc226b2`，计划SHA仍`6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`；本任务未读取或触碰外部diff |

### RED CONTRACT EVIDENCE

- LayerTokens缺失被隔离为可断言空source；同一suite其余83 tests仍collect。`cssFiles`已把`src/shared/styles/LayerTokens.css`锁为第一项，因此既有cascade alignment与新exact-once/first-import contract都将在GREEN由同一个`main.jsx` import满足。
- PostCSS active declaration遍历确认生产事实仍精确为39条、11文件，且invalid列表恰39条numeric值；final contract只接受9个exact known`var(--z-*)`，unknown/fallback同样进入invalid，不允许注释或inactive text影响active count。
- test-local HTML semantic helper证明valid sibling/order fixture通过，missing、duplicate、nested与script-before-host fixtures分别给出稳定分类；真实`index.html`当前只得到`overlay-root-count`，没有被LayerTokens缺失mask。
- `html`、`body`、`#overlay-root`六种禁用stacking properties当前扫描结果为空；该通过只证明没有意外context，不替代真实host缺失RED。
- theme迁移纠正为两个CSS文件共6条：`ThemePolish.css`的runtime tooltip / warning popover / code三条，以及`PagePrimitivesPolish.css`的Skills modal button / hover / ghost三条。RED一次输出remaining-old 6与missing-host 6，禁止漏迁一半。

### STOP

- 当前owned dirty恰为本执行记录、A1.1新guard test与`styles.test.js`；A1.2没有创建production guard/LayerTokens，没有修改HTML/main/CSS/package/setup或其他tests。
- 到此立即停止：不进入A2或GREEN，不stage/commit，不进入Task 8，不push。等待主控复核并下达下一分片。

## TASK 7 A2.1 — PORTAL / FOCUS TEST-ONLY RED

### STATE

`RED`（只创建`OverlayPortal.test.jsx`，修改`FocusTrapDialog.test.jsx`、`ProjectSelector.test.jsx`与本执行记录；OverlayPortal production module缺失，两个既有组件只在新portal ownership断言失败，原交互契约保持GREEN）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 16 | LSP locate / structure / read existing component/tests | 0 | 精读`FocusTrapDialog` focus/Tab/Escape/backdrop/restore实现与ProjectSelector local `portalContainer` owner；tests保持既有React Aria交互，不引入fake timers |
| 17 | A2.1三文件test-only变更 | 0 | 每个suite显式创建并清理唯一`#overlay-root`；Focus suite另建caller container，Project suite由本地`UNSAFE_PortalProvider getContainer={() => host}`提供统一container；未改global setup |
| 18 | 首次三suite串行运行 | 1 | OverlayPortal 0-test missing-module RED；Focus 1 portal placement失败 / 5 interaction通过；ProjectSelector因Vite下`import.meta.url`非`file:`而0-test collection失败。该URL错误不是目标RED，停止并只修test harness |
| 19 | ProjectSelector raw-source读取纠正 | 0 | 仅将test读取改为repo既有`cwd()+path`模式；没有改production或断言语义 |
| 20 | 三个target tests ESLint | 0 | 无error/warning |
| 21 | 最终三suite串行Vitest | 1（expected RED） | 3 files failed；OverlayPortal suite 0 tests且唯一错误为`./OverlayPortal.jsx`无法resolve；Focus 6中1 failed / 5 passed；ProjectSelector 6中2 failed / 4 passed。已collect的12 tests合计3 failed / 9 passed，无act/timer/host-cleanup噪声 |
| 22 | LSP grep / structure / inspect / xref / diagnostics | 0 | grep定位三suite 9个host契约点；structure识别Focus 6 cases与Project 6 cases；Focus JSX definition跳到production`FocusTrapDialog@61-147`；PortalProvider xref覆盖local d.ts与test wrapper；三test diagnostics `No diagnostics found`、total 0。首次inspect使用空白漂移行返回position-out-of-range，收窄到`25:6`后成功 |
| 23 | diff-check / status / main只读snapshot | 0 | `git diff --check`无输出，index empty；累计owned dirty恰6文件：本记录、A1.1 guard test、A1.2 styles test及本阶段3 tests。main被外部并发改为1668个status entries，cached SHA `3fc51dcbf6b52220235f0188c9aee07a9a6c6076f27d513b2dbe48fc3d61e93b`，unstaged empty SHA `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`，main计划文件SHA变为`02e9d43c3673c6a48e09aaad47113b406b219cfcdcc22579a824d9026d66f4f8`；本任务未读取、改写、unstage或吸收这些外部变化 |

### PORTAL / FOCUS CONTRACT EVIDENCE

- `OverlayPortal.test.jsx`用caller与唯一host锁定children只存在于host、unmount清理；missing与duplicate id必须同步抛含`overlay-root`错误，且body/caller都不能出现fallback内容。静态import使当前production module缺失成为唯一collection RED，不创建stub绕过。
- `FocusTrapDialog.test.jsx`每test自建caller与host。现有inline实现只让“host contains dialog / caller excludes dialog”失败；Escape、默认首焦点、`initialFocusSelector`、Tab最后到第一、Shift+Tab第一到最后、enabled backdrop close、`closeDisabled`阻断以及unmount恢复原active element共5 tests全部通过。
- `ProjectSelector.test.jsx`保留disabled、open-disable-recovery、select/remove/add、Escape与focus restore 4项既有行为GREEN。新增raw-source contract精确命中`UNSTABLE_portalContainer`和local`portalContainer`仍存在；统一provider已提供`#overlay-root`，但当前explicit override仍把Popover挂在`.sa-window`，因此host ownership test按预期失败。
- 三suite均在`afterEach`先cleanup React tree再移除自己的host/caller；没有修改`test-setup.js`，也没有让一个test的host泄漏给下一个test。

### STOP

- 当前RED将缺口隔离为三个production动作：创建`OverlayPortal.jsx`、让`FocusTrapDialog`委托它、删除ProjectSelector local portal override。App provider、index host与global setup仍不在本阶段授权范围。
- 到此立即停止：不进入A2.2或GREEN，不创建production，不改App/index/CSS/package/setup/guard production，不stage/commit，不进入Task 8，不push。等待主控复核。

## TASK 7 A2.2 — APP PROVIDER / THEME TEST-ONLY RED

### STATE

`RED`（只修改`App.test.jsx`与本执行记录；App全套199 tests精确为5 target failures / 194 existing passes，required host test infrastructure没有制造其他回归）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 24 | LSP定位App source/theme/boundary与现有tests | 0 | 精读`useColorTheme`唯一storage owner、AppShell/AppWindow render、既有theme toggle与`AppErrorBoundary` containment模式；A2.1 direct provider已足够，不扩真实App ProjectSelector fixture |
| 25 | App test-local host lifecycle与4个contract areas | 0 | 文件级beforeEach创建唯一`#overlay-root`；afterEach显式cleanup React后删除所有test hosts。新增source/provider、projection lifecycle、missing/duplicate it.each；既有theme test扩展host同步/tamper，不改global setup |
| 26 | `npx --no-install eslint src/App.test.jsx` | 0 | target ESLint无error/warning |
| 27 | exact new/theme filter Vitest | 1（expected RED） | 主控删除“reporter不得含安全结构标识”的过度约束后fresh复跑；App共199 tests，5 target failed / 194 skipped。失败严格为provider/source缺失、projection lifecycle缺失、missing未contain、duplicate未contain、theme host初始同步缺失 |
| 28 | full `src/App.test.jsx` Vitest | 1（expected RED） | 纠偏后fresh full仍为199 tests中5 failed / 194 passed，Duration 48.63s；其余既有App行为全部通过，证明test-local required host没有造成基础设施噪声 |
| 29 | LSP grep / inspect / xref / read / diagnostics | 0 | grep定位唯一source contract与18个`appOverlayHost` lifecycle点；inspect从projection assertion回到test host declaration，xref覆盖setup、lifecycle、boundary与theme test；精读完整contract block；App test diagnostics `No diagnostics found`、total 0 |
| 30 | diff-check / status / main只读snapshot | 0 | `git diff --check`无输出，index empty；累计owned dirty恰7文件：本记录、A1.1 guard test、A1.2 styles test、A2.1三tests、App test。main外部并发此刻为10个status entries，cached empty SHA `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`，unstaged SHA `3f5cf9232fb3b8c83997ab763e1d12433ab9c5212d2171e3bf6a741778b18427`，计划SHA恢复为`6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`；本任务仍未触碰main |

### APP PROVIDER / THEME CONTRACT EVIDENCE

- source contract要求App直接从`react-aria`消费`UNSAFE_PortalProvider`，由`requiredOverlayRoot()`对`querySelectorAll('#overlay-root')`做唯一性验证，并把required node交给provider `getContainer`；明确禁止`document.body` fallback和overlay theme store/storage/persistence。
- 既有`useColorTheme`仍是唯一owner。扩展theme test锁初始light、切dark、tamper host、再切light时shell与host同步，且backend preference call count保持0；当前只在host初始projection处失败。
- lifecycle test锁普通unmount删除自己写入的`data-theme`，remount覆盖stale值；mounted期间被外部改为`external`后unmount不得删除，随后remount再次从唯一owner覆盖stale。当前App没有projection，首个light断言即RED。
- missing/duplicate两种host错误都必须在App mount fail-fast，由既有`AppErrorBoundary`显示安全文案、隐藏shell，fallback DOM不得显示`overlay-root`原始标识，reporter则必须exactly once且允许保留安全结构诊断信息。test内spy只抑制预期React console输出并在finally恢复；当前App未验证host，因此两例都按预期只因安全fallback未出现而失败。
- 没有增加真实App内ProjectSelector overlay用例：A2.1已用本地`UNSAFE_PortalProvider`直接证明provider container与explicit override冲突；在A2.2复制整条App/sidebar/menu fixture只会扩大测试噪声，不增加独立契约证据。

### STOP

- 当前A2 RED阶段累计只有7个owned test/doc文件；仍没有任何production、index、CSS、main entry、package、setup或guard production变更。
- 到此立即停止：不进入GREEN，不stage/commit，不进入Task 8，不push。等待主控复核并决定GREEN分片。

## TASK 7 B1.1 — Z-INDEX GUARD PURE MODULE GREEN

### STATE

`GREEN_AT_PURE_SCOPE`（只创建`frontend-z-index-token-guard.mjs`并更新本执行记录；A1.1的19个内存契约全部通过，真实仓库CLI仍因计划中的`LayerTokens.css`尚未创建而严格失败；未进入LayerTokens/CSS/HTML/App/portal迁移）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 31 | create `scripts/frontend-z-index-token-guard.mjs` | 0 | 同文件导出`validateZIndexContract({ tokenSource, cssSources, ...policy })`与可复用`runZIndexGuard`；PostCSS解析active declarations，正常契约失败返回稳定coded violation objects，parse/输入/I-O异常保持coded或原生fail-fast |
| 32 | `node --check scripts/frontend-z-index-token-guard.mjs` | 0 | production guard module语法有效 |
| 33 | target guard module + contract test ESLint | 0 | `npx --no-install eslint scripts/frontend-z-index-token-guard.mjs scripts/frontend-z-index-token-guard.test.mjs`无error/warning |
| 34 | A1.1单suite Vitest | 0 | CLI导出按主控纠正为`runZIndexGuard`后fresh复跑：1 file / 19 tests passed，Duration 992ms；exact token set、定义唯一性/位置/使用、严格global顺序、裸值/unknown/fallback、overlay-root ancestor和policy bypass全部GREEN |
| 35 | `node scripts/frontend-z-index-token-guard.mjs` | 1（expected migration RED） | CLI直接读取严格路径`src/shared/styles/LayerTokens.css`并扫描`src/**/*.css`；当前唯一错误为该计划production文件ENOENT，没有baseline/allowlist/threshold或软降级 |
| 36 | source truth grep / direct execution shape | 0 | production module对`baseline|allowlist|threshold|>=8`搜索0 hits；导出pure validator与`runZIndexGuard`，用`realpathSync(process.argv[1])`和module URL robust判断直接执行 |
| 37 | LSP grep / structure / inspect / xref / read / diagnostics | 0 | grep与document symbols定位validator/CLI runner；test import definition跳到production `validateZIndexContract@159-175`，xref覆盖声明、CLI runner与test import/helper共4处；精读pure validator与strict I-O runner；module/test diagnostics `No diagnostics found`、total 0 |
| 38 | diff-check / status / main只读snapshot | 0 | `git diff --check`无输出，index empty；累计owned dirty恰8文件：本记录、A1.1 guard test、A1.2 styles test、A2.1三tests、App test与本阶段guard module。main仍为外部10个status entries，cached empty SHA `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`，unstaged SHA `3f5cf9232fb3b8c83997ab763e1d12433ab9c5212d2171e3bf6a741778b18427`，计划SHA `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`；本任务未触碰main |

### GUARD IMPLEMENTATION EVIDENCE

- token contract由同一固定9项表驱动；global token额外要求strict numeric且`popover < dialog < lightbox < critical`，重复、逆序与非数值都返回`overlay-order-invalid`，没有阈值或ratchet语义。
- production CSS只接受exact known `var(--z-*)`；所有active裸数字（含负数与0）统一返回`z-index-bare-number`，unknown token、fallback form和其他表达式返回独立稳定code。comment-only文本由PostCSS active declaration遍历自然排除。
- global token的每个selector分支都必须具备显式`#overlay-root` ancestor；selector splitter保留括号/属性中的逗号语义。任何额外policy key统一返回`policy-bypass-option`，调用方不能通过新参数偷偷绕过规则。
- CLI从调用时`appRoot`严格读取唯一token source，并递归收集`src`下全部CSS；token source自身从production inputs排除。缺文件、目录或读取失败立即失败，不创建默认token、allowlist、baseline或空扫描fallback。

### STOP

- B1.1只授权guard pure module；真实CLI RED是后续LayerTokens/CSS迁移尚未发生的预期边界，不把它误报为整体GREEN。
- 到此立即停止：不进入B1.2，不创建`LayerTokens.css`，不改CSS/index/main/App/portal/package/setup，不stage/commit，不进入Task 8，不push。等待主控复核并下达下一分片。

## TASK 7 B1.2 — LAYER TOKENS / CSS / STYLE-INDEX GREEN

### STATE

`GREEN`（创建唯一LayerTokens source，39条/11文件全部按真实owner迁移，四个global overlay rule显式限定`#overlay-root`，main/index/theme与旧style assertions同步；19/19、CLI和84/84均fresh通过。未进入App/portal production）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 39 | create `LayerTokens.css`; migrate main/index/theme/CSS/style assertions | 0 | 9个严格整数只在LayerTokens定义；main第一项CSS import；index唯一host与root同级且在script前；11个既有CSS保持39条active z-index；6条light overlay selector移到host |
| 40 | edit checkpoint token inventory | 0 | 主控语义纠偏前计数为behind2/handle2/raised12/sticky10/shell9/global各1；四global host rule均已显式存在，numeric 0 |
| 41 | 主控page owner纠偏 | 0 | Memory create menu、Composer model dropdown、chat toast/actions menu与scroll-bottom均改为`local-sticky`；最终计数behind2、handle2、raised11、sticky15、shell5、四global各1，总计39 |
| 42 | 首轮A1.1 guard contract / CLI | 0 | 1 file / 19 tests passed（844ms）；真实CLI输出`frontend z-index token guard passed`，不再是LayerTokens ENOENT |
| 43 | 首轮full styles | 1（fixture migration gap） | 84中83 passed / 1 failed；唯一失败是旧theme test把新LayerTokens的`:root`计作第二个theme root。生产层级、host、theme迁移和其余83 tests均GREEN |
| 44 | migrate old theme-root selector assertion | 0 | 只把旧断言收窄为包含`--bg`的theme root exact-one；不放宽LayerTokens、39/11、token或host contract；随后styles 84/84通过 |
| 45 | owner纠偏后fresh三组 | 0 | guard contract 19/19（528ms）；CLI PASS；styles 84/84（994ms）。修正后的page/local语义没有引入回归 |
| 46 | target lint / syntax / truth queries | 0 | main/styles/guard target ESLint 0，guard/styles node-check 0；numeric z-index 0、unknown/fallback refs 0；9个定义只在LayerTokens；active shape仍39条/11文件 |
| 47 | LSP locate / inspect / xref / read / diagnostics | 0 with CSS xref gap | grep定位global token定义与消费；structure识别LayerTokens唯一`:root`；main CSS import definition跳到LayerTokens。精读9值、global host rule、first import与index sibling/order；17个B1.2文件batch diagnostics `No diagnostics found`、total 0。CSS LS对custom property use两次收窄列号后仍不给definition，xref仅回当前use/definition自身，因此39条引用关系由PostCSS guard/style tests与truth query闭环，未将CSS xref缺口写成PASS |
| 48 | diff-check / status / main只读snapshot | 0 | `git diff --check`无输出，index empty；累计owned dirty 24 entries，全部属于既有RED/doc/guard与本阶段批准路径。main外部状态变为1个staged entry，cached SHA `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`、unstaged empty SHA `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`，计划SHA `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`；本任务未触碰main |

### FINAL TOKEN / OWNER EVIDENCE

- local顺序固定为`behind(1) < raised(2) < handle(3) < sticky(4) < shell(5)`；global顺序固定为`popover(100) < dialog(200) < lightbox(300) < critical(400)`。数值没有复制到consumer，也没有fallback。
- `local-behind`只用于drop-active低层装饰与mobile scrim；两个resizer只用`local-handle`；page-owned menu/dropdown/toast/action/scroll control使用`local-sticky`，没有冒充App shell owner。
- `shell-control`只保留给titlebar/top-command project dropdown、thread action tooltip和mobile sidebar/workbench expand/toggle。modal/image panel内部使用`local-raised`。
- 四个global mapping严格为warning log popover→popover、runtime stat tooltip→dialog、image lightbox→lightbox、modal overlay→critical；基础class rule不再含z-index，独立`#overlay-root .class` rule持有唯一global token。
- token使用最终精确计数：behind 2、raised 11、handle 2、sticky 15、shell 5、popover/dialog/lightbox/critical各1；合计39，覆盖同一11个production CSS文件。

### STOP

- B1.2没有修改App/FocusTrap/ProjectSelector/OverlayPortal production，没有改package/test setup，没有stage/commit/push。
- 到此立即停止：不进入B2 portal production，不运行full frontend suite/build，不stage/commit，不进入Task 8，不push。等待主控复核并下达下一分片。

## TASK 7 B1.3 — GUARD PACKAGE INTEGRATION TDD

### STATE

`GREEN`（只扩展既有guard contract test并把同一CLI接入既有`guard:critical-skip`链；20/20与完整critical guard链通过，`test:hook`仍从该链开始。未新建第二script truth）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 49 | package source contract test | 0 | test解析真实package scripts，要求CLI命令作为独立`&&`segment精确一次、无尾随参数、整条script无baseline/allowlist/threshold，且`test:hook`首项仍为`npm run guard:critical-skip` |
| 50 | 首次单suite | 1（invalid harness RED） | Vite把`import.meta.url`变为非`file:` URL，导致0 tests collection失败；不是目标package接线RED，未改production/package断言，只切到repo既有`process.cwd()`路径读取 |
| 51 | 有效TDD RED | 1（expected RED） | 20 tests中1 failed / 19 passed；唯一失败为critical command列表中缺少`node scripts/frontend-z-index-token-guard.mjs`，既有19项pure contract保持GREEN |
| 52 | package single-truth wiring | 0 | 只在既有`guard:critical-skip`末尾用正常`&&`追加同一CLI；没有新建script key，`test`→`test:hook`→`guard:critical-skip`既有调用链不变 |
| 53 | GREEN guard contract | 0 | 1 file / 20 tests passed，Duration 334ms |
| 54 | `npm run guard:critical-skip` | 0 | critical skip、silent async、contract/store、code size与z-index五个guard依序全部PASS；z-index输出`frontend z-index token guard passed` |
| 55 | JSON / lint / LSP | 0 | package JSON parse 0，guard test target ESLint 0；LSP grep/structure定位新contract，command constant hover与xref覆盖声明/4 references，精读test/package exact chain；两文件diagnostics `No diagnostics found`、total 0 |
| 56 | diff-check / status / main只读snapshot | 0 | `git diff --check`无输出，index empty；累计owned dirty 25 entries，只比B1.2增加已批准package path。main仍为外部1个staged entry，cached SHA `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`、unstaged empty SHA `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`，计划SHA `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`；本任务未触碰main |

### STOP

- B1.3没有修改guard production、App/portal/FocusTrap/ProjectSelector production、CSS、test setup或其他package scripts；没有运行full `npm test`/build。
- 到此立即停止：不进入B2，不stage/commit，不进入Task 8，不push。等待主控复核并下达下一分片。

## TASK 7 B2.1 — OVERLAY PORTAL / FOCUS TRAP GREEN

### STATE

`GREEN`（创建唯一portal primitive并让FocusTrapDialog只委托它；Overlay 3 + Focus 6共9 tests全部通过，原focus/ARIA/Escape/backdrop/restore逻辑保持。未进入App/ProjectSelector接线）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 57 | create `OverlayPortal.jsx` | 0 | 同模块export `requiredOverlayRoot`与`OverlayPortal`；每次render重新查询current document的exact-one host，document不可用/missing/duplicate/非HTMLElement统一抛固定安全错误；只调用ReactDOM `createPortal` |
| 58 | wrap existing FocusTrapDialog DOM | 0 | 唯一return在原overlay div外增加一层`<OverlayPortal>`；dialog/backdrop DOM、refs、effects、focus trap、Escape、closeDisabled、restore逻辑未分叉或重写 |
| 59 | first targeted tests | 0 | 2 files / 9 tests passed，Duration 1.04s；portal mount/ownership/unmount/missing/duplicate 3项与Focus现有6项全部GREEN |
| 60 | first target ESLint | 1（static contract conflict） | 唯一错误为`react-refresh/only-export-components`拒绝组件文件同时导出required helper；本阶段明确要求同模块export且禁止新增第三文件，因此仅在helper export前加单行、单规则disable-next-line，无运行时变化 |
| 61 | final lint + targeted tests | 0 | 四个target production/test文件ESLint 0；fresh 2 files / 9 tests passed，Duration 1.79s |
| 62 | source truth | 0 | Overlay module仅有`querySelectorAll('#overlay-root')`、固定错误与`createPortal(children, requiredOverlayRoot())`；无create host、append、body/inline fallback、cache、theme/storage或DOM内容回显。Focus production只有import与唯一JSX wrapper，无第二host/createPortal分支 |
| 63 | LSP locate / definition / xref / read / diagnostics | 0 | grep/structure定位两个exports；Focus import definition跳到`OverlayPortal@19-21`；OverlayPortal xref覆盖Focus与3个portal tests共11处，required helper xref仅声明和单一createPortal consumer；精读helper与完整Focus return；四文件diagnostics `No diagnostics found`、total 0 |
| 64 | diff-check / status / main只读snapshot | 0 | `git diff --check`无输出，index empty；累计owned dirty 27 entries，只比B1.3增加新Overlay production与已批准Focus production。main仍为外部1个staged entry，cached SHA `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`、unstaged empty SHA `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`，计划SHA `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`；本任务未触碰main |

### STOP

- B2.1没有修改App/ProjectSelector production、index/CSS/package/test setup或其他tests；没有运行App/Project tests或full suite/build。
- 到此立即停止：不进入B2.2，不stage/commit，不进入Task 8，不push。等待主控复核并下达下一分片。

## TASK 7 B2.2A — PROJECT SELECTOR UNIFIED PROVIDER GREEN

### STATE

`GREEN`（ProjectSelector删除局部portal owner与React Aria explicit override，自然继承父`UNSAFE_PortalProvider`；整套6 tests通过，open/disabled/actions/focus restore保持）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 65 | remove local portal owner | 0 | 删除`portalContainer` state、`setTriggerNode` callback、closest shell查询与`UNSTABLE_portalContainer`；清理无用`useCallback` import，AriaButton直接使用既有object `triggerRef`，Popover只保留class/placement |
| 66 | ProjectSelector full suite | 0 | 1 file / 6 tests passed，Duration 1.69s；source ownership、disabled close/recovery、select/remove/add、Escape focus restore与provider host ownership全部GREEN |
| 67 | target lint / code-size / source truth | 0 | production+test ESLint 0；frontend code size guard PASS（files=361, frozen=0）；production对portalContainer/override/callback/closest/document/body/host lookup/createPortal/useCallback搜索0 hits |
| 68 | LSP locate / definition / xref / read / diagnostics | 0 | grep/structure定位object ref、open state与全部actions；test import definition跳到`ProjectSelector@9-70`；triggerRef xref恰声明、focus restore与AriaButton ref 3处；精读完整component；production/test diagnostics `No diagnostics found`、total 0。首次在JSX属性空白列做definition返回0，收窄到test import后成功 |
| 69 | diff-check / status / main只读snapshot | 0 | `git diff --check`无输出，index empty；累计owned dirty 28 entries，只比B2.1增加已批准ProjectSelector production。main仍为外部1个staged entry，cached SHA `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`、unstaged empty SHA `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`，计划SHA `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`；本任务未触碰main |

### STOP

- B2.2a没有修改App/test setup、其他production/tests、CSS/index/package；没有运行App tests或full suite/build。
- 到此立即停止：不进入App provider，不stage/commit，不进入Task 8，不push。等待主控复核并下达下一分片。

## TASK 7 B2.2B — APP PROVIDER / OVERLAY THEME PROJECTION GREEN

### STATE

`GREEN`（AppShell在既有theme/layout owners初始化后解析统一host，以无条件layout effect投影theme，并用同一React Aria provider包住UI-test/normal两分支；App 199/199与portal联合15/15通过）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 70 | App provider + projection implementation | 0 | 从`react-aria`导入`UNSAFE_PortalProvider`，从OverlayPortal单一真源导入`requiredOverlayRoot`；`useAppShellState`与`createShellLayoutStore`完成后才解析host，保持theme storage/layout init错误优先级 |
| 71 | unconditional theme projection | 0 | `useLayoutEffect`位于uiTest分支前且每次render无条件调用；写入`shell.theme`，cleanup仅当host当前值仍等于该effect theme才删除，外部篡改被保留，下一次mount/effect仍覆盖stale |
| 72 | provider branch unification | 0 | 单一`UNSAFE_PortalProvider getContainer={() => overlayRoot}`包住`UITestMCPShell`与`AppWindow`条件内容；无body fallback、第二host lookup、theme store/storage或条件hook |
| 73 | App source contract correction | 0 | 删除“helper定义/querySelectorAll位于App”的错误断言；改为精确断言从OverlayPortal导入、单一调用、App内无helper定义/host query，同时保留provider/getContainer、existing theme owner与no fallback契约 |
| 74 | first App full | 1（migration-owned test scope） | 199中197 passed / 2 failed；仅两项shared-file preview modal仍从render `container`查询，统一Focus portal后真实内容位于`appOverlayHost`。5项target provider/theme/boundary tests与其余192项均PASS |
| 75 | approved App test ownership migration | 0 | 主控批准只把两处`.shared-file-content-preview`查询改为既有`appOverlayHost.querySelector`，没有扩成global screen/query fallback；随后App full 199/199通过（16.65s） |
| 76 | portal integration matrix | 0 | OverlayPortal + FocusTrapDialog + ProjectSelector：3 files / 15 tests passed，Duration 1.77s |
| 77 | lint correction / final App full | 0 | 首次lint只发现两处改scope后未使用的`container`解构；只删除该局部解构。App/App test target ESLint 0；fresh App 1 file / 199 tests passed，Duration 16.75s |
| 78 | code-size / source truth | 0 | frontend code size guard PASS（files=361, frozen=0）；App仅有统一helper import/call、theme attribute投影与provider，未出现document.body/querySelectorAll/createElement或overlay独立store |
| 79 | LSP locate / definition / xref / read / diagnostics | 0 | grep/structure定位AppShell新host/effect/provider；App import definition跳到`requiredOverlayRoot@7-17`，helper xref精确覆盖App import/call与OverlayPortal声明/call共4处；精读AppShell完整顺序与两分支；App/App test diagnostics `No diagnostics found`、total 0 |
| 80 | diff-check / status / main只读snapshot | 0 | `git diff --check`无输出，index empty；累计owned dirty 29 entries，只比B2.2a增加已批准App production。main外部此刻变为clean，cached/unstaged均empty SHA `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855`，计划SHA变为`02e9d43c3673c6a48e09aaad47113b406b219cfcdcc22579a824d9026d66f4f8`；本任务未触碰或吸收main变化 |

### STOP

- B2.2b没有修改test setup、其他production/tests、CSS/index/package；两处额外App test修改严格是portal后modal ownership迁移。
- 到此立即停止：不运行full frontend suite/build，不stage/commit，不进入Task 8，不push。等待主控复核并下达下一分片。

## TASK 7 B2.3 — GLOBAL TEST HOST / FULL-SUITE GREEN

### STATE

`GREEN`（以严格test-only host fixture覆盖非App suites，迁移显式portal tests的fixture ownership，并经系统化调试修复两处旧DOM ownership/order断言；fresh `npm test` 128/128 files、1573/1573 tests全绿）

| 顺序 | 命令 / 动作 | exit / result | 关键证据 |
|---:|---|---:|---|
| 81 | 首次full `npm test`只读证据 | 1 | 前置guards/typecheck/RPC audit均PASS；Vitest 10 failed / 118 passed files，37 failed / 1536 passed tests，3 unhandled。普通失败统一为missing unique overlay-root，3 timeout也伴随同一uncaught OverlayPortal错误；运行前后status/diff hash不变 |
| 82 | minimal global test fixture | 0 | `test-setup.js`从Vitest导入beforeEach；每test query exact selector，duplicate立即固定安全错误，missing才create+append，exact-one原样保留；不cleanup duplicate、不设置theme、不增加production fallback |
| 83 | first setup-targeted matrix | 1（fixture ownership conflict） | App+Project通过，Focus 6/6失败、Overlay 2/3失败，共206/214；global hook先建host，而三个explicit suites仍无条件创建本地host，导致duplicate；Overlay missing负例移除本地host后仍残留global host。未运行full、未弱化production |
| 84 | approved explicit fixture ownership migration | 0 | Overlay/Focus/Project beforeEach只领取setup提供的exact-one host并在缺失/非唯一时报固定test错误；caller仍本地创建。Overlay missing/duplicate继续由case内移除/追加，afterEach保持清理 |
| 85 | corrected targeted matrix | 0 | App+Overlay+Focus+Project：4 files / 214 tests passed，Duration 19.30s |
| 86 | second fresh `npm test` | 130（diagnostic STOP） | 前置guards/typecheck/RPC audit PASS；出现3个非host形态失败即按授权Ctrl-C：Workflow schedule两项各5s timeout，Files stale preview一项render-container query undefined；未自行扩面 |
| 87 | systematic-debug phase 1 isolated reproduction | 1 each | Workflow独立3/3进程均相同2 timeout，wall 12.32/14.92/11.49s；Files独立3/3均line165 undefined，wall 1.008/0.996/0.998s；两文件同进程仍3 failed / 14 passed（wall 12.11s）。确认独立+组合稳定复现，不是full-order |
| 88 | phase 1 data-flow finding | 0 | Workflow test用同名button列表`.at(-1)`依赖DOM顺序；portal host早于render container，modal confirm移到前方后`.at(-1)`重新点opener，applyDagOps不触发，waitFor按5000ms超时。Files modal已portal但test仍从render container找preview，故undefined；两业务production/test均未被Task7改动，变化只在shared portal seam |
| 89 | single test-ownership fix | 0 | Workflow导入within，打开后按name取得dialog，再只从dialog内取得confirm；禁止getAll/at/order猜测。Files移除render container ownership，三处preview断言均从已取得latestDialog/dialog查询；未改timeout、fixture或production |
| 90 | exact regression matrix | 0 | Workflow runtime + Files：2 files / 17 tests passed，Duration 2.60s；原5s schedule failures降为63/49ms，Files stale preview 33ms |
| 91 | combined portal matrix | 0 | App+Overlay+Focus+Project+Workflow+Files：6 files / 231 tests passed，Duration 18.96s |
| 92 | final fresh `npm test` | 0 | critical/silent-async/contract-store/code-size/z-index guards、contracts typecheck、RPC audit全部PASS；Vitest 128/128 files、1573/1573 tests passed，Duration 73.74s（tests 27.42s），无failed/unhandled |
| 93 | lint / code-size / LSP | 0 | setup及5个受影响test files target ESLint 0；code-size PASS（files=361, frozen=0）。LSP grep定位dialog ownership assertions，Workflow modal与Files component definition成功，DagScheduleModal xref 4处、FilesPage xref 5处；精读两修复test与setup fixture；9个相关test/production文件diagnostics `No diagnostics found`、total 0。FocusTrapDialog首次xref返回0，改用Files JSX definition成功，不把缺失edge写成PASS |
| 94 | diff-check / status / main只读snapshot | 0 | `git diff --check`无输出，index empty；累计owned dirty 32 entries。final full运行前后worktree status SHA均`b1f82908d465ff2224a158f710784f1e880f60e8c73e0f7dace53fc8481b3df7`、unstaged SHA均`7e9c3b7684ea8a80b24c231beeac48658e5e80a81e15574fc4cff46f86becf04`，证明无生成物漂移。main外部此刻2 entries，cached SHA `2195780a94c8404b40f92537a8982e5beebb89d60258f00e405b780ee5ceb16d`、unstaged SHA `6ea0ea8fc350c337d089bd442f06ff9e569edf91ded303d261ef730f766ab099`、计划SHA `6bbe18cd7191f58a23238b91b3b529f4cf7791adcb9bf9fcfe19130257e02061`；本任务未触碰main |

### FINAL TEST OWNERSHIP EVIDENCE

- production仍严格要求runtime唯一host；test setup只是Vitest环境fixture，不被App/OverlayPortal import，也不设置theme或吞掉duplicate。
- 通用page tests依赖global exact-one fixture；需要验证missing/duplicate的Overlay suite显式领取同一host并在case内控制，下一test由global beforeEach重建。
- modal内容/动作断言全部绑定已取得dialog，不再依赖portal前的render container或body sibling顺序；业务数据断言（cron payload、paused flag、stale preview exclusion）保持原样。

### STOP

- B2.3没有修改production、package、timeout或全局fallback；修复范围严格为setup及5个已批准test files，另更新本记录。
- 到此立即停止：不build、不stage/commit，不进入Task 8、不push。等待主控复核与Task 7后续收口授权。
