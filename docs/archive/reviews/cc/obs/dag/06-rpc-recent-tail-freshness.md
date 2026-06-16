# DAG Task 06 - RPC Recent Tail Freshness

Worker: W3 (`work/obs-tail-rpc-tests`)

Depends on: 02

Purpose: prove the `observability/recent/list` RPC path does not return stale tail data for repeated identical payloads.

Files:

- Modify: `internal/module/observability/rpc_test.go`

Steps:

1. Create a tail reader that returns a recent event with `TraceID: "first"` on call 1 and `TraceID: "second"` on call 2.
2. Dispatch `observability/recent/list` twice with the same payload, such as `{"limit":10}`.
3. Assert the second response includes `TraceID: "second"` and does not only reuse the first tail result.
4. Assert the reader was called twice.

Verification:

```bash
./scripts/test_with_guard.sh ./internal/module/observability -run TestRecentListRPCDoesNotReuseStaleTailResult -count=1
```

Expected: PASS after Task 02.

Constraints:

- Keep the test focused on tail freshness, not recent filtering semantics.
- Do not alter RPC response shape.
