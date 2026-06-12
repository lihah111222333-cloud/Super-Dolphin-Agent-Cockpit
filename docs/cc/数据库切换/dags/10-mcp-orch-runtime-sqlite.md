# Task 10: mcp-orch Runtime SQLite 注入

## Agent Prompt

你负责把 `cmd/mcp-orch` 的 runtime 从独立 pgx pool 切到共享 SQLite 文件。只处理 runtime wiring、Fx 注入、schema gate、logger/startup 测试，不改 DAG/wakeup/lease 的 SQL 语义。`cmd/mcp-orch` 在没有 `DATABASE_URL` 时必须能启动并连接同一个 SQLite 文件。

## Scope

依赖：Task 03。

解锁：Task 11、Task 12。

## 修改点

- Modify:
  - `cmd/mcp-orch/runtime.go`
  - `cmd/mcp-orch/fx.go`
  - `cmd/mcp-orch/runtime_test.go`
  - `cmd/mcp-orch/fx_test.go`
- Replace:
  - `newPool(cfg *platformconfig.Config) (*pgxpool.Pool, error)` with SQLite `newDB(cfg *platformconfig.Config) (*sql.DB, error)` or direct platform provider.
  - `newQueries(pool *pgxpool.Pool)` with `newQueries(db *sql.DB)`.
- Preserve:
  - `platformdb.VerifyMinSchemaVersion(ctx, probe)` in mcp-orch startup.
  - tool registry output shape.
  - peer/bootstrap behavior.

## 语义要求

- `cmd/mcp-orch` must not require `DATABASE_URL`.
- It must use `cfg.SQLitePath` or the shared platform DB provider.
- It must set/verify SQLite PRAGMAs through the shared platform open path, not duplicate inconsistent setup.
- Startup failure on low schema version remains fail-fast.

## 不允许改

- 不要 edit DAG SQL here.
- 不要 remove schema gate tests.
- 不要 pass DB path through `DATABASE_URL`.

## 验收方案

```bash
./scripts/test_with_guard.sh ./cmd/mcp-orch -count=1
```

静态扫描：

```bash
rg -n "pgxpool|DATABASE_URL|NewPool|pgx/v5" cmd/mcp-orch/runtime.go cmd/mcp-orch/fx.go cmd/mcp-orch/runtime_test.go cmd/mcp-orch/fx_test.go
```

预期：runtime wiring 无 pgx pool / PG DSN 依赖；若测试名还含 postgres，应重命名为 sqlite 语义。

