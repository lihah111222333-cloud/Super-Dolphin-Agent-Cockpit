# DAG Task 03 - In-Flight Coalescing Contract

Worker: W2 (`work/obs-tail-service-tests`)

Depends on: 02

Purpose: lock the intended replacement behavior: no completed-result cache, but concurrent identical tail reads still coalesce.

Files:

- Modify: `internal/platform/observability/service_test.go`

Steps:

1. Add a blocking `QueryTailReaderFunc` test helper.
2. Start two goroutines that call the same `Query{TraceID: "same", IncludeTail: true}` while the first tail read is still blocked.
3. Unblock the tail reader.
4. Assert both goroutines receive the same result and the reader was called once.
5. Add a sequential query assertion showing a later identical query calls the reader again.

Verification:

```bash
./scripts/test_with_guard.sh ./internal/platform/observability -run TestServiceQueryTailCoalescesInflightButDoesNotCacheCompletedResult -count=1
```

Expected: PASS after Task 02.

Constraints:

- Do not reintroduce a completed-result cache.
- Keep goroutine synchronization deterministic; no arbitrary sleeps.
