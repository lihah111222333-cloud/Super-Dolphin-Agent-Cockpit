# Task 09 - Event Bus / UI State Projection Spans

> **Agent 执行约束**：本任务只做本文件声明的范围。执行时使用 LSP 工具链定位/读取/修改代码；不要引入 PostgreSQL migration、sqlc 查询或 Phase 1 SQLite 依赖。完成后运行本文件列出的验证命令，并在交接中说明跳过项原因。
>
> **Source plan**：`docs/cc/observability-tracing/00-implementation-plan.md`

## DAG metadata

- **DAG node key**: `obs_09_bus_uistate_spans`
- **Depends on**: `obs_04_fx_wiring_disabled_service`
- **Can run in parallel with**: `obs_08_provider_toolbridge_spans`
- **Primary owner**: backend UI-state agent

## Goal

给 event bus 和 UI state projection 添加低噪声 trace spans，帮助定位 UI 卡顿、patch 过慢、timeline append 异常，同时避免 token/output stream 造成日志爆炸。

## Files

Modify:

```text
internal/platform/bus/sink.go
internal/module/uistate/projector.go
internal/module/uistate/patch.go
internal/module/uistate/patch_timeline.go
internal/module/uistate/timeline/projector.go
```

Create or extend tests in touched packages.

## Required design

Emit events:

```text
bus.event.lifecycle
uistate.patch.emit
uistate.timeline.append
uistate.projection.updated
```

Rules:

- Extract structured identifiers only: thread, agent, turn, call, tool.
- Keep high-frequency events debug-only, sampled, or summarized.
- Token deltas, streaming output deltas, repeated heartbeat, and sidebar refresh probes are not detailed events by default.
- Prefer periodic summaries such as delta count and byte count.
- No user message text, model output, tool result, or patch payload body in trace JSONL.

## Implementation steps

1. Inspect bus sink and uistate projection flow.
2. Identify low-frequency lifecycle points and high-frequency paths.
3. Add sampled/summary trace events at safe boundaries.
4. Add slow patch/projection timing events with code anchors.
5. Add tests proving high-frequency streams are summarized.
6. Add privacy tests for UI patch/timeline payload exclusion.

## Validation

```bash
./scripts/test_with_guard.sh ./internal/platform/bus ./internal/module/uistate -count=1
```

## Acceptance

- Lifecycle events are searchable by thread/agent/turn/tool ids.
- High-frequency paths cannot generate unbounded trace volume.
- UI-state slow/error events identify code anchors.
- Payload bodies are not persisted.
