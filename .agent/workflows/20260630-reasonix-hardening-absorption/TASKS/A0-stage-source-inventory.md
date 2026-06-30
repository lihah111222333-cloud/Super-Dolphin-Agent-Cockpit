---
task_id: A0-stage-source-inventory
owner: agent-a0
status: needs_approval
depends_on: [P0-orchestration]
---

# A0-stage-source-inventory

## 1. Goal

Prove where V3 can obtain an authoritative `planning` versus `execution` stage before any runtime blocking is wired.

## 2. Inputs

- `SOURCE_PLAN_SNAPSHOT.md` Lane A.
- `internal/platform/toolbridge/types.go`
- `internal/platform/toolbridge/handler*.go`
- `internal/provider/codexapp/`
- `internal/provider/claudecli/`
- `internal/provider/toolfilter/`
- `cmd/mcp-orch/tools/`
- Relevant tests in the same packages.

## 3. Outputs

- A written source inventory appended to `CHECKS/EVIDENCE.md`.
- A decision: `stage_source_found=true` or `stage_source_found=false`.
- A delegation entry proposal in `CHECKS/EVIDENCE.md`, if any concrete read-only delegation surface is found: exact file paths, matching test paths, and verification commands for the orchestrator to apply to A3.
- If false, runtime blocking remains absent and A2 closes as `not_applicable_with_evidence`.

## 4. File Permissions

- RW: `.agent/workflows/20260630-reasonix-hardening-absorption/CHECKS/EVIDENCE.md`
- RO: `internal/platform/toolbridge/`, `internal/provider/`, `cmd/mcp-orch/tools/`, `internal/dto/mcp/`
- NO-TOUCH: production source files.

## 5. Steps

1. Use LSP `structure(document_symbol)` on `internal/platform/toolbridge/types.go` and `handler.go`.
2. Use LSP `xref(references)` on `ToolCallRequest`.
3. Use LSP `grep(text_search)` for `AgentTypePlan`, `update_plan`, `EnterPlanMode`, `ExitPlanMode`, `TodoWrite`, `readOnlyHint`, `PlanSafety`, and `toolfilter`.
4. Use LSP `inspect(definition)` and `xref(call_hierarchy)` on any candidate stage or plan-mode source.
5. Read exact functions with LSP `file(read_file)`.
6. Record whether an explicit runtime/contract field can carry stage into toolbridge.
7. Record read-only delegation entry points and model-callable tool surface families for A3 and C1.
8. If A3 has a concrete delegation entry point, write a patch-ready proposal in `CHECKS/EVIDENCE.md` for the orchestrator to update `FILE_OWNERSHIP.tsv` and `TASKS/A3-readonly-delegation-filter.md`; do not edit those control files from A0.

## 6. Verification Commands

```bash
rg -n 'AgentTypePlan|update_plan|EnterPlanMode|ExitPlanMode|TodoWrite|readOnlyHint|PlanSafety|toolfilter' internal cmd
```

## 7. DoD

- [ ] Evidence contains exact file:line anchors and LSP actions used.
- [ ] Stage-source decision is explicit.
- [ ] Evidence includes a patch-ready A3 ownership and verification proposal if delegation entry files are found.
- [ ] No production code changed.
- [ ] A2 is marked `not_applicable_with_evidence` if no authoritative stage source exists.

## 8. Rollback

Revert only this task's evidence additions.
