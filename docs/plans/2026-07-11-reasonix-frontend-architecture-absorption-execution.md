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
| Task 4 crash containment | `TODO` | 依赖 Task 3 |
| Task 5 approval-only | `TODO` | 依赖 Task 4 |
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
