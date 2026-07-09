# 2026-07-09 Frontend Zod Normalization Next Wave Plan

## Goal

Continue the frontend mature-dependency replacement work with two high-value, low-risk zod migrations. Both targets replace hand-written backend response shape checks while preserving the current UI workflows and keeping changes independently reviewable.

## Current Evidence

- `frontend-app/src/features/prompts/PromptPageView.jsx:33-43` manually normalizes prompt list items and issues.
- `frontend-app/src/features/prompts/PromptPageView.jsx:76-78` calls the prompt list normalizer from both `prompt-assets/list` and readonly `dashboard/prompts` fallback paths.
- `frontend-app/src/pages/skills/SkillsPage.jsx:18-30` manually validates the skills dashboard response and required skill fields.
- `frontend-app/src/pages/skills/SkillsPage.jsx:55-60` manually accepts either `items` or `conflicts` from skill resolution responses.
- LSP references show `normalizePromptList` is only used by `fetchPromptAssetsSurface`, and `normalizeSkillsResponse` is only used by `fetchSkillsDashboard`.
- LSP diagnostics for both target files are currently empty.

## Non-Goals

- Do not change datasource/tool DTO handling in this wave.
- Do not change TanStack Query cache keys or refetch policy.
- Do not change prompt intent draft save payloads.
- Do not change skill resolution action labels, preview/apply payloads, or conflict decision UI.
- Do not add dependencies; `zod` is already present in `frontend-app/package.json`.

## A Candidate 1: Prompt Response Normalization With Zod

### Scope

- Primary file: `frontend-app/src/features/prompts/PromptPageView.jsx`
- Test file: `frontend-app/src/features/prompts/PromptPageView.test.jsx`

### Planned Change

Replace the hand-written prompt response container/item checks with local zod schemas:

- Add a `promptListResponseSchema` requiring an object with `prompts: array`.
- Add a passthrough prompt item schema so existing aliases stay accepted:
  - `id`, `prompt_key`, `promptKey`, `draft_key`, `draftKey`
  - `name`, `title`, `content`, `prompt_text`, `promptText`, `hint`
  - `description`, `summary`, `when_to_use`, `whenToUse`
  - `tags`, `assetType`, `asset_type`, `kind`, `prompt_kind`
  - `agent_key`, `agentKey`, `agentType`
  - `scope`, `Scope`, `state`, `status`, `draft_status`, `draftStatus`
  - `issues`, `card`, `isDefault`, `is_default`, `match_when`, `matchWhen`
- The item schema should only tighten "item must be an object"; fields should stay `z.unknown().optional()` plus `.passthrough()` and flow through the existing transforms.
- Keep existing transforms for tags, asset type, scope, priority, enabled, pending draft state, and preview text.
- Parse both `listPromptAssets` and `getDashboardPrompts` responses through the same schema.

### Side Effects

- Malformed prompt list responses become visible sync failures instead of silently rendering an empty list.
- Primitive or array prompt items become schema failures instead of becoming synthetic `prompt-${index}` rows.
- The readonly fallback still works when `prompt-assets/list` is not registered; zod only parses the fallback payload after that fallback is selected.

### Required Test Coverage

- Existing readonly fallback test must still show readonly mode and call `getDashboardPrompts`.
- Existing global scope and risk confirmation tests must still submit `confirmGlobal` and `confirmRisk`.
- Add or keep focused fail-fast tests for malformed prompt responses so the contract change is explicit:
  - primary malformed `prompt-assets/list` response should not trigger readonly fallback;
  - malformed fallback `dashboard/prompts` payload should show a sync failure after the method-not-registered fallback is selected.

### Focused Verification

```bash
cd frontend-app
npx vitest run src/features/prompts/PromptPageView.test.jsx --no-file-parallelism --maxWorkers=1
```

## A Candidate 2: Skills Dashboard And Resolution Response Schemas With Zod

### Scope

- Primary file: `frontend-app/src/pages/skills/SkillsPage.jsx`
- Test file: `frontend-app/src/pages/skills/SkillsPage.test.jsx`

### Planned Change

Replace dashboard and resolution response container checks with local zod schemas:

- Add a `skillsDashboardResponseSchema` requiring an object with `skills: array`.
- Add a passthrough skill item schema and keep the existing required field messages for:
  - `name`
  - `dir`
  - `skill_file`
- Add a `skillResolutionResponseSchema` accepting:
  - an array response
  - an object with `items: array`
  - an object with `conflicts: array`
- Keep the existing normalization functions for conflict kind labels, same-name source labels, external personal/project actions, provider labels, preview paths, and apply payload fields.

### Side Effects

- Invalid dashboard payloads keep failing fast with user-visible errors.
- Resolution responses with neither `items` nor `conflicts` remain fail-fast.
- The migration should not change conflict card labels, available action filtering, preview intro copy, or apply payload shape.

### Required Test Coverage

- Existing `skill_file` missing test must keep the current fail-fast message.
- Add or keep equivalent missing-field tests for `name`, `dir`, and `skill_file`; each should keep the existing `skills dashboard response item 0 is missing <field>` message.
- Add or keep container fail-fast tests for:
  - dashboard payload where `skills` is not an array, preserving `skills dashboard response skills must be an array`;
  - resolution payload object with neither `items` nor `conflicts`, preserving `skill resolutions response items must be an array`.
- Existing mirror/canonical resolution apply test must still show partial-failure feedback and follow-up action text.
- Add or keep coverage that the `conflicts` response alias is parsed without changing action labels:
  - use a `conflicts: [...]` fixture for a mirror/canonical conflict;
  - assert the action label remains `用本项目内容覆盖外部版本`;
  - assert preview/apply payloads still include the expected `action`, `preview_id`, and `preview_hash` fields.

### Focused Verification

```bash
cd frontend-app
npx vitest run src/pages/skills/SkillsPage.test.jsx --no-file-parallelism --maxWorkers=1
```

## Parallel Agent Plan

1. Plan adjudication agents:
   - Prompt schema reviewer: validate prompt scope, fail-fast behavior, and tests.
   - Skills schema reviewer: validate skills/resolution scope, labels, payloads, and tests.
   - Integration reviewer: validate atomic commit boundaries and full verification plan.
2. Implementation agents:
   - Prompt worker owns only `PromptPageView.jsx` and `PromptPageView.test.jsx`.
   - Skills worker owns only `SkillsPage.jsx` and `SkillsPage.test.jsx`.
3. Main session responsibilities:
   - Integrate worker patches.
   - Run LSP diagnostics for changed source and test files.
   - Run focused tests, full frontend validation, project-map/codemap checks, and pre-push guards.
   - Split commits by plan, prompt migration, and skills migration.

## Atomic Commit Order

The current local branch already contains the approved carry-forward commits from the previous replacement phase:

1. `docs(frontend): 记录 A 级替换实施计划`
2. `chore(frontend): 稳定 code size guard 冻结签名`
3. `test(frontend): 用 AST 收敛 critical skip 守卫`

This next wave appends three new commits:

1. `docs(frontend): 记录 zod 归一化下一步计划`
2. `refactor(frontend): 用 zod 收敛提示词响应归一化`
3. `refactor(frontend): 用 zod 收敛技能响应归一化`

Before pushing, list `git log --oneline origin/main..HEAD` and confirm it contains only the three carry-forward commits above plus the three next-wave commits. If any unexpected commit appears, stop and rebase/cherry-pick onto the latest `origin/main` before continuing.

Generated project-map/codemap files should be included only in the commit whose source/doc change caused them.
Shared generated files such as `docs/doc/codemap/project-map/index/app-ui.tsv` are owned by the main session integration step; implementation workers must not refresh or stage generated project-map/codemap files.

## Full Verification Before Push

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Refresh commands, only when source/doc changes require generated maps to move:

```bash
go run scripts/codemap_index.go
node scripts/generate_ai_project_map.mjs --filesystem-scan
```

Final checks after commit splitting:

```bash
make codemap-check
make project-map-check
git diff --check
git status --short
```

Before syncing `origin/main`, run `git fetch origin main`; if `origin/main` moved, rebase, resolve conflicts, and rerun matching verification. The user has explicitly authorized syncing this work to remote `main`; still verify the final `origin/main..HEAD` commit list immediately before `git push origin HEAD:main`.

## Completion-Gated Follow-Up Candidates

These remain candidates after the two A tasks above are implemented and verified:

1. Skills datasource and tools DTO schemas with zod:
   - Current area: `frontend-app/src/pages/skills/SkillsPage.jsx` datasource and tool list helpers.
   - Preserve `useQuery` / `useInfiniteQuery` behavior and `assertDatasourceChunkPageProgress`.

2. Settings runtime/preferences Query migration:
   - Current area: `frontend-app/src/pages/settings/SettingsPage.jsx`.
   - Requires dirty draft protection and explicit `refetchOnWindowFocus` choices.

3. Prompt focus refresh cleanup:
   - Current area: `frontend-app/src/features/prompts/PromptPageView.jsx`.
   - Must prove Query focus refetch does not duplicate RPC calls or break active prompt cleanup.

4. Runtime activity popovers with React Aria:
   - Current area: `frontend-app/src/pages/chat/components/RuntimeActivityPanel.jsx`.
   - Must preserve warning redaction and runtime panel resize semantics.

5. Image lightbox with React Aria Modal:
   - Current area: `frontend-app/src/pages/chat/components/ImageLightbox.jsx`.
   - Must preserve Mermaid sanitizer behavior and image preview close flows.

6. `rpc-contract-audit` AST parsing:
   - Current area: `frontend-app/scripts/rpc-contract-audit.mjs`.
   - Larger guard rewrite; split JS contract AST parsing from Go source scanning.

7. `frontend-code-size-guard` AST metrics:
   - Current area: `frontend-app/scripts/frontend-code-size-guard.mjs`.
   - Run as shadow comparison before replacing metrics; do not update or lower baseline as part of the migration.

8. Workflow schedule cron parsing against the new workflow module split:
   - Current area: `frontend-app/src/pages/workflows/services/workflowScheduleModel.js`.
   - The previous `WorkflowPage.jsx` plan entry must be re-evaluated because `origin/main` moved schedule parsing out of the page component.
