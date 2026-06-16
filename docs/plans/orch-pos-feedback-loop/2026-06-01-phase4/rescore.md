# Phase 4 Rescore

## Scores

| Scorer | Before | After | Delta |
| --- | ---: | ---: | ---: |
| Complex input | 48 | 86 | +38 |
| Compatibility | 72 | 88 | +16 |
| Verification | 56 | 85 | +29 |
| Average | 58.7 | 86.3 | +27.6 |

## Calibration

The score improves because the two highest-friction DAG tools now have flatter primary paths:

- `task_create_dag` can avoid nested `schedule` and `execution` for common cases.
- `task_dag_apply_ops` can avoid hand-building an `ops` array for one common action.

The score is not 95+ because:

- Output envelopes and next hints are still not normalized across orch tools.
- The feedback-loop process is still manual.
- Full `cmd/mcp-orch/...` remains red for unrelated failures.

## Verification Commands

Focused M4 verification:

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

M5 must start from `M5-001`: normalize output envelopes and actionable hints for orch tools where the response shape can be changed safely.
