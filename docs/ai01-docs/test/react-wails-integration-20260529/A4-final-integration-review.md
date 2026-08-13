# A4 Final Integration Review

## Scope

Read-only subagent review plus local follow-up fix for `tmp/react-wails-integration-20260529` against `origin/main`.

## Verdict

No P0/P1 blockers were found for the temporary integration branch.

The requested integration goals are satisfied:

- React is mounted from the official `cmd/agent-terminal/frontend` entry.
- `/wails/runtime.js` is loaded through a Vite-ignored dynamic import constant.
- `vite.config.js` keeps `/wails/runtime.js` external for production builds.
- A backend API facade exists under `cmd/agent-terminal/frontend/src/shared/api/backendApi.js` with contract tests.
- Bootstrap sets a resolved cwd preference scope before the first sidebar refresh.

## Findings

- P0: none.
- P1: none.
- P2: final reviewer found that string `startTurn({ input: "..." })` was forwarded as string `input`; fixed by mapping string input to backend `prompt` and adding a failing-then-passing test.
- P3: `backendApi` is not yet wired into every existing production caller; it currently documents and tests the official contract surface for the React/Wails migration.

## Commands Reviewed

```bash
cd cmd/agent-terminal/frontend
npx vitest run src/shared/api/backendApi.test.js
node scripts/size-guard.cjs
npx vitest run
npm run build
cd ../../..
./scripts/test_with_guard.sh ./internal/ui/wails ./internal/module/thread ./internal/module/turn ./internal/module/uistate ./internal/module/dashboard ./internal/app -count=1
```
