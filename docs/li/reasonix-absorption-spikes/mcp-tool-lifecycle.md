# MCP Tool Lifecycle Spike

## Existing Facts

- MCP server config has `enabled`.
- MCP tool DTO has name, description, input schema, and output schema.
- Toolbridge currently handles canonical names and aliases, not per-tool suspend/remove state.

## Decision

Phase 5 may centralize namespace helpers immediately. Per-tool lifecycle filtering must not be added until a later schema decision defines state owner, storage, and migration. For this plan, lifecycle absorption is limited to naming ownership and compatibility tests.
