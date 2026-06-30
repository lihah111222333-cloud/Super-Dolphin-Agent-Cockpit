# Result Gates

## Gate 0: Orchestration

- [x] Source plan read completely.
- [x] Current dirty worktree boundary recorded.
- [x] DAG exists and is acyclic.
- [x] Every implementation task has ownership, dependencies, DoD, and either concrete verification commands or an explicit pre-start blocker for commands that A0 must fill.
- [x] `FILE_OWNERSHIP.tsv` separates RW production files by task.
- [x] Risk register covers approval, stage source, tool trust, lifecycle, shell, path behavior, spike scope, and dirty files.

## Gate 1: Approval

- [x] User explicitly approves Lane A or the full workflow.
- [x] Isolated implementation worktree is created before production edits.
- [x] Source plan status is either updated to executable or approval is recorded in workflow evidence.
- [x] Worker report status is mapped to a DAG state; only `done` and `not_applicable_with_evidence` satisfy downstream DAG dependencies.

## Gate 2: Lane A Completion

Status: passed after A3 round2 integration on final code verification head `e17cb8b393293f34ae8af73238906b66abc8d45c`.

- [x] A0 stage source inventory completed with LSP references and exact source anchors.
- [x] A1 `internal/platform/toolpolicy` tests passed.
- [x] A2 remains `not_applicable_with_evidence` because A0 found no valid stage source.
- [x] A3 post-review repairs `836705200f7b4a7eca05bb93925dde4fbb9124f8` and `5f7406d992b4d2dba19408d738799b298673009a` are integrated.
- [x] Round2 Lane A guard commands passed on code verification head `e17cb8b393293f34ae8af73238906b66abc8d45c`.

## Gate 3: Lane B Completion

- [x] B1 sessionpaths golden tests pass before migration.
- [x] B2 caller migration preserves rollout and scratchpad behavior.
- [x] B1 dependency guard and B2 literal-placement guard pass.
- [x] Affected package guard command passes.

## Gate 4: Lane C ADR-Only Spike Completion

- [x] Writer inventory marks model-callable status and owner for each writer surface.
- [x] Preview feasibility is classified per writer type.
- [x] ADR or explicitly named plan amendment records which writers can preview and which cannot.
- [x] No production preview interface is added without separate approval.
- [x] Gate 4 does not claim the source plan's host-direct preview/execute unit-test acceptance; that production contract work remains deferred.

## Gate 5: Integration

Status: passed on code verification head `e17cb8b393293f34ae8af73238906b66abc8d45c` after A3 round2 and docs round2 were merged.

- [x] A3 round2 worker commit `5f7406d992b4d2dba19408d738799b298673009a` was merged by `0b16e06f`.
- [x] Docs round2 commit `ade6151d28c4d6480029cfe087322ec2acb2aebf` was merged by `e17cb8b3`.
- [x] Lane A, Lane B, C1 writer-surface `rg`, archtest, `make guard`, and diff checks passed fresh on code verification head `e17cb8b3`.
- [x] This docs-only status commit keeps workflow state synchronized after those gates.
- [x] Workflow `STATE.json`, `CHECKS/EVIDENCE.md`, `RESULT_GATES.md`, and `HANDOFF.md` updated to final integrated state.
