# Frontend Mature Replacement Remaining Wave Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the remaining frontend “去手写轮子化” candidates by replacing narrow hand-written parsing, focus refresh, query state, and popover/lightbox behavior with mature dependencies already present in `frontend-app`.

**Architecture:** Do this as one coordinated wave but not one mixed refactor. Each candidate remains an independently reviewable stage with its own focused tests, diagnostics, and atomic commit; main-session integration runs the full frontend guard suite only after all stage commits are locally green. UI replacements use `react-aria-components`, response parsing uses `zod`, RPC state uses TanStack Query, cron reading uses `cron-parser`, and guard parsing uses an AST parser only where the current tree has an executable script.

**Tech Stack:** React 19, Vite, Vitest, zod 4, TanStack Query 5, react-aria-components, cron-parser 4.9, TypeScript compiler API or Babel-compatible AST parsing already available to frontend scripts.

**Verification Surface:** LSP grep/definition/references/read/diagnostics for touched files, focused Vitest suites, `npm run guard:critical-skip`, `npm run typecheck:contracts`, `npm run audit:rpc-contracts`, `npm run lint`, `npm test`, `npm run build`, and codemap/project-map checks only when generated docs or maps are touched.

---

## Current Evidence

The current working tree has unrelated dirty files. Implementers must not revert or restage those changes. Use precise `git add <path>` for each stage and avoid `git add .`.

Dependency evidence from `frontend-app/package.json`:

```text
@tanstack/react-query
cron-parser
react-aria-components
typescript
zod
```

LSP evidence collected against the `origin/main` worktree before implementation:

```text
grep: ImageLightbox references are in MarkdownImagePreview.jsx and MermaidDiagram.jsx.
grep: frontend-code-size-guard.mjs and its tests are present in frontend-app/scripts.
definition/xref: useSettingsRuntime is in settingsRuntimeHook.js and used by SettingsPage.jsx.
definition/xref: useProviderPreferences is in settingsProviderPreferencesRuntime.js and used by SettingsPage.jsx.
definition/xref: usePromptRefreshEffects is internal to PromptPageView.jsx.
definition/xref: scheduleStateFromCron is in workflowScheduleModel.js and used by WorkflowNodeEditorPanels.jsx and workflowDagModel.js.
definition/xref: RuntimeActivityPanel is consumed by RuntimePanel.jsx and RuntimePanelComponents.test.jsx.
read_file: parseCronScheduleParts currently hand-parses five cron fields in workflowScheduleModel.js.
read_file: ImageLightbox currently uses createPortal + native <dialog open>.
read_file: RuntimeActivityPanel currently owns manual outside-click/Escape popup dismissal.
read_file: normalizeDatasourceDocument currently hand-validates datasource rows.
read_file: rpc-contract-audit.mjs currently extracts RPC_METHODS with regex.
diagnostics: touched candidate files returned no LSP diagnostics before this plan.
```

Important path corrections for this code tree:

```text
frontend-app/src/pages/chat/markdown/ImageLightbox.jsx
frontend-app/src/pages/chat/runtime/RuntimeActivityPanel.jsx
frontend-app/src/pages/chat/runtime/RuntimeActivityStats.jsx
frontend-app/src/pages/chat/runtime/RuntimeActivityLog.jsx
frontend-app/src/features/prompts/PromptPageView.jsx
frontend-app/src/pages/settings/SettingsPage.jsx
frontend-app/src/pages/settings/settingsRuntimeHook.js
frontend-app/src/pages/settings/settingsProviderPreferencesRuntime.js
frontend-app/src/pages/workflows/WorkflowPage.jsx
frontend-app/src/pages/workflows/services/workflowScheduleModel.js
frontend-app/src/pages/skills/SkillsPage.jsx
frontend-app/scripts/rpc-contract-audit.mjs
frontend-app/scripts/frontend-code-size-guard.mjs
```

The root working tree may be older than this detached worktree. Use `.worktrees/frontend-mature-remaining-wave-20260709` as the implementation truth source for this wave.

## Wave Order

This wave can be implemented by parallel agents, but integration must be serial:

1. Plan review arbitration.
2. Workflow cron `cron-parser` replacement in the existing schedule model.
3. Image lightbox React Aria Modal replacement.
4. Prompt focus refresh cleanup.
5. Skills datasource DTO zod normalization.
6. Runtime activity popovers React Aria replacement.
7. Settings runtime/preferences Query migration.
8. `rpc-contract-audit` frontend JS AST parsing.
9. `frontend-code-size-guard` AST shadow metrics.
10. Full frontend validation and remote sync.

The first four implementation stages are low-risk enough to run in parallel because they touch disjoint files. Stages 5-8 should still use independent agents, but main-session integration must inspect diffs before staging because Settings/Skills/Guard changes have broader behavior implications.

## Parallel Agent Review Gate

Before editing implementation files, dispatch review-only agents with these scopes:

```text
Agent A: Workflow cron model and cron-parser semantics.
Agent B: ImageLightbox and RuntimeActivityPanel React Aria migration risks.
Agent C: Prompt focus and Settings Query semantics.
Agent D: Skills datasource zod boundary, rpc-contract-audit AST parsing, and frontend-code-size-guard AST shadow parsing.
```

Each agent must return:

```text
Scope reviewed
Files inspected
Implementation decision: approve / amend / block
Required tests
Known side effects
Files that must not be touched
```

The main session updates this plan if any agent finds a real blocker. If an agent only requests broad refactors, reject that part unless it directly protects a listed behavior.

---

## Task 1: Use cron-parser In Existing Workflow Schedule Model

**Files:**
- Modify: `frontend-app/src/pages/workflows/services/workflowScheduleModel.js`
- Test: `frontend-app/src/pages/workflows/WorkflowPage.edge.test.jsx`
- Test: `frontend-app/src/pages/workflows/WorkflowPage.runtime.test.jsx`
- Test: `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`

**Side effects to preserve:**
- Keep generated cron expressions in the existing wire form: `CRON_TZ=Asia/Shanghai <minute> <hour> <day-of-month> <month> <day-of-week>`.
- Keep five-field cron input after stripping an inline `CRON_TZ=` prefix.
- Keep unsupported but syntactically valid ranges such as `*/15 9 * * 1-5` as range-warning UI state, not as a selected preset.
- Do not call `parser.next()` in page rendering; the page only needs shape validation and readable schedule state.

- [ ] **Step 1: Add pure model tests through existing workflow suites**

There is no standalone `workflowScheduleModel.test.js` in this tree. Add direct model assertions to the smallest existing workflow test file that already covers schedule behavior, preferably `frontend-app/src/pages/workflows/WorkflowPage.edge.test.jsx` if it already imports pure helpers, otherwise create `frontend-app/src/pages/workflows/services/workflowScheduleModel.test.js` and keep it model-only:

```js
import { describe, expect, it } from 'vitest';
import {
  cronExprFromSchedule,
  scheduleLabelFromCron,
  scheduleStateFromCron,
} from './workflowScheduleModel.js';

describe('workflowScheduleModel', () => {
  it('parses daily cron with the backend timezone prefix', () => {
    expect(scheduleStateFromCron('CRON_TZ=Asia/Shanghai 5 9 * * *')).toMatchObject({
      preset: 'daily',
      time: '09:05',
      warning: '',
    });
    expect(scheduleLabelFromCron('CRON_TZ=Asia/Shanghai 5 9 * * *')).toBe('每天 09:05');
  });

  it('parses weekday cron with the backend timezone prefix', () => {
    expect(scheduleStateFromCron('CRON_TZ=Asia/Shanghai 0 5 * * 1-5')).toMatchObject({
      preset: 'weekdays',
      time: '05:00',
      warning: '',
    });
  });

  it('keeps complex but valid cron outside the supported preset range', () => {
    expect(scheduleStateFromCron('CRON_TZ=Asia/Shanghai */15 9 * * 1-5')).toMatchObject({
      warning: '当前计划使用了暂不支持的 cron 范围，请重新选择运行频率。',
    });
  });

  it('rejects malformed or six-field cron expressions with the existing warning copy', () => {
    expect(scheduleStateFromCron('CRON_TZ=Asia/Shanghai 0 5 * * * *')).toMatchObject({
      warning: '当前计划使用了暂不支持的 cron 格式，请重新选择运行频率。',
    });
    expect(scheduleStateFromCron('CRON_TZ=Asia/Shanghai nope 5 * * *')).toMatchObject({
      warning: '当前计划使用了暂不支持的 cron 格式，请重新选择运行频率。',
    });
  });

  it('continues generating the backend cron format without cron-parser formatting', () => {
    expect(cronExprFromSchedule('weekly', '07:30', '3', '1')).toEqual({
      cronExpr: 'CRON_TZ=Asia/Shanghai 30 7 * * 3',
      error: '',
    });
  });
});
```

- [ ] **Step 2: Run RED tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/workflows/services/workflowScheduleModel.test.js \
  src/pages/workflows/WorkflowPage.runtime.test.jsx
```

Expected: FAIL until `cron-parser` syntax validation is wired into the existing model, or PASS for already-covered legacy cases and FAIL for the new complex-cron cases.

- [ ] **Step 3: Add cron-parser validation to the existing model**

In `frontend-app/src/pages/workflows/services/workflowScheduleModel.js`, import `cron-parser` without moving the model again:

```js
import parser from 'cron-parser';
```

Use `parser.parseExpression(cronText, { tz: timezone })` only to validate syntax after stripping `CRON_TZ=...`. Keep the existing preset matching rules for supported UI states:

```js
function parseCronScheduleParts(cronExpr) {
  const { cronText, timezone } = cronSchedulePartsWithTimezone(cronExpr);
  if (!cronText) return { empty: true };
  const parts = cronText.split(/\s+/);
  if (parts.length !== 5) return { error: DAG_SCHEDULE_FORMAT_WARNING };
  try {
    parser.parseExpression(cronText, { tz: timezone });
  } catch {
    return { error: DAG_SCHEDULE_FORMAT_WARNING };
  }
  const [minuteText, hourText, dayOfMonth, month, dayOfWeek] = parts;
  const hour = Number(hourText);
  const minute = Number(minuteText);
  if (!Number.isInteger(hour) || !Number.isInteger(minute) || hour < 0 || hour > MAX_SCHEDULE_HOUR || minute < 0 || minute > MAX_SCHEDULE_MINUTE) {
    return { error: DAG_SCHEDULE_FORMAT_WARNING };
  }
  return { minute, hour, dayOfMonth, month, dayOfWeek, time: `${twoDigits(hour)}:${twoDigits(minute)}`, timezone };
}
```

Keep existing exports intact. Do not move helpers back into `WorkflowPage.jsx`.

- [ ] **Step 5: Run focused workflow tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/workflows/services/workflowScheduleModel.test.js \
  src/pages/workflows/WorkflowPage.runtime.test.jsx \
  src/pages/workflows/WorkflowPage.edge.test.jsx \
  src/pages/workflows/WorkflowPage.test.jsx
```

Expected: PASS.

- [ ] **Step 6: LSP diagnostics**

Run diagnostics for:

```text
frontend-app/src/pages/workflows/services/workflowScheduleModel.js
frontend-app/src/pages/workflows/components/WorkflowNodeEditorPanels.jsx
```

Fix every Error, Warning, Information, and Hint.

- [ ] **Step 7: Commit**

```bash
git add frontend-app/src/pages/workflows/services/workflowScheduleModel.js \
  frontend-app/src/pages/workflows/services/workflowScheduleModel.test.js \
  frontend-app/src/pages/workflows/WorkflowPage.runtime.test.jsx \
  frontend-app/src/pages/workflows/WorkflowPage.edge.test.jsx \
  frontend-app/src/pages/workflows/WorkflowPage.test.jsx
git commit -m "refactor(frontend): 用 cron-parser 收敛工作流计划解析"
```

---

## Task 2: Replace ImageLightbox Native dialog With React Aria Modal

**Files:**
- Modify: `frontend-app/src/pages/chat/markdown/ImageLightbox.jsx`
- Modify: `frontend-app/src/pages/chat/markdown/MarkdownMessage.test.jsx`
- Modify: `frontend-app/src/pages/chat/markdown/MermaidDiagram.test.jsx`
- Test: `frontend-app/src/pages/chat/thread/TimelineMessage.test.jsx`

**Side effects to preserve:**
- `onClose` fires when the backdrop, close button, or Escape closes the lightbox.
- Markdown image preview and Mermaid preview continue sharing the same lightbox component.
- The close button keeps the `关闭图片预览` accessible name.
- Do not introduce another UI framework.

- [ ] **Step 1: Add focus and dismiss tests**

Extend `MarkdownMessage.test.jsx` around the existing lightbox test:

```jsx
it('closes the image lightbox with Escape and returns control to the preview trigger', async () => {
  const user = userEvent.setup();
  render(<MarkdownMessage content={'![demo](app://generated-image?path=generated_images/demo.png)'} />);
  const trigger = await screen.findByRole('button', { name: /demo/ });
  await user.click(trigger);
  expect(screen.getByRole('dialog', { name: /图片预览/ })).toBeInTheDocument();
  await user.keyboard('{Escape}');
  expect(screen.queryByRole('dialog', { name: /图片预览/ })).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
});
```

Extend `MermaidDiagram.test.jsx` to assert the shared dialog closes through the backdrop or Escape:

```jsx
expect(screen.getByRole('dialog', { name: /图片预览/ })).toBeInTheDocument();
await user.keyboard('{Escape}');
expect(screen.queryByRole('dialog', { name: /图片预览/ })).not.toBeInTheDocument();
```

- [ ] **Step 2: Run RED focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/chat/markdown/MarkdownMessage.test.jsx \
  src/pages/chat/markdown/MermaidDiagram.test.jsx
```

Expected: the new focus-return assertion may fail against the native `<dialog open>` implementation.

- [ ] **Step 3: Replace implementation**

Update `ImageLightbox.jsx`:

```jsx
import React from 'react';
import { Dialog, Modal, ModalOverlay } from 'react-aria-components';
import { X } from 'lucide-react';

const PREVIEW_LABEL = '预览';
const LIGHTBOX_LABEL_PREFIX = '图片预览：';
const CLOSE_LABEL = '关闭图片预览';

function ImageLightbox({ label, onClose, children }) {
  const displayLabel = (label || '').toString().trim() || PREVIEW_LABEL;
  return (
    <ModalOverlay
      className="image-lightbox"
      isDismissable
      isOpen
      onOpenChange={(isOpen) => {
        if (!isOpen) onClose();
      }}
    >
      <Modal className="image-lightbox-panel-shell">
        <Dialog aria-label={`${LIGHTBOX_LABEL_PREFIX}${displayLabel}`} className="image-lightbox-panel">
          <header>
            <strong>{displayLabel}</strong>
            <div>
              <button type="button" aria-label={CLOSE_LABEL} onClick={onClose}><X size={16} /></button>
            </div>
          </header>
          {children}
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}

export { ImageLightbox };
```

Adjust CSS selectors only if the current `.image-lightbox > .image-lightbox-panel` relationship breaks. Keep class names stable where possible.

- [ ] **Step 4: Run focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/chat/markdown/MarkdownMessage.test.jsx \
  src/pages/chat/markdown/MermaidDiagram.test.jsx \
  src/pages/chat/thread/TimelineMessage.test.jsx
```

Expected: PASS.

- [ ] **Step 5: LSP diagnostics**

Run diagnostics for:

```text
frontend-app/src/pages/chat/markdown/ImageLightbox.jsx
frontend-app/src/pages/chat/markdown/MarkdownImagePreview.jsx
frontend-app/src/pages/chat/markdown/MermaidDiagram.jsx
```

- [ ] **Step 6: Commit**

```bash
git add frontend-app/src/pages/chat/markdown/ImageLightbox.jsx \
  frontend-app/src/pages/chat/markdown/MarkdownMessage.test.jsx \
  frontend-app/src/pages/chat/markdown/MermaidDiagram.test.jsx
git commit -m "refactor(frontend): 用 React Aria 托管图片预览弹窗"
```

---

## Task 3: Remove Hand-Written Prompt Focus Refresh

**Files:**
- Modify: `frontend-app/src/features/prompts/PromptPageView.jsx`
- Modify: `frontend-app/src/features/prompts/PromptPageView.test.jsx`

**Side effects to preserve:**
- `refreshKey` still refreshes prompt assets and active prompt.
- Window focus and visibility refresh should be owned by TanStack Query, not custom DOM listeners.
- Stale active prompt ids must still be hidden when the asset list no longer contains a force-launchable prompt.
- No duplicate manual RPCs on focus.

- [ ] **Step 1: Add tests for Query-owned focus refresh**

Add a focused test in `PromptPageView.test.jsx`:

```jsx
it('uses Query focus refresh without custom duplicate prompt RPCs', async () => {
  const user = userEvent.setup();
  const calls = [];
  mockBackend({
    'prompt-assets/list': async () => {
      calls.push('assets');
      return { items: [] };
    },
    'settings/preference/get': async () => {
      calls.push('active');
      return '';
    },
  });
  renderPromptPageView({ cwd: '/repo' });
  await screen.findByText(/提示词/);
  calls.length = 0;
  window.dispatchEvent(new Event('focus'));
  await waitFor(() => expect(calls.filter((item) => item === 'assets')).toHaveLength(1));
  expect(calls.filter((item) => item === 'active')).toHaveLength(1);
});
```

Add an invalid-active prompt case:

```jsx
it('clears stale active prompt cache after prompt assets refresh', async () => {
  mockBackendSequence({
    'prompt-assets/list': [
      { items: [{ id: 'old', title: 'Old', kind: 'expert', enabled: true }] },
      { items: [] },
    ],
    'settings/preference/get': ['old', 'old'],
  });
  const queryClient = renderPromptPageView({ cwd: '/repo' });
  await screen.findByText('Old');
  await queryClient.invalidateQueries({ queryKey: ['dashboard', 'project', '/repo', 'prompts'], exact: true });
  await waitFor(() => expect(queryClient.getQueryData(['dashboard', 'project', '/repo', 'active-prompt'])).toBe(''));
});
```

Use the actual test helpers in the file; keep the query keys aligned with the implementation.

- [ ] **Step 2: Run RED focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/features/prompts/PromptPageView.test.jsx
```

Expected: FAIL until focus refresh is Query-owned or until the test helper query keys are corrected to the current file constants.

- [ ] **Step 3: Move focus refresh into query options**

In `usePromptQueries`, set explicit Query options:

```js
useQuery({
  queryKey: promptAssetsQueryKey(cwd),
  queryFn: () => fetchPromptAssets(cwd),
  enabled: Boolean(cwd),
  retry: false,
  refetchOnWindowFocus: 'always',
});

useQuery({
  queryKey: activePromptQueryKey(cwd),
  queryFn: () => fetchActivePrompt(cwd),
  enabled: Boolean(cwd),
  retry: false,
  refetchOnWindowFocus: 'always',
});
```

Remove the second `useEffect` in `usePromptRefreshEffects` that manually attaches `window.focus` and `document.visibilitychange`. Keep the `refreshKey` effect, or replace it with exact invalidation:

```js
function usePromptRefreshEffects(promptRefreshKey, queryClient, cwd) {
  useEffect(() => {
    if (promptRefreshKey <= 0 || !cwd) return;
    void queryClient.invalidateQueries({ queryKey: promptAssetsQueryKey(cwd), exact: true });
    void queryClient.invalidateQueries({ queryKey: activePromptQueryKey(cwd), exact: true });
  }, [cwd, promptRefreshKey, queryClient]);
}
```

Keep active prompt cleanup after data changes:

```js
useEffect(() => {
  if (!cwd) return;
  const nextItems = Array.isArray(promptAssetsData?.items) ? promptAssetsData.items : [];
  const nextActiveId = textValue(activePromptData);
  if (nextActiveId && !nextItems.some((item) => item.id === nextActiveId && canForceLaunchPrompt(item))) {
    queryClient.setQueryData(activePromptQueryKey(cwd), '');
  }
}, [activePromptData, cwd, promptAssetsData, queryClient]);
```

- [ ] **Step 4: Run focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/features/prompts/PromptPageView.test.jsx
```

Expected: PASS.

- [ ] **Step 5: LSP diagnostics**

Run diagnostics for:

```text
frontend-app/src/features/prompts/PromptPageView.jsx
frontend-app/src/features/prompts/PromptPageView.test.jsx
```

- [ ] **Step 6: Commit**

```bash
git add frontend-app/src/features/prompts/PromptPageView.jsx \
  frontend-app/src/features/prompts/PromptPageView.test.jsx
git commit -m "refactor(frontend): 交给 Query 刷新提示词焦点数据"
```

---

## Task 4: Move Skills Datasource DTO Normalization To zod

**Files:**
- Modify: `frontend-app/src/pages/skills/SkillsPage.jsx`
- Modify: `frontend-app/src/pages/skills/SkillsPage.test.jsx`

**Side effects to preserve:**
- Preserve `useQuery` and `useInfiniteQuery` behavior.
- Preserve datasource pagination progress guard: `hasMore` without chunks must fail fast.
- Preserve backend alias support: `documentId/document_id/id`, `nextCursor/next_cursor`, snake_case and camelCase response fields.
- Do not include skill tools DTO in this task; current tree has insufficient visible tool UI/test coverage for a safe combined migration.

- [ ] **Step 1: Add malformed datasource tests**

Extend `SkillsPage.test.jsx`:

```jsx
it('fails fast when datasource documents response is malformed', async () => {
  mockSkillsBackend({
    'datasource_v2/list': async () => ({ documents: null }),
  });
  renderSkillsPage({ cwd: '/repo' });
  await screen.findByText(/datasource documents must be an array/);
});

it('normalizes datasource detail aliases through the zod boundary', async () => {
  mockSkillsBackend({
    'datasource_v2/get': async () => ({
      document: { document_id: 9, file_name: 'a.md', chunk_count: 1 },
      chunks: [{ chunk_id: 3, content: 'hello', token_count: 2 }],
      next_cursor: '4',
      has_more: true,
    }),
  });
  renderSkillsPage({ cwd: '/repo' });
  await userEvent.click(await screen.findByRole('button', { name: /a.md/ }));
  expect(await screen.findByText('hello')).toBeInTheDocument();
});
```

Use existing datasource test helper names from the file.

- [ ] **Step 2: Run RED focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/skills/SkillsPage.test.jsx
```

Expected: FAIL until zod errors and aliases are implemented at the datasource boundary.

- [ ] **Step 3: Add local zod schemas**

In `SkillsPage.jsx`, import `z`:

```js
import { z } from 'zod';
```

Add schemas next to datasource normalizers:

```js
const datasourceDocumentSchema = z.object({
  documentId: z.unknown().optional(),
  document_id: z.unknown().optional(),
  id: z.unknown().optional(),
  sourcePath: z.unknown().optional(),
  source_path: z.unknown().optional(),
  fileName: z.unknown().optional(),
  file_name: z.unknown().optional(),
  extension: z.unknown().optional(),
  sizeBytes: z.unknown().optional(),
  size_bytes: z.unknown().optional(),
  contentHash: z.unknown().optional(),
  content_hash: z.unknown().optional(),
  chunkCount: z.unknown().optional(),
  chunk_count: z.unknown().optional(),
  totalChars: z.unknown().optional(),
  total_chars: z.unknown().optional(),
  status: z.unknown().optional(),
  errorMessage: z.unknown().optional(),
  error_message: z.unknown().optional(),
  createdAt: z.unknown().optional(),
  created_at: z.unknown().optional(),
  updatedAt: z.unknown().optional(),
  updated_at: z.unknown().optional(),
}).passthrough().transform((raw) => {
  const documentId = Number(raw.documentId ?? raw.document_id ?? raw.id);
  if (!Number.isInteger(documentId) || documentId <= 0) {
    throw new Error('datasource document is missing documentId');
  }
  return {
    documentId,
    sourcePath: cleanScalar(raw.sourcePath ?? raw.source_path),
    fileName: cleanScalar(raw.fileName ?? raw.file_name),
    extension: cleanScalar(raw.extension),
    sizeBytes: Number(raw.sizeBytes ?? raw.size_bytes ?? 0),
    contentHash: cleanScalar(raw.contentHash ?? raw.content_hash),
    chunkCount: Number(raw.chunkCount ?? raw.chunk_count ?? 0),
    totalChars: Number(raw.totalChars ?? raw.total_chars ?? 0),
    status: cleanScalar(raw.status),
    errorMessage: cleanScalar(raw.errorMessage ?? raw.error_message),
    createdAt: cleanScalar(raw.createdAt ?? raw.created_at),
    updatedAt: cleanScalar(raw.updatedAt ?? raw.updated_at),
  };
});
```

Use `safeParse` wrappers to preserve indexed error messages:

```js
function parseDatasourceDocument(raw, index = 0) {
  const result = datasourceDocumentSchema.safeParse(raw);
  if (!result.success) throw new Error(`datasource document ${index} must be an object`);
  return result.data;
}
```

Add equivalent schemas for list/detail/chunk page. Keep `assertDatasourceChunkPageProgress` after schema normalization.

- [ ] **Step 4: Replace hand-written datasource normalizers**

Replace:

```js
normalizeDatasourceDocument
normalizeDatasourceDocuments
normalizeDatasourceDetail
normalizeDatasourceChunkPage
```

with thin wrappers around the zod schemas. Preserve existing function names to minimize call-site churn.

- [ ] **Step 5: Run focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/skills/SkillsPage.test.jsx
```

Expected: PASS, including datasource CRUD/detail/pagination and `hasMore` fail-fast tests.

- [ ] **Step 6: LSP diagnostics**

Run diagnostics for:

```text
frontend-app/src/pages/skills/SkillsPage.jsx
frontend-app/src/pages/skills/SkillsPage.test.jsx
```

- [ ] **Step 7: Commit**

```bash
git add frontend-app/src/pages/skills/SkillsPage.jsx \
  frontend-app/src/pages/skills/SkillsPage.test.jsx
git commit -m "refactor(frontend): 用 zod 收敛数据源 DTO"
```

---

## Task 5: Replace Runtime Activity Popovers With React Aria

**Files:**
- Modify: `frontend-app/src/pages/chat/runtime/RuntimeActivityPanel.jsx`
- Modify: `frontend-app/src/pages/chat/runtime/RuntimeActivityStats.jsx`
- Modify: `frontend-app/src/pages/chat/runtime/RuntimeActivityLog.jsx`
- Modify: `frontend-app/src/pages/chat/components/RuntimePanelComponents.test.jsx`

**Side effects to preserve:**
- Runtime result and warning popovers must keep redaction behavior.
- Resizer must keep keyboard and pointer behavior.
- Collapsed panel must hide warning log lines and clear warning popover state.
- Opening one popover closes the other.
- Outside interaction must not close the popover when the target is the panel resizer.

- [ ] **Step 1: Add popover behavior tests**

Extend `RuntimePanelComponents.test.jsx`:

```jsx
it('keeps runtime popovers mutually exclusive and dismisses through Escape', async () => {
  const user = userEvent.setup();
  renderRuntimeActivityPanel({ warnings: warningFixtures, runtimeResults: runtimeResultFixtures });
  await user.click(screen.getByRole('button', { name: /上下文/ }));
  expect(screen.getByRole('dialog', { name: /上下文/ })).toBeInTheDocument();
  await user.click(screen.getByRole('button', { name: /警告/ }));
  expect(screen.queryByRole('dialog', { name: /上下文/ })).not.toBeInTheDocument();
  expect(screen.getByRole('dialog', { name: /警告/ })).toBeInTheDocument();
  await user.keyboard('{Escape}');
  expect(screen.queryByRole('dialog', { name: /警告/ })).not.toBeInTheDocument();
});

it('does not close an open runtime popover when dragging the activity resizer', async () => {
  const user = userEvent.setup();
  renderRuntimeActivityPanel({ warnings: warningFixtures });
  await user.click(screen.getByRole('button', { name: /警告/ }));
  fireEvent.pointerDown(screen.getByTestId('activity-panel-resizer'));
  expect(screen.getByRole('dialog', { name: /警告/ })).toBeInTheDocument();
});
```

Use existing fixture names or add minimal local fixtures.

- [ ] **Step 2: Run RED focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/chat/components/RuntimePanelComponents.test.jsx
```

Expected: at least role/focus assertions fail with the current manual tooltip/popover markup.

- [ ] **Step 3: Replace manual document listeners**

In `RuntimeActivityPanel.jsx`, remove `runtimePopupOpenRef` and the `document.addEventListener` effect. Use controlled active keys and pass popover state to child components:

```jsx
const [activePopover, setActivePopover] = useState(null);
const activeStatKey = activePopover?.type === 'stat' ? activePopover.key : null;
const activeWarningId = activePopover?.type === 'warning' ? activePopover.id : null;
```

Use `DialogTrigger`, `Popover`, and `Dialog` from `react-aria-components` in `RuntimeActivityStats.jsx` and `RuntimeActivityLog.jsx`. Keep the visual class names:

```jsx
<DialogTrigger isOpen={isOpen} onOpenChange={(open) => onOpenChange(open ? item.key : null)}>
  <button type="button" className="runtime-stat">...</button>
  <Popover
    className="runtime-stat-tooltip"
    shouldCloseOnInteractOutside={(element) => !element.closest('.activity-panel-resizer')}
  >
    <Dialog aria-label={item.label}>...</Dialog>
  </Popover>
</DialogTrigger>
```

For warnings:

```jsx
<DialogTrigger isOpen={isOpen} onOpenChange={(open) => onOpenChange(open ? entry.id : null)}>
  <button type="button" className="warning-log-line">...</button>
  <Popover
    className="warning-log-popover"
    shouldCloseOnInteractOutside={(element) => !element.closest('.activity-panel-resizer')}
  >
    <Dialog aria-label="警告详情">...</Dialog>
  </Popover>
</DialogTrigger>
```

Preserve the existing redacted content builders; do not move redaction into React Aria handlers.

- [ ] **Step 4: Run focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/chat/components/RuntimePanelComponents.test.jsx \
  src/pages/chat/runtime/RuntimePanelSlot.test.jsx
```

Expected: PASS.

- [ ] **Step 5: LSP diagnostics**

Run diagnostics for:

```text
frontend-app/src/pages/chat/runtime/RuntimeActivityPanel.jsx
frontend-app/src/pages/chat/runtime/RuntimeActivityStats.jsx
frontend-app/src/pages/chat/runtime/RuntimeActivityLog.jsx
```

- [ ] **Step 6: Commit**

```bash
git add frontend-app/src/pages/chat/runtime/RuntimeActivityPanel.jsx \
  frontend-app/src/pages/chat/runtime/RuntimeActivityStats.jsx \
  frontend-app/src/pages/chat/runtime/RuntimeActivityLog.jsx \
  frontend-app/src/pages/chat/components/RuntimePanelComponents.test.jsx
git commit -m "refactor(frontend): 用 React Aria 托管运行面板弹层"
```

---

## Task 6: Move Settings Runtime Preferences To TanStack Query

**Files:**
- Modify: `frontend-app/src/pages/settings/settingsRuntimeHook.js`
- Modify: `frontend-app/src/pages/settings/settingsProviderPreferencesRuntime.js`
- Modify: `frontend-app/src/pages/settings/SettingsPage.jsx` only if hook signatures change.
- Modify: `frontend-app/src/pages/settings/SettingsPage.test.jsx`

**Side effects to preserve:**
- App update mutations can remain as existing `useMutation`.
- Dirty provider drafts must not be overwritten by background refetch.
- Active provider remains codex-only where current behavior enforces it.
- Scoped/global fallback and tombstone behavior must remain visible in tests.
- Query focus behavior must be explicit; do not inherit accidental global focus refetch if it would overwrite form state.

- [ ] **Step 1: Add settings query regression tests**

Extend `SettingsPage.test.jsx`:

```jsx
it('does not reload runtime preferences on window focus when a runtime form is dirty', async () => {
  const user = userEvent.setup();
  const reads = [];
  mockSettingsBackend({
    'settings/preference/get': async (payload) => {
      reads.push(payload.key);
      return payload.key === 'stallThresholdSec' ? '30' : '';
    },
  });
  renderSettingsPage({ cwd: '/repo' });
  const threshold = await screen.findByLabelText(/卡顿阈值/);
  await user.clear(threshold);
  await user.type(threshold, '45');
  reads.length = 0;
  window.dispatchEvent(new Event('focus'));
  await waitFor(() => expect(threshold).toHaveValue(45));
  expect(reads).toEqual([]);
});
```

Keep or strengthen the existing dirty model provider draft test around `SettingsPage.test.jsx`.

- [ ] **Step 2: Run RED focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/settings/SettingsPage.test.jsx
```

Expected: FAIL until runtime preferences use explicit Query options or until the test helper locators are aligned.

- [ ] **Step 3: Add runtime preference query keys**

In `settingsRuntimeHook.js` and `settingsProviderPreferencesRuntime.js`, import Query hooks where they are needed:

```js
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
```

Add query keys near settings constants:

```js
function settingsRuntimePreferencesQueryKey(cwd) {
  return ['settings', 'runtime-preferences', optionalSettingsCwd(cwd)];
}

function settingsProviderPreferencesQueryKey(cwd, provider) {
  return ['settings', 'provider-preferences', optionalSettingsCwd(cwd), normalizeRuntimeProviderName(provider)];
}
```

- [ ] **Step 4: Replace request sequence for runtime preferences**

Inside `useSettingsRuntime` in `settingsRuntimeHook.js`, replace `preferenceRequestSeq`, `nextPreferenceRequest`, and `useEffect(() => { void loadPreferences(); }, ...)` with:

```js
const runtimePreferencesQuery = useQuery({
  queryKey: settingsRuntimePreferencesQueryKey(cwd),
  queryFn: () => readRuntimePreferences(cwd),
  enabled: Boolean(optionalSettingsCwd(cwd)),
  retry: false,
  refetchOnWindowFocus: false,
});

useEffect(() => {
  if (runtimePreferencesQuery.error) {
    setError(runtimePreferencesQuery.error.message || String(runtimePreferencesQuery.error));
    return;
  }
  if (runtimePreferencesQuery.data) {
    setForm((current) => settingsFormWithRuntimePreferences(current, runtimePreferencesQuery.data));
  }
}, [runtimePreferencesQuery.data, runtimePreferencesQuery.error]);
```

Use small pure helpers:

```js
async function readRuntimePreferences(cwd) {
  const values = await Promise.all([
    getPreference({ cwd, key: SETTINGS_KEYS.stallThreshold }),
    getPreference({ cwd, key: SETTINGS_KEYS.contextThresholds }),
    getPreference({ cwd, key: SETTINGS_KEYS.activeProvider }),
  ]);
  return normalizeRuntimePreferences(values);
}
```

Keep form update and save handlers local.

- [ ] **Step 5: Migrate provider preferences only after runtime tests pass**

Use a separate internal commit checkpoint if runtime migration is hard to review. Convert `useProviderPreferences` to `useQuery` with:

```js
useQuery({
  queryKey: settingsProviderPreferencesQueryKey(cwd, activeProvider),
  queryFn: () => readProviderRuntimePreferences(cwd, activeProvider),
  enabled: Boolean(optionalSettingsCwd(cwd) && activeProvider),
  retry: false,
  refetchOnWindowFocus: false,
});
```

When query data arrives, only apply it if the provider form is not dirty. Use the same dirty predicate currently protected by `SettingsPage.test.jsx`.

- [ ] **Step 6: Run focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  src/pages/settings/SettingsPage.test.jsx \
  src/pages/settings/components/ModelProvidersCard.test.jsx
```

Expected: PASS.

- [ ] **Step 7: LSP diagnostics**

Run diagnostics for:

```text
frontend-app/src/pages/settings/SettingsPage.jsx
frontend-app/src/pages/settings/settingsRuntimeHook.js
frontend-app/src/pages/settings/settingsProviderPreferencesRuntime.js
frontend-app/src/pages/settings/SettingsPage.test.jsx
```

- [ ] **Step 8: Commit**

```bash
git add frontend-app/src/pages/settings/settingsRuntimeHook.js \
  frontend-app/src/pages/settings/settingsProviderPreferencesRuntime.js \
  frontend-app/src/pages/settings/SettingsPage.jsx \
  frontend-app/src/pages/settings/SettingsPage.test.jsx
git commit -m "refactor(frontend): 用 Query 管理设置偏好读取"
```

---

## Task 7: Replace rpc-contract-audit Frontend JS Regex Parsing With AST

**Files:**
- Modify: `frontend-app/scripts/rpc-contract-audit.mjs`
- Modify: `frontend-app/scripts/rpc-contract-audit.test.mjs`

**Side effects to preserve:**
- Keep existing CLI output and exit behavior.
- Keep Go handler and Go struct scanning unchanged in this task.
- Do not change `backendApi.contractMatrix.js` or validator policy semantics.
- Add AST parsing under shadow parity first, then remove regex path after tests cover parity.

- [ ] **Step 1: Add multiline and computed-shape fixtures**

Extend `rpc-contract-audit.test.mjs`:

```js
it('extracts multiline frontend RPC methods and contract entries with AST parsing', () => {
  const methods = parseRpcMethodsForTest(`
    export const RPC_METHODS = Object.freeze({
      THREAD_START:
        'thread/start',
      TURN_START: 'turn/start',
    })
  `);
  expect(methods).toEqual([
    { key: 'THREAD_START', method: 'thread/start' },
    { key: 'TURN_START', method: 'turn/start' },
  ]);

  const entries = parseContractMatrixForTest(`
    export const BACKEND_CONTRACT_MATRIX = Object.freeze({
      THREAD_START: {
        method: RPC_METHODS.THREAD_START,
        responseValidator: parseThreadStartResponse,
      },
    })
  `);
  expect(entries).toEqual([
    { key: 'THREAD_START', methodKey: 'THREAD_START', hasResponseValidator: true },
  ]);
});
```

Add payload builder fixture:

```js
it('extracts payload keys from nested facade builders without splitTopLevelArguments regex', () => {
  const keys = extractPayloadBuilderConsumedKeysForTest(`
    function threadStartPayload(input) {
      return cleanObject({
        cwd: input.cwd,
        promptKey: normalize(input.promptKey),
        nested: { ignored: input.notPayload },
      })
    }
  `, 'threadStartPayload');
  expect([...keys].sort()).toEqual(['cwd', 'promptKey']);
});
```

- [ ] **Step 2: Run RED guard tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  scripts/rpc-contract-audit.test.mjs
```

Expected: FAIL until exported-for-test AST helpers exist.

- [ ] **Step 3: Add AST parse helper**

Use TypeScript compiler API because `typescript` is already a dev dependency:

```js
import ts from 'typescript';

function parseJavaScriptSource(source, fileName = 'source.js') {
  return ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
}
```

Replace `parseRpcMethods` regex with AST traversal:

```js
function parseRpcMethods(source) {
  const ast = parseJavaScriptSource(source, 'backendApi.js');
  const objectLiteral = findExportedObjectFreeze(ast, 'RPC_METHODS');
  if (!objectLiteral) throw new Error('RPC_METHODS object was not found in backendApi.js');
  return objectLiteral.properties.map((property) => {
    if (!ts.isPropertyAssignment(property) || !ts.isIdentifier(property.name) || !ts.isStringLiteralLike(property.initializer)) {
      throw new Error('RPC_METHODS entries must be identifier keys with string literal methods');
    }
    return { key: property.name.text, method: property.initializer.text };
  });
}
```

Add focused helpers:

```js
function findExportedObjectFreeze(ast, exportName) {
  let found = null;
  const visit = (node) => {
    if (found) return;
    if (ts.isVariableDeclaration(node) && ts.isIdentifier(node.name) && node.name.text === exportName) {
      found = objectLiteralFromObjectFreeze(node.initializer);
      return;
    }
    ts.forEachChild(node, visit);
  };
  visit(ast);
  return found;
}
```

- [ ] **Step 4: Replace contract matrix and payload builder scanning**

Replace:

```text
parseContractMatrix
parseObjectStringProperty
findCallArguments
splitTopLevelArguments
readBalancedParens
```

with AST helpers. Keep hardcoded payload guard text scanning if it intentionally searches source text for banned fields in non-parser contexts.

- [ ] **Step 5: Run focused guard tests and audit**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 scripts/rpc-contract-audit.test.mjs
npm run audit:rpc-contracts
```

Expected: PASS and the audit report remains semantically equivalent.

- [ ] **Step 6: LSP diagnostics**

Run diagnostics for:

```text
frontend-app/scripts/rpc-contract-audit.mjs
frontend-app/scripts/rpc-contract-audit.test.mjs
```

- [ ] **Step 7: Commit**

```bash
git add frontend-app/scripts/rpc-contract-audit.mjs \
  frontend-app/scripts/rpc-contract-audit.test.mjs
git commit -m "refactor(frontend): 用 AST 收敛 RPC 契约审计"
```

---

## Task 8: Add frontend-code-size-guard AST Shadow Metrics

**Files:**
- Modify: `frontend-app/scripts/frontend-code-size-guard.mjs`
- Modify: `frontend-app/scripts/frontend-code-size-guard.test.mjs`

**Side effects to preserve:**
- Do not change frozen baseline files in this task.
- Do not change the default pass/fail decision until AST and current metrics match on representative fixtures.
- Keep line-length, effective-line, and comment-marker checks text-based if the current guard intentionally measures text.
- Report any AST/current mismatch as a test failure in fixtures, not as a production guard failure against the full repo until parity is established.

- [ ] **Step 1: Add AST parity fixtures**

Extend `frontend-code-size-guard.test.mjs` with fixtures for cases the current regex/brace scanner can misread:

```js
it('keeps AST shadow metrics aligned for nested callbacks and template braces', () => {
  const lines = [
    'export function outer(value) {',
    '  const text = `literal ${value ? \"{\" : \"}\"}`;',
    '  return [value].map((item) => ({ item, text }));',
    '}',
  ];
  const current = measureFileForTest(lines, { useAstShadow: false });
  const shadow = measureFileForTest(lines, { useAstShadow: true });
  expect(shadow.functions.map((item) => item.name)).toContain('outer');
  expect(shadow.maxNesting).toBe(current.maxNesting);
});

it('counts destructured and rest params through AST shadow parsing', () => {
  const lines = [
    'function acceptsMany({ a, b }, ...rest) {',
    '  return rest.length + a + b;',
    '}',
  ];
  const shadow = measureFileForTest(lines, { useAstShadow: true });
  expect(shadow.maxParams).toBe(2);
});
```

Use actual exported helper names from the test file. If no measurement helper exists, export a test-only helper from the guard script rather than testing the CLI through temp files for every fixture.

- [ ] **Step 2: Run RED guard tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  scripts/frontend-code-size-guard.test.mjs
```

Expected: FAIL until AST shadow helpers are available.

- [ ] **Step 3: Add AST parser helpers**

Use the existing `typescript` dev dependency:

```js
import ts from 'typescript';

function parseSourceFileForMetrics(source, fileName = 'source.js') {
  return ts.createSourceFile(fileName, source, ts.ScriptTarget.Latest, true, ts.ScriptKind.JSX);
}
```

Add shadow functions for:

```text
extractFunctions
measureMaxNesting
countFunctionParams
countExports
console usage / any usage / empty function checks only where AST improves correctness
```

Keep current functions in place. The production guard should call both implementations on touched fixture paths or under a test-only option first.

- [ ] **Step 4: Add parity assertions**

In tests, compare current and AST shadow metrics for representative source snippets and at least one real small frontend file. Fail fast on mismatches with the file path and metric name.

- [ ] **Step 5: Run focused tests**

```bash
cd frontend-app
npx vitest run --no-file-parallelism --maxWorkers=1 \
  scripts/frontend-code-size-guard.test.mjs
node scripts/frontend-code-size-guard.mjs --check
```

Expected: PASS without changing any baseline/freeze files.

- [ ] **Step 6: LSP diagnostics**

Run diagnostics for:

```text
frontend-app/scripts/frontend-code-size-guard.mjs
frontend-app/scripts/frontend-code-size-guard.test.mjs
```

- [ ] **Step 7: Commit**

```bash
git add frontend-app/scripts/frontend-code-size-guard.mjs \
  frontend-app/scripts/frontend-code-size-guard.test.mjs
git commit -m "refactor(frontend): 为代码尺寸守卫加入 AST 影子指标"
```

---

## Task 9: Full Wave Validation And Remote Sync

**Files:**
- No source edits unless validation exposes a regression.
- Generated codemap/project-map files only change if the repository generator says they are stale.

- [ ] **Step 1: Inspect branch and remote**

```bash
git status --short
git fetch origin main
git log --oneline --decorate --max-count=8 --all
```

Expected: implementation commits are on local `main` or the active approved worktree branch. If `origin/main` moved, rebase before final validation.

- [ ] **Step 2: Run full frontend validation**

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: PASS.

- [ ] **Step 3: Run guard commands explicitly when touched**

```bash
cd frontend-app
npm run guard:critical-skip
npm run typecheck:contracts
npm run audit:rpc-contracts
```

Expected: PASS. The explicit audit command is mandatory because Task 7 touches the audit implementation.

- [ ] **Step 4: Run codemap/project-map checks when generated docs changed**

Use the repository-native codemap/project-map commands already used in the previous wave. If no docs/codemap generated files changed, record that these checks were not applicable for this wave.

- [ ] **Step 5: Push**

```bash
git push origin main
```

Expected: remote `main` fast-forwards. If push is rejected, run `git fetch origin main`, rebase, rerun full validation, then push.

---

## Completion-Gated Follow-Up Candidates

These are intentionally not mixed into this wave:

1. **Skill tools DTO zod normalization**
   - Current safe subset is datasource-only. Tools DTO should wait until `listSkillTools` UI reachability and tests are explicit.

2. **Settings prompt/runtime hook extraction**
   - Task 6 uses the existing `settingsRuntimeHook.js` and `settingsProviderPreferencesRuntime.js` files. Further hook/file reshaping should wait until Query migration tests pass.

3. **RPC contract Go-side AST parsing**
   - Task 7 only replaces frontend JS parsing. Go handler and struct parsing should be planned separately with Go/LSP evidence.

## Atomic Commit Ledger

Expected commit sequence:

```text
docs(frontend): 记录剩余成熟依赖替换计划
refactor(frontend): 用 cron-parser 收敛工作流计划解析
refactor(frontend): 用 React Aria 托管图片预览弹窗
refactor(frontend): 交给 Query 刷新提示词焦点数据
refactor(frontend): 用 zod 收敛数据源 DTO
refactor(frontend): 用 React Aria 托管运行面板弹层
refactor(frontend): 用 Query 管理设置偏好读取
refactor(frontend): 用 AST 收敛 RPC 契约审计
refactor(frontend): 为代码尺寸守卫加入 AST 影子指标
```

Do not squash these commits. If any stage requires unrelated repair work, create a separate fix commit before continuing the next stage.
