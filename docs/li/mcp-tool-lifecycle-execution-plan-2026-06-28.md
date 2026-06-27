# MCP Tool Lifecycle Execution Plan 2026-06-28

## 状态

NEEDS_APPROVAL。

本 lane 已停止生产实现并撤回未获批准的 partial schema/sqlc 改动。原因是 ADR 0003 的 schema/store 第一切片在当前仓库事实下必须触及原允许范围之外的运行时 SQLite migration、根 sqlc 配置和 sqlc 生成物；继续实现前需要主控批准扩大写入范围。

## 仓库事实

- README 明确产品 DB migration 会在启动时由 `internal/platform/db/module.go` 自动执行，产品主库是 SQLite。
- `internal/platform/db/module.go` 的 `sqliteMigrationsDir()` 返回 `internal/platform/db/sqlite/migrations`，因此顶层 `migrations/*` 不是当前产品 SQLite runtime migration 真源。
- `docs/契约/sqlc-convention.md` 明确根 `sqlc.yaml` 的 schema 必须指向 `internal/platform/db/sqlite/migrations/*.sql`，根查询目录为 `sql/queries/`，生成物为 `internal/store/sqlc`。
- `make sqlc-generate` 会重写 `internal/store/sqlc/models.go`、`internal/store/sqlc/querier.go`，并为新增 query 生成新的 `*.sql.go` 文件；这些生成物不能手写。

## 为什么需要扩大写入范围

ADR 0003 要求 schema gate 同时具备 migration、sqlc query、store wrapper、store tests，并且改 SQL/migration/sqlc 后运行 `make sqlc-verify`。在当前仓库结构下：

- 新表必须新增到 `internal/platform/db/sqlite/migrations/NNN_*.sql`，否则运行时不会迁移出 `mcp_tool_lifecycle_states`。
- 根 `sqlc.yaml` 必须把新增 SQLite migration 加入 schema 输入，否则 `sql/queries/mcp_tool_lifecycle.sql` 无法生成类型安全查询。
- `internal/store/sqlc/{models.go,querier.go,mcp_tool_lifecycle.sql.go}` 是 `make sqlc-generate` 的必要输出，不能手写也不能省略后再声称 `make sqlc-verify` 通过。

## 请求批准的额外写入范围

- `internal/platform/db/sqlite/migrations/*`
- `sqlc.yaml`
- `internal/store/sqlc/*` 仅限 sqlc 生成输出

原允许范围仍保持：

- `sql/queries/*mcp*`
- `internal/store/mcpserver/**`
- `internal/module/mcp_server/**`
- `internal/contract/**` 仅限 lifecycle reader/writer DTO/接口
- ADR/spike 文档状态更新

## 继续后的最小实施切片

1. 新增 SQLite migration `mcp_tool_lifecycle_states`，约束空 key、`active/suspended/removed` state、`discovery/user/migration/system` source。
2. 新增 `sql/queries/mcp_tool_lifecycle.sql` 并运行 `make sqlc-generate`，提交生成物。
3. 在 `internal/contract` 新增 lifecycle reader/writer DTO/接口，不修改 `internal/dto/mcp.MCPTool`。
4. 在 `internal/store/mcpserver` 新增 lifecycle store wrapper 和单元测试，所有空 workspace/server/tool key、非法 state/source、未知行都 fail-fast。
5. 在 `internal/module/mcp_server` 新增 owner API：显式写状态、列状态、对 discovery 结果 backfill active 行；backfill 不覆盖已有 `suspended/removed`。
6. 不修改 `internal/platform/toolbridge` 的 production list/call filtering，不把 lifecycle 状态投影到 Codex dynamicTools。

## 后续验证

批准扩大范围后，完成实现必须运行：

- `make sqlc-verify`
- LSP diagnostics 覆盖改过的 Go 文件
- `./scripts/test_with_guard.sh <每个改过的 Go 文件>`
- `./scripts/test_with_guard.sh ./internal/module/mcp_server ./internal/store/mcpserver -count=1`
- `git diff --check`
- `git status --short --branch`

## 未完成 ADR gates

- Schema gate：未完成，等待批准 runtime SQLite migration、sqlc.yaml 和 sqlc 生成物写入范围。
- Backfill gate：未完成，等待 owner API 和测试。
- Compatibility gate：未完成，待 API 落地后验证旧配置 API 不变。
- Toolbridge gate：未开始，按 ADR 禁止在第一切片接入。
- Direct-call gate：未开始，需在 Toolbridge gate 之后统一处理。
- Verification gate：未完成，当前仅完成阻塞确认和 docs-only plan。
