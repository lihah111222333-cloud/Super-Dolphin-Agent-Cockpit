# A2 Shared API / Wire Test Report

Date: 2026-05-29
Branch: `agent/a2-shared-api-wire-20260529`
Worktree: `/home/ai01@f666.com/.config/superpowers/worktrees/Super-Dolphin/a2-shared-api-wire-20260529`

## Scope

- Added shared RPC wrapper tests and implementation under `cmd/agent-terminal/frontend/src/shared/api/rpc`.
- Added runtime event subscription wrapper under `cmd/agent-terminal/frontend/src/shared/api/events`.
- Added wire ID and assertion helpers under `cmd/agent-terminal/frontend/src/shared/lib`.

## TDD Evidence

- RED: `npx vitest run src/shared/api/rpc/callAPI.test.js src/shared/lib/wire/ids.test.js`
  - Failed because `./callAPI.js` and `./ids.js` were missing.
- GREEN: same command passed after adding the minimal shared modules.

## Coverage

- `callAPI(method, params)` rejects arrays, strings, and other non-object params before touching the Wails bridge.
- `callAPI()` injects a string `requestId` into every bridge payload and preserves an incoming `operationId`.
- RPC failures rethrow the original root cause and record `rpc.failed` through an optional logger.
- `validateObjectResponse()` is exported for fail-fast unknown/non-object response shape validation.
- `requireStringId()` keeps 19-digit numeric strings as strings.
- `assertSafeInteger()` rejects unsafe integers.
- `onBridgeEvent()` and `onAgentEvent()` subscribe to `bridge-event` and `agent-event` and pass through payloads unchanged.

## Validation

```bash
npx vitest run src/shared/api/rpc/callAPI.test.js src/shared/lib/wire/ids.test.js
node scripts/size-guard.cjs
```

Result:

- Vitest: 2 files passed, 9 tests passed.
- Size guard: passed, 306 files scanned, no new over-limit files.

## Concerns

- The new shared RPC layer is not wired into React feature code yet; this report only covers the A2 shared API/wire slice.
