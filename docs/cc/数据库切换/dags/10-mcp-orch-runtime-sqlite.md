# Task 10: mcp-orch Runtime SQLite 注入

## Agent Prompt

你负责把 `cmd/mcp-orch` 的 runtime 从独立 pgx pool 切到共享 SQLite 文件，并完成 mcp-orch 专属 SQLC 的机械 cutover。此任务必须让 `cmd/mcp-orch` 在没有 `DATABASE_URL` 时完成 Fx 构图、schema gate、logger/startup 测试，并提供最小 SQLite runtime lock provider 替代 PG advisory locker。DAG/wakeup/lease/events 的深层业务并发语义由 Task 11/12 加固，但本任务不能留下生产启动必然需要 `*pgxpool.Pool` 的半成品。

## Scope

依赖：Task 03。

解锁：Task 11、Task 12。

## 修改点

- Modify:
  - `cmd/mcp-orch/runtime.go`
  - `cmd/mcp-orch/fx.go`
  - `cmd/mcp-orch/dag_cron_runner.go`
  - `internal/sidecar/orch/fxadapter/dag_cron_store.go`
  - `cmd/mcp-orch/runtime_test.go`
  - `cmd/mcp-orch/fx_test.go`
- Modify SQLC:
  - `internal/sidecar/orch/sqlc.yaml`
  - `internal/sidecar/orch/sql/queries/**` 中阻止 SQLite sqlc generate 的 PostgreSQL-only 语法。
  - `internal/sidecar/orch/store/sqlctx/db.go`
  - `internal/sidecar/orch/store/sqlc/**` 中带 `Code generated` 标记的文件，必须由 sqlc 生成后提交，禁止手改。
  - `internal/sidecar/orch/store/sqlc/types_ext.go` 是当前手写扩展文件，不是 generated 文件；本任务必须迁移、删除或移动它，确保不再依赖 `pgtype`。
- Mechanical compile adapters:
  - 允许为 mcp-orch SQLC cutover 做“只为编译闭合”的机械类型适配，范围包括直接消费 generated nullable/time/interval 类型的生产 store callsite 与测试 helper，例如 `internal/sidecar/orch/store/taskdag/**`、`internal/sidecar/orch/store/agent/**`、`internal/sidecar/orch/store/prompt/**`、`internal/sidecar/orch/store/commandcard/**`、`internal/sidecar/orch/store/sharedfile/**`、`internal/sidecar/orch/store/workspace/**`、`internal/sidecar/orch/orchestration/**`。
  - 这些适配只能替换 `pgtype.*` / `sqlc.Timestamptz` / `sqlc.Interval` / `sqlc.Text` / `sqlc.Int8` 的机械形状、构造器、scan helper 和测试 stub；不得在本任务实现 DAG OCC、wakeup claim、event append/truncate 等深层业务并发语义。
- Replace:
  - `newPool(cfg *platformconfig.Config) (*pgxpool.Pool, error)` with SQLite `newDB(cfg *platformconfig.Config) (*sql.DB, error)` or direct platform provider.
  - `newQueries(pool *pgxpool.Pool)` with `newQueries(db *sql.DB)`.
  - `providePGAdvisoryLocker(pool *pgxpool.Pool)` with SQLite runtime lock provider backed by `runtime_locks`.
- Preserve:
  - `platformdb.VerifyMinSchemaVersion(ctx, probe)` in mcp-orch startup.
  - tool registry output shape.
  - peer/bootstrap behavior.

## 语义要求

- `cmd/mcp-orch` must not require `DATABASE_URL`.
- It must use `cfg.SQLitePath` or the shared platform DB provider.
- It must set/verify SQLite PRAGMAs through the shared platform open path, not duplicate inconsistent setup.
- Startup failure on low schema version remains fail-fast.
- Scheduled DAG cron must not use `pg_try_advisory_lock` or `pg_advisory_xact_lock`; the minimum replacement must use `runtime_locks` with holder identity and lease expiry. Task 12 owns stress tests and richer renew/release semantics, but Task 10 must remove the active PG locker from Fx wiring.
- mcp-orch generated SQLC must compile with `database/sql`; if a query cannot be mechanically translated without changing DAG semantics, stop with BLOCKED and cite the query and calling store. Do not leave `sqlc.New(*sql.DB)` impossible because generated DBTX still expects pgx.
- Any remaining `pgtype` aliases in `internal/sidecar/orch/store/sqlc/types_ext.go` or store callsites are P1 blockers for this task, because they make later runtime startup and `go test` depend on pgx despite SQLite SQLC generation.

## 不允许改

- 不要 do deep DAG/wakeup/lease/events semantic rewrites here beyond mechanical SQLite sqlc generation and runtime startup closure.
- 不要 remove schema gate tests.
- 不要 pass DB path through `DATABASE_URL`.
- 不要 keep `*pgxpool.Pool`, `pgx.Tx`, `pgtype.*`, or PG advisory lock types in mcp-orch production runtime/wiring. Test-only compatibility must be clearly commented.

## 验收方案

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch ./internal/sidecar/orch/store/sqlctx ./internal/sidecar/orch/store/sqlc ./internal/sidecar/orch/fxadapter -count=1
make sqlc-verify
```

静态扫描：

```bash
rg -n "github.com/jackc/pgx|pgxpool|pgconn|pgtype|DATABASE_URL|NewPool|pg_advisory|pg_try_advisory" cmd/mcp-orch/runtime.go cmd/mcp-orch/fx.go cmd/mcp-orch/dag_cron_runner.go internal/sidecar/orch/fxadapter internal/sidecar/orch/store/sqlctx internal/sidecar/orch/store/agent internal/sidecar/orch/store/prompt internal/sidecar/orch/store/commandcard internal/sidecar/orch/store/sharedfile internal/sidecar/orch/store/workspace internal/sidecar/orch/store/taskdag internal/sidecar/orch/orchestration internal/sidecar/orch/sqlc.yaml internal/sidecar/orch/store/sqlc
```

预期：runtime wiring 和 mcp-orch SQLC 边界无 pgx pool / PG DSN / PG advisory lock 依赖；若测试名还含 postgres，应重命名为 sqlite 语义。Task 11/12 仍可继续清理 store 语义层测试中的旧 PG 断言。
