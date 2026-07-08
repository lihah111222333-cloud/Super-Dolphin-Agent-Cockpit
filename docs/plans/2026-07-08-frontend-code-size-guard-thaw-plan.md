# Frontend Code Size Guard Thaw Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate the current frontend production code-size guard freeze: 55 frozen production files plus 2 frozen directory-size entries in `frontend-app`.

**Architecture:** Treat `frontend-app/scripts/frontend-code-size-guard.mjs` and the two baseline JSON files as the source of truth, but do not update or weaken the baselines as a way to pass. First add a non-default production/test scope flag to the guard CLI so production debt can be verified without mixing the existing test baseline debt into every lane. Workers then refactor production code into focused modules/components until `node scripts/frontend-code-size-guard.mjs --strict --scope production` passes for their paths, and the controller runs an explicit freeze refresh only after reviewing every deletion from the production baseline.

**Tech Stack:** React, Vite, JavaScript/JSX, Vitest, ESLint, repository LSP tools (`grep`, `structure`, `inspect`, `xref`, `file`, `edit`).

**Verification Surface:** `frontend-app`: `node scripts/frontend-code-size-guard.mjs`, `node scripts/frontend-code-size-guard.mjs --strict --scope production`, `npm run lint`, `npm test`, `npm run build`; LSP diagnostics for every edited file.

---

## Current Baseline

- Production frozen file entries: 55.
- Production frozen directory entries: 2.
- Production frozen violation signatures: 153.
- Current normal guard status: `frontend code size guard passed: files=232, frozen=92`; this passes only because baseline masks existing debt.
- Directory freeze entries to remove:
  - `__dir__:src/entities/client/model` at 25 production files.
  - `__dir__:src/pages/chat/components` at 42 production files.

Rule distribution in production baseline:

| Rule | Count |
|---|---:|
| `params` | 70 |
| `nesting` | 28 |
| `file-length` | 14 |
| `func-length` | 11 |
| `line-length` | 11 |
| `exports` | 10 |
| `any` | 7 |
| `empty-func` | 2 |

## Non-Negotiable Constraints

1. Do not edit `frontend-app/.frontend_code_size_guard_baseline.json` until a worker's source changes make the corresponding entry graduate under `--strict`.
2. Do not loosen `FRONTEND_CODE_SIZE_LIMITS`.
3. Do not suppress guard rules, delete tests, or replace real behavior with fallback code.
4. Use LSP evidence before editing: `grep` or `structure`, `inspect`, `xref`, `file(read_file)`, and `file(diagnostics)`.
5. Keep lanes isolated. A worker may edit only its assigned files unless it records a handoff request and the controller approves it.
6. Every worker must leave `npm test -- <focused tests>` or `npm test` evidence for changed behavior, plus `node scripts/frontend-code-size-guard.mjs --strict --scope production --dir <lane-root>` where the lane root is valid.

## Parallel Dispatch Topology

Run one prerequisite guard-CLI worker first, then 8 source workers in parallel, then one controller integration pass.

| Lane | Scope | Primary Freeze Target | Risk |
|---|---|---|---|
| 0 | Guard CLI production scope | `--scope production/test/all` verification support | Medium, script surface |
| A | Client store model | 16 file entries + `src/entities/client/model` dir count | High, shared state |
| B | Chat page and chat components | 15 file entries + `src/pages/chat/components` dir count | High, UI workflow |
| C | Big product pages | Prompt, Memory, Observability, Skills, Files, page import guard helper | High, broad UI |
| D | Shared API and Wails bridge | `backendApi`, validators, bridge, contract matrix | High, contract surface |
| E | Settings page, subcomponents, and services | line-length, exports, params, nesting | Medium |
| F | Workflow page and shared small helpers | params, line-length, empty-func | Medium |
| G | App shell split | `App.jsx` file/function/nesting/params | High, top-level shell |
| H | i18n and directory-count graduation | `appI18n.js`, small module moves that unblock dir freezes | Medium |
| I | Controller integration | baseline shrink, conflict resolution, final verification | High |

## Shared Worker Setup

Each worker starts with:

- [ ] **Step 1: Confirm source of truth**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs
node -e "const fs=require('fs'); for (const p of ['.frontend_code_size_guard_baseline.json','.frontend_code_size_guard_baseline_test.json']) { const d=JSON.parse(fs.readFileSync(p,'utf8')); console.log(p, Object.keys(d.files).length); }"
```

Expected: normal guard passes; production baseline has 57 entries.

- [ ] **Step 2: Collect LSP evidence for assigned files**

Use the exact assigned paths. For each representative file in the lane:

```text
grep(text_search or ast_search) -> structure(document_symbol) -> inspect(definition or hover) -> xref(references or call_hierarchy) -> file(read_file) -> file(diagnostics)
```

Expected: worker report includes tool/action, file path, symbol, reference count or call hierarchy result, and diagnostics result.

- [ ] **Step 3: Run strict lane guard before editing**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/<lane-root>
```

Expected: FAIL with violations in the assigned files. Save the exact violation list in the worker summary.

- [ ] **Step 4: Refactor, do not rewrite**

Prefer these mechanical moves:

```text
params -> replace wide parameter lists with one named options object at internal helper boundaries.
nesting -> extract guard clauses and pure helpers; avoid changing render branching semantics.
file-length -> move cohesive components/helpers to sibling files; update imports and focused tests.
func-length -> extract named pure helpers/components under the same feature folder.
exports -> group constants in one exported object, or move private helpers out of barrel-like files.
line-length -> split literal arrays/objects/JSX props without changing values.
any -> replace with unknown plus explicit validation, or a concrete JSDoc typedef where JS callers need structure.
empty-func -> replace no-op test/bridge placeholder with explicit behavior or remove unreachable hook after xref proves unused.
```

- [ ] **Step 5: Validate lane**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/<lane-root>
npm test -- <focused-test-file-or-pattern>
```

Expected: strict lane guard passes for edited roots, and focused tests pass.

## Lane 0: Guard CLI Production Scope

**Files:**
- Modify: `frontend-app/scripts/frontend-code-size-guard.mjs`
- Modify: `frontend-app/scripts/frontend-code-size-guard.test.mjs`

- [ ] **Step 0.1: Write failing CLI tests**

Add tests that exercise file filtering through exported helpers or a small exported parser/collector:

```js
expect(parseFrontendCodeSizeGuardArgs(['--scope', 'production']).scope).toBe('production');
expect(parseFrontendCodeSizeGuardArgs(['--scope', 'test']).scope).toBe('test');
expect(parseFrontendCodeSizeGuardArgs(['--scope', 'all']).scope).toBe('all');
expect(parseFrontendCodeSizeGuardArgs(['--file', 'src/App.jsx']).files).toEqual(['src/App.jsx']);
expect(() => parseFrontendCodeSizeGuardArgs(['--scope', 'bad'])).toThrow(/invalid value for --scope/);
```

Expected before implementation: tests fail because `--scope` is unsupported.

- [ ] **Step 0.2: Implement scope filtering without changing defaults**

Keep default behavior as `scope=all`. Filter after `collectFiles()`:

```js
function filterFilesByScope(files, scope) {
  if (scope === 'all') return files;
  if (scope === 'production') return files.filter((file) => !isFrontendTestFile(file.rel));
  if (scope === 'test') return files.filter((file) => isFrontendTestFile(file.rel));
  throw new Error(`invalid value for --scope: ${scope}`);
}
```

Wire `--scope <production|test|all>` and repeatable `--file <relative-source-file>` into `parseArgs()`, and fail fast if filtering produces zero files. `--file` must reject paths outside `frontend-app`, directories, non-source files, and files under ignored directories.

- [ ] **Step 0.3: Validate guard CLI**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
npm test -- scripts/frontend-code-size-guard.test.mjs
node scripts/frontend-code-size-guard.mjs
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/shared/i18n
node scripts/frontend-code-size-guard.mjs --strict --scope production --file src/App.jsx
```

Expected: tests pass; default guard output remains unchanged; scoped strict commands report only production `appI18n.js` and `App.jsx` violations.

## Lane A: Client Store Model

**Files:**
- Modify: `frontend-app/src/entities/client/model/bridgePatchState.js`
- Modify: `frontend-app/src/entities/client/model/composerAttachments.js`
- Modify: `frontend-app/src/entities/client/model/composerSlice.js`
- Modify: `frontend-app/src/entities/client/model/contractStoreModel.js`
- Modify: `frontend-app/src/entities/client/model/forkSlice.js`
- Modify: `frontend-app/src/entities/client/model/projectSlice.js`
- Modify: `frontend-app/src/entities/client/model/providerRuntimeConfig.js`
- Modify: `frontend-app/src/entities/client/model/runtimeAssistantTimeline.js`
- Modify: `frontend-app/src/entities/client/model/runtimeResults.js`
- Modify: `frontend-app/src/entities/client/model/runtimeSlice.js`
- Modify: `frontend-app/src/entities/client/model/threadForkState.js`
- Modify: `frontend-app/src/entities/client/model/threadLifecycleRuntime.js`
- Modify: `frontend-app/src/entities/client/model/threadMessagesPagination.js`
- Modify: `frontend-app/src/entities/client/model/threadMessagesRuntime.js`
- Modify: `frontend-app/src/entities/client/model/timelineRuntime.js`
- Modify: `frontend-app/src/entities/client/model/useClientStore.js`
- Test: `frontend-app/src/entities/client/model/*.test.js`

- [ ] **Step A1: Establish failing strict baseline**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/entities/client/model
```

Expected: FAIL includes `useClientStore.js`, model nesting, params, exports, and directory-size debt.

- [ ] **Step A2: Split `useClientStore.js` by already-named slices**

Create or extend slice modules only when the state/action group is already visible in `useClientStore.js`. Move pure helpers and action groups into existing files such as `runtimeSlice.js`, `composerSlice.js`, `forkSlice.js`, and `timelineRuntime.js`; avoid a generic `utils.js`.

- [ ] **Step A3: Reduce directory file count**

If the directory still has more than 15 production files after splits, merge tiny single-purpose helpers into their owning slice file or move page-specific helpers out to the page feature folder. Do not create new files in `src/entities/client/model` unless another file is removed from the same directory.

- [ ] **Step A4: Validate**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/entities/client/model
npm test -- src/entities/client/model
```

Expected: no strict guard violations in `src/entities/client/model`, model tests pass.

## Lane B: Chat Page And Components

**Files:**
- Modify: `frontend-app/src/pages/chat/ChatPage.jsx`
- Modify: `frontend-app/src/pages/chat/adapters/composerModelSelectorState.js`
- Modify: `frontend-app/src/pages/chat/adapters/runtimeDiffLineAdapter.js`
- Modify: `frontend-app/src/pages/chat/components/ComposerAttachments.jsx`
- Modify: `frontend-app/src/pages/chat/components/MarkdownMessage.jsx`
- Modify: `frontend-app/src/pages/chat/components/MermaidDiagram.jsx`
- Modify: `frontend-app/src/pages/chat/components/ProjectSelector.jsx`
- Modify: `frontend-app/src/pages/chat/components/RuntimeDiffView.jsx`
- Modify: `frontend-app/src/pages/chat/components/RuntimePanel.jsx`
- Modify: `frontend-app/src/pages/chat/components/ThreadCard.jsx`
- Modify: `frontend-app/src/pages/chat/components/ThreadCardActions.jsx`
- Modify: `frontend-app/src/pages/chat/components/markdownMessageModel.js`
- Modify: `frontend-app/src/pages/chat/hooks/useChatWorkbenchLayout.js`
- Modify: `frontend-app/src/pages/chat/hooks/useComposerInteractions.js`
- Modify: `frontend-app/src/pages/chat/hooks/useSmoothStreamingText.js`
- Test: `frontend-app/src/pages/chat/**/*.test.*`

- [ ] **Step B1: Establish failing strict baseline**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/pages/chat
```

Expected: FAIL includes `ChatPage.jsx`, `MarkdownMessage.jsx`, `RuntimePanel.jsx`, params, nesting, and `src/pages/chat/components` directory-size debt.

- [ ] **Step B2: Split render-heavy files by visible UI regions**

Move cohesive JSX sections out of `ChatPage.jsx`, `MarkdownMessage.jsx`, and `RuntimePanel.jsx` into feature-specific sibling files. Prefer existing component names and props that reflect UI boundaries: header, timeline, composer, runtime panel, markdown block, tool result block.

- [ ] **Step B3: Collapse wide props**

For `ThreadCard`, `ThreadCardActions`, `RuntimeDiffView`, `ProjectSelector`, and adapter helpers, replace helpers with more than 5 parameters by a named options object. Keep call sites explicit:

```js
buildRuntimeDiffLine({
  line,
  index,
  mode,
  isSelected,
  onSelect,
  formatPath,
});
```

- [ ] **Step B4: Reduce `components` directory count**

The target is 15 or fewer production files in `src/pages/chat/components`. Move model-only helpers to `src/pages/chat/model` or fold one-off leaf components into their owning parent when xref proves a single caller.

- [ ] **Step B5: Validate**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/pages/chat
npm test -- src/pages/chat
```

Expected: strict chat guard passes, chat tests pass.

## Lane C: Big Product Pages

**Files:**
- Modify: `frontend-app/src/features/prompts/PromptPageView.jsx`
- Modify: `frontend-app/src/pages/files/FilesPage.jsx`
- Modify: `frontend-app/src/pages/importSurfaceGuard.test-helper.js`
- Modify: `frontend-app/src/pages/memory/MemoryPage.jsx`
- Modify: `frontend-app/src/pages/observability/ObservabilityPage.jsx`
- Modify: `frontend-app/src/pages/skills/SkillsPage.jsx`
- Modify: `frontend-app/src/pages/skills/services/skillsPageService.js`
- Test: matching page tests under `frontend-app/src/**/*Page*.test.*`

- [ ] **Step C1: Establish failing strict baseline**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --file src/pages/files/FilesPage.jsx
node scripts/frontend-code-size-guard.mjs --strict --scope production --file src/pages/importSurfaceGuard.test-helper.js
node scripts/frontend-code-size-guard.mjs --strict --scope production --file src/pages/memory/MemoryPage.jsx
node scripts/frontend-code-size-guard.mjs --strict --scope production --file src/pages/observability/ObservabilityPage.jsx
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/pages/skills
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/features/prompts
```

Expected: FAIL includes page file-length, params, nesting, function-length, and line-length debt.

- [ ] **Step C2: Split by page panels**

For each page file above 500 effective lines, extract one visible panel or table at a time into `components/` under the same page folder. Keep page files as orchestration shells: data loading, selected state, and composed child components.

- [ ] **Step C3: Convert page helper params to options objects**

When a page helper has more than 5 parameters, change only internal helper signatures and their local call sites. Do not change backend API shapes.

- [ ] **Step C4: Validate**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --file src/pages/files/FilesPage.jsx
node scripts/frontend-code-size-guard.mjs --strict --scope production --file src/pages/importSurfaceGuard.test-helper.js
node scripts/frontend-code-size-guard.mjs --strict --scope production --file src/pages/memory/MemoryPage.jsx
node scripts/frontend-code-size-guard.mjs --strict --scope production --file src/pages/observability/ObservabilityPage.jsx
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/pages/skills
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/features/prompts
npm test -- src/pages src/features/prompts
```

Expected: strict page guard passes for assigned files; page tests pass.

## Lane D: Shared API And Wails Bridge

**Files:**
- Modify: `frontend-app/src/shared/api/backendApi.js`
- Modify: `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- Modify: `frontend-app/src/shared/api/backendResponseValidators.js`
- Modify: `frontend-app/src/shared/api/wailsBridge.js`
- Test: `frontend-app/src/shared/api/*.test.js`

- [ ] **Step D1: Establish failing strict baseline**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/shared/api
```

Expected: FAIL includes `backendApi.js` file-length/export/any, `backendResponseValidators.js` any, and `wailsBridge.js` file-length/exports/params/empty-func.

- [ ] **Step D2: Split API modules by existing factory names**

Use current factories such as `createDatasourceApi`, `createMCPServerApi`, `createThreadApi`, and `createNativeApi` as module boundaries. Export a composed `createBackendApi` and keep public named exports stable.

- [ ] **Step D3: Remove `any` without weakening validation**

Replace `any` with `unknown` plus explicit shape checks. For JS files, use JSDoc typedefs only where they document a real payload boundary.

- [ ] **Step D4: Remove empty functions**

Use `xref(references)` on each no-op. If it is unreachable, delete it and its export. If it is required as a callback, replace it with a named behavior that records or throws a visible fail-fast error.

- [ ] **Step D5: Validate**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/shared/api
npm test -- src/shared/api
```

Expected: strict shared API guard passes, shared API tests pass.

## Lane E: Settings Components And Services

**Files:**
- Modify: `frontend-app/src/pages/settings/components/ModelProvidersCard.jsx`
- Modify: `frontend-app/src/pages/settings/components/PromptSettingsCard.jsx`
- Modify: `frontend-app/src/pages/settings/components/ProviderSettingsPanels.jsx`
- Modify: `frontend-app/src/pages/settings/components/UILogCard.jsx`
- Modify: `frontend-app/src/pages/settings/services/settingsPageService.js`
- Modify: `frontend-app/src/pages/settings/SettingsPage.jsx`
- Test: `frontend-app/src/SettingsPage.test.jsx`
- Test: `frontend-app/src/pages/settings/SettingsPage.test.jsx`

- [ ] **Step E1: Establish failing strict baseline**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/pages/settings
```

Expected: FAIL includes settings line-length, exports, function-length, nesting, and params.

- [ ] **Step E2: Extract provider card subviews**

Split `ModelProvidersCard.jsx` into focused child components for provider rows, edit controls, and footer actions. Keep form state ownership in the current parent unless tests force a smaller hook.

- [ ] **Step E3: Split long literals**

For line-length violations, split JSX props, arrays, or strings without changing text values. Verify snapshots or role queries still pass.

- [ ] **Step E4: Validate**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/pages/settings
npm test -- SettingsPage
```

Expected: strict settings guard passes, settings tests pass.

## Lane F: Workflow And Shared Small Helpers

**Files:**
- Modify: `frontend-app/src/pages/workflows/components/WorkflowFinalOutputPanel.jsx`
- Modify: `frontend-app/src/pages/workflows/WorkflowPage.jsx`
- Modify: `frontend-app/src/pages/workflows/services/workflowPageService.js`
- Modify: `frontend-app/src/pages/shared/pageShared.js`
- Modify: `frontend-app/src/services/modules/memoryService.js`
- Test: `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`
- Test: `frontend-app/src/services/modules/memoryService.test.js`

- [ ] **Step F1: Establish failing strict baseline**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/pages/workflows
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/pages/shared
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/services/modules
```

Expected: FAIL includes params, exports, nesting, line-length, and empty-func.

- [ ] **Step F2: Apply mechanical fixes**

Use options objects for params, split long lines, and remove or replace the empty function in `memoryService.js` after LSP references confirm intent.

- [ ] **Step F3: Validate**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/pages/workflows
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/pages/shared
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/services/modules
npm test -- WorkflowPage memoryService
```

Expected: strict guard passes for these roots, focused tests pass.

## Lane G: App Shell

**Files:**
- Modify: `frontend-app/src/App.jsx`
- Test: `frontend-app/src/App.test.jsx`

- [ ] **Step G1: Establish failing strict baseline**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --file src/App.jsx
```

Expected: FAIL includes `src/App.jsx` file-length, function-length, nesting, and params.

- [ ] **Step G2: Extract shell modules**

Split `App.jsx` into stable shell units: bootstrap, sidebar/project helpers, update banner, window shell, and query-client wrapper. Preserve `App` as the default composition point.

- [ ] **Step G3: Validate**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --file src/App.jsx
npm test -- App.test.jsx
```

Expected: `App.jsx` no longer appears in strict violations, app tests pass.

## Lane H: I18n And Remaining Directory Count Graduation

**Files:**
- Modify: `frontend-app/src/shared/i18n/appI18n.js`
- Modify only if controller assigns leftovers: files moved out of `frontend-app/src/entities/client/model`
- Modify only if controller assigns leftovers: files moved out of `frontend-app/src/pages/chat/components`
- Test: `frontend-app/src/styles.test.js` only if imports or copy keys touch styles; otherwise focused page tests for moved modules.

- [ ] **Step H1: Establish failing strict baseline**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/shared/i18n
```

Expected: FAIL includes `appI18n.js` file-length and nesting.

- [ ] **Step H2: Split locale data from logic**

Move large static dictionaries into data modules under `src/shared/i18n/` and keep runtime selection/lookup logic in `appI18n.js`.

- [ ] **Step H3: Validate**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production --dir src/shared/i18n
npm test -- i18n
```

Expected: strict i18n guard passes. If there is no i18n-specific test pattern, run `npm test -- App.test.jsx`.

## Lane I: Controller Integration

**Files:**
- Modify after all source lanes pass: `frontend-app/.frontend_code_size_guard_baseline.json`
- Do not modify unless production strict guard is clean: `frontend-app/.frontend_code_size_guard_baseline_test.json`

- [ ] **Step I1: Review worker diffs**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3
git diff -- frontend-app/src frontend-app/scripts frontend-app/.frontend_code_size_guard_baseline.json frontend-app/.frontend_code_size_guard_baseline_test.json
```

Expected: source changes are scoped to assigned lanes; no guard limit weakening; baseline files unchanged before freeze refresh.

- [ ] **Step I2: Run full strict production guard**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --strict --scope production
```

Expected: PASS for production files. If it fails, dispatch a focused follow-up worker for the remaining production file list. Do not require test baseline debt to be fixed in this production-thaw project.

- [ ] **Step I3: Refresh baseline only after strict pass**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
node scripts/frontend-code-size-guard.mjs --freeze
node scripts/frontend-code-size-guard.mjs
```

Expected: freeze output shows fewer production entries; normal guard passes with `frozen=37` or lower if test debt is untouched. Production `files` in `.frontend_code_size_guard_baseline.json` should be empty or contain only newly justified entries; this plan's target is zero production entries.

- [ ] **Step I4: Run frontend verification**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3/frontend-app
npm run lint
npm test
npm run build
```

Expected: all pass.

- [ ] **Step I5: Final review commands**

```bash
cd /Users/mima0000/Desktop/wj/super-agent-v3
git diff --check
git diff --stat
git diff -- frontend-app/.frontend_code_size_guard_baseline.json
```

Expected: no whitespace errors; diff stat matches planned frontend-only scope; production baseline removes the 55 file entries and 2 directory entries.

## Agent Prompts

Use one prompt per worker. Replace only `LANE` fields with the lane letter.

```markdown
You are Worker LANE for /Users/mima0000/Desktop/wj/super-agent-v3.

Goal: eliminate the production frontend code-size guard freeze for your assigned files only.

Read first:
- AGENTS.md instructions already loaded in the parent thread: use LSP evidence, do not downgrade to shell-only source analysis.
- docs/plans/2026-07-08-frontend-code-size-guard-thaw-plan.md
- frontend-app/scripts/frontend-code-size-guard.mjs

Constraints:
- Do not edit frontend-app/.frontend_code_size_guard_baseline.json or frontend-app/.frontend_code_size_guard_baseline_test.json.
- Do not loosen guard limits.
- Do not edit files outside your lane without asking the controller.
- Do not use fallback code or swallow errors.

Required evidence:
- LSP grep or structure, inspect, xref, file(read_file), and file(diagnostics) for representative changed symbols.
- Pre-edit strict guard failure for your lane.
- Post-edit strict guard pass for your lane.
- Focused npm test evidence.

Return:
- Files changed.
- Violations eliminated by rule and file.
- LSP evidence summary.
- Commands run and pass/fail.
- Any remaining blockers.
```

## Execution Order

1. Start lanes A-H in parallel in isolated worktrees or platform-native subagents.
2. Workers A and B must finish before Lane H handles directory-count leftovers.
3. Run Lane I only after all source lanes report strict guard pass.
4. If two workers need the same file, controller assigns ownership to the lane with the larger freeze count and tells the other lane to stop touching it.

## Completion Criteria

The project is complete only when all are true:

- `node scripts/frontend-code-size-guard.mjs --strict --scope production` passes in `frontend-app`.
- `frontend-app/.frontend_code_size_guard_baseline.json` no longer contains the 55 production file entries or the 2 `__dir__:` entries.
- `node scripts/frontend-code-size-guard.mjs` passes after freeze refresh.
- `npm run lint`, `npm test`, and `npm run build` pass.
- Controller summary lists every removed production baseline entry and cites the worker that removed it.
