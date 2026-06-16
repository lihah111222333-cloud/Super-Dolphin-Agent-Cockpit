# Task 01 - Observability Core Schema / Config / Sanitizer

> **Agent 执行约束**：本任务只做本文件声明的范围。执行时使用 LSP 工具链定位/读取/修改代码；不要引入 PostgreSQL migration、sqlc 查询或 Phase 1 SQLite 依赖。完成后运行本文件列出的验证命令，并在交接中说明跳过项原因。
>
> **Source plan**：`docs/cc/observability-tracing/00-implementation-plan.md`

## DAG metadata

- **DAG node key**: `obs_01_core_schema_config_sanitizer`
- **Depends on**: none
- **Can run in parallel with**: documentation-only review; code implementation tasks must depend on this schema.
- **Primary owner**: backend/platform observability agent

## Goal

建立 Phase 1 链路追踪的最小可信核心：事件模型、trace/span context、配置校验、统一 sanitizer、代码锚点与 compact stack 策略。该任务不写 RPC handler、不接 UI、不落盘 JSONL writer。

## Files

Create:

```text
internal/platform/observability/event.go
internal/platform/observability/context.go
internal/platform/observability/config.go
internal/platform/observability/sanitizer.go
internal/platform/observability/code_anchor.go
internal/platform/observability/stack.go
```

Create tests:

```text
internal/platform/observability/config_test.go
internal/platform/observability/sanitizer_test.go
internal/platform/observability/code_anchor_test.go
internal/platform/observability/stack_test.go
```

## Required design

- Define `TraceEvent`, `CodeAnchor`, `StackFrame`, status constants, and `schema_version=1`.
- Status values: `ok | slow | error | panic | sampled | dropped_summary`.
- Config parser covers all `OBS_*` knobs from the source plan with documented min/max bounds.
- If tracing is enabled, invalid zero/negative/extreme config must fail startup; do not silently clamp.
- Sanitizer applies before both in-memory indexing and JSONL write.
- Sanitizer covers every string field: errors, method, route, tool name, metadata strings, code anchors, stack frames.
- Metadata Phase 1 allowed values only: `string`, `bool`, finite numbers, `[]string`, `[]int64`, shallow `map[string]string`.
- Static line numbers are best-effort; file+function is the stable anchor. Tests should guard anchor construction shape.
- Stack capture is compact and only intended for slow/error/panic paths.

## Implementation steps

1. Add event model and constants.
2. Add context helpers for trace id/span id propagation without coupling to Wails or RPC packages.
3. Add strict config parser with safe defaults and explicit disabled/enabled states.
4. Add sanitizer with byte caps, multiline normalization, secret-pattern redaction, and metadata shaping.
5. Add code anchor helper constructors.
6. Add compact stack capture with frame count and byte limits.
7. Add unit tests for all unsafe inputs and boundary values.

## Validation

```bash
./scripts/test_with_guard.sh ./internal/platform/observability -run 'TestConfig|TestSanitizer|TestCodeAnchor|TestStack' -count=1
```

## Acceptance

- Tracing-enabled unsafe config fails fast.
- Disabled config is explicit and inspectable.
- Sanitizer redacts/truncates all persisted strings, not only metadata.
- No PostgreSQL, sqlc, or SQLite imports are introduced.
- Tests prove secret-like values, oversized strings, malformed metadata, and stack limits are handled.
