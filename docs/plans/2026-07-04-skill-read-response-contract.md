# Skill Read Response Contract Repair Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Prevent malformed `skills/local/read` success responses from opening an empty skill editor and later overwriting skill content.

**Architecture:** The fix belongs at the shared frontend API facade in `frontend-app/src/shared/api/backendApi.js`, because both `SkillsPage` editor paths call `readSkill()` through that boundary. The upper defense is contract metadata in `backendApi.contractMatrix.js` plus existing `rpc-contract-audit.mjs` validator-name enforcement. Page code should benefit from the boundary without adding local fallback defaults.

**Tech Stack:** React/Vite, Vitest, frontend API contract matrix, existing RPC contract audit.

**Verification Surface:** `frontend-app` targeted `backendApi` and contract matrix tests, then full `lint`, `npm test`, `build`, and `git diff --check`.

---

## Review Scope

- Worktree: `/home/l4place/Super-Dolphin/.worktrees/frontend-fixes-20260704-r2`
- Current base: `origin/main` at `a93b6a2a324e70c6fccafee92941f49441aba55d`
- Review method: 20 first-round frontend production-risk agents, then 5 cross-adjudication agents.
- Relevant dimensions: D02 fail-fast, D09 frontend boundary, D12 tests, D17 field guard.

## Evidence Summary

```text
P1 | D02/D09/D17 | frontend-app/src/pages/skills/SkillsPage.jsx:1970 | readSkill missing content is normalized to "" | rawSkill?.skill?.content || "" opens an empty editor; saveSkillEditor later writes payload.content through writeSkill | add an API response validator for SKILLS_LOCAL_READ before SkillsPage normalization
```

Source evidence:

- `frontend-app/src/shared/api/backendApi.js:1376-1379`: `readSkill()` calls `skills/local/read` through `callBackend` with no response validator.
- `frontend-app/src/shared/api/backendApi.js:945-951`: current validator registry covers UI/thread/turn only.
- `frontend-app/src/shared/api/backendApi.contractMatrix.js:142`: `SKILLS_LOCAL_READ` is P1 but has no `responseValidator`.
- `frontend-app/src/pages/skills/SkillsPage.jsx:1970-1983`: missing `rawSkill.skill.content` becomes an empty string for both main skill edit and child file edit.
- `frontend-app/src/pages/skills/SkillsPage.jsx:2032-2070`: save writes the derived form content back through `writeSkill`.

## Final Adjudication

The unique best repair is `skills/local/read` response validation at the shared frontend API boundary.

This beats page-local fallback edits because the same `readSkill()` facade feeds `openEditSkill`, `openSkillFile`, citation loading, and import summary paths. A facade validator blocks malformed success responses before any page normalizer can turn absent content into safe-looking empty content.

## Rejected Candidates

- Observability redaction: high-value security issue, but a separate diagnostics sink problem and larger privacy projection scope.
- Send failure rollback after thread switch: real P1 user-state bug, but it does not risk destructive content overwrite.
- Model provider malformed registry: real fail-fast gap, but backend save/apply has stronger registry validation than skill file writes.
- Prompt and memory list content previews: real privacy risks, but page-local and not as directly destructive as overwriting skill content.
- Existing `createBackendCaller` response-guard foundation: already pushed to `origin/main` as `a93b6a2a`.

## Upper Defense

Required.

Best landing points:

- `frontend-app/src/shared/api/backendApi.js`
  - Add `validateSkillReadResponse(method, response)`.
  - Require response is an object.
  - Require `response.skill` is a plain object.
  - Require `skill.path` is a non-empty string.
  - Require `skill.content` exists and is a string. Empty string is allowed only as explicit backend data, not as a missing-field fallback.
  - Register it under `RPC_METHODS.SKILLS_LOCAL_READ`.

- `frontend-app/src/shared/api/backendApi.contractMatrix.js`
  - Add `{ responseValidator: 'skillReadResponse' }` to `SKILLS_LOCAL_READ`.

- Tests:
  - `frontend-app/src/shared/api/backendApi.test.js`
  - `frontend-app/src/shared/api/backendApi.contractMatrix.test.js`
  - Add `SkillsPage` UI regression only if the facade-level regression does not prove the page cannot receive malformed success data.

## Implementation Tasks

### Task 1: Add fail-fast skill read response validation

- [x] Add `validateSkillReadResponse()` next to the other response validators in `backendApi.js`.
- [x] Add `SKILLS_LOCAL_READ` to `BACKEND_RESPONSE_VALIDATORS`.
- [x] Add malformed-success cases to `backendApi.test.js`:
  - missing `skill`
  - `skill` not an object
  - missing `skill.path`
  - missing `skill.content`
  - non-string `skill.content`
- [x] Preserve a valid empty content string test so the validator does not reject explicit empty files.

### Task 2: Register response policy metadata

- [x] Add `responseValidator: 'skillReadResponse'` to `RPC_CONTRACT_REGISTRY.SKILLS_LOCAL_READ`.
- [x] Extend the known contract exceptions test to assert `SKILLS_LOCAL_READ.responseValidator === 'skillReadResponse'`.
- [x] Run `npm run audit:rpc-contracts` to prove the existing upper defense recognizes the validator name.

### Task 3: Verify page-level protection

- [x] Run targeted API and SkillsPage tests.
- [x] Confirmed facade-level validation blocks malformed success data before `SkillsPage`; no page-level fallback change was needed.
- [x] Do not add page-level default content fallbacks.

## Verification Commands

```bash
cd frontend-app
npm test -- src/shared/api/backendApi.test.js src/shared/api/backendApi.contractMatrix.test.js src/pages/skills/SkillsPage.test.jsx
npm run audit:rpc-contracts
npm run lint
npm test
npm run build
git diff --check
```

## Stop Conditions / Follow-Up Boundary

- Stop and re-adjudicate if backend `skills/local/read` is proven to intentionally omit `skill.content`; current evidence says it returns `skill.content`.
- Do not include observability redaction, prompt previews, model provider registry validation, or send rollback in this commit.
- Do not weaken fail-fast behavior by defaulting missing `content` to an empty string at any layer.
