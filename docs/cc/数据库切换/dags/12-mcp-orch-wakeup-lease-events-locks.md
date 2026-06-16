# Task 12: mcp-orch Wakeup/Lease/Events/Runtime Locks

## Agent Prompt

你负责 `cmd/mcp-orch` 中所有 PostgreSQL locking/concurrency 特性的 SQLite 语义加固：DAG wakeup claim、worker lease、Task 10 已建立的 scheduled DAG runtime lock、DAG run events append/truncate。重复 dispatch、重复 scheduled run、event 覆盖都是 P0 发布阻断项。

## Scope

依赖：Task 10 + Task 11 merged。DAG core schema/store types and generated sqlc must be finalized before this task starts.

## 修改点

- Modify SQL:
  - `internal/sidecar/orch/sql/queries/task_dag_wakeup_dispatch.sql`
  - `internal/sidecar/orch/sql/queries/task_dag_wakeup_query.sql`
  - `internal/sidecar/orch/sql/queries/task_dag_worker_lease.sql`
  - `internal/sidecar/orch/sql/queries/task_dag_run.sql`
  - `internal/sidecar/orch/sql/queries/task_dag_dag.sql` remove advisory lock queries.
- Modify stores:
  - `internal/sidecar/orch/store/taskdag/store_wakeup.go`
  - `internal/sidecar/orch/store/taskdag/store_lease.go`
  - `internal/sidecar/orch/store/taskdag/store_run.go`
  - `internal/sidecar/orch/store/taskdag/store_dispatch_guard.go`
- Harden runtime lock from Task 10:
  - `cmd/mcp-orch/dag_cron_runner.go`
  - `cmd/mcp-orch/fx.go`
  - `internal/sidecar/orch/fxadapter/dag_cron_store.go`
  - `internal/sidecar/orch/orchestration/cron/scheduler_cron.go` only if interface needs holder/renew/release semantics.
- Add tests:
  - `internal/sidecar/orch/store/taskdag/wakeup_sqlite_concurrency_test.go`
  - `internal/sidecar/orch/store/taskdag/runtime_lock_sqlite_test.go`
  - `internal/sidecar/orch/store/taskdag/run_event_sqlite_golden_test.go`

## 目标语义

- Wakeup claim:
  - pending -> dispatching happens in one `UPDATE ... WHERE id IN (SELECT...) RETURNING ...` or one `BEGIN IMMEDIATE` transaction.
  - `MarkWakeupSent`, `RetryWakeup`, `FailWakeup` keep `id + claimed_at + claimed_by + lease_expires_at` fencing.
  - stale dispatching reclaim only reclaims expired lease.
- Worker lease:
  - `Acquire` uses SQLite CAS with owner and lease expiry.
  - `Renew` and `Release` match owner.
- Runtime locks:
  - Use shared table `runtime_locks`.
  - Start from the SQLite provider introduced by Task 10; do not reintroduce PG advisory lock code.
  - holder includes pid, hostname, process start nonce.
  - acquire/renew/release must match holder; no Go mutex.
- DAG events:
  - Implement append/truncate in Go under `BEGIN IMMEDIATE`.
  - Preserve last 50 events.
  - Golden cases: empty, 50, 51, object, array, string, null payload, node_spawn retry.
- Wakeup/run list queries must use metadata-only projection; large columns such as `prompt_payload`, `events`, `metadata`, `result`, and `config` are read only by detail-by-id queries unless a protocol golden proves they are required.

## 不允许改

- 不要 keep `pg_try_advisory_lock`.
- 不要 ignore `database is locked`; bounded retry then fail-fast.
- 不要 replace JSON event append with last-write-wins.

## 验收方案

```bash
./scripts/test_with_guard.sh ./internal/sidecar/orch/store/taskdag ./internal/sidecar/orch/orchestration ./internal/sidecar/orch/fxadapter ./cmd/mcp-orch -count=1
make sqlc-verify
```

并发测试必须覆盖：

- 100 pending wakeups, 4 goroutines + 2 OS processes claim -> duplicate dispatch = 0, missing = 0.
- two scheduled DAG tickers use same SQLite file -> only one run is started.
- event append under concurrent writers does not lose or overwrite events.
