# Phase 3 Adjudication

## Must Fix

- Add `pos` support to mutation and lifecycle tools.
- Keep legacy fields accepted.
- Reject conflicting `pos` and legacy selector fields.
- Add schema and handler tests.

## Runtime Node Selector Decision

`task_update_node` and `task_dispatch_node` currently require `run_id`, not `run_key`, at the service boundary. To avoid changing service contracts in M3, this phase introduces:

```text
dag:<dag_key>/run_id:<run_id>/node:<node_key>
```

This keeps M3 low risk and fully tools-layer scoped.

`dag:<dag_key>/run:<run_key>/node:<node_key>` can be considered later if a separate lookup from `run_key` to `run_id` is approved.

## Carry Forward

- Flattening `task_create_dag` and `task_dag_apply_ops`: M4.
- Output envelope normalization: M5.
- Automated multi-scorer execution harness: M6.
