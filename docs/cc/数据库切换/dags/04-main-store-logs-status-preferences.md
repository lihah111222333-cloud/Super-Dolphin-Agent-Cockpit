# Task 04: 主应用简单 Store 切换

## Agent Prompt

你负责主应用中低并发、CRUD 为主的 store SQLite 等价迁移。只处理 agent status、UI preference、日志、审计、bus log、routing tests。目标是锁定 JSON/text/time/search 的基础映射，让后续复杂 store 复用同一模式。当前源码没有 `internal/store/tasktrace` store，`task_traces` 只保留 schema/dbquery 历史查询入口；不要为不存在的 store 新建迁移范围。

## Scope

依赖：Task 03。

可并行：可与 Task 05、06、07、08、09 并行。

## 修改点

- Modify SQL:
  - `sql/queries/agent_status.sql`
  - `sql/queries/ui_preference.sql`
  - `sql/queries/system_log.sql`
  - `sql/queries/ai_log.sql`
  - `sql/queries/audit_log.sql`
  - `sql/queries/bus_log.sql`
  - `sql/queries/prompt_routing_test.sql`
- Modify stores:
  - `internal/store/agentstatus/store.go`
  - `internal/store/uipreference/store.go`
  - `internal/store/systemlog/store.go`
  - `internal/store/ailog/store.go`
  - `internal/store/auditlog/store.go`
  - `internal/store/buslog/store.go`
  - `internal/store/routingtest/store.go`
- Modify tests:
  - Add SQLite-backed tests for insert/list/get filters.
  - Keep existing stub tests when they protect mapping behavior.

## 语义要求

- `NOW()` 改为 Go 层传入 epoch milliseconds，或由 query 参数显式传入。
- JSON 字段写入前必须保持合法 JSON；无效 JSON 返回错误。
- `ILIKE` 改为 SQLite `lower(column) LIKE lower(?)` 或明确 helper。
- `ai_log.sql` 中三个查询使用了 PG-only 函数，必须按以下策略迁移：
  - `ListAILogsByCategory`：CTE 内用 `regexp_match` 提取 HTTP method/url/status/model 并分类。改写为返回原始 `message`、`source`、`level` 字段，分类逻辑上移至 `internal/store/ailog/store.go` Go 层，用预编译 `regexp.MustCompile` 实现；SQL 侧只做基础过滤和分页。
  - `CountAILogsByStatus`：用 `regexp_match` 提取 HTTP 状态码后 GROUP BY。改写为在 Go 层读取原始行后按正则分组计数，或退化为返回按 level/source 分组的 raw count 供调用方二次聚合。
  - `ListRecentAILogs`：CTE 内用 `regexp_match`/`regexp_replace` 提取 endpoint。同上，提取逻辑移至 Go 层。
  - 禁止注册 SQLite UDF 来代替 Go 层实现。上述三个查询的 SQL 改写后必须通过 `rg regexp_match sql/queries/ai_log.sql` 无命中验证。
- Limit 必须保留上限，不能让 dashboard 查询整表。
- Dashboard/list 查询必须采用 metadata-only projection；`raw`、`extra` 等大字段只在 detail-by-id 或明确需要的查询读取。
- 大表 fixture 必须证明列表页不会一次性返回或反序列化整表 JSON/日志大字段。
- `RowsAffected` 语义必须校验；更新 0 行时按原 store 行为返回 not found 或 count。

## 不允许改

- 不要碰 thread/binding/cwd/cron/prompt recall。
- 不要改变对外 DTO 字段名和 JSON shape。

## 验收方案

```bash
./scripts/test_with_guard.sh ./internal/store/agentstatus ./internal/store/uipreference ./internal/store/systemlog ./internal/store/ailog ./internal/store/auditlog ./internal/store/buslog ./internal/store/routingtest -count=1
make sqlc-verify
```

静态扫描：

```bash
rg -n "NOW\\(|ILIKE|regexp_match|::text|::bigint|::jsonb|jsonb|pgtype|pgx" sql/queries/agent_status.sql sql/queries/ui_preference.sql sql/queries/system_log.sql sql/queries/ai_log.sql sql/queries/audit_log.sql sql/queries/bus_log.sql sql/queries/prompt_routing_test.sql internal/store/agentstatus internal/store/uipreference internal/store/systemlog internal/store/ailog internal/store/auditlog internal/store/buslog internal/store/routingtest
```

预期：无 PostgreSQL-only 语法；测试覆盖 JSON、时间和 keyword filter。
