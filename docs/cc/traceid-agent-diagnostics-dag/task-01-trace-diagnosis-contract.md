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
- Include request fields for `Limit`, `ForceRefresh`, `IncludeStack`, `CWD`, and `WorkspaceRoot`.
- Use `CWD` or `WorkspaceRoot` only for repo-relative path normalization and source context; do not use them to discover arbitrary trace files.
- Keep raw `TraceEvent` out of exported model-facing result fields.
- Require every model-facing string field to pass the T04 redaction and path-scrubbing rules before it is returned.
- Include enough status fields for callers to distinguish memory-only, tail-only, mixed, stale, and degraded results.
- Fail fast on invalid request input.

Required bounds:

- default `Limit`: 80 diagnosis timeline entries;
- maximum `Limit`: 200 diagnosis timeline entries;
- maximum slow summaries: 20;
- maximum error summaries: 20;
- maximum panic summaries: 10;
- maximum stack frames per summarized event: 24;
- maximum related ids per id family: 50;
- maximum string field size: 4096 bytes after redaction;
- maximum serialized diagnosis payload: 256 KiB before toolbridge wrapping.

Required status fields:

- `source`: one of `memory`, `tail`, `mixed`, or `none`;
- `tail_attempted`;
- `tail_fresh`;
- `degraded`;
- `tail_error`;
- `tail_warnings`;
- `decode_error_count`;
- `tail_files_scanned`;
- `tail_bytes_read`;
- `tail_duration_ms`;
- `tail_timed_out`;
- `tail_truncated`.

## Acceptance Criteria

- Contract compiles in `internal/platform/observability`.
- Public fields are JSON-friendly and carry hard bounds in this task, not deferred to later tasks.
- The contract can support both host-direct tool output and future RPC parity.
