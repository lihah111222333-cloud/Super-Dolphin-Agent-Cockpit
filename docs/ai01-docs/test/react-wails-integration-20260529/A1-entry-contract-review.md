# A1 Entry / Contract Review

## Scope

Read-only review of official Wails React frontend entry, Wails runtime bridge, and key backend RPC contracts.

## Files Reviewed

- `cmd/agent-terminal/frontend/index.html`
- `cmd/agent-terminal/frontend/src/main.jsx`
- `cmd/agent-terminal/frontend/src/App.jsx`
- `cmd/agent-terminal/frontend/src/services/api.js`
- `cmd/agent-terminal/frontend/wails/runtime.js`
- `cmd/agent-terminal/frontend/vite.config.js`
- `cmd/agent-terminal/frontend.go`
- `internal/ui/wails/binding.go`
- `internal/module/thread/*`
- `internal/module/turn/*`
- `internal/module/uistate/*`
- `internal/module/dashboard/*`

## Findings

- P0: none.
- P1: none.
- P2: initial bootstrap can call `threadStore.refreshSidebarState()` before `threadStore.setPreferenceScopeCwd()` receives the resolved window/process cwd, causing a possible first `ui/sidebar/get` without explicit `cwd`.
- P3: dev runtime setup uses both `FRONTEND_DEVSERVER_URL` and `VITE_DEV_URL`; this is easy to misconfigure.

## Entry Evidence

- The desktop app uses `cmd/agent-terminal/frontend/dist` through `cmd/agent-terminal/frontend.go`.
- `cmd/agent-terminal/frontend/index.html` loads `./src/main.jsx`.
- `src/main.jsx` mounts React `AppRoot`.
- Root-level `frontend/` is not part of Wails embed/serve.

## Contract Evidence

- `thread/start`: frontend sends explicit `cwd`; `deferSpawn` is converted to `defer_spawn`.
- `turn/start`: frontend sends `threadId`, `input`, and explicit `cwd` when scoped.
- `ui/state/get`, `ui/sidebar/get`, `ui/projects/get`: scoped calls carry `cwd`.
- `ui/windowBootstrap/get`: frontend sends `{}` and reads backend bootstrap snapshot.
- `dashboard/dagStart`: strict payload uses `dagKey`, `triggerSource`, and `idempotencyKey`.
- `thread/name/set`: official rename method is `thread/name/set`, not `thread/rename`.

## Commands

No commands were run by A1; this was a read-only review.

## Recommended Verification

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run
npm run build
```

```bash
./scripts/test_with_guard.sh ./internal/ui/wails ./internal/module/thread ./internal/module/turn ./internal/module/uistate ./internal/module/dashboard ./internal/app -count=1
```
