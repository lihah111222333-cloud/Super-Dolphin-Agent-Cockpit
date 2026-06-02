# Review C R2: React Frontend Reality

## 1. Verdict

approve

## 2. High-confidence findings only

No required-change findings.

## 3. True positives

### TP-1: The plan correctly targets the React `frontend-app` surface and does not rely on the legacy Vue bridge

- Severity: positive / no change required
- Evidence:
  - `docs/cc/observability-tracing/00-implementation-plan.md:144-163` states Phase 1 targets `frontend-app/`, lists React facts, and warns not to assume the Vue `registerLogBridgeSink` path.
  - `frontend-app/package.json:14-20` depends on React, React DOM, Zustand, and Vite-era tooling.
  - `frontend-app/src/main.jsx:1-13` mounts `<App />` through `react-dom/client`.
- Why it matters: implementation agents should instrument the active React app files named by the plan, not cargo-cult the old Vue logging queue.
- Exact recommended doc change: none.

### TP-2: Existing W3C trace metadata and Wails parsing are described accurately

- Severity: positive / no change required
- Evidence:
  - `frontend-app/src/shared/api/wailsBridge.js:197-205` creates `traceId`, `spanId`, and W3C `traceparent`.
  - `frontend-app/src/shared/api/wailsBridge.js:289-297` injects `_aoClientKind`, `_aoClientRoute`, `_aoRequestId`, `_aoTraceparent`, `_aoTraceId`, and `_aoSpanId` into `callAPI()` payloads.
  - `internal/ui/wails/binding.go:199-222` parses `_aoTraceparent`, validates `_aoTraceId`/`_aoSpanId`, and attaches `trace_id`/`span_id` to the backend context.
- Why it matters: the plan can safely require new tracing work to reuse the existing RPC trace context rather than create a second unrelated trace id.
- Exact recommended doc change: none.

### TP-3: The plan correctly marks remote frontend flushing, patch timing, and render timing as new work

- Severity: positive / no change required
- Evidence:
  - `frontend-app/src/shared/api/wailsBridge.js:343-358` defines `sendFrontendLogBatch()` but only sends to existing `ui/log` when called.
  - References for `sendFrontendLogBatch()` are limited to import/re-export sites in `frontend-app/src/shared/api/backendApi.js:16` and `frontend-app/src/shared/api/backendApi.js:924`; no production React caller was found.
  - `frontend-app/src/entities/client/model/useClientStore.js:1880-2001` applies bridge patches without measuring duration.
  - `frontend-app/src/App.jsx` has no `Profiler`, `performance`, or `sendFrontendLogBatch` usage in the inspected relevant paths.
  - `docs/cc/observability-tracing/00-implementation-plan.md:156-162`, `351-359`, and `701-710` explicitly list those as not present / acceptance work.
- Why it matters: implementation agents will not mistake local bridge logs or exported helpers for completed observability ingestion.
- Exact recommended doc change: none.

## 4. Non-issues

- `frontend.rpc.start/done/failed` in the plan are acceptable as new trace events even though local debug bridge logs named `api.rpc.start/done/failed` already exist in `frontend-app/src/shared/api/wailsBridge.js:299-338`; the plan distinguishes remote/sanitized tracing from local logs.
- Listing `frontend-app/src/shared/api/backendApi.js` as a file to modify is reasonable because it currently only re-exports bridge helpers and RPC constants (`frontend-app/src/shared/api/backendApi.js:1-17`, `880-924`), so adding a strict observability ingest wrapper there is new work.
- The plan's warning not to reuse raw Vue log bridge behavior is supported; React currently registers bridge logs into Zustand only via `frontend-app/src/entities/client/model/useClientStore.js:3018-3023`.

## 5. Implementation-agent safety

The document is safe for implementation agents as-is for the reviewed React frontend scope. No proposed changes are required from this review.
