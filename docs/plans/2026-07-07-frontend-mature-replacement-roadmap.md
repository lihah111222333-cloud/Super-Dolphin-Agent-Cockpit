# Frontend Mature Replacement Roadmap Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Turn the 30-agent frontend audit into a staged, testable replacement roadmap that removes high-risk hand-rolled frontend behavior without forcing broad rewrites.

**Architecture:** Work from verification blockers and security/fail-fast issues outward. Keep existing page/component boundaries stable, replace internals with already-present mature dependencies first, add new dependencies only when the safety/performance payoff is concrete, and preserve domain-specific hand-written logic where a generic library would erase product semantics.

**Tech Stack:** React 19, Vite, Vitest, TanStack Query, react-aria-components, zod, diff, react-markdown, remark-gfm, unified, remark-parse, yaml, TypeScript compiler API, optional `lossless-json` or `json-bigint`, optional DOMPurify.

**Verification Surface:** `frontend-app` LSP diagnostics, focused Vitest suites, `npm run lint`, `npm test`, `npm run build`; guard/script tasks additionally run `npm run guard:critical-skip`, `npm run audit:rpc-contracts`, and `npm run typecheck:contracts` when touched.

---

## Source Audit Summary

This plan is based on 30 effective frontend audit agents. The originally spawned A20 returned stale stop-gate content and is excluded; A20R replaced it and reviewed the Wails bridge scope.

Highest-priority findings:

- Markdown URL sanitizers in Chat and Skills bypass `react-markdown` defaults.
- Wails runtime event JSON parsing uses regex to quote large integers before `JSON.parse`.
- Several response normalizers still turn malformed backend data into valid-looking UI state.
- LSP diagnostics contain Error-level JS `@ts-check` issues in runtime model helpers.
- Some UI controls are hand-written menus/popovers that should use existing `react-aria-components`.
- Some hand-written logic must stay: chat timeline/sticky scroll, runtime patch transforms, redaction, FocusTrapDialog wrapper, and apply_patch diff fallbacks.

## 中文整理版改动计划

### 当前执行边界

- 前端唯一源码目录是 `frontend-app/`；`cmd/agent-terminal/web-dist/` 是构建同步产物，不作为本轮手改目标。
- 本轮目标不是“换框架”，而是逐步替换已被审计确认的重复自研基础设施：解析、响应校验、静默兜底、纯 RPC 状态、低风险交互控件、安全边界和测试守卫。
- 每个阶段只处理一个能力面。阶段完成后必须运行聚焦测试、`npm run lint`、`npm test`、`npm run build`，必要时再用独立端口启动 `run-new-ui-desktop.sh` 做页面/RPC smoke。
- 提交边界按 `$Git原子提交规范` 执行：精确 staging，禁止 `git add .`，每个阶段一个可回滚提交。
- LSP 证据已确认 `backendApi.js` 的响应 validator 注册表、`backendSchemas.js` 的 zod 边界、`ObservabilityPage.jsx` 的手写 reducer/trace cache、`ComposerModelSelector.jsx` 的 document listener + native dialog 交互点。部分 JSX/JS 文件 hover/xref/diagnostics 会超时，实施阶段必须收窄重试；仍失败时记录 blocker，并用 lint/typecheck/focused tests/full tests 补证。

### 阶段总览

| 阶段 | 状态 | 为什么做 | 怎么做 | 主要副作用 | 验证口径 |
|---|---|---|---|---|---|
| Task 0 LSP 诊断清理 | 已完成 | refactor 前先清掉 Error 级诊断，避免后续改动把类型噪声和真实回归混在一起。 | 只补 JSDoc/typedef，不改运行时逻辑。 | 可能留下 TypeScript hint；只要不是 Error/Warning/Information/Hint 新增，记录后进入后续 JS typing 专项。 | 相关 model focused tests + LSP diagnostics + lint/test/build。 |
| Task 1 Markdown URL 安全过滤 | 已完成 | Chat/Skills 曾绕过 `react-markdown` 默认 URL 安全策略，安全收益高且改动面可控。 | 用默认策略做基线，只为产品链接和本地图片 token 保留显式 allowlist。 | 以前能渲染的 `data:*` 或不安全协议会被拒绝；需要正向用例覆盖 `agent://`、`app://`、`SKILL.md` 等产品路径。 | Markdown/Skills focused tests + `/skills` smoke。 |
| Task 2 Wails 大整数 JSON 解析 | 已完成 | regex 改 JSON 文本容易误伤字符串内容，也无法表达真实 JSON number 策略。 | 使用 `lossless-json` 解析 runtime event payload；安全整数仍转 Number，非安全整数转字符串，避免 BigInt 污染 JSON-serializable UI state。 | 下游如果依赖大整数 Number 比较会改变类型；本轮策略是显式字符串化，并用测试固定。 | `wailsBridge.test.js` + full frontend checks + Wails RPC smoke。 |
| Task 3 高风险静默兜底移除 | 已完成 | 缺失目录、畸形 shared-file detail、chat UI action 异常被吞会制造“看似成功”的 UI 状态。 | 守卫脚本缺根 fail-fast；shared-file detail 由 zod/RPC 边界校验；chat 复用共享 `runUIAction`。 | 以前被空数组/ fallback 掩盖的问题会直接报错；这是预期行为。 | guard + adapter/API/chat focused tests + `/files` smoke。 |
| Task 4 扩展 zod 响应边界 | 已完成 | adapter 仍承担过多手写 shape 校验，容易把后端坏数据标准化成可显示数据。 | 把 observability、memory、shared files dashboard、model provider registry 的入口 shape 放进 `backendSchemas.js` 和 `BACKEND_RESPONSE_VALIDATORS`；adapter 只保留 UI 字段转换。 | `observability.events` 缺失保留 degraded parse failure，不改成直接崩；memory/provider/files 的必需数组/对象缺失应 fail-fast。 | adapter/API/settings/files focused tests + typecheck contracts + audit rpc contracts + full checks + `/settings` smoke。 |
| Task 5 纯 RPC 状态迁移到 TanStack Query | 已完成 | settings/observability 仍有 reducer、request sequence、手写 cache；已有 Query 依赖可承接查询状态。 | 先迁移 observability recent/trace，再迁移 settings read/write；dirty draft state 保留本地，不让 background refetch 覆盖用户输入。 | Query 默认 focus refetch、retry、stale 策略可能改变请求时机；所有 query key 和 refetch 策略必须显式。 | Observability/Settings focused tests + full checks。 |
| Task 6 低风险交互控件迁移 React Aria | 已完成 | Memory create menu、Prompt scope、Composer model selector 有手写 outside-click/Escape/dialog 行为，易出现可访问性和焦点回归。 | 引入 `react-aria-components`，用 Menu/Popover/Dialog/RadioGroup 收敛轻量交互；保持现有 props、copy、CSS 结构尽量不动。 | DOM 结构和焦点顺序已改变：Prompt scope 从 button 变 radio，Composer dialog 从 native `<dialog>` 变 `section[role=dialog]`，Memory create item 从 button 变 menuitem。 | Memory/Prompt/Composer focused tests + full checks + desktop smoke。 |
| Task 7 图片与 Mermaid SVG 安全边界 | 已完成 | 本地图片和 SVG sanitizer 是安全边界，不能依赖字符串拼接和宽泛协议。 | 用 `URL`/`URLSearchParams` 验证 generated/local image route；禁止 frontend 直接生成 `file://` 预览；Mermaid 当前不引入 DOMPurify，先用 fixture 收紧现有 sanitizer。 | 旧的 raw file path preview 不再可见，必须走后端 token URL；Mermaid `<image>` 外链和非本地 `url(...)` 会被移除。 | Markdown/Mermaid/code preview focused tests + full checks。 |
| Task 8 后续 Query/virtualization | 已完成 | Memory polling、Skills chunk loading、大 diff 渲染有性能/状态收益，但副作用较大。 | 分成三个子任务：memory polling -> `useQuery`/polling；skills chunks -> `useInfiniteQuery`；runtime diff -> `@tanstack/react-virtual`。 | 请求并发、取消、滚动锚点和局部渲染都可能改变 UX；必须一项一项做。 | 每个子任务单独 focused tests + full checks + 必要页面 smoke。 |
| Task 9 测试/CSS 守卫强化 | 已完成 | regex import guard 容易误判 multiline/default/namespace import，也会被注释字符串干扰。 | 使用已有 TypeScript compiler API 解析 import；CSS 继续保留 PostCSS 守卫，只对关键 cascade 加 computed-style/Playwright 检查。 | AST guard 会更严格，可能暴露已有测试绕行；这是测试守卫收益。 | guard/script focused tests + `npm run guard:critical-skip` + full checks。 |

### 执行顺序和停机条件

执行顺序保持：Task 4 -> Task 5 -> Task 6 -> Task 7 -> Task 8 分拆子任务 -> Task 9。Task 0-3 已作为安全和 fail-fast 前置阶段完成，不再和后续阶段混提交。

任何阶段出现以下情况必须停下修当前阶段，不进入下一阶段：

- 聚焦测试、lint、full `npm test` 或 build 失败。
- 触摸文件出现新的 LSP Error/Warning/Information/Hint，且无法解释或记录 blocker。
- 成熟依赖把 fail-fast 行为变成静默 fallback。
- 产品语义被通用库吃掉，例如 redaction、Wails event envelope、apply_patch diff fallback、chat scroll anchor、local file safety。
- 需要新增依赖但收益只是不确定的“看起来更现代”。

## File Responsibility Map

- `frontend-app/src/entities/client/model/*.js`: runtime/thread/timeline state, type diagnostics, approval id normalization, domain rules that mostly stay hand-written.
- `frontend-app/src/shared/api/backendSchemas.js`: zod response/request schema registry and schema error formatting.
- `frontend-app/src/shared/api/backendApi.js`: RPC facade and validator registry; keep error message compatibility unless a task explicitly changes it.
- `frontend-app/src/shared/api/wailsBridge.js`: Wails/native bridge parsing, event envelopes, telemetry redaction, large JSON number policy.
- `frontend-app/src/pages/chat/components/MarkdownMessage.jsx`: Chat markdown URL and image source policy.
- `frontend-app/src/pages/skills/SkillsPage.jsx`: Skills markdown URL policy and skill response normalization.
- `frontend-app/src/adapters/*.js`: page-facing response adapters; migrate high-risk shape checks to zod while preserving explicit transforms.
- `frontend-app/src/pages/observability/ObservabilityPage.jsx`: pure RPC query/cache candidate for TanStack Query.
- `frontend-app/src/pages/settings/**/*.jsx`: pure RPC query/mutation and provider registry schema candidates.
- `frontend-app/src/pages/memory/MemoryPage.jsx`: menu accessibility, memory response schema, and later polling migration.
- `frontend-app/src/pages/chat/components/ComposerModelSelector.jsx`: React Aria popover/dialog candidate.
- `frontend-app/scripts/no-critical-skip.mjs`: guard fail-fast and later TypeScript AST parsing candidate.

## Non-Goals And Preserve List

Do not replace these in the first execution wave:

- `frontend-app/src/pages/chat/hooks/useTimelineMaterialization.js`: keep the 80-message materialization window; do not introduce `react-virtuoso` or `react-window` for chat timeline until there is profiler evidence and a scroll-anchor design.
- `frontend-app/src/entities/client/model/useClientStore.js` sidebar refresh/cache logic: keep it out of TanStack Query for now because it protects live event state and cwd/project switching.
- `frontend-app/src/shared/api/safeDiagnosticPreview.js`: preserve deny-by-default redaction; do not replace with generic serializers.
- `frontend-app/src/shared/ui/FocusTrapDialog.jsx`: already uses `react-aria-components`; do not replace with Radix/Headless UI.
- `frontend-app/src/pages/chat/adapters/runtimeDiffLineAdapter.js` fallback parsing: keep `diff.parsePatch` for standard unified diff and keep hand-written apply_patch fallback.
- `frontend-app/src/pages/workflows/adapters/workflowDisplayAdapter.js`: do not introduce a DAG execution/graph state library for read-only display ordering.

---

## Task 0: Clean Error-Level LSP Diagnostics Before Refactors

**Files:**
- Modify: `frontend-app/src/entities/client/model/threadMessagesRuntime.js`
- Modify: `frontend-app/src/entities/client/model/timelineRuntime.js`
- Modify: `frontend-app/src/entities/client/model/threadActivityMetrics.js`
- Modify: `frontend-app/src/entities/client/model/threadForkState.js`
- Test: existing model tests for the touched files

- [x] **Step 1: Capture current diagnostics baseline**

Run LSP diagnostics for:

```text
frontend-app/src/entities/client/model/threadMessagesRuntime.js
frontend-app/src/entities/client/model/timelineRuntime.js
frontend-app/src/entities/client/model/threadActivityMetrics.js
frontend-app/src/entities/client/model/threadForkState.js
```

Expected current failures include:

```text
threadMessagesRuntime.js: getThreadMessages/includeArchived/historyFallback inferred type errors
timelineRuntime.js: readonly string[] passed to inferred mutable any[]
threadActivityMetrics.js: readonly string[] passed to inferred mutable any[]
threadForkState.js: readSharedFile does not exist on {}
```

- [x] **Step 2: Add behavior-preserving JSDoc types**

Add only JSDoc/typedefs. Do not change runtime branches.

In `threadMessagesRuntime.js`, define local typedefs for the dependency and load options:

```js
/**
 * @typedef {{
 *   nowMillis?: () => number,
 *   getThreadMessages?: (params: Record<string, unknown>) => Promise<Record<string, unknown>>,
 * }} ThreadMessageFetcherDeps
 *
 * @typedef {{
 *   includeArchived?: boolean,
 *   historyFallback?: unknown[],
 * }} ThreadMessageLoadOptions
 */
```

In `timelineRuntime.js` and `threadActivityMetrics.js`, annotate helper parameters as readonly key lists:

```js
/**
 * @param {Record<string, unknown>} source
 * @param {readonly string[]} keys
 */
```

In `threadForkState.js`, annotate `createLoadForkSharedFiles` deps:

```js
/**
 * @typedef {{
 *   readSharedFile?: (request: { path: string }) => Promise<unknown>,
 * }} ForkSharedFileDeps
 */
```

- [x] **Step 3: Re-run diagnostics and focused tests**

Run:

```bash
cd frontend-app
npm test -- src/entities/client/model/threadMessagesRuntime.test.js \
  src/entities/client/model/timelineRuntime.test.js \
  src/entities/client/model/threadActivityMetrics.test.js \
  src/entities/client/model/threadForkState.test.js \
  --no-file-parallelism --maxWorkers=1
```

Expected: focused tests pass and the four files have no Error diagnostics. Hints may remain only if recorded.

Actual: 2026-07-07 LSP diagnostics for the four files have no Error diagnostics. Existing TypeScript `7044/7047` implicit-any hints remain recorded for a later broad JS typing pass. Focused model tests, full `npm run lint`, full `npm test`, and `npm run build` passed.

- [x] **Step 4: Milestone app smoke**

Run an isolated `run-new-ui-desktop.sh` instance after validation:

```bash
SUPER_DOLPHIN_HTTP_ADDR=127.0.0.1:4524 \
VITE_DEV_URL=http://127.0.0.1:5187 \
GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8104 \
./run-new-ui-desktop.sh
```

Expected: Vite and backend start, the Vite `/wails/ws` proxy accepts a WebSocket, and `ui/sidebar/get`, `ui/dashboard/get`, and `observability/status` return object results. Browser Playwright smoke was unavailable in this environment because Chrome was missing.

- [x] **Step 5: Commit**

```bash
git add frontend-app/src/entities/client/model/threadMessagesRuntime.js \
  frontend-app/src/entities/client/model/timelineRuntime.js \
  frontend-app/src/entities/client/model/threadActivityMetrics.js \
  frontend-app/src/entities/client/model/threadForkState.js
git commit -m "chore(frontend): 清理运行时模型 LSP 诊断"
```

---

## Task 1: Restore Mature Markdown URL Sanitization

**Files:**
- Modify: `frontend-app/src/pages/chat/components/MarkdownMessage.jsx`
- Modify: `frontend-app/src/pages/skills/SkillsPage.jsx`
- Test: Chat markdown tests, Skills page tests

- [x] **Step 1: Write failing tests for unsafe image URLs**

Add tests that prove current passthrough behavior is unsafe:

```jsx
render(<MarkdownMessage text={'![x](javascript:alert(1))'} actions={mockActions} />);
expect(screen.queryByRole('img')).not.toBeInTheDocument();

render(<MarkdownMessage text={'![x](data:text/html,<script>x</script>)'} actions={mockActions} />);
expect(screen.queryByRole('img')).not.toBeInTheDocument();
```

For Skills markdown preview, add equivalent cases in `SkillsPage.test.jsx`:

```jsx
expect(screen.queryByRole('img', { name: 'x' })).not.toBeInTheDocument();
```

Also add positive cases:

```jsx
expect(screen.getByRole('button', { name: /SKILL.md/ })).toBeEnabled();
expect(screen.getByRole('button', { name: /agent:\/\// })).toBeEnabled();
```

- [x] **Step 2: Run focused tests and confirm RED**

Run:

```bash
cd frontend-app
npm test -- src/pages/chat/components/MarkdownMessage.test.jsx src/pages/skills/SkillsPage.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected before implementation: at least one unsafe URL case fails.

Actual: 2026-07-07 focused tests failed before implementation on Chat `data:text/html` image rendering and Skills unsafe image/link buttons.

- [x] **Step 3: Replace passthrough transforms with default-backed allowlists**

In `MarkdownMessage.jsx`, import or reference `defaultUrlTransform` from `react-markdown` if exported by the installed version. If direct import is unavailable, implement a small wrapper that calls the same allowed-protocol policy and document the fallback.

Target behavior:

```js
function markdownUrlTransform(url, key, node) {
  const value = (url || '').toString().trim();
  if (!value) return '';
  const productUrl = productMarkdownUrl(value, { image: node?.tagName === 'img' });
  if (productUrl) return productUrl;
  return defaultUrlTransform(value);
}
```

Policy:

- Links may keep `http:`, `https:`, `mailto:`, and explicitly handled product file/directive schemes.
- Images may keep generated/local-image token routes and safe HTTP(S) URLs.
- Do not allow `data:text/html`.
- Do not allow `data:image/svg+xml` unless a later DOMPurify task explicitly sanitizes SVG.

In `SkillsPage.jsx`, remove `passthroughMarkdownUrlTransform` and use the same default-backed policy while preserving `agent://`, `app://`, and `SKILL.md` button handling.

- [x] **Step 4: Verify focused and full frontend checks**

Run:

```bash
cd frontend-app
npm test -- src/pages/chat/components/MarkdownMessage.test.jsx src/pages/skills/SkillsPage.test.jsx --no-file-parallelism --maxWorkers=1
npm run lint
npm test
npm run build
```

Expected: all pass.

Actual: 2026-07-07 focused tests passed after implementation; `npm run lint`, full `npm test`, and `npm run build` passed. LSP diagnostics were clean for Chat implementation and tests; `SkillsPage.jsx` diagnostics timed out twice because the large file did not publish diagnostics, so this is recorded as an LSP tooling gap covered by lint/test/build.

- [x] **Step 5: Milestone app smoke**

Run an isolated `run-new-ui-desktop.sh` instance after validation:

```bash
SUPER_DOLPHIN_HTTP_ADDR=127.0.0.1:4525 \
VITE_DEV_URL=http://127.0.0.1:5188 \
GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8105 \
./run-new-ui-desktop.sh
```

Expected: `/skills` returns the Vite app entry, the Vite `/wails/ws` proxy accepts a WebSocket, and `ui/sidebar/get`, `ui/dashboard/get` with `page: 'skills'`, and `observability/status` return object results. Browser Playwright smoke remained unavailable because Chrome was missing.

- [x] **Step 6: Commit**

```bash
git add frontend-app/src/pages/chat/components/MarkdownMessage.jsx \
  frontend-app/src/pages/skills/SkillsPage.jsx \
  frontend-app/src/pages/chat/components/MarkdownMessage.test.jsx \
  frontend-app/src/pages/skills/SkillsPage.test.jsx
git commit -m "fix(frontend): 恢复 Markdown URL 安全过滤"
```

---

## Task 2: Replace Runtime Event Big-Integer JSON Regex

**Files:**
- Modify: `frontend-app/package.json`
- Modify: `frontend-app/package-lock.json`
- Modify: `frontend-app/src/shared/api/wailsBridge.js`
- Test: `frontend-app/src/shared/api/wailsBridge.test.js`

- [x] **Step 1: Decide the number policy**

Use one policy consistently:

```text
Preferred: lossless-json parses unsafe JSON numbers as strings for bridge event payloads.
Rejected for this task: native BigInt in UI state, because downstream comparisons currently expect JSON-serializable values.
```

Actual: 2026-07-07 selected `lossless-json` with unsafe JSON numbers converted to strings. Native `BigInt` is intentionally not used in bridge event payloads.

- [x] **Step 2: Add failing bridge tests**

Add cases near the runtime event parse tests:

```js
expect(normalizeRuntimeEventEnvelope({
  name: 'runtime',
  data: '{"payload":{"message":"keep : 1234567890123456 inside string"}}',
}).payload.message).toBe('keep : 1234567890123456 inside string');

expect(normalizeRuntimeEventEnvelope({
  name: 'runtime',
  data: '{"payload":{"requestId":9007199254740993}}',
}).payload.requestId).toBe('9007199254740993');

expect(normalizeRuntimeEventEnvelope({
  name: 'runtime',
  data: '{"payload":{"ids":[9007199254740993]}}',
}).payload.ids).toEqual(['9007199254740993']);
```

Expected before implementation: current regex handling either misses a case or risks modifying string content.

Actual: 2026-07-07 added tests covering string preservation, object unsafe integer conversion, and array unsafe integer conversion. RED run failed before implementation because the regex-prepared payload could not preserve the large-number-looking string case.

- [x] **Step 3: Add the dependency**

Run:

```bash
cd frontend-app
npm install lossless-json
```

Do not hand-edit lockfile.

Actual: 2026-07-07 ran `npm install lossless-json`, adding `lossless-json` to `frontend-app/package.json` and `frontend-app/package-lock.json`.

- [x] **Step 4: Replace regex preparation in `parseRuntimeEventJSON`**

Replace:

```js
const preparedText = rawText
  .replace(/:\s*(-?\d{16,21})\b/g, ':"$1"')
  .replace(/([[,]\s*)(-?\d{16,21})\b/g, '$1"$2"');
parsed = JSON.parse(preparedText);
```

With a structured parser that converts unsafe numbers to strings and leaves normal JSON strings untouched. Keep malformed JSON returning `bridge.event.parse_failed`.

Actual: 2026-07-07 `parseRuntimeEventJSON` now calls `parseLosslessJSON(String(rawText), null, { parseNumber })`; `parseNumber` returns `Number(value)` only for safe JSON numbers and returns the original numeric string for unsafe values. Malformed JSON still returns `bridge.event.parse_failed`. LSP grep/read/hover/xref confirmed the implementation and references; diagnostics were clean for `wailsBridge.js` and `wailsBridge.test.js`.

- [x] **Step 5: Verify**

Run:

```bash
cd frontend-app
npm test -- src/shared/api/wailsBridge.test.js --no-file-parallelism --maxWorkers=1
npm run lint
npm test
npm run build
```

Expected: all pass.

Actual: 2026-07-07 `npm test -- src/shared/api/wailsBridge.test.js` passed with 1 file and 44 tests. `npm run lint` exited 0. Full `npm test` passed with 83 files and 1053 tests. `npm run build` exited 0. The plan command with duplicate `--maxWorkers=1` was incompatible with the current npm script because `npm test` already supplies that option; the compatible command passed by passing only the test file path.

- [x] **Step 6: Milestone app smoke**

Run an isolated `run-new-ui-desktop.sh` instance after validation:

```bash
SUPER_DOLPHIN_HTTP_ADDR=127.0.0.1:4526 \
VITE_DEV_URL=http://127.0.0.1:5189 \
GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8106 \
./run-new-ui-desktop.sh
```

Expected: Vite and backend start, `/skills` returns the Vite app entry, the Vite `/wails/ws` proxy accepts a WebSocket, and `ui/sidebar/get`, `ui/dashboard/get` with `page: 'skills'`, and `observability/status` return object results.

Actual: 2026-07-07 Vite became ready at `http://127.0.0.1:5189`, backend became ready at `http://127.0.0.1:4526/metrics`, `/skills` returned the Vite app entry, and direct WebSocket RPCs through `ws://127.0.0.1:5189/wails/ws` passed for `ui/sidebar/get`, `ui/dashboard/get` with `page: 'skills'`, and `observability/status`. Existing `npm run smoke:desktop:rpc` also passed. The isolated 4526/5189/8106 ports were released after the smoke run.

- [x] **Step 7: Commit**

```bash
git add frontend-app/package.json frontend-app/package-lock.json \
  frontend-app/src/shared/api/wailsBridge.js \
  frontend-app/src/shared/api/wailsBridge.test.js
git commit -m "fix(frontend): 使用成熟 JSON 解析处理桥接大整数"
```

---

## Task 3: Remove High-Risk Silent Fallbacks

**Files:**
- Modify: `frontend-app/package.json`
- Modify: `frontend-app/package-lock.json`
- Modify: `frontend-app/scripts/no-critical-skip.mjs`
- Modify: `frontend-app/scripts/no-critical-skip.test.mjs`
- Modify: `frontend-app/src/adapters/fileAdapter.js`
- Create: `frontend-app/src/adapters/fileAdapter.test.js`
- Create: `frontend-app/src/shared/api/backendSchemas.js`
- Modify: `frontend-app/src/shared/api/backendApi.js`
- Modify: `frontend-app/src/pages/chat/components/chatUiActions.js`
- Create: `frontend-app/src/pages/chat/components/chatUiActions.test.js`
- Test: relevant script, adapter, backendApi, and chat component tests

- [x] **Step 1: Make critical-skip guard fail on missing roots**

Write a failing test proving a missing root is not accepted:

```js
await expect(collectCriticalSkipViolations({ roots: ['missing-critical-root'] }))
  .rejects.toThrow(/critical skip root does not exist/);
```

Change `walkTestFiles(dir)` so missing directories throw instead of `return []`.

Verify:

```bash
cd frontend-app
npm run guard:critical-skip
npm test -- scripts/no-critical-skip.test.mjs --no-file-parallelism --maxWorkers=1
```

Actual: 2026-07-07 added a fail-fast missing-root test and changed `walkTestFiles(dir)` to throw when the configured root is missing or is not a directory. RED focused run failed before implementation because missing roots returned no violations. GREEN focused run passed after implementation.

- [x] **Step 2: Stop shared-file detail fallback from fabricating valid data**

Add failing tests for malformed detail responses:

```js
await expect(api.readSharedFile({ path: 'a.txt' })).rejects.toThrow(/shared file detail/);
```

Implementation target:

- Add `sharedFileDetailResponseSchema` in `backendSchemas.js`.
- Register it for the shared-file detail RPC in `backendApi.js`.
- Change `adaptSharedFileDetail(response, fallbackFile)` so `fallbackFile` is used only for UI-only display fields that are explicitly allowed, not for required contract fields such as `path`.

Verify:

```bash
cd frontend-app
npm test -- src/shared/api/backendApi.test.js src/adapters/fileAdapter.test.js src/pages/files/FilesPage.test.jsx --no-file-parallelism --maxWorkers=1
```

Actual: 2026-07-07 added `zod` and created `backendSchemas.js` with `sharedFileDetailResponseSchema`. `backendApi.js` now validates `ui/memory/shared-file/get` responses and preserves the schema error as `cause` when wrapping it with RPC method context. `adaptSharedFileDetail` rejects missing response `path` instead of fabricating it from fallback data, and only uses fallback metadata for display fields such as `updatedBy`, `updatedAt`, and `createdAt`. RED tests failed before implementation for both adapter and RPC boundary; GREEN focused run passed after implementation.

- [x] **Step 3: Use shared UI action error reporting in chat**

Replace private swallowing helper in `frontend-app/src/pages/chat/components/chatUiActions.js` with the shared helper in `frontend-app/src/shared/ui/runUIAction.js`.

Target behavior:

```js
export { runUIAction } from '../../../shared/ui/runUIAction.js';
```

If relative path differs, use the actual import path. Add tests that sync and async failures call the injected logger/onError instead of disappearing.

Verify:

```bash
cd frontend-app
npm test -- src/shared/ui/runUIAction.test.js src/pages/chat/ChatPage.test.jsx --no-file-parallelism --maxWorkers=1
```

Actual: 2026-07-07 `chatUiActions.js` now re-exports the shared `runUIAction`; `chatUiActions.test.js` verifies synchronous and asynchronous chat action failures call injected `logger` and `onError`. Focused run also covered `src/shared/ui/runUIAction.test.js` and `src/pages/chat/ChatPage.test.jsx`.

- [x] **Step 4: Verify**

Run:

```bash
cd frontend-app
npm run guard:critical-skip
npm run lint
npm test
npm run build
```

Actual: 2026-07-07 focused Task 3 run passed with 7 files and 135 tests. `npm run guard:critical-skip` exited 0. `npm run lint` exited 0 after preserving the wrapped schema error cause. Full `npm test` passed with 84 files and 1057 tests. `npm run build` exited 0. LSP diagnostics were clean for the new schema and new tests; `fileAdapter.js` diagnostics timed out and `backendApi.js` diagnostics exceeded the LSP output budget because of existing large-file diagnostic volume, so those are recorded as LSP tooling gaps covered by lint/typecheck/focused/full tests.

- [x] **Step 5: Milestone app smoke**

Run an isolated `run-new-ui-desktop.sh` instance after validation:

```bash
SUPER_DOLPHIN_HTTP_ADDR=127.0.0.1:4527 \
VITE_DEV_URL=http://127.0.0.1:5190 \
GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8107 \
./run-new-ui-desktop.sh
```

Expected: Vite and backend start, `/files` returns the Vite app entry, the Vite `/wails/ws` proxy accepts a WebSocket, and `ui/sidebar/get`, `ui/dashboard/get` with `page: 'files'`, and `observability/status` return object results.

Actual: 2026-07-07 Vite became ready at `http://127.0.0.1:5190`, backend became ready at `http://127.0.0.1:4527/metrics`, `/files` returned the Vite app entry, and direct WebSocket RPCs through `ws://127.0.0.1:5190/wails/ws` passed for `ui/sidebar/get`, `ui/dashboard/get` with `page: 'files'`, and `observability/status`. The isolated 4527/5190/8107 ports were released after the smoke run.

- [x] **Step 6: Commit**

Commit:

```bash
git add frontend-app/package.json frontend-app/package-lock.json \
  docs/plans/2026-07-07-frontend-mature-replacement-roadmap.md \
  frontend-app/scripts/no-critical-skip.mjs \
  frontend-app/scripts/no-critical-skip.test.mjs \
  frontend-app/src/adapters/fileAdapter.js \
  frontend-app/src/adapters/fileAdapter.test.js \
  frontend-app/src/shared/api/backendSchemas.js \
  frontend-app/src/shared/api/backendApi.js \
  frontend-app/src/shared/api/backendApi.test.js \
  frontend-app/src/pages/chat/components/chatUiActions.js \
  frontend-app/src/pages/chat/components/chatUiActions.test.js \
git commit -m "fix(frontend): 移除前端静默兜底路径"
```

---

## Task 4: Expand Zod Response Boundaries In Adapters

**Files:**
- Modify: `frontend-app/src/shared/api/backendSchemas.js`
- Modify: `frontend-app/src/shared/api/backendApi.js`
- Modify: `frontend-app/src/adapters/observabilityAdapter.js`
- Modify: `frontend-app/src/adapters/memoryAdapter.js`
- Modify: `frontend-app/src/adapters/fileAdapter.js`
- Modify: `frontend-app/src/pages/settings/components/ModelProvidersCard.jsx`
- Create: `frontend-app/src/pages/settings/components/ModelProvidersCardModel.js`
- Test: focused adapter/page tests

- [x] **Step 1: Add malformed response tests first**

Add explicit invalid cases:

```js
expect(() => adaptObservabilityResult({ tail: 10 })).toThrow(/events/);
expect(() => normalizeMemorySnapshot({ private: null, team: { entries: [] } })).toThrow(/memory/);
expect(() => normalizeRegistry(null)).toThrow(/model provider registry/);
```

For `observabilityAdapter`, decide whether missing `events` remains degraded parse failure. If preserving degraded display, test for that exact explicit conversion; do not leave it implicit.

Actual: 2026-07-07 added RED/GREEN coverage for malformed observability, memory, shared-files dashboard, and model-provider registry responses. `adaptObservabilityResult({ tail: 10 })` intentionally remains a degraded parse failure with an `observability.events.invalid` event instead of throwing, because the observability page already exposes partial parse failures in UI. The memory adapter export is `normalizeMemorySnapshot`, not `adaptMemorySnapshot`, so the test and implementation use the actual symbol.

- [x] **Step 2: Move schemas into `backendSchemas.js`**

Create schemas for:

```text
observabilityResultSchema
memorySnapshotSchema
sharedFilesDashboardSchema
modelProviderRegistrySchema
```

Use `transform` for real compatibility requirements:

- `snake_case` to `camelCase`
- known legacy aliases
- display-only labels

Do not use transforms to hide missing required arrays/objects.

Actual: 2026-07-07 `backendSchemas.js` now contains `observabilityResultSchema`, `memorySnapshotSchema`, `sharedFilesDashboardSchema`, `sharedFileDetailResponseSchema`, and `modelProviderRegistrySchema` plus parse helpers with stable fail-fast messages. The schemas allow passthrough fields for backend compatibility but do not synthesize missing required arrays/objects.

- [x] **Step 3: Register validators and simplify adapters**

Register schemas in `backendApi.js` where RPCs are known. Keep adapter transforms for UI-specific fields only.

Actual: 2026-07-07 registered zod-backed validators for shared files dashboard, model provider list/apply, observability list/trace methods, UI memory snapshot, and shared-file detail. `fileAdapter.js`, `memoryAdapter.js`, `observabilityAdapter.js`, and provider registry normalization now parse response boundaries first and keep UI-only mapping in adapters. `ModelProvidersCardModel.js` was created so provider registry normalization can be unit-tested without exporting non-components from `ModelProvidersCard.jsx`, preserving React Fast Refresh lint rules.

- [x] **Step 4: Verify**

Run:

```bash
cd frontend-app
npm test -- src/adapters/observabilityAdapter.test.js \
  src/adapters/memoryAdapter.test.js \
  src/pages/files/FilesPage.test.jsx \
  src/pages/settings/SettingsPage.test.jsx \
  src/shared/api/backendApi.test.js \
  src/shared/api/backendApi.contractMatrix.test.js \
  --no-file-parallelism --maxWorkers=1
npm run typecheck:contracts
npm run audit:rpc-contracts
npm run lint
npm test
npm run build
```

Actual: 2026-07-07 focused Task 4 suite passed with 8 files and 113 tests:

```bash
cd frontend-app
npm test -- src/adapters/observabilityAdapter.test.js \
  src/adapters/memoryAdapter.test.js \
  src/adapters/fileAdapter.test.js \
  src/pages/files/FilesPage.test.jsx \
  src/pages/settings/SettingsPage.test.jsx \
  src/pages/settings/components/ModelProvidersCard.test.jsx \
  src/shared/api/backendApi.test.js \
  src/shared/api/backendApi.contractMatrix.test.js
```

The command also ran `guard:critical-skip`, `typecheck:contracts`, and `audit:rpc-contracts`, all passing. Full `npm run lint`, full `npm test` with 86 files and 1061 tests, and `npm run build` passed after the Task 4 fix. LSP diagnostics were clean for `ModelProvidersCardModel.js`; diagnostics for large JSX/test files timed out after narrowed retries and are recorded as an LSP tooling gap covered by lint/typecheck/focused/full tests/build.

- [x] **Step 5: Milestone app smoke**

Run an isolated `run-new-ui-desktop.sh` instance after validation:

```bash
SUPER_DOLPHIN_HOME=/tmp/sd-task4-browser-smoke-${USER:-user}/super-dolphin-home \
SUPER_DOLPHIN_HTTP_ADDR=127.0.0.1:4528 \
VITE_DEV_URL=http://127.0.0.1:5191 \
GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8108 \
./run-new-ui-desktop.sh
```

Actual: 2026-07-07 Vite became ready at `http://127.0.0.1:5191`, backend became ready at `http://127.0.0.1:4528/metrics`, `/settings` returned the Vite app entry, and direct WebSocket RPCs through `ws://127.0.0.1:5191/wails/ws` passed for `ui/sidebar/get`, `ui/dashboard/get` with `page: 'settings'`, and `observability/status`. The isolated 4528/5191/8108 ports were released after the smoke run.

- [x] **Step 6: Commit**

```bash
git add frontend-app/src/shared/api/backendSchemas.js \
  frontend-app/src/shared/api/backendApi.js \
  frontend-app/src/shared/api/backendApi.test.js \
  frontend-app/src/adapters/observabilityAdapter.js \
  frontend-app/src/adapters/observabilityAdapter.test.js \
  frontend-app/src/adapters/memoryAdapter.js \
  frontend-app/src/adapters/memoryAdapter.test.js \
  frontend-app/src/adapters/fileAdapter.js \
  frontend-app/src/adapters/fileAdapter.test.js \
  frontend-app/src/pages/files/FilesPage.test.jsx \
  frontend-app/src/pages/settings/SettingsPage.test.jsx \
  frontend-app/src/pages/settings/components/ModelProvidersCard.jsx \
  frontend-app/src/pages/settings/components/ModelProvidersCardModel.js \
  frontend-app/src/pages/settings/components/ModelProvidersCard.test.jsx \
  docs/plans/2026-07-07-frontend-mature-replacement-roadmap.md
git commit -m "refactor(frontend): 用 zod 收敛前端响应边界"
```

---

## Task 5: Migrate Pure RPC State To TanStack Query

**Files:**
- Modify: `frontend-app/src/pages/observability/ObservabilityPage.jsx`
- Modify: `frontend-app/src/pages/settings/SettingsPage.jsx`
- Modify: `frontend-app/src/pages/settings/components/ModelProvidersCard.jsx`
- Test: observability/settings suites

- [x] **Step 1: Observability query tests**

Add tests for:

```text
recent query does not auto-poll after submit
rapid trace id switches do not write stale trace detail
trace detail cache is keyed by trace id and limit
```

- [x] **Step 2: Replace reducer sequence guards with Query keys**

Use Query for recent results and trace details. Keep user-triggered submit semantics and disable unwanted refetches.

Expected query key shape:

```js
['observability', 'recent', cwd, submittedLimit]
['observability', 'trace', cwd, traceId, eventLimit]
```

- [x] **Step 3: Settings query/mutation tests**

Add tests for:

```text
provider registry stale response cannot overwrite newer selection
save mutation disables only relevant controls
app update install is a mutation, not a window-focus query
```

- [x] **Step 4: Migrate Settings reads and writes**

Use `useQuery` for read snapshots and `useMutation` for button-triggered writes. Keep form draft state local; do not let background refetch overwrite a dirty draft.

- [x] **Step 5: Verify and commit**

Run:

```bash
cd frontend-app
npm test -- src/pages/observability/ObservabilityPage.test.jsx \
  src/pages/settings/SettingsPage.test.jsx src/SettingsPage.test.jsx \
  --no-file-parallelism --maxWorkers=1
npm run lint
npm test
npm run build
```

Commit:

```bash
git add frontend-app/src/pages/observability/ObservabilityPage.jsx \
  frontend-app/src/pages/settings/SettingsPage.jsx \
  frontend-app/src/pages/settings/components/ModelProvidersCard.jsx
git commit -m "refactor(frontend): 用 TanStack Query 收敛纯 RPC 状态"
```

Actual: 2026-07-07 focused Settings RED showed the dirty provider draft/refetch case timing out before the Query migration. After implementation, focused Settings passed with 33 tests, and the combined focused suite passed with 4 files and 66 tests:

```bash
cd frontend-app
npm test -- src/pages/settings/SettingsPage.test.jsx
npm test -- src/pages/observability/ObservabilityPage.test.jsx \
  src/pages/settings/SettingsPage.test.jsx src/SettingsPage.test.jsx \
  src/services/modules/observabilityPage.degradation.test.jsx
```

Full validation passed:

```bash
npm run lint
npm test
npx vitest run
npm run build
```

Counts: full `npm test` and bare `npx vitest run` both passed with 86 files and 1064 tests. Build passed and synced the frontend dist. `run_frontend_size_guard` is a stop-hook local function; its implementation falls back to `npm run lint`, which passed. LSP structure/read/xref evidence was captured for `ModelProvidersCard.jsx`, `SettingsPage.jsx`, and `ObservabilityPage.jsx`; LSP diagnostics for the touched JSX files timed out after narrowed single-file retries with `context deadline exceeded`, covered by lint/typecheck/focused/full tests/build.

Milestone app smoke used isolated ports:

```bash
SUPER_DOLPHIN_HOME=/tmp/sd-task5-browser-smoke-${USER:-user}/super-dolphin-home \
SUPER_DOLPHIN_HTTP_ADDR=127.0.0.1:4530 \
VITE_DEV_URL=http://127.0.0.1:5193 \
GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8110 \
./run-new-ui-desktop.sh
```

Verified `/settings` and `/observability` returned Vite HTML through port 5193. The Vite-proxied WebSocket `ws://127.0.0.1:5193/wails/ws` returned object results for `ui/sidebar/get`, `ui/dashboard/get page=settings`, `ui/dashboard/get page=observability`, and `observability/status`. Direct backend WS on port 4530 was rejected in this dev layout, so smoke used the same Vite proxy path as the browser. The smoke process was stopped and ports 4530/5193 were confirmed closed.

---

## Task 6: Move Low-Risk Interactions To React Aria Components

**Files:**
- Modify: `frontend-app/src/pages/memory/MemoryPage.jsx`
- Modify: `frontend-app/src/features/prompts/PromptPageView.jsx`
- Modify: `frontend-app/src/pages/chat/components/ComposerModelSelector.jsx`
- Test: focused component/page tests

- [x] **Step 1: Memory create menu**

Add tests:

```text
create menu opens with button
selecting a type calls the same openCreate path
```

Replace absolute-positioned `div + button` menu with `MenuTrigger`, `Button`, `Popover`, `Menu`, and `MenuItem` from `react-aria-components`.

Actual: 2026-07-07 replaced the create dropdown with RAC `MenuTrigger`, `Button`, `Popover`, `Menu`, and `MenuItem`. Tests now assert the create menu exposes `role=menu` and `role=menuitem` items, and the existing creation test clicks `menuitem` to verify the same `openCreate`/upsert payload path. Keyboard roving focus and MenuTrigger outside-dismiss are left to the RAC implementation; jsdom `fireEvent` did not reliably exercise those library internals, so component tests avoid re-testing them.

- [x] **Step 2: Prompt scope controls**

Replace active button pairs for prompt scope with `RadioGroup`/`Radio` or the closest existing RAC primitive. Keep current labels and submitted payload values.

Tests:

```text
wizard scope exposes one selected radio
editor scope exposes one selected radio
scope change updates draft payload
```

Actual: 2026-07-07 replaced prompt scope button pairs with RAC `RadioGroup`/`Radio`. Editor and wizard tests assert selected radio state with `toBeChecked()` and verify saved/draft payload scope changes.

- [x] **Step 3: Composer model selector popover**

Replace hand-written outside click and `<dialog open>` usage with `DialogTrigger + Popover + Dialog`. Keep native `<select>` controls inside the dialog.

Tests:

```text
Escape closes selector and restores focus
outside click closes selector
async load after unmount does not write stale state
save preserves inherited provider/model behavior
```

Actual: 2026-07-07 replaced the hand-written document outside listener and native `<dialog open>` with RAC `DialogTrigger`, `Popover`, and `Dialog`, keeping native `<select>` controls. Tests cover Escape focus restore, outside click close, async load after unmount, inherited model preservation when changing effort, and disabled trigger behavior.

- [x] **Step 4: Verify and commit**

Run:

```bash
cd frontend-app
npm test -- src/pages/memory/MemoryPage.test.jsx \
  src/features/prompts/PromptPageView.test.jsx \
  src/pages/chat/components/ComposerModelSelector.test.jsx \
  --no-file-parallelism --maxWorkers=1
npm run lint
npm test
npm run build
```

Actual validation:

```bash
npm test -- src/pages/memory/MemoryPage.test.jsx \
  src/features/prompts/PromptPageView.test.jsx \
  src/pages/chat/components/ComposerModelSelector.test.jsx
npx vitest run --no-file-parallelism --maxWorkers=1 src/App.test.jsx \
  -t "turns the composer model chip into a thread model selector|traps focus in the prompt editor and restores focus after Escape|wires memory center mutation actions to backend RPCs"
npm run lint
npm test
npx vitest run
npm run build
```

Counts: focused Memory/Prompt/Composer tests passed with 3 files and 35 tests. Focused App integration recheck passed with 3 selected tests. Full `npm test` and bare `npx vitest run` both passed with 86 files and 1071 tests. Build passed and synced frontend dist. LSP diagnostics were clean for touched focused files after fixing the `withTimeout` async hint; a few JSX diagnostics initially timed out or were partial during edits and were covered by lint/focused/full tests/build.

Milestone desktop smoke used isolated ports and Vite-proxied Wails RPC:

```bash
SUPER_DOLPHIN_HOME=/tmp/sd-task6-smoke-${USER:-user}/super-dolphin-home \
SUPER_DOLPHIN_HTTP_ADDR=127.0.0.1:4532 \
GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8112 \
VITE_DEV_URL=http://127.0.0.1:5195 \
FRONTEND_DEVSERVER_URL=http://127.0.0.1:5195 \
SUPER_DOLPHIN_DESKTOP_SMOKE_SKIP_FRONTEND_BUILD=1 \
SUPER_DOLPHIN_DESKTOP_SMOKE_WS_URL=ws://127.0.0.1:5195/wails/ws \
npm run smoke:desktop:rpc
```

Result: `ui/sidebar/get`, `ui/dashboard/get`, `observability/status`, `thread/start`, and `observability/frontend/ingest` returned valid object results; smoke printed `desktop smoke passed` and stopped Vite/backend processes.

Commit:

```bash
git add frontend-app/src/pages/memory/MemoryPage.jsx \
  frontend-app/src/features/prompts/PromptPageView.jsx \
  frontend-app/src/pages/chat/components/ComposerModelSelector.jsx
git commit -m "refactor(frontend): 用 React Aria 收敛轻量交互控件"
```

---

## Task 7: Add Security-Oriented Image And SVG Follow-Ups

**Files:**
- Modify: `frontend-app/src/pages/chat/components/markdownFileLinkModel.js`
- Modify: `frontend-app/src/pages/chat/adapters/codePreviewAdapter.js`
- Modify: `frontend-app/src/pages/chat/components/markdownMermaidModel.js`
- Optional dependency: DOMPurify if selected
- Test: chat markdown/code preview/Mermaid tests

- [x] **Step 1: Validate generated-image routes structurally**

Use `URL` and `URLSearchParams` to reject direct `/generated-image?path=/tmp/secret.png` strings unless they were derived from `.codex/generated_images`.

Add tests:

```js
expect(imagePreviewSource('/generated-image?path=/tmp/secret.png')).toBe('');
expect(imagePreviewSource('/repo/.codex/generated_images/a.png')).toContain('/generated-image');
```

Actual: 2026-07-07 added structured `/generated-image` route validation with `URL`/`URLSearchParams`. Direct `/generated-image?path=/tmp/secret.png` and traversal under `.codex/generated_images/../` are rejected, while generated image paths under `.codex/generated_images` still normalize to the backend route.

- [x] **Step 2: Remove frontend-generated `file://` image previews**

Align code preview with workflow final output behavior: image preview must use backend token URLs such as `/local-image?id=...`, not raw `file://`.

Add tests that current `file://` fallback is rejected or replaced.

Actual: 2026-07-07 removed frontend-generated `file://` fallbacks from code preview image state. Image preview now requires backend-issued safe URLs such as `/local-image?id=...`; unsafe or missing image URLs produce a visible error and no empty `<img src="">`.

- [x] **Step 3: Decide Mermaid sanitizer dependency**

If the current Mermaid SVG sanitizer remains, add fixtures for:

```text
style=url(...)
xlink:href
image href
data:image/svg+xml
namespace attributes
```

If DOMPurify is selected:

```bash
cd frontend-app
npm install dompurify
```

Configure SVG profile narrowly and keep dimension normalization as post-processing.

Actual: 2026-07-07 did not introduce DOMPurify. The existing sanitizer was kept but covered with fixtures for `style=url(...)`, `xlink:href`, `<image href>`, `data:image/svg+xml`, and namespace attributes. The sanitizer now removes SVG `<image>` nodes, strips `data:image/svg+xml`, and only allows `url(#localRef)` references.

- [x] **Step 4: Verify and commit**

Run:

```bash
cd frontend-app
npm test -- src/pages/chat/components/MarkdownMessage.test.jsx \
  src/pages/chat/components/MermaidDiagram.test.jsx \
  src/pages/chat/adapters/codePreviewAdapter.test.js \
  --no-file-parallelism --maxWorkers=1
npm run lint
npm test
npm run build
```

Actual validation:

```bash
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/chat/components/markdownMessageModel.test.js \
  src/pages/chat/components/MarkdownMessage.test.jsx \
  src/pages/chat/components/TimelineMessage.test.jsx \
  src/pages/chat/components/MermaidDiagram.test.jsx \
  src/pages/chat/components/CodePreviewDialog.test.jsx \
  src/pages/chat/adapters/codePreviewAdapter.test.js
npx vitest run --no-file-parallelism --maxWorkers=1 src/App.test.jsx \
  -t "renders image runtime diff previews without the text editor|renders generated local image paths from assistant replies as image previews|renders local image paths in markdown image syntax through the generated image route"
npm run lint
npm test
npm run build
```

Counts: focused component/model tests passed with 6 files and 39 tests. Focused App integration recheck passed with 3 selected tests. Full `npm test` passed with 86 files and 1077 tests. Build passed and synced frontend dist. LSP diagnostics were clean for the JSX files that became ready; pure JS model diagnostics repeatedly timed out before publishing after narrowed single-file retries, so lint/focused tests/full checks cover the gap.

Commit:

```bash
git add frontend-app/src/pages/chat/components/markdownFileLinkModel.js \
  frontend-app/src/pages/chat/adapters/codePreviewAdapter.js \
  frontend-app/src/pages/chat/components/markdownMermaidModel.js \
  frontend-app/package.json frontend-app/package-lock.json
git commit -m "fix(frontend): 收紧本地图片与 Mermaid 安全边界"
```

Omit `package.json` and `package-lock.json` from staging if DOMPurify is not introduced.

---

## Task 8: Later Query And Virtualization Work

**Files:**
- Modify later: `frontend-app/src/pages/memory/MemoryPage.jsx`
- Modify later: `frontend-app/src/pages/skills/SkillsPage.jsx`
- Modify later: `frontend-app/src/pages/chat/components/RuntimeDiffView.jsx`
- Optional dependency: `@tanstack/react-virtual`

- [x] **Step 1: Memory consolidation polling**

Migrate only the consolidation job polling loop to Query after Tasks 0-6 are stable.

Required tests:

```text
unmount cancels polling
max poll count produces explicit error
succeeded without result is an error
success invalidates memory dashboard
```

Actual: 2026-07-07 migrated only the background consolidation job polling from the manual async loop/map to TanStack Query `useQuery` with `enabled`, `refetchInterval`, `retry:false`, per-job poll count, and signal-aware status fetch. A `succeeded` status without an object result now fails fast instead of being treated as an empty successful consolidation.

Focused validation:

```bash
npx vitest run --no-file-parallelism --maxWorkers=1 src/pages/memory/MemoryPage.test.jsx
npm run lint
npm test
npm run build
```

Counts: MemoryPage focused tests passed with 1 file and 13 tests. Full `npm test` passed with 86 files and 1080 tests. Build passed and synced frontend dist. LSP diagnostics for `MemoryPage.jsx` and `MemoryPage.test.jsx` were clean after a narrowed single-file retry.

- [x] **Step 2: Skills datasource chunks**

Replace the blocking while-loop chunk load with `useInfiniteQuery`.

Required tests:

```text
first page renders before all chunks finish
hasMore with empty chunks fails fast
next page appends without losing existing chunks
```

Actual: 2026-07-07 replaced the blocking `while (hasMore)` detail loader with TanStack Query `useInfiniteQuery`. The first datasource detail page now renders as soon as `getDatasourceDocument` returns; follow-up chunk pages are fetched with `fetchNextPage` and appended automatically, preserving the previous eventual "show all chunks" behavior without blocking the dialog on later pages. A `hasMore` chunk page with no chunks now fails fast for both the initial `datasourceV2/get` page and follow-up `datasourceV2/list_chunks` pages.

Focused validation:

```bash
npx vitest run --no-file-parallelism --maxWorkers=1 src/pages/skills/SkillsPage.test.jsx
npm run lint
npm test
npm run build
npx vitest run
```

Counts: SkillsPage focused tests passed with 1 file and 15 tests. Full `npm test` and stop-gate-style bare `npx vitest run` both passed with 86 files and 1082 tests. Build passed and synced frontend dist. LSP diagnostics for `SkillsPage.jsx` and `SkillsPage.test.jsx` were clean after opening the files and retrying diagnostics individually; the first batch diagnostics request timed out on the large `SkillsPage.jsx`.

- [x] **Step 3: Runtime diff rendering**

Only introduce `@tanstack/react-virtual` for `RuntimeDiffView`, not for chat timeline.

Required tests:

```text
large diff renders visible lines only
collapsed file renders no diff rows
open/locate buttons keep accessible labels
```

Actual: 2026-07-07 added `@tanstack/react-virtual` only to `RuntimeDiffView`. Large per-file runtime diffs now render a fixed-height virtualized row window instead of mapping every parsed diff line into the DOM. Collapsed files still skip line parsing/rendering, and the existing locate/open/toggle accessible labels remain unchanged. The virtualizer uses a fixed 420px line viewport and a defensive row measurement floor so jsdom and zero-height first measurements do not produce an empty initial range.

Focused validation:

```bash
npx vitest run --no-file-parallelism --maxWorkers=1 src/pages/chat/components/RuntimePanelComponents.test.jsx
npx vitest run --no-file-parallelism --maxWorkers=1 src/styles.test.js src/pages/chat/components/RuntimePanelComponents.test.jsx
npx vitest run --no-file-parallelism --maxWorkers=1 src/pages/chat/components/RuntimePanelComponents.test.jsx src/App.test.jsx src/pages/chat/ChatPage.test.jsx
npm run lint
npm test
npm run build
npx vitest run
```

Counts: RuntimePanelComponents focused tests passed with 1 file and 10 tests after RED showed 240 rendered rows. Styles + runtime focused tests passed with 2 files and 71 tests. App/Chat integration focused tests passed with 3 files and 277 tests. Full `npm test` and stop-gate-style bare `npx vitest run` both passed with 86 files and 1084 tests. Build passed and synced frontend dist. LSP grep/read/xref succeeded for `RuntimeDiffView`; diagnostics repeatedly timed out for `RuntimeDiffView.jsx` and `RuntimePanelComponents.test.jsx` after narrowed open-file retries, so lint/test/build are the recorded fallback evidence for this subtask.

- [x] **Step 4: Verify each subtask separately**

Each subtask gets its own commit and runs:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

---

## Task 9: Test Guard And CSS Guard Hardening

**Files:**
- Modify: `frontend-app/src/shared/api/backendApi.surface.test.js`
- Modify: `frontend-app/src/pages/backendApiConsumer.surface.test.js`
- Modify: `frontend-app/src/styles.test.js`
- Modify: `frontend-app/scripts/no-critical-skip.mjs`
- Optional shared test helper under `frontend-app/src/test-utils/` or `frontend-app/scripts/`

- [x] **Step 1: Replace regex import guards with TypeScript AST**

Use the existing `typescript` dependency to parse imports. Support:

```text
named import
namespace import
default import
multiline import
commented strings that should not count
```

Add fail-first fixtures for each shape.

Actual: 2026-07-07 added `frontend-app/src/test-utils/importAst.js` around TypeScript `ImportDeclaration` parsing and replaced regex import extraction in `backendApi.surface.test.js` and the actual consumer guard file `backendApiConsumer.surface.test.js`. Fixtures now cover named import aliases, namespace imports, default imports, multiline imports, and commented/string import text that must not count. The raw bridge guard now also fails on default and namespace raw bridge imports instead of only detecting some namespace call sites via regex.

- [x] **Step 2: Keep PostCSS guards, add computed-style checks selectively**

Do not replace `styles.test.js` with screenshot-only testing. Keep PostCSS for import/token contract checks and add Playwright/computed-style checks only for critical cascade bugs.

Actual: no computed-style check was added in this subtask because the import-surface risk is static and already covered by AST fixtures. The existing PostCSS `styles.test.js` suite was retained and included in focused/full validation. `no-critical-skip.mjs` was reviewed and left unchanged because it scans `.skip` test declarations rather than import declarations; its guard was run directly.

- [x] **Step 3: Verify**

Run:

```bash
cd frontend-app
npm test -- src/shared/api/backendApi.surface.test.js \
  src/pages/backendApiConsumer.surface.test.jsx \
  src/styles.test.js \
  --no-file-parallelism --maxWorkers=1
npm run guard:critical-skip
npm run lint
npm test
npm run build
```

Actual validation:

```bash
npx vitest run --no-file-parallelism --maxWorkers=1 src/shared/api/backendApi.surface.test.js src/pages/backendApiConsumer.surface.test.js
npm run guard:critical-skip
npx vitest run --no-file-parallelism --maxWorkers=1 src/shared/api/backendApi.surface.test.js src/pages/backendApiConsumer.surface.test.js src/styles.test.js
npm run lint
npm test
npm run build
npx vitest run
```

Counts: surface focused tests passed with 2 files and 9 tests. Surface + styles focused tests passed with 3 files and 70 tests. Full `npm test` and stop-gate-style bare `npx vitest run` both passed with 86 files and 1088 tests. Build passed and synced frontend dist. LSP diagnostics were clean for the new `importAst.js` helper after changing TypeScript to a default import. Diagnostics for the two surface test files timed out after narrowed retries; lint/test/build are the recorded fallback evidence.

- [x] **Step 4: Commit**

```bash
git add frontend-app/src/shared/api/backendApi.surface.test.js \
  frontend-app/src/pages/backendApiConsumer.surface.test.jsx \
  frontend-app/src/styles.test.js \
  frontend-app/scripts/no-critical-skip.mjs
git commit -m "test(frontend): 用 AST 强化前端守卫"
```

## Final Application Smoke

The final application smoke used the startup script through `npm run smoke:desktop:rpc` with isolated ports and an explicit Vite-proxied Wails WebSocket URL:

```bash
cd frontend-app
SUPER_DOLPHIN_HTTP_ADDR=127.0.0.1:4531 \
VITE_DEV_URL=http://127.0.0.1:5194 \
FRONTEND_DEVSERVER_URL=http://127.0.0.1:5194 \
GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8111 \
SUPER_DOLPHIN_DESKTOP_SMOKE_WS_URL=ws://127.0.0.1:5194/wails/ws \
SUPER_DOLPHIN_DESKTOP_SMOKE_SKIP_FRONTEND_BUILD=1 \
npm run smoke:desktop:rpc
```

Result: 2026-07-07 `run-new-ui-desktop.sh` started Vite at `http://127.0.0.1:5194`, started the desktop backend at `127.0.0.1:4531`, and `ui/sidebar/get`, `ui/dashboard/get`, `observability/status`, `thread/start`, and `observability/frontend/ingest` all returned valid object results. The smoke printed `desktop smoke passed` and stopped the Vite/backend processes. A prior direct backend WebSocket attempt to `127.0.0.1:4530/wails/ws` failed with HTTP 403; this is the expected dev-layout behavior already recorded in Task 5, so the browser-equivalent Vite proxy path is the valid smoke surface.

---

## Per-Task Review Checklist

Before each task commit:

- [x] `git status --short` was reviewed before commits; exact task files were staged, while unrelated pre-existing/user dirty files were intentionally left unstaged.
- [x] LSP diagnostics for touched files are either clean or recorded above as LSP tooling gaps with focused tests, lint, build, and full test evidence.
- [x] Focused tests were run per task; RED/GREEN evidence is recorded for tasks where behavior-changing tests were introduced before implementation.
- [x] Full frontend validation was run for implementation tasks:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

- [x] Commits use atomic Chinese messages and exact staging; `git add .` was not used for the task commits.

## Recommended Execution Order

1. Task 0: LSP diagnostics cleanup.
2. Task 1: Markdown URL sanitizer.
3. Task 2: Wails big-int JSON parsing.
4. Task 3: high-risk silent fallbacks.
5. Task 4: zod adapter boundaries.
6. Task 5: pure RPC Query migration.
7. Task 6: low-risk React Aria controls.
8. Task 7: image/SVG security hardening.
9. Task 8: later high-side-effect Query/virtualization work.
10. Task 9: test/CSS guard hardening.

## Execution Options

1. **子代理驱动（推荐）** - 每个 task 使用新子代理执行，主 agent 在任务之间审查和验证。
2. **当前会话内执行** - 使用 `执行计划` 在当前会话按任务顺序推进，每个任务完成后验证和提交。
