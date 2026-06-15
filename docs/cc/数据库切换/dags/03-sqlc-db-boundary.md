# Task 03: sqlc 与 DB 边界改造

## Agent Prompt

你负责把主应用 root store 的 sqlc/DB 抽象从 pgx 切到 SQLite 可用形态，并建立后续 store 任务必须复用的 DB/Tx/helper 边界。此任务是串行 root SQLC cutover：必须让 `internal/store/sqlc/**` 变成 `database/sql` 可用的 generated 代码，并让主应用 store Fx 图不再依赖 `*pgxpool.Pool`。`cmd/mcp-orch` 的 SQLC 与 runtime cutover 由 Task 10 负责，不能塞进本任务。

## Scope

依赖：Task 01、Task 02。

解锁：Task 04 到 Task 09，以及 Task 10。

## 修改点

- Modify: `sqlc.yaml`
  - `engine: "sqlite"`。
  - schema 指向 `internal/platform/db/sqlite/migrations/001_baseline.sql` 和必要 SQLite-only schema patch。
  - 移除 `sql_package: "pgx/v5"`、`sql_driver: "github.com/jackc/pgx/v5"`。
  - JSON columns 映射到 `encoding/json.RawMessage` 或 `[]byte`，时间 columns 映射到 `int64` 或明确转换类型。
- Modify SQL for root generation only:
  - 允许修改 `sql/queries/**` 中仍阻止 SQLite sqlc generate 的 PostgreSQL-only 语法，目标是让 root generated 代码可生成、可编译。
  - 本任务只做 root query 的机械 cutover 与编译闭合；复杂并发语义、业务筛选语义、性能投影与 race 回归仍由 Task 04 到 Task 09 分 store 加固。
  - 如果某条 query 的机械转译会改变业务语义，必须在交付报告中点名 query、源码调用点和交给哪个后续任务验证；不能静默改行为。
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
- Regenerate:
  - `internal/store/sqlc/**`
  - generated 文件必须由 sqlc 生成后提交，禁止手改。reviewagent 必须检查 generated diff 与 `sqlc.yaml` / `sql/queries/**` 一致。

## 不允许改

- 不要在本任务里深入重写业务 store 语义；只做必要的 root SQL 机械修复以便 sqlc 能生成、store 能编译。需要业务语义判断的点必须移交 Task 04 到 Task 09。
- 不要用 ad hoc string scanner 替代 sqlc。
- 不要保留 `pgxpool.Pool` 作为 Fx 注入入口。
- 不要手改 generated sqlc 文件。
- 不要修改 `cmd/mcp-orch/sqlc.yaml`、`cmd/mcp-orch/sql/queries/**`、`cmd/mcp-orch/store/sqlc/**`、`cmd/mcp-orch/store/sqlctx/**`；这些属于 Task 10。

## 验收方案

```bash
make sqlc-verify
./scripts/test_with_guard.sh ./internal/platform/db ./internal/store/... -count=1
make guard
```

静态扫描：

```bash
rg -n "github.com/jackc/pgx|pgxpool|pgconn|pgtype" internal/platform/db internal/store sql/queries sqlc.yaml
```

预期：主应用产品 DB 边界不再依赖 pgx；若测试中保留 pgx 错误兼容，必须有注释说明不是 runtime 依赖。`cmd/mcp-orch` 的 pgx 残留不在本任务扫描范围内，由 Task 10 到 Task 12 清除。
