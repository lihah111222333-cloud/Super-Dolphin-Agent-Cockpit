# Phase 4 Implementation Plan

## Goal

Reduce cognitive load for complex DAG inputs without breaking existing nested/raw callers.

## task_create_dag

Add top-level schedule shortcuts:

- `trigger`
- `default_retry`
- `default_timeout_sec`
- `fail_fast`
- `max_concurrency`
- `queue_policy`

Add node-level execution shortcuts:

- `on_failure`
- `pool`
- `priority`
- `retry`
- `timeout_sec`

Rules:

- Existing `schedule` and `nodes[].execution` remain supported.
- If flat and nested fields both set different values, return a conflict error.
- If they match, accept.

## task_dag_apply_ops

Add flat single-action fields:

- `pos` / `dag_key`
- `base_version`
- `action`
- `node_key`
- `title`
- `node_type`
- `depends_on`
- `assigned_to`
- `config`
- `patch`

Rules:

- `action=apply_ops_raw` or empty `action` with `ops` keeps the legacy raw path.
- `action=add_node` builds one typed `add_node` op.
- `action=update_node` builds one typed `update_node` op.
- `action=remove_node` builds one typed `remove_node` op.
- `action=update_dag` builds one typed `update_dag` op.
- Existing `ops` remains accepted and is not removed.

## Verification

- Add focused tests in `internal/sidecar/orch/tools`.
- Run `go test ./internal/sidecar/orch/tools -count=1`.
- Run `go test ./cmd/mcp-orch/... -count=1` for residual awareness.
