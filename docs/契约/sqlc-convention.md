# `sqlc` 契约

## 0. 目标与结论

本文档定义 V3 Store 层的标准数据访问范式。

V3 的标准选型是：

- `sqlc` 负责 SQL 解析、类型生成、参数结构体生成、`Querier` 接口生成。
- `pgx/v5` 负责连接池、事务、批处理、`CopyFrom`。
- PostgreSQL 继续作为唯一主库。
- `fx` 负责提供 `*pgxpool.Pool`、`*sqlc.Queries`、`sqlc.Querier`。

V2 的主要痛点是：

- 20 个手写 Store 都嵌入 `BaseStore{pool *pgxpool.Pool}`。
- 每个 Store 自己维护列名常量、raw SQL、scan 逻辑。
- 大量依赖 `pgx.RowToStructByNameLax`。
- 无稳定 Repository 接口，mock 成本高。
- 加 tracing、租户隔离、统一事务策略时需要横改 20 个文件。
- Squirrel 使用率低，但仍然引入了 query builder 心智负担。

V3 的核心收敛原则是：

- SQL 是源码，写在 `.sql` 文件中。
- 生成代码是只读产物，严禁手改。
- 简单 CRUD 不再复制 20 份手写 Store。
- 复杂事务写成手写 application service，底层查询仍由 `sqlc` 生成。
- `DBQueryStore` 是唯一显式例外，因为它本质上执行动态只读 SQL，不能被 `sqlc` 静态生成。

经 2026-03-19 官方文档调研，并在本机用 `sqlc v1.30.0` + `pgx/v5` 做最小生成验证后，本文档给出以下契约。

## 1. 为什么选 `sqlc`

### 1.1 选型结论

| 方案 | 结论 | 原因 |
| --- | --- | --- |
| 手写 `pgx` + scan | 不采用 | 保留 SQL 透明度，但继续复制参数、scan、列名、事务胶水。 |
| `sqlx` | 不作为主方案 | 仍然是 SQL 字符串 + 运行时扫描，不解决“生成契约”问题。 |
| GORM | 不采用 | 适合快速 CRUD，但 V3 需要显式 SQL、复杂 PostgreSQL 语义和低隐式成本。 |
| ent | 不作为主方案 | schema-as-code 很强，但对现有 DB-first 迁移成本高，切换心智和代码量都更大。 |
| bun | 不作为主方案 | 比 GORM 更 SQL-first，但查询仍主要在 Go 中构造，不能像 `sqlc` 一样直接把 `.sql` 变成接口。 |
| `sqlc` | 采用 | 保留 SQL 可读性，同时生成类型、参数、方法、`Querier`，最贴合 V2 到 V3 的渐进迁移。 |

### 1.2 契约

- V3 的持久化层默认使用 `sqlc`，不是手写 scan，也不是 ORM。
- 新增表或新增查询时，优先新增 `.sql` 文件和 query annotation，而不是新增手写 Store。
- 简单 CRUD 场景直接依赖生成的 `*sqlc.Queries` 或 `sqlc.Querier`。
- 只有在存在跨表事务、领域校验、幂等语义、租户注入等场景时，才在 `internal/store` 增加薄封装。

### 1.3 SQL 示例

```sql
-- sql/queries/agent_status.sql

-- name: GetAgentStatus :one
SELECT
  agent_id,
  agent_name,
  session_id,
  status,
  output_tail,
  error,
  created_at,
  updated_at
FROM agent_statuses
WHERE agent_id = sqlc.arg(agent_id)
LIMIT 1;
```

### 1.4 Go 示例

```go
package runtime

import (
	"context"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type Service struct {
	q storedb.Querier
}

func NewService(q storedb.Querier) *Service {
	return &Service{q: q}
}

func (s *Service) GetAgentStatus(ctx context.Context, agentID string) (storedb.AgentStatus, error) {
	return s.q.GetAgentStatus(ctx, storedb.GetAgentStatusParams{
		AgentID: agentID,
	})
}
```

## 2. `sqlc.yaml` 配置范式

### 2.1 契约

- `sqlc.yaml` 必须放在仓库根目录。
- `version` 固定为 `"2"`。
- `engine` 固定为 `"postgresql"`。
- `schema` 指向顶层 `migrations/` 目录。
- `queries` 指向 `sql/queries/` 目录。
- 生成代码固定输出到 `internal/store/sqlc`。
- `sql_package` 固定为 `pgx/v5`。
- `emit_interface` 必须开启。
- `query_parameter_limit` 固定为 `0`，强制始终生成参数结构体，避免函数签名漂移。
- `emit_empty_slices` 建议开启，避免 `:many` 返回 `nil` slice。
- `emit_pointers_for_null_types` 建议开启，再配合 overrides 收敛成 V3 可接受的 Go 类型。
- `sql_driver` 必须配置为 `github.com/jackc/pgx/v5`，否则 `:copyfrom` 无法生成。
- `emit_methods_with_db_argument` 保持默认关闭，事务统一通过 `WithTx` 切换。

### 2.2 标准配置

```yaml
version: "2"

sql:
  - engine: "postgresql"
    schema: "migrations"
    queries: "sql/queries"
    strict_order_by: true
    gen:
      go:
        package: "sqlc"
        out: "internal/store/sqlc"
        sql_package: "pgx/v5"
        sql_driver: "github.com/jackc/pgx/v5"
        emit_interface: true
        emit_json_tags: true
        emit_db_tags: true
        emit_empty_slices: true
        emit_pointers_for_null_types: true
        query_parameter_limit: 0
        initialisms:
          - id
          - ui
          - dag
          - rpc
          - sql
          - uuid
          - cwd
        overrides:
          - db_type: "uuid"
            go_type:
              import: "github.com/google/uuid"
              type: "UUID"
          - db_type: "uuid"
            nullable: true
            go_type:
              import: "github.com/google/uuid"
              type: "UUID"
              pointer: true
          - db_type: "pg_catalog.timestamptz"
            go_type: "time.Time"
          - db_type: "pg_catalog.timestamptz"
            nullable: true
            go_type:
              import: "time"
              type: "Time"
              pointer: true
          - db_type: "pg_catalog.timestamp"
            go_type: "time.Time"
          - db_type: "pg_catalog.timestamp"
            nullable: true
            go_type:
              import: "time"
              type: "Time"
              pointer: true
          - db_type: "pg_catalog.jsonb"
            go_type:
              import: "encoding/json"
              type: "RawMessage"
          - db_type: "pg_catalog.jsonb"
            nullable: true
            go_type:
              import: "encoding/json"
              type: "RawMessage"
          - db_type: "pg_catalog.json"
            go_type:
              import: "encoding/json"
              type: "RawMessage"
          - db_type: "pg_catalog.json"
            nullable: true
            go_type:
              import: "encoding/json"
              type: "RawMessage"
```

### 2.3 SQL 示例

```sql
-- sql/queries/health.sql

-- name: GetDatabaseNow :one
SELECT NOW() AS now;
```

### 2.4 Go 示例

```go
package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func NewQueries(pool *pgxpool.Pool) *storedb.Queries {
	return storedb.New(pool)
}

func Smoke(ctx context.Context, q *storedb.Queries) error {
	_, err := q.GetDatabaseNow(ctx)
	return err
}
```

## 3. 目录布局范式

### 3.1 契约

- `migrations/` 保持顶层目录，不迁移到 `internal/`。
- 所有 query 文件放在 `sql/queries/`。
- 所有生成代码放在 `internal/store/sqlc/`。
- 手写事务封装、领域仓储、fx module 放在 `internal/store/`。
- 生成目录是只读目录，不能放手写辅助函数。

### 3.2 标准目录树

```text
super-agent-v3/
├── sqlc.yaml
├── migrations/
│   ├── 000001_init.up.sql
│   ├── 000001_init.down.sql
│   └── ...
├── sql/
│   └── queries/
│       ├── agent_status.sql
│       ├── agent_thread.sql
│       ├── agent_provider_binding.sql
│       ├── command_card.sql
│       ├── prompt_template.sql
│       ├── shared_file.sql
│       ├── system_log.sql
│       ├── task_dag.sql
│       ├── task_dag_wakeup.sql
│       ├── ui_preference.sql
│       └── workspace_run.sql
└── internal/
    └── store/
        ├── module.go
        ├── tx.go
        ├── repositories/
        │   ├── runtime.go
        │   ├── workflow.go
        │   └── dashboard.go
        └── sqlc/
            ├── db.go
            ├── models.go
            ├── querier.go
            ├── copyfrom.go
            ├── batch.go
            └── *.sql.go
```

### 3.3 SQL 示例

```sql
-- sql/queries/shared_file.sql

-- name: GetSharedFile :one
SELECT
  path,
  content,
  updated_by,
  created_at,
  updated_at
FROM shared_files
WHERE path = sqlc.arg(path)
LIMIT 1;
```

### 3.4 Go 示例

```go
package store

import (
	"context"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type FileRepository struct {
	q storedb.Querier
}

func NewFileRepository(q storedb.Querier) *FileRepository {
	return &FileRepository{q: q}
}

func (r *FileRepository) Read(ctx context.Context, path string) (storedb.SharedFile, error) {
	return r.q.GetSharedFile(ctx, storedb.GetSharedFileParams{Path: path})
}
```

## 4. 查询文件组织范式

### 4.1 契约

- 默认按实体分文件，一张主表一个 `.sql` 文件。
- 跨表事务或工作流相关查询允许单独建工作流文件。
- 不允许把所有查询堆进一个 `queries.sql`。
- 一个文件内的 query 必须围绕一个聚合或一个工作流边界组织。

### 4.2 推荐拆分

| 文件 | 内容 |
| --- | --- |
| `agent_status.sql` | `Create/Get/List/Upsert/Delete` |
| `shared_file.sql` | `Write/Read/List/Delete` |
| `workspace_run.sql` | run 与 file 的 CRUD |
| `task_dag.sql` | DAG、Node 基础读写 |
| `task_dag_wakeup.sql` | wakeup claim/retry/fail |
| `agent_thread_binding_tx.sql` | rebind、legacy 双写兼容 |

### 4.3 SQL 示例

```sql
-- sql/queries/prompt_template.sql

-- name: CreatePromptTemplate :one
INSERT INTO prompt_templates (
  prompt_key,
  title,
  agent_key,
  tool_name,
  prompt_text,
  variables,
  tags,
  description,
  enabled,
  created_by,
  updated_by
) VALUES (
  sqlc.arg(prompt_key),
  sqlc.arg(title),
  sqlc.arg(agent_key),
  sqlc.arg(tool_name),
  sqlc.arg(prompt_text),
  sqlc.arg(variables),
  sqlc.arg(tags),
  sqlc.arg(description),
  sqlc.arg(enabled),
  sqlc.arg(created_by),
  sqlc.arg(updated_by)
)
RETURNING
  id,
  prompt_key,
  title,
  agent_key,
  tool_name,
  prompt_text,
  variables,
  tags,
  description,
  enabled,
  created_by,
  updated_by,
  created_at,
  updated_at;

-- name: GetPromptTemplate :one
SELECT
  id,
  prompt_key,
  title,
  agent_key,
  tool_name,
  prompt_text,
  variables,
  tags,
  description,
  enabled,
  created_by,
  updated_by,
  created_at,
  updated_at
FROM prompt_templates
WHERE prompt_key = sqlc.arg(prompt_key)
LIMIT 1;
```

### 4.4 Go 示例

```go
package prompt

import (
	"context"
	"encoding/json"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func Create(ctx context.Context, q storedb.Querier) (storedb.PromptTemplate, error) {
	return q.CreatePromptTemplate(ctx, storedb.CreatePromptTemplateParams{
		PromptKey:   "summary/default",
		Title:       "Summary Default",
		AgentKey:    "writer",
		ToolName:    "none",
		PromptText:  "Summarize the input.",
		Variables:   json.RawMessage(`["input"]`),
		Tags:        json.RawMessage(`["summary"]`),
		Description: "default summary prompt",
		Enabled:     true,
		CreatedBy:   "system",
		UpdatedBy:   "system",
	})
}
```

## 5. 命名规范

### 5.1 契约

- query 名就是 Go 方法名，必须稳定且语义明确。
- 统一用动词前缀：`Create`、`Get`、`List`、`Count`、`Exists`、`Update`、`Patch`、`Delete`、`Upsert`、`Acquire`、`Claim`、`Bind`、`Touch`、`Reclaim`。
- 不使用模糊命名：`Save`、`Handle`、`Do`、`Query`、`Run`。
- 如果 query 带 `FOR UPDATE`，方法名必须以 `ForUpdate` 结尾。
- 如果 query 是 cursor 分页，方法名必须带 `Cursor` 后缀。
- 如果 query 是部分更新，方法名必须带 `Partial` 或 `Patch`。

### 5.2 规范映射

| SQL 注释 | Go 方法 |
| --- | --- |
| `-- name: GetAgentThread :one` | `GetAgentThread(ctx, GetAgentThreadParams)` |
| `-- name: ListAgentThreads :many` | `ListAgentThreads(ctx, ListAgentThreadsParams)` |
| `-- name: UpdateWorkspaceRunPartial :one` | `UpdateWorkspaceRunPartial(ctx, UpdateWorkspaceRunPartialParams)` |
| `-- name: DeleteSharedFile :execrows` | `DeleteSharedFile(ctx, DeleteSharedFileParams)` |
| `-- name: ClaimDueWakeups :many` | `ClaimDueWakeups(ctx, ClaimDueWakeupsParams)` |

### 5.3 SQL 示例

```sql
-- sql/queries/agent_thread.sql

-- name: GetAgentThread :one
SELECT
  thread_id,
  prompt,
  cwd,
  status,
  port,
  created_at,
  updated_at
FROM agent_threads
WHERE thread_id = sqlc.arg(thread_id)
LIMIT 1;

-- name: ListAgentThreads :many
SELECT
  thread_id,
  prompt,
  cwd,
  status,
  port,
  created_at,
  updated_at
FROM agent_threads
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
ORDER BY updated_at DESC, thread_id DESC
LIMIT sqlc.arg(limit_count)
OFFSET sqlc.arg(offset_count);
```

### 5.4 Go 示例

```go
package threads

import (
	"context"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func Load(ctx context.Context, q storedb.Querier, threadID string) (storedb.AgentThread, []storedb.AgentThread, error) {
	item, err := q.GetAgentThread(ctx, storedb.GetAgentThreadParams{
		ThreadID: threadID,
	})
	if err != nil {
		return storedb.AgentThread{}, nil, err
	}

	items, err := q.ListAgentThreads(ctx, storedb.ListAgentThreadsParams{
		LimitCount:  20,
		OffsetCount: 0,
	})
	return item, items, err
}
```

## 6. CRUD 查询范式

### 6.1 契约

- `Create` 使用 `INSERT ... RETURNING explicit columns`。
- `Get` 使用主键或唯一键查询，并显式 `LIMIT 1`。
- `List` 必须带稳定 `ORDER BY`，并明确 `LIMIT`/`OFFSET`。
- `Update` 使用 `RETURNING explicit columns`，便于服务层直接拿到落库结果。
- `Delete` 默认使用 `:execrows`，服务层据此判断是否删除成功。
- `SELECT *` 和 `RETURNING *` 在长期契约中默认禁止，统一列出显式列清单。

### 6.2 SQL 示例

```sql
-- sql/queries/command_card.sql

-- name: CreateCommandCard :one
INSERT INTO command_cards (
  card_key,
  title,
  description,
  command_template,
  args_schema,
  risk_level,
  enabled,
  created_by,
  updated_by
) VALUES (
  sqlc.arg(card_key),
  sqlc.arg(title),
  sqlc.arg(description),
  sqlc.arg(command_template),
  sqlc.arg(args_schema),
  sqlc.arg(risk_level),
  sqlc.arg(enabled),
  sqlc.arg(created_by),
  sqlc.arg(updated_by)
)
RETURNING
  id,
  card_key,
  title,
  description,
  command_template,
  args_schema,
  risk_level,
  enabled,
  created_by,
  updated_by,
  created_at,
  updated_at,
  last_run_at,
  run_count;

-- name: GetCommandCard :one
SELECT
  id,
  card_key,
  title,
  description,
  command_template,
  args_schema,
  risk_level,
  enabled,
  created_by,
  updated_by,
  created_at,
  updated_at,
  last_run_at,
  run_count
FROM command_cards
WHERE card_key = sqlc.arg(card_key)
LIMIT 1;

-- name: ListCommandCards :many
SELECT
  id,
  card_key,
  title,
  description,
  command_template,
  args_schema,
  risk_level,
  enabled,
  created_by,
  updated_by,
  created_at,
  updated_at,
  last_run_at,
  run_count
FROM command_cards
WHERE (sqlc.narg(keyword)::text IS NULL OR title ILIKE '%' || sqlc.narg(keyword)::text || '%' OR description ILIKE '%' || sqlc.narg(keyword)::text || '%')
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(limit_count)
OFFSET sqlc.arg(offset_count);

-- name: UpdateCommandCard :one
UPDATE command_cards
SET
  title = sqlc.arg(title),
  description = sqlc.arg(description),
  command_template = sqlc.arg(command_template),
  args_schema = sqlc.arg(args_schema),
  risk_level = sqlc.arg(risk_level),
  enabled = sqlc.arg(enabled),
  updated_by = sqlc.arg(updated_by),
  updated_at = NOW()
WHERE card_key = sqlc.arg(card_key)
RETURNING
  id,
  card_key,
  title,
  description,
  command_template,
  args_schema,
  risk_level,
  enabled,
  created_by,
  updated_by,
  created_at,
  updated_at,
  last_run_at,
  run_count;

-- name: DeleteCommandCard :execrows
DELETE FROM command_cards
WHERE card_key = sqlc.arg(card_key);
```

### 6.3 Go 示例

```go
package commandcard

import (
	"context"
	"encoding/json"
	"errors"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type Repository struct {
	q storedb.Querier
}

func NewRepository(q storedb.Querier) *Repository {
	return &Repository{q: q}
}

func (r *Repository) Create(ctx context.Context) (storedb.CommandCard, error) {
	return r.q.CreateCommandCard(ctx, storedb.CreateCommandCardParams{
		CardKey:         "workspace.merge",
		Title:           "Merge Workspace",
		Description:     "merge workspace changes",
		CommandTemplate: "workspace merge {{.run_key}}",
		ArgsSchema:      json.RawMessage(`{"type":"object"}`),
		RiskLevel:       "medium",
		Enabled:         true,
		CreatedBy:       "system",
		UpdatedBy:       "system",
	})
}

func (r *Repository) Delete(ctx context.Context, cardKey string) error {
	affected, err := r.q.DeleteCommandCard(ctx, storedb.DeleteCommandCardParams{
		CardKey: cardKey,
	})
	if err != nil {
		return err
	}
	if affected == 0 {
		return errors.New("command card not found")
	}
	return nil
}
```

## 7. 动态过滤范式

### 7.1 契约

- 优先使用 `sqlc.narg` 表达可选过滤条件。
- 少量可选条件适合 `narg`；大量组合条件不要硬堆一个 mega query。
- 部分更新使用 `COALESCE(sqlc.narg(...), column)`。
- 如果业务需要“显式设为 NULL”，不要复用 `COALESCE` 模式，必须写专用 query。

### 7.2 SQL 示例

```sql
-- sql/queries/workspace_run.sql

-- name: ListWorkspaceRuns :many
SELECT
  id,
  run_key,
  dag_key,
  source_root,
  workspace_path,
  status,
  created_by,
  updated_by,
  metadata,
  created_at,
  updated_at,
  finished_at
FROM workspace_runs
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
  AND (sqlc.narg(dag_key)::text IS NULL OR dag_key = sqlc.narg(dag_key)::text)
  AND (sqlc.narg(source_root_prefix)::text IS NULL OR source_root LIKE sqlc.narg(source_root_prefix)::text || '%')
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(limit_count)
OFFSET sqlc.arg(offset_count);

-- name: UpdateWorkspaceRunPartial :one
UPDATE workspace_runs
SET
  status = COALESCE(sqlc.narg(status), status),
  updated_by = COALESCE(sqlc.narg(updated_by), updated_by),
  metadata = COALESCE(sqlc.narg(metadata), metadata),
  finished_at = COALESCE(sqlc.narg(finished_at), finished_at),
  updated_at = NOW()
WHERE run_key = sqlc.arg(run_key)
RETURNING
  id,
  run_key,
  dag_key,
  source_root,
  workspace_path,
  status,
  created_by,
  updated_by,
  metadata,
  created_at,
  updated_at,
  finished_at;
```

### 7.3 Go 示例

```go
package workspace

import (
	"context"
	"encoding/json"
	"time"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func Filtered(ctx context.Context, q storedb.Querier) ([]storedb.WorkspaceRun, error) {
	status := "active"
	prefix := "/tmp/workspaces"
	return q.ListWorkspaceRuns(ctx, storedb.ListWorkspaceRunsParams{
		Status:           &status,
		SourceRootPrefix: &prefix,
		LimitCount:       50,
		OffsetCount:      0,
	})
}

func Patch(ctx context.Context, q storedb.Querier, runKey string) (storedb.WorkspaceRun, error) {
	status := "merged"
	updatedBy := "workspace-merge"
	finishedAt := time.Now().UTC()

	return q.UpdateWorkspaceRunPartial(ctx, storedb.UpdateWorkspaceRunPartialParams{
		RunKey:     runKey,
		Status:     &status,
		UpdatedBy:  &updatedBy,
		Metadata:   json.RawMessage(`{"merge_mode":"fast-forward"}`),
		FinishedAt: &finishedAt,
	})
}
```

## 8. 分页范式

### 8.1 契约

- 小表和后台管理页使用 `LIMIT/OFFSET`。
- 追加型日志表、大表、时间序列表使用 cursor-based 分页。
- cursor 分页必须使用稳定排序键，通常是 `(created_at, id)` 或 `(ts, id)`。
- cursor 编解码在 service 层处理，不放进生成代码。

### 8.2 SQL 示例

```sql
-- sql/queries/system_log.sql

-- name: ListSystemLogsPage :many
SELECT
  id,
  ts,
  level,
  logger,
  message,
  raw,
  source,
  component,
  agent_id,
  thread_id,
  trace_id,
  event_type,
  tool_name,
  duration_ms,
  extra
FROM system_logs
WHERE (sqlc.narg(level)::text IS NULL OR level = sqlc.narg(level)::text)
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(limit_count)
OFFSET sqlc.arg(offset_count);

-- name: ListSystemLogsCursor :many
SELECT
  id,
  ts,
  level,
  logger,
  message,
  raw,
  source,
  component,
  agent_id,
  thread_id,
  trace_id,
  event_type,
  tool_name,
  duration_ms,
  extra
FROM system_logs
WHERE (sqlc.narg(level)::text IS NULL OR level = sqlc.narg(level)::text)
  AND (
    sqlc.narg(cursor_ts)::timestamptz IS NULL
    OR (ts, id) < (sqlc.narg(cursor_ts)::timestamptz, sqlc.narg(cursor_id)::bigint)
  )
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(limit_count);
```

### 8.3 Go 示例

```go
package systemlog

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type Cursor struct {
	TS time.Time `json:"ts"`
	ID int64     `json:"id"`
}

func ListByCursor(ctx context.Context, q storedb.Querier, encoded string) ([]storedb.SystemLog, string, error) {
	var arg storedb.ListSystemLogsCursorParams
	arg.LimitCount = 100

	if encoded != "" {
		raw, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, "", err
		}
		var cur Cursor
		if err := json.Unmarshal(raw, &cur); err != nil {
			return nil, "", err
		}
		arg.CursorTS = &cur.TS
		arg.CursorID = &cur.ID
	}

	items, err := q.ListSystemLogsCursor(ctx, arg)
	if err != nil {
		return nil, "", err
	}
	if len(items) == 0 {
		return items, "", nil
	}

	last := items[len(items)-1]
	nextRaw, err := json.Marshal(Cursor{TS: last.Ts, ID: last.ID})
	if err != nil {
		return nil, "", err
	}
	return items, base64.StdEncoding.EncodeToString(nextRaw), nil
}
```

## 9. JSONB 列范式

### 9.1 契约

- V3 默认把 `json`/`jsonb` 映射为 `json.RawMessage`。
- 生成模型只承载原始 JSON，不直接承载 `map[string]any`。
- JSON 反序列化发生在 service 或 DTO adapter 层。
- 使用 `@>`、`->>`、`jsonb_set` 等 PostgreSQL 原生能力，不回退到 Go 侧手工过滤。
- 热路径 JSONB 查询必须配套 GIN 或表达式索引。

### 9.2 SQL 示例

```sql
-- sql/queries/ui_preference.sql

-- name: GetUIPreference :one
SELECT
  cwd,
  key,
  value,
  updated_at
FROM ui_preferences
WHERE cwd = sqlc.arg(cwd)
  AND key = sqlc.arg(key)
LIMIT 1;

-- name: ListUIPreferencesByJSONPath :many
SELECT
  cwd,
  key,
  value,
  updated_at
FROM ui_preferences
WHERE value @> sqlc.arg(value_filter)::jsonb
  AND (sqlc.narg(theme)::text IS NULL OR value ->> 'theme' = sqlc.narg(theme)::text)
ORDER BY updated_at DESC
LIMIT sqlc.arg(limit_count);
```

### 9.3 Go 示例

```go
package uipref

import (
	"context"
	"encoding/json"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type PreferenceValue struct {
	Theme   string `json:"theme"`
	Compact bool   `json:"compact"`
}

func Load(ctx context.Context, q storedb.Querier, cwd, key string) (PreferenceValue, error) {
	row, err := q.GetUIPreference(ctx, storedb.GetUIPreferenceParams{
		Cwd: cwd,
		Key: key,
	})
	if err != nil {
		return PreferenceValue{}, err
	}

	var out PreferenceValue
	if err := json.Unmarshal(row.Value, &out); err != nil {
		return PreferenceValue{}, err
	}
	return out, nil
}

func FindDarkTheme(ctx context.Context, q storedb.Querier) ([]storedb.UIPreference, error) {
	theme := "dark"
	return q.ListUIPreferencesByJSONPath(ctx, storedb.ListUIPreferencesByJSONPathParams{
		ValueFilter: json.RawMessage(`{"compact":true}`),
		Theme:       &theme,
		LimitCount:  100,
	})
}
```

## 10. Upsert 范式

### 10.1 契约

- 幂等写入优先用 `INSERT ... ON CONFLICT ... DO UPDATE`。
- `ON CONFLICT` 的目标必须对应唯一键或主键。
- Upsert 语义必须清晰说明“幂等更新”还是“冲突即业务错误”。
- 不能把“静默重绑定”藏进 upsert；如果冲突表示逻辑错误，就应该返回错误。

### 10.2 SQL 示例

```sql
-- sql/queries/agent_provider_binding.sql

-- name: UpsertAgentProviderBinding :one
INSERT INTO agent_provider_bindings (
  agent_id,
  provider,
  provider_thread_id,
  codex_thread_id,
  session_uuid,
  rollout_path,
  cwd,
  archived
) VALUES (
  sqlc.arg(agent_id),
  sqlc.arg(provider),
  sqlc.arg(provider_thread_id),
  sqlc.arg(codex_thread_id),
  sqlc.narg(session_uuid),
  sqlc.arg(rollout_path),
  sqlc.arg(cwd),
  FALSE
)
ON CONFLICT (agent_id) DO UPDATE SET
  provider = EXCLUDED.provider,
  provider_thread_id = EXCLUDED.provider_thread_id,
  codex_thread_id = EXCLUDED.codex_thread_id,
  session_uuid = EXCLUDED.session_uuid,
  rollout_path = EXCLUDED.rollout_path,
  cwd = EXCLUDED.cwd,
  archived = EXCLUDED.archived,
  updated_at = NOW()
RETURNING
  agent_id,
  provider,
  provider_thread_id,
  codex_thread_id,
  session_uuid,
  rollout_path,
  cwd,
  archived,
  created_at,
  updated_at;
```

### 10.3 Go 示例

```go
package binding

import (
	"context"

	"github.com/google/uuid"
	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func Upsert(ctx context.Context, q storedb.Querier, agentID string) (storedb.AgentProviderBinding, error) {
	sessionUUID := uuid.New()
	return q.UpsertAgentProviderBinding(ctx, storedb.UpsertAgentProviderBindingParams{
		AgentID:          agentID,
		Provider:         "codex",
		ProviderThreadID: "provider-thread-1",
		CodexThreadID:    "legacy-thread-1",
		SessionUUID:      &sessionUUID,
		RolloutPath:      "/tmp/rollout",
		Cwd:              "/tmp/project",
	})
}
```

## 11. 批量操作范式

### 11.1 契约

- 中小批量逐条执行，优先 `:batchexec`、`:batchone`、`:batchmany`。
- 大批量插入优先 `:copyfrom`。
- `:copyfrom` 需要 `sql_driver` 配置。
- 业务层必须自行限制批量大小，避免单次 batch 过大。
- batch 的错误处理必须逐项记录，不能吞错误。

### 11.2 SQL 示例

```sql
-- sql/queries/agent_status_batch.sql

-- name: DeleteAgentStatuses :batchexec
DELETE FROM agent_statuses
WHERE agent_id = $1;

-- name: BulkInsertAgentStatuses :copyfrom
INSERT INTO agent_statuses (
  agent_id,
  agent_name,
  session_id,
  status,
  output_tail,
  error
) VALUES ($1, $2, $3, $4, $5, $6);
```

### 11.3 Go 示例

```go
package agentstatus

import (
	"context"
	"encoding/json"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func DeleteMany(ctx context.Context, q *storedb.Queries, ids []string) error {
	args := make([]storedb.DeleteAgentStatusesParams, 0, len(ids))
	for _, id := range ids {
		args = append(args, storedb.DeleteAgentStatusesParams{AgentID: id})
	}

	br := q.DeleteAgentStatuses(ctx, args)
	var firstErr error
	br.Exec(func(_ int, err error) {
		if firstErr == nil && err != nil {
			firstErr = err
		}
	})
	return firstErr
}

func BulkInsert(ctx context.Context, q storedb.Querier) (int64, error) {
	return q.BulkInsertAgentStatuses(ctx, []storedb.BulkInsertAgentStatusesParams{
		{
			AgentID:    "agent-1",
			AgentName:  "Agent 1",
			Status:     storedb.AgentStatusKindIdle,
			OutputTail: json.RawMessage(`[]`),
			Error:      "",
		},
		{
			AgentID:    "agent-2",
			AgentName:  "Agent 2",
			Status:     storedb.AgentStatusKindRunning,
			OutputTail: json.RawMessage(`["booting"]`),
			Error:      "",
		},
	})
}
```

## 12. CTE / 复杂查询范式

### 12.1 契约

- 复杂查询优先用 PostgreSQL 原生 SQL 表达，而不是回退到 Go 侧拼装。
- 对于“选一批候选行再更新并返回”的流程，优先用 `WITH ... UPDATE ... RETURNING`。
- 对于 join 读模型，必要时使用 `sqlc.embed(...)`，避免扁平化字段爆炸。
- 如果 query 的业务语义超过 40 行，必须写清注释说明阶段和锁语义。

### 12.2 SQL 示例

```sql
-- sql/queries/task_dag_wakeup.sql

-- name: ClaimDueWakeups :many
WITH candidate AS (
  SELECT id
  FROM task_dag_wakeups
  WHERE status IN ('pending', 'retrying')
    AND next_retry_at <= NOW()
  ORDER BY next_retry_at ASC, id ASC
  LIMIT sqlc.arg(limit_count)
  FOR UPDATE SKIP LOCKED
)
UPDATE task_dag_wakeups AS w
SET
  status = 'dispatching',
  claimed_by = sqlc.arg(claimed_by),
  claimed_at = NOW(),
  lease_expires_at = sqlc.arg(lease_expires_at),
  updated_at = NOW()
FROM candidate c
WHERE w.id = c.id
RETURNING
  w.id,
  w.dag_key,
  w.node_key,
  w.wakeup_kind,
  w.target_agent_id,
  w.prompt_payload,
  w.idempotency_key,
  w.status,
  w.attempt_count,
  w.next_retry_at,
  w.claimed_at,
  w.claimed_by,
  w.lease_expires_at,
  w.sent_at,
  w.bound_turn_id,
  w.turn_bound_at,
  w.last_error,
  w.created_at,
  w.updated_at;
```

### 12.3 Go 示例

```go
package dag

import (
	"context"
	"time"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func Claim(ctx context.Context, q storedb.Querier, worker string, limit int32) ([]storedb.TaskDagWakeup, error) {
	expiresAt := time.Now().UTC().Add(30 * time.Second)
	return q.ClaimDueWakeups(ctx, storedb.ClaimDueWakeupsParams{
		ClaimedBy:      worker,
		LeaseExpiresAt: expiresAt,
		LimitCount:     limit,
	})
}
```

## 13. 事务范式

### 13.1 契约

- 事务入口在 hand-written service，不在生成代码里。
- 事务内统一使用 `q.WithTx(tx)` 切换到事务 querier。
- 复杂工作流使用 `pgx.TxOptions` 明确隔离级别。
- 生成 query 不携带事务语义；事务语义由调用点组合。
- V2 的 `RunTx` helper 可以保留，但只保留一份全局实现，不再每个 Store 都自行包装。

### 13.2 SQL 示例

```sql
-- sql/queries/agent_provider_binding_tx.sql

-- name: DeleteAgentProviderBindingByAgentID :exec
DELETE FROM agent_provider_bindings
WHERE agent_id = sqlc.arg(agent_id);

-- name: InsertAgentProviderBinding :exec
INSERT INTO agent_provider_bindings (
  agent_id,
  provider,
  provider_thread_id,
  codex_thread_id,
  session_uuid,
  rollout_path,
  cwd,
  archived
) VALUES (
  sqlc.arg(agent_id),
  sqlc.arg(provider),
  sqlc.arg(provider_thread_id),
  sqlc.arg(codex_thread_id),
  sqlc.narg(session_uuid),
  sqlc.arg(rollout_path),
  sqlc.arg(cwd),
  FALSE
);
```

### 13.3 Go 示例

```go
package binding

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type TxService struct {
	pool *pgxpool.Pool
	q    *storedb.Queries
}

func NewTxService(pool *pgxpool.Pool, q *storedb.Queries) *TxService {
	return &TxService{pool: pool, q: q}
}

func (s *TxService) Rebind(ctx context.Context, agentID string) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	qtx := s.q.WithTx(tx)
	if err := qtx.DeleteAgentProviderBindingByAgentID(ctx, storedb.DeleteAgentProviderBindingByAgentIDParams{
		AgentID: agentID,
	}); err != nil {
		return err
	}

	sessionUUID := uuid.New()
	if err := qtx.InsertAgentProviderBinding(ctx, storedb.InsertAgentProviderBindingParams{
		AgentID:          agentID,
		Provider:         "codex",
		ProviderThreadID: "new-provider-thread",
		CodexThreadID:    "new-legacy-thread",
		SessionUUID:      &sessionUUID,
		RolloutPath:      "/tmp/rebind",
		Cwd:              "/tmp/project",
	}); err != nil {
		return err
	}

	return tx.Commit(ctx)
}
```

## 14. 自定义类型映射范式

### 14.1 契约

- `UUID` 统一映射到 `github.com/google/uuid.UUID`。
- `timestamp`/`timestamptz` 统一映射到 `time.Time` 和 `*time.Time`。
- enum 保留 `sqlc` 生成的强类型别名，不回退到裸 `string`。
- `jsonb` 统一映射到 `json.RawMessage`。
- nullable scalar 优先配合 `emit_pointers_for_null_types` 使用指针，不回退到 `sql.NullString` 风格。

### 14.2 SQL 示例

```sql
-- migrations/000010_agent_provider_binding.up.sql

CREATE TYPE provider_kind AS ENUM ('codex', 'openai', 'claude');

CREATE TABLE agent_provider_bindings (
  agent_id TEXT PRIMARY KEY,
  provider provider_kind NOT NULL,
  provider_thread_id TEXT NOT NULL,
  codex_thread_id TEXT NOT NULL,
  session_uuid UUID,
  metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
  archived_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
```

### 14.3 Go 示例

```go
package types

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

type AgentProviderBinding struct {
	AgentID          string
	Provider         ProviderKind
	ProviderThreadID string
	CodexThreadID    string
	SessionUUID      *uuid.UUID
	Metadata         json.RawMessage
	ArchivedAt       *time.Time
	CreatedAt        time.Time
	UpdatedAt        time.Time
}
```

## 15. `Querier` 接口范式

### 15.1 契约

- `emit_interface: true` 是强制项。
- 默认依赖生成的 `sqlc.Querier`，不要手写一份同名大接口。
- 如果 service 只用到少数方法，可以在 service 包里定义窄接口。
- mock 测试优先基于窄接口；集成测试直接使用真实 `*sqlc.Queries`。

### 15.2 SQL 示例

```sql
-- sql/queries/agent_status_read.sql

-- name: GetAgentStatus :one
SELECT
  agent_id,
  agent_name,
  session_id,
  status,
  output_tail,
  error,
  created_at,
  updated_at
FROM agent_statuses
WHERE agent_id = sqlc.arg(agent_id)
LIMIT 1;

-- name: ListAgentStatuses :many
SELECT
  agent_id,
  agent_name,
  session_id,
  status,
  output_tail,
  error,
  created_at,
  updated_at
FROM agent_statuses
ORDER BY updated_at DESC
LIMIT sqlc.arg(limit_count)
OFFSET sqlc.arg(offset_count);
```

### 15.3 Go 示例

```go
package runtime

import (
	"context"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

type AgentStatusReader interface {
	GetAgentStatus(ctx context.Context, arg storedb.GetAgentStatusParams) (storedb.AgentStatus, error)
	ListAgentStatuses(ctx context.Context, arg storedb.ListAgentStatusesParams) ([]storedb.AgentStatus, error)
}

type DashboardService struct {
	q AgentStatusReader
}

func NewDashboardService(q AgentStatusReader) *DashboardService {
	return &DashboardService{q: q}
}

func (s *DashboardService) List(ctx context.Context) ([]storedb.AgentStatus, error) {
	return s.q.ListAgentStatuses(ctx, storedb.ListAgentStatusesParams{
		LimitCount:  100,
		OffsetCount: 0,
	})
}
```

## 16. 与 `fx` 集成

### 16.1 契约

- `fx` 负责提供 `Config`、`*pgxpool.Pool`、`*sqlc.Queries`、`sqlc.Querier`。
- `*sqlc.Queries` 本身没有生命周期，不需要 `OnStart`/`OnStop`。
- 连接池生命周期由 `fx.Lifecycle` 管理。
- service 层应该注入 `sqlc.Querier` 或窄接口，而不是自己 `sqlc.New(pool)`。
- 当前 V3 的 `internal/store/module.go` 应从手写 `Queries` 壳切换为生成代码入口。

### 16.2 SQL 示例

```sql
-- sql/queries/task_ack.sql

-- name: ListTaskAcks :many
SELECT
  id,
  ack_key,
  title,
  description,
  assigned_to,
  requested_by,
  priority,
  status,
  progress,
  ack_message,
  result_summary,
  metadata,
  due_at,
  acked_at,
  started_at,
  finished_at,
  created_at,
  updated_at
FROM task_acks
WHERE (sqlc.narg(status)::text IS NULL OR status = sqlc.narg(status)::text)
ORDER BY updated_at DESC, id DESC
LIMIT sqlc.arg(limit_count)
OFFSET sqlc.arg(offset_count);
```

### 16.3 Go 示例

```go
package store

import (
	"context"
	"log/slog"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/fx"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

var StoreModule = fx.Module("store",
	fx.Provide(
		NewConfig,
		NewPool,
		NewQueries,
		NewQuerier,
	),
	fx.Invoke(RegisterLifecycle),
)

func NewQueries(pool *pgxpool.Pool) *storedb.Queries {
	return storedb.New(pool)
}

func NewQuerier(q *storedb.Queries) storedb.Querier {
	return q
}

func RegisterLifecycle(lc fx.Lifecycle, logger *slog.Logger, pool *pgxpool.Pool) {
	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			logger.Info("store ready")
			return nil
		},
		OnStop: func(context.Context) error {
			pool.Close()
			logger.Info("store stopped")
			return nil
		},
	})
}
```

## 17. 测试范式

### 17.1 契约

- 查询正确性优先靠真实 PostgreSQL 集成测试，不靠 `sqlite` 替代。
- 集成测试使用 `testcontainers-go` + PostgreSQL 容器。
- 单元测试通过 `Querier` 窄接口 + mock 或 stub。
- 当前仓库未显式使用现成 mock 生成工具，但依赖图已包含 `go.uber.org/mock`，可作为首选。
- 涉及 SQL 语义、索引、锁、事务隔离的行为必须做 integration test。

### 17.2 SQL 示例

```sql
-- sql/queries/shared_file_testable.sql

-- name: WriteSharedFile :one
INSERT INTO shared_files (
  path,
  content,
  updated_by
) VALUES (
  sqlc.arg(path),
  sqlc.arg(content),
  sqlc.arg(updated_by)
)
ON CONFLICT (path) DO UPDATE SET
  content = EXCLUDED.content,
  updated_by = EXCLUDED.updated_by,
  updated_at = NOW()
RETURNING
  path,
  content,
  updated_by,
  created_at,
  updated_at;
```

### 17.3 Go 示例

```go
package store_test

import (
	"context"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go/modules/postgres"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

func TestWriteSharedFile(t *testing.T) {
	ctx := context.Background()

	pg, err := postgres.Run(ctx,
		"postgres:16-alpine",
		postgres.WithDatabase("super_agent_v3"),
		postgres.WithUsername("postgres"),
		postgres.WithPassword("postgres"),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pg.Terminate(ctx) })

	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	q := storedb.New(pool)
	_, err = q.WriteSharedFile(ctx, storedb.WriteSharedFileParams{
		Path:      "docs/readme.md",
		Content:   "hello",
		UpdatedBy: "tester",
	})
	if err != nil {
		t.Fatal(err)
	}
}
```

## 18. Schema 迁移范式

### 18.1 选型结论

| 方案 | 结论 | 说明 |
| --- | --- | --- |
| `golang-migrate` | 推荐主方案 | 纯 SQL 文件友好，和现有 `migrations/` 最接近，迁移成本最低。 |
| Atlas | 次选增强方案 | schema-as-code、diff、drift detection 很强，但 phase 1 引入成本更高。 |
| Goose | 可用但非首选 | 使用体验简单，但在团队规范、生态心智和严格 up/down 管理上不如 `golang-migrate` 收敛。 |

### 18.2 契约

- V3 的迁移文件继续存放在 `migrations/`。
- phase 1 优先采用 `golang-migrate`。
- `sqlc` 只消费 schema，不负责执行 migration。
- CI 默认流程：执行 migration → `sqlc generate` → `sqlc vet` → `go test`。
- `sqlc verify` 是可选增强项，只有在团队接受 `sqlc cloud` 工作流时才启用。

### 18.3 SQL 示例

```sql
-- migrations/000011_agent_statuses.up.sql

CREATE TYPE agent_status_kind AS ENUM ('idle', 'running', 'stagnant', 'error', 'stopped', 'unknown');

CREATE TABLE agent_statuses (
  agent_id TEXT PRIMARY KEY,
  agent_name TEXT NOT NULL,
  session_id UUID,
  status agent_status_kind NOT NULL,
  stagnant_sec INTEGER NOT NULL DEFAULT 0,
  error TEXT NOT NULL DEFAULT '',
  output_tail JSONB NOT NULL DEFAULT '[]'::jsonb,
  created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_agent_statuses_status_updated_at
  ON agent_statuses (status, updated_at DESC);
```

### 18.4 Go 示例

```go
package migrate

import (
	"fmt"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func Up(databaseURL string) error {
	m, err := migrate.New("file://migrations", databaseURL)
	if err != nil {
		return err
	}
	defer m.Close()

	if err := m.Up(); err != nil && err != migrate.ErrNoChange {
		return fmt.Errorf("migrate up: %w", err)
	}
	return nil
}
```

## 19. 反模式

### 19.1 契约

- 禁止在 service 里重新写 raw SQL，与 `sqlc` 并行演化。
- 禁止手改 `internal/store/sqlc/*.go`。
- 禁止长期使用 `SELECT *`。
- 禁止把 10 个可选条件全塞进一个不可维护的 mega query。
- 禁止把 `sqlc` 当 ORM，用它去承载领域行为。
- 禁止对动态用户 SQL 误用 `sqlc`。

### 19.2 SQL 示例

```sql
-- bad
-- name: ListAnything :many
SELECT *
FROM system_logs
WHERE (sqlc.narg(level)::text IS NULL OR level = sqlc.narg(level)::text)
  AND (sqlc.narg(source)::text IS NULL OR source = sqlc.narg(source)::text)
  AND (sqlc.narg(component)::text IS NULL OR component = sqlc.narg(component)::text)
  AND (sqlc.narg(agent_id)::text IS NULL OR agent_id = sqlc.narg(agent_id)::text)
  AND (sqlc.narg(thread_id)::text IS NULL OR thread_id = sqlc.narg(thread_id)::text)
  AND (sqlc.narg(trace_id)::text IS NULL OR trace_id = sqlc.narg(trace_id)::text)
  AND (sqlc.narg(event_type)::text IS NULL OR event_type = sqlc.narg(event_type)::text)
  AND (sqlc.narg(tool_name)::text IS NULL OR tool_name = sqlc.narg(tool_name)::text)
  AND (sqlc.narg(keyword)::text IS NULL OR raw ILIKE '%' || sqlc.narg(keyword)::text || '%')
ORDER BY ts DESC
LIMIT sqlc.arg(limit_count);

-- good
-- name: ListSystemLogsByLevel :many
SELECT
  id,
  ts,
  level,
  logger,
  message,
  raw,
  source,
  component,
  agent_id,
  thread_id,
  trace_id,
  event_type,
  tool_name,
  duration_ms,
  extra
FROM system_logs
WHERE (sqlc.narg(level)::text IS NULL OR level = sqlc.narg(level)::text)
ORDER BY ts DESC, id DESC
LIMIT sqlc.arg(limit_count)
OFFSET sqlc.arg(offset_count);
```

### 19.3 Go 示例

```go
package anti

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// bad: 绕过 sqlc，重新写 SQL。
func Bad(ctx context.Context, pool *pgxpool.Pool, level string) error {
	_, err := pool.Query(ctx, "SELECT * FROM system_logs WHERE level = $1", level)
	return err
}

// good: 所有静态查询统一走生成代码。
func Good(ctx context.Context, q storedb.Querier, level string) ([]storedb.SystemLog, error) {
	return q.ListSystemLogsByLevel(ctx, storedb.ListSystemLogsByLevelParams{
		Level:       &level,
		LimitCount:  100,
		OffsetCount: 0,
	})
}
```

## 20. 20 个 Store 迁移指南

### 20.1 总体策略

V3 不再机械复制 V2 的“一个表一个手写 Store + 一个 BaseStore”模型。

迁移后的目标形态是：

- `sql/queries/*.sql` 承载静态 SQL。
- `internal/store/sqlc` 承载生成代码。
- `internal/store/repositories/*` 只保留少量 hand-written 领域封装。
- 简单 CRUD 表不再保留独立 Store 类型。
- 复杂事务表保留 hand-written service，但底层 query 由 `sqlc` 生成。

### 20.2 迁移分期

| Phase | 范围 | 说明 |
| --- | --- | --- |
| Phase 0 | 基础设施 | 固化 `sqlc.yaml`、生成目录、`fx` wiring、全局 tx helper。 |
| Phase 1 | 低风险 CRUD | `AgentStatus`、`SharedFile`、`CommandCard`、`PromptTemplate`、`TaskAck`。 |
| Phase 2 | 读多写少/过滤类 | `AuditLog`、`SystemLog`、`AILog`、`BusLog`、`Interaction`、`TaskTrace`、`UIPreference`。 |
| Phase 3 | 状态实体 | `AgentThread`、`WorkspaceRun`、`TopologyApproval`、`CwdLock`。 |
| Phase 4 | 复杂事务 | `AgentProviderBinding`、`AgentThreadBinding`、`TaskDAG`。 |
| Phase E | 例外治理 | `DBQuery` 不迁到 `sqlc`，迁出 Store 体系为特例组件。 |

### 20.3 单表迁移清单

| V2 Store | V3 query 文件 | 迁移方式 | 备注 |
| --- | --- | --- | --- |
| `AgentProviderBinding` | `agent_provider_binding.sql` | `sqlc` + tx service | upsert 与唯一冲突语义要保留。 |
| `AgentStatus` | `agent_status.sql` | 纯 `sqlc` | 标准 CRUD + upsert。 |
| `AgentThread` | `agent_thread.sql` | `sqlc` + 小型 repo | 运行态查询和 recoverable 列表分开。 |
| `AgentThreadBinding` | `agent_thread_binding.sql` + `agent_thread_binding_tx.sql` | `sqlc` + tx service | 旧表兼容和 rebind 最复杂。 |
| `AILog` | `ai_log.sql` | 纯 `sqlc` | append/list 即可。 |
| `AuditLog` | `audit_log.sql` | 纯 `sqlc` | append/list。 |
| `BusLog` | `bus_log.sql` | 纯 `sqlc` | append/list。 |
| `CommandCard` | `command_card.sql` | 纯 `sqlc` | 典型 CRUD。 |
| `CwdLock` | `cwd_lock.sql` | `sqlc` + tx helper | 锁获取与心跳保留并发语义。 |
| `DBQuery` | 不迁 | 例外 | 动态只读 SQL，不能被 `sqlc` 静态化。 |
| `Interaction` | `interaction.sql` | `sqlc` | create/get/list/review。 |
| `PromptTemplate` | `prompt_template.sql` | 纯 `sqlc` | 典型 CRUD。 |
| `SharedFile` | `shared_file.sql` | 纯 `sqlc` | write/read/list/delete。 |
| `SystemLog` | `system_log.sql` | `sqlc` | append/list/filter values。 |
| `TaskAck` | `task_ack.sql` | 纯 `sqlc` | save/list。 |
| `TaskDAG` | `task_dag.sql` + `task_dag_wakeup.sql` | `sqlc` + tx service | node/wakeup/lease 事务收敛。 |
| `TaskTrace` | `task_trace.sql` | `sqlc` | create/list。 |
| `TopologyApproval` | `topology_approval.sql` | `sqlc` | create/approve/reject/list pending。 |
| `UIPreference` | `ui_preference.sql` | `sqlc` + value adapter | JSONB 读写与 scope fallback。 |
| `WorkspaceRun` | `workspace_run.sql` | `sqlc` + repo | run/file 双表，适合一个 repo 封装。 |

### 20.4 迁移步骤模板

每个 Store 按以下顺序迁：

1. 把现有 raw SQL 提炼到 `sql/queries/<entity>.sql`。
2. 用显式列清单替换 `SELECT *` 和 `RowToStructByNameLax` 心智。
3. 运行 `sqlc generate`。
4. 新增最薄兼容层，先保持旧调用点 API 不变。
5. 调用点切到生成 query。
6. 删除旧 Store 中的 raw SQL、列常量、scan 逻辑。
7. 最后再删除该 Store 类型本身。

### 20.5 SQL 示例

```sql
-- sql/queries/agent_status.sql

-- name: UpsertAgentStatus :one
INSERT INTO agent_statuses (
  agent_id,
  agent_name,
  session_id,
  status,
  stagnant_sec,
  error,
  output_tail
) VALUES (
  sqlc.arg(agent_id),
  sqlc.arg(agent_name),
  sqlc.narg(session_id),
  sqlc.arg(status),
  sqlc.arg(stagnant_sec),
  sqlc.arg(error),
  sqlc.arg(output_tail)
)
ON CONFLICT (agent_id) DO UPDATE SET
  agent_name = EXCLUDED.agent_name,
  session_id = EXCLUDED.session_id,
  status = EXCLUDED.status,
  stagnant_sec = EXCLUDED.stagnant_sec,
  error = EXCLUDED.error,
  output_tail = EXCLUDED.output_tail,
  updated_at = NOW()
RETURNING
  agent_id,
  agent_name,
  session_id,
  status,
  stagnant_sec,
  error,
  output_tail,
  created_at,
  updated_at;
```

### 20.6 Go 示例

```go
package compat

import (
	"context"
	"encoding/json"

	storedb "github.com/anthropic-ai/super-agent-v3/internal/store/sqlc"
)

// 迁移过渡期兼容层：先保留旧 API 形状，内部改走 sqlc。
type AgentStatusStore struct {
	q storedb.Querier
}

func NewAgentStatusStore(q storedb.Querier) *AgentStatusStore {
	return &AgentStatusStore{q: q}
}

func (s *AgentStatusStore) Upsert(ctx context.Context, agentID, name, status string) (storedb.AgentStatus, error) {
	return s.q.UpsertAgentStatus(ctx, storedb.UpsertAgentStatusParams{
		AgentID:     agentID,
		AgentName:   name,
		Status:      storedb.AgentStatusKind(status),
		StagnantSec: 0,
		Error:       "",
		OutputTail:  json.RawMessage(`[]`),
	})
}
```

## 21. 特别说明：`DBQueryStore`

`DBQueryStore` 是 V2 里唯一不适合迁到 `sqlc` 的组件。

原因不是它特殊，而是它的输入本身就是运行时动态 SQL。`sqlc` 的价值前提是“静态 SQL + 静态 schema + 生成契约”；一旦 query text 在运行时才出现，`sqlc` 无法提供静态分析和生成价值。

因此 V3 对 `DBQueryStore` 的处理是：

- 不迁到 `sqlc`。
- 从“Store 集合”中摘出，单独命名为 `ReadOnlySQLExecutor` 或 `QuerySandbox`。
- 保留只读 SQL 安全校验。
- 所有常规静态管理查询仍必须进 `sqlc`，不能因为存在 `DBQueryStore` 就偷懒回退。

### SQL 示例

```sql
-- 这是允许交给 sqlc 的静态查询
-- name: ListQueryAudit :many
SELECT
  id,
  actor,
  sql_text,
  created_at
FROM db_query_audits
ORDER BY created_at DESC
LIMIT sqlc.arg(limit_count)
OFFSET sqlc.arg(offset_count);
```

### Go 示例

```go
package rawquery

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

type ReadOnlySQLExecutor struct {
	pool *pgxpool.Pool
}

func NewReadOnlySQLExecutor(pool *pgxpool.Pool) *ReadOnlySQLExecutor {
	return &ReadOnlySQLExecutor{pool: pool}
}

func (e *ReadOnlySQLExecutor) Query(ctx context.Context, sqlText string, args ...any) error {
	_, err := e.pool.Query(ctx, sqlText, args...)
	return err
}
```

## 22. CI 与开发命令

建议的开发命令：

```bash
sqlc generate
sqlc vet
go test ./...
```

如果团队启用 `sqlc verify`：

```bash
sqlc push --tag main
sqlc verify --against main
```

如果团队使用 `golang-migrate`：

```bash
migrate -path ./migrations -database "$DATABASE_URL" up
sqlc generate
go test ./...
```

## 23. 参考资料

- `sqlc` Configuration: https://docs.sqlc.dev/en/latest/reference/config.html
- `sqlc` Transactions: https://docs.sqlc.dev/en/latest/howto/transactions.html
- `sqlc` Named parameters: https://docs.sqlc.dev/en/stable/howto/named_parameters.html
- `sqlc` Query annotations: https://docs.sqlc.dev/en/stable/reference/query-annotations.html
- `sqlc` Datatypes: https://docs.sqlc.dev/en/latest/reference/datatypes.html
- `sqlc` Overrides: https://docs.sqlc.dev/en/latest/howto/overrides.html
- `sqlc` Embedding: https://docs.sqlc.dev/en/stable/reference/macros.html
- `sqlc` Vet: https://docs.sqlc.dev/en/latest/howto/vet.html
- `sqlc` Verify: https://docs.sqlc.dev/en/v1.24.0/howto/verify.html
- `sqlx`: https://jmoiron.github.io/sqlx/
- GORM: https://gorm.io/
- ent: https://entgo.io/
- bun: https://bun.uptrace.dev/
- `golang-migrate`: https://github.com/golang-migrate/migrate
- Atlas: https://atlasgo.io/
- Goose: https://github.com/pressly/goose
- Testcontainers for Go: https://testcontainers.com/guides/getting-started-with-testcontainers-for-go/

## 24. 最终决策摘要

- V3 标准数据访问层采用 `sqlc + pgx/v5 + PostgreSQL`。
- `sqlc.yaml` 使用仓库根目录、`migrations/`、`sql/queries/`、`internal/store/sqlc/` 这一套固定布局。
- 简单 CRUD 不再保留 20 个手写 Store。
- 复杂事务通过 `pgx.Tx + Queries.WithTx` 组合，不再复制 `BaseStore` 模式。
- `DBQueryStore` 明确作为例外，不迁到 `sqlc`。
- migration 工具 phase 1 推荐 `golang-migrate`，`Atlas` 作为后续增强选项。
