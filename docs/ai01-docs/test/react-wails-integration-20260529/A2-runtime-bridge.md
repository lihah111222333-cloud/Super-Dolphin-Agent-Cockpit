# A2 Runtime Bridge

Worker branch: `agent/a2-runtime-bridge-20260529`

Scope:
- Keep the official Wails runtime path as `/wails/runtime.js`.
- Prevent Vite dev import analysis from treating that path as a normal module dependency.
- Preserve existing Vitest virtual mocks for `/wails/runtime.js`.

TDD evidence:
- Red: `npx vitest run src/api-service.behavior.test.js src/dev-runtime-shim.test.js`
  - Failed on `api service behavior > loads the Wails runtime through a Vite-ignored module constant`.
  - Failure confirmed `src/services/api.js` still used direct `import('/wails/runtime.js')`.
- Green: changed `src/services/api.js` to load `WAILS_RUNTIME_MODULE` via `import(/* @vite-ignore */ WAILS_RUNTIME_MODULE)`.
  - Same Vitest command passed with 14 tests.

Verification:
- `npx vitest run src/api-service.behavior.test.js src/dev-runtime-shim.test.js`
- `npm run build`

Notes:
- `vite.config.js` still keeps build `rollupOptions.external: ["/wails/runtime.js"]`.
- `cmd/agent-terminal/frontend/wails/runtime.js` remains the official dev shim served by Vite.
