# Handoff

## Current Status

Overall: done. A3 round2 commit `5f7406d992b4d2dba19408d738799b298673009a` was merged by `0b16e06f`, docs round2 commit `ade6151d28c4d6480029cfe087322ec2acb2aebf` was merged by `e17cb8b3`, and fresh final gates passed on final code verification head `e17cb8b393293f34ae8af73238906b66abc8d45c`. This docs-only status commit keeps the workflow state synchronized after those gates.

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
- A3 pre-round2 completed in `codex/reasonix-hardening-a3-20260630` at `2637e908adebffff737f9470adb84d647e017cbb`.
- A3 was later reopened by post-review findings on read-only launch/native tool propagation.
- Post-review repair `836705200f7b4a7eca05bb93925dde4fbb9124f8` is integrated in current HEAD.
- Round2 repair `5f7406d992b4d2dba19408d738799b298673009a` fixed Codex native disabled-tool propagation through orchestration launch, thread config, and provider tests; it was merged by `0b16e06f`.
- Fresh round2 Lane A focused gates and `make guard` passed on code verification head `e17cb8b393293f34ae8af73238906b66abc8d45c`.
- B1 completed in `codex/reasonix-hardening-b1-20260630` at `2f8c85a569037f25950498dc484848b80cc942a1`.
- B1 added `internal/platform/sessionpaths`, golden tests, and the stdlib dependency guard without migrating callers.
- B2 completed in `codex/reasonix-hardening-b2-20260630` at `28e36eae9cc079daca0ffbbbd05d0836125a7d2e`.
- B2 migrated provider/util/thread callers to `sessionpaths`; Lane B focused gates and `make guard` passed after integration.
- C1 completed in `codex/reasonix-hardening-c1-20260630` at `e13c157123fd4999b21387af0583d3a2d45f13b7`.
- C1 produced `docs/adr/2026-06-30-writer-preview-contract-spike.md` only; Lane C focused gates passed after integration.
- PN final gates passed after A3 round2 on code verification head `e17cb8b393293f34ae8af73238906b66abc8d45c`.

## In Progress

- None.

## Blocked

- A2 runtime blocking must remain absent until a future approved source proves a concrete planning/execution stage source.
- C1 production preview API and host-direct preview/execute tests remain deferred; the integrated C1 result is ADR-only.

## Recommended Next Action

Review or merge the integration branch. Fresh gates already passed on code verification head `e17cb8b393293f34ae8af73238906b66abc8d45c` with:

```bash
./scripts/test_with_guard.sh ./internal/platform/toolpolicy ./internal/provider/toolfilter ./internal/platform/toolbridge ./internal/provider/codexapp ./internal/provider/claudecli -run 'Plan|ReadOnly|Trust|Shell|Lifecycle|Sandbox|Permission|Reviewer|Worker|FullAccess|Native|Tool' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/module/thread ./internal/provider/codexapp ./internal/contract -run 'LaunchRequestFromExecutable|BuildStartSessionConfig|CodexNativeToolPolicy|ReadOnly' -count=1
./scripts/test_with_guard.sh ./cmd/mcp-orch/tools ./internal/module/thread ./internal/provider/toolfilter ./internal/platform/toolpolicy ./internal/contract -count=1
./scripts/test_with_guard.sh ./internal/platform/sessionpaths ./internal/provider/codexapp ./internal/module/thread ./internal/util/historyjsonl -run 'Rollout|Scratchpad|Path|Cleanup|CodexHome|History' -count=1
rg -n 'memory_write|workflow_template_save|workflow_template_rollback|shared_file_write|defineTaskWriteTool|workspace_create_run|workspace_merge_run|workspace_abort_run|tts_generate|av_merge|video_with_audio|difftracker|Preview' internal cmd docs
./scripts/test_with_guard.sh ./internal/archtest -run 'Dependency|Tool|Provider|Path|Preview|Workflow' -count=1
make guard
git diff --check
git diff --cached --check
git status --short
```

## Historical Execution Sequence

1. P0 created the workflow structure and ownership files.
2. A0 ran first, recorded `stage_source_found=false`, and proposed the concrete A3 ownership/verification paths.
3. A2 closed as `not_applicable_with_evidence`; no runtime planning-stage blocking was wired.
4. A1 landed the `toolpolicy` package and tests.
5. A3 applied the A0 delegation proposal and landed restricted read-only delegation tests.
6. Post-review A3 repairs expanded the task from toolfilter presets to orchestration launch, contract, thread startup config, and Codex provider test surfaces.
7. Lane B landed `sessionpaths` helper extraction, golden tests, and caller migration.
8. C1 produced only `docs/adr/2026-06-30-writer-preview-contract-spike.md`; no production preview contract or tests were added.

## Review Entry Points

- Start with `STATE.json` for task statuses and integrated branch/commit pointers.
- Use `CHECKS/EVIDENCE.md` for source anchors and verification logs.
- Use `CHECKS/RESULT_GATES.md` to distinguish completed gates from deferred A2/C1 production work.

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
