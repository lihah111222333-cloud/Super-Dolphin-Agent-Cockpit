# Workflow DAG response validation

## Scope

Fix a fail-fast gap in the frontend backend API facade for workflow DAG start mutations.

## Evidence

- `frontend-app/src/shared/api/backendApi.js` validates several lifecycle responses, but `dashboard/dagStart` and `dashboard/dagCreateAndStart` are not registered in `BACKEND_RESPONSE_VALIDATORS`.
- `frontend-app/src/shared/api/backendApi.contractMatrix.js` marks both methods as P0 DAG mutations without response validator metadata.
- `frontend-app/src/pages/workflows/WorkflowPage.jsx` reads `run_key` / `runKey` from the response and then shows a success notice. A malformed success envelope can therefore look like a started workflow while no run can be selected.

## Plan

1. Add RED coverage in `backendApi.test.js` for malformed DAG start and create-and-start responses.
2. Add response validators requiring:
   - `dashboard/dagStart`: object response with `runKey` or `run_key`.
   - `dashboard/dagCreateAndStart`: object response with `dagKey` or `dag_key`, and `runKey` or `run_key`.
3. Register validator metadata in `backendApi.contractMatrix.js` and lock it in the matrix test.
4. Run focused tests first, then frontend lint, full tests, build, diff check, and diagnostics where available.

## Validation

```bash
cd frontend-app
npm test -- backendApi.test.js -t "fails fast on malformed guarded backend responses"
npm test -- backendApi.contractMatrix.test.js
npm run lint
npm test
npm run build
```
