# DAG Task 02 - Remove Persistent Tail Result Cache

Worker: W1 (`work/obs-tail-core`)

Depends on: 01

Purpose: make Task 01 pass by removing persistent tail result reuse while preserving in-flight coalescing for concurrent identical queries.

Files:

- Modify: `internal/platform/observability/service.go`
- Modify: `internal/platform/observability/service_test.go`

Implementation direction:

- Remove the completed-result cache map from `Service`.
- Keep `inflight map[Query]*tailCall` and `tailCall(...)` so concurrent identical queries share the same active tail read.
- Delete `cachedTail` and `storeTail`, or leave no completed-result cache path.
- Constructors should still initialize `inflight`.

Verification:

```bash
./scripts/test_with_guard.sh ./internal/platform/observability -run 'TestServiceQueryTailDoesNotReuseStaleResult|TestServiceQueryReportsJSONLTailSourceWhenOnlyTailHasEvents|TestServiceQueryReportsMixedSourceWhenMemoryAndTailBothContribute|TestServiceQueryMixedTailDedupeLimitAndTruncation' -count=1
```

Expected after implementation: PASS.

Constraints:

- Keep source attribution behavior (`jsonl_tail`, `mixed`) unchanged.
- Do not add TTL/mtime complexity unless removing the cache is impossible.
