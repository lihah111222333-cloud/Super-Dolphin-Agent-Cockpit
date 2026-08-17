# TraceId Agent Diagnostics Finding - 2026-06-03

## Summary

Status: resolved on integration branch `feat/traceid-agent-diagnostics`.

Backend observability now exposes `observability_trace_get`, a Codex-facing, schema-validated host-direct tool that turns a `trace_id` into a bounded, redacted diagnosis. This document preserves the original finding, source evidence, and follow-up scope for RPC parity and project skill guidance.

## Implementation Status

Implemented source paths:

- `internal/platform/observability/diagnose.go` defines `TraceDiagnosisRequest`, `TraceDiagnosis`, and `DiagnoseTrace`.
- `internal/platform/observability/service.go` uses memory plus bounded JSONL tail reads with freshness, degradation, and tail-cost fields.
- `internal/platform/observability/jsonl_reader.go` reports decode, truncation, file, byte, timeout, and warning details into query results.
- `internal/platform/toolbridge/observability_trace_tool.go` registers `observability_trace_get` with strict input validation and the redacted diagnosis output.
- `internal/platform/toolbridge/module.go` composes the trace registry with existing host-direct memory tools through Fx.
- `internal/platform/toolbridge/handler_host_tools.go` and `handler_peer_decode.go` reserve host-only names, skip reserved peer duplicates and aliases, and keep non-reserved duplicate conflicts fail-fast.
- `internal/platform/toolbridge/handler_host_tools.go` populates `StructuredContent` for host-direct results and supports CWD-optional host tools.

Implemented contract values:

- diagnosis default `limit`: 80 timeline entries;
- diagnosis maximum `limit`: 200 timeline entries;
- maximum serialized diagnosis payload: 256 KiB;
- default JSONL query tail window: 20 MiB;
- default JSONL tail timeout: 750 ms;
- default JSONL tail max concurrency: 1.

Follow-ups intentionally left open:

- add RPC parity through `observability/trace/diagnose` while keeping `observability/trace/get` as the raw event query;
- add `.agent/skills/trace-diagnosis/SKILL.md` so agents are explicitly taught to call `observability_trace_get` instead of parsing JSONL files directly.

## Original Finding

### Trace IDs Need a Codex-Facing Diagnostic Tool

Severity: Medium. Raise to High only when this blocks a user-visible incident workflow.

The current backend has trace storage and RPC query paths:

- `internal/platform/observability/module.go:28-46` wires `*observability.Service` with a JSONL sink and tail reader.
- `internal/platform/observability/jsonl_sink.go:57-72` stores project trace files under `~/.super-dolphin/log/<project>/traces`.
- `internal/module/observability/module.go:5-7` provides the observability RPC handlers.
- `internal/module/observability/rpc.go:106-114` registers observability RPC methods, including `observability/trace/get`, `observability/recent/list`, `observability/slow/list`, `observability/error/list`, `observability/status`, and `observability/frontend/ingest`.
- `internal/module/observability/rpc.go:127-137` maps `observability/trace/get` to `svc.Query(ctx, Query{TraceID: ...})`.
- `internal/platform/rpc/module.go:93-105` collects handler maps through the `rpc_handlers` Fx group.

The app graph is capable of reaching a host-direct trace tool:

- `internal/app/modules.go:59-83` wires `platformobservability.Module`, `moduleobservability.Module`, `codexapp.Module`, `toolbridge.Module`, `ToolbridgeAdapters`, and `ToolbridgeCodexBinding` in the same app graph.
- `internal/platform/toolbridge/host_tools.go:10-34` defines the host-direct tool contract.
- `internal/platform/toolbridge/module.go:71-85` originally composed host-direct tools from memory reader/writer dependencies only.
- `internal/app/toolbridge_adapters.go:199-228` binds `toolbridge.Handler` into Codex `ServerManager` and `DriverFactory`.
- `internal/provider/codexapp/support.go:325-341` prepares dynamic tools through the factory's `prepareTools` callback.
- `internal/platform/toolbridge/handler_peer_decode.go:44-65` implements `PrepareCodexToolSurface`, which adds host tools before MCP stdio tools.
- `internal/platform/toolbridge/handler_peer_decode.go:81-90` adds host tools to the Codex surface.
- `internal/platform/toolbridge/handler_peer_decode.go:353-378` routes host-surface tool calls through `callHostTool`.

The original gap was that there was no `observability_trace_get`, `observability/trace/diagnose`, or equivalent Codex-facing trace diagnosis tool in source. A user-provided trace id was therefore only a text clue for the agent, not a discoverable tool input.

## Impact

An observability RPC caller can query a trace, but a Codex agent is not guaranteed to discover and call that path. Given a trace id, the agent may search source code or guess local JSONL paths instead of retrieving:

- the relevant trace timeline;
- slow, error, and panic spans;
- thread, turn, call, and tool identifiers;
- code anchors and stack summaries;
- explicit tail-read freshness and degradation status.

That makes trace-driven performance and bug triage inconsistent even though the backend already records relevant data.

## Original Source-Backed Readiness Risks

The following list is preserved as the pre-implementation evidence reviewed for this work. The implemented status above records how the host-direct MVP addressed these risks.

- `internal/platform/observability/service.go:153-170` returns memory results when tail read fails or when tail returns no events, without exposing degradation to callers.
- `internal/platform/observability/module.go:28-31` returns a disabled service when tracing config is disabled, and `internal/platform/observability/service.go:93-107` exposes status and enabled state; a new tool must not advertise a working trace lookup when `svc.Enabled()` is false.
- `internal/platform/observability/service.go:239-301` coalesces in-flight tail reads by `Query`; diagnosis freshness must report whether a fresh tail attempt happened and must not describe this as a completed-result cache.
- `internal/module/observability/rpc.go:508-515` defaults `includeTail` to true, so callers can believe persisted history was consulted.
- `internal/platform/observability/jsonl_reader.go:27-30` collects decode errors, while `jsonl_reader.go:44-51` drops them from `QueryResult`.
- `internal/platform/observability/jsonl_reader.go:146-193` enumerates and stats tail files; `jsonl_reader.go:216-238` reads selected tail bytes into memory.
- `internal/platform/observability/config.go:214-219` defaults tail reads to a 20 MB window, 750 ms timeout, and max concurrency of 1.
- `internal/platform/observability/index.go:83-95` selects one memory index bucket by priority, while `internal/platform/observability/jsonl_reader.go:119-137` applies AND semantics across trace, thread, slow, and error filters.
- `internal/platform/observability/sanitizer.go:13-17` redacts secret-like strings, and `sanitizer.go:122-148` redacts secret-like metadata keys; it does not prove that arbitrary prompt text, file snippets, PII, or absolute paths are safe for model-facing output.
- `internal/module/observability/rpc.go:66-73` and `rpc.go:234-244` expose raw event-oriented RPC responses, so a model-facing diagnosis tool should use a separate redacted projection.
- `internal/platform/toolbridge/handler_host_tools.go:184-230` requires a resolved CWD before calling any host tool; `handler_host_tools.go:299-317` validates that CWD with `os.Stat`.
- `internal/platform/toolbridge/handler_host_tools.go:218-230` currently marshals any host-tool result into an `inputText` JSON string; `internal/platform/toolbridge/types.go:118-127` supports `StructuredContent`, but host dispatch does not populate it today.
- `internal/platform/toolbridge/handler_host_tools.go:145-151` reserves only `memory_read` and `memory_write` as host-only names.
- `internal/platform/toolbridge/handler_peer_decode.go:186-215` currently returns an error for duplicate Codex surface tool names rather than shadowing peer tools behind host tools.

## Required Implementation

### Platform Diagnosis API

Add a shared platform API:

- file: `internal/platform/observability/diagnose.go`
- method: `DiagnoseTrace(ctx context.Context, req TraceDiagnosisRequest) (TraceDiagnosis, error)`

Required behavior:

- require a non-empty `TraceID`;
- include `CWD` or `WorkspaceRoot` for repo-relative path normalization without using those fields to discover arbitrary trace files;
- query memory and JSONL with matching predicate semantics;
- support bounded `ForceRefresh` as a fresh tail attempt without making force refresh the default;
- never return a clean diagnosis when JSONL tail read fails;
- either return an error, or return `degraded=true` with `tail_error` populated;
- surface decode or partial-tail warnings through fields such as `tail_warnings` or `decode_error_count`;
- include tail cost metadata such as `tail_files_scanned`, `tail_bytes_read`, and `tail_duration_ms`;
- summarize slow, error, and panic spans;
- preserve code anchors and stack summaries only as redacted, bounded data.
- bound output with explicit defaults and maxima, including `Limit`, string sizes, stack frames, summary counts, and serialized payload size.

### Model-Facing Redacted Projection

`TraceDiagnosis` must not be raw `TraceEvent` passthrough.

Required output constraints:

- omit raw `Metadata` unless keys are explicitly allowlisted;
- omit prompt text, file snippets, and arbitrary user content;
- redact PII-like and secret-like values before model exposure, including absolute paths outside the workspace and filesystem paths embedded in tail errors or warnings;
- normalize file and stack paths to repo-relative paths where possible;
- bound timeline length, error summaries, stack frame count, and string sizes;
- return source and freshness fields so the agent can tell memory-only, JSONL-tail, mixed, and degraded results apart.

### Host-Direct Tool

Add a host-direct tool in `internal/platform/toolbridge`:

- file: `internal/platform/toolbridge/observability_trace_tool.go`
- tool name: `observability_trace_get`
- input schema:
  - `trace_id` string, required
  - `limit` integer, optional
  - `force_refresh` boolean, optional
  - `include_stack` boolean, optional
- output: the redacted `TraceDiagnosis` projection.

Wire it by:

- extending `hostToolRegistryIn` in `internal/platform/toolbridge/module.go:71-76` to accept `*observability.Service`;
- appending the new registry to `NewCompositeHostToolRegistry` in `internal/platform/toolbridge/module.go:81-85`;
- exposing `observability_trace_get` from `ListHostTools` only when `svc != nil && svc.Enabled()`, while returning an explicit disabled result for stale direct calls when tracing is off;
- deciding whether to accept the current host-tool CWD requirement or refactor host dispatch to support CWD-optional host tools;
- deciding and testing the result contract: either extend host dispatch so this tool can populate `ToolCallResult.StructuredContent`, or deliberately keep the current JSON-text envelope and treat that as the model-facing contract;
- adding `observability_trace_get` to the host-only reservation path in `internal/platform/toolbridge/handler_host_tools.go:145-151`;
- updating production Codex surface duplicate handling in `internal/platform/toolbridge/handler_peer_decode.go:186-215` so reserved host-only duplicates from MCP peers are skipped or logged instead of failing the whole surface.

### Required Tests

Go tests should cover:

- `internal/platform/observability`: `DiagnoseTrace` returns slow, error, panic, anchor, related-id, freshness, and degradation fields.
- `internal/platform/observability`: identical repeated trace lookup can force a fresh JSONL read and see newly appended matching events.
- `internal/platform/observability`: unreadable tail directory or file never returns a clean diagnosis.
- `internal/platform/observability`: malformed JSONL and trailing partial lines expose decode or partial-tail warnings.
- `internal/platform/observability`: memory and tail filtering agree for combined trace/thread/slow/error predicates.
- `internal/platform/observability`: diagnosis output is a redacted projection and does not include raw `TraceEvent` or raw metadata.
- `internal/platform/toolbridge`: `PrepareCodexToolSurface` includes `observability_trace_get` when tracing is enabled.
- `internal/platform/toolbridge`: `PrepareCodexToolSurface` excludes `observability_trace_get` when tracing is disabled, and stale direct calls return an explicit disabled/degraded outcome rather than a clean diagnosis.
- `internal/platform/toolbridge`: `observability_trace_get` calls return either `StructuredContent` or bounded JSON text with a diagnosis payload, including degraded fields when appropriate.
- `internal/platform/toolbridge`: the tool result is either populated as `StructuredContent` or covered by tests that assert the current JSON-text envelope is parseable and bounded.
- `internal/platform/toolbridge`: reserved host-only duplicate names from MCP peers do not break `PrepareCodexToolSurface`.
- `internal/platform/toolbridge`: CWD behavior is covered, either by proving `_cwd` is injected for this tool path or by testing a CWD-optional host dispatch branch.

Recommended verification command after implementation:

```bash
./scripts/test_with_guard.sh ./internal/platform/observability ./internal/platform/toolbridge ./internal/module/observability -count=1
```

## Follow-Up Implementation

RPC parity is useful for UI and future peer forwarding, but it is not required for the first Codex-facing fix.

Suggested follow-ups:

- Add `observability/trace/diagnose` in `internal/module/observability/rpc.go`.
- Keep `observability/trace/get` as the raw event query.
- Route `observability/trace/diagnose` through the same `DiagnoseTrace` platform API.
- Add a project skill at `.agent/skills/trace-diagnosis/SKILL.md` telling agents to call `observability_trace_get` when the user provides `trace_id`, `traceId`, or a trace-like id.

## Non-Goals

- Do not make the agent parse `~/.super-dolphin/log/<project>/traces/*.jsonl` directly.
- Do not expose raw, unbounded JSONL content to the model.
- Do not rely only on prompt wording to make agents search logs.
- Do not make `cmd/mcp-orch` the first implementation path; host-direct is shorter because the app graph already owns `*observability.Service`.

## Decision

Treat trace ids as first-class diagnostic inputs for agents. Implement a bounded, redacted platform diagnosis API and expose it through a host-direct Codex tool. Add RPC parity and skill guidance after the host-direct path is working and covered by tests.
