# Skill Resolution Apply Proof Validation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fail fast in the shared frontend API when applying a skill resolution without the server-issued preview proof.

**Architecture:** The fix belongs in `frontend-app/src/shared/api/backendApi.js` because `applySkillResolution` is the shared RPC wrapper. Page-level callers already check preview proof, but the API boundary should enforce the backend contract before any RPC is sent.

**Tech Stack:** React/Vite frontend, shared backend API wrapper, Vitest.

**Verification Surface:** `frontend-app/src/shared/api/backendApi.js`, `frontend-app/src/shared/api/backendApi.test.js`, full `frontend-app` lint/test/build.

---

## Review Scope

- `frontend-app/src/shared/api/backendApi.js`
- `frontend-app/src/shared/api/backendApi.test.js`
- `frontend-app/src/pages/skills/SkillsPage.jsx`
- `internal/module/skill/rpc_types.go`
- `internal/module/skill/rpc_resolution_apply_test.go`

## Evidence Summary

- Backend `applySkillResolution` calls `validateResolutionApplyProof` before selecting an apply target.
- `validateResolutionApplyProof` rejects missing `preview_id` and missing `preview_hash`.
- The Skills page UI checks `preview_id` and `preview_hash` before calling `applySkillResolution`.
- The shared frontend API currently normalizes `previewId`/`previewHash` to empty strings and still sends the RPC.
- Existing API tests only cover the valid preview proof path.

## Final Decision

Add payload validation in `applySkillResolutionPayload` so missing `preview_id` or `preview_hash` throws before `callBackend`.

## Unique Best Fix

Validate the normalized `preview_id` and `preview_hash` inside the shared API wrapper and include only validated values in the outgoing payload.

## Rejected Candidate Fixes

- Leave validation only in the page: other shared API callers can still bypass the UI guard.
- Rely on backend rejection: the frontend API already owns payload fail-fast checks for required mutation fields.
- Add a response validator: this is an input contract problem, not a response-shape problem.

## Upper Defense

Add an API regression test that proves missing `previewId` and missing `previewHash` do not call `callAPI`.

## Tasks

- [x] Add RED API test for missing skill resolution preview proof.
- [x] Implement payload validation in `applySkillResolutionPayload`.
- [x] Run focused backend API tests.
- [x] Run LSP diagnostics for modified files.
- [x] Run `npm run lint`, `npm test`, `npm run build`, and `git diff --check`.

## Validation Results

- Baseline before edits: `npm ci`, `npm run lint`, `npm test`, and `npm run build` passed in the r46 worktree.
- RED: `npm test -- backendApi.test.js -t "rejects skill resolution apply without preview proof"` failed because `applySkillResolution` did not throw for missing `previewId`.
- GREEN: the same focused test passed after payload validation.
- `npm test -- backendApi.test.js`: passed, 61 tests.
- LSP: modified `applySkillResolutionPayload` was read successfully; full `backendApi.js` diagnostics exceeded the tool output budget, and `backendApi.test.js` diagnostics timed out after opening the file.
- `npm run lint`: passed.
- `npm test`: passed, 81 files and 1030 tests.
- `npm run build`: passed.
- `git diff --check`: passed.

## Validation Commands

```bash
cd frontend-app
npm test -- backendApi.test.js -t "rejects skill resolution apply without preview proof"
npm run lint
npm test
npm run build
git diff --check
```

## Stop Conditions

- Stop if backend apply proof is proven optional for any production apply action.
- Stop if existing API consumers intentionally call apply without proof.
- Do not refactor unrelated skill import, skill editor, or response validator code.
