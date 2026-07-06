# Code Preview Save Snapshot Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent code preview save success from treating edits typed during the in-flight save as persisted file content.

**Architecture:** The fix belongs in the shared code preview adapter because both `ChatPage` and `RuntimePanel` have the same save-state transition. The save request must capture a `savedDraft` snapshot before awaiting the backend, and the success transition must update `content` from that snapshot, not from the later live draft.

**Tech Stack:** React/Vite frontend, Vitest, Testing Library, shared frontend adapter functions.

**Verification Surface:** `frontend-app/src/pages/chat/adapters/codePreviewAdapter.test.js`, `frontend-app/src/pages/chat/ChatPage.test.jsx`, `frontend-app/src/pages/chat/components/RuntimePanelSlot.test.jsx`, full `frontend-app` lint/test/build.

---

## Review Scope

- `frontend-app/src/pages/chat/ChatPage.jsx`
- `frontend-app/src/pages/chat/components/RuntimePanel.jsx`
- `frontend-app/src/pages/chat/components/CodePreviewDialog.jsx`
- Existing code preview tests in `ChatPage.test.jsx` and `RuntimePanelSlot.test.jsx`

## Evidence Summary

- `ChatPage.jsx` sends `content: codePreview.draft` to `saveCodeFile`, then on success writes `content: current.draft`.
- `RuntimePanel.jsx` has the same pattern.
- `CodePreviewDialog.jsx` keeps the editor enabled during `preview.saving`, so the user can continue editing while the backend save promise is in flight.
- If the draft changes before the save resolves, the UI marks the new draft as saved even though the backend only received the old snapshot.

## Final Decision

Fix the shared save success transition and use it from both code preview save call sites.

## Unique Best Fix

Add `codePreviewStateAfterSave(current, result, relative, savedDraft)` in `codePreviewAdapter.js`:

- Set `content` to `savedDraft`.
- Keep `draft` as the current live draft.
- Count `totalLines` from `savedDraft` when the backend does not return a valid line count.
- For Markdown previews, leave `editing` true if the live draft differs from `savedDraft`; only exit editing when no extra edits happened.
- Use a status that makes the remaining dirty state visible.

## Rejected Candidate Fixes

- Disable textarea input while saving: avoids the race but blocks normal editing and still leaves duplicated save-state logic in two call sites.
- Patch only `ChatPage.jsx`: misses the duplicated `RuntimePanel.jsx` path.
- Files page stale preview guard: real candidate, but this round's code preview bug is a clearer user data-state issue with an immediate shared fix.
- Memory target validation: real contract-hardening candidate, but not as directly user-visible as incorrect saved/dirty state.
- Skill apply response handling: needs deeper product decision about partial failures and follow-up actions; defer until it can be proven from backend response shape.

## Upper Defense

The upper defense is an adapter-level regression test for the shared state transition. This prevents the two UI call sites from drifting back to live-draft success writes.

## Tasks

- [x] Add `frontend-app/src/pages/chat/adapters/codePreviewAdapter.test.js` with RED coverage for an in-flight save snapshot where `current.draft !== savedDraft`.
- [x] Export and implement `codePreviewStateAfterSave` in `codePreviewAdapter.js`.
- [x] Replace duplicated success-state objects in `ChatPage.jsx` and `RuntimePanel.jsx` with the shared helper.
- [x] Run focused adapter and UI tests.
- [x] Run `npm run lint`, `npm test`, `npm run build`, `git diff --check`, and LSP diagnostics where available.

## Validation Commands

```bash
cd frontend-app
npm test -- codePreviewAdapter.test.js
npm test -- ChatPage.test.jsx -t "ignores stale code preview responses"
npm test -- RuntimePanelSlot.test.jsx
npm run lint
npm test
npm run build
git diff --check
```

## Stop Conditions

- Stop if a failing test shows the existing dialog intentionally blocks editing during save.
- Stop if backend `saveCodeFile` returns a new authoritative content payload; current evidence shows the UI only gets metadata.
- Do not refactor unrelated preview opening, locating, or markdown rendering behavior.
