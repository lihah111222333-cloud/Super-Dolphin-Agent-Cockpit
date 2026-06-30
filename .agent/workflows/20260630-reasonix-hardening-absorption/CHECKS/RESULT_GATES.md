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

Status: passed after A3 round2, R1/R2 P1 repairs, final review2 dynamic disabled-tool repair, and LSP hint cleanup on code verification head `28aa3e54bcfa6b71bc5d83e859d96b3938c69016`.

- [x] A0 stage source inventory completed with LSP references and exact source anchors.
- [x] A1 `internal/platform/toolpolicy` tests passed.
- [x] A2 remains `not_applicable_with_evidence` because A0 found no valid stage source.
- [x] A3 post-review repairs `836705200f7b4a7eca05bb93925dde4fbb9124f8`, `5f7406d992b4d2dba19408d738799b298673009a`, `2803b0b5178959bc67bfac1951eb0da6b4f29099`, `6fdba6c1a2552dd0cf21d25b0dcf43d5130faa2c`, `d8754cc1e86bf3dfc62273390ebebf1f86b9b3fe`, and `dfeff4745e957ae6ee94d3f998f20c03f82ecd05` are integrated.
- [x] R2 P1 unknown non-empty Codex native tool IDs fail-fast across start/resume typed paths.
- [x] R1 P1 `launch_agent.read_only=true` is the structured read-only delegation flag; Plan/Explore compatibility remains, and ordinary workers are not read-only.
- [x] R3 passed with no additional code repair.
- [x] Final review2 recorded Mill PASS, Hume PASS, and Beauvoir FAIL P1; d8754cc1 fixed dynamic host/skill/MCP disabled-tool filtering and stale disabled scoped-call denial.
- [x] User-required six LSP hints were cleared: `range over int`, `strings.SplitSeq`, `strings.CutPrefix`, and `slices.ContainsFunc`/`slices.Contains`.
- [x] Final Lane A guard commands passed on code verification head `28aa3e54bcfa6b71bc5d83e859d96b3938c69016`.

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

Status: passed on code verification head `28aa3e54bcfa6b71bc5d83e859d96b3938c69016` after A3 round2, docs round2, R1/R2 P1 repairs, dynamic disabled-tool P1 repair, and LSP hint cleanup were merged.

- [x] A3 round2 worker commit `5f7406d992b4d2dba19408d738799b298673009a` was merged by `0b16e06f`.
- [x] Docs round2 commit `ade6151d28c4d6480029cfe087322ec2acb2aebf` was merged by historical docs merge `e17cb8b3`.
- [x] R2 P1 commit `2803b0b5178959bc67bfac1951eb0da6b4f29099` was merged by `ae857bba`.
- [x] R1 P1 commit `6fdba6c1a2552dd0cf21d25b0dcf43d5130faa2c` was merged by `73fe7f12`.
- [x] Dynamic disabled-tool P1 commit `d8754cc1e86bf3dfc62273390ebebf1f86b9b3fe` was merged by `d08c6962`.
- [x] LSP hint cleanup commit `dfeff4745e957ae6ee94d3f998f20c03f82ecd05` was merged by `28aa3e54`.
- [x] LSP diagnostics on the three cleanup files returned no diagnostics.
- [x] Focused Lane A, dynamic disabled-tool, `make guard`, JSON, and diff checks passed fresh on code verification head `28aa3e54`.
- [x] This docs-only status commit keeps workflow state synchronized after those gates.
- [x] Workflow `STATE.json`, `CHECKS/EVIDENCE.md`, `RESULT_GATES.md`, and `HANDOFF.md` updated to final integrated state.
