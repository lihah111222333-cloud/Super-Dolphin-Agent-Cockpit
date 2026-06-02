# Task 06 - React Frontend Trace Emitter / Patch and Render Timings

> **Agent 执行约束**：本任务只做本文件声明的范围。执行时使用 LSP 工具链定位/读取/修改代码；不要引入 PostgreSQL migration、sqlc 查询或 Phase 1 SQLite 依赖。完成后运行本文件列出的验证命令，并在交接中说明跳过项原因。
>
> **Source plan**：`docs/cc/observability-tracing/00-implementation-plan.md`

## DAG metadata

- **DAG node key**: `obs_06_react_frontend_trace_emitter`
- **Depends on**: `obs_04_fx_wiring_disabled_service` including its `observability/frontend/ingest` handler; preferably after `obs_05_wails_rpc_dispatch_instrumentation`
- **Can run in parallel with**: backend span tasks after ingest contract is stable
- **Primary owner**: React frontend agent

## Goal

在新 React 前端 `frontend-app/` 内实现 trace-worthy event 采集与安全远程 flush。不能假设旧 Vue log bridge 已经覆盖 React。

## Files

Modify:

```text
frontend-app/src/shared/api/wailsBridge.js
frontend-app/src/shared/api/backendApi.js
frontend-app/src/entities/client/model/useClientStore.js
frontend-app/src/App.jsx
frontend-app/src/main.jsx
```

Create or extend tests:

```text
frontend-app/src/shared/api/wailsBridge.test.js
frontend-app/src/shared/api/backendApi.test.js
frontend-app/src/entities/client/model/useClientStore.test.js
frontend-app/src/App.test.jsx
```

## Required design

- Reuse existing `createTraceContext()` in `wailsBridge.js`; do not create a second unrelated trace id for the same RPC.
- `callAPI()` should continue injecting `_aoTraceparent`, `_aoTraceId`, `_aoSpanId`, `_aoRequestId`, `_aoClientKind`, `_aoClientRoute`.
- Implement remote frontend trace flushing explicitly for React.
- Use the dedicated `observability/frontend/ingest` handler delivered by Task 04; do not depend on Task 10 for first ingest availability and do not reuse raw `ui/log`.
- Default remote flush only warn/error/slow trace-worthy events unless debug tracing is enabled.
- Trace flush failure is local-only and must not recursively enqueue remote logs.
- Emit: `frontend.rpc.start`, `frontend.rpc.done`, `frontend.rpc.failed`, `frontend.patch.apply.slow`.
- Emit `frontend.render.slow` only after stable React profiler or targeted timing path exists.
- Never persist `result_preview`, prompt text, user message text, file contents, tool results, or raw error stacks.

## Implementation steps

1. Inspect React frontend API/store entry points under `frontend-app/`.
2. Define frontend trace event allowlist and sanitizer before calling backend ingest.
3. Add RPC lifecycle event enqueue/flush around `callAPI()` using Task 04's `observability/frontend/ingest` contract.
4. Add slow patch timing around `applyBridgePatch`.
5. Add render slow timing only if a stable measurement point exists.
6. Add non-recursive flush failure handling.
7. Add frontend tests for trace context continuity and privacy.

## Validation

```bash
cd frontend-app
npm run test -- --run wailsBridge backendApi useClientStore
npm run build
```

## Acceptance

- React trace events share the same backend trace id.
- Remote flush is explicitly implemented in React, not inherited from Vue.
- Debug/info events stay local by default.
- Slow patch/render events are thresholded.
- Forbidden payload fields cannot enter trace JSONL through frontend ingest.
- Task 06 can be completed before Task 10 because the backend ingest RPC already exists from Task 04.
