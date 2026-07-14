# RPC Response Policy Proof Design

## Status and scope

This document specifies the approved expanded design for replacing generated RPC response-policy prose with machine-checkable proof metadata and fail-fast runtime response validation. It is a focused continuation of `docs/superpowers/specs/2026-07-11-p1-audit-remediation-design.md`: all canonical RPC-method truth, facade-to-key checks, service-factory tracing, direct Wails locators, payload protections, backend-handler checks, and runtime-validator union checks from that remediation remain mandatory.

The original four-file audit/matrix change is expanded, with user approval, to the runtime response boundary and the exact production consumers/tests needed to prove malformed-response handling:

- `frontend-app/src/shared/api/backendApi.contractMatrix.js`
- `frontend-app/src/shared/api/backendApi.contractMatrix.test.js`
- `frontend-app/scripts/rpc-contract-audit.mjs`
- `frontend-app/scripts/rpc-contract-audit.test.mjs`
- `frontend-app/src/shared/api/backendResponseValidators.js`
- `frontend-app/src/shared/api/backendSchemas.js`
- `frontend-app/src/shared/api/backendApi.test.js`
- the exact config/settings, thread/approval, DAG/template, skill/datasource, memory/prompt/dashboard/UI consumers and colocated tests listed in `docs/plans/2026-07-12-rpc-response-policy-proof.md`
- `internal/module/skill/mirror_reconciler.go` and `internal/module/skill/rpc_resolution_apply_test.go`, only for the approved skill-resolution report camelCase JSON-tag repair

No RPC method, request payload, risk level, backend business behavior, generated artifact, dependency, or lockfile is changed. The approved expansion may add strict runtime response validators, named production consumer guards, malformed-response regressions, and the one JSON-tag repair above. The existing product diff is user-owned and must be preserved.

## Problem and evidence baseline

The worktree began with 105 prose `responsePassthroughReason` declarations. The current checkpoint is intentionally partial: 28 are already structured (26 locked `unused`, `UI_LOG` as `ignored-result`, and `TURN_INTERRUPT` as `result-handled`), while 77 source entries still pass the obsolete prose option to `contract()`. Because `contract()` no longer stores that option, those 77 live entries currently have neither validator nor policy and the checkpoint is not green. Exact generated prose proves neither consumption nor safety and can make an audit green without a verifiable runtime validator, consumer, handler, malformed-response test, or absence-of-use claim.

The corrected approved treatment of the 77 remaining entries is: 66 new runtime-validator mappings, ten exact `ignored-result` policies, and one `UI_PREFERENCES_GET` `consumer-validated` policy. The latter is intentionally key-polymorphic and must be guarded at its real production consumers rather than by one global response shape. `CONFIG_READ` is implemented first with a complete `runtimeConfigResponse` validator. The prior 28 structured treatments remain locked.

## Approved runtime-validation expansion

`createBackendCaller()` remains the single runtime boundary: it awaits `callAPI`, resolves the validator by exact RPC method, and returns only the validated original value. New validators must be strict enough to reject malformed data before page/store normalization. They must not synthesize empty arrays/objects, coerce scalar types, accept arbitrary objects, swallow errors, or continue with a warning.

The 66 method mappings are implemented in the domain order defined by the implementation plan. Five thread/approval commands use exact matrix validator `nullResponse` because their Go handlers serialize successful `nil` results as JSON `null`. Thread config get/set use `threadConfigResponse`; recover uses `threadRecoverResponse`; and `threadCompactResponse` validates required string `threadId`, required string `command`, required integer `beforeTokens`, required integer `afterTokens`, and required boolean `compacted`. `estimated` carries `json:"estimated,omitempty"`: omission is valid, but when present it must be boolean. `APPROVAL_RESPOND` must no longer treat arbitrary objects or `undefined` as success through `result?.ok`; only the boundary-validated `null` response reaches the success notice.

Three DTOs are blocked on fresh LSP proof before implementation:

- `windowBootstrapResponse` from `internal/ui/wails/rpc.go:handleUIWindowBootstrapGet`;
- `sidebarStateResponse` from `internal/module/uistate/state.go:Sidebar` and `internal/module/uistate/service.go:GetSidebar`;
- `codeSaveResponse` from `internal/ui/wails/code_preview.go:codeSaveResult`.

The gate also requires LSP `file(diagnostics)` with all five fact sources in one `file_paths` request: `internal/module/uistate/config_rpc.go`, `internal/ui/wails/rpc.go`, `internal/module/uistate/state.go`, `internal/module/uistate/service.go`, and `internal/ui/wails/code_preview.go`. Every severity must be empty. Any diagnostic, timeout, tool error, or diagnostics unavailability blocks all three DTO validators; frontend-only diagnostics and shell checks cannot satisfy this gate.

`runtimeConfigResponse` follows `internal/module/uistate/config_rpc.go:runtimeConfigResult`: top-level `model`, `modelProvider`, `cwd`, `approvalPolicy`, `sandbox`, `config`, `baseInstructions`, `developerInstructions`, `personality`, and `toolRouting`; nested routing fields `mode`, `routerModel`, `routerProvider`, `routerBaseURL`, `routerHasAPIKey`, `confidenceThreshold`, and `timeoutSec`. `confidenceThreshold` is a finite number and `timeoutSec` is an integer. Presence and scalar/container types are checked without narrowing the Go `any` fields beyond their wire contract.

The skill-resolution apply report is a real cross-language defect, not an audit exception. Its owner is `internal/module/skill/mirror_reconciler.go:SkillMirrorResolutionReport`, with fields `Action`, `Name`, `ResultingHash`, `PartialFailure`, and `FollowUpAction`. It must carry exact camelCase JSON tags `action`, `name`, `resultingHash`, `partialFailure`, and `followUpAction`, locked by a focused Go RPC serialization RED/GREEN regression. The frontend validator accepts this corrected camelCase wire only.

## Locked response treatment table

This table is normative; keys cannot be moved merely to preserve a total.

| Exact key(s) | Exact treatment |
|---|---|
| `CONFIG_READ` | validator `runtimeConfigResponse` |
| `CONFIG_BUILTIN_TOOLS_READ`, `CONFIG_BUILTIN_TOOLS_WRITE` | validator `builtinToolsResponse` |
| `UI_WINDOW_BOOTSTRAP_GET` | validator `windowBootstrapResponse` |
| `UI_SIDEBAR_GET` | validator `sidebarStateResponse` |
| `OBSERVABILITY_FRONTEND_INGEST` | validator `frontendIngestResponse` |
| `UI_OPEN_NEW_WINDOW` | validator `openWindowResponse` |
| `UI_CODE_SAVE` | validator `codeSaveResponse` |
| `UI_PROJECTS_GET`, `UI_PROJECTS_SET_ACTIVE`, `UI_PROJECTS_ADD`, `UI_PROJECTS_REMOVE` | validator `projectsStateResponse` |
| `UI_PREFERENCES_SET`, `UI_VIDEO_SET_API_KEY`, `PROMPTS_DELETE` | validator `okResponse` |
| `MODEL_PROVIDERS_SAVE` | validator `modelProviderRegistryResponse` |
| `UI_DASHBOARD_GET` | validator `dashboardPageResponse` |
| `UI_VIDEO_GET_API_KEY` | validator `videoApiKeyStatusResponse` |
| `DASHBOARD_LOGS` | validator `dashboardLogsResponse` |
| `UI_MEMORY_ENTRY_GET`, `UI_MEMORY_ENTRY_UPSERT`, `UI_MEMORY_ENTRY_MERGE` | validator `memoryEntryDetailResponse` |
| `UI_MEMORY_ENTRY_DELETE` | validator `memoryEntryDeleteResponse` |
| `UI_MEMORY_AUTO_DREAM_SET_INTENT` | validator `memoryAutoDreamIntentResponse` |
| `UI_MEMORY_SIMILARITY_IGNORE` | validator `memorySimilarityIgnoreResponse` |
| `UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_START`, `UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL_STATUS` | validator `memoryConsolidationJobResponse` |
| `UI_SHARED_FILE_DELETE` | validator `sharedFileDeleteResponse` |
| `DASHBOARD_WORKFLOW_MATERIAL_WRITE` | validator `workflowMaterialWriteResponse` |
| `PROMPT_ASSETS_LIST` | validator `promptAssetsResponse` |
| `DASHBOARD_PROMPTS` | validator `dashboardPromptsResponse` |
| `PROMPTS_GET`, `PROMPTS_WRITE` | validator `promptDetailResponse` |
| `PROMPT_INTENTS_DRAFT` | validator `promptIntentDraftResponse` |
| `PROMPT_INTENTS_COMMIT` | validator `promptIntentCommitResponse` |
| `PROMPT_INTENTS_DISCARD` | validator `promptIntentDiscardResponse` |
| `PROMPT_INTENTS_DRY_RUN` | validator `promptIntentDryRunResponse` |
| `PERSONALIZATION_PROFILE_GET`, `PERSONALIZATION_PROFILE_SAVE` | validator `personalizationProfileResponse` |
| `DASHBOARD_DAG_DETAIL` | validator `dashboardDagDetailResponse` |
| `DASHBOARD_DAG_RUNS` | validator `dashboardDagRunsResponse` |
| `DASHBOARD_DAG_RUN` | validator `dashboardDagRunResponse` |
| `WORKFLOW_TEMPLATES_LIST` | validator `workflowTemplatesListResponse` |
| `WORKFLOW_TEMPLATES_GET` | validator `workflowTemplateResponse` |
| `WORKFLOW_TEMPLATES_RENDER_DAG` | validator `workflowTemplateDraftResponse` |
| `WORKFLOW_TEMPLATES_SAVE` | validator `workflowTemplateSaveResponse` |
| `SKILLS_LOCAL_LIST_FILES` | validator `skillFilesResponse` |
| `SKILLS_LOCAL_IMPORT_DIR` | validator `skillImportResponse` |
| `SKILLS_SUMMARY_SUGGEST` | validator `skillSummarySuggestionResponse` |
| `SKILLS_RESOLUTION_LIST` | validator `skillResolutionListResponse` |
| `SKILLS_RESOLUTION_PREVIEW` | validator `skillResolutionPreviewResponse` |
| `SKILLS_RESOLUTION_APPLY` | validator `skillResolutionApplyResponse` |
| `SKILL_TOOLS_LIST` | validator `skillToolsListResponse` |
| `DATASOURCE_V2_LIST` | validator `datasourceDocumentsResponse` |
| `DATASOURCE_V2_GET` | validator `datasourceDetailResponse` |
| `DATASOURCE_V2_LIST_CHUNKS` | validator `datasourceChunksResponse` |
| `DATASOURCE_V2_UPDATE` | validator `datasourceDocumentResponse` |
| `THREAD_ARCHIVE`, `THREAD_UNARCHIVE`, `THREAD_DELETE`, `THREAD_NAME_SET`, `APPROVAL_RESPOND` | validator `nullResponse` |
| `THREAD_CONFIG_GET`, `THREAD_CONFIG_SET` | validator `threadConfigResponse` |
| `THREAD_COMPACT_START` | validator `threadCompactResponse` |
| `THREAD_RECOVER` | validator `threadRecoverResponse` |
| `DASHBOARD_DAG_DISPATCH_NODE`, `DASHBOARD_DAG_TERMINATE`, `DASHBOARD_DAG_DELETE`, `DASHBOARD_DAG_APPLY_OPS`, `WORKFLOW_TEMPLATES_ROLLBACK`, `SKILLS_LOCAL_DELETE`, `SKILLS_LOCAL_WRITE`, `SKILLS_CREATE`, `DATASOURCE_V2_IMPORT_LOCAL_FILE`, `DATASOURCE_V2_DELETE` | `ignored-result`, with one exact consumer and malformed/behavior regression tuple per key |
| `UI_PREFERENCES_GET` | `consumer-validated` |

The D-domain split is additionally locked by test responsibility. Malformed-response RED/GREEN at `createBackendCaller` covers only its eleven validator keys: `SKILLS_LOCAL_LIST_FILES`, `SKILLS_LOCAL_IMPORT_DIR`, `SKILLS_SUMMARY_SUGGEST`, `SKILLS_RESOLUTION_LIST`, `SKILLS_RESOLUTION_PREVIEW`, `SKILLS_RESOLUTION_APPLY`, `SKILL_TOOLS_LIST`, `DATASOURCE_V2_LIST`, `DATASOURCE_V2_GET`, `DATASOURCE_V2_LIST_CHUNKS`, and `DATASOURCE_V2_UPDATE`. The five D ignored-result keys (`SKILLS_LOCAL_DELETE`, `SKILLS_LOCAL_WRITE`, `SKILLS_CREATE`, `DATASOURCE_V2_IMPORT_LOCAL_FILE`, `DATASOURCE_V2_DELETE`) must instead have production-consumer behavior regressions proving distinct unexpected successful bodies are never inspected and do not alter behavior; transport errors remain failures.

## Structured response-policy model

`contract()` replaces `responsePassthroughReason` with exactly one `responsePolicy`, unless a real `responseValidator` exists. The accepted discriminated union is:

```js
/**
 * @typedef {
 *   | { kind: 'ignored-result', consumer: { path: string, symbol: string }, regressionTest: { path: string, symbol: string } }
 *   | { kind: 'result-handled', consumer: { path: string, symbol: string }, handler: { path: string, symbol: string }, regressionTest: { path: string, symbol: string } }
 *   | { kind: 'consumer-validated', consumer: { path: string, symbol: string }, shape: { path: string, symbol: string }, regressionTest: { path: string, symbol: string } }
 *   | { kind: 'unused', productionScanRoots: readonly ['frontend-app/src'], excludedGlobs: readonly string[] }
 * } ResponsePolicy
 */
```

The meanings are intentionally narrow:

- `ignored-result`: a production consumer invokes the facade but does not read the resolved value. `consumer` locates that call and `regressionTest` locates a test proving the result is irrelevant to the behavior.
- `result-handled`: a production consumer either calls the facade directly or injects the entry's correct facade into a traceable runtime call for the entry's exact action literal. The same lexical binding produced by the runtime's awaited RPC call must be passed as the `result` property of an object argument to the exact module-level handler; that handler may be private. The handler must directly and executably match the action plus `result?.ok === false` (or an equivalent safe failure predicate), read `result.error`, emit the warning/failure, and terminate the failure path. The regression test must await the real runtime consumer with a locally proven `{ ok: false, error: ... }` response and assert the corresponding warning and absence of success.
- `consumer-validated`: a production consumer reads the value and validates or narrows its shape before use. `consumer` locates the use, `shape` locates the actual narrowing/schema/assertion, and `regressionTest` locates a test that rejects or safely handles malformed shape.
- `unused`: the facade exists but no production consumer exists under `frontend-app/src`. The audit proves this by AST reference scanning; metadata cannot whitelist a discovered production reference.

All locator paths are repository-relative, normalized, non-empty, and confined to the repository. Symbols are non-empty exact declaration or test names, not line numbers or prose. Test locators must point to `*.test.js`, `*.test.jsx`, `*.test.mjs`, `*.spec.js`, `*.spec.jsx`, or `*.spec.mjs`. A policy object may contain only the fields for its kind. Missing, extra, blank, malformed, escaping, absent-file, or absent-symbol metadata is a fatal audit error.

## Validator precedence and bidirectional protection

The runtime `responseValidator` mapping remains preferred truth. For every P0/P1 entry:

1. If the runtime mapping has a validator, the matrix must declare the matching `responseValidator`; a `responsePolicy` cannot replace or override it.
2. If the matrix declares `responseValidator`, the runtime mapping must contain the matching implementation.
3. Only keys with no runtime validator may declare a structured `responsePolicy`.
4. Declaring both fields, or neither field, fails fast.

This preserves the existing bidirectional matrix/runtime union: runtime-only keys, matrix-only validator names, name mismatches, and replacement of a real validator by passthrough policy all remain findings.

## Audit behavior

The audit parses policy metadata from the matrix AST; it does not evaluate the module or accept comments/prose as evidence. It must:

- reject every `responsePassthroughReason`, including the generated sentence and the former `TURN_INTERRUPT` exception;
- validate the policy discriminant and exact field set;
- validate every locator path, file kind, and exact symbol/test declaration;
- trace `ignored-result` and `consumer-validated` consumers to the entry's real facade/key using the existing factory, service, and Wails locator machinery;
- prove an ignored result is not read at the located call;
- limit the runtime/private-handler exception to `TURN_INTERRUPT` with the exact consumer locator `frontend-app/src/entities/client/model/threadLifecycleRuntime.js:attachActiveThreadRpcRuntime`, same-file handler `notifyThreadActionFailure`, and exact lifecycle-runtime regression locator. No other key or locator tuple may enter this path;
- independently prove the fixed injection fact in `frontend-app/src/entities/client/model/helpers/a1/clientStoreThreadActions.js:createActiveThreadActions`: its body has exactly one non-directive statement, a direct `return` of one object whose every property is a noncomputed `ObjectProperty`; the exact `interruptActiveThread` key appears once and its value is exactly `() => runtime.activeThreadRPC('thread.interrupt', interruptTurn)`, and the imported `interruptTurn` resolves to the entry's facade. A duplicate key, spread, object method, computed property, preceding throw, infinite loop, conditional, loop-only, nested-callback, or dead injection site invalidates the proof;
- bind runtime flow only inside the exact `attachActiveThreadRpcRuntime` declaration: its lexical `activeThreadRPC = async (action, rpc) => ...` must be the binding exposed on `runtime`; the awaited callee must be that arrow's `rpc` parameter, and that exact result binding must be the `result` property sent to the handler. Handler-call or await decoys inside nested functions/callbacks, or a correct-looking flow in another module-level function, cannot repair a wrong property or binding in the exact consumer;
- prove the handler's whole predicate is exactly the strict action equality and strict `result?.ok === false` equality joined by one `&&` (parentheses and reversed operand order are allowed). Matching subexpressions inside `||`, additional constant terms, or broader predicates are not evidence. The handler must derive its message from the same result through the exact same-file `interruptFailureMessage(result)` structure: one `for...of` over `[result?.error, result?.message, result?.reason, result?.status, result?.mode]`, normalization by `normalizeOptionalTextField`, conditional return of that normalized binding, then the exact required-message throw. Fabricated/dead returns are not evidence. It must emit `notifyAction(..., 'warning', ...)` and `addWarning('warn', ...)`, and return from the failure path;
- prove the exact regression callback has one fixed phase sequence: `const runtime = createRuntime()`, `const deps = createDeps()`, `const rpc = vi.fn().mockResolvedValue({ ok: false, error: <non-empty string> })`, and direct `attachActiveThreadRpcRuntime(runtime, deps)`, followed immediately by exact `await expect(runtime.activeThreadRPC('thread.interrupt', rpc)).resolves.toBe(false)`. No other declaration or expression may appear in that prelude. Every remaining callback statement must be a Vitest matcher call whose callee/member chain is rooted at a direct `expect(...)` call; a helper that merely receives or contains a nested `expect` is not an assertion statement. The tail must contain the exact failure `notifyAction(..., 'warning', ...)`, negated success notification, and `addWarning('warn', 'thread.interrupt.failed', ...)` assertions;
- prove a consumer-validated shape locator performs executable narrowing/validation and its test locator exists;
- scan production references for `unused`, excluding tests, stories, fixtures, contract matrix, audit scripts, declaration/re-export sites, and generated/build directories; any remaining facade reference makes the claim false;
- retain canonical RPC truth, registry-to-method equality, P0 handler, payload, facade-to-key, service factory, direct Wails, and validator-union findings;
- throw immediately for structurally invalid/missing metadata and report evidence mismatches as deterministic sorted findings that make the CLI exit non-zero.

The AST export check must also clear TypeScript Hint 2568: before reading `specifier.local`, narrow `specifier.type === 'ExportSpecifier'`. Optional chaining on a union member is not an acceptable substitute because the member itself is absent on other export-specifier variants.

## Locked treatment of 27 generated entries and the separate prose exception

The evidence scan found no production consumer outside declaration/re-export/service-factory sites for the following 26 keys. Each must use `unused`, and the implementation audit must independently confirm the claim against `frontend-app/src`. These locked references are the evidence baseline: discovery of any production reference is RED and blocks implementation; it must never be reclassified or exempted within this work.

1. `APP_UPDATE_DOWNLOAD`
2. `OBSERVABILITY_STATUS`
3. `UI_PREFERENCES_GET_ALL`
4. `UI_MEMORY_SIMILARITY_CONSOLIDATE_ALL`
5. `PROMPT_SECTIONS_LIST`
6. `PROMPT_SECTIONS_WRITE`
7. `PROMPT_SECTIONS_DELETE`
8. `DASHBOARD_DAGS`
9. `CRONJOB_LIST`
10. `CRONJOB_GET`
11. `CRONJOB_CREATE`
12. `CRONJOB_UPDATE`
13. `CRONJOB_DELETE`
14. `CRONJOB_RUN_ONCE`
15. `CRONJOB_SET_ENABLED`
16. `CRONJOB_LIST_RUNS`
17. `SKILL_TOOLS_CREATE`
18. `SKILL_TOOLS_GET`
19. `SKILL_TOOLS_UPDATE`
20. `SKILL_TOOLS_DELETE`
21. `DATASOURCE_V2_CREATE`
22. `MCP_TOOL_LIFECYCLE_SET`
23. `MCP_TOOL_LIFECYCLE_LIST`
24. `MCP_TOOL_LIFECYCLE_EXPORT`
25. `THREAD_LIST_PAGE`
26. `THREAD_LOADED_LIST_PAGE`

`UI_LOG` is the 27th generated entry. It must use `ignored-result`, with the real production logging call as `consumer` and its existing regression test as `regressionTest`. The implementation worker must use LSP definition/reference navigation and the focused test to record their exact repository-relative paths and exact symbols; if either locator or ignored-result behavior cannot be proven, implementation stops rather than substituting prose, `unused`, or another classification.

`TURN_INTERRUPT` is the separate 105th prose exception. Its metadata tuple is fixed: consumer `frontend-app/src/entities/client/model/threadLifecycleRuntime.js:attachActiveThreadRpcRuntime`, handler in that same file `notifyThreadActionFailure`, and regression `frontend-app/src/entities/client/model/threadLifecycleRuntime.test.js:reports interrupt ok:false as warning without showing success`. The separate injection source is fixed at `frontend-app/src/entities/client/model/helpers/a1/clientStoreThreadActions.js:createActiveThreadActions`. The private handler may derive the message through the same-file `interruptFailureMessage(result)` helper only when that helper has the exact approved field-order, normalization, conditional-return, and required-throw structure; unrelated fields, fabricated returns, and dead derived-message evidence are invalid. The regression must use the exact runtime/deps/failure-rpc/attach prelude, the exact awaited runtime matcher, and a root-`expect`-matcher-only tail containing the exact warning, addWarning, and no-success assertions. A warning-producing variable initializer before the await or an ordinary helper call with nested `expect` after it invalidates the proof. Failure of any proof blocks migration rather than permitting prose, `ignored-result`, `unused`, or another classification.

Of the 77 remaining entries, the locked 66 become validator-backed only after the matching runtime validator and malformed-response regression are green; the locked ten become `ignored-result`; and `UI_PREFERENCES_GET` alone becomes `consumer-validated` with a key-specific guard. A test filename listed in the matrix is not by itself proof: every policy needs an exact test symbol, and every matrix validator name must match an actual runtime mapping.

## TDD and acceptance

Focused tests must first prove RED for:

- generated or arbitrary prose policy;
- missing, escaping, nonexistent-path, nonexistent-symbol, and wrong-file-kind locators;
- a false `unused` claim after adding a production reference fixture;
- each policy kind with missing or invalid evidence;
- replacement of an existing runtime validator by any structured policy;
- an `ExportNamedDeclaration` specifier variant that triggers Hint 2568 before narrowing.

GREEN requires each policy kind to pass only with valid evidence, all 66 new runtime validators to reject malformed responses, the `UI_PREFERENCES_GET` guard to dominate every production use, runtime-validator precedence and bidirectional union checks to remain green, and all former prose to be absent. The former-prose arithmetic is `28 current policies + 66 new validator-backed + 10 new ignored-result + 1 new consumer-validated = 105`. The full registry must contain 95 validator entries (`29 + 66`), 39 policy entries (`28 + 10 + 1`), and four P2 entries without mandatory response governance. Full verification uses focused `npx vitest run` commands, `npm run audit:rpc-contracts`, `npm run lint`, mandatory `npm test`, an isolated-snapshot `npm run build` that leaves current `dist`/`web-dist` unchanged, the focused Go resolution-report test, LSP diagnostics, and `git diff --check`.

## Non-goals and fail-fast rule

This work does not generate TypeScript types, delete unused facades, refactor unrelated factories/services, change RPC methods/requests/levels, alter backend business behavior, or broaden the audit beyond response-policy and approved runtime-shape proof. Adding the approved validators, traceable consumer guards, malformed-response regressions, and skill-resolution JSON tags is explicitly in scope. No fallback to prose, default policy, fabricated locator, ignored AST variant, type coercion, silent normalization, or warning-only continuation is permitted.
