# A1 Contract Review Report

## Scope

- Reviewed current integration branch after A2, A3, and A6 merges.
- Focus areas:
  - `cmd/agent-terminal/frontend/src/shared/api/**`
  - `cmd/agent-terminal/frontend/src/entities/**`
  - `cmd/agent-terminal/frontend/src/features/**`
  - `cmd/agent-terminal/frontend/src/widgets/**`

## Commands

```bash
rg -n "callAPI\\(|thread/start|turn/start|ui/state/get|ui/sidebar/get|ui/preferences/set|ui/log" cmd/agent-terminal/frontend/src
rg -n "manualSkillSelection|manual_skill_selection|deferSpawn|defer_spawn|launchIntentId|launch_intent_id" cmd/agent-terminal/frontend/src
rg -n "Number\\([^)]*(agent_id|trace_id|timestamp|_ts)|parseInt\\([^)]*(agent_id|trace_id|timestamp|_ts)" cmd/agent-terminal/frontend/src
rg -n "catch\\s*\\([^)]*\\)\\s*\\{[^\\n]*(return \\[\\]|return \\{\\}|return null)|console\\.(warn|error)" cmd/agent-terminal/frontend/src
```

## Summary

- Result: pass for the currently integrated foundation slices.
- Highest severity: none.

## Findings

- No `Number()` / `parseInt()` precision-loss pattern found for `agent_id`, `trace_id`, `timestamp`, or `_ts` under `src`.
- No direct `console.warn()` / `console.error()` business logging found under `src`.
- No silent `catch { return [] }`, `return {}`, or `return null` fallback pattern found under `src`.
- `callAPI()` exists only in `src/shared/api/rpc/callAPI.js`; higher-level feature RPC usage is not yet integrated at this checkpoint.

## Coverage Gaps

- A4 still needs to provide concrete `thread/start -> turn/start` payload evidence, including explicit `cwd`, `deferSpawn:true`, and `manualSkillSelection:false`.
- React app bootstrap is not yet implemented, so `ui/state/get`, `ui/sidebar/get`, and runtime event subscription coverage remains for later integration.
