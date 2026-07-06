# Workflow Refresh Failure Notice Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Stop silently hiding Workflow page refresh failures after a successful mutation.

**Architecture:** The fix belongs in `frontend-app/src/pages/workflows/WorkflowPage.jsx` because Workflow actions own the post-mutation refresh and success notice. Mutations can succeed while the follow-up refresh fails; the UI should keep the success semantics but surface the stale-refresh risk.

**Tech Stack:** React/Vite frontend, TanStack Query, Testing Library, Vitest.

**Verification Surface:** `frontend-app/src/pages/workflows/WorkflowPage.jsx`, `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`, full `frontend-app` lint/test/build.

---

## Review Scope

- `frontend-app/src/pages/workflows/WorkflowPage.jsx`
- `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`

## Evidence Summary

- Multiple Workflow mutation paths call `refreshDags()` or `refreshDetail()` with `.catch(() => [])` or `.catch(() => {})`.
- Those paths then show pure success notices such as `已启动自动化`.
- The underlying mutation may have succeeded, so treating a refresh failure as mutation failure would be misleading.
- The current behavior also hides the fact that the visible DAG state may be stale.

## Final Decision

Capture refresh failures and append a warning suffix to the success notice: `但刷新状态失败：<reason>`.

## Unique Best Fix

Add small Workflow refresh helpers that return `{ items, error }`, then use the error to decorate existing success notices without changing the mutation success path.

## Rejected Candidate Fixes

- Let refresh failures fall into the mutation catch: this would report successful backend mutations as failed.
- Keep swallowing refresh errors: users can see stale state with no explanation.
- Refactor Workflow query ownership broadly: not needed for this focused production risk.

## Upper Defense

Add a regression test where `startDag` succeeds but the follow-up detail refresh fails, and assert the success notice includes the refresh failure reason.

## Tasks

- [x] Add RED Workflow page test for successful start with failed refresh.
- [x] Add refresh result helpers and use them in post-mutation Workflow actions.
- [x] Run focused Workflow page tests.
- [x] Run LSP diagnostics or record tool limitations.
- [x] Run `npm run lint`, `npm test`, `npm run build`, and `git diff --check`.

## Validation Results

- Baseline before edits: `npm ci`, `npm run lint`, `npm test`, and `npm run build` passed in the r49 worktree.
- RED: `npm test -- WorkflowPage.test.jsx -t "shows refresh failure after a successful DAG start"` failed because the notice only showed `已启动自动化`.
- GREEN: the same focused test passed after surfacing refresh failure details.
- `npm test -- WorkflowPage.test.jsx`: passed, 40 tests.
- LSP: modified helper block was read successfully; diagnostics for `WorkflowPage.jsx` and `WorkflowPage.test.jsx` timed out before publishing.
- `npm run lint`: passed.
- `npm test`: passed, 81 files and 1032 tests.
- `npm run build`: passed.
- `git diff --check`: passed.

## Validation Commands

```bash
cd frontend-app
npm test -- WorkflowPage.test.jsx -t "shows refresh failure after a successful DAG start"
npm run lint
npm test
npm run build
git diff --check
```

## Stop Conditions

- Stop if refresh failures already surface through an existing Workflow sync alert in the same user action.
- Stop if product requires refresh failure to block success notices entirely.
- Do not change backend mutation semantics or Workflow query keys.
