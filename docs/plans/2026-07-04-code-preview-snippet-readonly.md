# Code Preview Snippet Readonly Repair Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:执行计划 or superpowers:子代理驱动开发 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent partial code-preview snippets from being saved as whole-file content and overwriting source files.

**Architecture:** The repair belongs in the chat code-preview adapter and both preview save entry points. The backend currently exposes full-file save semantics only; partial snippets must be rendered read-only unless a future backend contract explicitly supports ranged edits.

**Tech Stack:** React/Vite, Vitest, frontend chat runtime panel, Wails code-preview RPC.

**Verification Surface:** Targeted chat/code-preview tests, then full frontend `lint`, `npm test`, `build`, and `git diff --check`.

---

## Review Scope

- Worktree: `/home/l4place/Super-Dolphin/.worktrees/frontend-fixes-20260704-r4`
- Current base: `origin/main` at `2efd1f4d41383572fdaf62fb46105015975ad014`
- Review method: 20 first-round frontend production-risk agents, then 5 cross-adjudication agents.
- Relevant dimensions: D09 frontend state, D12 tests, D15 destructive action/data loss.

## Evidence Summary

```text
P0 | D09 destructive write | frontend-app/src/pages/chat/adapters/codePreviewAdapter.js:68-82 | Non-full-text code previews are built from result.snippet but marked editable, so saving a .go/.jsx preview can overwrite the whole file with only the snippet.
```

Source evidence:

- `internal/ui/wails/code_preview.go:166-172`: only full-text preview extensions return the whole file; normal code files return `buildSnippetResult`.
- `internal/ui/wails/code_preview.go:187-203`: snippet previews contain only a bounded line window with `startLine`, `endLine`, and `totalLines`.
- `frontend-app/src/pages/chat/adapters/codePreviewAdapter.js:68-82`: the snippet becomes `content`/`draft`, then any preview with `filePath` is marked editable.
- `frontend-app/src/pages/chat/ChatPage.jsx:147-154`: save sends `codePreview.draft` as whole-file `content`.
- `frontend-app/src/pages/chat/components/RuntimePanel.jsx:91-98`: runtime panel has the same whole-file save path.
- `internal/ui/wails/code_preview.go:78-96`: backend save writes the supplied content with `os.WriteFile`.

## Final Adjudication

All returned adjudicators selected this issue as the best round-r4 fix.

Reason: it is a confirmed reachable data-loss path with a small, testable frontend patch. Security/privacy findings such as observability redaction, prompt/profile diagnostics, and preview URL allowlisting remain valid follow-ups, but they require broader policy choices. This issue has an immediate fail-closed fix: do not permit editing/saving when the preview is not the complete file.

## Upper Defense

Required.

Best landing points:

- `frontend-app/src/pages/chat/adapters/codePreviewAdapter.js`
  - Add a strict full-preview detector.
  - Default missing or inconsistent line metadata to read-only.
  - Keep images read-only.
  - Keep markdown/text full previews editable when `startLine === 1` and `endLine === totalLines`.
- `frontend-app/src/pages/chat/ChatPage.jsx`
  - Add a save guard so stale UI state cannot call `saveCodeFile` for non-editable previews.
- `frontend-app/src/pages/chat/components/RuntimePanel.jsx`
  - Add the same save guard for runtime diff preview saves.
- Tests
  - Lock partial snippets as read-only.
  - Preserve full-preview save behavior.
  - Assert no backend save call can be made for a partial snippet path.

## Implementation Tasks

### Task 1: Add regression tests

- [x] Add or update tests proving partial snippets do not expose the editor/save action.
- [x] Add or update tests proving full previews remain editable and saveable.
- [x] Run focused tests and confirm they fail before the implementation.

### Task 2: Make partial previews read-only

- [x] Add strict full-preview detection in `codePreviewAdapter.js`.
- [x] Set `editable: false` and `editing: false` for partial snippets.
- [x] Preserve existing image and markdown full-preview behavior.

### Task 3: Add save-entry guards

- [x] Guard `ChatPage.savePreviewChanges` against non-editable previews.
- [x] Guard `RuntimePanel.savePreviewChanges` against non-editable previews.
- [x] Keep the guard user-visible by setting a save error instead of silently returning when invoked from stale UI.

### Task 4: Verify

- [x] Run targeted tests for the changed chat/code-preview areas.
- [x] Run `cd frontend-app && npm run lint && npm test && npm run build`.
- [x] Run `git diff --check`.

## Verification Commands

```bash
cd frontend-app
npm test -- --run src/pages/chat/ChatPage.test.jsx src/pages/chat/components/RuntimePanelSlot.test.jsx
npm run lint
npm test
npm run build
git diff --check
```

## Stop Conditions / Follow-Up Boundary

- Stop and re-adjudicate if backend `ui/code/open` is proven to always return full-file content for saveable previews; current evidence says normal code files return snippets.
- Do not implement ranged save semantics in this commit.
- Do not include observability redaction, prompt/profile diagnostics redaction, workflow idempotency, memory/skill/cron validators, or preview URL allowlists in this commit.
