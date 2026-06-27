# ADR 0003: MCP Tool Lifecycle State

日期：2026-06-28

状态：Proposed

## 背景

Reasonix 提供了 per-tool `active / suspended / removed` 的生命周期思路，但 super-agent-v3 当前事实源并不相同：

- MCP server 级开关已经存在：`contract.MCPServerConfig.Enabled` 是跨模块配置字段，`internal/store/mcpserver` 将 `mcp_server_configs.enabled` 持久化为 SQLite 整数。
- provider 可消费的 server 配置由 `internal/module/mcp_server.AsMCPServerConfigProvider` 适配，只返回 enabled server。
- MCP tool DTO 是协议描述：`internal/dto/mcp.MCPTool` 只有 `name / description / inputSchema / outputSchema`，不携带产品 lifecycle policy。
- `internal/mcpserver/common.ToolProvider` 只定义 `ListTools` 与 `CallTool`，`tools/list` 直接返回 provider 工具列表。
- Codex 动态工具面在 `internal/platform/toolbridge` 聚合 host、skill、MCP tools，并在 surface 中维护 canonical name 与 alias 映射。
- `internal/platform/toolbridge/mcp_namespace.go` 在 `main@9d7cda57` 已存在，提供 `WrapMCPToolName` / `SplitMCPToolName`；本 ADR 不重复创建 namespace helper。

因此 per-tool lifecycle 不能直接塞进 `toolbridge` 的 list/call filtering。先冻结 owner、schema、存储和迁移门槛，再允许生产路径读取状态。

## 决策

### Owner

per-tool lifecycle 的领域 owner 是 `internal/module/mcp_server`。

原因：

1. 该模块已经拥有 workspace MCP server config 的读写入口和 server enabled 语义。
2. 它能在 `ListServerTools` / 后续 discovery API 中拿到 `server_name + MCPTool.Name` 的原始事实。
3. lifecycle 是产品策略，不是 MCP 协议字段；不能写进 `internal/dto/mcp.MCPTool`。
4. `toolbridge` 只保留执行面职责：surface 准备、alias/canonical name、call routing、diff/event bridge。它后续只能通过 owner 提供的只读 contract 查询状态。

### State Enum

canonical 状态固定为三值：

| State | 含义 | 后续生效行为 |
| --- | --- | --- |
| `active` | 工具可展示、可调用 | 出现在动态工具列表，调用放行 |
| `suspended` | 临时暂停 | 隐藏或标记不可用，直接调用返回明确错误 |
| `removed` | 用户移除的 tombstone | 默认隐藏，直接调用返回明确错误；恢复必须显式写回 `active` |

缺失记录不是第四种状态。生产 filtering 生效前必须完成 discovery/backfill；生效后遇到 managed server/tool 缺少 lifecycle 行应 fail-fast，而不是静默当作 `active`。

### Storage

状态存储在产品 SQLite 中，由 `internal/store/mcpserver` 包装 sqlc query 暴露给 owner module。建议新增表：

```sql
CREATE TABLE mcp_tool_lifecycle_states (
  workspace_root TEXT NOT NULL,
  server_name TEXT NOT NULL,
  tool_name TEXT NOT NULL,
  lifecycle_state TEXT NOT NULL,
  reason TEXT NOT NULL DEFAULT '',
  source TEXT NOT NULL,
  updated_by TEXT NOT NULL DEFAULT '',
  created_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
  updated_at INTEGER NOT NULL DEFAULT (CAST(strftime('%s','now') AS INTEGER) * 1000),
  PRIMARY KEY (workspace_root, server_name, tool_name),
  CHECK (workspace_root <> ''),
  CHECK (server_name <> ''),
  CHECK (tool_name <> ''),
  CHECK (lifecycle_state IN ('active', 'suspended', 'removed')),
  CHECK (source IN ('discovery', 'user', 'migration', 'system'))
);
```

Key 使用 `workspace_root + server_name + tool_name`：

- `server_name` 是 MCP server config 名称，或 first-party MCP family 的保留名，如 `lsp` / `orch` / `ida`。
- `tool_name` 必须是 `MCPTool.Name` 的原始工具名，不是 Codex surface canonical name，也不是 `mcp__server__tool` namespace alias。
- alias、legacy name、`mcp__server__tool` 都是 toolbridge 的派生执行面，不作为持久化主键。

暂不在第一版加外键到 `mcp_server_configs`。当前 `mcp_server_configs` 由 `internal/store/mcpserver` 懒建/修复，强外键会把 per-tool schema 迁移绑到旧表形状；先由 owner module 做 server/tool 存在性校验。

### Contract Boundary

后续代码落地时新增的跨模块 contract 应只表达 lifecycle 查询与写入意图，例如：

- `MCPToolLifecycleState`
- `MCPToolLifecycleRecord`
- `MCPToolLifecycleReader`
- `MCPToolLifecycleWriter`

这些类型属于 `internal/contract` 或 `internal/module/mcp_server` 的 DTO 层，不属于 `internal/dto/mcp`。`internal/dto/mcp.MCPTool` 保持协议纯净。

## Migration Plan

1. 新增 SQLite migration、`sql/queries`、sqlc 生成代码和 store wrapper；运行 `make sqlc-verify`。
2. 新增 owner module API：写状态、列状态、对 discovery 结果执行显式 backfill。
3. backfill 规则：从已保存的 server config 和实际 `tools/list` 结果写入 `active` 行；已有 `suspended` / `removed` 不覆盖。
4. 生效前验证所有 managed server/tool 都有 lifecycle 行；发现缺行直接报错并阻断开启 filtering。
5. 只有完成 schema、backfill、API/UI compatibility、fail-fast 测试后，才能让 `toolbridge` 在 list/call 路径读取 lifecycle reader。

## Non-Goals Now

本 ADR 不允许当前阶段修改以下生产过滤路径：

- `internal/platform/toolbridge.ListToolsForCodex`
- `appendMCPToolsWithShadowWarning`
- `PrepareCodexToolSurface` / `addMCPToolsToSurface`
- `routeCodexSurfaceToolCall`
- `internal/mcpserver/common.handleToolsList`
- `mergeConfiguredMCPServers`
- provider manifest / prompt snapshot 组装

当前阶段也不新增 UI 开关、不改变 `MCPTool` DTO、不把 `suspended` / `removed` 直接投影到 Codex dynamicTools。

## Implementation Gates

后续实施必须同时满足：

1. **Schema gate**：migration、sqlc query、store wrapper、store tests 全部通过；非法 state/source、空 key、重复 key 都 fail-fast。
2. **Backfill gate**：对 HTTP / stdio / first-party MCP family 的工具发现路径有明确 backfill 或阻断策略，不能靠缺省 active。
3. **Compatibility gate**：旧 API 不传 lifecycle 字段时行为不变；新 API 返回 lifecycle 时必须保持旧客户端可忽略。
4. **Toolbridge gate**：只通过 owner reader 查询状态；查询失败、状态未知、managed tool 缺行都返回错误，不发布半可用工具面。
5. **Direct-call gate**：即使工具未出现在 list 中，直接调用 `suspended` / `removed` 工具也必须返回明确错误。
6. **Verification gate**：相关 Go 文件先跑单文件 guard；受影响包至少覆盖 `./internal/module/mcp_server`、`./internal/store/mcpserver`、`./internal/platform/toolbridge`，改 SQL 时追加 `make sqlc-verify`。

## Consequences

- lifecycle 决策与 server config owner 对齐，避免 `toolbridge` 成为隐藏策略仓库。
- namespace/canonical helper 继续只处理调用名兼容，不承载状态。
- 在 backfill 前，per-tool lifecycle 不影响现有 filtering；这保持当前生产路径稳定。
- 后续若需要 UI 展示，可以从 owner module 暴露 `server_name + tool_name + lifecycle_state`，前端再按 toolbridge 派生名显示别名。
