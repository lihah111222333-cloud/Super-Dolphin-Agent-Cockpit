# Handoff

## Current Status

Overall: in progress. The user approved the full workflow with Lane A first, production/doc lane work was performed only by child agents in isolated worktrees, and the integration branch is ready for final PN gates.

## Completed

- P0 workflow structure created.
- DAG and task cards created.
- File ownership matrix created.
- Risk register and gates initialized.
- Initial source anchors recorded in evidence.
- A0 completed in `codex/reasonix-hardening-a0-20260630` at `9aa751b451fa10ddb10730138437d251c1527ab2`.
- A2 closed as `not_applicable_with_evidence` because A0 found no authoritative stage source.
- A0's A3 ownership and verification proposal was applied to `FILE_OWNERSHIP.tsv` and `TASKS/A3-readonly-delegation-filter.md`.
- A1 completed in `codex/reasonix-hardening-a1-20260630` at `5bffbcca075917b5fad03cc09ffb831c0ef5f6c1`.
- A1 initial implementation was held after independent review found unsafe `git branch` read-only classification; the worker fixed it before integration.
- A3 completed in `codex/reasonix-hardening-a3-20260630` at `2637e908adebffff737f9470adb84d647e017cbb`.
- A3 was held twice during main review: first for prefix-style deny evidence that did not match real `DeniedTools` exact-name semantics, then for missing Codex native recursive controls.
- Lane A focused gates and `make guard` passed after A3 integration.
- B1 completed in `codex/reasonix-hardening-b1-20260630` at `2f8c85a569037f25950498dc484848b80cc942a1`.
- B1 added `internal/platform/sessionpaths`, golden tests, and the stdlib dependency guard without migrating callers.
- B2 completed in `codex/reasonix-hardening-b2-20260630` at `28e36eae9cc079daca0ffbbbd05d0836125a7d2e`.
- B2 migrated provider/util/thread callers to `sessionpaths`; Lane B focused gates and `make guard` passed after integration.
- C1 completed in `codex/reasonix-hardening-c1-20260630` at `e13c157123fd4999b21387af0583d3a2d45f13b7`.
- C1 produced `docs/adr/2026-06-30-writer-preview-contract-spike.md` only; Lane C focused gates passed after integration.

## In Progress

- None.

## Blocked

- A2 runtime blocking must remain absent until a future approved source proves a concrete planning/execution stage source.

## Recommended Next Action

Run PN-integration final gates on `codex/reasonix-hardening-integration-20260630`:

```bash
./scripts/test_with_guard.sh ./internal/platform/toolpolicy ./internal/provider/toolfilter ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/provider/claudecli -run 'Plan|ReadOnly|Trust|Shell|Lifecycle|Sandbox|Permission|Reviewer|Worker|FullAccess|Native|Tool' -count=1
./scripts/test_with_guard.sh ./internal/platform/sessionpaths ./internal/provider/codexapp ./internal/module/thread ./internal/util/historyjsonl -run 'Rollout|Scratchpad|Path|Cleanup|CodexHome|History' -count=1
rg -n 'memory_write|workflow_template_save|workflow_template_rollback|shared_file_write|defineTaskWriteTool|workspace_create_run|workspace_merge_run|workspace_abort_run|tts_generate|av_merge|video_with_audio|difftracker|Preview' internal cmd docs
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Tool|Provider|Path|Preview|Workflow' -count=1
make guard
git diff --check
git diff --cached --check
git status --short
```

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
./scripts/test_with_guard.sh ./internal/platform/toolpolicy ./internal/provider/toolfilter ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/provider/claudecli -run 'Plan|ReadOnly|Trust|Shell|Lifecycle|Sandbox|Permission|Reviewer|Worker|FullAccess|Native|Tool' -count=1
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
rg -n 'memory_write|workflow_template_save|workflow_template_rollback|shared_file_write|defineTaskWriteTool|workspace_create_run|workspace_merge_run|workspace_abort_run|tts_generate|av_merge|video_with_audio|difftracker|Preview' internal cmd docs
./scripts/test_with_guard.sh ./internal/archtest -run 'VideoSkill|Tool|Preview|Workflow|Dependency' -count=1
```

## Ownership Reminder

Stage only files owned by the active task. Keep unrelated dirty and stash-preserved `.githooks`, older plan, and guard test modifications out of this workflow unless the user explicitly expands scope.
