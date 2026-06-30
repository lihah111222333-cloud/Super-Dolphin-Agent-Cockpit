---
task_id: A3-readonly-delegation-filter
owner: agent-a3
status: needs_approval
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

- RW: `internal/provider/toolfilter/`
- RW: exact delegation entry files added to `FILE_OWNERSHIP.tsv` by the orchestrator after A0 evidence review.
- RW: matching tests added to `FILE_OWNERSHIP.tsv` by the orchestrator after A0 evidence review.
- RO: `internal/platform/toolbridge/`
- NO-TOUCH: unrelated provider/session lifecycle code.

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
./scripts/test_with_guard.sh ./internal/provider/toolfilter ./internal/platform/toolpolicy -run 'ReadOnly|Reviewer|Tool|Trust|Plan' -count=1
```

The orchestrator must replace this base command with exact delegation package commands from A0 evidence before A3 starts. Without those commands after A0 identifies a concrete entry point, this task remains blocked.

## 7. DoD

- [ ] Restricted delegation tests cover all excluded tool classes from the source plan.
- [ ] `FILE_OWNERSHIP.tsv` names every production and test file this task edits.
- [ ] Prompt-only read-only enforcement is no longer the only boundary for the identified surface.
- [ ] No broad provider refactor.

## 8. Rollback

Revert task-owned toolfilter and exact delegation entry changes.
