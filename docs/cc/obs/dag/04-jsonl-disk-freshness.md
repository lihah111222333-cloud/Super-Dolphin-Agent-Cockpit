# DAG Task 04 - JSONL Disk Freshness Regression

Worker: W2 (`work/obs-tail-service-tests`)

Depends on: 02

Purpose: prove the real JSONL tail source, not only a spy reader, is refreshed for repeated identical service queries.

Files:

- Modify: `internal/platform/observability/service_test.go`

Steps:

1. Create a temp trace directory.
2. Create `svc := NewService(cfg, WithTailReader(JSONLTailReader{Dir: dir, MaxBytes: 1024 * 1024}))`.
3. Query `Query{TraceID: "disk-tail", IncludeTail: true}` before any file exists or before the matching event exists.
4. Append a matching JSONL event directly to `trace-2026-06-03.jsonl`.
5. Repeat the identical query.
6. Assert the second query sees the JSONL event.

Verification:

```bash
./scripts/test_with_guard.sh ./internal/platform/observability -run TestServiceQueryTailReReadsJSONLForSameQuery -count=1
```

Expected: PASS after Task 02.

Constraints:

- Do not call `svc.Record` for the appended event.
- The test must fail on the old persistent cache behavior.
