# Dev UI Test MCP

The UI test MCP is a dev/test-only stdio server for local machine-driven checks of the React UI. It must run only against loopback Vite pages with the browser harness enabled.

## Startup

Start a dev UI with the harness enabled:

```bash
cd frontend-app
VITE_SUPER_DOLPHIN_UI_TEST_MCP=1 npm run dev -- --host 127.0.0.1 --port 5175 --strictPort
```

Start the MCP server against that page:

```bash
cd frontend-app
SUPER_DOLPHIN_UI_TEST_MCP=1 SUPER_DOLPHIN_UI_TEST_BASE_URL=http://127.0.0.1:5175 npm run mcp:ui-test
```

Run deterministic acceptance. By default it starts its own Vite server on `127.0.0.1:5177`, starts the MCP server, runs the full tool flow, submits only through isolated acceptance mode, then verifies shutdown behavior.

```bash
cd frontend-app
npm run mcp:ui-test:acceptance
```

Direct invocation is equivalent when the package script is not available yet:

```bash
cd frontend-app
node scripts/ui-test-mcp-acceptance.mjs
```

When `SUPER_DOLPHIN_UI_TEST_BASE_URL` is provided to the acceptance script, it treats the page as caller-owned and runs read-only by default. Set `SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_OWNS_UI=1` only for an explicitly isolated caller-provided page where submit testing is allowed.

If Playwright cannot use its managed browser on the local platform, point the MCP server at an existing Chromium-compatible executable:

```bash
cd frontend-app
SUPER_DOLPHIN_UI_TEST_BROWSER_EXECUTABLE_PATH=/path/to/chrome npm run mcp:ui-test:acceptance
```

## Tools

All MCP calls use `tools/call` with a strict `arguments` object. Extra fields are rejected.

| Tool | Side effect | Arguments | Structured response |
| --- | --- | --- | --- |
| `ui_snapshot` | Read-only | `{}` | `route`, `currentThreadId`, `inputTextLength`, `hasRunningTurn`, `visibleErrors`, `availableActions` |
| `ui_diagnostics` | Read-only | `{}` | `consoleErrors`, `bridgeErrors`, `unhandledErrors`, `warningEntries`, `url`, `readyState` |
| `ui_frontend_logs` | Read-only | `level?`, `source?`, `since?`, `limit?` | Sanitized log entries |
| `ui_action` | State-changing | One of the action argument shapes below | Action result and sanitized action log data |

Supported actions:

| Action | Arguments |
| --- | --- |
| `navigate` | `{ "action": "navigate", "route": "chat" \| "settings" \| "observability" }` |
| `fill_composer` | `{ "action": "fill_composer", "target": "composer_input", "text": "..." }` |
| `submit_composer` | `{ "action": "submit_composer", "target": "composer_submit" }` |
| `wait_for` | `{ "action": "wait_for", "state": "frontend_ready" \| "composer_text_length" \| "route", "value"?: ..., "timeoutMs"?: ... }` |

Targets are `composer_input` and `composer_submit`. Routes are `chat`, `settings`, and `observability`. Wait states are `frontend_ready`, `composer_text_length`, and `route`.

## Logs

`ui_frontend_logs` accepts:

| Filter | Behavior |
| --- | --- |
| `level` | Returns only matching log levels. |
| `source` | Returns only matching sources, such as `ui_test_mcp`. |
| `since` | Returns entries newer than the provided timestamp or cursor value. |
| `limit` | Defaults to `100` and cannot exceed `100`. |

Log fields are sanitized before storage and before MCP output. Raw prompt text, user messages, memory, skill content, thread messages, tool results, tokens, API keys, authorization values, local file paths, and oversized strings must not appear in MCP log output.

## Limits

| Constant | Value |
| --- | ---: |
| `defaultLimit` | `100` |
| `maxLimit` | `100` |
| `maxTextLength` | `4000` |
| `maxStringLength` | `500` |
| `maxFieldDepth` | `4` |
| `maxFieldCount` | `50` |
| `defaultTimeoutMs` | `5000` |
| `maxTimeoutMs` | `30000` |
| `pollIntervalMs` | `100` |
| `maxFrameBytes` | `1048576` |
| `maxHeaderBytes` | `8192` |
| `maxLineBytes` | `1048576` |

Waits are bounded by `maxTimeoutMs`; frame, header, and NDJSON line sizes fail fast when they exceed the contract limits.

## Submit Safety

`submit_composer` is not available against normal product runtime. It requires all of these conditions:

- `SUPER_DOLPHIN_UI_TEST_ALLOW_SUBMIT=1`
- an isolated acceptance token
- token match in the browser harness
- MCP server ownership of the Vite process, or explicit `SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_OWNS_UI=1`
- composer send-mode readiness

The submit path must use the harness isolation helper. It must not click the product submit button, start a thread, start a turn, acquire or resume a provider session, or call the Wails/backend bridge.

## Forbidden Capabilities

The UI test MCP must not provide:

- production exposure or production bundle inclusion
- non-loopback URLs
- arbitrary JavaScript evaluation
- arbitrary CSS selectors
- unbounded waits or retries
- raw prompt, memory, skill, or thread-message logs
- token, key, authorization, secret, or local path leakage
- a generic click action

## Production Prohibition

Production builds must not load or expose the harness. `VITE_SUPER_DOLPHIN_UI_TEST_MCP=1` is valid only for dev/test Vite runs, and production build configuration must reject it. Production artifacts must not contain `SUPER_DOLPHIN_UI_TEST`, `ui_snapshot`, `ui_action`, `ui_frontend_logs`, `ui_diagnostics`, `uiTestHarness`, or `submitComposerInIsolation`.
