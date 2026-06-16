# Phase 4 Machine Review After Implementation

## Review A: Complex Input Contract

Result: Pass.

Evidence:

- `task_create_dag` now accepts flat schedule fields:
  - `trigger`
  - `default_retry`
  - `default_timeout_sec`
  - `fail_fast`
  - `max_concurrency`
  - `queue_policy`
- `task_create_dag` nodes now accept flat execution fields:
  - `on_failure`
  - `pool`
  - `priority`
  - `retry`
  - `timeout_sec`
- `task_dag_apply_ops` now accepts flat single-action fields through `action`.

## Review B: Compatibility

Result: Pass.

Evidence:

- Existing `schedule`, `nodes[].execution`, and raw `ops` remain supported.
- `task_create_dag` rejects flat/nested schedule conflicts.
- `task_dag_apply_ops` rejects combining raw `patch` with flat patch fields.
- `task_dag_apply_ops` keeps `action=apply_ops_raw` and omitted `action` raw paths.

Implementation fix during review:

- `depends_on: []` initially serialized to `null` through the shared trimming helper. This broke the typed ops “clear dependencies” semantics. The implementation now preserves explicit empty arrays while still trimming non-empty entries.

## Review C: Verification

Result: Pass for M4 scope.

Evidence:

- `go test ./internal/sidecar/orch/tools -count=1` passes.
- Tests cover flat schedule/node execution, conflict handling, flat apply_ops add/update actions, and raw patch conflict.

Residual full-suite status:

- `go test ./cmd/mcp-orch/... -count=1` still fails outside the M4 tools-layer scope:
  - `TestMcpOrchSidecarRuntimeConsumesPackagedParentContract`
  - `TestMcpOrchSidecarRuntimeDevParentIgnoresResidualPackagedEnv`
  - `TestAutomationExecutor_Happy`
