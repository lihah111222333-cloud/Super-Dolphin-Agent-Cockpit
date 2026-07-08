# UI Test MCP Implementation Plan

> **For agentic workers:** 强制要求子技能: Use superpowers:子代理驱动开发 (recommended) or superpowers:执行计划 to implement this plan task-by-task. In super-agent-v3, subagents may use platform-native dispatch directly; use mcp-orch DAG runs and nodes (`task_create_dag` / `task_start_dag` / `task_dispatch_node` / `task_update_node`) only when persistent orchestration records are needed. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a dev/test-only MCP interface that lets machines inspect the local React UI, execute allowlisted UI actions, read diagnostics, and retrieve sanitized frontend logs for local exploration tests.

**Architecture:** Keep the feature inside `frontend-app` as a browser devtools harness plus a local Node stdio MCP server. Production builds must not load or expose the harness; the MCP server only talks to loopback URLs, uses strict schemas from one shared contract, and never accepts arbitrary JavaScript or CSS selectors.

**Tech Stack:** React/Vite, Zustand store, Vitest/jsdom, Node ESM scripts, Playwright from existing `@playwright/test`, JSON-RPC/MCP over stdio.

**Verification Surface:** Focused Vitest tests, deterministic self-contained MCP acceptance against a Vite dev server, LSP diagnostics for all changed JS/JSX/MJS/JSON files, `cd frontend-app && npm run lint && npm test && npm run build`, production artifact scan, and `make frontend-embed-verify`.

---

## Review Gate

Round 1 review result: 4 PASS, 14 BLOCK. Round 2 was stopped early after D02/D03/D04/D05 returned P1 blockers despite D01/D06 passing. Do not implement until the revised plan has at least 9 of 18 dimension agents voting PASS and no unresolved P0/P1 finding.

Resolved in this revision:
- D02/D03/D04/D10/D11/D12/D17/D18: one `recordLog({ level, source, message, fields })` contract, strict schema, fail-fast store write, and action-log acceptance.
- D03/D12/D14: MCP initialize/ping/shutdown/exit, JSON-RPC error behavior, framed/NDJSON parser caps, Playwright cleanup, bounded wait behavior.
- D08/D10/D11: centralized log-field redaction/allowlist for prompt/thread/memory/skill content and warning projection.
- D09/D15: submit-vs-interrupt distinction, conditional `availableActions`, and submit guarded behind an explicit isolated acceptance mode.
- D13: dynamic dev-only harness import, production flag rejection, artifact scan, and embed verification.
- D16: target worktree-only workflow, main worktree duplicate plan removed, explicit sync/staging guard.

New fixes after Round 2 early stop:
- D02: acceptance submit is only enabled when the script owns the Vite server and installs isolated test mode; external `SUPER_DOLPHIN_UI_TEST_BASE_URL` runs read-only unless a separate ownership token is provided. `recordLog` rejects missing `fields`, and acceptance fails on non-empty diagnostics errors.
- D03: JSON-RPC error codes, id behavior, and result-vs-error boundaries are pinned; framing preserves request mode when writing responses.
- D04: `frontend-app/vite.config.test.js` is an owned path and part of the LSP diagnostics matrix when config tests are touched.
- D05: `submit_composer` cannot click or reach product thread/turn/provider runtime. It is rejected in normal product runtime and only succeeds through a server-owned isolated acceptance path with forbidden RPC call assertions.

Cycle 3 non-blocking clarifications incorporated:
- D03: lifecycle tests include `tools/list` before `initialize`, notification and id-bearing `exit`, and framed versus NDJSON parse-error responses.

## File Structure

- Create `frontend-app/src/shared/diagnostics/safeLogFields.js`: single browser-safe sanitizer for log fields. It redacts forbidden keys and values, strips local paths, truncates long strings, bounds object depth/field count, and is used by existing bridge logging plus the UI test harness.
- Modify `frontend-app/src/shared/api/wailsBridge.js`: replace local forbidden-key logic with `safeLogFields` without weakening current behavior.
- Create `frontend-app/src/devtools/uiTestContract.js`: pure browser-safe registry for tool names, action names, targets, routes, wait states, output field names, limits, and JSON-schema-like validation helpers. It must not import Playwright, Node modules, or React.
- Create `frontend-app/src/devtools/uiTestHarness.js`: dev/test-only browser harness that projects UI state, diagnostics, warnings, and frontend logs using `uiTestContract` and `safeLogFields`.
- Create `frontend-app/src/devtools/uiTestHarness.test.js`: unit tests for gating, production rejection, snapshot fields, conditional actions, submit/interrupt safety, diagnostics/warnings, log filtering, strict key sets, and redaction.
- Modify `frontend-app/src/main.jsx`: dynamically import the harness only when `!import.meta.env.PROD && (import.meta.env.DEV || import.meta.env.MODE === 'test' || import.meta.env.VITE_SUPER_DOLPHIN_UI_TEST_MCP === '1')`.
- Modify `frontend-app/vite.config.js`: fail-fast if `VITE_SUPER_DOLPHIN_UI_TEST_MCP` is set for a production build.
- Modify `frontend-app/vite.config.test.js`: assert production flag rejection and dev/test allowance.
- Modify `frontend-app/src/pages/chat/components/ComposerMeta.jsx`: add mode-specific stable anchors: `data-testid="composer-submit"` only when the primary action is send, and `data-testid="composer-interrupt"` only when it is interrupt.
- Modify `frontend-app/src/pages/chat/components/ComposerDock.test.jsx`: assert send and interrupt anchors are mutually exclusive and accessible.
- Create `frontend-app/scripts/ui-test-mcp-framing.mjs`: shared bounded Content-Length and NDJSON frame parser/writer used by server and acceptance.
- Create `frontend-app/scripts/ui-test-mcp-server.mjs`: stdio MCP server exposing `ui_snapshot`, `ui_action`, `ui_diagnostics`, `ui_frontend_logs`.
- Create `frontend-app/scripts/ui-test-mcp-server.test.mjs`: unit tests for MCP lifecycle, schemas, strict validation, parser caps, loopback/prod rejection, stdout/stderr separation, Playwright cleanup, and action failures.
- Create `frontend-app/scripts/ui-test-mcp-acceptance.mjs`: deterministic acceptance script that starts a Vite dev server on loopback by default, starts the MCP server, initializes MCP, calls all required tools, verifies action logs, submits only in isolated acceptance mode, and shuts everything down.
- Modify `frontend-app/package.json`: add `mcp:ui-test` and `mcp:ui-test:acceptance`.
- Create `docs/dev-ui-test-mcp.md`: startup, tool arguments, response fields, log filters, safety limits, action side effects, production prohibition, and forbidden capabilities.

## Contract Constants

`frontend-app/src/devtools/uiTestContract.js` is the single source for:

```js
export const UI_TEST_GLOBAL = '__SUPER_DOLPHIN_UI_TEST__';
export const UI_TEST_TOOLS = ['ui_snapshot', 'ui_action', 'ui_diagnostics', 'ui_frontend_logs'];
export const UI_TEST_ACTIONS = ['navigate', 'fill_composer', 'submit_composer', 'wait_for'];
export const UI_TEST_TARGETS = ['composer_input', 'composer_submit'];
export const UI_TEST_ROUTES = { chat: '/', settings: '/settings', observability: '/observability' };
export const UI_TEST_WAIT_STATES = ['frontend_ready', 'composer_text_length', 'route'];
export const UI_TEST_LIMITS = {
  defaultLimit: 100,
  maxLimit: 100,
  maxTextLength: 4000,
  maxStringLength: 500,
  maxFieldDepth: 4,
  maxFieldCount: 50,
  defaultTimeoutMs: 5000,
  maxTimeoutMs: 30000,
  pollIntervalMs: 100,
  maxFrameBytes: 1024 * 1024,
  maxHeaderBytes: 8192,
  maxLineBytes: 1024 * 1024,
};
```

Every tool schema, validator, harness `availableActions`, server action dispatcher, acceptance assertion, and docs table must derive from these constants or import this module.

## Task 0: Workflow Guard and Base Sync

**Files:**
- Read-only checks plus possible branch rebase.

- [ ] **Step 1: Verify target worktree only**

Run:

```bash
git -C /home/l4place/Super-Dolphin/.worktrees/ui-test-mcp-20260708 status --short --untracked-files=all
git -C /home/l4place/Super-Dolphin status --short --untracked-files=all
test ! -e /home/l4place/Super-Dolphin/docs/plans/2026-07-08-ui-test-mcp.md
```

Expected: target worktree has only this plan before implementation; main worktree may have unrelated dirty files, but no duplicate UI-test-MCP plan.

- [ ] **Step 2: Refresh from origin before implementation**

Run:

```bash
git fetch origin
git -C /home/l4place/Super-Dolphin/.worktrees/ui-test-mcp-20260708 rebase origin/main
```

Expected: rebase succeeds. If conflicts occur, stop and report; do not implement on a stale base.

- [ ] **Step 3: Staging rule**

Never run `git add .`. Owned paths are only:

```text
docs/dev-ui-test-mcp.md
docs/plans/2026-07-08-ui-test-mcp.md
frontend-app/package.json
frontend-app/src/devtools/**
frontend-app/src/main.jsx
frontend-app/src/pages/chat/components/ComposerMeta.jsx
frontend-app/src/pages/chat/components/ComposerDock.test.jsx
frontend-app/src/shared/api/wailsBridge.js
frontend-app/src/shared/diagnostics/**
frontend-app/scripts/ui-test-mcp-*.mjs
frontend-app/vite.config.js
frontend-app/vite.config.test.js
```

## Task 1: Shared Sanitizer and Harness Contract

**Files:**
- Create: `frontend-app/src/shared/diagnostics/safeLogFields.js`
- Test: `frontend-app/src/shared/diagnostics/safeLogFields.test.js`
- Create: `frontend-app/src/devtools/uiTestContract.js`
- Test: `frontend-app/src/devtools/uiTestContract.test.js`
- Modify: `frontend-app/src/shared/api/wailsBridge.js`

- [ ] **Step 1: Write sanitizer and contract tests**

Tests must fail before implementation and assert:
- Forbidden keys are redacted at any nesting depth: `token`, `api_key`, `secret`, `authorization`, `prompt`, `user_prompt`, `user_message`, `message_text`, `text`, `content`, `file_content`, `tool_result`, `memory`, `skill`, `thread_messages`.
- Absolute local paths such as `/home/me/repo/file.txt`, `/Users/me/repo/file.txt`, and `C:\Users\me\repo\file.txt` become `[path]`.
- Strings longer than `UI_TEST_LIMITS.maxStringLength` are truncated.
- Objects deeper than `maxFieldDepth` and wider than `maxFieldCount` are bounded.
- `UI_TEST_TOOLS`, `UI_TEST_ACTIONS`, `UI_TEST_TARGETS`, `UI_TEST_ROUTES`, and `UI_TEST_WAIT_STATES` have exact expected values and no duplicate entries.

- [ ] **Step 2: Run focused failing tests**

Run:

```bash
cd frontend-app
npx vitest run src/shared/diagnostics/safeLogFields.test.js src/devtools/uiTestContract.test.js --no-file-parallelism --maxWorkers=1
```

Expected before implementation: FAIL because files are missing.

- [ ] **Step 3: Implement sanitizer and contract modules**

Implement `safeLogFields(fields, options)` and `redactUITestValue(value, options)` as pure functions. Implement contract constants and validators:

```js
export function assertKnownToolName(name) {}
export function assertKnownActionName(name) {}
export function assertKnownTargetName(target) {}
export function normalizeLimit(limit) {}
export function normalizeTimeoutMs(timeoutMs) {}
export function validateExactKeys(value, allowedKeys, label) {}
```

Unknown fields must throw; do not silently ignore extra arguments.

- [ ] **Step 4: Reuse sanitizer in `wailsBridge.js`**

Replace the local bridge field redaction with the shared sanitizer while keeping the current blocked-key behavior. Existing bridge tests must still pass.

- [ ] **Step 5: Run focused tests**

Run:

```bash
cd frontend-app
npx vitest run src/shared/diagnostics/safeLogFields.test.js src/devtools/uiTestContract.test.js src/shared/api/wailsBridge.test.js --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

## Task 2: Browser Harness

**Files:**
- Create: `frontend-app/src/devtools/uiTestHarness.js`
- Create: `frontend-app/src/devtools/uiTestHarness.test.js`
- Modify: `frontend-app/src/main.jsx`
- Modify: `frontend-app/vite.config.js`
- Modify: `frontend-app/vite.config.test.js`

- [ ] **Step 1: Write harness tests**

Tests must cover:
- `isUITestHarnessEnabled({ PROD: true, VITE_SUPER_DOLPHIN_UI_TEST_MCP: '1' }) === false`.
- Dev/test modes enable the harness; production never enables it.
- `snapshot()` throws when `data-testid="frontend-app"` is missing.
- `snapshot()` returns exact keys: `route`, `currentThreadId`, `inputTextLength`, `hasRunningTurn`, `visibleErrors`, `availableActions`.
- `availableActions` is conditional and includes disabled reasons; `submit_composer` is absent or disabled when the primary button is interrupt or disabled.
- `diagnostics()` returns exact keys: `consoleErrors`, `bridgeErrors`, `unhandledErrors`, `warningEntries`, `url`, `readyState`.
- `frontendLogs()` throws when `state.logEntries` is missing, applies level/source/since/limit filters, and returns exact log keys.
- `recordLog({ level, source, message, fields })` requires `level`, `source`, and `message` to be strings, requires `fields` to be a plain object, requires `state.addLog` and `state.logEntries`, sanitizes before persisting, maps to `addLog(level, `${source}.${message}`, fields)`, and returns a sanitized log entry.
- `recordLog()` rejects unknown fields and unsafe source values.
- `recordLog()` rejects missing or invalid `fields` and must not write anything to the store on validation failure.

- [ ] **Step 2: Run focused failing test**

Run:

```bash
cd frontend-app
npx vitest run src/devtools/uiTestHarness.test.js --no-file-parallelism --maxWorkers=1
```

Expected before implementation: FAIL because harness is missing.

- [ ] **Step 3: Implement harness**

Harness API:

```js
export function isUITestHarnessEnabled(metaEnv = import.meta.env || {}) {}
export function createUITestHarness({ getState, documentRef = document, locationRef = window.location, now = () => new Date() }) {}
export function installUITestHarness({ windowRef = window, getState, metaEnv = import.meta.env }) {}
```

`createUITestHarness()` returns:

```js
{
  snapshot,
  frontendLogs,
  diagnostics,
  recordLog,
}
```

`recordLog()` signature is exactly:

```js
recordLog({ level, source, message, fields })
```

Allowed `source` for MCP action logs is `ui_test_mcp`. Missing `fields`, missing store `addLog`, or missing `logEntries` is an error.

The harness also exposes an internal fixed-method acceptance helper for the MCP server:

```js
verifyIsolatedAcceptance({ token })
submitComposerInIsolation({ token })
```

These helpers are not documented as public MCP tools. They only work when the page was initialized by the self-owned acceptance script with a server-generated token. `submitComposerInIsolation()` validates that `data-testid="composer-submit"` is present, enabled, and in send mode, then performs a bounded synthetic isolated submit that records a `ui_test_mcp.submit_composer` log without invoking product thread/turn/provider handlers. In normal product runtime, `submit_composer` must be disabled or rejected with a structured reason.

- [ ] **Step 4: Install with production-excluding dynamic import**

`main.jsx` must not statically import `uiTestHarness.js`. Use build-time gating:

```js
const shouldLoadUITestHarness = !import.meta.env.PROD && (
  import.meta.env.DEV ||
  import.meta.env.MODE === 'test' ||
  import.meta.env.VITE_SUPER_DOLPHIN_UI_TEST_MCP === '1'
);

if (shouldLoadUITestHarness) {
  void import('./devtools/uiTestHarness.js').then(({ installUITestHarness }) => {
    installUITestHarness({ getState: () => useClientStore.getState() });
  });
}
```

`vite.config.js` must throw during production build when `VITE_SUPER_DOLPHIN_UI_TEST_MCP` is set.
`vite.config.test.js` must prove production builds reject the flag and dev/test config accepts it.

- [ ] **Step 5: Run tests**

Run:

```bash
cd frontend-app
npx vitest run src/devtools/uiTestHarness.test.js vite.config.test.js --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

## Task 3: Mode-Specific Composer Anchors

**Files:**
- Modify: `frontend-app/src/pages/chat/components/ComposerMeta.jsx`
- Modify: `frontend-app/src/pages/chat/components/ComposerDock.test.jsx`

- [ ] **Step 1: Write anchor tests**

Add tests that assert:
- Send mode exposes `data-testid="composer-submit"` and accessible name `发送消息`.
- Interrupt mode exposes `data-testid="composer-interrupt"` and not `composer-submit`.
- Submit action tests must fail if the primary button is disabled or interrupting.

- [ ] **Step 2: Run focused failing test**

Run:

```bash
cd frontend-app
npx vitest run src/pages/chat/components/ComposerDock.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected before implementation: FAIL because mode-specific anchors are absent.

- [ ] **Step 3: Implement anchors**

Button should use:

```jsx
data-testid={canInterrupt ? 'composer-interrupt' : 'composer-submit'}
```

Do not add `composer-submit` when the action is interrupt.

- [ ] **Step 4: Run focused test**

Run:

```bash
cd frontend-app
npx vitest run src/pages/chat/components/ComposerDock.test.jsx --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

## Task 4: MCP Framing and Protocol Server

**Files:**
- Create: `frontend-app/scripts/ui-test-mcp-framing.mjs`
- Create: `frontend-app/scripts/ui-test-mcp-server.mjs`
- Create: `frontend-app/scripts/ui-test-mcp-server.test.mjs`
- Modify: `frontend-app/package.json`

- [ ] **Step 1: Write server tests**

Tests must cover:
- `initialize` returns protocol version, capabilities, server name/version, and preserves request id.
- `notifications/initialized` returns no response.
- `ping`, `shutdown`, and `exit` are supported. `exit` notification produces no response and stops the server; an id-bearing `exit` request returns one final success response, then stops.
- JSON-RPC error behavior follows the exact matrix below.
- `tools/list` returns exactly `UI_TEST_TOOLS`.
- Every tool has strict input schema object from `uiTestContract`.
- `tools/call` returns `{ content, structuredContent, isError }` for success and failure.
- Unknown tool/action/target/extra field throws structured error.
- Non-loopback URLs, unsafe protocols, credentials/userinfo, and production mode without `SUPER_DOLPHIN_UI_TEST_MCP=1` are rejected.
- Content-Length, NDJSON, chunked frames, oversized frame/header/line failures, and stdout/stderr separation.
- Playwright fake browser/page is closed on shutdown, exit, EOF, SIGINT/SIGTERM, and startup failure.

JSON-RPC error matrix:

| Case | Response boundary | Code | id behavior |
| --- | --- | --- | --- |
| Malformed JSON or invalid frame payload | top-level `error` | `-32700` | `null` |
| Parsed request is not an object | top-level `error` | `-32600` | `null` |
| Missing or invalid `jsonrpc: "2.0"` | top-level `error` | `-32600` | preserve valid scalar `id`, otherwise `null` |
| Missing or non-string `method` | top-level `error` | `-32600` | preserve valid scalar `id`, otherwise `null` |
| Unknown method | top-level `error` | `-32601` | preserve request `id` |
| Invalid method params, unknown tool/action/target, extra field | top-level `error` | `-32602` | preserve request `id` |
| Lifecycle violation, such as `tools/list` or `tools/call` before `initialize` | top-level `error` | `-32000` | preserve request `id` |
| Unexpected server exception | top-level `error` | `-32603` | preserve request `id` |
| Valid `tools/call` where page is unreadable, UI disconnected, timeout, or action fails | JSON-RPC success with tool result | no top-level code | `result.isError: true`, `structuredContent.error` includes action/tool/target/timeout/reason |

Tests must assert exact code, id preservation or null id, and that valid tool failures stay inside `{ content, structuredContent, isError: true }` instead of top-level JSON-RPC errors.

- [ ] **Step 2: Run failing server tests**

Run:

```bash
cd frontend-app
npx vitest run scripts/ui-test-mcp-server.test.mjs --no-file-parallelism --maxWorkers=1
```

Expected before implementation: FAIL because server/framing files are missing.

- [ ] **Step 3: Implement shared framing**

`ui-test-mcp-framing.mjs` exports:

```js
export function encodeMCPFrame(message, mode) {}
export function parseMCPFrame(buffer) {}
export function createMCPFrameReader({ onMessage, onError, limits }) {}
```

`parseMCPFrame()` returns the parsed `message`, consumed byte count, and detected `mode` (`content-length` or `ndjson`). The server must pass that mode back into `encodeMCPFrame(message, mode)` so Content-Length requests receive Content-Length responses and NDJSON requests receive NDJSON responses. Malformed Content-Length payloads return framed `-32700`; malformed NDJSON payloads return NDJSON `-32700`. Use `UI_TEST_LIMITS.maxFrameBytes`, `maxHeaderBytes`, and `maxLineBytes`. Oversized input fails fast.

- [ ] **Step 4: Implement MCP server**

Server requirements:
- Default base URL: self-contained acceptance URL or `http://127.0.0.1:5175/`.
- Only loopback hostnames/IPs are allowed: `127.0.0.1`, `localhost`, `[::1]`.
- Operations are single-flight per page to avoid interleaved Playwright state changes.
- `wait_for` default timeout is 5000 ms, hard max 30000 ms, poll interval 100 ms, abort-aware.
- `submit_composer` is rejected in normal product runtime even when the page reports enabled send mode. It only succeeds when all are true: `SUPER_DOLPHIN_UI_TEST_ALLOW_SUBMIT=1`, a server-generated acceptance token is present, the page reports `verifyIsolatedAcceptance({ token })` as isolated, the MCP server owns the Vite process, and the action is enabled send mode. The action must use `submitComposerInIsolation({ token })`, not a Playwright click on the product submit handler.
- `click` is not a public action in this revision; use `navigate`, `fill_composer`, `submit_composer`, and `wait_for` only.
- Tool calls use only fixed page evaluations:

```js
await page.evaluate(() => window.__SUPER_DOLPHIN_UI_TEST__.snapshot());
await page.evaluate((filters) => window.__SUPER_DOLPHIN_UI_TEST__.frontendLogs(filters), filters);
await page.evaluate(() => window.__SUPER_DOLPHIN_UI_TEST__.diagnostics());
await page.evaluate((entry) => window.__SUPER_DOLPHIN_UI_TEST__.recordLog(entry), entry);
await page.evaluate((input) => window.__SUPER_DOLPHIN_UI_TEST__.verifyIsolatedAcceptance(input), input);
await page.evaluate((input) => window.__SUPER_DOLPHIN_UI_TEST__.submitComposerInIsolation(input), input);
```

No arbitrary eval or selector input is accepted.

- [ ] **Step 5: Add package scripts**

Add:

```json
"mcp:ui-test": "node scripts/ui-test-mcp-server.mjs",
"mcp:ui-test:acceptance": "node scripts/ui-test-mcp-acceptance.mjs"
```

- [ ] **Step 6: Run server tests**

Run:

```bash
cd frontend-app
npx vitest run scripts/ui-test-mcp-server.test.mjs --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

## Task 5: Deterministic Acceptance

**Files:**
- Create: `frontend-app/scripts/ui-test-mcp-acceptance.mjs`
- Extend: `frontend-app/scripts/ui-test-mcp-server.test.mjs`

- [ ] **Step 1: Write acceptance helper tests**

Acceptance tests must import shared framing from `ui-test-mcp-framing.mjs`; no duplicate parser is allowed.

- [ ] **Step 2: Implement acceptance script**

Default behavior:
1. Start a Vite dev server on loopback with `VITE_SUPER_DOLPHIN_UI_TEST_MCP=1`. Use a deterministic default port such as `5177`; fail if unavailable unless `SUPER_DOLPHIN_UI_TEST_BASE_URL` is explicitly provided.
2. Generate a high-entropy acceptance token and mark the run as server-owned only when this script started the Vite process itself. If `SUPER_DOLPHIN_UI_TEST_BASE_URL` is provided, run read-only by default and do not set `SUPER_DOLPHIN_UI_TEST_ALLOW_SUBMIT=1`; a separate explicit `SUPER_DOLPHIN_UI_TEST_ACCEPTANCE_OWNS_UI=1` opt-in is required to test submit against a caller-provided URL.
3. Start `node scripts/ui-test-mcp-server.mjs` with `SUPER_DOLPHIN_UI_TEST_MCP=1`, the acceptance base URL, and the token. Set `SUPER_DOLPHIN_UI_TEST_ALLOW_SUBMIT=1` only for server-owned isolated acceptance.
4. The MCP server installs the isolated acceptance token before first navigation and the page harness must report `verifyIsolatedAcceptance({ token }) === { isolated: true, tokenMatched: true }`.
5. Send `initialize`, `notifications/initialized`, and `tools/list`.
6. Assert all four tools are present.
7. Call `ui_snapshot` and assert exact required fields.
8. Call `ui_frontend_logs` and assert a structured log response.
9. Call `ui_action fill_composer` with text `MCP UI test input`.
10. Call `ui_snapshot` and assert input length increased.
11. Call `ui_action submit_composer`. This must use the isolated harness path, must not click product submit, and must fail if any forbidden product runtime call is observed.
12. Call `ui_diagnostics` and fail the acceptance on non-empty `consoleErrors`, `bridgeErrors`, or `unhandledErrors`. Only explicitly injected expected warnings may be allowlisted.
13. Call `ui_frontend_logs` and require the `ui_test_mcp` action log to be present.
14. Assert no forbidden product runtime call was recorded, including thread start, turn start, provider/session acquire/resume, or Wails bridge calls that would reach the backend.
15. Send `shutdown` and `exit`; assert process cleanup.
16. After process stop, a subsequent RPC call must fail promptly.

If the script cannot start Vite, cannot launch Chromium, or cannot access the harness, the acceptance fails. It is never optional in final verification.

- [ ] **Step 3: Run acceptance helper tests**

Run:

```bash
cd frontend-app
npx vitest run scripts/ui-test-mcp-server.test.mjs --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

## Task 6: Documentation

**Files:**
- Create: `docs/dev-ui-test-mcp.md`

- [ ] **Step 1: Write docs from contract constants**

Document:
- Tool names from `UI_TEST_TOOLS`.
- Action names, targets, routes, wait states, limits, and submit side effects from `uiTestContract.js`.
- Start MCP server:

```bash
cd frontend-app
SUPER_DOLPHIN_UI_TEST_MCP=1 SUPER_DOLPHIN_UI_TEST_BASE_URL=http://127.0.0.1:5175 npm run mcp:ui-test
```

- Run deterministic acceptance:

```bash
cd frontend-app
npm run mcp:ui-test:acceptance
```

- Forbidden capabilities: no production exposure, no non-loopback URLs, no arbitrary JS eval, no arbitrary CSS selectors, no unbounded waits, no raw prompt/memory/skill/thread-message logs, no token/key/path leakage.
- Action tools are state-changing; snapshot/log/diagnostics are read-only.
- `submit_composer` is not available against product runtime. It requires explicit submit opt-in, server-owned isolated acceptance mode, token match, and send-mode readiness; it must not invoke backend, provider, thread, or turn runtime handlers.

- [ ] **Step 2: Docs check**

Run:

```bash
git diff --check
```

Expected: no whitespace errors.

## Task 7: Final Verification

**Files:**
- All changed files.

- [ ] **Step 1: Focused tests**

Run:

```bash
cd frontend-app
npx vitest run \
  src/shared/diagnostics/safeLogFields.test.js \
  src/devtools/uiTestContract.test.js \
  src/devtools/uiTestHarness.test.js \
  src/pages/chat/components/ComposerDock.test.jsx \
  scripts/ui-test-mcp-server.test.mjs \
  vite.config.test.js \
  --no-file-parallelism --maxWorkers=1
```

Expected: PASS.

- [ ] **Step 2: Required frontend validation**

Run:

```bash
cd frontend-app
npm run lint
npm test
npm run build
```

Expected: all exit 0.

- [ ] **Step 3: Production artifact scan**

After `npm run build`, run:

```bash
! rg "SUPER_DOLPHIN_UI_TEST|ui_snapshot|ui_action|ui_frontend_logs|ui_diagnostics|uiTestHarness|submitComposerInIsolation" frontend-app/dist cmd/agent-terminal/web-dist
```

Expected: no matches.

- [ ] **Step 4: Embed verification**

Run:

```bash
make frontend-embed-verify
```

Expected: PASS.

- [ ] **Step 5: Mandatory MCP acceptance**

Run:

```bash
cd frontend-app
npm run mcp:ui-test:acceptance
```

Expected: PASS. Failure to start Vite, launch Chromium, initialize MCP, read the harness, record action logs, submit in isolated mode, or clean up is a blocker.

- [ ] **Step 6: LSP diagnostics matrix**

Run LSP diagnostics on every changed JS/JSX/MJS/JSON file:

```text
frontend-app/src/shared/diagnostics/safeLogFields.js
frontend-app/src/shared/diagnostics/safeLogFields.test.js
frontend-app/src/shared/api/wailsBridge.js
frontend-app/src/devtools/uiTestContract.js
frontend-app/src/devtools/uiTestContract.test.js
frontend-app/src/devtools/uiTestHarness.js
frontend-app/src/devtools/uiTestHarness.test.js
frontend-app/src/main.jsx
frontend-app/src/pages/chat/components/ComposerMeta.jsx
frontend-app/src/pages/chat/components/ComposerDock.test.jsx
frontend-app/scripts/ui-test-mcp-framing.mjs
frontend-app/scripts/ui-test-mcp-server.mjs
frontend-app/scripts/ui-test-mcp-server.test.mjs
frontend-app/scripts/ui-test-mcp-acceptance.mjs
frontend-app/package.json
frontend-app/vite.config.js
frontend-app/vite.config.test.js
```

Every severity returned by LSP diagnostics must be fixed or documented as a blocker.

- [ ] **Step 7: Workflow/staging guard**

Run:

```bash
git -C /home/l4place/Super-Dolphin/.worktrees/ui-test-mcp-20260708 status --short --untracked-files=all
git -C /home/l4place/Super-Dolphin/.worktrees/ui-test-mcp-20260708 diff --staged --name-status
git -C /home/l4place/Super-Dolphin status --short --untracked-files=all
git diff --check
```

Expected: no unrelated files staged or modified by this task. Main worktree duplicate plan must remain absent.
