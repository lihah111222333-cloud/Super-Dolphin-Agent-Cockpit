# T04 - Redacted Model-Facing Projection

Depends on: T01

## Objective

Convert raw trace events into a bounded, redacted diagnosis suitable for model exposure.

## Source Anchors

- `internal/platform/observability/sanitizer.go:13-17`
- `internal/platform/observability/sanitizer.go:122-148`
- `internal/module/observability/rpc.go:66-73`
- `internal/module/observability/rpc.go:234-244`

## Scope

Implement projection logic used by `DiagnoseTrace`:

- timeline summaries
- slow span summaries
- error and panic summaries
- code anchors
- bounded stack summaries
- related thread, turn, call, and tool ids

## Requirements

- Do not expose raw `Metadata` except explicitly allowlisted keys.
- Omit prompt text, file snippets, and arbitrary user content.
- Redact PII-like and secret-like values before model exposure.
- Normalize file and stack paths to repo-relative paths where possible.
- Bound timeline length, string size, error summaries, and stack frame count.

## Acceptance Criteria

- Diagnosis output is not a raw `TraceEvent` passthrough.
- Tests can assert raw metadata and raw event bodies are absent.
- Output remains useful for locating performance and bug causes through event type, timing, ids, anchors, and summarized errors.

