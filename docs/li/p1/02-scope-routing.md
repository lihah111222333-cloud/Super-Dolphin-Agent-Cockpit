# P1-02：LSP scope 与 peer routing

## 目标

建立可信的 LSP 调用上下文，把 `agentID/threadID/callID/cwd` 从 provider session 传递到 mcp-lsp，并让 toolbridge / mcpcontrol / mcp-lsp 都按 scope 路由，而不是只按 `clientKind=lsp` 或全局 singleton manager 路由。

## 当前证据

- `internal/provider/codexapp/session_enrich.go:10-22`：Codex tool call params 中注入 `agentId` 与 `_cwd`。
- `internal/provider/codexapp/session.go:207-227`：tool call 进入 tool handler 前调用 enrichment。
- `internal/platform/toolbridge/types.go`：`ToolCallRequest` 已包含 `AgentID/ThreadID/TurnID/CallID/CWD/ClientKind`。
- `internal/platform/toolbridge/handler.go:131-140`：当前 `selectActiveToolPeer` 只按 `clientKind` 找 active peer，多 peer ambiguous。
- `internal/platform/mcpcontrol/resolution.go:79-93`：`FindActiveByKind` 只按 kind 返回实例。
- `internal/dto/mcp/protocol.go:11-26` 与 `internal/platform/mcpcontrol/registry.go:26-46`：当前注册模型只有 `AgentID/ThreadID`，没有 session 维度。
- `cmd/mcp-lsp/fx.go:62-75`：mcp-lsp `OnToolsCall` 目前只解析 `_cwd`。
- `internal/mcpserver/common/server.go:79-83`：common stdio `toolCallParams` 也只包含 `_cwd`。

## 设计

### Scope 数据结构

在 common/toolbridge 层新增可信 scope：

```go
type ToolScope struct {
    AgentID  string
    ThreadID string
    TurnID   string
    CallID   string
    CWD      string
    Family   string // lsp/orch/ida
}
```

mcp-lsp 内部扩展为：

```go
type LSPToolScope struct {
    ToolScope
    LanguageID            string
    TargetPath            string
    TargetURI             string
    WorkspaceRoot         string
    LanguageWorkspaceRoot string
    ProjectRoot           string
    RootKind              string
    LanguageSpecific      map[string]string
    ScopeKey              string
    ShardKey              string
    WorkspaceKey          string
    ManagerKey            string
}
```

### Context key

在 `internal/mcpserver/common` 中增加专用 context key：

```go
const ToolScopeContextKey = contextKey("mcp_tool_scope")
```

保留 `CwdContextKey` 作为兼容 alias，但新代码优先读 `ToolScopeContextKey`。

### tools/call 参数

stdio/control-plane `tools/call` 顶层 params 扩展：

```json
{
  "name": "file",
  "arguments": {"action":"read_file","file_path":"go.mod"},
  "agentId": "agent-...",
  "threadId": "...",
  "turnId": "...",
  "callId": "...",
  "_cwd": "/repo/worktree"
}
```

规则：

- 只信顶层 trusted 字段，不信 `arguments.agent_id`。
- 兼容当前 `agentId` 命名；可接受 `_agentId` / `_threadId` 作为内部别名，但输出统一。
- 本 P1 不新增 session 维度。当前 mcpcontrol 注册模型没有 `SessionID`，peer routing 与 manager key 都只使用 `agentID/threadID` 作为身份稳定部分。
- `_cwd` 必须归一化绝对路径；空值则 fallback 到 session/manager root。

## Peer routing

### 选择规则

新增 `FindActiveForScope(scope ToolScope)`，替代 LSP 路径上的 `FindActiveByKind`：

1. exact：`clientKind + agentID + threadID`
2. relaxed：`clientKind + agentID`
3. singleton fallback：只有一个 active peer 时可用
4. ambiguous：多个 active peer 且无 scope 命中时报错

`PoolKey/ShardKey/WorkspaceKey/ManagerKey` 不参与 toolbridge/mcpcontrol 的 peer 选择。它们是 mcp-lsp 内部在 peer 命中后，根据 trusted `ToolScope` 与目标文件/root resolver 派生出的 LSP manager/cache key。控制面不得提前依赖尚不存在的 pool/shard 字段。

### HTTP 保留后的影响

P1 保留现有 HTTP MCP proxy/discovery/manifest 兼容路径，但 LSP scoped routing 的主线不依赖 HTTP endpoint。toolbridge/mcpcontrol 的 scoped lookup 只服务控制面 / stdio peer 工具调用链；HTTP MCP 如需同等 scope 语义，后续单独设计。

## 实现步骤

1. 在 `internal/mcpserver/common` 增加 `ToolScope` 与 context helpers。
2. 不修改 `internal/dto/mcp/protocol.go` / `ToolInstance` 的注册身份维度；本轮不引入 `SessionID`。
3. 修改 `internal/platform/toolbridge/handler.go`：构造 peer call params 时带 trusted scope。
4. 修改 `internal/platform/mcpcontrol/resolution.go`：新增 scoped lookup，不破坏 legacy lookup；lookup 只使用 `clientKind/agentID/threadID`。
5. 修改 `cmd/mcp-lsp/fx.go` 与 `cmd/mcp-orch/fx.go`：从 `tools/call` params 解析 scope 并写入 context。
6. 修改 `cmd/mcp-lsp/tools/factory.go`：所有工具通过 helper 获取 workspace root/scope。
7. 增加日志字段：`agent_id/thread_id/call_id/cwd/workspace_key/shard_key`。

## 测试

- `toolbridge`：两个 LSP peer active，不带 scope 应报 ambiguous。
- `toolbridge`：两个 LSP peer active，带 agent/thread scope 命中对应 peer。
- `toolbridge`：peer routing 不读取 `PoolKey/ShardKey`；这些字段只在 mcp-lsp 内部出现。
- `mcp-lsp`：`OnToolsCall` 收到顶层 scope 后，handler context 可读取。
- `mcp-lsp`：模型在 arguments 伪造 cwd/agent_id 不会覆盖 trusted scope。
- `mcp-orch`：orchestration tools 继续可读 `_cwd`。
- HTTP MCP 兼容测试继续保留；P1 scope routing 测试不得要求删除 HTTP proxy/discovery。

## 完成定义

- LSP tool call 不再只依赖 global `clientKind=lsp`。
- 所有 LSP 操作都能记录并使用 trusted scope。
- Scope routing 覆盖 Codex/Claude 多 agent 控制面 / stdio peer 场景。
