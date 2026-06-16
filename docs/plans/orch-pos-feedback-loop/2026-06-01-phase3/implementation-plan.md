# Phase 3 Implementation Plan

## Goal

Make mutation and lifecycle tools accept the same `pos` selector style added to read tools in Phase 1.

## Implementation Steps

1. Extend `pos` grammar with runtime-node `run_id` support:
   - `dag:<dag_key>/run_id:<run_id>/node:<node_key>`
2. Add resolver helpers for:
   - `node_key`
   - `run_id`
3. Add `pos` fields to mutation input structs.
4. Update handlers/request builders:
   - `send_message`: resolve agent from `pos`.
   - `stop_agent`: resolve agent from `pos`.
   - `task_start_dag`: resolve DAG from `pos`.
   - `task_terminate_dag`: resolve DAG and run from `pos`.
   - `task_delete_dag`: resolve DAG from `pos`.
   - `task_update_node`: resolve DAG, node, and run_id from `pos`.
   - `task_dispatch_node`: resolve DAG, node, and run_id from `pos`.
5. Update schemas:
   - expose `pos`,
   - remove legacy selector fields from required lists,
   - keep non-selector business fields required.
6. Add tests:
   - schema exposes `pos`,
   - handlers accept `pos`,
   - conflicts reject with `pos_conflict`.
7. Run:
   - `go test ./internal/sidecar/orch/tools -count=1`
   - `go test ./cmd/mcp-orch/... -count=1` for residual awareness.

## Constraints

- Do not change orchestration service interfaces.
- Do not remove legacy fields.
- Keep M4 complex input flattening out of this phase.
