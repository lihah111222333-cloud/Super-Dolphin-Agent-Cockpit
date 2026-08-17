# Observability Tracing Manual Smoke

Run this after Task 10 is merged into the integration branch.

## Preconditions

- Build/run the desktop app from the integration worktree.
- Use the React frontend in `frontend-app`.
- Do not enable any PostgreSQL or SQLite tracing dependency; Phase 1 must use memory + JSONL only.

## Checklist

1. Start the app normally; tracing is enabled in safe mode by default. Set `OBS_TRACING_ENABLED=0` only when intentionally disabling tracing.
2. Send one chat message that reaches `turn/start` and causes at least one provider/tool path or normal turn lifecycle event.
3. Locate `~/.super-dolphin/log/<project>/traces/trace-YYYY-MM-DD.jsonl`.
4. Confirm the trace directory and JSONL file are owner-only where the OS supports permissions.
5. Pick one trace id and confirm frontend, Wails/RPC, turn/thread, provider/toolbridge, and bus/UI-state events can be correlated by `trace_id`, `thread_id`, `turn_id`, `agent_id`, `call_id`, or `tool_name`.
6. Open the “链路追踪” dashboard and query the trace id.
7. Confirm the UI shows `source` (`memory`, `jsonl_tail`, or `mixed`), `truncated`, slow/error summaries, and at least one code anchor (`file:line function`) for RPC/tool/error paths.
8. Confirm the JSONL contains no prompt text, full payloads, raw tool arguments, API keys, bearer tokens, or model output bodies.
9. Corrupt a copied JSONL fixture by appending one malformed line; verify the tail reader still returns valid earlier events and does not crash the query path.
10. Query `observability/status` and confirm enabled/disabled state plus sink/index counters are visible.

## Pass criteria

The smoke passes when an agent can start from a trace id and identify the likely slow/error code region without reading PostgreSQL tables, SQLite files, prompt payloads, or full tool/model payloads.
