# App Update Install Response Validator Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent app update install RPCs from reporting success when the backend or bridge returns malformed or non-started install results.

**Architecture:** Keep the guard at the frontend API boundary in `backendApi.js`, alongside existing response validators. Both `app/update/install` and `app/update/installLatest` must fail fast unless the backend returns the explicit install-started contract.

**Tech Stack:** React/Vite frontend, Vitest API tests, existing backend API facade.

**Verification Surface:** `frontend-app/src/shared/api/backendApi.test.js`, plus full `cd frontend-app && npm run lint && npm test && npm run build`.

---

### Task 1: Add App Update Install Response Guard

**Files:**
- Modify: `frontend-app/src/shared/api/backendApi.js`
- Test: `frontend-app/src/shared/api/backendApi.test.js`

- [x] **Step 1: Write failing API tests**

Add cases near the app update RPC wrapper test that expect valid install results to resolve and malformed responses to reject. The invalid list must include `{}`, `null`, `{ started: false, helper: 'helper' }`, `{ started: true, helper: '' }`, and `{ ok: true }`.

- [x] **Step 2: Run focused test and confirm failure**

Run: `cd frontend-app && npx vitest run src/shared/api/backendApi.test.js`
Expected: FAIL because install responses are currently passed through without validation.
Observed: FAIL with `promise resolved "{}" instead of rejecting`.

- [x] **Step 3: Implement validator**

Add `validateAppUpdateInstallResponse(method, response)` using `assertBackendResponseObject`. Require `started === true` and a non-empty string `helper`.

- [x] **Step 4: Register validator**

Add entries for `RPC_METHODS.APP_UPDATE_INSTALL` and `RPC_METHODS.APP_UPDATE_INSTALL_LATEST` in `BACKEND_RESPONSE_VALIDATORS`.

- [x] **Step 5: Run focused and full frontend checks**

Run:
```bash
cd frontend-app
npx vitest run src/shared/api/backendApi.test.js
npm run lint
npm test
npm run build
```
Expected: all commands exit 0.
Observed: focused Vitest, lint, full test, and build exited 0.
