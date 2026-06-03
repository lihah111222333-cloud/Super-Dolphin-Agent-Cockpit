# T07 - Codex Surface Wiring, Gating, And Duplicates

Depends on: T05

## Objective

Wire the trace tool into the app graph and Codex dynamic tool surface without breaking MCP peer tools.

## Source Anchors

- `internal/app/modules.go:59-83`
- `internal/app/toolbridge_adapters.go:199-228`
- `internal/provider/codexapp/support.go:325-341`
- `internal/platform/toolbridge/module.go:71-85`
- `internal/platform/toolbridge/handler_peer_decode.go:44-90`
- `internal/platform/toolbridge/handler_host_tools.go:145-151`
- `internal/platform/toolbridge/handler_peer_decode.go:186-215`

## Scope

Wire `observability_trace_get` by:

- extending `hostToolRegistryIn` to accept `*observability.Service`;
- appending the trace registry to `NewCompositeHostToolRegistry`;
- reserving `observability_trace_get` as host-only;
- preserving Codex host-tool aliases;
- preventing reserved MCP duplicate names from failing the whole surface.

## Requirements

- Codex surface includes `observability_trace_get` when tracing is enabled.
- Codex surface excludes it when tracing is disabled.
- Reserved host-only duplicate names from MCP peers are skipped or logged, not treated as fatal surface errors.
- Non-reserved duplicate conflicts should continue to fail fast.
- Reserved duplicate handling must cover canonical Codex names and aliases, not only raw MCP tool names.

## Acceptance Criteria

- `PrepareCodexToolSurface` returns a surface that contains the trace tool in enabled mode.
- Disabled observability config does not advertise the tool.
- Duplicate reserved host-only peer tool names do not block unrelated tools.
- Non-reserved duplicate names and aliases still fail fast.
