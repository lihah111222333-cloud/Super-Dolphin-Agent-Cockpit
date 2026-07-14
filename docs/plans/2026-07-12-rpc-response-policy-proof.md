# RPC Response Policy and Runtime Shape Proof Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Finish the remaining 77 RPC response contracts with fail-fast runtime shape validation or traceable consumer proof, including malformed-response regressions, while preserving the already migrated 28/105 response policies and all earlier RPC audit guards.

**Architecture:** Keep `createBackendCaller()` as the single runtime validation boundary and extend `createBackendResponseValidators()` with the approved 66 method mappings. Use strict, field-aware validators for stable DTOs, exact `null` validation for null-returning commands, ten traceable ignored-result policies, and one production consumer guard for the polymorphic `UI_PREFERENCES_GET` response. Migrate matrix entries only after each domain is green, then require the audit to prove the final 105-policy/validator partition without fallback prose or fabricated locators.

**Tech Stack:** React/Vite, JavaScript with `// @ts-check`, Babel AST audit, Vitest, Go JSON-RPC DTOs, repository multi-language LSP.

**Verification Surface:** Focused Vitest files, `npx vitest run`, `npm run audit:rpc-contracts`, frontend lint/build, focused Go tests for the skill-resolution wire fix, LSP navigation/diagnostics, and scope/diff checks.

---

## Current checkpoint, approved expansion, and invariants

- The current worktree is intentionally partial: 138 registry entries, 29 existing runtime-validator mappings, and 28 structured policies out of the former 105 prose entries. The 28 are exactly 26 locked `unused` entries, `UI_LOG` as `ignored-result`, and `TURN_INTERRUPT` as `result-handled`.
- The remaining 77 P0/P1 entries still contain source-level `responsePassthroughReason` arguments. Because `contract()` no longer preserves that option, the live registry currently exposes neither a validator nor a policy for those 77. Do not describe this checkpoint as green.
- The user approved expanding the earlier four-file scope: add strict response-shape validation, production consumers whose use is statically traceable, and malformed-response regressions. The earlier prohibition on new validators and production consumers is superseded only for the files and symbols listed below.
- The corrected locked treatment for the remaining 77 keys is 66 runtime-validator mappings, ten exact `ignored-result` policies, and one `UI_PREFERENCES_GET` `consumer-validated` policy. `CONFIG_READ` is implemented first. No key may move between these sets.
- Existing runtime-validator precedence remains absolute. A matrix policy cannot replace a runtime validator, and a new validator mapping must have the same matrix `responseValidator` name.
- No fallback is allowed: malformed, missing, extra, contradictory, or unreadable response data throws before a page/store normalizer sees it. Do not coerce `null`, arrays, booleans, numbers, or objects into another shape.
- A locator is recorded only after LSP proves definition, references/call hierarchy, exact consumer, and exact regression symbol. Do not invent a consumer, validator, test name, or path. Missing evidence is a blocker.

## Locked 66/10/1 mapping

The following mapping is normative. Counts alone are insufficient and implementation workers must not reclassify keys.

### 66 response-validator keys

| Exact key(s) | Exact matrix validator name |
|---|---|
| `CONFIG_READ` | `runtimeConfigResponse` |
| `CONFIG_BUILTIN_TOOLS_READ`, `CONFIG_BUILTIN_TOOLS_WRITE` | `builtinToolsResponse` |
| `UI_WINDOW_BOOTSTRAP_GET` | `windowBootstrapResponse` |
| `UI_SIDEBAR_GET` | `sidebarStateResponse` |
| `OBSERVABILITY_FRONTEND_INGEST` | `frontendIngestResponse` |
| `UI_OPEN_NEW_WINDOW` | `openWindowResponse` |
| `UI_CODE_SAVE` | `codeSaveResponse` |
| `UI_PROJECTS_GET`, `UI_PROJECTS_SET_ACTIVE`, `UI_PROJECTS_ADD`, `UI_PROJECTS_REMOVE` | `projectsStateResponse` |
| `UI_PREFERENCES_SET`, `UI_VIDEO_SET_API_KEY`, `PROMPTS_DELETE` | `okResponse` |
| `MODEL_PROVIDERS_SAVE` | `modelProviderRegistryResponse` |
| `UI_DASHBOARD_GET` | `dashboardPageResponse` |
| `UI_VIDEO_GET_API_KEY` | `videoApiKeyStatusResponse` |
| `DASHBOARD_LOGS` | `dashboardLogsResponse` |
| `UI_MEMORY_ENTRY_GET`, `UI_MEMORY_ENTRY_UPSERT`, `UI_MEMORY_ENTRY_MERGE` | `memoryEntryDetailResponse` |
| `UI_MEMORY_ENTRY_DELETE` | `memoryEntryDeleteResponse` |
| `UI_MEMORY_AUTO_DREAM_SET_INTENT` | `memoryAutoDreamIntentResponse` |
| `UI_MEMORY_SIMILARITY_IGNORE` | `memorySimilarityIgnoreResponse` |
| `UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START`, `UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS` | `memoryConsolidationJobResponse` |
| `UI_SHARED_FILE_DELETE` | `sharedFileDeleteResponse` |
| `DASHBOARD_WORKFLOW_MATERIAL_WRITE` | `workflowMaterialWriteResponse` |
| `PROMPT_ASSETS_LIST` | `promptAssetsResponse` |
| `DASHBOARD_PROMPTS` | `dashboardPromptsResponse` |
| `PROMPTS_GET`, `PROMPTS_WRITE` | `promptDetailResponse` |
| `PROMPT_INTENTS_DRAFT` | `promptIntentDraftResponse` |
| `PROMPT_INTENTS_COMMIT` | `promptIntentCommitResponse` |
| `PROMPT_INTENTS_DISCARD` | `promptIntentDiscardResponse` |
| `PROMPT_INTENTS_DRY_RUN` | `promptIntentDryRunResponse` |
| `PERSONALIZATION_PROFILE_GET`, `PERSONALIZATION_PROFILE_SAVE` | `personalizationProfileResponse` |
| `DASHBOARD_DAG_DETAIL` | `dashboardDagDetailResponse` |
| `DASHBOARD_DAG_RUNS` | `dashboardDagRunsResponse` |
| `DASHBOARD_DAG_RUN` | `dashboardDagRunResponse` |
| `WORKFLOW_TEMPLATES_LIST` | `workflowTemplatesListResponse` |
| `WORKFLOW_TEMPLATES_GET` | `workflowTemplateResponse` |
| `WORKFLOW_TEMPLATES_RENDER_DAG` | `workflowTemplateDraftResponse` |
| `WORKFLOW_TEMPLATES_SAVE` | `workflowTemplateSaveResponse` |
| `SKILLS_LOCAL_LIST_FILES` | `skillFilesResponse` |
| `SKILLS_LOCAL_IMPORT_DIR` | `skillImportResponse` |
| `SKILLS_SUMMARY_SUGGEST` | `skillSummarySuggestionResponse` |
| `SKILLS_RESOLUTION_LIST` | `skillResolutionListResponse` |
| `SKILLS_RESOLUTION_PREVIEW` | `skillResolutionPreviewResponse` |
| `SKILLS_RESOLUTION_APPLY` | `skillResolutionApplyResponse` |
| `SKILL_TOOLS_LIST` | `skillToolsListResponse` |
| `DATASOURCE_V2_LIST` | `datasourceDocumentsResponse` |
| `DATASOURCE_V2_GET` | `datasourceDetailResponse` |
| `DATASOURCE_V2_LIST_CHUNKS` | `datasourceChunksResponse` |
| `DATASOURCE_V2_UPDATE` | `datasourceDocumentResponse` |
| `THREAD_ARCHIVE`, `THREAD_UNARCHIVE`, `THREAD_DELETE`, `THREAD_NAME_SET`, `APPROVAL_RESPOND` | `nullResponse` |
| `THREAD_CONFIG_GET`, `THREAD_CONFIG_SET` | `threadConfigResponse` |
| `THREAD_COMPACT_START` | `threadCompactResponse` |
| `THREAD_RECOVER` | `threadRecoverResponse` |

### Ten ignored-result keys and one consumer-validated key

The exact new ignored-result set is `DASHBOARD_DAG_DISPATCH_NODE`, `DASHBOARD_DAG_TERMINATE`, `DASHBOARD_DAG_DELETE`, `DASHBOARD_DAG_APPLY_OPS`, `WORKFLOW_TEMPLATES_ROLLBACK`, `SKILLS_LOCAL_DELETE`, `SKILLS_LOCAL_WRITE`, `SKILLS_CREATE`, `DATASOURCE_V2_IMPORT_LOCAL_FILE`, and `DATASOURCE_V2_DELETE`.

`UI_PREFERENCES_GET` alone is `consumer-validated`.

## File ownership and sequencing

The following shared files are single-writer and must be edited serially in the domain order A -> B -> C -> D -> E -> F -> G:

- `frontend-app/src/shared/api/backendResponseValidators.js`
- `frontend-app/src/shared/api/backendApi.test.js`
- `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- `frontend-app/src/shared/api/backendApi.contractMatrix.test.js`
- `frontend-app/scripts/rpc-contract-audit.mjs`
- `frontend-app/scripts/rpc-contract-audit.test.mjs`

Domain-specific page/service tests may run in parallel only when they do not edit a shared file. After every domain: first run a specification review against the key list and Go wire shape; only after that passes run a code-quality review for fail-fast behavior, duplication, diagnostics, and test quality. Do not start the next domain while either review has an unresolved finding.

Allowed production changes are limited to the shared validator/audit/matrix files, the exact consumers named in Tasks A-F, and `internal/module/skill/mirror_reconciler.go` for the approved JSON-tag repair. Allowed tests are the colocated files named below. Forbidden: unrelated RPC payload/level/method changes, backend behavior changes other than the resolution-report JSON tags, generated artifacts, `cmd/agent-terminal/web-dist/**`, dependencies, lockfiles, baseline files, silent defaults, best-effort parsing, staging, commits, pushes, merges, or worktree cleanup.

### Task A: Config, bootstrap, projects, settings, and the three LSP-gated DTOs

**Keys:** `CONFIG_READ`, `CONFIG_BUILTIN_TOOLS_READ`, `CONFIG_BUILTIN_TOOLS_WRITE`, `UI_WINDOW_BOOTSTRAP_GET`, `UI_SIDEBAR_GET`, `OBSERVABILITY_FRONTEND_INGEST`, `UI_OPEN_NEW_WINDOW`, `UI_CODE_SAVE`, `UI_PROJECTS_GET`, `UI_PROJECTS_SET_ACTIVE`, `UI_PROJECTS_ADD`, `UI_PROJECTS_REMOVE`, `UI_PREFERENCES_SET`, `MODEL_PROVIDERS_SAVE`, `UI_DASHBOARD_GET`, `UI_VIDEO_GET_API_KEY`, `UI_VIDEO_SET_API_KEY`, `DASHBOARD_LOGS`.

**Files:**
- Modify: `frontend-app/src/shared/api/backendResponseValidators.js`
- Modify: `frontend-app/src/shared/api/backendApi.test.js`
- Test as applicable: `frontend-app/src/entities/client/model/runtimeSlice.test.js`, `frontend-app/src/pages/settings/SettingsPage.test.jsx`, `frontend-app/src/App.test.jsx`
- LSP-read before implementation: `internal/module/uistate/config_rpc.go`, `internal/ui/wails/rpc.go`, `internal/ui/wails/code_preview.go`, `internal/module/uistate/state.go`, `internal/module/uistate/service.go`

- [ ] **Step A1 (2-5 min): Prove `runtimeConfigResponse` before editing**

  Use LSP `structure(workspace_symbol, query="runtimeConfigResult")`, `inspect(definition)`, `xref(references)` and `file(read_file)` on `internal/module/uistate/config_rpc.go`. Lock all ten top-level fields: `model`, `modelProvider`, `cwd`, `approvalPolicy`, `sandbox`, `config`, `baseInstructions`, `developerInstructions`, `personality`, and `toolRouting`; lock all seven `toolRouting` fields: `mode`, `routerModel`, `routerProvider`, `routerBaseURL`, `routerHasAPIKey`, `confidenceThreshold`, and `timeoutSec`. Strings stay strings, `routerHasAPIKey` stays boolean, `confidenceThreshold` stays a finite number, and `timeoutSec` must be an integer; the intentionally polymorphic `any` fields must be present but are not coerced.

- [ ] **Step A2 (2-5 min): Write `CONFIG_READ` RED first**

  Extend `fails fast on malformed guarded backend responses before consumers normalize them` in `backendApi.test.js` with missing top-level, non-object `toolRouting`, wrong boolean, and wrong numeric fixtures for `api.readConfig()`. Run:

  `cd frontend-app && npx vitest run src/shared/api/backendApi.test.js -t 'fails fast on malformed guarded backend responses before consumers normalize them'`

  Expected RED: `readConfig()` resolves malformed fixtures because `CONFIG_READ` has no mapping.

- [ ] **Step A3 (2-5 min): Implement and map `runtimeConfigResponse`**

  Add `validateRuntimeConfigResponse(method, response)` and map `methods.CONFIG_READ` in `createBackendResponseValidators()`. Return the original validated object; throw a field-specific `TypeError` on the first mismatch. Re-run the Step A2 command; expected GREEN.

- [ ] **Step A4 (2-5 min): Complete the mandatory three-DTO LSP gate**

  Before writing their validators, obtain the same `structure -> inspect -> xref -> file` evidence for:

  - `windowBootstrapResponse`: `internal/ui/wails/rpc.go:handleUIWindowBootstrapGet`, exact object `{ snapshot: <plain object> }`.
  - `sidebarStateResponse`: `internal/module/uistate/state.go:Sidebar` and `internal/module/uistate/service.go:GetSidebar`; require `threads`, `agents`, `workspace`, and `token_usage` with their DTO container types, and type-check optional ID/status/group maps without fabricating default arrays.
  - `codeSaveResponse`: `internal/ui/wails/code_preview.go:codeSaveResult`; require exactly typed `ok`, `filePath`, `relative`, and finite integer `totalLines` fields.

  Then run one LSP `file(diagnostics)` request with `file_paths` containing all five related Go fact sources: `internal/module/uistate/config_rpc.go`, `internal/ui/wails/rpc.go`, `internal/module/uistate/state.go`, `internal/module/uistate/service.go`, and `internal/ui/wails/code_preview.go`. Expected: no Error, Warning, Information, or Hint in any file. Any severity, diagnostics timeout/error/unavailability, differing definition, or differing JSON tag blocks implementation; shell-only evidence and diagnostics on modified frontend files are not substitutes.

- [ ] **Step A5 (2-5 min): Add three-DTO RED cases**

  Add malformed fixtures to `backendApi.test.js` for `getWindowBootstrap`, `getSidebarState`, and `saveCodeFile`: absent `snapshot`, non-array `threads`, missing `workspace.runs`, wrong `token_usage.totalTokens`, false/non-boolean `ok`, blank/non-string `filePath`, and non-integer `totalLines`. Run the focused test from A2; expected RED for all three unmapped methods.

- [ ] **Step A6 (2-5 min): Add the three validators and mappings**

  Implement `validateWindowBootstrapResponse`, `validateSidebarStateResponse`, and `validateCodeSaveResponse` in `backendResponseValidators.js`; map the three methods and rerun A2. Expected GREEN with no normalization fallback.

- [ ] **Step A7 (2-5 min): Write the remaining settings-domain RED table**

  Add table-driven malformed cases for builtin-tool read/write, project state, open-window result, dashboard-page/log results, video-key get/set, preference/model-provider save acknowledgements, and the observability ingest result. Run:

  `cd frontend-app && npx vitest run src/shared/api/backendApi.test.js src/entities/client/model/runtimeSlice.test.js src/pages/settings/SettingsPage.test.jsx src/App.test.jsx`

  Expected RED: every method selected for runtime validation still accepts at least one malformed response.

- [ ] **Step A8 (2-5 min): Implement the remaining settings-domain validators**

  Add the minimal validators/mappings proven from the Go handlers for the exact Task A rows in the locked 66-key table. Do not add, remove, or reclassify a key.

- [ ] **Step A9 (2-5 min): Run the remaining settings-domain GREEN table**

  Run the exact A7 command. Expected GREEN: malformed transport responses reject before runtime/settings normalization and valid backend-shaped fixtures remain accepted.

- [ ] **Step A10 (2-5 min): Review A before B**

  Specification review: every Task A key has its exact validator mapping from the locked table; `CONFIG_READ` and the three gated DTOs match Go tags exactly. Quality review: no permissive `|| {}`, `?? []`, truthiness-only object acceptance, or swallowed validation error. Run LSP diagnostics for every modified Task A file; all severities must be empty.

### Task B: Thread lifecycle and approval fail-open closure

**Keys:** `THREAD_ARCHIVE`, `THREAD_UNARCHIVE`, `THREAD_DELETE`, `THREAD_CONFIG_GET`, `THREAD_CONFIG_SET`, `THREAD_COMPACT_START`, `THREAD_RECOVER`, `THREAD_NAME_SET`, `APPROVAL_RESPOND`.

**Files:**
- Modify: `frontend-app/src/shared/api/backendResponseValidators.js`
- Modify: `frontend-app/src/shared/api/backendApi.test.js`
- Modify: `frontend-app/src/entities/client/model/helpers/a1/clientStoreThreadActions.js`
- Modify: `frontend-app/src/entities/client/model/useClientStore.test.js`
- LSP-read: `internal/module/thread/rpc.go`, `internal/module/thread/command.go`, `internal/module/turn/rpc_helpers.go`, `internal/dto/provider/thread.go`

- [ ] **Step B1 (2-5 min): Lock the five exact-null methods RED**

  Add cases for archive, unarchive, delete, name-set, and approval/respond that return `{}`, `{ ok: true }`, `false`, or `undefined`. Expected contract is exact JSON `null`, matching `newThreadEffect`, the name-set handler, and `approvalRespondHandler`; no object acknowledgement is accepted. Run:

  `cd frontend-app && npx vitest run src/shared/api/backendApi.test.js -t 'rejects malformed null command responses'`

  Expected RED because the five responses currently pass through.

- [ ] **Step B2 (2-5 min): Implement `nullResponse` once and map five methods**

  Add one reusable `validateNullResponse` function that returns only `null` and otherwise throws `${method} response must be null`; expose/map its exact matrix name as `nullResponse` for the five methods. Re-run B1; expected GREEN.

- [ ] **Step B3 (2-5 min): Add thread config/compact/recover RED**

  Add malformed fixtures for `THREAD_CONFIG_GET` and `THREAD_CONFIG_SET` missing `threadId`, `override`, or `effective`; `THREAD_COMPACT_START` missing or mistyping any required `dto.ThreadCompactResult` field (`threadId`, `command`, integer `beforeTokens`, integer `afterTokens`, boolean `compacted`) plus a separate case where optional `estimated` is present but not boolean; and `THREAD_RECOVER` missing `thread.id`, boolean `recovered`, or string `mode`. Include one valid compact fixture with `estimated` omitted because its Go tag is `omitempty`. Run:

  `cd frontend-app && npx vitest run src/shared/api/backendApi.test.js -t 'rejects malformed thread lifecycle responses'`

  Expected RED for all unmapped methods.

- [ ] **Step B4 (2-5 min): Implement thread validators**

  Add `validateThreadConfigResponse` for both get/set, `validateThreadCompactResponse` for the complete `dto.ThreadCompactResult` camelCase wire, and `validateThreadRecoverResponse`; map the four methods and rerun B3. Preserve camel/snake aliases only where the Go wire already emits both; do not accept an arbitrary object because a consumer later normalizes it.

- [ ] **Step B5 (2-5 min): Close approval `null` fail-open in the consumer**

  In `respondApprovalAction`, remove the misleading `result?.ok === false` branch. Await `respondApprovalRPC()` and report success only after the boundary has returned exact `null`; malformed `{ ok: false }`, `{ ok: true }`, and `undefined` now reject into `warnApprovalFailed`. Add regressions to `useClientStore.test.js` asserting malformed responses return `false`, emit no success notice, add `timeline.approval.respond.failed`, and clear the in-flight marker. Run:

  `cd frontend-app && npx vitest run src/entities/client/model/useClientStore.test.js -t 'approval'`

  Expected GREEN; `null` is the only success response.

- [ ] **Step B6 (2-5 min): Review B before C**

  Specification review the five-null set and four structured validators against Go. Quality review the approval action for success-after-validation only and `finally` cleanup. Run focused tests plus LSP diagnostics on the four modified frontend files; all diagnostics severities must be empty.

### Task C: DAG detail/actions and workflow templates

**Keys:** `DASHBOARD_DAG_DETAIL`, `DASHBOARD_DAG_RUNS`, `DASHBOARD_DAG_RUN`, `DASHBOARD_DAG_DISPATCH_NODE`, `DASHBOARD_DAG_TERMINATE`, `DASHBOARD_DAG_DELETE`, `DASHBOARD_DAG_APPLY_OPS`, `WORKFLOW_TEMPLATES_LIST`, `WORKFLOW_TEMPLATES_GET`, `WORKFLOW_TEMPLATES_RENDER_DAG`, `WORKFLOW_TEMPLATES_SAVE`, `WORKFLOW_TEMPLATES_ROLLBACK`.

**Files:**
- Modify: `frontend-app/src/shared/api/backendResponseValidators.js`
- Modify: `frontend-app/src/shared/api/backendApi.test.js`
- Modify: `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`
- Read consumers: `frontend-app/src/pages/workflows/hooks/useWorkflowPageController.js`, `frontend-app/src/pages/workflows/hooks/useWorkflowActions.js`, `frontend-app/src/pages/workflows/components/WorkflowEnterpriseTemplateForm.jsx`, `frontend-app/src/pages/workflows/components/WorkflowEnterpriseTemplates.jsx`

- [ ] **Step C1 (2-5 min): LSP-confirm every locked C treatment**

  For each facade, use exact definitions and incoming references without reclassification. `DASHBOARD_DAG_DETAIL`, `DASHBOARD_DAG_RUNS`, `DASHBOARD_DAG_RUN`, `WORKFLOW_TEMPLATES_LIST`, `WORKFLOW_TEMPLATES_GET`, `WORKFLOW_TEMPLATES_RENDER_DAG`, and `WORKFLOW_TEMPLATES_SAVE` must retain the exact validators in the locked table. The five exact C ignored keys must get exact consumer/regression symbols in Task G. Record no locator from an import or mock-only call.

- [ ] **Step C2 (2-5 min): Add malformed DAG/template RED tables**

  In `backendApi.test.js`, use valid sibling fields plus one malformed field per case: non-object detail/run, non-array runs/templates, missing pagination identity, render result without a DAG draft, and save result without its persisted version/identity. Run:

  `cd frontend-app && npx vitest run src/shared/api/backendApi.test.js -t 'rejects malformed DAG and workflow template responses'`

  Expected RED for every new mapped candidate.

- [ ] **Step C3 (2-5 min): Implement minimal DAG/template validators**

  Add focused validators, reuse exact shared primitives only for identical wire shapes, map the approved methods, and rerun C2. Do not merge distinct detail, run-page, render, and save DTOs into a generic “object” validator.

- [ ] **Step C4 (2-5 min): Add page-level malformed regressions**

  Add cases proving malformed detail/runs/run and template render/save responses surface the existing workflow error state and do not publish partial DAG/template state. Run:

  `cd frontend-app && npx vitest run src/pages/workflows/WorkflowPage.test.jsx`

  Expected GREEN.

- [ ] **Step C5 (2-5 min): Review C before D**

  Confirm every C key has a validator or an exact awaited-but-ignored locator/test tuple, then run LSP diagnostics for changed files. Reject any locator that points only to `backendApi.test.js`.

### Task D: Skills, skill tools, datasource, and resolution-report wire repair

**Keys:** `SKILLS_LOCAL_DELETE`, `SKILLS_LOCAL_LIST_FILES`, `SKILLS_LOCAL_WRITE`, `SKILLS_LOCAL_IMPORT_DIR`, `SKILLS_CREATE`, `SKILLS_SUMMARY_SUGGEST`, `SKILLS_RESOLUTION_LIST`, `SKILLS_RESOLUTION_PREVIEW`, `SKILLS_RESOLUTION_APPLY`, `SKILL_TOOLS_LIST`, `DATASOURCE_V2_IMPORT_LOCAL_FILE`, `DATASOURCE_V2_LIST`, `DATASOURCE_V2_GET`, `DATASOURCE_V2_LIST_CHUNKS`, `DATASOURCE_V2_UPDATE`, `DATASOURCE_V2_DELETE`.

**Files:**
- Modify: `frontend-app/src/shared/api/backendResponseValidators.js`
- Modify: `frontend-app/src/shared/api/backendApi.test.js`
- Modify: `internal/module/skill/mirror_reconciler.go`
- Modify: `internal/module/skill/rpc_resolution_apply_test.go`
- Modify as regression evidence: `frontend-app/src/pages/skills/SkillsPage.test.jsx`
- Read consumer: `frontend-app/src/pages/skills/SkillsPage.jsx`

- [ ] **Step D1 (2-5 min): Write the Go camelCase resolution-report RED**

  Add a focused Go RPC test for `SKILLS_RESOLUTION_APPLY` that marshals the real `SkillMirrorResolutionReport` owned by `internal/module/skill/mirror_reconciler.go`. Assert the exact keys consumed by `applyResolutionReportFeedback` are `action`, `name`, `resultingHash`, `partialFailure`, and `followUpAction`, and assert the accidental exported Go names are absent. Run:

  `./scripts/test_with_guard.sh ./internal/module/skill -run 'Test.*Resolution.*Apply.*JSON' -count=1`

  Expected RED because `SkillMirrorResolutionReport` currently lacks the required camelCase JSON tags.

- [ ] **Step D2 (2-5 min): Fix only the report JSON tags**

  Add exact `json:"action"`, `json:"name"`, `json:"resultingHash"`, `json:"partialFailure"`, and `json:"followUpAction"` tags to `SkillMirrorResolutionReport` in `internal/module/skill/mirror_reconciler.go`; do not rename Go fields or change business behavior. Re-run D1; expected GREEN.

- [ ] **Step D3 (2-5 min): Add malformed-response RED for the eleven D validators**

  Cover exactly `SKILLS_LOCAL_LIST_FILES`, `SKILLS_LOCAL_IMPORT_DIR`, `SKILLS_SUMMARY_SUGGEST`, `SKILLS_RESOLUTION_LIST`, `SKILLS_RESOLUTION_PREVIEW`, `SKILLS_RESOLUTION_APPLY`, `SKILL_TOOLS_LIST`, `DATASOURCE_V2_LIST`, `DATASOURCE_V2_GET`, `DATASOURCE_V2_LIST_CHUNKS`, and `DATASOURCE_V2_UPDATE`. Each malformed fixture must reach the public facade and fail at `createBackendCaller`. Do not include the five locked ignored-result keys in this rejection table. Run:

  `cd frontend-app && npx vitest run src/shared/api/backendApi.test.js -t 'rejects malformed skill and datasource responses'`

  Expected RED before mappings.

- [ ] **Step D4 (2-5 min): Implement and map the eleven D validators**

  Add the eleven DTO-specific validators from the locked table. Preserve datasource cursor/document identity and require the repaired resolution report fields `action`, `name`, `resultingHash`, `partialFailure`, and `followUpAction`; do not accept both accidental PascalCase and approved camelCase. Re-run D3; expected GREEN.

- [ ] **Step D5 (2-5 min): Add behavior regressions for the five ignored-result keys**

  In `SkillsPage.test.jsx`, inject distinct unexpected response bodies for `SKILLS_LOCAL_DELETE`, `SKILLS_LOCAL_WRITE`, `SKILLS_CREATE`, `DATASOURCE_V2_IMPORT_LOCAL_FILE`, and `DATASOURCE_V2_DELETE`. Assert the real consumer completes the same success/refresh/close-state behavior as it does for `null`, never reads a response field, and does not reject in `createBackendCaller`. Run:

  `cd frontend-app && npx vitest run src/pages/skills/SkillsPage.test.jsx -t 'ignores RPC response body|ignored result'`

  Expected GREEN: arbitrary successful response bodies cannot alter consumer behavior; transport rejection still follows the existing error path.

- [ ] **Step D6 (2-5 min): Add SkillsPage malformed validator regressions**

  Test malformed resolution report, datasource page, skill-tool list, and summary suggestion responses. Assert the page reports failure and does not apply partial state or success feedback. Run:

  `cd frontend-app && npx vitest run src/pages/skills/SkillsPage.test.jsx`

  Expected GREEN.

- [ ] **Step D7 (2-5 min): Review D before E**

  Specification review includes the Go JSON wire and all 16 keys: eleven exact validators and five exact ignored-result consumers, with no overlap. Quality review checks exact casing, no duplicate frontend normalization, and no ignored-result body inspection. Run focused Go/frontend tests and LSP diagnostics on all four shared/Go files plus changed page tests.

### Task E: Memory, prompt, dashboard, and stable UI DTOs

**Keys:** `UI_MEMORY_ENTRY_GET`, `UI_MEMORY_ENTRY_UPSERT`, `UI_MEMORY_ENTRY_DELETE`, `UI_MEMORY_AUTO_DREAM_SET_INTENT`, `UI_MEMORY_ENTRY_MERGE`, `UI_MEMORY_SIMILARITY_IGNORE`, `UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START`, `UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS`, `UI_SHARED_FILE_DELETE`, `DASHBOARD_WORKFLOW_MATERIAL_WRITE`, `PROMPT_ASSETS_LIST`, `DASHBOARD_PROMPTS`, `PROMPTS_GET`, `PROMPTS_WRITE`, `PROMPTS_DELETE`, `PROMPT_INTENTS_DRAFT`, `PROMPT_INTENTS_COMMIT`, `PROMPT_INTENTS_DISCARD`, `PROMPT_INTENTS_DRY_RUN`, `PERSONALIZATION_PROFILE_GET`, `PERSONALIZATION_PROFILE_SAVE`.

**Files:**
- Modify: `frontend-app/src/shared/api/backendSchemas.js`
- Modify: `frontend-app/src/shared/api/backendResponseValidators.js`
- Modify: `frontend-app/src/shared/api/backendApi.test.js`
- Modify tests as applicable: `frontend-app/src/pages/memory/services/memoryPageService.test.js`, `frontend-app/src/pages/prompts/services/promptPageService.test.js`, `frontend-app/src/features/prompts/PromptPageView.test.jsx`, `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`
- Read consumers: `frontend-app/src/pages/memory/services/memoryPageService.js`, `frontend-app/src/pages/prompts/services/promptPageService.js`, `frontend-app/src/features/prompts/PromptPageView.jsx`, `frontend-app/src/pages/workflows/hooks/useWorkflowActions.js`

- [ ] **Step E1 (2-5 min): Lock stable DTO schemas with LSP**

  Trace each service facade through `SERVICE_FACADE_LOCATORS` to its public call and Go result. Extend `backendSchemas.js` only for stable reusable DTOs; mutations whose values are not read remain ignored-result policies. Field access without a rejecting guard is not consumer validation.

- [ ] **Step E2 (2-5 min): Add malformed stable-DTO RED**

  Add public-facade cases for memory entry/status, prompt asset/dashboard/detail/intent/profile, shared-file delete/material-write acknowledgements, and every other E value-read result. Use malformed nested collections and wrong scalar types, not only `null`. Run:

  `cd frontend-app && npx vitest run src/shared/api/backendApi.test.js -t 'rejects malformed memory prompt dashboard and UI responses'`

  Expected RED.

- [ ] **Step E3 (2-5 min): Implement schema parsers and validator mappings**

  Add exact parsers and `validateSchemaResponse` adapters; map the approved E methods. Parser failures must be rethrown with the RPC method and field reason. Re-run E2; expected GREEN.

- [ ] **Step E4 (2-5 min): Add service/page malformed regressions**

  Prove malformed responses reject without publishing partial memory entries, prompt assets/intents/profiles, workflow material state, or success notices. Run:

  `cd frontend-app && npx vitest run src/pages/memory/services/memoryPageService.test.js src/pages/prompts/services/promptPageService.test.js src/features/prompts/PromptPageView.test.jsx src/pages/workflows/WorkflowPage.test.jsx`

  Expected GREEN.

- [ ] **Step E5 (2-5 min): Review E before F**

  Cross-check all 21 E keys, shared-schema exactness, and locator evidence. Run LSP diagnostics; reject permissive object acceptance and any test that exercises only a mock normalizer rather than the public RPC boundary.

### Task F: `UI_PREFERENCES_GET` polymorphic consumer guard

**Key:** `UI_PREFERENCES_GET` is the sole approved member of the 67 non-ignored remaining keys that does not get a global runtime validator because the result type is key-dependent.

**Files:**
- Modify: `frontend-app/src/pages/settings/settingsPageRuntime.js`
- Modify: `frontend-app/src/pages/settings/settingsProviderPreferencesRuntime.js`
- Modify: `frontend-app/src/pages/settings/SettingsPage.test.jsx`
- Modify if exact store consumers require it: `frontend-app/src/entities/client/model/helpers/a1/clientStoreRuntimeCore.js`, `frontend-app/src/entities/client/model/useClientStore.test.js`
- Later matrix locator: `frontend-app/src/shared/api/backendApi.contractMatrix.js`

- [ ] **Step F1 (2-5 min): Enumerate production preference keys by LSP reference**

  Starting at `getPreference`, use `xref(references)` and incoming call hierarchy to list every production call and its literal key. Each call site must have a key-specific guard before the value reaches state: boolean keys accept only booleans, numeric threshold keys accept only finite in-range numbers, enum/provider keys accept only their documented strings, and structured keys require their exact object/array shape. Dynamic or untraceable keys are a blocker.

- [ ] **Step F2 (2-5 min): Write malformed preference RED**

  In `SettingsPage.test.jsx` and the store test if needed, return wrong-type values for representative boolean, number, enum/provider, and structured keys. Assert the load rejects or enters the existing error state and does not apply defaults or partial preferences. Run:

  `cd frontend-app && npx vitest run src/pages/settings/SettingsPage.test.jsx src/entities/client/model/useClientStore.test.js -t 'malformed preference|preference response'`

  Expected RED where consumers currently coerce or default.

- [ ] **Step F3 (2-5 min): Add key-specific guards at the consumers**

  Implement named guard functions in the owning runtime module and call them immediately after each `getPreference` result. Guards throw field/key-specific errors; they do not return fallback values. Re-run F2; expected GREEN.

- [ ] **Step F4 (2-5 min): Prove the consumer-validated policy tuple**

  Use LSP to capture the exact production consumer symbol, exact guard symbol, and exact malformed-response test symbol. Confirm the guard dominates every use. These three locators become the matrix `consumer`, `shape`, and `regressionTest` in Task G; if one symbol cannot cover all calls, refactor to one shared named guard before recording the locator.

- [ ] **Step F5 (2-5 min): Review F before G**

  Confirm this is the only new `consumer-validated` treatment, no global polymorphic validator was added, and all preference paths fail fast. Run focused tests and LSP diagnostics.

### Task G: Migrate all 77 entries and lock the final 105 partition

**Files:**
- Modify serially: `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- Modify serially: `frontend-app/src/shared/api/backendApi.contractMatrix.test.js`
- Modify serially as needed: `frontend-app/scripts/rpc-contract-audit.mjs`
- Modify serially: `frontend-app/scripts/rpc-contract-audit.test.mjs`

- [ ] **Step G1 (2-5 min): Record the Hint 2568 RED**

  Add/retain the audit fixture whose `ExportNamedDeclaration` includes non-`ExportSpecifier` variants, then run LSP `file(diagnostics)` on `frontend-app/scripts/rpc-contract-audit.mjs`. Expected RED: TypeScript Hint 2568 at the `assertRpcMethodsFacadeReExport` access that filters on `specifier.local?.name`; optional chaining does not narrow a union member absent on namespace/default export specifiers.

- [ ] **Step G2 (2-5 min): Patch the export-specifier narrowing**

  In `assertRpcMethodsFacadeReExport`, remove the pre-narrowing `.filter((specifier) => specifier.local?.name === 'RPC_METHODS')`. Iterate all specifiers, execute `if (specifier.type !== 'ExportSpecifier') continue`, and only then read `moduleExportName(specifier.local)` and compare the local/exported names and source.

- [ ] **Step G3 (2-5 min): Run the Hint 2568 GREEN checks**

  Run `cd frontend-app && npx vitest run scripts/rpc-contract-audit.test.mjs -t 'export specifier|RPC_METHODS re-export'`, then LSP `file(diagnostics)` for `frontend-app/scripts/rpc-contract-audit.mjs`. Expected GREEN: focused tests pass and no Error, Warning, Information, or Hint remains, specifically no Hint 2568.

- [ ] **Step G4 (2-5 min): Add final-partition RED assertions**

  Update the matrix test to assert: 138 total entries; exactly 95 P0/P1 validator entries (`29 existing + 66 new`); exactly 39 policies (`28 current + 10 new ignored-result + UI_PREFERENCES_GET`); the exact ten-key new ignored set; the prior 28 policies unchanged in kind; and exactly four P2 entries with no mandatory response governance. Assert all 105 former prose entries are represented by their locked validator or structured policy with no `responsePassthroughReason` in source/serialization. Run:

  `cd frontend-app && npx vitest run src/shared/api/backendApi.contractMatrix.test.js`

  Expected RED while the 77 source arguments still contain prose.

- [ ] **Step G5 (2-5 min per domain group): Migrate validator-backed entries A through E**

  Replace each approved validator-backed prose option with the exact `responseValidator` name already mapped in `createBackendResponseValidators()`. Migrate one domain group at a time and run:

  `cd frontend-app && npx vitest run src/shared/api/backendApi.contractMatrix.test.js src/shared/api/backendApi.test.js`

  Expected intermediate RED only for not-yet-migrated later groups; every migrated group must satisfy the bidirectional runtime/matrix union.

- [ ] **Step G6 (2-5 min per locator): Migrate ignored-result entries**

  For each awaited-but-unused result, record repository-relative `consumer` and `regressionTest` paths plus exact symbols proven in Tasks A-E. The audit must resolve the consumer call to the exact RPC key and prove its result is not assigned, returned, inspected, destructured, or passed onward. Do not use `backendApi.test.js` as production consumer evidence and do not create a locator merely to satisfy the audit.

- [ ] **Step G7 (2-5 min): Migrate `UI_PREFERENCES_GET`**

  Add the exact `consumer-validated` tuple from F4. Run:

  `cd frontend-app && npx vitest run scripts/rpc-contract-audit.test.mjs -t 'consumer-validated|malformed preference'`

  Expected GREEN: the guard dominates the real use and the regression injects malformed response data.

- [ ] **Step G8 (2-5 min): Preserve the original 28-policy checkpoint**

  Assert the exact 26-key `unused` set, `UI_LOG` ignored-result tuple, and `TURN_INTERRUPT` result-handled tuple are byte-for-byte equivalent in meaning to the current checkpoint. The 105 former prose entries are exactly `28 current policies + 66 new validator-backed entries + 10 new ignored-result entries + 1 UI_PREFERENCES_GET consumer-validated entry`. Across the full registry, assert 95 validator entries, 39 policy entries, and four P2 entries without mandatory response governance.

- [ ] **Step G9 (2-5 min): Run matrix and audit GREEN**

  Run:

  `cd frontend-app && npx vitest run src/shared/api/backendApi.contractMatrix.test.js scripts/rpc-contract-audit.test.mjs`

  `cd frontend-app && npm run audit:rpc-contracts`

  Expected: all tests PASS; audit exits 0 with 138 methods/entries and zero findings, including `invalidResponsePolicyEvidence` and validator-union findings.

- [ ] **Step G10 (2-5 min): Review G before H**

  Specification review all 77 keys and both arithmetic checks: `66 + 10 + 1 = 77` for the remaining work and `28 + 66 + 10 + 1 = 105` for former prose entries. Verify the full-registry partition `95 validators + 39 policies + 4 P2 = 138`. Quality review every locator with LSP and inspect the shared-file diff for cross-domain overwrites. Any missing locator or malformed regression blocks completion; never downgrade to prose or `unused`.

### Task H: Full verification and clean handoff

**Files:** verify only; do not stage or commit.

- [ ] **Step H1 (2-5 min): Run focused response suites**

  `cd frontend-app && npx vitest run src/shared/api/backendApi.test.js src/shared/api/backendApi.contractMatrix.test.js scripts/rpc-contract-audit.test.mjs`

  Expected PASS, including malformed shape, strict null, preference guard, locator, false-unused, runtime-validator precedence, facade tracing, and Hint 2568 regressions.

- [ ] **Step H2 (2-5 min): Run affected page/service suites**

  `cd frontend-app && npx vitest run src/entities/client/model/runtimeSlice.test.js src/entities/client/model/useClientStore.test.js src/pages/settings/SettingsPage.test.jsx src/pages/workflows/WorkflowPage.test.jsx src/pages/skills/SkillsPage.test.jsx src/pages/memory/services/memoryPageService.test.js src/pages/prompts/services/promptPageService.test.js src/features/prompts/PromptPageView.test.jsx`

  Expected PASS.

- [ ] **Step H3 (2-5 min): Run the Go wire regression**

  `./scripts/test_with_guard.sh ./internal/module/skill -run 'Test.*Resolution.*Apply.*JSON' -count=1`

  Expected PASS with camelCase report fields.

- [ ] **Step H4 (2-5 min): Run production audit**

  `cd frontend-app && npm run audit:rpc-contracts`

  Expected exit 0, 138 RPC methods, 138 registry entries, and every finding count zero.

- [ ] **Step H5 (2-5 min): Run complete frontend lint and test checks**

  `cd frontend-app && npm run lint`

  `cd frontend-app && npm test`

  Expected PASS with no warnings. `npm test` is mandatory even though focused suites use `npx vitest run`.

- [ ] **Step H6 (2-5 min): Record generated-output state before build**

  Run and preserve both outputs as the user-owned pre-build baseline; do not reset pre-existing changes:

  ```bash
  git status --short -- frontend-app/dist cmd/agent-terminal/web-dist
  (rg --files frontend-app/dist cmd/agent-terminal/web-dist 2>/dev/null || true) | sort | while IFS= read -r file; do shasum -a 256 "$file"; done
  ```

- [ ] **Step H7 (2-5 min): Run build in an isolated snapshot**

  Do not run the syncing build in the current dirty worktree. Run:

  ```bash
  repo_root="$PWD"
  snapshot="$(mktemp -d)"
  trap 'rm -rf "$snapshot"' EXIT
  rsync -a \
    --exclude='.git' \
    --exclude='.build-cache' \
    --exclude='frontend-app/node_modules' \
    --exclude='frontend-app/dist' \
    --exclude='cmd/agent-terminal/web-dist' \
    "$repo_root/" "$snapshot/repo/"
  ln -s "$repo_root/frontend-app/node_modules" "$snapshot/repo/frontend-app/node_modules"
  (cd "$snapshot/repo/frontend-app" && npm run build)
  ```

  Expected PASS, with all `dist`/`web-dist` writes confined to the snapshot.

- [ ] **Step H8 (2-5 min): Verify build isolation**

  Re-run the exact two commands from H6 in the real worktree. Expected byte-for-byte identical output. If an in-worktree build ever becomes unavoidable, stop and move it into an independently approved generated-artifact step; otherwise restore only files proven to have been generated by that build from the H6 snapshot, never user-owned pre-existing changes.

- [ ] **Step H9 (2-5 min): Run mandatory LSP diagnostics**

  Run `file(diagnostics)` for every modified Go/JavaScript/JSX file. Expected no Error, Warning, Information, or Hint, specifically no TypeScript Hint 2568 in `rpc-contract-audit.mjs`. Diagnostics unavailability is a blocker, not PASS.

- [ ] **Step H10 (2-5 min): Verify prose removal and scope**

  Run:

  `rg -n 'responsePassthroughReason|response is consumed unchanged by|turn interrupt returns a command result' frontend-app/src/shared/api/backendApi.contractMatrix.js frontend-app/scripts/rpc-contract-audit.mjs`

  Expected no matches. Run `git diff --check`, inspect `git status --short`, and inspect diffs only to verify no forbidden file, generated artifact, dependency, lockfile, baseline, staging, or history mutation was introduced.

- [ ] **Step H11 (2-5 min): Final two-stage review and placeholder scan**

  Specification review every design acceptance criterion and all A-H key lists. Quality review fail-fast behavior, shared-file consistency, exact locator evidence, malformed-response quality, and test output. Search the plan/spec for unresolved placeholder markers; expected zero. Report fresh commands/results and leave all implementation changes unstaged and uncommitted for user review.
