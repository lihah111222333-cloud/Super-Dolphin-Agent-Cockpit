# Observability Tracing Implementation Plan

> Status: draft for cross-review
> Owner: main agent
> Scope: full frontend/backend tracing for agent-assisted debugging, with Phase 1 JSONL persistence and no PostgreSQL expansion.

## 0. Decision Summary

We will implement a full tracing/observability chain, but Phase 1 must not expand PostgreSQL or introduce SQLite as a new packaging dependency.

Phase 1 uses:

- append-only JSONL trace files as the durable source of truth;
- bounded in-memory indexes for fast recent queries;
- structured trace events with code anchors so agents can jump to the relevant file/function quickly;
- low-volume default logging with richer stack traces only for slow/error/panic cases;
- an `observability` abstraction so the same instrumentation can later swap JSONL for SQLite.

Hard constraints:

1. No new PostgreSQL migrations.
2. No new `sql/queries/*.sql` for tracing.
3. No `sqlc` expansion for tracing.
4. No SQLite driver in Phase 1.
5. No prompt/file/tool-result full payload logging.
6. All indexes must have hard memory limits.
7. Dashboard must query the observability service, not PG-specific tables.
8. Tracing startup must be explicit: enabled tracing fails fast on invalid wiring/config; disabled tracing binds an explicit disabled service.
9. JSONL files must have retention caps and owner-only permissions.
10. Every persisted string field must pass the same sanitizer before it reaches JSONL or the in-memory index.

## 1. Goal

Build an application-level tracing system that lets a human or agent answer these questions within seconds:

- Which user action produced this failure or latency?
- Which stage was slow: frontend, Wails bridge, RPC dispatch, thread, turn, provider, tool, UI projection, or frontend patch/render?
- Which code area should the agent inspect first?
- Which thread/agent/tool call was involved?
- What compact stack is relevant for a slow/error path?

The output should be useful for agent repair loops. A trace result must contain enough code anchors to guide LSP navigation without requiring the agent to infer from free-form logs.

## 2. Non-Goals

- Do not build a complete OpenTelemetry implementation.
- Do not add remote telemetry/export.
- Do not add a generic analytics warehouse.
- Do not persist large payloads, prompts, model responses, file contents, base64 images, or full tool results.
- Do not solve the whole PostgreSQL-to-SQLite migration.
- Do not add a SQLite dependency just for tracing in Phase 1.

## 3. Target User Experience

### 3.1 Dashboard / Agent Query Output

A trace query should return a compact waterfall plus code anchors:

```text
Trace: 4bf92f3577b34da6a3ce929d0e0e4736
Method: turn/start
Thread: thread_123
Agent: agent_456
Total: 10.7s
Status: slow

[12ms]   frontend.rpc.call          frontend-app/src/shared/api/wailsBridge.js:252 callAPI
[8ms]    wails.call_api             internal/ui/wails/binding.go:43 App.CallAPI
[41ms]   backend.rpc.dispatch       internal/platform/rpc/server.go:266 Dispatch
[96ms]   turn.prepare               internal/module/turn/service.go:177 PrepareTurn
[1250ms] provider.session.ready     internal/provider/.../session.go acquireSession  SLOW
[8430ms] provider.turn.run          internal/provider/.../session.go StartTurn       SLOW
[620ms]  tool.call                  internal/platform/toolbridge/handler.go ...
[18ms]   uistate.patch              internal/module/uistate/patch.go threadPatchLocked
[34ms]   frontend.patch.apply       frontend-app/src/entities/client/model/useClientStore.js:1880 applyBridgePatch
```

When a node is slow or failed, the event can include a compact stack:

```text
stack:
  internal/provider/codexapp/session.go:188 StartTurn
  internal/module/turn/service.go:224 StartTurn
  internal/platform/rpc/server.go:284 Dispatch
```

### 3.2 JSONL File Output

Trace files live under an observability-owned `traces/` subdirectory inside the existing project log directory:

```text
~/.super-dolphin/log/<project>/traces/trace-YYYY-MM-DD.jsonl
```

Do not write trace JSONL directly into `~/.super-dolphin/log/<project>/`; ordinary app logs use different permissions and retention behavior.

Each line is one event:

```json
{"ts":"2026-06-01T10:00:00.123Z","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","parent_span_id":"","span_name":"backend.rpc.dispatch","component":"rpc","phase":"done","method":"turn/start","duration_ms":41,"status":"ok","code":{"file":"internal/platform/rpc/server.go","function":"Dispatch","line":266},"thread_id":"thread_123","agent_id":"","metadata":{"param_keys":["threadId","input","cwd"],"param_bytes":512}}
```

## 4. Architecture

```text
frontend-app callAPI
  -> Wails App.CallAPI
  -> rpc.Server.Dispatch
  -> module thread/turn/provider/tool/uistate
  -> frontend event/patch handlers

Every boundary emits observability.TraceEvent through:

  internal/platform/observability.Service
    -> JSONLSink append-only writer
    -> bounded in-memory Index
    -> Dashboard Query API
```

### 4.1 Package Layout

Create:

```text
internal/platform/observability/
  event.go          // TraceEvent, CodeAnchor, StackFrame, status constants
  context.go        // trace/span context helpers
  service.go        // Service Write/Query facade
  sink.go           // Sink interface and multi-sink composition
  jsonl_sink.go     // append-only JSONL writer with rotation hooks
  index.go          // bounded recent in-memory indexes
  sampler.go        // event classification and drop/keep rules
  stack.go          // compact stack capture for slow/error/panic
  sanitizer.go      // one sanitizer for every persisted string field
  code_anchor.go    // helper constructors for static code anchors
  config.go         // OBS_* parsing and fail-fast validation
  module.go         // fx module wiring for required/disabled service

internal/module/observability/
  module.go         // thin app/module adapter; registers RPC handlers
  rpc.go            // observability/* methods; no storage implementation
```

Do not create PG-specific store code for tracing. Keep `internal/platform/observability` limited to types, service, sinks, indexes, config, and sanitization. RPC ownership belongs to `internal/module/observability` so platform infrastructure does not depend upward on application RPC registration.

App wiring must be explicit. `internal/app/modules.go` must add both `internal/platform/observability.Module` and `internal/module/observability.Module`; new Fx modules are not auto-discovered. `internal/module/observability` must return `rpc.HandlerMapResult` for `observability/*` methods so `rpc.registerAllHandlers` registers them through the existing `group:"rpc_handlers"` aggregation.

### 4.2 Frontend Target Reality

Phase 1 frontend instrumentation targets the React frontend under `frontend-app/`, not the legacy/current Vue frontend under `cmd/agent-terminal/frontend/vue-app/`.

Confirmed React frontend capabilities:

- `frontend-app/package.json` uses React, React DOM, Zustand, and Vite.
- `frontend-app/src/main.jsx` mounts `<App />` with `react-dom/client`.
- `frontend-app/src/shared/api/wailsBridge.js` already creates W3C trace metadata in `createTraceContext()`.
- `callAPI()` injects `_aoTraceparent`, `_aoTraceId`, `_aoSpanId`, `_aoRequestId`, `_aoClientKind`, and `_aoClientRoute`.
- Wails parses `_aoTraceparent` in `internal/ui/wails/binding.go:199 frontendTraceContext` and attaches `trace_id`/`span_id` to the backend context.

Not currently present in React and therefore must be implemented, not assumed:

- remote frontend trace flushing;
- `frontend.patch.apply.slow` timing around `applyBridgePatch`;
- `frontend.render.slow` timing/profiling;
- sanitizer/allowlist before any frontend-originated trace/log field reaches JSONL.

The old Vue frontend has a different log bridge (`cmd/agent-terminal/frontend/vue-app/services/log.js` + `registerLogBridgeSink`). Do not use that old bridge as evidence that React frontend logs already flush remotely. Only instrument the Vue path if the frontend migration is delayed and the packaged app still depends on Vue during Phase 1.

### 4.3 Core Data Model

```go
type TraceEvent struct {
    SchemaVersion int            `json:"schema_version"`
    TS            time.Time      `json:"ts"`
    TraceID       string         `json:"trace_id"`
    SpanID        string         `json:"span_id,omitempty"`
    ParentSpanID  string         `json:"parent_span_id,omitempty"`
    SpanName      string         `json:"span_name"`
    Component     string         `json:"component"`
    Phase         string         `json:"phase,omitempty"`
    Method        string         `json:"method,omitempty"`
    ThreadID      string         `json:"thread_id,omitempty"`
    AgentID       string         `json:"agent_id,omitempty"`
    TurnID        string         `json:"turn_id,omitempty"`
    CallID        string         `json:"call_id,omitempty"`
    ToolName      string         `json:"tool_name,omitempty"`
    ClientKind    string         `json:"client_kind,omitempty"`
    ClientRoute   string         `json:"client_route,omitempty"`
    DurationMS    int64          `json:"duration_ms,omitempty"`
    Status        string         `json:"status"`
    Error         string         `json:"error,omitempty"`
    Code          CodeAnchor     `json:"code,omitempty"`
    Stack         []StackFrame   `json:"stack,omitempty"`
    Metadata      map[string]any `json:"metadata,omitempty"`
}

type CodeAnchor struct {
    File     string `json:"file,omitempty"`
    Function string `json:"function,omitempty"`
    Line     int    `json:"line,omitempty"`
}

type StackFrame struct {
    File     string `json:"file"`
    Function string `json:"function"`
    Line     int    `json:"line"`
}
```

Status values:

```text
ok | slow | error | panic | sampled | dropped_summary
```

Schema and sanitizer rules:

- `SchemaVersion` starts at `1` and is mandatory in JSONL so future SQLite migration can read mixed historical files safely.
- Every top-level string field, every `CodeAnchor`/`StackFrame` string, every error string, and every metadata string must pass the same sanitizer before indexing or writing.
- Metadata values are limited to `string`, `bool`, finite numbers, `[]string`, `[]int64`, and shallow `map[string]string` for Phase 1. Arbitrary nested objects must be converted to bounded JSON strings or dropped with a `metadata_dropped=true` marker.
- Query-critical fields must remain typed top-level fields; metadata is for bounded extras only.

### 4.4 Storage Strategy

Phase 1 durable storage:

```text
JSONL only
```

Phase 1 fast query:

```text
bounded memory index only
```

Future storage:

```text
SQLiteSink implementing the same Sink interface
```

No Phase 1 SQLite driver. This avoids packaging and notarization risk and keeps the tracing work independent from the main database migration.

## 5. Bounded Index Design

The index is a cache, not the durable source of truth.

Default limits:

```text
OBS_INDEX_MAX_EVENTS=5000
OBS_INDEX_MAX_TRACE_EVENTS=128
OBS_INDEX_MAX_THREAD_EVENTS=256
OBS_INDEX_MAX_SLOW_EVENTS=500
OBS_INDEX_MAX_ERROR_EVENTS=500
OBS_EVENT_MAX_BYTES=8192
OBS_METADATA_MAX_BYTES=4096
OBS_STACK_MAX_FRAMES=12
OBS_STACK_MAX_BYTES=8192
```

Debug-mode limits:

```text
OBS_INDEX_MAX_EVENTS=20000
OBS_INDEX_MAX_TRACE_EVENTS=256
OBS_INDEX_MAX_THREAD_EVENTS=512
```

Required behavior:

- global ring evicts oldest events;
- per-trace and per-thread rings evict oldest entries;
- slow/error indexes are capped independently;
- if metadata is too large, truncate it and add `metadata_truncated=true`;
- if any string field is too large or contains secret-like content, sanitize it before it reaches the index or JSONL sink;
- if serialized event is too large, truncate metadata/stack/error/preview fields first, then drop optional fields;
- never keep unbounded maps for old trace IDs;
- when the global ring evicts an event, remove its references from secondary indexes or tolerate stale references with safe lookup filtering;
- reject unsafe configured limits at startup; do not allow zero, negative, or unreasonably large bounds to disable the hard caps.

## 6. Sampling and Log Volume Rules

### 6.1 Always Keep

- RPC done/failed events.
- Slow RPC events.
- Errors and panics.
- Thread start/stop.
- Turn start/completed/stalled/interrupted.
- Tool call begin/end when status is error or slow.
- Approval requested/resolved.

### 6.2 Keep Conditionally

- Tool begin/end success under threshold: sample 1/N or keep summary only.
- UI projection/timeline events: debug only or aggregate counts.
- Frontend render/patch timings: keep only slow events by default.

### 6.3 Do Not Keep Detailed Events By Default

- token deltas;
- streaming output deltas;
- repeated heartbeat/keepalive;
- high-frequency sidebar refresh probes.

Instead emit periodic summaries:

```json
{"span_name":"turn.output.summary","metadata":{"delta_count":531,"bytes":120392}}
```

### 6.4 Payload Safety

Forbidden in trace events:

- full prompt text;
- full model response;
- full file contents;
- full tool result;
- frontend `result_preview` / backend response preview;
- user message text from frontend timeline or patch logs;
- base64 images;
- secrets or auth tokens;
- arbitrary environment dumps.

Allowed:

- param keys;
- byte counts;
- item counts;
- short preview under 256 bytes only for non-sensitive identifiers;
- stable hash;
- file path if already visible to the user/project;
- code anchor;
- status and timing.

Sanitization is mandatory for all persisted strings, not only metadata. This includes `Error`, `Method`, `ClientRoute`, `ToolName`, stack function names, code anchor file paths, and every string nested in metadata. The sanitizer must apply max-byte limits, multiline normalization, and secret-pattern redaction before the event is added to the in-memory index or written to JSONL.

## 7. Instrumentation Points

### 7.1 React Frontend

Files:

```text
frontend-app/src/shared/api/wailsBridge.js
frontend-app/src/entities/client/model/useClientStore.js
frontend-app/src/shared/api/backendApi.js
frontend-app/src/App.jsx
frontend-app/src/main.jsx
```

Existing facts:

- `wailsBridge.js` already creates W3C trace metadata in `createTraceContext()`.
- `callAPI()` already injects `_aoTraceparent`, `_aoTraceId`, `_aoSpanId`, `_aoRequestId`, `_aoClientKind`, and `_aoClientRoute`.
- `registerBridgeLogStore()` currently writes bridge logs into the Zustand client store only.
- `sendFrontendLogBatch()` exists but is only exported; no production React caller currently flushes bridge logs remotely.
- `useClientStore.js` applies backend patches in `applyBridgePatch`, but does not currently measure patch duration.
- `App.jsx` does not currently emit render performance events.

Events to implement:

- `frontend.rpc.start`
- `frontend.rpc.done`
- `frontend.rpc.failed`
- `frontend.patch.apply.slow`
- `frontend.render.slow` only after a stable threshold/profiler path exists
- `frontend.trace.flush.failed` as local-only warning, not recursive trace storm

Requirements:

- Reuse existing W3C trace generation in `wailsBridge.js`; do not generate a second unrelated trace id for the same RPC.
- Include `trace_id`, `span_id`, `traceparent`, `req_id`, `client_kind`, `client_route`.
- Implement remote frontend trace flushing explicitly for React; do not assume the old Vue `registerLogBridgeSink` queue exists.
- Prefer a dedicated `observability/frontend/ingest` RPC or an equally strict sanitized ingest path over raw `ui/log` reuse.
- Keep debug/info events local by default; send only warn/error/slow trace-worthy frontend events unless debug tracing is enabled.
- Never persist `result_preview`, prompt text, user message text, file contents, tool results, or raw error stacks from frontend fields.
- Trace flush failure must remain local-only and must not recursively enqueue another remote log.

### 7.2 Wails Binding

Files:

```text
internal/ui/wails/binding.go
internal/ui/wails/rpc.go
```

Events:

- `wails.call_api.start`
- `wails.call_api.done`
- `wails.call_api.failed`
- `frontend.log.ingested`

Requirements:

- Continue parsing `_aoTraceparent` in `frontendTraceContext`.
- Strip frontend trace metadata before strict RPC handlers.
- Emit code anchor `internal/ui/wails/binding.go:43 App.CallAPI`.
- Do not include full raw params; include method, param length, param keys when safe.

### 7.3 RPC Dispatch

Files:

```text
internal/platform/rpc/server.go
internal/platform/rpc/handler.go
```

Events:

- `backend.rpc.dispatch.start`
- `backend.rpc.dispatch.done`
- `backend.rpc.dispatch.failed`

Requirements:

- Instrument `Server.Dispatch` as the central local call boundary.
- Include method, trace id, duration, status, code anchor.
- RPC trace events must not reuse `rpcParamPreview`, `ParamsPreview`, `params_preview`, or any raw params preview from the existing RPC request tracker.
- RPC trace metadata is limited to sanitized `method`, `param_keys`, `param_bytes`, correlation IDs, duration/status, and code anchor.
- Slow thresholds:
  - `ui/state/get`, `ui/sidebar/get`: 300ms;
  - `thread/start`, `turn/start`: 1000ms;
  - default: 500ms.
- Avoid double-counting by making handler-level tracing optional or lower priority.

### 7.4 Thread / Turn

Files:

```text
internal/module/thread/rpc.go
internal/module/thread/start_session.go
internal/module/thread/start_session_helpers.go
internal/module/turn/rpc_helpers.go
internal/module/turn/service.go
internal/module/turn/prompt_assembly.go
internal/module/turn/manifest.go
internal/module/turn/tracker.go
```

Events:

- `thread.start`
- `thread.spawn_if_needed`
- `turn.ready_wait`
- `turn.prepare`
- `turn.assembly`
- `turn.start`
- `turn.watch.completed`
- `turn.interrupt`

Requirements:

- Focus on stage duration and code anchors.
- Do not log assembled prompts or memory contents.
- For `turn.prepare`, record counts: input item count, file count, image count, skill count, manifest tool count.

### 7.5 Provider / Tool Bridge

Files:

```text
internal/provider/codexapp/**
internal/provider/claudecli/**
internal/provider/unified/**
internal/platform/toolbridge/**
internal/platform/difftracker/**
```

Events:

- `provider.session.acquire`
- `provider.session.ready`
- `provider.turn.run`
- `tool.call.begin`
- `tool.call.end`
- `tool.diff.emit`

Requirements:

- Keep provider-specific code anchors.
- Tool results must be summarized only: success, elapsed, result bytes, truncated, affected files count.
- Capture compact stack only for slow/error tool paths.

### 7.6 Event Bus / UI State

Files:

```text
internal/platform/bus/sink.go
internal/module/uistate/projector.go
internal/module/uistate/patch.go
internal/module/uistate/patch_timeline.go
internal/module/uistate/timeline/projector.go
```

Events:

- `bus.event.lifecycle`
- `uistate.patch.emit`
- `uistate.timeline.append`
- `uistate.projection.updated`

Requirements:

- Extract structured fields from DTO events: thread, agent, turn, call, tool.
- Keep high-frequency events debug/sampled.
- Prefer summaries for token/output streams.

## 8. Dashboard Query API

Dashboard must call observability service APIs, not PG tables.

Proposed RPC methods:

```text
observability/trace/get       { traceId }
observability/thread/recent   { threadId, limit }
observability/slow/list       { limit, component }
observability/error/list      { limit, component }
observability/status          {}
```

Response shape:

```go
type TraceQueryResult struct {
    TraceID      string       `json:"traceId"`
    Events       []TraceEvent `json:"events"`
    Slowest      []TraceEvent `json:"slowest"`
    Errors       []TraceEvent `json:"errors"`
    TotalMS      int64        `json:"totalMs"`
    Truncated    bool         `json:"truncated"`
    Source       string       `json:"source"` // memory|jsonl_tail|mixed
}
```

Fallback behavior:

- First query memory index.
- If not found and enabled, scan only the tail of recent JSONL files up to `OBS_JSONL_QUERY_TAIL_MB`, default 20MB.
- Never scan unbounded historical files on UI request.
- Tail scans must use a short context deadline, default 750ms, and return partial results with `truncated=true` when the budget is exceeded.
- Use `singleflight` or equivalent per file/range so concurrent UI requests do not repeatedly decode the same 20MB tail.
- Keep a small LRU cache of recent trace-id tail results and cap concurrent tail scans, default 1.
- Malformed JSONL lines must not fail the whole query; skip malformed final lines, count decode errors, and return valid events with warning metadata.

## 9. JSONL Writer and Rotation

Writer requirements:

- single process owns writes;
- append-only line writes;
- create `~/.super-dolphin/log/<project>/traces/` from `internal/platform/observability`, not through `pkg/logger.InitWithFile`;
- create the `traces/` directory with `0700` permissions on Unix-like platforms where supported;
- create trace files inside `traces/` with `0600` permissions on Unix-like platforms and preserve secure permissions across rotation;
- `json.Encoder` or pre-marshaled bytes plus newline;
- mutex or one writer actor to serialize writes;
- best-effort flush on shutdown;
- daily file name by default;
- rotate by size if `OBS_JSONL_MAX_FILE_MB` is reached, default 64MB;
- enforce retention in Phase 1: default max age 14 days and max total trace bytes 512MB per project `traces/` directory, configurable with validated bounds;
- prune only files matching exact `trace-*.jsonl` names under the project `traces/` directory; never prune the parent project log directory;
- tolerate corrupt or partial trailing lines when reading; never rewrite a live file to repair it;
- compression is out of scope for Phase 1.

Multi-process rule:

- Sidecars do not write their own trace files by default.
- Sidecars should relay logs/events to the control process, which writes JSONL.
- If a sidecar cannot relay, it may write local fallback logs using the existing fallback mechanism, but those are not part of the primary trace index.

## 10. Stack and Code Anchor Policy

### 10.1 Code Anchor Default

Every important event must include a code anchor:

```go
observability.Anchor("internal/module/turn/service.go", "PrepareTurn", 116)
```

Anchor policy:

- `file + function` is the stable identity.
- `line` is best-effort and may drift after source edits.
- Static anchors are preferred because they are cheap, but they must be validated.
- Add tests or a generator that verifies every static anchor resolves to the named function in the current source tree.
- For slow/error stack-derived anchors, prefer runtime caller frames because they provide current line numbers.
- Dashboard should render file/function even when line validation fails, and mark the line as stale rather than hiding the anchor.

### 10.2 Stack Capture

Capture stack only when:

- status is `error`;
- status is `panic`;
- duration exceeds slow threshold;
- debug tracing explicitly enables stacks.

Stack rules:

- max frames: 12;
- max serialized bytes: 8KB;
- skip runtime/logging/internal observability frames when possible;
- prefer application frames under repo root;
- store file, function, line only.

## 11. Configuration

Environment variables:

```text
OBS_TRACING_ENABLED=<unset|1|0>   # unset/1 = safe tracing enabled; 0 = explicitly disabled
OBS_TRACE_DEBUG=0
OBS_TRACE_STACKS=slow,error,panic
OBS_INDEX_MAX_EVENTS=5000
OBS_INDEX_MAX_TRACE_EVENTS=128
OBS_INDEX_MAX_THREAD_EVENTS=256
OBS_EVENT_MAX_BYTES=8192
OBS_METADATA_MAX_BYTES=4096
OBS_JSONL_MAX_FILE_MB=64
OBS_JSONL_QUERY_TAIL_MB=20
OBS_JSONL_RETENTION_DAYS=14
OBS_JSONL_RETENTION_MAX_MB=512
OBS_QUERY_TAIL_TIMEOUT_MS=750
OBS_QUERY_TAIL_MAX_CONCURRENCY=1
OBS_SLOW_RPC_DEFAULT_MS=500
OBS_SLOW_RPC_UI_STATE_MS=300
OBS_SLOW_RPC_TURN_START_MS=1000
```

Defaults should be safe for packaged app users: tracing runs in safe mode when `OBS_TRACING_ENABLED` is absent or `1`, and is disabled only when `OBS_TRACING_ENABLED=0` is set.

Validation requirements:

- All OBS size/count/duration values must have documented min/max bounds.
- Invalid, zero, negative, or extreme values must fail startup when tracing is enabled.
- If tracing is disabled, the disabled service should expose config/status explaining that tracing is off.
- Do not silently clamp unsafe values because that hides deployment mistakes.

## 12. Implementation Tasks

### Task 1: Observability Core

Files:

```text
Create: internal/platform/observability/event.go
Create: internal/platform/observability/context.go
Create: internal/platform/observability/sink.go
Create: internal/platform/observability/service.go
Create: internal/platform/observability/index.go
Create: internal/platform/observability/sampler.go
Create: internal/platform/observability/jsonl_sink.go
Create: internal/platform/observability/stack.go
Create: internal/platform/observability/sanitizer.go
Create: internal/platform/observability/code_anchor.go
Create: internal/platform/observability/config.go
Create: internal/platform/observability/module.go
Modify: internal/app/modules.go              // add platform observability module when wiring the app graph
```

Acceptance:

- Unit tests prove event truncation, string sanitization, config validation, bounded index eviction, JSONL append, malformed-line tolerance, secure file modes, retention pruning, and query by trace/thread/slow/error.
- Enabled tracing fails fast on construction/config/wiring failure.
- Disabled tracing binds an explicit disabled service with status metadata.
- No PG or SQLite imports.
- `internal/platform/observability.Module` is available for explicit app graph wiring and does not register application RPC handlers itself.

### Task 2: Wails and RPC Instrumentation

Files:

```text
Modify: internal/ui/wails/binding.go
Modify: internal/ui/wails/rpc.go
Modify: internal/platform/rpc/server.go
Modify: internal/platform/rpc/handler.go
Modify: pkg/logger/fields.go
```

Acceptance:

- Existing traceparent tests still pass.
- RPC traces include method, trace, duration, code anchor.
- Invalid traceparent still fails fast.
- No full params logged.
- Trace events do not reuse `rpcParamPreview`, `ParamsPreview`, `params_preview`, or any raw params preview field.
- Add a regression test where failed RPC params contain prompt/user text and assert trace JSONL contains neither that text nor `params_preview`.

### Task 3: React Frontend Trace Emitter

Files:

```text
Modify: frontend-app/src/shared/api/wailsBridge.js
Modify: frontend-app/src/entities/client/model/useClientStore.js
Modify: frontend-app/src/shared/api/backendApi.js
Modify: frontend-app/src/App.jsx
Modify: frontend-app/src/main.jsx only if render/profiler wiring requires it
```

Acceptance:

- Existing `callAPI()` W3C trace metadata still matches Wails/backend trace context.
- React remote frontend trace flushing is explicitly implemented; it is not assumed from the old Vue log bridge.
- warn/error/slow events flush to backend without recursion.
- debug/info events stay local by default.
- `applyBridgePatch` emits `frontend.patch.apply.slow` only when duration exceeds a configured threshold.
- `frontend.render.slow` is emitted only after a stable React profiler or targeted timing path exists.
- Frontend trace ingest sanitizes and allowlists fields before backend persistence.
- `result_preview`, prompt text, user message text, file contents, tool results, and raw error stacks never enter trace JSONL.

### Task 4: Thread/Turn/Provider/Tool Spans

Files:

```text
Modify: internal/module/thread/rpc.go
Modify: internal/module/thread/start_session.go
Modify: internal/module/turn/rpc_helpers.go
Modify: internal/module/turn/service.go
Modify: internal/platform/toolbridge/*.go
Modify: selected internal/provider/* files at session start/run boundaries
```

Acceptance:

- `turn/start` trace shows ready wait, prepare, start, provider run where available.
- Tool spans show begin/end duration and success/error.
- Prompt/tool result payloads are not persisted.

### Task 5: Bus/UI Projection Spans

Files:

```text
Modify: internal/platform/bus/sink.go
Modify: internal/module/uistate/projector.go
Modify: internal/module/uistate/patch.go
Modify: internal/module/uistate/patch_timeline.go
```

Acceptance:

- Lifecycle events are searchable by thread/agent/turn/tool.
- High-frequency events are sampled or summarized.

### Task 6: Dashboard Query API

Files:

```text
Create: internal/module/observability/module.go
Create: internal/module/observability/rpc.go
Modify: internal/app/modules.go
Modify: frontend-app/src/shared/api/backendApi.js
Modify: frontend-app/src/App.jsx
```

Acceptance:

- `internal/app/modules.go` wires both `internal/platform/observability.Module` and `internal/module/observability.Module` into the Fx app graph.
- `internal/module/observability` returns `rpc.HandlerMapResult` for `observability/*` handlers so `rpc.registerAllHandlers` registers them.
- Dashboard can query by trace id, thread id, slow list, error list.
- UI clearly shows source `memory` or `jsonl_tail`.
- Missing trace returns an empty, non-error response with source metadata.

### Task 7: Verification and Docs

Files:

```text
Create: docs/cc/observability-tracing/README.md
Create: docs/cc/observability-tracing/manual-smoke.md
Update: docs/doc/codemap only if required by repo process after code changes
```

Acceptance:

- Unit tests pass for observability package.
- Wails binding tests pass.
- RPC tests pass.
- Frontend bridge/store tests pass.
- Manual smoke shows one `turn/start` trace JSONL line and Dashboard query result.

## 13. Test Strategy

Minimum tests:

```text
internal/platform/observability/index_test.go
internal/platform/observability/jsonl_sink_test.go
internal/platform/observability/sampler_test.go
internal/platform/observability/stack_test.go
internal/platform/observability/sanitizer_test.go
internal/platform/observability/config_test.go
internal/platform/observability/code_anchor_test.go
internal/module/observability/rpc_test.go
internal/ui/wails/binding_id_test.go
internal/platform/rpc/server_test.go or existing rpc tests
internal/platform/rpc/server_trace_privacy_test.go or equivalent trace privacy test
frontend-app/src/shared/api/wailsBridge.test.js
frontend-app/src/shared/api/backendApi.test.js or equivalent ingest test
frontend-app/src/entities/client/model/useClientStore.test.js
frontend-app/src/App.test.jsx only if render/profiler tracing is wired
```

Manual smoke:

1. Start app normally; tracing is enabled in safe mode by default. Set `OBS_TRACING_ENABLED=0` only when intentionally disabling tracing.
2. Send one message.
3. Locate `trace-YYYY-MM-DD.jsonl` in project log dir.
4. Confirm the trace directory and file permissions are owner-only where supported.
5. Confirm at least frontend/Wails/RPC/turn events share a trace id.
6. Query Dashboard by trace id.
7. Confirm code anchors are present.
8. Confirm no prompt/full payload appears in JSONL.
9. Append a malformed trailing JSON fragment to a copied trace fixture and confirm the reader returns valid earlier events with decode-error metadata.
10. Run sanitizer golden tests for errors, metadata, frontend params, tool summaries, stack frames, secret-like values, and oversized strings.

## 14. Operational Guardrails

- If JSONL write fails after successful startup, log one rate-limited warning, increment a sink error counter, and keep app functional.
- If tracing is enabled and service construction, config validation, directory creation, or fx wiring fails, startup must fail fast.
- If tracing is intentionally disabled, bind an explicit disabled service; do not rely on nil checks as the disabled state.
- If index limit is reached, evict oldest; never grow unbounded.
- If trace event serialization fails, drop the event and increment a dropped counter.
- If Dashboard query tail scan exceeds timeout/size/concurrency limits, return partial result with `truncated=true`.
- If JSONL contains malformed lines, skip bad lines, count decode errors, and continue returning valid events.
- Default packaged mode must not require PG, SQLite, or network for tracing.

## 15. Open Questions

1. Should trace JSONL stay enabled by default in packaged production safe mode, while still allowing `OBS_TRACING_ENABLED=0` to disable it?
2. Should normal successful RPC events be written to JSONL, or only indexed in memory unless slow/error?
3. What UI location should host the trace view: Dashboard page, debug panel, or per-thread drawer?
4. Should code anchors support click-to-open through existing `ui/code/open`?
5. Should trace files be pruned automatically after N days or total bytes?
6. Should legacy Vue frontend tracing be implemented if React migration is not yet the packaged default?

## 16. Recommended Answers

1. Keep tracing enabled by default in safe mode; allow explicit opt-out with `OBS_TRACING_ENABLED=0`, and keep the default sampler to lifecycle/RPC done/slow/error and summaries.
2. Write successful RPC done events for important lifecycle methods; for high-frequency methods such as `ui/state/get`, `ui/sidebar/get`, and `ui/log`, write only slow/error events by default and keep normal success in memory or sampled summaries.
3. Start with Dashboard debug panel plus trace id search; per-thread drawer can come later.
4. Yes, use existing code preview/open RPC if available; otherwise render path/function/line text.
5. Yes. Phase 1 must prune trace JSONL by default using validated retention bounds: default 14 days and 512MB per project trace directory. Retention only applies to trace files matching the exact trace filename pattern.
6. No by default. Phase 1 targets React `frontend-app`; instrument legacy Vue only if migration timing requires Vue to remain the packaged runtime.

## 17. Cross-Review Synthesis

Two codex review agents independently reviewed the draft:

- `reviews/review-a-architecture.md` focused on architecture, fx/module ownership, and code-location usefulness.
- `reviews/review-b-volume-packaging.md` focused on log volume, JSONL safety, packaging, privacy, and future SQLite migration.
- `reviews/review-c-react-frontend-reality-r2.md` rechecked React `frontend-app` truthfulness after frontend corrections and found no required changes.
- `reviews/review-d-backend-persistence-volume-r2.md` rechecked backend persistence, privacy, log volume, and Fx/RPC integration.

Accepted high-confidence findings incorporated into this document:

1. Enabled tracing must fail fast on invalid fx wiring/config; disabled tracing must be explicit, not accidental nil no-op.
2. RPC handlers belong in `internal/module/observability`; `internal/platform/observability` remains storage/service infrastructure.
3. Static line anchors can drift; file+function is stable and anchors require validation.
4. JSONL needs Phase 1 retention caps by age and total bytes.
5. Sanitization must cover every string field, especially `Error`, not only metadata.
6. Trace directory/file permissions must be owner-only where supported.
7. Tail fallback queries need timeout, concurrency limits, and caching/singleflight.
8. Malformed or partial JSONL lines must be tolerated by readers.
9. OBS_* knobs need fail-fast validation bounds.
10. Privacy tests must cover all event construction paths, not only manual smoke.
11. JSONL schema needs `schema_version` and constrained metadata for future SQLite migration.
12. React frontend already has W3C trace context, but remote frontend trace flushing, patch slow timing, and render slow timing are new work; do not assume old Vue log bridge behavior applies.
13. Trace JSONL must live under `~/.super-dolphin/log/<project>/traces/`, with `0700` directory and `0600` file permissions where supported; retention and pruning are scoped only to exact `trace-*.jsonl` files in that directory.
14. RPC trace events must not reuse existing raw params preview fields such as `rpcParamPreview` or `params_preview`; privacy tests must lock this down.
15. Fx wiring must be explicit in `internal/app/modules.go`; both platform and module observability modules must be added, and `observability/*` RPC handlers must be exposed through `rpc.HandlerMapResult`.

## 18. Success Criteria

The implementation is successful when an agent can receive a trace id and immediately identify:

- the slowest span;
- the associated component;
- the file/function/line to inspect;
- relevant compact stack for slow/error cases;
- correlated thread/agent/tool identifiers;
- enough context to start LSP navigation without scanning unrelated logs.

It is also successful only if:

- no PG schema/query expansion was introduced;
- no SQLite dependency was introduced in Phase 1;
- trace memory usage is bounded by config;
- JSONL writes are safe, append-only, and payload-sanitized.
