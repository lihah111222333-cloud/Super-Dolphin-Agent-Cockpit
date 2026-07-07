# Frontend API Fail-Fast Response Guards Repair Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a single fail-fast response-contract boundary for the current React/Vite frontend so malformed backend/native success responses cannot be hidden by page-level defaults.

**Architecture:** The primary fix belongs at `frontend-app/src/shared/api/backendApi.js:createBackendCaller`, because every named backend facade is wired through that caller. Native Wails helper responses are a separate transport boundary in `frontend-app/src/shared/api/wailsBridge.js`; they need strict shape checks, but they must not host business RPC field rules. Contract metadata and commit-time guards live beside the boundary in `backendApi.contractMatrix.js`, `rpc-contract-audit.mjs`, and `frontend-app/package.json`.

**Tech Stack:** React 19, Vite, Zustand, Vitest, ESLint, TypeScript contract checking via `tsc -p tsconfig.contracts.json --noEmit`.

**Verification Surface:** `frontend-app` lint/test/build, `backendApi` contract matrix/audit tests, Wails bridge helper tests, focused page regressions for high-risk candidates.

---

## Review Boundary

Worktree reviewed: `/home/l4place/Super-Dolphin/.worktrees/frontend-fixes-20260704`

Branch: `codex/frontend-fixes-20260704`

Scope: current new UI under `frontend-app/`, with source evidence from Wails/native bridge and relevant Go RPC contracts when needed.

Out of scope for the first repair: unrelated dirty files in the main worktree, broad redesign, bulk replacement of every page adapter, and generated embed output except as produced by `npm run build`.

## Evidence Summary

20 production-risk review agents plus 5 cross-adjudication agents were used. The findings converged on one systemic defect:

```text
P1 | D02/D09/D17/D18 | frontend-app/src/shared/api/backendApi.js:891-896 | backend facade validates request params but returns raw backend success responses | pages/adapters then convert malformed success payloads into empty arrays, default providers, stale data, or visible raw fields | add method-scoped response validation at the facade boundary and enforce it through contract metadata/audit/tests
```

Key local evidence:

- `frontend-app/src/shared/api/backendApi.js:891-896`: `createBackendCaller()` checks method/params and returns `callAPI(...)` raw.
- `frontend-app/src/shared/api/backendApi.contractMatrix.js:26-36`: `contract()` records method/facade/level/tests/notes but has no response policy.
- `frontend-app/scripts/rpc-contract-audit.mjs:13-31`: payload struct/builder coverage is hardcoded to `thread/start`, `turn/start`, and `turn/steer`.
- `frontend-app/scripts/rpc-contract-audit.mjs:89-96`: missing backend handler enforcement is currently P0-only.
- `frontend-app/src/shared/api/wailsBridge.js:957-985`: native file helper responses can be converted to `[]` or empty fields.
- `frontend-app/src/shared/api/wailsBridge.js:1011-1025`: malformed save/open success responses can be converted to cancel/success values.
- `frontend-app/package.json:18`: `npm test` currently runs only critical-skip guard and Vitest; `typecheck:contracts` and `audit:rpc-contracts` are not in the main test chain.

## Final Adjudication

### Unique Best Fix

The unique best fix is **method-scoped response validation at `backendApi.createBackendCaller`**, backed by explicit contract metadata and audit/test gates.

This is the first repair because it cuts off the shared failure mode before data reaches pages, stores, and adapters. Page-level fixes are necessary for some local bugs, but they do not prevent the same malformed backend success response from being silently accepted by another consumer.

### Required Upper Defense

Yes. The upper defense is required, and the best code landing points are:

1. Runtime boundary: `frontend-app/src/shared/api/backendApi.js`
   - Add `validateBackendResponse(method, raw, context)` and a method-scoped validator registry.
   - Call it inside `createBackendCaller()` after `callAPI()` resolves.
   - Throw on missing fields, wrong types, invalid enum values, and malformed envelopes.
   - Error messages must include `method` and a field path.

2. Contract fact source: `frontend-app/src/shared/api/backendApi.contractMatrix.js`
   - Add response metadata to each contract entry.
   - The first repair must require response metadata for the adjudicated lifecycle/read RPC set.
   - The metadata shape must support later expansion until every P0/P1 entry has either `responseValidator` or explicit `responsePassthroughReason`.

3. Commit-time guard: `frontend-app/scripts/rpc-contract-audit.mjs`
   - Parse response metadata from `backendApi.contractMatrix.js`.
   - Fail if a method in the adjudicated response-policy set lacks a validator or passthrough reason.
   - Confirm that methods declaring validators are also present in the frontend validator registry.
   - Avoid page-level response allowlists; keep the response-policy source beside the backend facade boundary.

4. Test chain: `frontend-app/package.json`
   - Make `npm test` run `guard:critical-skip`, `typecheck:contracts`, `audit:rpc-contracts`, then Vitest.
   - This is the best gate because CI and hooks already use `npm test`.

5. Native sub-boundary: `frontend-app/src/shared/api/wailsBridge.js`
   - Strictly decode native helper responses for file/project selection, dropped text files, save/open/preview shared file helpers, and frontend trace status.
   - This is a sibling boundary, not a replacement for backend RPC response validators.

## Rejected First Fixes

These are valid findings, but not the unique first repair:

- `WorkflowPage.jsx` selected-run final output fallback: real P1 local bug, but valid backend responses can still trigger it. It needs a focused follow-up because response validation cannot encode all workflow view ownership rules.
- `PromptPageView.jsx` prompt content preview and `observabilityAdapter.js` raw metadata display: real D10/D11 risks, but they are raw sink/redaction issues. They should follow the response boundary with view-model sanitization.
- Generic `adapterGuards.js`: useful later, but not first. Putting RPC contract facts in adapters would duplicate the backend facade boundary and spread D17 truth across layers.
- Page-by-page empty-array/default-provider fixes: symptoms. They are too easy to miss and do not prevent future drift.
- Go backend-only guards: necessary but insufficient. The frontend still needs submit-time and runtime evidence that its named facades match the contracts it consumes.

## First Repair Scope

### Task 1: Register Response Policies

**Files:**
- Modify: `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- Modify: `frontend-app/src/shared/api/backendApi.contractMatrix.test.js`

- [ ] Add typed response metadata to the contract entry shape.

Recommended shape:

```js
/**
 * @typedef {{
 *   key: string,
 *   method: string,
 *   facade: string,
 *   level: 'P0' | 'P1' | 'P2',
 *   backendOwner: string,
 *   tests: readonly string[],
 *   notes: readonly string[],
 *   rawLiteralRpc: boolean,
 *   responseValidator?: string,
 *   responsePassthroughReason?: string,
 * }} RpcContract
 */
```

- [ ] Move existing note-only facts such as `custom-decoder` and `passthrough response` into explicit response metadata.

Initial required validators or passthrough reasons:

```text
THREAD_START -> threadStartResponse
TURN_START -> turnStartResponse
THREAD_MESSAGES -> threadMessagesResponse
THREAD_RESOLVE -> threadResolveResponse
UI_STATE_GET -> uiStateResponse
TURN_INTERRUPT -> responsePassthroughReason: backend returns side-effect acknowledgement only
```

- [ ] Add a matrix/audit test that fails when a first-batch required contract has neither `responseValidator` nor `responsePassthroughReason`.

Run:

```bash
cd frontend-app
npm test -- backendApi.contractMatrix.test.js
```

Expected before implementation: failing test for missing response policy.

Expected after implementation: contract matrix test passes.

### Task 2: Validate Backend Facade Responses

**Files:**
- Modify: `frontend-app/src/shared/api/backendApi.js`
- Modify: `frontend-app/src/shared/api/backendApi.test.js`

- [ ] Add a method-scoped validator registry.

Implementation rule:

```js
function createBackendCaller(callAPI) {
  return async (method, params = {}) => {
    const rpcMethod = normalizeString(method);
    if (!rpcMethod) throw new Error('backend RPC method is required');
    const payload = assertPlainObject(rpcMethod, params);
    const raw = await callAPI(rpcMethod, payload);
    return validateBackendResponse(rpcMethod, raw, { params: payload });
  };
}
```

- [ ] Add small validator helpers in `backendApi.js`, not a new framework.

Minimum helper set:

```js
function requireResponseObject(method, value) { /* throw on non-object/array */ }
function requireStringField(method, value, field) { /* throw on empty or non-string */ }
function requireArrayField(method, value, field) { /* throw on non-array */ }
function rejectExtraResponseFields(method, value, allowed) { /* throw on unknown */ }
```

- [ ] Validate first high-risk responses.

Initial coverage:

```text
thread/start: accepts canonical nested thread id; rejects missing thread id.
turn/start: rejects malformed response envelope; preserves valid acknowledgement.
thread/messages: requires messages array and paging fields when present.
thread/resolve: requires resolved thread identity field shape.
ui/state/get: requires object response with thread-state envelope.
```

- [ ] Add regression tests proving malformed success payloads reject before consumers see them.

Required tests:

```js
await expect(api.startThread(validStartParams)).rejects.toThrow('thread/start: response');
await expect(api.getThreadMessages({ cwd, threadId })).rejects.toThrow('thread/messages: response.messages');
expect(callAPI).toHaveBeenCalledTimes(1);
```

Run:

```bash
cd frontend-app
npm test -- backendApi.test.js
```

### Task 3: Guard Native Wails Helper Responses

**Files:**
- Modify: `frontend-app/src/shared/api/wailsBridge.js`
- Modify: `frontend-app/src/shared/api/wailsBridge.test.js`

- [ ] Strictly decode helper responses that currently turn malformed success into cancel/success states.

Required helper behavior:

```text
selectFiles: explicit { paths: [] } means user selected nothing; malformed payload throws.
readDroppedTextFiles: explicit { files: [] } means no readable dropped files; malformed item throws.
saveTextFile: explicit { path: '' } means user cancelled; missing path or non-string path throws.
openSharedFile / previewSharedFile: success must be explicit, not any object.
frontend trace status: invalid status must be dropped or rejected; never rewritten to ok.
```

- [ ] Add fail-fast tests for malformed success responses.

Run:

```bash
cd frontend-app
npm test -- wailsBridge.test.js
```

### Task 4: Enforce Contract Guards in the Main Test Chain

**Files:**
- Modify: `frontend-app/package.json`
- Modify: `frontend-app/scripts/no-critical-skip.mjs`
- Modify: `frontend-app/scripts/no-critical-skip.test.mjs` if no existing self-test covers script roots
- Modify: `frontend-app/scripts/rpc-contract-audit.mjs`
- Modify: `frontend-app/scripts/rpc-contract-audit.test.mjs`

- [ ] Expand the critical skip guard to scan `frontend-app/src` and `frontend-app/scripts`.

Critical terms must include at least:

```text
rpc
contract
desktop
smoke
provider
thread
turn
workflow
```

- [ ] Extend `rpc-contract-audit.mjs` to parse response metadata from the contract matrix.

Required failure buckets:

```text
missingResponsePolicy
unknownResponseValidator
missingBackendHandlers
invalidPassthroughReason
```

- [ ] Update `npm test`.

Target script:

```json
"test": "npm run guard:critical-skip && npm run typecheck:contracts && npm run audit:rpc-contracts && vitest run --no-file-parallelism --maxWorkers=1"
```

Run:

```bash
cd frontend-app
npm run guard:critical-skip
npm run typecheck:contracts
npm run audit:rpc-contracts
npm test
```

### Task 5: Keep Local Page Bugs as Follow-Up Work

Do not mix these into the first response-boundary repair unless they block tests:

- `frontend-app/src/pages/workflows/WorkflowPage.jsx`: selected run without `final_output` must not fall back to latest run output.
- `frontend-app/src/features/prompts/PromptPageView.jsx`: list cards must not preview full prompt content.
- `frontend-app/src/adapters/observabilityAdapter.js`: metadata/stack must be redacted before UI state.
- `frontend-app/src/pages/chat/components/chatUiActions.js`: remove local swallow wrapper and reuse shared `runUIAction`.
- `frontend-app/src/entities/client/model/timelineRuntime.js`: content-based merge must not drop distinct historical messages.
- `frontend-app/src/entities/client/model/runtimeSlice.js`: `initializeEvents()` must be inside bootstrap failure handling.

These are real findings, but they are second-order once the shared response boundary is defined. If one is pulled into the same PR, it must have a narrow test and must not dilute the contract-boundary commit.

## Final Verification

Before claiming the repair complete, run:

```bash
cd frontend-app
npm run guard:critical-skip
npm run typecheck:contracts
npm run audit:rpc-contracts
npm run lint
npm test
npm run build
```

If `npm run build` changes generated embed output, inspect `git status --short` and include only intended generated files with the frontend change.

## Completion Criteria

- Malformed backend success responses reject at `backendApi.createBackendCaller`.
- P0/P1 backend RPCs have explicit response policy metadata.
- The audit fails when response policy metadata is missing or invalid.
- Native Wails helper malformed success responses reject at `wailsBridge.js`.
- `npm test` runs critical skip guard, contract typecheck, RPC contract audit, and Vitest.
- No page-level default can be used as the primary evidence that a backend contract is valid.
