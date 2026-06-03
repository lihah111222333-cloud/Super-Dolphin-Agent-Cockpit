# T08 - Platform Observability Tests

Depends on: T02, T03, T04

## Objective

Lock the platform diagnosis behavior with focused Go tests.

## Source Anchors

- `internal/platform/observability`

## Test Coverage

Add or extend tests for:

- `DiagnoseTrace` returns slow, error, panic, anchor, related-id, freshness, and degradation fields.
- Repeated identical trace lookup can force a fresh JSONL read and observe newly appended matching events.
- Unreadable tail directory or file never returns a clean diagnosis.
- Malformed JSONL and trailing partial lines expose decode or partial-tail warnings.
- Memory and tail filtering agree for combined trace/thread/slow/error predicates, or report documented differences.
- Diagnosis output does not include raw `TraceEvent` or raw metadata.

## Requirements

- Keep fixtures small and deterministic.
- Avoid sleeping for cache freshness; prefer explicit force-refresh controls or fake readers.
- Test fail-fast behavior for invalid `TraceID`.

## Acceptance Criteria

```bash
./scripts/test_with_guard.sh ./internal/platform/observability -count=1
```

passes locally.

