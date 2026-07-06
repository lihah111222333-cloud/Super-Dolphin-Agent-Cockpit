# Skill Resolution Required Payload Fields Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fail fast in the shared frontend API when skill resolution preview or apply calls omit the required conflict identifier or action.

**Architecture:** The fix belongs in `frontend-app/src/shared/api/backendApi.js` because `skillResolutionPayload` is the shared payload builder for both preview and apply RPCs. The backend already rejects invalid requests, but the frontend API boundary should enforce required fields before sending the RPC.

**Tech Stack:** React/Vite frontend, shared backend API wrapper, Vitest.

**Verification Surface:** `frontend-app/src/shared/api/backendApi.js`, `frontend-app/src/shared/api/backendApi.test.js`, full `frontend-app` lint/test/build.

---

## Review Scope

- `frontend-app/src/shared/api/backendApi.js`
- `frontend-app/src/shared/api/backendApi.test.js`
- `internal/module/skill/rpc_skill_types.go`
- `internal/module/skill/rpc_types.go`

## Evidence Summary

- Backend preview looks up a resolution item by `ConflictID` and validates that `Action` is available.
- Backend apply validates preview proof, then looks up the conflict/action pair before applying changes.
- The shared frontend API currently normalizes missing `conflict_id` and `action` to empty strings and removes them with `cleanObject`, then still calls the backend.
- `skillResolutionPayload` currently hardcodes `SKILLS_RESOLUTION_PREVIEW` as the method name even when called from apply, so apply input errors can point to the wrong RPC method.

## Final Decision

Make `skillResolutionPayload` method-aware and require normalized `conflict_id` and `action` before returning the payload.

## Unique Best Fix

Change the helper signature to `skillResolutionPayload(method, params)` and update both preview and apply callers. Add API tests proving missing `conflict_id` and missing `action` do not call `callAPI`.

## Rejected Candidate Fixes

- Leave validation to the backend: this keeps the shared API inconsistent with other frontend mutation payload guards.
- Validate only the page call sites: shared API callers and tests can still bypass page-level guards.
- Add separate duplicate checks in preview and apply: a shared method-aware helper keeps the contract in one place.

## Upper Defense

Add a focused API regression test for both preview and apply payloads when `conflict_id` or `action` is missing.

## Tasks

- [x] Add RED API test for missing resolution conflict/action fields.
- [x] Make `skillResolutionPayload` method-aware and fail-fast on missing fields.
- [x] Run focused backend API tests.
- [x] Run LSP diagnostics or record tool limitations.
- [x] Run `npm run lint`, `npm test`, `npm run build`, and `git diff --check`.

## Validation Results

- Baseline before edits: `npm ci`, `npm run lint`, `npm test`, and `npm run build` passed in the r47 worktree.
- RED: `npm test -- backendApi.test.js -t "rejects skill resolution payloads without required conflict fields"` failed because missing `conflictId` did not throw.
- GREEN: the same focused test passed after making `skillResolutionPayload` method-aware and fail-fast.
- `npm test -- backendApi.test.js`: passed, 62 tests.
- LSP: `backendApi.js` diagnostics returned 0 diagnostics; modified helper and preview/apply call sites were read successfully. `backendApi.test.js` diagnostics timed out after indexing.
- `npm run lint`: passed.
- `npm test`: passed, 81 files and 1031 tests.
- `npm run build`: passed.
- `git diff --check`: passed.

## Validation Commands

```bash
cd frontend-app
npm test -- backendApi.test.js -t "rejects skill resolution payloads without required conflict fields"
npm run lint
npm test
npm run build
git diff --check
```

## Stop Conditions

- Stop if any production skill resolution RPC is proven to allow missing `conflict_id` or missing `action`.
- Stop if an existing consumer intentionally uses `skillResolutionPayload` as a partial object builder.
- Do not refactor unrelated skill import, skill tool, or dashboard payload helpers.
