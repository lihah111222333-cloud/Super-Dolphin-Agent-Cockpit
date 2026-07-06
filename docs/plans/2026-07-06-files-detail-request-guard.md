# Files Detail Request Guard Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent an older shared-file detail request from overwriting the preview opened by the user's latest file selection.

**Architecture:** The fix belongs in `FilesPage.jsx` where the shared-file action hook owns `selectedFile` state. The detail loader can still fetch and adapt file content, but only the latest open request may commit `selectedFile`.

**Tech Stack:** React/Vite frontend, Vitest, Testing Library, shared file service mocks.

**Verification Surface:** `frontend-app/src/pages/files/FilesPage.test.jsx`, `frontend-app/src/pages/files/FilesPage.jsx`, full `frontend-app` lint/test/build.

---

## Review Scope

- `frontend-app/src/pages/files/FilesPage.jsx`
- `frontend-app/src/pages/files/FilesPage.test.jsx`
- `frontend-app/src/services/modules/fileService.js`

## Evidence Summary

- `useSharedFileActions.openFile` currently awaits `loadFileDetail(file)` and immediately calls `setSelectedFile(...)`.
- There is no request sequence, abort guard, or selected path check around that state write.
- Opening file A and then file B can leave two in-flight `readSharedFile` calls. If B resolves first and A resolves later, A becomes the visible preview even though B was the user's latest action.
- Local review used LSP symbol reads and references for `openFile`; external sub-agent review was unavailable because the platform returned usage-limit errors.

## Final Decision

Guard `openFile` with a monotonically increasing request id and only apply the result or error for the latest request.

## Unique Best Fix

Add an `openRequestRef` inside `useSharedFileActions`:

- Increment it for every `openFile` invocation.
- Capture the request id before awaiting `loadFileDetail`.
- Commit `setSelectedFile(detail)` only when the captured id is still current.
- Suppress stale text-file read errors so an obsolete failure does not replace the latest selection with an error notice.

## Rejected Candidate Fixes

- Compare only `selectedFile.path`: there may be no selected file yet, and repeated opens of the same path still need deterministic latest-request behavior.
- Move the guard into `useSharedFileDetailLoader`: export uses the same loader and should not be affected by preview selection state.
- Disable every other open button while one file is loading: this avoids the race by blocking a normal quick-selection workflow and still depends on busy-path UI state.

## Upper Defense

Add a Files page regression test with two deferred `readSharedFile` responses. It must prove that a slow earlier response cannot replace the later preview.

## Tasks

- [x] Add RED coverage in `FilesPage.test.jsx` for stale detail responses.
- [x] Add the request id guard in `FilesPage.jsx`.
- [x] Run focused Files page tests.
- [x] Run `npm run lint`, `npm test`, `npm run build`, `git diff --check`, and LSP diagnostics where available.

## Validation Results

- RED: `npm test -- FilesPage.test.jsx` failed before implementation because the stale `reports/first.md` detail replaced the latest `reports/second.md` preview.
- GREEN: `npm test -- FilesPage.test.jsx` passed after the request guard.
- `npm run lint`: passed.
- `npm test`: passed, 81 files and 1027 tests.
- `npm run build`: passed.
- `git diff --check`: passed.
- LSP diagnostics: `FilesPage.test.jsx` returned 0 diagnostics; `FilesPage.jsx` did not become ready in LSP after two attempts, so lint/build/test are the authoritative local checks for that file.

## Validation Commands

```bash
cd frontend-app
npm test -- FilesPage.test.jsx
npm run lint
npm test
npm run build
git diff --check
```

## Stop Conditions

- Stop if a focused test shows the UI already serializes file opening and cannot produce concurrent detail reads.
- Stop if the fix requires changing `fileService` response semantics or backend API contracts.
- Do not refactor unrelated file export, delete, retention, or JSON preview formatting behavior.
