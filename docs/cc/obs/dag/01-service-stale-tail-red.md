# DAG Task 01 - Service Stale Tail RED Test

Worker: W1 (`work/obs-tail-core`)

Depends on: none

Purpose: write a failing service-level regression test that proves repeated identical `IncludeTail:true` queries must consult fresh tail data.

Files:

- Modify: `internal/platform/observability/service_test.go`

Steps:

1. Add a test-only tail reader that returns a different result on each call and records call count.
2. Query the same `Query{TraceID: "tail-changing", IncludeTail: true}` twice.
3. Assert the second response contains the second tail event and the reader call count is 2.
4. Run:

```bash
./scripts/test_with_guard.sh ./internal/platform/observability -run TestServiceQueryTailDoesNotReuseStaleResult -count=1
```

Expected before Task 02: FAIL because `queryTail` returns `cachedTail(query)` on the second call.

Constraints:

- Do not use `Service.Record` to create the second event. The memory index could mask stale tail behavior.
- Do not change production code in this task.
