# A3 Backend API Contract Worker

## Scope

- Worker branch: `agent/a3-backend-api-contract-20260529`
- Worker path: `/home/ai01@f666.com/.config/superpowers/worktrees/Super-Dolphin/a3-backend-api-contract-20260529`
- Base integration worktree: `/home/ai01@f666.com/.config/superpowers/worktrees/Super-Dolphin/tmp-react-wails-integration-20260529`

## Changed Files

- `cmd/agent-terminal/frontend/src/shared/api/backendApi.js`
- `cmd/agent-terminal/frontend/src/shared/api/backendApi.test.js`
- `docs/ai01-docs/test/react-wails-integration-20260529/A3-backend-api-contract.md`

## Contract Summary

`backendApi.js` provides a visible React/Wails frontend facade over `src/services/api.js`.

- Default facade wraps `callAPI`, `getBuildInfo`, `onBridgeEvent`, and `onAppWillQuit`.
- Tests use `createBackendApi({ callAPI })` to inject a fake backend call function.
- Cwd-scoped methods fail fast when `cwd` is absent: `getProjects`, `getSidebar`, `getState`, `getDashboard`, `startThread`, and `startTurn`.
- `startTurn` requires `cwd`, `threadId`, and `input`; official non-empty array payloads are forwarded as `input`, while non-empty string compatibility input is mapped to backend `prompt`.
- `renameThread` requires `threadId` and `name`, and calls the official `thread/name/set` RPC.
- `startDag` sends only the strict dashboard `dagStart` fields: `dagKey`, `triggerSource`, and `idempotencyKey`; it does not forward `cwd`.
- Long ids such as `1234567890123456789` stay strings. The facade does not call `Number()` or `parseInt()`.

## TDD Evidence

1. Red: `npx vitest run src/shared/api/backendApi.test.js` initially failed because `./backendApi.js` did not exist.
2. Green: implemented the facade and reran the same test.
3. Refine: converted facade methods to async so fail-fast validation appears as rejected promises for async callers.
4. P1 review red: updated tests so `renameThread` must call `thread/name/set` and `startTurn` must accept non-empty array input. The old implementation failed with `thread/rename` and rejected array input.
5. P1 review green: changed the facade method name and input validator, then reran the focused vitest file.
6. Final review red: added a test proving string turn input must map to backend `prompt`; the previous implementation failed by forwarding a string as `input`.
7. Final review green: mapped string turn input to `prompt` while preserving array `input`.

## Verification

Commands required by A3:

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/shared/api/backendApi.test.js
node scripts/size-guard.cjs
```

Latest focused run before commit:

- `npx vitest run src/shared/api/backendApi.test.js`: PASS, 7 tests.
- `node scripts/size-guard.cjs`: PASS, no new size violations.

## Risk Register

- P0: None known.
- P1: None known.
- P2: None known after final string-input fix.
- P3: Facade is newly introduced and not yet wired into existing React callers; this worker intentionally limits scope to the explicit API contract files.
- P3: Method names encode current backend RPC strings in one place, reducing but not eliminating future drift if backend handlers are renamed.
