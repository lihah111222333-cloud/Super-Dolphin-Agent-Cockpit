# Dev Agentic E2E Harness

This document records the productized subset of the experimental `codex/agentic-e2e-harness` branch.

## Boundary

The supported agent-facing entry point is the dev/test-only UI Test MCP documented in [dev-ui-test-mcp.md](dev-ui-test-mcp.md). Agents should call `ui_scenario_run` for multi-step probes and use the lower-level `ui_snapshot`, `ui_action`, `ui_diagnostics`, and `ui_frontend_logs` tools only when they need to inspect an intermediate state.

The implementation intentionally keeps Playwright behind the MCP server. MCP clients do not receive arbitrary page handles, JavaScript evaluation, selectors, screenshots, network logs, or Wails bridge patching powers.

## Productized Ideas

| Experiment idea | Productized form |
| --- | --- |
| Goal allowlist | `UI_TEST_SCENARIO_IDS` in `frontend-app/src/devtools/uiTestContract.js`. |
| Deterministic planner | Fixed scenario step lists inside `ui_scenario_run`. |
| Diagnostic reporting | Scenario results include final snapshot, diagnostics, steps, and sanitized logs. |
| Local acceptance run | `npm run mcp:ui-test:scenario`. |
| Browser sandboxing | Loopback-only base URL, dev/test harness gate, bounded waits, and no production exposure. |

## Supported Scenarios

| Scenario | Purpose |
| --- | --- |
| `chat_composer_probe` | Verify the chat composer can be reached, filled, and observed through snapshot state. |
| `frontend_navigation_probe` | Verify chat-to-observability navigation plus composer state observation. |
| `observability_logs_probe` | Verify observability page navigation and sanitized log access. |
| `settings_open_probe` | Verify settings page navigation without settings writes. |
| `open_route_probe` | Verify one explicitly allowlisted route. |

## Local Flow

Run a full scenario through the same stdio MCP path an agent uses:

```bash
cd frontend-app
npm run mcp:ui-test:scenario
```

Use a specific scenario:

```bash
cd frontend-app
SUPER_DOLPHIN_UI_TEST_SCENARIO=settings_open_probe npm run mcp:ui-test:scenario
```

Use an explicit route probe:

```bash
cd frontend-app
SUPER_DOLPHIN_UI_TEST_SCENARIO=open_route_probe \
SUPER_DOLPHIN_UI_TEST_SCENARIO_ROUTE=skills \
npm run mcp:ui-test:scenario
```

The script writes its structured result to `frontend-app/.tmp/ui-test-mcp-scenarios`. That directory is a local run artifact and is not a source-controlled fixture.

## Out Of Scope

The experimental branch contained broader browser automation ideas that remain intentionally unavailable:

- arbitrary Playwright actions
- arbitrary JavaScript evaluation
- arbitrary CSS, role, or text selectors
- raw DOM, ARIA, screenshots, network URLs, request bodies, or RPC payload capture
- Wails WebSocket monkey-patching as a generic action
- real provider turns, real backend writes, or real settings changes
- browser-launching E2E suites inside ordinary `npm test`

New scenarios should be added only when they can be represented as a small allowlisted sequence of existing MCP primitives with bounded waits and sanitized outputs.
