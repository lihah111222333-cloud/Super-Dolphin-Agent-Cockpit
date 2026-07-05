# Composer Send Failure Draft Cache Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Preserve the failed chat draft and attachments when a send fails after the user has already navigated to another thread.

**Architecture:** The composer store already snapshots per-thread drafts in `composerDrafts`. On rollback, keep the current active composer untouched, and if the failed request cannot restore into the visible composer, write the failed snapshot into the original thread/new-chat draft cache.

**Tech Stack:** React, Zustand, Vitest, frontend-app store slices.

**Verification Surface:** `frontend-app/src/entities/client/model/useClientStore.test.js`, `frontend-app/src/entities/client/model/useClientStore.js`, `frontend-app/src/entities/client/model/composerSlice.js`, full `frontend-app` lint/test/build.

---

## 审查范围

- `frontend-app/src/entities/client/model/useClientStore.js`
- `frontend-app/src/entities/client/model/composerSlice.js`
- `frontend-app/src/entities/client/model/composerAttachments.js`
- `frontend-app/src/entities/client/model/useClientStore.test.js`
- 并行审查覆盖 API、workflow、runtime panel、bootstrap/memory/chat/files/skills/bridge 等前端生产风险面。

## 证据摘要

- `createSendDraftRequest()` 在发送前记录 `previousDraft` 和 `previousAttachments`。
- `optimisticSendDraftState()` 会清空当前 composer。
- `rollbackSendDraftState()` 只有当当前 `activeThreadId` 仍属于失败请求时才恢复 composer。
- 现有测试只断言失败不会覆盖切换后的当前 composer，没有断言失败草稿可以切回恢复。

## 最终裁决

交叉裁决 5 个 agent 中 3 个选择该问题为 r40 唯一最优修复点。理由：这是核心聊天输入的数据丢失，影响主工作流，修复边界小，回归测试可稳定复现。

## 唯一最优修复

在发送失败 rollback 后，如果未恢复到可见 composer，则把失败请求的 `previousDraft` 和 `previousAttachments` 保存到原始 `previousActiveThreadId` 对应的 composer draft cache。用户切回原线程或新聊天时应恢复该失败草稿。

## 被拒绝的候选修复

- API P0/P1 响应验证门禁：有上层防御价值，但范围较广，适合后续单独修复。
- Workflow DAG malformed success：可验证，但不如主聊天输入丢失直接。
- Code preview save race：真实数据风险，但影响文件预览编辑，低于主 composer。
- Memory target fallback、scoped cwd、bridge patch、FilesPage preview、Skills partial failure：均有证据或诊断价值，但本轮生产价值低于 failed send draft recovery。

## 上层防御

需要。最优落点是 store/runtime 层的 composer draft cache，而不是页面提示文案。失败请求离开当前 composer 时，缓存原始失败草稿可以保护所有调用 `sendDraft()` 的 UI 入口。

## 实施任务

### Task 1: 写失败回归测试

**Files:**
- Modify: `frontend-app/src/entities/client/model/useClientStore.test.js`

- [ ] **Step 1: Add a test beside the stale send failure test**

```jsx
it('restores a failed new-chat draft when returning after a thread switch', async () => {
  const turnResult = deferred();
  const originalAttachments = [{ path: '/tmp/original.txt', name: 'original.txt' }];
  const nextAttachments = [{ path: '/tmp/next.txt', name: 'next.txt' }];

  resetClientStoreForTests({
    cwd: '/repo/app',
    activeProject: '/repo/app',
    activeThreadId: '',
    draft: 'Original pending send',
    attachments: originalAttachments,
    threads: [{ id: 'thread-other', name: 'Other thread', provider: 'codex', status: 'running' }],
    sidebarThreadsByProject: {
      '/repo/app': [{ id: 'thread-other', name: 'Other thread', provider: 'codex', status: 'running' }],
    },
  });
  backend.startThread.mockResolvedValue({ threadId: 'thread-provisional' });
  backend.startTurn.mockImplementation(() => turnResult.promise);

  const sendPromise = useClientStore.getState().sendDraft();
  await flushPromises();

  useClientStore.setState({ activeThreadId: 'thread-other', draft: 'New active draft', attachments: nextAttachments });
  turnResult.reject(new Error('turn/start failed'));
  await expect(sendPromise).rejects.toThrow('turn/start failed');

  expect(useClientStore.getState().draft).toBe('New active draft');
  expect(useClientStore.getState().attachments).toEqual(nextAttachments);

  useClientStore.getState().newThread();

  expect(useClientStore.getState().draft).toBe('Original pending send');
  expect(useClientStore.getState().attachments).toEqual(originalAttachments);
});
```

- [ ] **Step 2: Run the focused test and confirm it fails**

Run: `cd frontend-app && npm test -- useClientStore.test.js -t "restores a failed new-chat draft"`

Expected: FAIL because the draft cache does not contain the failed new-chat snapshot.

### Task 2: Persist failed snapshots when rollback cannot restore visible composer

**Files:**
- Modify: `frontend-app/src/entities/client/model/useClientStore.js`
- Modify: `frontend-app/src/entities/client/model/composerSlice.js`

- [ ] **Step 1: Add a runtime helper**

Add a helper in `attachComposerDraftRuntime()` that saves an explicit snapshot for a supplied thread id:

```js
const saveComposerDraftSnapshot = (state, threadId, snapshot) => {
  const normalized = normalizeComposerDraftSnapshot(snapshot);
  const key = composerDraftKey(state, threadId);
  if (isEmptyComposerDraftSnapshot(normalized)) {
    composerDrafts.delete(key);
    return;
  }
  composerDrafts.set(key, normalized);
};
```

Export it through `Object.assign(runtime, ...)`.

- [ ] **Step 2: Use the helper after failed rollback**

In both `sendDraft()` paths (`composerSlice.js` and legacy copy in `useClientStore.js`), after `rollbackSendDraftState(...)`, detect whether the current active composer was restored. If not, save `{ draft: activeRequest.previousDraft, attachments: activeRequest.previousAttachments }` under `activeRequest.previousActiveThreadId`.

- [ ] **Step 3: Keep current composer untouched**

Do not set `draft` or `attachments` when the user is on another active thread. Only write the cache entry.

### Task 3: Verify and commit

**Files:**
- Test: `frontend-app/src/entities/client/model/useClientStore.test.js`

- [ ] **Step 1: Run focused regression**

Run: `cd frontend-app && npm test -- useClientStore.test.js -t "restores a failed new-chat draft"`

Expected: PASS.

- [ ] **Step 2: Run adjacent send failure tests**

Run: `cd frontend-app && npm test -- useClientStore.test.js -t "send fails|stale send failure|provisional send fails|failed new-chat draft"`

Expected: PASS.

- [ ] **Step 3: Run full frontend verification**

Run:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all pass.

- [ ] **Step 4: Git checks**

Run:

```bash
git diff --check
git status --short
```

Expected: only owned files changed, no whitespace errors.

## 停止条件和后续边界

- Stop this round after fixing only failed-send draft recovery.
- Do not implement API response validator audit, workflow DAG response validation, code preview race, or files preview race in this commit.
- If focused regression cannot fail before the fix, reassess the candidate before editing production code.
