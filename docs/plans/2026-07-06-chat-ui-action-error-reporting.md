# Chat UI Action Error Reporting Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent chat UI action failures from being silently swallowed.

**Architecture:** Keep the fix at the chat action wrapper boundary so all existing chat call sites inherit the same reporting behavior. Reuse `frontend-app/src/shared/ui/runUIAction.js`, which already logs failures and supports an `onError` callback.

**Tech Stack:** React/Vite frontend, Vitest.

**Verification Surface:** `frontend-app/src/pages/chat/components/chatUiActions.test.jsx`, `frontend-app/src/shared/ui/runUIAction.test.js`, `frontend-app` lint/test/build, LSP diagnostics for modified files.

---

### Task 1: Lock Chat Action Failure Reporting

**Files:**
- Create: `frontend-app/src/pages/chat/components/chatUiActions.test.jsx`
- Modify: `frontend-app/src/pages/chat/components/chatUiActions.js`

- [x] **Step 1: Write the failing test**

Add tests proving the chat wrapper reports synchronous and asynchronous failures through the same logger/onError contract as shared UI actions:

```js
import { expect, it, vi } from 'vitest';
import { runUIAction } from './chatUiActions.js';

it('reports synchronous chat UI action failures', () => {
  const onError = vi.fn();
  const logger = vi.fn();
  const error = new Error('chat sync boom');

  runUIAction(() => {
    throw error;
  }, { onError, logger });

  expect(onError).toHaveBeenCalledWith(error);
  expect(logger).toHaveBeenCalledWith('[frontend-app] UI action failed', error);
});

it('reports asynchronous chat UI action failures', async () => {
  const onError = vi.fn();
  const logger = vi.fn();
  const error = new Error('chat async boom');

  runUIAction(Promise.reject(error), { onError, logger });
  await Promise.resolve();

  expect(onError).toHaveBeenCalledWith(error);
  expect(logger).toHaveBeenCalledWith('[frontend-app] UI action failed', error);
});
```

- [x] **Step 2: Run test to verify it fails**

Run:

```bash
cd frontend-app
npx vitest run src/pages/chat/components/chatUiActions.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: FAIL because the current chat wrapper ignores the reporting options and swallows the error.

Actual: FAIL. Both tests failed with `Number of calls: 0` for `onError`.

- [x] **Step 3: Write minimal implementation**

Replace the local swallowing implementation with the shared reporting implementation:

```js
export { runUIAction } from '../../../shared/ui/runUIAction.js';
```

- [x] **Step 4: Run focused tests**

Run:

```bash
cd frontend-app
npx vitest run src/pages/chat/components/chatUiActions.test.jsx src/shared/ui/runUIAction.test.js --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

Actual: PASS. `2 passed (2)`, `4 passed (4)`.

- [x] **Step 5: Run full verification**

Run:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all commands exit 0.

Actual:
- `npm run lint`: PASS.
- `npm test`: PASS, `82 passed (82)`, `1034 passed (1034)`.
- `npm run build`: PASS.
- `git diff --check`: PASS.
- LSP diagnostics: `chatUiActions.test.jsx` returned 0 diagnostics; `chatUiActions.js` diagnostics timed out after retries, while focused tests, lint, full tests, and build passed.
