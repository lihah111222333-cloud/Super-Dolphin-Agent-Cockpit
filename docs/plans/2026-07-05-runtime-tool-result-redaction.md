# Runtime Tool Result Redaction Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent runtime activity tool-result entries from exposing raw prompt/content/path/token-like payloads in UI messages, details, or popovers.

**Architecture:** Reuse the existing `compactSafeDiagnosticPreview` policy already used for RPC result previews. Apply it to timeline tool result details and stored fields before runtime log entries are created.

**Tech Stack:** React/Vite frontend, Vitest unit tests, existing runtime result helper.

**Verification Surface:** `frontend-app/src/entities/client/model/runtimeResults.test.js`, full `cd frontend-app && npm run lint && npm test && npm run build`.

---

### Task 1: Sanitize Runtime Tool Result Entries

**Files:**
- Modify: `frontend-app/src/entities/client/model/runtimeResults.js`
- Test: `frontend-app/src/entities/client/model/runtimeResults.test.js`

- [x] **Step 1: Write failing redaction test**

Add a test that feeds a tool timeline item with JSON detail containing `content`, `path`, `api_key`, and a safe numeric field. Assert `detail`, `message`, and serialized `fields` exclude the secret/path/content while preserving safe numeric context.

- [x] **Step 2: Confirm RED**

Run: `cd frontend-app && npx vitest run src/entities/client/model/runtimeResults.test.js`
Expected: FAIL because current code copies raw tool output and full item fields.
Observed: FAIL with `expected ... not to contain 'private prompt body'`.

- [x] **Step 3: Implement sanitizer reuse**

Use `compactSafeDiagnosticPreview(value, RUNTIME_RESULT_DETAIL_LIMIT, { parseJsonStrings: true })` for tool result details. Store sanitized fields, not raw `item`, in `runtimeToolResultEntry`.

- [x] **Step 4: Confirm GREEN and full checks**

Run:
```bash
cd frontend-app
npx vitest run src/entities/client/model/runtimeResults.test.js
npm run lint
npm test
npm run build
```
Expected: all commands exit 0.
Observed: focused Vitest, lint, full test, and build exited 0.
