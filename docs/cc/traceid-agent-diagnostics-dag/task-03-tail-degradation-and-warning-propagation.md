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

- `tail_attempted`
- `degraded`
- `tail_error`
- `tail_warnings`
- `decode_error_count`
- `tail_files_scanned`
- `tail_bytes_read`
- `tail_duration_ms`
- `tail_timed_out`
- `tail_truncated`

## Requirements

- Never return a clean diagnosis when a requested tail read fails.
- Treat malformed JSONL and trailing partial lines as visible warnings.
- Keep tail scanning bounded by configured max bytes, timeout, and concurrency.
- Do not silently swallow file-system errors that affect diagnosis completeness.
- Do not implement `DiagnoseTrace` as a thin wrapper around `Service.Query` if `Service.Query` still drops decode errors or returns clean memory results on tail failure.
- Either extend `QueryResult` to carry tail stats and warnings, or give diagnosis a dedicated tail-read path with the same bounded reader behavior.
- `tail_error` and `tail_warnings` must be model-facing strings after T04 redaction and path scrubbing; raw filesystem paths, usernames, emails, or secret-like values must not appear in these fields.

## Acceptance Criteria

- Unreadable tail directory or file returns an error or `degraded=true`.
- Malformed JSONL is reflected in warning or decode count fields.
- Tail cost fields are populated for diagnosis calls that attempt a tail read.
- Timeout and truncation conditions are visible through explicit fields instead of being inferred from zero files or zero bytes.
- Tail errors and warnings containing absolute paths are redacted before diagnosis output.
