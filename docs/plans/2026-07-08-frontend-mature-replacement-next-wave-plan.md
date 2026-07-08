# Frontend Mature Replacement Next Wave Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Continue the frontend “no hand-written wheels when mature dependencies exist” process with small, independently verifiable replacements.

**Architecture:** Keep product behavior stable while moving response parsing into zod schema transforms, moving accessible menu/select behavior into React Aria Components, and moving local async state into TanStack Query where it already owns the surrounding data flow. Each task has a narrow file set and must preserve fail-fast behavior rather than replacing explicit errors with silent defaults.

**Tech Stack:** React 19, Vite, Vitest, zod, TanStack Query, react-aria-components, react-markdown, existing `frontend-app` service/API facades.

**Verification Surface:** `frontend-app` focused Vitest suites, `npm run lint`, `npm test`, `npm run build`, relevant LSP diagnostics, and desktop smoke only after multiple UI tasks land together.

---

## Evidence And Boundaries

This plan is based on the 2026-07-08 multi-agent scan. The orchestration attempted 18 agents; only 2 agents returned effective findings because the remaining agent threads stalled and were shut down. Their non-returned work is not used as evidence. The main session then checked targeted code snippets and dependency availability to shape this implementation plan.

Current dependency check from `frontend-app/package.json` confirms the needed mature dependencies already exist:

```text
zod
@tanstack/react-query
react-aria-components
react-markdown
@tanstack/react-virtual
```

Do not introduce Tailwind, shadcn, another UI framework, `date-fns`, `dayjs`, or `yaml` in this plan. If a later task wants a new dependency, split it into a separate plan.

---

## File Map

- `frontend-app/src/shared/api/backendSchemas.js` owns backend response shape validation and schema-level transforms.
- `frontend-app/src/adapters/observabilityAdapter.js` should become a display adapter, not a malformed-response parser.
- `frontend-app/src/adapters/memoryAdapter.js` should either disappear as a shape normalizer or become a thin display-model mapper after zod transform.
- `frontend-app/src/adapters/fileAdapter.js` should stop parsing shared-file dashboard unions that belong in schema transforms.
- `frontend-app/src/pages/chat/components/ChatPageHeader.jsx` owns the chat header action menu.
- `frontend-app/src/pages/chat/components/ProjectSelector.jsx` owns project selection and project removal UI.
- `frontend-app/src/pages/chat/components/ComposerModelSelector.jsx` owns model selector popover/dialog UI and thread config edits.
- `frontend-app/src/pages/chat/components/ChatApprovalMessage.jsx` owns approval submit UI.
- Existing tests next to those files are the primary safety net. Add or update focused tests before implementation in every task.

---

## Task 1: Move Observability Response Parsing Into Zod

**Files:**
- Modify: `frontend-app/src/shared/api/backendSchemas.js`
- Modify: `frontend-app/src/adapters/observabilityAdapter.js`
- Modify: `frontend-app/src/adapters/observabilityAdapter.test.js`
- Test: `frontend-app/src/services/modules/observabilityService.test.js`

- [ ] **Step 1: Write RED schema tests for malformed events**

Add assertions in `frontend-app/src/adapters/observabilityAdapter.test.js` that pin the intended boundary: schema parsing rejects a non-array `events` response, while valid event alias fields still normalize.

```js
import { parseObservabilityResultResponse } from '../shared/api/backendSchemas.js';

it('rejects observability responses with non-array events at the schema boundary', () => {
  expect(() => parseObservabilityResultResponse({ events: null }))
    .toThrow('observability response events must be an array');
});

it('normalizes observability event aliases at the schema boundary', () => {
  const result = parseObservabilityResultResponse({
    source: 'tail',
    events: [{
      trace_id: 'trace-1',
      span_id: 'span-1',
      duration_ms: 12,
      status: 'ok',
    }],
  });
  expect(result.events[0]).toMatchObject({
    traceId: 'trace-1',
    spanId: 'span-1',
    durationMs: 12,
    status: 'ok',
  });
});
```

- [ ] **Step 2: Run RED test**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 src/adapters/observabilityAdapter.test.js
```

Expected: FAIL because `observabilityResultSchema` is currently `objectSchema` and adapter still creates degraded parse failure events.

- [ ] **Step 3: Add zod event/result schemas**

In `frontend-app/src/shared/api/backendSchemas.js`, replace `export const observabilityResultSchema = objectSchema;` with explicit schemas and transform output:

```js
const observabilityEventSchema = z.object({
  ts: z.unknown().optional(),
  traceId: z.unknown().optional(),
  trace_id: z.unknown().optional(),
  spanId: z.unknown().optional(),
  span_id: z.unknown().optional(),
  parentSpanId: z.unknown().optional(),
  parent_span_id: z.unknown().optional(),
  method: z.unknown().optional(),
  phase: z.unknown().optional(),
  kind: z.unknown().optional(),
  status: z.unknown().optional(),
  threadId: z.unknown().optional(),
  thread_id: z.unknown().optional(),
  turnId: z.unknown().optional(),
  turn_id: z.unknown().optional(),
  agentId: z.unknown().optional(),
  agent_id: z.unknown().optional(),
  callId: z.unknown().optional(),
  call_id: z.unknown().optional(),
  toolName: z.unknown().optional(),
  tool_name: z.unknown().optional(),
  clientKind: z.unknown().optional(),
  client_kind: z.unknown().optional(),
  clientRoute: z.unknown().optional(),
  client_route: z.unknown().optional(),
  durationMs: z.unknown().optional(),
  duration_ms: z.unknown().optional(),
  error: z.unknown().optional(),
  code: z.unknown().optional(),
  metadata: z.unknown().optional(),
  stack: z.unknown().optional(),
}).passthrough().transform((event) => ({
  ts: schemaTextValue(event.ts),
  traceId: schemaTextValue(event.traceId ?? event.trace_id),
  spanId: schemaTextValue(event.spanId ?? event.span_id),
  parentSpanId: schemaTextValue(event.parentSpanId ?? event.parent_span_id),
  method: schemaTextValue(event.method),
  phase: schemaTextValue(event.phase),
  kind: schemaTextValue(event.kind),
  status: schemaTextValue(event.status) || 'unknown',
  threadId: schemaTextValue(event.threadId ?? event.thread_id),
  turnId: schemaTextValue(event.turnId ?? event.turn_id),
  agentId: schemaTextValue(event.agentId ?? event.agent_id),
  callId: schemaTextValue(event.callId ?? event.call_id),
  toolName: schemaTextValue(event.toolName ?? event.tool_name),
  clientKind: schemaTextValue(event.clientKind ?? event.client_kind),
  clientRoute: schemaTextValue(event.clientRoute ?? event.client_route),
  durationMs: schemaNumberValue(event.durationMs ?? event.duration_ms, 0),
  error: schemaTextValue(event.error),
  code: event.code || null,
  metadata: event.metadata || null,
  stack: Array.isArray(event.stack) ? event.stack : [],
}));

export const observabilityResultSchema = z.object({
  source: z.unknown().optional(),
  truncated: z.unknown().optional(),
  degraded: z.unknown().optional(),
  parseError: z.unknown().optional(),
  parse_error: z.unknown().optional(),
  tailError: z.unknown().optional(),
  tail_error: z.unknown().optional(),
  tailTimedOut: z.unknown().optional(),
  tail_timed_out: z.unknown().optional(),
  tailFilesScanned: z.unknown().optional(),
  tail_files_scanned: z.unknown().optional(),
  totalDurationMs: z.unknown().optional(),
  total_duration_ms: z.unknown().optional(),
  events: z.array(observabilityEventSchema),
}).passthrough().transform((value) => ({
  source: schemaTextValue(value.source),
  truncated: Boolean(value.truncated),
  degraded: Boolean(value.degraded),
  parseError: schemaTextValue(value.parseError ?? value.parse_error),
  tailError: schemaTextValue(value.tailError ?? value.tail_error),
  tailTimedOut: Boolean(value.tailTimedOut ?? value.tail_timed_out),
  tailFilesScanned: schemaNumberValue(value.tailFilesScanned ?? value.tail_files_scanned, 0),
  totalDurationMs: schemaNumberValue(value.totalDurationMs ?? value.total_duration_ms, 0),
  events: value.events,
}));
```

Add helpers near `formatIssue`:

```js
function schemaTextValue(value) {
  return value === null || value === undefined ? '' : value.toString().trim();
}

function schemaNumberValue(value, fallback = 0) {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
}
```

Extend `formatIssue`:

```js
if (label === 'observability response') {
  if (path === 'events') return 'observability response events must be an array';
  return 'observability response must be an object';
}
```

- [ ] **Step 4: Remove malformed-event fabrication from adapter**

In `frontend-app/src/adapters/observabilityAdapter.js`, remove `observabilityParseFailureEvent`, `adaptObservabilityEvent`, and `joinParseErrors`. Keep `adaptObservabilityResult` as a thin pass-through over schema output:

```js
function adaptObservabilityResult(response) {
  const value = parseObservabilityResultResponse(response);
  return {
    ...value,
    degraded: Boolean(value.degraded) || Boolean(value.parseError),
  };
}

export { adaptObservabilityResult };
```

If existing UI still requires malformed events to be visible, stop this task and split a product decision: schema-level fail-fast and degraded UI are incompatible unless the backend explicitly returns degraded events.

- [ ] **Step 5: Run GREEN focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/adapters/observabilityAdapter.test.js \
  src/services/modules/observabilityService.test.js \
  src/shared/api/backendApi.test.js
```

Expected: PASS. If tests expecting fabricated parse-failure events fail, update them to expect schema rejection or split the product decision.

- [ ] **Step 6: LSP diagnostics**

Run diagnostics for:

```text
frontend-app/src/shared/api/backendSchemas.js
frontend-app/src/adapters/observabilityAdapter.js
frontend-app/src/adapters/observabilityAdapter.test.js
```

Fix every Error/Warning/Information/Hint. If diagnostics time out after narrowed retries, record exact blocker.

- [ ] **Step 7: Commit**

```bash
git add frontend-app/src/shared/api/backendSchemas.js \
  frontend-app/src/adapters/observabilityAdapter.js \
  frontend-app/src/adapters/observabilityAdapter.test.js \
  frontend-app/src/services/modules/observabilityService.test.js \
  frontend-app/src/shared/api/backendApi.test.js
git commit -m "refactor(frontend): 用 zod 收敛观测响应解析"
```

---

## Task 2: Move Memory Snapshot DTO Transform Into Zod

**Files:**
- Modify: `frontend-app/src/shared/api/backendSchemas.js`
- Modify: `frontend-app/src/adapters/memoryAdapter.js`
- Modify: `frontend-app/src/adapters/memoryAdapter.test.js`
- Modify: `frontend-app/src/pages/shared/pageShared.js`

- [ ] **Step 1: Add RED tests for schema-level DTO output**

In `frontend-app/src/adapters/memoryAdapter.test.js`, add:

```js
import { parseMemorySnapshotResponse } from '../shared/api/backendSchemas.js';

it('normalizes memory snapshot entries at the schema boundary', () => {
  const result = parseMemorySnapshotResponse({
    overview: { health: { similarGroups: [] } },
    private: { entries: [{ path: 'u.md', type: 'user', name: 'User Memory', updated_at: '2026-07-08' }] },
    team: { entries: [{ path: 'p.md', type: 'project', title: 'Project Memory' }] },
  });
  expect(result.entries).toEqual([
    expect.objectContaining({ target: 'private', path: 'u.md', category: 'preference', updatedAt: '2026-07-08' }),
    expect.objectContaining({ target: 'team', path: 'p.md', category: 'project' }),
  ]);
});
```

- [ ] **Step 2: Run RED test**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 src/adapters/memoryAdapter.test.js
```

Expected: FAIL because `parseMemorySnapshotResponse` currently returns `{ overview, private, team }`, not `{ overview, entries }`.

- [ ] **Step 3: Add memory DTO transform in `backendSchemas.js`**

Move the type/category mapping into schema-level helpers:

```js
export const MEMORY_TYPE_INFO = Object.freeze({
  user: { category: 'preference', label: '偏好' },
  feedback: { category: 'preference', label: '偏好' },
  project: { category: 'project', label: '项目' },
  reference: { category: 'project', label: '项目' },
});
```

Add `memoryEntrySchema(target)` and transform `memorySnapshotSchema` to output:

```js
{
  overview: schemaObjectValue(value.overview),
  entries: [
    ...value.private.entries.map((item, index) => normalizeMemoryEntryDTO(item, index, 'private')),
    ...value.team.entries.map((item, index) => normalizeMemoryEntryDTO(item, index, 'team')),
  ],
}
```

Use fail-fast errors for empty path, unsupported type, and missing name. Preserve current error text where tests assert it:

```text
memory private entry 0 path is required
memory team entry 0 type is unsupported: (empty)
```

- [ ] **Step 4: Thin `memoryAdapter.js`**

Change `normalizeMemorySnapshot` to:

```js
function normalizeMemorySnapshot(response) {
  return parseMemorySnapshotResponse(response);
}
```

Keep `memoryHealth` and `normalizeSimilarityGroups` in `memoryAdapter.js` for this task unless they are also cleanly moved without breaking imports. Import `MEMORY_TYPE_INFO` from `backendSchemas.js` and re-export it for compatibility:

```js
import { MEMORY_TYPE_INFO, parseMemorySnapshotResponse } from '../shared/api/backendSchemas.js';
```

- [ ] **Step 5: Run focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/adapters/memoryAdapter.test.js \
  src/pages/memory/MemoryPage.test.jsx \
  src/pages/shared/pageShared.test.js
```

Expected: PASS.

- [ ] **Step 6: LSP diagnostics**

Run diagnostics for:

```text
frontend-app/src/shared/api/backendSchemas.js
frontend-app/src/adapters/memoryAdapter.js
frontend-app/src/adapters/memoryAdapter.test.js
frontend-app/src/pages/shared/pageShared.js
```

- [ ] **Step 7: Commit**

```bash
git add frontend-app/src/shared/api/backendSchemas.js \
  frontend-app/src/adapters/memoryAdapter.js \
  frontend-app/src/adapters/memoryAdapter.test.js \
  frontend-app/src/pages/memory/MemoryPage.test.jsx \
  frontend-app/src/pages/shared/pageShared.js \
  frontend-app/src/pages/shared/pageShared.test.js
git commit -m "refactor(frontend): 用 zod 收敛记忆快照 DTO"
```

---

## Task 3: Move Shared Files Dashboard Transform Into Zod

**Files:**
- Modify: `frontend-app/src/shared/api/backendSchemas.js`
- Modify: `frontend-app/src/adapters/fileAdapter.js`
- Modify: `frontend-app/src/adapters/fileAdapter.test.js`
- Modify: `frontend-app/src/pages/files/FilesPage.test.jsx`

- [ ] **Step 1: Add RED schema tests**

In `frontend-app/src/adapters/fileAdapter.test.js`, add:

```js
import { parseSharedFilesDashboardResponse } from '../shared/api/backendSchemas.js';

it('normalizes shared files dashboard aliases at the schema boundary', () => {
  const result = parseSharedFilesDashboardResponse({
    memory: [{ path: 'reports/final.md', content: 'body', updated_by: 'agent' }],
    finalOutputRefs: ['reports/final.md'],
    sharedFileRetention: { items: [{ path: 'reports/final.md', protected: true }], protectedCount: 1 },
  });
  expect(result.files).toEqual([
    expect.objectContaining({ path: 'reports/final.md', updatedBy: 'agent' }),
  ]);
  expect(result.finalOutputRefs).toEqual([
    expect.objectContaining({ path: 'reports/final.md' }),
  ]);
  expect(result.retention).toEqual(expect.objectContaining({ protectedCount: 1 }));
});
```

- [ ] **Step 2: Run RED test**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 src/adapters/fileAdapter.test.js
```

Expected: FAIL because current schema does not transform dashboard aliases.

- [ ] **Step 3: Add shared-file transform in `backendSchemas.js`**

Define schemas for:

```js
const sharedFileItemSchema = z.object({}).passthrough().transform((raw) => ({
  path: firstSchemaText(raw.path),
  content: firstSchemaText(raw.content),
  updatedBy: firstSchemaText(raw.updated_by, raw.updatedBy),
  updatedAt: firstSchemaText(raw.updated_at, raw.updatedAt),
  createdAt: firstSchemaText(raw.created_at, raw.createdAt),
}));

const finalOutputRefSchema = z.union([
  z.string().transform((path) => ({ path: schemaTextValue(path), runKey: '', dagKey: '', sourceNodeKey: '' })),
  z.object({}).passthrough().transform((item) => ({
    path: firstSchemaText(item.path, item.sharedfile?.path, item.sharedFile?.path, item.shared_file?.path),
    runKey: firstSchemaText(item.runKey, item.run_key),
    dagKey: firstSchemaText(item.dagKey, item.dag_key),
    sourceNodeKey: firstSchemaText(item.sourceNodeKey, item.source_node_key),
  })),
]);
```

Transform `sharedFilesDashboardSchema` to output:

```js
{
  files,
  finalOutputRefs,
  retention,
}
```

Preserve fail-fast errors for missing `files|memory`, malformed ref, malformed retention item path.

- [ ] **Step 4: Thin `fileAdapter.js`**

Change `adaptSharedFilesDashboard` to:

```js
function adaptSharedFilesDashboard(response) {
  return parseSharedFilesDashboardResponse(response);
}
```

Keep `adaptSharedFileDetail` in the adapter for now because it merges response detail with a fallback file.

- [ ] **Step 5: Run focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/adapters/fileAdapter.test.js \
  src/pages/files/FilesPage.test.jsx \
  src/shared/api/backendApi.test.js
```

Expected: PASS.

- [ ] **Step 6: LSP diagnostics**

Run diagnostics for:

```text
frontend-app/src/shared/api/backendSchemas.js
frontend-app/src/adapters/fileAdapter.js
frontend-app/src/adapters/fileAdapter.test.js
frontend-app/src/pages/files/FilesPage.test.jsx
```

- [ ] **Step 7: Commit**

```bash
git add frontend-app/src/shared/api/backendSchemas.js \
  frontend-app/src/adapters/fileAdapter.js \
  frontend-app/src/adapters/fileAdapter.test.js \
  frontend-app/src/pages/files/FilesPage.test.jsx \
  frontend-app/src/shared/api/backendApi.test.js
git commit -m "refactor(frontend): 用 zod 收敛共享文件仪表盘"
```

---

## Task 4: Replace Chat Header Action Menu With React Aria Menu

**Files:**
- Modify: `frontend-app/src/pages/chat/components/ChatPageHeader.jsx`
- Modify: `frontend-app/src/pages/chat/components/ChatPageHeader.test.jsx`
- Check: `frontend-app/src/pages/chat/components/ProjectSelector.jsx`

- [ ] **Step 1: Add RED keyboard/focus tests**

In `ChatPageHeader.test.jsx`, add tests for the current hand-written menu gaps:

```jsx
it('opens actions menu with keyboard and restores focus on Escape', async () => {
  const user = userEvent.setup();
  render(<ChatPageHeader {...baseProps} />);
  const trigger = screen.getByRole('button', { name: /更多操作|操作/ });
  trigger.focus();
  await user.keyboard('{Enter}');
  expect(screen.getByRole('menu')).toBeInTheDocument();
  await user.keyboard('{Escape}');
  expect(screen.queryByRole('menu')).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
});
```

Use the actual accessible trigger name from the current test fixture. Do not weaken the test to `getByText`.

- [ ] **Step 2: Run RED focused test**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 src/pages/chat/components/ChatPageHeader.test.jsx
```

Expected: FAIL if the current menu does not restore focus or lacks keyboard behavior.

- [ ] **Step 3: Replace manual listeners with React Aria Components**

In `ChatPageHeader.jsx`, replace `actionsOpen`, `useEffect(pointerdown/keydown)`, and manual menu rendering with:

```jsx
import { Button, Menu, MenuItem, MenuTrigger, Popover } from 'react-aria-components';
```

Use structure:

```jsx
<MenuTrigger>
  <Button className="chat-header-action-trigger" aria-label="更多操作">
    ...
  </Button>
  <Popover className="chat-header-actions-popover">
    <Menu className="chat-header-actions-menu" aria-label="聊天操作">
      <MenuItem onAction={onNewThread}>新建线程</MenuItem>
      <MenuItem onAction={onArchiveThread}>归档线程</MenuItem>
    </Menu>
  </Popover>
</MenuTrigger>
```

If `ProjectSelector` is currently nested inside the action menu, do not put an interactive project menu inside a `MenuItem`. Move project selection outside this menu or leave `ProjectSelector` unchanged in this task and document the remaining nested interaction risk.

- [ ] **Step 4: Run GREEN focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/chat/components/ChatPageHeader.test.jsx \
  src/pages/chat/ChatPage.test.jsx
```

Expected: PASS.

- [ ] **Step 5: Visual smoke**

Run the frontend app if needed and manually check:

```bash
cd frontend-app
npm run dev -- --host 127.0.0.1 --port 5175 --strictPort
```

Check `/chat` header menu with mouse, Enter, ArrowDown, Escape, and focus return. Stop the dev server after checking.

- [ ] **Step 6: Commit**

```bash
git add frontend-app/src/pages/chat/components/ChatPageHeader.jsx \
  frontend-app/src/pages/chat/components/ChatPageHeader.test.jsx \
  frontend-app/src/pages/chat/ChatPage.test.jsx
git commit -m "refactor(frontend): 用 React Aria 收敛聊天操作菜单"
```

---

## Task 5: Move Project Selector Popover To React Aria Select/ListBox

**Files:**
- Modify: `frontend-app/src/pages/chat/components/ProjectSelector.jsx`
- Modify: `frontend-app/src/pages/chat/components/ProjectSelector.test.jsx`
- Modify if needed: `frontend-app/src/pages/chat/components/ChatPageHeader.jsx`

- [ ] **Step 1: Add RED accessibility tests**

Add tests that require keyboard operation and removal action preservation:

```jsx
it('selects a project with keyboard navigation', async () => {
  const user = userEvent.setup();
  render(<ProjectSelector projects={projects} activeProject={projects[0]} onSelectProject={onSelectProject} />);
  await user.click(screen.getByRole('button', { name: /项目/ }));
  await user.keyboard('{ArrowDown}{Enter}');
  expect(onSelectProject).toHaveBeenCalledWith(projects[1]);
});

it('keeps remove project as an explicit secondary action', async () => {
  render(<ProjectSelector projects={projects} activeProject={projects[0]} onRemoveProject={onRemoveProject} />);
  await userEvent.click(screen.getByRole('button', { name: /项目/ }));
  await userEvent.click(screen.getByRole('button', { name: /移除/ }));
  expect(onRemoveProject).toHaveBeenCalled();
});
```

- [ ] **Step 2: Run RED focused test**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 src/pages/chat/components/ProjectSelector.test.jsx
```

Expected: FAIL if keyboard listbox semantics are missing.

- [ ] **Step 3: Implement with React Aria Components**

Use:

```jsx
import { Button, ListBox, ListBoxItem, Popover, Select, SelectValue } from 'react-aria-components';
```

Use `Select` for choosing project when each row is only selection. If row-level remove must stay inside the popup, use a `MenuTrigger/Menu/MenuItem` design instead and keep remove as a separate button outside the selectable item. Do not place a native `button` inside `ListBoxItem` if it breaks item selection semantics.

- [ ] **Step 4: Run GREEN focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/chat/components/ProjectSelector.test.jsx \
  src/pages/chat/components/ChatPageHeader.test.jsx \
  src/pages/chat/ChatPage.test.jsx
```

- [ ] **Step 5: Commit**

```bash
git add frontend-app/src/pages/chat/components/ProjectSelector.jsx \
  frontend-app/src/pages/chat/components/ProjectSelector.test.jsx \
  frontend-app/src/pages/chat/components/ChatPageHeader.jsx \
  frontend-app/src/pages/chat/components/ChatPageHeader.test.jsx \
  frontend-app/src/pages/chat/ChatPage.test.jsx
git commit -m "refactor(frontend): 用 React Aria 收敛项目选择器"
```

---

## Task 6: Move Composer Model Selector Async State To TanStack Query

**Files:**
- Modify: `frontend-app/src/pages/chat/components/ComposerModelSelector.jsx`
- Modify: `frontend-app/src/pages/chat/components/ComposerModelSelector.test.jsx`
- Check: `frontend-app/src/pages/shared/pageShared.js`

- [ ] **Step 1: Add RED stale-request test**

Add a test proving stale thread config loads cannot overwrite the current active thread draft:

```jsx
it('does not apply stale model config results after active thread changes', async () => {
  const first = deferred();
  const second = deferred();
  service.getThreadConfig
    .mockReturnValueOnce(first.promise)
    .mockReturnValueOnce(second.promise);

  const { rerender } = renderWithQueryClient(<ComposerModelSelector activeThreadId="thread-a" />);
  await userEvent.click(screen.getByRole('button', { name: /模型/ }));
  rerender(<ComposerModelSelector activeThreadId="thread-b" />);
  second.resolve({ model: 'gpt-5.4', effort: 'medium' });
  first.resolve({ model: 'stale-model', effort: 'low' });

  expect(await screen.findByText(/gpt-5.4/)).toBeInTheDocument();
  expect(screen.queryByText(/stale-model/)).not.toBeInTheDocument();
});
```

Use existing test helpers if they already provide deferred promises. If not, define:

```js
function deferred() {
  let resolve;
  let reject;
  const promise = new Promise((res, rej) => { resolve = res; reject = rej; });
  return { promise, resolve, reject };
}
```

- [ ] **Step 2: Run RED focused test**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 src/pages/chat/components/ComposerModelSelector.test.jsx
```

Expected: FAIL if current manual request refs are not covered or if test harness lacks QueryClientProvider.

- [ ] **Step 3: Replace manual load/save refs with Query**

Use:

```js
const configQuery = useQuery({
  queryKey: ['thread-config', activeThreadId],
  queryFn: () => getThreadConfig({ threadId: activeThreadId }),
  enabled: open && Boolean(activeThreadId),
});

const saveMutation = useMutation({
  mutationFn: (payload) => saveThreadConfig(payload),
  onSuccess: async () => {
    await queryClient.invalidateQueries({ queryKey: ['thread-config', activeThreadId] });
  },
});
```

Remove `mountedRef`, `loadRequestRef`, and render-time `setOpenState` patterns. Keep React Aria `DialogTrigger` / `Popover` / `Dialog`.

- [ ] **Step 4: Preserve Zustand compatibility**

If store fields like `threadConfigLoadingByThread` or `threadConfigSaving` are still consumed outside this component, either:

1. Keep writing those store fields from Query state in a small effect, or
2. Remove their consumers in the same task.

Do not leave duplicated loading sources that can disagree.

- [ ] **Step 5: Run focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/chat/components/ComposerModelSelector.test.jsx \
  src/pages/chat/ChatPage.test.jsx
```

- [ ] **Step 6: Commit**

```bash
git add frontend-app/src/pages/chat/components/ComposerModelSelector.jsx \
  frontend-app/src/pages/chat/components/ComposerModelSelector.test.jsx \
  frontend-app/src/pages/chat/ChatPage.test.jsx
git commit -m "refactor(frontend): 用 Query 管理模型选择器状态"
```

---

## Task 7: Move Approval Submit State To TanStack Mutation

**Files:**
- Modify: `frontend-app/src/pages/chat/components/ChatApprovalMessage.jsx`
- Modify: `frontend-app/src/pages/chat/components/ChatApprovalMessage.test.jsx`
- Check: `frontend-app/src/pages/shared/pageShared.js`

- [ ] **Step 1: Add RED mutation behavior tests**

Add tests for timeout, retry, and success resolved state:

```jsx
it('keeps approval retryable after a timed out mutation', async () => {
  vi.useFakeTimers();
  submitApproval.mockReturnValue(new Promise(() => {}));
  render(<ChatApprovalMessage approval={approval} onError={onError} />);
  await userEvent.click(screen.getByRole('button', { name: /批准/ }));
  await vi.advanceTimersByTimeAsync(15000);
  expect(onError).toHaveBeenCalledWith('approval.failed', expect.stringContaining('超时'));
  expect(screen.getByRole('button', { name: /批准/ })).not.toBeDisabled();
});

it('marks approval resolved after a successful mutation', async () => {
  submitApproval.mockResolvedValue({ ok: true });
  render(<ChatApprovalMessage approval={approval} />);
  await userEvent.click(screen.getByRole('button', { name: /批准/ }));
  expect(await screen.findByText(/已处理|已批准/)).toBeInTheDocument();
});
```

- [ ] **Step 2: Run RED focused test**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 src/pages/chat/components/ChatApprovalMessage.test.jsx
```

- [ ] **Step 3: Implement `useMutation`**

Use:

```js
const approvalMutation = useMutation({
  mutationFn: ({ decision }) => withTimeout(
    submitApproval({ decision }),
    APPROVAL_SUBMIT_TIMEOUT_MS,
    'approval submit timed out',
  ),
  onSuccess: () => {
    setResolved(true);
    setErrorText('');
  },
  onError: (error) => {
    const message = errorMessage(error);
    setErrorText(message);
    onError?.('approval.failed', message);
  },
});
```

Remove request id bookkeeping if Query mutation state is enough. Keep local `resolved` state because it is display state after the mutation completes.

- [ ] **Step 4: Run focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/chat/components/ChatApprovalMessage.test.jsx \
  src/pages/chat/ChatPage.test.jsx
```

- [ ] **Step 5: Commit**

```bash
git add frontend-app/src/pages/chat/components/ChatApprovalMessage.jsx \
  frontend-app/src/pages/chat/components/ChatApprovalMessage.test.jsx \
  frontend-app/src/pages/chat/ChatPage.test.jsx
git commit -m "refactor(frontend): 用 mutation 管理审批提交"
```

---

## Deferred Items

These were identified but should not be included in this repair train:

- `ThreadCard.jsx` / `ThreadRail.jsx` inline rename with React Aria `TextField`: lower benefit and blur-save behavior is easy to regress.
- `useTimelineMaterialization.js` with `@tanstack/react-virtual`: valuable but high side effect on scroll anchoring and old-message loading.
- `SkillsPage.jsx` full schema extraction: likely valuable, but the file is very large and should get its own split plan.
- `WorkflowPage.jsx` schema extraction and render-time state cleanup: very high value, but too large for this wave; create a dedicated workflow plan first.
- `FilesPage.jsx` JSON-like summary helpers: mostly UX display heuristics; no mature dependency currently installed that justifies replacement.
- `ObservabilityPage.jsx` date formatting with a date library: no date library is installed; do not add one only for this.

---

## Final Verification

After all non-deferred tasks:

- [ ] **Step 1: LSP diagnostics**

Run LSP diagnostics for all touched JS/JSX files. Hints, Information, Warnings, and Errors must be fixed or recorded as blockers with tool/action/work_dir/target/error/narrowing attempts.

- [ ] **Step 2: Full frontend validation**

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

- [ ] **Step 3: Stop gate fixture**

```bash
tmp=$(mktemp)
printf 'frontend-app/src/shared/api/backendSchemas.js\n' > "$tmp"
CODEX_STOP_GATE_CHANGED_FILES_FILE="$tmp" \
  CODEX_STOP_GATE_LOG_DIR=$(mktemp -d) \
  bash scripts/codex_stop_gate.sh
rm -f "$tmp"
```

Expected: frontend size guard, frontend lint, and frontend package tests all pass.

- [ ] **Step 4: Desktop smoke**

```bash
cd frontend-app
SUPER_DOLPHIN_HTTP_ADDR=127.0.0.1:4533 \
VITE_DEV_URL=http://127.0.0.1:5196 \
FRONTEND_DEVSERVER_URL=http://127.0.0.1:5196 \
GO_AGENT_CTL_RPC_ADDR=127.0.0.1:8113 \
SUPER_DOLPHIN_DESKTOP_SMOKE_WS_URL=ws://127.0.0.1:5196/wails/ws \
npm run smoke:desktop:rpc
```

Expected: `desktop smoke passed`.

---

## Suggested Commit Order

1. `refactor(frontend): 用 zod 收敛观测响应解析`
2. `refactor(frontend): 用 zod 收敛记忆快照 DTO`
3. `refactor(frontend): 用 zod 收敛共享文件仪表盘`
4. `refactor(frontend): 用 React Aria 收敛聊天操作菜单`
5. `refactor(frontend): 用 React Aria 收敛项目选择器`
6. `refactor(frontend): 用 Query 管理模型选择器状态`
7. `refactor(frontend): 用 mutation 管理审批提交`

---

## Execution Options

1. **子代理驱动（推荐）** - 每个 task 使用新子代理执行，主会话在任务之间复核 diff、验证结果和 LSP blocker。
2. **当前会话内执行** - 使用 `执行计划` 在当前会话按任务顺序推进，每完成一个 task 跑 focused tests 并提交。
