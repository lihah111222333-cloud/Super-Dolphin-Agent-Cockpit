# T01 - Trace Diagnosis Contract

Depends on: none

## Objective

Define the platform contract for trace diagnosis without changing toolbridge behavior yet.

## Source Anchors

- `internal/platform/observability/service.go:93-107`
- `internal/platform/observability/service.go:153-170`
- `internal/module/observability/rpc.go:66-73`
- `internal/module/observability/rpc.go:234-244`

## Scope

Create `internal/platform/observability/diagnose.go` with:

- `TraceDiagnosisRequest`
- `TraceDiagnosis`
- bounded timeline, summary, stack, freshness, warning, and degradation fields
- `DiagnoseTrace(ctx context.Context, req TraceDiagnosisRequest) (TraceDiagnosis, error)`

## Requirements

- Require non-empty `TraceID`.
- Include request fields for `Limit`, `ForceRefresh`, and `IncludeStack`.
- Keep raw `TraceEvent` out of exported model-facing result fields.
- Include enough status fields for callers to distinguish memory-only, tail-only, mixed, stale, and degraded results.
- Fail fast on invalid request input.

## Acceptance Criteria

- Contract compiles in `internal/platform/observability`.
- Public fields are JSON-friendly and bounded by later tasks.
- The contract can support both host-direct tool output and future RPC parity.

