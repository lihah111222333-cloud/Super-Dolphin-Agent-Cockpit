# Task 03: sqlc 与 DB 边界改造

## Agent Prompt

你负责把主应用和 `cmd/mcp-orch` 的 sqlc/DB 抽象从 pgx 切到 SQLite 可用形态。此任务只建立共享边界和生成链路，不负责逐个业务 store 语义修复。不要把 PostgreSQL 类型留在产品 runtime 的公共接口中。

## Scope

依赖：Task 01、Task 02。

解锁：Task 04 到 Task 12。

## 修改点

- Modify: `sqlc.yaml`
  - `engine: "sqlite"`。
  - schema 指向 `internal/platform/db/sqlite/migrations/001_baseline.sql` 和必要 SQLite-only schema patch。
  - 移除 `sql_package: "pgx/v5"`、`sql_driver: "github.com/jackc/pgx/v5"`。
  - JSON columns 映射到 `encoding/json.RawMessage` 或 `[]byte`，时间 columns 映射到 `int64` 或明确转换类型。
- Modify: `cmd/mcp-orch/sqlc.yaml`
  - 同步切到 SQLite。
  - 保留 mcp-orch 自己的 `queries: "sql/queries"` 和 `out: "store/sqlc"`。
- Modify: `internal/platform/db/pool.go`
  - 移除 `type Pool = pgxpool.Pool`，改为 `type DB = sql.DB` 或删除别名并直接注入 `*sql.DB`。
- Modify: `internal/platform/db/errors.go`
  - `WrapStoreError` 支持 `sql.ErrNoRows`、SQLite unique constraint、busy/locked timeout。
  - 删除对 `pgconn.PgError` 的产品路径依赖；测试可保留历史兼容只在明确需要时。
- Modify: `internal/platform/db/tx.go`
  - `Queryable` 改为 `database/sql` 兼容接口。
  - `WithTx(ctx, db, fn)` 使用 `*sql.Tx`。
  - 新增 `WithImmediateTx` 或等价 helper，统一 `BEGIN IMMEDIATE` 写事务入口。
  - `OpenReadOnlyRows` 使用 read-only tx 或受限执行器；`RowsFieldNames` 使用 `rows.Columns()`。
- Modify/Create: `internal/platform/db/sqlite_helpers.go`
  - 提供共享 helper，禁止后续任务各自发明不兼容实现：
    - `Millis(time.Time) int64` / `TimeFromMillis(int64) time.Time`
    - `ValidateJSONRaw(json.RawMessage) error`
    - `LikeContainsFold(string) string` 或等价的参数化 contains helper
    - `IsSQLiteBusyLocked(error) bool`
    - bounded write retry policy
  - bounded retry 只允许 retry `SQLITE_BUSY` / `SQLITE_LOCKED`；constraint、business duplicate、OCC conflict 不允许 retry。
  - retry 的 max attempts、总耗时、backoff 上限必须固定并可测试；耗尽后返回包含 op name、attempts、elapsed、sqlite code 的错误并写日志。
- Modify: `internal/store/module.go`
  - 用 `*sql.DB` 构造 `internal/store/sqlc.Queries`。
- Modify: `cmd/mcp-orch/store/sqlctx/db.go`
  - `WithTx` / `WithTxOrReuse` 支持 `*sql.DB` 与 `*sql.Tx`。
- Regenerate:
  - `internal/store/sqlc/**`
  - `cmd/mcp-orch/store/sqlc/**`
  - 这些是共享生成物：本任务只建立生成能力；最终提交的 generated diff 由 README 中的 Wave 1.5 / Wave 2.5 串行 finalize 统一负责。

## 不允许改

- 不要在本任务里重写业务查询语义；只做必要的语法修复以便 sqlc 能生成。
- 不要用 ad hoc string scanner 替代 sqlc。
- 不要保留 `pgxpool.Pool` 作为 Fx 注入入口。
- 不要手改 generated sqlc 文件。

## 验收方案

```bash
make sqlc-verify
./scripts/test_with_guard.sh ./internal/platform/db ./internal/store/... ./cmd/mcp-orch/store/... -count=1
make guard
```

静态扫描：

```bash
rg -n "github.com/jackc/pgx|pgxpool|pgconn|pgtype" internal/platform/db internal/store cmd/mcp-orch/store cmd/mcp-orch/runtime.go sqlc.yaml cmd/mcp-orch/sqlc.yaml
```

预期：产品 DB 边界不再依赖 pgx；若测试中保留 pgx 错误兼容，必须有注释说明不是 runtime 依赖。
