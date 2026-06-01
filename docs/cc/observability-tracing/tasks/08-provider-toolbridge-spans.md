# Task 08 - Provider / Tool Bridge / Diff Tracker Spans

> **Agent 执行约束**：本任务只做本文件声明的范围。执行时使用 LSP 工具链定位/读取/修改代码；不要引入 PostgreSQL migration、sqlc 查询或 Phase 1 SQLite 依赖。完成后运行本文件列出的验证命令，并在交接中说明跳过项原因。
>
> **Source plan**：`docs/cc/observability-tracing/00-implementation-plan.md`

## DAG metadata

- **DAG node key**: `obs_08_provider_toolbridge_spans`
- **Depends on**: `obs_04_fx_wiring_disabled_service`; recommended after `obs_07_thread_turn_spans`
- **Can run in parallel with**: `obs_09_bus_uistate_spans`
- **Primary owner**: provider/tooling agent

## Goal

补齐 provider session、provider turn、tool call、diff emission 的 trace spans，让慢调用或工具错误能直接定位到 provider/toolbridge/difftracker 代码区域。

## Files

Modify selected files under:

```text
internal/provider/codexapp/**
internal/provider/claudecli/**
internal/provider/unified/**
internal/platform/toolbridge/**
internal/platform/difftracker/**
```

Create or extend package tests around provider/toolbridge trace events.

## Required design

Emit events:

```text
provider.session.acquire
provider.session.ready
provider.turn.run
tool.call.begin
tool.call.end
tool.diff.emit
```

Rules:

- Keep provider-specific code anchors.
- Tool results are summarized only: success, elapsed, result bytes, truncated flag, affected files count.
- Never persist full tool result, model output, file contents, prompt text, or environment dumps.
- Capture compact stack only for slow/error tool paths.
- Use sampling for normal high-volume successful tool calls if required by global sampler.
- Preserve support for both Codex and Claude providers in code, but if subagents are used for implementation, launch them with `codex` provider only.

## Implementation steps

1. Map provider session acquire/run boundaries and toolbridge call boundaries with LSP xref.
2. Add trace propagation from turn context into provider/tool calls.
3. Emit provider session and turn events.
4. Emit tool begin/end with summary-only metadata.
5. Emit diff tracker summary events.
6. Add privacy tests for tool result and file content exclusion.
7. Add slow/error tests for compact stack policy.

## Validation

```bash
./scripts/test_with_guard.sh ./internal/platform/toolbridge ./internal/platform/difftracker ./internal/provider/... -count=1
```

If provider-wide tests are too broad or slow, narrow to touched packages and report the exact package list.

## Acceptance

- Provider and tool spans are correlated with turn trace ids.
- Tool failures include enough code anchor/stack context for LSP navigation.
- Tool output and file content never enter trace JSONL.
- Normal successful high-volume calls do not explode log volume.
