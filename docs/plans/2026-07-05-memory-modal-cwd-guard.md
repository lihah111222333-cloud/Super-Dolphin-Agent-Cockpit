# Memory Modal CWD Guard Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent MemoryPage edit/delete modals opened for one project from saving or deleting entries in another project after `projectPath` changes.

**Architecture:** Bind editor and delete modal state to the `dashboard.memoryCwd` that created it. Clear stale scoped state when the cwd changes, ignore stale edit-detail responses, and fail fast before save/delete if the modal scope no longer matches the active cwd.

**Tech Stack:** React hooks, Vitest, Testing Library, TanStack Query.

**Verification Surface:** `frontend-app/src/pages/memory/MemoryPage.jsx`, `frontend-app/src/pages/memory/MemoryPage.test.jsx`, full `frontend-app` validation.

---

### Task 1: Add Regression Tests

**Files:**
- Modify: `frontend-app/src/pages/memory/MemoryPage.test.jsx`

- [ ] **Step 1: Add helpers and failing tests**

Add tests under `describe('MemoryPage editor', ...)` that:
- open edit for `/repo/one`, rerender `MemoryPage` with `/repo/two`, then resolve the old detail request and assert no edit dialog opens;
- open delete for `/repo/one`, rerender with `/repo/two`, click confirm if present, and assert `deleteMemoryEntry` is not called for `/repo/two` with the stale target/path.

- [ ] **Step 2: Run focused tests to verify failure**

Run:

```bash
cd frontend-app
npm test -- MemoryPage.test.jsx
```

Expected before implementation: the stale edit dialog can open, or the stale delete confirm can call `deleteMemoryEntry` against the new cwd.

### Task 2: Scope Memory Modal State To CWD

**Files:**
- Modify: `frontend-app/src/pages/memory/MemoryPage.jsx`

- [ ] **Step 1: Capture and validate cwd**

Store `scopeCwd` in editor/delete state when create, edit, and delete actions open a modal. Add an effect in `useMemoryEditor` and `useMemoryDelete` that closes scoped state when `dashboard.memoryCwd` changes.

- [ ] **Step 2: Block stale async and stale submit paths**

In `openEdit`, capture the current cwd before `await getMemoryEntry` and only open the editor if the active cwd is still the captured cwd. In save/delete handlers, if scoped cwd differs from current cwd, show an error and do not call the mutation RPC.

- [ ] **Step 3: Run focused tests to verify pass**

Run:

```bash
cd frontend-app
npm test -- MemoryPage.test.jsx
```

Expected after implementation: all MemoryPage tests pass and stale project switch tests assert no wrong-cwd mutation.

### Task 3: Full Verification And Commit

**Files:**
- Validate: `frontend-app`
- Commit: `MemoryPage.jsx`, `MemoryPage.test.jsx`, this plan

- [ ] **Step 1: Run complete frontend validation**

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

- [ ] **Step 2: Commit and push directly to remote main**

```bash
git add docs/plans/2026-07-05-memory-modal-cwd-guard.md frontend-app/src/pages/memory/MemoryPage.jsx frontend-app/src/pages/memory/MemoryPage.test.jsx
git commit -m "fix: 防止记忆弹窗跨项目提交"
git push origin HEAD:main
```
