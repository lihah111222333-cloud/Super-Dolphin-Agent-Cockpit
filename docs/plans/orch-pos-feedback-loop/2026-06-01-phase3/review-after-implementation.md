# Phase 3 Machine Review After Implementation

## Review A: Selector Contract

Result: Pass.

Evidence:

- `send_message` and `stop_agent` accept `pos=agent:<agent_id>`.
- DAG lifecycle tools accept:
  - `task_start_dag`: `pos=dag:<dag_key>`
  - `task_terminate_dag`: `pos=dag:<dag_key>/run:<run_key>`
  - `task_delete_dag`: `pos=dag:<dag_key>`
- Runtime node tools accept:
  - `pos=dag:<dag_key>/run_id:<run_id>/node:<node_key>`

## Review B: Compatibility

Result: Pass.

Evidence:

- Legacy fields remain decoded.
- Schema no longer requires legacy selector fields when `pos` is available.
- `pos_conflict` rejects mismatched `pos` and legacy values.

Implementation fix during review:

- `task_dispatch_node` originally translated downstream errors with raw legacy fields. The handler now parses into `contract.DispatchNodeRequest` first and passes the resolved request into error translation, so `pos`-only calls produce accurate error text.

## Review C: Verification

Result: Pass for M3 scope.

Evidence:

- `go test ./internal/sidecar/orch/tools -count=1` passes.
- New tests cover mutation schema exposure, handler mapping, runtime node `run_id` selector grammar, and conflict rejection.

Residual full-suite status:

- `go test ./cmd/mcp-orch/... -count=1` still fails outside the M3 tools-layer scope:
  - `TestMcpOrchSidecarRuntimeConsumesPackagedParentContract`
  - `TestMcpOrchSidecarRuntimeDevParentIgnoresResidualPackagedEnv`
  - `TestAutomationExecutor_Happy`
