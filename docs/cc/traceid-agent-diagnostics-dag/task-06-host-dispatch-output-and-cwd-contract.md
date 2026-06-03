# T06 - Host Dispatch Output And CWD Contract

Depends on: T01, T08

## Objective

Make host dispatch behavior match the trace tool's model-facing contract.

## Source Anchors

- `internal/platform/toolbridge/handler_host_tools.go:184-230`
- `internal/platform/toolbridge/handler_host_tools.go:299-317`
- `internal/platform/toolbridge/types.go:118-127`

## Scope

Resolve two toolbridge contract questions before implementing the trace registry:

- whether `observability_trace_get` requires a resolved CWD;
- whether the tool result should use `ToolCallResult.StructuredContent` or the current JSON `inputText` envelope.

## Requirements

- If the tool is CWD-optional, add an explicit branch in host dispatch and tests.
- If the tool keeps the current CWD requirement, prove `_cwd` is injected for the Codex path and that missing CWD cannot block trace diagnosis unexpectedly.
- Prefer making `observability_trace_get` CWD-optional; use CWD only as optional source context passed into `TraceDiagnosisRequest`.
- Prefer structured content for schema-validated diagnosis if the dispatch contract can support it safely.
- If keeping JSON text output, test that the envelope is parseable, bounded, and documented.

## Acceptance Criteria

- A Codex tool call can invoke `observability_trace_get` without accidental CWD failure.
- The result contract is unambiguous and covered by tests.
- No host tool silently drops diagnosis payload fields during wrapping.
- T05 consumes this result/CWD contract instead of inventing a separate behavior.
