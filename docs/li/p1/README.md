# P1：多 Agent LSP Manager 实现总览

## 目标

在 `super-agent-v3` 中实现可被多个 Codex/Claude agent 并发使用的 LSP manager，同时保持每个 agent / thread / workspace 的 LSP 状态隔离，避免 diagnostics、bootstrap cache、workspace root、language-server client 在多 agent 场景下串线。

本 P1 方案以 **v3 当前 MCP 控制面 / stdio peer 工具调用链** 作为多 agent LSP 隔离的主路径。现有 HTTP MCP transport/proxy/discovery/manifest 兼容路径 **保留**，不在 P1 中弃用或删除；P1 也不把 HTTP MCP 改造成新的共享 LSP 隔离方案。

## 非目标

- 不删除、不弃用现有 HTTP MCP runner、proxy、discovery、manifest HTTP 分支。
- 不把 HTTP MCP endpoint 作为本轮 multi-agent LSP 隔离的新增承载协议。
- 不在 P1 中重写普通 WebSocket app-server、Wails UI、本地 metrics HTTP server 等非 MCP HTTP 能力。
- 不直接照搬 `go-agent-v2` 的 hidden `__tool_call_meta` 机制。
- 不把 LSP workspace key 归并到 common git root；LSP 必须以物理 workspace/worktree/root 为准。

## 背景结论

只读审查 agent 汇总结论：

- `super-agent-v3` 已有 mcp-lsp tool surface、per-call `_cwd` 注入、`cmd/mcp-lsp/multilsp` manager、diagnostics generation、bootstrap/cache、runner-owned recycler 基础。
- v3 当前 `ManagerPool` 还不是真实 shard pool：`cmd/mcp-lsp/multilsp/pool.go:106-111` 只返回 primary manager。
- v3 当前 diagnostics/cache 主要按 workspace/URI 组织，没有 agent/thread/scope 维度。
- v3 当前 Go root resolver 只向上找 `go.mod`，没有完整 `go.work` / 子模块识别。
- `go-agent-v2` 的可借鉴点是 `agentID -> shard`、`rootURI -> manager clone`、diagnostics stale refresh、Go root heuristic；不可直接照搬其 constructor goroutine、hidden meta、agentID-only key。

## 子计划

| 文档 | 主题 | 输出 |
| --- | --- | --- |
| [02-scope-routing.md](02-scope-routing.md) | Tool scope 与 peer routing | scope 结构、路由规则、fallback |
| [03-lsp-manager-pool.md](03-lsp-manager-pool.md) | LSP shard/pool/lease | 真实 ManagerPool、workspace clone、recycler |
| [04-diagnostics-bootstrap-cache.md](04-diagnostics-bootstrap-cache.md) | diagnostics stale/cache/bootstrap 生命周期 | 状态机、cache key、失效规则 |
| [05-go-workspace-root.md](05-go-workspace-root.md) | Go workspace root / go.work / 子模块 | root resolver、workspaceFolders、GOWORK 策略 |
| [06-rollout-verification.md](06-rollout-verification.md) | 验证与灰度 | feature flag、测试矩阵、验收命令 |

## 全局设计原则

### 1. MCP 协议边界

P1 的多 agent LSP 隔离只改造当前控制面 / stdio peer 工具调用链：

```text
provider session
  -> toolbridge ToolCallRequest(agentID/threadID/callID/cwd)
  -> mcpcontrol active peer
  -> bootstrap OnToolsCall / common.Server tools/call
  -> cmd/mcp-lsp tool handler
```

HTTP MCP 保留为既有兼容路径：

- 不删除 `cmd/mcp-lsp/http_runner.go` / `cmd/mcp-orch/http_runner.go`。
- 不删除 `internal/mcpserver/common/http_transport.go`。
- 不删除 HTTP discovery / proxy / manifest HTTP 分支。
- P1 的验收不要求 HTTP manifest 变为 stdio-only。
- 若未来要让 HTTP MCP 也承载同等 scope 隔离，必须另立计划；不得混入本 P1 的 manager/cache/root 改造。

### 2. Scope 是事实来源

新增统一 LSP scope，所有 LSP manager、cache、diagnostics、bootstrap 状态都从服务端可信 scope 派生：

```go
type LSPToolScope struct {
    AgentID       string
    ThreadID      string
    TurnID        string
    CallID        string
    CWD           string
    Family        string
    LanguageID    string
    TargetPath    string
    TargetURI     string
    WorkspaceRoot string
    RootKind      string // go_work, go_mod, single_submodule, multi_module, dir_fallback
    LanguageWorkspaceRoot string
    ProjectRoot           string
    LanguageSpecific      map[string]string // Go: goWorkPath/moduleRoot/moduleRootsHash; TS: tsconfig/package root; etc.
}
```

派生出的 `ScopeKey/WorkspaceKey/ShardKey/ManagerKey` 不属于 `LSPToolScope` 输入；它们只出现在 `03-lsp-manager-pool.md` 定义的 canonical `ResolvedLSPToolScope` 中，并由 `ManagerPool.ForScope` 统一返回给 diagnostics/cache/bootstrap 复用。

最小 scope 来源：

- `internal/provider/codexapp/session_enrich.go` 已把 `agentId` 与 `_cwd` 注入工具调用参数。
- `internal/platform/toolbridge/types.go` 的 `ToolCallRequest` 已有 `AgentID/ThreadID/TurnID/CallID/CWD/ClientKind`。
- `cmd/mcp-lsp/fx.go` 与 `internal/mcpserver/common/server.go` 目前只消费 `_cwd`；P1 需要扩展为可信 scope 上下文。

### 3. 隔离层次

P1 统一采用 **trusted scope 自动派生**，不再暴露 agent/pool/shared-root 运行时模式。隔离粒度：

```text
client kind = lsp
agent/thread scope
  -> shard/pool scope
  -> workspace/root scope
  -> language manager
  -> LSP client process
```

派生规则：

- `ScopeKey = family/clientKind + agentID/threadID`，其中 `family/clientKind` 只是命名空间，稳定身份部分只来自服务端可信 `agentID/threadID`。`turnID/callID` 只用于日志和请求追踪，不进入 manager/cache key，避免每次 tool call 创建新 manager。
- `WorkspaceKey` 来自 `language/rootKind/workspaceRoot/languageWorkspaceRoot/projectRoot/languageSpecific`。
- `ManagerKey = ScopeKey + WorkspaceKey`；同 shard hash collision 允许存在，但同 shard 内不同 manager key 不能共享 clone。
- 缺少 trusted identity 时才退化为 workspace-only key；这是兼容退化，不是用户可选 shared mode。

### 4. Diagnostics 不跨 scope 返回

`diagnostics(all)` 只能返回当前 scope 的 diagnostics。不能从全局 manager 返回所有 URI。旧 generation、旧 fingerprint、旧 workspace、旧 shard 的 diagnostics 必须被过滤或清理。

### 5. Go root 不使用 common git root

prompt/git context 可以归并 worktree 到 common root，但 LSP 不可以。Go 语言的 `gopls` client 需要真实物理路径与 `go.work`/`go.mod` 拓扑，否则会出现 module not included / wrong package metadata。

## 总体落地顺序

1. 引入可信 `LSPToolScope`，统一 toolbridge -> mcp-lsp 的 scope 传递。
2. 实现真实 `ManagerPool.ForScope` 与 workspace clone。
3. diagnostics/bootstrap/cache scoped 化，加入 stale refresh。
4. 实现 Go root resolver，支持 `go.work` 与子模块。
5. 灰度开关与回归测试，最后默认启用。

## 验收门槛

- 新增文档和代码实现必须能解释并验证：两个不同 agent 同时访问同一物理 repo 不串 diagnostics/cache。
- `diagnostics(all)` 不返回其他 agent/thread/workspace 的诊断。
- `go.work` / 单子模块 / 多子模块 / linked worktree 都有 unit test 或 integration test。
- 现有 HTTP MCP 兼容路径继续通过原有测试；P1 不要求 HTTP MCP 删除或 stdio-only manifest。
