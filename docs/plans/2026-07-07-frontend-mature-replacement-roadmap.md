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

- [ ] **Step 1: Write failing tests for unsafe image URLs**

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

- [ ] **Step 2: Run focused tests and confirm RED**

Run:

```bash
cd frontend-app
npm test -- src/pages/chat/components/MarkdownMessage.test.jsx src/pages/skills/SkillsPage.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected before implementation: at least one unsafe URL case fails.

- [ ] **Step 3: Replace passthrough transforms with default-backed allowlists**

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

- [ ] **Step 4: Verify focused and full frontend checks**

Run:

```bash
cd frontend-app
npm test -- src/pages/chat/components/MarkdownMessage.test.jsx src/pages/skills/SkillsPage.test.jsx --no-file-parallelism --maxWorkers=1
npm run lint
npm test
npm run build
```

Expected: all pass.

- [ ] **Step 5: Commit**

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

- [ ] **Step 1: Decide the number policy**

Use one policy consistently:

```text
Preferred: lossless-json parses unsafe JSON numbers as strings for bridge event payloads.
Rejected for this task: native BigInt in UI state, because downstream comparisons currently expect JSON-serializable values.
```

- [ ] **Step 2: Add failing bridge tests**

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

- [ ] **Step 3: Add the dependency**

Run:

```bash
cd frontend-app
npm install lossless-json
```

Do not hand-edit lockfile.

- [ ] **Step 4: Replace regex preparation in `parseRuntimeEventJSON`**

Replace:

```js
const preparedText = rawText
  .replace(/:\s*(-?\d{16,21})\b/g, ':"$1"')
  .replace(/([[,]\s*)(-?\d{16,21})\b/g, '$1"$2"');
parsed = JSON.parse(preparedText);
```

With a structured parser that converts unsafe numbers to strings and leaves normal JSON strings untouched. Keep malformed JSON returning `bridge.event.parse_failed`.

- [ ] **Step 5: Verify**

Run:

```bash
cd frontend-app
npm test -- src/shared/api/wailsBridge.test.js --no-file-parallelism --maxWorkers=1
npm run lint
npm test
npm run build
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add frontend-app/package.json frontend-app/package-lock.json \
  frontend-app/src/shared/api/wailsBridge.js \
  frontend-app/src/shared/api/wailsBridge.test.js
git commit -m "fix(frontend): 使用成熟 JSON 解析处理桥接大整数"
```

---

## Task 3: Remove High-Risk Silent Fallbacks

**Files:**
- Modify: `frontend-app/scripts/no-critical-skip.mjs`
- Modify: `frontend-app/scripts/no-critical-skip.test.mjs`
- Modify: `frontend-app/src/adapters/fileAdapter.js`
- Modify: `frontend-app/src/shared/api/backendSchemas.js`
- Modify: `frontend-app/src/shared/api/backendApi.js`
- Modify: `frontend-app/src/pages/chat/components/chatUiActions.js`
- Test: relevant script, adapter, backendApi, and chat component tests

- [ ] **Step 1: Make critical-skip guard fail on missing roots**

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

- [ ] **Step 2: Stop shared-file detail fallback from fabricating valid data**

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

- [ ] **Step 3: Use shared UI action error reporting in chat**

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

- [ ] **Step 4: Verify and commit**

Run:

```bash
cd frontend-app
npm run guard:critical-skip
npm run lint
npm test
npm run build
```

Commit:

```bash
git add frontend-app/scripts/no-critical-skip.mjs \
  frontend-app/scripts/no-critical-skip.test.mjs \
  frontend-app/src/adapters/fileAdapter.js \
  frontend-app/src/shared/api/backendSchemas.js \
  frontend-app/src/shared/api/backendApi.js \
  frontend-app/src/pages/chat/components/chatUiActions.js
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
- Test: focused adapter/page tests

- [ ] **Step 1: Add malformed response tests first**

Add explicit invalid cases:

```js
expect(() => adaptObservabilityResult({ tail: 10 })).toThrow(/events/);
expect(() => adaptMemorySnapshot({ personal: null })).toThrow(/memory/);
expect(() => normalizeRegistry(null)).toThrow(/model provider registry/);
```

For `observabilityAdapter`, decide whether missing `events` remains degraded parse failure. If preserving degraded display, test for that exact explicit conversion; do not leave it implicit.

- [ ] **Step 2: Move schemas into `backendSchemas.js`**

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

- [ ] **Step 3: Register validators and simplify adapters**

Register schemas in `backendApi.js` where RPCs are known. Keep adapter transforms for UI-specific fields only.

- [ ] **Step 4: Verify**

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

- [ ] **Step 5: Commit**

```bash
git add frontend-app/src/shared/api/backendSchemas.js \
  frontend-app/src/shared/api/backendApi.js \
  frontend-app/src/adapters/observabilityAdapter.js \
  frontend-app/src/adapters/memoryAdapter.js \
  frontend-app/src/adapters/fileAdapter.js \
  frontend-app/src/pages/settings/components/ModelProvidersCard.jsx
git commit -m "refactor(frontend): 用 zod 收敛前端响应边界"
```

---

## Task 5: Migrate Pure RPC State To TanStack Query

**Files:**
- Modify: `frontend-app/src/pages/observability/ObservabilityPage.jsx`
- Modify: `frontend-app/src/pages/settings/SettingsPage.jsx`
- Modify: `frontend-app/src/pages/settings/components/ModelProvidersCard.jsx`
- Test: observability/settings suites

- [ ] **Step 1: Observability query tests**

Add tests for:

```text
recent query does not auto-poll after submit
rapid trace id switches do not write stale trace detail
trace detail cache is keyed by trace id and limit
```

- [ ] **Step 2: Replace reducer sequence guards with Query keys**

Use Query for recent results and trace details. Keep user-triggered submit semantics and disable unwanted refetches.

Expected query key shape:

```js
['observability', 'recent', cwd, submittedLimit]
['observability', 'trace', cwd, traceId, eventLimit]
```

- [ ] **Step 3: Settings query/mutation tests**

Add tests for:

```text
provider registry stale response cannot overwrite newer selection
save mutation disables only relevant controls
app update install is a mutation, not a window-focus query
```

- [ ] **Step 4: Migrate Settings reads and writes**

Use `useQuery` for read snapshots and `useMutation` for button-triggered writes. Keep form draft state local; do not let background refetch overwrite a dirty draft.

- [ ] **Step 5: Verify and commit**

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

---

## Task 6: Move Low-Risk Interactions To React Aria Components

**Files:**
- Modify: `frontend-app/src/pages/memory/MemoryPage.jsx`
- Modify: `frontend-app/src/features/prompts/PromptPageView.jsx`
- Modify: `frontend-app/src/pages/chat/components/ComposerModelSelector.jsx`
- Test: focused component/page tests

- [ ] **Step 1: Memory create menu**

Add tests:

```text
create menu opens with button
Escape closes menu
outside click closes menu
ArrowDown moves between menu items
selecting a type calls the same openCreate path
```

Replace absolute-positioned `div + button` menu with `MenuTrigger`, `Button`, `Popover`, `Menu`, and `MenuItem` from `react-aria-components`.

- [ ] **Step 2: Prompt scope controls**

Replace active button pairs for prompt scope with `RadioGroup`/`Radio` or the closest existing RAC primitive. Keep current labels and submitted payload values.

Tests:

```text
wizard scope exposes one selected radio
editor scope exposes one selected radio
scope change updates draft payload
```

- [ ] **Step 3: Composer model selector popover**

Replace hand-written outside click and `<dialog open>` usage with `DialogTrigger + Popover + Dialog`. Keep native `<select>` controls inside the dialog.

Tests:

```text
Escape closes selector and restores focus
outside click closes selector
async load after unmount does not write stale state
save preserves inherited provider/model behavior
```

- [ ] **Step 4: Verify and commit**

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

- [ ] **Step 1: Validate generated-image routes structurally**

Use `URL` and `URLSearchParams` to reject direct `/generated-image?path=/tmp/secret.png` strings unless they were derived from `.codex/generated_images`.

Add tests:

```js
expect(imagePreviewSource('/generated-image?path=/tmp/secret.png')).toBe('');
expect(imagePreviewSource('/repo/.codex/generated_images/a.png')).toContain('/generated-image');
```

- [ ] **Step 2: Remove frontend-generated `file://` image previews**

Align code preview with workflow final output behavior: image preview must use backend token URLs such as `/local-image?id=...`, not raw `file://`.

Add tests that current `file://` fallback is rejected or replaced.

- [ ] **Step 3: Decide Mermaid sanitizer dependency**

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

- [ ] **Step 4: Verify and commit**

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

- [ ] **Step 1: Memory consolidation polling**

Migrate only the consolidation job polling loop to Query after Tasks 0-6 are stable.

Required tests:

```text
unmount cancels polling
max poll count produces explicit error
succeeded without result is an error
success invalidates memory dashboard
```

- [ ] **Step 2: Skills datasource chunks**

Replace the blocking while-loop chunk load with `useInfiniteQuery`.

Required tests:

```text
first page renders before all chunks finish
hasMore with empty chunks fails fast
next page appends without losing existing chunks
```

- [ ] **Step 3: Runtime diff rendering**

Only introduce `@tanstack/react-virtual` for `RuntimeDiffView`, not for chat timeline.

Required tests:

```text
large diff renders visible lines only
collapsed file renders no diff rows
open/locate buttons keep accessible labels
```

- [ ] **Step 4: Verify each subtask separately**

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
- Modify: `frontend-app/src/pages/backendApiConsumer.surface.test.jsx`
- Modify: `frontend-app/src/styles.test.js`
- Modify: `frontend-app/scripts/no-critical-skip.mjs`
- Optional shared test helper under `frontend-app/src/test-utils/` or `frontend-app/scripts/`

- [ ] **Step 1: Replace regex import guards with TypeScript AST**

Use the existing `typescript` dependency to parse imports. Support:

```text
named import
namespace import
default import
multiline import
commented strings that should not count
```

Add fail-first fixtures for each shape.

- [ ] **Step 2: Keep PostCSS guards, add computed-style checks selectively**

Do not replace `styles.test.js` with screenshot-only testing. Keep PostCSS for import/token contract checks and add Playwright/computed-style checks only for critical cascade bugs.

- [ ] **Step 3: Verify**

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

- [ ] **Step 4: Commit**

```bash
git add frontend-app/src/shared/api/backendApi.surface.test.js \
  frontend-app/src/pages/backendApiConsumer.surface.test.jsx \
  frontend-app/src/styles.test.js \
  frontend-app/scripts/no-critical-skip.mjs
git commit -m "test(frontend): 用 AST 强化前端守卫"
```

---

## Per-Task Review Checklist

Before each task commit:

- [ ] `git status --short` confirms only owned files changed.
- [ ] LSP diagnostics for touched files have no Error/Warning/Information/Hint unless recorded as blocker.
- [ ] Focused tests fail before implementation and pass after implementation.
- [ ] Full frontend validation is run unless the task is docs-only:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

- [ ] Commit uses an atomic Chinese message and stages exact files, not `git add .`.

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
