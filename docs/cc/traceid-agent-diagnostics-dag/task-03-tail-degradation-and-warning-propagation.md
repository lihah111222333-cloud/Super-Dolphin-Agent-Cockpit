# T03 - Tail Degradation And Warning Propagation

Depends on: T01, T02

## Objective

Ensure JSONL tail failures, decode errors, partial reads, and tail cost are visible to diagnosis callers.

## Source Anchors

- `internal/platform/observability/service.go:153-170`
- `internal/platform/observability/jsonl_reader.go:27-51`
- `internal/platform/observability/jsonl_reader.go:146-193`
- `internal/platform/observability/jsonl_reader.go:216-238`
- `internal/platform/observability/config.go:214-219`

## Scope

Extend the diagnosis path and, if needed, the tail reader result contract so `TraceDiagnosis` can report:

- `degraded`
- `tail_error`
- `tail_warnings`
- `decode_error_count`
- `tail_files_scanned`
- `tail_bytes_read`
- `tail_duration_ms`

## Requirements

- Never return a clean diagnosis when a requested tail read fails.
- Treat malformed JSONL and trailing partial lines as visible warnings.
- Keep tail scanning bounded by configured max bytes, timeout, and concurrency.
- Do not silently swallow file-system errors that affect diagnosis completeness.

## Acceptance Criteria

- Unreadable tail directory or file returns an error or `degraded=true`.
- Malformed JSONL is reflected in warning or decode count fields.
- Tail cost fields are populated for diagnosis calls that attempt a tail read.

