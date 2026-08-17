# Task 10 - Dashboard Query UI / End-to-End Verification / Docs

> **Agent 执行约束**：本任务只做本文件声明的范围。执行时使用 LSP 工具链定位/读取/修改代码；不要引入 PostgreSQL migration、sqlc 查询或 Phase 1 SQLite 依赖。完成后运行本文件列出的验证命令，并在交接中说明跳过项原因。
>
> **Source plan**：`docs/cc/observability-tracing/00-implementation-plan.md`

## DAG metadata

- **DAG node key**: `obs_10_dashboard_query_ui_verification_docs`
- **Depends on**: `obs_01_core_schema_config_sanitizer`, `obs_02_jsonl_sink_retention_tail_reader`, `obs_03_bounded_index_query_service`, `obs_04_fx_wiring_disabled_service`, `obs_05_wails_rpc_dispatch_instrumentation`, `obs_06_react_frontend_trace_emitter`, `obs_07_thread_turn_spans`, `obs_08_provider_toolbridge_spans`, `obs_09_bus_uistate_spans`
- **Can run in parallel with**: none; this is the integration/verification closeout task.
- **Primary owner**: integration agent

## Goal

把 observability query RPC、React dashboard/debug UI、manual smoke 与文档收口，验证 agent 能通过 trace id 快速定位慢点、错误、代码锚点和相关调用栈/compact stack。

## Files

Create or complete:

```text
internal/module/observability/rpc.go
internal/module/observability/rpc_test.go
docs/cc/observability-tracing/README.md
docs/cc/observability-tracing/manual-smoke.md
```

Modify as needed:

```text
internal/app/modules.go
frontend-app/src/shared/api/backendApi.js
frontend-app/src/App.jsx
```

Update codemap only if repo process requires it after code changes.

## Required design

RPC methods:

```text
observability/trace/get       { traceId }
observability/thread/recent   { threadId, limit }
observability/slow/list       { limit, component }
observability/error/list      { limit, component }
observability/status          {}
observability/frontend/ingest { events }  // created in Task 04; Task 10 verifies and extends tests/docs only
```

Query response requirements:

- Show `Source`: `memory`, `jsonl_tail`, or `mixed`.
- Include trace events, slowest events, errors, total duration, and `truncated`.
- Missing trace returns empty non-error result with source metadata.
- Tail fallback scans only bounded recent JSONL tail with timeout/concurrency/cache limits.
- Dashboard must call observability service APIs, not PG tables.
- `observability/frontend/ingest` is not first created here; Task 10 verifies the Task 04 ingest contract, privacy tests, and UI/API integration.
- UI should expose trace id search plus slow/error lists sufficient for agent debugging.
- Code anchors should display file/function/line; click-to-open can be follow-up unless existing code-open RPC is safe and easy.

## Implementation steps

1. Complete query RPC handlers over platform observability service.
2. Verify and extend, but do not first introduce, the Task 04 `observability/frontend/ingest` handler.
3. Add React API wrapper methods for observability queries.
4. Add minimal Dashboard/debug UI for trace id, thread recent, slow list, error list, and status.
5. Ensure UI clearly labels source `memory`/`jsonl_tail`/`mixed` and truncation.
6. Write README explaining storage, config, privacy, and operational guardrails.
7. Write manual smoke covering one `turn/start` trace and JSONL/privacy checks.
8. Run backend, frontend, and manual verification.

## Validation

```bash
./scripts/test_with_guard.sh \
  ./internal/platform/observability \
  ./internal/module/observability \
  ./internal/app \
  ./internal/ui/wails \
  ./internal/platform/rpc \
  ./internal/module/thread \
  ./internal/module/turn \
  ./internal/platform/toolbridge \
  ./internal/platform/difftracker \
  ./internal/provider/... \
  ./internal/platform/bus \
  ./internal/module/uistate \
  -count=1
cd frontend-app
npm run test
npm run build
```

If `./internal/provider/...` is too broad or slow, rerun the exact touched provider packages from Task 08 and record the narrowed package list in the closeout report. Task 10 must still verify or cite successful validation for `internal/app`, `internal/platform/bus`, `internal/platform/toolbridge`, and `internal/platform/difftracker`.

Manual smoke checklist:

```text
1. Start app normally; tracing is enabled in safe mode by default. Set `OBS_TRACING_ENABLED=0` only when intentionally disabling tracing.
2. Send one message.
3. Locate ~/.super-dolphin/log/<project>/traces/trace-YYYY-MM-DD.jsonl.
4. Confirm trace directory/file permissions are owner-only where supported.
5. Confirm frontend/Wails/RPC/turn events share a trace id.
6. Query Dashboard by trace id.
7. Confirm code anchors are present.
8. Confirm no prompt/full payload appears in JSONL.
9. Confirm malformed copied JSONL fixture still returns valid earlier events.
10. Confirm status shows tracing enabled/disabled and sink/index counters.
```

## Acceptance

- Agent can start from a trace id and identify slowest span, component, code anchor, compact stack for slow/error, and correlated thread/agent/tool ids.
- UI and RPC do not depend on PG tables.
- No SQLite dependency is introduced in Phase 1.
- JSONL writes and query fallback remain bounded.
- README/manual smoke are accurate and executable.
