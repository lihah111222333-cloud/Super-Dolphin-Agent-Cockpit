# Task 03 - Bounded In-Memory Index / Query Service / Sampler

> **Agent 执行约束**：本任务只做本文件声明的范围。执行时使用 LSP 工具链定位/读取/修改代码；不要引入 PostgreSQL migration、sqlc 查询或 Phase 1 SQLite 依赖。完成后运行本文件列出的验证命令，并在交接中说明跳过项原因。
>
> **Source plan**：`docs/cc/observability-tracing/00-implementation-plan.md`

## DAG metadata

- **DAG node key**: `obs_03_bounded_index_query_service`
- **Depends on**: `obs_01_core_schema_config_sanitizer`
- **Can run in parallel with**: `obs_02_jsonl_sink_retention_tail_reader` after interface alignment.
- **Primary owner**: backend/platform query agent

## Goal

实现 bounded memory index、sampler、service facade 与查询结果模型，使 agent/UI 能按 trace、thread、slow、error 快速定位问题代码区域，同时保证内存上限可证明。

## Files

Create or complete:

```text
internal/platform/observability/index.go
internal/platform/observability/service.go
internal/platform/observability/sampler.go
internal/platform/observability/index_test.go
internal/platform/observability/service_test.go
internal/platform/observability/sampler_test.go
```

## Required design

- Index 是 cache，不是 durable source of truth。
- Default caps: global 5000, per-trace 128, per-thread 256, slow 500, error 500.
- Debug caps: global 20000, per-trace 256, per-thread 512.
- Global ring evicts oldest events; secondary indexes remove old references or safely filter stale references.
- Never keep unbounded maps for old trace IDs or thread IDs.
- Sampler always keeps errors/panics/slow lifecycle events; high-frequency UI/state events are sampled or summary-only.
- Service must sanitize before indexing and before passing to sink.
- Query result `Source` values: `memory`, `jsonl_tail`, `mixed`.
- Tail fallback must enforce size, timeout, singleflight/cache, and concurrency caps once Task 02 is available.

## Implementation steps

1. Implement global ring and secondary index rings.
2. Implement slow/error capped indexes.
3. Implement stale-reference-safe lookup.
4. Implement sampler decisions and dropped summary accounting.
5. Implement service `Record`/`Query` facade over sampler, sanitizer, index, and sink.
6. Implement query fallback contract to JSONL tail reader without unbounded scans.
7. Add tests for eviction, stale references, sampling, truncation, and source metadata.

## Validation

```bash
./scripts/test_with_guard.sh ./internal/platform/observability -run 'TestIndex|TestService|TestSampler' -count=1
```

## Acceptance

- Memory use is bounded by config and tests prove eviction.
- Query by trace/thread/slow/error works from memory.
- Missing trace returns empty non-error result with source metadata.
- No UI query path can trigger unbounded historical file scans.
- Sanitizer is in the event write path, not just query path.
