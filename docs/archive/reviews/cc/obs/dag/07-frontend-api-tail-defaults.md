# DAG Task 07 - Frontend/API Tail Default Guard

Worker: W4 (`work/obs-tail-api-docs`)

Depends on: none

Purpose: lock the current UI/API reachability evidence: observability page calls trace/recent APIs without forcing `includeTail:false`.

Files:

- Modify: `frontend-app/src/pages/observability/ObservabilityPage.test.jsx`
- Modify: `frontend-app/src/shared/api/backendApi.test.js` only if needed

Steps:

1. Add or tighten a page test asserting trace drilldown calls `getObservabilityTrace({ traceId, limit })` without `includeTail:false`.
2. Add or tighten a page test asserting recent search calls `listObservabilityRecent(...)` without `includeTail:false`.
3. If backend API tests already cover payload shape, avoid redundant assertions.

Verification:

```bash
cd frontend-app
npm test -- ObservabilityPage.test.jsx backendApi.test.js
```

Expected: PASS.

Constraints:

- Do not add UI controls.
- Do not change runtime behavior unless the existing tests reveal the report evidence is stale.
