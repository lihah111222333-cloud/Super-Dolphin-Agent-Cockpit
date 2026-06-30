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

Status: pending post-review repair integration. Pre-round2 Lane A gate evidence is historical until the controller integrates `5f7406d992b4d2dba19408d738799b298673009a` and reruns the focused gates.

- A0 stage source inventory completed with LSP references and exact source anchors.
- A1 `internal/platform/toolpolicy` tests passed before the post-review repair.
- A2 remains `not_applicable_with_evidence` because A0 found no valid stage source.
- A3 pre-round2 checks passed, but review reopened the launch/native tool surface; `836705200f7b4a7eca05bb93925dde4fbb9124f8` is integrated and `5f7406d992b4d2dba19408d738799b298673009a` is worker-complete pending controller integration.
- Final Lane A closure requires fresh post-round2 guard evidence.

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

Status: pre-round2 final gate evidence is superseded. Do not claim final PN closure from `8fa31b90`; current base is `7c14c7ee435ae9051672ca79962cd938ba5ce780`, and A3 round2 still requires controller integration plus final gate rerun.

- Worker reports through C1 were reviewed before round2.
- Round2 A3 worker report for `5f7406d992b4d2dba19408d738799b298673009a` must be reviewed and merged by the controller.
- Lane-specific guard commands, `make guard`, diff checks, and clean status must be rerun after that merge before final closure is recorded.
