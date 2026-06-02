# Task 05 - Wails / RPC Dispatch Instrumentation

> **Agent 执行约束**：本任务只做本文件声明的范围。执行时使用 LSP 工具链定位/读取/修改代码；不要引入 PostgreSQL migration、sqlc 查询或 Phase 1 SQLite 依赖。完成后运行本文件列出的验证命令，并在交接中说明跳过项原因。
>
> **Source plan**：`docs/cc/observability-tracing/00-implementation-plan.md`

## DAG metadata

- **DAG node key**: `obs_05_wails_rpc_dispatch_instrumentation`
- **Depends on**: `obs_04_fx_wiring_disabled_service`
- **Can run in parallel with**: `obs_07_thread_turn_spans` after shared trace context helpers are stable
- **Primary owner**: backend RPC boundary agent

## Goal

在 React -> Wails -> backend RPC 的核心边界打点，确保前端 W3C trace context 进入后端，RPC dispatch 产生 start/done/failed 事件，并且不泄露 params 或响应预览。

## Files

Modify:

```text
internal/ui/wails/binding.go
internal/ui/wails/rpc.go
internal/platform/rpc/server.go
internal/platform/rpc/handler.go
pkg/logger/fields.go
```

Create or extend tests:

```text
internal/ui/wails/binding_id_test.go
internal/platform/rpc/server_trace_privacy_test.go
```

## Required design

- Continue parsing `_aoTraceparent` in `frontendTraceContext`.
- Strip frontend trace metadata before strict RPC handlers see application params.
- Emit Wails events: `wails.call_api.start`, `wails.call_api.done`, `wails.call_api.failed`, `frontend.log.ingested`.
- Emit RPC events: `backend.rpc.dispatch.start`, `backend.rpc.dispatch.done`, `backend.rpc.dispatch.failed`.
- Include method, trace id, span id, parent span id, duration, status, and code anchor.
- Do not include full raw params.
- Trace events must not reuse `rpcParamPreview`, `ParamsPreview`, `params_preview`, or any raw params preview field from existing request tracking.
- Metadata allowed: sanitized method, param keys, param bytes, correlation IDs, duration/status, code anchor.
- Slow thresholds: UI state 300ms, turn start 1000ms, default 500ms.

## Implementation steps

1. Inspect existing Wails traceparent parsing and tests.
2. Inject observability service into Wails/RPC boundary without creating import cycles.
3. Emit Wails CallAPI lifecycle events.
4. Emit RPC dispatch lifecycle events at central `Server.Dispatch` boundary.
5. Add privacy regression test with params containing prompt/user text.
6. Assert JSONL/event payload contains neither prompt text nor `params_preview` fields.

## Validation

```bash
./scripts/test_with_guard.sh ./internal/ui/wails ./internal/platform/rpc -count=1
```

## Acceptance

- Existing traceparent tests still pass.
- RPC events correlate with frontend trace ids.
- Failed RPC does not persist prompt/user text or raw params preview.
- Slow/error RPC events contain code anchors for quick LSP navigation.
- Handler-level tracing does not double-count central dispatch events.
