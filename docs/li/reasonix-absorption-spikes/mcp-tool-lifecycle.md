# MCP Tool Lifecycle Spike

## Existing Facts

- MCP server config has `enabled`.
- MCP tool DTO has name, description, input schema, and output schema.
- Toolbridge currently handles canonical names and aliases, not per-tool suspend/remove state.

## Decision

Phase 5 may centralize namespace helpers immediately. Per-tool lifecycle filtering must not be added until a later schema decision defines state owner, storage, and migration. For this plan, lifecycle absorption is limited to naming ownership and compatibility tests.

Owner/schema/storage decision is now captured in `docs/adr/0003-mcp-tool-lifecycle-state.md`. Until that ADR's implementation gates are met, `suspended` and `removed` states remain design-only and must not be wired into production toolbridge filtering.
