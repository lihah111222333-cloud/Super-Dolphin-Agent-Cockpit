# Task 07: Cron Claim 并发等价

## Agent Prompt

你负责把主应用 cron store 的 PostgreSQL `FOR UPDATE SKIP LOCKED` claim 语义迁移到 SQLite。重点是原子 claim、lease fencing、run dedupe、同进程与跨进程重复 claim 为 0。不要把上层 `claim_token` 当成 claim 互斥替代。

## Scope

依赖：Task 03。

可并行：可与 Task 04、05、06、08、09 并行。

## 修改点

- Modify SQL:
  - `sql/queries/cron_job.sql`
- Modify stores:
  - `internal/store/cron/store.go`
  - `internal/store/cron/contract.go` comments if they mention PG locking.
- Modify scheduler comments/tests:
  - `internal/module/cron/scheduler.go`
  - `internal/module/cron/*_test.go`
- Add concurrency tests:
  - `internal/store/cron/claim_sqlite_concurrency_test.go`
  - optional helper process test under `internal/store/cron/testdata` or Go test subprocess pattern.

## 目标 SQL 语义

`ClaimDueJobsForUpdate` must select and update in one SQLite write step or one `BEGIN IMMEDIATE` transaction. Use a statement equivalent to:

```sql
UPDATE cron_jobs
SET
  claimed_by = ?,
  claimed_at = ?,
  lease_expires_at = ?,
  claim_token = ?,
  updated_at = ?
WHERE id IN (
  SELECT id
  FROM cron_jobs
  WHERE enabled = 1
    AND (claim_token = '' OR COALESCE(lease_expires_at, 0) <= ?)
    AND COALESCE(next_retry_at, next_run_at) <= ?
  ORDER BY COALESCE(next_retry_at, next_run_at) ASC, id ASC
  LIMIT ?
)
RETURNING id, name, prompt, schedule_type, schedule_expr, timezone, provider,
          model, cwd, config, skills, notify_channel, enabled, next_run_at,
          last_scheduled_at, last_run_at, claimed_at, claimed_by,
          lease_expires_at, claim_token, thread_id, agent_id, active_turn_id,
          last_turn_id, failure_count, max_attempts, next_retry_at,
          last_status, last_error_at, last_error, created_at, updated_at;
```

## 语义要求

- Claim, renew, release, finish, fail must keep `id + claim_token` fencing.
- Lease expiry uses explicit `now_ms` passed from Go.
- Run dedupe remains a second-line guard, not a replacement for claim.
- `database is locked` retry must be bounded and logged; after retry exhaustion return error.

## 不允许改

- 不要 increase scheduler tick intervals to hide races.
- 不要 use process-local mutex.
- 不要 drop `claim_token` fencing.

## 验收方案

```bash
./scripts/test_with_guard.sh ./internal/store/cron ./internal/module/cron -count=1
make sqlc-verify
```

新增测试期望：

- 100 due jobs, 4 goroutines claim with same DB -> duplicate job IDs = 0, missing IDs = 0.
- 100 due jobs, 2 OS test subprocesses claim same SQLite file -> duplicate job IDs = 0, missing IDs = 0.
- stale lease can be reclaimed; fresh lease cannot be stolen.

