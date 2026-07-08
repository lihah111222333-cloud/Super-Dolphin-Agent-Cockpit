# UI Test MCP Scenario Migration Plan

## Goal

Extend the dev/test-only UI Test MCP with an allowlisted scenario runner that absorbs the useful parts of the experimental `codex/agentic-e2e-harness` branch without exposing arbitrary browser automation.

## Constraints

- Keep stdio MCP as the agent-facing boundary.
- Keep the browser harness dev/test-only and loopback-only.
- Reject extra arguments and unknown names fail-fast.
- Do not expose arbitrary JavaScript evaluation, selectors, screenshots, DOM dumps, network payloads, Wails bridge patching, provider turns, or settings writes.
- Preserve the existing `ui_snapshot`, `ui_action`, `ui_diagnostics`, and `ui_frontend_logs` tools.

## Planned Work

1. Add scenario ids and validators to the shared UI test contract.
2. Add `ui_scenario_run` to the MCP server with strict schema and structured failure results.
3. Compose scenarios from existing UI Test MCP primitives instead of new raw browser actions.
4. Add a local scenario acceptance script and npm entry.
5. Document startup, tool arguments, log filtering, scenario parameters, and forbidden powers.
6. Validate with focused tests, MCP acceptance scripts, full frontend lint/test/build, and production negative build checks.

## Initial Scenarios

| Scenario | Scope |
| --- | --- |
| `chat_composer_probe` | Local composer fill and snapshot state. |
| `frontend_navigation_probe` | Composer fill plus observability navigation. |
| `observability_logs_probe` | Read-only observability navigation and logs. |
| `settings_open_probe` | Read-only settings navigation. |
| `open_route_probe` | One explicit allowlisted route. |

## Acceptance

```bash
cd frontend-app
npm run mcp:ui-test:acceptance
npm run mcp:ui-test:scenario
npm run lint
npm test
npm run build
```

If Playwright cannot use its managed browser locally, set `SUPER_DOLPHIN_UI_TEST_BROWSER_EXECUTABLE_PATH` to a Chromium-compatible executable before running the MCP acceptance scripts.
