# Phase 1 Rescore

## Scores

| Scorer | Before | After | Delta |
| --- | ---: | ---: | ---: |
| Input contract | 68 | 88 | +20 |
| Compatibility | 74 | 90 | +16 |
| Verification | 70 | 83 | +13 |
| Average | 70.7 | 87.0 | +16.3 |

## Calibration

The score improves because read-only tools now have one preferred selector surface: `pos`.

The score is not 95+ because Phase 1 intentionally did not finish:

- mutation tool selectors,
- complex DAG creation and ops flattening,
- output envelope normalization,
- fully automated multi-agent scoring.

## Next Round Inputs

The next round must start from `issues-ledger.json` and handle:

1. M3 mutation tool `pos` support.
2. M4 flattened primary inputs for `task_create_dag` and `task_dag_apply_ops`.
3. A stricter pre-implementation scoring gate so the evidence chain exists before code changes.
