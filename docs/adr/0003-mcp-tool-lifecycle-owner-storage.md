# ADR 0003: MCP Tool Lifecycle Owner And Storage

日期：2026-06-29

状态：Accepted

## 背景

Reasonix 的 `mcp__server__tool` 命名方式能清楚表达工具来源。V3 已经有 `WrapMCPToolName` / `SplitMCPToolName`、server config 级 `enabled`、toolbridge 动态工具 surface、host-direct 工具和 MCP control plane，但还没有持久的 per-tool lifecycle 事实源。

这个 ADR 决定 per-tool lifecycle 的 owner、状态、存储和接入门槛；不表示生产 filtering 或 direct-call deny 已经落地。2026-06-29 主线程已明确批准进入实现，生产 enforcement 仍必须按本文的实施门槛分波落地。

## 当前事实

- `internal/module/mcp_server` 只管理 MCP server 配置、内置 SQLite/playwright server 启停，以及 HTTP `tools/list` 探测。
- `internal/store/mcpserver` 当前只持久化 `mcp_server_configs`，其中 `enabled` 是 server config 级开关，不是 per-tool lifecycle。
- `internal/contract/mcp_control.go` 目前只有 MCP server config、默认 server 启停、control-plane registry 相关 DTO 和接口，没有 per-tool lifecycle DTO。
- `internal/platform/toolbridge` 当前负责 host-direct、Codex surface、MCP peer list/call、namespace alias 和 schema 预校验；它不是 lifecycle 状态 owner。
- `internal/platform/mcpcontrol` 的 `active/stale/disconnected` 是已连接 peer 的活体状态，不能当作持久 tool lifecycle。

## 决策

1. 持久 lifecycle owner 是 `internal/module/mcp_server`。
2. 持久化 owner 是 `internal/store/mcpserver`，由该包封装 SQLite 读写；业务模块和 toolbridge 不直接操作表。
3. 跨模块 DTO 和窄接口放在 `internal/contract/mcp_control.go`，由 app Fx 图装配给 owner module 和 toolbridge。
4. `internal/platform/toolbridge` 只能消费 contract 层 lifecycle reader/policy port，用于 ListTools filtering 和 direct-call deny；它不得维护内存 lifecycle map，也不得成为事实源。
5. `cmd/mcp-*` 和 `internal/platform/mcpcontrol` 只提供工具执行面和活体 control plane，不拥有桌面主进程的 per-tool lifecycle 状态。

## 状态模型

持久状态固定为四个字符串：

| 状态 | 含义 | ListTools | Direct Call |
| --- | --- | --- | --- |
| `enabled` | 工具可见且可调用 | 展示 | 允许 |
| `disabled` | 用户关闭 server 或 tool | 不展示 | 拒绝，返回稳定 tool error |
| `suspended` | 策略或安全原因临时阻断 | 不展示 | 拒绝，说明策略原因 |
| `removed` | 工具已废弃、迁移或不可再用 | 不展示 | 拒绝，给迁移提示 |

这些状态不同于 control-plane peer 状态 `active/stale/disconnected`。同一 server 的 peer 可以是 active，但某个 tool 仍可能是 `disabled` 或 `suspended`。

## 标识与存储

per-tool lifecycle 的主键是：

```text
workspace_root + server_name + tool_name
```

- `server_name` 使用 canonical MCP server config map key，也就是 `mcp_server_configs` 的 `name`。manifest binary name 只能作为观测字段单独保存，不能参与主键。
- `tool_name` 使用 MCP peer 返回的原始工具名，不使用 `mcp__server__tool` 包装名。
- toolbridge 在查询 lifecycle 前负责把 `mcp__server__tool`、legacy alias 和 canonical 名解析回 `server_name + tool_name`。
- 如果工具调用无法解析到唯一 canonical server config key，owner policy 必须返回错误并阻断，不能用 manifest name、client kind 或 tool family 猜测。
- host-direct 工具不写入这张表；host-direct policy 继续由各自 owner 负责。

下一实现波次必须把 `mcp_tool_lifecycle` 作为正常产品存储落地：

1. 新增 `internal/platform/db/sqlite/migrations/109_mcp_tool_lifecycle.sql`。
2. 将该 migration 加入 `sqlc.yaml` schema 列表。
3. 新增 `sql/queries/mcp_tool_lifecycle.sql`。
4. 重新生成 `internal/store/sqlc`，再由 `internal/store/mcpserver` 封装查询。

`mcp_server_configs` 当前的 store-local DDL/repair 是遗留兼容例外；新表不得继续新增 `ensureTable` 本地 DDL 作为事实源。

持久表字段至少包含：

```text
mcp_tool_lifecycle(
  workspace_root TEXT NOT NULL,
  server_name TEXT NOT NULL,
  manifest_name TEXT NOT NULL DEFAULT '',
  tool_name TEXT NOT NULL,
  state TEXT NOT NULL CHECK (state IN ('enabled', 'disabled', 'suspended', 'removed')),
  reason TEXT NOT NULL DEFAULT '',
  replacement_tool TEXT NOT NULL DEFAULT '',
  last_seen_at INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (workspace_root, server_name, tool_name)
)
```

所有时间字段为 Unix epoch milliseconds，并使用 SQLite 兼容的 insert/update 表达式维护。测试必须锁定毫秒单位，避免秒/毫秒混用。

`mcp_server_configs.enabled` 继续保留 server config 级兼容语义。server disabled 时，owner policy 对该 server 下所有工具返回 `disabled`；per-tool row 不覆盖 server disabled。

## Backfill 与兼容

1. owner module 在所有可信 `tools/list` 发现工具后执行 backfill。
2. backfill 只为缺失 row 插入 `enabled`，并更新 `last_seen_at`。
3. backfill 不覆盖已有的 `disabled`、`suspended`、`removed` 或带 reason 的 row。
4. lifecycle filtering 未接入前，现有 provider config/list 行为保持不变：server-level `enabled=false` 仍过滤 provider 可启动配置。
5. enforcement 不得早于 backfill/migration readiness：必须先证明现有 server config 在没有 per-tool row 的情况下能完成发现、backfill、再进入过滤。
6. lifecycle filtering 接入后，policy reader 遇到缺失 row、未知 state、store 读取失败或 server/tool 身份无法解析时必须 fail-closed，不能静默展示或调用。

可信 backfill 入口包括：

- `internal/module/mcp_server.ListServerTools` 的 HTTP `tools/list` 探测。
- `internal/platform/toolbridge.PrepareCodexToolSurface` 的 stdio MCP surface 准备。
- `internal/platform/toolbridge.ListToolsForCodex` 的 peer tools/list 聚合。
- proxy `tools/list` 的 peer 工具列表路径。

这些入口只能调用 owner module 暴露的 backfill port；toolbridge 不直接写 store。

## Toolbridge 接入

ListTools filtering 和 direct-call deny 是两个独立消费点，必须分别实现和测试。

- `ListToolsForCodex`、`PrepareCodexToolSurface` 和 proxy `tools/list` 在发布 schema 前查询 lifecycle policy。
- `HandleToolCall` 的 Codex surface 路径在 `entry.client.CallTool` 前查询 lifecycle policy。
- 非 Codex surface 的 `routeToolCall` 路径在选择 peer 或调用 peer 前查询 lifecycle policy。
- `mcp__server__tool` 包装名和 canonical short name 必须解析到同一个 policy key；legacy LSP/orch alias 不再作为可调用入口。
- 被拒绝调用返回稳定 tool error envelope，至少包含 tool、server、state、reason 和 machine-readable code。

ListTools 隐藏不是安全边界；direct-call deny 必须独立存在。

## API 边界

contract 层只放 toolbridge 需要跨层消费的 DTO 与 policy reader。owner 写接口和 set/list/backfill command type 留在 `internal/module/mcp_server`，除非出现真实的跨模块写入消费者。

```go
type MCPToolLifecyclePolicyRequest struct {
    WorkspaceRoot       string
    WorkspaceRootSource string
    ServerName          string
    ManifestName        string
    ToolName            string
    CallName            string
}

type MCPToolLifecyclePolicyReader interface {
    ResolveMCPToolLifecycle(ctx context.Context, req MCPToolLifecyclePolicyRequest) (MCPToolLifecycleDecision, error)
}
```

`WorkspaceRoot` 必须是调用方已经解析好的 canonical root：优先取可信 CWD 的规范化根；CWD 缺失时才允许从 agent/thread binding 回查；多个候选 root 且没有 primary CWD 时必须报错。`WorkspaceRoots` 只能作为允许范围或诊断来源，不能替代 lifecycle 主键。

owner module 内部可以定义写接口，例如：

```go
type MCPToolLifecycleOwner interface {
    BackfillMCPServerTools(ctx context.Context, req BackfillMCPServerToolsRequest) error
    SetMCPToolLifecycle(ctx context.Context, req SetMCPToolLifecycleRequest) (contract.MCPToolLifecycleDecision, error)
    ListMCPToolLifecycle(ctx context.Context, req ListMCPToolLifecycleRequest) ([]contract.MCPToolLifecycleDecision, error)
}
```

`MCPToolLifecycleDecision` 必须显式携带 state、reason、server disabled 派生标记、manifest_name、updated_at 和 machine-readable deny code。未知 state 不得被解析成 `enabled`。

## 实施门槛

- owner/store/backfill 必须先于 toolbridge filtering/direct-call deny 合入。
- enforcement 合入前必须有旧配置无 per-tool row 的 backfill readiness 测试。
- enforcement 合入时必须同时覆盖 ListTools filtering 和 direct-call deny；只隐藏列表不满足安全边界。

## 非目标

- 不信任外部 MCP `readOnlyHint` 作为权限或 lifecycle 事实源。
- 不新增 process-global registry。
- 不让 `cmd/mcp-orch`、`cmd/mcp-lsp` 或 `cmd/mcp-ida` 反向成为桌面主进程的 lifecycle owner。
- 不把 frontend 本地状态作为 lifecycle 事实源。
- 不在 owner/storage 落地前向 toolbridge 添加临时 filtering map。

## 回滚

- lifecycle 表可保留为惰性数据；回滚实现代码后，旧的 server config `enabled` 行为仍按现状工作。
- 一旦 enforcement 代码接入，不允许用静默兼容模式绕过 store 错误。需要回滚时回滚 enforcement commit，不能把读取失败解释成 `enabled`。
- schema 回滚必须保留已写入状态的导出路径，避免用户关闭或移除的工具在降级后无记录可查。
- 实现提交必须提供 lifecycle state 导出或降级验证，证明回滚时不会丢失用户显式关闭、暂停或移除的状态。

## 可观测性

生命周期决策只记录 metadata：

- workspace_root hash 或规范化路径
- server_name
- tool_name
- state
- deny code
- reason category

日志和 trace 不记录工具参数、工具返回正文、prompt 正文或 secret。

## 验收测试

实现本 ADR 的提交必须覆盖以下测试面：

1. store：migration/sqlc/query、状态 enum、主键、毫秒时间戳、unknown state 读取失败、backfill 不覆盖非 `enabled` row。
2. module owner：server disabled 派生 tool disabled；缺 workspace/server/tool fail-fast；HTTP、stdio/Codex、`ListToolsForCodex`、proxy `tools/list` backfill。
3. contract：DTO JSON 形状、状态校验、canonical workspace root 请求、未知 state 不转 `enabled`。
4. toolbridge ListTools：`disabled/suspended/removed` 不进入 Codex dynamic tools 或 proxy `tools/list`。
5. toolbridge direct-call：裸名、`mcp__server__tool`、legacy alias、canonical short name 都被同一 lifecycle 决策拒绝。
6. compatibility：未接入 lifecycle enforcement 的路径保持当前 server-level `enabled` 行为；接入 enforcement 前，旧配置无 per-tool row 能先 backfill 再过滤。
7. rollback/export：已写入 lifecycle 状态有导出或降级验证。
8. error envelope：被拒绝工具返回稳定 machine-readable payload。

推荐验证命令：

```bash
./scripts/test_with_guard.sh ./internal/contract ./internal/module/mcp_server ./internal/store/mcpserver ./internal/platform/toolbridge -count=1
make guard
```

本 ADR 的实现涉及 SQLite migration 和 sqlc 查询时，必须追加：

```bash
make sqlc-verify
```

## 后果

- 实现必须先从 contract/store/module owner 和 backfill readiness 开始，再接入 toolbridge。
- 只改 toolbridge filtering 而没有 owner/storage/backfill 的实现不满足本 ADR。
- 未来 UI 若要控制 lifecycle，只能调用 guarded backend API，由 owner module 写入 store。
