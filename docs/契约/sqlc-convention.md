# `sqlc` 契约

## 0. 目标与结论

本文档定义 super-agent-v3 当前 Store 层的数据访问范式。源码和 `sqlc.yaml` 是最终真值：

- 产品主库使用 SQLite，本地路径由 `SUPER_DOLPHIN_SQLITE_PATH` 或 `SUPER_DOLPHIN_HOME` 决定。
- `sqlc` 负责解析根目录 `sql/queries/`，生成 `internal/store/sqlc`。
- schema 来自 `internal/platform/db/sqlite/migrations/*.sql`，启动时由 `internal/platform/db` 执行迁移和基线校验。
- Go 运行时使用 `database/sql` 与 `modernc.org/sqlite`；不要把其他数据库驱动、连接池或 ORM 当默认实现。
- mcp-orch sidecar 有独立查询目录 `cmd/mcp-orch/sql/queries/`，不要和产品 Store 查询混在一起。

## 1. `sqlc.yaml` 标准

当前仓库根目录 `sqlc.yaml` 必须满足：

```yaml
version: "2"
sql:
  - engine: "sqlite"
    queries: "sql/queries/"
    schema:
      - "internal/platform/db/sqlite/migrations/001_baseline.sql"
      - "internal/platform/db/sqlite/migrations/107_datasource_v2_chunk_embeddings.sql"
    strict_order_by: true
    gen:
      go:
        package: "sqlc"
        out: "internal/store/sqlc"
        emit_interface: true
        query_parameter_limit: 0
```

规则：

- `engine` 必须是 `sqlite`。
- `queries` 指向根目录 `sql/queries/`，只承载产品 Store 查询。
- `schema` 必须指向 SQLite migration 文件，不能引用旧迁移目录。
- 生成代码只读，禁止手改 `internal/store/sqlc`。
- `emit_interface` 保持开启，模块服务依赖窄接口或封装后的 Store。
- 空值和 JSON 字段优先通过 `sqlc.yaml` overrides 收敛为明确 Go 类型。

## 2. Store 分层

| 层级 | 职责 |
|---|---|
| `internal/platform/db` | 打开 SQLite、执行迁移、校验 schema 版本和基线表 |
| `sql/queries` | 产品 Store SQL 源码 |
| `internal/store/sqlc` | sqlc 生成代码，只读 |
| `internal/store` | 跨模块持久化封装和薄适配 |
| `internal/module/*` | 业务模块，按需依赖封装后的 Store 或 `sqlc.Querier` |
| `cmd/mcp-orch/sql/queries` | mcp-orch sidecar 自有查询 |

## 3. Go 使用范式

```go
package example

import (
    "context"
    "database/sql"
    "fmt"

    storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
    _ "modernc.org/sqlite"
)

func openTestDB() (*sql.DB, error) {
    db, err := sql.Open("sqlite", ":memory:")
    if err != nil {
        return nil, fmt.Errorf("open sqlite: %w", err)
    }
    return db, nil
}

func loadThread(ctx context.Context, q storedb.Querier, threadID string) (storedb.AgentThread, error) {
    if threadID == "" {
        return storedb.AgentThread{}, fmt.Errorf("thread_id is required")
    }
    return q.GetAgentThread(ctx, threadID)
}
```

规则：

- 打开数据库必须检查错误；测试中使用 `t.Cleanup` 关闭。
- 空路径、空 ID、缺字段和非法状态必须 fail-fast 返回错误。
- 事务边界放在 application service 或 Store 薄封装中，不把动态 SQL 拼接散落到业务层。
- 只读动态查询只能走明确的只读执行器，不绕过权限和审计边界。

## 4. SQL 写法

- 新增查询优先写 `.sql` 文件和 query annotation。
- 使用 SQLite 支持的语法；JSON、时间、排序和限制条件必须用当前 migration 实测。
- `strict_order_by` 开启后，分页/排序查询要显式消除歧义。
- 不把兼容性兜底写进 SQL 或 Go 侧过滤；数据缺失或 schema 不匹配应暴露错误。

## 5. 验证

Store 或 SQL 变更至少运行：

```bash
make sqlc-verify
./scripts/test_with_guard.sh <affected packages> -count=1
```

只改本文档或 repo-local skill 时，运行：

```bash
python3 scripts/validate_super_agent_skills.py
git diff --check -- docs/契约/sqlc-convention.md .agent/skills scripts/validate_super_agent_skills.py
```

任何 `sqlc.yaml`、migration 或生成代码 diff 都要在最终报告中单独说明。
