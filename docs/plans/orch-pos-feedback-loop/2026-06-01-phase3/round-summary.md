# mcp-orch pos Phase 3 Feedback Loop

Date: 2026-06-01

## Scope

Phase 3 extends the Phase 1 `pos` selector contract from read-only tools to mutation and lifecycle tools.

## Target Tools

- `send_message`
- `stop_agent`
- `task_update_node`
- `task_dispatch_node`
- `task_start_dag`
- `task_terminate_dag`
- `task_delete_dag`

## Required Workflow

This phase must execute in this order:

1. Machine scoring.
2. Review and adjudication.
3. Implementation plan.
4. Implementation.
5. Machine review after implementation.
6. Fix.
7. Re-score calibration.
8. Carry unresolved gaps to the next round.

## Phase Boundary

This phase does not flatten `task_create_dag` or `task_dag_apply_ops`; those remain M4.
