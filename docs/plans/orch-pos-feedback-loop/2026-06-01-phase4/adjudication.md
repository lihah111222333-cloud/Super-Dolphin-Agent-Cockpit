# Phase 4 Adjudication

## Must Fix

- Add flat schedule fields to `task_create_dag`.
- Add flat per-node execution shortcuts to `task_create_dag`.
- Add flat single-action path to `task_dag_apply_ops`.
- Preserve existing nested/raw inputs.
- Reject flat/nested conflicts instead of silently choosing one.
- Add tests for new flat paths and conflicts.

## Deliberate Constraints

- Do not change `nodeexec.Ops`.
- Do not invent unsupported fields for `NodeSpec`.
- Keep `raw ops` as the advanced fallback.
- Do not remove existing `nodes[].execution` or `schedule` inputs.

## Apply Ops Flat Action Decision

M4 supports one flat action per tool call:

```text
action=update_dag
action=add_node
action=update_node
action=remove_node
action=apply_ops_raw
```

Batch ops stay available through the existing `ops` raw array.
