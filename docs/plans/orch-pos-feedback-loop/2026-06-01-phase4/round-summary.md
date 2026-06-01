# mcp-orch pos Phase 4 Feedback Loop

Date: 2026-06-01

## Scope

Phase 4 reduces AI input complexity for the two remaining high-friction DAG tools:

- `task_create_dag`
- `task_dag_apply_ops`

## Boundary

This phase keeps existing raw/nested inputs compatible. It adds flatter primary paths instead of removing existing paths.

## Required Workflow

1. Machine scoring.
2. Review and adjudication.
3. Implementation plan.
4. Implementation.
5. Machine review after implementation.
6. Fix.
7. Re-score calibration.
8. Carry unresolved gaps forward.
