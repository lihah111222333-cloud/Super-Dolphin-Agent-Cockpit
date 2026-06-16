# T02 - Query Freshness And Predicate Alignment

Depends on: T01

## Objective

Make diagnosis queries distinguish memory results from a fresh persisted tail attempt, and align memory and tail predicate behavior.

## Source Anchors

- `internal/platform/observability/service.go:239-301`
- `internal/platform/observability/index.go:83-95`
- `internal/platform/observability/jsonl_reader.go:119-137`
- `internal/module/observability/rpc.go:508-515`

## Scope

Update observability query internals used by `DiagnoseTrace` so the diagnosis path can:

- use `ForceRefresh` to force a fresh tail attempt and avoid reusing an in-flight tail snapshot for one diagnosis lookup;
- report whether the result used memory, JSONL tail, or both;
- avoid surprising differences between memory index filtering and tail reader filtering.

## Requirements

- Do not make force refresh the default.
- Do not introduce a completed-result tail cache; the current source risk is in-flight coalescing and incomplete freshness reporting, not a persisted result cache.
- Keep normal query cost bounded by existing config.
- If memory and tail predicate semantics remain different, document the difference in the diagnosis output and tests.
- Preserve existing raw query behavior unless changing it is required for diagnosis correctness.

## Acceptance Criteria

- Repeated lookup of the same trace id can see newly appended JSONL events when `ForceRefresh` is true.
- A `ForceRefresh=true` diagnosis does not wait for or reuse an older ordinary lookup's in-flight tail result.
- Diagnosis output includes freshness metadata.
- Combined trace/thread/slow/error predicates behave consistently for diagnosis, or explicitly report why they differ.
