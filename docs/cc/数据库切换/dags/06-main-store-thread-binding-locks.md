# Task 06: Thread/Binding/CWD/Turn Dedupe Store

## Agent Prompt

你负责主应用运行态 store 的 SQLite 等价迁移：thread 生命周期、provider binding、CWD lock、turn dedupe。重点是唯一约束、不可变字段、CAS、过期抢占、幂等和时间窗口，不要处理 cron claim。

## Scope

依赖：Task 03。

可并行：可与 Task 04、05、07、08、09 并行。

## 修改点

- Modify SQL:
  - `sql/queries/agent_thread.sql`
  - `sql/queries/agent_thread_prompt_snapshot.sql`
  - `sql/queries/agent_provider_binding.sql`
  - `sql/queries/thread_binding.sql`
  - `sql/queries/cwd_lock.sql`
  - `sql/queries/turn_dedupe_registry.sql`
- Modify stores:
  - `internal/store/thread/store.go`
  - `internal/store/thread/module.go`
  - `internal/store/binding/store.go`
  - `internal/store/cwdlock/store.go`
  - `internal/store/turndedupe/store.go`
- Modify module-level consumers if needed:
  - `internal/module/thread/**`
  - `internal/module/dashboard/**`

## 语义要求

- `agent_provider_binding`:
  - `agent_id` primary key remains authoritative.
  - `(provider, provider_thread_id)` unique remains enforced.
  - immutable `agent_id/provider/provider_thread_id` behavior is preserved by trigger or store-level guarded update plus regression tests.
  - idempotent conflict for same `agent_id` still succeeds.
- `agent_threads`:
  - prompt snapshot/config JSON round-trips.
  - recoverable/pending launch/manual rename semantics unchanged.
- All `BEGIN IMMEDIATE`, CAS write transactions, CWD lock races, and turn dedupe live-entry races must use the shared bounded retry helper from Task 03, or have a test proving retry is unnecessary.
- `cwd_instance_locks`:
  - heartbeat stale threshold remains 45 seconds.
  - `Acquire`, `ForceAcquire`, `Heartbeat`, `Release`, `DeleteStale` preserve row-count semantics.
- `turn_dedupe_registry`:
  - concurrent start does not create duplicate live entry.
  - terminal mark allows retry after terminal.
  - time window comparisons use epoch milliseconds consistently.

## 不允许改

- 不要 weaken unique/check constraints to make tests pass.
- 不要 use Go mutex for CWD or turn dedupe semantics.
- 不要 alter public contract DTO fields.

## 验收方案

```bash
./scripts/test_with_guard.sh ./internal/store/thread ./internal/store/binding ./internal/store/cwdlock ./internal/store/turndedupe -count=1
./scripts/test_with_guard.sh ./internal/module/thread ./internal/module/dashboard -count=1
make sqlc-verify
```

并发/行为测试必须覆盖：

- two goroutines bind same provider thread -> one idempotent success or one conflict matching current behavior.
- two processes or two DB handles acquire same cwd -> one holder.
- turn dedupe live entry duplicate count = 0.
- prompt snapshot round-trip covered by `internal/store/thread/snapshot_test.go` or equivalent.
