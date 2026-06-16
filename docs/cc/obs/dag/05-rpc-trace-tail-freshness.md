# DAG Task 05 - RPC Trace Tail Freshness

Worker: W3 (`work/obs-tail-rpc-tests`)

Depends on: 02

Purpose: prove the `observability/trace/get` RPC path does not return a stale tail result for repeated identical payloads.

Files:

- Modify: `internal/module/observability/rpc_test.go`

Steps:

1. Create a tail reader that returns `method: "first"` on call 1 and `method: "second"` on call 2 for the same trace.
2. Build `svc := platformobs.NewService(testTraceConfig(), platformobs.WithTailReader(tail))`.
3. Dispatch `observability/trace/get` twice with the same JSON payload.
4. Assert the second response contains `method: "second"` and the reader was called twice.

Verification:

```bash
./scripts/test_with_guard.sh ./internal/module/observability -run TestTraceGetRPCDoesNotReuseStaleTailResult -count=1
```

Expected: PASS after Task 02.

Constraints:

- Do not set `includeTail:false`.
- Do not seed memory with the second event.
