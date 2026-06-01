# Task 02 - JSONL Sink / Retention / Tail Reader

> **Agent 执行约束**：本任务只做本文件声明的范围。执行时使用 LSP 工具链定位/读取/修改代码；不要引入 PostgreSQL migration、sqlc 查询或 Phase 1 SQLite 依赖。完成后运行本文件列出的验证命令，并在交接中说明跳过项原因。
>
> **Source plan**：`docs/cc/observability-tracing/00-implementation-plan.md`

## DAG metadata

- **DAG node key**: `obs_02_jsonl_sink_retention_tail_reader`
- **Depends on**: `obs_01_core_schema_config_sanitizer`
- **Can run in parallel with**: `obs_03_bounded_index_query_service`
- **Primary owner**: backend/platform persistence agent

## Goal

实现 Phase 1 durable storage：append-only JSONL 文件、trace 专用目录、权限控制、轮转/保留策略、容错 tail reader。该任务不做 Dashboard RPC handler。

## Files

Create or complete:

```text
internal/platform/observability/sink.go
internal/platform/observability/jsonl_sink.go
internal/platform/observability/jsonl_reader.go
internal/platform/observability/jsonl_sink_test.go
internal/platform/observability/jsonl_reader_test.go
internal/platform/observability/retention_test.go
```

## Required design

- Trace JSONL path: `~/.multi-agent/log/<project>/traces/trace-YYYY-MM-DD.jsonl`.
- Create `traces/` from `internal/platform/observability`; do not use `pkg/logger.InitWithFile` for trace file creation.
- On Unix-like platforms, create directory with `0700` and trace files with `0600` where supported.
- Single process owns primary trace writes; sidecars relay instead of directly writing primary trace JSONL.
- Append-only writes, one JSON event per line, never rewrite live files to repair corruption.
- Rotate daily by default and by size when `OBS_JSONL_MAX_FILE_MB` is reached.
- Retention applies only to exact `trace-*.jsonl` files under the project `traces/` directory.
- Retention must never prune the parent project log directory.
- Reader tolerates malformed/partial trailing lines and reports decode-error metadata without failing the whole query.

## Implementation steps

1. Define `Sink` interface and sink error accounting.
2. Implement append-only JSONL writer with serialized writes.
3. Implement trace directory creation and permission checks.
4. Implement size/day rotation hooks.
5. Implement retention by max age and total bytes using validated bounds.
6. Implement bounded tail reader used by query fallback.
7. Add tests using temp dirs, malformed trailing lines, and retention fixtures.

## Validation

```bash
./scripts/test_with_guard.sh ./internal/platform/observability -run 'TestJSONL|TestTraceDirectory|TestRetention|TestTailReader' -count=1
```

## Acceptance

- JSONL writer appends valid single-line events.
- Directory/file permissions are owner-only where the platform supports them.
- Retention only deletes exact trace JSONL files inside `traces/`.
- Malformed final line does not hide earlier valid events.
- No PG/SQLite dependency is introduced.
