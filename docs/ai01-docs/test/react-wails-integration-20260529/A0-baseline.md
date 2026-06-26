# A0 Baseline

## Scope

- Integration branch: `tmp/react-wails-integration-20260529`
- Integration worktree: `/home/ai01@f666.com/.config/superpowers/worktrees/Super-Dolphin/tmp-react-wails-integration-20260529`
- Base: `origin/main` at `bdf43d3cf3ce69fcae5454cd7137bc7b828be4d8`
- Official frontend package: `cmd/agent-terminal/frontend`
- Non-production prototype directory: root `frontend/` in the main checkout is not part of the Wails desktop embed path.

## Entry Evidence

- `cmd/agent-terminal/frontend/index.html` mounts `./src/main.jsx`.
- `cmd/agent-terminal/frontend/src/main.jsx` renders React `AppRoot`.
- `cmd/agent-terminal/frontend/vite.config.js` builds to `cmd/agent-terminal/frontend/dist`.
- `cmd/agent-terminal/frontend.go` embeds `cmd/agent-terminal/frontend/dist`.

## Baseline Commands

```bash
cd cmd/agent-terminal/frontend
node scripts/size-guard.cjs
npx vitest run src/dev-runtime-shim.test.js src/api-service.behavior.test.js src/app-root-react.bridge.test.jsx src/app-root.behavior.test.js
npm run build
```

## Baseline Result

- `node scripts/size-guard.cjs`: pass, 353 files scanned.
- Focused Vitest gate: pass, 4 files / 42 tests.
- `npm run build`: pass. Vite emitted existing large chunk and mixed dynamic/static import warnings, but exited 0.

## Agent Orchestration Note

The `mcp-go-agent-orchestration` DAG tools were not exposed in this Codex tool session. This run uses the allowed native path: isolated git worktrees plus `multi_agent_v1` subagents and per-agent Markdown reports for lifecycle evidence, without persistent mcp-orch DAG observability.

## Findings

- P0: none at baseline.
- P1: none at baseline.
- P2: frontend lacks a single explicit `shared/api` facade documenting the official backend RPC surface for React callers.
- P2: runtime import should be regression-locked so future `src/shared/api/wailsBridge.js`-style static imports cannot reintroduce the Vite pre-transform failure.
