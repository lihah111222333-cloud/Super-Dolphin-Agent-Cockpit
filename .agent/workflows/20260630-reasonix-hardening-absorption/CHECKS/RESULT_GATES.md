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

- [x] A0 stage source inventory completed with LSP references and exact source anchors.
- [x] If A0 identifies delegation entry files for A3, the orchestrator applies A0's evidence proposal to `FILE_OWNERSHIP.tsv` and A3 verification commands before A3 starts.
- [x] A1 `internal/platform/toolpolicy` tests pass, and `internal/archtest/toolpolicy_dependency_guard_test.go` passes if created.
- [x] A2 toolbridge planning-stage gate tests pass, or A2 is marked `not_applicable_with_evidence` because A0 found no valid stage source.
- [x] A3 read-only delegation filter tests pass, or A3 is marked `not_applicable_with_evidence` because A0 found no concrete delegation entry point.
- [x] Provider sandbox/security regression tests pass.

## Gate 3: Lane B Completion

- [x] B1 sessionpaths golden tests pass before migration.
- [x] B2 caller migration preserves rollout and scratchpad behavior.
- [x] B1 dependency guard and B2 literal-placement guard pass.
- [x] Affected package guard command passes.

## Gate 4: Lane C Spike Completion

- [x] Writer inventory marks model-callable status and owner for each writer surface.
- [x] Preview feasibility is classified per writer type.
- [x] ADR or explicitly named plan amendment records which writers can preview and which cannot.
- [x] No production preview interface is added without separate approval.

## Gate 5: Integration

- [x] Worker reports reviewed.
- [x] Diffs checked for ownership violations.
- [x] Lane-specific guard commands passed.
- [x] `make guard` passed.
- [x] Workflow `STATE.json`, `CHECKS/EVIDENCE.md`, and `HANDOFF.md` updated.
