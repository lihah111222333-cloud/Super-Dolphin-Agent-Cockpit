# T08 - Platform Observability Test Gate

Depends on: T02, T03, T04

## Objective

Lock the platform diagnosis behavior with focused Go tests and verify that T02, T03, and T04 carried their regression tests in the same worktree commits.

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
- `ForceRefresh=true` does not reuse an older ordinary lookup's in-flight tail result.
- Tail timeout and truncation produce degraded or warning fields.
- Large traces remain under the T01 output bounds.

## Requirements

- Keep fixtures small and deterministic.
- Avoid sleeping for cache freshness; prefer explicit force-refresh controls or fake readers.
- Test fail-fast behavior for invalid `TraceID`.
- Do not leave all platform tests to T08; behavior-changing work in T02, T03, and T04 must include its own tests before that worktree can pass review.
- Prefer fake tail readers or non-directory paths over permission-only unreadable file fixtures, which can be unstable under different test users.

## Acceptance Criteria

```bash
./scripts/test_with_guard.sh ./internal/platform/observability -count=1
```

passes locally.
