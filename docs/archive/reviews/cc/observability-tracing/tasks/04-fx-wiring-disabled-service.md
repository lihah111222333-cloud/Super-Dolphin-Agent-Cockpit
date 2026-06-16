# Task 04 - Fx Wiring / Disabled Service / Observability RPC Module Shell

> **Agent 执行约束**：本任务只做本文件声明的范围。执行时使用 LSP 工具链定位/读取/修改代码；不要引入 PostgreSQL migration、sqlc 查询或 Phase 1 SQLite 依赖。完成后运行本文件列出的验证命令，并在交接中说明跳过项原因。
>
> **Source plan**：`docs/cc/observability-tracing/00-implementation-plan.md`

## DAG metadata

- **DAG node key**: `obs_04_fx_wiring_disabled_service`
- **Depends on**: `obs_01_core_schema_config_sanitizer`, `obs_02_jsonl_sink_retention_tail_reader`, `obs_03_bounded_index_query_service`
- **Can run in parallel with**: none until core interfaces settle
- **Primary owner**: backend/app wiring agent

## Goal

把 observability platform service 和 application RPC module 显式接入 Fx app graph，同时支持 enabled fail-fast、disabled explicit service、最小 status RPC，以及供 React Task 06 使用的安全 frontend ingest RPC。该任务不实现完整 Dashboard UI。

## Files

Create:

```text
internal/platform/observability/module.go
internal/module/observability/module.go
internal/module/observability/rpc.go
internal/module/observability/rpc_test.go
```

Modify:

```text
internal/app/modules.go
```

## Required design

- `internal/platform/observability.Module` 只提供 service/sink/index/config，不注册 application RPC handlers。
- `internal/module/observability.Module` 注册 `observability/*` handlers。
- `internal/module/observability` must return `rpc.HandlerMapResult` so `rpc.registerAllHandlers` sees handlers through `group:"rpc_handlers"`.
- Task 04 owns the first backend handler for `observability/frontend/ingest { events }`; React Task 06 depends on this handler for remote flush and must not wait until Task 10 for ingest availability.
- `observability/frontend/ingest` must accept only allowlisted frontend trace fields, sanitize before recording, reject/trim oversized batches, and avoid raw `ui/log` passthrough semantics.
- `internal/app/modules.go` must explicitly add both modules; do not assume auto-discovery.
- Enabled tracing: invalid config, directory creation failure, sink construction failure, or Fx wiring failure must fail startup.
- Disabled tracing: bind explicit disabled service; status explains tracing is off. Do not rely on nil checks.
- No PG/sqlc/SQLite imports.

## Implementation steps

1. Add platform Fx module with config/service providers.
2. Add disabled service implementation or explicit disabled mode in service.
3. Add application observability module with minimal `observability/status {}` and `observability/frontend/ingest { events }`.
4. Wire both modules in `internal/app/modules.go`.
5. Add tests proving handlers are registered and disabled/enabled modes behave correctly.
6. Add frontend ingest tests for allowlist, sanitizer, oversized batch handling, and disabled-service behavior.
7. Add failure-path tests for invalid enabled config.

## Validation

```bash
./scripts/test_with_guard.sh ./internal/platform/observability ./internal/module/observability ./internal/app -count=1
```

## Acceptance

- Both Fx modules are wired explicitly.
- `observability/status {}` and `observability/frontend/ingest { events }` are registered through `rpc.HandlerMapResult`.
- Frontend ingest is available before Task 06 and records only sanitized allowlisted events.
- Enabled tracing fails fast on invalid construction/config.
- Disabled tracing is explicit and queryable.
- No persistence schema or DB coupling is added.
