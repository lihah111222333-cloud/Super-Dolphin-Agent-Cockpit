# Task 08: Hook/Interaction/Topology/DBQuery

## Agent Prompt

你负责迁移主应用中审批/交互状态机和受限查询面：hook pending reviews、agent interactions、topology approvals、dbquery。hook pending reviews 必须继续走 generated sqlc，不允许回退到手写 `Exec/Query/QueryRow`。

## Scope

依赖：Task 03。

可并行：可与 Task 04、05、06、07、09 并行。

## 修改点

- Modify SQL:
  - `sql/queries/interaction.sql`
  - `sql/queries/topology_approval.sql`
  - `sql/queries/hook_pending_review.sql`
  - `sql/queries/db_query.sql`
- Modify stores:
  - `internal/store/hookstore/hookstore.go`
  - `internal/store/interaction/store.go`
  - `internal/store/topologyapproval/store.go`
  - `internal/store/dbquery/store.go`
  - `internal/store/dbquery/executor.go`
  - `internal/store/dbquery/executor_parser.go`
  - `internal/store/dbquery/normalize.go`
- Modify dashboard query tests if they depend on pgx rows:
  - `internal/module/dashboard/query_test.go`

## 语义要求

- `hook_pending_reviews`:
  - hookstore 当前源码是 generated sqlc backed，并由 `TestHookstoreUsesGeneratedSQLC` 锁定；迁移时必须继续通过 `sql/queries/hook_pending_review.sql` 生成接口，不允许回退到手写 `database/sql` SQL。
  - 保留 generated-sqlc archtest 约束；如果 hook pending review 查询形状变化，先修改 `sql/queries/hook_pending_review.sql`，再运行 sqlc 生成/验证，让 generated diff 来自生成器。
  - `SavePendingReview` remains idempotent on `hook_call_id`.
  - `ResolvePendingReview` preserves idempotency key behavior.
  - cancel/recover/expire statuses remain exact.
- State transitions:
  - Resolve/cancel/recover/expire and topology approve/reject must be conditional updates with expected current status, idempotency key/deadline where applicable, and `RowsAffected` checks.
  - Add two-writer tests: approve vs cancel, resolve vs expire, stale idempotency key, and repeated resolve. Exactly one valid transition may win; stale transitions must return the existing not-found/conflict behavior.
- `agent_interactions` and `topology_approvals`:
  - JSON payload remains valid JSON text.
  - review status transitions preserve row count / not found behavior.
- `dbquery`:
  - Must be fail-closed.
  - Only one top-level read-only statement is allowed: `SELECT` or `WITH ... SELECT`.
  - Reject parser errors, trailing tokens, semicolon/comment smuggling, writable CTE, `RETURNING` DML, and every non-SELECT statement kind before execution.
  - Execute through a dedicated read-only SQLite connection/transaction with `PRAGMA query_only=ON` or a driver authorizer that denies write, attach/detach, pragma, vacuum, schema mutation, extension loading, and temp object creation. If the driver cannot enforce this, dbquery must return an error instead of executing.
  - All manually assembled filters must bind values as parameters; dynamic identifiers/order-by fields must come from code-side allowlists.
  - Column names use `rows.Columns()`.
  - Tests must cover mixed-case/whitespace variants of `PRAGMA`, `ATTACH`, `DETACH`, `VACUUM INTO`, `REINDEX`, `ALTER`, `CREATE TEMP`, `DROP`, `INSERT`, `UPDATE`, `DELETE`, `REPLACE`, writable `WITH`, multiple statements, and `SELECT load_extension(...)`.

## 不允许改

- 不要 allow arbitrary PRAGMA through dbquery.
- 不要 treat parser failure as permission to execute.
- 不要 remove hook review idempotency.
- 不要在 `internal/store/hookstore` 引入 pgx / pgxpool 类型；hook pending reviews 必须继续走 generated sqlc，不要新增手写 `Exec/Query/QueryRow` 路径。

## 验收方案

```bash
./scripts/test_with_guard.sh ./internal/store/hookstore ./internal/store/interaction ./internal/store/topologyapproval ./internal/store/dbquery ./internal/module/dashboard -count=1
make sqlc-verify
```

静态扫描：

```bash
rg -n "pgx|pgconn|FieldDescriptions|ILIKE|NOW\\(|::jsonb|RETURNING \\*" internal/store/hookstore internal/store/interaction internal/store/topologyapproval internal/store/dbquery internal/module/dashboard/query_test.go sql/queries/hook_pending_review.sql sql/queries/interaction.sql sql/queries/topology_approval.sql sql/queries/db_query.sql
```

预期：dbquery 安全测试覆盖白名单查询与每类危险语句。
