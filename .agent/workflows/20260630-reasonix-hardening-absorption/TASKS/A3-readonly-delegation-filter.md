---
task_id: A3-readonly-delegation-filter
owner: agent-a3
status: done
depends_on: [A1-toolpolicy-core, A0-stage-source-inventory]
---

# A3-readonly-delegation-filter

## 1. Goal

Ensure read-only subagent or planning-only delegation receives a restricted tool surface instead of relying on prompt text.

## 2. Inputs

- A0 delegation surface inventory.
- `internal/provider/toolfilter/`
- Any read-only subagent or delegation launch path identified by A0.
- A1 `toolpolicy` package.

## 3. Outputs

- Tests proving restricted surfaces exclude writers, workflow/meta tools, job/process tools, planning-state mutators, recursive agent/skill tools, connector tools, and external untrusted read-only hints.
- Minimal integration with the concrete delegation surface found by A0.

## 4. File Permissions

- RW: `internal/provider/toolfilter/presets.go`
- RW: `internal/provider/toolfilter/presets_test.go`
- RO: `internal/platform/toolbridge/`
- NO-TOUCH: unrelated provider/session lifecycle code.

Source entry point from A0:

- `internal/provider/toolfilter/presets.go:5-14`
- `internal/provider/toolfilter/presets.go:22-30`
- `internal/provider/toolfilter/presets_test.go:17-40`

## 5. Steps

1. If A0 did not identify a concrete delegation entry point, stop and mark this task `not_applicable_with_evidence`. If A0 did identify an entry point but the orchestrator did not apply exact `FILE_OWNERSHIP.tsv` paths and verification commands from A0 evidence, stop and mark this task blocked.
2. Add tests for writer exclusion.
3. Add tests for workflow/meta, job/process, and planning-state mutator exclusion, including `wait`, `bash_output`, `todo_write`, and `complete_step` if present in the surface.
4. Add tests for recursive agent/skill and connector tool exclusion, including `connect_tool_source` if present.
5. Add tests proving external untrusted read-only hints are not enough.
6. Reuse `internal/provider/toolfilter` reviewer presets as input only; make `toolpolicy` the decision owner.
7. Wire the concrete delegation surface to the restricted tool set.

## 6. Verification Commands

```bash
./scripts/test_with_guard.sh ./internal/provider/toolfilter -run 'Reviewer|Worker|FullAccess' -count=1
rg -n 'ReviewerDecision|reviewerAllowedTools|reviewerDeniedTools|shared_file_write|orchestration_launch_agent|lsp_edit|memory_write|task_|workspace_|workflow_template_|update_plan' internal/provider/toolfilter internal/platform/toolbridge cmd/mcp-orch/tools cmd/mcp-lsp
```

The orchestrator applied the exact delegation package command from A0 evidence before A3 dispatch. A3 then completed in `codex/reasonix-hardening-a3-20260630` at `2637e908adebffff737f9470adb84d647e017cbb`.

## 7. DoD

- [x] Restricted delegation tests cover all excluded tool classes from the source plan.
- [x] `FILE_OWNERSHIP.tsv` names every production and test file this task edits.
- [x] Prompt-only read-only enforcement is no longer the only boundary for the identified surface.
- [x] No broad provider refactor.

## 8. Rollback

Revert task-owned toolfilter and exact delegation entry changes.
