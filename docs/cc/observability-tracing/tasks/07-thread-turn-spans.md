# Task 07 - Thread / Turn Lifecycle Spans

> **Agent 执行约束**：本任务只做本文件声明的范围。执行时使用 LSP 工具链定位/读取/修改代码；不要引入 PostgreSQL migration、sqlc 查询或 Phase 1 SQLite 依赖。完成后运行本文件列出的验证命令，并在交接中说明跳过项原因。
>
> **Source plan**：`docs/cc/observability-tracing/00-implementation-plan.md`

## DAG metadata

- **DAG node key**: `obs_07_thread_turn_spans`
- **Depends on**: `obs_04_fx_wiring_disabled_service`; recommended after `obs_05_wails_rpc_dispatch_instrumentation`
- **Can run in parallel with**: `obs_08_provider_toolbridge_spans` after span naming contract is agreed
- **Primary owner**: backend thread/turn agent

## Goal

把 thread/turn 的关键耗时阶段串进同一 trace，让 agent 能从 trace id 直接定位到 `thread/start`、`turn/start`、prompt assembly、watch completion 等代码区域。

## Files

Modify:

```text
internal/module/thread/rpc.go
internal/module/thread/start_session.go
internal/module/thread/start_session_helpers.go
internal/module/turn/rpc_helpers.go
internal/module/turn/service.go
internal/module/turn/prompt_assembly.go
internal/module/turn/manifest.go
internal/module/turn/tracker.go
```

Create or extend same-package tests for traced lifecycle behavior.

## Required design

Emit events:

```text
thread.start
thread.spawn_if_needed
turn.ready_wait
turn.prepare
turn.assembly
turn.start
turn.watch.completed
turn.interrupt
```

Rules:

- Focus on stage duration, status, ids, and code anchors.
- Do not log assembled prompts, memory contents, or user message text.
- For `turn.prepare`, record counts only: input item count, file count, image count, skill count, manifest tool count.
- Preserve existing fail-fast behavior; tracing failure must not mask application errors unless startup construction failed.
- Slow/error/panic paths may include compact stack according to global stack policy.

## Implementation steps

1. Inspect thread/turn call hierarchy and existing tests.
2. Add trace context propagation through thread and turn service boundaries.
3. Emit begin/done/error events around ready wait, prepare, assembly, start, watch, interrupt.
4. Add code anchors for each span.
5. Add privacy tests proving prompt/memory contents are absent.
6. Add duration/status tests for happy and error paths.

## Validation

```bash
./scripts/test_with_guard.sh ./internal/module/thread ./internal/module/turn -count=1
```

## Acceptance

- A `turn/start` trace shows ready wait, prepare, assembly/start, and watch completion where applicable.
- Events carry thread/agent/turn ids needed for dashboard filtering.
- Prompt and memory payloads are never persisted.
- Slow/error turn stages point at file/function/line anchors.
