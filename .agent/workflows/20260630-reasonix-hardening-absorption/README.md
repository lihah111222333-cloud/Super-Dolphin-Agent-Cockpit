---
description: Orchestrate approved execution lanes for the June 30 Reasonix hardening absorption review.
workflow_key: 20260630-reasonix-hardening-absorption
---

# Reasonix Hardening Absorption Orchestration

## 1. Goal

- Business goal: turn the docs-only Reasonix hardening review into a controlled, auditable execution workflow.
- Technical goal: split the approved absorption work into lane-sized tasks with clear ownership, dependencies, and verification gates.
- Acceptance goal: no production lane starts until approval is explicit; each started lane produces tests, source proof, and guard evidence before integration.

## 2. Source Plan

- Source plan: `docs/plans/2026-06-30-reasonix-hardening-absorption-review.md`
- Packaged snapshot for isolated workers: `SOURCE_PLAN_SNAPSHOT.md`
- Source status: `NEEDS_APPROVAL`
- Execution flag: `plan_executable=false`
- Current workflow status: approved and in progress; Lane A is executing first through isolated worker worktrees.

## 3. Scope

- In scope after approval: Lane A tool policy hardening, Lane B session path helper extraction, Lane C writer preview inventory spike.
- Out of scope: provider wire-normalization, schema canonical-cache registry, Reasonix global registry, blank imports, event bus, workers/site/accounts shells.
- Production code changes are accepted only from child-agent worktrees and are reviewed before integration.

## 4. Execution Topology

- Serial control path: `P0-orchestration` -> `A0-stage-source-inventory` -> Lane A implementation -> Lane B implementation -> Lane C spike -> `PN-integration`.
- Lane B and Lane C stay behind Lane A in the DAG; A0 may record anchors that later workers reuse, but no B/C worker should start until its dependency is done.
- Lane A is the first implementation lane because it carries the highest safety value and defines the stage/trust language later lanes must not bypass.

## 5. DAG

See `DAG.json` for machine-readable state and `TASKS/` for task cards.

## 6. Task State Semantics

- `done`: task completed and its verification evidence is recorded.
- `not_applicable_with_evidence`: task has no valid runtime target after A0 evidence review; it satisfies DAG dependencies but must not produce production code.
- `ready`: task is approved, its dependencies are satisfied, and it is waiting for worker dispatch.
- `waiting`: task is approved but still depends on an unfinished upstream task.
- `blocked`: task cannot satisfy its dependency or ownership contract; downstream tasks must not start.
- `needs_approval`: task is not authorized to start.

Only `done` and `not_applicable_with_evidence` are terminal states that may satisfy downstream DAG dependencies.

Worker report statuses map into DAG states as follows: `DONE` -> `done`, `NOT_APPLICABLE_WITH_EVIDENCE` -> `not_applicable_with_evidence`, and `BLOCKED` -> `blocked`. `DONE_WITH_CONCERNS` and `NEEDS_CONTEXT` require orchestrator review before any DAG state update.

## 7. Current Status

- Overall: in progress after explicit user approval for the full workflow.
- Completed: P0 workflow structure, A0 stage-source inventory, A1 toolpolicy core, and A3 read-only delegation filter.
- Closed as not applicable: A2 runtime stage gate, because A0 found no authoritative stage source.
- Lane A gates passed.
- Next step: dispatch B1 in an isolated worker worktree.

## 8. Quick Navigation

- Context: `CONTEXT.md`
- Ownership: `FILE_OWNERSHIP.tsv`
- Risks: `RISK_REGISTER.md`
- Gates: `CHECKS/RESULT_GATES.md`
- Evidence: `CHECKS/EVIDENCE.md`
- Handoff: `HANDOFF.md`
- Source snapshot: `SOURCE_PLAN_SNAPSHOT.md`
- Worker prompt shell: `WORKER_PROMPT.md`
