# Handoff

## Current Status

Overall: in progress. The user approved the full workflow with Lane A first, and production code must still be implemented only by child agents in isolated worktrees.

## Completed

- P0 workflow structure created.
- DAG and task cards created.
- File ownership matrix created.
- Risk register and gates initialized.
- Initial source anchors recorded in evidence.
- A0 completed in `codex/reasonix-hardening-a0-20260630` at `9aa751b451fa10ddb10730138437d251c1527ab2`.
- A2 closed as `not_applicable_with_evidence` because A0 found no authoritative stage source.
- A0's A3 ownership and verification proposal was applied to `FILE_OWNERSHIP.tsv` and `TASKS/A3-readonly-delegation-filter.md`.

## In Progress

- None.

## Blocked

- A2 runtime blocking must remain absent until A0 proves a concrete planning/execution stage source.
- A3 waits for A1 because A3 uses the A1 `toolpolicy` decision owner.

## Recommended Next Action

Dispatch A1 next in an isolated worker worktree:

```text
执行 A1-toolpolicy-core；不得接 A2 runtime blocking。
```

This keeps the highest-risk safety lane narrow and prevents accidental runtime behavior changes after A0 proved no stage authority exists.

## After Approval

1. Create an isolated worktree with a branch using prefix `codex/`.
2. Run `git status --short` in the implementation worktree.
3. Execute A0 first and update `CHECKS/EVIDENCE.md`.
4. If A0 proves stage authority, continue A1 -> A2 -> A3.
5. If A0 does not prove stage authority, land only A1 tests/package if still approved, and mark A2 `not_applicable_with_evidence`.
6. If A0 finds no concrete delegation entry point, mark A3 `not_applicable_with_evidence`; if it finds one, the orchestrator must apply A0's ownership and verification proposal before A3 starts.
7. Treat `not_applicable_with_evidence` as a terminal dependency-satisfying state, not as permission to wire fallback production behavior.

## Verification Commands

Lane A:

```bash
./scripts/test_with_guard.sh ./internal/platform/toolpolicy ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/provider/claudecli -run 'Plan|ReadOnly|Trust|Shell|Lifecycle|Sandbox|Permission' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Tool|Provider' -count=1
make guard
```

Lane B:

```bash
./scripts/test_with_guard.sh ./internal/platform/sessionpaths ./internal/provider/codexapp ./internal/module/thread ./internal/util/historyjsonl -run 'Rollout|Scratchpad|Path|Cleanup|CodexHome|History' -count=1
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Path|Provider|Thread' -count=1
make guard
```

Lane C:

```bash
rg -n 'memory_write|workflow_template_save|workflow_template_rollback|shared_file_write|defineTaskWriteTool|workspace_create_run|workspace_merge_run|workspace_abort_run|tts_generate|av_merge|video_with_audio' internal cmd
```

## Ownership Reminder

Stage only files owned by the active task. Keep unrelated dirty and stash-preserved `.githooks`, older plan, and guard test modifications out of this workflow unless the user explicitly expands scope.
