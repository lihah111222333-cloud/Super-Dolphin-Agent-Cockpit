# Skill Resolution Partial Failure Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:执行计划 to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent skill conflict resolution apply results with partial failures from being shown as fully successful.

**Architecture:** The fix belongs in `SkillsPage.jsx` because the page owns resolution workflow state and user feedback after `applySkillResolution`. The backend already returns `SkillMirrorResolutionReport`; the UI must interpret it instead of discarding it.

**Tech Stack:** React/Vite frontend, Vitest, Testing Library, mocked skills backend API.

**Verification Surface:** `frontend-app/src/pages/skills/SkillsPage.jsx`, `frontend-app/src/pages/skills/SkillsPage.test.jsx`, full `frontend-app` lint/test/build.

---

## Review Scope

- `frontend-app/src/pages/skills/SkillsPage.jsx`
- `frontend-app/src/pages/skills/SkillsPage.test.jsx`
- `frontend-app/src/pages/skills/services/skillsPageService.js`
- `internal/module/skill/mirror_reconciler.go`
- `internal/module/skill/rpc_types.go`
- `internal/module/skill/rpc.go`

## Evidence Summary

- `autoApplyResolutionPreview` and `confirmResolutionPreview` currently await `applySkillResolution(...)` and ignore the returned value.
- Both paths then show `已处理技能冲突` unconditionally.
- Backend `SkillMirrorResolutionReport` includes `PartialFailure` and `FollowUpAction`, with a code comment stating the frontend must know when follow-up retry action is needed.
- Existing Skills page tests mock `applySkillResolution` as `{ ok: true }` and do not cover partial failure feedback.

## Final Decision

Normalize the apply report in `SkillsPage.jsx` and route partial failures to the page error channel with the follow-up action label.

## Unique Best Fix

Add small helpers:

- Read `partialFailure`, `partial_failure`, or `PartialFailure`.
- Read `followUpAction`, `follow_up_action`, or `FollowUpAction`.
- On partial failure, clear stale notice and show an alert like `技能冲突已部分处理，后续需要重试：<action label>`.
- On full success, keep the existing `已处理技能冲突` notice.

## Rejected Candidate Fixes

- Ignore the report and rely on refresh state: refresh can still leave the user with a misleading success message.
- Throw from the API layer on `partialFailure`: the mutation may have partially succeeded, so the page should show a precise follow-up action rather than treating it as a generic RPC failure.
- Patch only manual preview confirmation: auto-applied resolution actions use a separate path with the same bug.

## Upper Defense

Add a Skills page regression test where `applySkillResolution` returns `partialFailure: true` and `followUpAction`. The test must verify that the page shows an alert and does not show the normal success notice.

## Tasks

- [x] Add RED UI test for partial skill resolution apply feedback.
- [x] Implement report normalization and feedback helpers in `SkillsPage.jsx`.
- [x] Use the helpers in both auto-apply and confirm-preview apply paths.
- [x] Run focused Skills page tests.
- [x] Run `npm run lint`, `npm test`, `npm run build`, `git diff --check`, and LSP diagnostics where available.

## Validation Results

- RED: `npm test -- SkillsPage.test.jsx -t "shows partial failure feedback"` timed out waiting for an alert before implementation because the page showed normal success feedback instead.
- GREEN: `npm test -- SkillsPage.test.jsx -t "shows partial failure feedback"` passed after report feedback handling.
- `npm test -- SkillsPage.test.jsx`: passed, 13 tests.
- LSP diagnostics: `SkillsPage.jsx` and `SkillsPage.test.jsx` returned 0 diagnostics.
- `npm run lint`: passed.
- `npm test`: passed, 81 files and 1029 tests.
- `npm run build`: passed.
- `git diff --check`: passed.

## Validation Commands

```bash
cd frontend-app
npm test -- SkillsPage.test.jsx -t "shows partial failure feedback"
npm run lint
npm test
npm run build
git diff --check
```

## Stop Conditions

- Stop if backend apply report is proven to never expose partial failure to the frontend bridge.
- Stop if product copy requires a dedicated retry UI instead of alert feedback.
- Do not refactor unrelated skill editor, datasource, MCP server, or import summary behavior.
