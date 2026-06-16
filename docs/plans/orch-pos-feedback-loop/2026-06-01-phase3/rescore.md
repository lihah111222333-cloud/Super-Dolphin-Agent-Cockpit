# Phase 3 Rescore

## Scores

| Scorer | Before | After | Delta |
| --- | ---: | ---: | ---: |
| Mutation selector | 52 | 88 | +36 |
| Compatibility | 76 | 91 | +15 |
| Verification | 58 | 86 | +28 |
| Average | 62.0 | 88.3 | +26.3 |

## Calibration

The score improved because mutation and lifecycle tools now share the same preferred selector pattern as Phase 1 read tools.

The score is not 95+ because:

- M4 complex input flattening is still open.
- M5 output envelope normalization is still open.
- Full `cmd/mcp-orch/...` remains red because of unrelated failures outside the M3 touched surface.

## Verification Commands

Focused M3 verification:

```powershell
go test ./internal/sidecar/orch/tools -count=1
```

Result: pass.

Residual awareness:

```powershell
go test ./cmd/mcp-orch/... -count=1
```

Result: fail in pre-existing non-tools areas recorded in `issues-ledger.json`.

## Next Round Input

M4 must start from `M4-001`: flatten `task_create_dag` and `task_dag_apply_ops` primary inputs while preserving raw advanced paths.
