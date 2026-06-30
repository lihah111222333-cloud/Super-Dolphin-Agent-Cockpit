# Evidence

## 2026-06-30 Orchestration Setup

Status: orchestration only. No production implementation started.

Commands and tool checks used:

```bash
git status --short
rg --files | rg '(^AGENTS.md$|README.md$|docs/doc/codemap/README.md|\.agent/workflows|docs/plans/2026-06-30)'
find .agent/workflows -maxdepth 3 -type f 2>/dev/null | sort | tail -80
```

LSP/source checks used:

- `structure(document_symbol)` on `internal/platform/toolbridge/types.go`.
- `grep(text_search)` for `type ToolCallRequest`, `type HostToolRegistry`, `type MCPTool`, rollout glob literals, and scratchpad references.
- `file(read_file)` on `ToolCallRequest`, `HostToolRegistry`, `MCPTool`, Codex rollout discovery, historyjsonl Codex discovery, and thread scratchpad helpers.
- `xref(references)` for `ToolCallRequest` and `sanitizeScratchpadPath`.

Observed facts:

- `ToolCallRequest` currently has no stage/mode field.
- `MCPTool` currently has no read-only hint field.
- Codex rollout glob logic exists in both provider-local rollout reading and util history fallback reading, with different fallback semantics.
- Managed scratchpad path/sanitize/cleanup helpers are currently in `internal/module/thread/scratchpad.go`.
- The current worktree contains unrelated modified files and an untracked source plan.

Verification not run:

- No Go tests were run because this turn created orchestration docs only.

## 2026-06-30 Orchestration Review Fixes

Status: workflow-document fixes only. No production implementation started.

Fixed review findings:

- Approval gate and handoff now require an isolated implementation worktree, not a plain branch in the dirty main checkout.
- C1 writer-preview spike can only write one named ADR or one named plan amendment, not the whole `docs/plans/` directory.
- A0 must update A3's exact ownership and verification commands before A3 can start; A3 remains blocked without those concrete paths.
- README topology now matches `DAG.json`: Lane B and Lane C stay behind their declared dependencies.
- B1 dependency guard and B2 literal-placement guard responsibilities are split in ownership and task text.

Verification:

```bash
jq . .agent/workflows/20260630-reasonix-hardening-absorption/DAG.json >/dev/null
jq . .agent/workflows/20260630-reasonix-hardening-absorption/STATE.json >/dev/null
git diff --check -- .agent/workflows/20260630-reasonix-hardening-absorption
```

The red-flag placeholder scan was run against the workflow directory with evidence self-matches excluded.

## 2026-06-30 Continued Orchestration Review Fixes

Status: workflow-document fixes only. No production implementation started.

Fixed review findings:

- `DAG.json` now makes `B1-sessionpaths-core` depend on the Lane A implementation nodes, matching the README serial topology.
- Added `SOURCE_PLAN_SNAPSHOT.md` so isolated implementation worktrees can read the execution-relevant source plan content from the workflow package even if the original untracked plan file is absent.
- Replaced overlapping `internal/archtest/*sessionpaths*` / `*path*` ownership globs with explicit files: `sessionpaths_dependency_guard_test.go` and `sessionpaths_literal_guard_test.go`.
- Gate 0 now says tasks have concrete verification commands or an explicit pre-start blocker when A0 must fill exact delegation commands.

Verification:

```bash
jq . .agent/workflows/20260630-reasonix-hardening-absorption/DAG.json >/dev/null
jq . .agent/workflows/20260630-reasonix-hardening-absorption/STATE.json >/dev/null
git diff --check -- .agent/workflows/20260630-reasonix-hardening-absorption
```

## 2026-06-30 A0/A3 Ownership Boundary Fix

Status: workflow-document fix only. No production implementation started.

Fixed review finding:

- A0 no longer edits control files directly. It writes a patch-ready proposal into `CHECKS/EVIDENCE.md`; the orchestrator must then update `FILE_OWNERSHIP.tsv` and A3 verification commands before A3 can start.
- `FILE_OWNERSHIP.tsv` now lists A0's `CHECKS/EVIDENCE.md` append permission explicitly while keeping workflow control files under orchestrator ownership.
- A2/A3 now use `not_applicable_with_evidence` for the no-stage-source or no-delegation-entry cases, so Lane B is not permanently blocked by an evidence-backed non-applicable Lane A subtask.

## 2026-06-30 Skeptical Review State-Machine Fixes

Status: workflow-document fix only. No production implementation started.

Fixed review findings:

- Defined `not_applicable_with_evidence` as a dependency-satisfying terminal state and kept `blocked` as non-terminal for downstream scheduling.
- Replaced remaining stale A2 stop-state wording with the evidence-backed non-applicable state for the no-stage-source case.
- Added `NOT_APPLICABLE_WITH_EVIDENCE` to worker return statuses.
- Defined how worker return statuses map to lowercase DAG states, keeping `DONE_WITH_CONCERNS` and `NEEDS_CONTEXT` non-terminal until orchestrator review.
- Replaced A1's broad `internal/archtest/` write permission with the exact `internal/archtest/toolpolicy_dependency_guard_test.go` path.
- Tightened dirty-worktree wording so implementation lanes must use isolated worktrees and final staging remains owned-file-only.

Verification:

- Custom structural validation: `OK workflow structural validation passed`.
- Custom whitespace/conflict-marker validation over untracked workflow files: `OK workflow whitespace/conflict-marker validation passed`.
- JSON validation: both workflow JSON files parse with `jq`.
- Tracked-diff whitespace check: `git diff --check -- .agent/workflows/20260630-reasonix-hardening-absorption` produced no output, but the custom whitespace scan is the authoritative check because these files are untracked.
- Red-flag scan: no matches outside `CHECKS/EVIDENCE.md`.

## 2026-06-30 Stash Restore Boundary Fix

Status: workflow-document fix only. No production implementation started.

Observed state:

- Restored this workflow package and source plan from temporary stash commit `30dc0e34fc4d4e45151ccd4ecd098d49f694f673` third-parent untracked snapshot.
- Restored only `.agent/workflows/20260630-reasonix-hardening-absorption/` and `docs/plans/2026-06-30-reasonix-hardening-absorption-review.md`.
- Unrelated `.githooks`, older plan, and guard test changes are current dirty files and are also preserved in the stash snapshot.

Fixed review finding:

- `STATE.json` reports the dual boundary honestly: unrelated files are current dirty work and also stash-preserved; this workflow must not stage, edit, pop, or drop them.

Verification:

- Custom structural validation: `OK workflow structural validation passed`.
- Custom whitespace/conflict-marker validation over the workflow package and source plan: `OK workflow/source whitespace/conflict-marker validation passed`.
- JSON validation: `OK jq validation passed`.
- Red-flag scan outside `CHECKS/EVIDENCE.md`: no matches.
- Current unrelated dirty files remain outside workflow ownership: `.githooks/README.md`, `.githooks/pre-commit`, `docs/plans/2026-06-29-reasonix-design-absorption-plan.md`, `scripts/guard_fix_commits_have_tests_guard_test.go`, and `scripts/guard_fix_commits_have_tests_helpers_test.go`.

## 2026-06-30 A0 Stage Source Inventory

Status: source inventory only. No production implementation started. Source plan remains `NEEDS_APPROVAL`; execution flag remains `plan_executable=false`.

Exact commands/tools used:

```bash
git status --short
git branch --show-current
git rev-parse --show-toplevel
git worktree list --porcelain
rg --files internal/platform/toolbridge | rg 'handler.*\.go$'
rg -n 'AgentTypePlan|update_plan|EnterPlanMode|ExitPlanMode|TodoWrite|readOnlyHint|PlanSafety|toolfilter' internal cmd
rg -n 'defineTaskWriteTool|task_create_dag|task_apply_ops|task_start_dag|task_update_node|task_dispatch_node' cmd/mcp-orch/tools
rg -n 'tts_generate|av_merge|video_with_audio|ttsToolDefinitions|avMergeToolDefinitions|videoWithAudioToolDefinitions' cmd/mcp-orch/tools internal/platform/shared/builtinprompts internal/platform/shared/workflowtemplates internal/archtest
rg -n 'ListHostTools|WorkflowTemplateWriteHostToolRegistry|ToolNameWorkflowTemplateSave|ToolNameWorkflowTemplateRollback|ToolNameMemoryWrite|NewCompositeHostToolRegistry|provideHostToolRegistry|memory_write' internal/platform/toolbridge internal/app internal/module/memory
```

LSP tools/actions used:

- `structure(document_symbol)` on `internal/platform/toolbridge/types.go`, `internal/platform/toolbridge/handler.go`, `internal/platform/toolbridge/handler_host_tools.go`, `internal/platform/toolbridge/host_tools.go`, `cmd/mcp-orch/tools/registry.go`, and `cmd/mcp-lsp/tools/tool_edit.go`.
- `structure(workspace_symbol)` for `ToolCallRequest`, `HandleToolCall`, `AgentTypePlan`, `CodexNativeToolUpdatePlan`, `ReviewerDecision`, `codexSandboxIsReadOnly`, and `taskToolDefinitions`.
- `grep(text_search)` for `AgentTypePlan|update_plan|EnterPlanMode|ExitPlanMode|TodoWrite|readOnlyHint|PlanSafety|toolfilter` under `internal cmd`.
- `grep(ast_search)` for `func ($R) HandleToolCall($$$)` and `func ($R) CallHostTool($$$)`.
- `inspect(definition)` for `AgentTypePlan`, `CodexNativeToolUpdatePlan`, `ReviewerDecision`, and `codexSandboxIsReadOnly`.
- `xref(references)` for `ToolCallRequest`, `AgentTypePlan`, `CodexNativeToolUpdatePlan`, and `codexSandboxIsReadOnly`.
- `xref(call_hierarchy)` for `HandleToolCall`, `ReviewerDecision`, and `codexSandboxIsReadOnly`.
- `file(read_file)` for the exact functions/line windows listed below.
- `file(diagnostics)` on `internal/platform/toolbridge/types.go`, `internal/platform/toolbridge/handler.go`, and `internal/platform/toolbridge/handler_host_tools.go`: no diagnostics.

LSP limitations observed:

- `xref(call_hierarchy)` on `internal/contract/prompt.go:714:2` returned `AgentTypePlan is not a function`; this is expected for a constant and is not counted as call-hierarchy evidence.
- `structure(document_symbol)` on `cmd/mcp-lsp/tools/edit.go` failed because that path does not exist. Exact follow-up located the real file at `cmd/mcp-lsp/tools/tool_edit.go`.

Stage-source anchors:

- `internal/platform/toolbridge/types.go:27-36` defines `ToolCallRequest` with `Name`, `Arguments`, `AgentID`, `ThreadID`, `TurnID`, `CallID`, `CWD`, `WorkspaceRoots`, `ClientKind`, and internal `Scoped`; there is no `stage`, `mode`, `planning`, or `execution` field.
- `internal/platform/toolbridge/types.go:40-49` normalizes those existing fields only.
- `internal/platform/toolbridge/handler.go:136-156` decodes a `ToolCallRequest`, normalizes tracing metadata, routes Codex surface tools first, then calls `routeToolCall`; no stage value is read or derived.
- `internal/platform/toolbridge/handler_peer_decode.go:474-520` routes Codex surface calls by surface alias, entry execution kind, lifecycle, schema validation, and injected launch context; no planning/execution stage branch exists.
- LSP `xref(references)` for `ToolCallRequest` found consumers in `diff_gen.go`, `handler.go`, `handler_host_tools.go`, `handler_managed_launch.go`, `handler_peer_decode.go`, and decode helpers; the visible reference set used request metadata, routing, host tool, managed launch, lifecycle, and diff paths, not a stage carrier.
- `internal/dto/mcp/tool.go:7-12` defines `MCPTool` with name/description/input/output schema only; it does not carry `readOnlyHint`.
- `internal/contract/prompt.go:707-714` defines `AgentTypePlan` as a subagent prompt assembly type, not a tool-call stage.
- `internal/module/prompt/agent_assembler.go:26-37` uses `AgentTypePlan` only while assembling subagent prompts.
- `internal/module/prompt/agent_assembler.go:53-74` uses `AgentTypePlan` with `AgentTypeExplore` to redact parent context.
- `internal/module/thread/start_session_helpers.go:296-303` recognizes `AgentTypePlan` as a known subagent type; this is still session/prompt identity, not runtime tool stage.
- `internal/contract/provider.go:65` defines Codex native tool `update_plan`.
- `internal/contract/provider.go:138-156` treats `update_plan` as a known Codex native tool ID.
- `internal/contract/provider.go:243-259` classifies `update_plan` as a soft-audit native tool, not as authoritative stage input.
- `internal/provider/codexapp/driver.go:139-170` exposes Codex native tool descriptors; `CodexNativeToolUpdatePlan` is default-disabled soft filter metadata.
- `internal/module/prompt/section.go:127-144` mentions `update_plan` only in prompt/tool-preference text.
- `internal/provider/claudecli/module.go:53-60` declares Claude native `EnterPlanMode`, `ExitPlanMode`, and `TodoWrite` as default-disabled hard-filter native tools.
- `internal/provider/codexapp/driver_pool_routing.go:581-605` parses Codex sandbox read-only state conservatively; it recognizes only explicit read-only/readOnly forms.
- `internal/provider/codexapp/native_tool_policy_validation_test.go:62-84` proves `{"readOnlyHint":true}` alone is not trusted as read-only.
- The required `rg` command found no `PlanSafety` match under `internal cmd`.

Decision:

- `stage_source_found=false`
- V3 currently has planning-related prompt/native-tool markers, but no authoritative runtime/contract field carrying `planning` versus `execution` into `internal/platform/toolbridge`.
- A2 runtime blocking must remain absent and A2 should close as `not_applicable_with_evidence`; do not wire toolbridge runtime blocking from `AgentTypePlan`, `update_plan`, Claude plan-mode tools, or external `readOnlyHint`.

Read-only delegation entry point found:

- `internal/provider/toolfilter/presets.go:5-14` defines the current reviewer allow/deny lists.
- `internal/provider/toolfilter/presets.go:22-30` defines `ReviewerDecision()` as a read-only delegation preset allowing LSP/file/shared-file read tools and denying `edit`, `lsp_edit`, `orchestration_launch_agent`, and `orchestration_stop_agent`.
- `internal/provider/toolfilter/presets_test.go:17-40` tests allowed read-only tools, denied write/lifecycle tools, and exclusion of `shared_file_write`.
- LSP `xref(call_hierarchy)` for `ReviewerDecision` showed only tests as incoming callers, so this is a concrete preset/entry point but not yet a complete runtime policy owner.

Patch-ready orchestrator proposal for A3:

```diff
--- a/.agent/workflows/20260630-reasonix-hardening-absorption/FILE_OWNERSHIP.tsv
+++ b/.agent/workflows/20260630-reasonix-hardening-absorption/FILE_OWNERSHIP.tsv
@@
-internal/provider/toolfilter/	A3-readonly-delegation-filter	RW	Reuse reviewer preset only as input; not a complete policy owner.
+internal/provider/toolfilter/presets.go	A3-readonly-delegation-filter	RW	Tighten read-only delegation preset; must exclude writer, planning mutator, lifecycle/process-control, recursive agent, connector, and untrusted external hint surfaces.
+internal/provider/toolfilter/presets_test.go	A3-readonly-delegation-filter	RW	Focused tests for read-only delegation allow/deny surface.
```

```diff
--- a/.agent/workflows/20260630-reasonix-hardening-absorption/TASKS/A3-readonly-delegation-filter.md
+++ b/.agent/workflows/20260630-reasonix-hardening-absorption/TASKS/A3-readonly-delegation-filter.md
@@
+Source entry point:
+- `internal/provider/toolfilter/presets.go:5-14`
+- `internal/provider/toolfilter/presets.go:22-30`
+- `internal/provider/toolfilter/presets_test.go:17-40`
+
+Recommended verification commands:
+```bash
+./scripts/test_with_guard.sh ./internal/provider/toolfilter -run 'Reviewer|Worker|FullAccess' -count=1
+rg -n 'ReviewerDecision|reviewerAllowedTools|reviewerDeniedTools|shared_file_write|orchestration_launch_agent|lsp_edit|memory_write|task_|workspace_|workflow_template_|update_plan' internal/provider/toolfilter internal/platform/toolbridge cmd/mcp-orch/tools cmd/mcp-lsp
+```
```

C1 reusable model-callable writer/tool-surface family anchors:

- Host-direct tool surface: `internal/platform/toolbridge/handler_peer_decode.go:98-107` adds host tools into the Codex surface; `internal/platform/toolbridge/host_tools.go:29-40` defines `HostToolRegistry`.
- Host-direct default writer: `internal/platform/toolbridge/memory_write_tool.go:14-22` defines `memory_write`; `internal/platform/toolbridge/memory_write_tool.go:51-82` exposes and calls it when enabled.
- Host-direct workflow template read/write split: `internal/platform/toolbridge/host_tools.go:57-64` defines template tools; `internal/platform/toolbridge/host_tools.go:187-199` exposes read-only list/get/render; `internal/platform/toolbridge/host_tools.go:236-281` exposes authorized `workflow_template_save` and `workflow_template_rollback`.
- MCP LSP writer: `cmd/mcp-lsp/tools.go:35-41` registers `edit`; `cmd/mcp-lsp/schema.go:131-140` declares `replace_range`, `rename`, `code_action`, and `format`; `cmd/mcp-lsp/tools/tool_edit.go:68-99` dispatches those actions.
- MCP LSP write behavior: `cmd/mcp-lsp/tools/tool_edit_replace.go:170-249` applies `replace_range`; `cmd/mcp-lsp/tools/tool_edit_lsp_actions.go:21-82` can apply a single `code_action` workspace edit; `cmd/mcp-lsp/tools/tool_edit_lsp_actions.go:84-112` applies format edits.
- MCP-orch registry surface: `cmd/mcp-orch/tools/registry.go:38-48` registers orchestration/task/workspace/prompt/command/shared-file/registry/TTS/AV/video tool families.
- MCP-orch task writers: `cmd/mcp-orch/tools/task_tool_definitions.go:17-27` defines `defineTaskWriteTool`; `cmd/mcp-orch/tools/task_tool_definitions.go:50-101` registers `task_create_dag`, `task_dag_apply_ops`, `task_update_node`, `task_dispatch_node`, `task_start_dag`, `task_terminate_dag`, `task_delete_dag`, and recovery action as high-risk workflow-write tools.
- MCP-orch workspace writers: `cmd/mcp-orch/tools/workspace_tools.go:117-150` registers `workspace_create_run`, `workspace_merge_run` with `dry_run`, and `workspace_abort_run`.
- MCP-orch shared-file writer: `cmd/mcp-orch/tools/shared_file_tools.go:50-61` registers `shared_file_write`; `cmd/mcp-orch/tools/shared_file_tools.go:85-112` validates write path/content and upserts.
- Artifact/process side-effect tools: `cmd/mcp-orch/tools/tts_tools.go:23-57` registers `tts_generate`; `cmd/mcp-orch/tools/av_merge_tools.go:21-35` registers `av_merge`; `cmd/mcp-orch/tools/video_with_audio_tools.go:24-42` registers `video_with_audio`.
- Provider-native boundaries: `internal/provider/codexapp/driver.go:139-170` lists Codex native tools such as file write/apply patch/shell/subagent/update_plan; `internal/provider/claudecli/module.go:47-76` lists Claude native tools including write/edit/bash/plan/todo/task controls.

## 2026-06-30 A0 Main Review And Dispatch Decision

Status: orchestration-control update only. No production implementation performed by the main agent.

User approval recorded:

- Approved full workflow with Lane A first.
- Constraint: all execution must happen in child agents and independent worktrees; main agent only reviews and integrates.

A0 review:

- Worker branch: `codex/reasonix-hardening-a0-20260630`.
- Worker commit: `9aa751b451fa10ddb10730138437d251c1527ab2`.
- Main LSP review confirmed `ToolCallRequest` has no stage/mode field and `HandleToolCall` does not read or derive a planning/execution stage.
- Main LSP review confirmed `ReviewerDecision` exists in `internal/provider/toolfilter/presets.go` and is covered by `internal/provider/toolfilter/presets_test.go`.
- Main verification reran the required `rg` command and `git diff --check` for the A0 evidence diff.

Dispatch decision:

- A0 accepted as `done_with_concerns`.
- A2 mapped to `not_applicable_with_evidence` because `stage_source_found=false`.
- A0's A3 ownership proposal was applied to workflow control files before A3 dispatch.

## 2026-06-30 A1 Toolpolicy Core Review And Integration

Status: A1 production implementation was performed by a child agent in an isolated worktree and reviewed before integration.

Worker branch and commits:

- Branch: `codex/reasonix-hardening-a1-20260630`.
- Initial commit: `914d5dfddb8e0315107f522f7bfd63295c3c67ff`.
- Fix commit: `5bffbcca075917b5fad03cc09ffb831c0ef5f6c1`.
- Integrated by fast-forward into `codex/reasonix-hardening-integration-20260630`.

Ownership review:

- `git diff --name-status 93fa54348efecaa01b7ee4fb374b6de198aa6d76..HEAD` showed only A1-owned files:
  - `internal/platform/toolpolicy/policy.go`
  - `internal/platform/toolpolicy/shell.go`
  - `internal/platform/toolpolicy/policy_test.go`
  - `internal/archtest/toolpolicy_dependency_guard_test.go`
- No workflow, provider, toolbridge, frontend, generated, or mcp-orch production files were changed by the A1 worker.

Review finding and fix:

- Independent reviewer first returned `FAIL` because `allowGitArgs` treated every `git branch ...` invocation as read-only, allowing write forms such as `git branch foo`, `git branch -d foo`, `git branch -D foo`, rename, and upstream-changing commands.
- The A1 worker added regression tests for those `git branch` write forms and for `git diff --output file`.
- The A1 worker split `git branch` validation into exact read-only forms and added rejection for space-separated dangerous output/config args.
- Independent reviewer reran after `5bffbcca075917b5fad03cc09ffb831c0ef5f6c1` and returned `PASS`.

Main LSP review:

- `structure(document_symbol)` on `internal/platform/toolpolicy/policy.go`, `shell.go`, and `policy_test.go` confirmed the new owner package shape.
- `xref(references)` for `ClassifyShell` showed only same-package tests plus the declaration, so A1 did not wire A2 runtime blocking.
- `file(diagnostics)` for `internal/platform/toolpolicy/policy.go`, `internal/platform/toolpolicy/shell.go`, `internal/platform/toolpolicy/policy_test.go`, and `internal/archtest/toolpolicy_dependency_guard_test.go` returned no diagnostics.

Main verification:

```bash
./scripts/test_with_guard.sh ./internal/platform/toolpolicy -run 'Plan|ReadOnly|Trust|Shell' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'ToolPolicy|Dependency' -count=1
git diff --check 93fa54348efecaa01b7ee4fb374b6de198aa6d76..HEAD
git diff --cached --check
git status --short
```

All commands above passed; both status checks produced no output.

Dispatch decision:

- A1 accepted as `done`.
- A3 moved to `ready` because A0 identified exact delegation entry files and A1 is now integrated.
- Lane B remains waiting until A3 and the remaining Lane A gates close.

## 2026-06-30 A3 Read-only Delegation Filter Review And Lane A Gate

Status: A3 production implementation was performed by a child agent in an isolated worktree and reviewed before integration.

Worker branch and commits:

- Agent id: `019f1825-176b-71a0-bfb3-b5b079d6087c`.
- Branch: `codex/reasonix-hardening-a3-20260630`.
- Initial commit: `832797fbbbe89f613d3d8573cb74f97452793a0e`.
- Exact-deny fix commit: `8770a88067efca54303f07940c73afe14b242e4b`.
- Native-recursive fix commit: `2637e908adebffff737f9470adb84d647e017cbb`.
- Integrated by fast-forward into `codex/reasonix-hardening-integration-20260630`.

Ownership review:

- `git diff --name-status 686d6f76..2637e908adebffff737f9470adb84d647e017cbb` showed only A3-owned files:
  - `internal/provider/toolfilter/presets.go`
  - `internal/provider/toolfilter/presets_test.go`
- No workflow, toolbridge, cmd/mcp-orch, cmd/mcp-lsp, frontend, generated, or unrelated provider files were changed by the A3 worker.

Review findings and fixes:

- First main review held A3 because `DeniedTools` used `task_`, `workspace_`, and `workflow_template_` prefix sentinels, while real `internal/platform/hooks/merge.go` only merges exact string names. The A3 worker replaced prefix sentinels with exact tool names and added a regression test proving prefix sentinels are not used.
- Second main review held A3 because Codex native recursive controls were missing. Source anchors included `internal/contract/provider.go:41-47`, `internal/contract/provider.go:121-130`, `internal/provider/codexapp/driver.go:145-151`, and `cmd/mcp-orch/tools/orchestration_tools.go:120-121`. The A3 worker added exact denies for `multi_agent`, `multi_tool_use.parallel`, `spawn_agent`, `send_input`, `resume_agent`, `wait_agent`, and `close_agent`.
- Read-only reviewer `019f183f-c27b-7de3-af87-cc5d040d64d5` was resumed with the latest commit, but timed out without a verdict and was closed as `shutdown`. The controller did not use the stale/absent reviewer verdict for acceptance.

Main LSP review:

- `structure(document_symbol)` on `internal/provider/toolfilter/presets.go` and `presets_test.go` confirmed the new helper functions and tests.
- `xref(references)` for `ReviewerDecision` and `reviewerDeniedTools` showed references only in the same package and tests.
- `file(diagnostics)` for `internal/provider/toolfilter/presets.go` and `internal/provider/toolfilter/presets_test.go` returned no diagnostics.

Lane A verification after integration:

```bash
./scripts/test_with_guard.sh ./internal/platform/toolpolicy ./internal/provider/toolfilter ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/provider/claudecli -run 'Plan|ReadOnly|Trust|Shell|Lifecycle|Sandbox|Permission|Reviewer|Worker|FullAccess|Native|Tool' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Tool|Provider' -count=1
make guard
git diff --check 686d6f76..HEAD
git diff --cached --check
git status --short
```

All commands above passed; `git diff --cached --check` and final status checks produced no output.

Dispatch decision:

- A3 accepted as `done`.
- Lane A gates passed.
- B1 moved to `ready`; B2 and C1 remain waiting behind the DAG.
