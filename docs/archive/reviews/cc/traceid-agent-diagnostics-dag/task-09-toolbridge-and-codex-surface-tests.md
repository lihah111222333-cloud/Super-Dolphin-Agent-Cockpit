# T09 - Toolbridge And Codex Surface Test Gate

Depends on: T05, T07

## Objective

Lock toolbridge exposure, disabled gating, stale direct calls, duplicate handling, CWD behavior, and output envelope behavior.

## Source Anchors

- `internal/platform/toolbridge`
- `internal/app/toolbridge_adapters.go:199-228`
- `internal/provider/codexapp/support.go:325-341`

## Test Coverage

Add or extend tests for:

- `PrepareCodexToolSurface` includes `observability_trace_get` when tracing is enabled.
- `PrepareCodexToolSurface` excludes `observability_trace_get` when tracing is disabled.
- Stale direct calls return explicit disabled/degraded output rather than a clean diagnosis.
- `observability_trace_get` calls return diagnosis payload with degraded fields when appropriate.
- Result content is either `StructuredContent` or parseable bounded JSON text, matching the chosen T06 contract.
- Reserved host-only duplicate names from MCP peers do not break surface preparation.
- Non-reserved duplicate names and aliases still fail fast.
- CWD behavior is covered for the trace tool path.

## Requirements

- Keep non-reserved duplicate-name errors fail-fast.
- Do not weaken existing memory host tool tests.
- Assert input schema contains required `trace_id`.
- Assert input schema exposes maximum `limit` and the server-side call path enforces it.
- Do not leave all toolbridge tests to T09; behavior-changing work in T05, T06, and T07 must include its own tests before that worktree can pass review.

## Acceptance Criteria

```bash
./scripts/test_with_guard.sh ./internal/platform/toolbridge -count=1
```

passes locally.
