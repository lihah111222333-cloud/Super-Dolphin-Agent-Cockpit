# A09: Embedded PG Baseline Atomicity

**Goal:** baseline 探测、`001_baseline.sql` 读取、执行、`schema_migrations` marker 写入必须原子化，不能半损坏。

**Files:**
- Modify: `internal/platform/db/module.go`
- Test: `internal/platform/db/*baseline*_test.go`

**Steps:**
- [ ] Write red test: baseline detection query error returns error and does not write marker.
- [ ] Write red test: existing-schema probe query error returns error and does not write marker.
- [ ] Write red test: missing/unreadable `001_baseline.sql` returns error and does not write marker.
- [ ] Write red test: baseline SQL exec failure returns error and does not write marker.
- [ ] Write red test: marker insert failure rolls back any baseline SQL changes from the same attempt.
- [ ] Write red test: partial existing schema is not a confirmed baseline path and does not write marker.
- [ ] Write red test: confirmed existing schema path writes marker only after the schema probe succeeds and the full invariant passes.
- [ ] Define the confirmed existing schema path as an explicit invariant check, not only `agent_threads` existence.
- [ ] Wrap baseline detection, existing-schema probe, baseline execution, and marker write in one transaction; rollback on any error.
- [ ] Only mark applied after successful baseline SQL or after the confirmed existing schema invariant passes.

**Validation:**
```bash
./scripts/test_with_guard.sh ./internal/platform/db -run 'Test.*Baseline|Test.*Migration' -count=1
```
