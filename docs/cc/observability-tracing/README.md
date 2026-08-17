# Observability Tracing Phase 1

Phase 1 provides local, bounded chain tracing for frontend → Wails/RPC → thread/turn → provider/toolbridge → event bus/UI state. It is designed for agent debugging: start from a trace id, find the slowest/error span, then jump to file/function/line anchors or compact stack frames.

## Storage model

- Primary durable storage is JSONL under `~/.super-dolphin/log/<project>/traces/trace-YYYY-MM-DD.jsonl`.
- Hot queries use an in-memory bounded index; misses can scan a bounded recent JSONL tail.
- Query responses label their source as `memory`, `jsonl_tail`, or `mixed` and expose `truncated` when the bounded result omitted older matches.
- Phase 1 intentionally does not add PostgreSQL tables, sqlc queries, migrations, or SQLite dependencies.

## RPC and UI surface

Registered RPC methods:

- `observability/status {}`
- `observability/trace/get { traceId, limit?, includeTail? }`
- `observability/thread/recent { threadId, limit?, includeTail? }`
- `observability/slow/list { limit?, component? }`
- `observability/error/list { limit?, component? }`
- `observability/frontend/ingest { events }`

The React `frontend-app` exposes a minimal “链路追踪” dashboard. It can query by trace id, query recent thread events, list slow events, list errors, show source/truncation, and display code anchors as `file:line function`.

## Privacy and payload rules

- Events are allowlisted and sanitized before memory index and JSONL writes.
- Frontend ingest rejects unknown raw fields instead of persisting arbitrary UI log payloads.
- RPC events keep parameter keys and byte counts, not full parameter values.
- Tool/provider events keep summaries, result sizes, affected file counts, and compact stack/code anchors; full prompts, tool payloads, and raw model output must not be persisted.

## Configuration and guardrails

Tracing is enabled in safe mode by default. Set `OBS_TRACING_ENABLED=0` to explicitly disable it, or `OBS_TRACE_DEBUG=true` to raise debug-time index bounds. Other `OBS_*` bounds are validated in `internal/platform/observability/config.go`.

Operational guardrails:

- JSONL file size and retention are bounded.
- Tail fallback has max bytes, timeout, cache, and concurrency limits.
- In-memory index sizes are bounded by trace/thread/slow/error caps.
- Trace write failures on user-facing Wails/RPC paths are best-effort and logged, not allowed to block the main action.
- Malformed JSONL lines are skipped so earlier valid events remain queryable.

## Agent debugging flow

1. Copy a trace id from the frontend log, RPC log, JSONL, or dashboard.
2. Open the “链路追踪” page and query the trace id.
3. Sort mentally by `slowest_events` / `duration_ms` and inspect `errors`.
4. Use `code.file`, `code.function`, `code.line`, and stack frames to jump to the likely code area with LSP tools.
5. Correlate `thread_id`, `turn_id`, `agent_id`, `call_id`, and `tool_name` for follow-up queries.
