# T02 - Query Freshness And Predicate Alignment

Depends on: T01

## Objective

Make diagnosis queries distinguish cached memory results from fresh persisted tail reads, and align memory and tail predicate behavior.

## Source Anchors

- `internal/platform/observability/service.go:239-301`
- `internal/platform/observability/index.go:83-95`
- `internal/platform/observability/jsonl_reader.go:119-137`
- `internal/module/observability/rpc.go:490-497`

## Scope

Update observability query internals used by `DiagnoseTrace` so the diagnosis path can:

- use `ForceRefresh` to bypass stale tail cache for one lookup;
- report whether the result used memory, JSONL tail, or both;
- avoid surprising differences between memory index filtering and tail reader filtering.

## Requirements

- Do not make force refresh the default.
- Keep normal query cost bounded by existing config.
- If memory and tail predicate semantics remain different, document the difference in the diagnosis output and tests.
- Preserve existing raw query behavior unless changing it is required for diagnosis correctness.

## Acceptance Criteria

- Repeated lookup of the same trace id can see newly appended JSONL events when `ForceRefresh` is true.
- Diagnosis output includes freshness metadata.
- Combined trace/thread/slow/error predicates behave consistently for diagnosis, or explicitly report why they differ.

