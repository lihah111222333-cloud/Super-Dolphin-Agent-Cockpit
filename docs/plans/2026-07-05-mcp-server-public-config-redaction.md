# MCP Server Public Config Redaction

## Problem

`mcpServer/list` and default MCP server start RPCs returned full `MCPServerConfig` values to the UI. Those configs can contain headers, env vars, command args, URLs, and local database paths.

## Boundary

- Keep service, store, and provider-facing config paths full fidelity.
- Project only the public RPC responses.
- `mcpServer/list` returns `configPath` and `mcpServers[name].enabled`.
- default start RPCs return `configPath`, `serverName`, `added`, and `enabled` where applicable.
- Frontend response validators reject MCP config keys in public responses.

## Verification

- `go test ./internal/module/mcp_server ./internal/module/mcp_server_npx ./internal/module/ws_test -count=1`
- `cd frontend-app && npm test -- --run src/shared/api/backendApi.test.js src/shared/api/backendApi.contractMatrix.test.js src/pages/skills/SkillsPage.test.jsx`
