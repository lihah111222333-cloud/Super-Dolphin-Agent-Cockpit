# P1 Audit Remediation Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fail fast and preserve one user intent across the five approved P1 workflow, Codex event, prompt parsing, Codex resume, and RPC-audit defects.

**Architecture:** Keep each correction at its existing boundary: workflow action state owns one start intent and reconciliation key; Codex session notification handling converts malformed terminal input into one local failure; prompt list parsing validates the canonical wire shape before normalization; Codex resume requires effective remote approval configuration; the frontend RPC audit imports the same method registry as production. Do not add generic retries, provider-wide fallback behavior, generated bindings, or unrelated refactors.

**Tech Stack:** React 18, Zustand-facing hooks, Vitest/Testing Library, Zod, Node ESM audit scripts, Go 1.25 provider code, worktree-local multi-language LSP MCP.

**Verification Surface:** `frontend-app` focused Vitest plus lint/test/build; guarded `./internal/provider/codexapp`; frontend RPC contract audit; LSP diagnostics on every changed file; `git diff --check` and final uncommitted diff review. SQL/store, generated files, lockfiles, license, and every P2/P3 are excluded.

---

## Baseline, evidence, and locked scope

- Approved base and current `HEAD`: `e3d108327471b7e8acf21a5feb6fe556fdcaea65`. Re-run `test "$(git rev-parse HEAD)" = e3d108327471b7e8acf21a5feb6fe556fdcaea65` before implementation; expected exit status is 0. If it differs, stop and re-audit instead of applying this plan blindly.
- At planning time `git status --short` contained only `?? docs/superpowers/specs/2026-07-11-p1-audit-remediation-design.md`; no intervening tracked change had fixed any finding. Preserve that user-owned file.
- LSP evidence on this HEAD:
  - Workflow: `withWorkflowActionTimeout(promise): Promise<any>` at `workflowDagModel.js:28`; references/call hierarchy show ten action callers, while `useRunSelectedDagAction` creates `uniqueWorkflowActionKey('ui')` inside each click.
  - Codex terminal events: `translateCodexEvent` at `event_map.go:54` logs and returns on decode failure. `onNotification` reaches `dispatch` and then `finishTurn`; malformed terminal JSON never produces a translated terminal event or explicit handle failure.
  - Prompt items: `normalizePromptItem` at `PromptPageView.jsx:45` is called only by `normalizePromptList`; hover exposes `any` fields and the read shows generated `prompt-${index}`, default `expert`, `project`, priority `0`, and enabled `true`.
  - Resume: `restoreApprovalPolicy` at `support.go:730` has signature with no error return, is called by `finishResumedSession`, and writes local fallback values on call/decode/missing-effective paths.
  - RPC audit: production factory imports `backend/backendRpcMethods.js`, while `rpc-contract-audit.mjs` reads `backendApi.js`; `mismatchedRegistryMethods` is literally initialized to `[]`. `auditRpcContracts` references and call hierarchy are available and show the CLI entry call.
- LSP blocker: `grep(ast_search)` for the Go session methods failed twice after narrowing to `work_dir=.`, `path=internal/provider/codexapp`, `language=go`, with `sg run start: exec: "sg": executable file not found in $PATH`. The implementation worker must retry after repairing the worktree-local AST dependency; until then, this is not an AST-search PASS. LSP `text_search`, `inspect`, `xref`, `file(read_file)`, and `file(diagnostics)` did work.
- Baseline LSP diagnostics are empty for the inspected frontend/audit/support/event files. `internal/provider/codexapp/session_dispatch.go:466:2` has hint `slicescontains: Loop can be simplified using slices.Contains`; because Task 2 modifies that file, the task must clear this hint as part of the same diagnostics-clean result.
- No staging, commit, push, merge, worktree cleanup, lockfile update, generated-file update, or Git-state mutation is authorized. The last checkpoint deliberately leaves the complete product diff uncommitted for user review.

## Intended file ownership

- Modify `frontend-app/src/pages/workflows/hooks/useWorkflowActions.js`: retain one run-start intent/key through uncertain completion and reconciliation.
- Modify `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`: deferred start and same-key regression coverage.
- Modify `internal/provider/codexapp/session_approval.go`: detect malformed terminal notification before ordinary dispatch.
- Modify `internal/provider/codexapp/session_dispatch.go`: structured exactly-once local terminal failure helper and the existing diagnostics hint.
- Modify `internal/provider/codexapp/turn_completed_event_map_test.go`: malformed terminal handle regression coverage.
- Modify `frontend-app/src/features/prompts/PromptPageView.jsx`: strict canonical prompt-item Zod schema and normalization without synthesized required fields.
- Modify `frontend-app/src/features/prompts/PromptPageView.test.jsx`: invalid/canonical prompt response cases.
- Modify `internal/provider/codexapp/support.go`: make effective resume config parsing return contextual errors.
- Modify `internal/provider/codexapp/driver_session_helpers_test.go`: resume config error/malformed/missing/success cases.
- Modify `frontend-app/src/shared/api/backendApi.js`: re-export the production `RPC_METHODS`; remove its duplicate object.
- Modify `frontend-app/src/shared/api/backendApi.contractMatrix.js`: import production `RPC_METHODS` and supply reviewed P0/P1 response policies.
- Modify `frontend-app/scripts/rpc-contract-audit.mjs`: read production truth and compute method drift.
- Modify `frontend-app/scripts/rpc-contract-audit.test.mjs`: shadow drift and missing-policy fixtures.
- Do not modify `frontend-app/src/shared/api/backend/backendRpcMethods.js`; it is already the production source of truth. Do not modify `cmd/agent-terminal/web-dist/**`.

### Task 1: Preserve a single workflow run-start intent

**Files:**
- Modify: `frontend-app/src/pages/workflows/hooks/useWorkflowActions.js:80-100`
- Test: `frontend-app/src/pages/workflows/WorkflowPage.test.jsx`

- [ ] **Step 1: Add the deferred-mutation RED test**

  Add a test that installs a controllable `backend.startDag` promise, clicks the run button, advances fake timers past the former timeout, clicks again, resolves the original promise to `{ runKey: 'run-1' }`, and asserts both `backend.startDag` call count `1` and a single captured `idempotencyKey`. Assert the UI remains disabled with an explicit “正在确认启动结果” notice during uncertainty, then reconciles to the successful run.

- [ ] **Step 2: Run the focused test and record RED**

  Run: `cd frontend-app && npm test -- --run src/pages/workflows/WorkflowPage.test.jsx -t "retains one run-start intent while completion is uncertain"`

  Expected: FAIL because the current timeout clears `actioning`, reports failure, and a second click invokes `startDag` with a newly generated key.

- [ ] **Step 3: Implement the minimal intent state**

  In `useRunSelectedDagAction`, add a `useRef`-backed record with the exact shape `{ dagKey, idempotencyKey, promise }`. Create the key only when no record exists for `targetDagKey`; reuse the stored promise/key while it is unresolved. Do not use `withWorkflowActionTimeout` for `startDag`, because it cannot cancel the backend mutation. Keep `actioning === 'start'` until the original promise settles; on transport uncertainty, refresh `getDagRuns/getDagRun` through the existing refresh boundary before enabling a new intent. Clear the record only after confirmed success or confirmed backend failure, never merely because a client clock elapsed.

- [ ] **Step 4: Keep unrelated workflow timeouts unchanged**

  Confirm references from LSP still show `withWorkflowActionTimeout` on stop/delete/schedule/node/template actions, but no reference from `useRunSelectedDagAction`. This task must not introduce generic retry infrastructure or change `SKILLS_REQUEST_TIMEOUT_MS`.

- [ ] **Step 5: Run focused GREEN and diagnostics**

  Run: `cd frontend-app && npm test -- --run src/pages/workflows/WorkflowPage.test.jsx`

  Expected: PASS, including the existing payload-key assertion and the new one-call/same-key deferred case.

  Then run LSP `file(diagnostics)` for both changed files; expected: no Error, Warning, Information, or Hint.

### Task 2: Fail the active Codex handle on malformed terminal events

**Files:**
- Modify: `internal/provider/codexapp/session_approval.go:412-506`
- Modify: `internal/provider/codexapp/session_dispatch.go:24-95,450-475`
- Test: `internal/provider/codexapp/turn_completed_event_map_test.go`

- [ ] **Step 1: Add the exactly-once terminal RED test**

  Construct a session with an active `turnHandle`, call `s.onNotification("turn/completed", json.RawMessage(`{"turn":`))`, and assert the handle completes with an error containing `codexapp: malformed terminal event turn/completed`. Send the same malformed terminal notification again and assert the handle completion count remains one. Add a companion malformed non-terminal notification assertion that preserves current warn/drop behavior and does not finish the handle.

- [ ] **Step 2: Run the focused Go test and record RED**

  Run: `./scripts/test_with_guard.sh ./internal/provider/codexapp -run 'TestMalformedTerminalEventFailsActiveTurnExactlyOnce|TestMalformedNonTerminalEventDoesNotFinishTurn' -count=1`

  Expected: FAIL because `translateCodexEvent` currently warns and returns, leaving the active handle incomplete.

- [ ] **Step 3: Introduce a structured terminal decode failure**

  Add an unexported error type in `session_dispatch.go` with fields `EventType`, `ThreadID`, and `TurnID`, plus an `Error()` string that contains identities only when available. Add `failMalformedTerminalEvent(method string, params json.RawMessage) bool`: return false for non-terminal methods or valid JSON objects; for a malformed terminal payload, locate the active handle under the existing session lock, finish it with the structured error through the same exactly-once close path used by terminal completion, and return true.

  Do not store/log raw payload, prompts, absolute paths, or configuration values. Do not alter non-terminal translation policy.

- [ ] **Step 4: Invoke failure before dispatch and clear the existing hint**

  At the start of `onNotification`, call `failMalformedTerminalEvent`; if it returns true, log only method and available public identifiers and return before `sniffTurnOutput`/`dispatch`. Replace the diagnostic-marked membership loop at `session_dispatch.go:466` with `slices.Contains`, adding the standard-library import if needed.

- [ ] **Step 5: Run GREEN, package verification, and LSP diagnostics**

  Run: `./scripts/test_with_guard.sh ./internal/provider/codexapp -run 'MalformedTerminal|MalformedNonTerminal|TurnCompleted|FinishTurn' -count=1`

  Then run: `./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1`

  Expected: PASS. Run LSP `file(diagnostics)` on `session_approval.go`, `session_dispatch.go`, and the test; expected: zero diagnostics, including removal of `slicescontains`.

### Task 3: Strictly parse canonical PromptPageView items

**Files:**
- Modify: `frontend-app/src/features/prompts/PromptPageView.jsx:14-53`
- Test: `frontend-app/src/features/prompts/PromptPageView.test.jsx`

- [ ] **Step 1: Add table-driven invalid-item RED cases**

  Mock `listPromptAssets` with each response: `{ prompts: [{}] }`, an item with `enabled: "true"`, an item with unknown `scope`, an item with unknown `assetType`, and a canonical-looking item missing `id`. For every case assert the page renders its existing load-error surface and renders no synthesized `未命名`/expert card. Add one valid canonical item and assert it renders.

- [ ] **Step 2: Run the focused RED test**

  Run: `cd frontend-app && npm test -- --run src/features/prompts/PromptPageView.test.jsx -t "rejects malformed canonical prompt items"`

  Expected: FAIL because the current `unknown().optional().passthrough()` schema plus normalizer accepts all cases and synthesizes defaults.

- [ ] **Step 3: Replace the permissive schema with the canonical wire schema**

  Define strict Zod enums for the backend-supported prompt type, scope, bucket/category, and state values observed in the existing canonical test fixtures/backend contract. Require `id: z.string().trim().min(1)`, `enabled: z.boolean()`, `priority: z.number().finite()`, and the canonical type/scope/bucket fields; use `.strict()` on the item. Keep only backend-documented optional presentation fields as typed optional strings/arrays/objects. The response envelope may retain documented metadata fields, but item unknown keys must fail.

- [ ] **Step 4: Remove synthesized contract values from normalization**

  Make `normalizePromptItem(raw)` consume validated canonical fields directly. Remove `index`, generated `prompt-${index}`, boolean coercion, numeric coercion, default `expert`, default `project`, and default enabled `true`. Presentation-only fallback text may remain only for preview copy after a valid item exists.

- [ ] **Step 5: Run GREEN and diagnostics**

  Run: `cd frontend-app && npm test -- --run src/features/prompts/PromptPageView.test.jsx src/pages/prompts/services/promptPageService.test.js`

  Expected: PASS for invalid rejection, canonical rendering, and the service boundary. Run LSP `file(diagnostics)` for both changed files; expected: no diagnostics.

### Task 4: Make Codex resume effective configuration fail fast

**Files:**
- Modify: `internal/provider/codexapp/support.go:583-610,729-758`
- Test: `internal/provider/codexapp/driver_session_helpers_test.go`

- [ ] **Step 1: Add four resume configuration RED subtests**

  Extend the existing mock transport around `thread/config/get` with subtests `rpc error`, `malformed json`, `missing effective`, and `missing approval policy`; each must assert `finishResumedSession` returns no session and a contextual error naming `thread/config/get` without echoing response values. Add `valid effective config` asserting the remote approval policy is installed and the session is returned.

- [ ] **Step 2: Run focused RED**

  Run: `./scripts/test_with_guard.sh ./internal/provider/codexapp -run 'TestFinishResumedSessionRequiresEffectiveApprovalConfig' -count=1`

  Expected: FAIL because `restoreApprovalPolicy` returns no error and writes local fallback values for every invalid path.

- [ ] **Step 3: Return typed/contextual errors from restoreApprovalPolicy**

  Change the signature to `func (d *driver) restoreApprovalPolicy(ctx context.Context, s *session, threadID string) error`. Return wrapped errors for transport call failure, JSON decode failure, missing `effective`, and missing/blank approval fields. Parse into a small typed response struct rather than `map[string]any`; never include raw JSON or approval values in the error.

- [ ] **Step 4: Stop resume before exposing readiness**

  In `finishResumedSession`, call the new helper before returning `s`; on error return `nil, fmt.Errorf("codexapp: resume thread %q restore effective config: %w", threadID, err)`. Remove all local-policy/default/warning-only branches. Keep valid remote config application through `setApprovalPolicy` and `setRuntimeConfigValue`.

- [ ] **Step 5: Run GREEN, package verification, and diagnostics**

  Run: `./scripts/test_with_guard.sh ./internal/provider/codexapp -run 'FinishResumedSession|ResumeSession|ProviderContract' -count=1`

  Then run: `./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1`

  Expected: PASS. Run LSP `file(diagnostics)` for `support.go` and `driver_session_helpers_test.go`; expected: no diagnostics.

### Task 5: Audit RPC contracts from production truth with mandatory P0/P1 response policy

**Files:**
- Modify: `frontend-app/src/shared/api/backendApi.js:1-160`
- Modify: `frontend-app/src/shared/api/backendApi.contractMatrix.js:1-205`
- Modify: `frontend-app/scripts/rpc-contract-audit.mjs:1-180`
- Test: `frontend-app/scripts/rpc-contract-audit.test.mjs`

- [ ] **Step 1: Add production-source drift RED fixture**

  In the audit test fixture, give `backend/backendRpcMethods.js` one key/value and `backendApi.js` a conflicting duplicate. Assert `auditRpcContracts` reads the production module and reports the registry mismatch. Also assert a matrix key absent from production truth appears in `registryWithoutRpcMethods`.

- [ ] **Step 2: Add P0 and P1 missing-response-policy RED fixtures**

  Create one P0 and one P1 registry entry with neither `responseValidator` nor a non-blank `responsePassthroughReason`. Assert both keys appear in `missingResponsePolicies`; add whitespace-only passthrough reason and assert it still fails.

- [ ] **Step 3: Run audit tests and record RED**

  Run: `cd frontend-app && node --test scripts/rpc-contract-audit.test.mjs`

  Expected: FAIL because the audit reads `backendApi.js`, hardcodes `mismatchedRegistryMethods = []`, and does not enforce the complete production P0/P1 policy surface.

- [ ] **Step 4: Establish one RPC_METHODS source**

  In `backendApi.js`, delete the duplicate frozen object and add `export { RPC_METHODS } from './backend/backendRpcMethods.js';` so public imports remain compatible. In `backendApi.contractMatrix.js`, import `RPC_METHODS` from `./backend/backendRpcMethods.js` and set every contract entry's `method` from `RPC_METHODS[key]` rather than repeating a literal.

- [ ] **Step 5: Parse production truth and compute mismatch**

  Change `RPC_METHODS_PATH` to `frontend-app/src/shared/api/backend/backendRpcMethods.js`. Build `mismatchedRegistryMethods` by comparing every registry entry's resolved method with the production map; report `{ key, registryMethod, rpcMethod }` for unequal values. Do not initialize mismatch to an empty constant.

- [ ] **Step 6: Complete reviewed P0/P1 response policy declarations**

  For every P0/P1 entry, supply either the existing production validator key or a specific non-empty passthrough reason describing the consumed response envelope. Update `missingResponsePolicies` to trim reasons and enforce both P0 and P1. Do not weaken this by giving a blanket reason or exempting mutations.

- [ ] **Step 7: Run GREEN audit and focused frontend tests**

  Run: `cd frontend-app && node --test scripts/rpc-contract-audit.test.mjs && npm run rpc-contract-audit`

  Expected: PASS with zero missing production keys, zero registry-without-method keys, zero mismatched methods, zero missing backend handlers required by P0/P1, and zero missing response policies.

  Run: `cd frontend-app && npm test -- --run src/shared/api/backendApi.test.js src/pages/backendApiConsumer.surface.test.js`

  Expected: PASS and public `RPC_METHODS` imports remain compatible.

- [ ] **Step 8: Run LSP diagnostics**

  Run LSP `file(diagnostics)` on all four changed RPC/audit files. Expected: no Error, Warning, Information, or Hint.

### Task 6: Affected-surface verification and uncommitted review checkpoint

**Files:**
- Review only: every file listed in “Intended file ownership”

- [ ] **Step 1: Re-run the five focused RED/GREEN targets together**

  Run the Task 1, 2, 3, 4, and 5 focused commands again without cached assumptions. Expected: all PASS.

- [ ] **Step 2: Run affected Go verification**

  Run: `./scripts/test_with_guard.sh ./internal/provider/codexapp -count=1`

  Expected: PASS.

- [ ] **Step 3: Run the complete frontend verification**

  Run: `cd frontend-app && npm run lint && npm test && npm run build && npm run rpc-contract-audit`

  Expected: all commands exit 0. Do not sync `dist` into `cmd/agent-terminal/web-dist` and do not modify any generated artifact.

- [ ] **Step 4: Run final LSP evidence chain**

  For each changed source file, run LSP `grep` or `structure`, `inspect(hover|definition)`, `xref(references)` plus incoming/outgoing call hierarchy where supported, `file(read_file)`, and `file(diagnostics)`. Every diagnostic severity must be empty. If TypeScript/JS call hierarchy or another action is unsupported, record tool/action, work_dir, file/symbol, exact error, and narrowed retry; never replace it with shell-only evidence.

- [ ] **Step 5: Check diff hygiene and prohibited scope**

  Run: `git diff --check`

  Run: `git status --short`

  Inspect `git diff --` for the owned product files only. Expected: no lockfile, generated file, `cmd/agent-terminal/web-dist/**`, license, P2/P3, or unrelated change. Scan the diff for secrets, prompts/raw payload logging, and local absolute paths; expected: none introduced.

- [ ] **Step 6: Self-review specification, placeholders, and signatures**

  Confirm all five design sections map one-to-one to Tasks 1–5. Search this plan and the implementation diff for unresolved planning markers and vague implementation instructions; expected: none. Check signature consistency for `restoreApprovalPolicy(...) error`, the malformed-terminal helper, workflow intent record fields, canonical prompt schema fields, and RPC mismatch report fields at every caller/test.

- [ ] **Step 7: Stop at the explicit uncommitted review checkpoint**

  Present the complete diff, fresh test/LSP evidence, remaining blockers, and `git status --short` to the user. Leave every product change unstaged and uncommitted. Do not provide or execute commit commands until the user has reviewed the completed diff and explicitly authorizes Git changes.
