# Runtime Result Fields Redaction Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent raw RPC result payload fields from being stored or rendered in the frontend runtime activity panel.

**Architecture:** Keep redaction at the frontend runtime result normalization boundary, then add display-layer defense before popover rendering. Preserve useful non-sensitive correlation fields such as method, request id, trace ids, and sanitized result preview.

**Tech Stack:** React/Vite frontend, Vitest, existing `compactSafeDiagnosticPreview` redaction helper.

**Verification Surface:** `frontend-app/src/entities/client/model/runtimeResults.js`, `frontend-app/src/pages/chat/adapters/runtimeLogAdapter.js`, focused Vitest tests, full `frontend-app` lint/test/build.

---

## Review Scope

First round used 20 read-only review agents across frontend risk surfaces:

- API facade and contract matrix.
- Wails bridge.
- Client store and runtime state.
- Chat/timeline/runtime UI.
- App shell.
- Workflows/DAG/cron/material upload.
- Skills/MCP controls.
- Memory/shared files.
- Settings/model providers/runtime preferences.
- Files/shared-file dashboard.
- Prompts.
- Observability.
- Shared UI/focus/modal.
- CSS/accessibility.
- Frontend guard/audit scripts.
- Shared adapters and sanitizers.
- Dashboard/log aggregation.
- Event/timeline adapters.
- Contract registry response policy.

Second round used 5 cross-review agents:

- Global severity/risk裁决.
- Security-sensitive display裁决.
- Contract/response-policy裁决.
- State-machine/stale-response裁决.
- Fail-fast/default-fallback裁决.

## Evidence Summary

- `runtimeResultEntryFromRPCDone` builds sanitized `detail` from `fields.result_preview || fields.result`, but returns raw `fields` on the runtime result entry.
- `warningDetailText` renders runtime result entries by `JSON.stringify(entry.fields, null, 2)`.
- `RuntimeWarningPopover` displays that string in the runtime activity panel.
- Existing tests verify `detail`/`message` redaction, but do not prove `entry.fields` excludes raw `result_preview` or nested secrets for RPC done events.
- LSP document symbols and references show `runtimeResultEntryFromRPCDone` is created in `runtimeResults.js` and consumed through `useClientStore.addLog`, so the source-side redaction is the narrowest frontend boundary.

## Final Decision

Implement the P0 frontend runtime result raw fields redaction fix.

This is the unique best repair for this round because it is directly in `frontend-app`, is user-visible in the runtime activity popover, has clear source evidence, and has a small verifiable fix. The observability trace sanitizer is also security-relevant, but its primary fix is in Go platform observability and should be handled in a separate backend-focused round.

## Unique Best Fix

1. Store safe fields for `api.rpc.done` runtime result entries instead of raw backend fields.
2. Keep non-sensitive correlation fields:
   - `method`
   - `threadId`
   - `req_id`
   - `trace_id`
   - `span_id`
   - `status`
   - sanitized `preview` derived from `result_preview` or `result`
3. Add display-layer defense in `warningDetailText` so runtime result popovers never stringify untrusted raw fields without passing through the shared safe preview helper.

## Rejected Candidate Fixes

- `rpc-contract-audit` global response-policy gate: valid as an upper defense, but too broad as the only repair and not a direct runtime leak fix.
- Observability trace detail path leak: valid, but primary implementation belongs to Go observability sanitizer and should be isolated in a backend-oriented round.
- Files stale detail response: valid P2 state issue, lower severity.
- Model provider malformed registry: valid P1 fail-fast issue, lower severity.
- Workflow malformed config and timed-out mutation candidates: evidence is weaker or backend CAS/fail-fast already reduces blast radius.
- Skills scope default project: backend rejects non-empty unknown scope; lower priority than direct runtime result leak.
- Cron timezone candidates: valid but require compatibility decisions, not the smallest high-confidence frontend security repair.

## Upper Defense

Add the upper defense at `frontend-app/src/pages/chat/adapters/runtimeLogAdapter.js` by sanitizing the data used for runtime result popover detail. This protects the UI even if a future runtime result producer accidentally attaches unsafe fields.

## Implementation Tasks

### Task 1: Add Failing Tests

**Files:**
- Modify: `frontend-app/src/entities/client/model/runtimeResults.test.js`
- Modify: `frontend-app/src/entities/client/model/useClientStore.test.js`

- [x] **Step 1: Add unit regression for RPC done fields**

Add a test to `runtimeResults.test.js` that creates an `api.rpc.done` entry with:

```js
result_preview: JSON.stringify({
  messages: [{
    id: 1,
    content: 'private prompt body',
    path: '/home/l4place/private-project/secret.txt',
    api_key: 'sk-live-secret',
    count: 2,
  }],
  total: 1,
})
```

Assert `entry.detail`, `entry.message`, and `JSON.stringify(entry.fields)` do not contain `private prompt body`, `/home/l4place`, or `sk-live-secret`.

- [x] **Step 2: Add store integration regression**

Extend `useClientStore.test.js` near the RPC done coalescing test. Call `addLog('debug', 'api.rpc.done', ...)` with a sensitive `result_preview`, then assert `runtimeResultEntries[0].fields` does not serialize sensitive content or raw `result_preview` text, while still preserving `req_id`.

- [x] **Step 3: Run focused tests and confirm failure**

Run:

```bash
cd frontend-app
npm test -- --run src/entities/client/model/runtimeResults.test.js src/entities/client/model/useClientStore.test.js -t "runtime result|backend RPC return"
```

Observed: FAIL because raw `fields.result_preview` still contained sensitive data.

### Task 2: Redact Stored Runtime Result Fields

**Files:**
- Modify: `frontend-app/src/entities/client/model/runtimeResults.js`

- [x] **Step 1: Add safe RPC fields helper**

Add a helper inside `createRuntimeResultHelpers` that builds an object containing only safe correlation fields and sanitized preview content:

```js
const safeRuntimeRPCResultFields = (fields = {}, detail = '') => {
  const out = {};
  for (const [source, target = source] of [
    ['method'],
    ['rpcMethod', 'method'],
    ['rpc_method', 'method'],
    ['threadId'],
    ['thread_id', 'threadId'],
    ['req_id'],
    ['reqId', 'req_id'],
    ['trace_id'],
    ['traceId', 'trace_id'],
    ['span_id'],
    ['spanId', 'span_id'],
    ['status'],
  ]) {
    const value = normalizeString(fields[source]);
    if (value && out[target] === undefined) out[target] = value;
  }
  if (detail) out.preview = safeRuntimeToolResultFieldObject(detail);
  return out;
};
```

- [x] **Step 2: Use safe fields in `runtimeResultEntryFromRPCDone`**

Replace:

```js
fields,
```

with:

```js
fields: safeRuntimeRPCResultFields(fields, detail),
```

### Task 3: Add Display-Layer Defense

**Files:**
- Modify: `frontend-app/src/pages/chat/adapters/runtimeLogAdapter.js`
- Test: `frontend-app/src/pages/chat/components/RuntimePanelComponents.test.jsx` or closest existing runtime panel test if needed.

- [x] **Step 1: Sanitize popover detail**

Import `compactSafeDiagnosticPreview` and use it in `warningDetailText` for `entry.fields`:

```js
import { compactSafeDiagnosticPreview } from '../../../shared/api/safeDiagnosticPreview.js';

const RUNTIME_LOG_DETAIL_LIMIT = 1600;

function safeRuntimeLogDetail(value) {
  return compactSafeDiagnosticPreview(value, RUNTIME_LOG_DETAIL_LIMIT, { parseJsonStrings: true });
}
```

For runtime result entries, return `safeRuntimeLogDetail(entry.fields)`.

- [x] **Step 2: Keep warning fallback behavior unchanged**

For non-result entries, preserve the existing `entry.detail || JSON.stringify(entry.fields)` behavior. The display-layer defense is scoped to runtime result entries to avoid breaking existing warning popover expectations.

### Task 4: Verify and Commit

**Files:**
- Modify: `docs/plans/2026-07-05-runtime-result-fields-redaction.md`
- Modify: `frontend-app/src/entities/client/model/runtimeResults.js`
- Modify: `frontend-app/src/entities/client/model/runtimeResults.test.js`
- Modify: `frontend-app/src/entities/client/model/useClientStore.test.js`
- Modify: `frontend-app/src/pages/chat/adapters/runtimeLogAdapter.js`

- [x] **Step 1: Run focused tests**

```bash
cd frontend-app
npm test -- --run src/entities/client/model/runtimeResults.test.js src/entities/client/model/useClientStore.test.js
```

- [x] **Step 2: Run diagnostics**

Run LSP diagnostics on changed JS files. If `runtimeResults.js` diagnostics exceed tool budget or are unavailable, record the tool limitation and use lint/typecheck/test output as substitute evidence.

- [x] **Step 3: Run full frontend validation**

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

- [x] **Step 4: Run diff checks**

```bash
git diff --check
git diff --cached --check
```

- [ ] **Step 5: Commit and push**

Stage only owned files, commit with:

```bash
git commit -m "fix: 脱敏运行结果字段"
git push origin HEAD:main
```

## Stop Condition

This plan stops after the P0 runtime result fields leak is fixed, verified, committed, pushed to remote `main`, and the local r31 worktree/branch are cleaned. Other review findings remain candidates for later loop rounds and must not be mixed into this commit.
