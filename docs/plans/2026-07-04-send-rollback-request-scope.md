# Send Rollback Request Scope Repair Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent an older failed chat send from mutating the current composer or leaving a backend-deleted newly created thread visible in the frontend store.

**Architecture:** The fix belongs in the shared client store rollback helper because both direct chat sends and dashboard command sends use the same optimistic send state machine. The rollback must target the request-owned timeline/thread identifiers instead of whatever thread is active when the asynchronous failure arrives. Backend/API contracts are not the upper defense for this issue; store invariant regression tests are.

**Tech Stack:** React/Vite frontend, Zustand client store, Vitest store tests.

**Verification Surface:** `frontend-app/src/entities/client/model/useClientStore.test.js`, LSP diagnostics for touched store/test files, `frontend-app` lint/test/build, and `git diff --check`.

---

## Review Scope

- Worktree: `/home/l4place/Super-Dolphin/.worktrees/frontend-fixes-20260704-r3`
- Base: `origin/main` at `49bff74f31f406f337562d62ed9781ecd565a966`
- Baseline before review:
  - `cd frontend-app && npm ci`
  - `npm run lint`
  - `npm test` (`79` files / `991` tests)
  - `npm run build`
- Review method: 20 read-only frontend production-risk agents, then 5 read-only cross-adjudication agents.
- Covered dimensions: frontend code quality, production risk, request/response boundaries, error handling and fail-fast, page defaults and silent fallback, test gates, upper defenses, privacy, performance, accessibility, and release/embed boundaries.

## Evidence Summary

```text
P1 | D09/D15 | frontend-app/src/entities/client/model/useClientStore.js:1567-1582 | failed send rollback is not request-scoped | rollbackSendDraftState removes the optimistic item from timelinesByThread[state.activeThreadId] and unconditionally restores request.previousDraft/request.previousAttachments. If the user switches threads or edits a new draft while startTurn is pending, the older failure can overwrite the current composer and clean the wrong timeline. When a new backend thread was created then deleted after turn/start failure, the promoted thread can remain in threads/sidebar state. | make rollback target request-owned thread ids and restore composer only if the active composer still belongs to that request.
```

Source evidence:

- `frontend-app/src/entities/client/model/useClientStore.js:1567-1582`: `rollbackSendDraftState()` uses current `state.activeThreadId` for optimistic item removal and always restores old draft/attachments.
- `frontend-app/src/entities/client/model/composerSlice.js:232-289`: `sendDraft()` can asynchronously fail after `thread/start` succeeds and `turn/start` is pending.
- `frontend-app/src/entities/client/model/useClientStore.js:1528-1558`: `promotedDraftThreadState()` moves the provisional timeline into the newly created backend thread and inserts that thread into `threads` and `sidebarThreadsByProject`.
- `frontend-app/src/entities/client/model/composerSlice.js:283-287`: failure cleanup computes the created backend thread id, rolls back frontend state, then asks the backend to delete that thread.
- `frontend-app/src/entities/client/model/useClientStore.test.js:3415-3445`: existing regression only checks that an unrelated active thread is not deleted; it does not prove the newer composer state is preserved or that the promoted backend-deleted thread is removed locally.

## Final Adjudication

The unique best repair for this round is request-scoped send rollback in the client store.

Two adjudication agents selected this area. One selected a narrower "backend-deleted provisional thread remains visible" fix; another selected the broader root cause: rollback is keyed to current active state rather than the failed request. The broader fix covers both symptoms while staying in one store helper and one focused test file.

## Rejected Candidates

- Workflow DAG duplicate start guard: real P1 and narrow, but only affects workflow run start; chat send rollback is a more frequent primary user path and can leave the frontend pointing at a deleted backend resource.
- Markdown remote image autoload: real P1 privacy risk, but security scope needs a separate image policy decision and possible CSP/asset policy defense.
- Thread ID alias conflict: real identity-boundary risk, but the strongest fix belongs in Go RPC/thread/turn backend code and is outside this frontend-focused round.
- Prompt stale draft commit and fallback writes: real P1 candidates, but less central than the shared chat send state machine and likely require several prompt UI state changes.
- Code locate/open/save response validators and memory/prompt/DAG validators: useful response-boundary hardening, but narrower than a main send path state corruption fix.
- Observability path rendering, bridge path logs, runtime raw errors, and test gate gaps: valid follow-up candidates but lower severity or broader policy work for this round.

## Unique Best Fix

Make `rollbackSendDraftState()` request-scoped:

- Remove the optimistic user message from `request.previousThreadId || request.provisionalThreadId`, not from the current active thread.
- When a blank-thread send was promoted to a real backend thread before failing, accept that created thread id and remove it from `threads`, `sidebarThreadsByProject`, `timelinesByThread`, `threadTimelineReadyByThread`, and `activityThreadAtById`.
- Only restore `draft` and `attachments` if the current active thread still matches the failed request's active thread/provisional thread. If the user has switched threads or typed a new draft, keep the current composer untouched.
- Preserve the existing dashboard command caller by making the new behavior opt-in through helper options, not by changing unrelated dashboard page state.

## Upper Defense

Required at the store invariant level.

Best landing points:

- `frontend-app/src/entities/client/model/useClientStore.js`
  - Extend `rollbackSendDraftState(state, request, error, options = {})`.
  - Add request-owned timeline cleanup.
  - Add optional `createdThreadId` cleanup for promoted blank-thread sends.
  - Gate composer restoration based on the failed request's ownership of the current active composer.

- `frontend-app/src/entities/client/model/composerSlice.js`
  - Pass `createdThreadId` into `rollbackSendDraftState()` on the final send failure path.
  - Keep backend deletion call unchanged.

- `frontend-app/src/entities/client/model/useClientStore.test.js`
  - Add regression coverage for stale failure after a thread switch preserving the new active draft and attachments.
  - Extend the provisional-thread deletion test to assert the promoted thread is absent from `threads`, sidebar cache, timelines, ready map, and active id.

## Implementation Tasks

### Task 1: Add failing store regressions

- [x] Extend `deletes a provisional backend thread when the first turn fails`.
- [x] Assert `threads` does not contain `thread-provisional`.
- [x] Assert `sidebarThreadsByProject['/repo/app']` does not contain `thread-provisional`.
- [x] Assert `timelinesByThread.thread-provisional`, `threadTimelineReadyByThread.thread-provisional`, and `activityThreadAtById.thread-provisional` are absent.
- [x] Add a test where `startTurn` is pending, the user switches to `thread-other`, edits draft/attachments, then the original send fails.
- [x] Assert the newer active draft/attachments and `activeThreadId` survive.

### Task 2: Implement request-scoped rollback

- [x] Update `rollbackSendDraftState()` to accept an `options` object with `createdThreadId`.
- [x] Remove optimistic items from the request-owned timeline.
- [x] Remove `createdThreadId` from local thread/sidebar/timeline/ready/activity state when present.
- [x] Restore composer state only when current `state.activeThreadId` still matches the failed request's previous/provisional thread.
- [x] Keep `sending`, `error`, and `actionNotice` behavior unchanged.
- [x] Pass `createdThreadId` from `composerSlice.js` final catch path.

### Task 3: Verify focused and full gates

- [x] Run the focused store test.
- [x] Run LSP diagnostics on `useClientStore.js`, `composerSlice.js`, and `useClientStore.test.js`.
- [x] Run `npm run lint`.
- [x] Run `npm test`.
- [x] Run `npm run build`.
- [x] Run `git diff --check`.

LSP diagnostics note: `useClientStore.test.js` returned no diagnostics. `useClientStore.js` and `composerSlice.js` both returned `lsp_timeout` / `context deadline exceeded` after opening the files and retrying. Replacement evidence was `npm run lint`, focused `npm test -- src/entities/client/model/useClientStore.test.js`, full `npm test`, `npm run build`, and `git diff --check`.

## Verification Commands

```bash
cd frontend-app
npm test -- src/entities/client/model/useClientStore.test.js
npm run lint
npm test
npm run build
git diff --check
```

## Stop Conditions / Follow-Up Boundary

- Stop and re-adjudicate if current source proves `rollbackSendDraftState()` is no longer used by send failure paths.
- Do not include Workflow DAG duplicate-start, Markdown remote image policy, prompt response validators, skill scope hardening, memory preview privacy, Wails picker validation, or backend thread alias fixes in this commit.
- Do not add silent fallback defaults. If state ownership is ambiguous, preserve current user state and surface the original send error.
