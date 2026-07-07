# Memory Target Payload Validation Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent frontend memory APIs from sending missing or unknown memory targets that the backend can silently normalize to private memory.

**Architecture:** The fix belongs in `frontend-app/src/shared/api/backendApi.js`, because this is the frontend RPC boundary that already performs payload normalization and fail-fast validation before Wails/RPC calls.

**Tech Stack:** React/Vite frontend, Vitest API tests, backend API contract matrix.

**Verification Surface:** `frontend-app/src/shared/api/backendApi.test.js`, `frontend-app/src/shared/api/backendApi.js`, full `frontend-app` lint/test/build.

---

## Review Scope

- `frontend-app/src/shared/api/backendApi.js`
- `frontend-app/src/shared/api/backendApi.test.js`
- `frontend-app/src/adapters/memoryAdapter.js`
- `frontend-app/src/pages/memory/MemoryPage.jsx`
- `internal/module/memory/ui_rpc_mutations.go`

## Evidence Summary

- `memoryEntryGetPayload` currently requires `cwd` and `path`, but does not require or validate `target`.
- `memoryEntryUpsertPayload` trims `target` and sends it when present, without checking the allowed values.
- `memoryPairPayload` requires `targetA` and `targetB`, but does not check that they are `private` or `team`.
- The memory page normally generates `private/team`, but edit details and similarity groups come from backend data and are passed back into frontend mutation payloads.
- Backend `normalizeUIMemoryTarget` keeps legacy compatibility by mapping unknown UI targets to `private`, so the frontend boundary must fail fast to avoid silent cross-scope behavior.
- LSP text search located the payload helpers and backend target resolver; xref for `backendApi.js` timed out while diagnostics were not ready.

## Final Decision

Add a shared frontend memory target validator for RPC payloads and apply it to entry get/delete, entry upsert, and pair payloads.

## Unique Best Fix

Create `memoryTargetPayload(method, value, field = 'target')`:

- Trim and normalize the target string.
- Accept only `private` and `team`.
- Throw `<method>: <field> must be private or team` for empty or unknown values.
- Use it for `target`, `targetA`, and `targetB`.

## Rejected Candidate Fixes

- Rely on `MemoryPage` to only construct known targets: edit detail and similarity data are backend-fed and can still re-enter mutation payloads.
- Patch only `upsertMemoryEntry`: get/delete and similarity merge/ignore share the same cross-scope target risk.
- Change backend compatibility behavior in this frontend round: backend may still need legacy handling for older clients, while the current frontend can fail fast immediately.

## Upper Defense

Add API-level tests proving malformed memory target payloads reject before calling `callAPI`. This catches future regressions even if page-level data normalization changes.

## Tasks

- [x] Add RED API tests for invalid memory targets on get/upsert/delete/merge.
- [x] Implement the shared memory target validator in `backendApi.js`.
- [x] Run focused backend API tests.
- [x] Run `npm run lint`, `npm test`, `npm run build`, `git diff --check`, and LSP diagnostics where available.

## Validation Results

- RED: `npm test -- backendApi.test.js -t "rejects malformed memory target payloads"` failed before implementation because `getMemoryEntry` accepted an empty target and reached `callAPI`.
- GREEN: `npm test -- backendApi.test.js -t "rejects malformed memory target payloads"` passed after adding the shared target validator.
- `npm test -- backendApi.test.js`: passed, 60 tests.
- `npm run lint`: passed.
- `npm test`: passed, 81 files and 1028 tests.
- `npm run build`: passed.
- `git diff --check`: passed.
- LSP: modified `backendApi.js` lines were read successfully; full diagnostics for `backendApi.js` exceeded the tool output budget, while `backendApi.test.js` returned 0 diagnostics.

## Validation Commands

```bash
cd frontend-app
npm test -- backendApi.test.js -t "rejects malformed memory target payloads"
npm run lint
npm test
npm run build
git diff --check
```

## Stop Conditions

- Stop if product requirements show a third supported memory target beyond `private/team`.
- Stop if backend API contract requires the frontend to omit target for a specific memory operation.
- Do not refactor unrelated memory response validators or page UI behavior.
